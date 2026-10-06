package audit

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sampleCompletionEvent(routeID string) *CompletionEvent {
	return &CompletionEvent{
		EventID:         uuid.NewString(),
		Timestamp:       time.Now().UTC(),
		RequestID:       uuid.NewString(),
		PrincipalID:     "user-123",
		PrincipalKind:   "user",
		PrincipalRoles:  []string{"operator"},
		ClientIP:        "192.168.1.50",
		HTTPMethod:      "POST",
		CanonicalPath:   "/api/orders",
		RouteID:         routeID,
		ServiceID:       "orders-service",
		Decision:        "allow",
		ReasonCode:      "ALLOWED",
		HTTPStatus:      200,
		DurationMS:      1.25,
		SnapshotVersion: 1,
	}
}

func TestDiskSpool_AppendAndRead(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "aegis-spool-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	spool, err := NewDiskSpool(DiskSpoolConfig{
		SpoolDir:         tmpDir,
		MaxSegmentBytes:  1024 * 1024,
		VolumeQuotaBytes: 10 * 1024 * 1024,
	})
	require.NoError(t, err)

	evt1 := sampleCompletionEvent("route-1")
	evt2 := sampleCompletionEvent("route-2")

	err = spool.AppendPreForward(evt1)
	require.NoError(t, err)
	err = spool.AppendPreForward(evt2)
	require.NoError(t, err)

	activeSeg := spool.CurrentSegment()
	require.NotEmpty(t, activeSeg)

	err = spool.Close()
	require.NoError(t, err)

	// Read and verify records from segment file
	segPath := filepath.Join(tmpDir, activeSeg)
	f, err := os.Open(segPath)
	require.NoError(t, err)
	defer f.Close()

	read1, bytesRead1, err := ReadFramedRecord(f)
	require.NoError(t, err)
	assert.Greater(t, bytesRead1, int64(0))
	assert.Equal(t, evt1.EventID, read1.EventID)
	assert.Equal(t, evt1.RouteID, read1.RouteID)
	assert.Equal(t, evt1.PrincipalID, read1.PrincipalID)

	read2, bytesRead2, err := ReadFramedRecord(f)
	require.NoError(t, err)
	assert.Greater(t, bytesRead2, int64(0))
	assert.Equal(t, evt2.EventID, read2.EventID)
	assert.Equal(t, evt2.RouteID, read2.RouteID)

	_, _, err = ReadFramedRecord(f)
	assert.Equal(t, io.EOF, err)
}

func TestDiskSpool_ChecksumAndCorruptionDetection(t *testing.T) {
	evt := sampleCompletionEvent("route-test")
	payload := []byte(`{"event_id":"` + evt.EventID + `","decision":"allow"}`)
	checksum := crc32.ChecksumIEEE(payload)
	payloadLen := uint32(len(payload))

	// Construct valid binary frame
	buf := make([]byte, FrameHeaderSize+len(payload)+1)
	binary.BigEndian.PutUint32(buf[0:4], MagicHeader)
	binary.BigEndian.PutUint32(buf[4:8], checksum)
	binary.BigEndian.PutUint32(buf[8:12], payloadLen)
	copy(buf[12:], payload)
	buf[FrameHeaderSize+len(payload)] = '\n'

	// 1. Valid record verification
	r := bytes.NewReader(buf)
	readEvt, n, err := ReadFramedRecord(r)
	require.NoError(t, err)
	assert.Equal(t, int64(len(buf)), n)
	assert.Equal(t, evt.EventID, readEvt.EventID)

	// 2. Invalid magic header
	corruptedMagic := make([]byte, len(buf))
	copy(corruptedMagic, buf)
	binary.BigEndian.PutUint32(corruptedMagic[0:4], 0x12345678)
	_, _, err = ReadFramedRecord(bytes.NewReader(corruptedMagic))
	assert.ErrorIs(t, err, ErrCorruptedRecord)

	// 3. Corrupted payload (bit flip)
	corruptedPayload := make([]byte, len(buf))
	copy(corruptedPayload, buf)
	corruptedPayload[15] ^= 0xFF
	_, _, err = ReadFramedRecord(bytes.NewReader(corruptedPayload))
	assert.ErrorIs(t, err, ErrCorruptedRecord)

	// 4. Truncated frame
	truncated := buf[:FrameHeaderSize+len(payload)/2]
	_, _, err = ReadFramedRecord(bytes.NewReader(truncated))
	assert.Error(t, err)
}

func TestDiskSpool_SegmentRotation(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "aegis-spool-rotation-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Set tiny max segment bytes to trigger rotation after single record
	maxBytes := int64(200)
	spool, err := NewDiskSpool(DiskSpoolConfig{
		SpoolDir:         tmpDir,
		MaxSegmentBytes:  maxBytes,
		VolumeQuotaBytes: 10 * 1024 * 1024,
	})
	require.NoError(t, err)

	seg1 := spool.CurrentSegment()
	require.NotEmpty(t, seg1)

	// Write record that exceeds 200 bytes
	evt1 := sampleCompletionEvent("route-1")
	err = spool.AppendPreForward(evt1)
	require.NoError(t, err)

	seg2 := spool.CurrentSegment()
	assert.NotEqual(t, seg1, seg2, "Segment should have rotated after exceeding MaxSegmentBytes")

	evt2 := sampleCompletionEvent("route-2")
	err = spool.AppendPreForward(evt2)
	require.NoError(t, err)

	err = spool.Close()
	require.NoError(t, err)

	// Check files created in directory
	entries, err := os.ReadDir(tmpDir)
	require.NoError(t, err)
	var walCount int
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".log" {
			walCount++
		}
	}
	assert.GreaterOrEqual(t, walCount, 2, "Expected multiple WAL segment files after rotation")
}

func TestDiskSpool_ReopenRecovery(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "aegis-spool-reopen-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	spool1, err := NewDiskSpool(DiskSpoolConfig{
		SpoolDir:         tmpDir,
		MaxSegmentBytes:  1024 * 1024,
		VolumeQuotaBytes: 10 * 1024 * 1024,
	})
	require.NoError(t, err)

	evt1 := sampleCompletionEvent("route-1")
	err = spool1.AppendPreForward(evt1)
	require.NoError(t, err)
	seg1 := spool1.CurrentSegment()

	err = spool1.Close()
	require.NoError(t, err)

	// Reopen same directory with a new spool instance
	spool2, err := NewDiskSpool(DiskSpoolConfig{
		SpoolDir:         tmpDir,
		MaxSegmentBytes:  1024 * 1024,
		VolumeQuotaBytes: 10 * 1024 * 1024,
	})
	require.NoError(t, err)
	defer spool2.Close()

	// Should resume on the existing segment
	assert.Equal(t, seg1, spool2.CurrentSegment())

	evt2 := sampleCompletionEvent("route-2")
	err = spool2.AppendPreForward(evt2)
	require.NoError(t, err)

	// Read both records from the recovered file
	f, err := os.Open(filepath.Join(tmpDir, seg1))
	require.NoError(t, err)
	defer f.Close()

	r1, _, err := ReadFramedRecord(f)
	require.NoError(t, err)
	assert.Equal(t, evt1.EventID, r1.EventID)

	r2, _, err := ReadFramedRecord(f)
	require.NoError(t, err)
	assert.Equal(t, evt2.EventID, r2.EventID)
}

func TestDiskSpool_VolumeQuotaSaturation(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "aegis-spool-saturation-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Quota is 1000 bytes; 90% is 900 bytes
	quota := int64(1000)
	spool, err := NewDiskSpool(DiskSpoolConfig{
		SpoolDir:         tmpDir,
		MaxSegmentBytes:  500,
		VolumeQuotaBytes: quota,
	})
	require.NoError(t, err)
	defer spool.Close()

	// Write records until spool exceeds 900 bytes
	var reachedSaturation bool
	for i := 0; i < 20; i++ {
		evt := sampleCompletionEvent("route-fill")
		err := spool.AppendPreForward(evt)
		if err != nil {
			if err == ErrSpoolSaturated {
				reachedSaturation = true
				break
			}
			require.NoError(t, err)
		}
	}

	assert.True(t, reachedSaturation, "Expected ErrSpoolSaturated when quota reaches >= 90%")
}

func TestDiskSpool_ConcurrentAppends(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "aegis-spool-concurrency-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	spool, err := NewDiskSpool(DiskSpoolConfig{
		SpoolDir:         tmpDir,
		MaxSegmentBytes:  64 * 1024,
		VolumeQuotaBytes: 10 * 1024 * 1024,
	})
	require.NoError(t, err)
	defer spool.Close()

	numGoroutines := 10
	recordsPerGoroutine := 20
	var wg sync.WaitGroup

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < recordsPerGoroutine; j++ {
				evt := sampleCompletionEvent("route-concurrent")
				err := spool.AppendPreForward(evt)
				assert.NoError(t, err)
			}
		}(i)
	}

	wg.Wait()
}
