---
phase: 12-close-gaps-n1-dist-01-dist-02-k8s-audit-worker-postgres-netw
reviewed: 2026-11-16T14:36:00Z
depth: standard
files_reviewed: 30
files_reviewed_list:
  - cmd/gateway/drain.go
  - cmd/gateway/drain_test.go
  - cmd/gateway/main.go
  - cmd/gateway/metrics_wiring.go
  - cmd/gateway/metrics_wiring_test.go
  - cmd/gateway/wiring_test.go
  - deployments/compose/docker-compose.distributed.yml
  - deployments/compose/haproxy/haproxy.cfg
  - deployments/kubernetes/base/gateway/configmap.yaml
  - deployments/kubernetes/base/gateway/statefulset.yaml
  - deployments/kubernetes/base/postgres/networkpolicy.yaml
  - internal/audit/committer.go
  - internal/audit/committer_test.go
  - internal/audit/pipeline_e2e_delivery_test.go
  - internal/audit/spool_bytes_test.go
  - internal/audit/spool.go
  - internal/audit/worker.go
  - internal/audit/worker_readerr_test.go
  - internal/control/server.go
  - internal/control/server_metrics_test.go
  - internal/telemetry/callers_test.go
  - internal/telemetry/http_middleware.go
  - internal/telemetry/metrics_duration_test.go
  - internal/telemetry/metrics.go
  - scripts/certificates/main.go
  - scripts/certificates/main_test.go
  - tests/compose/compose_test.go
  - tests/compose/drain_grace_test.go
  - tests/manifests/drain_grace_test.go
  - tests/manifests/networkpolicy_peers_test.go
findings:
  critical: 2
  warning: 7
  info: 8
  total: 17
status: issues_found
---

# Phase 12: Code Review Report

**Reviewed:** 2026-11-16T14:36:00Z
**Depth:** standard
**Files Reviewed:** 30
**Status:** issues_found

## Summary

I reviewed every file in scope against diff base 11b3ca5. I did not start Docker. I checked the worker findings with throwaway pgxmock tests run in a scratch copy of the repo. The source tree was not modified.

What holds up:
- **Drain sequence.** `twoPhaseDrain` marks the replica draining before the delay. `DrainingState` is an `atomic.Bool`. The default of 0 matches the old behaviour. A second signal on the buffered `stop` channel skips only the delay. The K8s budget (5+10+30+10 <= 70) and the compose budget (5+30+10 <= 60) both add up. The alpine image has `sh`, so the preStop hook runs.
- **D-12a fail-closed invariants.** The D-09 pre-forward append, the 0.90 `CheckSaturation` gate, the 0.95 `ErrHardLimit` path and the write-error `setFault` calls are all unchanged.
- **New metric labels.** The histogram labels and the policy/rate-limit labels come from closed sets or from configured route IDs. None carries PII.
- **Hooks under locks.** The spool hook runs under `s.mu` and the control-plane gauge is set under `s.mu`. Both only touch lock-free Prometheus primitives, so neither can deadlock.
- **NetworkPolicy.** It is correct for the sidecar topology.

The main defect is in the rewritten audit-worker read loop. Phase 12 now treats every `io.ErrUnexpectedEOF` as a "torn tail". Two outcomes follow:
- On a historical segment, valid frames written after a torn frame are thrown away and the segment is pruned.
- On the newest segment, the worker gets stuck behind the torn frame.

This can happen in normal operation: after a crash the spool reopens the last segment and appends right behind the torn bytes. I reproduced both outcomes. The second Critical issue is a pre-existing, unauthenticated label-cardinality hole on `aegis_http_requests_total`. This phase was allowed to close it and left it open.

## Narrative Findings (AI reviewer)

## Critical Issues

### CR-01: Worker loses or stalls on valid frames that follow a torn frame (overlong length classified as "torn tail")

**File:** `internal/audit/worker.go:326-337` (with `internal/audit/spool.go:616-618`, `internal/audit/spool.go:147-155`)

**Issue:**
- `ReadFramedRecord` now wraps `io.ErrUnexpectedEOF` whenever the declared payload length runs past the end of the file. `processFile` matches that case before `ErrCorruptedRecord` and treats it as a torn tail at the end of the file.
- The frame header is not CRC-protected. A frame is also not necessarily at the end of the file just because its declared length overruns it.
- `NewDiskSpool` (spool.go:147-155) reopens the latest segment with `O_APPEND` and sets `currentSize = info.Size()`. After a crash mid-write, new frames are therefore appended directly behind the torn bytes, and `hasUntrackedTailLocked` cannot detect this because the size matches. If the torn frame declared a length larger than everything written after it, the reader hits `ErrUnexpectedEOF`.

Reproduced with pgxmock (segment = `A`, then the 12-byte header plus 100 bytes of a ~4.4 KB frame, then complete frames `B` and `C`):
- **Historical segment:** "torn tail in historical segment ... treating as end of segment". Two records were ingested (A and the next segment's D). **B and C were lost**, and the segment was pruned (`seg1Exists=false`). This contradicts AUD-03 and the "prune only on explicit EOF" decision: this is not a real EOF.
- **Newest segment:** every pass logs "torn tail ... will retry" with the cursor stuck at 465 and 0 records ingested. B and C are never ingested. Once the segment rotates, the historical case above applies and they are deleted.

There is a second, pre-existing problem in the same path. After a CRC mismatch, `scanToNextMagic(f)` (worker.go:338) scans from where the over-read left the file pointer, not from the start of the bad frame plus one byte. Every valid frame inside the bogus `length` window is skipped. In a repro with 20 valid frames behind a torn 4.4 KB frame, only 10 were recovered.

**Fix:** Only treat the condition as a torn tail when nothing decodable follows it. Always resync from `frameStart+1` before deciding:
```go
case errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, ErrCorruptedRecord):
    // Never trust the header's length: rescan from just after this frame's start.
    if _, serr := f.Seek(currentOffset+1, io.SeekStart); serr != nil {
        readErr = serr; break loop
    }
    resyncPos, scanErr := scanToNextMagic(f)
    if errors.Is(scanErr, io.EOF) {
        // genuinely nothing after it: newest -> retry later, historical -> end
        reachedEOF = !isLatest
        break loop
    }
    if scanErr != nil { readErr = scanErr; break loop }
    currentOffset = resyncPos
    continue
```
On restart, `NewDiskSpool` should also rotate to a fresh segment when the reopened tail does not end on a frame boundary, rather than appending behind it. Add regression tests for both repro shapes.

### CR-02: Unauthenticated, unbounded `method` label on `aegis_http_requests_total` (pre-existing, left open in scope)

**File:** `internal/telemetry/http_middleware.go:69`, `internal/telemetry/metrics.go:308-312`

**Issue:**
- `MetricsMiddleware` wraps the :8080 user handler before authentication. It passes the raw `r.Method` to `RecordRequest`, and that value becomes a label on `aegis_http_requests_total`.
- Go's server accepts any token as a method. An unauthenticated client can mint a new series per request (`FOO1`, `FOO2`, ...), growing gateway memory without bound and bloating Prometheus scrapes.
- Phase 12 added `normalizeMethod` for the new histogram, and 12-CONTEXT explicitly allowed normalizing this counter. 12-03 chose not to, so the cardinality hole the phase was meant to guard against is still reachable.

**Fix:**
```go
func (m *Metrics) RecordRequest(method, routeID string, status int) {
    if routeID == "" { routeID = "unknown" }
    m.HTTPRequestsTotal.WithLabelValues(normalizeMethod(method), routeID, strconv.Itoa(status)).Inc()
}
```
Also update `tests/integration/operator_telemetry_test.go` if it asserts on raw methods.

## Warnings

### WR-01: Hot-path "completion shed" log starves fault/write-error logs (shared rate limiter)

**File:** `internal/audit/committer.go:151-158`, `internal/audit/committer.go:214-218`

**Issue:**
- Every `logf` call shares one `lastLogNS` slot (one line per second).
- Under D-12a, `EnqueueCompletion` calls `logf` on every shed completion, which during a burst means thousands of calls per second. Whichever call arrives first after each 1 s window wins.
- So while the queue overflows, the important one-shot lines are almost always suppressed: "audit write fault set (hard_limit/write_error); refusing allowed traffic", "fault cleared" and "batch write failed".
- Overflow and write errors happen together in practice: `retry` blocks the loop, the queue fills, and shedding starts. The operator then loses the log line that explains why the replica went unready.

**Fix:** Give the shed message its own limiter (for example `lastShedLogNS`) and include a shed count. Alternatively, let `setFault`/`clearFaultIfGen` log unconditionally on state transitions; they are already edge-triggered.

### WR-02: Allow decisions are always labelled `reason="OTHER"`

**File:** `cmd/gateway/metrics_wiring.go:20-33`, `cmd/gateway/main.go:556`, `cmd/gateway/main.go:887`

**Issue:**
- `decisionReasonOrAllowed` returns `"ALLOWED"`, but `policyReasonSet` is built from `audit.ClosedReasonLabels()`. That table holds deny reasons only (governor.go:43-72) and does not contain `ALLOWED`.
- So every allow is recorded as `{decision="allow", reason="OTHER"}`, and the helper has no effect.
- The 12-05 summary claims the allow label matches the audit allow reason; it does not. `TestPolicyReasonLabel` does not cover the allow path.

**Fix:** Pass allow decisions through a separate closed set (for example `{"ALLOWED"}` plus any allow reason codes the bundled policies emit), or hard-code `"ALLOWED"` for allow. Add a test asserting `policyReasonLabel(decisionReasonOrAllowed(""))=="ALLOWED"`.

### WR-03: `ReadFramedRecord` trusts an unbounded, unauthenticated length (panic / multi-GB alloc)

**File:** `internal/audit/spool.go:613-622`

**Issue:**
- `length` is a raw `uint32` taken from the header, which has no CRC.
- If `length == 0xFFFFFFFF`, then `length+1` wraps to 0. `ReadFull` succeeds on an empty buffer, and `buf[:length]` panics. Any other large value makes the code try to allocate up to 4 GiB.
- Either way, one corrupted header puts the worker into a crash loop (the cursor never advances) or an OOM. That contradicts the phase goal of never getting stuck on read errors. This is pre-existing, but the function was modified in this phase and sits on the path CR-01 touches.

**Fix:** Reject `length > maxFrameBytes` (for example 1 MiB, enforced in `frameEvent` too) as `ErrCorruptedRecord` before allocating, and compute the buffer size in `int`/`int64`.

### WR-04: `AEGIS_DRAIN_PROPAGATION_DELAY` silently falls back to 0 and is not bounded by the grace period

**File:** `cmd/gateway/drain.go:44-54`, `cmd/gateway/main.go:1008`

**Issue:**
- A plausible operator typo such as `"10"` (no unit), or `"10 s"`, fails `time.ParseDuration` and silently disables the propagation delay. No log line is emitted, so the first sign is dropped connections during a rollout.
- There is also no upper bound. A value at or above `grace - drain - 10s` lets the orchestrator SIGKILL the process before `auditPipeline.Shutdown` runs, losing queued completions and denials.
- The rest of the gateway config is validated in `internal/config` and fails closed; this variable bypasses that.

**Fix:** Move parsing into `internal/config` validation: fail startup on unparsable or negative values and enforce a sane maximum (for example 60 s). At minimum, `log.Printf` a warning when a non-empty value is rejected.

### WR-05: User listener (:8080) is not drain-aware: K8s keep-alive clients stay pinned and are cut at Shutdown

**File:** `cmd/gateway/main.go:983-995`

**Issue:**
- Only the workload handler is wrapped in `drainAwareHandler`.
- In compose this is harmless: HAProxy in http mode stops dispatching to a DOWN server.
- In K8s the user Service is L4 (kube-proxy). Keep-alive clients connected to the draining pod keep sending requests on that connection for the whole 10 s delay. `Shutdown` then closes idle connections, racing requests already in flight on them; non-idempotent requests are not retried by most clients.
- That defeats the purpose of the propagation delay for the user path.

**Fix:** Wrap the user handler chain too (`drainAwareHandler(drainingState, userMetricsHandler)` inside `CreateProbeHandler`, or wrap `userProbeHandler`), and extend `TestWiringTwoPhaseDrain` to assert both listeners.

### WR-06: Postgres ingress now admits the internet-facing gateway container (pod-level peer)

**File:** `deployments/kubernetes/base/postgres/networkpolicy.yaml:18-21`

**Issue:**
- NetworkPolicy works at pod level. Admitting `app: aegis-gateway` gives the public :8080 gateway container itself a network path to Postgres:5432, not just the audit-worker sidecar.
- The DB credentials live only in the sidecar's env, so the gateway container needs no DB access. A gateway compromise now has a direct network route to the audit database.
- The fix is functionally correct for D-23, but this widening is neither recorded as an accepted risk nor covered by any control (for example a Postgres role limited to INSERT on `audit_events`).

**Fix:** Record the accepted risk in STATE/threat model and confirm the worker's DB role is INSERT-only. Longer term, run the audit worker as its own pod with its own label (and shared storage), so the peer can be narrowed.

### WR-07: Poison-frame skips are permanent audit loss but are not observable and are double-counted

**File:** `internal/audit/worker.go:288-289`, `internal/audit/worker.go:351-356`

**Issue:**
- A CRC-valid frame with invalid JSON is discarded permanently, and that frame can be a D-09 pre-forward allow row.
- The only counter is the in-memory `skipped` atomic. `SkippedFrames()` has no production caller and nothing exports it as a metric, so "counted" in D-12 of 12-CONTEXT is not externally visible.
- If a later read error makes `processFile` return before the cursor moves (worker.go:357-360 / 379-381), the next pass re-skips the same frame and counts it again.

**Fix:** Export a `aegis_audit_worker_skipped_frames_total` counter from the audit-worker binary, and count a skip only once the cursor has been persisted past it (or dedupe by segment and offset).

## Info

### IN-01: Stale comment on `retry()` describes removed behaviour

**File:** `internal/audit/committer.go:504-506`
**Issue:** The comment still says the loop blocks "so the queues fill and EnqueueCompletion faults the gateway". Under D-12a, overflow no longer faults anything.
**Fix:** Update the comment to say the write_error fault is set by `attempt`, and that overflow during a retry is shed.

### IN-02: `queue_full` still recoverable and queue depth still gates hard_limit recovery

**File:** `internal/audit/committer.go:338-356`
**Issue:**
- `recoverable()` still accepts `queue_full`, which production code can no longer reach.
- `tryRecover` still refuses to clear a hard_limit fault while the queue is more than half full. That contradicts the clearWriteErrorFault comment ("queue depth no longer influences faults").
- The behaviour is safe (it fails closed longer), but it is inconsistent with D-12a.
**Fix:** Either document that the queue-depth gate is kept for hard_limit, or remove it. Remove `queue_full` from `recoverable()` once the tests stop using it.

### IN-03: HAProxy `track` changes workload semantics beyond drain

**File:** `deployments/compose/haproxy/haproxy.cfg:36-38`
**Issue:**
- The :9443 TLS listener is no longer health-checked on its own.
- Any /readyz failure (LEASE_EXPIRED, SPOOL_SATURATED, SECURITY_STATE_MISSING) now also removes the replica from workload traffic. That is probably desirable, but nothing documents it.
- With no `on-marked-down shutdown-sessions`, existing TCP sessions survive until the gateway's `Connection: close` or `Shutdown` ends them. The `default-server inter/fall/rise` line has no effect on tracking servers.
**Fix:** Document the intent in a cfg comment. Optionally add `on-marked-down shutdown-sessions` if hard cut-over is preferred after the delay.

### IN-04: Newest-segment torn/corrupt retry logs on every tick forever

**File:** `internal/audit/worker.go:328-329`, `internal/audit/worker.go:337`
**Issue:** On an idle gateway, a torn or corrupt tail on the newest segment is never resolved, so the worker logs a warning every `FlushInterval` with no rate limit.
**Fix:** Rate-limit, or log only when the offset changes.

### IN-05: `TestRecorderCallers` matches selectors by name only and silently skips unparsable files

**File:** `internal/telemetry/callers_test.go:54-63`
**Issue:**
- Any `x.RecordRequest` on an unrelated type counts as a caller, so a generic method name (for example `Set`, `Handler`) would always pass.
- `perr != nil` returns nil, so a syntax error in a file hides its callers from the guard instead of failing it.
**Fix:** Use `go/types` (or at least require the receiver identifier to resolve to `*telemetry.Metrics`), and fail on parse errors.

### IN-06: NetworkPolicy peer test is narrower than its doc comment

**File:** `tests/manifests/networkpolicy_peers_test.go:103-110`, `tests/manifests/networkpolicy_peers_test.go:139-141`
**Issue:**
- The postgres port is hard-coded to 5432 instead of being read from `AEGIS_DB_PORT`.
- Only the first policy selecting a pod is examined; NetworkPolicy semantics are a union of all policies.
- Rules without `ports` or `from` (allow-all) are treated as "not admitted".
- Only `base` is checked, not the `production` overlay.
- These gaps make the test conservative (false failures) rather than unsafe, except that the overlay is never checked.
**Fix:** Read the port from env, union across all matching policies, and run the check on the overlay output as well.

### IN-07: Control-plane gauges count streams, not gateways; publish counter counts attempts

**File:** `internal/control/server.go:97-108`, `internal/control/server.go:221-223`
**Issue:**
- A gateway that reconnects before its old stream is torn down is counted twice in `aegis_control_plane_connected_gateways`.
- `RecordSnapshotPublish` increments even when every client channel was full and the message was dropped.
**Fix:** Document the semantics in the metric help text, or key the gauge on certID and add a separate counter for dropped broadcasts.

### IN-08: K8s audit-worker sidecar exits at t=0 while the gateway keeps writing during drain

**File:** `deployments/kubernetes/base/gateway/statefulset.yaml:110-148`
**Issue:**
- The sidecar has no preStop hook, so it receives SIGTERM immediately and exits after its 2 s final drain.
- Frames the gateway writes during the following ~45 s of drain and flush stay on the PVC until the pod comes back. After a StatefulSet scale-down they stay on the PVC indefinitely.
- No records are lost, but delivery to Postgres is delayed or stranded.
**Fix:** Give the sidecar a preStop sleep at least as long as the gateway's drain budget (or use a native sidecar with `restartPolicy: Always` in initContainers on K8s >= 1.29), so it outlives the gateway's final flush.

---

_Reviewed: 2026-11-16T14:36:00Z_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
