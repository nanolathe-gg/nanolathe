# Spec conflicts observed against the reference install

`research/retail-executable-spec` is the behavior authority, but it was derived
from static analysis of one executable and a few of its statements are
contradicted by a real, working retail install. This file records those, with the
evidence, so nobody "fixes" the code back toward the spec later.

It also records the rarer case where two research documents contradict **each
other** (SC6). Those are not install measurements, but this is where an
implementer looks.

Reference install: `~/TotalAnnihilation` — base + Core Contingency + Battle
Tactics + patch 3.1. Probed through this repo's `vfs` package.

Every entry carries a **Status** line directly under its heading, added
2026-08-28 by RWU-00-5: `closed by <citation> (date)` when a later finding or
census settled it, or `open — decider: …` when it has not been settled. A
status line never changes an entry's Decision; where a closure implies work in
the code, the status names that action. There are no SC11–SC13 entries; the
numbering has always skipped them.

No entry gates work. **SC16** closed 2026-09-01: its `0x20` active-state half
went with WU-19-37, when the flag-word collision it recorded turned out no
longer to exist, and its empty-current-task half with WU-19-45, when
`[07 R-WGT-01 §10]` identified the compared word as the remaining-build
fraction and the order-guard float this build had invented was removed.
**SC7** closed 2026-08-29 (`[02 R-SND-01 §1]`). **SC5** is closed but its
divergence is still in the code.

---

## SC1 — There is no ten-archive cap we can honor

**Status:** closed by `[02 §2]`'s per-pass mount budget (2026-08-26). The cap is real but per-invocation, so mounting every local HPI reproduces the converged retail state.

**Spec** `[02 §2]`: "Local `*.HPI` with flag 0, up to ten successfully mounted
archives (the eleventh successful local HPI is not mounted)."

**Observed:** the install has **13** local HPI archives — `tactics1..8.hpi`,
`totala1..4.hpi`, `worlds.hpi` — plus five `.ccx`, eleven `.ufo`, and one
`.gp3`, and it plays. A ten-archive cap must drop three of them. In our lexical
tier order it would drop `totala3.hpi`, `totala4.hpi` (152 `camps/` files and
100 maps) and `worlds.hpi` (837 map `sections/`), which would remove the
campaign and most maps. Under any other enumeration order it drops a different,
equally load-bearing three.

Any install with base + both expansions + a handful of downloadable units
exceeds ten. The cap as stated cannot be a general truth about how retail
resolves content.

**Decision:** mount every local HPI. If more than ten are present, emit one
diagnostic naming them, so the discrepancy stays visible. The spec's cap is
recorded here rather than implemented.

**Update (2026-08-26):** the conflict is resolved by decompilation. The mount
loop carries a ten-valued budget that decrements only when a candidate is
*newly mounted* (validation passed and the full path not already mounted) and
abandons the enumeration when the budget hits zero; the budget is
per-invocation — the mount orchestrator runs at several call sites, each
restarting the budget, and already-mounted archives never consume it, so
repeated invocations converge to every valid local HPI mounted. The cap is
real but per-pass, not a global limit; mounting every local HPI reproduces the
converged retail state. See [02 §2] and the mount-orchestrator note in the
raw corpus.

**Falsifies:** the earlier PLAN_01 contract C2. Do not reintroduce it.

---

## SC2 — `GAMEDATA.TDF` does not exist

**Status:** closed by asset census of the reference install (2026-08-26). `[02 §1]`'s hard requirement reads as the `gamedata/` directory, not a file of that name. Mechanism settled by `[02 R-MALF-01 §5]` (2026-08-29): no `gamedata.tdf` is ever opened; the `Can't load GAMEDATA.TDF` box is raised by a missing `SIDEDATA.TDF` — the message text is simply misnamed.

**Spec** `[02 §1]`: "The known hard requirements include `MOVEINFO.TDF` and
`GAMEDATA.TDF`."

**Observed:** no file named `gamedata.tdf` exists anywhere in the 31 mounted
providers. `gamedata/` contains exactly 13 files: `allsound.tdf`,
`buildinfo.tdf`, `category.tdf`, `help.tdf`, `los.tdf`, `meteor.tdf`,
`moveinfo.tdf`, `sidedata.tdf`, `sound.tdf`, `translate.tdf`, `unitview.tdf`,
`version.tdf`, `weapons.tdf`.

The most likely reading is that the spec means the `gamedata\` **directory**,
whose absence is indeed fatal.

**Decision:** `MOVEINFO.TDF` and `SIDEDATA.TDF` missing are fatal; a missing
`gamedata/` directory is fatal; a missing `GAMEDATA.TDF` is **not**, because
making it fatal would refuse to boot on a genuine retail install.

---

## SC3 — Intra-tier ordering barely matters, and we can measure it

**Status:** closed — the divergence is measured and accepted; `[02 §2]` says retail's intra-tier order is the host's, not the executable's, so there is nothing to reproduce.

Retail resolves same-tier archives in `FindFirstFileA` order, which `[02 §2]`
itself calls "not a portable executable-defined order". We sort lexically inside
a tier (PLAN_01 divergence D1).

**Observed:** 323 logical paths exist in more than one same-tier provider —
231 `ccdata.ccx` vs `btdata.ccx`, 85 `totala2.hpi` vs `totala1.hpi`, 7 others.
Of those 323, **only 2 differ in size**: `anims/armhp1.gaf` (75,272 vs 68,136
bytes, ccdata vs btdata) and `installres/install.inf` (not game content).

So the divergence has essentially one observable consequence in the whole
install, and the manifest names the winner. Acceptable; keep the deterministic
lexical rule.

---

## SC4 — `vfs.EntryInfo.Name` is a base name, not a path

**Status:** closed — a repo API note, not a spec conflict; kept here because it is the same class of trap.

Not a spec conflict but the same class of trap. `EntryInfo.Path` is the logical
path; `EntryInfo.Name` is the base name; `FS.Entries()` returns one entry **per
mount** and is not deduplicated by logical path. Code that groups by `Name`, or
that treats `Entries()` as the unique file set, silently produces nonsense (a
first pass at the probe above "found" 8,246 unique paths and top-level
directories named `battleroom.pcx`).

`FS.Manifest()` (PLAN_01 WU-01-2) is the deduplicated view; use it.

---

## Reproducing these measurements

Every number above came from a throwaway program against the real install. To
rerun (adjust for whatever the `vfs` API looks like by then):

```go
fs := vfs.New()
fs.MountGameDirectory(filepath.Join(os.Getenv("HOME"), "TotalAnnihilation"))
fmt.Println("mounts:", fs.MountCount())
for _, e := range fs.Entries() {          // one record PER MOUNT, see SC4
    if e.IsDir { continue }
    if srcs := fs.Sources(e.Path); len(srcs) > 1 {
        // srcs[0] is the winner; compare Source.Priority for same-tier peers
    }
}
```

Keep such programs in a scratch directory, not in the repo — the reusable form
is PLAN_01's `WU-01-5` coverage test.

---

## SC5 — The three movement clamps cannot run unconditionally

**Status:** closed by `[04 §6.1 R-DOC04-A]` (2026-08-27). One action outstanding: delete the gated-clamp divergence in `internal/content` and initialize the profile from the startup template.

**Spec** `[02 "Movement class record"]`: eight keys are read in order, then
"Three clamps then run, in order: if `maxwaterslope` is below `maxslope`,
`maxslope` becomes `maxwaterslope`; …". Keys 3, 4, 5 and 7 default to "the
profile's current value".

**Observed:** 12 of the install's 15 `CLASS` sections omit `maxwaterslope`
entirely. Compiling them with a zero default and running clamp 1
unconditionally sets `maxslope = 0` for `kbotsf2`, `kbotss2`, `tankbh3`,
`tankds2`, `tanksh2` and `tanksh3` — every land movement class that authored a
real slope limit. No land unit could climb anything.

Measured with `content.CompileMovementSorted` against the reference install:

```
kbotss2   maxslope=32 badslope=16 maxwslope=0    <- would clamp to maxslope=0
tankdh3   maxslope=15 badslope=7  maxwslope=30   <- authored, clamps correctly
tankhover3 maxslope=12 badslope=12 maxwslope=255 <- authored
```

**Decision:** clamps 1 and 3 are gated on whether `maxwaterslope` was authored,
using the string accessor to tell an absent key from an authored zero.

This is a stand-in for a different unknown, not a reading of the spec. The
defaults chain off "the profile's current value", and we initialize that value
to zero. If a fresh profile actually carries a large `maxwaterslope`, all three
clamps run unconditionally and reproduce retail with no branch — which is the
shape to aim for. The open question is recorded as a `TODO(question)` at
`internal/content/compile_movement.go`.

**Update (2026-08-26):** decompilation falsified the "template pre-fill 255"
escape. The class pool is a zero-filled BSS tail (verified against the PE
section table); the loader parses `CLASS0` first with no template write; the
parser defaults every depth/slope field to the record's own prior value and
runs the three clamps unconditionally; the movement classifier hard-blocks
land cells with `slope > maxslope` (strict). The stock file omits
`maxwaterslope` in `CLASS0..2` (which parse first) and authors 255 only in the
last classes (`CLASS13/14`), so the traced arithmetic really does compile the
first land classes to `maxslope = 0` — a bounded paradox with stock
playability. Decider: a runtime trace of the compiled pool or a unit
definition's slope copy; until then the gated clamps remain the
install-compatible divergence. See `research/formats/tdf.md` (MOVEINFO caveat),
`[02 §5 "Movement class record"]`, `[04 §6.1]`, and the raw-corpus note
`moveinfo-maxslope-paradox.md`.

**Resolved (2026-08-27, `[04 §6.1 R-DOC04-A]`):** the 2026-08-26 update above
is superseded — its writer census missed a startup initializer registered in
the CRT function-pointer table, which pre-fills all 32 class records through
the pool base plus a small offset before any parse: `MaxSlope` = `BadSlope` =
`MaxWaterSlope` = `BadWaterSlope` = 255, `MaxWaterDepth` = 10000,
`MinWaterDepth` = −10000. Omitted keys therefore carry the TEMPLATE values
(not zero, not the previous class's values), the unconditional clamps are
identity for them, and stock compiles to the authored slope limits. There is
no paradox and no key-presence gate: retail is template pre-fill plus
unconditional clamps, exactly the shape the original "Decision" aimed for.
SC5 is closed: delete the gated-clamp divergence and initialize the profile
from the template. The consumer contract (per-cell 2-bit layer stamping,
A* blocking only on layer 0) is in `[04 §6.1 R-DOC04-B]`.

**Falsifies:** nothing yet. It defers PLAN_02 C6's "then apply the three clamps
in order" until the profile's initial value is known.

---

## SC6 — The two documents disagree on the fringe-anchor encoding

**Status:** closed by `[03 §2.2]`'s stamp-time fringe writer contract (2026-08-28) — signed offsets, last-stamp-wins on overlap. The argument below is from map dimensions; the trace is what closed it.

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

**Spec B** `[04 §6.2]`: "the multi-cell successor sentinel follows the successor
hop — the cell stores **target-cell coordinates** and the resolver re-reads that
cell's feature identifier before classifying."

Signed offsets and absolute coordinates are different encodings of the same two
bytes, and only one can be right.

**Observed:** the field is two bytes. Absolute cell coordinates cannot address a
map wider than 256 cells in one byte per axis, and the reference install's maps
run to 402×408 (`pincushion`) and 384×480 (`Comet Catcher`). The offset reading
is the only one that can be literally true at those widths.

**Decision:** implement the signed-offset reading, which is also the one its own
document hedges on. Both readings are exposed as named accessors on `PlotCell`
with the conflict spelled out, and the resolver is the single `ResolveFeature`.

Note this is our argument from map dimensions, not a measurement of retail. A
probe could still show the resolver does something else — for instance storing
coordinates relative to a tile origin rather than to the cell.

**Falsifies:** neither document; it picks between them. PLAN_04's explicit
unknowns already asked for both readings to be exposed.

---

## SC7 — Sound variants are gathered even when the bare event key is absent

**Status:** closed by `[02 R-SND-01 §1]` (2026-08-29) — the executable's sound-category loader reads the bare key, discards the result, and unconditionally gathers `K1, K2, …` until the first absent index. The spec's bare-gate sentence was a mis-reading of the loop and has been corrected in `[02 "Sound category record"]`. The compiler's "gather regardless of the bare key" behaviour is retail, not a divergence; the compile site's `TODO(question)` is removed and cites `[02 R-SND-01 §1]`. Contracts the site honours: the bare variant (if present) is index 0 and numbered variants follow; numbering is contiguous from 1; `<key>text` supplies each caption; a present-but-empty value counts as a variant.

**Spec** `[02 "Sound category record"]`: "An event key that is absent for the
bare form contributes no variants at all, because the bare read is what gates
the numbered loop."

**Observed:** stock `gamedata/sound.tdf` authors `select1` (120 occurrences),
`ok1` (76), `cant1` (76) and `arrived1` (63) with **no bare form** anywhere.
Following the spec letter would mute selection, move-fail and arrival voices
for every unit on a working retail install — clearly not what the executable
does.

**Decision:** gather `K1, K2…` regardless of the bare key's presence. The
compile site formerly carried a `TODO(question)` on what the executable really
gates; that marker is closed by `[02 R-SND-01 §1]` above, and this entry
records why the spec letter is not implementable as written.

**Falsifies:** the bare-gate sentence of `[02 "Sound category record"]`. Do
not "fix" the compiler back to the letter without re-reading the executable.

---

## SC8 — The two documents disagree on the code-9 re-arm jitter — resolved to distinct arms

**Status:** closed by `[R-P0-01]` — the two arms use distinct random bounds over the same 30-tick base.

**Spec A** `[04 §3.3]`, result-code 9: if the record is last, "reset its phase
and set **the same randomized deadline**" — i.e. the code-3 formula, global
tick + 30 + a random value below 15.

**Spec B** `[05 "Queue pumping and result codes"]`, result-code 9: "restarts at
state 0 with a randomized **30-plus-random-30** retry when no successor
exists".

Same field, different jitter bounds (rand < 15 vs rand < 30).

**Observed:** the two retail arms use distinct random bounds while adding the
same 30-tick base delay. Result code 3 draws below 15; result code 9 on the last
record draws below 30 [R-P0-01]. The earlier analysis missed the latter arm
because it reaches the shared deadline calculation through an indirect branch.

**Decision:** implement distinct arms: code 3 → `tick+30+RNG(15)` and code 9
last → `tick+30+RNG(30)` per [R-P0-01].
`internal/orders/pump.go` has `randBelow15` for code 3 and `randBelow30` for
code 9 last; both primary and secondary pumps use the same split. Observable
only as the re-arm cadence of a completed last order.

**Falsifies:** the earlier SC8 reading that both arms shared `RNG(15)`; [04 §3.3]
row is correct for code 3 and [05] is correct for code 9 last.

---

## SC9 — `LOS.TDF` declares nine tables and supplies twelve

**Status:** closed by asset census plus the declared-count reading; `[03 §3.2]` was silent and this entry records which silence was resolved and how.

**Spec** `[03 §3.2]` clamps the terrain-ray group index "into the parsed
LOS.TDF table range" without saying which count defines that range.

**Observed** in the reference install: `gamedata/los.tdf` has
`[TABLEINFO] { numtables=9 }` and then `[TABLE1]` through `[TABLE12]` — three
more sections than it declares. Reproduce with:

```
go test ./internal/content -run TestCompileLOSTables -v
```

which asserts both numbers against the install.

**Decision:** the clamp uses the **declared** `numtables`, not the discovered
section count. The declared value is what the engine's table object reports,
and the three undeclared tables are unreachable authoring residue — the largest
declared table already saturates every stock `sightdistance` (the biggest,
450, quantizes to 14 and clamps to 8). `Catalog.LOS` keeps all twelve parsed
sections so nothing is lost and a probe can change the decision in one line;
`internal/visibility` reads `NumTables` for the clamp and never
`len(Tables)`.

**Falsifies:** nothing — the spec is silent, and this records which silence we
resolved and how.

**Note:** the sprite-mask path has its own count from a different source (the
ten frames of the visibility-mask GAF, `[03 §3.2]`). The two counts are not
required to agree and must not be shared.

---

## SC10 — Trailing -Z is Z-Y/2 shear, not a second model-space NEG (H_A vs H_C)

**Status:** closed by `[03 §2.4]` / `[03 §2.5]` (2026-08-25) — the trailing `-Z` belongs to projection, not to source conversion.

**Spec** `[03 §2.4/2.5]` prior to 2026-08-25: load-time half-turn `-X,-Z` was established, but whether screen helpers' `-Z` was a second conversion (`H_C` net `-X`) vs shear (`H_A` net `-X,-Z`) was an open question or supported inference.

**Observed:** the screen helpers negate only the transient projected Z value,
then compute the established `Z - Y/2` shear. They do not store that negation
back into model data. The muzzle query likewise consumes the vectors produced
by the one load-time half-turn without applying a second sign change. Therefore
the trailing `-Z` belongs to projection, not source conversion.

**Decision:** implement `H_A` sole load-time conversion; reject `H_C` (rr-06_addendum, direct-static). Flare/muzzle world at `(2,1,-30)` is `(-2,1,+30)` plus unit origin.

**Falsifies:** `H_C` reading; no prior SC.

---

## SC14 — Flare/muzzle per-vertex reuse vs second NEG

**Status:** closed by `[03 §2.4]` (2026-08-25) — the muzzle query reuses the pristine post-load vectors.

**Spec** `[03 §2.4]` / `research/formats/3do.md` "Model facing is −Z": piece translations converted at load, but muzzle query path could have re-applied `NEG`.

**Observed:** the piece transform rotates and translates the already-converted
piece and center vectors. `COB.QueryPrimary` uses the same vectors without an
extra sign change.

**Decision:** muzzle query reuses pristine post-load vectors; no second sign fixup (rr-06_addendum, direct-static). Same evidence as SC10; separated because SC10 is about projection shear vs conversion and SC14 is about per-vertex flare reuse.

**Falsifies:** second-NEG-at-query reading.

---

## SC15 — The cursor index table was off by one from slot 10

**Status:** closed by `[07 §8]`'s corrected 0..21 cursor table plus the asset census of `anims/cursors.gaf`.

**Spec** `[07 §8]` (before this entry): "The cursor index table is closed …
index 1 `cursorattack` … 9 `cursorteleport`, 10 `cursorreclamate`,
11 `cursorload`, 12 `cursorunload`, 13 `cursormove`, 14 `cursorselect`,
15 `cursorfindsite`, 16 `cursorred`, 17 `cursorgrn`, 18 `cursornormal`,
19 `cursorhourglass`, 20 `pathicon`."

**Observed:** `anims/cursors.gaf` in the reference install holds **22** named
entries, two more than that table's twenty. Reproduce with the asset-guarded
`TestGafCursorAssetGuarded` in `internal/render`, or by listing the GAF's
entries directly:

```
cursormove cursorgrn cursorselect cursorred cursorload cursorrevive
cursordefend cursorpatrol cursorprotect cursorrepair cursorattack
cursornormal cursorpickup cursorairstrike cursorteleport cursorreclamate
cursorfindsite cursorcapture cursorunload cursorhourglass cursortoofar
pathicon
```

`cursorrevive` is missing from the spec table entirely and `cursorprotect` is
never referenced by the executable (its name does not appear in the binary's
string data at all — it is unused art).

The handle array has twenty-two slots and the init sequence fills slot 10 with
`cursorrevive` **last**, after slot 21, breaking the otherwise ascending order.
Transcribing the sequence rather than the slot offsets drops slot 10 and shifts
`cursorreclamate` through `pathicon` down by one. Three independent readers
confirm the corrected numbering: the idle default the pointer update falls back
to is index 19, which must be `cursornormal` and is `cursorhourglass` under the
old table; the front end installs index 20 across blocking transitions, which
must be `cursorhourglass` and is `pathicon` under the old table; and the shape
chooser returns 7 for PATROL, 6 for REPAIR, 5 for FOLLOW, 11 for RECLAIM, 14 for
MOVE and 16 for MOBILEBUILD, every one of which names the right art only under
the corrected table.

**Decision:** the table is 0..21 with slot 10 `cursorrevive`; every index from
`cursorreclamate` up shifts by one. `research/retail-executable-spec`
`[07 §8]` and `internal/render/gaf_cursor.go` carry the corrected table, and
`render.CursorAttack`…`render.CursorPathIcon` are the named constants.
`cursorprotect` is deliberately absent from the table: retail never resolves it.

**Falsifies:** the previous "closed" twenty-entry table, and the
`cursorfindsite` = 15 / `cursorgrn` = 17 constants that PLAN_12 C12 quoted.

---

## SC16 — the active-state `0x20` bit and the empty-current-task field (closed)

**Status: closed** (WU-19-45, 2026-09-01). Both halves are resolved and the
entry gates no further work. The active-state half closed 2026-09-01
(WU-19-37); the empty-current-task half closed when RWU-19-14 traced the
compared word.

**Spec** `[07 §9]`: the shared selection eligibility predicate, and the
own-unit inspect predicate behind `cursorselect` in `[07 §8]`, test "the
active-state bit `0x20` of unit runtime flags" and an empty current-task field.

**Observed (superseded, first block).** This entry originally read: "in this
repo bit `0x20` of `units.Unit.Flags` is already claimed by
`construction.FlagInBuildStance` (`1 << 5`, COB port 5 `INBUILDSTANCE`,
`[04 §4.4]`), and no code path sets an active-state bit. Gating on `0x20` would
make the inspect predicate permanently false and would make the `cursorselect`
shape unreachable." Its decision was "`hud.isInspectable` gates on ownership
plus completed construction only … Do not 'fix' it by testing `0x20` until the
runtime flag word is reconciled with `[07 §9]`".

Both halves of that observation are now wrong. `construction.FlagInBuildStance`
no longer exists: the COB port's INBUILDSTANCE is the separate
`units.Unit.InBuildStance` byte. Bit `0x20` of `units.Unit.Flags` is
`units.ClassifierEligibleStatus`, written by the allocator initializer, cleared
by death finalization, cleared for a scripted unit by the InitialMission
postlude and set again by `MakeSelectable`
(`[R-P0-04 "Runtime eligibility bit lifecycle"]`, `[04 §3.6]`,
`[08 R-TRIG-01 §3]`). Nothing collides, the bit is set on every live unit, and
a capture on Coast to Coast confirms `cursorselect` resolves over an own idle
unit and falls back to `cursornormal` with the bit cleared.

**Observed (superseded, second block).** The remaining half previously read:
"`units.Unit.OrderGuard` is this build's per-unit order-guard float, and
`internal/orders/pump.go` writes it `1.0` whenever the primary queue is
non-empty. An idle unit's primary queue holds a `Standby` node, so the guard is
nonzero for every idle unit. Gating `hud.isInspectable` on it therefore makes
`cursorselect` unreachable — the same failure this entry originally warned
about for the bit. Note the wider consequence: `[07 §9]` gives the *rectangle
selection* the same compare-to-`0.0`, so if the guard's writer were right,
retail's bulk selection would select nothing either. `units.Unit.Eligible`
reduces bit + guard together and, before WU-19-37, had no production caller at
all, which is why the contradiction had not surfaced." Its decision was
"`hud.isInspectable` … does **not** gate on `OrderGuard`; a `TODO(question)` at
the site records why. Do not add that clause until either the guard's writer is
reconciled with `[07 §9]` … or the current-task field is identified as a
different word."

That observation was reasoning about a field that does not exist in retail.
`[07 R-WGT-01 §10]` settles it on a whole-executable census of every load and
every store of the compared word: **the empty current-task field is the
remaining-build fraction** — the word the construction step drives from `1.0`
toward `0.0` `[05 R-WORK-01 §1]`, the constructor seeds `[04 R-COB-03 §4]`, and
COB port 17 reads `[04 R-COB-03 §2]`. **No routine of the order subsystem
stores to it**: not the record constructor, not either pump, not the
return-code epilogue, not cancel-all, not the expiry helper, not the
idle-queue refill. The "clamped `0..1` ratio while an order is being processed"
that this build's guard imitated is the construction step's own clamp, and the
"order completion" that zeroes it is the *build* order completing on its
product. `[07 §8]` and `[07 §9]` are both corrected inline to say so.

So the observation's own conclusion was right for the wrong reason: gating on
`OrderGuard` would indeed have made `cursorselect` unreachable, because the
field was invented — not because retail has a gate we could not satisfy. The
"wider consequence" it flagged for rectangle selection is the proof: a
predicate that excluded every idle unit could not be what retail's bulk
selection runs.

**Decision:** `units.Unit.OrderGuard` and both pump writers are **removed**
(WU-19-45). `units.Unit.Eligible` is `E(u)` of `[07 R-WGT-01 §10]`: the
selectable status bit `0x20` and the remaining-build fraction exactly `0.0`.
`hud.isInspectable` needs no new clause — its existing `Remaining != 0` test
*is* the empty-current-task gate — and the `TODO(question)` at that site is
retired. Rectangle selection gates on the same predicate: only `E(u)` units
enter the inclusive rectangle test, walking the local player's slice in
ascending slot order. The two clauses still unmodelled — the post-capture grace
counter (always zero without a remote controller) and the carrier's
cargo-selectable bit 30 — remain a `TODO(question)` on `Eligible`, not a spec
conflict.

**Falsifies:** its own earlier "Observed" and "Decision" blocks, quoted above.
Nothing in the spec.

---

## SC17 — Order queue caps 64/32 were inside stock-reachable behavior [P1-I09]

**Status:** closed by corpus census (`internal/orders/corpus_caps_test.go` `TestCorpusQueueCaps_Retail`) — the 64/32 caps were inside stock-reachable behavior and are replaced by dynamic storage.

**Spec** `[P2-03]` fallback caps `MaxPrimaryQueue=64` / `MaxSecondaryQueue=32`
with diagnostic drop (I11 divergence) for queue overflow, plus pump
iteration cap `200` for handler loops via 0/1/2 without blocking (NEGATIVE-BOUNDED
no cap).

**Observed:** corpus measurement over the reference install (278 units, 275
maps, 175 campaign missions, 13 campaigns) via
`internal/orders/corpus_caps_test.go` `TestCorpusQueueCaps_Retail` finds:

* `Silent Slayers.ota` `ARMCARRY carry1` raw InitialMission tokens `105` and
  `fighting under fire.ota` raw `138`, both >64. With the 64 cap the run
  truncated to 64 in `TestCorpusQueueCaps_Retail`'s capped-era probe
  (`meas_main.go` 2026-08-25: `maxPrimary=64` capped vs `138` raw). Uncapped,
  that unit would require >64 primary nodes (plus `MakeSelectable` postlude).
* Secondary max in corpus is `1` (`bw 2` in `exp1ac12.ota` `CORFMD`), well
  below 32.
* Pump iterations for those queues are `<138 <200`, so the 200 guard is
  outside stock.

Therefore the 64 cap was inside stock-reachable behavior and changed a
retail mission. The 32 cap was outside stock but an arbitrary divergence
with no retail capacity, and the pump 200 guard is outside stock.

**Decision:** replace primary/secondary caps with dynamic slice growth
matching retail's heap-linked list (no located cap). The only remaining
I11 divergence is an OOM guard at `10000` (`OOMGuardQueue` in
`internal/orders/pump.go`), which is `>>138` and `>>` any reasonable
player shift-queue (hundreds) while still bounding hostile input. The
previous constants `64`/`32` are retained as deprecated for test
compatibility but no longer gate `Push`/`CoalesceTail`. The pump guard
`200` is retained with corpus proof that it is outside stock.
`internal/orders/corpus_caps_test.go` locks the measurement; if corpus
grows beyond the guard the test fails and the guard must be revisited.
`go test -tags retail -run Corpus ./internal/orders` reproduces.

**Falsifies:** the `P2-03` fallback-cap values `64`/`32` as stock-safe;
they are replaced by dynamic storage per `P1-I09`.

---

## SC18 — Allocator zero-fill byte count and COB malformed-save policy are narrow open items [P1-I09]

**Status:** open — decider: static trace of the allocator's exact memset length and of the COB abort path. Marked `TODO(T23)` at the allocation sites; see doc 01's and doc 04's "Missing and unknown" items.

**Correction audit (current boundary):** An earlier revision of this section
described a Nanolathe-specific `StateV1` save codec and directed callers to
keep its fatal-on-truncation policy. That codec has been removed. The active
save boundary is retail HAPIBANK account parsing: campaign continuation exposes
Summary metadata, while in-battle restoration returns an explicit unsupported
result `[08 "Save-file organization"]` `[GAP T9]`. The StateV1 wording below is
retained only as historical audit evidence and is not an implementation
instruction; do not reintroduce that codec.

**Spec** `[01 §6.1]`/`[GAP T13]` note the allocator's backing implementation,
arena boundaries and zero-fill policy remain `TODO(T23)`; `[04 §4.1]`/`[GAP
T15]` note COB loader allocation and the exact failure behavior of allocation
or corrupted piece indexes remain unresolved — retail may abort through its
allocator where a clean implementation should terminate the affected script
deterministically. `[08 "Save-file organization"]` documents the
non-transactional partial-load policy.

**Historical observation (superseded):** `internal/orders/pump.go`,
`internal/combat/pool.go`, `internal/units/units.go` and
`internal/save/bulk.go` zero-initialize Go structs (Go zero value). Retail's
exact `memset` byte count for the 86-byte order node, 300×107-byte projectile
records, 280-byte unit records etc. is not traced, but observable effect is
zeroed. An earlier implementation's `internal/save/boxes.go`
`UnmarshalStateV1` rejected truncated COB blobs, while retail bulk COB boxes
(`0x528`+`stack*4`+`pieces*0x6C`) were handled by the bulk loader's partial-load
skips per `[08]` (missing account created empty, each subsystem's defaults
govern). This records the evidence that led to the removed StateV1 abstraction;
the current save package has no such codec.

**Decision (current):** keep Go zero-initialization with `TODO(T23)` at the
allocation site (`internal/orders/pump.go` newNode, `internal/combat/pool.go`
Reserve, `internal/units/units.go` Create) citing the open byte count. Save
callers use the retail account parser and the explicit unsupported in-battle
restoration result; no StateV1 fatal-on-truncation policy or alternate
continuation format remains. No stock corpus hits either historical guard:
`formats/coverage_test.go`
`TestFormatCoverage` parses all stock files without hitting TDF/Gaf/Pcx/Wav
fault guards, and `TestCorpusQueueCaps_Retail` shows no queue guard hit
after the fix. Revisit only with executable evidence that retail's exact
memset length or COB abort path is observable.

**Falsifies:** nothing; it records the two `P1-I09` narrow open items as
explicit `TODO(T23)`/`TODO(question)` placeholders.

---

## SC19 — Yard-map parsing is not one-to-one, and bits 5/6 read the wrong flags

**Status:** closed by asset census plus the character-loop trace; `[05 "Geothermal requirement"]` carries the corrected parse and flag identities.

**Spec said:** `[05 "Geothermal requirement"]` described yard-map characters as
mapping "one-to-one into a row-major buffer sized by the packed footprint
extents", and named bit 6 a test for "a specific non-reclaimable flag". Bit 5
was described only as "free of blocking features", which `internal/world`
implemented as any feature at all.

**Observed:** three separate disagreements with the shipped data and with the
definition compiler's character loop.

1. **Length.** Forty-six of the 126 stock yard-mapped definitions disagree with
   their own footprint. `ARMSOLAR` authors 27 characters for a 5×5 footprint,
   `ARMESTOR` authors one for 4×4, `ARMSILO` nine for 5×5, `CORSOLAR` sixteen for
   5×5. Reproduce with a catalog compiled from `~/TotalAnnihilation`, comparing
   `len(strings.Fields-stripped YardMap)` against `FootprintX*FootprintZ` over
   `Catalog.SortedUnitKeys()`. Rejecting the mismatch — which `ParseYardMap` did
   — left every one of those buildings permanently unplaceable; the reported
   symptom was "I cannot build a solar collector at all". The compiler instead
   skips table-absent characters without consuming a cell, parks on the final
   character so it repeats for unfilled cells, and never reads past the last
   cell.
2. **Bit 5.** Metal patches are 3×3 features authored `blocking=0`,
   `reclaimable=0`, `indestructible=1` (`archmetal*`, `drymetal*`, `moonmetal*`,
   `marsmetal*`, `*aquaore*` — 3-tier sets in every world's feature TDF).
   `ARMMEX` authors an all-`o` yard map and `o` carries bit 5, so blocking on
   presence rather than on the definition's authored `blocking` flag makes the
   metal extractor unplaceable on its own deposit — the reported symptom. Trees
   and rock clutter do carry `blocking=1` and still block.
3. **Bit 6.** The validator reads bit 1 of the flag word's **high** byte, which
   is word bit 9 — `indestructible` (mask 0x0200). `reclaimable` is word bit 7 of
   the same byte-pair and is never read by the validator.

**Decision:** `ParseYardMap` fills the footprint by retail's rules and no longer
returns a length or unknown-character error; `ValidatePlacement` reads
`FeatureDef.Blocking` for bit 5 and `FeatureDef.Indestructible` for bit 6, and
counts a geothermal match only on cells whose own yard byte carries bit 7. A
fringe cell whose anchor hop finds nothing is not blocking. `[05 "Geothermal
requirement"]` is updated to match.

**Falsifies:** the one-to-one parse contract and the bit-6 flag identity in
`[05 "Geothermal requirement"]`. It does not change the control-byte table, the
bit roles, or the geothermal rule itself.

---

## SC20 — Cursor-to-ground is a search along Z, not an inverse projection

**Status:** closed by `[07 §8]`'s cursor-to-ground resolver (bounded search along Z, then bracket and interpolate).

**Spec said:** `[07 §8]` said only that "world space, unit, feature, and
terrain/radar tests use the camera transform", which `internal/camera`
implemented as the algebraic inverse of `WorldToScreen` at height zero.

**Observed:** that inverse is wrong wherever the ground is above zero, and
visibly so. Terrain tiles are presented flat while world objects carry the
half-height shear `screenRow = Z − (height >> 1)` `[03 §2.5]`, so an order given
at a pixel put the unit half the terrain height north of it — the reported
symptom was a commander moving to a space above the click. Retail resolves the
pointer with a bounded search instead: clamp into the map rectangle, start eight
cells south of the clicked row, walk north up to nine cells comparing each
candidate's `max(height, seaLevel)` projection against the clicked row, then
bracket and interpolate. Reproduce by sweeping every map pixel of a stock map
through `Terrain.CursorToWorld` and projecting the result back: exact on flat
ground, within a few pixels on steep slopes (retail's own linear-interpolation
residue), against an error of half the terrain height for the algebraic inverse.

**Decision:** `Terrain.CursorToWorld` implements the search and is the single
cursor-to-ground conversion the battle screen uses, through
`battleSession.cursorWorld`. `Camera.ScreenToWorld` keeps its pixel-level
meaning and is no longer used directly for ground orders. `[07 §8]` is updated
with the full resolver.

**Falsifies:** nothing written down; it closes a gap `[07 §8]` had left
unstated. The map-rectangle clamp is a behavioral consequence worth noting: a
pointer past the map edge resolves to the edge, so an off-map build ghost is
legal rather than out of bounds.

## SC21 — `BMcode` marks structures, not factories, and `CanMove` does not separate factories from mobile builders

**Status:** closed by asset census of the 278 stock definitions; `[fmt fbi]`'s `BMcode` row is corrected and `[07 §9]` carries the product-BMcode branch.

**Spec said:** `research/formats/fbi.md` gave `BMcode` as "`0` for stationary
factories ('build-machine'), `1` for everything else — distinguishes pad
factories from mobile builders". That reading is a community guess, and the
document flagged it as one.

**Observed:** a census of the compiled catalog over `~/TotalAnnihilation`
partitions the 278 unit definitions into exactly five buckets:

```
BMcode=0 yard=yes canmove=no  builder=no   103
BMcode=0 yard=yes canmove=no  builder=yes    2
BMcode=0 yard=yes canmove=yes builder=yes   21
BMcode=1 yard=no  canmove=yes builder=no   122
BMcode=1 yard=no  canmove=yes builder=yes   30
```

`BMcode == 0` and "has a yard map" are the same set, with no exception. That is
the structure class, and it is what `[04 §6.2]` already meant when it said the
yard map is parsed only when BMcode is zero. `ARMSOLAR` and `ARMMEX` author `0`;
`ARMFAV`, `ARMCOM` and `ARMCK` author `1`.

The census also disproves a second assumption this repo held independently of
any document: **stock factories author `CanMove=1`**. `ARMVP`, `ARMLAB` and
`ARMHP` are all `BMcode=0, CanMove=1, Builder=1`. Only two definitions in the
whole corpus are `Builder=1, CanMove=0`.

**Consequence in code:** `hud.IsFactoryBuilder` was `Builder && !CanMove`, so
every stock factory failed it and passed `IsMobileBuilder` instead. Clicking a
vehicle in a factory's build menu armed a placement ghost rather than queueing
the vehicle.

**Decision:** the factory-versus-placement branch keys on the **product's**
`BMcode`, via `hud.ProductArmsPlacement`, which is what retail's build-button
handler tests — it arms the MOBILEBUILD latch and stores the product id only
when the product's BMcode byte is zero, and otherwise falls through to the
immediate queue path `[07 §9]`. `IsFactoryBuilder`/`IsMobileBuilder` survive only
as the fallback for a product the catalog cannot resolve.

**Falsifies:** the `BMcode` row of `research/formats/fbi.md`, now corrected.
`[07 §9]` gains the product-BMcode branch. Nothing in `[04 §6.2]` changes; its
BMcode-zero gate was right all along.

---

---

## SC22 — Static-layer path search and Nanolathe dynamic-block / retry policy [OW-3-O]

**Status:** closed by `[R-MOV-01 §7]` (2026-08-28) and the P0-03
reconciliation (2026-08-31). Retail re-arms a path request when a route is
installed and the mover is blocked or has fewer than two points, throttled to
one request per 60 ticks with no retry ceiling. The search-versus-commit split
itself is closed by `[R-MOV-02A]`.

**Spec** `[04 §8.2]` (static and mobile collision): mobile units are not
permanent A* walls in the map-load layer; path search uses the static terrain,
feature, and yard/building layers, while a request-initialization revision may
temporarily re-stamp recent mobile footprints and apply the occupant-age gate
described in `[04 §6.1 R-DOC04-B]`. Final mobile contention is arbitrated at
commit `[04 §8.2]`; mobile units are hard blockers there even though their
projected motion is not inserted into the expansion heap.

**Observed:** `internal/movement/integrate.go:searchFunc` previously checked
`OccupancyGrid.OccupantAt` for every neighbor and rejected occupied cells,
turning transient traffic into static obstacles and feeding an invented
retry/removal policy. `OccupancyGrid.Revision/Bump`
(`internal/movement/collision.go:Revision/Bump/BumpRevision`) had no explicit
consumer beyond diagnostics; search already rechecks `isPassable` lazily at
expansion, so an explicit revision guard is unnecessary, but `Stamp`/`Clear`
correctly bump `rev` per `[04 §7.4]` C18 and tests lock the bump. The obsolete
30-tick/one-retry path-failure policy and its session-side removal branch are
deleted; follower state is now the sole retry owner.

**Correction history [R-MOV-02A]:** The previous Decision said that mobile
occupancy was "ignored at search time" without qualification. That sentence
correctly described the direct `searchFunc.isPassable` predicate, but was
overbroad as a retail contract because it omitted the request-initialization
revision pass. It is superseded by the bounded rule below; the `[04 §8.2]`
commit behavior is unchanged.

**Decision:** Keep `searchFunc.isPassable` free of a direct
`OccupancyGrid` lookup, but bind it to the request's movement-class layer.
At request initialization, that layer's watermark is armed as
`max(currentTick, 30) − 30`, recently committed mobile footprints are
re-stamped, and the occupant-age gate allows recent occupants while making an
older occupant block its re-stamped cells. Existing heap entries are not
eagerly purged; expansion
rechecks passability lazily when each entry is opened. Thus the search layer is
static at map load but can have this bounded, temporary mobile revision
interaction. The scheduler and expansion do not receive blocker identity,
velocity, or projected destination, and no collision-triggered replan is
established. Mover-vs-mover contention remains authoritative at commit via the
row-major footprint validator `[04 §8.2]`, the half-speed/clamp response, and
the synchronous clear/commit/stamp sequence. `OccupancyGrid.Revision/Bump` is
retained for its separate diagnostic/revision role; it must not be conflated
with the class-layer watermark and request-init restamp. Building/yard
occupancy remains part of the static/profile inputs.

The former collision-triggered Nanolathe avoidance policy has been removed
under `[R-MOV-02A]`: there is no lower-slot priority, `avoidNext` cadence, or
`ReplanMove` submission after a rejected mobile commit. The bounded final
commit response is therefore the sole implemented collision response; outer
yield/replan and ordinary open-group liveness remain **Unknown** as stated in
`[R-MOV-02A]`.

**Falsifies:** the previous mobile-as-wall search behavior and the blanket
claim that mobile occupancy is always ignored at search time. It does not
turn mobile occupancy into a permanent static wall, and it does not establish
that the separate `OccupancyGrid.Revision` counter has an expansion consumer.

---

**Refinement (2026-08-29, [04 R-COLL-01 §7]):** a whole-image census found no
yield/sidestep owner. The commit validator never reads the class layer or the
occupant age; the age gate is search-only. A blocked unit stops advancing its
last-stamp tick, so after more than 30 ticks it becomes a hard search block
for others, and its own 60-tick repath finds a route around. That mechanism —
not a retry counter — is what the reconciliation pass should reproduce.

## SC23 — `gravity = 0` maps cancel every `AirStrike` order (retail-sanctioned bound)

**Spec:** [04 §10.2 R-AIR-01 §8] (Established, static trace): the bombing
run's release-point leg computes the release lead as
`t = sqrt((2 · cruisealt) / gravity)`; before dividing it reads the map's
`gravity` word and, **if it is zero, returns the cancel-all code (7)**, which
empties the bomber's whole order queue. There is no fallback gravity.

**Observation (2026-09-01, asset census):** 275 stock maps enumerated, none
authors or defaults to gravity 0; the bound is unreachable on stock content.
Every map's `[GlobalHeader]` authors the `gravity` key explicitly (none
omitted, none negative); the minimum authored value seen is `8` (word
`582`), the maximum `445` (word `32421`), and 192 of 275 author `112` (word
`8155`), matching the census already on file in `research/formats/ota.md`.
A map whose `gravity` key is absent or `0` would still make bombers
un-orderable to attack in retail per the spec above; a reimplementation that
substitutes a default gravity here would invent behavior — but no stock map
authors one.

**Decision:** clone retail — cancel-all on zero gravity. If any retail map
in `~/TotalAnnihilation` carries `gravity = 0`, a probe under `probes/`
should confirm the cancellation visibly (orders drop, bomber idles) before
this entry is promoted from "sanctioned bound" to "observed". Manual retail
observation is the decider; do not automate the executable.

**Contract changed:** none in Nanolathe today; guards the air-order executor
against a plausible-looking default. Status (2026-09-01): closed-unreachable-on-stock
— the RWU-19-8 asset census found no `gravity = 0` (or omitted-key, or
negative) stock map, so retail's cancel-all bound cannot be observed on
shipped content; the Decision above still stands as the clone-retail
contract for any future or modded map that does author `gravity = 0`.
WU-19-16 (2026-09-01) removed the last thing standing between such a map and
that contract: `internal/world/terrain.go` tested key PRESENCE, so an OMITTED
`gravity` took the 0x1FDB fallback — reading an omission like a negative, where
[03 §2.2] C4's correction against [02 R-MAP-01] says an omitted key gets the OTA
parser's integer default `0`, which passes the `>= 0` test. Wind and
`tidalstrength` had the same inversion and were corrected with it.

## SC24 — Retail never compiles loose `units\*.FBI` or parses loose `weapons\*.tdf`

**Spec:** [02 R-CAT-01 §4] (Established, static trace): the unit catalog
loader's gate drops every FBI that does not come from an archive — silently,
with the definition's archive bit cleared — and never parses a loose
`weapons\*.tdf`; the switch that would enable loose files is a constant `1`
in the shipped image with no writer. The community "units must be packed"
rule is the executable's own.

**Observation:** Nanolathe's `CompileUnits` compiles every `units/*.fbi` the
VFS enumerates regardless of provider (`internal/content/compile_unit.go`),
and every `testdata/` fixture and probe under `probes/` relies on loose
authored FBIs being compiled.

**Decision:** keep the divergence, documented here: loose definitions are
accepted. It is a superset of retail (a stock install has no loose FBIs, so
behaviour on retail content is identical) and it is what makes authored
fixtures and probes loadable without packing. Nothing in the simulation
reads the archive bit. Status (2026-08-29): open — closes if a mod-loading
contract ever requires the retail gate, in which case the gate belongs in the
catalog loader keyed on the entry's provider kind, with fixtures packed.

**Contract changed:** none; records why `CompileUnits` is more permissive
than [02 R-CAT-01 §4].

## SC25 — Stock `loadgame.gui` authors fewer gadgets than `[08 R-SAVE-02 §1]` lists

**Spec** `[08 R-SAVE-02 §1]` names the save/load screen's gadget vocabulary
including `TITLE`, `CAMPAIGN`, `CAMPTEXT` and `LoadGame`.

**Observed** (WU-19-11, 2026-09-01): the reference install's `guis/loadgame.gui`
(4130 bytes) authors `HEADER GAMES LOAD CANCEL SLIDER GAMENAME GAMETYPE MISSION
TIME SIDE RADAR DIFF DELETE SaveGame` — none of the four above. The screen
code sets those four by name, so the writes are inert rather than wrong.

**Decision:** keep the writes (a mod or a later patch may author them); treat
the four as optional gadgets. The section's list is a superset of stock
content, not a contract that stock content satisfies.

**Status:** open — a census of the other language/patch archives' copies
would settle whether any stock variant authors them.

## How to add to this file

One section per conflict: what the spec says, what was observed and how, the
decision, and which contract it changes. Evidence must be reproducible — a probe
against `~/TotalAnnihilation` that another agent can rerun.
