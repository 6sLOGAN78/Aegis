package audit

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
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

// Sentinel errors for disk spool operations.
var (
	ErrSpoolSaturated  = errors.New("audit spool saturated (>= 90% capacity)")
	ErrCorruptedRecord = errors.New("wal record checksum mismatch")

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

	// Test seams used only by WriteFrames. nil means the real f.Write / f.Sync.
	writeFn func(*os.File, []byte) (int, error)
	syncFn  func(*os.File) error
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

		info, statErr := os.Stat(filepath.Join(cfg.SpoolDir, latest))
		if statErr == nil && info.Size() < cfg.MaxSegmentBytes {
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
	if s.writeFault.Load() {
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

	// 5. Append to active segment file
	if _, err := s.activeFile.Write(frame); err != nil {
		return fmt.Errorf("failed to append wal record: %w", err)
	}

	// 6. Mandatory fsync syscall (AUD-01, Invariant 10)
	if err := s.activeFile.Sync(); err != nil {
		return fmt.Errorf("failed to fsync wal record: %w", err)
	}

	s.currentSize += int64(len(frame))

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
// frame straddles two segments. On a write or fsync error the segment is truncated
// back to the last good size (or rotated if truncation fails).
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

	size := 0
	for _, f := range frames {
		size += len(f)
	}
	buf := make([]byte, 0, size)
	for _, f := range frames {
		buf = append(buf, f...)
	}

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
		// Drop any torn bytes so a retry cannot append whole frames after them.
		if terr := s.activeFile.Truncate(s.currentSize); terr != nil {
			if rerr := s.rotateSegment(); rerr != nil {
				return fmt.Errorf("failed to write wal batch: %w (truncate: %v, rotate: %v)", werr, terr, rerr)
			}
		}
		return fmt.Errorf("failed to write wal batch: %w", werr)
	}

	s.currentSize += int64(len(buf))

	if s.currentSize >= s.cfg.MaxSegmentBytes {
		if err := s.rotateSegment(); err != nil {
			return fmt.Errorf("failed to rotate segment after batch: %w", err)
		}
	}
	return nil
}

func (s *DiskSpool) rotateSegment() error {
	if s.activeFile != nil {
		_ = s.activeFile.Sync()
		_ = s.activeFile.Close()
	}

	s.seq++
	filename := fmt.Sprintf("wal-%020d-%06d.log", time.Now().UTC().UnixNano(), s.seq)
	path := filepath.Join(s.cfg.SpoolDir, filename)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return fmt.Errorf("failed to open rotated wal segment: %w", err)
	}

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

	// Read payload + trailing newline
	buf := make([]byte, length+1)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, int64(FrameHeaderSize), fmt.Errorf("%w: partial record payload", ErrCorruptedRecord)
	}

	payload := buf[:length]
	actualCRC := crc32.ChecksumIEEE(payload)
	if actualCRC != expectedCRC {
		return nil, int64(FrameHeaderSize + int(length) + 1), ErrCorruptedRecord
	}

	var event CompletionEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return nil, int64(FrameHeaderSize + int(length) + 1), fmt.Errorf("failed to unmarshal record json: %w", err)
	}

	totalBytes := int64(FrameHeaderSize + int(length) + 1)
	return &event, totalBytes, nil
}
