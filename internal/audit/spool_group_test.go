package audit

import (
	"os"
	"path/filepath"
	"testing"

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
