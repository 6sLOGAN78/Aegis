---
phase: 12
slug: close-gaps-n1-dist-01-dist-02-k8s-audit-worker-postgres-netw
status: draft
nyquist_compliant: true
wave_0_complete: false
created: 2026-11-16
---

# Phase 12 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | go test + testify, yaml.v3 for manifests |
| **Config file** | none (go.mod) |
| **Quick run command** | `go test -short ./tests/... ./internal/audit/ ./internal/telemetry/ ./internal/control/ ./cmd/gateway/ ./scripts/certificates/` |
| **Full suite command** | `go build ./... && go vet ./... && go test -short ./...` (plus `-race` on internal/audit internal/control cmd/gateway) |
| **Estimated runtime** | ~12 seconds |

---

## Sampling Rate

- **After every task commit:** Run the quick run command
- **After every plan wave:** Run the full suite command
- **Before `/gsd:verify-work`:** Full suite must be green
- **Max feedback latency:** 12 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Threat Ref | Secure Behavior | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|------------|-----------------|-----------|-------------------|-------------|--------|
| 12-01-01 | 01 | 1 | AUD-01, AUD-03, REV-03 | T-12-01/02 | every connecting pod admitted by target NetworkPolicy; red first | unit | `go test -short ./tests/manifests/ -run NetworkPolicyPeers` | ❌ W0 | ⬜ pending |
| 12-01-02 | 01 | 1 | AUD-01 | T-12-03 | gateway cert verifies K8s DNS names | unit | `go test -short ./scripts/certificates/ -run GatewaySANs` | ✅ | ⬜ pending |
| 12-02-01 | 02 | 1 | AUD-01 | T-12-05/06/07 | overflow sheds without fault; hard_limit/write_error still fault | unit -race | `go test -short -race ./internal/audit/ -run 'Committer|Spool|Pipeline'` | ✅ | ⬜ pending |
| 12-02-02 | 02 | 1 | AUD-03 | T-12-08/09 | no cursor advance/prune on read error; poison skipped; torn tail retried | unit -race | `go test -short -race ./internal/audit/ -run Worker` | ✅ | ⬜ pending |
| 12-02-03 | 02 | 1 | DIST-02 | T-12-10 | spool bytes hook on both write paths | unit -race | `go test -short -race ./internal/audit/ -run SpoolBytes` | ❌ W0 | ⬜ pending |
| 12-03-01 | 03 | 1 | DIST-02 | T-12-11/12 | histogram bounded labels, no PII | unit -race | `go test -short -race ./internal/telemetry/` | ✅ | ⬜ pending |
| 12-03-02 | 03 | 1 | DIST-02 | T-12-13 | connected gateways gauge + publish counter | unit -race | `go test -short -race ./internal/control/ -run 'ConnectedGateways|PublishMetric'` | ❌ W0 | ⬜ pending |
| 12-04-01 | 04 | 1 | DIST-01 | T-12-14/15 | readyz first, delay served, then shutdown; Connection: close | unit -race | `go test -short -race ./cmd/gateway/ -run Drain` | ❌ W0 | ⬜ pending |
| 12-04-02 | 04 | 1 | DIST-01 | T-12-16 | main.go uses twoPhaseDrain, order preserved | unit -race | `go test -short -race ./cmd/gateway/` | ✅ | ⬜ pending |
| 12-05-01 | 05 | 2 | DIST-02 | T-12-20 | recorder AST guard red first | unit | `go test -short ./internal/telemetry/ -run RecorderCallers` | ❌ W0 | ⬜ pending |
| 12-05-02 | 05 | 2 | DIST-02 | T-12-17/18/19 | policy/ratelimit/spool wiring, closed-set reasons | unit -race | `go test -short -race ./cmd/gateway/ && go test -short ./internal/telemetry/ -run RecorderCallers` | ✅ | ⬜ pending |
| 12-06-01 | 06 | 2 | DIST-01 | T-12-21/23 | K8s grace >= preStop+delay+drain+10 | unit | `go test -short ./tests/manifests/ -run DrainBudget` | ❌ W0 | ⬜ pending |
| 12-06-02 | 06 | 2 | DIST-01 | T-12-21/22 | compose grace and HAProxy track | unit | `go test -short ./tests/compose/` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] tests/manifests/networkpolicy_peers_test.go (red first)
- [ ] internal/telemetry/callers_test.go (red first, plan 05)
- [ ] cmd/gateway/drain_test.go, tests/manifests/drain_grace_test.go, tests/compose/drain_grace_test.go

No framework install needed.


---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| Live K8s/HAProxy drain behaviour and haproxy.cfg `track` syntax | DIST-01 | no Docker or cluster this phase (D-19) | deferred to live UAT: `haproxy -c -f` and rolling restart under load |


---

## Validation Sign-Off

- [ ] All tasks have `<automated>` verify or Wave 0 dependencies
- [ ] Sampling continuity: no 3 consecutive tasks without automated verify
- [ ] Wave 0 covers all MISSING references
- [ ] No watch-mode flags
- [ ] Feedback latency < 12s
- [ ] `nyquist_compliant: true` set in frontmatter

**Approval:** {pending / approved YYYY-MM-DD}
