# ADR-0001: Go Standard Library Reverse Proxy with Strict Path Validation and Rewrite Hook

## Status
Accepted

## Context and Problem Statement
Aegis functions as a distributed zero-trust access gateway that terminates ingress traffic from external users and automated workloads, enforces identity and policy authorization, sanitizes headers, and dispatches authorized requests to private backend microservices over mutual TLS (mTLS).

Traditional HTTP reverse proxies often attempt to "repair" or normalize malformed request paths (e.g. cleaning `..` dot segments, resolving `%2F` encoded slashes, collapsing `//` consecutive slashes). When the proxy and upstream microservices evaluate or clean paths differently, severe path traversal and authorization bypass vulnerabilities emerge (CWE-22, CWE-444). Furthermore, older reverse proxy architectures frequently leak hop-by-hop or client-supplied forwarding headers (`X-Forwarded-*`, `X-Aegis-*`), enabling identity spoofing.

Aegis requires an edge HTTP reverse proxy implementation that enforces strict zero-repair path rejection, decouples inbound from outbound request representations, strips untrusted headers unconditionally, and adds minimal latency overhead (<20ms added p99 latency budget).

## Decision Drivers
1. **Security Perimeter Integrity**: Prevent path traversal, HTTP request smuggling, and header spoofing vulnerabilities (CWE-22, CWE-444, RFC 9110/9112).
2. **Path Canonicalization Consistency**: Guarantee that the Policy Decision Point (embedded OPA) and the upstream dispatch receive the exact identical, validated byte sequence for paths and methods (Invariant 7).
3. **Decoupled Request Pipeline**: Ensure modifications to outbound proxy requests do not inadvertently pollute or leak inbound client state.
4. **Latency and Resource Efficiency**: Maintain minimal proxy overhead (<20ms added p99 latency budget) with zero third-party CGO or proxy daemon dependencies.
5. **Simplicity and Auditability**: Pure Go implementation using maintained standard library networking primitives.

## Considered Options
* **Option A**: Standard Go `net/http` and `httputil.ReverseProxy` utilizing modern Go 1.20+ `Rewrite` hook
* **Option B**: Envoy Proxy with custom C++ / Wasm filter extensions or external `ext_authz` gRPC sidecars
* **Option C**: Go `httputil.ReverseProxy` using the legacy `Director` hook

## Decision Outcome
Chosen option: **Option A — Standard Go `net/http` and `httputil.ReverseProxy` with Go 1.20+ `Rewrite` hook**.

### Rationale and Architectural Implementation
1. **`Rewrite` Hook Decoupling**: Go 1.20 introduced `httputil.ProxyRequest` and the `Rewrite` function (`func(pr *httputil.ProxyRequest)`), which cleanly decouples the client inbound request (`pr.In`) from the outbound forwarding request (`pr.Out`). `Rewrite` automatically removes hop-by-hop headers and prevents path desynchronization between `URL.Path` and `URL.RawPath`.
2. **Strict Zero-Repair Path Validation**: Aegis explicitly rejects path traversal patterns before policy evaluation or proxy forwarding. Any request containing:
   - Dot segments (`..` or `/.`)
   - Encoded path separators (`%2f`, `%2F`, `%5c`, `%5C`)
   - Duplicate slashes (`//`)
   - NUL bytes (`%00`)
   - Unrecognized URL encoding sequences
   is immediately rejected with **HTTP 400 Bad Request**. No `path.Clean()` or path repair is performed on ingress paths. The exact validated byte path is forwarded to upstream services.
3. **Ingress Header Scrubbing**: The proxy unconditionally strips all incoming client-supplied `X-Aegis-*` headers, `Forwarded`, and `X-Forwarded-*` headers upon ingress arrival. Gateway-minted assertion tokens (`X-Aegis-Assertion`) and context headers are newly constructed by the gateway after successful authorization.
4. **No Route Ambiguity**: Dynamic routing matches explicit configured paths from monotonic configuration snapshots. Requests that do not match a declared route fail closed with **HTTP 404 Not Found** or **HTTP 403 Forbidden**. External `Host` or `X-Forwarded-Host` headers never influence upstream dispatch.

### Pros and Cons of the Options

#### Option A: Go `net/http` + `Rewrite` Hook (Chosen)
* **Good**: Native Go runtime, zero external runtime binaries, zero CGO dependencies.
* **Good**: Modern `Rewrite` hook prevents hop-by-hop header leaks and isolates `pr.In` from `pr.Out`.
* **Good**: Extremely low memory footprint and predictable garbage collection behavior.
* **Good**: Full programmatic control over path rejection, WAL spooling, and token assertions.
* **Bad**: HTTP/3 (QUIC) requires additional experimental packages if needed in future phases.

#### Option B: Envoy Proxy with Custom Filters
* **Good**: High-throughput C++ data plane with extensive enterprise L7 routing features.
* **Bad**: External `ext_authz` sidecar communication adds 2–10ms network round-trip per request, violating the <2ms p99 policy SLA.
* **Bad**: Substantial operational complexity, large Docker image footprint, and high barrier to automated testing.
* **Bad**: C++ / Wasm filter toolchains complicate zero-trust verification and local reproducibility.

#### Option C: Go `httputil.ReverseProxy` with Legacy `Director` Hook
* **Good**: Uses standard library.
* **Bad**: Deprecated in Go 1.20+. Fails to sanitize hop-by-hop headers, does not isolate inbound request headers from outbound modifications, and suffers from subtle path divergence bugs between `URL.Path` and `URL.RawPath`.
* **Bad**: Vulnerable to header injection and path desynchronization attacks.

## Invariant Mapping
This decision directly enforces the following non-negotiable security invariants from `spec.md` §3:
* **Invariant 6 (Route identifies upstream)**: Configured routes match strictly declared upstream targets; client-supplied headers (e.g. `Host`) never determine upstream dispatch.
* **Invariant 7 (Identical validated path/method)**: The Policy Decision Point (OPA) and the upstream HTTP request evaluate and receive the exact same validated canonical path bytes and HTTP method.
* **Invariant 8 (No header impersonation)**: All external `X-Aegis-*` and forwarding headers are stripped on ingress; gateway-minted assertion headers cannot be spoofed.
