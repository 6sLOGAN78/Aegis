---
phase: 12-close-gaps-n1-dist-01-dist-02-k8s-audit-worker-postgres-netw
verified: 2026-11-16T00:00:00Z
status: human_needed
score: 9/9 must-haves verified
overrides_applied: 0
human_verification:
  - test: "Run the committed deployments/compose/haproxy/haproxy.cfg under a real haproxy (haproxy -c -f), then check /stats"
    expected: "Config validates; aegis_workload_back servers on :9443 (tcp mode) track aegis_http_back/gateway-N and go DOWN/UP with the :8080 /readyz check"
    why_human: "Track syntax and tcp-mode tracking of an http-mode backend need a real haproxy; no Docker/haproxy run here"
  - test: "Live drain behind HAProxy: send SIGTERM to one distributed-compose gateway during load"
    expected: "/readyz returns 503 DRAINING at once, HAProxy removes it within about 2s, in-flight requests finish, workload keep-alive clients get Connection: close, container exits within stop_grace_period 60s with audit flushed"
    why_human: "Requires Docker compose stack and real timing"
  - test: "Live K8s: delete a gateway pod (StatefulSet) and apply the netpols in a CNI-enforcing cluster"
    expected: "preStop sleep 5 plus propagation 10s plus drain fits in terminationGracePeriodSeconds 70; gateway pod sidecar reaches postgres:5432; gateway cert SANs match the Service DNS"
    why_human: "D-19: no cluster in this phase"
---

# Phase 12 Verification Report

**Goal:** Close N1, DIST-01, DIST-02, audit worker hardening and D-12a, and W2 cert SANs.
**Status:** human_needed (all automated checks pass; only live-only checks remain)
**Re-verification:** No

## Test runs (executed by verifier)
- `go build ./... && go vet ./... && go test -short ./...`: all packages ok (including tests/manifests, tests/compose, scripts/certificates, tests/chaos).
- `go test -short -race ./internal/audit ./internal/control ./internal/telemetry ./cmd/gateway`: all ok.

## Observable Truths
| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | N1: postgres NetworkPolicy admits `app: aegis-gateway`, dead `aegis-audit-worker` peer gone, cross-check test exists | VERIFIED | deployments/kubernetes/base/postgres/networkpolicy.yaml; no `aegis-audit-worker` match in deployments/; tests/manifests/networkpolicy_peers_test.go, crosscheck_test.go pass |
| 2 | DIST-01 two-phase drain: fail /readyz, wait delay while serving, Shutdown with drain timeout, then audit flush | VERIFIED | cmd/gateway/drain.go `twoPhaseDrain` (SetDraining, timer/interrupt, Shutdown ctx); main.go:1008-1030 calls it before auditPipeline.Shutdown; probe.go:74 fails /readyz when draining; default delay 0 |
| 3 | Workload listener sends Connection: close while draining | VERIFIED | `drainAwareHandler` wired at main.go:995 |
| 4 | Grace periods and preStop: compose 60s/5s, K8s 70 + `sleep 5` + 10s | VERIFIED | statefulset.yaml (70, preStop), configmap.yaml (10s), compose env 5s x3; tests/manifests/drain_grace_test.go and tests/compose/drain_grace_test.go pass |
| 5 | HAProxy :9443 servers track :8080 readyz check | VERIFIED statically | haproxy.cfg lines 36-38 `track aegis_http_back/gateway-N`; live syntax validity in human_verification |
| 6 | DIST-02: six recorders have production callers | VERIFIED | gateway main.go:482/819 (rate limit), 523-556/860-887 (policy eval/decision, both listeners), main.go:150 (spool bytes hook), control/server.go:98,107 (gateways), :222 (snapshot publish); AST guard internal/telemetry/callers_test.go passes |
| 7 | Latency histogram, labels method/route_id/status_class only, normalized | VERIFIED | metrics.go:230-238, normalizeMethod/statusClass; called from http_middleware.go:70, middleware wired in main.go:981,992 |
| 8 | Audit worker read errors: bad-JSON frame skipped+counted, torn tail retried, advance/prune only on explicit EOF | VERIFIED | worker.go processFile reachedEOF logic (lines 254-385), SkippedFrames counter; tests pass |
| 9 | D-12a honored and fail-closed invariants intact | VERIFIED | EnqueueCompletion/enqueueAsync (committer.go:214-252) only counts `queue_full` and logs, no setFault; setFault remaining only for hard_limit (481, 534) and write_error (499); spool.go 0.90 saturation gate and ErrHardLimit unchanged; D-12a recorded in STATE.md:96; D-09 sync denial/allow path untouched (denial waiters still settle with errors) |
| 10 | W2 gateway cert SANs | VERIFIED | scripts/certificates/main.go:156-158; committed deployments/certs/gateway-server.crt has all Service DNS names and gateway-1..3 and verifies against root-ca.crt |

## Requirements Coverage
| ID | Plans | Status | Evidence |
|----|-------|--------|----------|
| DIST-01 | 12-04, 12-06 | SATISFIED (live check pending) | truths 2-5 |
| DIST-02 | 12-03, 12-05 | SATISFIED | truths 6-7 |
| AUD-01 | 12-01, 12-02 | SATISFIED | truth 9, N1 |
| AUD-03 | 12-01, 12-02 | SATISFIED | truths 1, 8 |
| REV-03 | 12-01 | SATISFIED (N1 path; 5s propagation itself is Phase 3/9 work) | truth 1 |

All five IDs appear in plan frontmatter and in REQUIREMENTS.md. No orphans.

## Anti-patterns
No TBD/FIXME/XXX found in the touched drain/committer/worker code paths reviewed. No blockers.

## Warnings (non-blocking)
- `.planning/REQUIREMENTS.md` still shows DIST-01 and DIST-02 as `[ ]` and traceability "Gap closure" (lines 71-72, 145-146). Update to checked/Complete after human verification.
- HAProxy tracking applies to tcp-mode backend tracking an http-mode check; syntactically valid per HAProxy docs, but only a real haproxy run confirms it.

## Gaps Summary
No code gaps. Remaining work is live-only verification listed in frontmatter.
