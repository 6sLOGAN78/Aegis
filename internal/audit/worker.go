package audit

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// WorkerConfig defines parameters for WAL ingestion and batch flushing.
type WorkerConfig struct {
	SpoolDir      string
	BatchSize     int           // default 500
	FlushInterval time.Duration // default 200ms
	PruneArchived bool
}

// BatchPool abstracts batch database execution, satisfied by *pgxpool.Pool and pgxmock.PgxPoolIface.
type BatchPool interface {
	SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults
	Close()
}

// AuditWorker reads local WAL segments, batches events, and flushes them to PostgreSQL.
type AuditWorker struct {
	pool   BatchPool
	cursor *CursorTracker
	cfg    WorkerConfig
	mu     sync.Mutex

	skipped atomic.Int64
	// skipHook is told about every newly skipped frame (metric export, no event data).
	skipHook atomic.Pointer[func()]
	// skipSeg/skipOff are the furthest (segment, offset) whose skip was already counted.
	// A retry that re-reads the same frame before the cursor moved does not count it
	// again. Guarded by mu (processFile runs under it).
	skipSeg string
	skipOff int64
}

// NewAuditWorker instantiates a new background WAL ingestion worker.
func NewAuditWorker(pool BatchPool, cfg WorkerConfig) (*AuditWorker, error) {
	if cfg.SpoolDir == "" {
		return nil, errors.New("spool directory cannot be empty")
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 500
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = 200 * time.Millisecond
	}

	cursor := NewCursorTracker(cfg.SpoolDir)

	return &AuditWorker{
		pool:   pool,
		cursor: cursor,
		cfg:    cfg,
	}, nil
}

// Cursor returns the worker's persistent cursor checkpoint tracker.
func (w *AuditWorker) Cursor() *CursorTracker {
	return w.cursor
}

// ProcessBatch sends a batch of completion events to PostgreSQL using pgx.Batch,
// applying ON CONFLICT DO NOTHING deduplication, and advances the cursor only after commit.
func (w *AuditWorker) ProcessBatch(ctx context.Context, events []CompletionEvent, segmentFile string, newOffset int64) error {
	if len(events) == 0 {
		return nil
	}

	batch := &pgx.Batch{}
	query := `
		INSERT INTO audit_events (
			event_id, event_type, request_id, timestamp, event_date,
			principal_id, principal_kind, roles, service_id, route_id,
			http_method, request_path, decision, reason_code, snapshot_version,
			http_status, duration_ms, client_ip, error_code, suppressed_count, jti
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
			$11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21
		) ON CONFLICT (event_date, event_id) DO NOTHING;
	`

	for _, e := range events {
		// Hostile or legacy values must never make Postgres reject the whole batch (D-18).
		e.Normalize()
		// uuid.Parse accepts forms Postgres' uuid type rejects (urn:uuid:...), so the
		// canonical text is what gets sent, never the original.
		eventID, ok := canonicalUUID(e.EventID)
		if !ok {
			eventID = uuid.NewString()
		}
		reqID, ok := canonicalUUID(e.RequestID)
		if !ok {
			reqID = uuid.NewString()
		}

		ts := e.Timestamp
		if ts.IsZero() {
			ts = time.Now().UTC()
		} else {
			ts = ts.UTC()
		}

		eventDate := ts.Format("2006-01-02")
		// Explicit type wins (D-03); untyped or unknown records fall back to the legacy guess.
		eventType := e.EventType
		if !ValidEventType(eventType) {
			eventType = EventTypeDecision
			if e.HTTPStatus > 0 {
				eventType = EventTypeCompletion
			}
		}

		var suppressed any
		if e.SuppressedCount > 0 {
			suppressed = e.SuppressedCount
		}

		var jti any
		if e.JTI != "" {
			jti = e.JTI
		}

		rolesJSON := "[]"
		if len(e.PrincipalRoles) > 0 {
			if b, err := json.Marshal(e.PrincipalRoles); err == nil {
				rolesJSON = string(b)
			}
		}

		batch.Queue(query,
			eventID,
			eventType,
			reqID,
			ts,
			eventDate,
			e.PrincipalID,
			e.PrincipalKind,
			rolesJSON,
			e.ServiceID,
			e.RouteID,
			e.HTTPMethod,
			e.CanonicalPath,
			e.Decision,
			e.ReasonCode,
			e.SnapshotVersion,
			e.HTTPStatus,
			e.DurationMS,
			e.ClientIP,
			e.ErrorCode,
			suppressed,
			jti,
		)
	}

	br := w.pool.SendBatch(ctx, batch)
	defer br.Close()

	for i := 0; i < len(events); i++ {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("batch execute failed at event %d: %w", i, err)
		}
	}

	if err := br.Close(); err != nil {
		return fmt.Errorf("failed to close batch results: %w", err)
	}

	// Advance persistent checkpoint only after successful DB commit (AUD-03, at-least-once)
	if err := w.cursor.SaveOffset(segmentFile, newOffset); err != nil {
		return fmt.Errorf("failed to checkpoint cursor offset: %w", err)
	}

	return nil
}

// ProcessAvailable scans the spool directory, ingests all available records
// up to the latest segment EOF, commits batches to PostgreSQL, and advances the cursor.
// Returns the total number of processed records.
func (w *AuditWorker) ProcessAvailable(ctx context.Context) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	cursorState, err := w.cursor.LoadOffset()
	if err != nil {
		return 0, fmt.Errorf("failed to load cursor offset: %w", err)
	}

	// Same ordering as the spool (sequence number first, never the raw wall clock), so
	// the last entry is always the segment the spool is writing to.
	walFiles, err := listSegments(w.cfg.SpoolDir)
	if err != nil {
		return 0, fmt.Errorf("failed to list spool directory: %w", err)
	}

	if len(walFiles) == 0 {
		return 0, nil
	}

	// Locate start file and offset
	startIdx := 0
	startOffset := int64(0)
	if cursorState.SegmentFile != "" {
		found := false
		for i, f := range walFiles {
			if f == cursorState.SegmentFile {
				startIdx = i
				startOffset = cursorState.Offset
				found = true
				break
			}
		}
		if !found {
			// If the cursor's segment was pruned, resume at the first segment ordered after it.
			for i, f := range walFiles {
				if compareSegmentNames(f, cursorState.SegmentFile) > 0 {
					startIdx = i
					startOffset = 0
					found = true
					break
				}
			}
		}
	}

	totalProcessed := 0

	for i := startIdx; i < len(walFiles); i++ {
		fileName := walFiles[i]
		filePath := filepath.Join(w.cfg.SpoolDir, fileName)
		isLatest := (i == len(walFiles)-1)

		offset := int64(0)
		if i == startIdx {
			offset = startOffset
		}

		processedInFile, nextOffset, reachedEOF, err := w.processFile(ctx, fileName, filePath, offset, isLatest)
		totalProcessed += processedInFile
		if err != nil {
			return totalProcessed, err
		}

		// A historical segment is advanced past and pruned ONLY on an explicit EOF with no
		// error; anything else keeps the cursor and the file untouched (AUD-03).
		if !isLatest && i+1 < len(walFiles) {
			if !reachedEOF {
				return totalProcessed, fmt.Errorf("segment %s not fully read, cursor held", fileName)
			}
			nextFile := walFiles[i+1]
			// Defensive: only ever advance forward, and never past the highest-ordered
			// segment (the one the spool may be appending to), which is never pruned.
			if compareSegmentNames(fileName, nextFile) >= 0 {
				return totalProcessed, fmt.Errorf("segment order violated: %s does not precede %s, cursor held", fileName, nextFile)
			}
			if err := w.cursor.SaveOffset(nextFile, 0); err != nil {
				return totalProcessed, fmt.Errorf("failed to advance cursor to next segment: %w", err)
			}
			if w.cfg.PruneArchived {
				_ = os.Remove(filePath)
			}
		} else if isLatest && nextOffset > offset {
			// Update cursor within latest segment
			if err := w.cursor.SaveOffset(fileName, nextOffset); err != nil {
				return totalProcessed, err
			}
		}
	}

	return totalProcessed, nil
}

// readFramedRecord is the frame reader; a package var so tests can inject read errors
// hermetically.
var readFramedRecord = ReadFramedRecord

// SkippedFrames returns the number of CRC-valid frames skipped for undecodable JSON.
// Each frame is counted once, even if a later failure makes the worker re-read it.
func (w *AuditWorker) SkippedFrames() int64 { return w.skipped.Load() }

// SetSkippedFrameHook registers fn to be called once per newly skipped frame (for
// example telemetry.Metrics.RecordAuditWorkerSkippedFrame). Pass nil to clear. fn runs
// with the worker lock held and must not call back into the worker.
func (w *AuditWorker) SetSkippedFrameHook(fn func()) {
	if fn == nil {
		w.skipHook.Store(nil)
		return
	}
	w.skipHook.Store(&fn)
}

// countSkip records a skipped frame at (fileName, offset) unless that position was
// already counted. Segments are ordered by compareSegmentNames and offsets grow within
// a segment, so the high-water mark dedupes retries. Caller holds w.mu.
func (w *AuditWorker) countSkip(fileName string, offset int64) {
	if c := compareSegmentNames(fileName, w.skipSeg); w.skipSeg != "" && (c < 0 || (c == 0 && offset <= w.skipOff)) {
		return
	}
	w.skipSeg, w.skipOff = fileName, offset
	w.skipped.Add(1)
	if h := w.skipHook.Load(); h != nil {
		(*h)()
	}
}

// processFile ingests one segment from startOffset. reachedEOF is true only when the
// segment was read to a definite end. A torn or corrupt frame is resynced from its
// start + 1; it counts as a torn tail only when no decodable frame follows it. A torn
// tail on the newest segment returns reachedEOF=false with a nil error and the offset at
// the torn frame's start so the next pass retries it.
func (w *AuditWorker) processFile(ctx context.Context, fileName, filePath string, startOffset int64, isLatest bool) (int, int64, bool, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return 0, startOffset, false, fmt.Errorf("failed to open segment %s: %w", filePath, err)
	}
	defer f.Close()

	// Read against a stable size snapshot: bytes the writer appends during this pass are
	// invisible until the next pass, so a frame still being written is consistently
	// seen as incomplete rather than half-read.
	fi, err := f.Stat()
	if err != nil {
		return 0, startOffset, false, fmt.Errorf("failed to stat segment %s: %w", filePath, err)
	}
	src := io.NewSectionReader(f, 0, fi.Size())

	if startOffset > 0 {
		if _, err := src.Seek(startOffset, io.SeekStart); err != nil {
			return 0, startOffset, false, fmt.Errorf("failed to seek segment %s to %d: %w", filePath, startOffset, err)
		}
	}

	var batch []CompletionEvent
	currentOffset := startOffset
	processedCount := 0
	reachedEOF := false
	var readErr error
	// badStart is the start of the first undecodable frame since the last frame that
	// decoded; -1 when the reader is in sync. A frame header is not CRC-protected, so a
	// torn or corrupt frame is only a tail when nothing decodable follows it.
	badStart := int64(-1)

loop:
	for {
		if ctx.Err() != nil {
			if badStart >= 0 {
				currentOffset = badStart
			}
			return processedCount, currentOffset, false, ctx.Err()
		}

		evt, bytesRead, err := readFramedRecord(src)
		if err != nil {
			switch {
			case errors.Is(err, io.EOF):
				reachedEOF = true
				break loop
			case errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, ErrCorruptedRecord):
				// Torn (length runs past the snapshot) or corrupt frame. Never trust its
				// length: rescan from just after this frame's start so frames inside the
				// bogus length window are not skipped.
				//
				// The rescan is provisional. The cursor only moves past badStart once a
				// CRC-valid frame lying wholly inside the snapshot decodes after it.
				// Appends are sequential, so such a frame proves the writer has finished
				// with (or abandoned) the bad frame. Without one, every exit path below
				// resets to badStart, so on the active segment a frame that is still
				// being written is retried from its start and never skipped.
				if badStart < 0 {
					badStart = currentOffset
				}
				if _, serr := src.Seek(currentOffset+1, io.SeekStart); serr != nil {
					readErr = fmt.Errorf("resync seek in %s failed: %w", fileName, serr)
					break loop
				}
				resyncPos, scanErr := scanToNextMagic(src)
				if scanErr != nil {
					if errors.Is(scanErr, io.EOF) {
						// Nothing decodable follows: a genuine torn tail. The newest segment
						// may still be growing, so it is retried from the first bad frame;
						// a historical segment ends here.
						currentOffset = badStart
						if isLatest {
							log.Printf("AuditWorker: torn tail in %s at offset %d, will retry", fileName, badStart)
						} else {
							log.Printf("Warning: torn tail in historical segment %s at offset %d, treating as end of segment", fileName, badStart)
							reachedEOF = true
						}
						break loop
					}
					readErr = fmt.Errorf("resync scan of %s failed: %w", fileName, scanErr)
					break loop
				}
				log.Printf("Warning: corrupted WAL record in %s at offset %d, resynced at %d", fileName, currentOffset, resyncPos)
				currentOffset = resyncPos
				continue
			case errors.Is(err, ErrInvalidRecordJSON):
				// CRC-valid frame with undecodable JSON: skip and count, never wedge.
				badStart = -1
				w.countSkip(fileName, currentOffset)
				log.Printf("Warning: undecodable JSON in WAL frame of %s at offset %d, skipped", fileName, currentOffset)
				currentOffset += bytesRead
				continue
			default:
				readErr = fmt.Errorf("read error in segment %s at offset %d: %w", fileName, currentOffset, err)
				break loop
			}
		}

		badStart = -1
		batch = append(batch, *evt)
		currentOffset += bytesRead
		processedCount++

		if len(batch) >= w.cfg.BatchSize {
			if err := w.ProcessBatch(ctx, batch, fileName, currentOffset); err != nil {
				return processedCount, currentOffset - bytesRead, false, err
			}
			batch = batch[:0]
		}
	}

	// A provisional resync that never reached a decodable frame must not move the
	// cursor: fall back to the first bad frame's start.
	if badStart >= 0 {
		currentOffset = badStart
	}

	// Flush frames read before the stop condition; the cursor only covers them.
	if len(batch) > 0 {
		if err := w.ProcessBatch(ctx, batch, fileName, currentOffset); err != nil {
			return processedCount, currentOffset, false, err
		}
	}
	if readErr != nil {
		return processedCount, currentOffset, false, readErr
	}

	return processedCount, currentOffset, reachedEOF, nil
}

func scanToNextMagic(f io.ReadSeeker) (int64, error) {
	buf := make([]byte, 1)
	headerBuf := make([]byte, 4)
	for {
		n, err := f.Read(buf)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return 0, io.EOF
			}
			return 0, err
		}
		if n == 0 {
			return 0, io.EOF
		}
		if buf[0] == 0xAE { // First byte of MagicHeader 0xAE615001
			cur, _ := f.Seek(0, io.SeekCurrent)
			m, _ := io.ReadFull(f, headerBuf[1:4])
			headerBuf[0] = 0xAE
			if m == 3 && binary.BigEndian.Uint32(headerBuf) == MagicHeader {
				// Found next magic header! Rewind to start of frame
				magicPos := cur - 1
				_, _ = f.Seek(magicPos, io.SeekStart)
				return magicPos, nil
			}
			_, _ = f.Seek(cur, io.SeekStart)
		}
	}
}

// Start begins the tailing worker loop, flushing batches periodically or when batch size is reached.
// Survives database outages by holding cursor position and retrying upon reconnection.
func (w *AuditWorker) Start(ctx context.Context) error {
	ticker := time.NewTicker(w.cfg.FlushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Final drain before shutdown
			drainCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_, _ = w.ProcessAvailable(drainCtx)
			cancel()
			return ctx.Err()
		case <-ticker.C:
			if _, err := w.ProcessAvailable(ctx); err != nil {
				if !errors.Is(err, context.Canceled) {
					log.Printf("AuditWorker batch flush warning: %v", err)
				}
			}
		}
	}
}
