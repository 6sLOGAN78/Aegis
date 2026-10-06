# Pitfalls Research

**Domain:** Distributed Zero-Trust Access Gateway & Go Reverse Proxy
**Researched:** 2026-10-06
**Confidence:** HIGH

---

## Critical Pitfalls

### Pitfall 1: Path Normalization, Encoding, and Traversal Bypass (`%2F`, `..`, Unicode, NUL)

**What goes wrong:**
The gateway evaluates authorization on one interpretation of an HTTP request path, but the upstream backend decodes and executes a different path. For example, an attacker requests `/api/orders/%2e%2e/admin/users` or `/api/orders%2F..%2Fadmin`. The gateway's path matcher treats `%2e%2e` as a literal path segment or matches prefix `/api/orders/` and permits the request under normal `developer` role privileges. When the gateway proxies the raw or partially unescaped path, the backend router or framework normalizes the dot-segments and executes the protected administrative handler `/api/admin/users`. Other variants include `%2F` (encoded slash) altering route boundaries, NUL bytes (`%00`) truncating paths in C-based upstreams, multiple consecutive slashes (`//api/admin`) evading exact string equality, or Unicode normalization discrepancies (e.g., full-width slashes `\uFF0F`).

**Why it happens:**
Standard Go routing and proxying libraries have historically treated path cleaning and forwarding inconsistently. The legacy `httputil.ReverseProxy.Director` pattern modifies `req.URL.Path` while leaving `req.URL.RawPath` unchanged, leading to proxy desynchronization where the backend sees different bytes than the authorization engine evaluated. Furthermore, developers attempt to "clean" or URL-decode paths before policy evaluation using `path.Clean()` or naive string manipulation, creating parser disparity (CWE-22 / CWE-444) between the gateway policy engine, the gateway proxy transport, and the backend application framework.

**How to avoid:**
1. Grounded in `spec.md` Section 5 & Invariant 7: **Enforce strict canonical path evaluation where the policy engine and proxy evaluate and forward the exact same validated path bytes.**
2. Adopt Go 1.20+ `httputil.ReverseProxy.Rewrite` instead of the deprecated `Director`. Explicitly control `OutReq.URL.Path` and `OutReq.URL.RawPath`.
3. Strict fail-close validation: Reject requests with HTTP 400 Bad Request immediately if the path contains:
   - Encoded slashes or backslashes: `%2F`, `%2f`, `%5C`, `%5c`
   - Dot-segment variants: `..`, `%2e%2e`, `.%2e`, `%2e.`
   - NUL bytes: `\x00` or `%00`
   - Repeated slashes (`//`)
   - Ambiguous percent encodings or invalid UTF-8 sequences.
4. **Never attempt to "repair" or normalize ambiguous paths.** Return an immediate 400 error.
5. In integration tests, execute a parameterized fuzzing matrix containing traversal sequences, encoded separators, and Unicode edge cases against protected routes to assert 400 rejections.

**Warning signs:**
- Gateway logs show path `/api/orders/%2e%2e/admin`, but backend logs show `/api/admin`.
- Route matching logic uses `strings.HasPrefix(r.URL.Path, prefix)` without prior delimiter validation.
- Unit tests only test clean paths (`/api/orders/123`) without negative test suites for `%2e%2e` or `%2f`.

**Phase to address:**
Phase 1 (Secure Vertical Slice / MVP)

---

### Pitfall 2: Header Spoofing and Forwarding Leakage (`X-Forwarded-*`, `X-Aegis-*`, Hop-by-Hop)

**What goes wrong:**
An external attacker injects headers such as `X-Aegis-User: admin`, `X-Forwarded-For: 127.0.0.1`, or `X-Client-Cert-SAN: spiffe://aegis.local/workload/admin`. If the gateway blindly forwards client headers or if private backend microservices rely on unauthenticated identity headers forwarded over the wire, the attacker achieves complete authorization bypass and identity impersonation. Additionally, an attacker can exploit RFC 7230 hop-by-hop headers (`Connection: close, X-Aegis-Context`) to manipulate how intermediate proxies or Go's transport layer strip or pass headers, stripping downstream security metadata while preserving forged headers.

**Why it happens:**
Developers building reverse proxies often rely on default forwarding behavior or assume internal networks are trusted. Backend microservices are frequently written to read convenience headers (`r.Header.Get("X-User-Id")`) without verifying cryptographic signatures, falsely assuming that "traffic reaching this service has already passed the gateway."

**How to avoid:**
1. Grounded in `spec.md` Section 5 & Invariants 5 and 8: **No incoming header can impersonate a gateway-generated identity or authorization decision.**
2. At the gateway ingress pipeline:
   - Strip all incoming `X-Aegis-*` headers unconditionally.
   - Strip all client-supplied `Forwarded` and `X-Forwarded-*` headers unless received from an explicitly configured, trusted upstream load balancer CIDR.
   - Strip all RFC 7230 hop-by-hop headers (`Connection`, `Keep-Alive`, `Proxy-Authenticate`, `Proxy-Authorization`, `TE`, `Trailers`, `Transfer-Encoding`, `Upgrade`).
   - Reconstruct `X-Forwarded-For`, `X-Forwarded-Proto`, and `X-Request-ID` strictly from the authenticated connection context.
3. For backend identity propagation, **never forward raw external user tokens or plain HTTP headers**. Instead, generate a signed backend assertion JWT:
   - Issuer: `aegis-gateway`
   - Audience: Destination service ID (e.g., `payments`)
   - Subject: Original authenticated principal ID
   - Bound context: Request ID, HTTP method, canonical path, policy version
   - Lifetime: Bounded to maximum 15 seconds
   - Signature: Signed with a dedicated gateway private key (distinct from user OIDC signing keys).
4. Backend services must run verified middleware that validates the signed assertion's signature, audience, expiration, and path before processing any request.

**Warning signs:**
- Backend services call `r.Header.Get("X-User-Role")` without verifying a cryptographic JWT signature.
- Negative test suite lacks tests injecting forged `X-Aegis-*` headers.
- Gateway configuration uses `r.Header.Clone()` and forwards it without an explicit header allowlist/striplist.

**Phase to address:**
Phase 1 (MVP Header Sanitization) & Phase 2 (Signed Backend Assertions & Backend Middleware)

---

### Pitfall 3: Synchronous Dependency Failure Causing "Fail-Open" Authorization Bypass

**What goes wrong:**
When an external dependency (such as Redis for rate limits/revocation checks, or PostgreSQL/Control Plane for configuration) experiences latency, a network partition, or a crash, the gateway's request pipeline catches an error or timeout. If error handling defaults to allowing the request through, treats the error as non-fatal, or unhandled errors fall through to the proxy handler, unauthenticated, unauthorized, or revoked requests are forwarded to backend services. Attackers deliberately trigger dependency exhaustion (e.g., flooding Redis) to force the gateway into a fail-open degraded mode.

**Why it happens:**
Engineers trained on high-availability web services often intuitively prioritize availability over consistency/security ("if the cache is down, let the request pass so the user isn't disrupted"). In a zero-trust security architecture, this intuition is fatal: availability MUST yield to authorization integrity.

**How to avoid:**
1. Grounded in `spec.md` Section 3, Section 4, & Invariant 1: **Default-deny and fail-closed across all paths. No matching rule or an evaluation error means deny.**
2. Redis is explicitly an **availability dependency** for protected requests (`spec.md` Section 4 & Section 10). If a Redis token revocation check or principal quarantine check fails, times out, or returns a connection error, the gateway MUST return HTTP 503 Service Unavailable with reason code `REVOCATION_CHECK_UNAVAILABLE`.
3. Enforce strict timeouts on all dependency calls: bounded to 200ms per dependency check.
4. If an in-memory OPA evaluation returns an error, empty decision, or unexpected schema, default immediately to HTTP 503 or 403; never proceed to the reverse proxy director/rewrite phase.
5. In CI and failure testing, execute chaos tests that drop Redis connections or crash Redis, asserting that 100% of subsequent protected requests return 503 and 0 requests reach backend services.

**Warning signs:**
- Code patterns like:
  ```go
  if err := checkRevocation(jti); err != nil {
      logger.Warn("Redis check failed, proceeding anyway", "err", err)
      // BUG: fails open!
  }
  ```
- Absence of negative integration tests verifying behavior during Redis downtime.
- Readiness probe reporting `Healthy` when Redis is completely partitioned.

**Phase to address:**
Phase 1 (Default-Deny Policy Execution) & Phase 3 (Redis Revocation Fail-Closed Architecture)

---

### Pitfall 4: Split-Brain or Stale Policy Execution Without Leases and Monotonic Versioning

**What goes wrong:**
In a distributed deployment with multiple gateway replicas, control plane network partitions or asynchronous distribution delays cause replicas to run conflicting policy versions. Replica A enforces Policy v12 (which revoked a vulnerable route or quarantined a compromised role), while Replica B remains partitioned and silently continues enforcing Policy v11. An attacker simply retries requests until load-balanced to Replica B to exploit revoked permissions. Furthermore, if a control plane restarts and publishes an older policy snapshot or a gateway reboots and loads an unversioned stale cache, security configuration silently regresses.

**Why it happens:**
Teams often distribute policies using simple pub/sub, loose polling without freshness leases, or direct database reads without snapshot isolation. Without atomic snapshot distribution and monotonic version tracking, replicas cannot detect when their in-memory security rules have drifted or expired.

**How to avoid:**
1. Grounded in `spec.md` Section 6 & Invariant 9:
   - Configuration activation is **atomic**. A single immutable snapshot contains routes, Rego modules, policy data, and identity mappings. A request cannot evaluate routes from version $N$ and Rego policies from version $N-1$.
   - Snapshots contain a strictly monotonic integer version number, cryptographic signature (Ed25519/ECDSA), publisher key ID, and SHA-256 payload checksum.
   - Gateways **strictly reject** lower or duplicate conflicting version numbers. Rollbacks are executed exclusively by publishing a *new higher monotonic version* containing the previous configuration.
2. **Enforce bounded signed freshness leases:**
   - The control plane issues a signed freshness lease renewed every 10 seconds over an authenticated gRPC stream.
   - If a gateway fails to receive and verify a lease renewal for >60 seconds, its readiness probe fails, and protected requests immediately return HTTP 503 (`POLICY_LEASE_EXPIRED`).
   - Gateways must never authorize traffic indefinitely on cached snapshots during prolonged control plane disconnects.
3. Every gateway acknowledgment reports its active snapshot version, lease timestamp, and activation status to PostgreSQL so operators and dashboards can detect divergence in real time.

**Warning signs:**
- Gateways reading policy files directly from disk using `fsnotify` without version headers or signatures.
- Gateway readiness probes checking only `/healthz` HTTP 200 without checking policy snapshot age and lease validity.
- Lack of convergence metrics (`aegis_snapshot_active_version` differing across replica instances in Prometheus).

**Phase to address:**
Phase 3 (Control Plane Snapshot Distribution & Leases) & Phase 5 (Distributed Convergence & Drain)

---

### Pitfall 5: Workload Identity Header Trust Instead of End-to-End mTLS URI SAN Verification

**What goes wrong:**
Workloads (e.g., `orders`, `payments`) authenticate to the gateway, but the gateway terminates TLS at an outer reverse proxy/ingress and relies on HTTP headers (e.g. `X-Client-Cert-SAN`, `X-Workload-ID`) to determine caller identity. A compromised container or malicious insider on the internal network crafts plain HTTP requests with forged headers, masquerading as the high-privilege `orders` workload to invoke sensitive `/api/payments` endpoints. Alternatively, the gateway accepts both user bearer tokens and workload client certificates on the same HTTP listener, causing ambiguous principal selection vulnerabilities.

**Why it happens:**
Configuring mutual TLS (mTLS) with custom client certificate validation in development environments and Docker Compose can feel cumbersome. Developers frequently compromise by terminating TLS at an edge ingress or reverse proxy, passing identity via headers, and assuming the internal Docker/Kubernetes network is inherently secure.

**How to avoid:**
1. Grounded in `spec.md` Section 4 & Invariants 2, 4, 5, and 12: **Network location is context, never sufficient identity or authorization.**
2. Use a **dedicated mTLS listener** (e.g., port `:8443`) exclusively for workload-to-gateway traffic, distinct from user access (port `:443` or `:8080`).
3. Reject user bearer tokens on the workload mTLS listener to prevent ambiguous principal selection.
4. Workload identity MUST be extracted directly from the verified client certificate's URI Subject Alternative Name (SAN) (e.g., `spiffe://aegis.local/workload/orders`). Verify the root CA trust chain, Extended Key Usage (`clientAuth`), validity window, and trust domain on every TLS handshake.
5. **Backend Bypass Prevention:**
   - Private backend services must never expose published host ports in Docker Compose or public ingress in Kubernetes.
   - Backends must run mTLS middleware verifying that the caller's certificate matches the designated gateway identity. Direct calls between workloads (e.g., `orders` calling `payments` directly, bypassing the gateway) must fail at the network and TLS layers.
   - Backends must verify the short-lived signed assertion issued by the gateway (`spec.md` Section 5).

**Warning signs:**
- Docker Compose exposes backend ports directly (`ports: ["8081:8081"]`).
- Workload authorization logic reads identity from `r.Header.Get("X-Workload-Identity")`.
- Workload certificates lack SPIFFE URI SANs or rely solely on Common Name (CN), which is deprecated and ambiguous.

**Phase to address:**
Phase 2 (Workload Identity & Bypass Prevention)

---

### Pitfall 6: Audit Record Loss During Database Outage Without Durable Pre-Forward Spool

**What goes wrong:**
A gateway authorizes a mutating action (e.g., `POST /api/payments`), forwards the request to the backend service, and attempts to write an audit log asynchronously into an in-memory queue or directly to PostgreSQL. If PostgreSQL is experiencing high latency, lock contention, or an outage, the in-memory queue overflows and drops audit records. Worse, if the gateway process crashes or is killed by Kubernetes before flushing its in-memory buffer, the payment mutation commits on the backend, but zero audit trail exists. This violates non-repudiation, zero-trust accountability, and regulatory compliance. Conversely, performing synchronous database writes on the request hot path introduces catastrophic 20–100ms latency spikes and saturates DB connection pools.

**Why it happens:**
Balancing audit durability with gateway throughput is hard. Developers either choose synchronous database writes (killing performance) or naive in-memory channels (e.g., `make(chan AuditEvent, 1000)`) that drop events under load or process restart.

**How to avoid:**
1. Grounded in `spec.md` Section 8 & Invariant 10: **All permitted requests require a durable pre-forward audit record in the production profile.**
2. Implement a **local, append-only durable disk spool (WAL)** on persistent storage per gateway replica:
   - For every permitted request, append the authorization decision record to the local spool file and execute `fsync` *before* proxying the request to the upstream backend.
   - If the spool write fails or disk is full, the request MUST be rejected with HTTP 503 before forwarding.
3. Decouple ingestion from delivery: A background audit worker process reads spooled records, batches them into PostgreSQL, and checkpoints disk read offsets only after successful database commits (at-least-once delivery).
4. Deduplicate events at the PostgreSQL level using a composite key: `(partition_timestamp, request_id)`.
5. Enforce strict disk bounds: Default 1 GiB spool capacity per replica. Alert at 70% saturation. If spool reaches 90% capacity, **stop admitting permitted requests** (return HTTP 503) rather than silently dropping audit logs.

**Warning signs:**
- Audit logging implemented as a non-blocking Go channel with `select { case ch <- event: default: drop() }`.
- Audit records emitted only in an HTTP middleware `defer` statement *after* the backend response has completed.
- Gateway test suite passes when PostgreSQL is shut down, but no local spool files are generated.

**Phase to address:**
Phase 3 (Control Plane, Durable State & Audit Worker)

---

### Pitfall 7: Goroutine, Memory, and Connection Leaks Under Ingress Flooding

**What goes wrong:**
Under sustained ingress floods, slow clients (Slowloris), or lagging backends, the gateway process leaks goroutines, file descriptors, and memory until it crashes with out-of-memory (OOM) or thread exhaustion. In Go reverse proxies, three primary failure modes dominate:
1. Standard `http.Server` without explicit timeout limits allows slow clients to hold idle connections open indefinitely.
2. In custom HTTP transport or reverse proxy error paths, failing to drain and close `resp.Body` (`io.Copy(io.Discard, resp.Body)` and `resp.Body.Close()`) prevents underlying TCP socket reuse, exhausting ephemeral ports and keeping goroutines alive.
3. In embedded OPA evaluation, creating a new `rego.New()` object or compiling Rego queries on every incoming request causes severe heap allocations, AST parsing churn, and GC pauses that degrade latency from <2ms to >500ms.

**Why it happens:**
Go makes concurrency so effortless that developers assume goroutines and garbage collection are free. Default configurations in Go's `net/http` package are optimized for general-purpose convenience rather than edge gateway resilience.

**How to avoid:**
1. Grounded in `spec.md` Section 10: Explicitly configure production timeouts on `http.Server`:
   - `ReadHeaderTimeout = 5 * time.Second`
   - `IdleTimeout = 120 * time.Second`
   - `MaxHeaderBytes = 16 * 1024` (16 KiB)
2. In `httputil.ReverseProxy` and transport configuration:
   - Configure a dedicated `http.Transport` with `MaxIdleConns = 1000`, `MaxIdleConnsPerHost = 100`, `IdleConnTimeout = 90 * time.Second`, `ResponseHeaderTimeout = 5 * time.Second`, and `ExpectContinueTimeout = 1 * time.Second`.
   - Ensure all response bodies in custom error/intercept handlers are fully drained and closed:
     ```go
     defer resp.Body.Close()
     io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
     ```
3. Precompile OPA Rego queries once during snapshot activation using `rego.PrepareForEval(ctx)`. Re-use the thread-safe `rego.PreparedEvalQuery` across all concurrent request goroutines; pass only typed request input maps to `Eval()`.
4. Apply global concurrency bounds (e.g., bounded worker pool or semaphore pattern) and principal rate limiting before spawning downstream work.

**Warning signs:**
- Go `pprof` profile shows goroutine count climbing monotonically under load test.
- `netstat`/`ss` showing thousands of sockets stuck in `CLOSE_WAIT` or `TIME_WAIT` toward backends.
- Heap profile shows significant allocations originating from `github.com/open-policy-agent/opa/ast.Compile`.

**Phase to address:**
Phase 1 (Server Timeout Baselines & OPA Precompilation) & Phase 5 (Distributed Load Testing & Resource Bounds)

---

## Technical Debt Patterns

Shortcuts that seem reasonable during initial implementation but introduce critical security or operational failure modes.

| Shortcut | Immediate Benefit | Long-term Cost | When Acceptable |
|---|---|---|---|
| Hardcoding Rego policies in Go strings or compiling per request | Avoids building control plane snapshot compilation pipeline | Extreme CPU/memory overhead, GC pauses, impossible to update policies dynamically | MVP Phase 1 prototype only (with `PrepareForEval` caching); never compile dynamically per request |
| Shared database credentials across all services | Simple Docker Compose environment setup | If demo service `orders` is compromised, attacker directly accesses `payments` and `audit_events` tables | Never. Database credentials must be strictly service-scoped |
| Relying solely on Docker bridge networks for isolation without mTLS | Skips certificate generation and TLS verification boilerplate | Any container sharing the Docker network can spoof IP/ports and reach private backend microservices | Never. Enforced bypass prevention requires application-level mTLS middleware from Phase 2 onward |
| In-memory rate limiting instead of Redis token bucket | No Redis dependency needed in local development | Rate limits are isolated per replica; traffic scale multiplies allowed burst by $N$ gateway instances | Phase 1 (MVP single-replica) only; must migrate to Redis atomic token bucket in Phase 3 |
| Single shared cryptographic key pair for user tokens and backend assertions | Simplifies key management scripts | Compromise of public backend assertion verification key allows forging external user access tokens | Never. Maintain distinct keys and validation profiles for user tokens vs backend assertions |
| Logging request bodies and query parameters for debugging | Fast debugging of failed requests | Secrets, tokens, PII, and financial data leak into audit stores and log collectors | Never. Strict redaction: log only validated path, method, principal ID, request ID, and decision |
| Using `time.Sleep` in tests for async synchronization | Fast test authoring | Flaky CI builds, race conditions, unpredictable timeouts under load | Never. Use explicit synchronization primitives, channels, or polling with bounded deadlines |

---

## Integration Gotchas

Common mistakes when integrating core external and embedded components.

| Integration | Common Mistake | Correct Approach |
|---|---|---|
| **Embedded OPA Rego SDK** | Invoking `rego.New(...).Eval(ctx)` on every request hot path. Also, allowing Rego rules to use network built-ins like `http.send`. | Compile once at snapshot activation via `r.PrepareForEval(ctx)`. Store the resulting `rego.PreparedEvalQuery` in the immutable snapshot struct. Disable networking built-ins; pass all context via typed in-memory input. |
| **Redis 7+ (Rate Limits & Revocation)** | Using non-atomic multiple commands (`GET`, `SET`) or calling `KEYS *` to inspect revocations, blocking the single-threaded Redis event loop. | Use atomic Lua scripts for token-bucket rate limiting. Use direct key existence checks (`EXISTS rev:jti:<jti>`) for revocations with explicit TTL matching token lifetime. Enforce 200ms context deadlines. |
| **Go `httputil.ReverseProxy`** | Using deprecated `Director` hook, which fails to distinguish between inbound and outbound request objects and mishandles `X-Forwarded-*`. | Use Go 1.20+ `Rewrite` hook receiving `*httputil.ProxyRequest`. Explicitly inspect `r.In` and modify `r.Out`. Clear sensitive headers and configure `ErrorHandler` to emit structured 502/504 audit events. |
| **PostgreSQL 16+ (Audit Store)** | Writing high-volume audit logs to a single unpartitioned table with multiple b-tree indexes, causing write saturation and slow vacuuming. | Use daily declarative range partitioning on `audit_events` by `timestamp`. Use synthetic composite keys `(partition_timestamp, request_id)` for idempotent upserts. Worker batches inserts using `COPY` or multi-value `INSERT`. |
| **gRPC Snapshot Stream** | Establishing unbuffered gRPC streams without keepalives or reconnect jitter, causing thundering herds when the control plane restarts. | Implement client-side exponential backoff with full jitter (e.g. 1s base, 30s max). Configure gRPC keepalive parameters (`Time: 30s, Timeout: 10s`). Trigger full snapshot reconciliation upon reconnect. |
| **Workload mTLS & SPIFFE SANs** | Checking client certificate Common Name (CN) instead of URI SAN, or accepting wildcard trust domains (`spiffe://*`). | Parse `cert.URIs`. Validate exact URI scheme (`spiffe`), expected trust domain (`aegis.local`), and path format (`/workload/{service}`). Ensure `ExtKeyUsage` includes `ExtKeyUsageClientAuth`. |

---

## Performance Traps

Patterns that work at small scale (10 req/s) but degrade or fail catastrophically under production loads (1,000+ req/s).

| Trap | Symptoms | Prevention | When It Breaks |
|---|---|---|---|
| **Redis Key Cardinality Explosion** | Redis memory climbs continuously; latency increases from sub-millisecond to >50ms; Redis OOM evicts revocation keys. | Never store unauthenticated client IPs or arbitrary query parameters as unconstrained Redis keys. Enforce global ingress rate limits before principal resolution; set strict TTLs on all rate-limit keys matching the bucket window. | Breaks during DDoS scans or port sweeps (>10,000 unique source IPs/minute). |
| **Prometheus Metric Label Cardinality Explosion** | Gateway memory balloons; Prometheus scrape times exceed 10s; Prometheus crashes with out-of-memory. | **Never include high-cardinality values as Prometheus labels**: no `principal_id`, `request_id`, user IDs, or raw URL paths with IDs. Use normalized route templates (`/api/orders/{id}`) and bounded reason codes. | Breaks when traffic exceeds ~500 unique authenticated users or dynamic URLs. |
| **Synchronous Disk `fsync` Bottleneck** | Gateway p99 latency spikes to 15–30ms; disk queue length increases; CPU remains low while I/O waits peak. | Naive per-goroutine `file.Sync()` limits throughput to disk IOPS (~500–1000 IOPS on standard SSD). Implement **group committing / batch fsyncing** in the durable spool worker where concurrent requests share a single disk flush cycle. | Breaks at >500 req/s on standard SSDs without group commit batching. |
| **OPA Rego AST Allocation Churn** | High memory allocation rates; Go GC pauses exceed 10ms; CPU consumed by `runtime.mallocgc`. | Pass flat, typed Go structs/maps as policy input. Avoid serializing large JSON request bodies into Rego. Precompile policies via `PrepareForEval`. | Breaks at >2,000 req/s with dynamic JSON unmarshaling. |
| **TCP Port Exhaustion to Upstream Backends** | Gateway returns HTTP 502; errors log `dial tcp: cannot assign requested address`; sockets stuck in `TIME_WAIT`. | Re-use HTTP connections via a single shared `http.Transport` instance. Set `MaxIdleConnsPerHost >= 100`. Always read and close upstream response bodies to return sockets to the idle pool. | Breaks at >1,000 req/s under bursty traffic with default `MaxIdleConnsPerHost = 2`. |

---

## Security Mistakes

Domain-specific security issues beyond general web vulnerabilities.

| Mistake | Risk | Prevention |
|---|---|---|
| **Clock Skew Token Invalidation** | Legitimate users or workloads are rejected with 401 Unauthorized immediately after token issuance due to minor host NTP drift. | Grounded in `spec.md` Section 4: Allow a **30-second clock skew tolerance** in JWT expiration (`exp`) and not-before (`nbf`) validations. Default access token lifetime is 5 minutes; require host NTP synchronization. |
| **CORS / CSRF on Management API & BFF** | Cross-origin browser attacker executes administrative actions (e.g. publishing malicious policies or quarantining workloads) via victim's browser session. | Grounded in `spec.md` Section 4 & Section 7: Browser BFF uses `HttpOnly`, `Secure`, `SameSite=Lax` or `Strict` session cookies. All mutating endpoints (`POST`, `PUT`, `DELETE`) require a cryptographically verified `X-CSRF-Token` header. Restrict CORS to exact management dashboard origins. |
| **JWT Algorithm Confusion & Remote Key Injection** | Attacker signs token using HMAC with public RSA key, or injects attacker-controlled JWKS URL (`jku`/`x5u`) in JWT header, achieving full authentication bypass. | Grounded in `spec.md` Section 4: Enforce an explicit allowlist of pinned signature algorithms (`RS256` or `EdDSA`). **Strictly reject `alg: "none"`**. Never fetch keys from `jku` or `x5u` headers; resolve verification keys only from locally configured issuer JWKS endpoints. |
| **Host Header & Upstream URL Manipulation** | Attacker supplies `Host: internal-admin` or proxy-form request (`GET http://internal.bank/ HTTP/1.1`) to trick gateway into routing to unauthorized internal services (SSRF). | Grounded in `spec.md` Section 3 & Invariant 6: **A route identifies a configured fixed upstream; request headers, query parameters, and absolute URLs never choose an upstream.** Reject proxy-form URLs; route strictly by matched path template in active snapshot. |
| **Policy & Route Version Desynchronization** | Gateway evaluates route routing rules from version $N$ but runs Rego policy rules from version $N-1$, creating a semantic authorization mismatch. | Grounded in `spec.md` Invariant 9: Atomic snapshot activation. The gateway holds an atomic pointer (`atomic.Pointer[Snapshot]`) containing both compiled routes and the precompiled Rego query. Hot-path requests grab a single immutable reference. |
| **Treating Shared Docker Network as Security Perimeter** | Developer assumes that because backend services are not published to the host, they are secure from unauthorized callers. | Grounded in `spec.md` Section 5 & Invariant 12: Network location is never sufficient proof. Backends must require mTLS client certificates from the gateway and verify signed backend assertions. |

---

## UX Pitfalls

Operational and developer experience failure modes in zero-trust gateways.

| Pitfall | User / Operator Impact | Better Approach |
|---|---|---|
| **Opaque 403 Forbidden Without Reason Codes** | Client receives a bare `403 Forbidden` with no explanation. Developers spend hours wondering whether the failure was due to token expiry, missing role, quarantine, or path mismatch. | Return a structured JSON error body containing a machine-readable reason code (`ROLE_NOT_PERMITTED`, `PRINCIPAL_QUARANTINED`, `ROUTE_NOT_FOUND`) and a unique `request_id` matching audit records. Never expose internal stack traces or policy Rego source. |
| **Silent Metric Dropping Under Ingress Floods** | Under heavy load, the gateway silently aggregates or drops denial audit events without alerting operators, creating forensic blindness. | If audit admission queue exceeds capacity, expose exact aggregation counters, fire a high-severity Prometheus alert (`AegisAuditEventsDropped`), and clearly label aggregated metrics in the dashboard. |
| **Dashboard Displaying Stale or Simulated Data as Live State** | Operator modifies a policy in the simulator and mistakenly believes it is actively enforced in the cluster, or views dashboard metrics unaware that the gateway reporting them is partitioned. | Grounded in `spec.md` Section 7: Visually differentiate live cluster state from simulation dry-runs. Every metric card must display freshness timestamps and replica convergence status ($N/M$ replicas active). |
| **Irreversible Workload Quarantine** | Operator quarantines a workload during an incident, but restoring service requires manual Redis database flushes or gateway restarts. | Implement an audited unquarantine management endpoint (`DELETE /control/v1/principals/{id}/quarantine`) that immediately removes the block in Redis and emits an audit event. |
| **Unbounded Gateway Shutdowns Dropping In-Flight Traffic** | Gateway process terminates immediately on `SIGTERM`, severing active client requests and leaving dangling transactions. | Grounded in `spec.md` Section 10: Implement a **30-second graceful drain window**. Gateway fails its readiness probe to stop receiving new load-balancer connections, flushes in-flight proxies and audit spools, and only then exits. |

---

## "Looks Done But Isn't" Checklist

Critical verification gates where implementations frequently appear complete in local demos but fail in production or security evaluations.

- [ ] **OPA Policy Engine:** Often missing precompilation or hot-path snapshot isolation — verify that `rego.PrepareForEval` is executed during snapshot load and that zero Rego AST compilation occurs during request evaluation under race detector (`go test -race`).
- [ ] **Path Traversal Protection:** Often missing encoded separator and dot-segment rejection — verify that requests to `/api/orders/%2e%2e/admin`, `/api/orders/%2f`, and `//api/admin` return HTTP 400 Bad Request at the gateway before any route lookup occurs.
- [ ] **Workload mTLS Authentication:** Often missing dedicated listener separation and URI SAN extraction — verify that client certificates are enforced on port `:8443`, that identity is extracted strictly from `URI:spiffe://...`, and that user bearer tokens on this port are rejected.
- [ ] **Redis Revocation & Fail-Closed:** Often missing failure-mode testing — verify that when Redis is stopped (`docker compose stop redis`), protected requests immediately return HTTP 503 (`REVOCATION_CHECK_UNAVAILABLE`) and zero requests reach backend services.
- [ ] **Durable Audit Spooling:** Often missing pre-forward fsync durability — verify that decision records are written and fsync'd to disk *before* forwarding to the upstream backend, and that when PostgreSQL is stopped, requests are admitted until spool reaches 90% capacity, then rejected with 503.
- [ ] **Bypass Prevention:** Often missing backend middleware verification — verify that executing `curl -k https://payments:8443/api/payments` from inside another container fails with TLS handshake failure or 401 Unauthorized because the caller lacks the gateway certificate and signed assertion.
- [ ] **Control Plane Lease Expiry:** Often missing client-side lease eviction — verify that when the control plane is stopped, the gateway continues serving for up to 60 seconds, then fails readiness and returns HTTP 503 on protected routes.
- [ ] **Management API CSRF & Auth:** Often missing CSRF enforcement on state-changing REST endpoints — verify that `POST /control/v1/policies/{id}/publish` fails with HTTP 403 when the `X-CSRF-Token` header is omitted from a browser session.

---

## Recovery Strategies

Operational remediation procedures when pitfalls occur in production environments.

| Pitfall / Incident | Recovery Cost | Recovery Steps |
|---|---|---|
| **Compromised Workload Private Key** | MEDIUM | 1. Invoke `POST /control/v1/principals/{id}/quarantine` with principal `spiffe://aegis.local/workload/{name}` to block traffic across all replicas within 5 seconds via Redis.<br>2. Revoke the certificate in the workload PKI.<br>3. Issue and deploy a new key pair.<br>4. Unquarantine the principal via `DELETE /control/v1/principals/{id}/quarantine`. |
| **Audit Spool Disk Saturation (>90%)** | HIGH | 1. Gateways have automatically failed-closed (returning 503).<br>2. Verify PostgreSQL health and resolve connectivity or query lock contention.<br>3. Start auxiliary audit delivery worker processes to drain spools from mounted persistent volumes.<br>4. Once spool utilization drops below 70%, gateways automatically resume normal request admission. |
| **Corrupt or Malicious Policy Snapshot Published** | LOW | 1. Invoke `POST /control/v1/policies/{id}/rollback` to publish the last known good snapshot under a new higher monotonic version number.<br>2. Gateways receive the new version over gRPC and atomically activate it within 5 seconds.<br>3. Review operator audit logs in `admin_events` to identify the publisher identity. |
| **Redis Outage / Unavailability** | MEDIUM | 1. Gateways fail closed (503) to prevent un-revoked token bypass.<br>2. Failover to Redis HA replica or restart cluster.<br>3. If Redis state was lost, keep gateways unready until active quarantine and revocation lists are reconstructed from PostgreSQL audit logs/state before enabling traffic. |
| **Control Plane Network Partition (>60s)** | MEDIUM | 1. Gateways drop out of load-balancer pools after 60s lease expiry.<br>2. Restore control plane network connectivity or fail over to secondary control plane instance.<br>3. Gateways re-establish gRPC stream, renew leases, and resume traffic admission automatically. |
| **Backend Service Overwhelmed / Unresponsive** | LOW | 1. Gateway circuit breaker trips after configured error threshold (e.g. 50% 5xx over 10s).<br>2. Gateway returns fast HTTP 503/502 without forwarding requests to the distressed backend, allowing it to recover.<br>3. Half-open probing resumes canary traffic once backend stabilizes. |

---

## Pitfall-to-Phase Mapping

How Aegis implementation milestones must systematically prevent, detect, and verify each pitfall.

| Pitfall | Prevention Phase | Verification Strategy |
|---|---|---|
| **Path Traversal / Normalization Bypass** | Phase 1 (MVP) | Automated security test suite in `tests/security/` executing fuzzing vectors (`%2e%2e`, `%2f`, Unicode, NUL) against the gateway; assert 400 Bad Request. |
| **Header Spoofing / Forwarding Leakage** | Phase 1 & Phase 2 | Phase 1 test suite verifies stripping of inbound `X-Aegis-*` and hop-by-hop headers. Phase 2 verifies backend middleware rejects requests missing valid signed assertion JWT. |
| **Synchronous Dependency Fail-Open** | Phase 1 & Phase 3 | Phase 1 asserts default-deny Rego rule matching. Phase 3 failure test shuts down Redis during active load; assert 100% of protected requests return 503 and zero reach backends. |
| **Workload Identity Header Trust** | Phase 2 (Workload Identity) | Integration test exercises `orders` workload mTLS call to `/api/payments`. Test attempts direct connection from unauthorized workload and verifies TLS handshake failure. |
| **Backend Direct Bypass** | Phase 2 (Bypass Prevention) | Security test runs a compromised test container on the Docker network attempting direct HTTP calls to private backend ports; assert connection refusal / failure. |
| **Audit Loss on DB Outage** | Phase 3 (Durable State) | Chaos test stops PostgreSQL while running sustained traffic. Verify local spool accepts records until capacity; verify delivery worker drains and deduplicates once DB returns. |
| **Split-Brain / Stale Policy Execution** | Phase 3 & Phase 5 | Phase 3 tests gRPC lease expiration at 60s (assert 503). Phase 5 load test validates version convergence across 3 replicas under load within 5 seconds at p99. |
| **Goroutine / Memory / Connection Leaks** | Phase 1 & Phase 5 | Phase 1 configures strict HTTP server timeouts. Phase 5 executes a 10-minute sustained k6 load test and 1-hour soak test with Go pprof goroutine/heap profiling. |
| **Redis Cardinality Explosion** | Phase 3 (Rate Limiting) | Rate limit tests flood unauthenticated synthetic IPs; verify keys have fixed TTLs and global ingress concurrency limits throttle traffic before Redis write. |
| **Clock Skew Invalidation** | Phase 1 & Phase 2 | Unit tests evaluate tokens with $\pm 25$-second clock offset; assert acceptance. Tokens with $\pm 35$-second offset assert rejection. |
| **Management API CSRF & CORS** | Phase 4 (Operator Experience) | Security tests execute cross-origin `POST /control/v1/...` requests without valid `X-CSRF-Token` and assert 403 Forbidden. |

---

## Sources

- **NIST SP 800-207**: *Zero Trust Architecture* (Section 3.1: Logical components and PEP/PDP enforcement; Section 3.3: Threat model assumptions) — https://csrc.nist.gov/pubs/sp/800/207/final
- **RFC 8725**: *JSON Web Token Best Current Practices* (Algorithm confusion, `jku`/`x5u` injection prevention, clock skew recommendations) — https://www.rfc-editor.org/rfc/rfc8725.html
- **RFC 7230 / RFC 9112**: *HTTP/1.1 Message Syntax and Routing* (Section 6.1: Connection-specific hop-by-hop headers and stripping requirements) — https://www.rfc-editor.org/rfc/rfc9112.html
- **Go ReverseProxy Documentation**: `net/http/httputil` API reference and Go 1.20 `Rewrite` security rationale — https://pkg.go.dev/net/http/httputil#ReverseProxy
- **Open Policy Agent (OPA) Documentation**: *Go SDK Integration and Performance Best Practices* (`PrepareForEval` concurrency patterns) — https://www.openpolicyagent.org/docs/latest/integration/#integrating-with-go
- **SPIFFE / SPIRE Specification**: *Workload Identity and X.509 SVID Validation* — https://spiffe.io/docs/latest/spiffe-about/overview/
- **CWE-22 / CWE-444**: *Improper Limitation of a Pathname to a Restricted Directory ('Path Traversal')* and *Inconsistent Interpretation of HTTP Requests ('HTTP Request Smuggling')*
- **Aegis Architectural Specification**: `spec.md` (Version 1.0, 2026-10-06)

---
*Pitfalls research for: Distributed Zero-Trust Access Gateway & Reverse Proxy (Aegis)*
*Researched: 2026-10-06*
