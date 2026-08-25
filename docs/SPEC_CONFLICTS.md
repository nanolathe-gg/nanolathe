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

## SC8 — The two documents disagree on the code-9 re-arm jitter

**Spec A** `[04 §3.3]`, result-code 9: if the record is last, "reset its phase
and set **the same randomized deadline**" — i.e. the code-3 formula, global
tick + 30 + a random value below 15.

**Spec B** `[05 "Queue pumping and result codes"]`, result-code 9: "restarts at
state 0 with a randomized **30-plus-random-30** retry when no successor
exists".

Same field, different jitter bounds (rand < 15 vs rand < 30).

**Decision:** implement Spec A (+30 + rand(15)) because document 04 is the
dedicated orders/queue section and its row is internally consistent with its
own code-3 entry; the pump site carries a `TODO(question)` naming both
readings. Observable only as the re-arm cadence of a completed last order.

**Falsifies:** neither document; it picks between them. Revisit only with
executable evidence.

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

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

**Decision:** implement `H_A` sole load-time conversion; reject `H_C` (rr-06_addendum, direct-static). Flare/muzzle world at `(2,1,-30)` is `(-2,1,+30)` plus unit origin.

**Falsifies:** `H_C` reading; no prior SC.

---

## SC14 — Flare/muzzle per-vertex reuse vs second NEG

**Spec** `[03 §2.4]` / `research/formats/3do.md` "Model facing is −Z": piece translations converted at load, but muzzle query path could have re-applied `NEG`.

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

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

## How to add to this file

One section per conflict: what the spec says, what was observed and how, the
decision, and which contract it changes. Evidence must be reproducible — a probe
against `~/TotalAnnihilation` that another agent can rerun.
