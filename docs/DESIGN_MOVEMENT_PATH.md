# Design — Movement, path search and flight

`internal/path`, `internal/movement` and `internal/airdiag`. The weighted A\*
that answers "can this unit get there, and by which cells", the profiles and
stamped layers that decide which cells it may stand on, the route it follows,
the ground integrator and its collision commit, the flight command block and
the air executors that fill it, transports and their cargo, and the diagnostic
harness that flies one aircraft through a real session and writes down what
happened every tick.

This is one of the design documents listed by [ARCHITECTURE.md](ARCHITECTURE.md);
that document owns package boundaries, the tick, and the citation routing that
names this document as the home of `[OW-3-P]`, `[P0-I03]` — the path scheduler
and its registration in the tick — and `[P1-I02]` — hover, naval, amphibious,
transport and flight. A bare `[Cn]` in `internal/movement` or `internal/path` is
contract `n` of §3 below.

## 1. Purpose and boundary

These packages answer three questions, and nothing else:

* **Where may this unit stand?** One compiled movement profile per unit, one
  packed passability layer per movement class over the whole map, and one
  footprint validator that every proposal — a mover's next tick, a factory
  exit, a build site — goes through.
* **How does it get there?** One weighted A\* over the attribute-cell lattice,
  one goal per request drawn from a small family of shapes, one global
  scheduler that admits a single request at a time and charges it per-player
  work, and one publication that is full or empty and never a prefix.
* **How does it move once a route exists?** One ground follower and integrator
  with a synchronous occupancy commit; one flight command block, filled once a
  tick from whatever goal payload the active air order installed, that the
  flight integrator is the sole consumer of.

The single most dangerous mistake here is inventing an avoidance behaviour.
Retail has no push, no yield, no sidestep, no shortcut smoothing and no
collision-triggered replan. A blocked mover halves its speed, clamps against
its old footprint boundary and proposes the same move again next tick; what
looks like sidestepping emerges from two independent timers — the follower's
60-tick repath and the class layer's 30-tick occupant age — and from nothing
else `[04 R-COLL-01 §7]` `[04 R-MOV-01 §7]`. Every plausible-looking addition
in this area is invented behaviour, and it is indistinguishable from a traced
contract once merged.

The boundary runs at five places:

* **The tick belongs to `internal/session`.** The path scheduler runs first
  inside the fifth phase, ahead of the per-player order and work pumps; the
  mover sweep is the per-unit body of the second phase, driven as one explicit
  transaction — `BeginTick`, ascending `StepUnit`, `EndTick` — so that the
  occupancy grid is read and written in slot order and a later slot observes an
  earlier one's commit `[01 §4.4]` `[01 R-CORE-01 §4.4.1]` `[04 §8.2]`. Neither
  package reads a clock or logs inside a tick.
* **The order record belongs to `internal/orders`.** Movement owns the *payload*
  an order installs — the ground goal handle of `[04 §8.3]` and the air path
  marker of `[04 R-AIR-01 §4]` — and the pending bits an install or release
  raises on the record that owned the previous payload `[04 R-ORD-01 §0]`
  `[04 R-ORD-01 §1]`. It does not own the queue, the descriptor table or the
  pump's result codes. The queue selects the steering target; it never gates the
  mover tick, which runs for every live unit that has a mover, under the owner's
  control byte alone `[04 R-MOV-03 §1]`.
* **Terrain and the plot cell belong to `internal/world`.** Classification reads
  the plot cell's own derived height pair; there is no height aggregate across a
  footprint in any movement classifier `[04 R-SLOPE-01 §3]`. Mobile occupancy
  lives in the plot cell's two `uint16` words — ground plane and air plane — and
  the occupancy grid and those words are written by one call so they cannot
  disagree `[03 §2.2]` `[04 R-COLL-01 §4]`.
* **Combat, COB and the economy are callers.** The mover sweep raises
  `StartMoving`, `StopMoving`, `MoveRate` and `setSFXoccupy` on the unit's
  script through the deferred-wake barrier `[04 §5.2]` `[04 §9.1]`; the air-base
  registry's third list is refreshed on its own 30-tick cadence and read stale
  in between `[06 §3.1]` `[04 R-AIR-01 §11]`; pad repair is one `SelfRepair`
  record pushed by a landing phase and settled by the shared construction step,
  never by anything in this package `[04 R-AIR-01 §6]` `[05 R-WORK-01 §3]`.
* **Presentation is not a caller at all.** No camera or window state enters
  either package [I6]. `internal/airdiag` reads committed values after a
  completed `Session.Step` and writes nothing into a simulation package.

## 2. Packages and key types

### 2.1 `internal/path`

**The lattice and the request** (`types.go`, `queue.go`). `Cell` is an
attribute-cell coordinate — sixteen map pixels to a cell; `Point` is a published
route point in signed world coordinates; `Rect` is an inclusive lattice
rectangle `[04 §7.1]`. `Status` carries the two notified codes, `0x100`
already-satisfied and `0x200` rejected `[04 §7.2]`. `Request` names the unit,
the player, the start cell, the goal and an activation token: a monotonically
increasing value the movement system binds to the order node that was head when
the request was submitted, so a late result cannot publish onto a queue head
that has since been replaced `[04 R-PATH-01 §8]`.

**Goals** (`goals.go`). `Goal` is the family interface: `Enumerate` yields the
goal cells, `StartSatisfied` is the early-exit predicate, `H` is the pre-scale
heuristic. Four ground families and two air surfaces exist — `PointGoal`,
`AnnulusGoal`, `RectPerimeterGoal`, and `AirWorkGoal`/`AirMovingGoal`, which
carry a zero heuristic, enumerate nothing and never report the start satisfied,
so a serialized air surface cannot satisfy a ground search `[04 R-PATH-01 §9]`.
The annulus family stores the raw authored radii the heuristic clamps against
and, separately, the `>>4`-quantized squared radii the arrival predicate
compares. That is two unit systems in one family; it is the established
contract and is reproduced, not repaired `[04 §7.4]` `[04 R-PATH-01 §9]`.

**The open list and node store** (`heap.go`). The heap key is `f = g + hScaled`
and comparisons are signed and strict. Equal keys have no insertion-order
tiebreaker: sift-down chooses the left equal child, while root replacement can
move a later equal-key node ahead. An expansion keeps its root spent until the
first new neighbour can replace it or a lowering relaxation displaces and
removes it. `NodeStore` is the allocated-node table keyed by cell, with node 0
invalid the way pool slot 0 is `[01 §6.1]`. `H` is write-once per node;
relaxation adjusts `f` by the `g` delta alone. Capacity is an ordinary Go slice:
retail's heap-exhaustion policy is not traced `[04 §11]`, and the route caps
belong to the publisher, not to the heap `[04 R-PATH-01 §1]`.

**The resumable search** (`search.go`). `SearchConfig` carries the start, the
goal, the heuristic scale, the mover's authored footprint pair for the
cell-to-world conversion, optional bounds, the passability accessor, the
request-initialization revision hook and the start direction. `Session` is one
request's search across budget slices: the touched-entry table with its status
and direction bytes, the goal-cell set, the write-once arrival tolerance, the
notified status, the node store and the heap. The neighbour fan is a value —
nine entries on the first expansion, a directed five-entry fan afterwards —
so expanding a node allocates nothing `[04 §7.1]`. `walkRay` is the pre-search
greedy forward walk with its alternating two-sided wall follow; its only product
is the acceptance threshold and the connect flag `[04 R-PATH-01 §5]`
`[04 R-PATH-01 §15]`.

**The scheduler** (`queue.go`). `Scheduler` holds one active request at a time.
`CandidateProvider` is the admission surface the movement system implements:
player count, unit limit, per-player eligibility and a stable per-player cursor
poll — path derives neither cursor nor eligibility from the queued requests
`[04 R-PATH-01 §6]`. `SearchFunc` is the search boundary and reports the work it
actually did, setup-ray steps and heap pops, which is exactly what the scheduler
charges. `PublishFunc` is called only when the search is done, never at a budget
boundary. `Trace`, `RequestTrace`, `GoalTrace` and `SchedulerTraceState` are
value-only copies for diagnostics; no interface or pointer identity can reach a
deterministic report.

### 2.2 `internal/movement`

**The profile** (`profile.go`, `profile_footprint.go`). `Profile` is the eight
compiled movement-class fields: the footprint pair, the two water depths and
the four slope bytes. `Template()` is the startup record every class holds
before any parse — 255 in all four slope fields and depths of ±10000 — which is
also the scratch profile a unit gets when its authored `movementclass` name does
not resolve `[04 §6.1 R-DOC04-A]` `[02 §5]`. `classifyCell` is the single
per-cell classifier chain in its documented order — feature gate, deep gate,
shallow gate, medium split, slope tier — reading this cell's own derived
minimum and maximum and nothing aggregated across a footprint
`[04 §6.1 R-DOC04-B]` `[04 R-SLOPE-01 §2]` `[04 R-SLOPE-01 §3]`. Profiles are
per unit: passability, path bias, collision footprint and occupancy stamps are
that unit's identity, never a session-wide default.

**The class layer** (`layer.go`). `ClassLayer` is one packed two-bits-per-cell
passability image per movement class over the whole map, stamped once at map
load by running the classifier chain over every attribute cell; `ClassLayers` is
the per-class registry. A rectangle restamp re-runs the same chain over a
rectangle, so a dynamic blocker, a building's occupancy or a feature change
rewrites the same packing. `Revise` is the request-initialization pass: it
advances the record's revision watermark to `max(tick, 30) − 30`, re-stamps the
footprints of recently committed occupants and refreshes the requester's own
commit tick. Search consumption returns 0 out of bounds, 2 when the requesting
player's slot bit is absent from the visibility mapping word — the block is
unexplored — and otherwise the packed terrain tier; 1, 2 and 3 all expand and
only 0 hard-blocks, which is why retail units path optimistically straight
through fog `[04 §6.1 R-DOC04-B]` `[04 R-PATH-01 §2]`.

**Routes** (`route.go`). `Route` is up to twenty published points plus the
active and dirty bits. `Publish` clamps the count first and applies the zero-
publication rule; `Prune` is the index-1 proximity test; `At` and `Export` are
the two read shapes, only one of which is a predicate; `EncodeRoute` and
`DecodeRoute` are the save form. Publication adopts the search's points
verbatim — the route-acceptance rule of `[04 R-PATH-01 §8]` belongs to the
follower's goal installer and to nothing else, and running it here is the defect
`[05 R-EGRESS-02]` describes.

**Ground steering** (`steer.go`). `SteerState` is the explicit integration
surface retail scatters across the unit record: position, heading, pending
heading and dirty bit, scalar speed, the definition's velocity, acceleration,
brake and turn rate, the height word, the map's sea-level byte and the
definition flags word. Desired heading wraps on the sixteen-bit circle, the
change clamps to the authored turn rate, and there is no reverse branch: speed
is non-negative and the step is forward only `[04 §8.1]`.

**The velocity triple.** The mover holds a signed 16.16 velocity per axis
beside its scalar speed word, and the two are different quantities: the scalar
is a magnitude, the triple is the displacement the commit step adds to the
position, `proposed = position + velocity` `[04 R-MOV-01 §1]`
`[04 R-COLL-01 §1]`. The ground speed update writes
`(-sin(heading, speed), 0, -cos(heading, speed))` — a ground mover never has
vertical velocity and there is no gravity term on that path `[04 R-MOV-01 §4]`;
the flight integrator writes all three and zeroes all three for any mode but
airborne `[04 §10.1]`; the blocked branch rewrites the horizontal pair at the
halved speed `[04 R-COLL-01 §1]`; and the carried branch copies the carrier's
triple, zeroing when the carrier has no mover `[04 R-FAC-02 §2]`. Every one of
those sites publishes the triple onto `units.MoveState`, which is how it leaves
this package: its one reader elsewhere is the pre-fire lead of `[06 §3.3]`,
which multiplies the **target's** triple by the scaled flight time. The retail
mover save record instead reads the collision mirror, so the carried branch
writes all three copied words to `CollisionState` as well as `units.MoveState`;
an attached aircraft's `FlightState` receives the same triple. `RestoreMover`
republishes the saved triple `[08 R-SAVE-02 §8]`.

**Collision and occupancy** (`collision.go`, `place.go`, `forget.go`).
`OccupancyGrid` is the mover store and the writer of the plot cell's two
occupancy words; the plane a mover writes is its committed mover mode — 1
grounded, 2 airborne, 0 attached — so an aircraft over a tank contends with
nothing `[04 R-COLL-01 §4]` `[04 R-AIR-01 §3]`. `CollisionState` is one mover's
committed transform, its cached anchor and mode, the rectangle its stamp
actually added, the blocked bit, the last-stamp and last-proposal ticks and the
building/yard split. The sector filing and the overlap scan are the same
grid's second index: a stamp head-inserts into its sector bucket, so a bucket
reads back in reverse order of each unit's most recent relink, and the scan
walks the rectangle's own sectors grown by one on every side
`[04 R-COLL-01 §4A]` `[04 R-COLL-01 §11]`. `PlaceUnit` is the direct
position commit — retail's carried-position setter, the commit's success branch
without the validator — used by the teleport row and by carried motion
`[04 R-COLL-01 §4]` `[04 R-SPEC-01 §2]`. `ForgetUnit` is the only lifecycle
cleanup: pool slots are reused by handle, so a new unit landing on a dead one's
slot must not inherit its footprint, profile, tier or occupancy.

**The composition root** (`integrate.go`). `System` holds the terrain, the
compiled class table, the occupancy grid, the scheduler, the per-handle route,
steer, flight and collision maps, the per-handle resolved profile, the class
layer registry, the air sector grid, the per-tick cargo snapshot and the
air-base registry. `BeginTick` builds the deterministic per-tick indexing —
the carried set in player-then-slot order and the throttled air-base rebuild;
`StepUnit` runs one unit's whole mover tick; `EndTick` slaves cargo to its
carriers after every carrier has moved. `StepUnit` refuses to run outside that
transaction rather than reconstructing a snapshot of its own. The per-unit body
is: the carried early exit, then either the air path or the ground path; on the
ground path the follower's per-tick service answers arrival first, then the
route is consulted, then steering, occupancy commit, movement-rate callbacks
and occupancy-band classification. The post-move Y, pitch and roll correction
runs after the whole mover tick `[04 R-MOV-03 §1]` `[04 R-MOV-01 §1]`
`[04 R-MOV-01 §3]` `[04 R-MOV-01 §5]`.

`moveGoal` is the per-mover movement-goal handle, deliberately distinct from
the order record's stored position: a mobile-build record stores the
footprint's *centre*, and steering at that instead of at the chosen border cell
walks the builder into its own site `[04 §8.3]` `[04 R-ORD-02 §2]`.
`arrivalHandle` binds the arrival test to the same goal object the search used.
`hoverBob` carries the `canhover` inputs of the four-corner conform: the
per-unit phase word drawn by the allocator, the amplitude and the animation
counter `[04 R-MOV-01 §5]` `[04 R-MOV-01 §5a]` `[04 R-MOV-01 §5b]`
`[04 R-MOV-01 §5c]`. `MediumBand` is the edge-triggered occupancy band the
`setSFXoccupy` callback reports `[04 §9.1]`.

**Flight** (`flight.go`, `flightcommand.go`, `altitude.go`, `takeoff.go`,
`landing.go`, `airorders.go`). `FlightState` is the flight integrator's
surface: the mover mode, the position and three velocity components, the scalar
speed, heading and target heading, the turn residual, the definition's
velocity, acceleration, brake and turn rate, and the command altitude. The
integrator reads no order record at all. It reads the **flight command block**,
which the per-tick controller hook fills from whatever goal payload the active
air order installed; there is exactly one such supply and the orders differ only
in which payload they install `[04 R-AIR-01 §1]`. `airMarker` is that payload:
a flags word, a horizontal arrival radius, a signed altitude offset, a heading,
an attach-piece index, a weak target handle, a goal triple and a radial offset
`[04 R-AIR-01 §4]`. `AirSectorGrid` is the coarse second grid built once with
the terrain — 128-world-unit cells whose smoothed byte is the maximum terrain
height over the surrounding three-by-three block — and it, not the four-corner
terrain query, is what the per-tick cruise-altitude rule reads; its
out-of-bounds record is the off-map sector whose sentinel drives the vertical
bypass and the goal update's refusal to follow a target linked to it
`[04 R-AIR-01 §5]`. `SetMoverMode` is the one committed-mode setter, and the
mode is the occupancy plane rather than a moving flag `[04 R-AIR-01 §3]`. The
executors are the legs `VTOL_Move`, `VTOL_LandIfCan`, `VTOL_Standby`,
`VTOL_AirBuild` and `VTOL_Landing`, each building markers and reading the phase
tables of `[04 R-AIR-01 §6]` and `[04 R-AIR-01 §7]`; `QueryLandingPad` is the
synchronous four-output pad query with its free-pad predicate `[04 §10.2]`.
Cruise altitude is `max(sea level, terrain height at the target) + offset`,
scaled to 16.16 and capped at `0x1FF0000`, with no lower clamp `[04 §10.1]`.

**Cargo and transports** (`cargo.go`, `transport.go`, `admission.go`).
Attachment is a carrier handle plus a cargo list on the unit. Carried motion is
slaved each tick to the named attach piece's world transform through the shared
piece locator, which folds the carrier's committed heading matrix and negates Z
the way the model loader does `[04 R-REV-02]` `[04 R-FAC-02 §1]`; the cargo
copies the piece heading and pitch and the carrier's velocity, takes the
floater deck-height clamp, and returns before ordinary footprint validation
while still clearing and stamping through the carried-position setter
`[04 R-FAC-02 §2]`. `CanTransport` is the nine-reject admission predicate in
order `[04 §10.2]`. `legVTOLPickup` and `legVTOLUnload` are the two air
transport executors; both are pump-driven legs whose commands are ordinary air
path markers, and the gates, the interrupt bit, the drop point, the released
cargo's height, the two climb-aways and the `becarried` re-arm are
`[04 R-AIR-01 §9]` and `[04 R-AIR-01 §10]`, which also correct the load table's
phase-4 row: that climb-away marker is built and never installed.

**Save boxes** (`retail_save.go`, `retail_restore.go`). The detached mover image
is the fixed record the save format defines; the route, the follower and the
proposal are derived state and are absent from the boundary and cleared on
restore `[08 R-SAVE-02 §8]`.

**Diagnostics** (`audit.go`, `p28_parity_trace.go`, `airdiag_export.go`).
`AuditVerdict` and `AuditFinding` classify evidence at a cell and deliberately
choose no gameplay response; an unexplained case stays unexplained.
`MovementTrace` and `CollisionTrace` are value copies taken at a capture
boundary — the next route comes from the scheduler's own request trace, never
from a guessed route. `AirExecutorState` exposes an air executor's phase and
latches read-only so a harness can observe them without this package exporting
mutable state. Nothing in the simulation reads any of them.

### 2.3 `internal/airdiag`

The verification harness. It mounts a real install, compiles the catalog,
composes a two-player skirmish on an authored map, spawns a named aircraft,
issues a real order through the ordinary command boundary and records one `Row`
per authoritative tick: position, altitude, velocity, heading, mode, executor
phase and goal. `Trace` returns the rows and `Format` prints one. It is the tool
that turns "the gunship flies sideways" into a per-tick statement about which
command the block held, and it exists because the flight path is the one place
where a plausible-looking integrator can be wrong in a way no unit test notices.
Every test in it skips without retail assets.

## 3. Contracts

Contract numbers are the ones Go comments cite as bare `[Cn]` in
`internal/path` and `internal/movement`.

### 3.1 Search — C1…C10

**C1 — the lattice.** The lattice is the TNT attribute cell, sixteen map pixels
per cell. Route points come from cell coordinates plus the profile's
half-footprint bias `[04 §7.1]` `[fmt tnt]`.

**C2 — neighbour order.** Neighbours are visited north, north-west, west,
south-west, south, south-east, east, north-east. The first expansion attempts
nine entries — all eight plus one harmless duplicate; every later expansion uses
a directed five-entry fan centred on the parent's travel direction `[04 §7.1]`.

**C3 — diagonals.** A diagonal step checks **only** the destination cell, never
both cardinal corners `[04 §7.1]`.

**C4 — step costs.** Cardinal steps cost 16, diagonal steps 22. The
direction-change table adds `0, 40, 60, 80, 100, 80, 60, 40` for the eight
directional differences. A per-neighbour 30 is the steep-tier terrain cost and
applies to every live expansion; a short-run penalty of 75 applies when the
parent chain's straight run is below five and a parent exists `[04 §7.2]`
`[04 R-PATH-01 §3]`.

**C5 — heap order.** `f = g + h`; the heap compares signed values **strictly
less** with no sequence tie-breaker. Sift-down chooses the left child on an
equal child pair and stops when the moved key is equal to that child. An
expanded root remains spent until the next selection, unless the first new
neighbour replaces it in place or a strictly improving relaxation displaces it;
the latter removes the old root from its current heap position. Equal `g` does
not replace an existing parent `[04 R-PATH-01 §1]`.

**C6 — the weighted heuristic.** `hScaled = (h · scale) >> 16`, formed as a full
signed 64-bit product with an arithmetic shift; there is no floating point in
the search. `scale` is the per-player quantum, recomputed at each 150-tick
replenish as `base × {6, 3, 1}` for the tiers `serviceCount / divisor` below 1,
below 2 and at or above 2. There is no settings string, registry key or TDF key:
`base` is the compiled-in `0x18000` — 1.5 in 16.16 — whose only writer is the
developer console's `Search` command, and the tier divisor is the session's
per-player unit limit. The quantum is the heuristic weight and **only** the
heuristic weight; it never sizes a work slice. The effective weights are
therefore `0x90000`, `0x48000` and `0x18000` `[04 §7.2]` `[04 R-PATH-01 §6]`
`[04 R-PATH-01 §10]`.

**C7 — h is write-once.** `h` is evaluated once per allocated node and is not
recomputed on relaxation; relaxation adjusts `f` by the `g` delta alone
`[04 §7.2]`.

**C8 — the goal heuristics** `[04 §7.2]` `[04 R-PATH-01 §9]`:

* point/radius — `h = max(18·max(|dx|,|dz|) + 7·min(|dx|,|dz|) − R, 0)` against
  the raw authored radius; arrival is a squared-distance test against a
  `>>4`-quantized radius;
* annulus — the same inflated octile with a V-shaped zero band inside
  `[inner, outer]`, rising as `oct − outer` outward and `inner − oct` inward,
  with the same dual radius units;
* rectangle perimeter — the admissible `16·max + 6·min` outside and
  `16 × min(distance to each edge)` inside; the goal cells are exactly the
  border, and the constructor grows the argument rectangle by the owning mover's
  own footprint, so the border is the ring of anchor cells at which the mover
  stands flush against the target `[04 R-PATH-01 §12]`;
* the air surfaces — identically zero heuristic, empty enumeration, and a start
  predicate that is always false, so they cannot satisfy a ground search.

**C9 — arrival tolerance.** The tolerance is a write-once threshold: the greedy
forward-only ray walk stores the minimum scaled `h` over its frontier into a
slot that is never updated afterwards. Any opened neighbour at or below it gets
open-plus-goal status, and popping such a node terminates the search and
reconstructs `[04 §7.2]` `[04 R-PATH-01 §5]`.

**C10 — the early-exit ladder, in this order.** A non-zero start-satisfies-goal
predicate publishes empty with status `0x100`; an out-of-bounds start notifies
`0x200` and publishes empty; a ray that connects start to goal notifies `0x100`
but the search still seeds and runs; and when the ray's best scaled `h` is at or
above the start cell's own scaled `h`, the search notifies `0x200` and publishes
empty **without seeding**. An out-of-bounds enumerated goal cell is left
unmarked but still aims the ray `[04 §7.2]` `[04 R-PATH-01 §4]`.

### 3.2 Scheduler and routes — C11…C19

**C11 — the scheduler's two accountings.** The global counter replenishes every
150 calls and recomputes the per-player quanta `6×`, `3×`, `1×`; those quanta
weight the **heuristic** (C6) and nothing else. Work is a separate per-player
step accumulator, topped up once per call by an equal share
`stepAllowance / playerCount` in integer division, and charged exactly the setup
steps and heap pops the search reports. Each slice is limited to 100 heap pops.
A budget-exhausted slice ends the *iteration*, not the call: the request stays
latched and keeps taking slices from its own player's accumulator until that
accumulator goes non-positive. The scheduler runs once per tick, before the
per-player unit sweeps `[04 §7.3]` `[04 R-PATH-01 §6]` `[04 R-PATH-01 §10]`.

The movement provider reads eligibility from the session player record at the
actual slot index; the participant count remains only the equal-share divisor.
Sparse restored slots are never compacted or represented by a count cutoff
`[04 R-PATH-01 §6]`.

**C12 — full or empty.** Requests are full-or-empty. Budget exhaustion leaves
the heap and the request active and publishes nothing — never a partial prefix.
Heap exhaustion publishes an empty route, and carries no charge `[04 §7.3]`
`[04 R-PATH-01 §6]`.

**C13 — reconstruction.** Reconstruction walks predecessors backward, storing
the packed cell into a 64-entry ring at `index & 63` each time the direction
**changes**, with a wrap overwriting the oldest, then appends the start cell.
Emission walks masked indices downward, newest first, and publishes
`min(directionChanges + 1, 64)` points converted to world coordinates
`[04 §7.3]` `[04 R-PATH-01 §7]`.

**C14 — publication.** The publisher clamps any count above 20 to 20 first. A
non-empty publication writes the count and the points and sets active and dirty.
A **zero** publication clears active and sets dirty but writes **neither** the
count nor the array, so stale bytes stay physically present. Queries gate on the
active bit `[04 §7.3]`.

**C15 — pruning.** When the count exceeds one, compare the mover's signed
integer position against stored point index 1; if `dx² + dz² ≤ 25`, shift the
later points down, decrement, clear active below two points and set dirty
`[04 §7.3]`.

**C16 — the save form.** An inactive route serializes a two-bit count of zero;
an active route serializes `min(count, 3)` followed by that many signed 16-bit
X/Z pairs. This is the explicit-layout exception [I13] `[04 §7.3]`.

**C17 — the export helper is not a predicate.** It repeats the last stored point
for excess indices and reads adjacent fields at a zero count. Callers gate on
the active bit themselves `[04 §7.3]`.

**C18 — lazy revalidation.** A dynamic blocker bumps a revision counter. Heap
entries are **not** purged eagerly; passability is rechecked lazily when each
entry is opened at expansion `[04 §7.4]`.

**C19 — there is no smoothing pass.** The earlier contract described collinear
removal and a two-directional shortcut ray. It is withdrawn: reconstruction
publishes the direction-change points of C13 converted to world coordinates and
nothing else. Do not implement a shortcut or a collinear-removal step
`[04 §7.5]` `[04 R-PATH-01 §7]`.

### 3.3 Ground steering and collision — C20…C25

**C20 — heading.** Desired heading wraps on the sixteen-bit circle and the
heading change clamps to the definition's turn rate; the pending heading and the
dirty flag update before integration. There is no reverse-speed branch
`[04 §8.1]`.

**C21 — the speed cap.** Signed pitch is arithmetic-shifted right by 11 and
clamped to `[-5, +5]` to index the table `25, 55, 70, 85, 100, 100, 75, 50, 25,
20, 15`; the cap is `table[i] × MaxVelocity / 100` with signed truncation. If
the unit's signed height word is below the map's sea-level byte and neither
`canhover` nor `floater` is set in the definition flags, the cap halves
`[04 §8.1]` `[04 R-MOV-01 §4]` `[04 R-MOV-01 §8a]`.

**C22 — synchronous commit.** Occupancy commits synchronously in sweep order: a
unit that claims a cell first blocks a later one, a vacated cell is reusable in
the same sweep, and head-on swaps block. One unit's clear, commit and stamp
finish before the next slot begins `[04 §8.2]` `[04 R-COLL-01 §1]` [I1].

**C23 — the same-cell fast path.** If the proposed anchor pair and mover mode
equal the committed cached pair and mode, commit the transform and the dirty
state **without** calling the validator and without restamping — and therefore
without writing an occupancy commit tick `[04 §8.2]` `[04 R-COLL-01 §1]`.

**C24 — the blocked response.** A blocked result does **not** try X-only or
Z-only movement and does not push another unit. It caps scalar speed at
`MaxVelocity / 2` if higher, recomputes the horizontal velocity at that speed
and heading, clamps X and Z against the old footprint boundary using `0x7FFFF`,
commits, and marks the transform dirty without clearing or restamping
occupancy. On any non-stationary proposal the last-proposal tick is written
first, before the cell test and before the validator, so it records the last
tick on which the unit *tried* to move — blocked ticks included — which is the
age term the hover bob reads `[04 §8.2]` `[04 R-COLL-01 §1]` `[04 R-MOV-01 §5]`.

**C25 — the footprint validator.** The validator scans the proposed footprint
row-major, returns immediately on a rejecting per-cell predicate, and applies
the aggregate height, depth and slope gates after the scan `[04 §8.2]`
`[04 R-COLL-01 §2]`.

### 3.4 Flight and transports — C26…C31

**C26 — the mode gate.** The flight integrator runs only when the mover's low
mode bits equal 2. Any other mode zeroes all three velocity components, the
scalar speed word and the sixteen-bit turn residual, with no partial step
`[04 §10.1]` `[04 R-AIR-01 §3]`.

**C27 — per-component decay, before any command input.**
`decay = 0x10000 − trunc((Acceleration << 16) / MaxVelocity)`, then
`v = trunc((v · decay) >> 16)`. The division has **no** zero guard: a zero
`MaxVelocity` terminates retail, and the fault is reproduced rather than
defended [I11] `[04 §10.1]`.

**C28 — brake shaping.** With `h = hypot(vx, vz) / 65536` and
`b = BrakeRate / 65536`, only under the **strict** `h > b` — equality skips the
whole block — both horizontal components scale by `trunc((b / h) · 65536)` via
`>>16`, and then the excess `q = trunc((h − b) · 65536)` is subtracted per axis
through the fixed-point sine and cosine of the heading. The mixed fixed and
floating instruction order is kept; it is not algebraically rearranged
`[04 §10.1]`.

**C29 — vertical control.** With `dy = unitY − targetY`: if the unit's
sector-list head is the off-map sector record, the vertical assignment is
**skipped** and `vy` keeps its damped value. Otherwise
`yLimit = 65536` — one world unit — when `speed & ~3` is below `262144`, else
`speed >> 2`, and
`dy ≤ −limit ⇒ vy = +limit`; `dy < limit ⇒ vy = −dy` as an exact snap; else
`vy = −limit` `[04 §10.1]` `[04 R-AIR-01 §5]`.

**C30 — heading integration.** Independent of the vertical block:
`err = int16(targetHeading − heading)`; a zero error zeroes the turn residual,
otherwise the change clamps to the definition's turn rate `[04 §10.1]`
`[04 R-AIR-01 §2]`.

**C31 — transports.** Every phase re-checks that the target is non-null, that
the executor flags are free of `0x10048`, that the target is above sea level and
that an air carrier's cargo list is empty. `Unit is too heavy to transport` is
reproduced verbatim; the two event codes are 12 and 13; an interruption in the
attach phase ends the transport. Admission is the nine rejects in order — the
`cantbetransported` flag, a carrier without `canload`, the carried count against
`transportcapacity` as a count and not a sum of sizes, the signed
`transportsize` against the candidate's footprint width, a candidate with no
mover, a candidate in active locomotion, a ground carrier against a candidate
authoring a non-negative `MinWaterDepth`, and a submerged candidate
`[04 §10.2]` `[04 R-AIR-01 §9]` `[04 R-AIR-01 §10]` `[04 R-UNIT-06 §3]`.

### 3.5 Goal-family wiring — `[OW-3-P]`

`[OW-3-P]` is the seam between an order and a path goal: which order produces
which goal family, with which radii, and which families have no producer. The
rule that governs it is that this package supplies *structure* and never a
radius. Every radius is the handler's own, computed from authored data and
installed through the record's payload installer; the wiring below consults the
bound payload first and only falls back to a family when a record's payload is
not bound — one restored from a save, or one re-activated after another record
evicted the mover's single goal slot `[04 §7.2]` `[04 §7.4]` `[04 §3.5]`.

| Order | Family | Radii |
|---|---|---|
| Ordinary ground movement, patrol legs, and everything not listed below | point, radius 0 | none `[04 §7.2]` |
| `Attack_Chase` | whatever its own maneuver phase installed — a point goal for five of the six live substates, an annulus for the other two | every radius sized from the slot's weapon range: `d`, `d/2`, `trunc(d/4)`, `0`, annulus `(d, d/2)`, annulus `(2d, d)` `[04 R-ORD-01 §3]` `[06 R-WPN-05 §1]` |
| `Follow_Ground` (the ground guard's follow) | point at the ward's position plus the record's stored anchor offset | arrival radius is half the handler's `(FootPrintX(me) + FootPrintX(ward) + 2) · 16` `[04 R-ORD-01 §8]` |
| `HelpBuild` phase 0 | annulus at the target | outer `builddistance + half`, inner `half`, where `half` is the assist approach term over the **target's** footprint and `builddistance` is the builder's own `[04 R-ORD-01 §12]` `[05 R-WORK-01 §2]` |
| `RepairUnit` phase 1, `Capture` phase 0 | rectangle perimeter from the target's committed anchor cell and footprint | grown by the mover's own footprint `[04 R-PATH-01 §12]` |
| `Reclaim` phase 0, `Resurrect` phase 0 | rectangle perimeter from the **feature's** origin cell and size | the anchor already is the origin: a feature's stamp writes its index on the anchor and the fringe sentinel across the rest `[04 R-ORD-01 §5]` `[05 R-ECO-02 §2]` `[05 R-FEAT-01 §3]` |
| `MobileBuild` approach | rectangle perimeter at the product's anchor cell and footprint | no candidate generator, no range filter and no sort: the candidate set is the search's own border enumeration and the selection is the border cell it closes first `[04 R-PATH-01 §12]` `[04 R-PATH-01 §13]` `[04 R-MOV-03 §9]` |
| `VTOL_MobileBuild` approach (the air executor's phase 1) | air point marker at the goal snapped onto the product's footprint | horizontal arrival radius `builddistance`, strict; the executor sets the record's gate to `0xE0` with the install, and the construction service's approach phase advances into placement on that marker's outcome — not on the takeoff preamble's climb marker, which the same gate also delivers `[04 R-ORD-02 §2]` `[04 R-AIR-01 §6]` |
| `Park` (a no-rally product's terminal record) | rectangle perimeter on the rectangle the handler installed, centred on the product's own committed cell | `[04 R-FAC-02 §4]` `[04 §7.2]` |

Two surfaces are deliberately **unwired**, and each is unwired because no
established producer exists rather than because the work is outstanding:

* **The saved-goal compatibility surface.** The zero-heuristic, never-satisfied
  surface is owned by the two air goals, which is what the serialized air work
  and air moving forms are. Nothing else constructs it, and the withdrawn
  ground-side saved goal had no producer at all `[04 R-PATH-01 §9]`.
* **Air-goal chaining for patrol.** Patrol legs — `Patrol`, `QPatrol`,
  `VTOL_Patrol` — are queued as sequential point goals through the ordinary
  order queue. No bounded evidence shows patrol chaining through an air-goal
  surface, so that path stays unwired rather than being forced `[04 §3.3]`
  `[04 §7.4]` `[04 §10.3]`.

The inspection helpers in `internal/path` expose the private family fields so
this wiring can be asserted without inventing a public kind on `Goal`.

### 3.6 Not implemented

* **A saved route restores no goal.** The mover save box carries the mover's
  live words; the route, the follower state and the proposal are derived and are
  cleared on restore, so a restored mover re-arms an ordinary request rather
  than resuming a search `[08 R-SAVE-02 §8]`.
* **In-battle save restoration of an air payload is not supported.** The goal
  payload base declares six operations and the sixth is serialization; it has no
  consumer here `[04 R-AIR-01 §4]`.
* **Nothing in these packages heals.** Pad repair has exactly one producer — the
  landing phase that pushes a `SelfRepair` record on the lander it has just
  attached — and that record's work visits run the shared construction step with
  the pad's quantum and the pad owner's energy admission.
  `VTOL_GetRepaired` is a two-phase wait that never calls the helper
  `[04 R-AIR-01 §6]` `[05 R-WORK-01 §3]`.
* **The steep tier carries no forward-speed factor.** The per-neighbour search
  cost of the steep tier is the established 30 `[04 R-PATH-01 §3]`; a movement
  *rate* multiplier for soft terrain is not traced, and the classifier preserves
  the tier without inventing one `[04 §6.1 R-DOC04-B]`.
* **The heap has no exhaustion policy.** Retail's behaviour when its working set
  fills is not traced; the Go store grows `[04 §11]`.

## 4. Retail behaviour that is not a bug

* **Nobody yields, sidesteps or pushes.** The whole-image census found no
  yield owner: the commit validator never reads the class layer or the occupant
  age, and the age gate is search-only. A blocked unit re-proposes every tick at
  capped speed; because it stops advancing its last-stamp tick, after thirty
  ticks it becomes a hard search block for others, and its own sixty-tick repath
  finds a route around. That pair of timers *is* the avoidance mechanism
  `[04 R-COLL-01 §7]` `[04 R-MOV-01 §7]`.
* **Units path optimistically straight through fog.** A cell whose mapping word
  lacks the requesting player's slot bit reads as unexplored, and unexplored
  expands. Only a hard-blocked cell stops the search `[04 §6.1 R-DOC04-B]`
  `[04 R-PATH-01 §2]`.
* **There is no smoothing pass, and routes look angular because of it.** What is
  published is the direction-change points, converted to world coordinates
  (C13, C19) `[04 §7.5]` `[04 R-PATH-01 §7]`.
* **A route of twenty points on a long map is the cap, not a truncation bug.**
  The publisher clamps to twenty before writing anything (C14) `[04 §7.3]`.
* **The annulus family really does mix radius units.** Raw authored radii in the
  heuristic clamp, `>>4`-quantized squared radii in arrival. It is a dual-unit
  contract to reproduce, not a defect to normalize `[04 §7.4]`
  `[04 R-PATH-01 §9]`.
* **A no-rally aircraft hovering above its plant is retail.** Park becomes a
  vertical move to the aircraft's own position, so the order completes at the top
  of the initial climb `[04 R-AIR-02]`.
* **A column of products behind a *ground* factory is a defect, not retail.**
  Retail's no-rally products fan out around the whole border of the shared park
  rectangle. The column forms only when the follower's route-acceptance rule is
  applied at route publication instead of at the follower's goal installer:
  publication adopts the search's points verbatim, and running the rule there
  throws away every two-point route and rewrites it as a straight line at the
  rectangle's far-edge goal point `[05 R-EGRESS-02]` `[04 R-PATH-01 §8]`. What
  still stands is the rest of that section: nothing pushes a mover already
  parked, and the goal point is not made per-mover.
* **A factory whose product never moves is blocked indefinitely.** There is no
  push, no stacking and no force placement, and the script port often read as a
  "bugger off" broadcast has no engine reader at all `[04 R-FAC-02 §6]`
  `[04 R-COB-05]`.
* **A map authoring `gravity = 0` cancels every `AirStrike` order.** The release
  leg reads the map's gravity word before dividing and returns the cancel-all
  code on zero, emptying the bomber's whole queue. Bombers idle there in retail
  too (SC23) `[04 R-AIR-01 §8]`.
* **A definition authoring `MaxVelocity = 0` terminates.** The flight decay
  divides by it with no guard; the fault is reproduced (C27) `[04 §10.1]` [I11].
* **A carried unit is not a mover.** Transport removes it from the follower and
  the scheduler entirely; the queue record survives for the eventual unload
  `[04 §10.2]` `[04 R-PATH-01 §8]`.
* **An aircraft over a tank contends with nothing.** The occupancy plane is the
  mover mode, and the two planes are independent `[04 R-COLL-01 §4]`
  `[04 R-AIR-01 §3]`.
* **An aircraft whose rectangle has left the map keeps its intruder bit.** A
  unit filed in the off-map sector record is never reached by a restamp, because
  that record is not in the grid array `[04 R-COLL-01 §11]` `[04 R-AIR-01 §5]`.
* **A unit with no order still gets a mover tick.** The queue selects the
  steering target; it does not gate the sweep. An orderless mover is exactly the
  follower's no-waypoint case: it brakes without turning, never touches its
  heading, and still gets its post-move Y `[04 R-MOV-03 §1]` `[04 R-MOV-01 §3]`
  `[04 R-MOV-01 §5]`.
* **Pivot turns, reversing, arc turns and a minimum turn radius do not exist.**
  There is no key evidence for any of them in the movement class record; do not
  add them `[02 §5]` `[02 "Movement class record"]`.

## 5. Divergences

* **SC5 — the three movement clamps.** The entry recorded a divergence: because
  twelve of the reference install's fifteen `CLASS` sections omit
  `maxwaterslope`, compiling with a zero default and running the first clamp
  unconditionally set `maxslope = 0` for every land class that authored a real
  slope limit, and the compiler gated clamps 1 and 3 on whether the key was
  authored. That divergence is retired. The startup initializer pre-fills every
  class record before any parse — all four slope fields 255, depths ±10000 — so
  an omitted key carries the template value, the first clamp is the identity for
  a class that omits `maxwaterslope`, and all three clamps run unconditionally
  with no branch `[04 §6.1 R-DOC04-A]` `[02 §5]`.
* **The movement-class parse order.** Each class reads its eight keys in strict
  order — `FootPrintX`, `FootPrintZ`, `MaxWaterDepth`, `MinWaterDepth`,
  `MaxSlope`, `BadSlope`, `MaxWaterSlope`, `BadWaterSlope`. Footprints default
  to 0; every depth and slope field defaults to the record's prior value, which
  is the startup template's for the first class to touch it; `BadSlope` and
  `BadWaterSlope` default to a right shift of one on the `MaxSlope` or
  `MaxWaterSlope` just read. Then the three unsigned-byte clamps run
  unconditionally, in order: `MaxWaterSlope` caps `MaxSlope`, the resulting
  `MaxSlope` caps `BadSlope`, and `MaxWaterSlope` caps `BadWaterSlope`. The
  compiled profile this document consumes is retail's, and the order is part of
  the contract because the defaults chain through it `[04 §6.1 R-DOC04-A]`
  `[02 §5]`.
* **SC22 — the static layer and the dynamic-block policy.** The search
  passability predicate holds no direct occupancy lookup; it reads the
  request's movement-class layer. At request initialization that layer's
  watermark is armed at `max(tick, 30) − 30`, recently committed mobile
  footprints are re-stamped, and the occupant-age gate lets a recent occupant
  through while making an older one block its re-stamped cells. Existing heap
  entries are not purged; expansion rechecks passability lazily (C18). The
  scheduler and the expansion receive no blocker identity, velocity or projected
  destination, and no collision-triggered replan exists. Mover-versus-mover
  contention is authoritative at commit through the row-major validator, the
  half-speed clamp response and the synchronous clear/commit/stamp sequence.
  Building and yard occupancy stay part of the static inputs
  `[04 §8.2]` `[04 §6.1 R-DOC04-B]` `[04 R-MOV-02A]` `[04 R-COLL-01 §7]`.
* **SC23 — `gravity = 0`.** Retail's cancel-all bound is cloned exactly. An
  asset census of 275 stock maps found none authoring, omitting or defaulting
  the key to zero, so the bound is unreachable on shipped content and a
  substituted default gravity would be invented behaviour
  `[04 R-AIR-01 §8]` `[fmt ota]`.

The hover-bob animation counter is the one place these packages depart from
retail's arithmetic on purpose. Retail derives it from the wall clock the
presentation layer shares; deriving it from the tick instead keeps the rest of
the four-corner conform exact and confines the difference to which of `{0, −1}`
a hovering unit's height offset takes on a tick where wall clock and tick
disagree. Cloning the wall clock would re-fire an edge-triggered occupancy
callback on the unit's script at a wall-clock cadence, because the band-2 test
is an equality against sea level and ten stock `canhover` definitions author a
waterline of zero `[04 R-MOV-01 §5b]` `[04 R-MOV-01 §5c]` `[04 §9.1]` [I2].

## 6. Research map

| Behaviour | Owning research |
|---|---|
| Terrain classification, the classifier chain, the class layer and its packing | `[04 §6.1]`, `[04 §6.1 R-DOC04-A]`, `[04 §6.1 R-DOC04-B]` |
| Footprints, yard maps, and the placement seam | `[04 §6.2]`, `[04 §6.4]`, `[04 R-COLL-01 §2]`, `[04 R-COLL-01 §3]` |
| The slope tiers, their comparison strictness, and the bounded aggregate census | `[04 R-SLOPE-01 §2]`, `[04 R-SLOPE-01 §3]`, `[04 R-SLOPE-01 §4]` |
| Grid and passability: neighbour order, the wide first fan, diagonal checks | `[04 §7.1]`, `[04 R-PATH-01 §1]` |
| The coarse word the search tests is the mapping grid | `[04 R-PATH-01 §2]`, `[04 R-PATH-01 §14]` |
| A\* state and costs, the turn table, the weighted pipeline, the early exits | `[04 §7.2]`, `[04 R-PATH-01 §3]`, `[04 R-PATH-01 §4]` |
| The pre-search ray, the wall follow, and the write-once tolerance | `[04 R-PATH-01 §5]`, `[04 R-PATH-01 §15]` |
| The scheduler, exactly: the quantum is the heuristic weight, work is a separate accumulator | `[04 §7.3]`, `[04 R-PATH-01 §6]`, `[04 R-PATH-01 §10]` |
| Reconstruction, world conversion, and the publication contract | `[04 R-PATH-01 §7]`, `[04 §7.5]` |
| The follower's protocol and the route-acceptance rule | `[04 R-PATH-01 §8]`, `[04 R-MOV-03 §1]` |
| The goal classes, exactly, and the air surfaces that never reach the search | `[04 R-PATH-01 §9]`, `[04 §7.4]` |
| The rectangle goal's growth by the mover's footprint; the build approach | `[04 R-PATH-01 §12]`, `[04 R-PATH-01 §13]`, `[04 R-MOV-03 §9]` |
| The search draws no random numbers | `[04 R-PATH-01 §11]` [I4] |
| The ground mover, desired heading, the turn clamp, accelerate versus brake | `[04 §8.1]`, `[04 R-MOV-01 §1]`, `[04 R-MOV-01 §2]`, `[04 R-MOV-01 §3]` |
| The post-move Y, pitch and roll correction and the movement-rate tiers | `[04 R-MOV-01 §4]`, `[04 §5.2]` |
| Hover: the four-corner conform, the bob's inputs, its phase word, its counter | `[04 R-MOV-01 §5]`, `[04 R-MOV-01 §5a]`, `[04 R-MOV-01 §5b]`, `[04 R-MOV-01 §5c]` |
| Mover modes, the band classifier, floaters and upright height | `[04 §9]`, `[04 §9.1]`, `[04 R-MOV-01 §6]`, `[04 R-MOV-01 §8]`, `[04 R-MOV-01 §8a]` |
| The blocked mover's repath request and its throttle | `[04 R-MOV-01 §7]`, `[04 R-PATH-01 §14]` |
| The dynamic-blocker boundary: search versus commit | `[04 R-MOV-02A]`, `[04 §8.2]` |
| The goal-payload method table and the three goal-point queries | `[04 R-MOV-03 §2]`, `[04 R-MOV-03 §3]` |
| The movement goal handle and its satisfied word | `[04 §8.3]`, `[04 R-P0-01]` |
| Goal install and release, the five movement pending bits, the release callbacks | `[04 R-ORD-01 §0]`, `[04 R-ORD-01 §1]`, `[04 R-ORD-01 §9]`, `[04 R-ORDER-02 §1]` |
| Which order installs which goal, with which radii | `[04 §3.5]`, `[04 R-ORD-01 §3]`, `[04 R-ORD-01 §5]`, `[04 R-ORD-01 §8]`, `[04 R-ORD-01 §12]`, `[04 R-ORD-02 §2]`, `[06 R-WPN-05 §1]` |
| The commit step in order, the two occupancy planes, the stamp and clear | `[04 R-COLL-01 §1]`, `[04 R-COLL-01 §4]`, `[03 §2.2]` |
| The sector index, the overlap scan span, and the bucket's read order | `[04 R-COLL-01 §4A]`, `[04 R-COLL-01 §10]`, `[04 R-COLL-01 §11]` |
| The blocked flag, the "I can't get there" bit, and the whole-image negative on yield | `[04 R-COLL-01 §5]`, `[04 R-COLL-01 §7]`, `[04 R-COLL-01 §8]` |
| The flight command block and its single per-tick supply | `[04 R-AIR-01 §1]`, `[04 §10.1]` |
| Bank, pitch and the lean accumulator | `[04 R-AIR-01 §2]` |
| The mover-mode setter and the takeoff preamble | `[04 R-AIR-01 §3]`, `[04 R-AIR-01 §6]`, `[04 R-AIR-02]` |
| The air path marker: its flags, its constructors, its goal update | `[04 R-AIR-01 §4]` |
| The air sector grid and the off-map sentinel's vertical bypass | `[04 R-AIR-01 §5]`, `[04 R-AIR-01 §6a]` |
| Landing, standby, seek, and the pad query | `[04 R-AIR-01 §6]`, `[04 R-AIR-01 §7]`, `[04 §10.2]` |
| Attack runs, the release lead, and the maneuver leash | `[04 R-AIR-01 §8]`, `[04 R-AIR-01 §13]`, `[04 R-AIR-01 §17]` |
| The transport executor pairs and what §10.2 left open | `[04 R-AIR-01 §9]`, `[04 R-AIR-01 §10]`, `[04 R-UNIT-06 §3]` |
| The air-base registry's third list and its cadence | `[04 R-AIR-01 §11]`, `[06 §3.1]` |
| The attack-run leg tables and their arrival windows | `[04 R-AIR-01 §14.1]`, `[04 R-AIR-01 §14.2]`, `[04 R-AIR-01 §14.3]` |
| Cruise altitude, flight levels, the arrival radii | `[04 §10.1]`, `[04 §10.3]` |
| Producer and product collision; the yard map is the exemption | `[04 R-FAC-02 §5]`, `[04 R-FAC-02 §6]` |
| The product is cargo: attach, carry, detach | `[04 R-FAC-02 §1]`, `[04 R-FAC-02 §2]`, `[04 R-FAC-02 §3]`, `[04 R-FAC-02 §4]` |
| The exit-piece locator: the shared piece transform and its coordinate negation | `[04 R-REV-02]`, `[03 R-RAST-01 §2]` |
| The no-rally column is a route-publication defect | `[05 R-EGRESS-02]`, `[04 R-PATH-01 §8]` |
| The movement class record's eight keys, widths and defaults | `[02 §5]`, `[02 "Movement class record"]` |
| The plot cell's occupancy words, derived height pair and sea level | `[03 §2.2]`, `[03 §2.3]`, `[fmt tnt]` |
| The visibility mapping word the layer tests | `[03 R-LAYER §1]`, `[03 R-P0-18-A §1]` |
| The assist approach term and the shared repair helper | `[05 R-WORK-01 §2]`, `[05 R-WORK-01 §3]` |
| Feature origin cells for the reclaim and resurrect rectangles | `[05 R-ECO-02 §2]`, `[05 R-FEAT-01 §3]` |
| Projectile contact against the occupancy words | `[06 R-DMG-01 §7]` |
| The mover save image and what is derived state | `[08 R-SAVE-02 §8]` |
| The tick phases the two sweeps sit in | `[01 §4.4]`, `[01 R-CORE-01 §4.4.1]` |
| Pool slot reuse and handle identity | `[01 §6.1]`, `[01 §6.2]` |

## 7. Not implemented and open

The code retains the following `TODO(question)` markers. Each names the
current behavior and the evidence needed before changing it:

* `internal/path/search.go`: opening a cell replaces its status byte and drops
  the ray-visited bit. Trace the open write and the pop-time blocked re-test to
  decide whether that bit must survive `[04 R-PATH-01 §1]`.
* `internal/movement/retail_restore.go`: the code-3 air-velocity marker retains
  its auxiliary and padding words without assigning new runtime meanings.
  Trace their constructor, save and execution readers before interpreting them
  `[08 R-SAVE-02 §8]` [04 "Missing and unknown"].
* `internal/movement/integrate.go`: callback classification reads the collision
  record's turn residual, which currently has no live steering update. Trace
  the ground and flight writers through the callback boundary before choosing
  the correct maintained residual; the current zero/restored value remains an
  implementation gap `[04 §5.2]` `[04 R-MOV-01 §6]`.

The contracts also carry these questions, each with its settling observation.

* Which order types can produce an out-of-bounds goal. The search's own
  behaviour is established — an out-of-bounds start is a `0x200` reject and an
  out-of-bounds enumerated cell is unmarked but still aims the ray — but the
  per-order census is open; a static trace per order type settles it
  `[04 R-PATH-01 §4]`.
* The per-order census of which goal family is constructed with which radii.
  The families and their arithmetic are established; the wiring table in §3.5 is
  built from the individual order sections rather than from one census, and a
  static trace of every goal-family call site's order layer would close it
  `[04 §7.4]` `[04 R-PATH-01 §9]`.
* The movement wrapper's per-tick release-callback states — the "wrapper
  identities" question `[04 R-ORDER-02 §1]`.
* The emergent head-on deadlock timing. That two movers ordered through each
  other in a one-cell corridor take their first divergent route between thirty
  and sixty ticks after the block is a **Supported inference** from the two
  timers, not a trace; a manual retail observation of that corridor settles it
  `[04 R-COLL-01 §7]`.
* Which of `{0, −1}` a hovering unit's height offset takes on a tick where
  retail's wall clock and the tick disagree — the bounded residue of the
  deliberate divergence in §5 `[04 R-MOV-01 §5b]`.
* Whether soft terrain carries a forward-speed factor. The search's steep-tier
  cost is established at 30; a movement-rate multiplier is not traced, and the
  classifier preserves the tier without one. One Go comment still cites this as
  the orphan `[R-P1-11]`, which [ARCHITECTURE.md](ARCHITECTURE.md) §7 routes to
  "cite the owning section directly": that section is `[04 §6.1 R-DOC04-B]`,
  with the cost in `[04 R-PATH-01 §3]`.
* What retail does when the search's working set fills. No exhaustion policy is
  traced `[04 §11]`.
* Whether the bucket order the overlap scan walks is stable for a unit the
  binding cannot file. Every stamp site relinks through the filing, and the
  filing lives on the collision record, so the only residue is a unit with no
  filing at all, which falls back on live-unit order `[04 R-COLL-01 §11]`.
* Whether any retail map authors `gravity = 0`. The census of 275 stock maps
  found none, so the cancel-all bound is unreachable on shipped content; a probe
  under `probes/` would confirm the cancellation visibly on a map that did
  (SC23) `[04 R-AIR-01 §8]`.

## Full-layer rebuild storage

A blocking-feature revision still refreshes a stale class synchronously before
its path request proceeds. Each full rebuild classifies each source cell once
into a reusable byte array, then computes the footprint/ring minimum through
row and column windows
[04 R-SLOPE-01 §3]. Each window counts blocked and non-clear cells, updating
those counts as one cell enters and another leaves. Classification and both
passes complete before occupancy, watermark or terrain inputs can change.
Rectangle restamps retain their distinct edge rule.
Storage is two bytes per map cell for each instantiated class (source tiers
and row results), reused across rebuilds; it is derived runtime storage and is
not serialized.

In the seeded 1080p Ashap Plateau experiment, blocking corpses at ticks 178 and
202 forced two full class rebuilds. Classifying once reduced their combined
cost from 21–22 ms to 6.4–6.8 ms, and those simulation steps from 23–24 ms to
8–9 ms. These are measured workload results, not scheduling guarantees. Future
window-minimum optimization must preserve the full classifier output and
same-tick visibility. Local dirty rectangles cannot simply replace the full
refresh without accounting for its occupancy-age reads.

The scene-version-3 benchmark adds factory production and non-overlapping
building yards. On the same Mac, row/column windows reduced its two slow
simulation steps (ticks 179 and 202) from about 8 ms to about 5 ms. CPU captures
were unchanged; repeated GPU controls established the same small nanoframe
pixel variation observed in the optimized run. These observations do not
establish stutter-free gameplay or GPU completion timing.
