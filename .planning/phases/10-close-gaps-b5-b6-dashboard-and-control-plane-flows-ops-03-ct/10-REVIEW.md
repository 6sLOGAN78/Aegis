---
phase: 10-close-gaps-b5-b6-dashboard-and-control-plane-flows-ops-03-ct
reviewed: 2026-10-11T10:25:31Z
depth: standard
scope: gap-closure plans 10-09, 10-10, 10-11 (diff 00873946bb08dcba8f07980005c16a326e26d5c5..HEAD)
supersedes: prior 10-REVIEW.md covering plans 10-01..10-08 (27 files, reviewed 2026-10-11T06:21:57Z), which is preserved in git history
files_reviewed: 11
files_reviewed_list:
  - deployments/compose/docker-compose.distributed.yml
  - deployments/compose/docker-compose.mvp.yml
  - internal/control/api.go
  - internal/control/auth.go
  - internal/control/gateway_handler.go
  - internal/control/gateway_handler_stale_test.go
  - internal/control/session_test.go
  - web/dashboard/package.json
  - web/dashboard/src/components/PolicyStudio.tsx
  - web/dashboard/src/utils/monaco-setup.ts
  - web/dashboard/vite.config.ts
findings:
  critical: 0
  warning: 5
  info: 6
  total: 11
status: issues_found
---

# Phase 10: Code Review Report (Gap Closure 10-09 / 10-10 / 10-11)

**Reviewed:** 2026-10-11T10:25:31Z
**Depth:** standard
**Files Reviewed:** 11
**Status:** issues_found

## Summary

This review covers only the gap-closure diff. The earlier review of plans 10-01..10-08 is in git history.

- **10-09 (AEGIS_COOKIE_SECURE):** This change is correct and fails secure. Only `false` or `0` turns the flag off. The env value is trimmed and matched case-insensitively. Unset or unrecognised values keep `Secure: true`. The server logs a warning when the flag is off. Login and logout use the same flag, and the tests cover both. Only minor configuration notes remain.
- **10-10 (stale gateway hiding):** The filter works as written. However, it hides gateways that have been partitioned for a long time from the convergence denominator, so the dashboard reports convergence while part of the fleet is missing. The REJECTED exemption also means a decommissioned rejected gateway is never hidden, which leaves the original ghost-gateway gap partly open. Invalid env values are ignored without any log message.
- **10-11 (local Monaco and editor hydration):** The bundling is sound. `tsc -b` passes, the built `dist/` has no `jsdelivr` reference, and `editor.worker` is emitted as a same-origin asset, which the `worker-src 'self' blob:` CSP allows. The hydration logic has two state bugs. It overwrites the parent's unsaved candidate Rego when the Studio tab remounts, and it never re-hydrates when the policy ID changes.

Targeted Go tests pass: `go test ./internal/control/ -run 'Gateway|Stale|Cookie|Session'`.

## Narrative Findings (AI reviewer)

## Warnings

### WR-01: Remount hydration overwrites the parent's unsaved candidate Rego when switching tabs

**File:** `web/dashboard/src/components/PolicyStudio.tsx:98-111` (with `web/dashboard/src/App.tsx:73-82`)
**Issue:** `App.tsx` renders `PolicyStudio` only while `activeTab === 'studio'`, so switching tabs unmounts it. `hydratedRef` and `userEditedRef` are component refs, so they reset on every mount. The flow:
1. The operator edits the policy. `App.candidateRego` now holds the edits.
2. The operator switches to the Simulator tab and back. `PolicyStudio` remounts.
3. react-query returns the cached `versionsData` synchronously, so the effect runs right away.
4. The effect calls `setRegoCode(server source)` and `onCandidateRegoChange(server source)`.

The operator's unsaved draft in `App` is replaced without warning, and the Simulator now tests the published policy instead of the candidate. Before this change, the editor reset to the template on remount but the parent's candidate survived. Now both copies are lost.
**Fix:** Lift the editor text to `App` (or seed from it) and skip hydration when a candidate already exists.
```tsx
// App.tsx
<PolicyStudio session={session} candidateRego={candidateRego} onCandidateRegoChange={setCandidateRego} />

// PolicyStudio.tsx
const [regoCode, setRegoCode] = useState<string>(candidateRego ?? DEFAULT_REGO_TEMPLATE);
const userEditedRef = useRef(candidateRego !== undefined);
```

### WR-02: Hydration is not tied to `policyId`, so the editor keeps policy A's source while publishing as policy B

**File:** `web/dashboard/src/components/PolicyStudio.tsx:98-108, 340`
**Issue:** The Policy Identifier field is editable (`setPolicyId`, line 340), and the versions query key includes `policyId`. But `hydratedRef` is set to `true` after the first load and never reset. If the operator changes the policy ID, the editor keeps the first policy's source. `publishMutation` then saves that source as the draft for the new `policy_id` and publishes it with the new policy's ETag. The new hydration feature presents the editor as "the latest stored policy", which makes this mismatch easy to miss. On the server side, `HandleGetPolicyVersions` (`internal/control/policy_handler.go:71-100`) ignores `id` and returns global snapshots, so `list[0].source_rego` is not specific to any policy anyway.
**Fix:** Track hydration per policy, and only re-hydrate when the operator has no unsaved edits:
```tsx
const hydratedForRef = useRef<string | null>(null);
useEffect(() => {
  if (hydratedForRef.current === policyId || userEditedRef.current) return;
  const list = versionsData?.versions;
  if (!list || list.length === 0) return;
  hydratedForRef.current = policyId;
  ...
}, [policyId, versionsData, onCandidateRegoChange]);
```
Also filter versions by `policy_id` on the server, or document that the endpoint is global.

### WR-03: Long-partitioned gateways disappear from the convergence denominator (observability fails open)

**File:** `internal/control/gateway_handler.go:124-130`
**Issue:** If a gateway stops sending heartbeats for more than 15 minutes (crash, network partition, fail-closed data plane denying all traffic), it is removed from the list. `ClusterTopology.tsx:45` computes `converged / total` from that list, so a fleet with one dead replica out of three shows "2 / 2 Replicas (100%)" with a green bar. A replica that went silent is the most important partition signal, and this change hides it after 15 minutes. Nothing tells the operator that records were hidden.
**Fix:** Do not drop these records without a trace. Return them in a separate field (for example `stale_gateways` or a `hidden_stale_count`) and show "N stale replicas hidden" in the topology view. At minimum, increment a metric or log once when a gateway moves from partitioned to stale.

### WR-04: REJECTED gateways are never stale, so decommissioned rejecters stay as ghosts forever

**File:** `internal/control/gateway_handler.go:46-51`
**Issue:** `isStaleReplica` always returns false for `ACK_STATUS_REJECTED`. The Pong handler (`server.go:114-117`) also keeps REJECTED sticky. Take a gateway that rejects a bad snapshot and is then removed from the fleet:
- Its `gateway_acks` row stays REJECTED.
- The in-memory `AckTracker` entry is never evicted.
- No endpoint deletes either record.

It stays "partitioned" indefinitely, and convergence never reaches 100%. This is the ghost-gateway problem 10-10 was meant to fix, and it still exists for one status. The exemption is reasonable for recent failures, but it should have an upper bound.
**Fix:** Give REJECTED records a longer but finite cutoff (for example `rejectedStaleAfter = 24h` or `4 * staleAfter`). Alternatively, add an authenticated sec-ops endpoint that deletes a gateway's ack row and its `AckTracker` entry.

### WR-05: Invalid or too-small `AEGIS_GATEWAY_STALE_AFTER` is silently replaced with 15m

**File:** `internal/control/gateway_handler.go:33-43`
**Issue:** If an operator sets `AEGIS_GATEWAY_STALE_AFTER=1m` (or a typo like `5min`), the value is silently replaced with 15m, which is a much looser threshold than they asked for. The value is re-read and re-parsed on every request with no log, so the misconfiguration can't be seen. The 10-09 cookie change logs its override at startup. This one should do the same.
**Fix:** Parse the value once when `NewAPIServer` builds the server, store it on `APIServer`, and log once if it is rejected:
```go
if err != nil || d <= gatewayPartitionAfter {
    log.Printf("WARNING: AEGIS_GATEWAY_STALE_AFTER=%q invalid or <= %s; using %s", v, gatewayPartitionAfter, defaultGatewayStaleAfter)
    return defaultGatewayStaleAfter
}
```
A value of 60s or less could also be clamped to `gatewayPartitionAfter + 1s` rather than jumping to 15m.

## Info

### IN-01: Staleness relies on the timestamp the gateway reports

**File:** `internal/control/gateway_handler.go:87-97, 127`
**Issue:** For in-memory entries, `lastHeartbeatAt` comes from `ack.AcknowledgedAt`. `server.go:98-100` only fills this with server time when the gateway leaves it empty. A gateway whose clock runs ahead (or that lies) has `now.Sub(lastHb)` negative and is never stale or partitioned. The persisted path uses DB `NOW()`, so the two sources disagree.
**Fix:** Always overwrite `AcknowledgedAt` with server time on receipt, or clamp future timestamps to `now`.

### IN-02: No handler-level test for the stale filter

**File:** `internal/control/gateway_handler_stale_test.go`
**Issue:** The tests cover `gatewayStaleAfter` and `isStaleReplica` on their own. Nothing checks that `HandleListGateways` actually leaves out a 20-minute-old APPLIED record while keeping a 20-minute-old REJECTED one. That loop is where a regression (for example, moving the `continue`) would happen.
**Fix:** Add a case to `gateway_handler_test.go` that seeds the `AckTracker` with an old `AcknowledgedAt`, then asserts which IDs are present and checks `len(resp.Gateways)`.

### IN-03: Cookie Secure setting is read from the environment inside `NewAPIServer`

**File:** `internal/control/api.go:63-66`
**Issue:** `cookieSecureFromEnv(os.Getenv)` is called inside the constructor instead of being passed in through config. This hidden environment coupling makes `NewAPIServer` harder to test. It also differs from how other settings are wired in `cmd/control-plane/main.go`. The fail-secure default and the warning log are correct.
**Fix:** Add a `CookieSecure bool` option to `NewAPIServer` and read the environment in `main.go`.

### IN-04: The hardened profile still serves the dashboard over plain HTTP with Secure cookies

**File:** `deployments/compose/docker-compose.hardened.yml:38` (context for the 10-09 compose changes)
**Issue:** mvp and distributed opt out with `AEGIS_COOKIE_SECURE=false`. The hardened profile keeps the secure default but serves `http://...:8084` with no TLS. Login only works through `localhost`/`127.0.0.1`, which browsers treat as a secure context. From another host the session cookie is dropped without error. This behaviour is correct, but it is not documented.
**Fix:** Either put the hardened control plane behind TLS, or add a comment saying the hardened dashboard needs HTTPS or localhost access.

### IN-05: The Vite CDN-strip plugin is fragile and hard-codes the base path

**File:** `web/dashboard/vite.config.ts:15-20`
**Issue:**
- The replacement target `/dashboard/monaco-local/vs` duplicates `base: '/dashboard/'` and points to a path that does not exist.
- The plugin does nothing if `@monaco-editor/loader` switches CDN host or quoting style. For example, with backticks, `[^'"]*` would consume past the end of the string.
- `transform` hooks do not run on dev-mode `optimizeDeps` pre-bundles, so the dev bundle still contains the jsdelivr string.

Today's production build is clean; I checked `dist/assets` and found no `jsdelivr`.
**Fix:** Add a `generateBundle` / `closeBundle` check that fails the build if any chunk contains `cdn.jsdelivr.net` (or any `https://` monaco URL). Derive the replacement from `config.base`.

### IN-06: `getWorker` ignores the language label

**File:** `web/dashboard/src/utils/monaco-setup.ts:8-10`
**Issue:** `import * as monaco from 'monaco-editor'` pulls in every language contribution. The build emits `css.worker` and the others. `getWorker` returns the base editor worker for every label. Rego uses Monarch only, so this is harmless today. If anyone later opens a JSON/TS/CSS model, its language service will run in the wrong worker and fail without an error.
**Fix:** Import `monaco-editor/esm/vs/editor/editor.api` plus only the needed contributions. Alternatively, switch on `label` in `getWorker` and throw for anything other than `editorWorkerService`.

---

_Reviewed: 2026-10-11T10:25:31Z_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_
