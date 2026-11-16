package audit

import (
	"bytes"
	"context"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func lengthHeader(length uint32) []byte {
	h := make([]byte, FrameHeaderSize)
	binary.BigEndian.PutUint32(h[0:4], MagicHeader)
	binary.BigEndian.PutUint32(h[4:8], 0)
	binary.BigEndian.PutUint32(h[8:12], length)
	return h
}

// WR-03: an unauthenticated length field is bounded before any allocation; 0xFFFFFFFF
// (which wraps length+1 to 0) and any length over the frame limit are corruption.
func TestReadFramedRecord_RejectsOversizedLength(t *testing.T) {
	for _, l := range []uint32{0xFFFFFFFF, MaxFramePayloadBytes + 1} {
		var ev *CompletionEvent
		var n int64
		var err error
		require.NotPanics(t, func() {
			ev, n, err = ReadFramedRecord(bytes.NewReader(append(lengthHeader(l), make([]byte, 64)...)))
		})
		assert.Nil(t, ev)
		assert.ErrorIs(t, err, ErrCorruptedRecord, "length %#x", l)
		assert.Equal(t, int64(FrameHeaderSize), n)
	}
}

// WR-03: the worker resyncs past a 0xFFFFFFFF header instead of crash-looping.
func TestAuditWorker_OversizedLengthHeaderResynced(t *testing.T) {
	dir := t.TempDir()
	data := append(lengthHeader(0xFFFFFFFF), eventFrame(t, "after")...)
	writeSegment(t, dir, "wal-00000000000000000001-000001.log", data)
	w, mock := newReadErrWorker(t, dir, false)
	expectInserts(mock, 1)

	var n int
	var err error
	require.NotPanics(t, func() { n, err = w.ProcessAvailable(context.Background()) })
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// WR-03: writers never produce a frame the reader would reject.
func TestFrameEvent_RejectsOversizedPayload(t *testing.T) {
	ev := sampleCompletionEvent("big")
	ev.CanonicalPath = "/" + strings.Repeat("x", int(MaxFramePayloadBytes))
	_, err := frameEvent(ev)
	assert.Error(t, err)

	s, serr := NewDiskSpool(DiskSpoolConfig{SpoolDir: t.TempDir(), MaxSegmentBytes: 1 << 24, VolumeQuotaBytes: 1 << 34, StatfsFunc: func(string) (uint64, uint64, error) { return 0, 100, nil }})
	require.NoError(t, serr)
	t.Cleanup(func() { _ = s.Close() })
	assert.Error(t, s.AppendPreForward(ev))
}
