---
phase: quick-261117-rvo
plan: 01
type: execute
wave: 1
depends_on: []
files_modified:
  - internal/proxy/server.go
  - internal/proxy/proxy.go
  - internal/proxy/body_limit_test.go
  - internal/telemetry/metrics.go
  - internal/telemetry/metrics_test.go
autonomous: true
requirements: [GW-01]
must_haves:
  truths:
    - "A request with Content-Length > MaxBodyBytes gets 413 problem+json before any handler/backend is reached (user and workload listeners)"
    - "A chunked body over the limit never yields a 2xx from the backend; the reverse proxy maps *http.MaxBytesError to 413"
    - "A body of exactly MaxBodyBytes is still forwarded"
    - "413 rejections are counted via the pre-middleware rejection recorder with reason body_too_large (closed set updated)"
  artifacts:
    - path: "internal/proxy/body_limit_test.go"
      provides: "red-first regression tests for GW-01"
    - path: "internal/proxy/server.go"
      provides: "Content-Length pre-check on all 3 ingress handlers"
  key_links:
    - from: "internal/proxy/server.go"
      to: "WriteProblemDetails"
      via: "413 with type https://aegis.local/errors/body-too-large"
      pattern: "body-too-large"
    - from: "internal/proxy/proxy.go"
      to: "http.MaxBytesError"
      via: "errors.As in both ErrorHandlers"
      pattern: "MaxBytesError"
---

<objective>
Fix GW-01 (UAT test 13): the 1 MiB body limit is not enforced; a 2,000,000-byte POST /api/payments reached the backend and returned 200.

Purpose: enforce the body limit at ingress (Content-Length) and for unknown-length bodies (reverse proxy error mapping), with consistent rejection metrics.
Output: server.go pre-check, proxy.go ErrorHandler mapping, closed-set reason body_too_large, regression tests.
</objective>

<execution_context>
@$HOME/.claude/get-shit-done/workflows/execute-plan.md
@$HOME/.claude/get-shit-done/templates/summary.md
</execution_context>

<context>
@.planning/STATE.md
@.planning/phases/01-mvp-secure-vertical-slice/01-UAT.md
@internal/proxy/server.go
@internal/proxy/proxy.go
@internal/proxy/errors.go
@internal/proxy/server_test.go

<interfaces>
internal/proxy/errors.go:
func WriteProblemDetails(w http.ResponseWriter, status int, title, detail, problemType, requestID string)

internal/proxy/server.go: three ingress handlers set reqID (uuid) then X-Request-ID; NewServer (~line 30, no rejection holder), NewDualServer userIngress (~129) and workloadIngress (~162, after the Authorization/ambiguous_credentials check and header check). Existing 431 path in the DualServer handlers: rejections.record("header_too_large") then WriteProblemDetails(...431..., reqID). MaxBytesReader is applied right after the header check in each.

internal/proxy/proxy.go: two ErrorHandlers (NewReverseProxyWithMTLS ~line 84, NewReverseProxy ~line 141), both writing 502 bad-gateway with reqID.

internal/telemetry/metrics.go: closed set `rejectionReasons` (line ~57) and `RecordRejection` switch (line ~491); metrics_test.go ~line 210 and ~231 enumerate the reasons (drift guard).
</interfaces>
</context>

<tasks>

<task type="auto" tdd="true">
  <name>Task 1: Red-first regression tests for GW-01</name>
  <files>internal/proxy/body_limit_test.go</files>
  <behavior>
    - Backend httptest server replies 200 WITHOUT reading the body and counts invocations; proxy built with NewReverseProxyWithMTLS (nil transport) pointed at it, wrapped as the handler passed to NewDualServer with cfg.MaxBodyBytes = 1048576 (see server_test.go for cfg shape and the fakeRecorder in limiter_test.go).
    - Content-Length 2,000,000 on UserHandler and WorkloadHandler (httptest.NewRequest with a bytes reader gives ContentLength) -> 413, Content-Type application/problem+json, JSON type https://aegis.local/errors/body-too-large, title "Payload Too Large", instance equals the X-Request-ID response header; backend invocation count == 0; fakeRecorder.got() == ["body_too_large"].
    - Chunked 2,000,000 bytes (ContentLength = -1, e.g. wrap body in io.NopCloser(io.MultiReader) or use a real httptest server + client with a reader of unknown length) through both handlers -> status 413 (hard requirement is: never a 2xx; assert 413 first, and if the Go transport races with the backend's early reply, see Task 2 fallback).
    - Exactly 1 MiB (1048576 bytes) with Content-Length -> forwarded, backend invoked once, 200.
    - Table-drive over user/workload handler so both are covered.
  </behavior>
  <action>Create body_limit_test.go in package proxy using testify (already used). Prefer exercising real servers (httptest.NewServer wrapping ds.UserHandler()/ds.WorkloadHandler() with a real client) for the chunked case so transport behaviour is genuine; the Content-Length case may use httptest.NewRecorder. Run `go test -short -count=1 -run BodyLimit ./internal/proxy` and confirm the new tests FAIL (red) before Task 2; do not commit a green-only test. Name tests TestBodyLimit_*.</action>
  <verify>
    <automated>go test -short -count=1 -run BodyLimit ./internal/proxy 2>&1 | grep -c "^--- FAIL" | grep -v '^0$'</automated>
  </verify>
  <done>Test file compiles; oversize Content-Length and chunked tests fail against current code; exact-1MiB test passes.</done>
</task>

<task type="auto" tdd="true">
  <name>Task 2: Enforce limit at ingress, map MaxBytesError, extend closed reason set</name>
  <files>internal/proxy/server.go, internal/proxy/proxy.go, internal/telemetry/metrics.go, internal/telemetry/metrics_test.go</files>
  <action>
1. server.go: in all three ingress handlers (NewServer, userIngress, workloadIngress), immediately after the header-size check and BEFORE MaxBytesReader/auth/policy/forwarding, add: if r.ContentLength > cfg.MaxBodyBytes then (DualServer handlers only) rejections.record("body_too_large") and WriteProblemDetails(w, http.StatusRequestEntityTooLarge, "Payload Too Large", "Request body size exceeds limit", "https://aegis.local/errors/body-too-large", reqID); return. NewServer has no rejection holder, so it only writes the 413 (matches how its 431 path behaves). Keep MaxBytesReader for unknown-length bodies. Factor a small unexported helper (e.g. writeBodyTooLarge) if it reduces triplication. Per GW-01 gap.
2. proxy.go: in both ErrorHandlers, before the 502 write, `var mbe *http.MaxBytesError; if errors.As(err, &mbe)` then write the same 413 problem JSON (same type/title/detail/reqID) and return. Add "errors" import.
3. Closed set: add "body_too_large" to rejectionReasons and to the RecordRejection switch in internal/telemetry/metrics.go; update metrics_test.go drift-guard enumerations (~lines 210 and 231) to include it. No other reason codes added.
4. If the chunked test still returns 2xx because the backend replies before the transport hits the read error (response wins the race), add a fallback in proxy.go: wrap pr.In.Body/Out.Body in a reader that sets an atomic flag when it returns *http.MaxBytesError, and add a ModifyResponse that returns that error when the flag is set, so ErrorHandler produces 413. Only add this if the test demonstrates the race; do not add it speculatively.
Do not alter any other behaviour.</action>
  <verify>
    <automated>cd /home/logan78/Desktop/Aegis && go build ./... && go vet ./... && go test -short -count=1 ./... && go test -race -short -count=1 ./internal/proxy ./cmd/gateway</automated>
  </verify>
  <done>All TestBodyLimit_* pass for user and workload handlers; existing TestDualServer_BodySizeBounding and metrics drift tests pass; build, vet, full short tests and -race on proxy and cmd/gateway are green.</done>
</task>

</tasks>

<threat_model>
## Trust Boundaries

| Boundary | Description |
|----------|-------------|
| client -> gateway ingress | untrusted Content-Length / chunked body size crosses here |
| gateway -> backend | oversized body must not be forwarded |

## STRIDE Threat Register

| Threat ID | Category | Component | Disposition | Mitigation Plan |
|-----------|----------|-----------|-------------|-----------------|
| T-rvo-01 | D | ingress handlers | mitigate | Reject Content-Length > MaxBodyBytes with 413 before auth/forwarding; MaxBytesReader retained for chunked |
| T-rvo-02 | T | reverse proxy ErrorHandler | mitigate | Map *http.MaxBytesError to 413 so oversize chunked bodies never surface as backend 2xx |
| T-rvo-03 | R | rejection metrics | mitigate | Count body_too_large via the pre-middleware RejectionRecorder (closed set, drift guard updated) |
</threat_model>

<verification>
go build ./... && go vet ./... && go test -short -count=1 ./... && go test -race -short -count=1 ./internal/proxy ./cmd/gateway
</verification>

<success_criteria>
- 2,000,000-byte Content-Length POST returns 413 problem+json and backend is never invoked (both listeners)
- Chunked 2,000,000-byte POST never returns backend 2xx (413)
- Exactly 1 MiB still forwarded
- body_too_large counted in the closed rejection set; all tests green
</success_criteria>

<output>
Create `.planning/quick/261117-rvo-fix-gw-01-1-mib-body-limit-not-enforced-/261117-rvo-SUMMARY.md` when done
</output>
