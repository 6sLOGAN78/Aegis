---
phase: 10-close-gaps-b5-b6-dashboard-and-control-plane-flows-ops-03-ct
reviewed: 2026-10-11T05:47:30Z
depth: standard
files_reviewed: 28
files_reviewed_list:
  - api/openapi/control-v1.yaml
  - cmd/control-plane/main.go
  - deployments/compose/docker-compose.distributed.yml
  - deployments/compose/docker-compose.hardened.yml
  - deployments/compose/docker-compose.mvp.yml
  - deployments/compose/seed/seed.sh
  - go.mod
  - internal/control/api.go
  - internal/control/api_test.go
  - internal/control/control_test.go
  - internal/control/gateway_handler_test.go
  - internal/control/idempotency.go
  - internal/control/idempotency_required_test.go
  - internal/control/identity_mappings.go
  - internal/control/identity_mappings_test.go
  - internal/control/openapi_drift_test.go
  - internal/control/policy_handler.go
  - internal/control/publish_handler.go
  - internal/control/publish_handler_test.go
  - internal/control/server.go
  - internal/snapshot/client.go
  - internal/snapshot/client_test.go
  - scripts/compose-smoke.sh
  - tests/compose/compose_test.go
  - tests/compose/smoke_script_test.go
  - tests/integration/operator_telemetry_test.go
  - web/dashboard/src/components/EmergencyQuarantine.tsx
  - web/dashboard/src/components/PolicyStudio.tsx
findings:
  critical: 3
  warning: 9
  info: 6
  total: 18
status: issues_found
---

# Phase 10: Code Review Report

**Reviewed:** 2026-10-11T05:47:30Z
**Depth:** standard
**Files Reviewed:** 28
**Status:** issues_found

## Narrative Findings (AI reviewer)

## Summary

Scope: `git diff 48ce48d..HEAD` across the files listed. This phase adds mandatory Idempotency-Key gating, publish-time Rego validation, identity-mapping config, lease-driven gateway heartbeats, a server-side Pong handler, OpenAPI drift tests, and dashboard changes.

Main concerns:
1. The heartbeat design does not survive a stream reconnect. After any control-plane restart or gRPC blip, every gateway sends REJECTED heartbeats until the next publish.
2. The snapshot gRPC server has no transport security at all, so there is no authenticated peer identity. Any client that can reach port 9090 can write ack state for any gateway. The snapshots it receives now also carry the RBAC role mappings.
3. The dashboard calls `crypto.randomUUID()`, which exists only in secure contexts. The control plane serves the SPA over plain HTTP on a host-published port, so Policy Studio crashes during render for remote operators.

Orchestrator-flagged items:
- **authz-identity-spoofing:** Confirmed. It is worse than reported, because there is no mTLS at all (`grpc.NewServer()` with no credentials). Classified **BLOCKER** (CR-02). The root cause predates this phase. This phase adds a second unauthenticated write path (Pong) and puts identity mappings into the open stream.
- **fail-open-state-drift:** Confirmed as a real TOCTOU. Its practical impact is small because (a) the server never sends Ping in production, so the Pong branch can't be reached by a well-behaved gateway, and (b) the Ack path already overwrites status with no guard. Classified **WARNING** (WR-01).

## Critical Issues

### CR-01: Re-delivered snapshot on reconnect sets REJECTED, and heartbeats then keep sending it (fleet shows partitioned after any CP restart)

**File:** `internal/snapshot/client.go:216-231, 248-259` (interacts with `internal/control/server.go:53-61`, `internal/snapshot/verifier.go:70`)
**Issue:** On every new stream, `StreamSnapshots` immediately pushes the current active snapshot (version N). A gateway that already runs N (stream reconnect, CP restart, rolling CP deploy) calls `ValidateAndActivate`. `VerifySnapshot` rejects it because `env.Version <= currentVersion` (`ErrMonotonicVersionViolation`). The client sets `lastStatus, lastErr = nextAckState(actErr)` to REJECTED and sends a REJECTED ack. Every verified lease after that sends `buildHeartbeatAck(..., lastStatus=REJECTED, ...)`. `HandleListGateways` maps REJECTED to `partitioned`. The result is that all healthy gateways show as partitioned in the dashboard until someone publishes. The idle-health gap this phase was meant to close comes back after the first reconnect. The new tests do not cover this, because `TestControl_HeartbeatOnLease` only uses a fresh manager.
**Fix:** Treat delivery of the already-active version as a no-op, not a rejection:
```go
case *snapshotv1.ControlPlaneMessage_Snapshot:
    snap := p.Snapshot
    if cur := c.manager.Active(); cur != nil && snap.Version == cur.Version {
        // idempotent re-delivery on (re)connect: confirm, don't change lastStatus
        _ = stream.Send(ackMsg(c.gatewayID, cur.Version, snapshotv1.AckStatus_ACK_STATUS_ACTIVATED, ""))
        continue
    }
    actErr := c.manager.ValidateAndActivate(ctx, snap)
    ...
```
Add an integration test that restarts the stream against the same manager and checks that the status stays ACTIVATED.

### CR-02: Snapshot gRPC stream is unauthenticated; any client can spoof any gateway's ack/heartbeat state and receive signed snapshots that now include RBAC mappings

**File:** `cmd/control-plane/main.go:288`, `internal/control/server.go:92-119`
**Issue:** `grpc.NewServer()` is created with no TLS or mTLS credentials. No peer identity exists to bind to, so `ack.GatewayId` and `req.GatewayId` are fully attacker-controlled. Any host that can reach `control-plane:9090` (every container on the compose network) can:
(a) send `Ack{GatewayId:"gateway-2", Status:ACTIVATED, AcknowledgedAt:<far future>}` to hide a rejected or dead replica as healthy indefinitely (see WR-02);
(b) mark healthy replicas REJECTED or partitioned;
(c) write unlimited rows into the gateway ack table through `RecordGatewayAck`.
This phase adds a second unauthenticated write path (the Pong branch, lines 105-119). It also starts publishing `IdentityMappings` (role to permissions) inside snapshots that go to any unauthenticated subscriber. The signature protects integrity but not confidentiality.
**Fix:** Serve gRPC with mTLS (`grpc.Creds(credentials.NewTLS(&tls.Config{ClientAuth: tls.RequireAndVerifyClientCert, ...}))`). In the Recv goroutine, get the identity from the verified peer certificate (SPIFFE URI SAN) and use it in place of or to check `GatewayId`:
```go
p, _ := peer.FromContext(stream.Context())
gwID := gatewayIDFromPeer(p) // from verified SPIFFE SAN
if req.GatewayId != gwID { continue } // or close stream
ack.GatewayId = gwID
```
Until mTLS ships, at minimum reject an ack whose GatewayId differs from the first GatewayId seen on that stream, and record the issue as a tracked security debt.

### CR-03: `crypto.randomUUID()` is undefined over plain HTTP from non-localhost, so Policy Studio crashes on render and quarantine/revoke/rollback all throw

**File:** `web/dashboard/src/components/PolicyStudio.tsx:129, 139, 148, 158, 184`; `web/dashboard/src/components/EmergencyQuarantine.tsx:73, 96, 115`
**Issue:** `crypto.randomUUID` is only exposed in secure contexts (HTTPS or localhost). The control plane serves the SPA with `apiSrv.ListenAndServe()` (plain HTTP, `cmd/control-plane/main.go:277`), and `8084:8084` is published to the host in the distributed and hardened compose files. An operator who browses to `http://<host>:8084` gets `TypeError: crypto.randomUUID is not a function`. In PolicyStudio, `useRef<string>(crypto.randomUUID())` evaluates its argument on every render, so the whole component throws during render, not just the publish action. Every emergency quarantine, unquarantine, and revocation mutation fails too, and this is the incident-response surface.
**Fix:** Add one helper with a fallback, and use a lazy ref initialiser:
```ts
// src/api/idempotency.ts
export function newIdempotencyKey(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') return crypto.randomUUID();
  const b = crypto.getRandomValues(new Uint8Array(16)); // available in insecure contexts
  b[6] = (b[6] & 0x0f) | 0x40; b[8] = (b[8] & 0x3f) | 0x80;
  const h = [...b].map(x => x.toString(16).padStart(2, '0')).join('');
  return `${h.slice(0,8)}-${h.slice(8,12)}-${h.slice(12,16)}-${h.slice(16,20)}-${h.slice(20)}`;
}
// PolicyStudio
const publishKeyRef = useRef<string | null>(null);
if (publishKeyRef.current === null) publishKeyRef.current = newIdempotencyKey();
```

## Warnings

### WR-01: Pong "never upgrade REJECTED" guard is a non-atomic read-then-write, can't be reached in production, and has no effect on the real heartbeat path

**File:** `internal/control/server.go:105-119`
**Issue:** (orchestrator item 2) `GetReplicaStatuses()` takes the RLock, copies, and releases it. `RecordAck()` then takes the write lock separately. A REJECTED ack recorded by another stream or goroutine for the same GatewayId between those two calls gets overwritten with ACTIVATED. The memory write and the `RecordGatewayAck` DB write also happen outside one critical section, so the memory and DB order can diverge. Two more problems: no production code builds `ControlPlaneMessage_Ping` (grep shows only tests), so this branch is dead for real gateways and only reachable by unsolicited Pongs. And the real heartbeat (client lease acks) goes through the unguarded Ack branch at lines 92-103, so the stated invariant is not enforced where it matters.
**Fix:** Either delete the Pong branch until the server sends Pings, or move the guard into the tracker as one atomic operation:
```go
func (a *AckTracker) RecordHeartbeat(ctx context.Context, gwID string, ver int64) error {
    a.mu.Lock()
    rec := &snapshotv1.SnapshotAck{GatewayId: gwID, ActiveVersion: ver,
        Status: snapshotv1.AckStatus_ACK_STATUS_ACTIVATED, AcknowledgedAt: timestamppb.Now()}
    if prior, ok := a.statuses[gwID]; ok && prior.Status == snapshotv1.AckStatus_ACK_STATUS_REJECTED {
        rec.Status, rec.ErrorMessage = prior.Status, prior.ErrorMessage
    }
    a.statuses[gwID] = rec
    a.mu.Unlock()
    if a.repo != nil { return a.repo.RecordGatewayAck(ctx, rec) }
    return nil
}
```

### WR-02: Heartbeat acks use the client-supplied `AcknowledgedAt`, so clock skew or a future timestamp keeps a dead gateway "healthy"

**File:** `internal/control/server.go:96-98`; `internal/snapshot/client.go:291-299`
**Issue:** The server keeps `ack.AcknowledgedAt` when it is non-nil. Heartbeats are now the liveness signal, and `HandleListGateways` computes `now.Sub(lastHeartbeatAt) > 60s` with no upper bound. A gateway clock that runs ahead (or a spoofed ack, CR-02) keeps a replica healthy long after it dies. A clock that runs more than 60s behind makes a live gateway show partitioned. The Pong branch uses server time, which is correct, but the Ack branch, which is the one actually used, does not.
**Fix:** In the Recv goroutine, always overwrite `ack.AcknowledgedAt = timestamppb.Now()` (server receive time). If the gateway's timestamp is needed, keep it in a separate field.

### WR-03: A lagging gateway can never be reported "degraded"; lease version mismatch suppresses heartbeats and it shows as partitioned

**File:** `internal/snapshot/client.go:241-260`; `internal/control/lease.go:65-71`
**Issue:** Leases are signed for the CP's active version. If a gateway missed a broadcast (`BroadcastSnapshot` drops the message when the channel is full) and is on N-1, `VerifyLease(lease N, expected N-1)` fails and no heartbeat is sent. After 60s the replica is partitioned rather than degraded, and nothing triggers a resync. The degraded state documented in `gateway_handler.go:96` can't be reached through heartbeats.
**Fix:** On a lease version mismatch where the signature is valid, still send a heartbeat with the gateway's real `ActiveVersion`, and/or request a resync (`GetActiveSnapshot`). Split signature verification from the version check in `VerifyLease`.

### WR-04: Idempotency cache is not scoped to session/method/path and runs before `RequireRole`; mandatory keys make cross-endpoint and cross-user replay part of the core contract

**File:** `internal/control/api.go:108`; `internal/control/idempotency.go:136-160`
**Issue:** `IdempotencyMiddleware` is applied with `pr.Use` before the per-route `RequireRole`/`RequireIdempotencyKey`. The cache key is the raw header value. A request that reuses key K on a different endpoint, or from a different (lower-privileged) session, gets the cached response with `Idempotent-Replay: true`, and `RequireRole` never runs. Now that every mutation must carry a key, that key shows up in client logs, proxies, and HAR files.
**Fix:** Key the cache on `sessionUserID + method + routePattern + key`, store a request-body hash, and return 422 when the same key arrives with a different fingerprint. Move the idempotency step after the role check (apply it per route after `RequireRole`).

### WR-05: Idempotency middleware caches 5xx responses for 24h

**File:** `internal/control/idempotency.go:180-182`
**Issue:** The comment says "Cache successful or expected client error responses", but every status is cached, including 500 and 503. The new publish code returns 503 (validator or storage unavailable) and 500 (persist failure). A retry with the same key, which is what idempotency keys exist for, replays the transient failure for 24h. `seed.sh` and PolicyStudio work around this by minting a new key on every attempt, which removes the retry guarantee.
**Fix:** `if statusCode >= 500 { s.Release(key) } else { s.Complete(...) }` and keep `completed` set accordingly.

### WR-06: Policy Studio "save draft then publish" is a TOCTOU; another operator's draft can be signed while the UI claims the editor bytes were published

**File:** `web/dashboard/src/components/PolicyStudio.tsx:135-145`; `internal/control/publish_handler.go:61`
**Issue:** The UI upserts the draft (`ON CONFLICT DO UPDATE`) and then calls publish as a separate request. Publish re-reads the draft from the DB. A concurrent save to the same `policy_id` by another sec-ops operator between the two requests gets signed and broadcast instead. The comment "published bytes match the editor" is not guaranteed. If publish fails (412/422), the draft has still been overwritten.
**Fix:** Have publish accept the expected `source_rego` SHA-256 (or a draft revision/ETag) in the body and return 409 or 412 on mismatch. Alternatively, publish the submitted source atomically in one request.

### WR-07: "Configured mappings are authoritative" is not enforced by rollback or bootstrap

**File:** `internal/control/publish_handler.go:17-36`; `internal/control/rollback.go:66-76`; `cmd/control-plane/main.go:190-212, 257-266`
**Issue:** Rollback re-signs the historical payload exactly as it was. Rolling back to a snapshot taken before mappings existed publishes empty `IdentityMappings` even when `AEGIS_IDENTITY_MAPPINGS_FILE` is set, and gateways lose every role-to-permission mapping. The bootstrap v1 snapshot is built before the mappings are loaded and never includes them.
**Fix:** Load the mappings before the bootstrap snapshot is built and include them. In `RollbackEngine`, override `payload.IdentityMappings` with the configured mappings when they are present (pass a resolver in), or document clearly that rollback restores historical RBAC.

### WR-08: Idle-health smoke check fails every time with `--stop-check` on mvp/hardened (stale random gateway IDs are never pruned)

**File:** `scripts/compose-smoke.sh:945-958`
**Issue:** mvp and hardened do not set `AEGIS_GATEWAY_ID`, so the gateway creates `gateway-<rand>` at each process start (`cmd/gateway/main.go:107-110`). The GRACE step (`dc stop` then `dc up`) restarts the gateway under a new ID. The old ID stays in the AckTracker and the DB with a frozen heartbeat. After `sleep 65` it is partitioned, and `all(.gateways[]; .status == "healthy")` fails. Leftover DB rows from earlier runs on a persistent volume cause the same failure.
**Fix:** Set fixed `AEGIS_GATEWAY_ID` in the mvp/hardened compose files. Also, either have `HandleListGateways` prune or age out entries with no heartbeat for more than N minutes, or limit the smoke assertion to replicas that are currently running (`dc ps`).

### WR-09: Concurrent publishes: the longer If-Match to persist window makes the loser get 500 instead of 412

**File:** `internal/control/publish_handler.go:40-50, 146-150`
**Issue:** The If-Match check reads the active version and does not lock. Rego validation and test execution were added between that check and `SaveSnapshot`, which widens the window. Two publishes with the same ETag both compute N+1. The second fails on the `snapshots.version` primary key and gets a generic 500 "Failed to persist snapshot". That breaks the documented OPS-02 contract, which promises 412.
**Fix:** Run check-and-persist under a mutex in `APIServer` (or `SELECT ... FOR UPDATE` / a version CAS), and map a unique-violation error (`23505`) to 412 `PRECONDITION_FAILED`.

## Info

### IN-01: `strings.Contains(source, "test_")` is a weak trigger for running Rego unit tests
**File:** `internal/control/publish_handler.go:79`
**Issue:** Any identifier or comment that contains `test_` (for example `latest_version`) triggers a test run, and test rules ship inside the production snapshot. **Fix:** Parse the module and check for rules with a `test_` prefix, or keep tests in a separate module that is not published.

### IN-02: Draft module name is hard-coded and `policyId` is interpolated into URLs without encoding
**File:** `web/dashboard/src/components/PolicyStudio.tsx:136-145`
**Issue:** `name: 'authz.rego'` ignores the `policyName` input. `policyId` is user-editable and goes into URLs raw. **Fix:** Use `encodeURIComponent(policyId)` and either drop or wire up the unused name field.

### IN-03: `LoadIdentityMappings` accepts whitespace-only roles and permissions, and duplicate JSON keys silently resolve to the last one
**File:** `internal/control/identity_mappings.go:31-43`
**Fix:** Apply `strings.TrimSpace` before the empty checks. Consider a `json.Decoder`-based duplicate-key check.

### IN-04: The spec does not document the new 503 responses and the `policy_id` 400
**File:** `api/openapi/control-v1.yaml` (publish and POST /policies operations)
**Issue:** The handlers now return 503 (`SERVICE_UNAVAILABLE`) and 400 on an invalid `policy_id`. The drift test only checks 422. **Fix:** Add the 503 response to both operations.

### IN-05: Double-clicking Publish shows an error even though the first publish succeeds
**File:** `web/dashboard/src/components/PolicyStudio.tsx:129-160`
**Issue:** Concurrent `mutate()` calls share `publishKeyRef.current`. The second call gets 409, `onError` shows a failure banner, and it re-mints the key while the first publish is still running. **Fix:** Disable the button while `publishMutation.isPending`.

### IN-06: `resolveIdentityMappings` silently publishes empty mappings
**File:** `internal/control/publish_handler.go:26-33`
**Issue:** If the active payload fails to unmarshal, the error is dropped and an empty mapping set is signed. **Fix:** Log the error and refuse to publish when the mappings resolve to empty while the previous snapshot had them.

---

_Reviewed: 2026-10-11T05:47:30Z_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
