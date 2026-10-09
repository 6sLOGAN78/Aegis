package audit

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pashagolub/pgxmock/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func anyAuditArgs() []any {
	args := make([]any, 20)
	for i := 0; i < 20; i++ {
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

// writeWorkerTestFrames writes one WAL frame per event straight into a segment file,
// bypassing DiskSpool (and its statfs saturation gate), and returns the segment name.
func writeWorkerTestFrames(t *testing.T, dir string, events ...*CompletionEvent) string {
	t.Helper()
	const name = "wal-00000000000000000001-000001.log"
	var buf []byte
	for _, e := range events {
		payload, err := json.Marshal(e)
		require.NoError(t, err)
		frame := make([]byte, FrameHeaderSize+len(payload)+1)
		binary.BigEndian.PutUint32(frame[0:4], MagicHeader)
		binary.BigEndian.PutUint32(frame[4:8], crc32.ChecksumIEEE(payload))
		binary.BigEndian.PutUint32(frame[8:12], uint32(len(payload)))
		copy(frame[12:], payload)
		frame[FrameHeaderSize+len(payload)] = '\n'
		buf = append(buf, frame...)
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), buf, 0600))
	return name
}

// argMatcher adapts a predicate to pgxmock.Argument.
type argMatcher func(v any) bool

func (m argMatcher) Match(v any) bool { return m(v) }

// auditArgsWith returns 20 AnyArg matchers with the given index overrides.
func auditArgsWith(overrides map[int]any) []any {
	args := anyAuditArgs()
	for i, v := range overrides {
		args[i] = v
	}
	return args
}

func runWorkerOnce(t *testing.T, events []*CompletionEvent, overrides map[int]any) (*AuditWorker, pgxmock.PgxPoolIface, string) {
	t.Helper()
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	t.Cleanup(mock.Close)

	dir := t.TempDir()
	seg := writeWorkerTestFrames(t, dir, events...)

	b := mock.ExpectBatch()
	b.ExpectExec("INSERT INTO audit_events").
		WithArgs(auditArgsWith(overrides)...).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	worker, err := NewAuditWorker(mock, WorkerConfig{SpoolDir: dir, BatchSize: 10, FlushInterval: 100 * time.Millisecond})
	require.NoError(t, err)
	n, err := worker.ProcessAvailable(context.Background())
	require.NoError(t, err)
	require.Equal(t, len(events), n)
	require.NoError(t, mock.ExpectationsWereMet())
	return worker, mock, seg
}

func TestAuditWorker_EventType(t *testing.T) {
	cases := []struct {
		name      string
		eventType string
		status    int
		want      string
	}{
		{"explicit denial", EventTypeDenial, 403, "denial"},
		{"explicit completion", EventTypeCompletion, 200, "completion"},
		{"explicit decision", EventTypeDecision, 0, "decision"},
		{"untyped status 0 falls back to decision", "", 0, "decision"},
		{"untyped status 200 falls back to completion", "", 200, "completion"},
		{"unknown type falls back by status", "weird", 200, "completion"},
		{"uppercase type falls back by status", "DENIAL", 0, "decision"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			evt := sampleCompletionEvent("orders.create")
			evt.EventType = tc.eventType
			evt.HTTPStatus = tc.status
			runWorkerOnce(t, []*CompletionEvent{evt}, map[int]any{1: tc.want})
		})
	}
}

func TestAuditWorker_NormalizesPoison(t *testing.T) {
	evt := sampleCompletionEvent("orders.create")
	evt.EventID = "also-not-a-uuid"
	evt.RequestID = "not-a-uuid"
	evt.CanonicalPath = "/api/\x00x"
	evt.HTTPMethod = strings.Repeat("M", 28)
	evt.PrincipalID = strings.Repeat("p", 200)

	isUUID := func(v any) bool {
		s, ok := v.(string)
		if !ok {
			return false
		}
		_, err := uuid.Parse(s)
		return err == nil
	}
	worker, _, seg := runWorkerOnce(t, []*CompletionEvent{evt}, map[int]any{
		0:  argMatcher(isUUID),
		2:  argMatcher(isUUID),
		5:  strings.Repeat("p", 128),
		10: strings.Repeat("M", 16),
		11: "/api/x",
	})

	cursor, err := worker.Cursor().LoadOffset()
	require.NoError(t, err)
	assert.Equal(t, seg, cursor.SegmentFile)
	assert.Greater(t, cursor.Offset, int64(0))
}

func TestAuditWorker_SuppressedCount(t *testing.T) {
	t.Run("positive count is inserted", func(t *testing.T) {
		evt := sampleCompletionEvent("orders.create")
		evt.EventType = EventTypeDenial
		evt.SuppressedCount = 7
		runWorkerOnce(t, []*CompletionEvent{evt}, map[int]any{19: 7})
	})
	t.Run("zero count is NULL", func(t *testing.T) {
		evt := sampleCompletionEvent("orders.create")
		runWorkerOnce(t, []*CompletionEvent{evt}, map[int]any{19: nil})
	})
}

func TestAuditWorker_InsertStatementShape(t *testing.T) {
	src, err := os.ReadFile("worker.go")
	require.NoError(t, err)
	text := string(src)
	assert.True(t, strings.Contains(text, "error_code, suppressed_count"), "insert column list must end with suppressed_count")
	assert.True(t, strings.Contains(text, "$19, $20"), "insert must have 20 placeholders")
	assert.True(t, strings.Contains(text, "ON CONFLICT (event_date, event_id) DO NOTHING"), "dedupe clause must remain")
}
