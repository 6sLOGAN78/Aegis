---
phase: 6
slug: production-hardening-runbooks
status: draft
nyquist_compliant: true
wave_0_complete: false
created: 2026-10-07
---

# Phase 6 — Validation Strategy

> Per-phase validation contract for feedback sampling during execution.

---

## Test Infrastructure

| Property | Value |
|----------|-------|
| **Framework** | Go Test CLI (`go test -race`) + `bitnami/kubectl:latest` (`kustomize build`) + `yaml` validation |
| **Config file** | `deployments/kubernetes/base/kustomization.yaml`, `deployments/kubernetes/overlays/production/kustomization.yaml` |
| **Quick run command** | `go test -v -race ./tests/rotation/... ./tests/dr/... ./tests/manifests/...` |
| **Full suite command** | `go test -race ./... && docker run --rm -v $(pwd):/work -w /work bitnami/kubectl:latest kustomize build deployments/kubernetes/overlays/production > /dev/null` |
| **Estimated runtime** | ~15 seconds |

---

## Sampling Rate

- **After every task commit:** Run quick run command (`go test -v -race ./tests/manifests/...`, `./tests/rotation/...`, or `./tests/dr/...`)
- **After every plan wave:** Run full suite command (`go test -race ./...`)
- **Before `/gsd-verify-work`:** Full suite green, manifest compilation verified, rotation and DR tests passing
- **Max feedback latency:** 15 seconds

---

## Per-Task Verification Map

| Task ID | Plan | Wave | Requirement | Threat Ref | Secure Behavior | Test Type | Automated Command | File Exists | Status |
|---------|------|------|-------------|------------|-----------------|-----------|-------------------|-------------|--------|
| 06-01-01 | 01 | 1 | K8S-01 | T-06-01 | Base and production Kustomize manifests define 3 gateway replicas, PSS Restricted profile, non-root user 10001, and read-only root FS | manifest | `docker run --rm -v $(pwd):/work -w /work bitnami/kubectl:latest kustomize build deployments/kubernetes/overlays/production > /dev/null` | ❌ W0 | ⬜ pending |
| 06-01-02 | 01 | 1 | K8S-01 | T-06-02 | Default-deny NetworkPolicies enforce strict namespace and pod isolation with explicit egress to backends, Redis, CP, and CoreDNS | manifest | `go test -v ./tests/manifests/ -run TestNetworkPolicyIsolation` | ❌ W0 | ⬜ pending |
| 06-01-03 | 01 | 1 | K8S-01 | T-06-03 | PodDisruptionBudget (`minAvailable: 2`), topology spread constraints, and L7 health probes validated in manifest test | manifest | `go test -v ./tests/manifests/ -run TestGatewayManifestHardening` | ❌ W0 | ⬜ pending |
| 06-02-01 | 02 | 2 | AUTH-01 | T-06-04 | User JWT issuer key rotation supports dual verification keys with zero 401 errors during transition | unit/rotation | `go test -v -race ./tests/rotation/ -run TestJWTIssuerKeyRotation` | ❌ W0 | ⬜ pending |
| 06-02-02 | 02 | 2 | CTRL-02 | T-06-05 | Ed25519 snapshot signing key and mTLS CA rotation execute with zero dropped requests across replicas | unit/rotation | `go test -v -race ./tests/rotation/ -run "TestSnapshotSigningKeyRotation|TestMTLSCARotation"` | ❌ W0 | ⬜ pending |
| 06-02-03 | 02 | 2 | GW-01 | T-06-06 | Security audit review checklist documents all 12 invariants, threat boundaries, and negative test coverage | audit/docs | `opa test policies/ -v && go test -v -race ./tests/security/...` | ❌ W0 | ⬜ pending |
| 06-03-01 | 03 | 3 | REV-03 | T-06-07 | Redis quarantine reconstruction drill rebuilds principal bans and JTI revocations from DB before gateway readiness marks healthy | integration/dr | `go test -v -race ./tests/dr/ -run TestRedisQuarantineReconstruction` | ❌ W0 | ⬜ pending |
| 06-03-02 | 03 | 3 | AUD-01 | T-06-08 | PostgreSQL PITR monotonic version reconciliation and WAL crash recovery replay verified with zero event loss | integration/dr | `go test -v -race ./tests/dr/ -run "TestPostgresPITRVersionContinuity|TestWALCrashRecoveryReplay"` | ❌ W0 | ⬜ pending |
| 06-03-03 | 03 | 3 | DIST-01 | T-06-09 | Comprehensive incident response runbooks and release SLO sign-off checklists established in docs/runbooks/ | docs/runbook | `test -f docs/runbooks/incident-response.md && test -f docs/runbooks/disaster-recovery.md && test -f docs/runbooks/production-slo-checklist.md` | ❌ W0 | ⬜ pending |

*Status: ⬜ pending · ✅ green · ❌ red · ⚠️ flaky*

---

## Wave 0 Requirements

- [ ] Directory scaffolding:
  - `deployments/kubernetes/base/`
  - `deployments/kubernetes/overlays/production/`
  - `tests/manifests/`
  - `tests/rotation/`
  - `tests/dr/`
  - `docs/runbooks/`

---

## Manual-Only Verifications

| Behavior | Requirement | Why Manual | Test Instructions |
|----------|-------------|------------|-------------------|
| None | N/A | N/A | All Phase 6 behaviors have automated verification via Go test suite, manifest compilation validation, automated rotation and disaster recovery drills, and documentation existence checks. |

---

## Validation Sign-Off

- [x] All tasks have `<automated>` verify or Wave 0 dependencies
- [x] Sampling continuity: no 3 consecutive tasks without automated verify
- [x] Wave 0 covers all MISSING references
- [x] No watch-mode flags
- [x] Feedback latency < 15s
- [x] `nyquist_compliant: true` set in frontmatter

**Approval:** approved 2026-10-07
