package audit

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pashagolub/pgxmock/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func anyAuditArgs() []any {
	args := make([]any, 19)
	for i := 0; i < 19; i++ {
		args[i] = pgxmock.AnyArg()
	}
	return args
}

func TestAuditWorker_BatchFlushAndCursorAdvance(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	tmpSpoolDir := t.TempDir()

	// 1. Write sample events using DiskSpool
	spool, err := NewDiskSpool(DiskSpoolConfig{
		SpoolDir:         tmpSpoolDir,
		MaxSegmentBytes:  1024 * 1024,
		VolumeQuotaBytes: 10 * 1024 * 1024,
	})
	require.NoError(t, err)

	evt1 := sampleCompletionEvent("orders.create")
	evt2 := sampleCompletionEvent("payments.charge")

	err = spool.AppendPreForward(evt1)
	require.NoError(t, err)
	err = spool.AppendPreForward(evt2)
	require.NoError(t, err)
	segName := spool.CurrentSegment()
	require.NoError(t, spool.Close())

	// 2. Setup pgxmock batch expectations
	b := mock.ExpectBatch()
	b.ExpectExec("INSERT INTO audit_events").
		WithArgs(anyAuditArgs()...).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	b.ExpectExec("INSERT INTO audit_events").
		WithArgs(anyAuditArgs()...).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	// 3. Run worker pass
	worker, err := NewAuditWorker(mock, WorkerConfig{
		SpoolDir:      tmpSpoolDir,
		BatchSize:     10,
		FlushInterval: 100 * time.Millisecond,
	})
	require.NoError(t, err)

	processed, err := worker.ProcessAvailable(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 2, processed, "Expected 2 events processed in batch")

	// 4. Verify all database expectations were met
	assert.NoError(t, mock.ExpectationsWereMet())

	// 5. Verify persistent cursor advanced
	cursorState, err := worker.Cursor().LoadOffset()
	require.NoError(t, err)
	assert.Equal(t, segName, cursorState.SegmentFile)
	assert.Greater(t, cursorState.Offset, int64(0))
}

func TestAuditWorker_IdempotentDuplicateHandling(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	tmpSpoolDir := t.TempDir()

	spool, err := NewDiskSpool(DiskSpoolConfig{
		SpoolDir:         tmpSpoolDir,
		MaxSegmentBytes:  1024 * 1024,
		VolumeQuotaBytes: 10 * 1024 * 1024,
	})
	require.NoError(t, err)

	evtDuplicate := sampleCompletionEvent("orders.duplicate")
	err = spool.AppendPreForward(evtDuplicate)
	require.NoError(t, err)
	require.NoError(t, spool.Close())

	// Simulate ON CONFLICT DO NOTHING (0 rows inserted)
	b := mock.ExpectBatch()
	b.ExpectExec("INSERT INTO audit_events").
		WithArgs(anyAuditArgs()...).
		WillReturnResult(pgxmock.NewResult("INSERT", 0))

	worker, err := NewAuditWorker(mock, WorkerConfig{
		SpoolDir:      tmpSpoolDir,
		BatchSize:     10,
		FlushInterval: 100 * time.Millisecond,
	})
	require.NoError(t, err)

	processed, err := worker.ProcessAvailable(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, processed)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestAuditWorker_CursorRetainsOnDatabaseError(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	tmpSpoolDir := t.TempDir()

	spool, err := NewDiskSpool(DiskSpoolConfig{
		SpoolDir:         tmpSpoolDir,
		MaxSegmentBytes:  1024 * 1024,
		VolumeQuotaBytes: 10 * 1024 * 1024,
	})
	require.NoError(t, err)

	evt := sampleCompletionEvent("orders.fail")
	err = spool.AppendPreForward(evt)
	require.NoError(t, err)
	require.NoError(t, spool.Close())

	// Simulate database connectivity or execution failure
	b := mock.ExpectBatch()
	b.ExpectExec("INSERT INTO audit_events").
		WithArgs(anyAuditArgs()...).
		WillReturnError(os.ErrDeadlineExceeded)

	worker, err := NewAuditWorker(mock, WorkerConfig{
		SpoolDir:      tmpSpoolDir,
		BatchSize:     10,
		FlushInterval: 100 * time.Millisecond,
	})
	require.NoError(t, err)

	// Processing should fail
	_, err = worker.ProcessAvailable(context.Background())
	assert.Error(t, err)

	// Checkpoint offset must NOT advance on failure (at-least-once guarantee)
	cursorState, err := worker.Cursor().LoadOffset()
	require.NoError(t, err)
	assert.Equal(t, int64(0), cursorState.Offset, "Cursor must not advance when database batch fails")
}

func TestAuditWorker_RestartRecovery(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	tmpSpoolDir := t.TempDir()

	spool, err := NewDiskSpool(DiskSpoolConfig{
		SpoolDir:         tmpSpoolDir,
		MaxSegmentBytes:  1024 * 1024,
		VolumeQuotaBytes: 10 * 1024 * 1024,
	})
	require.NoError(t, err)

	evt1 := sampleCompletionEvent("orders.first")
	evt2 := sampleCompletionEvent("orders.second")

	require.NoError(t, spool.AppendPreForward(evt1))
	require.NoError(t, spool.AppendPreForward(evt2))
	require.NoError(t, spool.Close())

	// Phase 1: Worker 1 processes evt1 and evt2
	b1 := mock.ExpectBatch()
	b1.ExpectExec("INSERT INTO audit_events").
		WithArgs(anyAuditArgs()...).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	b1.ExpectExec("INSERT INTO audit_events").
		WithArgs(anyAuditArgs()...).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	worker1, err := NewAuditWorker(mock, WorkerConfig{
		SpoolDir:      tmpSpoolDir,
		BatchSize:     10,
		FlushInterval: 100 * time.Millisecond,
	})
	require.NoError(t, err)

	processed1, err := worker1.ProcessAvailable(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 2, processed1)
	assert.NoError(t, mock.ExpectationsWereMet())

	// Append evt3 to same spool
	spool2, err := NewDiskSpool(DiskSpoolConfig{
		SpoolDir:         tmpSpoolDir,
		MaxSegmentBytes:  1024 * 1024,
		VolumeQuotaBytes: 10 * 1024 * 1024,
	})
	require.NoError(t, err)
	evt3 := sampleCompletionEvent("orders.third")
	require.NoError(t, spool2.AppendPreForward(evt3))
	require.NoError(t, spool2.Close())

	// Phase 2: Worker 2 restarts and only processes evt3 (skipping evt1 & evt2 via cursor)
	b2 := mock.ExpectBatch()
	b2.ExpectExec("INSERT INTO audit_events").
		WithArgs(anyAuditArgs()...).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	worker2, err := NewAuditWorker(mock, WorkerConfig{
		SpoolDir:      tmpSpoolDir,
		BatchSize:     10,
		FlushInterval: 100 * time.Millisecond,
	})
	require.NoError(t, err)

	processed2, err := worker2.ProcessAvailable(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, processed2, "Restarted worker must process only new records beyond cursor")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestAuditWorker_CorruptedRecordRecovery(t *testing.T) {
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	defer mock.Close()

	tmpSpoolDir := t.TempDir()

	spool, err := NewDiskSpool(DiskSpoolConfig{
		SpoolDir:         tmpSpoolDir,
		MaxSegmentBytes:  1024 * 1024,
		VolumeQuotaBytes: 10 * 1024 * 1024,
	})
	require.NoError(t, err)

	evt1 := sampleCompletionEvent("orders.clean1")
	evt2 := sampleCompletionEvent("orders.clean2")

	require.NoError(t, spool.AppendPreForward(evt1))
	activeSeg := spool.CurrentSegment()

	// Inject corrupted bytes into the middle of the active segment file
	segPath := filepath.Join(tmpSpoolDir, activeSeg)
	f, err := os.OpenFile(segPath, os.O_WRONLY|os.O_APPEND, 0600)
	require.NoError(t, err)

	corruptBytes := make([]byte, 20)
	binary.BigEndian.PutUint32(corruptBytes[0:4], 0xDEADBEEF) // Bad magic
	_, err = f.Write(corruptBytes)
	require.NoError(t, err)
	require.NoError(t, f.Sync())
	require.NoError(t, f.Close())

	// Append clean record evt2
	require.NoError(t, spool.AppendPreForward(evt2))
	require.NoError(t, spool.Close())

	// Worker should ingest both clean records and skip corrupted bytes
	b := mock.ExpectBatch()
	b.ExpectExec("INSERT INTO audit_events").
		WithArgs(anyAuditArgs()...).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	b.ExpectExec("INSERT INTO audit_events").
		WithArgs(anyAuditArgs()...).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	worker, err := NewAuditWorker(mock, WorkerConfig{
		SpoolDir:      tmpSpoolDir,
		BatchSize:     10,
		FlushInterval: 100 * time.Millisecond,
	})
	require.NoError(t, err)

	processed, err := worker.ProcessAvailable(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 2, processed, "Worker must preserve and process both clean records despite corruption")
	assert.NoError(t, mock.ExpectationsWereMet())
}
