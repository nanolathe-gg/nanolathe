# Spec conflicts observed against the reference install

`research/retail-executable-spec` is the behavior authority, but a few of its
statements are contradicted by a real, working retail install, and a few
contradict each other. This file records those, with the evidence and the
decision, so nobody "fixes" the code back toward the disproved claim later.

Reference install: `~/TotalAnnihilation` — base + Core Contingency + Battle
Tactics + patch 3.1, probed through this repo's `vfs` package.

Each entry is one statement: what the spec said, what the install or the
executable shows, the decision, and where the contract lives now — a research
section and the design document that owns it. **Status** is one word. *Closed*
means the resolution is settled and the contract is written down; *Open* means
it is not, and the entry names what would settle it. No entry gates work.

**How to add one.** One entry per disproved claim, numbered from the highest
number in use. Numbers are never reused and never renumbered: code cites them
as `SC7` and `docs/SPEC_CONFLICTS.md SC7`, and the design documents cite them
by number too. Evidence must be reproducible — a probe or a test against
`~/TotalAnnihilation` that another agent can rerun. The numbering skips
SC11–SC13; it always has.

---

## SC1 — There is no ten-archive cap we can honor

**Status:** Closed.

**Spec** `[02 §2]` stated a cap of ten successfully mounted local `*.HPI`
archives, the eleventh not mounted.

**Observed:** the reference install has 13 local HPI archives —
`tactics1..8.hpi`, `totala1..4.hpi`, `worlds.hpi` — plus five `.ccx`, eleven
`.ufo` and one `.gp3`, and it plays. A global cap of ten must drop three of
them; in lexical tier order it drops `totala3.hpi`, `totala4.hpi` and
`worlds.hpi`, removing the campaign and most maps, and under any other
enumeration order it drops a different, equally load-bearing three. Any install
with base, both expansions and a handful of downloaded units exceeds ten.

The cap is real but per-invocation. The mount loop carries a ten-valued budget
that decrements only when a candidate is *newly* mounted — validation passed and
the full path not already mounted — and abandons the enumeration when the budget
reaches zero. The orchestrator runs at several call sites, each restarting the
budget, and an already-mounted archive never consumes it, so repeated
invocations converge to every valid local archive mounted.

**Decision:** mount every local HPI, which reproduces the converged retail
state, and record the archive count as a mount note so the discrepancy stays
visible.

**Contract:** `[02 §2]`; DESIGN_CONTENT_VFS §5.

---

## SC2 — `GAMEDATA.TDF` does not exist

**Status:** Closed.

**Spec** `[02 §1]` listed `MOVEINFO.TDF` and `GAMEDATA.TDF` among the hard
requirements.

**Observed:** no file named `gamedata.tdf` exists in any of the 31 mounted
providers. `gamedata/` contains exactly 13 files: `allsound.tdf`,
`buildinfo.tdf`, `category.tdf`, `help.tdf`, `los.tdf`, `meteor.tdf`,
`moveinfo.tdf`, `sidedata.tdf`, `sound.tdf`, `translate.tdf`, `unitview.tdf`,
`version.tdf`, `weapons.tdf`. The hard requirement is the `gamedata/`
**directory**. The mechanism behind the misleading name is separately
established: no `gamedata.tdf` is ever opened, and the `Can't load
GAMEDATA.TDF` box is raised by a missing `SIDEDATA.TDF` — the message text is
simply misnamed `[02 R-MALF-01 §5]`.

**Decision:** a missing `gamedata/` directory, `MOVEINFO.TDF` or `SIDEDATA.TDF`
is fatal; `translate.tdf` is optional; a missing `GAMEDATA.TDF` is not fatal,
because making it fatal would refuse to boot on a genuine retail install. The
retail diagnostic text is reproduced verbatim for the case that really raises
it.

**Contract:** `[02 §1]`, `[02 R-MALF-01 §5]`; DESIGN_CONTENT_VFS §5.

---

## SC3 — Intra-tier ordering barely matters, and we can measure it

**Status:** Closed.

**Spec** `[02 §2]` says retail resolves same-tier archives in host enumeration
order, and calls that "not a portable executable-defined order".

**Observed:** 323 logical paths exist in more than one same-tier provider —
231 `ccdata.ccx` versus `btdata.ccx`, 85 `totala2.hpi` versus `totala1.hpi`,
7 others. Of those 323 only two differ in size: `anims/armhp1.gaf` (75,272
versus 68,136 bytes) and `installres/install.inf`, which is not game content.
The blast radius of choosing a different winner is one art file.

**Decision:** sort lexically inside a tier, which is deterministic, and record
every shadowed provider in the manifest so a differing winner can be named.
There is nothing of retail's to reproduce here, only a measured divergence to
accept.

**Contract:** `[02 §2]`; DESIGN_CONTENT_VFS §5 and §7.

---

## SC4 — `vfs.EntryInfo.Name` is a base name, not a path

**Status:** Closed.

**Spec:** none. This is a repository API trap of the same class, kept here
because it is where an implementer looks.

**Observed:** `EntryInfo.Path` is the logical path and `EntryInfo.Name` is the
base name; `FS.Entries()` returns one entry **per mount** and is not
deduplicated by logical path. Code that groups by `Name`, or treats `Entries()`
as the unique file set, silently produces nonsense — a first pass at the
archive census "found" 8,246 unique paths and top-level directories named
`battleroom.pcx`.

**Decision:** `FS.Manifest()` is the deduplicated view; use it for any census.

**Contract:** DESIGN_CONTENT_VFS §5.

---

## SC5 — The three movement clamps cannot run unconditionally

**Status:** Closed.

**Spec** `[02 "Movement class record"]`: eight keys are read in order, then
three clamps run unconditionally — `maxwaterslope` caps `maxslope`, the
resulting `maxslope` caps `badslope`, and `maxwaterslope` caps `badwaterslope`.
Keys 3, 4, 5 and 7 default to "the profile's current value".

**Observed:** 12 of the install's 15 `CLASS` sections omit `maxwaterslope`.
Compiling them with a zero default and running the first clamp unconditionally
sets `maxslope = 0` for `kbotsf2`, `kbotss2`, `tankbh3`, `tankds2`, `tanksh2`
and `tanksh3` — every land movement class that authored a real slope limit — so
no land unit could climb anything. The zero default was the error: a startup
initializer registered in the CRT function-pointer table pre-fills all 32 class
records before any parse, with `MaxSlope`, `BadSlope`, `MaxWaterSlope` and
`BadWaterSlope` all 255, `MaxWaterDepth` 10000 and `MinWaterDepth` −10000. An
omitted key therefore carries the template value, not zero and not the previous
class's value; the clamps are the identity for a class that omits
`maxwaterslope`; and stock compiles to the authored slope limits
`[04 §6.1 R-DOC04-A]`.

**Decision:** initialize the profile from the startup template and run all
three clamps unconditionally, with no key-presence gate. The gated-clamp
divergence this entry once recorded is gone from `internal/content`.

**Contract:** `[04 §6.1 R-DOC04-A]`, `[02 §5]`, `[02 "Movement class record"]`,
`[fmt tdf]`; DESIGN_MOVEMENT_PATH §5. The consumer contract — per-cell 2-bit
layer stamping, A* blocking only on layer 0 — is `[04 §6.1 R-DOC04-B]`.

---

## SC6 — The two documents disagree on the fringe-anchor encoding

**Status:** Closed.

**Spec A** `[fmt tnt]`, fringe-anchor fields: "at fringe members, signed offsets
locating their anchor cell", marked *supported inference*. **Spec B**
`[04 §6.2]`: the cell stores **target-cell coordinates**, and the resolver
re-reads that cell's feature identifier before classifying. Two encodings of
the same two bytes; only one can be right.

**Observed:** the field is two bytes, and absolute cell coordinates cannot
address a map wider than 256 cells per axis. The reference install's maps run
to 402×408 (`pincushion`) and 384×480 (`Comet Catcher`), so only the offset
reading can be literally true at those widths. The stamp-time fringe writer
contract confirms it: signed offsets, last stamp wins on overlap `[03 §2.2]`.

**Decision:** implement the signed-offset reading. Both readings stay exposed
as named accessors on `PlotCell` with the conflict spelled out, and
`ResolveFeature` is the single resolver every consumer hops through — reclaim,
damage, burning, area candidates and the impact ladder alike.

**Contract:** `[03 §2.2]`, `[fmt tnt]`, `[05 R-FEAT-01 §8]`;
DESIGN_WORLD_VISIBILITY §4 owns it, DESIGN_ECONOMY_CONSTRUCTION and
DESIGN_WEAPONS_PROJECTILES consume the resolver.

---

## SC7 — Sound variants are gathered even when the bare event key is absent

**Status:** Closed.

**Spec** `[02 "Sound category record"]` said an event key absent in its bare
form contributes no variants, because the bare read gates the numbered loop.

**Observed:** stock `gamedata/sound.tdf` authors `select1` (120 occurrences),
`ok1` (76), `cant1` (76) and `arrived1` (63) with no bare form anywhere.
Following the letter would mute selection, move-fail and arrival voices for
every unit on a working install. The loader in fact reads the bare key,
discards the result, and unconditionally gathers `K1, K2, …` until the first
absent index; the bare-gate sentence was a mis-reading of the loop
`[02 R-SND-01 §1]`.

**Decision:** gather the numbered keys regardless of the bare key. The bare
variant, when present, is index 0 and the numbered variants follow; numbering
is contiguous from 1; `<key>text` supplies each caption; a present-but-empty
value counts as a variant. This is retail, not a divergence.

**Contract:** `[02 R-SND-01 §1]`, `[02 "Sound category record"]`;
DESIGN_CONTENT_VFS §5.

---

## SC8 — The two documents disagree on the code-9 re-arm jitter

**Status:** Closed.

**Spec A** `[04 §3.3]`, result code 9: if the record is last, reset its phase
and set "the same randomized deadline" as code 3 — global tick + 30 + a draw
below 15. **Spec B** `[05 "Queue pumping and result codes"]`, result code 9:
"restarts at state 0 with a randomized 30-plus-random-30 retry when no
successor exists". Same field, different jitter bounds.

**Observed:** the two arms are distinct, and each document is right about its
own. Retail adds the same 30-tick base through two paths with different bounds:
code 3 draws below 15, and code 9 on the last record draws below 30. The
earlier analysis missed the second arm because it reaches the shared deadline
calculation through an indirect branch `[04 R-P0-01]`.

**Decision:** implement both arms — code 3 is `tick + 30 + RNG(15)`, code 9 on
the last record is `tick + 30 + RNG(30)` — with the primary and secondary pumps
using the same split. It is observable only as the re-arm cadence of a
completed last order.

**Contract:** `[04 R-P0-01]`, `[04 §3.3]`,
`[05 "Queue pumping and result codes"]`; DESIGN_UNITS_ORDERS_COB §5.

---

## SC9 — `LOS.TDF` declares nine tables and supplies twelve

**Status:** Closed.

**Spec** `[03 §3.2]` clamps the terrain-ray group index "into the parsed
LOS.TDF table range" without saying which count defines that range.

**Observed:** `gamedata/los.tdf` has `[TABLEINFO] { numtables=9 }` and then
`[TABLE1]` through `[TABLE12]` — three more sections than it declares.
Reproduce with `go test ./internal/content -run TestCompileLOSTables -v`, which
asserts both numbers against the install.

**Decision:** the clamp uses the **declared** `numtables`, never the discovered
section count. The declared value is what the engine's table object reports,
and the three undeclared tables are unreachable authoring residue — the largest
declared table already saturates every stock `sightdistance` (the biggest, 450,
quantizes to 14 and clamps to 8). `Catalog.LOS` keeps all twelve parsed
sections so nothing is lost, and `internal/visibility` reads `NumTables` and
never `len(Tables)`. The sprite-mask path keeps its own, unrelated count from
the ten frames of the visibility-mask GAF; the two counts must not be shared.

**Contract:** `[03 §3.2]`, `[03 R-COMP-02 §1]`; DESIGN_WORLD_VISIBILITY §4.

---

## SC10 — The trailing `-Z` is the projection shear, not a second model-space negation

**Status:** Closed.

**Spec** `[03 §2.4]` and `[03 §2.5]` established the load-time half-turn
`-X,-Z`, but left open whether the screen helpers' trailing `-Z` was a second
source conversion (net `-X`) or part of the projection (net `-X,-Z`).

**Observed:** the screen helpers negate only the transient projected Z value
and then compute the established `Z − Y/2` shear; they never store that
negation back into model data. The muzzle query likewise consumes the vectors
produced by the one load-time half-turn with no second sign change. The
trailing `-Z` therefore belongs to projection, not to source conversion.

**Decision:** the load-time half-turn is the sole source conversion. Model
space is mirrored in Z against world space, which is why `ModelVertexToScreen`
and `ModelProjectToScreen` are two names and not one; a flare or muzzle
authored at model `(2,1,-30)` is world `(-2,1,+30)` plus the unit origin.

**Contract:** `[03 §2.4]`, `[03 §2.5]`; DESIGN_PRESENTATION_CLIENT §5.

---

## SC14 — The muzzle query reuses the pristine post-load vectors

**Status:** Closed.

**Spec** `[03 §2.4]` and `[fmt 3do]` establish that piece translations are
converted at load, but left open whether the muzzle query path re-applies the
negation per vertex.

**Observed:** the piece transform rotates and translates the already-converted
piece and center vectors, and the primary-muzzle query uses those same vectors
with no extra sign change.

**Decision:** the muzzle query reuses the pristine post-load vectors; there is
no second sign fixup at query time. Same evidence as SC10, kept separate
because SC10 is about the projection shear and SC14 about per-vertex flare
reuse.

**Contract:** `[03 §2.4]`, `[fmt 3do]`; DESIGN_PRESENTATION_CLIENT §5.

---

## SC15 — The cursor index table was off by one from slot 10

**Status:** Closed.

**Spec** `[07 §8]` published a closed twenty-entry cursor table running index 1
`cursorattack` through index 20 `pathicon`, with `cursorreclamate` at 10.

**Observed:** `anims/cursors.gaf` in the reference install holds **22** named
entries — two more than that table — including `cursorrevive`, which the table
omits entirely, and `cursorprotect`, which the executable never references at
all. The handle array has 22 slots and the init sequence fills slot 10 with
`cursorrevive` **last**, after slot 21, breaking the otherwise ascending order;
transcribing the sequence rather than the slot offsets drops slot 10 and shifts
`cursorreclamate` through `pathicon` down by one. Three independent readers
confirm the corrected numbering: the idle fallback is index 19, which must be
`cursornormal`; the front end installs index 20 across blocking transitions,
which must be `cursorhourglass`; and the shape chooser returns 7 for PATROL, 6
for REPAIR, 5 for FOLLOW, 11 for RECLAIM, 14 for MOVE and 16 for MOBILEBUILD,
every one of which names the right art only under the corrected table.
Reproduce with the asset-guarded `TestGafCursorAssetGuarded` in
`internal/render`.

**Decision:** the table is 0..21 with slot 10 `cursorrevive`, and every index
from `cursorreclamate` up shifts by one. `internal/render/gaf_cursor.go`
carries it and `render.CursorAttack`…`render.CursorPathIcon` are the named
constants. `cursorprotect` is deliberately absent: retail never resolves it.

**Contract:** `[07 §8]`; DESIGN_INTERFACE_HUD_INPUT §5 and
DESIGN_PRESENTATION_CLIENT §5.

---

## SC16 — The active-state `0x20` bit and the empty current-task field

**Status:** Closed.

**Spec** `[07 §9]`: the shared selection eligibility predicate, and the
own-unit inspect predicate behind `cursorselect` in `[07 §8]`, test the
active-state bit `0x20` of the unit runtime flags and an empty current-task
field. This entry originally held that neither test had an implementable
counterpart here — that `0x20` was already claimed by an in-build-stance flag,
and that the only candidate for the current-task field was a per-unit order
guard that is nonzero for every idle unit — so gating on either would make
`cursorselect` and rectangle selection unreachable.

**Observed:** both halves of that reading are wrong. Bit `0x20` of the runtime
flag word is the classifier-eligibility bit: written by the allocator
initializer, cleared by death finalization, cleared for a scripted unit by the
`InitialMission` postlude, and set again by `MakeSelectable`. Nothing collides
with it — the COB port's `INBUILDSTANCE` is a separate byte — and it is set on
every live unit `[04 §3.6]` `[08 R-TRIG-01 §3]`. The "empty current-task field"
is the **remaining-build fraction**: the word the construction step drives from
`1.0` toward `0.0` `[05 R-WORK-01 §1]`, that the constructor seeds
`[04 R-COB-03 §4]`, and that COB port 17 reads. A whole-executable census of
every load and every store of that word finds **no routine of the order
subsystem storing to it** — not the record constructor, either pump, the
return-code epilogue, cancel-all, the expiry helper or the idle-queue refill
`[07 R-WGT-01 §10]`. The per-unit order guard this build had invented was
imitating the construction step's own clamp; the entry's own "rectangle
selection would then select nothing" argument is the proof that no such gate
exists in retail.

**Decision:** the invented order guard and both pump writers are removed. The
eligibility predicate is the selectable status bit `0x20` plus a
remaining-build fraction of exactly `0.0`, and reads no order state at all; the
inspect predicate's existing "construction complete" test *is* the
empty-current-task gate. Rectangle selection uses the same predicate, walking
the local player's slice in ascending slot order into an inclusive rectangle
test. Two clauses stay unmodelled and carry a `TODO(question)` rather than an
entry here: the post-capture grace counter, always zero without a remote
controller, and the carrier's cargo-selectable bit 30.

**Contract:** `[07 R-WGT-01 §10]`, `[04 §3.6]`, `[08 R-TRIG-01 §3]`,
`[07 §8]`, `[07 §9]`; DESIGN_UNITS_ORDERS_COB §5.

---

## SC17 — Order queue caps 64/32 were inside stock-reachable behavior [P1-I09]

**Status:** Closed.

**Spec** `[P2-03]` gave fallback caps `MaxPrimaryQueue = 64` and
`MaxSecondaryQueue = 32` with a diagnostic drop on overflow, plus a pump
iteration cap of 200.

**Observed:** a corpus census over the reference install (278 units, 275 maps,
175 campaign missions, 13 campaigns) finds stock `InitialMission` scripts well
past the primary cap — `ARMCARRY carry1` in `Silent Slayers.ota` produces 105
raw tokens and `fighting under fire.ota` produces 138 — so the 64 cap truncated
a retail mission. The secondary maximum in the whole corpus is 1, and pump
iterations for those queues stay below 200, so neither of the other two bounds
is stock-reachable. Reproduce with
`go test -tags retail -run Corpus ./internal/orders`.

**Decision:** replace both caps with dynamic growth matching retail's
heap-linked list, which has no located cap. The only remaining bounds are an
out-of-memory guard far outside anything stock or any plausible player
shift-queue, and the pump's iteration guard, which the census proves
unreachable; both are the sanctioned bounds-check exception to [I11].
`internal/orders/corpus_caps_test.go` locks the measurement, so a corpus that
grows past a guard fails the test rather than silently truncating.

**Contract:** `[04 §3.3]`, `[05 "Queue pumping and result codes"]`;
DESIGN_UNITS_ORDERS_COB §5.

---

## SC18 — Constructor initialization and reuse [P1-I09]

**Status:** Allocator policy closed; constructor residuals open.

**Established:** `[01 §6]` and `[01 R-PLAT-01 §5]` settle the allocator:
ordinary allocations are not filled and contain whatever the heap held; allocation failure
logs the retail diagnostic, shows a modal error and terminates the process.
The old allocator questions `[GAP T13]` and `[GAP T15]` do not establish any
blanket zero-fill length.

**Decision:** Go's zero-initialization is a deterministic host policy.
Constructor writes must be implemented from each record's owning contract;
zeroed Go storage is not evidence that retail initializes the same fields.
The COB parser's checked malformed-input handling remains a separate host
boundary `[04 §4.1]`; it does not imply a recoverable retail allocation failure.

**Unknown:** the complete constructor/slot-reuse write set and reads before
initialization for records whose owning contract remains incomplete. Settle
these with a per-field writer/reader census and allocation/reuse call-path
trace in the owning category. Record an actual unanswered field question at
its code site, rather than a generic unknown allocator fill count.

The save boundary is retail HAPIBANK account parsing and staged battle
restoration `[08 "Save-file organization"]`. Save truncation is not an
allocator residual.

**Contract:** `[01 §6]`, `[01 R-PLAT-01 §5]`, `[04 §4.1]`;
DESIGN_RUNTIME_DETERMINISM §5 and §7 own this initialization policy.

---

## SC19 — Yard-map parsing is not one-to-one, and bits 5/6 read the wrong flags

**Status:** Closed.

**Spec** `[05 "Geothermal requirement"]` described yard-map characters as
mapping one-to-one into a row-major buffer sized by the packed footprint
extents, named bit 6 a test for "a specific non-reclaimable flag", and
described bit 5 only as "free of blocking features", which `internal/world`
implemented as any feature at all.

**Observed:** three separate disagreements with the shipped data and with the
definition compiler's character loop.

1. **Length.** Forty-six of the 126 stock yard-mapped definitions disagree with
   their own footprint — `ARMSOLAR` authors 27 characters for a 5×5 footprint,
   `ARMESTOR` one for 4×4, `ARMSILO` nine for 5×5, `CORSOLAR` sixteen for 5×5.
   Rejecting the mismatch left every one of those buildings permanently
   unplaceable; the reported symptom was "I cannot build a solar collector at
   all". Retail's loop skips a table-absent character without consuming a cell,
   parks on the final character so it repeats for unfilled cells, and never
   reads past the last cell. Reproduce by comparing the whitespace-stripped
   yard-map length against `FootprintX × FootprintZ` over
   `Catalog.SortedUnitKeys()` on a catalog compiled from the install.
2. **Bit 5.** Metal patches are 3×3 features authored `blocking=0`,
   `reclaimable=0`, `indestructible=1` (`archmetal*`, `drymetal*`,
   `moonmetal*`, `marsmetal*`, `*aquaore*`). `ARMMEX` authors an all-`o` yard
   map and `o` carries bit 5, so blocking on presence rather than on the
   authored `blocking` flag makes the metal extractor unplaceable on its own
   deposit. Trees and rock clutter do author `blocking=1` and still block.
3. **Bit 6.** The validator reads the indestructible flag, not the reclaimable
   one; `reclaimable` is never read by the validator at all.

**Decision:** `ParseYardMap` fills the footprint by retail's character loop and
returns neither a length nor an unknown-character error; `ValidatePlacement`
reads the feature definition's authored blocking flag for bit 5 and its
indestructible flag for bit 6, and counts a geothermal match only on cells
whose own yard byte carries bit 7. A fringe cell whose anchor hop finds nothing
is not blocking. The control-byte table, the bit roles and the geothermal rule
itself are unchanged.

**Contract:** `[05 "Geothermal requirement"]`, `[fmt fbi]`;
DESIGN_WORLD_VISIBILITY §4, consumed by DESIGN_ECONOMY_CONSTRUCTION.

---

## SC20 — Cursor-to-ground is a search along Z, not an inverse projection

**Status:** Closed.

**Spec** `[07 §8]` said only that world-space, unit, feature and terrain/radar
tests use the camera transform, which `internal/camera` implemented as the
algebraic inverse of `WorldToScreen` at height zero.

**Observed:** that inverse is wrong wherever the ground is above zero, and
visibly so. Terrain tiles are presented flat while world objects carry the
half-height shear `screenRow = Z − (height >> 1)` `[03 §2.5]`, so an order
given at a pixel put the unit half the terrain height north of it — the
reported symptom was a commander moving to a space above the click. Retail
resolves the pointer with a bounded search instead: clamp into the map
rectangle, start eight cells south of the clicked row, walk north up to nine
cells comparing each candidate's `max(height, seaLevel)` projection against the
clicked row, then bracket and interpolate. Reproduce by sweeping every map
pixel of a stock map through `Terrain.CursorToWorld` and projecting the result
back — exact on flat ground, within a few pixels on steep slopes (retail's own
interpolation residue), against an error of half the terrain height for the
algebraic inverse.

**Decision:** `Terrain.CursorToWorld` implements the search and is the single
cursor-to-ground conversion the battle screen uses; `Camera.ScreenToWorld`
keeps its pixel-level meaning and is no longer used for ground orders. The
map-rectangle clamp is a behavioral consequence worth knowing: a pointer past
the map edge resolves to the edge, so an off-map build ghost is legal rather
than out of bounds.

**Contract:** `[07 §8]`, `[03 §2.5]`; DESIGN_INTERFACE_HUD_INPUT §5.

---

## SC21 — `BMcode` marks structures, not factories

**Status:** Closed.

**Spec** `[fmt fbi]` gave `BMcode` as "`0` for stationary factories
('build-machine'), `1` for everything else — distinguishes pad factories from
mobile builders", and flagged it as a community guess.

**Observed:** a census of the compiled catalog partitions the 278 stock unit
definitions into exactly five buckets:

```
BMcode=0 yard=yes canmove=no  builder=no   103
BMcode=0 yard=yes canmove=no  builder=yes    2
BMcode=0 yard=yes canmove=yes builder=yes   21
BMcode=1 yard=no  canmove=yes builder=no   122
BMcode=1 yard=no  canmove=yes builder=yes   30
```

`BMcode == 0` and "has a yard map" are the same set with no exception: that is
the structure class, and it is what `[04 §6.2]` already meant when it said the
yard map is parsed only when BMcode is zero. The census also disproves a second
assumption this repo held independently of any document: **stock factories
author `CanMove = 1`** (`ARMVP`, `ARMLAB` and `ARMHP` are all `BMcode=0,
CanMove=1, Builder=1`), so a `Builder && !CanMove` factory test failed every
stock factory — clicking a vehicle in a factory's build menu armed a placement
ghost instead of queueing it.

**Decision:** the building-class status bit is set from the authored `BMcode`
at creation, and the placement-versus-queue branch keys on the **product's**
`BMcode`, which is what retail's build-button handler tests: it arms the
MOBILEBUILD latch and stores the product id only when the product's BMcode byte
is zero, and otherwise falls through to the immediate queue path. The
mobility-keyed helpers survive only as the fallback for a product the catalog
cannot resolve.

**Contract:** `[04 §6.2]`, `[07 §9]`, `[fmt fbi]` (its `BMcode` row corrected);
DESIGN_ECONOMY_CONSTRUCTION §5, with the interface half in
DESIGN_INTERFACE_HUD_INPUT and the order half in DESIGN_UNITS_ORDERS_COB.

---

## SC22 — Static-layer path search and the dynamic-block policy [OW-3-O]

**Status:** Closed.

**Spec** `[04 §8.2]`: mobile units are not permanent A* walls in the map-load
layer; path search uses the static terrain, feature and yard/building layers,
while final mobile contention is arbitrated at commit, where mobile units are
hard blockers even though their projected motion never enters the expansion
heap.

**Observed:** the search predicate checked the occupancy grid for every
neighbor and rejected occupied cells, turning transient traffic into static
obstacles and feeding an invented retry-and-removal policy. The opposite
blanket claim — that mobile occupancy is simply ignored at search time — is also
wrong, because it omits the request-initialization revision pass. A whole-image
census additionally found no yield or sidestep owner anywhere: the commit
validator never reads the class layer or the occupant age, so the age gate is
search-only `[04 R-COLL-01 §7]`.

**Decision:** the search predicate holds no direct occupancy lookup; it reads
the request's movement-class layer. At request initialization that layer's
watermark is armed at `max(tick, 30) − 30`, recently committed mobile
footprints are re-stamped, and the occupant-age gate lets a recent occupant
through while making an older one block its re-stamped cells. Existing heap
entries are not purged; expansion rechecks passability lazily as each entry
opens. The scheduler and the expansion receive no blocker identity, velocity or
projected destination, and no collision-triggered replan exists — there is no
lower-slot priority, no avoidance cadence and no replan submission after a
rejected commit. Mover-versus-mover contention stays authoritative at commit
through the row-major footprint validator, the half-speed clamp response and
the synchronous clear/commit/stamp sequence. Building and yard occupancy remain
static inputs. The liveness mechanism is the age gate itself: a blocked unit
stops advancing its last-stamp tick, becomes a hard search block for others
after 30 ticks, and its own repath — re-armed when a route is installed and the
mover is blocked or has fewer than two points, throttled to one request per 60
ticks with no retry ceiling — routes around. The separate occupancy revision
counter is diagnostic and must not be conflated with the class-layer watermark.
Outer yield/replan and ordinary open-group liveness remain **Unknown**.

**Contract:** `[04 §8.2]`, `[04 §6.1 R-DOC04-B]`, `[04 R-MOV-02A]`,
`[04 R-COLL-01 §7]`, `[04 R-MOV-01 §7]`; DESIGN_MOVEMENT_PATH §5.

---

## SC23 — `gravity = 0` maps cancel every `AirStrike` order

**Status:** Closed.

**Spec** `[04 R-AIR-01 §8]` (Established): the bombing run's release-point leg
computes the release lead as `t = sqrt((2 · cruisealt) / gravity)`, and before
dividing it reads the map's `gravity` word and, if it is zero, returns the
cancel-all code, emptying the bomber's whole order queue. There is no fallback
gravity.

**Observed:** an asset census of the 275 stock maps finds none that authors,
omits or defaults the key to zero. Every `[GlobalHeader]` authors `gravity`
explicitly, none negative; the minimum authored value is 8, the maximum 445,
and 192 of 275 author 112. The bound is therefore unreachable on shipped
content — but a map that did author zero would make bombers un-orderable to
attack in retail, and substituting a default gravity would invent behavior.

**Decision:** clone retail and cancel all orders on zero gravity. The terrain
loader tests the parsed value, not key presence: an omitted key takes the OTA
parser's integer default of 0 and passes the non-negative test, which is what
`[03 §2.2]` requires. Wind and `tidalstrength` carried the same
presence-versus-value inversion and were corrected with it. If a map carrying
`gravity = 0` ever turns up, a probe under `probes/` should show the
cancellation visibly before this entry is promoted from a sanctioned bound to
an observation; manual retail observation is the decider, and the executable is
not to be automated.

**Contract:** `[04 R-AIR-01 §8]`, `[03 §2.2]`, `[fmt ota]`;
DESIGN_MOVEMENT_PATH §5, consumed by DESIGN_WEAPONS_PROJECTILES.

---

## SC24 — Retail never compiles loose `units\*.FBI` or parses loose `weapons\*.tdf`

**Status:** Closed.

**Spec** `[02 R-CAT-01 §4]` (Established): the unit catalog loader's gate drops
every FBI that does not come from an archive — silently, with the definition's
archive bit cleared — and never parses a loose `weapons\*.tdf`. The switch that
would enable loose files is a constant in the shipped image with no writer. The
community "units must be packed" rule is the executable's own.

**Observed:** the catalog discovery helper used to reopen a shadowed archive
copy after the overlay selected a loose winner. That changed both the winning
bytes and their provenance; the loose FBI also skipped the required
parse-before-drop order.

**Decision:** follow the retail gate. Unit discovery reads and parses the VFS
winner, then silently drops a loose FBI; a loose winner without `[UNITINFO]`
still ends the parse stage before later entries. Weapon discovery ignores a
loose winner before parsing and never substitutes a shadowed archive copy.
Focused compiler fixtures declare archive provenance, so no host-fixture
exception broadens the ordinary catalog path.

**Contract:** `[02 R-CAT-01 §4]`; DESIGN_CONTENT_VFS §5 and §7.

---

## SC25 — Stock `loadgame.gui` authors fewer gadgets than the section lists

**Status:** Open.

**Spec** `[08 R-SAVE-02 §1]` names the save/load screen's gadget vocabulary
including `TITLE`, `CAMPAIGN`, `CAMPTEXT` and `LoadGame`.

**Observed:** the reference install's `guis/loadgame.gui` (4,130 bytes) authors
`HEADER GAMES LOAD CANCEL SLIDER GAMENAME GAMETYPE MISSION TIME SIDE RADAR DIFF
DELETE SaveGame` — none of the four above. The screen code sets those four by
name, so the writes are inert rather than wrong.

**Decision:** keep the writes and treat the four as optional gadgets. The
section's list is a superset of stock content, not a contract stock content
satisfies; a mod or a later patch may author them.

**What would settle it:** a census of the other language and patch archives'
copies of the layout, showing whether any stock variant authors the four.

**Contract:** `[08 R-SAVE-02 §1]`; DESIGN_SESSIONS_AI_SAVE §5 and §7.
