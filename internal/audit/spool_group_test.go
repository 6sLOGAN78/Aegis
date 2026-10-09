package audit

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pashagolub/pgxmock/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixedStatfs returns a StatfsFunc reporting a constant used ratio so tests never
// depend on the host filesystem usage.
func fixedStatfs(ratio float64) func(string) (uint64, uint64, error) {
	return func(string) (uint64, uint64, error) {
		const total = 1_000_000
		return uint64(ratio * total), total, nil
	}
}

// newGroupSpool builds a spool with an explicit quota and injected statfs.
func newGroupSpool(t *testing.T, dir string, quota int64, ratio float64) *DiskSpool {
	t.Helper()
	s, err := NewDiskSpool(DiskSpoolConfig{
		SpoolDir:         dir,
		MaxSegmentBytes:  1 << 20,
		VolumeQuotaBytes: quota,
		StatfsFunc:       fixedStatfs(ratio),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestHardLimitBand(t *testing.T) {
	t.Run("statfs", func(t *testing.T) {
		dir := t.TempDir()
		ratio := 0.50
		s, err := NewDiskSpool(DiskSpoolConfig{
			SpoolDir:         dir,
			VolumeQuotaBytes: 1 << 30,
			StatfsFunc: func(string) (uint64, uint64, error) {
				return uint64(ratio * 1000), 1000, nil
			},
		})
		require.NoError(t, err)
		defer s.Close()

		sat, err := s.CheckSaturation()
		require.NoError(t, err)
		assert.False(t, sat)
		hl, err := s.HardLimitExceeded()
		require.NoError(t, err)
		assert.False(t, hl)

		ratio = 0.92
		sat, err = s.CheckSaturation()
		require.NoError(t, err)
		assert.True(t, sat)
		assert.ErrorIs(t, s.AppendPreForward(sampleCompletionEvent("r")), ErrSpoolSaturated)
		hl, err = s.HardLimitExceeded()
		require.NoError(t, err)
		assert.False(t, hl, "0.92 is inside the headroom band")

		ratio = 0.96
		hl, err = s.HardLimitExceeded()
		require.NoError(t, err)
		assert.True(t, hl)
	})

	t.Run("quota", func(t *testing.T) {
		dir := t.TempDir()
		s := newGroupSpool(t, dir, 10000, 0.10)
		pad := filepath.Join(dir, "padding.bin")

		require.NoError(t, os.WriteFile(pad, make([]byte, 5000), 0600))
		sat, err := s.CheckSaturation()
		require.NoError(t, err)
		assert.False(t, sat)

		require.NoError(t, os.WriteFile(pad, make([]byte, 9200), 0600))
		sat, err = s.CheckSaturation()
		require.NoError(t, err)
		assert.True(t, sat)
		assert.ErrorIs(t, s.AppendPreForward(sampleCompletionEvent("r")), ErrSpoolSaturated)
		hl, err := s.HardLimitExceeded()
		require.NoError(t, err)
		assert.False(t, hl)

		require.NoError(t, os.WriteFile(pad, make([]byte, 9600), 0600))
		hl, err = s.HardLimitExceeded()
		require.NoError(t, err)
		assert.True(t, hl)
	})
}

func TestWriteFaultGate(t *testing.T) {
	s := newGroupSpool(t, t.TempDir(), 1<<30, 0.10)

	require.NoError(t, s.AppendPreForward(sampleCompletionEvent("ok")))
	faulted, reason := s.WriteFaulted()
	assert.False(t, faulted)
	assert.Equal(t, "", reason)

	s.SetWriteFault("queue_full")
	sat, err := s.CheckSaturation()
	require.NoError(t, err)
	assert.True(t, sat)
	assert.True(t, s.IsSaturated())
	assert.ErrorIs(t, s.AppendPreForward(sampleCompletionEvent("blocked")), ErrSpoolSaturated)
	faulted, reason = s.WriteFaulted()
	assert.True(t, faulted)
	assert.Equal(t, "queue_full", reason)
	hl, err := s.HardLimitExceeded()
	require.NoError(t, err)
	assert.False(t, hl, "the fault flag must not affect the hard limit predicate")

	s.ClearWriteFault()
	require.NoError(t, s.AppendPreForward(sampleCompletionEvent("again")))
	faulted, reason = s.WriteFaulted()
	assert.False(t, faulted)
	assert.Equal(t, "", reason)
}

func TestHardLimitRatioValidation(t *testing.T) {
	mk := func(ratio float64) (*DiskSpool, error) {
		return NewDiskSpool(DiskSpoolConfig{
			SpoolDir:         t.TempDir(),
			VolumeQuotaBytes: 1 << 30,
			StatfsFunc:       fixedStatfs(0.10),
			HardLimitRatio:   ratio,
		})
	}

	s, err := mk(0)
	require.NoError(t, err)
	assert.InDelta(t, 0.95, s.cfg.HardLimitRatio, 1e-9)
	_ = s.Close()

	for _, bad := range []float64{0.90, 0.5, -1, 0.991, 1.5} {
		_, err := mk(bad)
		assert.Error(t, err, "ratio %v must be rejected", bad)
	}

	s, err = mk(0.97)
	require.NoError(t, err)
	assert.InDelta(t, 0.97, s.cfg.HardLimitRatio, 1e-9)
	_ = s.Close()
}

// groupSegments lists the wal segment file names in dir in lexicographic order.
func groupSegments(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "wal-") && strings.HasSuffix(e.Name(), ".log") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// readSegmentFrames decodes every frame of one segment; any torn or corrupt frame fails the test.
func readSegmentFrames(t *testing.T, path string) []*CompletionEvent {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()
	var out []*CompletionEvent
	for {
		ev, _, err := ReadFramedRecord(f)
		if errors.Is(err, io.EOF) {
			return out
		}
		require.NoError(t, err, "segment %s holds a torn or corrupt frame", path)
		out = append(out, ev)
	}
}

func mustFrame(t *testing.T, routeID string) []byte {
	t.Helper()
	fr, err := frameEvent(sampleCompletionEvent(routeID))
	require.NoError(t, err)
	return fr
}

func TestFrameEvent(t *testing.T) {
	ev := sampleCompletionEvent("route-x")
	frame, err := frameEvent(ev)
	require.NoError(t, err)

	assert.Equal(t, MagicHeader, binary.BigEndian.Uint32(frame[0:4]))
	payloadLen := int(binary.BigEndian.Uint32(frame[8:12]))
	assert.Equal(t, FrameHeaderSize+payloadLen+1, len(frame))
	assert.Equal(t, byte('\n'), frame[len(frame)-1])

	got, n, err := ReadFramedRecord(strings.NewReader(string(frame)))
	require.NoError(t, err)
	assert.Equal(t, int64(len(frame)), n)
	assert.Equal(t, ev.EventID, got.EventID)
	assert.Equal(t, ev.RouteID, got.RouteID)
	assert.Equal(t, ev.RequestID, got.RequestID)
	assert.True(t, ev.Timestamp.Equal(got.Timestamp))
}

func TestWriteFramesClosedAndEmpty(t *testing.T) {
	dir := t.TempDir()
	s := newGroupSpool(t, dir, 1<<30, 0.10)
	var syncs atomic.Int64
	s.syncFn = func(f *os.File) error { syncs.Add(1); return f.Sync() }

	require.NoError(t, s.WriteFrames(nil))
	require.NoError(t, s.WriteFrames([][]byte{}))
	assert.Equal(t, int64(0), syncs.Load(), "an empty batch must not touch the file")
	info, err := os.Stat(filepath.Join(dir, s.CurrentSegment()))
	require.NoError(t, err)
	assert.Equal(t, int64(0), info.Size())

	require.NoError(t, s.Close())
	assert.Error(t, s.WriteFrames([][]byte{mustFrame(t, "late")}))
}

func TestGroupCommit(t *testing.T) {
	dir := t.TempDir()
	s := newGroupSpool(t, dir, 1<<30, 0.10)
	var syncs atomic.Int64
	s.syncFn = func(f *os.File) error { syncs.Add(1); return f.Sync() }

	const goroutines = 64
	var submitted, calls atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			n := 1 + g%8
			frames := make([][]byte, 0, n)
			for i := 0; i < n; i++ {
				fr, err := frameEvent(sampleCompletionEvent(fmt.Sprintf("g%d-%d", g, i)))
				if err != nil {
					t.Errorf("frameEvent: %v", err)
					return
				}
				frames = append(frames, fr)
			}
			if err := s.WriteFrames(frames); err != nil {
				t.Errorf("WriteFrames: %v", err)
				return
			}
			submitted.Add(int64(n))
			calls.Add(1)
		}(g)
	}
	wg.Wait()
	require.False(t, t.Failed())

	total := 0
	routes := map[string]bool{}
	for _, seg := range groupSegments(t, dir) {
		for _, ev := range readSegmentFrames(t, filepath.Join(dir, seg)) {
			total++
			routes[ev.RouteID] = true
		}
	}
	assert.Equal(t, int(submitted.Load()), total)
	assert.Equal(t, total, len(routes), "every frame must be present exactly once")
	assert.Equal(t, calls.Load(), syncs.Load(), "exactly one fsync per batch, never per frame")
}

func TestGroupCommitRotation(t *testing.T) {
	dir := t.TempDir()
	s, err := NewDiskSpool(DiskSpoolConfig{
		SpoolDir:         dir,
		MaxSegmentBytes:  600,
		VolumeQuotaBytes: 1 << 30,
		StatfsFunc:       fixedStatfs(0.10),
	})
	require.NoError(t, err)
	defer s.Close()

	const batches = 24
	var wg sync.WaitGroup
	for b := 0; b < batches; b++ {
		wg.Add(1)
		go func(b int) {
			defer wg.Done()
			n := 1 + b%3
			frames := make([][]byte, 0, n)
			for i := 0; i < n; i++ {
				fr, err := frameEvent(sampleCompletionEvent(fmt.Sprintf("b%02d-%d", b, i)))
				if err != nil {
					t.Errorf("frameEvent: %v", err)
					return
				}
				frames = append(frames, fr)
			}
			if err := s.WriteFrames(frames); err != nil {
				t.Errorf("WriteFrames: %v", err)
			}
		}(b)
	}
	wg.Wait()
	require.False(t, t.Failed())

	segs := groupSegments(t, dir)
	require.Greater(t, len(segs), 1, "the tiny segment size must force rotation")

	batchSeg := map[string]string{}
	perSegment := map[string]int{}
	total := 0
	for _, seg := range segs {
		evs := readSegmentFrames(t, filepath.Join(dir, seg))
		perSegment[seg] = len(evs)
		for _, ev := range evs {
			total++
			batch := strings.SplitN(ev.RouteID, "-", 2)[0]
			if prev, ok := batchSeg[batch]; ok {
				assert.Equal(t, prev, seg, "batch %s was split across segments", batch)
			}
			batchSeg[batch] = seg
		}
	}
	expected := 0
	for b := 0; b < batches; b++ {
		expected += 1 + b%3
	}
	assert.Equal(t, expected, total)

	// A segment may exceed MaxSegmentBytes by at most one batch (3 frames here).
	for _, seg := range segs {
		info, err := os.Stat(filepath.Join(dir, seg))
		require.NoError(t, err)
		assert.Less(t, info.Size(), int64(600+3*1500))
	}

	// The audit worker must consume every frame across all segments.
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()
	for _, seg := range segs {
		if perSegment[seg] == 0 {
			continue
		}
		b := mock.ExpectBatch()
		for i := 0; i < perSegment[seg]; i++ {
			b.ExpectExec("INSERT INTO audit_events").
				WithArgs(anyAuditArgs()...).
				WillReturnResult(pgxmock.NewResult("INSERT", 1))
		}
	}
	worker, err := NewAuditWorker(mock, WorkerConfig{SpoolDir: dir, BatchSize: 1000, FlushInterval: 100 * time.Millisecond})
	require.NoError(t, err)
	n, err := worker.ProcessAvailable(context.Background())
	require.NoError(t, err)
	assert.Equal(t, expected, n)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestWriteFramesHardLimit(t *testing.T) {
	dir := t.TempDir()
	ratio := 0.96
	s, err := NewDiskSpool(DiskSpoolConfig{
		SpoolDir:         dir,
		VolumeQuotaBytes: 1 << 30,
		StatfsFunc:       func(string) (uint64, uint64, error) { return uint64(ratio * 1000), 1000, nil },
	})
	require.NoError(t, err)
	defer s.Close()

	seg := filepath.Join(dir, s.CurrentSegment())
	err = s.WriteFrames([][]byte{mustFrame(t, "denied")})
	assert.ErrorIs(t, err, ErrHardLimit)
	info, statErr := os.Stat(seg)
	require.NoError(t, statErr)
	assert.Equal(t, int64(0), info.Size(), "nothing may be written past the hard limit")

	ratio = 0.92
	assert.ErrorIs(t, s.AppendPreForward(sampleCompletionEvent("allow")), ErrSpoolSaturated)
	require.NoError(t, s.WriteFrames([][]byte{mustFrame(t, "headroom")}))
	assert.Len(t, readSegmentFrames(t, seg), 1)

	// The fault flag never blocks WriteFrames either.
	s.SetWriteFault("queue_full")
	require.NoError(t, s.WriteFrames([][]byte{mustFrame(t, "faulted")}))
	assert.Len(t, readSegmentFrames(t, seg), 2)
}

func TestWriteFramesFailureTruncates(t *testing.T) {
	sizeOf := func(t *testing.T, dir string, s *DiskSpool) int64 {
		t.Helper()
		info, err := os.Stat(filepath.Join(dir, s.CurrentSegment()))
		require.NoError(t, err)
		return info.Size()
	}
	boom := errors.New("injected failure")

	t.Run("write error", func(t *testing.T) {
		dir := t.TempDir()
		s := newGroupSpool(t, dir, 1<<30, 0.10)
		require.NoError(t, s.WriteFrames([][]byte{mustFrame(t, "first")}))
		before := sizeOf(t, dir, s)

		// Only part of the first frame lands: no reader can have decoded it, so it is
		// safe (and tidy) to truncate it away and keep appending to the same segment.
		s.writeFn = func(f *os.File, b []byte) (int, error) {
			n, _ := f.Write(b[:FrameHeaderSize-1])
			return n, boom
		}
		err := s.WriteFrames([][]byte{mustFrame(t, "torn-a"), mustFrame(t, "torn-b")})
		require.Error(t, err)
		assert.ErrorIs(t, err, boom)
		assert.Equal(t, before, sizeOf(t, dir, s), "torn bytes must be truncated away")

		s.writeFn = nil
		require.NoError(t, s.WriteFrames([][]byte{mustFrame(t, "after")}))
		evs := readSegmentFrames(t, filepath.Join(dir, s.CurrentSegment()))
		require.Len(t, evs, 2)
		assert.Equal(t, "first", evs[0].RouteID)
		assert.Equal(t, "after", evs[1].RouteID)
	})

	t.Run("write error after whole frames landed rotates", func(t *testing.T) {
		dir := t.TempDir()
		s := newGroupSpool(t, dir, 1<<30, 0.10)
		require.NoError(t, s.WriteFrames([][]byte{mustFrame(t, "first")}))
		oldSeg := s.CurrentSegment()
		before := sizeOf(t, dir, s)

		// A complete frame plus a torn tail landed: the worker may have ingested the
		// complete frame, so nothing is truncated and the retry goes to a new segment.
		fa, fb := mustFrame(t, "torn-a"), mustFrame(t, "torn-b")
		s.writeFn = func(f *os.File, b []byte) (int, error) {
			n, _ := f.Write(b[:len(fa)+5])
			return n, boom
		}
		err := s.WriteFrames([][]byte{fa, fb})
		require.ErrorIs(t, err, boom)
		info, statErr := os.Stat(filepath.Join(dir, oldSeg))
		require.NoError(t, statErr)
		assert.Greater(t, info.Size(), before, "bytes that may have been ingested must stay")
		assert.NotEqual(t, oldSeg, s.CurrentSegment())

		s.writeFn = nil
		require.NoError(t, s.WriteFrames([][]byte{mustFrame(t, "after")}))
		evs := readSegmentFrames(t, filepath.Join(dir, s.CurrentSegment()))
		require.Len(t, evs, 1)
		assert.Equal(t, "after", evs[0].RouteID)
		assert.Equal(t, []string{"first", "torn-a"}, scanValidFrames(t, filepath.Join(dir, oldSeg)), "the old segment keeps its acknowledged frame and the whole frame that landed")
	})

	t.Run("sync error", func(t *testing.T) {
		dir := t.TempDir()
		s := newGroupSpool(t, dir, 1<<30, 0.10)
		require.NoError(t, s.WriteFrames([][]byte{mustFrame(t, "first")}))
		oldSeg := s.CurrentSegment()
		before := sizeOf(t, dir, s)

		// After a failed fsync the whole batch is readable (WR-06): it must not be
		// truncated; the retry lands in a fresh segment instead.
		s.syncFn = func(*os.File) error { return boom }
		err := s.WriteFrames([][]byte{mustFrame(t, "unsynced")})
		require.Error(t, err)
		info, statErr := os.Stat(filepath.Join(dir, oldSeg))
		require.NoError(t, statErr)
		assert.Greater(t, info.Size(), before)
		assert.NotEqual(t, oldSeg, s.CurrentSegment())

		s.syncFn = nil
		require.NoError(t, s.WriteFrames([][]byte{mustFrame(t, "after")}))
		evs := readSegmentFrames(t, filepath.Join(dir, s.CurrentSegment()))
		require.Len(t, evs, 1)
		assert.Equal(t, "after", evs[0].RouteID)
	})

	t.Run("truncate failure rotates", func(t *testing.T) {
		dir := t.TempDir()
		s := newGroupSpool(t, dir, 1<<30, 0.10)
		require.NoError(t, s.WriteFrames([][]byte{mustFrame(t, "first")}))
		oldSeg := s.CurrentSegment()

		// Closing the file inside the failing write makes Truncate fail as well.
		s.writeFn = func(f *os.File, b []byte) (int, error) {
			_ = f.Close()
			return 0, boom
		}
		require.Error(t, s.WriteFrames([][]byte{mustFrame(t, "lost")}))
		assert.NotEqual(t, oldSeg, s.CurrentSegment(), "a failed truncate must rotate to a new segment")

		s.writeFn = nil
		require.NoError(t, s.WriteFrames([][]byte{mustFrame(t, "after")}))
		assert.Len(t, readSegmentFrames(t, filepath.Join(dir, oldSeg)), 1)
		evs := readSegmentFrames(t, filepath.Join(dir, s.CurrentSegment()))
		require.Len(t, evs, 1)
		assert.Equal(t, "after", evs[0].RouteID)
	})
}
