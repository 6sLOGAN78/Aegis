package audit

import (
	"errors"
	"os"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSpoolBytesWrittenHook_WriteFrames(t *testing.T) {
	s := newTestSpool(t, 0.10)
	var total atomic.Int64
	var calls atomic.Int64
	s.SetBytesWrittenHook(func(n int) { total.Add(int64(n)); calls.Add(1) })

	frames := [][]byte{[]byte("aaaa"), []byte("bbbbbb"), []byte("cc")}
	require.NoError(t, s.WriteFrames(frames))
	assert.Equal(t, int64(12), total.Load())
	assert.Equal(t, int64(1), calls.Load())
}

func TestSpoolBytesWrittenHook_AppendPreForward(t *testing.T) {
	s := newTestSpool(t, 0.10)
	var got atomic.Int64
	s.SetBytesWrittenHook(func(n int) { got.Store(int64(n)) })

	require.NoError(t, s.AppendPreForward(sampleCompletionEvent("pre")))
	fi, err := s.activeFile.Stat()
	require.NoError(t, err)
	assert.Equal(t, fi.Size(), got.Load(), "hook reports the whole frame size")
}

func TestSpoolBytesWrittenHook_FailureAndNil(t *testing.T) {
	s := newTestSpool(t, 0.10)

	// nil hook is a no-op.
	require.NoError(t, s.WriteFrames([][]byte{[]byte("x")}))
	require.NoError(t, s.AppendPreForward(sampleCompletionEvent("nohook")))

	var calls atomic.Int64
	s.SetBytesWrittenHook(func(int) { calls.Add(1) })
	s.writeFn = func(*os.File, []byte) (int, error) { return 0, errors.New("injected disk error") }
	assert.Error(t, s.WriteFrames([][]byte{[]byte("fail")}))
	assert.Equal(t, int64(0), calls.Load(), "failed writes never invoke the hook")

	// Clearing the hook is safe too.
	s.writeFn = nil
	s.SetBytesWrittenHook(nil)
	require.NoError(t, s.WriteFrames([][]byte{[]byte("ok")}))
	assert.Equal(t, int64(0), calls.Load())
}
