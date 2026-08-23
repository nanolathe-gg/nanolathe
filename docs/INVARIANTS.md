# Invariants

Rules that apply to every phase. A diff that violates one of these is rejected
even if its tests pass. Each rule states what it is, why retail forces it, and
how a reviewer checks it.

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
| Resource stocks, ledger carry, debt/accept ratios | `float32` | `[05 "Player slot"]`, `[05 "Two-stage settlement algorithm"]` |
| Wind scalar published to consumers (clamped to 1.0) | `float32` | `[01 §7.3]` |
| Clock budget product `delta × speed + carry` | `float64` product, `float32` carry | `[01 §4.2]` |
| Ballistic discriminant, `acos`, `sqrt` | `float64` | `[06 §3.3]` |
| Model piece rotation trig in the draw path | `float64`, round-to-nearest | `[03 §2.4]` |

Everything else is integer. Simulation velocity integration uses the fixed-point
trig tables, **not** the float path `[03 §2.4]`.

**Check.** `grep -rn "float64\|float32" internal/` — every hit maps to a row
above or is presentation-only.

## I3 — Truncation toward zero

**Rule.** Narrowing follows retail's `__ftol`: truncate toward zero, never round,
never floor. Go's `int32(f)` is correct; `math.Floor` and `math.Round` are not.

Cell/tile math on possibly-negative world coordinates is the opposite case: it
needs **floor** division, because retail uses an arithmetic shift with a sign
correction `[03 §2.1]`. Use an explicit helper, never `/`:

```go
func floorDiv(a, b int64) int64 { q := a / b; if a%b != 0 && (a < 0) != (b < 0) { q-- }; return q }
```

**Why.** `x / 65536` on `x = -1` yields 0, but the arithmetic shift yields -1.
The two disagree exactly on the map's west and north edges.

## I4 — Two RNG streams, call order is behavior

**Rule.** One global Park-Miller simulation stream (`16807 / 127773 / 2836 /
0x7fffffff`, Schrage) and one CRT stream (`*214013 + 2531011`). No per-entity
streams, no `math/rand`, no `crypto/rand`.

Consumers must draw the documented number of times **even when the result is
discarded**: a pool-full fire attempt still draws up to two spread values
`[06 §4.4]`; feature reproduction draws once per eligible visit even at
`reproduce=0` `[05 "Feature reproduction"]`; `bound < 2` returns 0 without
advancing `[01 §7.1]`.

Which stream: gameplay uses the simulation stream; meteor geometry `[06 §6.5]`,
screen shake `[03 §5.6]`, audio variant selection `[03 §8.3]`, and briefing wind
`[08 "Wind initialization"]` use the CRT stream.

**Check.** `rng.Global.Draws()` is stable across two identical headless runs.

## I5 — Pools, not handles

**Rule.** Fixed-capacity pools with slot 0 reserved as null, lowest-free
allocation, immediate reuse, no generation tags. A stale damage packet
addressing a reused slot is accepted — that is retail `[06 §5.1]`.

Capacities: units per `[01 §6.1]`, projectiles `300 × 107` bytes, COB threads
`8 × 164`, order nodes 86 bytes, fixed effects `300 × 0x54`, effect strips
evicting oldest-first above 400.

The projectile pool **appends at the tail** and never fills holes; dead records
set a flag without decrementing the count; compaction is stable and runs before
presentation `[06 §5.2]`.

## I6 — Presentation boundary

**Rule.** Sim never reads wall-clock time, input state, camera, or renderer
state. Presentation never writes sim state. The only channel is
`internal/snapshot`, published at the end of a tick and read by the renderer
with an interpolation `alpha`.

**Why.** This is the one deliberate divergence from retail's draw path, which
samples committed state with no interpolation `[03 §2.4]`. We interpolate for
modern motion; the sim is unaffected because it never observes `alpha`.

**Check.** `grep -rn "time.Now\|time.Since" internal/{clock,kernel,units,orders,cob,movement,path,economy,construction,features,combat,visibility,ai,mission,triggers}` returns nothing. `internal/client` imports sim packages; no sim package imports `internal/client`.

## I7 — Tick phase order

**Rule.** The kernel runs the twelve phases of `[01 §4.4]` in order, incrementing
the global tick before phase 1 of each sub-tick. Subsystems register into a
named phase; nothing runs outside one.

Within a tick, the same-tick callback windows of `[GAP T15]` hold: unit update
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

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.


**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

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

## I12 — Standard library first

Use `sort`, `slices`, `io/fs`, `errors` shapes rather than bespoke utilities. No
new module dependencies without orchestrator sign-off; `go.mod` today is Kaiju
plus `golang.org/x/image`.

## I13 — Research byte offsets are identity, not layout

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

**Publication omission:** Raw-analysis detail or a retail example was omitted from this public edition. This editorial omission is not a new behavioral finding.

The exceptions — where byte layout *is* the contract, because bytes cross a
boundary — are: file formats in `formats/`, the 13-byte plot cell (`[03 §2.2]`),
the 28-byte game-time save box, `HAPIBANK` headers and account records, the
9-byte damage packet's wire form if it is ever serialized, and route save
records. Everything else is a Go struct.

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
