package audit

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pashagolub/pgxmock/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// expectRoutes expects one batch whose rows carry exactly these route IDs, in order.
func expectRoutes(mock pgxmock.PgxPoolIface, routes ...string) {
	b := mock.ExpectBatch()
	for _, r := range routes {
		args := anyAuditArgs()
		args[9] = r // route_id
		b.ExpectExec("INSERT INTO audit_events").
			WithArgs(args...).
			WillReturnResult(pgxmock.NewResult("INSERT", 1))
	}
}

func legacySegName(ts, seq int64) string {
	return fmt.Sprintf("wal-%020d-%06d.log", ts, seq)
}

// R2 CR-01: the newest segment on disk carries a future timestamp (clock stepped back
// across a restart). The segment the spool rotates to must still order after it, so the
// worker never treats the live segment as historical and never prunes it.
func TestAuditWorker_ClockStepBackNeverPrunesLiveSegment(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC().UnixNano()
	future := now + int64(time.Hour)
	older := legacySegName(now-int64(time.Hour), 4)
	futureName := legacySegName(future, 5)
	writeSegment(t, dir, older, eventFrame(t, "O"))
	writeSegment(t, dir, futureName, append(eventFrame(t, "A"), tornBigFrame(t)...))

	w, mock := newReadErrWorker(t, dir, true)
	require.NoError(t, w.Cursor().SaveOffset(older, 0))

	s, err := NewDiskSpool(DiskSpoolConfig{SpoolDir: dir, MaxSegmentBytes: 1 << 20, VolumeQuotaBytes: 1 << 30, StatfsFunc: zeroStatfs})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	active := s.CurrentSegment()
	require.NotEqual(t, futureName, active, "torn tail forces a rotation")
	assert.True(t, compareSegmentNames(futureName, active) < 0, "new segment orders after the future-dated one")
	assert.True(t, futureName < active, "new segment name also sorts after it as a string")

	require.NoError(t, s.AppendPreForward(sampleCompletionEvent("B")))

	expectRoutes(mock, "O")
	expectRoutes(mock, "A")
	expectRoutes(mock, "B")
	n, err := w.ProcessAvailable(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 3, n)
	_, serr := os.Stat(filepath.Join(dir, active))
	require.NoError(t, serr, "the live segment must never be pruned")

	require.NoError(t, s.AppendPreForward(sampleCompletionEvent("C")))
	expectRoutes(mock, "C")
	n, err = w.ProcessAvailable(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, n, "C, acknowledged after the step-back, is ingested")
	assert.NoError(t, mock.ExpectationsWereMet())
	_, serr = os.Stat(filepath.Join(dir, active))
	assert.NoError(t, serr)
}

// R2 CR-01 stall variant: the cursor already sits on the future-dated segment. The new
// live segment must order after it and be read, not skipped forever.
func TestAuditWorker_ClockStepBackCursorOnFutureSegmentDoesNotStall(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC().UnixNano()
	futureName := legacySegName(now+int64(time.Hour), 5)
	a := eventFrame(t, "A")
	writeSegment(t, dir, futureName, append(append([]byte{}, a...), tornBigFrame(t)...))

	w, mock := newReadErrWorker(t, dir, true)
	require.NoError(t, w.Cursor().SaveOffset(futureName, int64(len(a))))

	s, err := NewDiskSpool(DiskSpoolConfig{SpoolDir: dir, MaxSegmentBytes: 1 << 20, VolumeQuotaBytes: 1 << 30, StatfsFunc: zeroStatfs})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	require.NoError(t, s.AppendPreForward(sampleCompletionEvent("B")))

	expectRoutes(mock, "B")
	n, err := w.ProcessAvailable(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.NoError(t, mock.ExpectationsWereMet())
	cur, lerr := w.Cursor().LoadOffset()
	require.NoError(t, lerr)
	assert.Equal(t, s.CurrentSegment(), cur.SegmentFile)
}

// R2 CR-01 upgrade compatibility: segments written by the old wall-clock naming (including
// a pair already out of time order after a step-back, and a duplicate sequence number) are
// read in sequence order, and the spool resumes on the highest one.
func TestSegmentOrdering_LegacyNamesUpgrade(t *testing.T) {
	dir := t.TempDir()
	T := time.Now().UTC().UnixNano()
	s4 := legacySegName(T-int64(time.Hour), 4)
	s5 := legacySegName(T+int64(time.Hour), 5)
	s6 := legacySegName(T, 6)
	s6b := legacySegName(T+int64(time.Minute), 6) // old code reused seq 6 after the step-back
	writeSegment(t, dir, s4, eventFrame(t, "L4"))
	writeSegment(t, dir, s5, eventFrame(t, "L5"))
	writeSegment(t, dir, s6, eventFrame(t, "L6"))
	writeSegment(t, dir, s6b, eventFrame(t, "L6b"))
	writeSegment(t, dir, "wal-garbage.log", []byte("not a segment"))

	got, err := listSegments(dir)
	require.NoError(t, err)
	assert.Equal(t, []string{s4, s5, s6, s6b}, got)

	s, err := NewDiskSpool(DiskSpoolConfig{SpoolDir: dir, MaxSegmentBytes: 1 << 20, VolumeQuotaBytes: 1 << 30, StatfsFunc: zeroStatfs})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	assert.Equal(t, s6b, s.CurrentSegment(), "spool resumes on the highest-ordered segment")
	require.NoError(t, s.AppendPreForward(sampleCompletionEvent("X")))

	w, mock := newReadErrWorker(t, dir, true)
	expectRoutes(mock, "L4")
	expectRoutes(mock, "L5")
	expectRoutes(mock, "L6")
	expectRoutes(mock, "L6b", "X")
	n, err := w.ProcessAvailable(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 5, n)
	assert.NoError(t, mock.ExpectationsWereMet())
	_, serr := os.Stat(filepath.Join(dir, s6b))
	assert.NoError(t, serr, "highest-ordered segment is never pruned")
}

func TestCompareSegmentNames_NumericNotString(t *testing.T) {
	a := legacySegName(5, 999999)
	b := fmt.Sprintf("wal-%020d-%d.log", 1, 1000000) // seq outgrew its padding
	assert.True(t, compareSegmentNames(a, b) < 0)
	assert.True(t, compareSegmentNames(b, a) > 0)
	assert.Equal(t, 0, compareSegmentNames(a, a))
	_, _, ok := parseSegmentName("wal-x-1.log")
	assert.False(t, ok)
}

func zeroStatfs(string) (uint64, uint64, error) { return 0, 100, nil }

// R2 WR-01: a torn frame cut exactly before its '\n' must not swallow the first byte of
// the next frame. G is never acknowledged and is dropped; B and C are ingested.
func TestAuditWorker_TornFrameMissingOnlyNewlineDoesNotSwallowNext(t *testing.T) {
	for _, historical := range []bool{true, false} {
		t.Run(fmt.Sprintf("historical=%v", historical), func(t *testing.T) {
			dir := t.TempDir()
			g := eventFrame(t, "G")
			var data []byte
			data = append(data, g[:len(g)-1]...)
			data = append(data, eventFrame(t, "B")...)
			data = append(data, eventFrame(t, "C")...)
			writeSegment(t, dir, legacySegName(1, 1), data)
			if historical {
				writeSegment(t, dir, legacySegName(1, 2), eventFrame(t, "D"))
			}
			w, mock := newReadErrWorker(t, dir, false)
			expectRoutes(mock, "B", "C")
			want := 2
			if historical {
				expectRoutes(mock, "D")
				want = 3
			}
			n, err := w.ProcessAvailable(context.Background())
			require.NoError(t, err)
			assert.Equal(t, want, n)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestReadFramedRecord_MissingTerminatorIsCorrupt(t *testing.T) {
	fr := eventFrame(t, "G")
	fr[len(fr)-1] = 0xAE
	_, _, err := ReadFramedRecord(bytes.NewReader(fr))
	assert.ErrorIs(t, err, ErrCorruptedRecord)
}

// R2 WR-02: a fully written CRC-bad frame that is resynced over is permanent loss and must
// be counted, once, even when a failed batch makes the worker re-read it.
func TestAuditWorker_CRCBadFrameCountedOnceAcrossRetry(t *testing.T) {
	dir := t.TempDir()
	a := eventFrame(t, "A")
	a[FrameHeaderSize+5] ^= 0x01 // bit rot in the payload
	data := append(append([]byte{}, a...), eventFrame(t, "B")...)
	writeSegment(t, dir, legacySegName(1, 1), data)
	w, mock := newReadErrWorker(t, dir, false)
	hookCalls := 0
	w.SetSkippedFrameHook(func() { hookCalls++ })

	fb := mock.ExpectBatch()
	fb.ExpectExec("INSERT INTO audit_events").
		WithArgs(anyAuditArgs()...).
		WillReturnError(assert.AnError)
	_, err := w.ProcessAvailable(context.Background())
	require.Error(t, err)

	expectRoutes(mock, "B")
	n, err := w.ProcessAvailable(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.NoError(t, mock.ExpectationsWereMet())
	assert.Equal(t, int64(1), w.SkippedFrames(), "CRC-bad frame counted once")
	assert.Equal(t, 1, hookCalls)
}

// R2 WR-02: bytes abandoned as a historical torn tail are counted; a torn tail on the
// newest segment is only retried and is not counted.
func TestAuditWorker_TornTailCountedOnlyWhenAbandoned(t *testing.T) {
	dir := t.TempDir()
	torn := append(eventFrame(t, "A"), tornBigFrame(t)...)
	writeSegment(t, dir, legacySegName(1, 1), torn)
	w, mock := newReadErrWorker(t, dir, false)
	expectRoutes(mock, "A")
	_, err := w.ProcessAvailable(context.Background())
	require.NoError(t, err)
	_, err = w.ProcessAvailable(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(0), w.SkippedFrames(), "a newest-segment tail may still complete")

	writeSegment(t, dir, legacySegName(1, 2), eventFrame(t, "B"))
	expectRoutes(mock, "B")
	_, err = w.ProcessAvailable(context.Background())
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
	assert.Equal(t, int64(1), w.SkippedFrames(), "abandoned historical tail counted")
}
