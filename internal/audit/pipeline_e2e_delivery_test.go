package audit

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pashagolub/pgxmock/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// e2eEffectiveType mirrors the worker's event_type rule: an explicit valid type wins,
// otherwise the legacy status-based guess.
func e2eEffectiveType(e *CompletionEvent) string {
	if ValidEventType(e.EventType) {
		return e.EventType
	}
	if e.HTTPStatus > 0 {
		return EventTypeCompletion
	}
	return EventTypeDecision
}

func TestE2EWorkerDeliversAllKinds(t *testing.T) {
	env := newE2E(t, e2eOptions{})

	ok := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}
	require.Equal(t, http.StatusOK, env.do(e2eAllowHandler(env.spool, true, ok), e2eRequest(http.MethodGet, "/orders", "203.0.113.9:4444")).Code)
	require.Equal(t, http.StatusForbidden, env.do(
		e2eDenyHandler("user-1", "user", "admin.write", "DENIED_DEVELOPER_ADMIN_FORBIDDEN", "FORBIDDEN", http.StatusForbidden),
		e2eRequest(http.MethodPost, "/admin", "203.0.113.9:4444")).Code)
	limited := e2eDenyHandler("user-2", "user", "orders.read", "RATE_LIMIT_EXCEEDED", "RATE_LIMIT_EXCEEDED", http.StatusTooManyRequests)
	for i := 0; i < 3; i++ {
		require.Equal(t, http.StatusTooManyRequests, env.do(limited, e2eRequest(http.MethodGet, "/orders", "203.0.113.9:4444")).Code)
	}
	require.Equal(t, http.StatusUnauthorized, env.do(
		e2eDenyHandler("", "", "", "UNAUTHORIZED", "UNAUTHORIZED", http.StatusUnauthorized),
		e2eRequest(http.MethodGet, "/orders", "198.51.100.20:1234")).Code)

	frames := env.shutdown()
	require.Len(t, frames, 6, "decision, completion, 2 denial rows, 1 anonymous denial, 1 summary")

	kinds := map[string]int{}
	for _, f := range frames {
		kinds[e2eEffectiveType(f)]++
	}
	assert.Equal(t, 1, kinds[EventTypeDecision])
	assert.Equal(t, 1, kinds[EventTypeCompletion])
	assert.Equal(t, 4, kinds[EventTypeDenial])
	_, summaries := e2eSplit(frames)
	require.Len(t, summaries, 1)
	assert.Equal(t, 2, summaries[0].SuppressedCount)

	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	t.Cleanup(mock.Close)
	b := mock.ExpectBatch()
	for _, f := range frames {
		var suppressed any
		if f.SuppressedCount > 0 {
			suppressed = f.SuppressedCount
		}
		b.ExpectExec("INSERT INTO audit_events").
			WithArgs(auditArgsWith(map[int]any{1: e2eEffectiveType(f), 19: suppressed})...).
			WillReturnResult(pgxmock.NewResult("INSERT", 1))
	}

	worker, err := NewAuditWorker(mock, WorkerConfig{SpoolDir: env.dir(), BatchSize: 100, FlushInterval: 50 * time.Millisecond})
	require.NoError(t, err)
	n, err := worker.ProcessAvailable(context.Background())
	require.NoError(t, err)
	assert.Equal(t, len(frames), n)
	require.NoError(t, mock.ExpectationsWereMet())

	entries, err := os.ReadDir(env.dir())
	require.NoError(t, err)
	var segs []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "wal-") && strings.HasSuffix(e.Name(), ".log") {
			segs = append(segs, e.Name())
		}
	}
	sort.Strings(segs)
	require.NotEmpty(t, segs)
	last := segs[len(segs)-1]
	st, err := os.Stat(filepath.Join(env.dir(), last))
	require.NoError(t, err)
	cur, err := worker.Cursor().LoadOffset()
	require.NoError(t, err)
	assert.Equal(t, last, cur.SegmentFile)
	assert.Equal(t, st.Size(), cur.Offset, "cursor advanced to the end of the latest segment")
}

func TestE2EAllowedNotDelayedByStuckDisk(t *testing.T) {
	env := newE2E(t, e2eOptions{Committer: CommitterConfig{QueueSize: 8, MaxBatchFrames: 4}})
	release, entered := blockSync(env.spool)
	t.Cleanup(release)

	var sent int
	plain := e2eAllowHandler(env.spool, false, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	allowed := func() {
		t.Helper()
		start := time.Now()
		rr := env.do(plain, e2eRequest(http.MethodGet, "/orders", "203.0.113.9:4444"))
		elapsed := time.Since(start)
		sent++
		require.Equal(t, http.StatusOK, rr.Code, "an allowed response is never changed by a stuck disk")
		require.Less(t, elapsed, 50*time.Millisecond, "request %d was delayed by the blocked fsync", sent)
	}

	for i := 0; i < 5; i++ {
		allowed()
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("the committer never reached the blocked fsync")
	}

	for i := 0; i < 100; i++ {
		if faulted, _ := env.spool.WriteFaulted(); faulted {
			break
		}
		allowed()
	}
	faulted, reason := env.spool.WriteFaulted()
	require.True(t, faulted, "queue overflow must set the spool write fault")
	assert.Equal(t, dropQueueFull, reason)
	saturated, err := env.spool.CheckSaturation()
	require.NoError(t, err)
	assert.True(t, saturated)
	assert.Greater(t, env.rec.dropped("completion", "queue_full"), 0)
	assert.True(t, env.rec.degraded())

	// Hold the recovery just before it clears the fault so the fail-closed gate can be
	// observed deterministically once the disk is free again.
	hookEntered := make(chan struct{}, 1)
	hookGate := make(chan struct{})
	var gateOnce sync.Once
	openGate := func() { gateOnce.Do(func() { close(hookGate) }) }
	t.Cleanup(openGate)
	env.pipeline.com.setProbeHook(func() {
		select {
		case hookEntered <- struct{}{}:
		default:
		}
		<-hookGate
	})
	release()
	select {
	case <-hookEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("recovery did not start after the disk was unblocked")
	}

	// The disk works again but the fault is still set: new allowed requests fail closed.
	err = env.spool.AppendPreForward(sampleCompletionEvent("closed"))
	assert.ErrorIs(t, err, ErrSpoolSaturated)
	rr := env.do(e2eAllowHandler(env.spool, true, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), e2eRequest(http.MethodGet, "/orders", "203.0.113.9:4444"))
	sent++
	assert.Equal(t, http.StatusServiceUnavailable, rr.Code, "pre-forward append refused under the write fault")

	openGate()
	eventually(t, func() bool {
		f, _ := env.spool.WriteFaulted()
		return !f
	}, "the fault must clear without further requests")
	assert.NoError(t, env.spool.AppendPreForward(sampleCompletionEvent("reopened")))

	env.shutdown()
	assert.Equal(t, sent, env.rec.written("completion")+env.rec.dropped("completion", "queue_full"),
		"every allowed request is either written or counted as lost")
}

func TestE2EShutdownFlushesQueuedRecords(t *testing.T) {
	env := newE2E(t, e2eOptions{Committer: CommitterConfig{CompletionFlush: 10 * time.Second}})

	limited := e2eDenyHandler("user-1", "user", "orders.read", "RATE_LIMIT_EXCEEDED", "RATE_LIMIT_EXCEEDED", http.StatusTooManyRequests)
	for i := 0; i < 6; i++ {
		require.Equal(t, http.StatusTooManyRequests, env.do(limited, e2eRequest(http.MethodGet, "/orders", "203.0.113.9:4444")).Code)
	}
	allow := e2eAllowHandler(env.spool, false, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	for i := 0; i < 30; i++ {
		require.Equal(t, http.StatusOK, env.do(allow, e2eRequest(http.MethodGet, fmt.Sprintf("/orders/%d", i), "203.0.113.9:4444")).Code)
	}

	evs := env.shutdown()
	var completions int
	for _, e := range evs {
		if e.EventType == EventTypeCompletion {
			completions++
		}
	}
	rows, summaries := e2eSplit(evs)
	assert.Equal(t, 30, completions)
	assert.Len(t, rows, 1)
	require.Len(t, summaries, 1)
	assert.Equal(t, 5, summaries[0].SuppressedCount)

	// Requests after Shutdown still get their responses and never panic.
	assert.NotPanics(t, func() {
		assert.Equal(t, http.StatusOK, env.do(allow, e2eRequest(http.MethodGet, "/late", "203.0.113.9:4444")).Code)
		assert.Equal(t, http.StatusTooManyRequests, env.do(limited, e2eRequest(http.MethodGet, "/late", "203.0.113.9:4444")).Code)
	})
	assert.Equal(t, 1, env.rec.dropped("completion", "closed"))
	assert.Equal(t, 1, env.rec.dropped("denial", "closed"))
	assert.Len(t, readAllEvents(t, env.dir()), 32, "nothing is written after Shutdown")
}
