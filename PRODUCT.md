# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

Primary: the on-call security or SRE operator. They open the console during an incident or a routine check, need to know within seconds whether the gateway fleet is healthy and enforcing the current policy, read the audit trail to see what was allowed or denied, and cut off a compromised principal or token fast. Secondary: platform engineers who write, simulate and publish Rego policy; reviewers evaluating Aegis as a reference system from screenshots and a demo.

## Product Purpose

Aegis is a distributed zero-trust access gateway. The operator console (`web/dashboard`, served by the control plane at `/dashboard/`) is the human control surface for it: author and publish signed policy snapshots, simulate decisions, watch gateway replicas converge on a snapshot version, investigate the audit log, and quarantine principals or revoke tokens across every replica in under five seconds. Success is an operator who can answer "is enforcement healthy, and who is being denied" without hunting, and act on it safely.

## Positioning

Every decision is fail-closed and provable: snapshots are Ed25519-signed and versioned, each gateway reports which version it enforces, every permitted request has a durable audit record written before forwarding, and revocation survives losing Redis. The console shows that evidence directly (versions, reason codes, record types), not abstractions over it.

## Operating Context

Used on a laptop or desktop browser, often during an incident and sometimes screen-shared. Sessions are short and task-driven. Signed in with a control-plane account; roles are sec-ops (full) and viewer. Pages poll the `/control/v1` REST API. Mutating actions (publish, rollback, quarantine, revoke) are CSRF-protected, role-gated and audited, with typed confirmation for destructive ones.

## Capabilities and Constraints

- Sections: Overview (new landing page: fleet health, live policy version, recent denials, active quarantines), Policy (Monaco Rego editor, validate, publish, version history and rollback), Simulator, Gateways, Audit log (decision and completion records, filters, detail drawer), Quarantine (principal quarantine, token revocation).
- Data available: `/control/v1/gateways`, `/policies/{id}/versions`, `/audit-events` (filters: decision, service, principal, time range), `/principals/quarantine`, `/policies/simulate`.
- CSP: `font-src 'self'`; fonts must be self-hosted. Built assets are embedded into the Go control-plane binary.
- Stack: React 19, TypeScript, Tailwind CSS v4, TanStack Query, lucide-react, Monaco.
- Terminology: principal, workload, snapshot (vN), reason code, decision record, completion record, quarantine, revocation (jti).

## Brand Commitments

Name: Aegis. No third-party product names, logos or branding anywhere in the product (the previous style reference was borrowed from another company's marketing site and is being replaced).

## Evidence on Hand

Live data from the local distributed compose stack (3 gateways, real audit events, policy versions v1..v11). No customers, benchmarks beyond `make bench`, or testimonials exist; do not invent them.

## Product Principles

1. State first: the console answers "is it healthy and enforcing the right policy" before anything else.
2. Show the evidence: versions, reason codes, record types and timestamps are the content, not decoration.
3. Dangerous actions are deliberate: quarantine, revoke, publish and rollback are clear, confirmed and never one stray click.
4. Calm under pressure: readable at a glance during an incident; neither a marketing page nor a hacker-movie prop.

## Accessibility & Inclusion

WCAG 2.2 AA: keyboard operable, visible focus, status never conveyed by color alone, sufficient contrast, works at phone width.
