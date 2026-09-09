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
| Meteor parameter installation (spacing, duration and interval) | `float64` transient after the source `float32` store; signed-64 truncation retains the low 32 bits, with no intervening float store | `[06 §6.5]`, `[01 R-DET-01 §1]` |
| Map tidal scalar, stored once at map load without fixed-point conversion | `float32` | `[03 R-TERR-01 §6]`, `[05 R-PROD-01 §4]` |
| Wind scalar published to consumers (clamped to 1.0) | `float32` | `[01 §7.3]` |
| Clock budget product `delta × speed + carry` | `float64` product, `float32` carry | `[01 §4.2]` |
| Ballistic discriminant, `acos`, `sqrt` | `float64` | `[06 §3.3]` |
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
| AI resource-score expressions (`energyRaw`, `metalRaw`) | `float32` temporaries and inputs; `TODO(question)` on exact x87 spills | `[08 "Established AI-facing data and rooted planner"]` |
| AI metal-spot records and exhaustive-placement heap keys | authored feature-metal copy and helper-local negative squared-distance key, both `float32` | `[08 R-AI-03 §1]`, `[08 R-AI-03 §3]` |
| Simulation trig-table construction at initialization | `float64` transient; authoritative table entries are integers | `[04 §5.1]` |
| Model piece rotation trig in the draw path and admission-time shatter pose | `float64`, round-to-nearest; geometry narrows back to fixed point | `[03 §2.4]`; approved current-simulation-pose departure in DESIGN_UNITS_ORDERS_COB §3.3 |
| Queued-order range-ring adaptive chord count `trunc(radius × 2π × 1/8)` | `float64` presentation transient, narrowed immediately to the integer chord count | `[07 R-P0-11 §3]` |
| Shatter-fragment normal construction — each reciprocal-65535 vertex conversion, vector difference, cross-product component, and normalized component narrows at its named binary32 result; square, sum, square-root and division use working precision until that component store | `float64` transient, `float32` named stores | `[04 R-COB-04 §3]` |
| Nanolathe particle travel distance (`sqrt`, truncated to the tick count) and the nanoframe reveal's barycentric interpolants | `float64` presentation temporaries, never stored | `[03 §5.5]`, `[03 §5.2]` |
| Load-time 2× art synthesis (`internal/upscale`: PCA basis, feature distances, tone terms) | `float32`/`float64` presentation-only; runs on the loader goroutine, its output is index art the simulation never reads | DESIGN_GPU_RENDERER §14.4 |
| Strip-object span `sqrt` — the sprinkle's `len` and the flame segment's span, over raw 16.16 deltas, truncated before the fixed-point step is formed | `float64` transient, never stored | `[03 R-FX-01 §3]`, `[03 R-FX-02 §2]` |
| AI strategic-centre weighted accumulators and per-unit weight (weighted centroid of complete own units, truncated into three 16.16 words) | `float32` transients, never stored | `[08 R-P0-05 §10]`, `[08 R-AI-01 §16]` |

Everything else is integer. Simulation velocity integration uses the fixed-point
trig tables, **not** the float path `[03 §2.4]`.

**Check.** `grep -rn "float64\|float32" internal/` — every hit maps to a row
above or is presentation-only.

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
| world → cell/tile | floor with sign correction `[03 §2.1]` | `world.WorldToCell` / `WorldToTile` |
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

Which stream: gameplay normally uses the simulation stream; meteor geometry
`[06 §6.5]`, screen shake `[03 §5.6]`, audio variant selection `[03 §8.3]`, and
wind's next-change interval `[01 §7.3]` use the CRT recurrence. Audio advances
its private copy; briefing wind advances the private front-end stream. Later
battle wind strength and 16-bit heading use the simulation stream.

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

**Check.** `grep -rn "time.Now\|time.Since" internal/{clock,units,orders,cob,movement,path,economy,construction,features,combat,visibility,ai,mission,triggers}` returns nothing. `internal/client` imports sim packages; no sim package imports `internal/client`. Outside `internal/client/interpolate.go` the frame path has no `Lerp`, `alpha`, or previous-frame read; `frame.Buffer.Previous` has no caller outside that file and its tests.

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

## I11 — One behavior

No compatibility flags, no "retail mode" toggles, no alternate code paths for
mods. Where retail's behavior is a bug (unguarded divide, wrapped deadline,
stale pointer after compaction), reproduce it and cite it; do not defend against
it. Bounds checks that reject data retail would accept are the one exception and
must be noted in the plan's Divergences.

The renderer is the sanctioned presentation switch of
[DESIGN_GPU_RENDERER.md](DESIGN_GPU_RENDERER.md): currently classic or modern,
with all GPU prototypes behind `--renderer=modern` and classic the default.
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
9-byte damage packet's wire form if it is ever serialized, and route save
records. The active save boundary is retail account parsing and staged battle
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
