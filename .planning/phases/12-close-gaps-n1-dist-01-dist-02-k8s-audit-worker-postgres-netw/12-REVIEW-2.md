---
phase: 12-close-gaps-n1-dist-01-dist-02-k8s-audit-worker-postgres-netw
reviewed: 2026-11-16T14:56:36Z
depth: deep
scope: "git diff 27fce63 HEAD -- internal/audit/ (b01e7fb, 53f8774, c9ccf20, d06fe99, babe748)"
files_reviewed: 5
files_reviewed_list:
  - internal/audit/spool.go
  - internal/audit/worker.go
  - internal/audit/committer.go
  - internal/audit/cursor.go
  - internal/audit/event.go
findings:
  critical: 1
  warning: 2
  info: 3
  total: 6
status: issues_found
---

# Phase 12: Code Review Report, re-review 2 (audit WAL fix)

**Reviewed:** 2026-11-16T14:56:36Z
**Depth:** deep
**Files Reviewed:** 5 (diff scope plus the call chain: cursor.go, event.go, `cmd/gateway/main.go` AppendPreForward call sites, and `internal/proxy/server.go` limits)
**Status:** issues_found

## Summary

The CR-01 reader rework holds up under adversarial testing as long as the segment layout is the one the current writer produces. I wrote throwaway property tests in a scratch copy and deleted them afterwards:

- **Historical segments:** 300 random segments mixing torn prefixes, CRC-flipped frames and valid frames. Every complete valid frame was ingested exactly once.
- **Growing newest segment:** 200 runs where the newest segment grew in random chunks with a poll between each chunk. Nothing was lost, except in the one shape described in WR-01.

The provisional-resync proof ("a CRC-valid frame inside the snapshot after `badStart` proves the bad frame is finished") is sound for this writer:

- `json.Marshal` escapes 0x01, so a payload can never contain the magic.
- The only false magic possible inside a header is `CRC == 0xAE615001`.
- Even then, the false "length" would be the payload's leading `{"`, which is 0x7B22xxxx. That is greater than `MaxFramePayloadBytes`, so it is rejected before any read.

Writes are sequential under `s.mu`, so the bytes after an in-progress frame are always a prefix of that frame.

Two defects remain:

1. **Segment ordering (CR-01).** Segment names start with wall-clock time and are sorted as strings. If the clock steps back across a restart (or at a rotation), the *active* segment sorts before an older one. The worker then treats the active segment as historical and **prunes the file the gateway is still appending to**. Every acknowledged, fsynced frame written to it afterwards is lost. This is pre-existing in `rotateSegment`, but the new restart rotation now takes this path on every crash that leaves a torn tail. Reproduced.
2. **Missing newline check (WR-01).** `ReadFramedRecord` never checks the trailing `'\n'`. A torn frame cut exactly before its newline decodes, consumes the first byte of the next frame, and the resync then skips that next valid frame. Reproduced on both historical and newest segments. The current writer can no longer produce this layout; WAL segments written before the fix can.

Answers to the five questions:

1. **Can a valid, fsynced frame be lost or skipped?**
   - The following are all handled correctly: a torn frame followed by valid frames, a partial active frame that later completes, a false magic match, a fully present CRC-bad frame, a crash between rotation and the first write (an empty segment is reused or reads straight to EOF), and pruning after advance (`SaveOffset(next,0)` runs before `Remove`).
   - Loss is possible via CR-01 (clock step-back) and WR-01 (a torn frame missing only its `'\n'`).
   - CRC-bad frames are skipped permanently without being counted (WR-02).
2. **Can the worker loop forever or stall permanently?**
   - No loop inside a pass: every branch either moves `currentOffset` strictly forward (decode moves it by `bytesRead > 0`; resync lands at a position of at least `currentOffset+1`) or exits.
   - The torn-tail retry on the newest segment ends at the next append, because the writer rotates away from an untracked tail.
   - Permanent stall: **yes, under CR-01**. If the cursor already sits on the segment named with the future timestamp, the new active segment sorts before `startIdx` and is never read. The spool then fills until the 90% gate returns 503 for allowed traffic.
3. **Duplicates the `event_id` dedupe would not absorb?**
   - The fix adds none. Provisional positions are never persisted; every exit path falls back to `badStart` before `ProcessBatch` or `SaveOffset`.
   - Pre-existing gaps (IN-02): frames with an unparseable `event_id` get a fresh `uuid.NewString()` on every re-ingest, and a zero `Timestamp` becomes `now`, which changes `event_date` (part of the conflict key).
   - Gateway-written frames always set both fields, so in practice this only affects legacy or hostile frames.
4. **Does the restart rotation preserve fsync-before-forward and the ordering and naming the worker and pruner rely on?**
   - Fsync-before-forward is preserved. Nothing is truncated, and the Write+Sync path is unchanged.
   - No race with the worker reading the old newest segment:
     - `rotateSegment` creates the new file only after the last write to the old one has finished under `s.mu`, so a segment the worker lists as historical is final.
     - A segment listed as newest is read against a `Stat` snapshot. A frame still being written is seen as incomplete and retried; nothing is skipped.
   - Ordering is **not** preserved when the wall clock steps back (CR-01).
5. **Can the WR-03 1 MiB cap refuse legitimate traffic?** No. Measured worst cases:
   - A normalized event with every string field filled with `<` (each escapes to 6 bytes as `<`), 64 roles of 128 bytes and a 2048-byte path: **66,763 bytes**.
   - The un-normalized `ac == nil` AppendPreForward path, with the whole 16 KiB header budget spent on `<` in both roles and path: **196,901 bytes**.
   - Request bodies are never placed in an audit record.
   - The cap has at least 5x headroom and cannot produce a 503 on valid traffic.

## Critical Issues

### CR-01: Wall-clock segment names: when the clock steps back, the worker prunes the active segment (loss of acknowledged frames) or never reads it (permanent stall)

**File:** `internal/audit/spool.go:166` (restart rotation), `internal/audit/spool.go:597-598` (`rotateSegment` name), `internal/audit/worker.go:217`, `internal/audit/worker.go:266-279` (advance and prune), `internal/audit/spool.go:137-138` (latest by sort)

**Issue:**
- Segments are named `wal-<UnixNano wall clock>-<seq>.log`. Every component sorts them as strings, so ordering depends on the wall clock never stepping backwards: `NewDiskSpool` picks "latest" this way, and the worker uses the same order for both `isLatest` and the order it walks segments.
- Suppose the newest segment on disk carries a timestamp later than "now". This happens after an NTP step-back, a VM or RTC reset, or when a StatefulSet pod and its PV move to a node whose clock is behind. The next segment the spool creates then sorts **before** it.
- Before this fix, a restart reused the newest segment unless it was full. Now every crash that leaves a torn tail forces this rotation (`segmentEndsOnFrameBoundary` returns false), and so do the new `hasUntrackedTailLocked` rotations in `AppendPreForward`.

The consequences:
- **Cursor on an older segment** (the normal case): the worker sees `[active, oldTorn]`. It processes `active` as historical, reaches EOF, saves the cursor as `(oldTorn, 0)` and calls `os.Remove(active)`. The gateway keeps appending and fsyncing into the unlinked inode. Every acknowledged pre-forward allow, denial and completion written afterwards is lost (an AUD-03 / Invariant 10 violation). A frame still in flight on `active` is also classified as a historical torn tail.
- **Cursor already on the segment with the future timestamp:** `startIdx` points past `active`, so `active` is never read. The spool grows to the 90% gate and allowed traffic gets 503s.

**Repro (scratch test, removed):**
1. Write `wal-<now+1h>-000005.log` containing A followed by a torn frame.
2. Write an older segment and point the cursor at it.
3. Call `NewDiskSpool`, which rotates to `wal-<now>-000006.log`. That name sorts before `-000005`.
4. Call `AppendPreForward(B)`.
5. Run `ProcessAvailable`.

Result: the active segment no longer exists after the first pass. `AppendPreForward(C)` returns nil, but C is never ingested. Final directory: `[wal-<now+1h>-000005.log wal.cursor]`.

**Fix:** Make segment names strictly increasing regardless of the clock, and use one ordering everywhere. Either:
- Put the zero-padded seq first: `wal-%020d-%020d.log` with seq then ts, and sort by parsed seq. Or
- Clamp the timestamp:

```go
// in NewDiskSpool keep lastTS parsed from latest; in rotateSegment:
ts := time.Now().UTC().UnixNano()
if ts <= s.lastTS { ts = s.lastTS + 1 }
s.lastTS = ts
filename := fmt.Sprintf("wal-%020d-%06d.log", ts, seq)
```

Also add a worker guard: never `os.Remove` a segment whose seq is at or above the highest seq present. Or, more simply, compare by parsed seq instead of by string.

## Warnings

### WR-01: `ReadFramedRecord` ignores the trailing `'\n'`: a torn frame missing only its terminator swallows the next valid frame

**File:** `internal/audit/spool.go:673-690` (no check on `buf[length]`), interacting with `internal/audit/worker.go:312-344`

**Issue:**
- The reader reads `length+1` bytes and verifies the CRC over `buf[:length]`, but never checks `buf[length] == '\n'`.
- If a torn frame G is cut exactly before its newline and another frame B follows it in the same segment:
  - G's CRC and JSON are valid, so G decodes, with `bytesRead = len(G)`.
  - That consumes B's first magic byte (0xAE) as G's "newline".
  - The next read at B+1 fails the magic check, and the resync from B+2 skips all of B.
- On the newest segment, the decode of G also clears `badStart`, so the provisional-resync guard does not protect B.
- As a side effect, G (never acknowledged) is ingested.
- Live writers can no longer create this layout, because every torn tail now forces a rotation. Pre-fix WAL segments can contain it, and so can segments touched by a future writer change. This is exactly the "torn frame followed by valid frames" shape that CR-01 set out to recover.

**Repro (scratch test, removed):** A segment holding `G[:len(G)-1] + B + C`, run both as a historical and as the newest segment. Result: `ingested=2, gotB=false`. The log shows "corrupted WAL record ... at offset 467, resynced at 933", and the cursor ends past B. The randomized growing-segment property test also hit this shape (cursor saved at `frameStart+1`).

**Fix:**
```go
if buf[length] != '\n' {
    return nil, int64(FrameHeaderSize), fmt.Errorf("%w: missing frame terminator", ErrCorruptedRecord)
}
```
Place this check before the CRC check. The worker then resyncs from `frameStart+1` and finds B's magic at its true offset. Add the repro as a regression test.

### WR-02: CRC-bad and resynced-over frames are permanent audit loss but are not counted (WR-07 only covers JSON skips)

**File:** `internal/audit/worker.go:312-344`, `internal/audit/worker.go:337-339` (historical torn tail)

**Issue:**
- A frame whose bytes are all present but whose CRC is bad is discarded permanently when a valid frame follows it, or when it is the last frame of a historical segment. Bit rot on a fully written, fsynced frame produces exactly this.
- The same applies to any bytes the resync jumps over.
- Only `log.Printf` records it. `countSkip` and `aegis_audit_worker_skipped_frames_total` fire only for `ErrInvalidRecordJSON`.
- Repro: a segment of `[A with one payload bit flipped][B]` gives `ingested=1, SkippedFrames()=0`.
- The original WR-07 concern ("permanent audit loss not observable") therefore still holds for the more likely corruption class.

**Fix:** When a provisional resync is committed (that is, `badStart >= 0` is cleared by a successful decode or a JSON skip), call `w.countSkip(fileName, badStart)`. Do the same in the historical torn-tail branch when bytes past `badStart` are being abandoned. Optionally, add a separate `reason` counter for corrupt and torn frames so that operators can tell bit rot apart from crash tails.

## Info

### IN-01: Fsync failure, or a post-durable rotation failure, in AppendPreForward refuses the request while its "allow" frame may still be ingested

**File:** `internal/audit/spool.go:396-414`

**Issue:**
- If `Write` succeeds and `Sync` fails, the full frame is already in the page cache. `currentSize` is not advanced, so the next append rotates away. The worker can still ingest the frame, which records an `allow` row for a request that was answered with 503 `AUDIT_SPOOL_WRITE_ERROR`.
- Step 7 has a similar problem: when the rotation after a durable append fails, `AppendPreForward` returns an error and refuses a request whose record is already durable. `WriteFrames` deliberately treats the same situation as success.
- Both issues are pre-existing.

**Fix:** In step 7, log the failure and return nil, mirroring `WriteFrames`. For the Sync-failure case, document that a phantom allow row can appear, or let the worker reconcile it against the denial row with the same `request_id`.

### IN-02: Re-ingest duplicates that ON CONFLICT cannot absorb (legacy or hostile frames)

**File:** `internal/audit/worker.go:104-118`

**Issue:**
- An unparseable `event_id` is replaced by `uuid.NewString()` on every read.
- A zero `Timestamp` becomes `time.Now()`, which changes `event_date`.
- Any re-read of such a frame inserts a new row: after a cursor-save failure following commit, after a restart before checkpoint, or via the WR-01 shape.
- Gateway-written frames always carry both fields, so the impact is limited to legacy or hostile frames.

**Fix:** Derive a deterministic fallback ID, for example UUIDv5 over (segment, offset) or over the payload CRC. Derive a fallback date from the segment's name timestamp instead of `now`.

### IN-03: Corrupt-record and resync log lines repeat on every 200 ms pass while a provisional resync is pending

**File:** `internal/audit/worker.go:343`

**Issue:**
- While the newest segment has a torn frame followed by an in-progress frame, each pass logs the same "corrupted WAL record ... resynced at" line again. The property test produced 3 to 6 identical lines per offset.
- This is the same class as the still-open IN-04 (torn-tail retry log not rate-limited).

**Fix:** Log once per (segment, badStart), reusing the `countSkip` high-water idea, or rate-limit the message.

---

_Reviewed: 2026-11-16T14:56:36Z_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: deep_
