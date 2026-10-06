# ADR-0003: Physical Dual-Listener Identity Separation & Backend Assertions

## Status
Accepted

## Context and Problem Statement
Aegis must securely authenticate two fundamentally distinct classes of ingress traffic:
1. **Human / External API Users**: Interactive clients authenticating with OpenID Connect / OAuth2 JSON Web Tokens (Bearer JWTs) containing user IDs and assigned role claims.
2. **Automated Machine Workloads**: Microservices and batch workers authenticating with mutual TLS (mTLS) client certificates bearing cryptographic SPIFFE IDs in the Subject Alternative Name (`URI:spiffe://aegis.local/ns/.../sa/...`).

Multiplexing both credential types on a single network listener port creates severe security hazards:
- **Confused Deputy & Precedence Ambiguity**: If a request supplies both a client certificate and an `Authorization: Bearer <jwt>` header, which identity takes precedence? A compromised workload with a valid certificate could forward a forged user JWT, or an external caller could exploit optional TLS client auth negotiation.
- **TLS Renegotiation & Client Cert Optionality**: In Go and standard TLS stacks, configuring `tls.VerifyClientCertIfGiven` allows unauthenticated clients to connect, requiring manual per-request application verification that easily introduces bypass bugs.
- **Direct Upstream Bypass**: If backend microservices accept raw client requests without cryptographic verification of gateway mediation, malicious containers on the internal network can bypass gateway policy enforcement entirely.

Aegis requires rigorous identity separation on ingress and cryptographic assurance on backend forwarding.

## Decision Drivers
1. **Zero-Trust Identity Clarity (NIST SP 800-207)**: Every incoming request must map unambiguously to exactly one verified principal type (user or workload) based on physical entry port.
2. **Fail-Closed Port Isolation**: Ingress listeners must enforce distinct TLS and credential constraints at the network transport layer before application request parsing.
3. **Direct Upstream Bypass Prevention**: Backends must verify that incoming requests originated from an authentic gateway instance and were authorized by the central policy engine.
4. **Credential Scope Limitation**: Raw external JWTs and client certificates must never be forwarded directly into backend services, preventing credential replay or impersonation across the backend mesh.

## Considered Options
* **Option A**: Physical dual listeners (`:8080`/`:8443` for User JWT vs `:9443` for Workload mTLS) with gateway-minted signed backend assertion tokens (`X-Aegis-Assertion`) and backend mTLS verification
* **Option B**: Single multiplexed port (`:8443`) with `tls.VerifyClientCertIfGiven`, inspecting headers to branch authentication logic dynamically
* **Option C**: Terminating TLS at an external edge load balancer and trusting forwarded headers (`X-Forwarded-Client-Cert`, `X-Forwarded-User`)

## Decision Outcome
Chosen option: **Option A — Physical dual listeners with signed backend assertions and backend mTLS**.

### Rationale and Architectural Implementation
1. **Physical Dual Ingress Listeners**:
   - **User Listener (`:8080` HTTP / `:8443` TLS)**: Configured with standard server TLS. Dedicated to user Bearer JWT tokens. It strictly rejects workload mTLS certificates and never evaluates SPIFFE identity. The `Authorization: Bearer <jwt>` token is cryptographically verified (algorithm allowlist RS256/ES256/EdDSA, issuer, audience, expiration, and Redis JTI revocation check).
   - **Workload Listener (`:9443` mTLS)**: Configured with `tls.RequireAndVerifyClientCert` using the Aegis Workload Root CA. Workload clients must present an x509 certificate with a valid SPIFFE ID in the SAN URI (`spiffe://aegis.local/...`). Bearer JWT headers on `:9443` are strictly rejected. The SPIFFE identity authenticates the workload; the embedded OPA policy authorizes the action.
2. **Gateway-to-Backend Signed Assertions (`X-Aegis-Assertion`)**:
   - Raw external user tokens and workload client certificates are **never** forwarded to upstream backends.
   - Upon successful OPA authorization, the gateway mints a short-lived, signed assertion JWT:
     - Issuer: `iss: aegis-gateway`
     - Subject: `sub: <principal-id>`
     - Audience: `aud: <target-backend-service>` (e.g. `orders-service`, `payments-service`)
     - Lifetime: `exp: <= 15 seconds` (strictly bounded)
     - Claims: `principal_kind`, `roles`, `spiffe_id`, `request_id`, `snapshot_version`
     - Signature: Ed25519 digital signature generated with the gateway's private assertion key.
   - The assertion is injected as the `X-Aegis-Assertion` header.
3. **Backend Workload Verification**:
   - Backends listen exclusively on internal mTLS interfaces with zero published host ports.
   - Backends verify two distinct security layers:
     1. Transport layer: The connecting client certificate belongs to the Aegis Gateway SPIFFE identity (`spiffe://aegis.local/ns/gateway/sa/aegis-gateway`).
     2. Application layer: The `X-Aegis-Assertion` header is verified against the Gateway Assertion Public Key, ensuring `aud` matches the backend service and the token is unexpired.
   - Requests arriving without valid gateway mTLS or valid assertion tokens are rejected with **HTTP 401 Unauthorized** or **HTTP 403 Forbidden**.

### Pros and Cons of the Options

#### Option A: Physical Dual Listeners + Signed Assertions (Chosen)
* **Good**: Eliminates confused deputy risk at the socket layer; impossible for user requests to spoof workload identity or vice versa.
* **Good**: Workload listener enforces mandatory TLS client verification (`tls.RequireAndVerifyClientCert`) in Go stdlib without permissive fallback.
* **Good**: Short-lived (15s) audience-bound assertion tokens prevent token theft and replay across microservices.
* **Good**: Completely eliminates direct bypass: backends reject any connection not mediated by the gateway.
* **Bad**: Requires managing two listening ports and provisioning workload certificates with SPIFFE SAN extensions.

#### Option B: Single Multiplexed Port with Dynamic Branching
* **Good**: Conserves one listening port.
* **Bad**: Requires `tls.VerifyClientCertIfGiven`, which leaves the transport layer open to unauthenticated connections.
* **Bad**: High risk of credential confusion when requests include both client certificates and JWT headers.
* **Bad**: Complex application routing logic needed to deduce intent before authentication.

#### Option C: Trusting Forwarded Headers from External Load Balancer
* **Good**: Simpler local application code.
* **Bad**: Catastrophic security failure mode: trusting `X-Forwarded-*` headers allows any network caller capable of reaching the gateway to forge identities trivially.
* **Bad**: Violates NIST SP 800-207 and Invariant 8.

## Invariant Mapping
This decision directly enforces the following non-negotiable security invariants from `spec.md` §3:
* **Invariant 2 (Verified identity only)**: Identity is extracted exclusively from cryptographically verified JWT tokens (on user listener) or verified client certificate SPIFFE SANs (on workload listener).
* **Invariant 3 (Workload cert authenticates, policy authorizes)**: Workload certificates prove workload identity; policy engine explicitly evaluates permissions. Possession of a valid certificate never grants implicit authorization.
* **Invariant 5 (Backends accept only gateway client identity + assertion)**: Backends enforce mTLS and require short-lived, audience-bound `X-Aegis-Assertion` tokens minted by the gateway.
* **Invariant 12 (Network location is never authorization)**: Network proximity (e.g. sharing a Docker bridge or private subnet) confers zero access; cryptographic authentication and assertions are mandatory.
