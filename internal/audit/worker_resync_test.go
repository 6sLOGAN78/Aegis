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
