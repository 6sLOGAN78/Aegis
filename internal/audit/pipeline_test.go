package audit

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// e2eClock is a settable clock injected into the pipeline (shared with the e2e tests).
type e2eClock struct {
	mu sync.Mutex
	t  time.Time
}

func newE2EClock(t time.Time) *e2eClock { return &e2eClock{t: t} }

func (c *e2eClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *e2eClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

var pipelineT0 = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// pipelineFast is a committer config with short intervals for tests.
func pipelineFast() CommitterConfig {
	return CommitterConfig{
		GroupFlush:       2 * time.Millisecond,
		CompletionFlush:  5 * time.Millisecond,
		RecoveryInterval: 20 * time.Millisecond,
	}
}

// pipelineStart builds a pipeline over the spool with an injected clock and a bounded
// Shutdown cleanup.
func pipelineStart(t *testing.T, spool *DiskSpool, rec Recorder, cfg PipelineConfig, clk *e2eClock) *Pipeline {
	t.Helper()
	p := newPipelineClock(spool, rec, cfg, clk.Now)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = p.Shutdown(ctx)
	})
	return p
}

func pipelineDenial(principal, kind, route, reason string, status int) *CompletionEvent {
	ev := sampleCompletionEvent(route)
	ev.PrincipalID = principal
	ev.PrincipalKind = kind
	ev.Decision = "deny"
	ev.ReasonCode = reason
	ev.HTTPStatus = status
	ev.EventType = EventTypeDenial
	return ev
}

func pipelineShutdown(t *testing.T, p *Pipeline) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, p.Shutdown(ctx))
}

func TestPipelineRecordDenial(t *testing.T) {
	spool := newTestSpool(t, 0.10)
	rec := newFakeRecorder()
	clk := newE2EClock(pipelineT0)
	p := pipelineStart(t, spool, rec, PipelineConfig{
		Governor:  GovernorConfig{SuppressWindow: 60 * time.Second},
		Committer: pipelineFast(),
	}, clk)

	p.RecordDenial(pipelineDenial("u1", "user", "r1", "RATE_LIMIT_EXCEEDED", 429))
	require.Len(t, readAllEvents(t, spool.cfg.SpoolDir), 1, "first identified denial is durable on return")

	for i := 0; i < 9; i++ {
		p.RecordDenial(pipelineDenial("u1", "user", "r1", "RATE_LIMIT_EXCEEDED", 429))
	}
	assert.Len(t, readAllEvents(t, spool.cfg.SpoolDir), 1, "repeats are not written")
	assert.Equal(t, 9, rec.suppressed("RATE_LIMIT_EXCEEDED"))

	p.RecordDenial(pipelineDenial("u1", "user", "r2", "RATE_LIMIT_EXCEEDED", 429))
	evs := readAllEvents(t, spool.cfg.SpoolDir)
	require.Len(t, evs, 2)
	assert.Equal(t, map[string]int{"r1": 1, "r2": 1}, routeIDs(evs))
	assert.Equal(t, 2, rec.written("denial"))
}

func TestPipelineUnauthCap(t *testing.T) {
	spool := newTestSpool(t, 0.10)
	rec := newFakeRecorder()
	clk := newE2EClock(pipelineT0)
	p := pipelineStart(t, spool, rec, PipelineConfig{
		Governor:  GovernorConfig{UnauthRate: 10, UnauthBurst: 3},
		Committer: pipelineFast(),
	}, clk)

	for i := 0; i < 3; i++ {
		p.RecordDenial(pipelineDenial("anonymous", "anonymous", "", "UNAUTHORIZED", 401))
	}
	require.Len(t, readAllEvents(t, spool.cfg.SpoolDir), 3)
	assert.Equal(t, 0, rec.dropped("denial", "unauth_cap"))

	p.RecordDenial(pipelineDenial("anonymous", "anonymous", "", "UNAUTHORIZED", 401))
	assert.Equal(t, 1, rec.dropped("denial", "unauth_cap"))
	assert.Len(t, readAllEvents(t, spool.cfg.SpoolDir), 3, "the capped denial is not written")
}

func TestPipelineSuppressorOverflow(t *testing.T) {
	spool := newTestSpool(t, 0.10)
	rec := newFakeRecorder()
	clk := newE2EClock(pipelineT0)
	window := time.Hour
	p := pipelineStart(t, spool, rec, PipelineConfig{
		Governor:  GovernorConfig{SuppressWindow: window, MaxKeys: 2},
		Committer: pipelineFast(),
	}, clk)

	p.RecordDenial(pipelineDenial("f1", "user", "r1", "DENIED_DEFAULT", 403))
	p.RecordDenial(pipelineDenial("f2", "user", "r1", "DENIED_DEFAULT", 403))
	require.Len(t, readAllEvents(t, spool.cfg.SpoolDir), 2)
	require.Equal(t, 0, rec.overflows())

	for i := 0; i < 20; i++ {
		p.RecordDenial(pipelineDenial(fmt.Sprintf("flood%d", i), "user", "r1", "RATE_LIMIT_EXCEEDED", 429))
	}
	assert.Len(t, readAllEvents(t, spool.cfg.SpoolDir), 3, "exactly one extra row: the overflow bucket representative")
	assert.Equal(t, 19, rec.suppressed("RATE_LIMIT_EXCEEDED"))
	assert.Equal(t, 20, rec.overflows())

	p.sweepOnce(pipelineT0.Add(window))
	eventually(t, func() bool { return len(readAllEvents(t, spool.cfg.SpoolDir)) == 4 }, "summary row")
	var summary *CompletionEvent
	for _, e := range readAllEvents(t, spool.cfg.SpoolDir) {
		if e.SuppressedCount > 0 {
			summary = e
		}
	}
	require.NotNil(t, summary)
	assert.Equal(t, 19, summary.SuppressedCount)
	assert.Equal(t, EventTypeDenial, summary.EventType)
}

func TestPipelineSweeper(t *testing.T) {
	spool := newTestSpool(t, 0.10)
	rec := newFakeRecorder()
	clk := newE2EClock(pipelineT0)
	window := 60 * time.Second
	p := pipelineStart(t, spool, rec, PipelineConfig{
		Governor:  GovernorConfig{SuppressWindow: window},
		Committer: pipelineFast(),
	}, clk)

	first := pipelineDenial("u1", "user", "r1", "RATE_LIMIT_EXCEEDED", 429)
	p.RecordDenial(first)
	for i := 0; i < 5; i++ {
		p.RecordDenial(pipelineDenial("u1", "user", "r1", "RATE_LIMIT_EXCEEDED", 429))
	}
	require.Equal(t, 5, rec.suppressed("RATE_LIMIT_EXCEEDED"))

	p.sweepOnce(pipelineT0.Add(window - time.Nanosecond))
	assert.Equal(t, 1, p.gov.keyCount(), "an open window is not swept")
	time.Sleep(30 * time.Millisecond)
	assert.Len(t, readAllEvents(t, spool.cfg.SpoolDir), 1, "nothing enqueued before the window closes")

	p.sweepOnce(pipelineT0.Add(window))
	assert.Equal(t, 0, p.gov.keyCount())
	eventually(t, func() bool { return len(readAllEvents(t, spool.cfg.SpoolDir)) == 2 }, "summary row on disk")

	evs := readAllEvents(t, spool.cfg.SpoolDir)
	var summary *CompletionEvent
	for _, e := range evs {
		if e.SuppressedCount > 0 {
			summary = e
		}
	}
	require.NotNil(t, summary)
	assert.Equal(t, EventTypeDenial, summary.EventType)
	assert.Equal(t, 5, summary.SuppressedCount)
	assert.Equal(t, first.RequestID, summary.RequestID)
	assert.NotEqual(t, first.EventID, summary.EventID)
}

func TestPipelineShutdown(t *testing.T) {
	spool := newTestSpool(t, 0.10)
	rec := newFakeRecorder()
	clk := newE2EClock(pipelineT0)
	cfg := pipelineFast()
	cfg.CompletionFlush = 10 * time.Second
	p := pipelineStart(t, spool, rec, PipelineConfig{
		Governor:  GovernorConfig{SuppressWindow: 60 * time.Second},
		Committer: cfg,
	}, clk)

	p.RecordDenial(pipelineDenial("u1", "user", "r1", "RATE_LIMIT_EXCEEDED", 429))
	for i := 0; i < 4; i++ {
		p.RecordDenial(pipelineDenial("u1", "user", "r1", "RATE_LIMIT_EXCEEDED", 429))
	}
	for i := 0; i < 20; i++ {
		ev := sampleCompletionEvent(fmt.Sprintf("c%d", i))
		ev.EventType = EventTypeCompletion
		p.EnqueueCompletion(ev)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, p.Shutdown(ctx))

	evs := readAllEvents(t, spool.cfg.SpoolDir)
	var completions, denials, summaries int
	for _, e := range evs {
		switch {
		case e.SuppressedCount > 0:
			summaries++
			assert.Equal(t, 4, e.SuppressedCount)
		case e.EventType == EventTypeCompletion:
			completions++
		case e.EventType == EventTypeDenial:
			denials++
		}
	}
	assert.Equal(t, 20, completions)
	assert.Equal(t, 1, denials)
	assert.Equal(t, 1, summaries)

	select {
	case <-p.sweepDone:
	default:
		t.Fatal("sweeper goroutine still running after Shutdown")
	}

	assert.NotPanics(t, func() {
		p.RecordDenial(pipelineDenial("u2", "user", "r9", "RATE_LIMIT_EXCEEDED", 429))
		p.EnqueueCompletion(sampleCompletionEvent("late"))
	})
	assert.Equal(t, 1, rec.dropped("denial", "closed"))
	assert.Equal(t, 1, rec.dropped("completion", "closed"))
	assert.NoError(t, p.Shutdown(ctx), "second Shutdown is a no-op")
	assert.Len(t, readAllEvents(t, spool.cfg.SpoolDir), 22)
}

func TestPipelineEnqueueCompletionPassThrough(t *testing.T) {
	spool := newTestSpool(t, 0.10)
	rec := newFakeRecorder()
	clk := newE2EClock(pipelineT0)
	cfg := pipelineFast()
	cfg.CompletionFlush = 10 * time.Second
	cfg.QueueSize = 2048
	p := pipelineStart(t, spool, rec, PipelineConfig{Committer: cfg}, clk)

	ev := sampleCompletionEvent("same")
	ev.EventType = EventTypeCompletion
	for i := 0; i < 1000; i++ {
		p.EnqueueCompletion(ev)
	}
	assert.Equal(t, 0, rec.dropped("completion", "queue_full"))
	faulted, _ := spool.WriteFaulted()
	assert.False(t, faulted)
	assert.Equal(t, 0, rec.suppressed("OTHER"), "completions never reach the governor")

	pipelineShutdown(t, p)
	assert.Len(t, readAllEvents(t, spool.cfg.SpoolDir), 1000)
	assert.Equal(t, 1000, rec.written("completion"))
}

func TestPipelineNilEvents(t *testing.T) {
	spool := newTestSpool(t, 0.10)
	p := pipelineStart(t, spool, newFakeRecorder(), PipelineConfig{Committer: pipelineFast()}, newE2EClock(pipelineT0))
	assert.NotPanics(t, func() {
		p.RecordDenial(nil)
		p.EnqueueCompletion(nil)
	})
	assert.Equal(t, 0, p.QueueDepth())
}
