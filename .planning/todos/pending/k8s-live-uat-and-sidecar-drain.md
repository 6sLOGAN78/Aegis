---
title: Live Kubernetes UAT and native-sidecar audit worker
created: 2026-11-18
source: Phase 13 review (WR-01, WR-02) and 11/12/13 HUMAN-UAT skipped items (D-19)
severity: medium
---

## Problem

Kubernetes manifests are proven only by static tests. Never run live:
- bring-up with an operator-chosen AEGIS_DB_PASSWORD (Phase 13 N2 fix),
- gateway StatefulSet drain and rollout (Phase 12 DIST-01, Phase 13 W3),
- audit-worker sidecar reaching postgres through the NetworkPolicy (N1).

The audit-worker sidecar uses a fixed `sleep 55` preStop (WR-01): every pod takes at least 55s
to terminate, and a 3-replica rollout takes about 3 minutes. The budget has zero margin (WR-02):
some gateway shutdown steps are not counted, and on scale-down undelivered WAL on an orphaned PVC
is stranded.

## Fix direction

Make audit-worker a native sidecar (initContainers with restartPolicy: Always, K8s >= 1.29) so
it stops after the gateway with no sleep; drain stranded PVCs on scale-down (or document it);
then run 11-, 12- and 13-HUMAN-UAT live on a CNI-enforcing cluster.
