package audit

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var govT0 = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

const govWindow = 60 * time.Second

func govNew(maxKeys int) *Governor {
	return NewGovernor(GovernorConfig{SuppressWindow: govWindow, MaxKeys: maxKeys, UnauthRate: 10, UnauthBurst: 50})
}

func govEvent(principal, route, reason string) *CompletionEvent {
	return &CompletionEvent{
		EventID:       "first-" + principal + route + reason,
		RequestID:     fmt.Sprintf("req-%s-%s-%s", principal, route, reason),
		PrincipalID:   principal,
		PrincipalKind: "user",
		RouteID:       route,
		Decision:      "deny",
		ReasonCode:    reason,
		HTTPStatus:    403,
		DurationMS:    12.5,
	}
}

func govAnon(reason string) *CompletionEvent {
	ev := govEvent("anonymous", "", reason)
	ev.PrincipalKind = "anonymous"
	ev.HTTPStatus = 401
	return ev
}

func TestGovernorSuppression(t *testing.T) {
	reasons := []string{
		"RATE_LIMIT_EXCEEDED", "AUDIT_SPOOL_SATURATED", "ROUTE_NOT_FOUND",
		"PRINCIPAL_QUARANTINED", "TOKEN_REVOKED", "EVALUATION_ERROR",
		"DENIED_EMPTY_RESULT", "MALFORMED_DECISION", "DENIED_DEFAULT",
	}
	for _, reason := range reasons {
		t.Run(reason, func(t *testing.T) {
			g := govNew(100)
			first := govEvent("u1", "r1", reason)
			adm := g.Admit(first, govT0)
			assert.Equal(t, Record, adm.Disposition)
			assert.Equal(t, ReasonLabel(reason), adm.Label)
			assert.False(t, adm.Overflow)
			for i := 0; i < 9; i++ {
				dup := govEvent("u1", "r1", reason)
				dup.RequestID = fmt.Sprintf("dup-%d", i)
				assert.Equal(t, Suppress, g.Admit(dup, govT0.Add(time.Duration(i+1)*time.Second)).Disposition)
			}
			assert.Empty(t, g.Sweep(govT0.Add(59*time.Second)))
			sums := g.Sweep(govT0.Add(govWindow))
			require.Len(t, sums, 1)
			s := sums[0]
			assert.Equal(t, EventTypeDenial, s.EventType)
			assert.Equal(t, 9, s.SuppressedCount)
			assert.Equal(t, first.RequestID, s.RequestID)
			assert.Equal(t, first.HTTPStatus, s.HTTPStatus)
			assert.Equal(t, reason, s.ReasonCode)
			assert.NotEmpty(t, s.EventID)
			assert.NotEqual(t, first.EventID, s.EventID)
			assert.True(t, s.Timestamp.Equal(govT0.Add(govWindow)))
			assert.Equal(t, 0.0, s.DurationMS)
			assert.Equal(t, 0, g.keyCount())
			assert.Equal(t, Record, g.Admit(govEvent("u1", "r1", reason), govT0.Add(61*time.Second)).Disposition)
		})
	}
}

func TestGovernorDistinctDenialsAllRecorded(t *testing.T) {
	g := govNew(100)
	assert.Equal(t, Record, g.Admit(govEvent("u1", "r1", "ROUTE_NOT_FOUND"), govT0).Disposition)
	assert.Equal(t, Record, g.Admit(govEvent("u2", "r1", "ROUTE_NOT_FOUND"), govT0).Disposition)
	assert.Equal(t, Record, g.Admit(govEvent("u1", "r2", "ROUTE_NOT_FOUND"), govT0).Disposition)
	assert.Equal(t, Record, g.Admit(govEvent("u1", "r1", "DENIED_DEFAULT"), govT0).Disposition)
	assert.Equal(t, Suppress, g.Admit(govEvent("u1", "r1", "ROUTE_NOT_FOUND"), govT0).Disposition)
}

func TestGovernorOutageByReason(t *testing.T) {
	for _, reason := range []string{"DEPENDENCY_OUTAGE_REDIS", "AUDIT_SPOOL_WRITE_ERROR", "POLICY_LEASE_EXPIRED", "UNINITIALIZED"} {
		t.Run(reason, func(t *testing.T) {
			g := govNew(1000)
			var rec, sup int
			var firstReq string
			for i := 0; i < 150; i++ {
				ev := govEvent(fmt.Sprintf("p%d", i%50), fmt.Sprintf("r%d", i%3), reason)
				if i%7 == 0 {
					ev.PrincipalKind = "anonymous"
				}
				ev.RequestID = fmt.Sprintf("req-%d", i)
				if i == 0 {
					firstReq = ev.RequestID
				}
				switch g.Admit(ev, govT0.Add(time.Duration(i)*100*time.Millisecond)).Disposition {
				case Record:
					rec++
				case Suppress:
					sup++
				}
			}
			assert.Equal(t, 1, rec)
			assert.Equal(t, 149, sup)
			sums := g.Sweep(govT0.Add(govWindow))
			require.Len(t, sums, 1)
			assert.Equal(t, 149, sums[0].SuppressedCount)
			assert.Equal(t, firstReq, sums[0].RequestID)
		})
	}

	t.Run("independent reasons", func(t *testing.T) {
		g := govNew(100)
		assert.Equal(t, Record, g.Admit(govEvent("p1", "r1", "DEPENDENCY_OUTAGE_REDIS"), govT0).Disposition)
		assert.Equal(t, Record, g.Admit(govEvent("p1", "r1", "POLICY_LEASE_EXPIRED"), govT0).Disposition)
		assert.Equal(t, Suppress, g.Admit(govEvent("p2", "r2", "DEPENDENCY_OUTAGE_REDIS"), govT0).Disposition)
	})

	t.Run("saturation stays keyed per principal and route", func(t *testing.T) {
		g := govNew(1000)
		rec := 0
		for p := 0; p < 5; p++ {
			for r := 0; r < 3; r++ {
				for n := 0; n < 10; n++ {
					if g.Admit(govEvent(fmt.Sprintf("p%d", p), fmt.Sprintf("r%d", r), "AUDIT_SPOOL_SATURATED"), govT0).Disposition == Record {
						rec++
					}
				}
			}
		}
		assert.Equal(t, 15, rec)
	})
}

func TestGovernorAnonymousFailClosed(t *testing.T) {
	g := govNew(100)
	for _, reason := range []string{"POLICY_LEASE_EXPIRED", "UNINITIALIZED"} {
		for i := 0; i < 20; i++ {
			ev := govAnon(reason)
			ev.RouteID = fmt.Sprintf("r%d", i)
			ev.CanonicalPath = fmt.Sprintf("/path/%d", i)
			ev.HTTPStatus = 503
			want := Suppress
			if i == 0 {
				want = Record
			}
			assert.Equal(t, want, g.Admit(ev, govT0).Disposition, "%s %d", reason, i)
		}
	}
	sums := g.Sweep(govT0.Add(govWindow))
	require.Len(t, sums, 2)
	for _, s := range sums {
		assert.Equal(t, 19, s.SuppressedCount)
	}
}

func TestGovernorZeroCountExpiresSilently(t *testing.T) {
	g := govNew(100)
	g.Admit(govEvent("u1", "r1", "ROUTE_NOT_FOUND"), govT0)
	require.Equal(t, 1, g.keyCount())
	assert.Empty(t, g.Sweep(govT0.Add(govWindow)))
	assert.Equal(t, 0, g.keyCount())
}

func TestGovernorLateAdmitStashesSummary(t *testing.T) {
	g := govNew(100)
	first := govEvent("u1", "r1", "ROUTE_NOT_FOUND")
	g.Admit(first, govT0)
	for i := 0; i < 4; i++ {
		g.Admit(govEvent("u1", "r1", "ROUTE_NOT_FOUND"), govT0.Add(time.Second))
	}
	late := govT0.Add(2 * govWindow)
	assert.Equal(t, Record, g.Admit(govEvent("u1", "r1", "ROUTE_NOT_FOUND"), late).Disposition)
	sums := g.Sweep(late)
	require.Len(t, sums, 1)
	assert.Equal(t, 4, sums[0].SuppressedCount)
	assert.Equal(t, first.RequestID, sums[0].RequestID)
	assert.True(t, sums[0].Timestamp.Equal(govT0.Add(govWindow)))
	assert.Equal(t, 0, g.stashLen())
}

func TestGovernorOverflowBucket(t *testing.T) {
	g := govNew(3)
	for i := 0; i < 3; i++ {
		g.Admit(govEvent(fmt.Sprintf("k%d", i), "r1", "RATE_LIMIT_EXCEEDED"), govT0)
		// one repeat each so the keyed summaries have count > 0
		g.Admit(govEvent(fmt.Sprintf("k%d", i), "r1", "RATE_LIMIT_EXCEEDED"), govT0)
	}
	require.Equal(t, 3, g.keyCount())

	var rec, sup int
	var firstOverflowReq string
	for i := 0; i < 100; i++ {
		ev := govEvent(fmt.Sprintf("x%d", i), "r1", "RATE_LIMIT_EXCEEDED")
		adm := g.Admit(ev, govT0.Add(time.Second))
		assert.True(t, adm.Overflow)
		switch adm.Disposition {
		case Record:
			rec++
			firstOverflowReq = ev.RequestID
		case Suppress:
			sup++
		}
	}
	assert.Equal(t, 1, rec)
	assert.Equal(t, 99, sup)
	assert.Equal(t, 3, g.keyCount())
	assert.Equal(t, 1, g.overflowCount())

	// a second reason uses a second bucket with its own representative
	adm := g.Admit(govEvent("y0", "r1", "ROUTE_NOT_FOUND"), govT0.Add(time.Second))
	assert.Equal(t, Record, adm.Disposition)
	assert.True(t, adm.Overflow)
	assert.Equal(t, 2, g.overflowCount())

	sums := g.Sweep(govT0.Add(govWindow + time.Second))
	var overflowSum *CompletionEvent
	keyed := 0
	for _, s := range sums {
		switch {
		case s.SuppressedCount == 99:
			overflowSum = s
		case s.SuppressedCount == 1:
			keyed++
		}
	}
	assert.Equal(t, 3, keyed)
	require.NotNil(t, overflowSum)
	assert.Equal(t, firstOverflowReq, overflowSum.RequestID)
	assert.Equal(t, 0, g.keyCount())
	assert.Equal(t, 0, g.overflowCount())

	// after a sweep, new keys are tracked normally again
	adm = g.Admit(govEvent("z0", "r1", "RATE_LIMIT_EXCEEDED"), govT0.Add(2*govWindow))
	assert.Equal(t, Record, adm.Disposition)
	assert.False(t, adm.Overflow)
	assert.Equal(t, 1, g.keyCount())
}

func TestGovernorOverflowBucketsBounded(t *testing.T) {
	g := govNew(1)
	g.Admit(govEvent("seed", "r", "ROUTE_NOT_FOUND"), govT0)
	for i := 0; i < 1000; i++ {
		g.Admit(govEvent(fmt.Sprintf("p%d", i), "r", fmt.Sprintf("WEIRD_%d", i)), govT0)
	}
	assert.LessOrEqual(t, g.overflowCount(), len(ClosedReasonLabels()))
	assert.Equal(t, 1, g.overflowCount())
}

func TestGovernorStashBounded(t *testing.T) {
	g := govNew(3)
	a := func(at time.Time) { g.Admit(govEvent("A", "r", "ROUTE_NOT_FOUND"), at) }
	a(govT0)
	a(govT0.Add(time.Second)) // count 1 in window 1
	a(govT0.Add(61 * time.Second))
	a(govT0.Add(62 * time.Second)) // count 1 in window 2
	assert.Equal(t, 1, g.stashLen())
	a(govT0.Add(122 * time.Second))
	a(govT0.Add(123 * time.Second))
	assert.Equal(t, 1, g.stashLen())

	sums := g.Sweep(govT0.Add(400 * time.Second))
	var total int
	for _, s := range sums {
		total += s.SuppressedCount
	}
	assert.Equal(t, 3, total)
	assert.Equal(t, 0, g.stashLen())

	// churn three map keys plus overflow buckets through repeated late expiries
	bound := 3 + len(ClosedReasonLabels())
	reasons := []string{"ROUTE_NOT_FOUND", "RATE_LIMIT_EXCEEDED", "TOKEN_REVOKED", "DENIED_DEFAULT"}
	now := govT0.Add(500 * time.Second)
	for round := 0; round < 6; round++ {
		for k := 0; k < 8; k++ {
			for _, r := range reasons {
				for n := 0; n < 2; n++ {
					g.Admit(govEvent(fmt.Sprintf("k%d", k), "r", r), now)
				}
			}
		}
		now = now.Add(61 * time.Second)
		assert.LessOrEqual(t, g.stashLen(), bound)
	}
}

func TestGovernorFlush(t *testing.T) {
	g := govNew(2)
	for i := 0; i < 3; i++ {
		g.Admit(govEvent("a", "r", "ROUTE_NOT_FOUND"), govT0)
	}
	g.Admit(govEvent("b", "r", "ROUTE_NOT_FOUND"), govT0) // count 0 -> no summary
	g.Admit(govEvent("c", "r", "ROUTE_NOT_FOUND"), govT0) // overflow representative
	g.Admit(govEvent("d", "r", "ROUTE_NOT_FOUND"), govT0) // overflow repeat
	sums := g.Flush(govT0.Add(time.Second))
	require.Len(t, sums, 2)
	got := []int{sums[0].SuppressedCount, sums[1].SuppressedCount}
	assert.ElementsMatch(t, []int{2, 1}, got)
	for _, s := range sums {
		assert.True(t, s.Timestamp.Equal(govT0.Add(time.Second)))
	}
	assert.Equal(t, 0, g.keyCount())
	assert.Equal(t, 0, g.overflowCount())
	assert.Equal(t, 0, g.stashLen())
	assert.Empty(t, g.Flush(govT0.Add(time.Second)))
}

func TestReasonLabel(t *testing.T) {
	assert.Equal(t, "RATE_LIMIT_EXCEEDED", ReasonLabel("RATE_LIMIT_EXCEEDED"))
	assert.Equal(t, "OTHER", ReasonLabel("DENIED_SOMETHING_NEW"))
	assert.Equal(t, "OTHER", ReasonLabel(""))
	assert.Equal(t, "OTHER", ReasonLabel("POLICY_EVALUATION_ERROR"))

	labels := ClosedReasonLabels()
	assert.Contains(t, labels, "OTHER")
	assert.IsIncreasing(t, labels)
	for r := range reasonTable {
		assert.Contains(t, labels, r)
	}
	assert.Len(t, labels, len(reasonTable)+1)

	g := govNew(10)
	assert.Equal(t, "OTHER", g.Admit(govEvent("u", "r", "DENIED_SOMETHING_NEW"), govT0).Label)
}

func TestGovernorUnauthCap(t *testing.T) {
	g := govNew(100)
	for i := 0; i < 50; i++ {
		assert.Equal(t, Record, g.Admit(govAnon("UNAUTHORIZED"), govT0).Disposition, "event %d", i)
	}
	adm := g.Admit(govAnon("UNAUTHORIZED"), govT0)
	assert.Equal(t, Drop, adm.Disposition)
	assert.Equal(t, "UNAUTHORIZED", adm.Label)

	t1 := govT0.Add(time.Second)
	for i := 0; i < 10; i++ {
		assert.Equal(t, Record, g.Admit(govAnon("UNAUTHORIZED"), t1).Disposition, "refill %d", i)
	}
	assert.Equal(t, Drop, g.Admit(govAnon("UNAUTHORIZED"), t1).Disposition)

	t2 := t1.Add(time.Hour)
	for i := 0; i < 50; i++ {
		assert.Equal(t, Record, g.Admit(govAnon("UNAUTHORIZED"), t2).Disposition, "burst %d", i)
	}
	assert.Equal(t, Drop, g.Admit(govAnon("UNAUTHORIZED"), t2).Disposition)
	assert.Equal(t, 0, g.keyCount(), "unauthenticated rejections never enter the suppressor map")
}

func TestGovernorUnauthBucketsIndependent(t *testing.T) {
	g := govNew(100)
	for i := 0; i < 60; i++ {
		g.Admit(govAnon("BAD_REQUEST_INVALID_PATH"), govT0)
	}
	assert.Equal(t, Drop, g.Admit(govAnon("BAD_REQUEST_INVALID_PATH"), govT0).Disposition)
	assert.Equal(t, Record, g.Admit(govAnon("UNAUTHORIZED"), govT0).Disposition)
	assert.Equal(t, Record, g.Admit(govAnon("UNAUTHORIZED_NO_CERT"), govT0).Disposition)
	assert.Equal(t, Record, g.Admit(govAnon("UNAUTHORIZED_INVALID_SPIFFE"), govT0).Disposition)
}

func TestGovernorUnauthOtherSharedAndBounded(t *testing.T) {
	g := govNew(100)
	for i := 0; i < 50; i++ {
		assert.Equal(t, Record, g.Admit(govAnon(fmt.Sprintf("WEIRD_%d", i)), govT0).Disposition)
	}
	assert.Equal(t, Drop, g.Admit(govAnon("SOMETHING_ELSE"), govT0).Disposition)
	// anonymous with an identified-class reason falls to the OTHER bucket, not the suppressor map
	assert.Equal(t, Drop, g.Admit(govAnon("RATE_LIMIT_EXCEEDED"), govT0).Disposition)
	assert.Equal(t, 0, g.keyCount())
	assert.LessOrEqual(t, g.bucketCount(), len(ClosedReasonLabels()))
	assert.Equal(t, 1, g.bucketCount())
}

func TestGovernorDefaults(t *testing.T) {
	g := NewGovernor(GovernorConfig{})
	assert.Equal(t, 60*time.Second, g.cfg.SuppressWindow)
	assert.Equal(t, 4096, g.cfg.MaxKeys)
	assert.Equal(t, 10.0, g.cfg.UnauthRate)
	assert.Equal(t, 50, g.cfg.UnauthBurst)
}

func TestGovernorConcurrent(t *testing.T) {
	g := govNew(16)
	const workers, perWorker = 32, 200
	var admits atomic.Int64
	var rec, sup, drop atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				var ev *CompletionEvent
				if i%5 == 0 {
					ev = govAnon("UNAUTHORIZED")
				} else {
					ev = govEvent(fmt.Sprintf("p%d", (w+i)%40), "r", "ROUTE_NOT_FOUND")
				}
				admits.Add(1)
				switch g.Admit(ev, govT0.Add(time.Duration(i)*time.Millisecond)).Disposition {
				case Record:
					rec.Add(1)
				case Suppress:
					sup.Add(1)
				case Drop:
					drop.Add(1)
				}
				if i%50 == 0 {
					g.Sweep(govT0.Add(time.Duration(i) * time.Millisecond))
				}
			}
		}(w)
	}
	wg.Wait()
	assert.Equal(t, admits.Load(), rec.Load()+sup.Load()+drop.Load())
	assert.Equal(t, int64(workers*perWorker), admits.Load())
}
