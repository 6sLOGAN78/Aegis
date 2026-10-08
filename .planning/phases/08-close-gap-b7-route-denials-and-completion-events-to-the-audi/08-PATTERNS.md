# Phase 8: Close gap B7 (route denials and completion events to the audit spool) - Pattern Map

**Mapped:** 2026-10-09
**Files analyzed:** 27 (13 new, 14 modified; tests counted with their subject)
**Analogs found:** 22 / 27 (5 have no in-repo analog; see "No Analog Found")

All paths are relative to `/home/logan78/Desktop/Aegis`. Line numbers were read in this session.

Repo-wide conventions the planner must keep (verified):
- Go module path is `aegis/...` (`import "aegis/internal/audit"`). Tests use `testing` + `testify` (`assert`/`require`), same package as the subject (white-box, e.g. `package audit`).
- There is NO functional-option type and NO injectable clock anywhere in `internal/` or `cmd/` (grep for `type Option`, `func With*`, `func() time.Time`, `nowFn`, `clock` found nothing in production code; the only `With*` funcs are `WithAuditContext` and `WithRouteID`, which are context helpers). The new `audit.WithSink` option and `Governor.Admit(ev, now)` / `Sweep(now)` clock injection are new conventions. Make `now` an explicit parameter (as RESEARCH proposes) rather than a package-level var, so tests need no globals.
- Stop/shutdown idiom for goroutines in this repo: a `stopCh chan struct{}` closed under `sync.Once` or a mutex (`internal/snapshot/client.go:29,51,270-276`), `select { case <-ctx.Done(): ... case <-ticker.C: ... }` loops (`internal/audit/worker.go:346-366`, `cmd/gateway/main.go:134-151`), and a final drain with a fresh `context.WithTimeout(context.Background(), ...)` after cancel (`worker.go:352-357`).

## File Classification

| New/Modified File | Role | Data Flow | Closest Analog | Match Quality |
|-------------------|------|-----------|----------------|---------------|
| `internal/audit/event.go` (mod: `EventType`, `SuppressedCount`, kind consts, `Normalize()`) | model | transform | itself (`event.go:7-25`) + `Normalize` has no analog | exact (struct) / no analog (Normalize) |
| `internal/audit/spool.go` (mod: `writeBatchLocked`, hard limit, fault flag, `StatfsFunc`, `frameEvent`) | service (storage) | file-I/O (append WAL) | `AppendPreForward` + `CheckSaturation` in same file | exact |
| `internal/audit/committer.go` (new: group-commit goroutine) | service | event-driven / batch | `AuditWorker.Start` ticker loop (`worker.go:346-366`) + `StreamClient` stopCh (`snapshot/client.go`) | role-match |
| `internal/audit/governor.go` (new: suppression map + token buckets) | utility | transform (in-memory state) | `internal/ratelimit/limiter.go` (concept only, Redis-backed) | partial |
| `internal/audit/pipeline.go` (new: `Sink` impl, `Recorder` iface, `Shutdown`) | service | event-driven | `telemetry.Server` Start/Shutdown (`telemetry/server.go`) + `DualServer.Shutdown` (`proxy/server.go:314-345`) | role-match |
| `internal/audit/logger.go` (mod: `WithSink`, first-WriteHeader hook, decisionSet, `WrapUpstreamErrorHandler`) | middleware | request-response | itself (`AuditMiddleware`, `StatusCaptureResponseWriter`) | exact |
| `internal/audit/worker.go` (mod: typed `event_type`, Normalize, 20th arg) | service | batch (WAL to Postgres) | itself (`ProcessBatch`, `worker.go:72-162`) | exact |
| `internal/audit/*_test.go` new: `governor_test.go`, `committer_test.go`, `pipeline_test.go`, `event_test.go`, `main_reasons_test.go`; extended: `logger_test.go`, `worker_test.go`, `spool_test.go` | test | mixed | `spool_test.go`, `worker_test.go`, `logger_test.go` | exact |
| `cmd/gateway/main.go` (mod: pipeline construction, `WithSink` x2, `WrapUpstreamErrorHandler` x2, shutdown, metrics wiring) | config / entrypoint | request-response + lifecycle | itself (lines 107-151, 905-919, 939-964) | exact |
| `internal/config/config.go` (+ `config_test.go`) (mod: new `AEGIS_AUDIT_*`, `AEGIS_SPOOL_*` keys) | config | request-response (env parse) | itself (`config.go:123-133`, `AEGIS_SPOOL_MAX_BYTES`) | exact |
| `internal/telemetry/metrics.go` (+ `metrics_test.go`) (mod: audit counters/gauges) | utility (metrics) | event-driven | itself (`RateLimitRejectionsTotal` CounterVec, `SpoolUtilizationRatio` Gauge) | exact |
| `internal/proxy/limiter.go` and `internal/proxy/server.go` (mod: pre-middleware rejection counter, D-16) | middleware | request-response | `ConcurrencyLimiter.Wrap` (`limiter.go:20-39`), ingress closures (`server.go:127-201`) | exact |
| `internal/proxy/proxy.go` (touch only if wrapping `ErrorHandler` there instead of in main) | utility | request-response | `ErrorHandler` at `proxy.go:84-97` | exact |
| `migrations/000003_add_audit_suppressed_count.sql` (new) | migration | CRUD (DDL) | `migrations/000002_create_partitioned_audit_tables.sql` | role-match |
| `api/openapi/control-v1.yaml:869`, `pkg/api/control/v1/types.gen.go:22-26`, `web/dashboard/src/api/types.ts:2` (mod: add `denial`) | config (contract) | n/a | the same three lines | exact |
| `scripts/compose-smoke.sh` (mod: AUDIT-TYPES, DEDUPE, ROTATION sections) | test (live) | request-response | itself (`AUDIT` section lines ~190-230, helpers lines 100-165) | exact |
| `deployments/compose/docker-compose.*.yml` + `tests/compose/compose_test.go` (mod: allowlist, comment, grace lint) | config + test | n/a | `gatewayAllowlist` (`compose_test.go:32-57`), `TestGatewayStopGracePeriod` (`:341-363`) | exact |

## Pattern Assignments

### `internal/audit/spool.go` (service, file-I/O)

**Analog:** `internal/audit/spool.go` itself. D-09 requires `AppendPreForward` body to be byte-for-byte unchanged (RESEARCH pitfall 10 "AppendPreForward body diff is empty"). Extract the frame builder into a private helper used ONLY by new paths, rather than refactoring `AppendPreForward` to call it.

**Frame format to replicate** (`spool.go:212-228`):
```go
payload, err := json.Marshal(event)
checksum := crc32.ChecksumIEEE(payload)
payloadLen := uint32(len(payload))

frame := make([]byte, FrameHeaderSize+len(payload)+1)
binary.BigEndian.PutUint32(frame[0:4], MagicHeader)
binary.BigEndian.PutUint32(frame[4:8], checksum)
binary.BigEndian.PutUint32(frame[8:12], payloadLen)
copy(frame[12:], payload)
frame[FrameHeaderSize+len(payload)] = '\n'
```

**Lock / closed / write / fsync / rotate skeleton to copy for `writeBatchLocked`** (`spool.go:198-250`):
```go
s.mu.Lock()
defer s.mu.Unlock()
if s.closed { return errors.New("spool is closed") }
// ... gate check (new path uses checkHardLimit(), NOT CheckSaturation()) ...
if _, err := s.activeFile.Write(frame); err != nil {
    return fmt.Errorf("failed to append wal record: %w", err)
}
if err := s.activeFile.Sync(); err != nil {
    return fmt.Errorf("failed to fsync wal record: %w", err)
}
s.currentSize += int64(len(frame))
if s.currentSize >= s.cfg.MaxSegmentBytes {
    if err := s.rotateSegment(); err != nil { ... }
}
```
For a batch: concatenate frames, ONE `Write`, ONE `Sync`, then rotate (so no frame straddles a segment). On error `Truncate(currentSize)` per RESEARCH Pattern 2.

**Saturation-check pattern to copy for `checkHardLimit` and the StatfsFunc hook** (`spool.go:130-153`):
```go
if s.cfg.VolumeQuotaBytes > 0 {
    usage, err := s.dirUsageBytes()
    if err == nil {
        ratio := float64(usage) / float64(s.cfg.VolumeQuotaBytes)
        if ratio >= 0.90 { return true, nil }
    }
}
var stat syscall.Statfs_t
if err := syscall.Statfs(s.cfg.SpoolDir, &stat); err == nil && stat.Blocks > 0 {
    usedBlocks := stat.Blocks - stat.Bfree
    ratio := float64(usedBlocks) / float64(stat.Blocks)
    if ratio >= 0.90 { return true, nil }
}
```
Replace the inline `syscall.Statfs` with `s.cfg.StatfsFunc` when non-nil (nil = real syscall). Parameterise 0.90 vs hard ratio. `CheckSaturation` must additionally return true while the fault flag is set (D-12); `IsSaturated` (`spool.go:156-159`) already treats a check error as saturated and needs no change.

**Config struct style** (`spool.go:32-37`): add `StatfsFunc func(path string) (used, total uint64, err error)` and `HardLimitRatio float64` as new fields with defaults filled in `NewDiskSpool` exactly like `MaxSegmentBytes`/`VolumeQuotaBytes` (`spool.go:56-61`: `if cfg.X <= 0 { cfg.X = default }`).

**Close ordering note:** `Close()` (`spool.go:280-296`) takes `s.mu`; the pipeline must be shut down before it (main.go change below).

**Test analog for spool changes:** `spool_test.go`. Reuse `sampleCompletionEvent` (`:19-38`), `os.MkdirTemp` + `NewDiskSpool(DiskSpoolConfig{SpoolDir, MaxSegmentBytes, VolumeQuotaBytes})` + `ReadFramedRecord` loop (`:40-87`), tiny-segment rotation (`TestDiskSpool_SegmentRotation`, `:130-170`, `MaxSegmentBytes: 200`), quota saturation with `VolumeQuotaBytes: 1000` (`:225-252`), and the concurrency pattern `sync.WaitGroup` + `assert.NoError` inside goroutines (`:254-282`). New hard-limit-band tests must inject `StatfsFunc` (RESEARCH "Test determinism"; CONTEXT specifics note host is over 90 percent full) instead of relying on real usage.

---

### `internal/audit/event.go` (model, transform)

**Analog:** itself (`event.go:7-25`). Add fields in the same tag style, `omitempty` where old records must still decode:
```go
ErrorCode       string    `json:"error_code,omitempty"`
// new:
EventType       string    `json:"event_type,omitempty"`      // old records: "" -> worker fallback (D-03)
SuppressedCount int       `json:"suppressed_count,omitempty"`
```
`ReadFramedRecord` uses plain `json.Unmarshal` into `CompletionEvent` (`spool.go:329-332`), so adding `omitempty` fields is backward compatible with existing spools. Pre-forward decision rows must set `EventType` explicitly in `ToCompletionEvent` (`logger.go:159-176`) without touching the durability path.

`Normalize()` has no analog in the repo (see No Analog Found); use the RESEARCH "Normalisation chokepoint" excerpt with column widths from migration 000002 (`event_type 32, principal_id 128, principal_kind 32, service_id 64, route_id 64, http_method 16, decision 16, reason_code 64, client_ip 64, error_code 64`).

---

### `internal/audit/logger.go` (middleware, request-response)

**Analog:** itself. All hooks go into existing structures.

**AuditContext mutator style to copy for a `decisionSet` flag and any new setter** (`logger.go:92-101`):
```go
func (ac *AuditContext) SetDecision(decision, reasonCode string) {
	if ac == nil {
		return
	}
	ac.mu.Lock()
	defer ac.mu.Unlock()
	ac.Decision = decision
	ac.ReasonCode = reasonCode
}
```
(nil-receiver guard + mutex is universal; keep it. `SetErrorCode` is nil-safe so `WrapUpstreamErrorHandler` needs no extra check.)

**Capture-writer hook point** (`logger.go:228-250`):
```go
type StatusCaptureResponseWriter struct {
	http.ResponseWriter
	StatusCode  int
	wroteHeader bool
}
func (rw *StatusCaptureResponseWriter) WriteHeader(code int) {
	if !rw.wroteHeader {
		rw.StatusCode = code
		rw.wroteHeader = true
		rw.ResponseWriter.WriteHeader(code)
	}
}
```
Add an unexported `onFirstHeader func(code int)` called for `code >= 200` BEFORE `rw.ResponseWriter.WriteHeader(code)` (D-07). Note `Write` (`:253-258`) also sets `wroteHeader = true` without calling the hook; the middleware `defer` must therefore also record a denial if the hook never fired (RESEARCH Pattern 1). Keep `NewStatusCaptureResponseWriter` signature (tests/others call it).

**Middleware signature to extend** (`logger.go:274`): `func AuditMiddleware(logger *Logger, snapshotVersion int64) func(http.Handler) http.Handler` becomes `AuditMiddleware(logger *Logger, snapshotVersion int64, opts ...Option)`. Existing callers that must keep compiling unchanged: `tests/failure/redis_outage_test.go:338`, `tests/security/workload_identity_test.go:419-420`, chaos helpers, `logger_test.go`, `cmd/gateway/main.go:905,915`.

**Existing defer to extend** (`logger.go:302-351`): builds the `CompletionEvent` under `ac.mu.Lock()`, then calls `logger.LogCompletion(event)` after unlock. Keep stdout emission; add `sink.EnqueueCompletion(&event)` for allow (completion row needs its OWN `event_id`; here `uuid.NewString()` at `:330` already generates a fresh one, which is distinct from the pre-forward row's, satisfying D-01) and `EventType` constants. The status-based decision flip to fix is at `:305-313`:
```go
decision := ac.Decision
if captureWriter.StatusCode >= 200 && captureWriter.StatusCode < 300 {
	if decision != "deny" { decision = "allow" }
} else if captureWriter.StatusCode >= 400 {
	decision = "deny"
}
```
Gate the `>= 400` flip on the new `decisionSet` flag so a backend 500 on an allowed request stays `allow` (RESEARCH Pitfall 3). Do not change `LogCompletion` field set (`:194-226`); `logger_test.go` asserts all 17 fields present (`logger_test.go:~96-103`).

**Request-id/IP handling already correct** (`:279-294`): client IP from `r.RemoteAddr` only. Keep. Event construction at `:329-347` reads `r.Method` and `ac.CanonicalPath` verbatim: this is exactly where `Normalize()` must be applied for durable rows (not necessarily for stdout).

**Test analog:** `logger_test.go:17-` (`TestCompletionAuditLogging`). Pattern: `var buf bytes.Buffer; logger := NewLogger(&buf)`, build `AuditMiddleware(logger, 42)(http.HandlerFunc(func(w, r){ ac := FromContext(r.Context()); require.NotNil(t, ac); ac.SetPrincipal(...); ac.SetRoute(...); ac.SetCanonicalPath(...); ac.SetDecision(...); ac.SetErrorCode(...); w.WriteHeader(tc.statusCode) }))` and `httptest.NewRecorder()`. New sink tests use a fake `Sink` struct in the same style; table-driven on `{statusCode, decision, reason, errCode}` exactly like `testStatuses` (`logger_test.go:18-25`). D-07 "denial recorded before response" test: fake sink inspects the `httptest.ResponseRecorder` (`rec.Flushed`/`rec.Code` not yet set / `wroteHeader` false) when `RecordDenial` is invoked.

---

### `internal/audit/committer.go` (service, event-driven + batch)

**Analog (role-match, no exact):** worker ticker loop, `internal/audit/worker.go:344-366`:
```go
func (w *AuditWorker) Start(ctx context.Context) error {
	ticker := time.NewTicker(w.cfg.FlushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			// Final drain before shutdown
			drainCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_, _ = w.ProcessAvailable(drainCtx)
			cancel()
			return ctx.Err()
		case <-ticker.C:
			if _, err := w.ProcessAvailable(ctx); err != nil {
				if !errors.Is(err, context.Canceled) {
					log.Printf("AuditWorker batch flush warning: %v", err)
				}
			}
		}
	}
}
```
Copy: `select` over done/ticker, "final drain with a fresh timeout context" on shutdown, `log.Printf("... warning: %v", err)` for non-fatal write errors (the repo uses stdlib `log`, not slog, for operational logs).

**Stop-channel idiom** (`internal/snapshot/client.go:28-29,51,270-276`): `stopCh chan struct{}` + mutex-guarded close. For the committer RESEARCH requires `closed` atomic + `quit` channel and NEVER closing the data channels. Use `sync.Once` for `Shutdown` like `DualServer.shutdownOnce` (`proxy/server.go:114,316-318`):
```go
d.shutdownOnce.Do(func() { close(d.shutdownCh) })
```

**Non-blocking enqueue** has no in-repo analog; the idiom is stdlib `select { case ch <- x: default: drop }` (see `ConcurrencyLimiter.Wrap`, `limiter.go:22-37`, which uses the same non-blocking `select` on a semaphore channel and a `default:` rejection branch). That is the nearest in-repo example of the pattern:
```go
select {
case l.sem <- struct{}{}:
	defer func() { <-l.sem }()
	next.ServeHTTP(w, r)
default:
	// reject immediately
}
```

**Test analog:** `TestDiskSpool_ConcurrentAppends` (`spool_test.go:254-282`) for the N-goroutine `sync.WaitGroup` stress style; run with `-race`. For fsync counting use an injected hook field on `DiskSpoolConfig`/committer (RESEARCH "sync-count hook"); no existing fake-file seam exists.

---

### `internal/audit/governor.go` (utility, in-memory state)

**Analog (partial):** `internal/ratelimit/limiter.go` is the repo's rate limiter, but it is Redis-backed and uses wall time server-side, so it is NOT reusable here (RESEARCH deliberately keeps this in-process). Use only as a naming/API-shape reference. No in-repo in-memory token bucket or keyed map with sweeper exists.

Copy these repo conventions: a `sync.Mutex`-guarded struct with `New...(cfg)` constructor and doc comments on every exported symbol (see `AuditContext`, `DiskSpool`); closed-set labels with unknown mapped to a catch-all (`telemetry.RecordRequest` maps `routeID == ""` to `"unknown"`, `metrics.go:114-119`; use the same style for reason code -> `OTHER`).

**Test analog:** none for fake clocks. The repo's only time-faking precedent is miniredis `FastForward` (`internal/revocation/store_test.go:111`, Redis-side). Use explicit `now time.Time` args (`Admit(ev, now)`, `Sweep(now)`), tests never sleep.

**Drift-guard test `main_reasons_test.go`:** no in-repo analog (parses source with `go/parser`). Closest in spirit: `tests/compose/compose_test.go` hermetic lint-of-source tests (`gatewayAllowlist` at `:30-57` is a hand-maintained set checked against files). Mirror that: a `map[string]class` table in the governor and a test asserting every `SetDecision`/`SetErrorCode` string literal in `cmd/gateway/main.go` is a key.

---

### `internal/audit/pipeline.go` (service, event-driven)

**Analog:** `internal/telemetry/server.go` for Start/Shutdown shape and `proxy.DualServer.Shutdown` (`proxy/server.go:314-345`) for combined error reporting.

**Lifecycle shape to copy** (`telemetry/server.go:37-45`):
```go
func (s *Server) Start() { go func() { _ = s.httpServer.ListenAndServe() }() }
func (s *Server) Shutdown(ctx context.Context) error { return s.httpServer.Shutdown(ctx) }
```
Pipeline: `NewPipeline(spool, gov, rec, cfg)` starts the committer + sweeper goroutines; `Shutdown(ctx) error` stops sweeper, emits summaries, drains, final fsync.

**Decoupling from telemetry:** `audit` does not import `telemetry` today (check: `logger.go`, `worker.go`, `spool.go` imports have no `aegis/internal/telemetry`). Keep it that way with a small `Recorder` interface implemented by `*telemetry.Metrics`, in the same way `worker.go:30-34` defines `BatchPool` as a consumer-side interface "satisfied by `*pgxpool.Pool` and `pgxmock.PgxPoolIface`":
```go
type BatchPool interface {
	SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults
	Close()
}
```

---

### `internal/audit/worker.go` (service, batch)

**Analog:** itself. Insert point is `ProcessBatch` (`worker.go:72-162`).

**Current type derivation to replace** (`worker.go:107-111`):
```go
eventDate := ts.Format("2006-01-02")
eventType := "decision"
if e.HTTPStatus > 0 {
	eventType = "completion"
}
```
New: allowlist `{decision, completion, denial}`; else fall back to this exact guess (D-03). Apply `e.Normalize()` on a copy before `batch.Queue`. Request id fallback already exists (`:95-98`: empty -> `uuid.NewString()`); add `uuid.Parse` validation per RESEARCH.

**Insert statement to extend** (`worker.go:78-88`): the column list ends `http_status, duration_ms, client_ip, error_code` with placeholders `$1..$19`; append `suppressed_count` and `$20`, and add the arg after `e.ErrorCode` at `:139`.

**Atomicity/cursor pattern to leave untouched** (`:143-161`): `br := w.pool.SendBatch(...)`, loop `br.Exec()`, `br.Close()`, then `w.cursor.SaveOffset(segmentFile, newOffset)` only after commit. This is why a poison row wedges the worker (RESEARCH Pitfall 1).

**Test analog:** `worker_test.go`. `anyAuditArgs()` at `:16-22` hard-codes 19; change to 20 and every caller (`:52,55,102,140,184,187,216,271,274`) is fixed by that one helper. Pattern:
```go
mock, err := pgxmock.NewPool()
b := mock.ExpectBatch()
b.ExpectExec("INSERT INTO audit_events").
	WithArgs(anyAuditArgs()...).
	WillReturnResult(pgxmock.NewResult("INSERT", 1))
worker, _ := NewAuditWorker(mock, WorkerConfig{SpoolDir: tmpSpoolDir, BatchSize: 10, FlushInterval: 100 * time.Millisecond})
processed, err := worker.ProcessAvailable(context.Background())
assert.NoError(t, mock.ExpectationsWereMet())
```
For the `event_type` arg test, replace `pgxmock.AnyArg()` at index 1 with the literal expected string (`"denial"`, `"completion"`, `"decision"`, fallback case). Write events via `NewDiskSpool` + `AppendPreForward` as in `TestAuditWorker_BatchFlushAndCursorAdvance` (`:24-78`) or via the new writer.

---

### `cmd/gateway/main.go` (entrypoint, request-response + lifecycle)

**Analog:** itself. Edit locations (current line numbers):

1. **Logger/spool/metrics construction** (`:107-131`). `auditLogger := audit.NewLogger(nil)` (`:107`) and `diskSpool` (`:109-118`) are built BEFORE `metrics := telemetry.NewMetrics()` (`:128`). The pipeline needs the recorder, so construct it after `:128` (RESEARCH). Existing spool config style to extend:
```go
spoolCfg := audit.DiskSpoolConfig{ SpoolDir: cfg.SpoolDir }
if cfg.SpoolMaxBytes > 0 { spoolCfg.VolumeQuotaBytes = cfg.SpoolMaxBytes }
diskSpool, err := audit.NewDiskSpool(spoolCfg)
if err != nil { log.Fatalf("Failed to initialize audit disk spool: %v", err) }
```
Add `MaxSegmentBytes: cfg.SpoolSegmentBytes`, `HardLimitRatio: cfg.SpoolHardLimitRatio` the same way.

2. **1-second gauge loop** (`:133-151`): add `metrics.SetAuditQueueDepth(pipeline.QueueDepth())` / degraded gauge here, inside the existing `case <-ticker.C:` (it already calls `metrics.SetSpoolUtilization(diskSpool.UtilizationRatio())`).

3. **Two reverse-proxy forwards** (`:595-596` user, `:898-899` workload):
```go
rp := proxy.NewReverseProxyWithMTLS(route.ParsedURL(), canonicalPath, reqID, assertionToken, upstreamTransport)
rp.ServeHTTP(w, r)
```
Insert `rp.ErrorHandler = audit.WrapUpstreamErrorHandler(rp.ErrorHandler)` between them.

4. **Middleware wiring** (`:905` and `:915`):
```go
auditedUserHandler := audit.AuditMiddleware(auditLogger, 0)(gatewayHandler)
...
auditedWorkloadHandler := audit.AuditMiddleware(auditLogger, 0)(workloadHandler)
```
Add `audit.WithSink(auditPipeline)` as a third arg to each. `:911` (`diskSpool.IsSaturated` to the probe handler) stays; fault flag flows through `CheckSaturation`.

5. **Shutdown** (`:943-964`). Current order: `dualServer.Shutdown(drainCtx)` then `streamCancel()/streamClient.Stop()` then `metricsServer.Shutdown(shutdownCtx 5s)` then `rdb.Close()` then `diskSpool.Close()`. Insert directly after the `dualServer.Shutdown` block (`:946-948`), mirroring the existing style:
```go
if err := dualServer.Shutdown(drainCtx); err != nil {
	log.Printf("Error during dual server shutdown: %v", err)
}
// new, same shape:
auditCtx, auditCancel := context.WithTimeout(context.Background(), 5*time.Second)
if err := auditPipeline.Shutdown(auditCtx); err != nil {
	log.Printf("audit pipeline shutdown: %v", err)
}
auditCancel()
```
`diskSpool.Close()` block at `:961-963` unchanged.

6. **Do NOT touch** the two `AppendPreForward` call sites (`:535` and `:844`, with their saturation/write-error handling at `:536-565`): D-09.

7. **Env-var parsing style in main.go itself** is the silent-fallback style (`:923-927` `AEGIS_DRAIN_TIMEOUT`: `if d, err := time.ParseDuration(val); err == nil && d > 0`). RESEARCH says new keys go in `internal/config` with error-on-invalid instead; do not copy the silent style.

**Test analog:** none runs `main()` (CONTEXT: pipeline is an inline closure and tests re-implement it; extraction is out of scope). Verification is live via `scripts/compose-smoke.sh`. Tests that re-implement the pipeline and must keep compiling: `tests/failure/redis_outage_test.go:338-339` (`audit.AuditMiddleware(auditLogger, 0)(gatewayHandler)` then `httptest.NewServer`).

---

### `internal/config/config.go` (+ `config_test.go`) (config)

**Analog:** itself, `AEGIS_SPOOL_MAX_BYTES` handling.

**Struct + defaults style** (`config.go:28-53`): flat fields with defaults in the `cfg := &Config{...}` literal:
```go
SpoolDir:      "/var/log/aegis/wal",
SpoolMaxBytes: 1073741824, // 1 GiB
```
**Parse-or-error style to copy for every new key** (`config.go:127-133`):
```go
if spoolMaxBytesStr := os.Getenv("AEGIS_SPOOL_MAX_BYTES"); spoolMaxBytesStr != "" {
	maxBytes, err := strconv.ParseInt(spoolMaxBytesStr, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid AEGIS_SPOOL_MAX_BYTES %q: %w", spoolMaxBytesStr, err)
	}
	cfg.SpoolMaxBytes = maxBytes
}
```
Duration keys (`AEGIS_AUDIT_GROUP_FLUSH_INTERVAL`, etc.): same shape with `time.ParseDuration` (the file already imports `time`), plus range validation returning `fmt.Errorf("invalid AEGIS_X %q: ...")`. Float key (`AEGIS_SPOOL_HARD_LIMIT_RATIO`): `strconv.ParseFloat`. New keys: see RESEARCH "Proposed defaults" table.

**Test style** (`config_test.go`): one `TestConfig` with `t.Run` subtests; defaults subtest uses `os.Unsetenv`, override subtests use `t.Setenv`, and invalid values assert `Error`:
```go
t.Run("invalid port returns error", func(t *testing.T) {
	t.Setenv("AEGIS_PORT", "invalid")
	_, err := LoadConfig()
	assert.Error(t, err)
})
```
Add the new keys to the "default configuration" unset list (`config_test.go:~16-27`) so a stray env var cannot flip defaults, then one default/override/invalid/out-of-range subtest each.

---

### `internal/telemetry/metrics.go` (+ `metrics_test.go`) (utility)

**Analog:** itself. Three-part pattern for every metric: (1) field on `Metrics` (`metrics.go:11-23`), (2) constructor entry (`:29-95`), (3) `reg.MustRegister(...)` entry (`:97-108`), (4) a small `Record*/Set*` method.

**CounterVec with closed labels** (`:45-51` + method `:126-135`):
```go
PolicyDecisionsTotal: prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "aegis_policy_decisions_total",
		Help: "Total authorization decisions partitioned by decision and reason code.",
	},
	[]string{"decision", "reason_code"},
),
...
func (m *Metrics) RecordPolicyDecision(decision, reasonCode string) {
	if decision == "" { decision = "unknown" }
	if reasonCode == "" { reasonCode = "NONE" }
	m.PolicyDecisionsTotal.WithLabelValues(decision, reasonCode).Inc()
}
```
**Gauge** (`:71-76`, setter `:156-158`): `SpoolUtilizationRatio` is the model for `aegis_audit_completion_queue_depth` and `aegis_audit_degraded`. **Histogram** (`:38-44`): `PolicyEvalDuration` with explicit `Buckets` is the model for `aegis_audit_flush_duration_seconds` (buckets 0.0002..0.1). **Counter** (`:77-82`): `SpoolBytesWrittenTotal`. `RecordSpoolBytes` (`:161-165`) already exists with no production caller and can be called from the committer.

Naming convention: `aegis_<area>_<noun>_total`. For the D-16 pre-middleware counter use e.g. `aegis_http_rejected_total{reason}` (RESEARCH Q5) with a CLOSED reason set (`concurrency`, `header_too_large`, `ambiguous_credentials`).

**Pitfall 7 (pre-initialise):** after construction call `.WithLabelValues(...).Add(0)` for each closed `(kind, reason)` combination so the series exist at scrape time.

**Test style** (`metrics_test.go`): single `TestPrometheusMetrics` with subtests. Add calls to the "Metric Registration and Increments" subtest, new `assert.Contains(t, content, "aegis_audit_...")` lines in the scrape subtest (`:~85-95`), and rely on the existing negative test that scrapes `/metrics` and fails on any label key in `{principal_id, client_ip, user_id, ip, path, url, query, bearer, token}` (`:~118-150`). New labels (`kind`, `reason`, `reason_code`) are safe; do not introduce `route_id` per-principal or any forbidden key.

---

### `internal/proxy/limiter.go` and `internal/proxy/server.go` (middleware; D-16 metrics-only counter)

**Analog:** the rejection sites themselves.

- Concurrency 429: `limiter.go:26-36` (`default:` branch of the semaphore select).
- Oversized-header 431: `server.go:140-150` (user) and `:185-195` (workload).
- Workload bearer-on-mTLS 401: `server.go:164-175`.

All three call `WriteProblemDetails(w, status, title, detail, typeURI, reqID)` then `return`. Add a one-line counter increment immediately before each `WriteProblemDetails`. `proxy` does not import `telemetry` today (imports in `server.go:3-15` are stdlib + `aegis/internal/config` + uuid). Follow the repo's consumer-side-interface convention: pass a tiny `RejectionRecorder interface{ RecordRejection(reason string) }` (nil-safe) via a new optional argument/setter on `NewConcurrencyLimiter` / `NewDualServer`, OR a setter such as `DualServer.SetRejectionRecorder`. Constructors currently take positional args only: `NewConcurrencyLimiter(maxConcurrent int)` (`limiter.go:13`), `NewDualServer(cfg, userHandler, workloadHandler, workloadTLSConfig)` (`server.go:118-123`). Changing these signatures breaks callers; grep before editing (`limiter_test.go:17,34,96` call `NewConcurrencyLimiter(n)`; `server_test.go` and tests/ call `NewDualServer`/`NewServer`). Prefer an additive setter to keep call sites compiling.

**Test analog:** `limiter_test.go:33-90` (hold a slot with `holdChan`, fire a second request, assert 429 + `Retry-After`, problem+json body). Extend with a fake recorder asserting the counter incremented once.

---

### `internal/proxy/proxy.go` (utility; only if ErrorHandler classification lives there)

**Analog:** `proxy.go:84-97`.
```go
ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
	reqID := requestID
	if reqID == "" && r != nil { reqID = r.Header.Get("X-Request-ID") }
	WriteProblemDetails(w, http.StatusBadGateway, "Bad Gateway",
		"Upstream backend unreachable or TLS handshake failed",
		"https://aegis.local/errors/bad-gateway", reqID)
},
```
RESEARCH keeps the wire behaviour unchanged and puts classification in `audit.WrapUpstreamErrorHandler` (wraps and delegates). Note `NewReverseProxy` (`:105-`) is the legacy variant; only `NewReverseProxyWithMTLS` is used by main.go.

---

### `migrations/000003_add_audit_suppressed_count.sql` (migration)

**Analog:** `migrations/000002_create_partitioned_audit_tables.sql`. Numbering is zero-padded 6-digit + snake_case description; embedded via `//go:embed *.sql` (`migrations/migrations.go`), applied by `goose.Up` (`internal/storage/migration.go:18-22`). Markers required by `internal/storage/db_test.go:55-77` (`TestEmbeddedMigrations_Integrity`: every file must contain `-- +goose Up` and `-- +goose Down`):
```sql
-- +goose Up
ALTER TABLE audit_events ADD COLUMN IF NOT EXISTS suppressed_count INT;

-- +goose Down
ALTER TABLE audit_events DROP COLUMN IF EXISTS suppressed_count;
```
Style match: 000002 uses `IF NOT EXISTS` / `IF EXISTS` guards on every statement. Column must be nullable (no DEFAULT needed). That test also asserts specific filenames for 000001 and 000002 only; add an `assert.Contains` for 000003 if desired. `event_type VARCHAR(32)` has no CHECK/enum, so no migration is needed for the `denial` type.

---

### Contract one-liners (OpenAPI / generated types / dashboard types)

**Analog:** the three lines themselves.
- `api/openapi/control-v1.yaml:869`: `enum: [decision, completion]` -> `enum: [decision, completion, denial]`.
- `pkg/api/control/v1/types.gen.go:21-26`: add a constant in generated style. Note the generated names are unqualified (`Completion`, `Decision`), so a new constant would be `Denial AuditEventEventType = "denial"`; check it does not collide with an existing identifier in the package (`grep -n "Denial" pkg/api/control/v1/types.gen.go`) and, if the repo regenerates via oapi-codegen, prefer regenerating over hand-editing.
- `web/dashboard/src/api/types.ts:2`: `'completion' | 'decision'` -> `'completion' | 'decision' | 'denial'`.

---

### `scripts/compose-smoke.sh` (live test)

**Analog:** itself. Conventions to copy:
- Helpers at top-level: `dc()` (`docker compose -f "${FILE}" "$@"`), `pass()`/`fail()` counting `FAILS`, `dev_token`, `gw_code TOKEN PATH` (curl status only, `-H "Authorization: Bearer $1"`), `audit_count` (`dc exec -T postgres psql -U aegis -d aegis -tAc '...' | tr -d '[:space:]'`), `wait_count_above N SECONDS` polling loop with `sleep 1`.
- Section layout: `echo "=== NAME: ... ==="` then `pass "NAME ..."` / `fail "NAME" "detail"`.
- `WAL_CURSOR="/var/log/aegis/wal/wal.cursor"` and `dc exec -T "${w}" cat "${WAL_CURSOR}"` for the per-worker cursor; workers enumerated with `mapfile -t WORKERS < <(dc config --services | grep '^audit-worker')`.
- Gateway selection: `GW="gateway-1"` for distributed else `gateway`.

New helper `gw_req_id TOKEN PATH`: copy `gw_code` but `curl -s -D - -o /dev/null` and parse `X-Request-ID`. New SQL assertions use the same `psql -tAc` form and `tr -d '[:space:]'`-style trimming. Place the new sections after the existing `AUDIT` section (the `# --- 4. AUDIT` block) and before A4. Update header comment at lines 3-14 ("It does NOT establish REV-03 or AUD-03") and the PASS wording "(proves drain wiring per spool, not AUD-03)" to stay honest.

Dedupe step uses `dc exec -T audit-worker rm /var/log/aegis/wal/wal.cursor` then `dc restart audit-worker` (assumption A2: `rm` exists; confirm). Cleanup is registered once with `trap cleanup EXIT` (lines ~150-160); add any new cleanup there, not a second trap.

---

### Compose files + `tests/compose/compose_test.go`

**Analog:** `compose_test.go`.
- `gatewayAllowlist` (`:32-57`) is a `map[string]bool` of every `AEGIS_*` key the gateway reads; the test at `:~290-296` asserts `gatewayAllowlist[key]` for each key set in compose. Any new key a compose file sets (e.g. `AEGIS_SPOOL_SEGMENT_BYTES` for the rotation run, or `AEGIS_AUDIT_SUPPRESS_WINDOW` for a short-window live run) must be added to this map in the SAME change, alphabetical placement is not enforced (map is grouped by topic).
- `TestGatewayStopGracePeriod` (`:341-363`) asserts `grace >= drain + 5s`. mvp compose has `stop_grace_period: 45s` (`docker-compose.mvp.yml:113`) with comment `# Drain timeout 30s + 5s metrics shutdown + margin.`; update the comment to include the 5s audit-pipeline shutdown (30 + 5 audit + 5 metrics = 40 < 45). Tightening the lint to `drain + 10s` is optional per RESEARCH; if done, check all three profiles still pass.
- Gateway env in compose is a list of `- AEGIS_FOO=bar` strings (`docker-compose.mvp.yml:100-109`); worker prune is `- AEGIS_PRUNE_ARCHIVED=true` (`:152`).

---

## Shared Patterns

### Nil-safe, mutex-guarded context mutators
**Source:** `internal/audit/logger.go:55-121`
**Apply to:** any new `AuditContext` setter (`decisionSet`, upstream error classification).
```go
if ac == nil { return }
ac.mu.Lock()
defer ac.mu.Unlock()
```

### Fail closed on audit failure
**Source:** `cmd/gateway/main.go:535-565` (saturation -> 503 `AUDIT_SPOOL_SATURATED`; other write error -> 500 `AUDIT_SPOOL_WRITE_ERROR`), `internal/audit/spool.go:156-159` (`IsSaturated` treats check errors as saturated).
**Apply to:** the fault flag wiring (D-12): `CheckSaturation()` returns true while faulted, which makes the unchanged `AppendPreForward` gate return `ErrSpoolSaturated` and `/readyz` go 503 via `CreateProbeHandler(... diskSpool.IsSaturated ...)` (`main.go:907-913`).

### Deny responses always go through `proxy.WriteProblemDetails` and set decision/error code first
**Source:** `cmd/gateway/main.go:536-550`
```go
if ac != nil {
	ac.SetDecision("deny", "AUDIT_SPOOL_SATURATED")
	ac.SetErrorCode("AUDIT_SPOOL_SATURATED")
}
proxy.WriteProblemDetails(w, http.StatusServiceUnavailable, "Service Unavailable",
	"Audit spool saturated; halting admission", "https://aegis.local/errors/spool-saturated", reqID)
return
```
**Apply to:** the first-`WriteHeader` hook design: decision/reason/principal are final when `WriteProblemDetails` calls `WriteHeader`, so no main.go denial site needs to change.

### Consumer-side interfaces for cross-package decoupling
**Source:** `internal/audit/worker.go:30-34` (`BatchPool`)
**Apply to:** `audit.Recorder` (satisfied by `*telemetry.Metrics`), proxy `RejectionRecorder`.

### Operational logging
**Source:** `worker.go:286,361`, `main.go:551`: stdlib `log.Printf("<Component> <what> warning: %v", err)`. Structured audit lines go through `audit.Logger` (slog JSON) only.
**Apply to:** committer error logs and the D-12 "logs it" requirement; rate-limit repeated log lines (RESEARCH).

### Closed-label metrics and negative PII assertions
**Source:** `internal/telemetry/metrics.go` (label sets), `metrics_test.go` negative test
**Apply to:** every new metric (no principal/path/IP/token labels; unknown -> catch-all value).

### Test conventions
**Source:** `spool_test.go`, `worker_test.go`, `logger_test.go`, `limiter_test.go`
`testify` `assert`/`require`, same-package tests, temp dirs via `os.MkdirTemp` + `defer os.RemoveAll` (spool_test) or `t.TempDir()` (worker_test; prefer this for new tests), `pgxmock.NewPool()` for DB, `httptest.NewRecorder()` for handlers. Host disk was 88 percent full during research: new tests must set `VolumeQuotaBytes` and inject `StatfsFunc`; fallback `TMPDIR=/dev/shm/aegis-gotmp`.

---

## No Analog Found

| File / Piece | Role | Data Flow | Reason |
|--------------|------|-----------|--------|
| `audit.Option` / `WithSink` functional option | config | n/a | No functional-option pattern exists in repo; introduce a minimal `type Option func(*middlewareConfig)` |
| Injectable clock (`Admit(ev, now)`, `Sweep(now)`) | utility | transform | No production code injects a clock; tests elsewhere fast-forward miniredis only (`revocation/store_test.go:111`) |
| `CompletionEvent.Normalize()` (NUL strip, UTF-8, rune-boundary truncation) | utility | transform | No sanitiser exists in repo; use RESEARCH "Normalisation chokepoint" |
| In-memory token bucket / keyed suppression map with sweeper | utility | transform | `internal/ratelimit` is Redis-backed; nothing in-process |
| Group-commit goroutine with dual channels and waiter `done` chans | service | event-driven | Closest is the worker ticker loop and `ConcurrencyLimiter` non-blocking `select`; no batching writer exists |
| `go/parser` drift-guard test of `cmd/gateway/main.go` (`main_reasons_test.go`) | test | n/a | No source-parsing test in repo; nearest is the hand-maintained allowlist lint in `tests/compose/compose_test.go` |

## Metadata

**Analog search scope:** `internal/audit`, `internal/proxy`, `internal/telemetry`, `internal/config`, `internal/snapshot`, `internal/storage`, `internal/ratelimit`, `cmd/gateway`, `cmd/audit-worker`, `migrations`, `scripts`, `tests/compose`, `tests/failure`, `deployments/compose`, plus the three contract files.
**Files scanned:** ~40 (read in full: spool.go, logger.go, event.go, worker.go, metrics.go, config.go, proxy/server.go, proxy/limiter.go, telemetry/server.go; read in part: main.go (lines 1-262, 525-600, 895-965), proxy.go, spool_test.go, worker_test.go, logger_test.go, metrics_test.go, config_test.go, limiter_test.go, compose_test.go, compose-smoke.sh, db_test.go, audit-worker main).
**Pattern extraction date:** 2026-10-09
