---
title: Bound request-body read time on gateway listeners (slow-body clients)
created: 2026-11-17
source: automated security review of quick task 261117-rvo (GW-01)
severity: medium
---

## Problem

The :8080 and :9443 servers in `internal/proxy/server.go` set `ReadHeaderTimeout`
but no `ReadTimeout`, and handlers do not set a per-request body read deadline.
A client that sends headers and then trickles (or stalls) a chunked body keeps
a `MaxConcurrentRequests` slot while the reverse proxy streams that body
upstream. A few hundred such clients exhaust the gateway's concurrency.

Quick task 261117-rvo bounded only the post-reply drain it introduced
(`bodyDrainTimeout`, 5s, fails closed with 408; commit 03491f5).

## Fix direction

Set a body read deadline per request with `http.NewResponseController(w).SetReadDeadline`
(configurable, for example `AEGIS_BODY_READ_TIMEOUT`, default 30s) on both ingress
handlers, or a server `ReadTimeout` if no endpoint needs long uploads. Add a test
with a stalled chunked client and a backend that reads the body.
