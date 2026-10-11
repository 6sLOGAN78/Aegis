# Phase 11: Close gaps B8, B2, CR-02 - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md. This log preserves the alternatives considered.

**Date:** 2026-10-11
**Phase:** 11-close-gap-b8-wire-key-rotation-and-dr-quarantine-reconstruct
**Areas discussed:** DR quarantine source, Keyset loading & rotation, gRPC mTLS identity binding, K8s proof of done (B2)

---

## DR quarantine source

| Option | Description | Selected |
|--------|-------------|----------|
| Postgres table, write-through | principal_quarantines table; control plane writes PG then Redis | ✓ |
| Redis persistence only (AOF/RDB) | No new table; doesn't survive volume loss | |
| Rebuild from audit_events | Replay quarantine actions; fragile | |

| Option | Description | Selected |
|--------|-------------|----------|
| Control plane reconstructs, gateways gate on marker | Restore at startup and on Redis-empty; gateways 503 until marker exists | ✓ |
| Gateway at startup, reads Postgres | Literal roadmap wording; adds PG dependency to gateway | |
| Control plane at startup only | Misses a Redis wipe while running | |

| Option | Description | Selected |
|--------|-------------|----------|
| Postgres first, Redis fail → 503, row kept | Durable intent recorded, reconcile later | ✓ |
| Both or nothing | Roll back PG row on Redis failure | |

| Option | Description | Selected |
|--------|-------------|----------|
| Fail closed 503 until marker restored | Treat missing state like Redis outage | ✓ |
| Only /readyz flips | Serving continues; could fail open | |

| Option | Description | Selected |
|--------|-------------|----------|
| Live smoke: quarantine → FLUSHALL → denied again | All 3 profiles, within 60s | ✓ |
| Hermetic tests only | | |

| Option | Description | Selected |
|--------|-------------|----------|
| jti revocations durable too (same pattern) | revoked_tokens table | ✓ |
| Quarantines only | | |

**User's choice:** All recommended options.

---

## Keyset loading & rotation

| Option | Description | Selected |
|--------|-------------|----------|
| Mounted keyset dir per key family | Matches runbook keyset.pub.N; env fallback | ✓ |
| Comma-separated env list | Restart needed | |

| Option | Description | Selected |
|--------|-------------|----------|
| Hot reload (poll/SIGHUP, atomic set, keep last-good) | Real callers for Add/Set | ✓ |
| Startup load + rolling restart | Add/Set still uncalled | |

| Option | Description | Selected |
|--------|-------------|----------|
| JWT + snapshot verify, signers load from file | Separate issuer/snapshot keys | ✓ |
| Snapshot verifier only | | |
| All three incl. mTLS CA reload | | |

| Option | Description | Selected |
|--------|-------------|----------|
| Smoke drill expand → switch → prune | No gateway restart | ✓ |
| Hermetic rotation tests only | | |

| Option | Description | Selected |
|--------|-------------|----------|
| No kid, try all keys | | ✓ |
| Add kid | | |

| Option | Description | Selected |
|--------|-------------|----------|
| All 3 profiles mount keysets | | ✓ |
| Hardened + distributed only | | |

**User's choice:** All recommended options.

---

## gRPC mTLS identity binding

| Option | Description | Selected |
|--------|-------------|----------|
| Per-gateway certs, ID in SPIFFE path | .../gw/<id> | ✓ |
| Shared cert, GatewayId = cert identity | Replicas collapse | |
| Shared cert, trust claimed id | Doesn't bind | |

| Option | Description | Selected |
|--------|-------------|----------|
| Terminate stream with PermissionDenied | | ✓ |
| Drop message, keep stream | | |

| Option | Description | Selected |
|--------|-------------|----------|
| Dedicated control-plane server cert, verify SAN, no plaintext fallback | | ✓ |
| Reuse gateway-server cert | | |

| Option | Description | Selected |
|--------|-------------|----------|
| Move chaos/integration tests to mTLS + negative tests | | ✓ |
| Keep insecure in tests | | |

| Option | Description | Selected |
|--------|-------------|----------|
| Separate per-gateway control-plane client cert | Backend pinned SPIFFE ID unchanged | ✓ |
| Replace shared cert, backends prefix-match | | |

| Option | Description | Selected |
|--------|-------------|----------|
| GatewayId derived from cert; env must match | | ✓ |
| AEGIS_GATEWAY_ID env as source | | |

**User's choice:** All recommended options.
**Notes:** Scout found that backends pin the exact gateway SPIFFE ID, which led to the separate-cert question.

---

## K8s proof of done (B2)

| Option | Description | Selected |
|--------|-------------|----------|
| Hermetic cross-check tests against binaries | | ✓ |
| Cross-check + live kind bring-up | | |
| kustomize build + kubeconform only | | |

| Option | Description | Selected |
|--------|-------------|----------|
| Secret/ConfigMap refs + documented creation | | ✓ |
| Kustomize secretGenerator from deployments/certs | | |

| Option | Description | Selected |
|--------|-------------|----------|
| Ordinal-named per-pod certs | | ✓ |
| You decide | | |

**User's choice:** All recommended options.

---

## Claude's Discretion

- Table, column, marker and env names; poll interval; metric and reason-code names
- K8s per-pod cert mounting mechanism
- Runbook rewrites to match the real mechanisms

## Deferred Ideas

- Live K8s bring-up (kind/minikube)
- kid-based key selection
- mTLS CA / workload cert hot reload
- Full K8S-01 (v2)
