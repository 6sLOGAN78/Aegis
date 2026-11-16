package audit

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// MagicHeader is the 4-byte binary magic prefix identifying Aegis WAL frames.
const MagicHeader uint32 = 0xAE615001

// FrameHeaderSize is the 12-byte header size: [Magic: 4B][CRC32: 4B][Length: 4B].
const FrameHeaderSize = 12

// MaxFramePayloadBytes bounds a frame's payload. The length field is not covered by the
// CRC, so the reader rejects anything larger as corruption before allocating, and the
// writers refuse to produce such a frame. Normalized events are a few KB at most.
const MaxFramePayloadBytes uint32 = 1 << 20

// Sentinel errors for disk spool operations.
var (
	ErrSpoolSaturated  = errors.New("audit spool saturated (>= 90% capacity)")
	ErrCorruptedRecord = errors.New("wal record checksum mismatch")

	// ErrInvalidRecordJSON marks a CRC-valid frame whose payload is not decodable JSON.
	// The frame is intact and its bytes are consumed, so a reader can skip it.
	ErrInvalidRecordJSON = errors.New("wal record json invalid")

	// ErrHardLimit is returned by WriteFrames when the spool has crossed the hard limit
	// (default 95%). Nothing is written; the caller drops and counts the batch.
	ErrHardLimit = errors.New("audit spool at hard limit")
)

// defaultHardLimitRatio is the usage ratio above which even denial and completion
// records are refused. It sits above the 0.90 admission gate so those records keep
// writing into the headroom while allowed traffic is refused.
const defaultHardLimitRatio = 0.95

// DiskSpoolConfig defines storage directory and capacity thresholds for the append-only WAL.
type DiskSpoolConfig struct {
	SpoolDir         string
	MaxSegmentBytes  int64 // default 16MB
	VolumeQuotaBytes int64 // default 1GB

	// HardLimitRatio gates WriteFrames. 0 means 0.95; it must be greater than the
	// 0.90 saturation gate and at most 0.99.
	HardLimitRatio float64

	// StatfsFunc reports filesystem usage for path. nil means the real syscall.Statfs
	// (used = Blocks-Bfree, total = Blocks). Tests inject it to avoid depending on
	// the host's real disk usage.
	StatfsFunc func(path string) (used, total uint64, err error)
}

// DiskSpool manages local append-only WAL files on disk with synchronous fsync
// and segment rotation, enforcing Invariant 10 non-repudiation.
type DiskSpool struct {
	mu             sync.Mutex
	cfg            DiskSpoolConfig
	activeFile     *os.File
	currentSegment string
	currentSize    int64
	seq            int64
	closed         bool

	// writeFault, when set, makes CheckSaturation report saturation so the unchanged
	// AppendPreForward refuses allowed traffic (fail-closed, D-12). It never blocks WriteFrames.
	writeFault  atomic.Bool
	faultReason atomic.Value // string

	// needsRotate is set when the active segment may end in torn or possibly-ingested
	// bytes and a rotation away from it failed. While set, CheckSaturation reports true
	// (so the unchanged AppendPreForward refuses instead of appending behind the torn
	// bytes) and WriteFrames rotates before it writes anything.
	needsRotate atomic.Bool
	lastWarnNS  atomic.Int64

	// bytesHook, when set, is told how many bytes each successful write persisted.
	bytesHook atomic.Pointer[func(int)]

	// Test seams used only by WriteFrames. nil means the real f.Write / f.Sync.
	writeFn func(*os.File, []byte) (int, error)
	syncFn  func(*os.File) error
	// openFn opens a rotated segment. nil means os.OpenFile (O_CREATE|O_WRONLY|O_APPEND, 0600).
	openFn func(path string) (*os.File, error)
}

// NewDiskSpool initializes a DiskSpool in the specified directory with 0700 permissions.
func NewDiskSpool(cfg DiskSpoolConfig) (*DiskSpool, error) {
	if cfg.SpoolDir == "" {
		return nil, errors.New("spool directory cannot be empty")
	}
	if cfg.MaxSegmentBytes <= 0 {
		cfg.MaxSegmentBytes = 16 * 1024 * 1024 // 16 MB default
	}
	if cfg.VolumeQuotaBytes <= 0 {
		cfg.VolumeQuotaBytes = 1024 * 1024 * 1024 // 1 GB default
	}

	if cfg.HardLimitRatio == 0 {
		cfg.HardLimitRatio = defaultHardLimitRatio
	}
	if cfg.HardLimitRatio <= 0.90 || cfg.HardLimitRatio > 0.99 {
		return nil, fmt.Errorf("hard limit ratio must be greater than the 0.90 saturation gate and at most 0.99, got %v", cfg.HardLimitRatio)
	}

	if err := os.MkdirAll(cfg.SpoolDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create spool directory %s: %w", cfg.SpoolDir, err)
	}
	_ = os.Chmod(cfg.SpoolDir, 0700)

	entries, err := os.ReadDir(cfg.SpoolDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read spool directory %s: %w", cfg.SpoolDir, err)
	}

	var walFiles []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "wal-") && strings.HasSuffix(e.Name(), ".log") {
			walFiles = append(walFiles, e.Name())
		}
	}
	sort.Strings(walFiles)

	var activeFile *os.File
	var currentSegment string
	var currentSize int64
	var seq int64

	if len(walFiles) > 0 {
		latest := walFiles[len(walFiles)-1]
		parts := strings.Split(strings.TrimSuffix(latest, ".log"), "-")
		if len(parts) >= 3 {
			if parsedSeq, parseErr := strconv.ParseInt(parts[2], 10, 64); parseErr == nil {
				seq = parsedSeq
			}
		}

		latestPath := filepath.Join(cfg.SpoolDir, latest)
		info, statErr := os.Stat(latestPath)
		// Reuse the newest segment only when it ends exactly on a frame boundary. A crash
		// mid-write can leave a torn frame; appending behind it would hide later frames
		// from a reader that trusts the torn header's length, so such a segment is left
		// untouched and a fresh one is opened instead (nothing is truncated, so no
		// acknowledged, fsynced frame can ever be cut).
		if statErr == nil && info.Size() < cfg.MaxSegmentBytes && segmentEndsOnFrameBoundary(latestPath) {
			f, openErr := os.OpenFile(filepath.Join(cfg.SpoolDir, latest), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
			if openErr == nil {
				activeFile = f
				currentSegment = latest
				currentSize = info.Size()
			}
		}
	}

	if activeFile == nil {
		seq++
		filename := fmt.Sprintf("wal-%020d-%06d.log", time.Now().UTC().UnixNano(), seq)
		path := filepath.Join(cfg.SpoolDir, filename)
		f, openErr := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if openErr != nil {
			return nil, fmt.Errorf("failed to create initial wal segment: %w", openErr)
		}
		activeFile = f
		currentSegment = filename
		currentSize = 0
	}

	return &DiskSpool{
		cfg:            cfg,
		activeFile:     activeFile,
		currentSegment: currentSegment,
		currentSize:    currentSize,
		seq:            seq,
	}, nil
}

// segmentEndsOnFrameBoundary reports whether every byte of the segment at path belongs
// to a complete, CRC-valid frame. Any read error, torn or corrupt frame returns false.
func segmentEndsOnFrameBoundary(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	r := bufio.NewReader(f)
	for {
		_, _, err := ReadFramedRecord(r)
		switch {
		case err == nil, errors.Is(err, ErrInvalidRecordJSON):
			continue
		case errors.Is(err, io.EOF):
			return true
		default:
			return false
		}
	}
}

// statfsRatio returns the used/total ratio of the filesystem holding the spool.
// ok is false when the figure is unavailable (statfs error or zero total).
func (s *DiskSpool) statfsRatio() (float64, bool) {
	if s.cfg.StatfsFunc != nil {
		used, total, err := s.cfg.StatfsFunc(s.cfg.SpoolDir)
		if err != nil || total == 0 {
			return 0, false
		}
		return float64(used) / float64(total), true
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(s.cfg.SpoolDir, &stat); err != nil || stat.Blocks == 0 {
		return 0, false
	}
	usedBlocks := stat.Blocks - stat.Bfree
	return float64(usedBlocks) / float64(stat.Blocks), true
}

// CheckSaturation evaluates whether the spool directory or underlying filesystem
// has reached or exceeded 90% capacity (AUD-02). It also reports saturation while
// the write-fault flag is set, which fails allowed traffic closed (D-12).
func (s *DiskSpool) CheckSaturation() (bool, error) {
	if s.writeFault.Load() || s.needsRotate.Load() {
		return true, nil
	}

	// 1. Check configured volume quota
	if s.cfg.VolumeQuotaBytes > 0 {
		usage, err := s.dirUsageBytes()
		if err == nil {
			ratio := float64(usage) / float64(s.cfg.VolumeQuotaBytes)
			if ratio >= 0.90 {
				return true, nil
			}
		}
	}

	// 2. Check underlying filesystem stats
	if ratio, ok := s.statfsRatio(); ok && ratio >= 0.90 {
		return true, nil
	}

	return false, nil
}

// checkHardLimit mirrors CheckSaturation's quota and filesystem checks against
// cfg.HardLimitRatio and deliberately ignores the write-fault flag.
func (s *DiskSpool) checkHardLimit() (bool, error) {
	if s.cfg.VolumeQuotaBytes > 0 {
		usage, err := s.dirUsageBytes()
		if err == nil {
			ratio := float64(usage) / float64(s.cfg.VolumeQuotaBytes)
			if ratio >= s.cfg.HardLimitRatio {
				return true, nil
			}
		}
	}
	if ratio, ok := s.statfsRatio(); ok && ratio >= s.cfg.HardLimitRatio {
		return true, nil
	}
	return false, nil
}

// HardLimitExceeded reports whether usage has crossed the hard limit that gates
// WriteFrames. The committer polls it to decide when to clear a write fault.
func (s *DiskSpool) HardLimitExceeded() (bool, error) {
	return s.checkHardLimit()
}

// SetWriteFault marks the audit pipeline as losing data. While set, CheckSaturation
// reports true so AppendPreForward refuses allowed traffic. WriteFrames is unaffected.
func (s *DiskSpool) SetWriteFault(reason string) {
	s.faultReason.Store(reason)
	s.writeFault.Store(true)
}

// ClearWriteFault clears the write-fault flag.
func (s *DiskSpool) ClearWriteFault() {
	s.writeFault.Store(false)
	s.faultReason.Store("")
}

// WriteFaulted reports whether the write-fault flag is set and the recorded reason.
func (s *DiskSpool) WriteFaulted() (bool, string) {
	if !s.writeFault.Load() {
		return false, ""
	}
	reason, _ := s.faultReason.Load().(string)
	return true, reason
}

// IsSaturated reports whether the spool has reached or exceeded 90% capacity.
func (s *DiskSpool) IsSaturated() bool {
	saturated, err := s.CheckSaturation()
	return err != nil || saturated
}

// UtilizationRatio returns the current capacity utilization ratio of the spool (0.0 to 1.0).
func (s *DiskSpool) UtilizationRatio() float64 {
	if s.cfg.VolumeQuotaBytes > 0 {
		usage, err := s.dirUsageBytes()
		if err == nil {
			return float64(usage) / float64(s.cfg.VolumeQuotaBytes)
		}
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(s.cfg.SpoolDir, &stat); err == nil && stat.Blocks > 0 {
		usedBlocks := stat.Blocks - stat.Bfree
		return float64(usedBlocks) / float64(stat.Blocks)
	}
	return 0.0
}

func (s *DiskSpool) dirUsageBytes() (int64, error) {
	entries, err := os.ReadDir(s.cfg.SpoolDir)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err == nil {
			total += info.Size()
		}
	}
	return total, nil
}

// AppendPreForward serializes event, calculates CRC32 checksum, frames the record,
// and synchronously flushes to non-volatile disk storage via fsync (AUD-01).
// Halts admission with ErrSpoolSaturated if the 90% capacity safety gate is breached (AUD-02).
func (s *DiskSpool) AppendPreForward(event *CompletionEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return errors.New("spool is closed")
	}

	// 1. Evaluate 90% saturation gate
	saturated, err := s.CheckSaturation()
	if err != nil || saturated {
		return ErrSpoolSaturated
	}

	// 2. Marshal event to JSON payload
	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal completion event: %w", err)
	}

	if len(payload) > int(MaxFramePayloadBytes) {
		return fmt.Errorf("completion event payload %d bytes exceeds frame limit %d", len(payload), MaxFramePayloadBytes)
	}

	// 3. Compute 32-bit CRC32 checksum
	checksum := crc32.ChecksumIEEE(payload)
	payloadLen := uint32(len(payload))

	// 4. Construct binary frame: [Magic: 4B][CRC32: 4B][Length: 4B][Payload: NB][\n: 1B]
	frame := make([]byte, FrameHeaderSize+len(payload)+1)
	binary.BigEndian.PutUint32(frame[0:4], MagicHeader)
	binary.BigEndian.PutUint32(frame[4:8], checksum)
	binary.BigEndian.PutUint32(frame[8:12], payloadLen)
	copy(frame[12:], payload)
	frame[FrameHeaderSize+len(payload)] = '\n'

	// 5. Never append behind bytes this spool did not account for (a torn tail left by
	// an earlier failed write or fsync): rotate first, as WriteFrames does, so a live
	// append can never land behind a torn frame.
	if s.hasUntrackedTailLocked() {
		if rerr := s.rotateSegment(); rerr != nil {
			s.needsRotate.Store(true)
			return fmt.Errorf("failed to rotate away from an untrusted wal segment tail: %w", rerr)
		}
		s.needsRotate.Store(false)
	}

	// Append to active segment file
	if _, err := s.activeFile.Write(frame); err != nil {
		return fmt.Errorf("failed to append wal record: %w", err)
	}

	// 6. Mandatory fsync syscall (AUD-01, Invariant 10)
	if err := s.activeFile.Sync(); err != nil {
		return fmt.Errorf("failed to fsync wal record: %w", err)
	}

	s.currentSize += int64(len(frame))
	s.notifyBytesWritten(len(frame))

	// 7. Rotate segment if size threshold exceeded
	if s.currentSize >= s.cfg.MaxSegmentBytes {
		if err := s.rotateSegment(); err != nil {
			return fmt.Errorf("failed to rotate segment after append: %w", err)
		}
	}

	return nil
}

// frameEvent marshals ev as given and builds the exact frame AppendPreForward builds:
// [Magic: 4B][CRC32: 4B][Length: 4B][Payload: NB][\n: 1B]. It does not normalize;
// callers call ev.Normalize() first. Used only by the batched write path.
func frameEvent(ev *CompletionEvent) ([]byte, error) {
	payload, err := json.Marshal(ev)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal completion event: %w", err)
	}
	if len(payload) > int(MaxFramePayloadBytes) {
		return nil, fmt.Errorf("completion event payload %d bytes exceeds frame limit %d", len(payload), MaxFramePayloadBytes)
	}
	frame := make([]byte, FrameHeaderSize+len(payload)+1)
	binary.BigEndian.PutUint32(frame[0:4], MagicHeader)
	binary.BigEndian.PutUint32(frame[4:8], crc32.ChecksumIEEE(payload))
	binary.BigEndian.PutUint32(frame[8:12], uint32(len(payload)))
	copy(frame[12:], payload)
	frame[FrameHeaderSize+len(payload)] = '\n'
	return frame, nil
}

// WriteFrames appends a batch of pre-built frames to the active segment with ONE
// write and ONE fsync under the spool mutex (group commit). It is gated by the hard
// limit (default 95%), never by the 90% admission gate or the write-fault flag, so
// denial and completion records keep writing while allowed traffic is refused.
// The segment is rotated only after the whole batch is written and synced, so no
// frame straddles two segments.
//
// Recovery rule (one rule for every failure; the audit worker tails the active segment
// concurrently, so bytes that reached the file may already have been ingested):
//
//   - Nothing is ever appended behind bytes this spool did not account for. Before a
//     write the file's real size is compared with currentSize; any difference (a torn or
//     unsynced tail left by AppendPreForward, whose size counter is not advanced on a
//     failed write or fsync) triggers a rotation first, so currentSize is never trusted
//     to decide where a segment ends.
//   - After a failed write or fsync nothing is truncated, except a tail that is shorter
//     than the first frame of the batch (no reader can decode it, so no cursor can have
//     moved past it). Anything longer stays where it is and the spool rotates to a fresh
//     segment, so acknowledged frames are never cut and a cursor can never end up beyond
//     the rewritten end of a file. Frames are self-describing and the retry's duplicates
//     are absorbed by ON CONFLICT.
//   - If the rotation itself fails the segment is flagged (needsRotate): allowed traffic
//     is refused through CheckSaturation and RepairSegment / the next WriteFrames retry
//     the rotation before anything is written.
//   - A rotation failure after a batch that was written and synced does not fail the
//     batch; the next write retries the rotation.
func (s *DiskSpool) WriteFrames(frames [][]byte) error {
	if len(frames) == 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return errors.New("spool is closed")
	}

	exceeded, err := s.checkHardLimit()
	if err != nil || exceeded {
		return ErrHardLimit
	}

	if s.needsRotate.Load() || s.hasUntrackedTailLocked() {
		if rerr := s.rotateSegment(); rerr != nil {
			s.needsRotate.Store(true)
			return fmt.Errorf("failed to rotate away from an untrusted wal segment tail: %w", rerr)
		}
		s.needsRotate.Store(false)
	}

	size := 0
	for _, f := range frames {
		size += len(f)
	}
	buf := make([]byte, 0, size)
	for _, f := range frames {
		buf = append(buf, f...)
	}

	base := s.currentSize
	var werr error
	if s.writeFn != nil {
		_, werr = s.writeFn(s.activeFile, buf)
	} else {
		_, werr = s.activeFile.Write(buf)
	}
	if werr == nil {
		if s.syncFn != nil {
			werr = s.syncFn(s.activeFile)
		} else {
			werr = s.activeFile.Sync()
		}
	}
	if werr != nil {
		return s.settleFailedBatchLocked(base, len(frames[0]), werr)
	}

	s.currentSize += int64(len(buf))
	s.notifyBytesWritten(len(buf))

	if s.currentSize >= s.cfg.MaxSegmentBytes {
		// The batch is durable: a failed rotation must not turn it into a failure.
		// currentSize stays over the limit, so the next write retries the rotation.
		if rerr := s.rotateSegment(); rerr != nil {
			s.warnf("audit spool: segment rotation failed after a durable batch, continuing on the current segment: %v", rerr)
		}
	}
	return nil
}

// hasUntrackedTailLocked reports whether the active file holds bytes beyond (or fewer
// than) currentSize, or cannot be inspected at all. Caller holds s.mu.
func (s *DiskSpool) hasUntrackedTailLocked() bool {
	if s.activeFile == nil {
		return true
	}
	fi, err := s.activeFile.Stat()
	if err != nil {
		return true
	}
	return fi.Size() != s.currentSize
}

// settleFailedBatchLocked applies the recovery rule after a failed write or fsync of a
// batch that started at offset base. Caller holds s.mu.
func (s *DiskSpool) settleFailedBatchLocked(base int64, firstFrameLen int, werr error) error {
	fi, serr := s.activeFile.Stat()
	if serr == nil {
		landed := fi.Size() - base
		if landed <= 0 {
			// Nothing reached the file, so there is nothing to repair.
			return fmt.Errorf("failed to write wal batch: %w", werr)
		}
		if landed < int64(firstFrameLen) {
			// Only part of the first frame: no reader can have decoded it.
			if terr := s.activeFile.Truncate(base); terr == nil {
				return fmt.Errorf("failed to write wal batch: %w", werr)
			}
		}
	}
	if rerr := s.rotateSegment(); rerr != nil {
		s.needsRotate.Store(true)
		return fmt.Errorf("failed to write wal batch: %w (rotate: %v)", werr, rerr)
	}
	return fmt.Errorf("failed to write wal batch: %w", werr)
}

// RepairSegment retries a rotation that failed after a write error left the active
// segment untrustworthy. It is a no-op when no repair is pending. The committer calls
// it from its recovery tick so allowed traffic does not stay refused until a denial
// happens to arrive.
func (s *DiskSpool) RepairSegment() {
	if !s.needsRotate.Load() {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || !s.needsRotate.Load() {
		return
	}
	if err := s.rotateSegment(); err == nil {
		s.needsRotate.Store(false)
	}
}

// warnf logs at most one line per second.
func (s *DiskSpool) warnf(format string, args ...any) {
	now := time.Now().UnixNano()
	last := s.lastWarnNS.Load()
	if now-last < int64(time.Second) || !s.lastWarnNS.CompareAndSwap(last, now) {
		return
	}
	log.Printf(format, args...)
}

// rotateSegment opens the next segment first and only then retires the old one, so a
// failed open leaves the current segment untouched and usable.
func (s *DiskSpool) rotateSegment() error {
	seq := s.seq + 1
	filename := fmt.Sprintf("wal-%020d-%06d.log", time.Now().UTC().UnixNano(), seq)
	path := filepath.Join(s.cfg.SpoolDir, filename)
	open := s.openFn
	if open == nil {
		open = func(p string) (*os.File, error) {
			return os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		}
	}
	f, err := open(path)
	if err != nil {
		return fmt.Errorf("failed to open rotated wal segment: %w", err)
	}

	if s.activeFile != nil {
		_ = s.activeFile.Sync()
		_ = s.activeFile.Close()
	}
	s.seq = seq
	s.activeFile = f
	s.currentSegment = filename
	s.currentSize = 0
	return nil
}

// CurrentSegment returns the name of the currently active WAL segment file.
func (s *DiskSpool) CurrentSegment() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.currentSegment
}

// Close flushes and cleanly closes the active segment file.
func (s *DiskSpool) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}
	s.closed = true

	if s.activeFile != nil {
		_ = s.activeFile.Sync()
		err := s.activeFile.Close()
		s.activeFile = nil
		return err
	}
	return nil
}

// ReadFramedRecord reads a single binary framed WAL record from r,
// verifies the magic bytes and CRC32 checksum, and returns the deserialized CompletionEvent.
func ReadFramedRecord(r io.Reader) (*CompletionEvent, int64, error) {
	header := make([]byte, FrameHeaderSize)
	if _, err := io.ReadFull(r, header); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, 0, io.EOF
		}
		return nil, 0, err
	}

	magic := binary.BigEndian.Uint32(header[0:4])
	if magic != MagicHeader {
		return nil, int64(FrameHeaderSize), ErrCorruptedRecord
	}

	expectedCRC := binary.BigEndian.Uint32(header[4:8])
	length := binary.BigEndian.Uint32(header[8:12])

	// The length is not CRC-protected: bound it before allocating (a value such as
	// 0xFFFFFFFF would otherwise wrap length+1 to 0 and panic, or allocate ~4 GiB).
	if length > MaxFramePayloadBytes {
		return nil, int64(FrameHeaderSize), fmt.Errorf("%w: frame length %d exceeds limit %d", ErrCorruptedRecord, length, MaxFramePayloadBytes)
	}

	// Read payload + trailing newline
	buf := make([]byte, int(length)+1)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, int64(FrameHeaderSize), fmt.Errorf("%w: partial record payload: %w", ErrCorruptedRecord, io.ErrUnexpectedEOF)
	}

	payload := buf[:length]
	actualCRC := crc32.ChecksumIEEE(payload)
	if actualCRC != expectedCRC {
		return nil, int64(FrameHeaderSize + int(length) + 1), ErrCorruptedRecord
	}

	var event CompletionEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return nil, int64(FrameHeaderSize + int(length) + 1), fmt.Errorf("%w: %v", ErrInvalidRecordJSON, err)
	}

	totalBytes := int64(FrameHeaderSize + int(length) + 1)
	return &event, totalBytes, nil
}

// SetBytesWrittenHook registers fn to be called with the number of bytes persisted by
// every successful WriteFrames and AppendPreForward. Pass nil to clear. Safe to call
// after construction; fn runs while the spool lock is held, so it must not call back
// into the spool.
func (s *DiskSpool) SetBytesWrittenHook(fn func(int)) {
	if fn == nil {
		s.bytesHook.Store(nil)
		return
	}
	s.bytesHook.Store(&fn)
}

func (s *DiskSpool) notifyBytesWritten(n int) {
	if h := s.bytesHook.Load(); h != nil {
		(*h)(n)
	}
}
