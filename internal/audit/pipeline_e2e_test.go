package audit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- shared end-to-end harness (also used by pipeline_e2e_delivery_test.go) ----

// e2eOptions overrides the harness defaults.
type e2eOptions struct {
	Governor   GovernorConfig
	Committer  CommitterConfig
	controlled bool // build the spool with newTestSpoolControlled
	ratio      float64
	logOut     io.Writer
}

// e2eEnv is a real AuditMiddleware -> Pipeline -> DiskSpool chain with a fake Recorder.
type e2eEnv struct {
	t        *testing.T
	spool    *DiskSpool
	rc       *ratioControl
	pipeline *Pipeline
	rec      *fakeRecorder
	clk      *e2eClock
	logBuf   *bytes.Buffer
}

func newE2E(t *testing.T, opts e2eOptions) *e2eEnv {
	t.Helper()
	env := &e2eEnv{t: t, rec: newFakeRecorder(), clk: newE2EClock(pipelineT0), logBuf: &bytes.Buffer{}}

	ratio := opts.ratio
	if ratio == 0 {
		ratio = 0.10
	}
	if opts.controlled {
		env.spool, env.rc = newTestSpoolControlled(t, ratio)
	} else {
		env.spool = newTestSpool(t, ratio)
	}

	gov := opts.Governor
	if gov.SuppressWindow == 0 {
		gov.SuppressWindow = 60 * time.Second
	}
	if gov.MaxKeys == 0 {
		gov.MaxKeys = 4096
	}
	if gov.UnauthRate == 0 {
		gov.UnauthRate = 10
	}
	if gov.UnauthBurst == 0 {
		gov.UnauthBurst = 50
	}
	com := opts.Committer
	if com.GroupFlush == 0 {
		com.GroupFlush = 2 * time.Millisecond
	}
	if com.CompletionFlush == 0 {
		com.CompletionFlush = 5 * time.Millisecond
	}
	if com.RecoveryInterval == 0 {
		com.RecoveryInterval = 20 * time.Millisecond
	}
	env.pipeline = newPipelineClock(env.spool, env.rec, PipelineConfig{Governor: gov, Committer: com}, env.clk.Now)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = env.pipeline.Shutdown(ctx)
	})
	return env
}

func (e *e2eEnv) dir() string { return e.spool.cfg.SpoolDir }

// handler wraps h in the real AuditMiddleware wired to the real pipeline.
func (e *e2eEnv) handler(h http.Handler) http.Handler {
	var out io.Writer = e.logBuf
	return AuditMiddleware(NewLogger(out), 0, WithSink(e.pipeline))(h)
}

// do serves one request through the middleware and returns the recorded response.
func (e *e2eEnv) do(h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	e.handler(h).ServeHTTP(rr, r)
	return rr
}

// shutdown drains the pipeline and returns every record on disk.
func (e *e2eEnv) shutdown() []*CompletionEvent {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(e.t, e.pipeline.Shutdown(ctx))
	return readAllEvents(e.t, e.dir())
}

func e2eRequest(method, path, remote string) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	r.RemoteAddr = remote
	return r
}

func e2eClientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil && host != "" {
		return host
	}
	return r.RemoteAddr
}

// e2eProblem mimics proxy.WriteProblemDetails (WriteHeader first, then the body).
func e2eProblem(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `{"status":%d,"detail":%q}`, status, detail)
}

// e2eAllowHandler mimics the allow exit of cmd/gateway/main.go: principal, route,
// decision, optional pre-forward append, then the upstream response.
func e2eAllowHandler(spool *DiskSpool, preForward bool, respond func(w http.ResponseWriter, r *http.Request)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ac := FromContext(r.Context())
		ac.SetPrincipal("user-1", "user", []string{"developer"})
		ac.SetRoute("orders.read", "orders-service")
		ac.SetDecision("allow", "ALLOWED_DEVELOPER_ORDERS")
		if preForward {
			ev := ac.ToCompletionEvent(r.Method, e2eClientIP(r), 0)
			if err := spool.AppendPreForward(ev); err != nil {
				e2eProblem(w, http.StatusServiceUnavailable, "audit unavailable")
				return
			}
		}
		time.Sleep(time.Millisecond) // a measurable upstream duration
		respond(w, r)
	})
}

// e2eDenyHandler mimics a deny exit: decision, error code, then the problem response.
func e2eDenyHandler(principal, kind, route, reason, errCode string, status int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ac := FromContext(r.Context())
		if principal != "" {
			ac.SetPrincipal(principal, kind, []string{"developer"})
		}
		if route != "" {
			ac.SetRoute(route, "orders-service")
		}
		ac.SetDecision("deny", reason)
		ac.SetErrorCode(errCode)
		time.Sleep(time.Millisecond)
		e2eProblem(w, status, reason)
	})
}

func e2eByRequest(evs []*CompletionEvent, requestID string) []*CompletionEvent {
	var out []*CompletionEvent
	for _, e := range evs {
		if e.RequestID == requestID {
			out = append(out, e)
		}
	}
	return out
}

func e2eOfType(evs []*CompletionEvent, eventType string) []*CompletionEvent {
	var out []*CompletionEvent
	for _, e := range evs {
		if e.EventType == eventType {
			out = append(out, e)
		}
	}
	return out
}

// e2eSplit separates ordinary denial rows from summary rows.
func e2eSplit(evs []*CompletionEvent) (rows, summaries []*CompletionEvent) {
	for _, e := range e2eOfType(evs, EventTypeDenial) {
		if e.SuppressedCount > 0 {
			summaries = append(summaries, e)
		} else {
			rows = append(rows, e)
		}
	}
	return rows, summaries
}

// ---- request-path scenarios ----

func TestE2EAllowedProducesDecisionAndCompletion(t *testing.T) {
	env := newE2E(t, e2eOptions{})
	h := e2eAllowHandler(env.spool, true, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	rr := env.do(h, e2eRequest(http.MethodGet, "/orders", "203.0.113.9:4444"))
	require.Equal(t, http.StatusOK, rr.Code)
	reqID := rr.Header().Get("X-Request-ID")
	require.NotEmpty(t, reqID)

	pair := e2eByRequest(env.shutdown(), reqID)
	require.Len(t, pair, 2)
	dec := e2eOfType(pair, EventTypeDecision)
	comp := e2eOfType(pair, EventTypeCompletion)
	require.Len(t, dec, 1)
	require.Len(t, comp, 1)

	assert.Equal(t, 0, dec[0].HTTPStatus)
	assert.Equal(t, float64(0), dec[0].DurationMS)
	assert.Equal(t, "allow", dec[0].Decision)
	assert.Equal(t, http.StatusOK, comp[0].HTTPStatus)
	assert.Greater(t, comp[0].DurationMS, 0.0)
	assert.Equal(t, "allow", comp[0].Decision)
	assert.NotEqual(t, dec[0].EventID, comp[0].EventID)
	assert.Equal(t, "user-1", comp[0].PrincipalID)
	assert.Equal(t, "203.0.113.9", comp[0].ClientIP)
	assert.Equal(t, 1, env.rec.written("completion"))
}

func TestE2EBackendFailureKeepsAllow(t *testing.T) {
	env := newE2E(t, e2eOptions{})
	upstreamErr := WrapUpstreamErrorHandler(func(w http.ResponseWriter, r *http.Request, err error) {
		w.WriteHeader(http.StatusBadGateway)
	})
	h := e2eAllowHandler(env.spool, true, func(w http.ResponseWriter, r *http.Request) {
		upstreamErr(w, r, errors.New("dial tcp: connection refused"))
	})
	rr := env.do(h, e2eRequest(http.MethodGet, "/orders", "203.0.113.9:4444"))
	require.Equal(t, http.StatusBadGateway, rr.Code)
	reqID := rr.Header().Get("X-Request-ID")

	pair := e2eByRequest(env.shutdown(), reqID)
	require.Len(t, pair, 2)
	comp := e2eOfType(pair, EventTypeCompletion)
	require.Len(t, comp, 1)
	assert.Equal(t, http.StatusBadGateway, comp[0].HTTPStatus)
	assert.Equal(t, "UPSTREAM_UNAVAILABLE", comp[0].ErrorCode)
	assert.Equal(t, "allow", comp[0].Decision, "a backend failure must not flip the policy decision")
	assert.Empty(t, e2eOfType(pair, EventTypeDenial))
}

func TestE2EDenialSingleRow(t *testing.T) {
	env := newE2E(t, e2eOptions{})
	h := e2eDenyHandler("user-1", "user", "admin.write", "DENIED_DEVELOPER_ADMIN_FORBIDDEN", "FORBIDDEN", http.StatusForbidden)
	rr := env.do(h, e2eRequest(http.MethodPost, "/admin", "203.0.113.9:4444"))
	require.Equal(t, http.StatusForbidden, rr.Code)
	reqID := rr.Header().Get("X-Request-ID")

	rows := e2eByRequest(env.shutdown(), reqID)
	require.Len(t, rows, 1, "exactly one record per denied request (D-02)")
	r := rows[0]
	assert.Equal(t, EventTypeDenial, r.EventType)
	assert.Equal(t, "deny", r.Decision)
	assert.Equal(t, "DENIED_DEVELOPER_ADMIN_FORBIDDEN", r.ReasonCode)
	assert.Equal(t, "FORBIDDEN", r.ErrorCode)
	assert.Equal(t, http.StatusForbidden, r.HTTPStatus)
	assert.Greater(t, r.DurationMS, 0.0)
	assert.Equal(t, "user-1", r.PrincipalID)
	assert.Equal(t, "user", r.PrincipalKind)
	assert.Equal(t, "admin.write", r.RouteID)
	assert.Equal(t, 0, r.SuppressedCount)
}

// e2eOrderWriter records how many records were on disk when the header was first written.
type e2eOrderWriter struct {
	*httptest.ResponseRecorder
	dir      string
	t        *testing.T
	once     sync.Once
	onDiskAt int
}

func (w *e2eOrderWriter) WriteHeader(code int) {
	w.once.Do(func() { w.onDiskAt = len(readAllEvents(w.t, w.dir)) })
	w.ResponseRecorder.WriteHeader(code)
}

func TestE2EDenialDurableBeforeBody(t *testing.T) {
	env := newE2E(t, e2eOptions{})
	h := e2eDenyHandler("user-1", "user", "admin.write", "DENIED_DEVELOPER_ADMIN_FORBIDDEN", "FORBIDDEN", http.StatusForbidden)
	w := &e2eOrderWriter{ResponseRecorder: httptest.NewRecorder(), dir: env.dir(), t: t}
	env.handler(h).ServeHTTP(w, e2eRequest(http.MethodPost, "/admin", "203.0.113.9:4444"))
	require.Equal(t, http.StatusForbidden, w.Code)
	assert.Equal(t, 1, w.onDiskAt, "the denial must be fsynced before the response header is written (D-07)")
}

func TestE2ESuppressionAndSummary(t *testing.T) {
	t.Run("identical repeats collapse", func(t *testing.T) {
		env := newE2E(t, e2eOptions{})
		h := e2eDenyHandler("user-1", "user", "orders.read", "RATE_LIMIT_EXCEEDED", "RATE_LIMIT_EXCEEDED", http.StatusTooManyRequests)
		var firstID string
		for i := 0; i < 25; i++ {
			rr := env.do(h, e2eRequest(http.MethodGet, "/orders", "203.0.113.9:4444"))
			require.Equal(t, http.StatusTooManyRequests, rr.Code)
			if i == 0 {
				firstID = rr.Header().Get("X-Request-ID")
			}
		}
		rows, summaries := e2eSplit(env.shutdown())
		require.Len(t, rows, 1)
		require.Len(t, summaries, 1)
		assert.Equal(t, firstID, rows[0].RequestID)
		assert.Equal(t, 24, summaries[0].SuppressedCount)
		assert.Equal(t, firstID, summaries[0].RequestID)
		assert.Equal(t, float64(0), summaries[0].DurationMS)
		assert.Equal(t, 24, env.rec.suppressed("RATE_LIMIT_EXCEEDED"))
	})

	t.Run("distinct principals are all recorded", func(t *testing.T) {
		env := newE2E(t, e2eOptions{})
		for i := 0; i < 25; i++ {
			h := e2eDenyHandler(fmt.Sprintf("user-%d", i), "user", "orders.read", "RATE_LIMIT_EXCEEDED", "RATE_LIMIT_EXCEEDED", http.StatusTooManyRequests)
			env.do(h, e2eRequest(http.MethodGet, "/orders", "203.0.113.9:4444"))
		}
		rows, summaries := e2eSplit(env.shutdown())
		assert.Len(t, rows, 25)
		assert.Empty(t, summaries)
		assert.Equal(t, 0, env.rec.suppressed("RATE_LIMIT_EXCEEDED"))
	})
}

func TestE2EOutageKeyedByReason(t *testing.T) {
	env := newE2E(t, e2eOptions{})
	for i := 0; i < 40; i++ {
		h := e2eDenyHandler(fmt.Sprintf("user-%d", i), "user", "orders.read", "DEPENDENCY_OUTAGE_REDIS", "DEPENDENCY_OUTAGE_REDIS", http.StatusServiceUnavailable)
		rr := env.do(h, e2eRequest(http.MethodGet, "/orders", "203.0.113.9:4444"))
		require.Equal(t, http.StatusServiceUnavailable, rr.Code)
	}
	rows, summaries := e2eSplit(env.shutdown())
	require.Len(t, rows, 1, "a gateway-wide outage costs one row per window (D-14)")
	require.Len(t, summaries, 1)
	assert.Equal(t, 39, summaries[0].SuppressedCount)
}

func TestE2EUnauthCap(t *testing.T) {
	env := newE2E(t, e2eOptions{Governor: GovernorConfig{UnauthBurst: 50, UnauthRate: 10}})
	h := e2eDenyHandler("", "", "", "UNAUTHORIZED", "UNAUTHORIZED", http.StatusUnauthorized)
	for i := 0; i < 200; i++ {
		rr := env.do(h, e2eRequest(http.MethodGet, "/orders", "198.51.100.20:1234"))
		require.Equal(t, http.StatusUnauthorized, rr.Code, "the cap never changes the response")
	}
	rows, summaries := e2eSplit(env.shutdown())
	assert.Len(t, rows, 50)
	assert.Empty(t, summaries)
	assert.Equal(t, 150, env.rec.dropped("denial", "unauth_cap"))
}

func TestE2EPoisonNormalized(t *testing.T) {
	env := newE2E(t, e2eOptions{})
	h := e2eDenyHandler("user-1", "user", "orders.read", "RATE_LIMIT_EXCEEDED", "RATE_LIMIT_EXCEEDED", http.StatusTooManyRequests)
	req := e2eRequest(http.MethodGet, "/api/x", "203.0.113.9:4444")
	req.URL.Path = "/api/\x00x"
	req.Method = strings.Repeat("M", 28)
	rr := env.do(h, req)
	require.Equal(t, http.StatusTooManyRequests, rr.Code)

	rows, _ := e2eSplit(env.shutdown())
	require.Len(t, rows, 1)
	assert.NotContains(t, rows[0].CanonicalPath, "\x00")
	assert.Equal(t, "/api/x", rows[0].CanonicalPath)
	assert.LessOrEqual(t, len(rows[0].HTTPMethod), 16)

	logged := env.logBuf.String()
	assert.Contains(t, logged, "canonical_path")
	assert.Contains(t, logged, "http_method")
	assert.Contains(t, logged, strings.Repeat("M", 28), "the stdout log keeps the raw value")
}

func TestE2EHeaderSpoofingIgnored(t *testing.T) {
	env := newE2E(t, e2eOptions{})
	h := e2eDenyHandler("user-1", "user", "orders.read", "RATE_LIMIT_EXCEEDED", "RATE_LIMIT_EXCEEDED", http.StatusTooManyRequests)
	req := e2eRequest(http.MethodGet, "/orders", "203.0.113.9:4444")
	req.Header.Set("X-Forwarded-For", "198.51.100.7")
	req.Header.Set("X-Real-IP", "198.51.100.8")
	env.do(h, req)

	rows, _ := e2eSplit(env.shutdown())
	require.Len(t, rows, 1)
	assert.Equal(t, "203.0.113.9", rows[0].ClientIP)
}
