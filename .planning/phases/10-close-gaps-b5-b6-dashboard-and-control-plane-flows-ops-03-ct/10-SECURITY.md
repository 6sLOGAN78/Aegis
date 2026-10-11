---
phase: 10
slug: close-gaps-b5-b6-dashboard-and-control-plane-flows-ops-03-ct
status: verified
threats_open: 0
asvs_level: 1
created: 2026-10-11
---

# Phase 10 — Security

> Per-phase security contract: threat register, accepted risks, and audit trail.
> Register authored at plan time (10-01..10-11 `<threat_model>` blocks); mitigations verified against code by gsd-security-auditor on 2026-10-11. Evidence line numbers refer to HEAD at audit time (before the concurrent `/gsd-code-review 10 --fix` pass).

---

## Trust Boundaries

| Boundary | Description | Data Crossing |
|----------|-------------|---------------|
| gateway → control plane gRPC stream | Gateway-asserted GatewayId, ack status and timestamps | Replica health / convergence state (integrity-relevant) |
| control plane → gateway stream | Snapshot envelopes; trust derives from Ed25519 signature verification | Signed policy snapshots (integrity-critical) |
| operator → publish API | Untrusted Rego source and policy ids | Authorization policy (integrity-critical) |
| mappings file → control plane | Host config signed into every snapshot | Identity → permission mappings (privilege-relevant) |
| browser → control API | Mutations need session cookie + CSRF + Idempotency-Key | Session credential, operator actions |
| client → mutating API | Replays and duplicates | Idempotency keys, If-Match ETags |
| spec → clients/generators | OpenAPI contract relied on by tooling | API surface definition |
| operator → gateways list | Read-only visibility data | Fleet health view |
| browser → third-party CDN | Removed by 10-11 (Monaco bundled locally) | Script supply chain |

---

## Threat Register

| Threat ID | Category | Component | Disposition | Mitigation | Status |
|-----------|----------|-----------|-------------|------------|--------|
| T-10-01 | Repudiation/Info | heartbeat ack | mitigate | Ack echoes lastStatus/lastErr — internal/snapshot/client.go:183-184,219,241; client_test.go:99,173 | closed |
| T-10-02 | Spoofing | GatewayId in Ack/Pong | accept | See AR-01 (gRPC stream is NOT authenticated) | closed |
| T-10-03 | Tampering | Pong upgrading REJECTED | mitigate | GetReplicaStatuses preserves REJECTED — internal/control/server.go:114-117; TestControl_Pong_DoesNotMaskRejected | closed |
| T-10-04 | DoS | concurrent stream Send | mitigate | All stream.Send in single Recv goroutine (client.go:169,221,237,252); `-race` passes | closed |
| T-10-05 | Elevation | unverified lease triggers ack | mitigate | Ack only inside `VerifyLease(...) err == nil` — client.go:233-243 | closed |
| T-10-06 | Tampering/Elevation | HandlePublishPolicy | mitigate | ValidatePolicyDraft + RunRegoTests → 422 before SignSnapshot — publish_handler.go:74-83,135; RequireRole("sec-ops") api.go:135 | closed |
| T-10-07 | Elevation | tampered mappings file | mitigate | LoadIdentityMappings validation + deterministic sort — identity_mappings.go:23-45 | closed |
| T-10-08 | Info Disclosure | SaveSnapshot error echo | mitigate | Generic message, err logged server-side — publish_handler.go:147-149 | closed |
| T-10-09 | DoS | nil repo/validator panic | mitigate | 503 guards — publish_handler.go:58-61,70-72 | closed |
| T-10-10 | Tampering | unvalidated policy_id on create | mitigate | validIDRegex → 400 — policy_handler.go:26-27 | closed |
| T-10-11 | Tampering (CSRF) | save-draft/publish | mitigate | ApiClient adds X-CSRF-Token (api/client.ts:46-47); no raw fetch in PolicyStudio | closed |
| T-10-12 | Availability | stale cached 4xx replay | mitigate | Fresh Idempotency-Key per attempt, re-mint on success/error — PolicyStudio.tsx:147-177,203 | closed |
| T-10-13 | Tampering | published bytes ≠ editor | mitigate | Draft saved from editor content before publish — PolicyStudio.tsx:154-164 | closed |
| T-10-14 | Elevation | tampered mappings file | mitigate | `:ro` mounts in all 3 profiles; log.Fatalf on malformed — cmd/control-plane/main.go:257-262 | closed |
| T-10-15 | Tampering | silently empty mappings | mitigate | compose_test asserts env + `:ro` per profile — tests/compose/compose_test.go:542-558 | closed |
| T-10-16 | Tampering | duplicate/replayed mutation | mitigate | Missing key → 400; LockOrReplay; RequireIfMatch — idempotency.go:125-165; publish_handler.go:48 | closed |
| T-10-17 | Availability | cached 4xx replay (callers) | mitigate | Per-call key in seed.sh:88 cp_call, compose-smoke.sh:154, dashboard | closed |
| T-10-18 | DoS | unbounded idempotency store | accept | See AR-02 | closed |
| T-10-19 | Tampering | key-check bypass via alias | mitigate | All mutating routes/aliases wrapped — api.go:123-148; idempotency_required_test.go:31-41 | closed |
| T-10-20 | Tampering | undocumented route exposure | mitigate | Bidirectional chi.Walk ↔ spec drift test — openapi_drift_test.go:71-98 | closed |
| T-10-21 | Repudiation | contract/server key mismatch | mitigate | `required: true` (control-v1.yaml:752-755) + test openapi_drift_test.go:101-126 | closed |
| T-10-SC | Tampering | Go dependency supply chain | accept | See AR-03 | closed |
| T-10-07-01 | Tampering | snapshot re-delivery short-circuit | mitigate | Skip only on Version AND PayloadSha256 match — client.go:310-315; manager.go:143; test :187 | closed |
| T-10-07-02 | Spoofing | unauthenticated gRPC / GatewayId (CR-02) | accept | See AR-01 | closed |
| T-10-07-03 | Repudiation | masking real rejections | mitigate | Older/invalid snapshots still REJECTED and echoed — client_test.go:173-187 | closed |
| T-10-08-01 | Tampering | predictable idempotency keys | mitigate | crypto.getRandomValues fallback, no Math.random — src/api/idempotency.ts:9 | closed |
| T-10-08-02 | DoS | key collision | mitigate | 122 random bits v4 (idempotency.ts:10-11); 1000-key uniqueness re-run by auditor (no committed test, by plan design) | closed |
| T-10-08-03 | DoS | render crash on insecure origin | mitigate | typeof guard (idempotency.ts:6); lazy ref init PolicyStudio.tsx:147-148 | closed |
| T-10-09-01 | Info | session cookie | mitigate | Secure default true; only "false"/"0" disables; warning logged — auth.go:44,56-63,98,125; api.go:63-66 | closed |
| T-10-09-02 | Info | demo compose plain HTTP | accept | See AR-04 | closed |
| T-10-09-03 | Tampering | CSRF | accept | See AR-05 | closed |
| T-10-10-01 | Repudiation | hidden stale gateways | accept | See AR-06 | closed |
| T-10-10-02 | DoS | misconfigured stale env | mitigate | Invalid or ≤60s → 15m default — gateway_handler.go:32-43 | closed |
| T-10-10-03 | Repudiation | rejected gateways hidden | mitigate | REJECTED exempt from stale filter — gateway_handler.go:46-50; status strings match storage/snapshot_repo.go:94,109,171 | closed |
| T-10-11-01 | Tampering | Monaco script supply chain | mitigate | Local bundle via loader.config({ monaco }); monaco-editor pinned 0.57.0; CSP unchanged; no jsdelivr in dist | closed |
| T-10-11-SC | Tampering | npm install | accept | See AR-07 | closed |

*Status: open · closed*
*Disposition: mitigate (implementation required) · accept (documented risk) · transfer (third-party)*

---

## Accepted Risks Log

| Risk ID | Threat Ref | Rationale | Accepted By | Date |
|---------|------------|-----------|-------------|------|
| AR-01 | T-10-02, T-10-07-02 | **Security debt CR-02 — this entry is its only tracking record.** The gateway↔control-plane gRPC server has NO transport credentials (`grpc.NewServer()` at cmd/control-plane/main.go:288). The plan-time rationale for T-10-02 ("authenticated at gRPC layer (mTLS)") was factually wrong and is superseded here. Consequence: any host that can reach the gRPC port can assert an arbitrary GatewayId, ack status and AcknowledgedAt (server.go:98-100 only fills it when empty), so convergence/partition/staleness views can be spoofed (incl. keeping a dead gateway looking fresh, review IN-01). Snapshot integrity is NOT affected: gateways verify Ed25519 signatures. Scheduled into Phase 11 (ROADMAP.md, "CR-02"): mTLS on gRPC + bind GatewayId to client cert. | Phase 10 plans (10-01, 10-07); corrected by security audit | 2026-10-11 |
| AR-02 | T-10-18 | Pre-existing in-memory idempotency store is unbounded; tracked as tech debt, out of phase scope. | Phase 10 plan 10-05 | 2026-10-11 |
| AR-03 | T-10-SC | No new Go module: gopkg.in/yaml.v3 v3.0.1 moved indirect → direct; go.sum unchanged during phase (audit-confirmed). | Phase 10 plan 10-06 | 2026-10-11 |
| AR-04 | T-10-09-02 | mvp and distributed demo profiles serve plain HTTP and set AEGIS_COOKIE_SECURE=false (mvp.yml:42, distributed.yml:78) by design; hardened profile does not opt out. | Phase 10 plan 10-09 | 2026-10-11 |
| AR-05 | T-10-09-03 | Disabling Secure on demo profiles leaves CSRF defences intact: HttpOnly + SameSite=Strict (auth.go:96-97,123-124) and CSRF token unchanged. | Phase 10 plan 10-09 | 2026-10-11 |
| AR-06 | T-10-10-01 | Gateways silent > AEGIS_GATEWAY_STALE_AFTER (default 15m, >60s enforced) are omitted from GET /gateways. Records remain in storage. Note (audit): omission also removes them from the ClusterTopology convergence denominator, so a long-crashed replica is not visible in the UI (review WR-03) — broader than "list view only". | Phase 10 plan 10-10; scope clarified by audit | 2026-10-11 |
| AR-07 | T-10-11-SC | monaco-editor was already a dependency; diff only pins `^0.57.0` → `0.57.0` in package.json and lock root; lock resolves 0.57.0 with integrity hash. | Phase 10 plan 10-11 | 2026-10-11 |

*Accepted risks do not resurface in future audit runs.*

---

## Security Audit Trail

| Audit Date | Threats Total | Closed | Open | Run By |
|------------|---------------|--------|------|--------|
| 2026-10-11 | 36 | 36 (28 mitigated, 8 accepted) | 0 | gsd-security-auditor via /gsd-secure-phase 10 |

Auditor checks run: `go test -race ./internal/snapshot/ ./internal/control/` (pass); OpenAPIDrift + RequireIdempotencyKey tests (pass); 1000-key v4 uniqueness of `newIdempotencyKey()` with randomUUID absent (pass); `grep jsdelivr web/dashboard/dist` (empty).

Follow-up noted outside the plan-time register: a background commit scan flagged an optimistic-concurrency weakness in PolicyStudio.tsx (If-Match ETag tracks the live latest version rather than the editor's base version, so a stale draft can overwrite a concurrent publish). Routed to the `/gsd-code-review 10 --fix` pass as SEC-01.

---

## Sign-Off

- [x] All threats have a disposition (mitigate / accept / transfer)
- [x] Accepted risks documented in Accepted Risks Log
- [x] `threats_open: 0` confirmed
- [x] `status: verified` set in frontmatter

**Approval:** verified 2026-10-11
