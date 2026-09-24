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
immediate replan inside the collision commit. A blocked mover halves its speed,
clamps against its old footprint boundary and proposes the same move next tick; what
looks like sidestepping emerges from two independent timers — the follower's
60-tick repath and the class layer's 30-tick occupant age — and from nothing
else `[04 R-COLL-01 §7]` `[04 R-MOV-01 §7]`. Unapproved additions in this area invent retail behaviour. The explicit
Modern construction-clearance contract below,
[Modern learned terrain](#modern-learned-terrain),
[Modern re-route staggering](#modern-re-route-staggering),
[Modern group-order spreading](#modern-group-order-spreading) and
[Modern unreachable moves](#modern-unreachable-moves) are
user-authorized exceptions and must not be removed as parity defects.

### Modern construction clearance priority

**Nanolathe Modern policy.** Construction may supply `StageModernClearance`
with a new idle blocker's `Move_Ground` record and a locally proven cardinal
path. Only `construction.ModernRules` produces these requests, through the
central `construction.Rules` seam; `construction.StrictRules` produces none.
Eligibility, proof and work limits belong to
[Modern construction-site yielding](DESIGN_ECONOMY_CONSTRUCTION.md#modern-construction-site-yielding).

Staging writes no position, occupancy, speed, RNG or scheduler state. The hint
is keyed by unit slot and exact order record. The next normal `ActivateMove`
installs the ordinary goal/arrival ownership, verifies the hint's start and end
against the live request and verifies cardinal adjacency, then publishes the
complete local path through existing route storage instead of submitting A*.
World waypoints use each committed cell plus the mover's half-footprint bias
[04 §7.1]; point zero uses the actual current position. There is no smoothing.
At most 20 points are accepted. The hint is consumed once, so later goal changes
and collision-driven repaths keep the normal scheduler. A mismatched hint is
discarded and ordinary activation proceeds. Explicit order cleanup and unit
forgetting also discard pending hints, preventing slot reuse from inheriting one.

This gives the construction request routing priority without interrupting an
active global search or changing its budgets. The ordinary mover still owns
turning, acceleration, collision, arrival and completion. No additional movement
or order visit is run outside the authoritative phase sequence.

The hint is transient and is not a retail save field. A route already installed
uses ordinary route persistence; if saved before activation, the retained
`Move_Ground` restores through ordinary pathfinding. Changing to Strict does not
cancel an already issued move. These boundaries do not promise physical
clearance when a route becomes obstructed or a unit cannot move fast enough.

### Package boundaries

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
  pump's result codes. `System.recordGoals` retains each record's object in a
  per-unit slice independently of the controller binding. Arrival and rebind
  drop only the binding; explicit record release unbinds the controller before
  destroying its own object, while record destruction unbinds only if its
  object is currently bound. `ForgetUnit` destroys all retained objects in
  insertion order. Mover-only restore preserves live record ownership; full
  session restore builds a new system, reconstructs all saved record objects,
  and binds only the primary head. Save projection reads these retained objects
  through `RetailOrderPayload`, including displaced objects; a steering-only
  binding does not manufacture a saved goal. Final target removal clears weak
  links on every retained air marker without adding an order event
  `[04 R-ORD-01 §9]` `[04 R-AIR-01 §4]` `[08 R-SAVE-02 §8, §10, §11]`.
  The queue selects the steering target; it never gates the
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
heuristic. Three ground families exist — `PointGoal`, `AnnulusGoal` and
`RectPerimeterGoal`. The two serialized air surfaces of `[04 R-PATH-01 §9]`
carry a zero heuristic, enumerate nothing and never report the start satisfied,
so a serialized air surface cannot satisfy a ground search; no ground order
produces one, and this package constructs neither rather than shipping an inert
family object nothing can reach. `DescribeGoal` answers which family a goal is
and with which parameters, and the scheduler's diagnostics already call it, so
it is also how a test reads a family.
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
belong to the publisher, not to the heap `[04 R-PATH-01 §1]`. The store keeps no per-cell table of its own: it binds the
session's, which already carries status, direction and node identity.

**The resumable search** (`search.go`). `SearchConfig` carries the start, the
goal, the heuristic scale, the mover's authored footprint pair for the
cell-to-world conversion, optional bounds, the passability accessor, the
request-initialization revision hook and the start direction. `Session` is one
request's search across budget slices: the touched-entry table with its status
and direction bytes — a dense generation-stamped table indexed by cell when the
scheduler has one to lend, and a `map[Cell]entry` otherwise, answering
identically either way, and resolved once per neighbour so the expansion
reads and writes a cell through one slot — the goal-cell set, the write-once arrival tolerance, the
notified status, the node store and the heap. The neighbour fan is a value —
nine entries on the first expansion, a directed five-entry fan afterwards —
so expanding a node allocates nothing `[04 §7.1]`. `walkRay` is the pre-search
greedy forward walk with its alternating two-sided wall follow; its only product
is the acceptance threshold and the connect flag `[04 R-PATH-01 §5]`
`[04 R-PATH-01 §15]`.

**The search kernel** (`kernel.go`). `Kernel` opens one request's search and
`Search` is that resumable object behind its interface, so this is the only
place a request chooses a search implementation. The movement system holds the
bound kernel on `System.Kernel` and asks it once per admitted request — never
once per budget slice and never once per expanded node. **The kernel is
selected by the session's gameplay rule set** (`RuleSet.Path`, see
[DESIGN_GAMEPLAY_RULES](DESIGN_GAMEPLAY_RULES.md#the-path-search-kernel)),
which binds it at composition and at the phase-1 command boundary; both
reserved sets bind `RetailKernel`, the search described above, and a system
with no kernel bound searches the same way. The scheduler keeps admission
order, the per-player step allowance `[04 R-PATH-01 §10]` and the full-or-empty
publication boundary `[04 §7.3]`, so a replacement kernel may change how a
route is found and never when one publishes.

**The scheduler** (`queue.go`). `Scheduler` holds one active request at a time,
and it also owns the search's per-cell `Workspace`. That ownership is the point:
a generation-stamped table is shared storage, and the scheduler is what decides
which search is running, so it lends the table to the search it has admitted and
refuses it to every other. A session that is refused one keeps its own map and
produces the same visit order and the same route; every path that drops a
session hands the table back, and one that did not would cost the next search
the table and nothing else.
`CandidateProvider` is the admission surface the movement system implements:
player count, unit limit, per-player eligibility and a stable per-player cursor
poll. The provider advances that cursor through the bound player's fixed unit
slice one physical slot per poll; holes and units without a route follower
still consume visits. Staged request data is lookup-only: the selected
follower's wants-repath flag, inclusive 60-tick throttle, committed start and
goal are read at a positive poll, which stamps the timestamp. Path derives
neither cursor nor eligibility from staged requests `[04 R-PATH-01 §6]`
`[04 R-MOV-01 §7]`. `SearchFunc` is the search boundary and reports the work it
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
rewrites the same packing. Storage: a restamp runs the chain once per cell of
the rectangle its anchors read and classifies every anchor from those kept
tiers, since the chain is a pure read that no restamp writes; the per-anchor
form survives only as the test reference. The mirrored occupant-age clocks are
a dense row indexed by handle whose entries keep the old map's presence answer.
A feature change reaches the layers where it
happens: `internal/features` writes the plot cells and calls the terrain's
`NoteFootprintRestamp`, which `internal/movement` binds to
`System.NoteFeatureFootprint` at map load, and every named class is restamped
over the changed rectangle synchronously, in the calling phase
`[03 §5.1.2]` `[03 R-LAYER §2]` `[04 R-MOV-03 §3]`. That port is the seam
because `internal/features` cannot import `internal/movement`; the terrain's
static-obstacle revision word is Nanolathe diagnostic metadata. A mismatch
never invalidates a published route or interrupts a mover; the follower's
blocked/count gates and the scheduler's poll own repathing `[04 R-MOV-01 §3]`
`[04 R-MOV-01 §7]`. `Revise` is the request-initialization pass: it
advances the record's revision watermark to `max(tick, 30) − 30`, re-stamps the
footprints of recently committed occupants and refreshes the requester's own
commit tick. Search consumption returns 0 out of bounds, 2 when the requesting
player's slot bit is absent from the visibility mapping word — the block is
unexplored — and otherwise the packed terrain tier; 1, 2 and 3 all expand and
only 0 hard-blocks, which is why retail units path optimistically straight
through fog `[04 §6.1 R-DOC04-B]` `[04 R-PATH-01 §2]`.

**The policy seam** (`rules.go`, `learned.go`). `Rules` is this package's
gameplay seam, bound on `System.Rules` by the session's rule set; unbound it
answers as Strict 3.1. It carries
[Community contested-cell claims](#community-contested-cell-claims),
[Modern learned terrain](#modern-learned-terrain),
[Modern re-route staggering](#modern-re-route-staggering),
[Modern group-order spreading](#modern-group-order-spreading),
[Modern bounded path work](#modern-bounded-path-work) and
[Modern unreachable moves](#modern-unreachable-moves), and `LearnedTerrain`
is the per-owner grid the learned-terrain policy keeps on the `System`.

**Routes** (`route.go`). `Route` is up to twenty published points plus the
active and dirty bits. `Publish` clamps the count first and applies the zero-
publication rule; `Prune` is the index-1 proximity test; `At` and `Export` are
the two read shapes, only one of which is a predicate. There is no route save
codec: the retail mover image omits route, follower and proposal state
`[08 R-SAVE-02 §8]`, so C16's two-bit count and signed pairs have no writer and
no reader in this build. Publication adopts the search's points
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
grid's second index, and it is a real index rather than a filter: the grid
keeps one record per eight-cell sector holding the head of a doubly linked list
of the units filed there, a stamp head-inserts into its record, and the clear's
scan visits only the records in the rectangle's own sectors grown by one on
every side, column-major, each read from its head — which is reverse order of
each unit's most recent relink `[04 R-COLL-01 §4A]` `[04 R-COLL-01 §11]`. The
back link is Nanolathe's; retail walks from the head to find a predecessor.
Membership is maintained at the three points the filing is written — the
stamp's relink, the cargo detach mirror and unit finalisation — so the buckets
and the filings agree by construction. Retail links a unit at creation and so
has no unfiled population; here a collision record exists before its first
stamp, and the modes that write no cell never take one, so the overlap binding
names those separately and the sweep places them after each record's bucket. `PlaceUnit` is the direct
position commit — retail's carried-position setter, the commit's success branch
without the validator — used by the teleport row and by carried motion
`[04 R-COLL-01 §4]` `[04 R-SPEC-01 §2]`. `ForgetUnit` is the only lifecycle
cleanup: pool slots are reused by handle, so a new unit landing on a dead one's
slot must not inherit its footprint, profile, tier or occupancy.

**The composition root** (`integrate.go`). `System` holds the terrain, the
compiled class table, the occupancy grid, the scheduler, the per-handle route,
steer, flight and collision maps, the per-handle resolved profile, the class
layer registry, the air sector grid and the air-base registry. `BeginTick`
starts the transaction and rebuilds the throttled air-base registry;
`StepUnit` runs one unit's whole mover tick; `EndTick` records post-sweep
diagnostics. `StepUnit` refuses to run outside that transaction. The per-unit
body first reads the unit's live attachment: a carried unit commits its carried
pose, velocity and occupancy at its own slot and exits. Thus a cargo before
its carrier sees the carrier's prior committed pose, while a later cargo sees
the carrier's same-tick pose; an attachment or release before a cargo's visit
is effective immediately `[04 R-MOV-03 §1]` `[04 R-COLL-01 §1]`
`[04 R-FAC-02 §2]`. An uncarried unit then takes either the air path or the
ground path; on the
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
in which payload they install `[04 R-AIR-01 §1]`. The flight command retains
the installed payload's owning record independently of the current queue head
and executor state; arrival, displacement and cleanup use that identity
`[04 R-ORD-01 §9]`. `airMarker` is that payload:
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
`VTOL_MobileBuild`, `VTOL_HelpBuild` and `VTOL_Landing`, each building markers and reading the phase
tables of `[04 R-AIR-01 §6]` and `[04 R-AIR-01 §7]`; `QueryLandingPad` is the
synchronous four-output pad query with its free-pad predicate `[04 §10.2]`.
All air executors retain their phase on the order record. Move and standby
run through the ordinary pump; standby also keeps its integer post there.
Construction calls `VisitAirBuildApproach` from its work window and applies the
returned pump result to the same record. Both air build bodies call
`VisitAirBuildWork` before their work quantum, including a finishing visit.
Movement holds no parallel order phase, so restoring a save or exposing a
suspended record preserves its marker and progress `[08 R-SAVE-ORDER-01]`
`[04 R-ORD-02 §2]` `[04 §10.3]`.
`VTOL_Landing` runs directly through the order pump: its record carries the
phase, reused bearing/pad word, and the descent's 15-tick deadline. Target
removal reaches the entry guard even during descent; arrival and deadline
deliver distinct phase-5 arms. Phase 6 either attaches the empty aircraft and
pushes its repair record in that same pump visit, or transfers its cargo onto
the pad `[04 R-AIR-01 §6]`. The shared attachment commit unlinks a previous
carrier before relinking; only the COB adapter rejects another carrier's cargo
`[04 R-COB-03 §5]`.
`VTOL_LandIfCan` also runs directly through the pump. Its phase, search bearing
and low-bit scratch belong to the order record, so a temporary `Paralyze` head
preserves the waiting descent. The descent wakes `EndTransport` before installing
its marker and lowering activation; the search rotates its fallback bearing on
any delivered movement outcome `[04 R-AIR-01 §6]` `[04 §2.4]`. Point-marker
arrival delivers arrival and release together through the pump. A successor
ends terrain landing at the next admitted handler visit before touchdown or
recovery, so queued work does not inherit an unnecessary grounded-mode write. The
ordinary-producer census closes standalone failure/release reachability as a
bounded negative: ground route failure has no flight producer, and recovered
preemption paths either preserve the landing binding or remove the landing
record before another movement record installs. The handler's Established
non-arrival branches remain implemented `[04 R-AIR-01 §6]`.
Air attack preparation uses the inhibit-all slot verb, not target binding.
The strafing and hover release phases and the bomber release phase bind only
slot zero; dogfight phase one inhibits all, then releases and binds slot zero.
The bomber break phase clears its primary target without changing control or
Aim state. These verbs pass through the existing combat adapter
`[04 R-AIR-01 §8]` `[06 §3.2]`.

`VTOL_SeekAttack` calls orders' `AutonomousEngage` for a retained target and
`AutonomousAcquire` for its search phase. Both issue resolved attack records;
the targeted restart requires the new queue head to exist before it returns.
The weapon adapter's target setter does not perform this queue handoff
`[04 R-AIR-01 §7]` `[04 R-STANCE-01 §3]`.
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

The **move-rate cache is not derived state**, and `RestoreMover` seeds it from
the restored unit record. The emitter keeps the classifier's last category in a
per-handle row of its own, while the save carries that category inside the
packed unit word `[08 R-SAVE-02 §6]`; with nothing seeding the row, the first
mover tick after a load would read a zero cache, see a category change that did
not happen, and wake `StartMoving` plus `MoveRateN` on every restored mover,
although `[04 §5.2]` emits only on a change. The `setSFXoccupy` band cache
beside it is deliberately *not* seeded: whether retail persists that band is
Unknown — `[04 §9.1]` does not say where the cached band lives, and neither
`[08 R-SAVE-02 §6]` nor `[08 R-SAVE-02 §8]` names a band word — so the code
carries a `TODO(question)` there rather than a guess.

**Diagnostics** (`p28_parity_trace.go`, `airdiag_export.go`). `MovementTrace`
and `CollisionTrace` are value copies taken at a capture boundary — the next
route comes from the scheduler's own request trace, never from a guessed route.
`AirExecutorState` exposes an air executor's phase and latches read-only so a
harness can observe them without this package exporting mutable state. Nothing
in the simulation reads either of them.

A third diagnostic stood here until the reachability sweep of 2026-09-16: a
per-cell movement audit that collected the classifier's verdict, the resolved
feature, the static layer value and the two revisions, and classified the
result into a fault domain. No sink was ever attached — not the headless
runner, not a benchmark, not `internal/airdiag` — so the collector, its finding
classifier and the route-failure walk were reached by nothing but their own
tests, and they have been removed rather than left looking wired. What the
audit would have observed is still observable: the class layer's value, the
feature resolution and the revision words are all read directly.

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

The follower's last-request tick records an admitted scheduler poll. Goal
installation preserves that tick when at most ten ticks old and clears it when
older; activation and request staging never replace it with the current tick
`[04 R-PATH-01 §8]`. The scheduler alone applies the inclusive
`lastRequestTick + 60 <= tick` admission test and stamps a positive poll
`[04 R-MOV-01 §7]`; the 60 is `Rules.RepathDelay`, which Modern lengthens by
0–7 ticks ([Modern re-route staggering](#modern-re-route-staggering)). This distinction matters for `RepairUnit`, which refreshes
its rectangle goal every 30–59 ticks: stamping each refresh as a request would
continually postpone admission and leave its synthetic route through an
obstacle. Replacement invalidates the old route binding and failure state while
preserving the poll timestamp for the goal installer's age test.

Movement diagnostic snapshots expose the follower's wants-repath flag and
last-request tick, the ground goal and its owning record, the active-order
binding, and explicit record-identity matches. Staged provider requests are
separate from admitted scheduler requests. Both current request states are
available without enabling historical tracing; past search results and
collision histories still require that opt-in. Reading a snapshot neither
advances a request nor changes the route or its binding.

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
X/Z pairs `[04 §7.3]`. Nothing implements it: the retail save boundary carries
no route `[08 R-SAVE-02 §8]`, and the codec that used to stand in `route.go`
round-tripped only with itself, so it was removed rather than left as a mirror
no save path consults.

**C17 — the export helper is not a predicate.** It repeats the last stored point
for excess indices and reads adjacent fields at a zero count. Callers gate on
the active bit themselves `[04 §7.3]`.

**C18 — first-open layer admission.** Untouched neighbors probe the current
class layer. Opening preserves the ray and terminal flags; already-open nodes
relax with their stored terrain term. Popping tests the terminal flag before
writing the closed state, and never probes passability again. Layer changes
do not remove existing heap entries `[04 R-PATH-01 §1]` `[04 §7.4]`.

**C19 — there is no smoothing pass.** The earlier contract described collinear
removal and a two-directional shortcut ray. It is withdrawn: reconstruction
publishes the direction-change points of C13 converted to world coordinates and
nothing else. Do not implement a shortcut or a collinear-removal step
`[04 §7.5]` `[04 R-PATH-01 §7]`.

### 3.3 Ground steering and collision — C20…C25

**No-waypoint service.** Exhaustion and arrival still run the speed update
with `-BrakeRate`, velocity integration, collision commit and post-movement
correction. Proximity to the recorded goal never skips that work
`[04 R-MOV-01 §3]` `[04 R-MOV-01 §4]` `[04 R-MOV-01 §5]`.

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

#### Community contested-cell claims

**Community 3.9 and Nanolathe Modern policy.** When the resolved Community
feature table enables `GridClaimTieBreak`, the unit with the lower unsigned
sixteen-bit unit index wins a contested occupancy cell. The comparison is
strict: an identity re-claiming its own cell keeps it. This is the
[CP-DMG-2 outcome-changing rule](../research/extensions/community-patch-engine.md).

**Strict 3.1 behavior.** The incumbent yields only when its owner's player-row
control byte is 3; otherwise the first claimant keeps the cell
`[04 R-COLL-01 §4]`. A Community content profile that disables the feature also
uses this answer.

The same `Rules.ClaimConflict` answer is asked by `OccupancyGrid.ArbitrateOverlap`
for the ground plane, air plane and building/yard-map ground stamp, both in the
ordinary stamp and in the re-claim after a host vacates. Enabling the Community
rule removes the owner-state read entirely, so a lower-index claimant may
displace an inactive owner's lingering unit
`[community-patch-engine.md CP-DMG-2]`. The grid still owns all existing
host/intruder flag writes and the subsequent clear-and-restamp protocol; the
rule changes only which side takes the cell. No path decision, RNG draw,
resource effect or allocation is added. The grid binds a stable `System` method
that reads the current `Rules` value, so a command-boundary rule rebind changes
later claims without retaining a stale policy.

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

**Explicit air attacks at a position (Established retail behavior).** Ground
and feature attack commands resolve to `AirStrike` or `AirToGround` with a
cached goal and no unit observer [04 R-ORD-02 §1][04 R-MOV-03 §7]. The entry
must preserve that goal and continue into the flight phases. Only a record
originally issued against a unit takes the missing-target completion path
[04 R-AIR-01 §8][04 R-AIR-01 §16]. This applies to Modern and Strict 3.1.
`air_point_attack_test.go` in orders checks admission; its movement counterpart
runs ground and feature orders through bomb release in both modes.

Replacing an airborne attack starts a fresh record. The shared takeoff
preamble installs no climb marker or climb-arrival wait when already airborne
[04 R-AIR-01 §6]. Replacement purges also remove any seek appended by the
cancelled run, so it cannot block the new player command [04 R-MOV-03 §6]
[04 R-AIR-01 §16]. Weapon reload persists; the new point is armed by the new
run's ordinary release phase. The session regression
`TestRetailBomberRetargetAfterRelease` issues a second attack after a stock
Thunder releases a bomb and verifies another release at the new point in
both modes. The ordinary distance and reload gates still decide how soon
that release occurs [04 R-AIR-01 §8][06 §4.1].

### 3.4.1 Modern bomber pass completion

**Nanolathe Modern policy.** An accepted bombing pass may finish before its
maneuver leash makes it return to post. The central `gameplay.Mode` selects the
order package's rule set, `orders.Rules`, carried on the session's shared order
binding; the air entry asks it `DeferBomberLeash` before the leash comparison,
and `orders.StrictRules` answers no, which is Strict 3.1.

**Strict baseline (Established).** Autonomous Maneuver inserts a leashed
attack followed by a return move [04 R-STANCE-01 §4]. Air attacks test that
leash before their phase body [04 R-AIR-01 §8]. A close-target bomber setup
uses a 2240-world-unit displacement and a strict 960-unit arrival radius
[04 R-AIR-01 §8][04 R-AIR-01 §4]. The 1280-unit leash observed on the captured Thunder can
therefore end the attack when setup arrives, before bomb release. This
interaction reproduces in Nanolathe; exact repeated-loop timing in retail
remains unverified. The individual retail rules remain unchanged in research.

**Modern behavior.** Only `AirStrike` defers the leash in phases 1 through 5:
setup, approach and release. Phase 6 is admitted after the overflight marker
completes, and applies the ordinary inclusive leash comparison before starting
another pass. If outside, normal order completion clears the weapon targets,
releases the movement marker and exposes the saved return move. If inside,
the existing break-off and next-pass/repair behavior continues. Phase 0 keeps
its existing admission check. No new latch, target blacklist, trajectory
prediction or serialized state is introduced.

**Boundaries.** Target removal/cloaking, explicit cancellation and replacement,
readiness failure and zero-gravity cancellation retain their existing paths.
Hold Fire, resource costs, reload, aiming and projectile admission retain their
own gates: reaching the release phase guarantees neither a launch nor a hit.
Unleashed attacks, other air executors and ground combat retain their behavior.
Changing the central mode affects the next handler visit, including an existing
pass. The policy consumes no random draws or resources itself; completing a
previously aborted pass naturally reaches the existing approach-jitter and
weapon draw/cost sites.

**Verification.** Focused order tests lock the phase boundary, inclusive leash,
cancellation and executor scope, with unchanged RNG/resource state at the
policy decision. A movement regression runs a close-target autonomous bombing
attack through real order and flight updates: Strict returns before release;
Modern reaches release, finishes overflight and returns when beyond the leash.
Session tests cover command-boundary mode changes and existing/new queues.
Run `tools/check`, `tools/check-retail` and the live battle performance checks
from [BATTLE_BENCHMARK.md](BATTLE_BENCHMARK.md).

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
| `Move_Ground` | point at the order's goal | first general parameter plus 4; ordinary mouse move uses 4 `[04 R-ORD-01 §4]` |
| Patrol legs and everything not listed below | point, radius 0 | none `[04 §7.2]` |
| `Attack_Chase` | whatever its own maneuver phase installed — a point goal for five of the six live substates, an annulus for the other two | every radius sized from the slot's weapon range: `d`, `d/2`, `trunc(d/4)`, `0`, annulus `(d, d/2)`, annulus `(2d, d)` `[04 R-ORD-01 §3]` `[06 R-WPN-05 §1]` |
| `Follow_Ground` (the ground guard's follow) | point at the ward's position plus the record's stored anchor offset | arrival radius is half the handler's `(FootPrintX(me) + FootPrintX(ward) + 2) · 16` `[04 R-ORD-01 §8]` |
| `HelpBuild` phase 0 | annulus at the target | outer `builddistance + half`, inner `half`, where `half` is the assist approach term over the **target's** footprint and `builddistance` is the builder's own `[04 R-ORD-01 §12]` `[05 R-WORK-01 §2]` |
| `RepairUnit` phase 1, `Capture` phase 0 | rectangle perimeter from the target's committed anchor cell and footprint | grown by the mover's own footprint `[04 R-PATH-01 §12]` |
| `Reclaim` phase 0, `Resurrect` phase 0 | rectangle perimeter from the **feature's** origin cell and size | the anchor already is the origin: a feature's stamp writes its index on the anchor and the fringe sentinel across the rest `[04 R-ORD-01 §5]` `[05 R-ECO-02 §2]` `[05 R-FEAT-01 §3]` |
| `MobileBuild` approach | rectangle perimeter at the product's anchor cell and footprint | no candidate generator, no range filter and no sort: the candidate set is the search's own border enumeration and the selection is the border cell it closes first `[04 R-PATH-01 §12]` `[04 R-PATH-01 §13]` `[04 R-MOV-03 §9]` |
| `VTOL_MobileBuild` approach (the air executor's phase 1) | air point marker at the goal snapped onto the product's footprint | horizontal arrival radius `builddistance`, strict; construction dispatches the movement leg from record phase 1, which installs the marker and gate `0xE0`; phase 2 consumes its outcome before placement `[04 R-ORD-02 §2]` `[04 R-AIR-01 §6]` |
| `Park` (a no-rally product's terminal record) | rectangle perimeter on the rectangle the handler installed, centred on the product's own committed cell | `[04 R-FAC-02 §4]` `[04 §7.2]` |

The ground move's radius is quantized by the point goal's arrival predicate;
a radius of 4 still requires the exact destination cell. Stopping at the best
reachable point does not complete the last Move: it can retry indefinitely
[04 R-ORDER-02 §1]. Ordinary group commands can supply different per-actor
goals upstream [04 R-STANCE-01 §5]; the broadcast boundary is described
in DESIGN_INTERFACE_HUD_INPUT, "Ordinary group-click destinations".

`internal/orders/pump.go` reads the complete signed 32-bit first parameter
before adding 4, retaining the 32-bit sum [04 R-ORD-01 §4]. Its point-goal
installation tests distinguish a large positive parameter from a negative
parameter with the same low word and preserve addition wraparound. Ordinary
mouse moves (zero) and the AI gather value (160) use the same path.

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
  `[04 R-PATH-01 §2]`. Both modes keep this.
* **Under Strict 3.1, a unit that fog-paths into a wall parks there for the life
  of its order.** A rejection changes no search input, so the sixty-tick repath
  is provably the route it replaced; and because the search reads the mapping
  grid in the unsheared ground frame while the LOS publisher writes it in the
  height-sheared one, ground at the foot of a south-rising face stays unexplored
  to the search while the player can see it `[04 R-MOV-01 §7]`
  `[04 R-COLL-01 §3]` `[04 R-PATH-01 §2]`. That is retail and Strict reproduces
  it. Modern removes only the permanent trap:
  [Modern learned terrain](#modern-learned-terrain).
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
  leg reads `Terrain.OTAGravity`, the unconverted mission-header integer, before
  dividing and returns the cancel-all
  code on zero, emptying the bomber's whole queue. Bombers idle there in retail
  too (SC23). The terrain-selected gravity and its projectile acceleration
  remain separate: neither supplies a fallback for this reader, and reversing
  the acceleration conversion would change even a stock value of 112 into 111
  `[04 R-AIR-01 §8]`.
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
  entries retain their admission; expansion probes untouched neighbors against
  the current layer on first opening (C18). The scheduler and the expansion receive no blocker identity, velocity or projected
  destination, and the collision commit never submits a replan. The follower
  requests one through its separate 60-tick poll. Mover-versus-mover
  contention is authoritative at commit through the row-major validator, the
  half-speed clamp response and the synchronous clear/commit/stamp sequence.
  Building and yard occupancy stay part of the static inputs
  `[04 §8.2]` `[04 §6.1 R-DOC04-B]` `[04 R-MOV-02A]` `[04 R-COLL-01 §7]`.
* **SC23 — `gravity = 0`.** Retail's cancel-all bound is cloned exactly. An
  asset census of 275 stock maps found none authoring, omitting or defaulting
  the key to zero, so the bound is unreachable on shipped content and a
  substituted default gravity would be invented behaviour
  `[04 R-AIR-01 §8]` `[fmt ota]`.
* **The ground steering's zero-divisor fallback — a recorded divergence.** The
  two distance gates that choose acceleration over braking divide the heading
  error by `TurnRate` and the squared speed by twice `BrakeRate`, and retail
  tests neither divisor for zero: a mobile definition that authors zero for
  either key, or omits it, divides by zero and terminates retail. The research
  states this as a fault and permits bounding it only as a **sanctioned,
  recorded** divergence `[04 R-MOV-01 §4]`. Nanolathe takes that permission:
  when either divisor is zero, `followerAccelerates` returns "accelerate"
  without evaluating the gates, which keeps a partial or synthetic definition
  making forward progress instead of faulting. Valid mobile content authors both
  keys, so the substitution is not expected to be reachable there; it is not a
  claim about what retail does. No census of the stock corpus has been run to
  bound reachability, which is what would let the entry move from recorded
  divergence to unreachable — as SC23 above did for gravity.

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

The three implementation questions previously listed here are closed by
static trace. Search preserves flags on open and performs no pop-time
passability recheck (C18). Ground steering now publishes its signed heading
step to the collision record before callback classification and save; flight
already uses that publication boundary `[04 R-MOV-01 §6]`. The code-3
velocity marker's auxiliary and trailing words remain opaque save state with
no movement consumer in the traced methods `[04 R-AIR-01 §14.5]`.

The contracts retain these questions, each with its settling observation.

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

The whole-layer classifier runs when a class layer is allocated and at no other
time `[03 R-LAYER §2]`, once: the registry attaches the mover, mapping and
commit-clock ports first and then stamps. (It used to stamp the bare layer and
stamp again with the ports, discarding the first result; a new layer's zero
watermark keeps the seeded clocks out of that stamp either way. On the
simulation benchmark's 540×540 map this removed about 5 ms from the tick that
allocates a class, with every fingerprint unchanged.) Each full rebuild classifies each source cell once
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

### Modern danger escape

**Nanolathe Modern policy (user-authorized prototype).** Orders own the decision
and work/stance protection described in DESIGN_UNITS_ORDERS_COB "Modern danger
response". Movement exposes straight and detour feasibility as pure queries
through the session's order binding. Orders first ranks all straight candidates
and only calls `System.DangerRouteFeasible` if none is admitted. This preserves
an available direct escape even when a detour endpoint has greater separation.
Strict never requests this escape mechanism.

Its first check, `DangerStepFeasible`, admits a short straight corridor, at most sixteen terrain
cells along either axis. It uses the mover's existing packed footprint bias,
footprint size and profile, rejects off-map footprints and other occupants,
and checks every crossed anchor with a supercover walk. Diagonal crossings
also check their two orthogonal neighbours. Ground candidates use the existing
terrain/feature classifier; aircraft candidates use the air occupancy plane
and map bounds, leaving altitude and takeoff to ordinary flight execution.
If the straight ground corridor is blocked, a bounded local probe can admit a
detour to a destination within 64 world units. It visits a fixed 16-unit lattice
inside that radius in breadth-first east/south/west/north order, using at most
five cardinal edges and a final corridor whose components are each at most 16
units. The disk contains at most 49 lattice positions. Every edge uses the same
footprint and supercover checks; copied unit positions supply hypothetical
starts without changing the real mover or occupancy. Intermediate positions
need not increase threat separation. This allows a unit to step around a crowd
before moving away, while orders still requires a safer destination. The probe
does not publish a route or invoke a second path kernel; ordinary scheduled
pathfinding chooses and executes the actual route. Aircraft retain the straight
check only. The bounds and traversal are Modern prototype tuning. This is
local admission, not proof of an arbitrary global escape route. A fully trapped
unit receives no fabricated movement solution.

The query writes no state, allocates no route, draws no RNG and changes no
speed or movement scheduler budget. Accepted goals still use ordinary order
activation, path scheduling, movement and collision; moving obstacles may
invalidate an initially clear corridor. The order's retry and expiry policy
owns subsequent choices. No new movement state is saved or carried through a
mode switch. Tests cover blocked corridors despite clear destinations,
diagonal corners, footprint bounds, the different ground/air planes, bounded
detour admission and execution around the friendly crowd observed in an F11
capture on Great Divide.

### Modern crowded arrival

**Nanolathe Modern policy (user-authorized prototype).** The ground follower
asks `orders.Rules.CrowdedMoveArrival` on its ordinary visit before the existing
arrival check, including when its route is inactive or rejected. Orders owns
terminal-move eligibility, the 96-world-unit radius and the 90-tick dwell:
[Modern crowded arrival](DESIGN_UNITS_ORDERS_COB.md#modern-crowded-arrival).
Strict returns false without writes or random draws and retains its researched
arrival/retry behavior [04 R-PATH-01 §9][04 R-ORD-01 §4].

`System.CrowdedMoveBlocked`, bound through the existing movement adapter, is a
pure local query over the mover's committed ground footprint and current
profile. The order must still own the active movement binding, and a route
with multiple active points remains ineligible. The requested goal is
quantized with the mover's half-footprint bias. Its footprint must overlap at
least one same-owner stationary mobile; structures, enemies, incomplete or
carried units and moving occupants are never relaxed.

The short corridor spans at most six cells per axis. Every footprint along a
supercover walk must be in bounds and statically passable for this movement
profile. Occupied footprints may contain only the admitted friendly mobiles;
diagonal crossings check both orthogonal neighbors. The eight adjacent
anchors are then tested in a fixed order: if any strictly reduces squared cell
distance to the destination and has a free, statically passable footprint and
crossing, the mover must continue trying. A crowded destination alone is not
enough. No global closest-point search, global route reachability promise or
interpretation of stale route point bytes is involved. Existing search
frontier tolerance remains separate [04 R-PATH-01 §5][04 R-PATH-01 §9].

Once orders admits completion, the follower raises arrival through its normal
arrival/release helper and returns before repath service can rearm the old
payload. Ordinary movement braking and order cleanup continue afterward. The
query changes no terrain, occupancy, search budgets, RNG or resources. Tests
retain the captured two-by-two Flash rally layout on flat terrain, including
an inactive rejected route and an unexpired phase-zero retry deadline; they
also reject static obstructions, wrong or moving occupants, active routes and
an available closer anchor. Terrain and live crowd changes are revalidated
throughout the dwell; no fresh route result is required after loading a save.
The composed session regression also uses Great Divide's authored terrain and
the captured fourteen-unit crowd, restoring five nearby tree footprints that
were already cleared in the diagnostic. It drives an ordinary point move
through live path rejection and verifies Modern idle/cleanup versus Strict
retry. Decorative ground marks do not participate in its collision fixture.

### Modern learned terrain

**Nanolathe Modern policy.** A ground mover that static ground rejects teaches
its owner the ground that rejected it, so the follower's next ordinary repath
routes around it instead of reproducing the route that failed. Fog-of-war
pathing stays honest — a unit still walks blind into ground its owner has not
explored — and only the permanent trap is removed.

**Strict 3.1 behavior.** The search answers "unexplored" for any block whose
mapping word lacks the requester's owner bit and treats that answer as
passable, without reading the class layer `[04 R-PATH-01 §2]`. The commit
validator tests the real ground and rejects the step, and a rejection changes
no search input: no cell is marked, the mapping grid is not written, no count
is kept `[04 R-MOV-01 §7]` `[04 R-COLL-01 §3]`. The follower re-requests every
sixty ticks and receives the same route for the life of the order. The LOS
publisher does not rescue the unit, because it stamps the mapping grid at the
height-sheared tile while the search consults the unsheared ground block: the
ground at the foot of a south-rising face is read by the search from a block
that lies behind the crest `[04 R-PATH-01 §2]` `[03 R-VIS-01 §3]`.
`StrictRules` does nothing at the rejection and returns no grid at the request
open, so every search input is retail's.

**Modern behavior.** When the commit validator rejects a mode-1 ground
proposal and the first failing footprint cell failed the *static* test —
terrain or a blocking feature, `Profile.IsPassableCommitCell` — rather than the
occupant test, `ModernRules.StaticRejection` sets the owner's bit in
`System`'s `LearnedTerrain` grid for every mapping block the search consults
for an anchor inside the rejected footprint rectangle, skipping blocks the
owner's mapping word already carries. At each request open
`ModernRules.LearnedTerrain` hands that grid to the search's passability port;
a block the requester's owner has learned is then answered from the stamped
class layer exactly as a mapped block is, and everything else keeps the retail
four-step read. Nothing else changes: the blocked response, the half-speed cap,
the sixty-tick throttle, the scheduler's single active request and its
100-unit charge, the search budgets and the publication boundary are retail's
in both modes.

A set bit means "this owner has touched this block", never "this block is
blocked". The verdict always comes from the class layer at search time, so a
reclaimed feature or a changed layer is seen at the next search and learned
knowledge cannot go stale.

**Where the knowledge lives, and why not the mapping grid.** Writing the
owner's bit into the visibility publisher's mapping word grid would have been
one line and would have been saved for free. It was rejected because that grid
has other readers, and every one of them indexes it by the height-sheared tile:
the unit visibility sample (which reads the word grid directly when LOS is off
and Mapping is on), the known-site placement gate, the aircraft landing accept,
the explored-terrain fog and the minimap `[03 R-LAYER §1]` `[03 R-VIS-01 §3]`.
A bit written at the search's unsheared index names, to all of them, ground
about half the terrain height further south than the ground the unit touched:
it would reveal a 32-pixel square of terrain the player has not seen, could
make an enemy standing there visible and targetable, and could admit a build
site. `LearnedTerrain` is therefore a separate grid of the same shape — one
word per 2×2-cell block, one bit per player slot — indexed in the search's own
frame and read by nothing but the search.

**How much a rejection teaches.** Every block the search consults for an
anchor inside the rejected rectangle, not only the proposed anchor's block.
Measured on Crystal Maze with `ARMFLEA` under the default options (Mapping on,
True LOS), over 42 single-unit start/goal pairs for 6000 ticks: both variants
free all ten units Strict wedges, on identical completion ticks; across the 42
pairs they differ in one, which the rectangle variant finishes 141 ticks
sooner. In two 100-unit rally-style runs the rectangle variant left fewer units
in transit at tick 9000 (53 and 30, against 67 and 52). The cost is the same.
A unit "feels along" a long face one block per lesson, because each lesson
reveals one block and the next blind route tries the block beside it. That
cost is bounded and small: blocked ticks were 3–8.5 % of the affected trips
(88–275 ticks of 1776–3723), with no stall episode of 150 ticks; the reported
flea spends about 300 ticks working along its face. The rest of the gap to an
all-mapped run (trips 1.3–1.8× longer) is walking into and back out of dead
ends, which is what honest fog pathing costs and which no repath timing
changes.

**No prompt repath.** Letting Modern re-request immediately after a lesson,
instead of waiting out the sixty-tick throttle, was considered and not adopted:
it would be a second departure, and the measurement above bounds what it could
recover at the blocked share of the trip. This policy never shortens the
throttle; the separate
[Modern re-route staggering](#modern-re-route-staggering) only lengthens it by
up to seven ticks.

**The seam.** `movement.Rules` (`internal/movement/rules.go`) is a new seam,
composed as `session.RuleSet.Movement` beside the other package seams:
`StrictRuleSet` binds `movement.StrictRules{}`, `ModernRuleSet` binds
`&movement.ModernRules{}`, the registry completes an unstated field from the
set's base, and `Session.BindRules` projects it onto `System.Rules` at the same
site that projects the search kernel, so a system composed later or by a
restore receives it through the same `RebindRules` call. No existing seam owns
the two questions: they are asked by this package's own occupancy commit and
request open about state this package owns; they are not an order decision
(`orders.Rules`), not construction, and not a replacement search —
`path.Kernel` still opens the same retail search over the same passability
port, and what changes is one input to that port
([DESIGN_GAMEPLAY_RULES §9](DESIGN_GAMEPLAY_RULES.md#9-extending-the-existing-mechanism)
step 3). Both implementations are zero size. Both questions are asked at
request granularity — once per rejected commit, once per opened search — and
the per-node work is a slice read inside the search's existing passability
closure, never an interface call
([§4](DESIGN_GAMEPLAY_RULES.md#4-granularity)). An unbound `System` answers as
Strict.

**State.** `System.learned` is nil until the first lesson, so a Strict session
never allocates it and a Modern session that has learned nothing keeps the
retail passability closure. It is sized from the terrain (`CellW>>1 ×
CellH>>1` words), lives as long as its `System` — one battle — and is never
cleared: bits are only ORed, as the mapping grid's are. It is derived state
with no retail save field and Nanolathe adds no save metadata
([DESIGN_GAMEPLAY_RULES §6](DESIGN_GAMEPLAY_RULES.md#6-save-interaction)), so
**a save does not carry it**: a loaded battle starts with nothing learned and
relearns one rejection at a time, which costs the affected units their lessons
again and nothing else. The visibility mapping grid a save does carry is never
written by a lesson. After a switch to Strict the grid is kept but neither
written nor read, so Strict's search inputs are retail's from the next request;
a switch back to Modern resumes with what was learned. A route published
before a switch is followed to its end in either direction.

**Boundaries.** Only mode-1 ground commits teach; aircraft, carried units and
buildings never reach the validator's cell scan. A rejection by another unit
teaches nothing — the occupant-age gate is that case's writer
`[04 R-COLL-01 §3]` — and neither does the map edge, which the search bounds
itself. A rejection whose first failing cell is occupied teaches nothing on
that tick even if a later cell is a wall. Knowledge is per owner: allies do not
share it, computer players learn exactly as humans do, and nothing is learned
with Mapping off, because every block is already mapped. The policy does not
touch a wedge the search cannot see for another reason — a class layer that
disagrees with the commit test, or the documented near-goal re-arm loop around
an occupied destination `[04 R-PATH-01 §9]`. It does not promise a short
route.

**Determinism.** The write happens inside the phase-2 unit visit, in pool slot
order, from committed state only. It draws from neither stream, touches no
resource, writes no transform, occupancy, visibility or order state, and
iterates no map. A scene in which no unit is rejected under ground its owner's
search calls unexplored is bit-identical to the same scene without the policy:
all eight locked fingerprints (`headless.TestStrictFingerprintIsLocked`,
`headless.TestModernFingerprintIsLocked`) are unchanged, as are the
simulation-cost benchmark's three Modern fingerprints, and the benchmark's cost
is unchanged within run-to-run noise. A Modern scene that does take a lesson
under unexplored ground diverges from its earlier self at the next repath, by
design; Strict never does.

**Verification.** `movement.TestModernTerrainRejectionTeachesOnlyTheOwner`
(the owner's next search routes around; another player's is unchanged),
`TestModernUnitRejectionTeachesNothing`,
`TestModernMappedRejectionTeachesNothing`,
`TestStrictTerrainRejectionLearnsNothing` (no grid, the same blind repath, no
draw from either stream, bound or unbound),
`TestStrictIgnoresTerrainLearnedBeforeASwitch` and
`TestMovementRulesDispatchDoesNotAllocate`;
`session.TestBindRulesProjectsEverySeam` and
`TestCompositionProjectsTheSearchKernelOntoMovement` for the composition. In
the retail tier, `session.TestModernLearnedTerrainFreesTheMazeFlea` runs the
reported case in both modes — the Strict half encodes this build's LOS stamp
set at that face, which `[04 R-PATH-01 §2]` records as not yet observed in
retail, and says so — and `TestModernLearnedTerrainIsRelearnedAfterALoad` locks
that a lesson never reaches the visibility grid, that a load restores nothing
learned, and that the restored unit still gets free.

### Modern re-route staggering

**Nanolathe Modern policy.** A follower's re-route throttle is lengthened by a
deterministic 0–7 ticks, re-drawn from the unit's slot and its last admission
tick at every admission, so followers that were admitted together stop coming
due together. It is a load-balancing policy: it spreads path-search work that
retail's throttle concentrates on one tick, and it changes no route a search
returns.

**Strict 3.1 behavior.** The follower's poll answers "wants a path" when its
wants-repath flag is set and `lastRequestTick + 60 <= currentTick`, inclusively;
a yes stamps the current tick `[04 R-MOV-01 §7]`. Goal installation zeroes a
stamp older than ten ticks `[04 R-PATH-01 §8]`, so every unit given an order on
one tick is admitted on that tick as far as the scheduler's budget reaches, and
from then on a unit that stays blocked or keeps exhausting its route re-requests
exactly sixty ticks after its last admission. `StrictRules.RepathDelay` returns
60 for every unit; Community inherits it unchanged (this is a Modern policy,
not a Community 3.9 feature), and an unbound `System` answers the same.

**Why the cohort matters.** Retail's step allowance of 1,333 per call admits at
most about thirteen searches a tick (each admission costs 100 plus its pops
`[04 R-PATH-01 §6]`), so — by that arithmetic, not by observation of retail —
the budget itself spreads a large cohort over many ticks and the stamps drift
apart. Community and Modern raise the allowance to
66,650 (DESIGN_COMMUNITY_PATCH §4.1): the whole cohort is now admitted on its
first tick, receives one stamp, and is due again on one tick every sixty ticks
for as long as it keeps re-arming. Measured with temporary per-tick
instrumentation (not committed) on the pre-policy Modern build:

* Battle benchmark (scene v5, 360 mobiles created on one tick): 274 followers
  were admitted on tick 60 (the first tick a zero stamp permits), 205 on 120,
  141 on 181, then 37–89 on each of 240–243, 300–303 and 360–363; tick 181 ran
  98,000 search steps and fifteen ticks exceeded 20,000. This first cohort is
  an artifact of staging every unit on one tick.
* Simulation benchmark (three computer armies, 1,500 ticks): the planner's
  group orders admit 150–285 units on one tick, and 4,291 of the re-admissions
  came exactly sixty ticks after the previous one, and 236 units whose last
  admissions fell on ticks 602–605 were all re-admitted on tick 666. The
  alignment therefore arises in ordinary play, not only in the fixture.

**Modern behavior.** `ModernRules.RepathDelay(s, slot, last)` returns
`60 + k`, where `k` in `0..7` is the top three bits (scaled by
`modernRepathSpread`) of a 32-bit integer mix of the slot and `last + 1`. It
reads no state, draws from neither RNG stream and uses no floating point. The
follower's staging test and the scheduler's poll both call `System.repathDue`,
so the staged payload and the admission can never disagree; the stamp, the
inclusive comparison, the flag's writers, the ten-tick goal-install reset, the
scheduler's single active request, its charges and budgets are retail's.

**Per-admission mix, not a fixed per-unit phase.** A fixed offset per slot
splits a cohort into eight sub-cohorts at its first re-admission, but units that
share an offset then share every later stamp and stay in step for ever, so the
worst tick settles at one eighth of the cohort. Mixing in the last admission
tick re-draws each unit's offset every cycle, so two units that happen to
share a stamp almost always separate at the next one. For 240 units stamped on
one tick the largest same-tick re-admission is 40 after one cycle and 20 after
ten, still falling (`TestModernRepathStaggerSeparatesACohort`); Strict keeps all
240 together.

**What it does not spread.** The first admission after an order is not
delayed: a zeroed stamp plus at most 67 ticks is due on any tick after 67, so
a group order is still answered on the tick it is issued, exactly as in Strict.
Group-order bursts therefore remain under this policy alone — the simulation
benchmark still admits up to 264 fresh requests on one planner tick — and are
spread by [Modern group-order spreading](#modern-group-order-spreading).

**Cost to the player.** A blocked or route-exhausted unit re-requests after
60–67 ticks instead of 60, at most a quarter of a second later. No unit is
refused a route it would have received; it receives it up to seven ticks
later. Where a unit re-routes repeatedly the delays add up, and the changed
timing can change the route: the Crystal Maze flea of
[Modern learned terrain](#modern-learned-terrain) still frees itself from the
face after about 400 ticks there, but its diverged trip stalls about 200 ticks
at an earlier face and is freed near tick 1150 instead of 950, so
`session.TestModernLearnedTerrainFreesTheMazeFlea` measures over ticks
700–1500 rather than 700–1200.

**Measured effect.** Two back-to-back pairs of each benchmark against the
same build without the policy, `GOMAXPROCS=2` (2026-09-22):

| | before | after |
|---|---|---|
| Battle benchmark `phase5-orders` max (ms) | 7.53, 7.71 | 2.46, 2.50 |
| Battle benchmark `phase5-orders` mean (ms) | 0.75, 0.78 | 0.89, 0.88 |
| Battle benchmark Step p95 / max (ms) | 5.38 / 9.71, 5.97 / 9.95 | 4.28 / 4.85, 4.26 / 4.86 |
| Battle run, largest per-tick search steps | 97,996 | 35,371 |
| Battle run, ticks above 20,000 steps | 15 | 7 |
| Simulation benchmark tick mean (ms) | 1.86, 1.92 | 1.71, 1.73 |
| Simulation benchmark `phase5-orders` mean (ms) | 0.72, 0.73 | 0.65, 0.65 |
| Simulation benchmark tick p95 / max (ms) | 3.09 / 11.4, 3.10 / 12.5 | 3.07 / 13.3, 3.16 / 14.0 |

The battle benchmark (`--renderer=modern --benchmark-tps=120`, scene v5) has a
45-tick measured window containing one cadence tick; its spike is gone. Its
mean rises because, in the diverged trajectory, the searches in that window
are longer (instrumented: 669 admissions and 776,000 steps over ticks 240–419,
against 690 and 558,000); the policy changes no search input, so this is a
different battle rather than a per-search cost, and the simulation benchmark's
mean falls. The simulation benchmark's worst tick is a planner group order's
first admissions, which this policy deliberately leaves alone.

**Boundaries.** Only the throttle period changes, for every follower the
scheduler polls. It keeps no state, so saves, restores and switches need nothing: a switch to
Strict applies retail's 60 from the next poll, and a follower stamped under
either mode is judged by the mode bound when it is polled.

**Determinism and fingerprints.** The answer is a pure integer function of the
slot and the committed stamp and is asked in the scheduler's existing slot
order, so Modern stays deterministic and bit-identical across hosts. Modern
fingerprints move, because routes arrive on different ticks; Strict and
Community fingerprints do not. The Modern long ashap lock moved to
`partial-v1:002787072051be96` and that run no longer ends before its
54000-tick bound (it ended at 49950); the Modern battle-fixture warm and final
locks moved to `partial-v1:9a3af7e60b955710` and `partial-v1:6a826efb7465ecb2`.
The Modern ashap 6000-tick and fixture initial locks are unchanged.

**Verification.** `movement.TestStrictRepathDelayIsRetailsSixty` (Strict and
Community return 60 for every slot and stamp; unbound refuses at `last+59` and
admits at `last+60`), `TestModernRepathDelayBoundsAndDeterminism`,
`TestModernRepathStaggerSeparatesACohort`, `TestModernPollHonoursTheRepathDelay`
(refused one tick before the delay, admitted and stamped on it) and
`TestRepathDueDoesNotAllocate`; the Strict, Community and Modern
`headless` fingerprint locks cover RNG and resource effects over whole
battles.

### Modern group-order spreading

**Nanolathe Modern policy.** When one player's order gives sixteen or more
units their first route request on the same scheduler tick, the requests are
admitted over three consecutive ticks instead of one: nearest goal first, cut
so that each tick carries about a third of the group's summed goal distance.
It is a load-balancing policy like
[Modern re-route staggering](#modern-re-route-staggering), which it completes:
that policy spreads re-requests and deliberately leaves the first request
alone. It changes only the tick a request is admitted on, never the request,
its goal or any search input.

**Strict 3.1 behavior.** Goal installation zeroes an admission stamp older than
ten ticks `[04 R-PATH-01 §8]`, and the follower's poll admits any armed
follower whose `lastRequestTick + 60 <= currentTick` `[04 R-MOV-01 §7]`, so
every unit given an order on one tick is admissible on the next scheduler call,
as far as the step allowance reaches `[04 R-PATH-01 §6]`.
`StrictRules.FirstRequestSpread` answers "off" (a threshold of zero);
Community inherits it (this is not a Community 3.9 feature), and an unbound
`System` answers the same. With the rule off nothing is recorded and the one
added comparison in the due test, against a hold that is always zero, never
refuses.

**Why.** At retail's allowance of 1,333 steps a call, the budget alone admits
about thirteen searches a tick, so — by that arithmetic, not by observation of
retail — a large order is spread over many ticks. Under the 66,650 allowance
(DESIGN_COMMUNITY_PATCH §4.1) the whole group is searched on one tick. In the
simulation benchmark (seed 7, instrumented, not committed; all 4,200 ticks)
78 ticks admitted 16
or more first requests, 5,507 in all and up to 313 on one tick; those were the
benchmark's slowest ticks, 12.6–13.9 ms against a median of about 1.7 ms. Below
sixteen the per-tick count is ordinary traffic: over 4,200 ticks, 1,437 ticks
admitted 0–3 first requests, 76 admitted 4–7, 18 admitted 8–11 and 4 admitted
12–15, after which the counts are the planner's group orders.

**Modern behavior.** `ModernRules.FirstRequestSpread` returns a threshold of 16
and a width of three ticks.

1. *Recording.* Every staging of a route request (`pathProvider.Submit`) whose
   follower's admission stamp is zero is a first request: the unit has not been
   admitted since its goal was installed. It is appended once to the
   `System`'s pending list, and any hold an earlier staging was given is
   cleared, because a new order is a new request.
2. *Grouping.* At the start of each scheduler call, before its first poll, the
   pending first requests that are due on this tick (armed, and the re-route
   throttle from the zero stamp elapsed) form the candidates; the rest stay
   pending until they are due. A candidate whose request was withdrawn or that
   has since been admitted is dropped. Candidates are grouped by the player
   whose request map holds them.
3. *Assignment.* A player's group smaller than the threshold is left alone.
   Otherwise its requests are sorted by estimated cost, then by slot. The cost
   is the request goal's own search heuristic from the unit's committed start
   cell `[04 §7.2]` plus one, so a group of zero-distance requests still splits
   by count. Walking the sorted list, a request is given hold `k` = ⌊3 ×
   (summed cost of the requests before it) ÷ (group total)⌋, capped at 2: the
   second tick starts where the running sum first reaches a third, the third
   where it reaches two thirds. Hold 0 is admitted as today; hold `k` sets the
   route's earliest admission tick to `tick + k`.
4. *Admission.* `System.repathDue`, which both the follower's staging test and
   the scheduler poll use, refuses a follower whose hold tick is still ahead.
   Admission stamps the tick as always and clears the hold.

Integer arithmetic only, no RNG, no map iteration; the list is walked in
staging order and the sort breaks ties by slot. Assignment reuses its scratch
and allocates nothing once warm.

**Composition with re-route staggering.** A held request is admitted on its hold
tick and stamped with it; from then on the follower is under the ordinary
throttle, so its next re-request is `RepathDelay` (60–67 ticks) after the held
admission. The two policies share `repathDue`, so staging and admission agree.

**What a waiting unit does.** Nothing new. Goal installation already gives the
follower something to steer by before any search returns `[04 R-PATH-01 §8]`:
the synthetic straight line to the goal, or the points the route-acceptance
rule kept, or — where neither applies — no active route, and the unit waits.
That is exactly the state a unit is in between its order and its search in
Strict, which under retail's allowance lasts many ticks. Instrumented over the
simulation benchmark's warm-up and 600 measured ticks, of 1,210 held requests
735 held a two-point route (the shape of the synthetic line), 461 a longer
kept route and 14 no active route. Order acceptance, the
acknowledgement, the queue and everything shown to the player on the order
tick are untouched: the hold is on the scheduler's admission only.

**Cost to the player.** Only groups of sixteen or more wait, and only their
farther members. Over the seed-7 simulation benchmark's 4,200 ticks, 5,282
requests were spread: 55% were admitted on the due tick, 28% one tick later (33 ms) and 17%
two ticks later (67 ms); the mean added wait of a spread request is 0.62 ticks.
The rear of a column would be waiting behind the front line anyway.

**Boundaries.** The threshold is per player per tick; two players' orders on one
tick are two groups. Re-requests are never spread by this policy (they have a
non-zero stamp). A long search can still block the scheduler: it holds the
single working set, the other players' shares accumulate while it runs, and
when it publishes the waiting requests — held ones included — are admitted
together `[04 R-PATH-01 §6]`. The largest remaining planner-tick spikes in the
simulation benchmark are of this kind; this policy does not change the
scheduler's accumulator and does not address them. Holds and the pending list
are runtime state and are not saved: a restore admits a held request when it
next comes due. After a switch to Strict, holds already assigned (at most two
ticks) run out and nothing new is recorded.

**Measured effect.** Built against main `412cedcc` (the re-route staggering
landing), `GOMAXPROCS=2`, 2026-09-22, on a host with other work running.

Simulation benchmark (1,200 warm-up and 3,000 measured ticks, profiles off),
five seeds. "Group-window max" is the slowest tick in the first ten ticks of
each 300-tick planner cycle, excluding ticks that carried a class-layer
rebuild:

| seed | group-window max (ms) before → after | tick max | p95 | p99 | mean |
|---|---|---|---|---|---|
| 7 (two runs each) | 13.8, 13.9 → 9.8, 10.0 | 13.8, 18.2 → 11.5, 22.6 | 3.26, 3.38 → 2.74, 3.00 | 4.63, 4.68 → 4.39, 5.46 | 1.83, 1.86 → 1.84, 1.91 |
| 8 | 11.5 → 12.7 | 11.9 → 12.7 | 2.76 → 3.28 | 3.54 → 4.50 | 1.79 → 1.88 |
| 9 | 11.1 → 8.2 | 11.1 → 12.0 | 3.00 → 2.67 | 4.40 → 3.96 | 1.94 → 1.78 |
| 10 | 11.5 → 8.6 | 12.1 → 13.2 | 2.73 → 2.90 | 3.72 → 4.24 | 1.85 → 1.90 |
| 11 | 15.2 → 11.9 | 15.2 → 11.9 | 2.67 → 2.65 | 4.49 → 3.96 | 1.84 → 1.81 |

The first-admission spike is gone: at seed 7 the 12.6–13.9 ms group ticks
become three ticks of about 3.5–8 ms. Seed 8's worst group-window tick after the
change is a backlog release (a 438-request group was assigned to ticks
3901–3903, a long search then held the scheduler, and 230 requests were
admitted on 3906). The remaining tick maxima that exceed the group windows are
class-layer rebuild ticks, which the benchmark counts separately, or host
stalls spanning consecutive ticks (seed 7's 22.6 and 18.2). Mean, p95 and p99
move both ways within the spread of runs: each change of admission tick
produces a different battle from that point, so these are different
trajectories rather than a per-tick cost.

Battle benchmark (`--renderer=modern --benchmark-tps=120`, scene v5), two
back-to-back pairs:

| | before | after |
|---|---|---|
| `phase5-orders` max (ms) | 2.38, 2.45 | 2.26, 2.14 |
| `phase5-orders` mean (ms) | 0.90, 0.89 | 0.74, 0.71 |
| Step p95 / max (ms) | 4.24 / 4.77, 4.25 / 4.88 | 3.77 / 4.52, 3.46 / 4.29 |

**Determinism and fingerprints.** Modern stays deterministic and bit-identical
across hosts; Strict and Community fingerprints do not move. The Modern
benchmark-fixture warm and final locks moved to `partial-v1:8d9eef3348ae2124`
and `partial-v1:b1586e88c6421317`. The Modern ashap locks do not move: that
scene never forms a group of sixteen same-tick first requests (its largest is
seven). The Modern fixture initial lock is unchanged.

**Verification.** `movement.TestStrictFirstRequestSpreadIsOff` (Strict,
Community and unbound: the rule is off, nothing is recorded, a twenty-unit
group is admitted on its due tick), `TestModernFirstRequestSpreadOverThreeTicks`
(twenty units use all three ticks and a later admission is never nearer its
goal than an earlier one; fifteen units, and two players of twelve, are not
spread), `TestHoldGroupBalancesByCost` (cut points on a worked example),
`TestFirstRequestHoldComposesWithRepathDelay` and
`TestAssignFirstRequestHoldsDoesNotAllocate`; the Strict, Community and Modern
`headless` fingerprint locks cover RNG and resource effects over whole
battles.

### Modern bounded path work

**Nanolathe Modern policy.** The path scheduler carries at most four ticks'
share of unspent search work per player, and stops polling a player for the
rest of a call once a whole sweep of that player's units has admitted nothing.
It is a frame-time policy: it bounds how much search and polling one tick can
do, and changes no search input or route.

**Strict 3.1 behavior.** Each call adds `stepAllowance / playerCount` to every
eligible player's accumulator, and the admission loop polls and searches until
the call's total is spent; unspent work carries forward without bound
`[04 R-PATH-01 §6]`. Every poll raises the player's service count, from which
the 150-call replenish derives the heuristic weight tier `[04 R-PATH-01 §10]`.
`StrictRules.PathWorkBound` answers `(0, false)`; Community inherits it and an
unbound `System` answers the same. A provider that does not implement the
question — every standalone scheduler — also keeps this accounting.

**Why.** At retail's allowance of 1,333 the carry is small. Under the 66,650
allowance (DESIGN_COMMUNITY_PATCH §4.1), a player whose units wait behind
another player's long search banks about 22,000 steps a tick. In the opt-in
path benchmark's `traffic/waves` 1500-unit case two players had banked about
275,000 each when the working set freed, and one tick then made 1.8 million
polls — mostly of units whose staged requests the
[group-order spreading](#modern-group-order-spreading) hold still refuses,
which the idle-run batching cannot skip — taking about 150 ms; another tick
spent 403,000 search pops in 53 ms. The same deterministic ticks spiked in
every repeat. Separately, an ordinary tick with nothing admissible still
polled until its whole share was gone.

**Modern behavior.** `ModernRules.PathWorkBound` answers `(4, true)`; the
movement system's path provider relays it to `path.Scheduler` through the
optional `workBoundProvider` question, asked once per call.

1. *Carry cap.* After a player's share is added, an accumulator above four
   shares is cut to four shares. The discarded amount is added to the player's
   service count — the polls it would have bought — so the heuristic tier the
   next replenish derives sees the same load as retail's accounting.
2. *Futile-sweep stop.* The scheduler counts each player's polls since its last
   admission in this call, including polls charged in bulk by the idle-run
   batching. When the count exceeds the provider's sweep length — the number of
   physical slots in the player's unit slice, which one sweep of the poll
   cursor visits — the player's remaining accumulator is credited to its
   service count and set to zero. An admission restarts the count; every count
   restarts at the next call.

Active-search continuation, admission charges, the 100-pop slice, the
full-or-empty publication, the poll cursor and its per-slot walk are retail's.
Integer arithmetic only, no RNG, no map iteration, no allocation.

**Cost to the player.** Work that would have been spent in one burst is spread
over the following ticks, so in a burst some requests are admitted later: in
`traffic/waves` 1500 the mean pending age of a move rose from 54 to 62 ticks
while the longest fell from 196 to 164. In the controlled-input
`round2/fixed-mixed-waves` 1500 case arrivals are unchanged (1005 → 1007 of
4500). The futile stop also changes the order in which later requests are
admitted, since the poll cursor no longer spins through the remaining share;
dense 256-unit crowds are sensitive to that order in both directions (three
perturbed open layouts: 256/125/214 arrivals in the window under the previous
Modern, 212/256/256 under this policy).

**Measured effect.** Opt-in path benchmark (research branch
`proto/path-round3`, GOMAXPROCS=2, three repeats, medians):

| | before | after |
|---|---|---|
| `traffic/waves` 1500 p95 / p99 / max (ms) | 6.1 / 12.5 / 148 | 3.4 / 5.1 / 10.0 |
| `traffic/waves` 1500 ticks above 16.7 ms per run | 6–8 | 0 |
| `round2/fixed-mixed-waves` 1500 p95 / p99 (ms) | 2.7 / 6.6 | 1.6 / 3.0 |
| open 256-unit group p95 (ms) | 3.0 | 0.4 |
| open 128-unit group p95 (ms) | 1.5 | 0.2 |

Displayless simulation benchmark (`nanolathe-headless --sim-benchmark`, seed 7,
1,200 warm-up and 3,000 measured ticks, GOMAXPROCS=2, three alternating runs
each, 2026-09-23): mean tick 1.67 → 1.60 ms, p95 2.52 → 2.31 ms, p99
3.68 → 3.38 ms. The maximum is 10.8–10.9 ms before and 11.5–12.0 ms after; the
trajectories diverge after the warm-up, so this is a different battle's worst
tick rather than a per-tick cost, and the same order of maximum is present
without the policy.

**Boundaries.** The cap applies to every eligible player and to the player of
an active search alike; a search that outlives four shares resumes next call as
before. The sweep length is the player's whole slot slice, so a stop never
precedes a full visit of every unit. Accumulators, service counts and the poll
cursor carry no new saved state; the per-call counts reset every call. After a
switch to Strict an accumulator already capped simply grows again from the
next call.

**Determinism and fingerprints.** Modern stays deterministic and bit-identical
across hosts; Strict and Community fingerprints do not move, nor do the Modern
ashap locks or the benchmark-fixture initial lock. The Modern benchmark-fixture
warm and final locks moved to `partial-v1:471cf87f63c23989` and
`partial-v1:9d57adada125953c`: admission order after a futile sweep differs.

**Verification.** `path.TestModernWorkBoundCapsCarryAndCreditsPolls` (retail
banks at least nineteen shares behind a long search; the bound keeps four and
carry plus credited polls equals retail's), `TestModernWorkBoundSweepStop` (a
`(0, false)` answer equals a provider without the question; Modern stops after
one sweep plus the proving poll, credits the rest, and an admission restarts
the sweep), `movement.TestPathWorkBoundAnswers` (Strict, Community, unbound,
Modern; the answer does not allocate); the Strict, Community and Modern
`headless` fingerprint locks cover RNG and resource effects over whole battles.

### Modern allied pass-through

**Nanolathe Modern policy.** Two ground movers of the same owner, or of owners
allied with each other, that meet heading against each other while both are
mid-route may pass through each other's footprints instead of stopping. It
replaces a head-on deadlock with a brief overlap; every other occupant still
blocks.

**Strict 3.1 behavior.** The commit validator rejects a proposal whose
footprint holds any other occupant; the blocked mover halves its speed, clamps
against its old footprint and proposes the same move next tick `[04 R-COLL-01
§1]` `[04 R-COLL-01 §2]`. Only the 60-tick repath and the occupant-age gate
change anything, so two groups meeting head-on stay locked until one side's
stationary units become walls to the other's searches. In the opt-in path
benchmark two same-owner 32-flea groups meeting head-on in open ground finish
16 of 64 moves in 900 ticks, and allied groups through an eight-cell aperture 10
of 64. `StrictRules.AlliedPassThrough` answers false; Community inherits it.

**Modern behavior.** `ModernRules.AlliedPassThrough` answers true. In the
ground commit's per-cell occupant test, a cell held by another unit is
treated as free when that unit (`System.alliedPassPartner`):

1. is a grounded (mode 1) mover, not a building, not carried, alive;
2. belongs to the mover's owner, or to an owner whose alliance row and the
   mover owner's row both declare the other allied (the same rows the guard
   join reads `[05 R-SHARE-01 §1]`, resolved once per tick through the order
   binding);
3. has an active route of at least two points whose final point is more than
   64 world units away, as does the mover — so an overlap is never carried into
   a destination, where the crowded-arrival and destination-slot policies
   apply;
4. heads at least 0x5555 (about 120°) away from the mover's committed heading.

Terrain, features, structures, enemies, stopped units and same-direction or
crossing traffic block exactly as before. The stamp's existing overlap
arbitration owns any contested cell once the mover commits
(ClaimConflict, `[04 R-COLL-01 §4]`); the pass changes only the occupant test.
No state is kept, nothing is saved, no RNG is drawn.

**Cost to the player.** Opposing friendly units briefly overlap while
passing. A variant that also passed crossing traffic (≥ 90°) or waited four
blocked ticks first was measured and rejected: the first wedged an opposing
choke, the second gave back most of the gain. Hostile head-on groups are
unchanged — they meet and block as in retail.

**Measured effect.** Opt-in path benchmark on research branch
`proto/path-round3`, deterministic one-repeat outcomes, against a Modern with
bounded path work and destination slots: near-goal arrivals 829 → 1,000 of
1,463 and pending moves 594 → 415 over 43 case/size combinations; allied
eight-cell choke with 64 units 10 → 31, allied passing bays with 8 units 0 → 8,
same-owner head-on 64 29 → 55 (16 under the earlier Modern), same-owner
four-cell choke 16 0 → 16. Two cases lost arrivals (a 64-unit rotated
formation crossing 11 → 9, a 64-unit perpendicular crossing 54 → 52). Total
tick cost did not rise: blocked units re-search less. A hostile head-on
control shows no pass between the groups.

**Boundaries.** Aircraft and carried units never
reach the ground occupant test. Four-cell chokes with 64 units per side still
jam: passing needs a head-on meeting, and a crowded aperture holds units at
every angle. Directional choke coordination remains open.

**Determinism and fingerprints.** The partner test reads committed state in
the sweep's slot order and writes nothing; Modern stays deterministic and
bit-identical across hosts. Strict and Community fingerprints do not move,
nor do the Modern ashap locks; the Modern benchmark-fixture warm and final
locks moved to `partial-v1:4fd8922d85f7e16f` and `partial-v1:8fdb5da2c8ee19d8`
because that battle's computer armies now pass through each other.

**Verification.** `movement.TestAlliedPassThrough` (Strict and Community
reject; Modern passes same-owner and mutually allied head-on movers and
commits into the passed cell; an unallied owner, same-direction traffic, a
routeless blocker and a mover near its route end are rejected by that
blocker), `TestAlliedPassThroughAnswers` (the answers; a Strict tick never
resolves the alliance query); the Strict, Community and Modern `headless`
fingerprint locks.

### Modern unreachable moves

**Nanolathe Modern policy.** A plain terminal ground move whose goal the unit
cannot reach — the goal is sealed off by terrain, features or buildings the
owner knows about — finishes at the wall after a short wait instead of
retrying for ever. The user chose this explicitly: "end the order after a
reasonable short wait". A goal behind a crowd, behind unexplored ground, or
one that opens during the wait is not finished this way.

**Strict 3.1 behavior.** An empty publication for a live order raises the
cannot-get-there bit `0x40` [04 R-PATH-01 §7]. `Move_Ground`'s phase 1 wakes on
its `0xE0` gate and returns code 9; as the last primary record it re-arms
phase 0 with a 30–59-tick deadline, which re-installs the point goal and
requests a new search [04 R-ORD-01 §4][04 §3.3]. Nothing records that the
previous search failed. `StrictRules.UnreachableMoves` answers `(0, 0)`;
Community inherits it and an unbound `System` answers the same.

**Why.** On a sealed goal the first search usually succeeds — to the wrong
place. The setup ray's wall follow circles the obstacle and keeps the lowest
heuristic it touched as the arrival tolerance [04 R-PATH-01 §5]
[04 R-PATH-01 §15], so the route ends at the tolerance frontier, the closest
point of the traced boundary. Once the unit stands there every later search
is rejected at setup without seeding and publishes nothing, and the retry
above repeats for the life of the order: the unit never goes idle, never
takes its next order from an idle refill, and in a group at the same wall
the members keep re-requesting and jostling (research-branch diagnosis:
nine setup rejections between ticks 210 and 619 for one flea; 150 searches
in 1,800 ticks for four fleas at one dead end).

**Modern behavior.** `ModernRules.UnreachableMoves` answers `(6, 90)`.

1. *Certification.* At the one site where an empty publication raises `0x40`
   on the live order, the movement system asks orders whether the record is
   eligible (`orders.Rules.UnreachableMoveArrival`,
   [DESIGN_UNITS_ORDERS_COB](DESIGN_UNITS_ORDERS_COB.md#modern-unreachable-moves)).
   For an eligible record it re-runs the same setup ray, read-only, from the
   unit's committed cell (`path.ProbeGoalSealed`). The probe reads a *static
   view* of the requester's class layer: every anchor the layer admits keeps
   its answer, so the view is never stricter than the search's own read;
   ground the owner has not mapped (nor learned, under
   [Modern learned terrain](#modern-learned-terrain)) keeps the search's
   optimistic value 2; and a blocked anchor on known ground is re-read from
   the terrain and feature chain and from building occupancy (an occupant
   with no mover) over its footprint, inside the restamp's edge bound
   [04 R-PATH-01 §2][04 R-PATH-01 §14][04 R-SLOPE-01 §3]. Every mobile
   occupant — ally or enemy, parked or moving — is transparent. The probe is
   *sealed* when the wall follow closes its loop (the cursors meet) or
   exhausts every sector without touching a goal cell, reaching its target or
   entering the goal's zero-heuristic band. A sealed probe records a
   certificate when the unit also stands within six cells per axis of the
   probe's frontier — the first touched cell with the lowest heuristic, which
   is where the first route led. A sealed unit farther away (held back by a
   crowd or still walking) keeps the retail retry and may still close in.
2. *Dwell.* The certificate belongs to the order record, not to an
   activation: the retry re-installs the goal under a new activation every
   30–59 ticks, and each later empty publication re-certifies while keeping
   the tick of the first certification. An unsealed or far probe clears the
   certificate, as does a published route for the order that ends in the
   goal's zero band (the search has just shown the goal reachable). The
   ordinary retry continues throughout.
3. *Closing probe and completion.* The ground follower asks, before the
   ordinary arrival step and only while some certificate is live, whether its
   head's certificate is due: it must name the head, the activation it was
   made for must still be bound, and 90 ticks must have passed since the
   first certification. The follower then probes once more from where the
   unit stands; if the goal is still sealed with the unit at the frontier and
   orders still admits the record, the move completes through the
   crowded-arrival path — phase 1, arrival bit armed, `raiseArrival` releases
   goal, route and pending search, and the ordinary pump emits `Arrived`,
   removes the move and refills idle
   ([Modern crowded arrival](#modern-crowded-arrival)). Otherwise the
   certificate is cleared and the retry goes on.

No new closest-point search runs: the unit stops where the rejected request
found it. Integer arithmetic only, no RNG, no map iteration; a large goal's
enumerated cells are sorted into a canonical order for membership lookup.

**Cost to the player.** A goal that is sealed when the order is judged and
opens more than 90 ticks later is not reached: the unit is idle at the wall
and must be ordered again (research-branch fixture `unreach-sealed-reopen`,
where a wreck closing the only gate is reclaimed at tick 450). A goal that
opens within the dwell is reached by the ordinary retry. Large groups at one
unreachable goal still leave some orders pending, because members more than
six cells from the frontier keep retrying; widening the radius completed
units mid-crowd, where they stood idle in other units' paths. The move
reports `Arrived`; there is no separate "cannot get there" acknowledgement.
About one unreachable goal in ten on random maps is not certified (for
example when the walk enters a large goal radius's zero band) and keeps the
retail retry.

**Measured effect.** Research-branch measurements (`proto/path-round3`,
`docs/PATH_PROTOTYPE_UNREACH.md`; opt-in path benchmark, GOMAXPROCS=2, three
repeats, medians) of the certificate with **immediate** completion, before the
dwell and closing probe were added. The dwell postpones each completion by 90
ticks, during which the ordinary retry and its searches continue, and adds one
closing probe per completion:

| Fixture | Pending moves at end | Searches | Setup+pops |
|---|---|---|---|
| `terrain-concave-sealed` / 1 | 1 → 0 | 11 → 2 | 818 → 179 |
| `shared-same-goal-deadend` / 4 | 4 → 0 | 150 → 13 | 14,296 → 1,486 |
| `unreach-goal-enclosed` / 4 | 4 → 0 | 79 → 28 | 16,768 → 14,447 |
| `unreach-fog-sealed` / 1 | 1 → 0 | 48 → 6 | 5,161 → 1,969 |
| `unreach-corner-group` / 64 | 39 → 2 | 926 → 313 | 74,290 → 48,362 |
| `naval-four-cell-cruiser` / 1 | 1 → 0 | 10 → 2 | 1,561 → 385 |

Reachable fixtures — winding, branching maze, concave, the three dynamic
wreck cases, the seven knowledge cases, capacity control and the shared-goal
maze — kept identical outcomes, search counts and work, and
`unreach-jammed-gate` (a goal reachable only through a gate two parked allies
fill) made 19 probes and no certificate. On the 1,500-unit scripted waves a
probe-only control ran 6,157 probes (348,428 ray steps) inside the host-drift
bracket of an unchanged Modern control (total 3,607 → 3,614 ms, p99
12,278 → 12,330 µs, allocation +0.04 MB). The randomized check below found no
sealed verdict for a reachable goal in 40,000 maps.

In tree, on the session fixture below a zero-dwell reference finishes the
flea's move at the wall at tick 144; Modern finishes it at tick 233, the first
follower visit after the dwell, and with the wreck reclaimed at tick 174
reaches the goal by tick 339.

**Boundaries.** Only a sole primary `Move_Ground` with no target, not produced
by automatic work and not a danger response or return, on a live, complete,
unstunned, uncarried ground mover; orders owns that list. A move with
successors already leaves the queue on its first failure through code 9;
patrol, guard, build and repair approaches, attacks, and aircraft keep their
own failure handling. The certificate is dense per-handle state on the
`System` (`unreachable`, with a live count that keeps the follower's question
off while it is zero) and is written only when the bound rules answer on, so
Strict never allocates it. It is cleared with the order binding
(`DeactivateMove`, hence `ForgetUnit` on death and slot reuse), on a restored
unit, when the follower finds a different head, and at the first follower
visit after a switch to Strict or Community. It is not saved: a load starts
with no certificate and the next cannot-get-there publication certifies
afresh, starting a new dwell. A certificate is not revalidated after the move
completes.

**Determinism and fingerprints.** Modern stays deterministic and bit-identical
across hosts. Completing a move removes a code-9 retry, whose 30–59-tick
deadline draws from the simulation stream, so any scene with a certified move
diverges from the previous Modern. None of the locked scenes contains one:
the Strict, Community and Modern `headless` fingerprint locks (the Ashap
Plateau battles and the benchmark fixture) are unchanged.

**Verification.** `path.TestProbeGoalSealedNeverSealsAReachableGoal` (40,000
pseudo-random maps against an exhaustive flood over the search's own step
rule: 9,299 sealed verdicts, none for any of 12,017 reachable goals),
`TestProbeGoalSealedSealsOnlyAClosedKnownRing` (a known closed ring seals, the
same ring unexplored or opened does not), `TestProbeGoalSealedReusesScratch`,
`TestRouteEndCellInvertsWorldPoint`; `movement.TestUnreachableMovesAnswers`
(Strict, Community and unbound answer off, touch no certificate state and the
dispatch does not allocate), `TestUnreachableCertificateLiveCount`,
`TestStaticPassableSeesThroughMobilesOnly`;
`orders.TestUnreachableMoveArrivalEligibilityAndStrictBypass` and the rule
dispatch allocation and Strict no-draw tests; and the retail-tier
`session.TestModernUnreachableMoveFinishesAfterDwell` on an authored wall and
gate closed by a wreck: Strict and Community retry to the end of the window,
Modern finishes at the wall no sooner than 90 ticks after the first
certificate, a gate reopened during the dwell is reached, and a switch to
Strict during the dwell keeps the retry.
