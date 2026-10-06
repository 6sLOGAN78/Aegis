package audit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// CursorState holds the persisted cursor checkpoint position in the WAL spool.
type CursorState struct {
	SegmentFile string    `json:"segment_file"`
	Offset      int64     `json:"offset"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// CursorTracker provides atomic, durable persistence of the worker's WAL processing offset.
type CursorTracker struct {
	mu       sync.Mutex
	filePath string
	tmpPath  string
}

// NewCursorTracker instantiates a tracker for wal.cursor inside spoolDir.
func NewCursorTracker(spoolDir string) *CursorTracker {
	return &CursorTracker{
		filePath: filepath.Join(spoolDir, "wal.cursor"),
		tmpPath:  filepath.Join(spoolDir, "wal.cursor.tmp"),
	}
}

// SaveOffset atomically writes and fsyncs the new segment file and offset checkpoint.
func (c *CursorTracker) SaveOffset(segmentFile string, offset int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	state := CursorState{
		SegmentFile: segmentFile,
		Offset:      offset,
		UpdatedAt:   time.Now().UTC(),
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal cursor state: %w", err)
	}

	// Write to temporary file with 0600 permissions
	f, err := os.OpenFile(c.tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to open temp cursor file: %w", err)
	}

	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("failed to write temp cursor file: %w", err)
	}

	// Force fsync before atomic rename
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("failed to sync temp cursor file: %w", err)
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("failed to close temp cursor file: %w", err)
	}

	// Atomic rename to target cursor file
	if err := os.Rename(c.tmpPath, c.filePath); err != nil {
		return fmt.Errorf("failed to atomically rename cursor file: %w", err)
	}

	return nil
}

// LoadOffset loads the persisted checkpoint from disk.
// Returns an empty initial CursorState if wal.cursor does not exist yet.
func (c *CursorTracker) LoadOffset() (*CursorState, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	data, err := os.ReadFile(c.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return &CursorState{
				SegmentFile: "",
				Offset:      0,
				UpdatedAt:   time.Time{},
			}, nil
		}
		return nil, fmt.Errorf("failed to read cursor file: %w", err)
	}

	var state CursorState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to unmarshal cursor state: %w", err)
	}

	return &state, nil
}
