# Phase 12: Close gaps N1, DIST-01, DIST-02 - Research

**Researched:** 2026-11-16
**Domain:** Go gateway/control-plane gap closure: K8s NetworkPolicy, two-phase graceful drain, Prometheus wiring, audit worker robustness, dev PKI SANs
**Confidence:** HIGH (everything below was read from the repo at HEAD fe40cf5; only K8s/HAProxy behavior claims are from training and are tagged)

No CONTEXT.md exists for Phase 12. `.planning/v1.0-MILESTONE-AUDIT.md` (re-audit 3) and the ROADMAP Phase 12 goal are the decision source. No CLAUDE.md in the repo.

<user_constraints>
## User Constraints (no 12-CONTEXT.md; derived from ROADMAP Phase 12 + audit)

### Locked Decisions (ROADMAP goal, treat as locked)
- N1: postgres NetworkPolicy must admit the gateway pod; tests/manifests cross-checks NetworkPolicy peers against pods that actually connect.
- DIST-01: two-phase drain (fail /readyz -> propagation delay while serving -> drain in-flight), :9443 readiness follows same state, K8s preStop + terminationGracePeriodSeconds cover drain + audit flush + shutdown.
- DIST-02: the six recorders get production callers; add request-latency histogram, no PII labels.
- Audit worker logs/retries non-corrupt read errors (no silent cursor advance/prune); completion-queue overflow sheds/counts without fleet-wide /readyz fault.
- W2: gateway-server cert SANs include aegis-gateway, aegis-gateway.aegis-system, aegis-gateway.aegis-system.svc.
- Hermetic tests; `go build`, `go vet`, `go test -short ./...` stay green. Docker must NOT be started.

### Claude's Discretion
Env var names, default values, call-site placement, metric names/buckets, test locations.

### Deferred Ideas (OUT OF SCOPE)
Live Docker/K8s UAT, W1 (control-plane replica state), W3, OPS-03 jti display, K8s seed job, postgres TLS in cluster, test-copy AuditMiddleware without WithSink (tech debt, not in the Phase 12 goal).
</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|------------------|
| DIST-01 | Graceful two-phase drain | Sections "Drain", compose/K8s grace math |
| DIST-02 | Prometheus metrics wired | Section "DIST-02 call sites" |
| AUD-01 / AUD-03 | Audit rows reach Postgres in K8s; worker doesn't lose frames | N1 fix, worker hardening |
| REV-03 | Audit trail of revocation/quarantine in K8s | Unblocked by N1 |
</phase_requirements>

## Summary

All five items are small, well-localized edits. The only real design risk is item 4b: **a locked Phase 8 decision (D-12) says a completion that cannot be written (queue full, hard limit, write error) makes the gateway treat itself as saturated and 503 allowed traffic.** The Phase 12 goal ("no fleet-wide /readyz fault from a burst") contradicts D-12 for the queue-full case. The planner must treat this as an explicit, recorded amendment of D-12 (queue_full only; hard_limit and write_error stay fail-closed). Note the committer already self-recovers a queue_full fault when the queue drains below half (`tryRecover`, committer.go:~320), so today's behavior is a transient unready, not permanent; the change is to stop gating on it at all.

N1 is a one-line selector add. The audit worker is a sidecar in the gateway pod (label `app: aegis-gateway`), and the gateway egress policy already allows postgres:5432 (gateway/networkpolicy.yaml rule 4); only the postgres ingress side is missing. [VERIFIED: repo grep]

Drain is currently single-phase: `main.go:1005-1012` does `SetDraining()` then immediately `dualServer.Shutdown`. HAProxy in compose checks `/readyz` every 1s, fall 2 (so ~2-3s to eject). :9443 in HAProxy is a plain TCP check (`mode tcp`, no httpchk), so it cannot observe `/readyz`; use HAProxy `track` to make workload servers follow the :8080 check state. [VERIFIED: deployments/compose/haproxy/haproxy.cfg]

**Primary recommendation:** Add `AEGIS_DRAIN_PROPAGATION_DELAY` (code default 0, set explicitly to 5s in compose and 10s in the K8s configmap); extract the shutdown sequence into a testable function; fix grace math (compose 45s -> 60s, K8s add `terminationGracePeriodSeconds: 60` + preStop); give each recorder a caller and add an AST guard test.

## Architectural Responsibility Map

| Capability | Primary Tier | Secondary Tier | Rationale |
|------------|-------------|----------------|-----------|
| Postgres reachability for audit worker | K8s NetworkPolicy (infra) | tests/manifests | Ingress on postgres pod must name the connecting pod label |
| Drain / readiness | API/Backend (gateway process) | LB (HAProxy) / kube endpoints | Gateway owns the state; LB consumes `/readyz` |
| Metrics emission | API/Backend (gateway, control plane) | Prometheus scrape | Recorders live in `internal/telemetry`, called from request path |
| Audit WAL drain to Postgres | Audit worker sidecar | Postgres | Reads PVC WAL, writes rows |
| Cert SANs | Dev PKI generator (`scripts/certificates`) | committed `deployments/certs` | Hostname verification by in-cluster clients |

## Standard Stack

No new dependencies. Everything uses what the repo already has. [VERIFIED: go.mod / imports]

| Library | Purpose |
|---------|---------|
| `github.com/prometheus/client_golang` (already used) | Add `HistogramVec` for request latency |
| `gopkg.in/yaml.v3` + testify (already used by tests/manifests) | Manifest cross-check |
| stdlib `go/ast`, `go/parser` (already used by cmd/gateway/wiring_test.go) | Recorder caller guard |
| stdlib `crypto/x509` | SAN assertion in cert test |

**Package Legitimacy Audit:** no external packages are installed in this phase. slopcheck not run; N/A.

## Architecture Patterns

### Drain sequence (DIST-01)

```
SIGTERM -> drainingState.SetDraining()   /readyz = 503 DRAINING; all other paths still served
        -> sleep(propagationDelay)       (interruptible by 2nd signal / ctx)
        -> dualServer.Shutdown(drainCtx) (drainTimeout, both listeners)
        -> auditPipeline.Shutdown (5s) -> streamClient.Stop -> metricsServer.Shutdown (5s) -> spool.Close
```

Implementation guidance:
- Add to `cmd/gateway/main.go` an env parse beside `AEGIS_DRAIN_TIMEOUT` (line ~989): `AEGIS_DRAIN_PROPAGATION_DELAY` (time.ParseDuration, `>= 0`, default 0).
- Extract the post-signal block into a function (suggest `proxy.TwoPhaseDrain(ctx, state *DrainingState, delay time.Duration, shutdown func(context.Context) error, drainTimeout time.Duration) error` in `internal/proxy/`, or an unexported helper in cmd/gateway) so a hermetic test can drive it with short durations and assert: (a) `/readyz` is 503 immediately, (b) a non-probe request sent during the delay window still returns 200, (c) `Shutdown` is not invoked before `delay` elapses, (d) an in-flight request finishes during the shutdown phase, (e) delay 0 skips the wait. Existing test `cmd/gateway/main_test.go` (drain test around line 68) shows the httptest pattern.
- A second SIGINT/SIGTERM during the delay should skip the remaining delay (signal channel has buffer 1; re-`Notify` is fine). Optional.
- :9443 follows the same state: the workload listener has no probe path (mTLS client cert required, so no kubelet/HAProxy HTTP probe is possible). Do two things: (1) in the workload handler chain, when `drainingState.IsDraining()` set `Connection: close` on responses so keep-alive clients reconnect to another replica (needs the state passed to `NewDualServer` or wrapped around `workloadMetricsHandler` in main.go; a small wrapper func is enough and avoids changing the `NewDualServer` signature); (2) in `deployments/compose/haproxy/haproxy.cfg` change workload backend servers to `server gateway-1 gateway-1:9443 track aegis_http_back/gateway-1` (HAProxy `track` makes the workload server inherit the :8080 /readyz check) [CITED: HAProxy docs, `track` server keyword; ASSUMED exact syntax, verify with `haproxy -c` if available, otherwise by review]. In K8s the single readinessProbe on :8080 already governs both ports (same pod).
- Do NOT close the :9443 listener during phase 1: that would reject in-flight-adjacent new connections that LB hasn't yet ejected. `Shutdown` in phase 2 closes both.

### Grace budget (DIST-01)

Worst case = preStop sleep + propagation delay + drain timeout (30s default) + audit flush (5s, main.go) + metrics shutdown (5s) + margin.

| Env | Delay | preStop | Sum | Set grace to |
|-----|-------|---------|-----|--------------|
| Compose distributed (3 gateways, lines 171/234/297 `stop_grace_period: 45s`) | `AEGIS_DRAIN_PROPAGATION_DELAY=5s` (HAProxy inter 1s fall 2 = ~3s ejection, so 5s is enough) | none | 5+30+5+5 = 45 (zero margin) | **60s** |
| K8s gateway StatefulSet | configmap `AEGIS_DRAIN_PROPAGATION_DELAY: "10s"` (readiness periodSeconds 5, failureThreshold 1 + endpoint propagation) | exec `sleep 5` (alpine image has `sh`, verified in deployments/compose/Dockerfile) | 5+10+30+5+5 = 55 | **`terminationGracePeriodSeconds: 70`** |

K8s notes: preStop time counts against the grace period [CITED: kubernetes.io pod lifecycle, ASSUMED recalled]. The preStop sleep covers the race where the endpoint removal and SIGTERM begin simultaneously; the in-process delay covers readiness-propagation to external LBs. Use `lifecycle.preStop.exec.command: ["sh","-c","sleep 5"]` (native `sleep` handler needs newer K8s; exec is portable). Add the `terminationGracePeriodSeconds` to the pod spec (default is 30s, which is the audited bug). Mounting is read-only root FS; `sleep` needs no writes. The sidecar `audit-worker` gets SIGTERM at the same time as the gateway and exits early; remaining WAL stays on the PVC and is drained on next start (cursor persisted), acceptable. Also set compose `AEGIS_DRAIN_PROPAGATION_DELAY` in mvp/hardened only if desired (no LB; leave unset).
- Add tests/manifests assertions: statefulset `terminationGracePeriodSeconds >= preStopSleep + delay(configmap) + drain(configmap) + 10`; preStop present on container `gateway`; `AEGIS_DRAIN_PROPAGATION_DELAY` key is read by source (the existing `TestManifestEnvKeysAreReadBySource` scans gateway source for env keys, so the new key must be read via `os.Getenv` in cmd/gateway or internal/config).
- Add a compose test (tests/compose/compose_test.go parses compose YAML, Docker not needed) asserting `stop_grace_period >= delay + drain + 10s` for gateway-1..3.

### N1 NetworkPolicy fix and cross-check

Edit `deployments/kubernetes/base/postgres/networkpolicy.yaml`: add `podSelector: matchLabels: app: aegis-gateway` to the ingress `from` list. Keep `aegis-audit-worker` only if some manifest still labels pods that way (grep shows none; the standalone worker Deployment was removed in Phase 11), otherwise replace it. [VERIFIED: base/ file listing has no audit-worker dir]

Cross-check test (new, in `tests/manifests/crosscheck_test.go`, reuse `baseDocs`, `podSpec`, `list`, `containerEnvNames`, `configMapData`; these helpers already exist):
1. For every workload doc (Deployment/StatefulSet) collect pod labels `spec.template.metadata.labels`.
2. Determine "connects to postgres" per container: any env name (incl. via `envFrom` configMap keys, since control-plane `AEGIS_DB_HOST` lives in its configmap) matching `AEGIS_DB_*`/`*DATABASE*`/`*DSN*`, with value/host containing `postgres`.
   Likewise "connects to redis" via `AEGIS_REDIS_ADDR` (gateway, control-plane configmaps) and "connects to control-plane gRPC" via `AEGIS_CONTROL_PLANE_GRPC_ADDR`.
3. Assert the target service's NetworkPolicy (podSelector `app: postgres` / `app: redis` / `app: aegis-control-plane`) has an ingress `from.podSelector.matchLabels` that selects each connecting pod's labels on the right port, AND the connecting pod's own NetworkPolicy egress allows that peer/port (gateway policy has egress for all three today; control-plane egress for redis+postgres).
4. Current state check, N1 is the only mismatch: redis ingress admits gateway + control-plane (OK); control-plane ingress 9090 admits `app: aegis-gateway` (OK); postgres ingress admits control-plane (OK) and a nonexistent audit-worker pod (BUG, missing gateway). [VERIFIED: read all four policies]
5. Must fail on the pre-fix tree (write the test first, confirm red, then fix the YAML).
Also account for the production overlay: `overlays/production/networkpolicy-prod.yaml` only annotates, so no peer changes; load overlay docs only if kustomize is not required (the test harness reads YAML directly, and `manifest-build` needs kustomize, so keep the test on `base/`).

### DIST-02 call sites

Metrics instance: gateway `metrics := telemetry.NewMetrics()` main.go:90; control plane main.go:159, and `distServer.SetMetrics(metrics)` already exists (main.go:264). [VERIFIED]

| Recorder | Call site | Notes |
|----------|-----------|-------|
| `RecordPolicyEval(d)` + `RecordPolicyDecision(decision, reason)` | `cmd/gateway/main.go` around `state.PolicyEngine.Evaluate` at ~519 (user) and ~851 (workload): `t0 := time.Now(); decision, err := ...; metrics.RecordPolicyEval(time.Since(t0))`, then record "deny"/"allow" with the reason where `ac.SetDecision` is called | `reason_code` comes from Rego (policy-author controlled, finite but not enforced). Normalize through the existing closed-set classifier in `internal/audit` (`ClosedReasonLabels()`, used by suppression; tests `main_reasons_test.go` assert every policy reason is classified) or map unknown to `OTHER` to keep cardinality bounded. Do not put principal/path in labels. |
| `RecordRateLimitRejection(routeID)` | the `!res.Allowed` branches after `rateLimiter.Allow` at ~459 (user) and ~791 (workload), where `ac.SetDecision("deny","RATE_LIMIT_EXCEEDED")` at ~482/~814 | route_id is from the signed snapshot, bounded |
| `RecordSpoolBytes(n)` | In `internal/audit/spool.go` `WriteFrames` (~404) and `AppendPreForward` (~307) after a successful write. Spool has no recorder dependency; add `DiskSpool.SetBytesWrittenHook(func(int))` (or Config field) wired in main.go: `diskSpool.SetBytesWrittenHook(metrics.RecordSpoolBytes)` | Do NOT add to the `audit.Recorder` interface: it has test fakes (adding a method breaks them); nopRecorder default is fine. Alternative: count bytes in committer where `RecordAuditWritten` is called (committer.go:~616) but that misses `AppendPreForward` (the allow records, the bulk of AUD-01 volume). Hook in spool covers both. |
| `SetConnectedGateways(n)` | `internal/control/server.go` `StreamSnapshots`: after `s.clients[connID] = msgCh` (line ~99) and in the deferred delete (~103), call `s.metrics.SetConnectedGateways(len(s.clients))` while holding `s.mu`; `s.metrics` field exists (`SetMetrics`, line 36); nil-guard like `countRejection` | gauge, no labels |
| `RecordSnapshotPublish()` | `BroadcastSnapshot` (server.go:208) covers both publish (`publish_handler.go:155`) and rollback (`rollback_handler.go:56`). If only "publish" is wanted, call in `HandlePublishPolicy` after SaveSnapshot succeeds. Recommend BroadcastSnapshot, nil-guarded | counter |
| NEW `aegis_http_request_duration_seconds` | `internal/telemetry/metrics.go`: `HTTPRequestDuration *prometheus.HistogramVec` labels `{method, route, status_class}`, register in `reg.MustRegister`. `http_middleware.go:66`: `start := time.Now()` before `next.ServeHTTP`, `m.ObserveRequestDuration(r.Method, holder.value, rec.statusCode, time.Since(start))` after | Buckets e.g. `{.001,.0025,.005,.01,.025,.05,.1,.25,.5,1,2.5,5,10}`. `status_class` = "2xx".."5xx"; method must be normalized to the closed set GET/POST/PUT/PATCH/DELETE/HEAD/OPTIONS else "OTHER" (existing `RecordRequest` passes raw `r.Method` and raw status, an attacker can mint arbitrary methods; fix both while here). "route" = canonical `route_id` from `GetRouteID` (already low cardinality, "unknown" fallback); on the control plane use the registered route pattern, never `r.URL.Path`. No principal, IP, jti or path. |

Where the control-plane HTTP API is concerned: does `MetricsMiddleware` wrap it? Not verified (grep shows only gateway uses it). Out of scope unless the planner wants control-plane request latency; gateway histogram satisfies the audit evidence.

Wiring guard test (new `internal/telemetry/callers_test.go`): walk the module root (`../..`), `parser.ParseFile` every non-`_test.go` .go file (skip `internal/telemetry/metrics.go`, vendor, web/), collect `*ast.CallExpr` whose `Fun` is a `SelectorExpr` with `Sel.Name` in `{RecordPolicyEval, RecordPolicyDecision, RecordRateLimitRejection, RecordSpoolBytes, SetConnectedGateways, RecordSnapshotPublish, ObserveRequestDuration}`; fail listing any with zero callers. Because a method value passed as a hook (`diskSpool.SetBytesWrittenHook(metrics.RecordSpoolBytes)`) is a `SelectorExpr` but not a call, also count bare `SelectorExpr` uses in non-call position, or write the hook as a closure. The guard idea mirrors `cmd/gateway/wiring_test.go` (`wiringCalls`, `wiringSelector`). Better: derive the recorder list by reflecting every exported method on `*telemetry.Metrics` and requiring a caller for each (catches future dead recorders); `RecordRequest`, `SetActiveSnapshotVersion`, etc. already have callers.

Also add a metrics_test.go unit test that the new histogram appears in `/metrics` text with the expected label names and none of `principal|ip|path|jti|user`.

### Audit hardening (items AUD-01/AUD-03)

**4a. Worker non-corrupt read error** (`internal/audit/worker.go` `processFile` ~308-316, caller loop ~257-264). Current behavior: any non-EOF, non-`ErrCorruptedRecord` error (`io.ErrUnexpectedEOF` on a partial header, genuine I/O error, and `json.Unmarshal` failure returned by `ReadFramedRecord` spool.go:620) hits `break`, `processFile` returns `nil`, and the caller then treats the historical segment as done: `SaveOffset(nextFile, 0)` and, with `PruneArchived`, `os.Remove(filePath)`. That drops every unread frame behind the error. Also `scanToNextMagic` failure `break`s the same way.
Safe design:
- Classify: `io.EOF` = clean end. `ErrCorruptedRecord` = existing resync. JSON unmarshal failure (record has valid CRC; the returned `bytesRead` is the full frame) = treat as corrupt: log + count + advance `currentOffset += bytesRead` (poison-frame must not wedge the worker; D-18 requires one hostile row not to wedge). `io.ErrUnexpectedEOF` = torn tail: on the latest segment return without error and WITHOUT advancing past `currentOffset` (writer may still be mid-write; retry next tick); on a historical segment this is a crash-torn tail and is lost by definition, log a warning and treat as end. Any other error (permission, EIO) = `return processedCount, currentOffset, fmt.Errorf(...)` so the caller returns the error before touching the cursor or pruning (caller already does `if err != nil { return totalProcessed, err }`).
- Make the caller defensive: only advance/prune a historical segment when `processFile` returned `err == nil` AND fully consumed it (add an explicit `reachedEOF bool` return), so the invariant is structural rather than by convention.
- The main loop's error handling (find where `ProcessOnce`/`Run` calls this) must log and retry with the existing backoff; verify it does not swallow. Tests (hermetic, no Postgres; see worker_test.go for the fake DB pattern): (1) injected reader error on historical segment 1 of 2 -> cursor unchanged, segment 1 file still on disk, error returned; (2) valid-CRC bad-JSON frame between two good frames -> both good frames delivered, bad one skipped and counted; (3) torn tail on the latest segment -> no error, cursor not past the torn frame, completes after the frame is finished.

**4b. Committer overflow gate** (`internal/audit/committer.go:213-216`):
`EnqueueCompletion` calls `setFault(dropQueueFull)` when the bounded queue is full, which makes `CheckSaturation` true (spool.go:195) and `/readyz` returns 503 SPOOL_SATURATED, and the 0.90 gate stops allowed traffic (D-12).
**Conflict flag:** locked decision **D-12** (08-CONTEXT.md line 38) explicitly lists "queue full" as a condition that must make the gateway "treat itself as saturated: new allowed requests get 503". Phase 12's goal requires queue overflow not flip the replica unready. Recommended amendment, to be recorded as a new decision (e.g. "D-12a, Phase 12") and surfaced to the user before planning, because it changes a fail-closed rule:
- queue_full: completions are post-response records (D-08: off the request path, never delay a response); the authorization-relevant record is the pre-forward allow row (D-09, synchronous fsync, unchanged). So queue overflow: count (`RecordAuditDropped(completion, queue_full)` already done in `enqueueAsync`), log (rate-limited `logf` already exists), and do NOT call `setFault`. Keep a visible signal: a Prometheus counter already exists, and optionally add an alert rule.
- hard_limit and write_error remain fail-closed via `setFault` (committer.go:480, 498, 533): a real disk fault or exhausted headroom still stops traffic, preserving AUD-02/D-11.
- Consequences to update: `recoverable()`/`tryRecover` queue_full branches become mostly dead for the completion-overflow path (`clearWriteErrorFault` line ~552 sets `dropQueueFull` when the queue is >half full after a write-error recovery; reconsider so that it simply clears). Existing tests that assert the queue_full fault (committer_test.go, spool_recovery_test.go, pipeline tests) must be updated, not deleted; grep `dropQueueFull` and `queue_full` in `internal/audit/*_test.go`.
- Alternative if the user refuses to loosen D-12: keep the fault but add hysteresis (set fault only after N consecutive/seconds of sustained overflow, e.g. queue full for >2s) so a burst does not trip it. Lower risk but does not satisfy "without flipping unready" for sustained overload. Present both; recommend the amendment.

### W2 cert SANs

`scripts/certificates/main.go:155` issues `ca.IssueServerCert("gateway", []string{"gateway","localhost"}, loopback)`. Change the DNS list to `gateway, localhost, aegis-gateway, aegis-gateway.aegis-system, aegis-gateway.aegis-system.svc, aegis-gateway.aegis-system.svc.cluster.local`; also add `gateway-1, gateway-2, gateway-3` since compose HAProxy names them (distributed compose). `pki.CA.IssueServerCert(cn, dnsNames, ips)` (internal/pki/pki.go:137) takes the slice directly. Test in `scripts/certificates/main_test.go` (pattern: `run(t.TempDir(), "")` then `readCert`): assert `cert.VerifyHostname(n) == nil` for each name; run `go test ./scripts/certificates/`.
Certs ARE committed (`deployments/certs/*`, not gitignored). `run()` reuses the existing root CA (`loadOrCreateCA`) but reissues EVERY leaf with fresh keys, so `go run ./scripts/certificates/main.go` (make certs) rewrites ~30 files. This is safe for compose (CA unchanged; all leaves are signed by the same CA and mounted together) but makes a large diff; acceptable, commit it. Cert validity runs to Oct 2027. After regenerating, confirm `git status` shows root-ca.* unchanged and run `go test -short ./scripts/certificates/ ./tests/manifests/`. `create-secrets.sh` consumes these files for K8s (`aegis-gateway-workload-tls`).

## Don't Hand-Roll

| Problem | Use Instead | Why |
|---------|-------------|-----|
| Graceful shutdown | `http.Server.Shutdown` after the delay (existing `DualServer.Shutdown`) | already drains both listeners concurrently |
| Latency metric | `prometheus.HistogramVec` | existing registry pattern |
| Manifest parsing | existing `tests/manifests` helpers + yaml.v3 | no kustomize/kubectl needed, hermetic |
| Dead-code detection | go/ast guard like `cmd/gateway/wiring_test.go` | already the repo convention |
| Cert generation | `pki.CA.IssueServerCert` | exists |

## Common Pitfalls

1. **Delay sleeps with Shutdown already started.** Ensure `Shutdown` is called only after the delay; otherwise listeners close and the delay is meaningless. Test (c) above covers it.
2. **Grace period shorter than the sum.** Compose is already exactly 45s = 5+30+5+5 with no margin; K8s default 30s is the original bug. Assert with tests, not by eye.
3. **Delay inherited by mvp/hardened single-gateway compose and by unit tests.** Keep code default 0; set explicit values only in the distributed compose and K8s configmap.
4. **preStop plus in-process delay double wait.** Intentional, but both are in the grace budget (table above).
5. **HAProxy workload backend is TCP-checked**, so it never sees `/readyz`; without `track` the :9443 side keeps routing to a draining replica.
6. **`setFault` removal changes tests and `tryRecover` logic.** Update, do not delete, the queue_full tests; keep hard_limit/write_error behavior byte-identical.
7. **Cardinality:** `RecordPolicyDecision` reason_code and the latency histogram route/method labels must be closed sets; `RecordRequest` currently takes raw method and status (fix to normalized values, but this changes existing series labels, so check `internal/telemetry/metrics_test.go` and dashboards/alert rules for `aegis_http_requests_total{status=...}` consumers before changing labels; if risky, leave `RecordRequest` alone and only constrain the new histogram).
8. **Passing recorder as method value** (`hook(metrics.RecordSpoolBytes)`) fools a call-only AST guard; the guard must count selector uses or the wiring must use a closure.
9. **Worker partial-record at the live tail** looks like corruption (`ErrCorruptedRecord` wrapping partial payload) and `io.ErrUnexpectedEOF`: must not be skipped past on the latest segment.
10. **Regenerating certs rewrites all leaf key pairs**; make sure `deployments/certs` is committed as one atomic change and `tests/compose` (cert presence checks) still passes.

## Code Examples

### Two-phase drain core (pattern)
```go
func twoPhaseDrain(state *proxy.DrainingState, delay, drainTimeout time.Duration,
    shutdown func(context.Context) error, interrupt <-chan os.Signal) error {
    state.SetDraining() // /readyz -> 503; traffic still served
    if delay > 0 {
        t := time.NewTimer(delay)
        select {
        case <-t.C:
        case <-interrupt: // second signal: skip the wait
            t.Stop()
        }
    }
    ctx, cancel := context.WithTimeout(context.Background(), drainTimeout)
    defer cancel()
    return shutdown(ctx)
}
```

### Overflow without fault (committer.go)
```go
func (c *Committer) EnqueueCompletion(ev *CompletionEvent) {
    // D-12a (Phase 12): overflow is counted and logged in enqueueAsync; completions are
    // post-response records, so a burst must not fail the replica unready.
    if !c.enqueueAsync(ev, kindCompletion, true) {
        c.logf("AuditCommitter warning: completion queue full; completion shed")
    }
}
```

### Postgres ingress fix
```yaml
  ingress:
    - from:
        - podSelector: {matchLabels: {app: aegis-control-plane}}
        - podSelector: {matchLabels: {app: aegis-gateway}}   # audit-worker sidecar
      ports: [{protocol: TCP, port: 5432}]
```

### StatefulSet drain hooks
```yaml
    spec:
      terminationGracePeriodSeconds: 70
      containers:
        - name: gateway
          lifecycle:
            preStop:
              exec: {command: ["sh", "-c", "sleep 5"]}
```

## State of the Art

| Old | Current | Impact |
|-----|---------|--------|
| Fail readiness and close at once | readiness fail -> delay -> drain (standard K8s/LB practice) | removes dropped requests during endpoint propagation [ASSUMED, community consensus] |

## Assumptions Log

| # | Claim | Section | Risk if Wrong |
|---|-------|---------|---------------|
| A1 | HAProxy `track aegis_http_back/gateway-N` syntax on server lines works across the two backends | Drain | `haproxy -c` fails at runtime; fallback: tcp-check with send/expect is impossible over mTLS, so accept Connection: close + delay only |
| A2 | preStop time counts against terminationGracePeriodSeconds | Grace budget | Under-sized grace; margin of 10-15s covers it |
| A3 | K8s endpoint removal races SIGTERM, so a 5s preStop helps | Drain | None harmful |
| A4 | Amending D-12 for queue_full is acceptable to the user | Audit hardening 4b | Violates a locked decision; needs explicit user confirmation before plan lock |
| A5 | `RecordRequest` label normalization may break dashboard queries | Pitfalls 7 | Not verified; grep `deployments/`/docs for the metric before changing |

## Open Questions

1. **D-12 amendment (queue_full).** Confirm with the user: shed+count (recommended) or hysteresis. Blocks plan for the committer task only.
2. Should the control-plane HTTP API also get the latency histogram? Not required by the audit; default no.
3. Whether to keep `aegis-audit-worker` in the postgres policy (no pod uses it). Default: remove.

## Environment Availability

| Dependency | Required By | Available | Fallback |
|------------|------------|-----------|----------|
| Go 1.26.0 | all tests | yes (`go version`) | none |
| openssl | cert SAN inspection (manual) | yes | Go test uses crypto/x509 |
| Docker, kustomize, haproxy | live validation, `manifest-build` | not used (Docker must not be started) | YAML-parsing tests only; HAProxy cfg verified by review/string test |

## Validation Architecture

### Test Framework
| Property | Value |
|----------|-------|
| Framework | Go `testing` + testify; yaml.v3 for manifests |
| Config file | none (go.mod) |
| Quick run command | `go test -short ./tests/manifests/ ./internal/audit/ ./internal/telemetry/ ./internal/proxy/ ./cmd/gateway/ ./scripts/certificates/` |
| Full suite command | `go build ./... && go vet ./... && go test -short ./...` |

### Phase Requirements -> Test Map
| Item / Req | Behavior | Test | Command | File |
|------------|----------|------|---------|------|
| N1 / AUD-01, AUD-03, REV-03 | every pod connecting to postgres/redis/CP-gRPC is admitted by target NetworkPolicy and own egress | unit (YAML) | `go test -short ./tests/manifests/ -run NetworkPolicyPeers` | new in crosscheck_test.go (Wave 0; write red first) |
| DIST-01 | readyz fails first, delay served, then shutdown, in-flight completes | unit | `go test -short ./cmd/gateway/ ./internal/proxy/ -run 'Drain'` | new |
| DIST-01 | grace >= preStop+delay+drain+margin (K8s, compose); preStop present; env key read by source | unit | `go test -short ./tests/manifests/ ./tests/compose/ -run 'Drain\|Grace'` | new |
| DIST-01 | workload responses carry `Connection: close` while draining | unit | `go test -short ./cmd/gateway/ -run WorkloadDrain` | new |
| DIST-02 | each recorder has a non-test caller | AST guard | `go test -short ./internal/telemetry/ -run RecorderCallers` | new callers_test.go |
| DIST-02 | histogram exists, labels low-cardinality, no PII | unit | `go test -short ./internal/telemetry/ -run RequestDuration` | extend metrics_test.go |
| DIST-02 | SetConnectedGateways and snapshot publish increment on stream connect/publish | unit | `go test -short ./internal/control/ -run 'ConnectedGateways\|PublishMetric'` | new |
| DIST-02 | spool bytes hook fires on WriteFrames and AppendPreForward | unit | `go test -short ./internal/audit/ -run SpoolBytes` | new |
| AUD-03 | non-corrupt read error does not advance cursor or prune; bad-JSON frame skipped; torn tail retried | unit | `go test -short ./internal/audit/ -run 'Worker.*(ReadError\|Torn\|Poison)'` | extend worker_test.go |
| AUD-01 | queue overflow sheds+counts, no fault, readiness unaffected; hard_limit/write_error still fault | unit | `go test -short ./internal/audit/ -run 'Committer.*(Overflow\|Fault)'` | update committer_test.go |
| W2 | gateway-server cert verifies for all K8s Service DNS names | unit | `go test -short ./scripts/certificates/ -run GatewaySANs` | extend main_test.go |

### Sampling Rate
- Per task commit: the specific package command above.
- Per wave merge: quick run command.
- Phase gate: `go build ./... && go vet ./... && go test -short ./...` green; `go test -race` on `./internal/audit/ ./internal/control/ ./cmd/gateway/` for the concurrency-touching edits.

### Wave 0 Gaps
- [ ] NetworkPolicy peer cross-check (red on current tree)
- [ ] recorder caller guard (red on current tree: six recorders)
- [ ] drain test harness (extracted function)
- [ ] no framework install needed

## Security Domain

| ASVS | Applies | Control |
|------|---------|---------|
| V7 Logging/Errors | yes | audit loss counted and logged, no event fields in logs (committer `logf` rule) |
| V8 Data Protection | yes | metric labels contain no PII (principal, IP, jti, path) |
| V9 Communications | yes | cert SANs enable real hostname verification instead of skipping it |
| V14 Config | yes | NetworkPolicy least privilege: admit exactly connecting pods |
| V2/V3/V4 | no change | |

Threats: metric label cardinality explosion (DoS) via attacker-chosen method/status/reason: normalize to closed sets. Poison audit frame wedging worker: skip+count. Widening the postgres policy: admit only `app: aegis-gateway` pods, not namespace-wide.

## Sources

### Primary (HIGH, repo reads at HEAD)
- .planning/v1.0-MILESTONE-AUDIT.md, ROADMAP.md Phase 12, 08-CONTEXT.md (D-08..D-12)
- cmd/gateway/main.go (drain 960-1050, policy eval ~519/851, ratelimit ~459/791), internal/proxy/{probe,server}.go, internal/telemetry/{metrics,http_middleware}.go, internal/audit/{committer,worker,spool}.go, internal/control/{server,publish_handler}.go
- deployments/kubernetes/base/{postgres,redis,gateway,control-plane}/*, deployments/compose/{haproxy/haproxy.cfg,docker-compose.distributed.yml,Dockerfile}, scripts/certificates/main.go, tests/manifests/*, cmd/gateway/wiring_test.go

### Tertiary (LOW, recalled, not fetched this session)
- Kubernetes lifecycle/preStop/grace semantics; HAProxy `track` syntax

## Metadata
**Confidence:** stack HIGH (no new deps); architecture HIGH (call sites verified); K8s/HAProxy semantics MEDIUM; D-12 amendment is a user decision.
**Research date:** 2026-11-16. **Valid until:** 30 days.
