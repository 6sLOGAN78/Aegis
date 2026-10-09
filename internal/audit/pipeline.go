package audit

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// PipelineConfig tunes the governor and the committer that make up a Pipeline.
type PipelineConfig struct {
	Governor  GovernorConfig
	Committer CommitterConfig
}

// Pipeline assembles the audit write path behind the middleware Sink seam: the
// governor decides what is written, the committer writes it durably, and a sweeper
// turns closed suppression windows into summary rows (D-04..D-08, D-10, D-13..D-15).
type Pipeline struct {
	spool *DiskSpool
	rec   Recorder
	gov   *Governor
	com   *Committer

	// now is the clock used for governor decisions and sweeping. Tests replace it
	// through newPipelineClock before the sweeper starts.
	now func() time.Time

	// sumMu guards carry and maxCarry: closed-window summaries the committer's bounded
	// queue had no room for (WR-02).
	sumMu    sync.Mutex
	carry    []*CompletionEvent
	maxCarry int

	sweepEvery   time.Duration
	sweepQuit    chan struct{}
	sweepDone    chan struct{}
	closed       atomic.Bool
	shutdownOnce sync.Once
}

var _ Sink = (*Pipeline)(nil)

// NewPipeline builds the governor and committer and starts the sweeper goroutine.
func NewPipeline(spool *DiskSpool, rec Recorder, cfg PipelineConfig) *Pipeline {
	return newPipelineClock(spool, rec, cfg, time.Now)
}

// newPipelineClock is NewPipeline with an injected clock so tests never race on p.now.
func newPipelineClock(spool *DiskSpool, rec Recorder, cfg PipelineConfig, now func() time.Time) *Pipeline {
	if rec == nil {
		rec = nopRecorder{}
	}
	if now == nil {
		now = time.Now
	}
	window := cfg.Governor.SuppressWindow
	if window <= 0 {
		window = 60 * time.Second // NewGovernor's default
	}
	every := window / 4
	if every < time.Second {
		every = time.Second
	}
	maxKeys := cfg.Governor.MaxKeys
	if maxKeys <= 0 {
		maxKeys = 4096 // NewGovernor's default
	}
	p := &Pipeline{
		spool:      spool,
		rec:        rec,
		gov:        NewGovernor(cfg.Governor),
		com:        NewCommitter(spool, rec, cfg.Committer),
		now:        now,
		maxCarry:   4 * maxKeys,
		sweepEvery: every,
		sweepQuit:  make(chan struct{}),
		sweepDone:  make(chan struct{}),
	}
	go p.sweepLoop()
	return p
}

func (p *Pipeline) sweepLoop() {
	defer close(p.sweepDone)
	t := time.NewTicker(p.sweepEvery)
	defer t.Stop()
	for {
		select {
		case <-p.sweepQuit:
			return
		case <-t.C:
			p.sweepOnce(p.now())
		}
	}
}

// sweepOnce enqueues a summary row for every suppression window closed by now. The
// governor forgets a window once it is swept, so the summary is the only record of the
// suppressed count: one that finds the queue full is carried to the next sweep instead
// of being dropped (WR-02). The carry is bounded (maxCarry, 4x the key table); whatever
// does not fit is counted as a dropped denial.
func (p *Pipeline) sweepOnce(now time.Time) {
	p.sumMu.Lock()
	defer p.sumMu.Unlock()

	p.carry = append(p.carry, p.gov.Sweep(now)...)
	sent := 0
	for sent < len(p.carry) && p.com.TryEnqueueSummary(p.carry[sent]) {
		sent++
	}
	p.carry = p.carry[sent:]
	if over := len(p.carry) - p.maxCarry; over > 0 {
		p.rec.RecordAuditDropped(kindDenial, dropQueueFull, over)
		p.carry = p.carry[over:] // the oldest go first
	}
	// Re-slice into a fresh array so delivered and dropped summaries can be collected.
	if len(p.carry) == 0 {
		p.carry = nil
	} else {
		p.carry = append([]*CompletionEvent(nil), p.carry...)
	}
}

// flushSummaries enqueues the shutdown summaries, waiting for queue room while the
// committer is still draining, until ctx ends. Whatever is still unsent then is counted.
func (p *Pipeline) flushSummaries(ctx context.Context, pending []*CompletionEvent) {
	for i, s := range pending {
		for !p.com.TryEnqueueSummary(s) {
			t := time.NewTimer(2 * time.Millisecond)
			select {
			case <-ctx.Done():
				t.Stop()
				p.rec.RecordAuditDropped(kindDenial, dropQueueFull, len(pending)-i)
				return
			case <-t.C:
			}
		}
	}
}

// RecordDenial applies the governor and then records the denial durably, counts it
// into a suppression window, or drops it. It never changes the HTTP response: a
// failed or dropped append is counted by the committer and handed back to the
// governor so the next identical denial is recorded again.
func (p *Pipeline) RecordDenial(ev *CompletionEvent) {
	if ev == nil {
		return
	}
	if p.closed.Load() {
		p.rec.RecordAuditDropped(kindDenial, dropClosed, 1)
		return
	}
	adm := p.gov.Admit(ev, p.now())
	if adm.Overflow {
		p.rec.RecordAuditSuppressorOverflow()
	}
	switch adm.Disposition {
	case Record:
		// A denial that did not reach the spool must not leave a suppression window
		// (or a spent unauthenticated token) behind (WR-01).
		if err := p.com.SubmitDenial(ev); err != nil {
			p.gov.Forget(adm)
		}
	case Suppress:
		p.rec.RecordAuditSuppressed(adm.Label, 1)
	default:
		p.rec.RecordAuditDropped(kindDenial, "unauth_cap", 1)
	}
}

// EnqueueCompletion forwards to the committer's bounded, non-blocking queue. Completions
// are never governed.
func (p *Pipeline) EnqueueCompletion(ev *CompletionEvent) {
	if ev == nil {
		return
	}
	p.com.EnqueueCompletion(ev)
}

// QueueDepth reports the number of queued completion and summary frames.
func (p *Pipeline) QueueDepth() int { return p.com.QueueDepth() }

// Shutdown stops the sweeper, turns every open suppression window into a summary
// row, and only then lets the committer drain everything inside ctx (D-10).
// Subsequent calls return nil.
func (p *Pipeline) Shutdown(ctx context.Context) error {
	var err error
	p.shutdownOnce.Do(func() {
		p.closed.Store(true)
		close(p.sweepQuit)
		<-p.sweepDone
		p.sumMu.Lock()
		pending := append(p.carry, p.gov.Flush(p.now())...)
		p.carry = nil
		p.sumMu.Unlock()
		p.flushSummaries(ctx, pending)
		err = p.com.Shutdown(ctx)
	})
	return err
}
