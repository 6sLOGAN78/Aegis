package main

import (
	"context"
	"sync/atomic"
	"time"

	"aegis/internal/revocation"
	"aegis/internal/telemetry"
)

// securityStatePoller polls the Redis security-state marker and exposes a fail-closed
// Missing() signal for /readyz (REV-03, D-05). Before the first successful poll, on any
// poll error, and when the last success is stale, Missing() is true.
type securityStatePoller struct {
	store    *revocation.Store
	metrics  *telemetry.Metrics
	interval time.Duration

	ready       atomic.Bool
	lastSuccess atomic.Int64 // unix nanos of last successful poll
}

func newSecurityStatePoller(store *revocation.Store, metrics *telemetry.Metrics, interval time.Duration) *securityStatePoller {
	if interval <= 0 {
		interval = time.Second
	}
	return &securityStatePoller{store: store, metrics: metrics, interval: interval}
}

func (p *securityStatePoller) pollOnce(ctx context.Context) {
	pctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	ok, err := p.store.SecurityStateReady(pctx)
	if err != nil {
		p.ready.Store(false)
	} else {
		p.ready.Store(ok)
		p.lastSuccess.Store(time.Now().UnixNano())
	}
	if p.metrics != nil && p.metrics.SecurityStateReady != nil {
		if p.ready.Load() {
			p.metrics.SecurityStateReady.Set(1)
		} else {
			p.metrics.SecurityStateReady.Set(0)
		}
	}
}

// Run polls until ctx is cancelled.
func (p *securityStatePoller) Run(ctx context.Context) {
	p.pollOnce(ctx)
	t := time.NewTicker(p.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.pollOnce(ctx)
		}
	}
}

// Missing reports whether the security state must be treated as missing (fail closed).
func (p *securityStatePoller) Missing() bool {
	if !p.ready.Load() {
		return true
	}
	last := p.lastSuccess.Load()
	if last == 0 {
		return true
	}
	return time.Since(time.Unix(0, last)) > 3*p.interval
}
