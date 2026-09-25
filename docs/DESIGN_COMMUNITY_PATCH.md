# Design — Community patch profile (3.9.x)

How Nanolathe supports the behaviour of the TA community patch line — the
TADR `tdraw.dll` engine component, its recorder ports and its authored content
keys — as a third selectable gameplay profile, **Community 3.9**, sitting
between the retail baseline **Strict 3.1** and the default **Modern**, and how
a content set or a player configures which of its features are live.

This document owns the *policy and the mapping*: which patch contract becomes
which Nanolathe decision, on which seam, with which default per content set,
and what stays out. It restates no arithmetic; every behaviour cites the
extension contract that specifies it, and the retail baseline it departs from
stays cited from the retail specification. The mechanism it extends — seams,
registry, binding, allocation and switch timing — is
[DESIGN_GAMEPLAY_RULES](DESIGN_GAMEPLAY_RULES.md); the two changes this design
makes to that mechanism are stated in §2 and nowhere else.

Evidence: [community patch engine behavior](../research/extensions/community-patch-engine.md)
(the MIT source at the pinned revision; contract IDs `CP-*` below are its),
[extended script ports](../research/extensions/script-ports.md),
[non-retail weapon target keys](../research/extensions/weapon-target-keys.md),
[community patch pathfinding](../research/extensions/community-patch-pathfinding.md)
and [mod engine-package compatibility](../research/extensions/mod-engine-compatibility.md).
Every claim those documents make was re-verified sentence by sentence
against the pinned source on 2026-09-21, and the corrections were landed
before this design was written, so the rows below cite the corrected text.
The maintainer approved the decisions in §11 on 2026-09-21. That record
owns the implementation policy, including the explicit differences from the
patch source.

## 1. Purpose and boundary

**In scope.** Everything the patch changes in a single-player or skirmish
simulation (its Tier 2), everything it lets content authors say (Tier 1), and
the recorder's script ports. Each becomes one of:

- a **rule** on an existing or new gameplay seam, answered as retail under
  Strict 3.1, as the patch under Community 3.9 and as the patch plus
  Nanolathe's own policy under Modern;
- a **parameter** of the session — a pool capacity, a step allowance, a
  margin, a multiplier — carried in the feature table (§3) and read once at
  composition;
- a **content reader** — an authored key parsed onto the compiled definition
  in every mode and consumed only by a rule that is bound;
- or an explicit **not adopted**, with the reason recorded (§8).

**Out of scope, by design.** The patch's session and wire mechanics (its Tier
3: vote-reject, take arbitration, share guard, lag guard, identity audit,
chat-envelope packets, anti-cheat exchange) stay recorded in the research and
unimplemented, because Nanolathe has no networking. Its renderer and host
features (Tier 4: megamap, whiteboard, chat drawer, counters, colours,
nanoframe preview) are host preferences and map onto Nanolathe's own
presentation controls (§7); they are never part of a gameplay profile. Its
tooling (Tier 5) is not engine behaviour.

**What "compatible" means here.** Same outcome as the corresponding `tdraw`
build where the source states the arithmetic, given the same inputs — not the
same battle. Whole-battle equality with a patched executable is unattainable:
the patch seeds its deterministic wind from a network identifier and the
retail random seed is process-derived, so no two runs share a stream anyway.
The testable claim is per-contract: the rule answers as the source specifies
(§10).

## 2. Three reserved rule sets

**Today.** Two reserved sets, `strict-3.1` and `modern`; a registered set
derives from one of them and `RuleSet.Base` reduces to that word
([DESIGN_GAMEPLAY_RULES §1](DESIGN_GAMEPLAY_RULES.md#1-shape), §8).

**This design.** Three reserved sets, in a strict derivation order:

| Name | Derives from | Carries |
|---|---|---|
| `strict-3.1` | — | the retail executable, including documented faults |
| `community-3.9` | `strict-3.1` | the patch's single-player simulation behaviour, selected by the feature table (§3) |
| `modern` | `community-3.9` | the community behaviour plus every approved Nanolathe Modern policy |

Modern *embeds* the community implementations the way it embeds Strict today:
`combat.ModernRules` composes `combat.CommunityRules`, which composes
`combat.StrictRules`, and each layer overrides only the answers its contract
changes. Where a Modern policy and a community feature answer the same
question, §4 says which wins and why; the default is that the Modern policy
wins, because Modern is Nanolathe's own product and the community feature is
compatibility.

Consequences for the mechanism, each a deliberate change to
[DESIGN_GAMEPLAY_RULES](DESIGN_GAMEPLAY_RULES.md):

1. `RuleSet.Base` is three-valued: the reserved word the set derives from.
   The only strict-versus-modern readers today are the options panel's stage
   (`session.BaseModeOf`), which becomes a three-stage control (Strict 3.1 /
   Community 3.9 / Modern), and the save unit-limit seam, which already has
   its own interface. `Session.Gameplay` keeps carrying the base word.
2. `completeRuleSet` fills an unstated seam from the declared base, which
   may now be `community-3.9`; a registered set may therefore derive from the
   community set without taking Modern's policies.
3. Three fingerprints are locked, not two: the headless 6000-tick and the
   simulation-cost fingerprints for each reserved set. Strict's must not move
   when this lands; Community's and Modern's move once, when a feature lands,
   and the landing commit states the expected difference.
4. The reserved names are `strict-3.1`, `community-3.9` and `modern`; the
   registry refuses all three for registration.

The name is the patch line's public version family, not a build: the seven
`tdraw` build profiles are feature *tables* under the one set (§3.3), and a
content profile selects its table. A registered set remains the way to change
an *answer* the tables cannot express.

## 3. The feature table

The patch configures itself three ways: compile-time profile flags (one
matrix per content package), preference-file integers (limits) and
author-facing content keys (per definition)
([community patch engine behavior](../research/extensions/community-patch-engine.md) §3, §4, §6).
Nanolathe keeps the third as it is — content keys are read by the catalog —
and folds the first two into one **feature table**: a closed, typed value
describing which community features are live and with which parameters.

### 3.1 Shape

```go
package community // internal/community

// Features is the resolved table one session runs under. It is a value,
// complete after resolution, immutable afterwards, and every field has a
// documented Strict answer (its zero value, which is "as retail").
type Features struct {
    // Switches: one per outcome-changing contract that has no parameter.
    ConstructionKickout       bool // CP-CON-1
    GuardingBuildersHold      bool // CP-CON-2 (the option itself is per player, §4.3)
    PatrollingBuilderFilters  bool // CP-CON-3
    ReclaimToggleKeepsBuild   bool // CP-CON-4
    StructureRotation         bool // CP-CON-5
    AreaDamageOverflow        bool // CP-DMG-1
    AreaDamageDedupCap        bool // CP-DMG-1 (the cap half)
    GridClaimTieBreak         bool // CP-DMG-2
    TransportedExplosions     bool // CP-DMG-3
    BuildWeaponSlotGuard      bool // CP-DMG-5
    AntinukeCircularCoverage  bool // CP-FIX-5
    AlliedJammingIgnored      bool // CP-FIX-6
    ResurrectionFinalization  bool // CP-FIX-1
    WeaponTargetKeys          bool // CP-WPN-1..5: nottoair, nottounderwater, surfacefire, notoverwater, notoverland, nomapweaponalert
    Veterancy                 bool // CP-UD-1
    SchemaUnits               bool // CP-UD-3
    AirCorpseFall             bool // CP-ENV-2
    ScriptPorts               bool // recorder ports 32 and 69–75 (the eight content uses; §4.5)
    MexSnap, WreckSnap        bool // click snap (command-time, CP-CON-6, §4.6)

    // ProTA 4.8 package switches (§4.7): false in every shipped table,
    // enabled only by a content profile's gameplay block or a player override.
    AIDifficultyIncome        bool // computer-player Easy/Medium/Hard 0.5/1/4 income
    AIStockpileProducts       bool // armed-building record on the resource/queue task
    TargetLockRelease         bool // retained-target release in the maintenance scan
    AIApplianceEnergy         bool // energyuse sign-byte appliance selector
    AIBuilderStopThreshold    bool // capture-capable placement cutoff at ten
    WorkingWeaponsAutonomous  bool // five ground work handlers leave weapons autonomous
    AttackSingleSlotTake      bool // Attack_Chase / Suppress take one slot, not two or three
    MapFeatureOwnerEleven     bool // terrain-file features stamped owner 11, drawn without LOS
    ResurrectionTextFix       bool // "Resurrection failed" spelling

    // Parameters: zero means "as retail" for every one of them.
    RepairRate                RepairRate // CP-DMG-4: Enabled, RepairMultiplier, SelfHealMultiplier (1..100)
    OffMapAircraftMarginTiles int        // CP-ENV-1: 0 = retail (no off-map targeting)
    ProjectileCapacity        int        // CP-LIM-1: 0 = retail 300
    ExplosionCapacity         int        // CP-LIM-1: 0 = retail
    DebrisCapacity            int        // CP-LIM-1: 0 = retail
    PathStepAllowance         int        // CP-LIM-2 AISearchMapEntries: 0 = retail 1333
    UnitLimit                 int        // CP-LIM-2 UnitLimit: 0 = the configured setting
    MexSnapRadius, WreckSnapRadius int   // click snap radii, capped by the table's own maxima
}
```

The table is the whole community configuration. There is no second registry
and no per-feature Go interface: a **switch** is read by the community
implementation of the seam that owns the decision, and a **parameter** is read
once at composition by the owner that sizes or bounds something. Nothing in a
tick reads the table by name; a service reads its own copy of the answer it
needs, which the composer projected onto it beside the rule set — exactly the
"state with its owner" rule of
[DESIGN_GAMEPLAY_RULES §3](DESIGN_GAMEPLAY_RULES.md#3-allocation-rules). The
cached rule objects stay zero size; the table lives on the session and is
copied field by field onto the services that ask (`combat.Service.Community`,
`construction.Service.Community`, the movement scheduler's step allowance, the
pool sizes at battle entry).

### 3.2 Sources and precedence

The table is resolved once, at composition, from four sources. Later sources
override earlier ones field by field; an absent field means "keep":

1. **The reserved set's base table.** Strict 3.1 is the zero table. Community
   3.9 and Modern start from the *mainline* table (§3.3, D2).
2. **The content profile's `gameplay` block.** A content profile
   ([DESIGN_CONTENT_VFS §5 "Content profiles"](DESIGN_CONTENT_VFS.md)) may
   carry a `gameplay` object naming the build profile its content was
   authored for (`"table": "escalation"`) and overriding individual fields.
   This is how a content set declares what it needs: Escalation declares the
   repair multipliers, extended weapon IDs and the 32-tile margin its units
   are balanced against, exactly as its `tdraw` build compiles them in.
3. **The settings file.** `gameplayFeatures` — a JSON object of the same
   shape — is the player's persistent override, written by the options page
   and hand-editable.
4. **The command line.** `--gameplay-feature name=value`, repeatable, for
   probes and benchmarks.

Under **Strict 3.1 every source is ignored** and the table is zero: the
retail baseline cannot be configured, which is the whole point of having it.
The composer reports the resolved table's digest beside `rules` in the
headless, benchmark and capture reports, so a run is reproducible from its
report alone.

*Why the content profile is allowed to say this.* The profile is load-time
data and by the existing rule must not become a hidden gameplay selector
([DESIGN_GAMEPLAY_RULES §9](DESIGN_GAMEPLAY_RULES.md#9-extending-the-existing-mechanism)
"Content profiles are a separate input"). That rule protects Strict 3.1, and
Strict still ignores the block. Within the community set the block is not
hidden: it is a named, reported, overridable declaration of the build profile
the content was authored for, which is the one fact the patch itself makes
compile-time. A content set authored against Escalation's 3× repair simply
does not play right without it, and asking every player to hand-configure it
would reproduce the patch's "mixed fleet" problem in single player. See D1.

### 3.3 The shipped tables

`internal/community/tables.go` embeds one table per `tdraw` build profile at
the pinned revision, transcribed from the source's feature matrix
([community patch engine behavior](../research/extensions/community-patch-engine.md) §3.1)
and the shipped preference defaults (§4.1): `ota`, `prota`, `escalation`,
`tazero`, `bta`, `mayhem`, `twilight`. The **mainline** table is `prota` —
the maintainers' "all features enabled" build and the one the ProTA package
ships for otherwise-retail content — and it is what Community 3.9 and Modern
start from for retail content (D2). The shipped content profiles gain a
`gameplay` block: `escalation.json` → `escalation`, `prota.json` → `prota`,
`zero.json` → `tazero`, `retail.json` → nothing (the set's base table
applies). The three profiles Nanolathe ships no content table for (`bta`,
`mayhem`, `twilight`) are selectable by name from the settings file or a
user-authored profile.

The five ProTA 4.8 package switches of §4.7 are false in **every** shipped
table, `prota` included. They are behaviours of that historical package's
engine loader, not of any `tdraw` build profile, so enabling them in the
mainline table would change the computer player for retail content under
Community 3.9 and Modern. The fields are omitted from the canonical JSON while
false, so every shipped table keeps its digest; an enabled switch enters the
digest by name.

Two rows of the matrix are not table fields because they are not gameplay:
the weather-report and megamap rows (host presentation, §7) and the
compile-time "extended weapon IDs" flag, which the content profile's
`limits.weapons` already expresses (§5).

## 4. The feature catalogue

One row per contract. *Seam* names the interface that carries the decision
and whether the method exists today. *Strict* is the retail baseline the
existing implementation already gives. *Community* is the patch behaviour and
the table field that enables it. *Modern* is what the default profile does
and, where it differs from Community, why.

Class labels are the source's: **A** bit-identical where not triggered, **B**
outcome-changing, **P** presentation, **S** session. Note that the source's
class A is judged against the *patched executable's* degenerate paths; two
class-A contracts (CP-FIX-5, CP-FIX-1) change outcomes relative to *retail*
and are therefore rules here, bypassed by Strict, whatever the source calls
them.

### 4.1 Limits and pools

| Contract | Seam / owner | Strict | Community | Modern |
|---|---|---|---|---|
| CP-LIM-1 pools (B) | session composition parameter → `pool` capacities | projectiles 300, the retail explosion and debris caps `[06 §5.1]` `[01 §6.1]` | 3000 / 3000 / 1000 per the table; allocation above the cap still silently fails, as retail does | same as Community |
| CP-LIM-2 `AISearchMapEntries` (B) | movement scheduler parameter (`path` step allowance, today a literal 1333 `[04 R-PATH-01 §10]`) | 1333 | the table's `PathStepAllowance`, 66650 in every shipped table | same |
| CP-LIM-2 `UnitLimit` (B) | the existing configured unit limit ([DESIGN_CONTENT_VFS §5](DESIGN_CONTENT_VFS.md)) | the setting | the setting, unless the table names a limit, capped at Nanolathe's 3276 | same |
| CP-LIM-2 `UnitType`, `SfxLimit`, composite buffer | content profile `limits` / renderer | already expressed by `limits.units`; effects and composite sizes are Nanolathe host sizing | — |
| CP-LIM-3, CP-LIM-4, CP-LIM-5 | — | not applicable: Nanolathe's build-menu and download compilers have no fixed-size copy to overrun; display minimums are host policy | — |

The auxiliary pool in CP-LIM-1 is the shatter geometry paired with the fixed
effect owner ([04 R-COB-04 §3], CP-LIM-1 "Auxiliary owner mapping"). The
explosion capacity sizes both existing stores; the Community geometry
allocator uses the patch's cyclic first-free search, while Strict retains
lowest-free allocation.

Capacity overrides are rejected above their owner's representable bounds:
32767 projectile records (signed compaction markers), 65535 effects (unsigned
fragment identities), and 2147483647 scheduler steps. Zero retains the retail
answer; accepted values are used without silent clamping.

Pool capacities, the path allowance and the unit limit are battle-entry
parameters. A live rule switch changes future policy decisions but retains
these allocated owners and their entry parameters, as DESIGN_GAMEPLAY_RULES §5
retains other already-created state. Starting a new battle under Strict is
required for retail capacities. Reports include the entry table separately
from the current table so this distinction remains reproducible. Modern and
Community 3.9 save restores preserve the saved unit layout through the existing
`ModernUnitLimit` save rule, and their writers record the live layout. This is
user-authorized Nanolathe Modern policy shared for save compatibility, not
patch parity; see [Modern save unit limits](DESIGN_SESSIONS_AI_SAVE.md#modern-save-unit-limits).
Strict retains its pre-load configured layout; campaign limits remain the
mission's in every mode.

The stockpile reload-word clamp is likewise a battle-entry operation, matching
the patch's weapon loader. It derives a private catalog only when a reload word
changes, preserves the authored catalog and its identity, and survives a live
rule switch. The corrupt-slot decision remains a live order rule. A fresh
Strict battle always uses the authored reload word.

The projectile cap is the one that matters: retail drops a shot when its pool
is full, so a 3000-record pool is a visible gameplay change and the pool's
array size becomes a battle-entry parameter rather than a compile-time
constant. Q3 asks what the retail save does with more than 300 records.

### 4.2 Combat and damage

| Contract | Seam / owner | Strict | Community | Modern |
|---|---|---|---|---|
| CP-WPN-1 `nottoair` (B) | `combat.Rules.AdmitTarget` (new; asked at the shared per-weapon auto-aim check the acquisition path already owns, [DESIGN_WEAPONS_PROJECTILES §2.3](DESIGN_WEAPONS_PROJECTILES.md#23-the-shot-admission-gate)) | key parsed, ignored `[02 R-KEYS-01]` | airborne target rejected; wins over `surfacefire` | same |
| CP-WPN-2 `nottounderwater` (B) | same method | ignored | target whose bounding-box top (position plus the definition's vertical extent, both truncated to whole world units) is at or below sea level rejected, at the convergence point of every water-weapon allow path | same |
| CP-WPN-3 `surfacefire` (B) | same method, the script-action ATTACK gate (slot 0 only) and the guidance-kill site in projectile motion | ignored | both above-sea-level rejections and the can-aim depth sequence bypassed; guidance kept **unconditionally** for a tagged self-propelled projectile (retail kills it at or above sea level) | same |
| CP-WPN-4 `notoverwater` / `notoverland` (B) | `combat.Rules.SlotMayFire` (new; asked in the per-tick weapon-slot loop after the reload decrement and before aim, the position the contract states) | keys parsed, ignored | the firer's terrain-versus-sea-level gate; suppressed slot keeps reloading and its target, turret holds | same |
| CP-WPN-5 `nomapweaponalert` (B/P) | `combat.Rules.DetonationBroadcast` (new) and the HUD's alert-dot producer | ignored | damage-0 attacker-less projectile skips the area-damage-and-broadcast call; no minimap alert | same |
| CP-WPN-6 extended weapon IDs (B) | content profile `limits.weapons` (exists) | 256 | the profile's table size; Nanolathe admits IDs up to that size and refuses beyond it, where the patch loads ≥ 4096 into slot 0 (documented difference, T1 semantics preserved for all authored content) | same |
| CP-WPN-7 `reloadbar` (P) | HUD (§7) | — | — | — |
| CP-DMG-1 area-damage overflow (B) | `combat.Rules.AreaVictims` (new; replaces the two-word cell read of the sweep, [DESIGN_WEAPONS_PROJECTILES §2.9](DESIGN_WEAPONS_PROJECTILES.md#29-damage)) | two occupancy words per cell, the twenty-entry memory `[06 §9.3]` | the eight-pair enumeration with six per-cell overflow slots indexed per tick, per-explosion de-dup, saturation counted (Q1) | same enumeration and de-duplication with no six-slot cap, per Q1 |
| CP-DMG-2 grid claim tie-break (B) | `movement.Rules.ClaimConflict` (new; asked at the occupancy commit, [DESIGN_MOVEMENT_PATH §3.3 C22](DESIGN_MOVEMENT_PATH.md#33-ground-steering-and-collision--c20c25), and at the building/yardmap stamp) | first claimant keeps the cell `[04 R-COLL-01 §1]` | the lower unit index wins, unsigned strict less-than, self re-claim keeps; applied at the re-claim and per-move stamp paths for the ground slot, the air slot and the building stamp alike, so an inactive owner's lingering unit can be displaced | same |
| CP-DMG-3 transported explosion keys (B) | `combat.Rules.DeathWeapon` (new; asked where the death explosion's weapon is chosen `[06 §12.1]`) | `explodeas`/`selfdestructas` | the transported override under the three conditions; unknown weapon name falls back | same |
| CP-DMG-4 repair-rate fix (B, balance) | `construction.Rules.RepairContribution` (new; replaces the heal packet the repair and `healtime` executors form, [DESIGN_ECONOMY_CONSTRUCTION §2.2](DESIGN_ECONOMY_CONSTRUCTION.md#22-internalconstruction)) | the retail helper `[05 R-WORK-01 §3]` | the module's 64-bit owed-HP and banked-remainder algorithm (two target-tagged banks per repairer) with the table's multipliers, the ten-step guard order as stated; off in every table but `escalation` (Q4) | same |
| CP-DMG-5 build-weapon slot guard (A) | stockpile build path `[06 §11.1]` | the established malformed arms | reload divisor 0 clamped to 1; corrupt slot index redirected to the corrupt-order exit | same |
| CP-FIX-5 antinuke coverage (A by the source, **B against retail**) | `combat.Rules.InterceptorCoverage` (new; the acquisition scan of [DESIGN_WEAPONS_PROJECTILES §2.11](DESIGN_WEAPONS_PROJECTILES.md#211-stockpile-and-interceptors)) | inclusive axis-aligned square on the aim point `[06 §11.2]` `[06 R-WPN-05 §10]` | circular test: reject only when the squared horizontal distance is strictly greater than the squared `coverage`; own, non-targetable and already-claimed projectiles skipped; first qualifying record in pool order, not the nearest; the minimap ring drops its fixed 512 subtraction | same |
| CP-UD-1 veterancy keys (B) | `combat.Rules.VeteranLevel` (new; the tier reader every consumer already shares `[06 §4.2]` `[06 R-DMG-01 §2]`) and the capture cost in `orders` `[05 "Capture"]` | `min(kills/5, 5)`, `kills/5` capture, `kills/12` spread | the authored thresholds and rate: bounded level then clamped (25 for damage taken, 16 for reload), bounded for damage dealt, unbounded for the captured unit's cost, `kills > thresholds[0]` for the lead gate; absent keys are the retail identity, which is the lock test | same; the `Vet<n>` HUD label is a host option (§7) |

### 4.3 Construction

| Contract | Seam / owner | Strict | Community | Modern |
|---|---|---|---|---|
| CP-CON-1 build under own units + kickout (B) | placement admission: `construction.Rules.AdmitSiteOccupants` (new, asked by the placement validator's occupancy test, [DESIGN_WORLD_VISIBILITY §3.1](DESIGN_WORLD_VISIBILITY.md#31-terrain-and-placement--w1w13)); evacuation: the existing `construction.Rules.YieldObstruction` | own units block placement; the builder waits `[04 R-ORD-01 §5]` | a site over the ordering player's own units is admitted (the source has no mobility test; Q2 decides whether Nanolathe adds the author's "mobile" intent) with the yellow preview and the inclusive wait-counter limit of 20; at the wait branch the contract's kick algorithm moves each occupant: the target-invested-energy rule, the unconditional random bearing per considered unit (Q3), the full-circle sweep, the order rewrite with its resumed build and its dropped queue | placement admission adopted; the inclusive wait-counter limit stays 10 and evacuation stays [Modern construction-site yielding](DESIGN_ECONOMY_CONSTRUCTION.md#modern-construction-site-yielding), which already owns the same moment and issues ordinary orders (D3) |
| CP-CON-2 guarding builders hold (B, per-player option) | `orders.Rules.GuardHome` (new; ground-guard follow maintenance) | stock | the diagonal offset the option selects: stay-put or scatter | Modern guard assistance keeps its own guard legs; the option applies to the home position only (D5) |
| CP-CON-3 patrolling builder filters (B, per-player option) | `orders.Rules.PatrolWork` (new; the patrol's reclaim/build/repair branches) | both | reclaim-only or assist-only per movement option; the patch's defaults are **Reclaim Only for Hold Position** and Both for the other two, and Community takes those defaults | same as Community |
| CP-CON-4 prepared-build quickkey toggle (A) | `orders.Rules.PreserveBuildToggle` projected into the host widget accelerator | normal toggle mutation | retain a nonzero low status byte while BUILD is prepared; group clearing and firing continue, with no gadget-name test | same |
| CP-CON-5 structure rotation (B, content-driven) | `construction.Rules.AllowedFacings` (new) plus the placement command carrying a facing; footprint and yardmap rotated at creation | `Rotations` parsed, ignored; every structure faces south | the authored facings; the heading word rounded to a quarter turn is the persistent form, so save/load and resurrection need no new state | same |

The per-player builder options of CP-CON-2/3 are, in the patch, registry
preferences of one client (under a TADR-owned key, not the game's) that
change that client's simulation. In Nanolathe
they are **per-player session settings** changed through a typed human command
at the phase-1 boundary, like a gameplay switch, persisted in the settings
file and applied to every builder of that player (D5). They are not unit
stances, because the patch has no per-unit state for them either.

### 4.4 Environment and visibility

| Contract | Seam / owner | Strict | Community | Modern |
|---|---|---|---|---|
| CP-ENV-1 off-map aircraft margin (B, compile-time) | a `visibility` decision and three `combat` decisions, parameterized by the table's margin: the flying-tier LOS substitute (which also repairs on-map aircraft near the upper edge whose sheared visibility row falls off the grid), the off-map second chance (the round survives whenever a reachable enemy aircraft is in the band, `noexplode` rounds included), the over-the-map second chance (engine first, `noexplode` excluded) and the splash pass that runs **on every blast** before the tile scan, with its own strict distance and single-precision falloff | off-map units are invisible and untargetable | the four mechanisms within the margin (1 tile mainline, 32 Escalation/Mayhem/Twilight) | same |
| CP-ENV-2 aircraft wrecks fall (B) | `features` corpse placement: the existing sinking machinery's vertical velocity word `[05 R-FEAT-01 §13]`, seeded through a `combat.Rules.CorpseVelocity` answer at the death site `[06 §12.2]` | a corpse rests where it is created | a corpse created strictly above land terrain with zero velocity is seeded with the smallest downward unit so gravity takes it; only the `escalation` table enables it | same per-table gate, per D6 |
| CP-ENV-3 dragon's teeth visible (P) | fog presentation (§7) | — | inert in the patch itself at this revision; a Nanolathe host option if wanted | — |
| CP-FIX-6 allied jamming ignored (P/S in the source, **B here**: it changes what a player's sensors report and therefore what the computer player and automatic acquisition see) | `visibility.Rules` — a **new seam** in `internal/visibility`, justified because no existing interface owns a per-viewer sensor decision (§9) | allied jammers suppress the viewer's contacts `[03 R-VIS-01 §5]` | jammers owned by allies do not suppress | same |
| CP-FIX-4 deterministic wind (B) | — | retail chain | **not adopted** (§8) | — |

### 4.5 Scripts and spawns

| Contract | Seam / owner | Strict | Community | Modern |
|---|---|---|---|---|
| Recorder ports 32, 69–75 | `session.ScriptPortRules` (new session seam, like `UnitLimitRules`): the session binds the eight ports on every unit's VM to handlers that ask the bound seam, so a switch at the command boundary reaches units already created | every extended port reads zero `[04 R-COB-03 §1]` | the port table of [extended script ports](../research/extensions/script-ports.md) — the eight ports the inspected content authors — with the two out-of-range behaviours the recorder leaves undefined answered as zero (Q6); the recorder's wider set (21–400, with setters) is unrecorded and out of scope until it is (Q10) | same |
| CP-UD-3 skirmish `[units]` spawns (B) | skirmish battle entry `[08 R-ENTRY-01 §5]` through the existing mission placement reader | skirmish spawns commanders only | the read field set, `YPos` replaced by terrain height (authored value kept for an off-map cell), player 11 to the neutral computer player, commander suppression on any attempted spawn, countdown spawns in authored order (the patch's sort is unstable; Q11) without `InitialMission` (as the patch does; Q11); ordinary slot allocation instead of the patch's unit-number workaround (documented difference, Q7) | same |
| CP-FIX-1 resurrection finalization (A by the source, **B against retail**) | `construction.Rules` at the resurrection transplant ([DESIGN_ECONOMY_CONSTRUCTION §2.2](DESIGN_ECONOMY_CONSTRUCTION.md#22-internalconstruction)) | the retail abandonment when the wreck lookup fails after creation `[05 R-WORK-01 §7]` | the preserved wreck information completes the unit | same |
| CP-FIX-8 dispatch validation, CP-FIX-7/9/11 guards and hardening | — | Nanolathe's bounds rejection already covers the crash paths (INVARIANTS I11 "bounds rejection"); no rule needed | — | — |

### 4.6 Command-time features

Click snap (mex and wreck snap radii) changes the *position an order carries*,
not how the simulation resolves it: the cursor position is moved to the
nearest metal spot or wreck within the radius before the command is issued.
It is therefore a host input policy under
[DESIGN_INTERFACE_HUD_INPUT](DESIGN_INTERFACE_HUD_INPUT.md), with the table
supplying the per-package defaults and caps, and it is on in every profile
whose table enables it because it changes no authoritative rule. The override
key (default Alt) suppresses it for one click, as in the patch.

### 4.7 ProTA 4.8 package behaviours

The shipped ProTA 4.8 package's engine loader patches the computer player,
its income and the weapon-maintenance scan
([ProTA 4.8 engine package, "AI and economy evidence audit"](../research/extensions/prota-engine.md#ai-and-economy-evidence-audit)).
Each contract is a table switch that no shipped table enables. The ProTA
content profile turns them on through its `gameplay` block's field overrides
(§3.2 source 2), so they apply only when that package's content is mounted and
Community 3.9 or Modern is selected; Strict 3.1 resolves the zero table and
ignores them, and a player may switch any of them off field by field. The
retail baseline of every row is the existing implementation. Modern embeds
Community, so each row's Modern answer is Community's.

| Contract | Owner / where the rule lives | Strict | Community (switch on) |
|---|---|---|---|
| Computer-player income factors (`AIDifficultyIncome`) | `economy.Service.Community`, read in `addContribution` and `CreditFeatureReclaim` (`internal/economy/maker.go`) | Easy/Medium/Hard `0.5/0.7/1` at the per-unit contribution store and both feature-reclaim credits `[05 R-ECO-01 §3]` | `0.5/1/4` (every selector other than 0 and 1 is Hard) on the seven per-unit routes and both feature-reclaim credits, at the same working-precision store; unit reclaim, spawn credits, transfers, reverse construction and factory-cancellation refunds keep `0.5/0.7/1` |
| Stockpile purchasing (`AIStockpileProducts`) | `ai.Manager.Community`, read by the task dispatcher and the resource/queue body (`internal/ai/manager.go`); the ordinary submission helper in `internal/session/ai_bind.go` | the null task has no body; the resource task admits any live, completed building `[08 R-AI-01 §2]` | the armed-building record runs the resource/queue body at `+30` in vector order; a unit holding any secondary order is skipped for the whole visit; the product branch submits one CANBUILD product, and a `MAKENUKE`/`MAKEANTI` product becomes one counted slot-zero `BuildWeapon` round `[07 R-P0-11 §1]` |
| Target-lock release (`TargetLockRelease`) | `combat.Rules.TargetLockRelease` (Strict false, Community the projected switch), asked by the autonomous maintenance scan (`internal/combat/autonomous.go`) | the scan admits exactly Fire at Will and retains a target through alliance, category and stunned checks `[06 §3.2]` | the scan admits standing-fire values two and three; a retained unit target failing the unit-to-unit physical gate `[06 R-WPN-05 §9]` is set to the empty encoding without `TargetCleared`, the inherited checks still run on the old target, and reacquisition waits for the next visit when they accept; unit acquisition still requires exactly two |
| Low-energy appliances (`AIApplianceEnergy`) | `ai.Manager.Community`, `activationBranch` | the authored `makesmetal` byte selects the activation arm `[08 R-AI-01 §2]` | the signed top byte of the binary32 `energyuse` must be at least 66 (`energyuse >= 32` for finite nonnegative values); the retail disable/enable order, the bound-five draw and the no-fallthrough rule are unchanged |
| Builder stop threshold (`AIBuilderStopThreshold`) | `ai.Manager.Community`, construction placement pass | a capture-capable member skips placement at five build-capable units `[08 R-AI-01 §3]` | the placement cutoff is ten; the reposition pass keeps five, so counts five through nine are eligible for both passes |
| Weapons while working (`WorkingWeaponsAutonomous`) | `orders.Rules.WorkLeavesWeaponsAutonomous` (Strict false, Community the projected switch), asked by `orders.TakeWorkSlots` at all five sites: `HelpBuild` phase 1, `Capture` phase 0, `ReclaimUnit` phase 0 and `RepairUnit` phase 1's in-reach arm (`internal/orders/work.go`), and `MobileBuild` phase 1 after a legal placement check (`mobilePlacementVisit`, `internal/construction/states.go`) | each site releases all three slots, so the builder's weapons are silent until the record's destructor gives them back `[04 R-ORD-01 §5]` `[04 R-ORD-01 §7]` | the same call with the inhibit verb: a slot an earlier order held becomes autonomous with its target cleared and `TargetCleared` raised, an autonomous slot keeps its target, and autonomous acquisition then runs under its ordinary gates `[06 §3.2]`; `SelfRepair`, `RepairUnitNoMove`, the VTOL twins' preamble, `VTOL_MobileBuild`'s takeoff preamble, `BuildingBuild` and the destructor are unchanged |
| Single-slot attack take (`AttackSingleSlotTake`) | `orders.Rules.AttackTakesOneSlot`, asked by `Attack_Chase` phase 1 (`internal/orders/guard.go`) and `Suppress` phase 1 (`internal/orders/combat.go`) | `Attack_Chase` phase 1 releases slots 0 and 2 before binding slot `p1`; `Suppress` phase 1 with `p1 = 2` releases all three `[04 R-ORD-01 §3]` | `Attack_Chase` phase 1 releases only slot 2 when `p1 > 1` (signed), else only slot 0; `Suppress` with `p1 = 2` releases only slot 2. `Attack_Chase` phase 3 and `Suppress`'s `p1 ≠ 2` arm keep retail's take |
| Resurrection failure text (`ResurrectionTextFix`) | `orders.Rules.ResurrectionFailureText`, asked by `Resurrect` phase 3 (`internal/orders/work.go`) | the unresolved-corpse failure shows status 7 `Ressurection failed`, retail's spelling `[04 R-ORD-01 §5]` | status 7 `Resurrection failed`, the feature-lookup failure's string; status, abandon and every other caption are unchanged |
| Map-owned features drawn without LOS (`MapFeatureOwnerEleven`) | read once at battle entry by `loadTerrainStrict` (`internal/session/composition.go`), which passes the placer to `world.Load` through `world.WithTerrainFeaturePlacer`; the draw hook is `tallFeatureVisibleForFrame` (`internal/client/world_draw.go`), shared by the Classic and Modern executors | terrain-file anchors take placer nibble 10; a `nodrawundergray` feature in either feature pass draws only for the local slot or when the two-corner LOS test passes `[05 R-FEAT-01 §3]` `[03 R-RAST-01 §6]` | terrain-file anchors in both attribute layouts take 11 and void cells take none; mission-file, successor, reproduction, reload and corpse stamps are unchanged; the second (tall, height ≥ 10) pass draws a selector-11 feature without the LOS test once the `nodrawundergray` and local-slot tests fail; the first pass and the fog overlay are unchanged, so unexplored cells stay dark; the PlayerFeatures image stores and restores 11 `[08 R-SAVE-02 §12]`. The draw hook reads no table, as the shipped renderer's has no gate: no retail stamp writes 11, so it is unreachable under Strict |

The last four rows are the same loader's order, drawing and text patches
([ProTA 4.8 engine package, "Shipped order, drawing, sound and text patches"](../research/extensions/prota-engine.md#shipped-order-drawing-sound-and-text-patches)).

*The `MobileBuild` slot call.* Retail's `MobileBuild` phase 1 releases all
three slots after a legal placement check and before the site is prepared
`[04 R-ORD-01 §5]`. The ground row's placement visit
(`internal/construction/states.go`, `mobilePlacementVisit`) makes that call
through `orders.TakeWorkSlots` as the first effect of its legal arm, before the
site height is written and the nanoframe allocated; a blocked visit makes no
call, and a visit after an allocation refusal repeats it as a guarded no-op. The
record destructor returns the slots on every removal path, because the row's
static mask lacks the slot-keeper bit `[04 R-ORD-01 §7]`. `VTOL_MobileBuild`
shares the visit but makes no placement-time call: its takeoff preamble
released the slots in phase 0 `[04 R-ORD-02 §2]`. Under Strict this is a
parity fix, and a ground builder's weapons are silent while it builds; with the
switch on, the same call hands them back. `mobile_build_slots_test.go` locks
both verbs, the destructor's return, and the absence of a call on a blocked or
aircraft placement.

*No new seam.* The economy and the computer player read their projected copy
of the table, like the combat interceptor and corpse rows; Strict's zero table
is the bypass. The target-lock row adds one method to the existing
`combat.Rules`, because it changes an acquisition decision the combat seam
already owns, and the three order rows add three methods to `orders.Rules`,
whose Community answers read the queue binding's projected copy.

*Content.* The switches need the package's authored data to matter: the six
stationary stockpile producers (`ARMAMD`, `ARMEMP`, `ARMSILO`, `CORFMD`,
`CORTRON`, `CORSILO`) author `builder=1` and one `MAKENUKE*`/`MAKEANTI*`
pseudo-product each. The shipped package adds no route for mobile anti-nuke
generation (`ARMSCAB`, `CORMABM`) and no CANBUILD membership for the Core
east/west shipyards, so neither is implemented
([ProTA 4.8 engine package](../research/extensions/prota-engine.md#unknown)).
The ProTA content profile enables all five switches in its `gameplay` block.

## 5. Content interface

Every author-facing key the patch reads is parsed onto the compiled
definition **in every mode**, the way `nottoair` and its three siblings already
are ([DESIGN_WEAPONS_PROJECTILES §2.3](DESIGN_WEAPONS_PROJECTILES.md#23-the-shot-admission-gate)):
a typed reader, the retail integer accessor's default, and no consumer unless
a community rule is bound. The keys stay off the definition digest unless a
record authors one, so a retail catalog's hash is unchanged.

| Owner | Keys | Compiled form |
|---|---|---|
| Unit FBI | `Rotations` | four facing bits, south always set |
| Unit FBI | `VeterancyThresholds`, `VeterancyAccuracyBuffRate` | a bounded integer list (invalid tokens dropped, empty → the five defaults) and an integer (≤ 0 → off) |
| Unit FBI | `TransportedExplodeAs`, `TransportedSelfDestructAs` | weapon names resolved at link time; unresolved → empty with a warning |
| Unit FBI | `PreviewPieces`, `PreviewPiecesS/E/N/W`, `PreviewFaceOpponent`, `PreviewObject3D` | presentation metadata, read by the nanoframe preview (§7) |
| Weapon TDF | `notoverwater`, `notoverland`, `nomapweaponalert`, `reloadbar` | flags beside the four existing non-retail flags; `& 1` semantics, so `=2` is off |
| Map OTA | `[units]` under the selected schema | already parsed by the mission placement reader; skirmish entry gains a consumer |
| Unit FBI categories | `CTRL_F`, `CTRL_B`, `CTRL_W` | already compiled as ordinary categories; consumed by the host selection shortcuts (§7) with the patch's heuristic fallback; pinned Ctrl-S filters by `canfly`, not a `NOTAIR`/`NAIR` lookup |
| Animation | `anims/buildrotate.gaf`, `anims/buildrotateclick.gaf` | rotation overlay art; loaded by the placement input (§7) with a built-in fallback |

The content profile's `limits` already carries the definition-table and
weapon-table sizes the `UnitType` setting and the extended-ID module provide.
`unit_limit` and `search_entries`, carried today but unconsumed, become the
per-profile defaults for the table's `UnitLimit` and `PathStepAllowance`
fields, which is what they were added for. The retail profile retains its historical
limit metadata for content diagnostics but does not project those words into
the feature table: otherwise its old 1333-step value would replace the
approved mainline default. Within a mod profile the named table applies first,
legacy parameter defaults second, and explicit `gameplay` fields last.

## 6. Configuration, in one place

| Who | Where | What |
|---|---|---|
| Player | options page (three-stage Gameplay control), settings `gameplay` | which reserved set, or a registered name |
| Player | settings `gameplayFeatures`, `--gameplay-feature` | field-level overrides of the table |
| Player | settings, in-battle command | the per-player builder options (CP-CON-2/3) |
| Content author | content profile `gameplay` block | the build profile its content was authored for and any field overrides |
| Content author | FBI / TDF / OTA keys | per-definition behaviour (§5) |
| Engine developer | `mods/<name>` | a registered rule set that changes an *answer*, deriving from any reserved set |
| Host | settings `presentation`, HUD options | every Tier-4 feature (§7); never gameplay |

The precedence and the Strict override are §3.2. One digest of the resolved
table appears in every report. No other mechanism exists: no environment
variable, no per-unit selector, no marker-detected gameplay.

## 7. Host and presentation features (out of the profile)

Tier-4 features map onto existing or planned Nanolathe controls and are owned
by [DESIGN_INTERFACE_HUD_INPUT](DESIGN_INTERFACE_HUD_INPUT.md) and
[DESIGN_GPU_RENDERER](DESIGN_GPU_RENDERER.md). They are listed here only so the
boundary is explicit and nothing is silently dropped:

| Patch feature | Nanolathe home | Status |
|---|---|---|
| Megamap, its wheel entry and exit, under-attack flash, ring minimums, `Player1..10DotColors` | the optional megamap overview ([DESIGN_INTERFACE_HUD_INPUT §3.15](DESIGN_INTERFACE_HUD_INPUT.md#315-optional-megamap)) | implemented as an optional overview, `presentation.overview` (Zoom by default) with the `megamap*` and `playerDotColors` keys; the ProTA controls preset selects it |
| Wheel zoom, dither, icon config | the strategic view and smooth zoom (DESIGN_GPU_RENDERER §16) | existing view and zoom; optional ordered INI/PCX icon configuration in `presentation.strategicIconConfig`, which the megamap's icons also use |
| ProTA 4.8 victory cue on every local win | end-of-battle audio ([DESIGN_INTERFACE_HUD_INPUT §3.16](DESIGN_INTERFACE_HUD_INPUT.md#316-optional-victory-cue)) | implemented as the host option `presentation.victoryCue` (Options → HUD), off by default; the ProTA controls preset turns it on |
| Whiteboard, chat drawer, fonts, Unicode | HUD messages | not planned |
| Stockpile and transport counters, reload bars, `Vet<n>` label, group numbers | battle HUD | implemented as host HUD options |
| Team-coloured nanolathe, stream/frame colours | shared effect presentation | implemented; optional team switch and per-player stream/frame lists |
| Nanoframe preview (full / wireframe / off), `PreviewPieces*`, face-opponent, `PreviewObject3D` | placement preview | implemented; the face-opponent fog gate reads only the committed frame |
| Map dragon's teeth always visible (CP-ENV-3) | fog presentation | excluded under §8; source feature is inert |
| Double-click same-type selection, ctrl-Z | selection commands | implemented through the existing selection dispatcher; double-click extension is optional |
| ctrl-F / ctrl-B idle factory and builder cycling (category or heuristic membership), ctrl-S on-screen weapon units | selection commands | implemented as the optional Orders-page selection controls |
| Queued build or move order drag, Shift+q/e snap alternation, the `v` movement-option key | placement and order input | optional queued-order drag and CP-CON-1 manual drag implemented; authored Q/E/v examples retain ordinary gadget dispatch pending the evidence recorded under CP-CON-6 |
| Allied resource bar with minimise control, weather report overlay, `+bps` readout | HUD | implemented as host options (`+bps` is a transient local toggle) |
| `.mute`/`.unmute`, percentage share thresholds (CP-SES-9/10) | chat and share commands | not applicable without other humans; skip |
| Rotation key, rotation overlay GAFs, build-menu edge clicks | placement input | implemented with the Placement page and optional four-frame GAF overlays |
| Sound instance limiter | audio | Nanolathe's mixer already bounds voices; not adopted |
| Display-mode minimums, menu resolution, movie de-interlace, CD check | host | not applicable |

## 8. Not adopted

- **CP-FIX-2 unit identity recycling holdback.** Its purpose is stale network
  packets; single player has none. It would change slot identities and every
  fingerprint for no observable benefit.
- **CP-FIX-3 ghost commander correction.** Network only.
- **CP-FIX-4 deterministic wind.** Its purpose is cross-client agreement;
  Nanolathe's single simulation stream is already deterministic. Adopting it
  would replace the retail wind chain's simulation-stream draws
  `[01 §7.3]` with a Mersenne-Twister sequence seeded from a value Nanolathe
  does not have, changing every later consumer of the stream. The rule is
  recorded; the stream is not reproduced. If a future multiplayer effort needs
  it, it is a session seam then.
- **CP-UID-1, CP-UID-2, CP-SES-1..8, lag guard, share percent, player mute,
  allied build queue.** Session and wire; recorded for a future networking
  design.
- **CP-LIM-4/5, CP-FIX-7/9/11 hardening.** Crash paths Nanolathe does not
  have (the circle-radius patch is a presentation divide-by-zero guard; two
  of the listed guards are dead code in the patch); bounds rejection is
  already policy.
- **CP-ENV-3 map dragon's teeth.** Inert in the patch at the pinned revision;
  if wanted it is a Nanolathe fog-presentation option, not a port.
- **The COB dispatch table.** A performance change with identical semantics;
  Nanolathe's VM is not the retail interpreter.
- **`toaironly`.** Unimplemented by the patch and unsettled by any source;
  stays parsed and inert.

## 9. Seams this design adds

Following [DESIGN_GAMEPLAY_RULES §9](DESIGN_GAMEPLAY_RULES.md#9-extending-the-existing-mechanism)
rule 3, a new owning-package seam needs a stated boundary the existing
interfaces cannot serve:

- **`visibility.Rules`** — one method, whether a jammer owned by an ally of
  the viewer suppresses the viewer's contacts (CP-FIX-6), and the off-map LOS
  clamp margin (CP-ENV-1). No existing seam is asked inside the sensor
  refresh; `combat.Rules` is the consumer of visibility, not its owner. The
  refresh is per viewer per tick, and the answer is per viewer, so dispatch is
  request-granular (§4 of the rules design).
- **`session.ScriptPortRules`** — the extended port table. The session owns
  port binding, as it owns the unit-limit questions; the seam is asked per
  port read, which is request-granular by the same reasoning as the existing
  query ports.

Everything else is a method added to `combat.Rules`, `orders.Rules`,
`construction.Rules` or `movement.Rules`, with a Strict implementation that
returns the retail answer without work and a Community implementation that
reads the service's projected table copy. Modern embeds Community.

## 10. Verification

- **Three fingerprint locks** (§2), plus the simulation-cost benchmark's
  initial, warm and final fingerprints per reserved set.
- **Table identity locks.** The resolved table for retail content under
  Community 3.9 equals the embedded `prota` table; under Strict it is zero
  whatever the sources say; a content profile's `gameplay` block resolves to
  the named embedded table with its overrides applied; a settings override
  wins over the profile and the command line over both.
- **ProTA package switches (§4.7).** Every shipped table leaves them false and
  keeps its digest; a content-profile block enables them and a later source
  disables them field by field; Strict projects zero onto the economy, combat
  and every computer player. Per-contract tests lock the three income forms
  and the unchanged unit-reclaim refund, the `+30` armed-building task and its
  secondary-order suppression, the target-lock release against Strict, the
  appliance selector's boundary at `energyuse` 32, and the five-through-nine
  construction overlap; an asset-gated check proves a computer-owned ProTA
  silo receives one slot-zero `BuildWeapon` round.
- **Per-contract tests in the owning package**, each locking the arithmetic
  the extension contract states and the Strict bypass, including RNG and
  resource effects: the veterancy identity with absent keys; the `[min, max)`
  and strict comparisons of the weapon gates; the repair guard order and the
  banked remainder; the claim tie-break's unsigned strict less-than; the
  eight-pair enumeration and the six-slot saturation of area damage; the
  interceptor circle and its pool-order pick; the port table including the
  two defined out-of-range answers and the exact port-73 formula; the spawn
  field set, `YPos` replacement and commander suppression; the kickout's
  target-energy rule, radius bounds, sweep and order rewrite; the rotation
  round-trip through the heading word and a save.
- **Modern-versus-Community locks** for every row of §4 where the two differ
  (D3, D5), so a later Modern policy cannot silently absorb or drop a
  community feature.
- The usual gates: `tools/check`, `tools/check-retail`, and the live battle
  benchmark for the pool-capacity and area-damage units.

## 11. Decisions and open questions

Decisions the maintainer must make before implementation starts. Each has a
recommendation. The maintainer answered on 2026-09-21; the record below is
the authorization the work units in §12 proceed under, and each item's
original reasoning is kept beneath it.

**Settled 2026-09-21.**

| Item | Decision |
|---|---|
| D1 | Yes: the data-driven feature table. I11 is amended by unit 1. |
| D2 | Yes: retail content gets the `prota` table under Community 3.9; the other six tables stay selectable by name. |
| D3 | Modern keeps the existing construction-site yielding for evacuation and adopts only the placement admission; revisit with testing. |
| D4 | Yes: Modern = Community plus Nanolathe's own enhancements, built on top. |
| D5 | Per-player session settings, model (a) below: six values per player, the human's loaded from the settings file and changed by a typed command at the command boundary, computer players on the patch's defaults. |
| D6 | The recommendation: keep the patch's per-table default for aircraft wrecks; a Modern lift is a separate policy. |
| D7 | Yes: the three-stage options control. |
| Q1 | Reproduce the six-slot saturation under Community; lift the cap under Modern. |
| Q2 | Mobile units only, recorded as a deliberate difference from the source. |
| Q3–Q10 | The proposed answers. |
| Q11 | Match the patch: no `InitialMission` for deferred spawns; authored order among equal countdowns. |
| Q12 | The proposed answer. |

**D1 — Is a data-driven feature table acceptable?** INVARIANTS I11 forbids
"independently configurable flags" and a "capability-selection system". The
table is one closed value resolved once and bound as part of one rule set,
reported by digest, and ignored entirely by Strict 3.1 — but it is,
undeniably, a set of switches a file can flip. *Recommendation:* accept it,
amend I11 to say so in one sentence, and keep the alternative (one registered
Go set per `tdraw` profile, no data) as the fallback if the table proves to
sprawl. The patch itself is exactly this shape: a compile-time matrix per
package, which is data we transcribe.

**D2 — Which table does retail content get under Community 3.9?** The
maintainers ship `ota` for unmodified content (fixes and limits only; no
kickout, no builder options, no area-damage overflow, no tie-break) and
`prota` as the mainline "all features" build. *Recommendation:* `prota`,
because it is what players actually install to play retail content today and
the features it adds are the reason to select the profile; `ota` remains
selectable by name for anyone wanting the minimal patch.

**D3 — Kickout versus Modern construction-site yielding.** Both act at the
"waiting for the target area to clear" moment. Under Community 3.9 the
contract's kick algorithm runs. Under Modern, *recommendation:* adopt only the
admission half (placing on own mobile units) and let the existing yielding
policy do the evacuation, because it already exists, is tested, issues
ordinary orders, and does not draw from the C runtime generator. Alternative:
Modern runs the kick algorithm too, which would retire the yielding policy.

**D4 — Does Modern take every mainline feature?** The recommendation is yes:
Modern = Community with its table, plus Nanolathe policies. The exceptions are
D3 and D5. A user who wants Modern's policies without a community feature
turns the field off in the table.

**D5 — The per-player builder options (settled: model (a)).** What the patch has: two
three-way options, one for guarding builders (CP-CON-2: Stay / Cavedog /
Scatter) and one for patrolling builders (CP-CON-3: Reclaim Only / Both /
Assist Only), each chosen **once per movement setting** — Hold Position,
Maneuver and Roam — in the ctrl-F2 dialog, so six values in all. They are
stored in that machine's registry and read by the builder's own order logic
at the moment it decides what to do, and the unit's existing movement stance
is what selects between them. A player therefore sets, say, "my Hold Position
builders only reclaim, my Roam builders do both" once, and then uses the
ordinary per-unit movement stance to pick a behaviour per builder. Nothing is
sent on the wire: in a multiplayer game each human's builders follow that
human's own six values, and the settings are not part of session identity.

Why this needs a decision at all: Nanolathe's simulation cannot read a
settings file inside a tick, and the feature table is one value per
session, not per player, so "which of the three does this builder do" must
be state the simulation owns. Three ways to hold it:

- *(a) Per-player session settings* — a six-value record on each player's
  session state. The human's record is loaded from the settings file at
  battle entry and changed in battle through a typed human command at the
  phase-1 command boundary (the same path a gameplay switch takes), so the
  change lands between ticks; computer players hold the patch's defaults.
  This is the patch's model exactly, it adds no per-unit state, it needs no
  save change because the retail bank has no room for it and the values
  reload from the settings file, and the movement stance keeps doing the
  per-builder selection. *This is the recommendation.*
- *(b) Per-unit stances* — a new unit field cycled like Hold Fire. More
  direct, but the patch has no such state, the save format has no room, and
  it doubles the number of stances a player manages.
- *(c) Global table fields* — six more fields in the feature table, the same
  for every player. Simplest, and in single player with one human it is
  observably the same as (a) unless the computer player's builders are
  also affected, which is the question: the retail computer player does
  issue guard and patrol orders to its builders, so under (c) its builders
  would change behaviour with the human's preference. (a) keeps the two
  apart.

Under any of the three, and under Modern, the guard option changes the
home position of every ground guard reaching follow maintenance; the Modern
guard-assistance legs are unchanged. This broader, source-verified scope was
approved by the maintainer on 2026-09-22, replacing the original builder/factory
restriction. Defaults follow the patch: Cavedog for all three guard values,
Reclaim Only for Hold Position and Both for the other two patrol values.

**D6 — Community-only features Modern might want everywhere.** Aircraft
wrecks falling (CP-ENV-2) is Escalation-only in the patch and, arguably, what
most players expect. *Recommendation:* keep the patch's per-table default and
raise it as a separate Modern policy if wanted; do not conflate.

**D7 — The three-stage options control.** Adding Community 3.9 to the
retail-shaped panel is the visible change of this design. *Recommendation:*
three stages, in derivation order, with a registered name still selectable
only from the settings file or the command line.

**Q1 — Area-damage saturation.** Beyond six overflow units per cell the patch
reaches no further aircraft and counts the event. Reproduce the cap (exact
compatibility) or lift it under Modern? *Recommendation:* reproduce under
Community; a Modern lift is a one-line policy once measured.

**Q2 — Kickout candidates: own units or own mobile units?** The source has
no mobility test — an own building standing in the footprint is admitted and
then sent a move order — while the author's release note says "mobile".
*Recommendation:* follow the author's intent and admit mobile units only,
recorded as a deliberate difference from the source; a building that cannot
move gains nothing from a move order.

**Q3 — The kick direction's random draw.** The approved Nanolathe contract
consumes one CRT modulo-360 draw for every admitted occupant considered by
Community automatic kickout, before the protected-worker decision. Modern's
existing yielding remains draw-free (D3). Source verification during
implementation corrected the original rationale: the patch draws
unconditionally at destination-search entry, but calls that search only after
its should-move predicate succeeds. Keeping the approved per-candidate cadence
is therefore an explicit Nanolathe policy, not a claim of identical patch RNG
position for protected occupants. The owning construction tests lock that
cadence and Strict's bypass. (The retail save carries no in-flight projectiles,
so the larger pool of CP-LIM-1 raises no save question.)

**Q4 — Repair-rate baseline.** The module's own model of "vanilla" disagrees
with the retail repair contract; the research records a supported inference
that the module describes the Escalation executable's helper. This design
implements the module's algorithm as stated under its table flag and leaves
Strict on the retail contract; it does not need the inference settled. Flagged
so nobody "fixes" Strict to match the module.

**Q5 / D6** — see D6.

**Q6 — Two undefined port answers.** Port 73 with an out-of-range id faults in
the recorder and port 72/74 with argument 0 address unit slot 0. *Proposed:*
zero for the fault case, and slot 0 (never a unit) reads as "no unit" — both
recorded as Nanolathe decisions, not recorder behaviour.

**Q7 — The spawn unit-number workaround.** The patch creates non-commander
initial units at a fixed offset from the player's block base for a
network-identity reason it does not explain. Nanolathe allocates normally. If
any authored content depends on the resulting unit ids (port 71 readers), the
difference is observable; no inspected content does.

**Q8 — What does `community-3.9` mean for campaign missions?** The patch runs
in missions too. *Proposed:* the same table applies; mission `[units]` are
already authored placements, so CP-UD-3 adds nothing there; the unit limit
stays the mission's.

**Q9 — Registered sets and the table.** A `mods/` set that derives from
`community-3.9` gets the table its session resolved; should a set be able to
pin table fields (say, a mod that *requires* rotation)? *Proposed:* yes, via a
`Features` override in its `RuleSet`, applied after the content profile and
before the player — the same precedence as a content declaration.

**Q10 — The recorder's wider port set.** The recorder serves grouped
extension ports from 21 to 400 with a setter side; only the eight that the
inspected content authors are recorded. *Proposed:* implement the eight now
under `ScriptPortRules`, leave the rest reading zero, and treat each further
group as its own research-then-implement unit if content that needs it
appears.

**Q11 — Deferred spawns: order and `InitialMission`.** The patch sorts
countdown units with an unstable sort and never runs their order script,
contradicting its own author text. *Proposed:* spawn in authored order among
equal countdowns (deterministic, and what an author would expect) and run no
script for them (what the patch does), both recorded as Nanolathe decisions
with a `TODO(question)` pointing at the maintainer.

**Q12 — Fixed-position builder options as seams.** The spacing operand has
now been verified against retail as the stored ground-follow radius; the
extension contract records its identity and arithmetic. The hook applies to
all ground guards reaching follow maintenance, not just builders guarding
factories. **Settled 2026-09-22:** match that verified patch scope.

**Q13 — Callback timing (settled 2026-09-22).** The source registers deferred
schema spawns, transported-death pruning, and the area index refresh in that
order after its outer game loop. The maintainer approves running the same
ordered callbacks after **each completed authoritative tick**, before frame
publication. This is a Nanolathe determinism policy for Community and Modern:
the callbacks must not depend on whether the host advances one tick or several
in a catch-up batch. They remain outside the twelve retail phases; Strict
bypasses their feature work. The schema boundary and a batched-versus-single
step test lock the placement of these callbacks. Their arithmetic and feature
gates remain those of the extension contracts.

## 12. Work units

Ordered so each lands green on its own; fingerprints move only where a row
says so.

1. **Mechanism.** Third reserved set, three-valued `Base`, registry, options
   control, fingerprint locks. Community's table zero at this point, so its
   fingerprints equal Strict's; existing Modern policy fingerprints remain unchanged. `DESIGN_GAMEPLAY_RULES` and I11 updated.
2. **The table.** `internal/community`, the embedded tables, resolution and
   precedence, the reports' digest, the settings key and flag, the content
   profile `gameplay` block. Still no consumer.
3. **Content readers.** The FBI/TDF keys of §5, parsed and inert.
4. **Parameters.** Pool capacities, step allowance, unit limit from the
   table. First fingerprint move for Community and Modern.
5. **Script ports.** `ScriptPortRules`, the eight handlers.
6. **Weapon gates.** CP-WPN-1..5 on `combat.Rules`.
7. **Veterancy, transported explosions, interceptor coverage, resurrection.**
8. **Area damage and claim tie-break.**
9. **Construction: rotation, reclaim toggle, builder options, kickout
   admission (Community evacuation).**
10. **Visibility: allied jamming, off-map margin (with its combat halves).**
11. **Skirmish spawns and aircraft wrecks.**
12. **Host features** (§7), each under its own owning design.

## 13. Research map

| Behaviour | Owning research |
|---|---|
| Every `CP-*` contract, the build-profile matrix, the preference defaults | [community patch engine behavior](../research/extensions/community-patch-engine.md) |
| Ports 32 and 69–75 | [extended script ports](../research/extensions/script-ports.md) |
| The four earlier weapon keys and their Gold-era unknowns | [non-retail weapon target keys](../research/extensions/weapon-target-keys.md) |
| The documented 3.9.01/3.9.02 patch line and `AISearchMapEntries` | [community patch pathfinding](../research/extensions/community-patch-pathfinding.md) |
| Package identities, key collisions, content-tree layouts | [mod engine-package compatibility](../research/extensions/mod-engine-compatibility.md) |
| Retail baselines cited per row | `[06 §9.3]`, `[06 §11.2]`, `[06 §4.2]`, `[06 R-DMG-01 §2]`, `[05 "Capture"]`, `[05 R-WORK-01 §3]`, `[05 R-WORK-01 §7]`, `[04 R-COLL-01 §1]`, `[04 R-PATH-01 §10]`, `[04 R-COB-03 §1]`, `[03 R-VIS-01 §5]`, `[08 R-ENTRY-01 §5]`, `[01 §6.1]`, `[01 §7.3]`, `[02 R-KEYS-01]` |
