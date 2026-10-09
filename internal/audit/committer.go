package audit

import (
	"context"
	"errors"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

// Recorder is the metrics surface the committer (and the governor in the pipeline)
// reports to. *telemetry.Metrics satisfies it structurally; the audit package never
// imports telemetry. A nil Recorder passed to NewCommitter is replaced by a no-op.
type Recorder interface {
	RecordAuditWritten(kind string, n int)
	RecordAuditDropped(kind, reason string, n int)
	RecordAuditSuppressed(reasonCode string, n int)
	RecordAuditSuppressorOverflow()
	ObserveAuditFlush(d time.Duration)
	SetAuditDegraded(degraded bool)
	SetAuditQueueDepth(n int)
}

type nopRecorder struct{}

func (nopRecorder) RecordAuditWritten(string, int)         {}
func (nopRecorder) RecordAuditDropped(string, string, int) {}
func (nopRecorder) RecordAuditSuppressed(string, int)      {}
func (nopRecorder) RecordAuditSuppressorOverflow()         {}
func (nopRecorder) ObserveAuditFlush(time.Duration)        {}
func (nopRecorder) SetAuditDegraded(bool)                  {}
func (nopRecorder) SetAuditQueueDepth(int)                 {}

// Metric kinds and drop reasons used by the committer.
const (
	kindCompletion = "completion"
	kindDenial     = "denial"

	dropQueueFull  = "queue_full"
	dropHardLimit  = "hard_limit"
	dropWriteError = "write_error"
	dropClosed     = "closed"
	dropTimeout    = "timeout"
)

var (
	// ErrCommitterClosed is returned once Shutdown has begun.
	ErrCommitterClosed = errors.New("audit committer closed")
	// ErrDenialQueueFull is returned by SubmitDenial when the denial queue is full.
	ErrDenialQueueFull = errors.New("audit denial queue full")
	// ErrDenialTimeout is returned by SubmitDenial when the fsync did not finish
	// within the waiter timeout. The frame may still be written afterwards.
	ErrDenialTimeout = errors.New("audit denial wait timed out")
)

// CommitterConfig tunes the group-commit writer. Zero values select the defaults.
type CommitterConfig struct {
	GroupFlush       time.Duration // linger for batches holding a denial (default 2ms)
	CompletionFlush  time.Duration // linger for completion-only batches (default 25ms)
	QueueSize        int           // completion queue capacity (default 8192)
	DenialQueueSize  int           // denial queue capacity (default 1024)
	MaxBatchFrames   int           // frames per flush (default 512)
	MaxBatchBytes    int           // bytes per flush (default 512 KiB)
	WaiterTimeout    time.Duration // SubmitDenial wait bound (default 2s)
	RecoveryInterval time.Duration // fault recovery tick (default 250ms)
}

func (c *CommitterConfig) applyDefaults() {
	if c.GroupFlush <= 0 {
		c.GroupFlush = 2 * time.Millisecond
	}
	if c.CompletionFlush <= 0 {
		c.CompletionFlush = 25 * time.Millisecond
	}
	if c.QueueSize <= 0 {
		c.QueueSize = 8192
	}
	if c.DenialQueueSize <= 0 {
		c.DenialQueueSize = 1024
	}
	if c.MaxBatchFrames <= 0 {
		c.MaxBatchFrames = 512
	}
	if c.MaxBatchBytes <= 0 {
		c.MaxBatchBytes = 512 * 1024
	}
	if c.WaiterTimeout <= 0 {
		c.WaiterTimeout = 2 * time.Second
	}
	if c.RecoveryInterval <= 0 {
		c.RecoveryInterval = 250 * time.Millisecond
	}
}

// commitItem is one framed record. done is non-nil only for synchronous denials.
type commitItem struct {
	frame []byte
	kind  string
	done  chan error
}

// Committer is the single goroutine that owns every denial, completion and summary
// write to the spool. It batches whatever has arrived into one DiskSpool.WriteFrames
// call (one write, one fsync) per flush. It never calls the pre-forward append or
// the 0.90 admission gate; the pre-forward allow path is unchanged (D-09).
type Committer struct {
	spool *DiskSpool
	rec   Recorder
	cfg   CommitterConfig

	// The data channels are never closed so a late sender cannot panic.
	denialCh     chan commitItem
	completionCh chan commitItem
	quit         chan struct{}
	loopDone     chan struct{}

	closed       atomic.Bool
	shutdownOnce sync.Once

	// faultGen increments on every setFault so a recovery probe that started before a
	// newer fault cannot clear it.
	faultMu  sync.Mutex
	faultGen uint64

	probeHook atomic.Pointer[func()]
	lastLogNS atomic.Int64
}

// NewCommitter applies defaults and starts the committer goroutine.
func NewCommitter(spool *DiskSpool, rec Recorder, cfg CommitterConfig) *Committer {
	cfg.applyDefaults()
	if rec == nil {
		rec = nopRecorder{}
	}
	c := &Committer{
		spool:        spool,
		rec:          rec,
		cfg:          cfg,
		denialCh:     make(chan commitItem, cfg.DenialQueueSize),
		completionCh: make(chan commitItem, cfg.QueueSize),
		quit:         make(chan struct{}),
		loopDone:     make(chan struct{}),
	}
	go c.run()
	return c
}

// logf is a rate limited (one line per second) logger. It carries reasons and counts
// only, never event fields.
func (c *Committer) logf(format string, args ...any) {
	now := time.Now().UnixNano()
	last := c.lastLogNS.Load()
	if now-last < int64(time.Second) || !c.lastLogNS.CompareAndSwap(last, now) {
		return
	}
	log.Printf(format, args...)
}

// frame copies, normalizes (D-18) and frames ev.
func (c *Committer) frame(ev *CompletionEvent) ([]byte, error) {
	if ev == nil {
		return nil, errors.New("nil audit event")
	}
	cp := *ev
	cp.Normalize()
	return frameEvent(&cp)
}

// SubmitDenial blocks until the denial frame is fsynced, the committer shuts down,
// or the waiter timeout elapses. It takes no context on purpose: a canceled client
// request must not skip durability (D-07).
func (c *Committer) SubmitDenial(ev *CompletionEvent) error {
	if c.closed.Load() {
		c.rec.RecordAuditDropped(kindDenial, dropClosed, 1)
		return ErrCommitterClosed
	}
	frame, err := c.frame(ev)
	if err != nil {
		c.rec.RecordAuditDropped(kindDenial, dropWriteError, 1)
		return err
	}
	done := make(chan error, 1)
	select {
	case c.denialCh <- commitItem{frame: frame, kind: kindDenial, done: done}:
	default:
		c.rec.RecordAuditDropped(kindDenial, dropQueueFull, 1)
		return ErrDenialQueueFull
	}

	timer := time.NewTimer(c.cfg.WaiterTimeout)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-c.loopDone:
		// The loop completes every waiter it handled before closing loopDone.
		select {
		case err := <-done:
			return err
		default:
		}
		c.rec.RecordAuditDropped(kindDenial, dropClosed, 1)
		return ErrCommitterClosed
	case <-timer.C:
		c.rec.RecordAuditDropped(kindDenial, dropTimeout, 1)
		return ErrDenialTimeout
	}
}

// EnqueueCompletion queues a completion frame without ever blocking (D-08). When the
// queue is full the loss is counted and the spool write fault is set (D-12).
func (c *Committer) EnqueueCompletion(ev *CompletionEvent) {
	if !c.enqueueAsync(ev, kindCompletion, true) {
		c.setFault(dropQueueFull)
	}
}

// EnqueueSummary queues a suppression summary row (metric kind "denial"). It never
// blocks and never sets the write fault.
func (c *Committer) EnqueueSummary(ev *CompletionEvent) {
	c.enqueueAsync(ev, kindDenial, true)
}

// TryEnqueueSummary is EnqueueSummary for callers that keep the summary and retry: it
// returns false, without counting a drop, when the queue is full. Any other outcome
// (queued, committer closed, unframeable event) is final and already counted.
func (c *Committer) TryEnqueueSummary(ev *CompletionEvent) bool {
	return c.enqueueAsync(ev, kindDenial, false)
}

// enqueueAsync returns false only when the queue was full. A full queue is counted as a
// drop only when countFull is set.
func (c *Committer) enqueueAsync(ev *CompletionEvent, kind string, countFull bool) bool {
	if c.closed.Load() {
		c.rec.RecordAuditDropped(kind, dropClosed, 1)
		return true
	}
	frame, err := c.frame(ev)
	if err != nil {
		c.rec.RecordAuditDropped(kind, dropWriteError, 1)
		return true
	}
	select {
	case c.completionCh <- commitItem{frame: frame, kind: kind}:
	default:
		if countFull {
			c.rec.RecordAuditDropped(kind, dropQueueFull, 1)
		}
		return false
	}
	select {
	case <-c.loopDone:
		// The loop already exited; nothing will ever write this frame.
		c.rec.RecordAuditDropped(kind, dropClosed, 1)
	default:
	}
	return true
}

// QueueDepth reports the number of queued completion and summary frames.
func (c *Committer) QueueDepth() int { return len(c.completionCh) }

// Shutdown stops intake, flushes everything queued with a final fsync and waits for
// the loop to exit or ctx to end. It is idempotent.
func (c *Committer) Shutdown(ctx context.Context) error {
	c.shutdownOnce.Do(func() {
		c.closed.Store(true)
		close(c.quit)
	})
	select {
	case <-c.loopDone:
		return nil
	default:
	}
	select {
	case <-c.loopDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ---- fault handling (D-12) ----

func (c *Committer) setFault(reason string) {
	c.faultMu.Lock()
	defer c.faultMu.Unlock()
	c.faultGen++
	was, _ := c.spool.WriteFaulted()
	c.spool.SetWriteFault(reason)
	if !was {
		c.rec.SetAuditDegraded(true)
		c.logf("AuditCommitter warning: audit write fault set (%s); refusing allowed traffic", reason)
	}
}

func (c *Committer) currentFaultGen() uint64 {
	c.faultMu.Lock()
	defer c.faultMu.Unlock()
	return c.faultGen
}

// clearFaultIfGen clears the fault only when no newer fault was raised since gen was
// read. There is deliberately no unconditional clear.
func (c *Committer) clearFaultIfGen(gen uint64) bool {
	c.faultMu.Lock()
	defer c.faultMu.Unlock()
	if c.faultGen != gen {
		return false
	}
	if was, _ := c.spool.WriteFaulted(); was {
		c.spool.ClearWriteFault()
		c.rec.SetAuditDegraded(false)
		c.logf("AuditCommitter warning: audit write fault cleared")
	}
	return true
}

func (c *Committer) setProbeHook(fn func()) {
	if fn == nil {
		c.probeHook.Store(nil)
		return
	}
	c.probeHook.Store(&fn)
}

// recoveryProbe evaluates the recovery conditions for a queue_full or hard_limit
// fault. The generation is read FIRST so a fault raised mid-probe survives.
func (c *Committer) recoveryProbe() bool {
	if !c.recoverable() {
		return false
	}
	return c.tryRecover(c.currentFaultGen())
}

func (c *Committer) recoverable() bool {
	faulted, reason := c.spool.WriteFaulted()
	return faulted && (reason == dropQueueFull || reason == dropHardLimit)
}

func (c *Committer) tryRecover(gen uint64) bool {
	if !c.recoverable() {
		return false
	}
	if len(c.completionCh) > cap(c.completionCh)/2 {
		return false
	}
	if exceeded, err := c.spool.HardLimitExceeded(); err != nil || exceeded {
		return false
	}
	if h := c.probeHook.Load(); h != nil {
		(*h)()
	}
	return c.clearFaultIfGen(gen)
}

// ---- the loop ----

func (c *Committer) run() {
	defer close(c.loopDone)
	ticker := time.NewTicker(c.cfg.RecoveryInterval)
	defer ticker.Stop()

	for {
		var first commitItem
		select {
		case first = <-c.denialCh:
		case first = <-c.completionCh:
		case <-ticker.C:
			c.rec.SetAuditQueueDepth(len(c.completionCh))
			c.spool.RepairSegment()
			c.recoveryProbe()
			continue
		case <-c.quit:
			c.finalDrain(nil)
			return
		}

		batch, quitSeen := c.collect(first)
		if quitSeen {
			c.finalDrain(batch)
			return
		}
		if retained := c.attempt(batch); len(retained) > 0 {
			if !c.retry(retained) {
				c.finalDrain(nil)
				return
			}
		}
	}
}

// collect builds one batch starting with first. It stops when the flush deadline
// fires (then sweeps whatever is immediately available), a batch cap is reached, or
// shutdown starts. A pending denial shortens the deadline to the group-flush linger.
func (c *Committer) collect(first commitItem) (batch []commitItem, quitSeen bool) {
	batch = []commitItem{first}
	size := len(first.frame)
	hasDenial := first.done != nil

	linger := c.cfg.CompletionFlush
	if hasDenial {
		linger = c.cfg.GroupFlush
	}
	deadline := time.Now().Add(linger)
	timer := time.NewTimer(linger)
	defer func() { timer.Stop() }()

	add := func(it commitItem) {
		batch = append(batch, it)
		size += len(it.frame)
	}
	full := func() bool {
		return len(batch) >= c.cfg.MaxBatchFrames || size >= c.cfg.MaxBatchBytes
	}

	for !full() {
		select {
		case it := <-c.denialCh:
			add(it)
			if !hasDenial {
				hasDenial = true
				if nd := time.Now().Add(c.cfg.GroupFlush); nd.Before(deadline) {
					deadline = nd
					timer.Stop()
					timer = time.NewTimer(time.Until(nd))
				}
			}
			continue
		case it := <-c.completionCh:
			add(it)
			continue
		case <-c.quit:
			return batch, true
		case <-timer.C:
		}
		// Deadline fired: take whatever has already arrived, then flush.
		for !full() {
			select {
			case it := <-c.denialCh:
				add(it)
			case it := <-c.completionCh:
				add(it)
			default:
				return batch, false
			}
		}
		return batch, false
	}
	return batch, false
}

// attempt performs one WriteFrames call for the batch and settles it. It returns the
// non-denial frames that must be retried after a write error (nil otherwise).
func (c *Committer) attempt(batch []commitItem) (retained []commitItem) {
	if len(batch) == 0 {
		return nil
	}
	gen := c.currentFaultGen()
	frames := make([][]byte, len(batch))
	for i, it := range batch {
		frames[i] = it.frame
	}
	start := time.Now()
	err := c.spool.WriteFrames(frames)
	c.rec.ObserveAuditFlush(time.Since(start))
	c.rec.SetAuditQueueDepth(len(c.completionCh))

	if err == nil {
		c.settle(batch, nil)
		c.countWritten(batch)
		c.tryRecover(gen)
		return nil
	}
	if errors.Is(err, ErrHardLimit) {
		c.settle(batch, ErrHardLimit)
		c.countDropped(batch, dropHardLimit)
		if hasKind(batch, kindCompletion) {
			c.setFault(dropHardLimit)
		}
		return nil
	}

	// Write or sync error: denial waiters fail, everything else is retained.
	var waiters []commitItem
	for _, it := range batch {
		if it.done != nil {
			waiters = append(waiters, it)
		} else {
			retained = append(retained, it)
		}
	}
	c.settle(waiters, err)
	c.countDropped(waiters, dropWriteError)
	c.logf("AuditCommitter warning: batch write failed (%d frames, %d retained): %v", len(batch), len(retained), err)
	if hasKind(retained, kindCompletion) {
		c.setFault(dropWriteError)
	}
	return retained
}

// retry keeps writing retained frames with backoff (100ms doubling to 1s). It blocks
// the loop on purpose so the queues fill and EnqueueCompletion faults the gateway.
// It returns false if shutdown interrupted it (retained frames are then dropped).
func (c *Committer) retry(retained []commitItem) bool {
	backoff := 100 * time.Millisecond
	for {
		t := time.NewTimer(backoff)
		select {
		case <-t.C:
		case <-c.quit:
			t.Stop()
			c.countDropped(retained, dropWriteError)
			return false
		}
		gen := c.currentFaultGen()
		frames := make([][]byte, len(retained))
		for i, it := range retained {
			frames[i] = it.frame
		}
		start := time.Now()
		err := c.spool.WriteFrames(frames)
		c.rec.ObserveAuditFlush(time.Since(start))
		switch {
		case err == nil:
			c.countWritten(retained)
			c.clearWriteErrorFault(gen)
			return true
		case errors.Is(err, ErrHardLimit):
			c.countDropped(retained, dropHardLimit)
			if hasKind(retained, kindCompletion) {
				c.setFault(dropHardLimit)
			}
			return true
		}
		c.logf("AuditCommitter warning: retry write failed (%d frames): %v", len(retained), err)
		if backoff *= 2; backoff > time.Second {
			backoff = time.Second
		}
	}
}

// clearWriteErrorFault ends a write_error fault after a successful retry. If the
// completion queue is meanwhile more than half full the fault is handed to the
// recovery tick as queue_full instead of reopening the gate immediately.
func (c *Committer) clearWriteErrorFault(gen uint64) {
	if faulted, reason := c.spool.WriteFaulted(); !faulted || reason != dropWriteError {
		return
	}
	if len(c.completionCh) > cap(c.completionCh)/2 {
		c.setFault(dropQueueFull)
		return
	}
	c.clearFaultIfGen(gen)
}

// finalDrain flushes batch and everything still queued with no retries, then
// releases any waiter that raced in with ErrCommitterClosed.
func (c *Committer) finalDrain(batch []commitItem) {
	flush := func(b []commitItem) {
		retained := c.attempt(b)
		c.countDropped(retained, dropWriteError)
	}
	flush(batch)
	for {
		var b []commitItem
		size := 0
	fill:
		for len(b) < c.cfg.MaxBatchFrames && size < c.cfg.MaxBatchBytes {
			select {
			case it := <-c.denialCh:
				b = append(b, it)
				size += len(it.frame)
				continue
			default:
			}
			select {
			case it := <-c.completionCh:
				b = append(b, it)
				size += len(it.frame)
			default:
				break fill
			}
		}
		if len(b) == 0 {
			break
		}
		flush(b)
	}
	// Sweep stragglers that arrived after the drain emptied the queues.
	for {
		select {
		case it := <-c.denialCh:
			c.settle([]commitItem{it}, ErrCommitterClosed)
			c.rec.RecordAuditDropped(it.kind, dropClosed, 1)
		case it := <-c.completionCh:
			c.rec.RecordAuditDropped(it.kind, dropClosed, 1)
		default:
			return
		}
	}
}

func (c *Committer) settle(items []commitItem, err error) {
	for _, it := range items {
		if it.done != nil {
			it.done <- err // buffered 1, one send per waiter: never blocks
		}
	}
}

func (c *Committer) countWritten(items []commitItem) {
	comp, den := countKinds(items)
	if comp > 0 {
		c.rec.RecordAuditWritten(kindCompletion, comp)
	}
	if den > 0 {
		c.rec.RecordAuditWritten(kindDenial, den)
	}
}

func (c *Committer) countDropped(items []commitItem, reason string) {
	comp, den := countKinds(items)
	if comp > 0 {
		c.rec.RecordAuditDropped(kindCompletion, reason, comp)
	}
	if den > 0 {
		c.rec.RecordAuditDropped(kindDenial, reason, den)
	}
}

func countKinds(items []commitItem) (completions, denials int) {
	for _, it := range items {
		if it.kind == kindCompletion {
			completions++
		} else {
			denials++
		}
	}
	return
}

func hasKind(items []commitItem, kind string) bool {
	for _, it := range items {
		if it.kind == kind {
			return true
		}
	}
	return false
}
