package audit

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scanValidFrames returns the route IDs of every CRC-valid frame in one segment. It
// looks for the magic at every offset, so torn bytes never hide a later valid frame.
func scanValidFrames(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var routes []string
	for i := 0; i+FrameHeaderSize <= len(data); {
		if binary.BigEndian.Uint32(data[i:i+4]) == MagicHeader {
			ev, n, derr := ReadFramedRecord(bytes.NewReader(data[i:]))
			if derr == nil {
				routes = append(routes, ev.RouteID)
				i += int(n)
				continue
			}
		}
		i++
	}
	return routes
}

// routeSegments maps every route ID that survives as a valid frame to the segment holding it.
func routeSegments(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, seg := range groupSegments(t, dir) {
		for _, r := range scanValidFrames(t, filepath.Join(dir, seg)) {
			out[r] = seg
		}
	}
	return out
}

// appendRaw appends bytes to the active segment behind the spool's back. It models the
// state an unchanged AppendPreForward leaves after a partial write or a failed fsync:
// the file is longer than currentSize.
func appendRaw(t *testing.T, path string, b []byte) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0600)
	require.NoError(t, err)
	_, err = f.Write(b)
	require.NoError(t, err)
	require.NoError(t, f.Close())
}

// CR-01: a drifted size counter must never make a later failure cut acknowledged frames.
func TestWriteFramesDriftNeverCutsAcknowledgedFrames(t *testing.T) {
	dir := t.TempDir()
	s := newGroupSpool(t, dir, 1<<30, 0.10)
	boom := errors.New("injected fsync failure")

	require.NoError(t, s.WriteFrames([][]byte{mustFrame(t, "A")}))

	// AppendPreForward hit a partial write: 7 stray bytes, currentSize not advanced.
	appendRaw(t, filepath.Join(dir, s.CurrentSegment()), []byte("torn!!!"))

	require.NoError(t, s.WriteFrames([][]byte{mustFrame(t, "B")}))

	s.syncFn = func(*os.File) error { return boom }
	require.ErrorIs(t, s.WriteFrames([][]byte{mustFrame(t, "C")}), boom)
	s.syncFn = nil

	got := routeSegments(t, dir)
	assert.Contains(t, got, "A", "acknowledged frame A must survive")
	assert.Contains(t, got, "B", "acknowledged frame B must survive a later failed batch")

	// B must not sit behind the stray bytes: its segment decodes strictly.
	_ = readSegmentFrames(t, filepath.Join(dir, got["B"]))

	// And the spool is still usable afterwards.
	require.NoError(t, s.WriteFrames([][]byte{mustFrame(t, "D")}))
	assert.Contains(t, routeSegments(t, dir), "D")
	require.NoError(t, s.AppendPreForward(sampleCompletionEvent("pre")))
	assert.Contains(t, routeSegments(t, dir), "pre")
}

// WR-06: after a failed fsync the batch may already have been read by the worker, so the
// bytes must stay where they are and later frames must never land at a lower offset.
func TestWriteFramesSyncFailureKeepsPossiblyIngestedBytes(t *testing.T) {
	dir := t.TempDir()
	s := newGroupSpool(t, dir, 1<<30, 0.10)
	boom := errors.New("injected fsync failure")

	require.NoError(t, s.WriteFrames([][]byte{mustFrame(t, "first")}))
	oldSeg := s.CurrentSegment()

	var observedEnd int64
	s.syncFn = func(f *os.File) error {
		// The worker tails the active segment concurrently: it can read the whole
		// batch right now and checkpoint a cursor at this end offset.
		info, err := f.Stat()
		require.NoError(t, err)
		observedEnd = info.Size()
		return boom
	}
	require.ErrorIs(t, s.WriteFrames([][]byte{mustFrame(t, "unsynced-1"), mustFrame(t, "unsynced-2")}), boom)
	s.syncFn = nil

	info, err := os.Stat(filepath.Join(dir, oldSeg))
	require.NoError(t, err)
	assert.GreaterOrEqual(t, info.Size(), observedEnd,
		"bytes a concurrent reader may already have consumed must not be truncated away")

	require.NoError(t, s.WriteFrames([][]byte{mustFrame(t, "retry")}))
	got := routeSegments(t, dir)
	assert.Contains(t, got, "first")
	assert.Contains(t, got, "retry")
	assert.NotEqual(t, oldSeg, got["retry"], "the retry must go to a fresh segment, not behind possibly-ingested bytes")
}

// WR-07: a failed rotation after a durable batch must not fail the batch or close the
// active segment.
func TestWriteFramesRotationFailureAfterDurableBatch(t *testing.T) {
	dir := t.TempDir()
	s, err := NewDiskSpool(DiskSpoolConfig{
		SpoolDir:         dir,
		MaxSegmentBytes:  100, // any frame is larger, so every batch asks for a rotation
		VolumeQuotaBytes: 1 << 30,
		StatfsFunc:       fixedStatfs(0.10),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	s.openFn = func(string) (*os.File, error) { return nil, errors.New("injected EMFILE") }

	require.NoError(t, s.WriteFrames([][]byte{mustFrame(t, "A")}),
		"the batch was written and fsynced, so it must be reported as written")
	require.NoError(t, s.WriteFrames([][]byte{mustFrame(t, "B")}),
		"the active segment must still be usable after the failed rotation")

	s.openFn = nil
	require.NoError(t, s.AppendPreForward(sampleCompletionEvent("pre")),
		"AppendPreForward must keep working on the surviving active segment")

	got := routeSegments(t, dir)
	for _, r := range []string{"A", "B", "pre"} {
		assert.Contains(t, got, r)
	}
}

// A failure that leaves torn bytes behind and cannot rotate must fail allowed traffic
// closed instead of letting AppendPreForward append after them, and must heal once
// rotation works again.
func TestWriteFramesUnrotatableTornTailRefusesAppendUntilRepaired(t *testing.T) {
	dir := t.TempDir()
	s := newGroupSpool(t, dir, 1<<30, 0.10)
	boom := errors.New("injected write failure")

	require.NoError(t, s.WriteFrames([][]byte{mustFrame(t, "first")}))
	oldSeg := s.CurrentSegment()

	s.openFn = func(string) (*os.File, error) { return nil, errors.New("injected EMFILE") }
	s.writeFn = func(f *os.File, b []byte) (int, error) {
		n, _ := f.Write(b) // the whole batch lands, then the call reports failure
		return n, boom
	}
	require.Error(t, s.WriteFrames([][]byte{mustFrame(t, "doomed")}))
	s.writeFn = nil

	assert.True(t, s.IsSaturated(), "a segment that cannot be rotated away from torn bytes must refuse allowed traffic")
	assert.ErrorIs(t, s.AppendPreForward(sampleCompletionEvent("must-not-land")), ErrSpoolSaturated)
	assert.Error(t, s.WriteFrames([][]byte{mustFrame(t, "also-refused")}), "nothing may be appended behind the torn bytes")

	s.openFn = nil
	s.RepairSegment()
	assert.False(t, s.IsSaturated())
	assert.NotEqual(t, oldSeg, s.CurrentSegment())

	require.NoError(t, s.AppendPreForward(sampleCompletionEvent("pre")))
	require.NoError(t, s.WriteFrames([][]byte{mustFrame(t, "after")}))
	got := routeSegments(t, dir)
	assert.NotContains(t, got, "must-not-land")
	assert.NotContains(t, got, "also-refused")
	_ = readSegmentFrames(t, filepath.Join(dir, s.CurrentSegment()))
}

// The committer's recovery tick must repair a segment that could not be rotated, so
// allowed traffic does not stay refused until a denial happens to arrive.
func TestCommitterTickRepairsUnrotatableSegment(t *testing.T) {
	s := newGroupSpool(t, t.TempDir(), 1<<30, 0.10)
	boom := errors.New("injected write failure")
	s.openFn = func(string) (*os.File, error) { return nil, errors.New("injected EMFILE") }
	s.writeFn = func(f *os.File, b []byte) (int, error) {
		n, _ := f.Write(b)
		return n, boom
	}
	require.Error(t, s.WriteFrames([][]byte{mustFrame(t, "doomed")}))
	s.writeFn = nil
	require.True(t, s.IsSaturated())

	s.mu.Lock()
	s.openFn = nil
	s.mu.Unlock()
	startCommitter(t, s, nil, CommitterConfig{RecoveryInterval: 10 * time.Millisecond})
	eventually(t, func() bool { return !s.IsSaturated() }, "the recovery tick must repair the segment")
	require.NoError(t, s.AppendPreForward(sampleCompletionEvent("pre")))
}
