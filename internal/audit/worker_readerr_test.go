package audit

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/pashagolub/pgxmock/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func rawFrame(payload []byte) []byte {
	frame := make([]byte, FrameHeaderSize+len(payload)+1)
	binary.BigEndian.PutUint32(frame[0:4], MagicHeader)
	binary.BigEndian.PutUint32(frame[4:8], crc32.ChecksumIEEE(payload))
	binary.BigEndian.PutUint32(frame[8:12], uint32(len(payload)))
	copy(frame[12:], payload)
	frame[FrameHeaderSize+len(payload)] = '\n'
	return frame
}

func eventFrame(t *testing.T, id string) []byte {
	t.Helper()
	p, err := json.Marshal(sampleCompletionEvent(id))
	require.NoError(t, err)
	return rawFrame(p)
}

func writeSegment(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, data, 0600))
	return path
}

func expectInserts(mock pgxmock.PgxPoolIface, n int) {
	b := mock.ExpectBatch()
	for i := 0; i < n; i++ {
		b.ExpectExec("INSERT INTO audit_events").
			WithArgs(anyAuditArgs()...).
			WillReturnResult(pgxmock.NewResult("INSERT", 1))
	}
}

func newReadErrWorker(t *testing.T, dir string, prune bool) (*AuditWorker, pgxmock.PgxPoolIface) {
	t.Helper()
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	t.Cleanup(mock.Close)
	w, err := NewAuditWorker(mock, WorkerConfig{SpoolDir: dir, BatchSize: 10, PruneArchived: prune})
	require.NoError(t, err)
	return w, mock
}

// Test A: a non-EOF, non-corrupt read error never advances the cursor or prunes. The
// error is injected through the unexported readFramedRecord seam, so the test is
// hermetic and independent of file permissions (root-safe).
func TestAuditWorker_ReadErrorHoldsCursorAndSegment(t *testing.T) {
	dir := t.TempDir()
	seg1 := writeSegment(t, dir, "wal-00000000000000000001-000001.log", eventFrame(t, "a"))
	writeSegment(t, dir, "wal-00000000000000000001-000002.log", eventFrame(t, "b"))
	w, mock := newReadErrWorker(t, dir, true)

	injected := errors.New("injected EIO")
	orig := readFramedRecord
	readFramedRecord = func(io.Reader) (*CompletionEvent, int64, error) { return nil, 0, injected }
	t.Cleanup(func() { readFramedRecord = orig })

	n, err := w.ProcessAvailable(context.Background())
	require.Error(t, err)
	assert.ErrorIs(t, err, injected)
	assert.Equal(t, 0, n)

	cur, lerr := w.Cursor().LoadOffset()
	require.NoError(t, lerr)
	assert.Equal(t, "", cur.SegmentFile, "cursor must not advance")
	assert.Equal(t, int64(0), cur.Offset)
	_, serr := os.Stat(seg1)
	assert.NoError(t, serr, "segment must not be pruned")
	assert.NoError(t, mock.ExpectationsWereMet(), "nothing from segment 2 processed")
}

// Test B: a CRC-valid frame with bad JSON is skipped and counted without wedging.
func TestAuditWorker_PoisonFrameSkipped(t *testing.T) {
	dir := t.TempDir()
	var data []byte
	data = append(data, eventFrame(t, "g1")...)
	data = append(data, rawFrame([]byte("{not json"))...)
	data = append(data, eventFrame(t, "g2")...)
	writeSegment(t, dir, "wal-00000000000000000001-000001.log", data)
	w, mock := newReadErrWorker(t, dir, false)
	expectInserts(mock, 2)

	n, err := w.ProcessAvailable(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	assert.Equal(t, int64(1), w.SkippedFrames())
	assert.NoError(t, mock.ExpectationsWereMet())
	cur, lerr := w.Cursor().LoadOffset()
	require.NoError(t, lerr)
	assert.Equal(t, int64(len(data)), cur.Offset)
}

// Test C: a torn tail on the newest segment is retried without advancing past it.
func TestAuditWorker_TornTailOnNewestRetried(t *testing.T) {
	dir := t.TempDir()
	const name = "wal-00000000000000000001-000001.log"
	good := eventFrame(t, "g1")
	second := eventFrame(t, "g2")
	torn := second[:len(second)/2]
	path := writeSegment(t, dir, name, append(append([]byte{}, good...), torn...))
	w, mock := newReadErrWorker(t, dir, false)
	expectInserts(mock, 1)

	n, err := w.ProcessAvailable(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	cur, lerr := w.Cursor().LoadOffset()
	require.NoError(t, lerr)
	assert.Equal(t, name, cur.SegmentFile)
	assert.Equal(t, int64(len(good)), cur.Offset, "cursor stops at the torn frame start")

	// The writer completes the frame; the next pass delivers it.
	require.NoError(t, os.WriteFile(path, append(append([]byte{}, good...), second...), 0600))
	expectInserts(mock, 1)
	n, err = w.ProcessAvailable(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.NoError(t, mock.ExpectationsWereMet())
	cur, lerr = w.Cursor().LoadOffset()
	require.NoError(t, lerr)
	assert.Equal(t, int64(len(good)+len(second)), cur.Offset)
}

// A torn tail on a historical segment is treated as its end so the worker moves on.
func TestAuditWorker_TornTailOnHistoricalSegmentEndsIt(t *testing.T) {
	dir := t.TempDir()
	good := eventFrame(t, "g1")
	second := eventFrame(t, "g2")
	seg1 := writeSegment(t, dir, "wal-00000000000000000001-000001.log", append(append([]byte{}, good...), second[:5]...))
	seg2Name := "wal-00000000000000000001-000002.log"
	writeSegment(t, dir, seg2Name, eventFrame(t, "g3"))
	w, mock := newReadErrWorker(t, dir, true)
	expectInserts(mock, 1)
	expectInserts(mock, 1)

	n, err := w.ProcessAvailable(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	assert.NoError(t, mock.ExpectationsWereMet())
	_, serr := os.Stat(seg1)
	assert.True(t, os.IsNotExist(serr), "fully read historical segment is pruned")
	cur, lerr := w.Cursor().LoadOffset()
	require.NoError(t, lerr)
	assert.Equal(t, seg2Name, cur.SegmentFile)
}

// WR-07: a skipped poison frame is reported through the hook exactly once, even when a
// later failure makes the worker re-read it before the cursor moves past it.
func TestAuditWorker_PoisonFrameCountedOnceAcrossRetry(t *testing.T) {
	dir := t.TempDir()
	var data []byte
	data = append(data, eventFrame(t, "g1")...)
	data = append(data, rawFrame([]byte("{not json"))...)
	data = append(data, eventFrame(t, "g2")...)
	writeSegment(t, dir, "wal-00000000000000000001-000001.log", data)
	w, mock := newReadErrWorker(t, dir, false)
	hookCalls := 0
	w.SetSkippedFrameHook(func() { hookCalls++ })

	// First pass: the DB rejects the batch, so the cursor is not saved.
	fb := mock.ExpectBatch()
	fb.ExpectExec("INSERT INTO audit_events").
		WithArgs(anyAuditArgs()...).
		WillReturnError(errors.New("db down"))
	fb.ExpectExec("INSERT INTO audit_events").
		WithArgs(anyAuditArgs()...).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	_, err := w.ProcessAvailable(context.Background())
	require.Error(t, err)
	cur, lerr := w.Cursor().LoadOffset()
	require.NoError(t, lerr)
	assert.Equal(t, int64(0), cur.Offset, "cursor held after the failed batch")

	// Second pass re-reads the poison frame and succeeds.
	expectInserts(mock, 2)
	n, err := w.ProcessAvailable(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	assert.NoError(t, mock.ExpectationsWereMet())
	assert.Equal(t, int64(1), w.SkippedFrames(), "skip counted once across the retry")
	assert.Equal(t, 1, hookCalls, "metric hook called once across the retry")
}
