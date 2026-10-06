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
)

// DiskSpoolConfig defines storage directory and capacity thresholds for the append-only WAL.
type DiskSpoolConfig struct {
	SpoolDir         string
	MaxSegmentBytes  int64 // default 16MB
	VolumeQuotaBytes int64 // default 1GB
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

// CheckSaturation evaluates whether the spool directory or underlying filesystem
// has reached or exceeded 90% capacity (AUD-02).
func (s *DiskSpool) CheckSaturation() (bool, error) {
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
	var stat syscall.Statfs_t
	if err := syscall.Statfs(s.cfg.SpoolDir, &stat); err == nil && stat.Blocks > 0 {
		usedBlocks := stat.Blocks - stat.Bfree
		ratio := float64(usedBlocks) / float64(stat.Blocks)
		if ratio >= 0.90 {
			return true, nil
		}
	}

	return false, nil
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
