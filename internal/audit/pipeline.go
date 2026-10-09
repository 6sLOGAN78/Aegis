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
	p := &Pipeline{
		spool:      spool,
		rec:        rec,
		gov:        NewGovernor(cfg.Governor),
		com:        NewCommitter(spool, rec, cfg.Committer),
		now:        now,
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

// sweepOnce enqueues a summary row for every suppression window closed by now.
func (p *Pipeline) sweepOnce(now time.Time) {
	for _, s := range p.gov.Sweep(now) {
		p.com.EnqueueSummary(s)
	}
}

// RecordDenial applies the governor and then records the denial durably, counts it
// into a suppression window, or drops it. It never changes the HTTP response: a
// failed or dropped append is counted by the committer and otherwise ignored.
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
		_ = p.com.SubmitDenial(ev)
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
		for _, s := range p.gov.Flush(p.now()) {
			p.com.EnqueueSummary(s)
		}
		err = p.com.Shutdown(ctx)
	})
	return err
}
