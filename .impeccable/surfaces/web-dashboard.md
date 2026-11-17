---
version: 1
slug: "web-dashboard"
primary_target: "web/dashboard"
related_targets: []
---

# Surface: Aegis operator console (web/dashboard)

Mode: Operate. Audience: on-call security/SRE operator; secondary platform engineer. Job: know within seconds whether enforcement is healthy and on the right policy, see who is being denied, act (quarantine, revoke, publish, roll back) safely. Constraints: self-hosted fonts only (CSP), embedded in Go binary, live data only, no third-party branding, WCAG 2.2 AA. Must avoid: marketing-site look, hacker-movie look, generic admin template, cramped density.

Research basis: SOC dashboards lead with data freshness, then change, then action; ISA-101 high-performance HMI keeps a neutral base and spends colour only on abnormal state, coded redundantly with shape and text; tables at 40-48px rows.

## Direction contract

THESIS: The fleet read like a transit line. Requests run clients → load balancer → gateways → services, and every stop states plainly whether it is in good service. Refuses the category default of a grey sidebar, stat cards and one blue accent.

OWN-WORLD: Signage black (#111214) sign band carries brand and section nav with a white underline for the current section; white ground, cool grey panel (#F3F4F5) and hairline #D5D7DA rules, 4px radius. Colour is quarantined: round route bullets per service (orders blue #0039A6, payments brown #996633, admin purple #B933AD) and abnormal state only (red #C8102E denied/critical, amber degraded), always with a word and a shape. Neo-grotesk bold headings, tabular numerals; IDs and reason codes in mono.

STORY: The operator sees freshness, then four status tiles, then the line diagram of the live path, then the latest denials, and jumps straight to the section that needs action.

FIRST VIEWPORT: Sign band across the top. Below: "Updated Ns ago" freshness line, Overview title, a four-cell status strip (Gateways n of n + status word, Live policy vN + publisher/time, Denials last hour + hourly history bars, Quarantined principals). Then the line diagram panel full width: Clients → HAProxy → three gateway stations (name, snapshot version, heartbeat age, status) → service bullets. Then a recent-denials table. Primary actions live on their own pages; Overview links to them.

FORM: Transit signage and service-diagram system, position 4 of 7 on the grounded list; seed: a4a50aad6082ba26. Raises: colour quarantine (from lexicon plate), tabular column-aligned numerics (from datamatics), length encodes an exact measure for heartbeat/latency (from dance score).

Signature interaction: the line diagram is live; a gateway whose heartbeat is late or snapshot is behind changes its station to an open ring with an amber/red status word, and the segment to it turns dashed. Motion: 150-200ms state transitions only, none on load.

FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, DESIGN.md, and every shipping raster carrying its provenance
