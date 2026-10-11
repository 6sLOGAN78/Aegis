---
phase: 10-close-gaps-b5-b6-dashboard-and-control-plane-flows-ops-03-ct
reviewed: 2026-10-11T06:21:57Z
depth: standard
files_reviewed: 27
files_reviewed_list:
  - cmd/control-plane/main.go
  - deployments/compose/docker-compose.distributed.yml
  - deployments/compose/docker-compose.hardened.yml
  - deployments/compose/docker-compose.mvp.yml
  - deployments/compose/seed/seed.sh
  - internal/control/api.go
  - internal/control/api_test.go
  - internal/control/control_test.go
  - internal/control/gateway_handler_test.go
  - internal/control/idempotency.go
  - internal/control/idempotency_required_test.go
  - internal/control/identity_mappings.go
  - internal/control/identity_mappings_test.go
  - internal/control/policy_handler.go
  - internal/control/publish_handler.go
  - internal/control/publish_handler_test.go
  - internal/control/server.go
  - internal/snapshot/client.go
  - internal/snapshot/client_test.go
  - internal/snapshot/manager.go
  - scripts/compose-smoke.sh
  - tests/compose/compose_test.go
  - tests/compose/smoke_script_test.go
  - tests/integration/operator_telemetry_test.go
  - web/dashboard/src/api/idempotency.ts
  - web/dashboard/src/components/EmergencyQuarantine.tsx
  - web/dashboard/src/components/PolicyStudio.tsx
findings:
  critical: 1
  warning: 9
  info: 9
  total: 19
status: issues_found
---

# Phase 10: Code Review Report (re-review after gap closure 10-07 / 10-08)

**Reviewed:** 2026-10-11T06:21:57Z
**Depth:** standard
**Files Reviewed:** 27
**Status:** issues_found

## Narrative Findings (AI reviewer)

## Summary

Scope: `git diff 55016e4..HEAD` over the listed files. This re-review checks the two gap-closure plans and re-checks every finding from the prior review.

`go build ./...` and `go vet` are clean. `go test ./internal/snapshot/ ./internal/control/` passes.

### Prior-finding disposition

| Prior ID | Status | Evidence |
|---|---|---|
| CR-01 snapshot re-delivery sets REJECTED | **RESOLVED** | `internal/snapshot/client.go:299-316`: `processSnapshot` treats `Version == active.Version && PayloadSha256 == active.PayloadSha256` as an idempotent ACTIVATED confirm and keeps the previous status. `manager.go` now stores `PayloadSha256` in the only `ActiveState` constructor (`manager.go:141`). The CP bootstrap payload is deterministic (no timestamps in `SnapshotPayload`), so an in-memory CP restart re-delivers the same sha. Older versions and same-version-different-hash are still rejected, and unit tests cover both. Remaining nits are IN-07, IN-08 and IN-09. |
| CR-02 unauthenticated gRPC stream | **STILL PRESENT (BLOCKER)** | `cmd/control-plane/main.go:288` is still `grpc.NewServer()` with no creds. `server.go:92-120` still trusts the client-supplied `GatewayId`. |
| CR-03 `crypto.randomUUID` on non-secure origins | **RESOLVED** | `web/dashboard/src/api/idempotency.ts` falls back to `crypto.getRandomValues` and sets the v4 and variant bits correctly. No raw `crypto.randomUUID()` call is left in `src/` outside the helper. `PolicyStudio.tsx:130-131` uses a lazy ref initializer, so render no longer throws. The rebuilt `dist/assets/index-*.js` contains the guarded helper, with both `randomUUID` occurrences inside the guard. A minor nit remains (IN-10). |
| WR-01 Pong TOCTOU / dead branch | STILL PRESENT | `server.go:105-119` is unchanged. |
| WR-02 client-supplied `AcknowledgedAt` | STILL PRESENT | `server.go:98` only fills the timestamp when it is nil. |
| WR-03 lease version mismatch suppresses heartbeat | STILL PRESENT, and wider than first reported | See WR-03 below. Because of this, the "echo REJECTED in heartbeats" path cannot be reached for the main rejection case. |
| WR-04 idempotency cache unscoped, runs before RBAC | STILL PRESENT | `api.go:108` |
| WR-05 5xx cached for 24h | STILL PRESENT | `idempotency.go:189-190` |
| WR-06 save-then-publish TOCTOU | STILL PRESENT | `PolicyStudio.tsx:136-147` |
| WR-07 mappings not authoritative on rollback/bootstrap | STILL PRESENT | `TestPublish_IdentityMappings_Rollback` now locks in historical-mapping restore, and nothing tests the configured-mappings case. The bootstrap is still built before the mappings load (`main.go:191` vs `main.go:257`). |
| WR-08 smoke idle check fails on mvp/hardened `--stop-check` | STILL PRESENT | Only `docker-compose.distributed.yml` sets `AEGIS_GATEWAY_ID`. |
| WR-09 concurrent publish gets 500 not 412 | STILL PRESENT | `publish_handler.go:48` vs `:147` |
| IN-01 `test_` substring trigger | STILL PRESENT | `publish_handler.go:78` |
| IN-02 hard-coded module name / unencoded `policyId` | STILL PRESENT | `PolicyStudio.tsx:140, 144` |
| IN-03 whitespace roles/perms accepted | STILL PRESENT | `identity_mappings.go:31-41` |
| IN-04 OpenAPI 503 undocumented | NOT RE-REVIEWED | `api/openapi/control-v1.yaml` is outside this review's file scope. |
| IN-05 double-click publish | **RESOLVED** | The publish confirm button is `disabled={publishMutation.isPending}` (`PolicyStudio.tsx:534`). |
| IN-06 silent empty mappings | STILL PRESENT | `publish_handler.go:26-33` |

## Critical Issues

### CR-02: Snapshot gRPC stream is unauthenticated; any client can spoof any gateway's ack/heartbeat state and receive snapshots that carry RBAC mappings (UNRESOLVED from prior review)

**File:** `cmd/control-plane/main.go:288`, `internal/control/server.go:92-120`
**Issue:** `grpc.NewServer()` has no TLS or mTLS credentials, and the gateway dials with `insecure.NewCredentials()` by default (`internal/snapshot/client.go:136-138`). The server takes `ack.GatewayId` and `req.GatewayId` as given. Any host on the compose network that can reach `control-plane:9090` can:
- report a dead or rejected replica as healthy by sending `Ack{GatewayId:"gateway-2", Status:ACTIVATED, AcknowledgedAt:<future>}` (made worse by WR-02);
- mark healthy replicas REJECTED or partitioned;
- grow the gateway ack table without limit through `RecordGatewayAck`;
- subscribe and read every signed snapshot, which now includes `IdentityMappings` (role-to-permission RBAC).

Nothing in 10-07 or 10-08 touched this. Plaintext transport also allows on-path tampering with leases. The signature check rejects forged leases, but an attacker can drop them, which forces gateways to fail closed.
**Fix:** Serve gRPC with mTLS, and take the gateway identity from the verified peer certificate:
```go
creds := credentials.NewTLS(&tls.Config{ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool, Certificates: []tls.Certificate{cpCert}, MinVersion: tls.VersionTLS13})
grpcServer := grpc.NewServer(grpc.Creds(creds))
// in StreamSnapshots Recv goroutine:
p, _ := peer.FromContext(stream.Context())
gwID, err := gatewayIDFromSPIFFE(p)   // URI SAN of verified leaf
if err != nil || (req.GatewayId != "" && req.GatewayId != gwID) { return status.Error(codes.PermissionDenied, "gateway id mismatch") }
ack.GatewayId = gwID
```
Interim mitigation until mTLS ships: pin the stream to the first `GatewayId` it sends and drop any message that claims a different ID. Record the remaining exposure as tracked security debt.

## Warnings

### WR-01: Pong "never upgrade REJECTED" guard is a non-atomic read-then-write, and no real gateway can reach it

**File:** `internal/control/server.go:105-119`
**Issue:** The code is unchanged from the prior review. `GetReplicaStatuses()` (RLock, copy, unlock) and `RecordAck()` (write lock) are separate critical sections. A REJECTED ack recorded between the two calls gets overwritten with ACTIVATED. The server never sends `ControlPlaneMessage_Ping` in production, so only unsolicited Pongs (see CR-02) reach this branch.
**Fix:** Delete the Pong branch until the server sends Pings, or move the guard into one `AckTracker.RecordHeartbeat` method that does the check and the write under `a.mu.Lock()`.

### WR-02: Server keeps the client-supplied `AcknowledgedAt`, so gateway clock skew or a spoofed timestamp decides liveness

**File:** `internal/control/server.go:98-100`
**Issue:** `AcknowledgedAt` is replaced only when it is nil. `HandleListGateways` uses it as the last heartbeat. A gateway clock running more than 60s fast keeps a dead replica "healthy" for that much longer. A clock running more than 60s slow shows a live replica as partitioned. A future timestamp (CR-02) hides a dead replica indefinitely.
**Fix:** In the Recv goroutine, always set `ack.AcknowledgedAt = timestamppb.Now()`. If the gateway's own timestamp is needed, store it in a separate column.

### WR-03: Lease/version coupling means the REJECTED heartbeat echo can't be reached for a rejected publish, and a lagging gateway goes partitioned (and fails closed) instead of degraded

**File:** `internal/snapshot/client.go:227-245`; `internal/snapshot/verifier.go:118-121`
**Issue:** The CP signs every lease for its active version N+1. If a gateway rejects N+1 (for example, the Rego fails to compile on the gateway) or misses the broadcast (`BroadcastSnapshot` drops the message when the channel is full), it stays on N. Then `VerifyLease(lease N+1, expected N)` fails on every tick. As a result:
1. No heartbeat is sent, so the `buildHeartbeatAck(..., lastStatus=REJECTED, ...)` path that 10-01 added (and `TestHeartbeat_PreservesRejected` covers only at the helper level) never runs for the most important rejection case.
2. `RecordLeaseRenewal()` is never called, so the gateway's lease expires and it fails closed even though it holds a valid signed N.
3. After 60s the CP shows the replica as partitioned, not REJECTED or degraded. Nothing triggers a resync.

Heartbeats now echo REJECTED only when the rejected snapshot is not the CP's active one, which is rare.
**Fix:** Split `VerifyLease` into a signature-and-expiry check and a separate version comparison. On a lease that is validly signed but has a different version, send a heartbeat with the gateway's real `ActiveVersion` and the preserved `lastStatus`. Also call `GetActiveSnapshot` to resync if the gateway is behind. Decide explicitly, and document, whether a version-lagging gateway should keep renewing its lease.

### WR-04: Idempotency cache is not scoped to session/method/route and runs before `RequireRole`

**File:** `internal/control/api.go:108`; `internal/control/idempotency.go:136-170`
**Issue:** `pr.Use(IdempotencyMiddleware)` runs before the per-route `RequireRole` and `RequireIdempotencyKey`. The cache key is the raw header value. If a lower-privileged session replays a known key K, possibly against a different endpoint, it gets the cached response back with `Idempotent-Replay: true`, and `RequireRole` never runs for that request. Every mutation now has to send a key, so keys show up routinely in logs and HAR captures.
**Fix:** Build the cache key as `sessionUserID + "|" + method + "|" + chi.RouteContext(r).RoutePattern() + "|" + key`. Store a hash of the request body and return 422 when the same key arrives with a different body. Apply the middleware per route, after `RequireRole`.

### WR-05: Idempotency middleware caches 5xx responses for 24h

**File:** `internal/control/idempotency.go:189-190`
**Issue:** The comment says "successful or expected client error", but every status gets cached. Publish returns 503 (validator or storage unavailable) and 500 (persist failure). A retry with the same key replays that transient failure for 24h. `seed.sh` (`cp_call`) and PolicyStudio work around it by minting a new key on every attempt, which removes the retry-safety that idempotency keys are supposed to give.
**Fix:**
```go
if statusCode >= 500 { s.Release(key) } else { s.Complete(key, statusCode, buf.Header(), buf.body.Bytes()) }
completed = true
```

### WR-06: Policy Studio save-draft-then-publish is a TOCTOU; another operator's bytes can be signed

**File:** `web/dashboard/src/components/PolicyStudio.tsx:136-147`; `internal/control/publish_handler.go:61`
**Issue:** The draft upsert and the publish are two separate requests, and publish re-reads the draft from the DB. If another sec-ops user saves the same `policy_id` between the two requests, that user's Rego gets signed and broadcast. When publish fails (412 or 422), the shared draft has still been overwritten.
**Fix:** Send `expected_source_sha256` (or a draft revision) in the publish body and return 409 when it doesn't match, or make publish accept the source and persist it atomically.

### WR-07: Configured identity mappings are not authoritative for bootstrap or rollback

**File:** `cmd/control-plane/main.go:191-218, 257-266`; `internal/control/rollback.go:66-76`; `internal/control/publish_handler_test.go:190-230`
**Issue:** The mappings file is loaded after the bootstrap v1 snapshot is signed, so v1 never carries the configured mappings. Gateways run with no role mappings until the first publish. Rollback re-signs the historical payload as it was, so rolling back to a pre-mappings snapshot removes all RBAC even when `AEGIS_IDENTITY_MAPPINGS_FILE` is set. `TestPublish_IdentityMappings_Rollback` checks historical restore only with no configured mappings, so the behavior that conflicts with "config is authoritative" (`publish_handler.go:17-18`) is untested.
**Fix:** Load the mappings before building the bootstrap and include them in `initialPayload`. Pass a mappings resolver into `RollbackEngine` and override `payload.IdentityMappings` when mappings are configured. Add a test where configured mappings differ from the historical ones.

### WR-08: Smoke idle-health check fails every time on mvp/hardened with `--stop-check`

**File:** `scripts/compose-smoke.sh:945-958`; `deployments/compose/docker-compose.mvp.yml`, `deployments/compose/docker-compose.hardened.yml` (gateway service env)
**Issue:** Only the distributed profile pins `AEGIS_GATEWAY_ID`. In mvp and hardened, the gateway generates `gateway-<rand>` at each start. The GRACE `dc stop`/`dc up` cycle registers a new ID, and the old one stays in the AckTracker and DB with a frozen heartbeat. After `sleep 65` the old ID shows partitioned, and `all(.gateways[]; .status == "healthy")` fails. Leftover rows on a persistent `postgres_data` volume cause the same failure.
**Fix:** Add `AEGIS_GATEWAY_ID=gateway-1` to the gateway in both files, and assert it in `tests/compose/compose_test.go`. Also either age out replicas with no heartbeat for more than N minutes in `HandleListGateways`, or limit the assertion to the IDs of running services.

### WR-09: Concurrent publishes: the slower request gets 500 instead of 412

**File:** `internal/control/publish_handler.go:40-50, 145-151`
**Issue:** The If-Match check reads the active version without a lock. Rego validation and the unit-test run then sit between that check and `SaveSnapshot`. Two publishes with the same ETag both compute N+1, and the second fails on the `snapshots.version` primary key with a generic 500. That breaks the OPS-02 412 contract.
**Fix:** Serialize check-to-broadcast with a mutex on `APIServer` (or use a DB CAS), and map unique-violation `23505` to `412 PRECONDITION_FAILED`.

## Info

### IN-01: `strings.Contains(source, "test_")` is a weak trigger for running Rego tests
**File:** `internal/control/publish_handler.go:78`
**Issue:** Identifiers like `latest_version` trigger a test run, and test rules ship inside the production snapshot. **Fix:** Parse the module and look for rules named `test_*`, or keep tests in a separate module that is not published.

### IN-02: Hard-coded module name and unencoded `policyId` in URLs
**File:** `web/dashboard/src/components/PolicyStudio.tsx:140, 144, 183`
**Fix:** Use `encodeURIComponent(policyId)`. Wire the policy name input into the request or remove it.

### IN-03: `LoadIdentityMappings` accepts whitespace-only roles and permissions; duplicate JSON keys are silently last-wins
**File:** `internal/control/identity_mappings.go:31-41`
**Fix:** Apply `strings.TrimSpace` before the empty checks. Consider a `json.Decoder` token walk to reject duplicate keys.

### IN-06: `resolveIdentityMappings` drops the unmarshal error and signs empty mappings
**File:** `internal/control/publish_handler.go:26-33`
**Fix:** Log the error, and refuse to publish when the mappings resolve to empty but the previous snapshot had them.

### IN-07 (new, 10-07): Re-delivery short-circuit acks ACTIVATED based on unverified header fields
**File:** `internal/snapshot/client.go:310-315`
**Issue:** The fast path compares the envelope's claimed `PayloadSha256` header with the active one. It does not recompute the digest or check the signature. Gateway state doesn't change, so this can't swap config. But over the plaintext stream (CR-02), an on-path attacker can send an envelope with the right header and junk payload and get an ACTIVATED ack echoed back. **Fix:** Also require `bytes.Equal(snap.Signature, activeSig)`, which means storing the signature in `ActiveState`, or recompute `sha256(snap.Payload)` before taking the fast path.

### IN-08 (new, 10-07): Per-stream status reset and unconditional ACTIVATED presence ack briefly hide a prior REJECTED
**File:** `internal/snapshot/client.go:164-184`
**Issue:** `lastStatus` is local to `runStream`, and the initial presence ack always sends ACTIVATED. After a reconnect, a gateway that was REJECTED shows ACTIVATED on the CP until the re-delivered snapshot is processed again. That window is usually short, but it is unbounded if the CP has no active snapshot. **Fix:** Store `lastStatus` and `lastErr` on `StreamClient` (under `mu`) so they survive reconnects, and send them in the presence ack.

### IN-09 (new, 10-07): CR-01 fix has only unit-level coverage
**File:** `internal/snapshot/client_test.go:156-196`
**Issue:** The tests call `processSnapshot` directly. Nothing runs a real `StreamSnapshots` reconnect against the same `Manager` and checks that the CP-side `AckTracker` status stays ACTIVATED, which was the scenario the prior review asked for. A later refactor of `runStream` (for example, calling `ValidateAndActivate` directly again) would not be caught. **Fix:** Add a bufconn test in `internal/control/control_test.go` that starts a `StreamClient`, cancels the stream, reconnects, and asserts `GetReplicaStatuses()[gw].Status == ACTIVATED`.

### IN-10 (new, 10-08): `typeof crypto` guard is applied only halfway
**File:** `web/dashboard/src/api/idempotency.ts:6-9`
**Issue:** The guard allows for `crypto` being undefined, but the fallback calls `crypto.getRandomValues` anyway and throws a `ReferenceError` in that case. Every supported browser defines `crypto`, so this has no practical impact, but the guard suggests a safety it doesn't provide. Nothing tests the fallback branch. **Fix:** Drop the `typeof crypto` check, or throw a clear error. Add a vitest case that stubs `crypto.randomUUID = undefined` and checks the v4 format (`/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/`).

---

_Reviewed: 2026-10-11T06:21:57Z_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
