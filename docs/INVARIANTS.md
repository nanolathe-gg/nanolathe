# Invariants

The fourteen rules every diff is reviewed against. They apply to every phase,
and a diff that violates one is rejected even if its tests pass. Each rule
states what it is, why retail forces it, and how a reviewer checks it. Code
cites them as `[I4]`; there is no I15 or I16.

## I1 — Deterministic iteration

**Rule.** Nothing that can affect simulation state may iterate a Go `map`, use
`sort.Slice` on a non-total order, or depend on goroutine scheduling. Iterate
players `0..9` ascending, unit sweeps by pool slot ascending, projectiles over
the count captured at phase entry. Indexed queries follow their own established
traversal: repair patrol gathers spatial sectors in Z/X order and each bucket
head to tail [04 R-ORD-02 §4]. Replacing that query with a unit-slot sweep
changes the meaning of its random candidate index.

**Why.** Retail's order is the behavior: `[05 "Authoritative settlement order"]`
notes earlier units consume live stock before later units are tested, so slot
order changes outcomes. `[06 §5.1]` captures the projectile count at entry so
clones appended during the pass are not visited this tick.

**Modern AI exception** (user-authorized 2026-09-24). The Modern AI
computer player may think on a background goroutine (DESIGN_GAMEPLAY_RULES
"The Modern AI controller"). The simulation thread builds its observation in
phase 5, hands it over, and joins the think at a fixed reaction deadline,
applying its commands there — the tick and content the synchronous host
produces — so no simulation state depends on scheduling. This is the only
goroutine an authoritative package starts.

**Check.** `grep -rn "range .*map\[" internal/` outside `content` compile-time
code and presentation caches. Sorting uses `sort.SliceStable` or a comparator
that is a total order on a canonical key.
`internal/architecture.TestAuthoritativePackagesStartNoGoroutines` names every
goroutine an authoritative package may start, with its argument.

## I2 — Fixed point is the world

**Rule.** World state is `numeric.Fixed` (16.16, `int64` backing). Angles are
`uint16`, 65536 per circle. Never store authoritative positions, velocities, or
angles as floating point.

Allowed floating point, exhaustively:

| Where | Type | Citation |
|---|---|---|
| Immutable authored content definitions and compile-time parsing/conversion | source-appropriate `float32`/`float64`; consumers narrow at the documented boundary | `[02 §5]`, `[02 "Weapon record"]` |
| Resource stocks, ledger carry, debt/accept ratios | `float32` | `[05 "Player slot"]`, `[05 "Two-stage settlement algorithm"]` |
| Repair-patrol resource thresholds and feature-fit comparisons | `float64` transient over stored `float32` inputs, with no intermediate store; Modern guard policy retains its own comparator | `[04 R-ORD-01 §4]`, `[04 R-ORD-01 §7]` |
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
| AI candidate-score pressure terms (`energyRaw`, `metalRaw`) — the capacity difference and its product, and the production-minus-consumption queries, at the 53-bit working precision retail runs with | `float64` transients, including the two net results in call-local `ScoreInputs`, never authoritative stored state; source fields and multipliers stay `float32`, with the pressure terms' named integer truncations and no single store before the net comparisons | `[08 R-P0-05 §3]`, `[08 "Arithmetic and clamping"]` |
| AI class-vector working intermediates — the net-energy query return, first-pass cost sums and the later C0/C1/C2 coefficient arithmetic, at retail’s 53-bit working precision; each explicitly stored intermediate retains its single-precision boundary | `float64` transient, never authoritative stored state; narrow only at the established single-precision stores, then truncate toward zero at the integer conversions; authored fields retain their existing storage types | `[05 R-PROD-01 §1]`, `[08 "Arithmetic and clamping"]`, `[08 R-P0-05 §5]` |
| AI metal-spot records and exhaustive-placement heap keys | authored feature-metal copy and helper-local negative squared-distance key, both `float32` | `[08 R-AI-03 §1]`, `[08 R-AI-03 §3]` |
| Survival authored cost and reward conversions | `float64` only for exact widening of a stored `float32` into the defined signed-64 conversion; no floating arithmetic is added | DESIGN_SURVIVAL §5 and §6.9; DESIGN_MULTIPLAYER §16.1 M1-C4–C5 |
| Canonical checkpoint encoder | `float32`/`float64` parameters copied to exact IEEE integer bits, with integer-mask NaN rejection; no floating arithmetic or new authoritative storage | DESIGN_MULTIPLAYER §16.3.6 |
| Portable numeric kernel: retail distance and its bounded truncation shortcut, unfused radian functions, immutable 65,536-angle table, and defined integer conversions | `float64` API values and working transients; integer significands implement distance rounding; radian coefficients and the angle table are immutable binary64 data; conversions narrow at their documented stores | `[01 R-DET-01 §1]`, `[01 R-DET-01 §2]`, `[01 R-DET-01 §7]`; DESIGN_MULTIPLAYER §16.1 M1-C1–C4 |
| Leash, construction/work reach and air-order planar distances, including footprint pads | `float64` transient through the portable distance kernel; signed input words and raw differences, scaled pads and output narrowing follow each caller | `[01 R-DET-01 §7]`, `[04 R-STANCE-01 §4]`, `[04 R-AIR-01 §8]`, `[05 R-WORK-01 §2]` |
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
| The macOS Metal renderer (`internal/meshscene`, `internal/metalhud`, `internal/platform/metalrender`, `internal/platform/mtl`, `cmd/nanolathe/metal_*_darwin.go`), including its copy of the blast boosts | `float32`/`float64` presentation-only geometry, poses, shader operands and timing, built from the committed frame pair the production client pins; never simulation inputs | DESIGN_METAL_RENDERER §3 |
| Enhanced model normals and battle-light response (`internal/client/model_compose.go`, `internal/platform/gpurender/lighting.go`) | `float32`/`float64` presentation-only geometry and light calculations; never simulation inputs | DESIGN_GPU_RENDERER §23 |
| Enhanced coastal surface and particle geometry (`internal/client/water_wakes.go`, `water_motion.go`, `water_buildings.go`, `internal/platform/gpurender/water.go`, `water_reflections.go`) | `float32`/`float64` presentation-only screen geometry, drift, fades and shader operands; never simulation inputs | DESIGN_GPU_RENDERER §26 |
| Offline film capture (`internal/film`: overlay glyph rasterization, camera-track easing, cue envelopes) | `float64` presentation-only capture geometry and timing; it runs after the frame is composed, reads no session state and is never a simulation input | docs/FILM_CAPTURE.md |
| Nanolathe screen layout, animation and preview camera (`internal/platform/screenkit`, `cmd/nanolathe/nlscreen*.go`) | `float64` presentation-only device-pixel geometry, easing and camera framing; the preview's own staged battle is never saved, networked or seen by a battle | DESIGN_INTERFACE_HUD_INPUT §3.17 |
| Unit viewer and its opt-in projected model preview (`cmd/nanolathe/unit_viewer*.go`, `internal/client/model_preview_projection.go`, `internal/drawlist/model_preview.go`, `internal/platform/gpurender/model_preview.go`) | `float64` presentation-only layout, orbit, zoom, fit/pivot bounds, fractional preview positions/depth and normalization, display conversions, the animation accumulator, the preview product's construction-fraction step (the shared step's working precision) and the nanospray's particle distance, lifetime and projection; no battle is created or advanced | DESIGN_DEVELOPER_TOOLS §7; DESIGN_GPU_RENDERER §22.5 |
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
package, and in a load-time package whose output the simulation reads (the
file system, format parsers, catalog compiler, Community tables and gameplay
selection), every product that feeds an add or a subtract — including one
formed in an earlier statement — carries an explicit floating-point
conversion, the specification's rounding barrier. It changes no operand
width, no constant, no evaluation order and no narrowing point. Presentation
packages are exempt.

**Library functions and conversions — in force** (DESIGN_MULTIPLAYER M1).
The standard library's transcendental functions
do not return the same bits on every architecture: `math.Hypot` is assembly
on amd64 and fused Go on arm64, and package `math` itself is compiled with
fused multiply-adds on arm64, where the guard below cannot see it. An
authoritative or load-time simulation-input package therefore calls only the
exactly rounded library functions — `math.Sqrt`, `math.Abs`, `math.Floor`, `math.Ceil`, `math.Trunc`,
`math.Round`, `math.RoundToEven` — and takes distance, sine, cosine,
arctangent, arccosine and tangent from the in-repository implementations in
`internal/sim/numeric`, each defined by the retail routine it stands for
(DESIGN_MULTIPLAYER §5.3 L1). And a floating-point value becomes an integer
only through a helper that defines the result for a value that does not fit
or is not a number: Go leaves that conversion to the processor, and the
processors disagree. The numeric kernel alone may perform raw conversions,
after enforcing the documented bounds. IEEE-754 classification and encoding
operations (`IsNaN`, `IsInf`, `Signbit`, the `Float32`/`Float64` bit conversions,
`Inf`, `NaN` and `Copysign`) inspect or construct bits rather than approximate
arithmetic; they remain permitted. This does not settle propagated NaN
payloads, which remain the explicit distance-kernel research Unknown.

**Check.** `grep -rn "float64\|float32" internal/` — every hit maps to a row
above or is presentation-only. `internal/architecture`'s
`TestAuthoritativeArithmeticIsNotFused` compiles the authoritative packages,
and the load-time packages whose output the simulation reads (the file system,
format parsers, catalog compiler with its mutators, Community tables and
gameplay selection), for arm64 and for `GOAMD64=v3` and fails on a fused
instruction outside its shrink-only allowlist, each entry of which argues that
its product is exact. `TestAuthoritativeNumericPortability` reads the same
packages and enforces both library-function identity and floating-to-integer
conversion types, including aliases, named types and generic constraints; no
caller outside `internal/sim/numeric` is exempt. Only these two guards read the
load-time packages; the other authoritative audits do not.

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
| in-range float → integer | truncate toward zero (`__ftol`) | `numeric.TruncateFloat64ToLow32` and its siblings (a raw `int32(f)` is refused by the portability guard, I2), `Fixed.Int` |
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

**Modern AI exception** (user-authorized 2026-09-24: "private random number
gen is fine, keeping its own state is fine"). The Modern AI computer player
(`internal/aikit`: a computer player marked Modern, in any gameplay mode since
the user made it a per-player choice on 2026-09-25; a Classic player binds no
such controller, so a battle of Classic players is untouched) may vary its
decisions with one private PCG32 generator per computer player
(`aikit.Rand`), owned by that player's controller and seeded from the
battle's simulation seed and the player slot. It never draws from or seeds
either authoritative stream, so it cannot displace their draws; only that
player's brain draws from it, inside its think, so a game replays exactly
from its seed and a background think cannot race the simulation. The
controller's engine upkeep still takes the retail step's strategic-refresh
draw from the simulation stream, in the retail position. This is the one
exception to "no per-entity streams", and it covers AI decisions only
(DESIGN_SESSIONS_AI_SAVE "Modern AI computer player").

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
The fixed effect pool is such a pool for both streams: at capacity it
refuses a shatter fragment before that fragment's simulation-stream draws
`[04 R-COB-04 §3]` and a land-dust puffer before its CRT draws, so how long
its records live is a determinism input too (DESIGN_MULTIPLAYER §5.3 L9).

**Multiplayer seed handoff** (DESIGN_MULTIPLAYER §8.3). In a lockstep battle
the lobby's host draws the explicit seed pair in `cmd/nanolathe` when it
freezes the configuration, whose digest covers it, and every client enters it
into composition through the same handoff a single-player battle uses. The
host's `crypto/rand` is outside every authoritative package, and no
client-side history seeds either stream.

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

**Command references** (DESIGN_MULTIPLAYER §7.2; in force from its milestone
M2). A human command that waits a network round trip before it applies names
a unit by its handle plus an allocation serial. A session counter assigns a
fresh serial at each successful unit creation and never reuses or wraps one,
and a reference whose serial does not match the slot's occupant is dropped
rather than redirected. This adds no generation tag to the pools: slot
allocation, immediate reuse and every internal retail reference are
unchanged, and a stale damage packet still aliases as retail's does. The
serial and its counter are authoritative state.

`pool.Projectiles` is the sole allocation/dead/count authority. The projectile
pool **appends at the tail** and never fills holes; dead records
set a flag without decrementing the count; compaction is stable and runs before
presentation `[06 §5.2]`.

## I6 — Presentation boundary

**Rule.** Sim never reads wall-clock time, input state, camera, or renderer
state. Presentation never writes sim state. The only channel is the committed
frame, published once after every completed sub-tick and sampled at the
current committed tick by the renderer, or by Enhanced presentation the two
most recent committed ticks and bounded renderer-owned copies of held walk-axis
endpoints (DESIGN_GPU_RENDERER §13.5). The active runtime
has one authoritative session implementation, hosted by the Ebitengine window
or the graphical command's headless mode and dedicated headless command
(ARCHITECTURE §5). A host may measure elapsed time or profile execution, but
those observations never determine simulation state or tick behavior.
Presentation and front-end random histories never become session seed inputs;
the explicit battle seed pair is the only RNG handoff into composition
(DESIGN_RUNTIME_DETERMINISM §2.2 and §5).

**Seats and hosts** (adopted with DESIGN_MULTIPLAYER §5.4). Which seat is
local, the viewing slot and the host's pump sizes are presentation inputs: no
authoritative state, draw count or draw order may depend on them, outside the
single-seat equivalences of DESIGN_MULTIPLAYER §6.5, where a single-player
battle's one human seat is the perspective every such read resolves to. And
nothing a host supplies after composition — a renderer, an art cache, an
audio service, a preference — may change a lifetime, a pool's occupancy or a
draw. The fixed effect pool is authoritative state although it is kept
beside the publication boundary. Its authored holds come from the battle's
immutable `content.SimArt`, bound before the first unit script; no host timing
resolver exists (DESIGN_MULTIPLAYER §5.3 L9, M1). The committed frame's
selection and command-page sections, and the selected bit and page field of
its unit and contact copies, are presentation-only: the host composes them
from its local interface state keyed by allocation reference
(DESIGN_MULTIPLAYER §7.3, M2 U3), the session writes neither bit, and the
frame buffer retains each tick's local-interface facts — step 7's readiness
verdicts, reported through an observation sink that writes nothing — until
the host drains them. The reads of the local seat
and viewing slot listed in that design's §6.3 do not yet meet this paragraph
and remain its milestone M5 work. A diff must not add another exception.

**Why.** Retail's draw path samples the accumulators exactly as committed at
the current tick; no interpolation between updates exists `[03 §2.4]`.
Original preserves that sampling. Enhanced presentation
(DESIGN_GPU_RENDERER §13.5) is the one path allowed to read the two most
recent committed ticks and the clock's carry, blending them in retained
presentation buffers and retaining bounded copies of held walk-axis endpoints; it writes nothing back, consumes no simulation RNG, and
the simulation still publishes only after the complete phase sequence
[01 §4.4]. `--shot` and Original never blend.

**Check.** `grep -rn "time.Now\|time.Since" internal/{clock,units,orders,cob,movement,path,economy,construction,features,combat,visibility,ai,mission,triggers}` returns nothing. `internal/client` imports sim packages; no sim package imports `internal/client`. The frame blend is owned by `internal/client/interpolate.go`, its walk-history adapter and `internal/poseblend`; none writes a committed frame. `frame.Buffer.Previous` has exactly two production callers: that file, which performs the Enhanced blend, and the live battle benchmark's census in `cmd/nanolathe`, which only counts which units moved between two committed ticks and feeds nothing back. The older frame of a pinned pair (`frame.Buffer.PinTick`/`PinLatest`, DESIGN_GPU_RENDERER §13.13) is likewise read only in that file.

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
Current contracts are DESIGN_WEAPONS_PROJECTILES §2.3.1 (terrain admission),
§2.3.2 (friendly and feature obstruction), "Modern submerged target release", and
"Modern threat targeting and incoming fire", DESIGN_INTERFACE_HUD_INPUT
"Modern submerged wreck picking", DESIGN_UNITS_ORDERS_COB
"Modern Hold Fire" and "Modern danger response", DESIGN_MOVEMENT_PATH
"Modern danger escape", "Modern learned terrain", "Modern group-order
spreading", "Modern bounded path work", "Modern unreachable moves", "Modern
route straightening", "Modern wedge escape" and "Modern traffic" with its
seven parts (which retired "Modern re-route staggering", "Modern allied
pass-through", "Modern jam release" and "Modern pocket release" from Modern;
those remain documented as the pathfinding laboratory's baseline),
DESIGN_INTERFACE_HUD_INPUT "Modern group destination slots", and
DESIGN_ECONOMY_CONSTRUCTION "Modern factory-exit yielding", "Modern construction-site yielding",
and "Modern authored build membership", and
DESIGN_SESSIONS_AI_SAVE "Modern save unit limits" and "Modern wave air
targets", and DESIGN_UNITS_ORDERS_COB "Modern AI move retention", and
DESIGN_UNITS_ORDERS_COB "Modern infection" with DESIGN_SURVIVAL "Modern
infection hunters", DESIGN_WEAPONS_PROJECTILES "Modern infector target
preference" and DESIGN_SESSIONS_AI_SAVE "Modern AI infector focus". Each
departure reaches its algorithm through the
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

**Multiplayer** (user-authorized 2026-10-01) is likewise available in every
mode. It is a session kind — relayed deterministic lockstep, every client
running the whole simulation from one command stream — not a rule, so it
adds no seam and binds the rule set the lobby agreed like any other battle.
"Strict 3.1 online" is owner-machine equivalence: each seat's work runs as
that seat's own retail machine would have run it, because retail multiplayer
has no single outcome to match. Its online policies (cheat permission for
world-changing commands, no pause or speed change in the first releases,
room-wide view restrictions) are Nanolathe's in every mode and are recorded
as such, not as retail behavior. Q22–Q25, approved 2026-10-02, additionally
define shared directed declarations, canonical per-player exploration with
own/hosted reset scope, request-tick map sharing, explicit per-computer
difficulty and initial computer exclusion from Deathmatch. Strict retains
one computer per human; Modern/Community allow multiple within available
seats through the existing RuleSet, as documented in DESIGN_MULTIPLAYER
§6.6–§6.7. A single-seat battle is unchanged, and every
fingerprint lock runs single-player. It is owned by
[DESIGN_MULTIPLAYER](DESIGN_MULTIPLAYER.md) and is not yet implemented.

**Unit restrictions** (user-authorized 2026-10-05) are likewise available in
every mode, for skirmish and Survival. A restriction is retail's multiplayer
per-definition count — 0 removes the definition from the per-battle catalog
clone at battle entry, 1–100 caps each player's records of it at the
allocator's existing per-definition test — applied before the mutators, with
no seam, no RNG and no per-tick state; campaign missions keep their own
authored unit lists. Every player obeys it, Survival's attacker included:
Classic computer players meet a capped product as retail's allocator refusal,
and the Modern AI computer player does not choose a product its own records
have reached. `norestrict` definitions are never restricted and nothing is
seeded, so an empty set leaves the catalog, its hash and every identity
untouched. Strict 3.1 *with no restrictions* is the retail baseline, and every
fingerprint lock runs with none. It is owned by
[DESIGN_MODS_MUTATORS](DESIGN_MODS_MUTATORS.md) §15 and is not yet
implemented.

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
explicit lifecycle, switching and save design before implementation. The
Modern AI computer player (user-authorized 2026-09-24) is the one stateful
think step: its cached planner stays zero size, its controller lives in the
computer player's manager, and its lifecycle, switching and save behaviour are
in DESIGN_GAMEPLAY_RULES "The Modern AI controller". No rule set binds it as
its own step: `mods/aikit` installs it in the session's one slot for it, and
the session gives it, with full income, to each computer player the lobby
marks Modern (user-authorized 2026-09-25, a mode-independent exception like
mutators and Survival: it chooses who decides for a player, never a rule;
DESIGN_SESSIONS_AI_SAVE "Modern AI computer player" and
DESIGN_ECONOMY_CONSTRUCTION "Modern AI full income"). Content
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
"Content profiles"): a load-time description of the mounted content set — a
directory table and the limits its content needs — taken from the running
mod's own config (the base game's is built in; nothing is detected) before the
catalog compiles, never reaching a tick and orthogonal to the gameplay rule
set, with provenance and the catalog hash staying retail-named.

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
On macOS, `--metal` (user-authorized 2026-10-08) plays one `--map` or
`--mission` battle in the experimental native Metal renderer of
[DESIGN_METAL_RENDERER.md](DESIGN_METAL_RENDERER.md). It is a command-line
host, not a persisted option, and like the other renderers it reads only the
committed frame pair and never changes authoritative state.

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
The Nanolathe canonical checkpoint stream is also an explicit byte boundary
(DESIGN_MULTIPLAYER §16.3.6): diagnostic hashing and byte capture, with no
restore reader or alternate save codec. Its schema never dictates Go layout.
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
