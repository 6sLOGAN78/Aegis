package audit

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// summaryRig builds a pipeline whose completion queue (size 2) can be held full by
// blocking the committer's fsync, with one closed window of 3 suppressed repeats.
type summaryRig struct {
	spool   *DiskSpool
	rec     *fakeRecorder
	p       *Pipeline
	release func()
	first   *CompletionEvent
	window  time.Duration
}

func newSummaryRig(t *testing.T, extraRoutes ...string) *summaryRig {
	t.Helper()
	spool := newTestSpool(t, 0.10)
	rec := newFakeRecorder()
	clk := newE2EClock(pipelineT0)
	window := 60 * time.Second
	cc := pipelineFast()
	cc.QueueSize = 2
	p := pipelineStart(t, spool, rec, PipelineConfig{
		Governor:  GovernorConfig{SuppressWindow: window},
		Committer: cc,
	}, clk)

	first := pipelineDenial("u1", "user", "r1", "RATE_LIMIT_EXCEEDED", 429)
	p.RecordDenial(first)
	for i := 0; i < 3; i++ {
		p.RecordDenial(pipelineDenial("u1", "user", "r1", "RATE_LIMIT_EXCEEDED", 429))
	}
	require.Equal(t, 3, rec.suppressed("RATE_LIMIT_EXCEEDED"))
	// Further closed windows, each with one suppressed repeat (recorded before fsync blocks).
	for _, route := range extraRoutes {
		p.RecordDenial(pipelineDenial("u1", "user", route, "RATE_LIMIT_EXCEEDED", 429))
		p.RecordDenial(pipelineDenial("u1", "user", route, "RATE_LIMIT_EXCEEDED", 429))
	}

	release, entered := blockSync(spool)
	t.Cleanup(release)
	p.EnqueueCompletion(sampleCompletionEvent("held")) // the committer takes it and blocks in fsync
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("the committer never reached the blocked fsync")
	}
	p.EnqueueCompletion(sampleCompletionEvent("fill-1"))
	p.EnqueueCompletion(sampleCompletionEvent("fill-2"))
	require.Equal(t, 2, p.QueueDepth(), "the completion queue must be full")
	return &summaryRig{spool: spool, rec: rec, p: p, release: release, first: first, window: window}
}

func summariesOnDisk(t *testing.T, dir string) []*CompletionEvent {
	t.Helper()
	var out []*CompletionEvent
	for _, e := range readAllEvents(t, dir) {
		if e.SuppressedCount > 0 {
			out = append(out, e)
		}
	}
	return out
}

// WR-02: a summary that does not fit in the queue must be carried to the next sweep.
func TestPipelineSummaryCarriedWhenQueueFull(t *testing.T) {
	r := newSummaryRig(t)
	dropsBefore := r.rec.dropped("denial", "queue_full")

	r.p.sweepOnce(pipelineT0.Add(r.window)) // queue is full: the summary cannot be enqueued
	assert.Equal(t, dropsBefore, r.rec.dropped("denial", "queue_full"), "a deferred summary is not a loss")

	r.release()
	eventually(t, func() bool { return r.p.QueueDepth() == 0 }, "queue drains")
	r.p.sweepOnce(pipelineT0.Add(r.window + 5*time.Second))
	eventually(t, func() bool { return len(summariesOnDisk(t, r.spool.cfg.SpoolDir)) == 1 }, "the carried summary must reach the spool")

	sum := summariesOnDisk(t, r.spool.cfg.SpoolDir)[0]
	assert.Equal(t, 3, sum.SuppressedCount)
	assert.Equal(t, r.first.RequestID, sum.RequestID)
}

// The carry is bounded; what does not fit is counted as dropped, never silently lost.
func TestPipelineSummaryCarryBoundedAndCounted(t *testing.T) {
	r := newSummaryRig(t, "r2", "r3")
	r.p.sumMu.Lock()
	r.p.maxCarry = 1
	r.p.sumMu.Unlock()
	dropsBefore := r.rec.dropped("denial", "queue_full")
	r.p.sweepOnce(pipelineT0.Add(r.window)) // three summaries, queue full, carry cap 1

	assert.Len(t, r.p.carry, 1)
	assert.Equal(t, dropsBefore+2, r.rec.dropped("denial", "queue_full"), "the two summaries over the cap are counted")
}

// The shutdown flush waits for queue room instead of dropping the summary.
func TestPipelineShutdownWaitsForQueueRoomForSummaries(t *testing.T) {
	r := newSummaryRig(t)

	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		done <- r.p.Shutdown(ctx)
	}()
	eventually(t, func() bool { return r.p.closed.Load() }, "shutdown started")
	time.Sleep(20 * time.Millisecond) // let it hit the full queue at least once
	r.release()

	require.NoError(t, <-done)
	sums := summariesOnDisk(t, r.spool.cfg.SpoolDir)
	require.Len(t, sums, 1, "the shutdown summary must not be dropped when the queue is momentarily full")
	assert.Equal(t, 3, sums[0].SuppressedCount)
	assert.Equal(t, 0, r.rec.dropped("denial", "queue_full"))
}

// When the shutdown deadline passes first, the unsent summaries are counted.
func TestPipelineShutdownCountsSummariesLostToDeadline(t *testing.T) {
	r := newSummaryRig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_ = r.p.Shutdown(ctx)
	assert.Equal(t, 1, r.rec.dropped("denial", "queue_full"))
}
