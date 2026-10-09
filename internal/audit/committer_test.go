package audit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- shared test helpers (plan 08-07 reuses these exact names) ----

// newTestSpool builds a spool in t.TempDir() with an explicit quota and a statfs that
// reports a fixed usage ratio, so no test depends on real disk usage.
func newTestSpool(t *testing.T, statfsRatio float64) *DiskSpool {
	t.Helper()
	s, err := NewDiskSpool(DiskSpoolConfig{
		SpoolDir:         t.TempDir(),
		VolumeQuotaBytes: 1 << 30,
		StatfsFunc: func(string) (uint64, uint64, error) {
			return uint64(statfsRatio * 1000), 1000, nil
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// ratioControl is a statfs ratio that tests can change while a spool is in use.
type ratioControl struct{ bits atomic.Uint64 }

// Set changes the usage ratio reported by statfs.
func (r *ratioControl) Set(ratio float64) { r.bits.Store(math.Float64bits(ratio)) }

func (r *ratioControl) statfs(string) (used, total uint64, err error) {
	ratio := math.Float64frombits(r.bits.Load())
	return uint64(ratio * 1000), 1000, nil
}

// newTestSpoolControlled is newTestSpool with a ratio the test can move.
func newTestSpoolControlled(t *testing.T, initialRatio float64) (*DiskSpool, *ratioControl) {
	t.Helper()
	rc := &ratioControl{}
	rc.Set(initialRatio)
	s, err := NewDiskSpool(DiskSpoolConfig{
		SpoolDir:         t.TempDir(),
		VolumeQuotaBytes: 1 << 30,
		StatfsFunc:       rc.statfs,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s, rc
}

// readAllEvents decodes every top-level wal-*.log segment in dir in name order.
func readAllEvents(t *testing.T, dir string) []*CompletionEvent {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "wal-") && strings.HasSuffix(e.Name(), ".log") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	var out []*CompletionEvent
	for _, n := range names {
		f, err := os.Open(filepath.Join(dir, n))
		require.NoError(t, err)
		for {
			ev, _, err := ReadFramedRecord(f)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				_ = f.Close()
				require.NoError(t, err, "segment %s holds a corrupt frame", n)
			}
			out = append(out, ev)
		}
		_ = f.Close()
	}
	return out
}

// fakeRecorder is a mutex-guarded Recorder.
type fakeRecorder struct {
	mu          sync.Mutex
	writtenN    map[string]int
	droppedN    map[string]int
	suppressN   map[string]int
	overflowN   int
	flushN      int
	degradedNow bool
	degradedEv  bool
	degFalseN   int
	queueDepth  int
}

func newFakeRecorder() *fakeRecorder {
	return &fakeRecorder{
		writtenN:  map[string]int{},
		droppedN:  map[string]int{},
		suppressN: map[string]int{},
	}
}

func (f *fakeRecorder) RecordAuditWritten(kind string, n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writtenN[kind] += n
}

func (f *fakeRecorder) RecordAuditDropped(kind, reason string, n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.droppedN[kind+"/"+reason] += n
}

func (f *fakeRecorder) RecordAuditSuppressed(label string, n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.suppressN[label] += n
}

func (f *fakeRecorder) RecordAuditSuppressorOverflow() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.overflowN++
}

func (f *fakeRecorder) ObserveAuditFlush(time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.flushN++
}

func (f *fakeRecorder) SetAuditDegraded(d bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.degradedNow = d
	if d {
		f.degradedEv = true
	} else {
		f.degFalseN++
	}
}

func (f *fakeRecorder) SetAuditQueueDepth(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queueDepth = n
}

func (f *fakeRecorder) written(kind string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.writtenN[kind]
}

func (f *fakeRecorder) dropped(kind, reason string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.droppedN[kind+"/"+reason]
}

func (f *fakeRecorder) suppressed(label string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.suppressN[label]
}

func (f *fakeRecorder) overflows() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.overflowN
}

func (f *fakeRecorder) flushes() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.flushN
}

func (f *fakeRecorder) degraded() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.degradedNow
}

func (f *fakeRecorder) everDegraded() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.degradedEv
}

func (f *fakeRecorder) lastQueueDepth() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.queueDepth
}

func (f *fakeRecorder) degradedFalseCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.degFalseN
}

// ---- committer-local helpers ----

// startCommitter starts a committer and registers a bounded Shutdown cleanup.
func startCommitter(t *testing.T, spool *DiskSpool, rec Recorder, cfg CommitterConfig) *Committer {
	t.Helper()
	c := NewCommitter(spool, rec, cfg)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = c.Shutdown(ctx)
	})
	return c
}

// blockSync makes the spool fsync block until release is called. It returns release
// and a channel that is signalled the first time a sync blocks.
func blockSync(spool *DiskSpool) (release func(), entered <-chan struct{}) {
	gate := make(chan struct{})
	in := make(chan struct{}, 1)
	var once sync.Once
	spool.syncFn = func(f *os.File) error {
		select {
		case in <- struct{}{}:
		default:
		}
		<-gate
		return f.Sync()
	}
	return func() { once.Do(func() { close(gate) }) }, in
}

func routeIDs(evs []*CompletionEvent) map[string]int {
	m := map[string]int{}
	for _, e := range evs {
		m[e.RouteID]++
	}
	return m
}

func eventually(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	require.Eventually(t, cond, 2*time.Second, 10*time.Millisecond, msg)
}

func denialEvent(route string) *CompletionEvent {
	ev := sampleCompletionEvent(route)
	ev.Decision = "deny"
	ev.ReasonCode = "POLICY_DENIED"
	ev.HTTPStatus = 403
	ev.EventType = EventTypeDenial
	return ev
}

// ---- Task 1 tests ----

func TestCommitterGroupFlush(t *testing.T) {
	spool := newTestSpool(t, 0.10)
	var syncs atomic.Int64
	spool.syncFn = func(f *os.File) error {
		syncs.Add(1)
		return f.Sync()
	}
	rec := newFakeRecorder()
	c := startCommitter(t, spool, rec, CommitterConfig{GroupFlush: 100 * time.Millisecond})

	const n = 50
	start := make(chan struct{})
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs <- c.SubmitDenial(denialEvent(fmt.Sprintf("r%d", i)))
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	assert.Len(t, readAllEvents(t, spool.cfg.SpoolDir), n)
	assert.GreaterOrEqual(t, syncs.Load(), int64(1))
	assert.Less(t, syncs.Load(), int64(n), "fsyncs must be shared across concurrent denials")
	assert.Equal(t, n, rec.written("denial"))
	assert.Greater(t, rec.flushes(), 0)
}

func TestCommitterDurableBeforeReturn(t *testing.T) {
	spool := newTestSpool(t, 0.10)
	c := startCommitter(t, spool, newFakeRecorder(), CommitterConfig{GroupFlush: 2 * time.Millisecond})

	begin := time.Now()
	require.NoError(t, c.SubmitDenial(denialEvent("durable")))
	assert.Less(t, time.Since(begin), 250*time.Millisecond)

	// Read straight from disk: the frame must already be there.
	evs := readAllEvents(t, spool.cfg.SpoolDir)
	require.Len(t, evs, 1)
	assert.Equal(t, "durable", evs[0].RouteID)
}

func TestCommitterIgnoresCancellation(t *testing.T) {
	spool := newTestSpool(t, 0.10)
	c := startCommitter(t, spool, newFakeRecorder(), CommitterConfig{})

	// The signature has no context parameter: request cancellation cannot skip durability.
	var submit func(*CompletionEvent) error = c.SubmitDenial
	require.NoError(t, submit(denialEvent("cancel-proof")))
	assert.Len(t, readAllEvents(t, spool.cfg.SpoolDir), 1)
}

func TestCommitterCompletionLingerAndShortening(t *testing.T) {
	spool := newTestSpool(t, 0.10)
	rec := newFakeRecorder()
	c := startCommitter(t, spool, rec, CommitterConfig{GroupFlush: 2 * time.Millisecond, CompletionFlush: 5 * time.Second})

	for i := 0; i < 3; i++ {
		c.EnqueueCompletion(sampleCompletionEvent(fmt.Sprintf("c%d", i)))
	}
	require.Never(t, func() bool {
		return len(readAllEvents(t, spool.cfg.SpoolDir)) > 0
	}, 100*time.Millisecond, 10*time.Millisecond, "completions must linger")

	begin := time.Now()
	require.NoError(t, c.SubmitDenial(denialEvent("d")))
	assert.Less(t, time.Since(begin), 500*time.Millisecond)

	got := routeIDs(readAllEvents(t, spool.cfg.SpoolDir))
	assert.Equal(t, map[string]int{"c0": 1, "c1": 1, "c2": 1, "d": 1}, got)
	assert.Equal(t, 3, rec.written("completion"))
	assert.Equal(t, 1, rec.written("denial"))
}

func TestCommitterShutdown(t *testing.T) {
	spool := newTestSpool(t, 0.10)
	rec := newFakeRecorder()
	c := NewCommitter(spool, rec, CommitterConfig{CompletionFlush: 10 * time.Second})

	for i := 0; i < 100; i++ {
		c.EnqueueCompletion(sampleCompletionEvent(fmt.Sprintf("c%d", i)))
	}
	for i := 0; i < 3; i++ {
		s := sampleCompletionEvent(fmt.Sprintf("s%d", i))
		s.EventType = EventTypeDenial
		s.SuppressedCount = 5
		c.EnqueueSummary(s)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, c.Shutdown(ctx))
	assert.Len(t, readAllEvents(t, spool.cfg.SpoolDir), 103)
	assert.Equal(t, 100, rec.written("completion"))
	assert.Equal(t, 3, rec.written("denial"))

	require.NoError(t, c.Shutdown(ctx), "second Shutdown is a no-op")

	assert.ErrorIs(t, c.SubmitDenial(denialEvent("late")), ErrCommitterClosed)
	c.EnqueueCompletion(sampleCompletionEvent("late-c"))
	assert.Equal(t, 1, rec.dropped("completion", "closed"))
	faulted, _ := spool.WriteFaulted()
	assert.False(t, faulted, "a closed-committer drop must not set the write fault")
	assert.Len(t, readAllEvents(t, spool.cfg.SpoolDir), 103)
}

func TestCommitterShutdownCanceledContext(t *testing.T) {
	spool := newTestSpool(t, 0.10)
	release, entered := blockSync(spool)
	defer release()
	c := startCommitter(t, spool, newFakeRecorder(), CommitterConfig{WaiterTimeout: 5 * time.Second})

	go func() { _ = c.SubmitDenial(denialEvent("stuck")) }()
	<-entered

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 1)
	go func() { done <- c.Shutdown(ctx) }()
	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown with a canceled context hung")
	}
}

func TestCommitterNormalizes(t *testing.T) {
	spool := newTestSpool(t, 0.10)
	c := startCommitter(t, spool, newFakeRecorder(), CommitterConfig{CompletionFlush: 5 * time.Millisecond})

	mk := func(route string) *CompletionEvent {
		ev := sampleCompletionEvent(route)
		ev.CanonicalPath = "/a\x00b/c"
		ev.HTTPMethod = strings.Repeat("M", 28)
		return ev
	}
	require.NoError(t, c.SubmitDenial(mk("nd")))
	c.EnqueueCompletion(mk("nc"))
	eventually(t, func() bool { return len(readAllEvents(t, spool.cfg.SpoolDir)) == 2 }, "both frames written")

	for _, ev := range readAllEvents(t, spool.cfg.SpoolDir) {
		assert.Equal(t, "/ab/c", ev.CanonicalPath)
		assert.Equal(t, strings.Repeat("M", 16), ev.HTTPMethod)
	}
}

func TestCommitterWaiterTimeout(t *testing.T) {
	spool := newTestSpool(t, 0.10)
	release, _ := blockSync(spool)
	defer release()
	rec := newFakeRecorder()
	c := startCommitter(t, spool, rec, CommitterConfig{WaiterTimeout: 100 * time.Millisecond})

	err := c.SubmitDenial(denialEvent("slow"))
	require.ErrorIs(t, err, ErrDenialTimeout)
	assert.Equal(t, 1, rec.dropped("denial", "timeout"))

	// The committer later completes the abandoned waiter; that must not block or panic.
	release()
	eventually(t, func() bool { return rec.written("denial") == 1 }, "abandoned frame still flushed")
	require.NoError(t, c.SubmitDenial(denialEvent("after")))
}

// ---- Task 2 tests ----

func TestCommitterQueueFullSetsFault(t *testing.T) {
	spool := newTestSpool(t, 0.10)
	release, entered := blockSync(spool)
	defer release()
	rec := newFakeRecorder()
	c := startCommitter(t, spool, rec, CommitterConfig{
		QueueSize:        64,
		MaxBatchFrames:   4,
		RecoveryInterval: 20 * time.Millisecond,
	})

	// Fill one batch so the committer blocks inside fsync.
	for i := 0; i < 4; i++ {
		c.EnqueueCompletion(sampleCompletionEvent(fmt.Sprintf("pre%d", i)))
	}
	<-entered

	// 64 queue slots plus 10 extra: at least 10 must be refused, none may block.
	for i := 0; i < 74; i++ {
		begin := time.Now()
		c.EnqueueCompletion(sampleCompletionEvent(fmt.Sprintf("q%d", i)))
		require.Less(t, time.Since(begin), 50*time.Millisecond, "EnqueueCompletion must never block")
	}
	assert.GreaterOrEqual(t, rec.dropped("completion", "queue_full"), 10)
	faulted, reason := spool.WriteFaulted()
	assert.True(t, faulted)
	assert.Equal(t, "queue_full", reason)
	saturated, err := spool.CheckSaturation()
	require.NoError(t, err)
	assert.True(t, saturated)
	assert.True(t, rec.everDegraded())

	// Recovery needs no new traffic.
	release()
	eventually(t, func() bool { f, _ := spool.WriteFaulted(); return !f }, "fault clears without traffic")
	eventually(t, func() bool { return !rec.degraded() }, "degraded gauge reset")
	assert.NoError(t, spool.AppendPreForward(sampleCompletionEvent("allowed-again")))
}

func TestCommitterHardLimitBand(t *testing.T) {
	spool, rc := newTestSpoolControlled(t, 0.92)
	rec := newFakeRecorder()
	c := startCommitter(t, spool, rec, CommitterConfig{
		CompletionFlush:  10 * time.Millisecond,
		RecoveryInterval: 20 * time.Millisecond,
	})
	dir := spool.cfg.SpoolDir

	// 0.92: between the admission gate and the hard limit, audit records still write.
	assert.ErrorIs(t, spool.AppendPreForward(sampleCompletionEvent("allowed")), ErrSpoolSaturated)
	require.NoError(t, c.SubmitDenial(denialEvent("band-d")))
	c.EnqueueCompletion(sampleCompletionEvent("band-c"))
	eventually(t, func() bool { return len(readAllEvents(t, dir)) == 2 }, "headroom writes")

	// 0.96: past the hard limit, frames are dropped and counted.
	rc.Set(0.96)
	err := c.SubmitDenial(denialEvent("over-d"))
	require.ErrorIs(t, err, ErrHardLimit)
	assert.Equal(t, 1, rec.dropped("denial", "hard_limit"))
	faulted, _ := spool.WriteFaulted()
	assert.False(t, faulted, "a denial drop alone must not set the fault")

	c.EnqueueCompletion(sampleCompletionEvent("over-c"))
	eventually(t, func() bool { f, r := spool.WriteFaulted(); return f && r == "hard_limit" }, "completion drop sets the fault")
	assert.Equal(t, 1, rec.dropped("completion", "hard_limit"))
	assert.Len(t, readAllEvents(t, dir), 2)

	// Back to 0.50: the fault clears with no traffic and completions write again.
	rc.Set(0.50)
	eventually(t, func() bool { f, _ := spool.WriteFaulted(); return !f }, "fault clears")
	c.EnqueueCompletion(sampleCompletionEvent("back-c"))
	eventually(t, func() bool { return len(readAllEvents(t, dir)) == 3 }, "completions write again")
}

func TestCommitterWriteErrorRetry(t *testing.T) {
	spool := newTestSpool(t, 0.10)
	var calls atomic.Int64
	spool.writeFn = func(f *os.File, b []byte) (int, error) {
		if calls.Add(1) <= 3 {
			return 0, errors.New("injected disk error")
		}
		return f.Write(b)
	}
	rec := newFakeRecorder()
	c := startCommitter(t, spool, rec, CommitterConfig{
		CompletionFlush:  500 * time.Millisecond,
		RecoveryInterval: 20 * time.Millisecond,
	})

	c.EnqueueCompletion(sampleCompletionEvent("c1"))
	c.EnqueueCompletion(sampleCompletionEvent("c2"))
	s := sampleCompletionEvent("s1")
	s.EventType = EventTypeDenial
	c.EnqueueSummary(s)
	err := c.SubmitDenial(denialEvent("d1"))
	require.Error(t, err, "the denial waiter in a failed batch gets the error")
	assert.Equal(t, 1, rec.dropped("denial", "write_error"))

	// The fault is raised right after the waiter is released, so wait for it.
	eventually(t, func() bool { f, r := spool.WriteFaulted(); return f && r == "write_error" }, "write_error fault during outage")

	require.Eventually(t, func() bool { return len(readAllEvents(t, spool.cfg.SpoolDir)) == 3 },
		5*time.Second, 10*time.Millisecond, "retained frames written after recovery")
	assert.Equal(t, map[string]int{"c1": 1, "c2": 1, "s1": 1}, routeIDs(readAllEvents(t, spool.cfg.SpoolDir)))
	eventually(t, func() bool { f, _ := spool.WriteFaulted(); return !f }, "retry success clears the fault")
	assert.Equal(t, 0, rec.dropped("completion", "write_error"))
	assert.Equal(t, int64(4), calls.Load())
}

func TestCommitterDenialDropsDoNotFault(t *testing.T) {
	spool := newTestSpool(t, 0.96)
	rec := newFakeRecorder()
	c := startCommitter(t, spool, rec, CommitterConfig{RecoveryInterval: 20 * time.Millisecond})

	for i := 0; i < 100; i++ {
		require.ErrorIs(t, c.SubmitDenial(denialEvent(fmt.Sprintf("d%d", i))), ErrHardLimit)
	}
	assert.Equal(t, 100, rec.dropped("denial", "hard_limit"))
	faulted, _ := spool.WriteFaulted()
	assert.False(t, faulted)
	assert.False(t, rec.everDegraded())
}

func TestCommitterFaultGenerationRace(t *testing.T) {
	t.Run("probe", func(t *testing.T) {
		spool := newTestSpool(t, 0.10)
		rec := newFakeRecorder()
		// A very long tick keeps the loop out of the way; the probe is driven directly.
		c := startCommitter(t, spool, rec, CommitterConfig{RecoveryInterval: time.Hour})

		c.setFault("queue_full")
		require.True(t, rec.degraded())

		var hookRuns atomic.Int64
		c.setProbeHook(func() {
			hookRuns.Add(1)
			c.setProbeHook(nil)
			c.setFault("hard_limit") // a newer fault lands mid-probe
		})
		assert.False(t, c.recoveryProbe(), "a fault raised mid-probe must survive")
		assert.Equal(t, int64(1), hookRuns.Load())
		faulted, reason := spool.WriteFaulted()
		assert.True(t, faulted)
		assert.Equal(t, "hard_limit", reason)
		assert.True(t, rec.degraded())
		assert.Equal(t, 0, rec.degradedFalseCalls(), "SetAuditDegraded(false) must not have been called")

		// A stale generation can never clear.
		stale := c.currentFaultGen()
		c.setFault("queue_full")
		assert.False(t, c.clearFaultIfGen(stale))
		faulted, _ = spool.WriteFaulted()
		assert.True(t, faulted)

		// With the hook gone the next probe clears.
		assert.True(t, c.recoveryProbe())
		faulted, _ = spool.WriteFaulted()
		assert.False(t, faulted)
		assert.Equal(t, 1, rec.degradedFalseCalls())
	})

	t.Run("ticker", func(t *testing.T) {
		spool := newTestSpool(t, 0.10)
		rec := newFakeRecorder()
		c := startCommitter(t, spool, rec, CommitterConfig{RecoveryInterval: 20 * time.Millisecond})

		var once sync.Once
		var hookRan atomic.Bool
		c.setFault("queue_full")
		c.setProbeHook(func() {
			once.Do(func() {
				hookRan.Store(true)
				c.setFault("hard_limit")
			})
		})
		eventually(t, hookRan.Load, "first tick ran the hook")
		// The fault raised mid-probe is not cleared by that tick but by a later one.
		eventually(t, func() bool { f, _ := spool.WriteFaulted(); return !f }, "next tick clears")
		assert.Equal(t, 1, rec.degradedFalseCalls())
	})
}

func TestCommitterShutdownDuringRetry(t *testing.T) {
	spool := newTestSpool(t, 0.10)
	spool.writeFn = func(*os.File, []byte) (int, error) { return 0, errors.New("injected disk error") }
	rec := newFakeRecorder()
	c := NewCommitter(spool, rec, CommitterConfig{CompletionFlush: time.Millisecond})

	c.EnqueueCompletion(sampleCompletionEvent("stuck"))
	eventually(t, func() bool { f, r := spool.WriteFaulted(); return f && r == "write_error" }, "write_error fault")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	begin := time.Now()
	require.NoError(t, c.Shutdown(ctx))
	assert.Less(t, time.Since(begin), 2*time.Second)
	assert.GreaterOrEqual(t, rec.dropped("completion", "write_error"), 1)
}
