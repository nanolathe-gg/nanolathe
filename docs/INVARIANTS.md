# Invariants

The fourteen rules every diff is reviewed against. They apply to every phase,
and a diff that violates one is rejected even if its tests pass. Each rule
states what it is, why retail forces it, and how a reviewer checks it. Code
cites them as `[I4]`; there is no I15 or I16.

## I1 — Deterministic iteration

**Rule.** Nothing that can affect simulation state may iterate a Go `map`, use
`sort.Slice` on a non-total order, or depend on goroutine scheduling. Iterate
players `0..9` ascending, units by pool slot ascending, projectiles over the
count captured at phase entry.

**Why.** Retail's order is the behavior: `[05 "Authoritative settlement order"]`
notes earlier units consume live stock before later units are tested, so slot
order changes outcomes. `[06 §5.1]` captures the projectile count at entry so
clones appended during the pass are not visited this tick.

**Check.** `grep -rn "range .*map\[" internal/` outside `content` compile-time
code and presentation caches. Sorting uses `sort.SliceStable` or a comparator
that is a total order on a canonical key.

## I2 — Fixed point is the world

**Rule.** World state is `numeric.Fixed` (16.16, `int64` backing). Angles are
`uint16`, 65536 per circle. Never store authoritative positions, velocities, or
angles as floating point.

Allowed floating point, exhaustively:

| Where | Type | Citation |
|---|---|---|
| Immutable authored content definitions and compile-time parsing/conversion | source-appropriate `float32`/`float64`; consumers narrow at the documented boundary | `[02 §5]`, `[02 "Weapon record"]` |
| Resource stocks, ledger carry, debt/accept ratios | `float32` | `[05 "Player slot"]`, `[05 "Two-stage settlement algorithm"]` |
| Economy working-precision intermediates (production contributions and difficulty discounts, pool, stage remainders/ratios, carry terms, and promoted excess) | `float64` transient; narrowed at the named `float32` stores | `[05 R-ECO-01 §1]`, `[05 R-ECO-01 §3]`, `[05 R-ECO-01 §5]`, `[05 R-ECO-01 §6]` |
| Economy cumulative totals and waste counters — including the opt-in trace copy of the same totals in `internal/economy/p28_parity_trace.go` (P28-OBS-00C; mirrored by `internal/architecture`'s parity ratchet as this row) | `float64` | `[05 "Stocks, counters, and waste"]` |
| Construction remaining fraction and its proportional cost/health intermediates | `float32` | `[05 "Construction target state"]`, `[05 "Construction arithmetic"]` |
| Community repair energy calculation | `float64` transient, narrowed by the shared integer conversion | `community-patch-engine.md CP-DMG-4`; enabled only through `DESIGN_COMMUNITY_PATCH` |
| Community construction kickout geometry and invested-energy test | `float64` transient for bearings, circle/ray intersection, trigonometric sweep and `buildcostenergy × (1 − remaining)`; destinations narrow to whole world units and then 16.16 | `community-patch-engine.md CP-CON-1`; enabled only through `DESIGN_COMMUNITY_PATCH` |
| Meteor parameter installation (spacing, duration and interval) | `float64` transient after the source `float32` store; signed-64 truncation retains the low 32 bits, with no intervening float store | `[06 §6.5]`, `[01 R-DET-01 §1]` |
| Map tidal scalar, stored once at map load without fixed-point conversion | `float32` | `[03 R-TERR-01 §6]`, `[05 R-PROD-01 §4]` |
| Wind scalar published to consumers (clamped to 1.0) | `float32` | `[01 §7.3]` |
| Clock budget product `delta × speed + carry` | `float64` product, `float32` carry | `[01 §4.2]` |
| Ballistic discriminant, `acos`, `sqrt` | `float64` | `[06 §3.3]` |
| Ordinary creator muzzle-to-aim planar `hypot`, truncated into its stored 16.16 distance before pitch and later burst expiry | `float64` transient; stored distance is fixed point | `[06 §6.3]`, `[06 §4.3]` |
| Pre-fire lead distance `D` — the three-dimensional `sqrt` over the raw 16.16 shooter-minus-point deltas, truncated toward zero before the integer flight-time divide | `float64` transient, never stored; `D`, `T` and `T2` are integers | `[06 §3.3]` |
| Cruise waypoint distance — the same three-dimensional `sqrt` over the raw 16.16 current-minus-stored-target deltas, truncated toward zero before the signed-short threshold compare | `float64` transient, never stored | `[06 §6.8]` |
| Area-damage range `sqrt` (radial falloff distance, truncated toward zero to `int32`) | `float64` transient, never stored | `[06 §9.3]` |
| Area-damage falloff expression `f*f*(1−edge) + edge`, `f = d/R − 1` — the whole expression is evaluated at working precision and narrowed by **one** store at the end | `float64` transients; the falloff itself is the `float32` store | `[06 §9.3]` |
| Area-damage amount product `trunc((double)base × falloff)` — the promoted base damage times the **stored** single-precision falloff, truncated toward zero before attacker veterancy | `float64` transient, never stored; the falloff keeps its `float32` store | `[06 §9.2]` |
| Capture timer base sum — each authored cost scaled by two `float32` constants, the two terms combined and truncated once toward zero into the integer budget | `float64` working-precision transients, never stored; the constants stay `float32` | `[05 R-WORK-01 §6]` |
| Flight brake integration temporaries (`hypot`, `h`, `b`, ratio) | `float64`, narrowed at the named fixed-point stores | `[04 §10.1]` |
| Flight command producer's goal distance `hypot` and the shared air `bearing` `atan2` with its `65536/2π` scale | `float64` transient, narrowed at the `__ftol` truncation and the `uint16` angle store | `[04 R-AIR-01 §1]` |
| AirStrike release lead `sqrt((2·cruisealt)/gravity) · 30 · speedInteger` | `float64` transient, narrowed by truncation toward zero to the marker's integer radius | `[04 R-AIR-01 §8]` |
| Lean accumulator's two `atan2` terms and the `fsincos` coordinate-pair rotation that feeds them | `float64` transient; bank and pitch are stored as `uint16` angles and are **authoritative**, not presentation | `[04 R-AIR-01 §2]` |
| Ground follower goal-point bearing and route-distance/lookahead `hypot` temporaries | `float64`, narrowed at the named angle and fixed-point boundaries | `[04 R-MOV-01 §2]`, `[04 R-MOV-01 §3]`, `[04 R-MOV-03 §2]`, `[04 R-PATH-01 §8]` |
| `StartBuilding` first-argument bearing — `atan2` of the builder-minus-target delta, the compiled `65536/2π` scale, and its round-half-even store | `float64` transient, narrowed at the `uint16` script-argument boundary | `[04 R-CB-01 §3]` |
| AI candidate-score pressure terms (`energyRaw`, `metalRaw`) — the capacity difference and its product, at the 53-bit working precision retail runs with | `float64` transients, never stored; the inputs and the multipliers stay `float32`, and the capacity truncation and the single final truncation are the only narrowing steps | `[08 R-P0-05 §3]`, `[08 "Arithmetic and clamping"]` |
| AI class-vector first-pass accumulator — the two cost products and their sums, at the 53-bit working precision retail runs with; the two coefficients that narrow keep their single-precision stores | `float64` transient, never stored; each sum truncates toward zero into the integer coefficient immediately, and the constants stay `float32` | `[08 "Arithmetic and clamping"]`, `[08 R-P0-05 §5]` |
| AI metal-spot records and exhaustive-placement heap keys | authored feature-metal copy and helper-local negative squared-distance key, both `float32` | `[08 R-AI-03 §1]`, `[08 R-AI-03 §3]` |
| Simulation trig-table construction at initialization | `float64` transient; authoritative table entries are integers | `[04 §5.1]` |
| Model piece rotation trig in the draw path and admission-time shatter pose | `float64`, round-to-nearest; geometry narrows back to fixed point | `[03 §2.4]`; approved current-simulation-pose departure in DESIGN_UNITS_ORDERS_COB §3.3 |
| Queued-order range-ring adaptive chord count `trunc(radius × 2π × 1/8)` | `float64` presentation transient, narrowed immediately to the integer chord count | `[07 R-P0-11 §3]` |
| Shatter-fragment normal construction — each reciprocal-65535 vertex conversion, vector difference, cross-product component, and normalized component narrows at its named binary32 result; square, sum, square-root and division use working precision until that component store | `float64` transient, `float32` named stores | `[04 R-COB-04 §3]` |
| Nanolathe particle travel distance (`sqrt`, truncated to the tick count) — the strip-6 emitter's particle lifetime, computed in `internal/session` | `float64` transient, never stored; the lifetime it produces is **authoritative-side**, for the reason the strip-object span row below gives | `[03 §5.5]` |
| Nanoframe reveal's barycentric interpolants (`internal/client/model_raster.go`) | `float64` presentation-only, never stored | `[03 §5.2]` |
| Startup lens displacement-map radius and coordinate divisions (`internal/drawlist/lens.go`) | `float64` presentation temporaries; each coordinate truncates before immutable signed-16 displacement storage | `[03 R-FX-01 §4]` |
| Load-time 2× art synthesis (`internal/upscale`: PCA basis, feature distances, tone terms) | `float32`/`float64` presentation-only; runs on the loader goroutine, its output is index art the simulation never reads | DESIGN_GPU_RENDERER §14.4 |
| Enhanced glow layer geometry and kernel (`internal/platform/gpurender/glow.go`: stroke length `sqrt`, Gaussian weights) | `float32`/`float64` presentation-only; device-side executor state built from the recorded list, never read by the simulation | DESIGN_GPU_RENDERER §19 |
| Enhanced blast radius and displacement boosts (`internal/platform/gpurender/distortion.go`) | `float32`/`float64` presentation-only square-root tuning, never simulation inputs | DESIGN_GPU_RENDERER §25.2 |
| Enhanced model normals and battle-light response (`internal/client/model_compose.go`, `internal/platform/gpurender/lighting.go`) | `float32`/`float64` presentation-only geometry and light calculations; never simulation inputs | DESIGN_GPU_RENDERER §23 |
| Enhanced coastal surface and particle geometry (`internal/client/water_wakes.go`, `water_motion.go`, `water_buildings.go`, `internal/platform/gpurender/water.go`, `water_reflections.go`) | `float32`/`float64` presentation-only screen geometry, drift, fades and shader operands; never simulation inputs | DESIGN_GPU_RENDERER §26 |
| Offline film capture (`internal/film`: overlay glyph rasterization, camera-track easing, cue envelopes) | `float64` presentation-only capture geometry and timing; it runs after the frame is composed, reads no session state and is never a simulation input | docs/FILM_CAPTURE.md |
| Load-time strategic-icon coverage synthesis (`internal/client/strategic_icon_art.go`) and offline review-sheet filtering | `float64` presentation-only geometry/filter intermediates; immutable mask bytes are never simulation inputs | Enhanced design policy, DESIGN_GPU_RENDERER §18.5 and §18.7 |
| Strip-object span `sqrt` — the nanolathe particle's travel distance, the sprinkle's `len` and the flame segment's span, over raw 16.16 deltas, truncated before the fixed-point step or tick count is formed | `float64` transient, never stored — and **authoritative-side, not presentation**: these run in `internal/session` and set strip-container and sub-record lifetimes, and a lifetime decides how many CRT draws phase 11 spends, which is the stream three authoritative consumers read. The no-fusion rule below binds all three sites. | `[03 §5.5]`, `[03 R-FX-01 §3]`, `[03 R-FX-02 §2]`, `[01 §7.5]` |
| AI strategic-centre weighted accumulators and per-unit weight (weighted centroid of complete own units, truncated into three 16.16 words) | `float32` transients, never stored | `[08 R-P0-05 §10]`, `[08 R-AI-01 §16]` |

Everything else is integer. Simulation velocity integration uses the fixed-point
trig tables, **not** the float path `[03 §2.4]`.

**No fused multiply-add.** The rows above fix widths and narrowing points; this
one fixes the number of roundings. The Go specification lets a compiler combine
`x*y + z` into one operation that rounds once instead of twice, possibly across
statements — `gc` does on arm64 and on amd64 under `GOAMD64=v3`, and not on
default amd64, so one source has three answers. Retail has no fused
multiply-add: every product is rounded to the working precision before it is
added, which is what rows like "narrowed by **one** store at the end" and
"combined and truncated **once**" already describe. So in an authoritative
package every product that feeds an add or a subtract — including one formed in
an earlier statement — carries an explicit floating-point conversion, the
specification's rounding barrier. It changes no operand width, no constant, no
evaluation order and no narrowing point. Presentation packages are exempt.

**Check.** `grep -rn "float64\|float32" internal/` — every hit maps to a row
above or is presentation-only. `internal/architecture`'s
`TestAuthoritativeArithmeticIsNotFused` compiles the authoritative packages for
arm64 and for `GOAMD64=v3` and fails on a fused instruction outside its
shrink-only allowlist, each entry of which argues that its product is exact.

## I3 — Truncation toward zero

**Rule.** Narrowing follows retail's `__ftol`: truncate toward zero, never round,
never floor. Definition parsers first retain the low 32 bits of a signed-64
truncation; use `numeric.TruncateFloat64ToLow32`, not a direct `int32(f)`.
Finite values in the signed-64 range wrap through the retained word; non-finite
or out-of-range values retain zero [01 R-DET-01 §1].

The catalog Version pair and scheduler budget explicitly floor their
binary64 operands **before** this truncating conversion. Preserve that
separate operation and its floating result where used for the budget carry
[01 §4.2][01 R-DET-01 §3][02 R-MALF-01 §5].

Cell/tile math on possibly-negative world coordinates is the opposite case: it
needs **floor** division, because retail uses an arithmetic shift with a sign
correction `[03 §2.1]`. Use an explicit helper, never `/`:

```go
func floorDiv(a, b int64) int64 { q := a / b; if a%b != 0 && (a < 0) != (b < 0) { q-- }; return q }
```

**Why.** `x / 65536` on `x = -1` yields 0, but the arithmetic shift yields -1.
The two disagree exactly on the map's west and north edges.

A **fixed-by-fixed multiply** is the third case, and it floors. The product is
formed at full width and shifted down, which is an arithmetic shift, so
`numeric.Fixed.Mul` uses `>> 16` and not `/ 65536`. Division is the exception
within the exception: it is an `idiv`, not a shift, so `Fixed.Div` truncates
toward zero. The asymmetry is the hardware's, not a choice.

The multiply's rounding is Established, not inferred: `[04 §7.2]` forms the
A* heuristic scale as a full signed 64-bit product arithmetically shifted, and
`[04 R-MOV-01 §3]`, `[04 R-MOV-01 §4]` and `[04 §10.1]` write the same shape.
`numeric.Fixed.Mul` carries no open question about it.

| Operation | Rule | Helper |
|---|---|---|
| definition float → integer | signed-64 truncation, retain low 32 bits (`__ftol`) `[01 R-DET-01 §1]` | `numeric.TruncateFloat64ToLow32` |
| in-range float → integer | truncate toward zero (`__ftol`) | `int32(f)`, `Fixed.Int` |
| world → cell | floor with sign correction `[03 §2.1]` | `world.WorldToCell` |
| fixed × fixed | floor (arithmetic shift) | `Fixed.Mul` |
| fixed ÷ fixed | truncate toward zero (`idiv`) | `Fixed.Div` |

## I4 — Two RNG streams, call order is behavior

**Rule.** One authoritative Park-Miller simulation stream (`16807 / 127773 /
2836 / 0x7fffffff`, Schrage) and one authoritative CRT stream (`*214013 +
2531011`) per session. A skirmish setup shuffle uses a disposable CRT seeded
from the explicit battle seed; briefing, audio, music and client presentation
advance private copies. None of those histories may advance or seed the
authoritative session CRT. No per-entity streams, no `math/rand`, no
`crypto/rand` (DESIGN_RUNTIME_DETERMINISM §2.2 and §5).

Consumers must draw the documented number of times **even when the result is
discarded**: a pool-full fire attempt still draws up to two spread values
`[06 §4.4]`; feature reproduction draws once per visited cell even at
`reproduce=0` `[03 §5.1.2]`; `bound < 2` returns 0 without advancing
`[01 §7.1]`.

Modern shot admission may preview accuracy draws on a temporary value copy of
that same simulation stream, committing its resulting state only when the
shot passes the terrain check (DESIGN_WEAPONS_PROJECTILES §2.3.1). This is a
bounded transaction over the one stream, not a persistent alternate generator.
Strict 3.1 retains the original draw sites and failure effects.

Which stream: gameplay normally uses the simulation stream; meteor geometry
`[06 §6.5]`, screen shake `[03 §5.6]`, audio variant selection `[03 §8.3]`, and
wind's next-change interval `[01 §7.3]` use the CRT recurrence. Audio advances
its private copy; briefing wind advances the private front-end stream. Later
battle wind strength and 16-bit heading use the simulation stream.

The CRT stream's draw **count** is behavior even where the drawn **value** is
only presentation. Phase 11's effect strips, phase 10's shake, phase 6's
burning-feature smoke and the phase-2 elimination line all paint pixels or
text, but every draw they spend displaces the one stream that the phase-8 wind
interval, the phase-9 meteor scheduler and the phase-5 victory-timer arm read
on later ticks `[01 §7.5]`. Anything that changes how long a strip container
or sub-record lives — a span, a lifetime, a pool-occupancy decision — is
therefore a determinism input, and the arithmetic that produces it is
authoritative-side even when its product is only ever drawn `[03 R-STRIP-01 §3]`.

**Check.** Identical seeded session setups have stable simulation and CRT draw
counts; setup and briefing draws leave the retained battle CRT fresh; the
graphical and headless hosts compose and advance the same authoritative
session with explicit battle seeds. Host timing and profiling do not enter
simulation decisions.

## I5 — Pools, not handles

**Rule.** Fixed-capacity pools with slot 0 reserved as null, lowest-free
allocation, immediate reuse, no generation tags. A stale damage packet
addressing a reused slot is accepted — that is retail `[06 §5.1]`.

Capacities: units are the game value derived from setup times ten plus one
with 280-byte records sliced per player in sorted order, stock roughly two
thousand to five thousand rather than 500 folklore, per `[01 §6.1]`; 300
projectiles (107-byte retail identity), eight COB threads per unit (164-byte
identity), order nodes (86-byte identity), 300 fixed effects (0x54-byte
identity), and effect strips evicting oldest-first above 400. Go records use
named fields per I13. Allocation scans the owning slice for the lowest free
flag, reuses immediately, and validates per-definition limits and forced slots;
stale 16-bit packets that validate only slot nonzero and alive alias silently
after reuse.

`pool.Projectiles` is the sole allocation/dead/count authority. The projectile
pool **appends at the tail** and never fills holes; dead records
set a flag without decrementing the count; compaction is stable and runs before
presentation `[06 §5.2]`.

## I6 — Presentation boundary

**Rule.** Sim never reads wall-clock time, input state, camera, or renderer
state. Presentation never writes sim state. The only channel is the committed
frame, published once after every completed sub-tick and sampled at the
current committed tick by the renderer, or by Enhanced presentation the two
most recent committed ticks (DESIGN_GPU_RENDERER §13.5). The active runtime
has one authoritative session implementation, hosted by the Ebitengine window
or the graphical command's headless mode and dedicated headless command
(ARCHITECTURE §5). A host may measure elapsed time or profile execution, but
those observations never determine simulation state or tick behavior.
Presentation and front-end random histories never become session seed inputs;
the explicit battle seed pair is the only RNG handoff into composition
(DESIGN_RUNTIME_DETERMINISM §2.2 and §5).

**Why.** Retail's draw path samples the accumulators exactly as committed at
the current tick; no interpolation between updates exists `[03 §2.4]`.
Original preserves that sampling. Enhanced presentation
(DESIGN_GPU_RENDERER §13.5) is the one path allowed to read the two most
recent committed ticks and the clock's carry, blending them in retained
presentation buffers; it writes nothing back, consumes no simulation RNG, and
the simulation still publishes only after the complete phase sequence
[01 §4.4]. `--shot` and Original never blend.

**Check.** `grep -rn "time.Now\|time.Since" internal/{clock,units,orders,cob,movement,path,economy,construction,features,combat,visibility,ai,mission,triggers}` returns nothing. `internal/client` imports sim packages; no sim package imports `internal/client`. Outside `internal/client/interpolate.go` the frame path has no `Lerp`, `alpha`, or previous-frame blend. `frame.Buffer.Previous` has exactly two production callers: that file, which performs the Enhanced blend, and the live battle benchmark's census in `cmd/nanolathe`, which only counts which units moved between two committed ticks and feeds nothing back. The older frame of a pinned pair (`frame.Buffer.PinTick`/`PinLatest`, DESIGN_GPU_RENDERER §13.13) is likewise read only in that file.

## I7 — Tick phase order

**Rule.** The session runs the twelve phases of `[01 §4.4]` in order, incrementing
the global tick before phase 1 of each sub-tick. Subsystems register into a
named phase; nothing runs outside one.

Within a tick, the same-tick callback windows of `[04 §5.4]` hold: unit update
(queues `SetDirection`/`SetSpeed`) → weapon update (queues `TargetCleared`,
`Aim*`, `Fire*`, `RockUnit`) → normal COB drain (delta 1, eight thread slots
then one piece pass) → orders/build work → movement integration (immediate
`MoveRate`, `setSFXoccupy`) → slot-end death handling.

## I8 — Authored values are not per-tick

**Rule.** Economy fields are per-settlement-pass, consumed once per ~30 ticks
`[05 "Authoritative settlement order"]`. Never multiply or divide by the tick
rate in the ledger. Weapon fields *are* converted at catalog compile time
(`weaponvelocity × 65536 / 30`, durations `× 30`, `turnrate / 30`) — once, in
`internal/content`, never again at use `[02 "Weapon record"]`.

## I9 — Unknowns stay unknown

**Rule.** Three forms only:

```go
// TODO(T25): the extractor placement helpers' geometry is untraced.
// Placeholder: fall through to the generic build command path.
```

`TODO(T23)` platform residual, `TODO(T25)` accepted blocked item, or
`TODO(question)` for a local gap with the question written out. Never a bare
constant with no citation, and never a plausible-sounding name for something
the research explicitly leaves unnamed. Use a neutral logical name and cite the
owning clean-room contract; executable offsets stay outside the repository per
`AGENTS.md` rule 3.

## I10 — Citations in code

**Rule.** Any constant, ordering, or comparison lifted from research carries the
citation:

```go
// [04 §7.2] cardinal 16, diagonal 22; turn penalties by direction delta.
var stepCost = [8]int32{16, 22, 16, 22, 16, 22, 16, 22}
var turnPenalty = [8]int32{0, 40, 60, 80, 100, 80, 60, 40}
```

**Why.** Review is a spot-check against the source of truth. An uncited constant
cannot be reviewed, only trusted.

## I11 — Retail baseline and Modern gameplay

The central gameplay mode selects **Modern** by default, **Community 3.9** for
the sourced compatibility profile, or **Strict 3.1** for the retail baseline,
independently of the renderer. The reserved derivation order is Strict 3.1 →
Community 3.9 → Modern. Every new intentional gameplay departure must enter
through the owning rule interface at its approved layer. No scattered
compatibility flags or unapproved alternate behavior paths.

**Modern differences are intentional and user-authorized.** Do not remove a
documented Modern rule as a retail parity fix. The owning design document must
state its retail baseline, Modern rule, conservative limits and verification;
retail research continues to describe the executable. Both branches need
contract tests, including resource and RNG effects. An unknown retail mechanic
is still an unknown; the Modern setting does not authorize invented evidence.
Current contracts are DESIGN_WEAPONS_PROJECTILES §2.3.1 (terrain admission) and
"Modern threat targeting and incoming fire", DESIGN_UNITS_ORDERS_COB
"Modern Hold Fire" and "Modern danger response", DESIGN_MOVEMENT_PATH
"Modern danger escape", "Modern learned terrain", "Modern re-route
staggering", "Modern group-order spreading", "Modern bounded path work",
"Modern allied pass-through", "Modern unreachable moves", "Modern jam
release" with its "Modern pocket release", "Modern route straightening" and
"Modern wedge escape",
DESIGN_INTERFACE_HUD_INPUT "Modern group destination slots", and
DESIGN_ECONOMY_CONSTRUCTION "Modern factory-exit yielding", "Modern construction-site yielding" and
"Modern authored build membership", and DESIGN_SESSIONS_AI_SAVE
"Modern save unit limits" and "Modern wave air targets". Each departure reaches its algorithm through the
owning package's rule interface, bound once from the central session mode as
one named rule set — not through independently configurable flags; new and
restored queues inherit the same set, and an unbound seam answers as retail.
Community contracts and feature-table precedence are owned by
DESIGN_COMMUNITY_PATCH. Its closed feature-table value is the one approved
exception to the ban on independently configurable gameplay flags: content
declares one table, composition resolves it once as part of the selected rule
set, reports its digest and projects immutable copies to the service owners.
Strict 3.1 ignores every table override. Community applies the resolved table;
Modern builds on that result and then applies its own documented policies.
This exception is not a second registry or a general capability system.

**Mutators** (user-authorized 2026-09-23) are the one exception that applies
in every mode, Strict 3.1 included. They are a closed set of global
multipliers applied once to the per-battle catalog clone at battle entry: a
transform of content, not a rule, with no seam, no RNG and no per-tick state.
They are owned by [DESIGN_MODS_MUTATORS](DESIGN_MODS_MUTATORS.md) §6. The
retail baseline is Strict 3.1 *with no mutators*, and every fingerprint lock
runs with none.

**Survival** (user-authorized 2026-09-23) is likewise available in every mode.
It is a scenario — a skirmish session with a commanderless attacker slot and a
wave director that creates units through the ordinary allocator and gives
them ordinary orders, with the survivors sharing sight, radar and income as
one side — not a rule, so it adds no seam and consults the bound rule set like
any other battle. Its director exists only in a Survival
session and draws the simulation stream only there. It is owned by
[DESIGN_SURVIVAL](DESIGN_SURVIVAL.md).

The mode word also selects a **registered** set by name: a third-party set is
compiled in through `mods/`, which only a command may import, and it composes
the shipped implementations rather than reimplementing a policy. Such a set
declares the reserved set it derives from. `Session.Gameplay` carries that
three-valued base; `Session.Rules.Name` carries the selected name, which
headless and simulation-cost reports expose as `rules`.
The retail save does not store the selected name. Strict 3.1 remains the
retail baseline and no registered name can shadow any reserved set. The
seam list, the registry, the allocation and granularity rules, the switch timing and what a save knows about a set are in
[DESIGN_GAMEPLAY_RULES](DESIGN_GAMEPLAY_RULES.md).

Extend the existing owning interface for a new gameplay decision, implement
all three reserved defaults through the derivation chain, and test each changed
contract plus its lower-layer bypass, including RNG and resources. Add an
owning-package seam only with a documented boundary that existing interfaces
cannot serve, and bind it through the same
`session.RuleSet`; no second registry or capability-selection system. Cached
implementations hold no state, including through pointers: mutable request or
session state belongs to its existing owner. Stateful rule objects require an
explicit lifecycle, switching and save design before implementation. Content
profiles select load-time content layout independently; detecting an install
or content marker does not select gameplay. Extension research belongs in
[research/extensions](../research/extensions/README.md), separate from retail
evidence; documenting a patch is not approval to adopt its mechanics.

Strict 3.1 retains the retail firing pipeline, including documented faults.
Outside an explicitly approved Modern contract, reproduce retail bugs
(unguarded divide, wrapped deadline, stale pointer after compaction) and cite
them. Bounds rejection and the existing host/presentation policies listed
below retain their separately documented exceptions.

The user-requested startup root list is a sanctioned host extension
(DESIGN_CONTENT_VFS §5). Only multiple roots add directory precedence above
existing provider tiers; one root retains its existing resolution. Installation
discovery is host policy and supplies explicit roots before content loading.
The installer-facing `--list-installs` and `--check-install` diagnostics are
sanctioned host entry points (DESIGN_CONTENT_VFS §5). `--save-dir` selects an
exact save/load directory independently of the content roots, retaining the
existing directory policy when omitted (DESIGN_SESSIONS_AI_SAVE §5). With that
explicit override, filename normalization applies only to the leaf name and
rejects path components so dotted ancestors cannot redirect the write.

Unit catalog admission is sanctioned content policy (DESIGN_CONTENT_VFS §5
"Unit admission"): the retail `Version` and `Copyright` drop gates and their
incompatibility report are deliberately not implemented, while the loose-file
gate, record order, definition IDs and the retail catalog hash are unchanged.

The content profile is sanctioned content policy (DESIGN_CONTENT_VFS §5
"Content profiles"): a named, load-time description of the mounted content set
— a directory table and the limits its content needs — detected or selected
before the catalog compiles, never reaching a tick and orthogonal to the
gameplay rule set, with provenance and the catalog hash staying retail-named.

The user-requested skirmish unit-limit default of 1000 and expanded configured
range are sanctioned setup policy (DESIGN_CONTENT_VFS §5), with CLI and JSON
configuration. Campaign limits remain authored by the mission.

The renderer is the sanctioned presentation switch of
[DESIGN_GPU_RENDERER.md](DESIGN_GPU_RENDERER.md): currently classic or modern,
with modern the default and a 60 FPS presentation cap. Both are persisted
Nanolathe options (DESIGN_INTERFACE_HUD_INPUT §3.4.1).
The planned labels are Original, GPU Classic and Enhanced, sharing two executors.
GPU Classic permits visually reviewed raster approximations; Enhanced may add
separately designed zoom, lighting, glow, antialiasing and optional interpolation.
These choices never change authoritative state. Departures from classic are
listed in that design document's Divergences. New public modes and interpolation
are deferred beyond the prototype human-review gate.

## I12 — Standard library first

Use `sort`, `slices`, `io/fs`, `errors` shapes rather than bespoke utilities. No
new module dependencies without orchestrator sign-off; the active window
backend is Ebitengine and all other dependencies must be justified in the
module diff.

## I13 — Retail record identity is not Go layout

**Rule.** Retail's in-memory record sizes and field positions describe how the
executable represented state, not how Nanolathe must lay out Go memory. Go
structs use named fields and whatever layout the compiler picks. Do **not**
build byte arrays with hand-packed accessors merely to imitate an executable
record.

When two clean-room contracts name the same logical field, they resolve to the
same Go field. Preserve that identity through the logical name and citations,
not through executable offsets:

```go
type Unit struct {
    // ...
    StateFlags uint16 // armored and hidden/cloaked state [06 §9.2], [03 §3.2]
}
```

The exceptions — where byte layout *is* the contract, because bytes cross a
boundary — are: file formats in `formats/`, the 13-byte plot cell (`[03 §2.2]`),
the 28-byte game-time save box, HAPIBANK headers and account records, the
9-byte damage packet's wire form if it is ever serialized, and the 35-byte
mover save record — which carries no route state of its own, because the
retail image rebuilds the route object instead of persisting it
`[08 R-SAVE-02 §8]`. The active save boundary is retail account parsing and staged battle
restoration. There is no alternate Nanolathe save codec.
Everything else is a Go struct.

**Why.** Pool capacities (300 projectiles, 8 COB threads, 86-byte order nodes)
are behavioral limits and must be honored exactly; the byte *sizes* are how the
research names them, not a requirement on our memory layout.

## I14 — Verify against the reference install before believing a count

**Rule.** Any statement about how much content exists — how many weapons, sides,
movement classes, archives — is checked against `~/TotalAnnihilation` before it
becomes a test assertion or a fixed-size array. `docs/SPEC_CONFLICTS.md` records
what that install has already disproved.

**Why.** Two spec statements have already failed this check (the ten-archive cap
and `GAMEDATA.TDF`). Both would have produced an engine that refuses to boot on
real data.
