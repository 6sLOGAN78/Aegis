package control

import (
	"context"
	"sync"
	"time"

	"aegis/internal/snapshot"
)

// LeaseGenerator runs a periodic loop issuing signed 10-second freshness leases (CTRL-04, Invariant 1).
type LeaseGenerator struct {
	server   *SnapshotDistributionServer
	signer   *snapshot.Signer
	interval time.Duration
	leaseTTL time.Duration

	mu      sync.Mutex
	stopCh  chan struct{}
	stopped bool
}

// NewLeaseGenerator instantiates a LeaseGenerator with configured interval and TTL.
func NewLeaseGenerator(server *SnapshotDistributionServer, signer *snapshot.Signer, interval, leaseTTL time.Duration) *LeaseGenerator {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	if leaseTTL <= 0 {
		leaseTTL = 15 * time.Second
	}

	return &LeaseGenerator{
		server:   server,
		signer:   signer,
		interval: interval,
		leaseTTL: leaseTTL,
		stopCh:   make(chan struct{}),
	}
}

// Start begins the lease generation loop. It blocks until ctx is canceled or Stop() is called.
func (l *LeaseGenerator) Start(ctx context.Context) {
	ticker := time.NewTicker(l.interval)
	defer ticker.Stop()

	// Emit initial lease immediately on startup if an active snapshot exists
	l.emitLease()

	for {
		select {
		case <-ctx.Done():
			return
		case <-l.stopCh:
			return
		case <-ticker.C:
			l.emitLease()
		}
	}
}

func (l *LeaseGenerator) emitLease() {
	if l.server == nil || l.signer == nil {
		return
	}

	active := l.server.GetActiveSnapshotInternal()
	if active == nil {
		return
	}

	lease, err := l.signer.SignLease(active.Version, l.leaseTTL)
	if err != nil {
		return
	}

	l.server.BroadcastLease(lease)
}

// Stop cleanly stops the lease generator loop.
func (l *LeaseGenerator) Stop() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.stopped {
		l.stopped = true
		close(l.stopCh)
	}
}
