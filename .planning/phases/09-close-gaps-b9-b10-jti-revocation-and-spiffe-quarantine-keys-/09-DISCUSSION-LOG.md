# Phase 9: Close gaps B9, B10: jti revocation and SPIFFE quarantine keys - Discussion Log

> **Audit trail only.** Do not use as input to planning, research, or execution agents.
> Decisions are captured in CONTEXT.md — this log preserves the alternatives considered.

**Date:** 2026-10-10
**Phase:** 09-close-gaps-b9-b10-jti-revocation-and-spiffe-quarantine-keys
**Areas discussed:** Issuer credentials, jti and revocation UX, Quarantine API shape, Proof of done

---

## Issuer credentials

| Option | Description | Selected |
|--------|-------------|----------|
| Username + password | Seeded users with demo passwords; wrong password returns 401 | ✓ |
| Role + shared demo secret | Keep the role body and add one shared secret | |
| Keep role-only, reword AUTH-04 | No code change; accept with a note | |

| Option | Description | Selected |
|--------|-------------|----------|
| Env vars with demo defaults | Per-user env var; warning logged when the default is used | ✓ |
| Hardcoded in the seed map | Simplest; another hardcoded secret | |
| You decide | Claude picks | |

| Option | Description | Selected |
|--------|-------------|----------|
| Update every caller in this phase | Old body returns 400 | ✓ |
| Accept both behind a flag | Compatibility path | |

| Option | Description | Selected |
|--------|-------------|----------|
| Keep per-IP throttling | Existing checkRateLimit | ✓ |
| Add per-username lockout | New behavior | |

**User's choice:** all recommended options.

---

## jti and revocation UX

| Option | Description | Selected |
|--------|-------------|----------|
| Random UUID v4 | uuid.NewString() | ✓ |
| Prefixed random id | jti_<base32> | |

| Option | Description | Selected |
|--------|-------------|----------|
| Login response + audit rows | jti in issuer JSON and in audit events | ✓ |
| Login response only | Smallest change | |
| Decode it from the token | New dashboard feature | |

| Option | Description | Selected |
|--------|-------------|----------|
| Keep 24h default, allow override | Current behavior | ✓ |
| Match the token's remaining lifetime | Needs the token's exp | |

| Option | Description | Selected |
|--------|-------------|----------|
| Deny tokens without jti | Fail closed | ✓ |
| Allow tokens without jti | Compatibility | |

**User's choice:** all recommended options.
**Notes:** Audit events have no jti field today, so D-07 needs migration 000004, an event field and a worker insert change.

---

## Quarantine API shape

| Option | Description | Selected |
|--------|-------------|----------|
| Decode {id} on the server | url.PathUnescape; dashboard unchanged | ✓ |
| Principal id in the request body | API change | |
| Both | Decode path and make body work | |

| Option | Description | Selected |
|--------|-------------|----------|
| Make POST /quarantine work with principal_id in the body | Body-form handler | ✓ |
| Remove the route | One way to quarantine | |

| Option | Description | Selected |
|--------|-------------|----------|
| Validate SPIFFE form, keep users as is | Reject malformed spiffe:// ids with 400 | ✓ |
| No validation | Store whatever arrives | |

| Option | Description | Selected |
|--------|-------------|----------|
| Only quarantine + revocation routes | Spec the routes this phase touches | ✓ |
| All missing routes | Close OPS-01 drift fully | |

**User's choice:** all recommended options.

---

## Proof of done

| Option | Description | Selected |
|--------|-------------|----------|
| All three profiles | mvp, hardened, distributed | ✓ |
| mvp + distributed | Skip hardened | |
| Hermetic tests only | No live run | |

| Option | Description | Selected |
|--------|-------------|----------|
| Measure on every replica | Poll gateway-1/2/3 directly, all must deny within 5s | ✓ |
| Single check after 5s | One request via HAProxy | |

| Option | Description | Selected |
|--------|-------------|----------|
| JTI revoke → TOKEN_REVOKED | | ✓ |
| Workload quarantine on :9443 | | ✓ |
| Body-form POST /quarantine | | ✓ |
| Audit row carries jti | | ✓ |

| Option | Description | Selected |
|--------|-------------|----------|
| Pre-flight disk check in smoke | Fail fast above ~85% | ✓ |
| Nothing; free space first | No script change | |

**User's choice:** all recommended options; every flow selected.

---

## Claude's Discretion

- Env var names, reason-code names, shared or separate handler core.
- Whether the dashboard audit stream shows a jti column.
- 401 vs 403 for revoked tokens and tokens without a jti (follow existing conventions).

## Deferred Ideas

- Per-username login lockout.
- Dashboard JWT paste to extract the jti.
- Revocation TTL from the token's remaining lifetime.
- Remaining OpenAPI drift (auth/*, simulate, rollback, audit/events).
- B8 wiring (ReconstructQuarantines, rotation keysets).
