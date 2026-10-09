package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompletionAuditLogging(t *testing.T) {
	testStatuses := []struct {
		statusCode int
		decision   string
		reason     string
		errCode    string
	}{
		{http.StatusOK, "allow", "ALLOWED", ""},
		{http.StatusBadRequest, "deny", "BAD_REQUEST_INVALID_PATH", "INVALID_PATH"},
		{http.StatusUnauthorized, "deny", "UNAUTHORIZED", "UNAUTHORIZED"},
		{http.StatusForbidden, "deny", "DENIED_DEVELOPER_ADMIN_FORBIDDEN", "FORBIDDEN"},
		{http.StatusTooManyRequests, "deny", "RATE_LIMITED", "TOO_MANY_REQUESTS"},
		{http.StatusBadGateway, "deny", "UPSTREAM_UNREACHABLE", "BAD_GATEWAY"},
	}

	t.Run("Direct LogCompletion emission across 200 400 401 403 429 502 responses", func(t *testing.T) {
		var buf bytes.Buffer
		logger := NewLogger(&buf)

		for _, tc := range testStatuses {
			event := CompletionEvent{
				EventID:         uuid.NewString(),
				Timestamp:       time.Now().UTC(),
				RequestID:       uuid.NewString(),
				PrincipalID:     "usr_dev_01",
				PrincipalKind:   "user",
				PrincipalRoles:  []string{"developer"},
				ClientIP:        "192.168.1.100",
				HTTPMethod:      "GET",
				CanonicalPath:   "/api/orders",
				RouteID:         "route_orders_read",
				ServiceID:       "orders",
				Decision:        tc.decision,
				ReasonCode:      tc.reason,
				HTTPStatus:      tc.statusCode,
				DurationMS:      1.45,
				SnapshotVersion: 1,
				ErrorCode:       tc.errCode,
			}

			buf.Reset()
			logger.LogCompletion(event)

			line := buf.String()
			require.NotEmpty(t, line)

			// Parse as generic map to check JSON field existence and types
			var parsedMap map[string]interface{}
			err := json.Unmarshal(buf.Bytes(), &parsedMap)
			require.NoError(t, err, "Log output must be valid JSON: %s", line)

			// 1. Assert message is "audit_completion"
			assert.Equal(t, "audit_completion", parsedMap["msg"])

			// 2. Assert event_id and request_id are valid non-empty UUIDs
			eventID, ok := parsedMap["event_id"].(string)
			require.True(t, ok, "event_id must be a string")
			_, err = uuid.Parse(eventID)
			require.NoError(t, err, "event_id must parse as valid UUID: %s", eventID)

			reqID, ok := parsedMap["request_id"].(string)
			require.True(t, ok, "request_id must be a string")
			_, err = uuid.Parse(reqID)
			require.NoError(t, err, "request_id must parse as valid UUID: %s", reqID)

			// 3. Assert decision matches "allow" or "deny"
			dec, ok := parsedMap["decision"].(string)
			require.True(t, ok)
			assert.Contains(t, []string{"allow", "deny"}, dec)
			assert.Equal(t, tc.decision, dec)

			// 4. Assert duration_ms is positive float
			dur, ok := parsedMap["duration_ms"].(float64)
			require.True(t, ok, "duration_ms must be a float64")
			assert.Greater(t, dur, 0.0)

			// 5. Assert status matches response code
			statusFloat, ok := parsedMap["status"].(float64)
			require.True(t, ok, "status must be numeric")
			assert.Equal(t, float64(tc.statusCode), statusFloat)

			httpStatusFloat, ok := parsedMap["http_status"].(float64)
			require.True(t, ok, "http_status must be numeric")
			assert.Equal(t, float64(tc.statusCode), httpStatusFloat)

			// 6. Assert presence and types of all 17 schema fields
			requiredFields := []string{
				"event_id", "timestamp", "request_id", "principal_id", "principal_kind",
				"principal_roles", "client_ip", "http_method", "canonical_path", "route_id",
				"service_id", "decision", "reason_code", "http_status", "duration_ms",
				"snapshot_version", "error_code",
			}
			for _, field := range requiredFields {
				_, exists := parsedMap[field]
				assert.True(t, exists, "Required field %q must be present in log output", field)
			}

			// 7. Unmarshal directly into CompletionEvent struct
			var parsedEvent CompletionEvent
			err = json.Unmarshal(buf.Bytes(), &parsedEvent)
			require.NoError(t, err)
			assert.Equal(t, tc.statusCode, parsedEvent.HTTPStatus)
			assert.Equal(t, tc.decision, parsedEvent.Decision)
			assert.Equal(t, tc.reason, parsedEvent.ReasonCode)
			assert.Equal(t, "usr_dev_01", parsedEvent.PrincipalID)
		}
	})

	t.Run("AuditMiddleware captures execution duration and status for HTTP pipeline", func(t *testing.T) {
		for _, tc := range testStatuses {
			t.Run(fmt.Sprintf("HTTP %d", tc.statusCode), func(t *testing.T) {
				var buf bytes.Buffer
				logger := NewLogger(&buf)

				middleware := AuditMiddleware(logger, 42)
				handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					ac := FromContext(r.Context())
					require.NotNil(t, ac)
					ac.SetPrincipal("usr_test_user", "user", []string{"tester"})
					ac.SetRoute("route_test", "service_test")
					ac.SetCanonicalPath("/test/endpoint")
					ac.SetDecision(tc.decision, tc.reason)
					if tc.errCode != "" {
						ac.SetErrorCode(tc.errCode)
					}

					// Simulate small execution delay to ensure positive duration
					time.Sleep(1 * time.Millisecond)
					w.WriteHeader(tc.statusCode)
					_, _ = w.Write([]byte(`{"status":"response"}`))
				}))

				req := httptest.NewRequest(http.MethodGet, "http://localhost:8080/test/endpoint", nil)
				req.RemoteAddr = "10.0.0.5:54321"
				// Invariant 8 / AUD-04: client header should NOT overwrite client_ip
				req.Header.Set("X-Forwarded-For", "203.0.113.195")
				w := httptest.NewRecorder()

				handler.ServeHTTP(w, req)

				require.Equal(t, tc.statusCode, w.Code)
				respReqID := w.Header().Get("X-Request-ID")
				require.NotEmpty(t, respReqID)

				line := buf.String()
				require.NotEmpty(t, line)

				var parsedMap map[string]interface{}
				err := json.Unmarshal(buf.Bytes(), &parsedMap)
				require.NoError(t, err)

				assert.Equal(t, "10.0.0.5", parsedMap["client_ip"], "client_ip must come from RemoteAddr, not X-Forwarded-For")
				assert.Equal(t, respReqID, parsedMap["request_id"])
				assert.Equal(t, float64(tc.statusCode), parsedMap["status"])
				assert.Equal(t, tc.decision, parsedMap["decision"])
				assert.Equal(t, tc.reason, parsedMap["reason_code"])
				assert.Equal(t, float64(42), parsedMap["snapshot_version"])

				dur := parsedMap["duration_ms"].(float64)
				assert.Greater(t, dur, 0.0)
			})
		}
	})
}

// sinkFake records Sink calls; onDenial/onCompletion are optional probes.
type sinkFake struct {
	mu           sync.Mutex
	denials      []CompletionEvent
	completions  []CompletionEvent
	onDenial     func()
	onCompletion func()
}

func (f *sinkFake) RecordDenial(ev *CompletionEvent) {
	if f.onDenial != nil {
		f.onDenial()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.denials = append(f.denials, *ev)
}

func (f *sinkFake) EnqueueCompletion(ev *CompletionEvent) {
	if f.onCompletion != nil {
		f.onCompletion()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.completions = append(f.completions, *ev)
}

// sinkProbeWriter records the calls made to the client-facing writer.
type sinkProbeWriter struct {
	header      http.Header
	headerCalls []int
	writes      int
}

func newSinkProbeWriter() *sinkProbeWriter { return &sinkProbeWriter{header: http.Header{}} }

func (p *sinkProbeWriter) Header() http.Header  { return p.header }
func (p *sinkProbeWriter) WriteHeader(code int) { p.headerCalls = append(p.headerCalls, code) }
func (p *sinkProbeWriter) Write(b []byte) (int, error) {
	p.writes++
	return len(b), nil
}
func (p *sinkProbeWriter) touched() bool { return len(p.headerCalls) > 0 || p.writes > 0 }

func sinkServe(t *testing.T, sink Sink, h http.HandlerFunc, w http.ResponseWriter, mutate func(*http.Request)) (*bytes.Buffer, http.ResponseWriter) {
	t.Helper()
	var buf bytes.Buffer
	var mw func(http.Handler) http.Handler
	if sink != nil {
		mw = AuditMiddleware(NewLogger(&buf), 42, WithSink(sink))
	} else {
		mw = AuditMiddleware(NewLogger(&buf), 42)
	}
	req := httptest.NewRequest(http.MethodGet, "http://localhost:8080/orders", nil)
	req.RemoteAddr = "10.0.0.5:54321"
	if mutate != nil {
		mutate(req)
	}
	if w == nil {
		w = httptest.NewRecorder()
	}
	mw(h).ServeHTTP(w, req)
	return &buf, w
}

func sinkLogMap(t *testing.T, buf *bytes.Buffer) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &m), buf.String())
	return m
}

func TestAuditMiddlewareSink(t *testing.T) {
	t.Run("allowed request enqueues one completion with new event id and same request id", func(t *testing.T) {
		sink := &sinkFake{}
		var standIn *CompletionEvent
		_, w := sinkServe(t, sink, func(w http.ResponseWriter, r *http.Request) {
			ac := FromContext(r.Context())
			ac.SetPrincipal("usr_1", "user", []string{"developer"})
			ac.SetRoute("route_orders", "orders")
			ac.SetDecision("allow", "ALLOWED_DEVELOPER_ORDERS")
			standIn = ac.ToCompletionEvent("GET", "10.0.0.5", 42)
			time.Sleep(time.Millisecond)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		}, nil, nil)

		require.Len(t, sink.denials, 0)
		require.Len(t, sink.completions, 1)
		ev := sink.completions[0]
		assert.Equal(t, EventTypeCompletion, ev.EventType)
		assert.Equal(t, "allow", ev.Decision)
		assert.Equal(t, http.StatusOK, ev.HTTPStatus)
		assert.Greater(t, ev.DurationMS, 0.0)
		_, err := uuid.Parse(ev.EventID)
		require.NoError(t, err)
		assert.Equal(t, w.Header().Get("X-Request-ID"), ev.RequestID)
		require.NotNil(t, standIn)
		assert.Equal(t, ev.RequestID, standIn.RequestID)
		assert.NotEqual(t, standIn.EventID, ev.EventID, "completion must carry its own event id (D-01)")
		assert.Equal(t, "10.0.0.5", ev.ClientIP)
	})

	t.Run("denied request records exactly one denial and no completion", func(t *testing.T) {
		sink := &sinkFake{}
		sinkServe(t, sink, func(w http.ResponseWriter, r *http.Request) {
			ac := FromContext(r.Context())
			ac.SetDecision("deny", "RATE_LIMIT_EXCEEDED")
			ac.SetErrorCode("RATE_LIMIT_EXCEEDED")
			time.Sleep(time.Millisecond)
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte("slow down"))
		}, nil, nil)

		require.Len(t, sink.denials, 1)
		require.Len(t, sink.completions, 0)
		ev := sink.denials[0]
		assert.Equal(t, EventTypeDenial, ev.EventType)
		assert.Equal(t, "deny", ev.Decision)
		assert.Equal(t, "RATE_LIMIT_EXCEEDED", ev.ReasonCode)
		assert.Equal(t, "RATE_LIMIT_EXCEEDED", ev.ErrorCode)
		assert.Equal(t, http.StatusTooManyRequests, ev.HTTPStatus)
		assert.Greater(t, ev.DurationMS, 0.0)
	})

	t.Run("implicit 200 deny without WriteHeader is recorded once by the defer", func(t *testing.T) {
		sink := &sinkFake{}
		sinkServe(t, sink, func(w http.ResponseWriter, r *http.Request) {
			FromContext(r.Context()).SetDecision("deny", "POLICY_DENY")
			_, _ = w.Write([]byte("body"))
		}, nil, nil)
		require.Len(t, sink.denials, 1)
		assert.Equal(t, http.StatusOK, sink.denials[0].HTTPStatus)
		assert.Len(t, sink.completions, 0)
	})

	t.Run("hook-recorded denial is not recorded again by the defer", func(t *testing.T) {
		sink := &sinkFake{}
		sinkServe(t, sink, func(w http.ResponseWriter, r *http.Request) {
			FromContext(r.Context()).SetDecision("deny", "POLICY_DENY")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("no"))
		}, nil, nil)
		assert.Len(t, sink.denials, 1)
	})

	t.Run("legacy status guess preserved when no handler set a decision", func(t *testing.T) {
		for _, tc := range []struct {
			status int
			reason string
		}{
			{http.StatusOK, "HTTP_200"},
			{http.StatusForbidden, "HTTP_403"},
			{http.StatusFound, "HTTP_302"},
		} {
			t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
				sink := &sinkFake{}
				buf, _ := sinkServe(t, sink, func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(tc.status)
				}, nil, nil)
				require.Len(t, sink.denials, 1)
				assert.Len(t, sink.completions, 0)
				assert.Equal(t, "deny", sink.denials[0].Decision)
				assert.Equal(t, tc.reason, sink.denials[0].ReasonCode)
				assert.Equal(t, tc.reason, sinkLogMap(t, buf)["reason_code"])
			})
		}
	})

	t.Run("explicit deny stays deny on a 200", func(t *testing.T) {
		sink := &sinkFake{}
		sinkServe(t, sink, func(w http.ResponseWriter, r *http.Request) {
			FromContext(r.Context()).SetDecision("deny", "POLICY_DENY")
			w.WriteHeader(http.StatusOK)
		}, nil, nil)
		require.Len(t, sink.denials, 1)
		assert.Equal(t, "deny", sink.denials[0].Decision)
		assert.Equal(t, http.StatusOK, sink.denials[0].HTTPStatus)
	})

	t.Run("1xx informational response does not trigger the hook or mark the header written", func(t *testing.T) {
		sink := &sinkFake{}
		probe := newSinkProbeWriter()
		sinkServe(t, sink, func(w http.ResponseWriter, r *http.Request) {
			FromContext(r.Context()).SetDecision("deny", "POLICY_DENY")
			w.WriteHeader(http.StatusEarlyHints)
			assert.Len(t, sink.denials, 0, "103 must not trigger the hook")
			w.WriteHeader(http.StatusForbidden)
		}, probe, nil)
		require.Len(t, sink.denials, 1)
		assert.Equal(t, http.StatusForbidden, sink.denials[0].HTTPStatus)
		assert.Equal(t, []int{http.StatusEarlyHints, http.StatusForbidden}, probe.headerCalls)
	})

	t.Run("durable event is normalized while stdout keeps its field set", func(t *testing.T) {
		sink := &sinkFake{}
		method := strings.Repeat("M", 28)
		buf, _ := sinkServe(t, sink, func(w http.ResponseWriter, r *http.Request) {
			FromContext(r.Context()).SetDecision("deny", "POLICY_DENY")
			w.WriteHeader(http.StatusForbidden)
		}, nil, func(r *http.Request) {
			r.Method = method
			r.URL.Path = "/or\x00ders"
		})
		require.Len(t, sink.denials, 1)
		ev := sink.denials[0]
		assert.NotContains(t, ev.CanonicalPath, "\x00")
		assert.Equal(t, "/orders", ev.CanonicalPath)
		assert.LessOrEqual(t, len(ev.HTTPMethod), 16)

		m := sinkLogMap(t, buf)
		for _, f := range []string{
			"event_id", "timestamp", "request_id", "principal_id", "principal_kind",
			"principal_roles", "client_ip", "http_method", "canonical_path", "route_id",
			"service_id", "decision", "reason_code", "http_status", "duration_ms",
			"snapshot_version", "error_code",
		} {
			assert.Contains(t, m, f)
		}
		assert.NotContains(t, m, "event_type")
		assert.Equal(t, method, m["http_method"], "stdout keeps the raw method")
	})

	t.Run("no sink behaves as before", func(t *testing.T) {
		buf, w := sinkServe(t, nil, func(w http.ResponseWriter, r *http.Request) {
			FromContext(r.Context()).SetDecision("allow", "ALLOWED")
			w.WriteHeader(http.StatusOK)
		}, nil, nil)
		assert.Equal(t, http.StatusOK, w.(*httptest.ResponseRecorder).Code)
		assert.Equal(t, "allow", sinkLogMap(t, buf)["decision"])
	})
}

func TestDenialRecordedBeforeResponse(t *testing.T) {
	probe := newSinkProbeWriter()
	sink := &sinkFake{}
	var touchedAtRecord bool
	sink.onDenial = func() { touchedAtRecord = probe.touched() }

	sinkServe(t, sink, func(w http.ResponseWriter, r *http.Request) {
		FromContext(r.Context()).SetDecision("deny", "DENIED_FORBIDDEN")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("forbidden"))
	}, probe, nil)

	require.Len(t, sink.denials, 1)
	assert.False(t, touchedAtRecord, "the denial must be handed to the sink before any header or byte is written")
	assert.Equal(t, []int{http.StatusForbidden}, probe.headerCalls)
	assert.Equal(t, 1, probe.writes)
}

func TestCompletionKeepsAllowOnBackendError(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusInternalServerError, http.StatusBadGateway} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			sink := &sinkFake{}
			buf, _ := sinkServe(t, sink, func(w http.ResponseWriter, r *http.Request) {
				FromContext(r.Context()).SetDecision("allow", "ALLOWED_DEVELOPER_ORDERS")
				w.WriteHeader(status)
			}, nil, nil)
			require.Len(t, sink.denials, 0)
			require.Len(t, sink.completions, 1)
			ev := sink.completions[0]
			assert.Equal(t, "allow", ev.Decision)
			assert.Equal(t, status, ev.HTTPStatus)
			assert.Equal(t, EventTypeCompletion, ev.EventType)
			assert.Equal(t, "ALLOWED_DEVELOPER_ORDERS", ev.ReasonCode)
			assert.Equal(t, "allow", sinkLogMap(t, buf)["decision"])
		})
	}
}

func TestCompletionNonBlocking(t *testing.T) {
	probe := newSinkProbeWriter()
	sink := &sinkFake{}
	var wroteBeforeEnqueue bool
	sink.onCompletion = func() { wroteBeforeEnqueue = len(probe.headerCalls) == 1 && probe.writes == 1 }

	sinkServe(t, sink, func(w http.ResponseWriter, r *http.Request) {
		FromContext(r.Context()).SetDecision("allow", "ALLOWED")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}, probe, nil)

	assert.True(t, wroteBeforeEnqueue, "completion is enqueued after the response was written")
	assert.Len(t, sink.completions, 1)
	assert.Len(t, sink.denials, 0)
}

type sinkTimeoutErr struct{}

func (sinkTimeoutErr) Error() string   { return "i/o timeout" }
func (sinkTimeoutErr) Timeout() bool   { return true }
func (sinkTimeoutErr) Temporary() bool { return true }

func TestWrapUpstreamErrorHandler(t *testing.T) {
	tooLargeAndTimeout := fmt.Errorf("both: %w", errors.Join(&http.MaxBytesError{Limit: 1}, sinkTimeoutErr{}))
	canceledAndTimeout := errors.Join(context.Canceled, sinkTimeoutErr{})

	cases := []struct {
		name string
		err  error
		want string
	}{
		{"max bytes", &http.MaxBytesError{Limit: 10}, "REQUEST_BODY_TOO_LARGE"},
		{"max bytes wrapped", fmt.Errorf("x: %w", &http.MaxBytesError{Limit: 10}), "REQUEST_BODY_TOO_LARGE"},
		{"canceled", context.Canceled, "CLIENT_CANCELED"},
		{"canceled wrapped", fmt.Errorf("proxy: %w", context.Canceled), "CLIENT_CANCELED"},
		{"deadline", context.DeadlineExceeded, "UPSTREAM_TIMEOUT"},
		{"net timeout", sinkTimeoutErr{}, "UPSTREAM_TIMEOUT"},
		{"refused", errors.New("dial tcp: connection refused"), "UPSTREAM_UNAVAILABLE"},
		{"max bytes beats timeout", tooLargeAndTimeout, "REQUEST_BODY_TOO_LARGE"},
		{"canceled beats timeout", canceledAndTimeout, "CLIENT_CANCELED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotW http.ResponseWriter
			var gotErr error
			wrapped := WrapUpstreamErrorHandler(func(w http.ResponseWriter, r *http.Request, err error) {
				gotW, gotErr = w, err
				w.WriteHeader(http.StatusBadGateway)
			})
			ctx, ac := WithAuditContext(context.Background(), "req-1")
			req := httptest.NewRequest(http.MethodGet, "/x", nil).WithContext(ctx)
			rec := httptest.NewRecorder()
			wrapped(rec, req, tc.err)
			assert.Equal(t, tc.want, ac.ErrorCode)
			assert.Same(t, http.ResponseWriter(rec), gotW)
			assert.Equal(t, tc.err, gotErr)
			assert.Equal(t, http.StatusBadGateway, rec.Code)
		})
	}

	t.Run("nil audit context is safe", func(t *testing.T) {
		called := false
		wrapped := WrapUpstreamErrorHandler(func(http.ResponseWriter, *http.Request, error) { called = true })
		wrapped(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil), errors.New("boom"))
		assert.True(t, called)
	})

	t.Run("nil next is safe and writes nothing", func(t *testing.T) {
		ctx, ac := WithAuditContext(context.Background(), "req-1")
		req := httptest.NewRequest(http.MethodGet, "/x", nil).WithContext(ctx)
		rec := httptest.NewRecorder()
		require.NotPanics(t, func() { WrapUpstreamErrorHandler(nil)(rec, req, errors.New("boom")) })
		assert.Equal(t, "UPSTREAM_UNAVAILABLE", ac.ErrorCode)
		assert.Equal(t, 0, rec.Body.Len())
	})

	t.Run("end to end keeps allow with upstream error code", func(t *testing.T) {
		sink := &sinkFake{}
		sinkServe(t, sink, func(w http.ResponseWriter, r *http.Request) {
			FromContext(r.Context()).SetDecision("allow", "ALLOWED_DEVELOPER_ORDERS")
			h := WrapUpstreamErrorHandler(func(w http.ResponseWriter, r *http.Request, err error) {
				w.WriteHeader(http.StatusBadGateway)
			})
			h(w, r, errors.New("dial tcp: connection refused"))
		}, nil, nil)
		require.Len(t, sink.completions, 1)
		require.Len(t, sink.denials, 0)
		ev := sink.completions[0]
		assert.Equal(t, "allow", ev.Decision)
		assert.Equal(t, http.StatusBadGateway, ev.HTTPStatus)
		assert.Equal(t, "UPSTREAM_UNAVAILABLE", ev.ErrorCode)
	})
}

func TestToCompletionEventTypeAndNormalize(t *testing.T) {
	_, ac := WithAuditContext(context.Background(), "req-1")
	ac.SetPrincipal(strings.Repeat("p", 200), "user", []string{"dev"})
	ac.SetCanonicalPath("/a\x00b")

	e1 := ac.ToCompletionEvent("GET", "10.0.0.5", 7)
	e2 := ac.ToCompletionEvent("GET", "10.0.0.5", 7)
	require.NotNil(t, e1)
	assert.Equal(t, EventTypeDecision, e1.EventType)
	assert.NotEqual(t, e1.EventID, e2.EventID)
	assert.Equal(t, "/ab", e1.CanonicalPath)
	assert.Len(t, e1.PrincipalID, 128)
	assert.Equal(t, "deny", e1.Decision)
	assert.Equal(t, "UNKNOWN_REASON", e1.ReasonCode)
	assert.Equal(t, int64(7), e1.SnapshotVersion)

	ac.SetDecision("allow", "")
	assert.Equal(t, "ALLOWED", ac.ToCompletionEvent("GET", "10.0.0.5", 0).ReasonCode)
}

// flushProbeWriter is a sinkProbeWriter that also implements http.Flusher.
type flushProbeWriter struct {
	*sinkProbeWriter
	flushes int
}

func (f *flushProbeWriter) Flush() { f.flushes++ }

// WR-03: the denial must be handed to the sink before the first response byte on every
// path, including a handler that never calls WriteHeader.
func TestDenialRecordedBeforeImplicitHeader(t *testing.T) {
	deny := func(w http.ResponseWriter, r *http.Request) {
		FromContext(r.Context()).SetDecision("deny", "DENIED_DEFAULT")
	}

	t.Run("Write without WriteHeader", func(t *testing.T) {
		probe := newSinkProbeWriter()
		sink := &sinkFake{}
		var touchedAtRecord bool
		sink.onDenial = func() { touchedAtRecord = probe.touched() }

		sinkServe(t, sink, func(w http.ResponseWriter, r *http.Request) {
			deny(w, r)
			_, _ = w.Write([]byte("body"))
			_, _ = w.Write([]byte("more"))
		}, probe, nil)

		require.Len(t, sink.denials, 1, "recorded once, from the first byte")
		assert.False(t, touchedAtRecord, "the denial must be recorded before the first response byte")
		assert.Equal(t, http.StatusOK, sink.denials[0].HTTPStatus)
		assert.Equal(t, 2, probe.writes)
	})

	t.Run("Flush without WriteHeader", func(t *testing.T) {
		probe := &flushProbeWriter{sinkProbeWriter: newSinkProbeWriter()}
		sink := &sinkFake{}
		var flushedAtRecord int
		sink.onDenial = func() { flushedAtRecord = probe.flushes }

		sinkServe(t, sink, func(w http.ResponseWriter, r *http.Request) {
			deny(w, r)
			w.(http.Flusher).Flush()
		}, probe, nil)

		require.Len(t, sink.denials, 1)
		assert.Equal(t, 0, flushedAtRecord, "the denial must be recorded before the implicit-200 flush")
		assert.Equal(t, 1, probe.flushes)
	})

	t.Run("1xx then Write", func(t *testing.T) {
		probe := newSinkProbeWriter()
		sink := &sinkFake{}
		var writesAtRecord = -1
		sink.onDenial = func() { writesAtRecord = probe.writes }

		sinkServe(t, sink, func(w http.ResponseWriter, r *http.Request) {
			deny(w, r)
			w.WriteHeader(http.StatusEarlyHints)
			_, _ = w.Write([]byte("body"))
		}, probe, nil)

		require.Len(t, sink.denials, 1)
		assert.Equal(t, 0, writesAtRecord)
		assert.Equal(t, http.StatusOK, sink.denials[0].HTTPStatus, "the informational status is not the final one")
		assert.Equal(t, []int{http.StatusEarlyHints}, probe.headerCalls, "the 1xx is still forwarded")
	})

	t.Run("allowed Write is unchanged", func(t *testing.T) {
		probe := newSinkProbeWriter()
		sink := &sinkFake{}
		sinkServe(t, sink, func(w http.ResponseWriter, r *http.Request) {
			FromContext(r.Context()).SetDecision("allow", "ALLOWED")
			_, _ = w.Write([]byte("ok"))
		}, probe, nil)
		assert.Empty(t, sink.denials)
		require.Len(t, sink.completions, 1)
		assert.Equal(t, http.StatusOK, sink.completions[0].HTTPStatus)
	})

	t.Run("no sink keeps the implicit 200", func(t *testing.T) {
		probe := newSinkProbeWriter()
		buf, _ := sinkServe(t, nil, func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("ok"))
		}, probe, nil)
		assert.Empty(t, probe.headerCalls, "without a sink the writer must not add an explicit WriteHeader")
		assert.EqualValues(t, 200, sinkLogMap(t, buf)["http_status"])
	})
}
