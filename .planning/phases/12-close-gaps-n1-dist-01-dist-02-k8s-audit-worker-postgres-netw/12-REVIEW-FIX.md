---
phase: 12-close-gaps-n1-dist-01-dist-02-k8s-audit-worker-postgres-netw
fixed_at: 2026-11-16T20:30:00Z
review_path: .planning/phases/12-close-gaps-n1-dist-01-dist-02-k8s-audit-worker-postgres-netw/12-REVIEW.md
iteration: 1
findings_in_scope: 9
fixed: 9
skipped: 0
status: all_fixed
---

# Phase 12: Code Review Fix Report

**Fixed at:** 2026-11-16T20:30:00Z
**Source review:** 12-REVIEW.md
**Iteration:** 1

**Summary:**
- Findings in scope: 9 (2 Critical, 7 Warning)
- Fixed: 9. WR-06 is resolved by documenting it as an accepted risk, as instructed.
- Skipped: 0
- Info: IN-01 fixed (stale comment). IN-02 to IN-08 are left open; see below.

**Verification (final tree):** all of the following pass: `go build ./...`, `go vet ./...`, `go test -short -count=1 ./...`, and `go test -short -race -count=1 ./internal/audit/ ./internal/control/ ./internal/telemetry/ ./cmd/gateway/`. `gofmt -l cmd internal tests scripts` is empty. Docker was not started. STATE.md and ROADMAP.md were not touched.

| Finding | Disposition | Commit(s) |
|---------|-------------|-----------|
| CR-01 | fixed: requires human verification | b01e7fb, 53f8774 |
| CR-02 | fixed | b97bad7 |
| WR-01 | fixed | babe748 |
| WR-02 | fixed (with 12-05 SUMMARY correction note) | 6bfb7d4 |
| WR-03 | fixed | c9ccf20 |
| WR-04 | fixed | 5033e82 |
| WR-05 | fixed | fd10a87 |
| WR-06 | accepted risk AR-12-01 (documented) | 0188d9a |
| WR-07 | fixed: requires human verification | d06fe99 |
| IN-01 | fixed | 789dd0a |

## Fixed Issues

### CR-01: Worker loses or stalls on valid frames that follow a torn frame

**Files modified:** `internal/audit/worker.go`, `internal/audit/spool.go`, `internal/audit/worker_resync_test.go`, `internal/audit/worker_test.go`
**Commits:** b01e7fb, 53f8774

**Applied fix:**
- **Reader.** When a frame over-runs EOF (`io.ErrUnexpectedEOF`) or fails CRC (`ErrCorruptedRecord`), the reader seeks to the bad frame's start + 1 and scans for the next magic. It never trusts the unprotected length field. The frame counts as a torn tail only when no decodable frame follows. In that case the newest segment retries from the first bad frame's start and a historical segment ends there.
- **Spool restart.** `NewDiskSpool` reuses the newest segment only if every byte of it parses as complete, CRC-valid frames (`segmentEndsOnFrameBoundary`). Otherwise it rotates to a fresh segment. Rotation was chosen over truncation: no byte is ever cut, so an acknowledged, fsynced frame cannot be lost, and the fsync-before-forward path (D-09) is unchanged.
- **Tests.** Regression tests for (a) a historical segment with a torn frame followed by B and C, (b) the same layout on the newest segment, and (c) 20 valid frames behind a CRC-bad frame, plus a reopen-rotation test. All four were confirmed red before the fix and green after.

**Security-review follow-up (53f8774, requested by the coordinator).** The concern was that on the active segment, resyncing past a frame that is still being written could skip that record once it completes. Resolution:
- The worker reads each pass against a stable size snapshot (`io.NewSectionReader` over `Stat().Size()`), so bytes appended during a pass are not seen until the next pass.
- A resync is **provisional**. The cursor moves past `badStart` only after a CRC-valid frame that lies fully inside the snapshot decodes after it. Writes are sequential appends, so such a frame proves the writer finished with or abandoned the bad frame.
- Every other exit path falls back to `badStart`: genuine tail, resync scan error, read error, batch flush and ctx cancel. An incomplete frame on the active segment is retried from its start and never skipped. The pre-follow-up code could persist a provisional resync position on the read-error flush path; that is now closed.
- `AppendPreForward` now rotates away from an untracked tail before appending, as `WriteFrames` already did. A torn tail left by a failed live append can therefore no longer have new frames written behind it.
- New tests: `TestAuditWorker_PartiallyWrittenActiveFrameCompletesLater` (the partial last frame is held, then ingested once complete along with its successor, nothing skipped), `TestAuditWorker_SpuriousMagicInIncompleteTailHoldsCursor`, and `TestDiskSpool_AppendPreForwardRotatesAwayFromUntrackedTail`.
- The legacy `TestAuditWorker_CorruptedRecordRecovery` now expects evt2 in a rotated segment, which is the intended behaviour change.
- Required scenario (b) still holds. A torn frame followed by complete, CRC-valid frames on the newest segment is skipped, because those frames prove the torn one will never be completed.

**Human verification:** please confirm the resync and torn-tail logic, which is a logic change on the audit-loss path.

### CR-02: Unbounded `method` label on `aegis_http_requests_total`

**Files modified:** `internal/telemetry/metrics.go`, `internal/telemetry/metrics_duration_test.go`
**Commit:** b97bad7
**Applied fix:** `RecordRequest` now passes the method through `normalizeMethod`, the same closed set the histogram uses. `TestHTTPRequestsTotal_MethodNormalized` checks that FOO1 and FOO2 map to OTHER; it was red before the fix. `tests/integration/operator_telemetry_test.go` only checks that the metric name is present and did not need changes.

### WR-01: Completion-shed log starves fault and write-error logs

**Files modified:** `internal/audit/committer.go`, `internal/audit/committer_log_test.go`
**Commit:** babe748
**Applied fix:** A new `logShed()` has its own limiter (`lastShedLogNS`) and reports how many completions were shed since the last report. `logf`, which carries the fault-set, fault-cleared and batch-write-failed lines, is no longer touched by the shed path. The test fires 1000 shed calls and then a fault line, and asserts that the fault line is printed.

### WR-02: Allow decisions always labelled `reason="OTHER"`

**Files modified:** `cmd/gateway/metrics_wiring.go`, `cmd/gateway/metrics_wiring_test.go`, `12-05-SUMMARY.md`
**Commit:** 6bfb7d4
**Applied fix:**
- `ALLOWED` (the audit logger's allow reason) is added to `policyReasonSet`. A policy-supplied allow reason outside the closed set still maps to OTHER.
- `TestPolicyReasonLabelAllow` covers the allow path; it was red before the fix.
- A correction note was added to 12-05-SUMMARY.md.

### WR-03: Unbounded frame length field

**Files modified:** `internal/audit/spool.go`, `internal/audit/spool_framelen_test.go`
**Commit:** c9ccf20
**Applied fix:**
- New constant `MaxFramePayloadBytes = 1 MiB`.
- `ReadFramedRecord` rejects a larger length as `ErrCorruptedRecord` before allocating and sizes the buffer in `int`.
- `frameEvent` and `AppendPreForward` refuse to produce an oversized frame, so the writers can never emit a frame the reader would reject.
- Tests cover a 0xFFFFFFFF header (no panic, resynced past) and a max+1 length.

### WR-04: `AEGIS_DRAIN_PROPAGATION_DELAY` silently falls back to 0

**Files modified:** `cmd/gateway/drain.go`, `cmd/gateway/drain_test.go`, `cmd/gateway/main.go`, `deployments/kubernetes/base/gateway/configmap.yaml`
**Commit:** 5033e82
**Applied fix:**
- Accepted values are a Go duration or a bare non-negative integer (seconds).
- Unparsable or negative values return an error, and `main` stops startup with `log.Fatalf` (fail-closed).
- Values above the documented `maxDrainPropagationDelay = 60s` are capped and logged as a warning.
- The configmap gained a comment documenting the rules.
- The table-driven test covers `"10"`, `"10 s"`, negatives, `"1.5"` and the cap.

### WR-05: User listener (:8080) is not drain-aware

**Files modified:** `cmd/gateway/main.go`, `cmd/gateway/drain.go`, `cmd/gateway/wiring_test.go`
**Commit:** fd10a87
**Applied fix:**
- `userProbeHandler` is now wrapped in the same `drainAwareHandler` as the workload chain.
- `TestWiringTwoPhaseDrain` checks that both `userProbeHandler` and `workloadMetricsHandler` are wrapped; it was red before the fix.

### WR-07: Poison-frame skips not observable and double-counted

**Files modified:** `internal/audit/worker.go`, `internal/audit/worker_readerr_test.go`, `internal/telemetry/metrics.go`, `internal/telemetry/metrics_duration_test.go`, `cmd/audit-worker/main.go`
**Commit:** d06fe99
**Applied fix:**
- New unlabelled counter `aegis_audit_worker_skipped_frames_total` (`Metrics.RecordAuditWorkerSkippedFrame`). It carries no PII.
- `AuditWorker.SetSkippedFrameHook(func())` follows the same pattern as `SetBytesWrittenHook`.
- Skips are deduped by a (segment, offset) high-water mark, so a retry before the cursor moves does not count the same frame again. `SkippedFrames()` and the hook both see a single count.
- `cmd/audit-worker` wires the hook and serves `/metrics` only when `AEGIS_AUDIT_WORKER_METRICS_ADDR` is set (opt-in). The sidecar opens no new port by default, and manifests and NetworkPolicy are unchanged.
- The test forces a failed batch and then a successful retry, and asserts the count is 1.

**Human verification:** please confirm the dedupe logic, and decide whether to enable the scrape address in the K8s and compose manifests.

### WR-06: Postgres ingress admits the internet-facing gateway container (accepted risk)

**Files modified:** `12-REVIEW.md` (fix note), `12-SECURITY.md` (new)
**Commit:** 0188d9a
**Disposition:** recorded as accepted risk AR-12-01. I checked `statefulset.yaml`: the gateway container holds no DB credentials, and only the audit-worker sidecar references `aegis-db-credentials`.
**Residual exposure, stated accurately:**
- The worker's DB role is **not** INSERT-only; it shares the control plane's Secret.
- The default ServiceAccount token is automounted.
- DB SSL is disabled.

**Full fix (deferred to v2):** a separate audit-worker pod or per-container identity, plus an INSERT-only role.

### IN-01: Stale comment on `retry()`

**Files modified:** `internal/audit/committer.go`
**Commit:** 789dd0a
**Applied fix:** The comment now says the write_error fault is set by `attempt`, and that overflow during a retry is shed rather than faulted (D-12a).

## Info Findings Left Open

These are not trivial comment fixes, so they are out of scope for this pass:
- **IN-02:** `queue_full` is still accepted by `recoverable()`, and queue depth still gates hard_limit recovery.
- **IN-03:** HAProxy `track` semantics are undocumented, and `on-marked-down shutdown-sessions` is not set.
- **IN-04:** The newest-segment torn-tail retry log is not rate-limited.
- **IN-05:** `TestRecorderCallers` matches selectors by name only and ignores parse errors.
- **IN-06:** The NetworkPolicy peer test is narrower than its doc comment (hard-coded port, single policy, no overlay check).
- **IN-07:** Control-plane gauges count streams rather than gateways, and the publish counter counts attempts.
- **IN-08:** The K8s audit-worker sidecar has no preStop hook, so it exits before the gateway's final flush.

---

_Fixed: 2026-11-16T20:30:00Z_
_Fixer: Claude (gsd-code-fixer)_
_Iteration: 1_
