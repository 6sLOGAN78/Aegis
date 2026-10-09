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
	"sort"
	"strings"
	"sync"
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
			http_status, duration_ms, client_ip, error_code, suppressed_count
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
			$11, $12, $13, $14, $15, $16, $17, $18, $19, $20
		) ON CONFLICT (event_date, event_id) DO NOTHING;
	`

	for _, e := range events {
		// Hostile or legacy values must never make Postgres reject the whole batch (D-18).
		e.Normalize()
		eventID := e.EventID
		if _, perr := uuid.Parse(eventID); perr != nil {
			eventID = uuid.NewString()
		}
		reqID := e.RequestID
		if _, perr := uuid.Parse(reqID); perr != nil {
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

	entries, err := os.ReadDir(w.cfg.SpoolDir)
	if err != nil {
		return 0, fmt.Errorf("failed to list spool directory: %w", err)
	}

	var walFiles []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "wal-") && strings.HasSuffix(e.Name(), ".log") {
			walFiles = append(walFiles, e.Name())
		}
	}
	sort.Strings(walFiles)

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
			// If cursor file was pruned, find first file lexicographically greater
			for i, f := range walFiles {
				if f > cursorState.SegmentFile {
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

		processedInFile, nextOffset, err := w.processFile(ctx, fileName, filePath, offset)
		totalProcessed += processedInFile
		if err != nil {
			return totalProcessed, err
		}

		// If historical segment completed and next segment exists, advance cursor to next segment
		if !isLatest && i+1 < len(walFiles) {
			nextFile := walFiles[i+1]
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

func (w *AuditWorker) processFile(ctx context.Context, fileName, filePath string, startOffset int64) (int, int64, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return 0, startOffset, fmt.Errorf("failed to open segment %s: %w", filePath, err)
	}
	defer f.Close()

	if startOffset > 0 {
		if _, err := f.Seek(startOffset, io.SeekStart); err != nil {
			return 0, startOffset, fmt.Errorf("failed to seek segment %s to %d: %w", filePath, startOffset, err)
		}
	}

	var batch []CompletionEvent
	currentOffset := startOffset
	processedCount := 0

	for {
		if ctx.Err() != nil {
			return processedCount, currentOffset, ctx.Err()
		}

		evt, bytesRead, err := ReadFramedRecord(f)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			if errors.Is(err, ErrCorruptedRecord) {
				// Corrupted record detected: scan forward for next MagicHeader to resynchronize
				log.Printf("Warning: corrupted WAL record in %s around offset %d, seeking resync", fileName, currentOffset)
				resyncPos, scanErr := scanToNextMagic(f)
				if scanErr != nil {
					// Cannot resynchronize further in this file
					break
				}
				currentOffset = resyncPos
				continue
			}
			// Unexpected I/O error
			break
		}

		batch = append(batch, *evt)
		currentOffset += bytesRead
		processedCount++

		if len(batch) >= w.cfg.BatchSize {
			if err := w.ProcessBatch(ctx, batch, fileName, currentOffset); err != nil {
				return processedCount, currentOffset - bytesRead, err
			}
			batch = batch[:0]
		}
	}

	// Flush remaining records in batch
	if len(batch) > 0 {
		if err := w.ProcessBatch(ctx, batch, fileName, currentOffset); err != nil {
			return processedCount, currentOffset, err
		}
	}

	return processedCount, currentOffset, nil
}

func scanToNextMagic(f *os.File) (int64, error) {
	buf := make([]byte, 1)
	headerBuf := make([]byte, 4)
	for {
		n, err := f.Read(buf)
		if err != nil || n == 0 {
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
