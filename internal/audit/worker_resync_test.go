package audit

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tornBigFrame returns the first 12+100 bytes of a valid frame whose header declares a
// ~4.4 KB payload: the shape a crash mid-write leaves behind.
func tornBigFrame(t *testing.T) []byte {
	t.Helper()
	ev := sampleCompletionEvent("big")
	ev.CanonicalPath = "/" + strings.Repeat("p", 4400)
	p, err := json.Marshal(ev)
	require.NoError(t, err)
	full := rawFrame(p)
	require.Greater(t, len(full), 4000)
	return full[:FrameHeaderSize+100]
}

// CR-01 (a): a historical segment holding a torn frame followed by complete frames B and
// C must deliver B and C before the segment is pruned.
func TestAuditWorker_TornFrameMidHistoricalSegmentRecovered(t *testing.T) {
	dir := t.TempDir()
	var data []byte
	data = append(data, eventFrame(t, "A")...)
	data = append(data, tornBigFrame(t)...)
	data = append(data, eventFrame(t, "B")...)
	data = append(data, eventFrame(t, "C")...)
	seg1 := writeSegment(t, dir, "wal-00000000000000000001-000001.log", data)
	seg2Name := "wal-00000000000000000001-000002.log"
	writeSegment(t, dir, seg2Name, eventFrame(t, "D"))

	w, mock := newReadErrWorker(t, dir, true)
	expectInserts(mock, 3) // A, B, C
	expectInserts(mock, 1) // D

	n, err := w.ProcessAvailable(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 4, n, "B and C behind the torn frame must be ingested")
	assert.NoError(t, mock.ExpectationsWereMet())
	_, serr := os.Stat(seg1)
	assert.True(t, os.IsNotExist(serr), "segment pruned only after B and C were delivered")
	cur, lerr := w.Cursor().LoadOffset()
	require.NoError(t, lerr)
	assert.Equal(t, seg2Name, cur.SegmentFile)
}

// CR-01 (b): the same layout on the newest segment must not stall: the cursor moves past
// the torn frame and B and C are ingested.
func TestAuditWorker_TornFrameMidNewestSegmentRecovered(t *testing.T) {
	dir := t.TempDir()
	const name = "wal-00000000000000000001-000001.log"
	var data []byte
	data = append(data, eventFrame(t, "A")...)
	data = append(data, tornBigFrame(t)...)
	data = append(data, eventFrame(t, "B")...)
	data = append(data, eventFrame(t, "C")...)
	writeSegment(t, dir, name, data)

	w, mock := newReadErrWorker(t, dir, false)
	expectInserts(mock, 3)

	n, err := w.ProcessAvailable(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 3, n)
	assert.NoError(t, mock.ExpectationsWereMet())
	cur, lerr := w.Cursor().LoadOffset()
	require.NoError(t, lerr)
	assert.Equal(t, name, cur.SegmentFile)
	assert.Equal(t, int64(len(data)), cur.Offset, "cursor advances past B and C")
}

// CR-01 (c): a CRC-bad frame whose declared length swallows later frames must not hide
// them: resync starts at the bad frame start + 1, so all 20 are recovered.
func TestAuditWorker_CRCBadFrameResyncRecoversAllFollowing(t *testing.T) {
	dir := t.TempDir()
	var tail []byte
	for i := 0; i < 20; i++ {
		tail = append(tail, eventFrame(t, "r")...)
	}
	bad := make([]byte, FrameHeaderSize)
	binary.BigEndian.PutUint32(bad[0:4], MagicHeader)
	binary.BigEndian.PutUint32(bad[4:8], 0xDEADBEEF)
	binary.BigEndian.PutUint32(bad[8:12], uint32(len(tail)/2))
	data := append(bad, tail...)
	writeSegment(t, dir, "wal-00000000000000000001-000001.log", data)

	w, mock := newReadErrWorker(t, dir, false)
	expectInserts(mock, 10)
	expectInserts(mock, 10)

	n, err := w.ProcessAvailable(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 20, n, "every frame behind the CRC-bad frame is recovered")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// CR-01 restart: reopening a spool whose newest segment ends in a torn frame must not
// append new frames directly behind the torn bytes; it rotates to a fresh segment.
func TestDiskSpool_ReopenRotatesAwayFromTornTail(t *testing.T) {
	dir := t.TempDir()
	const name = "wal-00000000000000000001-000001.log"
	data := append(eventFrame(t, "A"), tornBigFrame(t)...)
	writeSegment(t, dir, name, data)

	s, err := NewDiskSpool(DiskSpoolConfig{SpoolDir: dir, MaxSegmentBytes: 1 << 20, VolumeQuotaBytes: 1 << 30})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	assert.NotEqual(t, name, s.CurrentSegment(), "a torn tail must not be appended behind")

	require.NoError(t, s.AppendPreForward(sampleCompletionEvent("after-restart")))
	got, err := os.ReadFile(filepath.Join(dir, name))
	require.NoError(t, err)
	assert.Equal(t, data, got, "the torn segment is left untouched")
	evs := readSegmentFrames(t, filepath.Join(dir, s.CurrentSegment()))
	require.Len(t, evs, 1)
	assert.Equal(t, "after-restart", evs[0].RouteID)
}

// Coordinator follow-up to CR-01: on the active segment, a last frame whose declared
// window is not yet fully on disk is "not yet complete": the cursor stays at its start
// and, once the writer finishes it, it is ingested with nothing skipped.
func TestAuditWorker_PartiallyWrittenActiveFrameCompletesLater(t *testing.T) {
	dir := t.TempDir()
	const name = "wal-00000000000000000001-000001.log"
	a := eventFrame(t, "A")
	ev := sampleCompletionEvent("big")
	ev.CanonicalPath = "/" + strings.Repeat("p", 4400)
	p, err := json.Marshal(ev)
	require.NoError(t, err)
	big := rawFrame(p)
	partial := big[:FrameHeaderSize+100] // header fully present, window past EOF
	path := writeSegment(t, dir, name, append(append([]byte{}, a...), partial...))

	w, mock := newReadErrWorker(t, dir, false)
	expectInserts(mock, 1)
	n, err := w.ProcessAvailable(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	cur, lerr := w.Cursor().LoadOffset()
	require.NoError(t, lerr)
	assert.Equal(t, int64(len(a)), cur.Offset, "cursor held at the incomplete frame's start")

	// A second pass while still incomplete changes nothing.
	n, err = w.ProcessAvailable(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, n)

	// The writer completes the frame and appends one more.
	b := eventFrame(t, "B")
	full := append(append(append([]byte{}, a...), big...), b...)
	require.NoError(t, os.WriteFile(path, full, 0600))
	expectInserts(mock, 2)
	n, err = w.ProcessAvailable(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 2, n, "completed frame and its successor are both ingested")
	assert.NoError(t, mock.ExpectationsWereMet())
	cur, lerr = w.Cursor().LoadOffset()
	require.NoError(t, lerr)
	assert.Equal(t, int64(len(full)), cur.Offset)
}

// A spurious magic inside an incomplete tail must not move the cursor: the resync is
// provisional until a CRC-valid frame decodes after it.
func TestAuditWorker_SpuriousMagicInIncompleteTailHoldsCursor(t *testing.T) {
	dir := t.TempDir()
	const name = "wal-00000000000000000001-000001.log"
	a := eventFrame(t, "A")
	tail := make([]byte, FrameHeaderSize+40)
	binary.BigEndian.PutUint32(tail[0:4], MagicHeader)
	binary.BigEndian.PutUint32(tail[4:8], MagicHeader) // CRC field happens to look like magic
	binary.BigEndian.PutUint32(tail[8:12], 4000)
	writeSegment(t, dir, name, append(append([]byte{}, a...), tail...))

	w, mock := newReadErrWorker(t, dir, false)
	expectInserts(mock, 1)
	n, err := w.ProcessAvailable(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.NoError(t, mock.ExpectationsWereMet())
	cur, lerr := w.Cursor().LoadOffset()
	require.NoError(t, lerr)
	assert.Equal(t, int64(len(a)), cur.Offset, "cursor must stay at the first bad frame, not the spurious magic")
}

// Live path: after a failed AppendPreForward leaves torn bytes, the next append rotates
// instead of landing behind them.
func TestDiskSpool_AppendPreForwardRotatesAwayFromUntrackedTail(t *testing.T) {
	dir := t.TempDir()
	s, err := NewDiskSpool(DiskSpoolConfig{SpoolDir: dir, MaxSegmentBytes: 1 << 20, VolumeQuotaBytes: 1 << 30, StatfsFunc: func(string) (uint64, uint64, error) { return 0, 100, nil }})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	require.NoError(t, s.AppendPreForward(sampleCompletionEvent("A")))
	seg := s.CurrentSegment()
	appendRaw(t, filepath.Join(dir, seg), tornBigFrame(t))

	require.NoError(t, s.AppendPreForward(sampleCompletionEvent("B")))
	assert.NotEqual(t, seg, s.CurrentSegment())
	evs := readSegmentFrames(t, filepath.Join(dir, s.CurrentSegment()))
	require.Len(t, evs, 1)
	assert.Equal(t, "B", evs[0].RouteID)
}
