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

---

## SC1 — There is no ten-archive cap we can honor

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

**Spec** `[02 "Sound category record"]`: "An event key that is absent for the
bare form contributes no variants at all, because the bare read is what gates
the numbered loop."

**Observed:** stock `gamedata/sound.tdf` authors `select1` (120 occurrences),
`ok1` (76), `cant1` (76) and `arrived1` (63) with **no bare form** anywhere.
Following the spec letter would mute selection, move-fail and arrival voices
for every unit on a working retail install — clearly not what the executable
does.

**Decision:** gather `K1, K2…` regardless of the bare key's presence. The
compile site carries the `TODO(question)` on what the executable really gates;
this entry records why the spec letter is not implementable as written.

**Falsifies:** the bare-gate sentence of `[02 "Sound category record"]`. Do
not "fix" the compiler back to the letter without re-reading the executable.

---

## SC8 — The two documents disagree on the code-9 re-arm jitter — resolved to distinct arms

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

**Spec** `[03 §2.4]` / `research/formats/3do.md` "Model facing is −Z": piece translations converted at load, but muzzle query path could have re-applied `NEG`.

**Observed:** the piece transform rotates and translates the already-converted
piece and center vectors. `COB.QueryPrimary` uses the same vectors without an
extra sign change.

**Decision:** muzzle query reuses pristine post-load vectors; no second sign fixup (rr-06_addendum, direct-static). Same evidence as SC10; separated because SC10 is about projection shear vs conversion and SC14 is about per-vertex flare reuse.

**Falsifies:** second-NEG-at-query reading.

---

## SC15 — The cursor index table was off by one from slot 10

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

## SC16 — No runtime bit corresponds to `[07 §9]`'s active-state `0x20`

**Spec** `[07 §9]`: the shared selection eligibility predicate, and the
own-unit inspect predicate behind `cursorselect` in `[07 §8]`, test "the
active-state bit `0x20` of unit runtime flags".

**Observed:** in this repo bit `0x20` of `units.Unit.Flags` is already claimed
by `construction.FlagInBuildStance` (`1 << 5`, COB port 5 `INBUILDSTANCE`,
`[04 §4.4]`), and no code path sets an active-state bit. Gating on `0x20`
would make the inspect predicate permanently false and would make the
`cursorselect` shape unreachable.

**Decision:** `hud.isInspectable` gates on ownership plus completed
construction only, with a `TODO(question)` naming both missing gates. Do not
"fix" it by testing `0x20` until the runtime flag word is reconciled with
`[07 §9]` — `INBUILDSTANCE` and the active-state bit cannot both be `0x20`.

**Falsifies:** nothing in the spec; it records that a `[07 §9]` gate has no
implementable counterpart yet.

---

## SC17 — Order queue caps 64/32 were inside stock-reachable behavior [P1-I09]

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

**Spec** `[01 §6.1]`/`[GAP T13]` note the allocator's backing implementation,
arena boundaries and zero-fill policy remain `TODO(T23)`; `[04 §4.1]`/`[GAP
T15]` note COB loader allocation and the exact failure behavior of allocation
or corrupted piece indexes remain unresolved — retail may abort through its
allocator where a clean implementation should terminate the affected script
deterministically. `[08 "Save-file organization"]` documents the
non-transactional partial-load policy.

**Observed:** `internal/orders/pump.go`, `internal/combat/pool.go`,
`internal/units/units.go` and `internal/save/bulk.go` zero-initialize Go
structs (Go zero value). Retail's exact `memset` byte count for the 86-byte
order node, 300×107-byte projectile records, 280-byte unit records etc. is
not traced, but observable effect is zeroed. For COB saves,
`internal/save/boxes.go` `UnmarshalStateV1` returns an error on truncated
COB blobs (fatal for that StateV1), while retail bulk COB boxes
(`0x528`+`stack*4`+`pieces*0x6C`) would be handled by the bulk loader's
partial-load skips per `[08]` (missing account created empty, each
subsystem's defaults govern). The two policies are therefore different
abstractions: Nanolathe StateV1 is versioned and fatal on truncation,
retail bulk is partial-load skip.

**Decision:** keep Go zero-initialization with `TODO(T23)` at the allocation
site (`internal/orders/pump.go` newNode, `internal/combat/pool.go`
Reserve, `internal/units/units.go` Create) citing the open byte count.
For COB saves, keep StateV1 fatal-on-truncation with
`TODO(question)` naming the fatal-versus-skip question, and keep bulk save
partial-load skip with diagnostic as documented in `internal/save/bulk.go`.
No stock corpus hits either guard: `formats/coverage_test.go`
`TestFormatCoverage` parses all stock files without hitting TDF/Gaf/Pcx/Wav
fault guards, and `TestCorpusQueueCaps_Retail` shows no queue guard hit
after the fix. Revisit only with executable evidence that retail's exact
memset length or COB abort path is observable.

**Falsifies:** nothing; it records the two `P1-I09` narrow open items as
explicit `TODO(T23)`/`TODO(question)` placeholders.

---

## SC19 — Yard-map parsing is not one-to-one, and bits 5/6 read the wrong flags

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

**Spec** `[04 §8.2]` (static and mobile collision): mobile units are **not** A* walls; path search runs on the static layer (terrain + static features + yard/building occupancy) and arbitrates at commit `[04 §8.2]`; supported inference adds "mobile units are hard blockers at the movement commit stage even though they are not inserted into the static A* layer." `[04 §7.4]` dynamic blockers bump a profile revision, heap entries are not purged eagerly, passability is rechecked lazily at expansion (a previously open node can become blocked without rebuilding the heap).

**Observed:** `internal/movement/integrate.go:searchFunc` previously checked `OccupancyGrid.OccupantAt` for every neighbor and rejected occupied cells, turning transient traffic into static obstacles and feeding an invented retry/removal policy (needless rejected routes around movers, then path-failure retry count). `OccupancyGrid.Revision/Bump` (`internal/movement/collision.go:Revision/Bump/BumpRevision`) had no explicit consumer beyond diagnostics; search already rechecks `isPassable` lazily at expansion, so an explicit revision guard is unnecessary, but `Stamp`/`Clear` correctly bumped `rev` per `[04 §7.4]` C18 and tests locked the bump. `landPathFailureRetryInterval = 30` and `landPathFailureMaxRetries = 1` (`integrate.go:127-128`) and the `rec.Retries >= 1` hard-coded check in `internal/session/loop.go:1149` are Nanolathe retry policy where retail's dynamic-blocker retry cadence/count remain unresolved `[R-P1-10]`.

**Decision:** Align search with the static layer: `searchFunc.isPassable` now checks only `Profile.IsPassableFootprint` (terrain + feature/slope/water per `[04 §6.1]`) and **no longer** checks `OccupancyGrid` occupancy — mobile occupancy is ignored at search time `[04 §8.2]`. Mover-vs-mover contention is resolved only at commit via the footprint validator row-major scan `[04 §8.2]` C25, the `MaxVelocity/2` cap + fixed-trig recompute + `±0x7FFFF` clamp without restamp `[04 §8.2]` C24 (existing `ApplyBlocked` stays), and the synchronous clear/commit/stamp pipeline `[04 §8.2]` C22. Building/yard occupancy remains via terrain profile and construction terrain stamps (static); transient mobile occupancy is ignored at search time. `OccupancyGrid.Revision/Bump` is **retained**; its lazy-revalidation consumer is the search expansion's per-node `isPassable` recheck `[04 §7.4]` (no eager purge, no explicit revision comparison needed). `Stamp`/`Clear`/`Block`/`Unblock` continue to bump `rev` for diagnostics and future profile versioning.

Deliberate Nanolathe policy divergences retained with `I9`/`I11` hygiene (one-behavior, unknowns stay unknown):

* `replanDynamicBlock` (`integrate.go:replanDynamicBlock` — deterministic 1-tick cadence `avoidNext = tick+1`, lower pool slot wins, higher slot replans from current anchor to the order goal via `ReplanMove` preserving active-order identity) — explicit Nanolathe avoidance because retail has no recovered automatic repath/priority/wait-queue in the bounded mover graph `[04 §8.2]` negative-bounded. No pushing/slide/yield.

* `landPathFailureRetryInterval = 30`, `landPathFailureMaxRetries = 1` (`integrate.go`) plus `internal/session/loop.go:1149` `if rec.Retries >= 1` — explicit Nanolathe failed-path recovery where retail's retry cadence/count remain unresolved `[R-P1-10]`. The hard-coded `>=1` in `loop.go` is session-owned, so per `OW-3-O` ownership it is **not** changed here; it mirrors the movement-owned constant and is documented as policy, not spec. If the hunk were movement-owned it would reference `landPathFailureMaxRetries`; as session-owned it is reported and left intact.

This is an `I9`/`I11` divergence: bounded retry and priority replans that reject or reroute where retail would have accepted only to keep determinism and avoid unbounded growth; no retail constant is invented.

**Falsifies:** the previous mobile-as-wall search behavior; the previous assumption that `Revision` had no consumer (its consumer is lazy recheck).

---

## How to add to this file

One section per conflict: what the spec says, what was observed and how, the
decision, and which contract it changes. Evidence must be reproducible — a probe
against `~/TotalAnnihilation` that another agent can rerun.
