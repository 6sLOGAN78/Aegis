---
name: Aegis Operator Console
description: Transit-signage console for a zero-trust gateway fleet; the request path read like a service diagram.
colors:
  signage-black: "#111214"
  ground-white: "#ffffff"
  platform-grey: "#f3f4f5"
  hairline: "#d5d7da"
  control-rule: "#8a9099"
  text-strong: "#2a2c30"
  text-muted: "#4b4f56"
  text-quiet: "#626771"
  route-orders-blue: "#0039a6"
  route-payments-brown: "#996633"
  route-admin-purple: "#b933ad"
  state-ok-green: "#1e7b34"
  state-warn-amber: "#a14b06"
  state-bad-red: "#c8102e"
typography:
  headline:
    fontFamily: "Inter, ui-sans-serif, system-ui, sans-serif"
    fontSize: "28px"
    fontWeight: 700
    lineHeight: 1.15
    letterSpacing: "-0.02em"
  title:
    fontFamily: "Inter, ui-sans-serif, system-ui, sans-serif"
    fontSize: "14px"
    fontWeight: 600
    lineHeight: 1.4
  figure:
    fontFamily: "Inter, ui-sans-serif, system-ui, sans-serif"
    fontSize: "28px"
    fontWeight: 700
    lineHeight: 1.15
    fontFeature: "tnum"
  body:
    fontFamily: "Inter, ui-sans-serif, system-ui, sans-serif"
    fontSize: "14px"
    fontWeight: 400
    lineHeight: 1.45
  label:
    fontFamily: "Inter, ui-sans-serif, system-ui, sans-serif"
    fontSize: "12px"
    fontWeight: 600
    lineHeight: 1.4
  data-mono:
    fontFamily: "JetBrains Mono, ui-monospace, Menlo, Consolas, monospace"
    fontSize: "13px"
    fontWeight: 400
    lineHeight: 1.4
rounded:
  sm: "2px"
  md: "4px"
  bullet: "9999px"
spacing:
  xs: "4px"
  sm: "8px"
  md: "16px"
  lg: "20px"
  xl: "24px"
  page-gutter: "24px"
components:
  sign-band:
    backgroundColor: "{colors.signage-black}"
    textColor: "{colors.ground-white}"
    height: "56px"
  button-primary:
    backgroundColor: "{colors.signage-black}"
    textColor: "{colors.ground-white}"
    rounded: "{rounded.md}"
    height: "36px"
    padding: "0 14px"
  button-secondary:
    backgroundColor: "{colors.ground-white}"
    textColor: "{colors.signage-black}"
    rounded: "{rounded.md}"
    height: "36px"
    padding: "0 14px"
  button-danger:
    backgroundColor: "{colors.state-bad-red}"
    textColor: "{colors.ground-white}"
    rounded: "{rounded.md}"
    height: "36px"
  field:
    backgroundColor: "{colors.ground-white}"
    textColor: "{colors.signage-black}"
    rounded: "{rounded.md}"
    height: "38px"
    padding: "0 12px"
  panel:
    backgroundColor: "{colors.ground-white}"
    rounded: "{rounded.md}"
  route-bullet-orders:
    backgroundColor: "{colors.route-orders-blue}"
    textColor: "{colors.ground-white}"
    rounded: "{rounded.bullet}"
    size: "22px"
  status-bad:
    backgroundColor: "#fce8eb"
    textColor: "#8e0b20"
    rounded: "{rounded.sm}"
    height: "24px"
---

# Design System: Aegis Operator Console

## Overview

**Creative North Star: "The Line Diagram"**

The console reads the gateway fleet the way a passenger reads a transit system. A black sign band carries the name and the sections; below it the ground is white and quiet. The Overview draws the live request path as a line diagram: clients, the load balancer, each gateway as a station showing its snapshot version and heartbeat, and each backend service as a coloured route bullet. Every stop states its condition in plain words: Good service, Delayed, Suspended.

The system is built for an on-call operator who needs to know within seconds whether enforcement is healthy and on the right policy. It follows the high-performance HMI principle (ISA-101): a neutral base, colour spent only where something is abnormal, and every state carried by a word and a shape as well as a hue. It is neither a marketing page (no airy cards, no whisper-weight headlines, no beige canvas) nor a hacker-movie prop (no neon on black, no glow), nor a stock admin template.

**Key Characteristics:**
- Black sign band header with white type and a white underline on the current section.
- White ground, hairline rules, 4px corners, no shadows on anything that sits in the page.
- Round lettered route bullets per service (O orders, P payments, A admin), the only decorative colour.
- State colour only for abnormal conditions, always paired with a status word and a distinct mark shape.
- Bold, tight headings; tabular numerals; IDs, paths, reason codes and timestamps in mono.

## Colors

A monochrome signage system with three route colours and three state colours held in reserve.

### Primary
- **Signage Black** (#111214): sign band, headings and body text, primary buttons, table header rules, the line diagram's track.

### Secondary
- **Route Blue, orders** (#0039a6): bullet for the orders service; also the focus ring and selection tint.
- **Route Brown, payments** (#996633): bullet for the payments service.
- **Route Purple, admin** (#b933ad): bullet for the admin service.

### Tertiary (state, abnormal only)
- **Signal Red** (#c8102e): denied requests, suspended gateways, quarantine, destructive actions. Tinted ground #fce8eb with text #8e0b20 for status chips.
- **Delay Amber** (#a14b06): late heartbeats, gateways behind the live snapshot. Tinted ground #fdf0e4 with text #7a3905.
- **Service Green** (#1e7b34): only the small dot beside "Good service" and "Allowed"; the word itself stays black.

### Neutral
- **Ground White** (#ffffff): page and panels.
- **Platform Grey** (#f3f4f5): inset areas, diagram ground, hovered rows.
- **Hairline** (#d5d7da): panel borders and row dividers.
- **Control Rule** (#8a9099): input and secondary-button borders (3:1 on white).
- **Muted / Quiet text** (#4b4f56 / #626771): secondary copy, captions; both pass 4.5:1 on white.

### Named Rules
**The Quarantine Rule.** Hue lives only in route bullets and abnormal state. Chrome, navigation, buttons and headings stay black, white and grey.

**The Redundant State Rule.** No state is shown by colour alone: Good service is a round dot plus the word, Delayed is an open ring plus the word, Suspended or Denied is a square mark plus the word.

## Typography

**Body and Display Font:** Inter (self-hosted, 400/500/600/700)
**Data Font:** JetBrains Mono (self-hosted, 400/500)

**Character:** One neo-grotesk does every job, as on a sign system: bold and tight for headings and figures, regular for copy. Mono is reserved for things an operator copies or compares: principal IDs, paths, reason codes, timestamps.

### Hierarchy
- **Headline** (700, 28px, 1.15, -0.02em): page titles.
- **Figure** (700, 28px, tabular numerals): status-board values (3 of 3, v11, counts).
- **Title** (600, 14px): panel titles, section names in the sign band.
- **Body** (400, 14px, 1.45): copy and table cells; descriptions capped at 72ch.
- **Label** (600, 12px): field labels, status-board labels, table headers.
- **Data mono** (400, 12-13px): IDs, request paths, reason codes, timestamps.

### Named Rules
**The Hyphen Rule.** Tabular numerals widen Inter's hyphen. Apply `tabular-nums` only to pure numbers; set dates and hyphenated IDs in mono.

## Layout

Content sits in a 1320px max container with 16px (phone) / 24px gutters under a sticky sign band. Pages open with a bold title, a one-line description or freshness line, and right-aligned actions. The Overview reads top to bottom in the order an operator triages: freshness, four-cell status board, request-path diagram, latest denials. Below 1024px the section nav moves to a second, horizontally scrolling row of the band; below 768px the line diagram becomes a vertical line with stops listed top to bottom, and status cells stack.

## Elevation & Depth

Flat. Anything that sits in the page is separated by hairlines and the platform grey, never by shadow. Only surfaces that float above the page carry depth.

### Shadow Vocabulary
- **Overlay** (`0 16px 40px -8px rgb(17 18 20 / 0.24)`): the audit event drawer and confirmation dialogs.

## Shapes

4px corners on panels, buttons and fields; 2px on status chips; full circles only for route bullets and line-diagram stations. Table headers carry a 2px black rule, the sign system's heavy line; rows divide with hairlines.

## Components

- **Sign band:** 56px black bar; brand mark (a station on a line) and name at left, sections as 14px semibold white text at 65% opacity, current section at 100% with a 3px white underline; user and Sign out at right.
- **Status board:** one bordered panel split into four cells by hairlines; each cell is a button that opens its section; label, figure, then a status word or one-line context.
- **Line diagram:** 8px black track; stations are white discs with a 7px black ring; a delayed station is an open dashed amber ring with a dashed amber segment; a suspended station is a red square with a dashed red segment. Each gateway shows name, status word, snapshot, and a heartbeat bar whose full 120px length is the 60 s lease.
- **Route bullet:** round, lettered, white bold letter, one colour per service, always followed by the service name.
- **Buttons:** primary black, secondary white with control-rule border, danger red, ghost text; 36px (32px small); 150ms colour transitions.
- **Fields:** 38px, control-rule border, black 1px focus ring; invalid fields turn the border and ring red.
- **Tables:** 12px semibold headers over a 2px black rule; 44-48px rows; hover row in platform grey.

## Do's and Don'ts

- Do lead every operational page with whether its data is fresh.
- Do pair every state colour with a word and a mark shape.
- Do use route bullets wherever a service is named.
- Don't add colour to chrome, navigation or headings.
- Don't use shadows on in-page panels, or card radii above 4px.
- Don't use eyebrow labels, gradient text, glows or neon accents.
- Don't set hyphenated strings with tabular numerals.
