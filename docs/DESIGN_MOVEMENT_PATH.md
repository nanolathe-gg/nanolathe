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
[Modern group-order spreading](#modern-group-order-spreading),
[Modern unreachable moves](#modern-unreachable-moves),
[Modern wedge escape](#modern-wedge-escape) and
[Modern traffic](#modern-traffic), which does steer, smooth and re-plan, are
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
  object is currently bound. The order queue's removal paths reach the
  destructor through the binding's `Destroy` port and handlers reach the
  explicit release through `Release`. Wiring teardown to the explicit release
  let a displaced record's removal wake the bound record with the release bit
  (`session.TestQueueTeardownOfADisplacedRecordLeavesTheBoundGoal`).
  `ForgetUnit` destroys all retained objects in
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
increasing value the movement system binds to the installed payload owner,
which can differ from the queue head. A goal handoff invalidates that identity;
a queue rotation alone does not `[04 R-PATH-01 §8]`.

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
which binds it at composition and at the phase-1 command boundary; Strict 3.1
and Community bind `RetailKernel`, the search described above, Modern binds
`StraightenKernel` ([Modern route straightening](#modern-route-straightening)),
and a system with no kernel bound searches as retail does. The scheduler keeps admission
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
still consume visits. A new pool seeds the cursor at its first allocatable
slot and advances before inspection; polling and idle batching share that
initial position. Same-world binding retains the cursor. Production binds
the world before the battle-entry prime, whose scheduler visits persist into
the first ordinary tick even when no request is staged. Staged request data is lookup-only: the selected
follower's wants-repath flag, inclusive 60-tick throttle, committed start and
goal are read at a positive poll, which stamps the timestamp. Path derives
neither cursor nor eligibility from staged requests `[04 R-PATH-01 §6]`
`[04 R-MOV-01 §7]`. `SearchFunc` is the search boundary and reports the work it
actually did, setup-ray steps and heap pops, which is exactly what the scheduler
charges. `PublishFunc` is called only when the search is done, never at a budget
boundary. `Trace`, `RequestTrace`, `GoalTrace` and `SchedulerTraceState` are
value-only copies for diagnostics; no interface or pointer identity can reach a
deterministic report.

Under a large step allowance almost every poll is idle, so the scheduler's cost
is dominated by bookkeeping rather than search unless that bookkeeping is kept
proportional to the requests it finds. Whole rounds of idle polls are charged at
once (`skipIdleRounds`), and the round is abandoned as soon as the first member's
next poll is not idle, since every member's idle run bounds it. The provider
caches per-player eligibility for one scheduler call (`SetPathTick` fills it,
`EndPathTick` drops it): the player-record gate cannot change while paths are
being searched, and the admission loop asks it for every player on every
iteration. Both are pure reorganisations of queries — the charged work, the
admission order and every route are unchanged, and every fingerprint lock
holds. On the opt-in path benchmark's Community 3.9 scripted waves with 1,500
units (interleaved runs, four each) they cut simulation CPU 19% and the
tick p99 from 20.9 to 15.9 ms; the worst tick fell from 70 to 59 ms, which
remains the cost of the 3.9 step allowance's searches themselves.

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
[Modern group-order spreading](#modern-group-order-spreading),
[Modern bounded path work](#modern-bounded-path-work),
[Modern unreachable moves](#modern-unreachable-moves),
[Modern wedge escape](#modern-wedge-escape) and
[Modern traffic](#modern-traffic), and `LearnedTerrain`
is the per-owner grid the learned-terrain policy keeps on the `System`.
`OverlapRules` answers the policies Modern traffic retired, for the
pathfinding laboratory's baseline; no reserved rule set binds it.

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
Attached units retain their last-stamp sector reference but have no top-level
link. Rectangle overlap visits each selected parent followed by its cargo list.
`VisitUnitsInRadius` (`repair_scan.go`) excludes attached units before invoking
any visitor: repair patrol and mission radius conditions share the top-level
population, sector traversal and arithmetic of [04 R-COLL-01 §11] and
[04 R-ORD-02 §4]. Rectangle overlap and owner-slice queries keep their distinct
populations. This filter neither changes Modern traffic admission nor the
Community patrol work options.

**Remaining evidence gaps:** a reachable cached-mode-0/3 intruder restamp and
an accepted detach without an initialized retained filing remain unresolved
[04 R-COLL-01 §11]. Their producer histories must be established before
changing restamp plane dispatch or inventing a missing reference.

`PlaceUnit` is the direct
position commit — retail's carried-position setter, the commit's success branch
without the validator — used by the teleport row and by carried motion
`[04 R-COLL-01 §4]` `[04 R-SPEC-01 §2]`. `ForgetUnit` is the only lifecycle
cleanup: pool slots are reused by handle, so a new unit landing on a dead one's
slot must not inherit its footprint, profile, tier or occupancy.

### Canonical attachment filing correction contract

**Implemented attachment-index contract.** The synchronous projection follows
[04 R-COLL-01 §4, §4A, §11]. It adds no gameplay decision or rule set. Unit attachment lists remain authoritative; movement owns the spatial
projection and all cell writes.

**State and synchronous boundary.** Keep `SectorFiling` as the last stamp's
selected sector, including an explicit never-stamped state and the off-map
record. Keep actual top-level membership in the existing grid link row;
`Filed` must no longer imply `linked`. A retained reference survives attach,
transfer and movement while carried. A link sequence changes only on an
actual head insertion, not on a cargo-only reference update.

The cross-package boundary is one observer on `units.World`, bound
to its movement system: `AttachmentObserver` exposes
`AttachmentChanged(*Unit)`, with `World.SetAttachmentObserver` binding its sole
owner and `World.NotifyAttachmentChanged(*Unit)` notifying that owner.
`World.ClearAttachmentObserver(expected AttachmentObserver)` clears only the
expected pointer owner, so teardown cannot remove a replacement binding. No
second unit registry, per-unit callback or policy lookup is needed. The
observer reads the already-written carrier relationship. Accepted attach
unlinks the child from the top-level index without erasing its retained
reference. Accepted detach head-inserts the child into that retained sector,
even if the mode, cell and sector are unchanged. Call the observer after the
cargo-chain and carrier writes, before `writeRequestedMoverMode`; rejected
requests and an already-detached no-op never call it. Transfer and same-carrier
reattach retain the cargo list's existing head-insertion behavior and do not
create a transient world-bucket entry.

Bind the observer before the first battle-entry allocation/attachment in
session composition, and before recursive save restoration. `BindWorld`
refreshes the same owner's binding without replaying events or rebuilding
lists. Replacing a world must detach only this owner's prior binding; a nil
observer is an explicit uncomposed host context, not a retail lifecycle.
The observer must not run `EnsureUnit`, fabricate a retained sector from
current XYZ, clear/stamp cells, draw RNG, call COB or change mover modes.
An absent collision/filing record is an initialization prerequisite failure;
its runtime site needs `TODO(question)` rather than a guessed rehead target.

The order-side `dropFromCarrier` in `orders/vtolwork.go`
uses `DetachTakeoff func(*units.Unit) bool` on the existing
`MovementGoalAdapter`, composed to movement's shared mode-2 detach. This is a
mutation of movement state, so it does not belong on `WorldQueryAdapter` or
on a new rule interface. Keep the preamble's callback/order sequence; do not
fold its activation, goal or mode tests into the detach callback. An unbound
adapter cancels the carried work preamble through its existing incomplete-host
context result; this is host composition handling, not a retail detach rule.
Production composition binds the callback and has a focused regression.

**Stamp and overlap.** Run sector selection at every actual ordinary stamp
boundary, including modes with no cell plane and initialization of restored
movers. Keep footprint bounds, committed-position sector selection and the
setter's same-cell/same-mode early return in their existing order. A carried
stamp refreshes only the reference; an uncarried sector change unlinks and
head-inserts. In particular, same-cell XYZ movement never refreshes it merely
because XYZ crosses a world-sector boundary. Quantize a carried hang position
without adding the carrier's copied velocity again [04 R-COLL-01 §1]. The
old delayed cached-mode transition rehead is removed from `CommitSuccess`.
COB drop also leaves the stamp to the subsequent mover commit; its accepted
release only relinks and requests the mode [04 R-COB-03 §5].

The overlap query keeps its column-before-row sector walk. For each top-level
parent, test the parent's cached rectangle and then each cargo rectangle in
cargo-list order, independently of whether the parent intersected. The child's
retained sector does not select its reachability. Do not skip the cargo walk
when the parent is the clearing unit; the ordinary restamp flag gate handles
self visits. A small optional attachment view beside `OverlapPositions` and
`OverlapFilings` can expose the current carrier and borrowed cargo slice from
`System`; fixtures without attachment state retain their ordinary parent-only
walk. The existing reusable candidate buffer remains valid because these
restamp callbacks do not relink. Unfiled-fixture fallback must neither offer
attached children independently nor duplicate a parent/cargo visit.

Out-of-map stamps retain the separate off-map reference without writing
cells. A carried child has no off-map top-level link; detach head-inserts into
that retained record. Ordinary overlap and radius scans exclude the record.
Community's separately bound `VisitOffMapFiled` consumer retains its existing
feature gate and synchronous traversal semantics; it reads actual top-level
membership, never a synthesized list of cargo reference holders. This is an
index correction, not authority to change its damage policy. Modern traffic
admission and Community overlap arbitration remain at their existing seams.

**Lifecycle scope.** Factory allocation/completion, pickup, landing transfer,
unload, COB attach/drop, takeoff and death-cargo detach already reach
the shared movement helpers; the order preamble above is the additional
writer. Save restoration also uses `AttachCargoMode` and must preserve its
recursive allocation order, without replay at publication. Finalization already uses `ForgetUnit`. Never-created allocation rollback
releases construction placement cells and discards the existing movement record
and filing before returning the pool slot for reuse. Use the movement cleanup owner, not a detach notification that
would manufacture recency. Capture replaces the unit and leaves its old record
to ordinary death cleanup; it does not transfer the cargo list.
The direct orphan cleanup in carried movement and the units-only unwind need
explicit boundary tests: a missing carrier is not evidence for a retail
rehead target. No changes to owner-slice eligibility, mission adapter filters,
restamp plane dispatch or transport admission belong in this correction.

**Implementation ownership.** The lifecycle crosses these existing owners:

| Files | Responsibility |
|---|---|
| `internal/units/attachment.go` (new), `units.go` | Single world-owned observer field, setter and notification; no gameplay logic or index storage. |
| `internal/movement/collision.go`, `cargo.go`, `integrate.go`, `takeoff.go`, `retail_restore.go`, `forget.go` | Retained filing versus links, synchronous observer, all-mode stamp filing, direct hang quantization, cargo overlap order, initialization/restore/free cleanup. |
| `internal/orders/binding.go`, `vtolwork.go` | Add the existing adapter's detach callback and remove the direct live relationship writer. |
| `internal/session/composition.go` | Bind the index observer before allocation and the order detach callback; refresh idempotently. |
| `internal/construction/nanoframe.go` | Ensure rejected-allocation rollback forgets its movement state before pool reuse. |
| Focused tests in movement, units, orders, construction and session | Lifecycle boundaries below; update existing index-equivalence fixtures to include actual attachment events. |

`place.go` already guards its same-cell path before `syncMoverStamp` and needs
no second index implementation. Production save reference restoration already
uses the shared attachment helper; `session/retail_restore_core.go` needs a
composed regression, not a second attachment writer. `units/retail_restore.go`
contains a detached reference installer, which must remain explicitly outside
live relationship mutation unless a new production caller is introduced.
The existing `repair_scan.go` carrier filter and mission adapter may remain;
no consumer eligibility API changes are required.

**Verification.** Independently checked arithmetic/order
contracts are the setter's wrapped raw-coordinate footprint bias and cached
mode comparison, and the overlap walk's signed rectangle extents, half-open
intersection, column-major margin and parent-then-cargo sequence. Tests must
cover:

- Stationary mode-1 factory completion reheads immediately, before any mover
  visit; rejected/no-op detach does not. Attach, carrier transfer and
  same-carrier reattach produce no duplicate links and preserve cargo order.
- Ordinary mode-0 carried motion changes the retained sector without adding
  a top-level member; crossing off map and back behaves the same way.
- The two-cell X = 127 to 129 same-anchor example keeps its old reference;
  detach uses it. A one-cell hang at X = 127 with velocity +2 quantizes the
  hang itself, not a future proposal. These are authored consumer-boundary
  fixtures, not stock-animation assertions.
- A parent outside the query rectangle but inside the scanned sector span
  contributes intersecting cargo; a parent outside the span does not. Two
  mode-1 intruders contesting a released cell lock the first-writer result,
  with cargo order distinct from handle order and child-sector order.
- Radius queries remain top-level only, including the production
  factory/repair regression. Finalization, rollback and immediate handle
  reuse leave no old links. Rebinding and recursive save attachment neither
  duplicate links nor repeat head insertions. Existing Strict/Community/
  Modern policy contracts and the Community off-map consumer still pass.

`TODO(question)`: mode-0/3 restamp reachability remains separate. Restamp reads
the cached mirror, whereas attach writes requested mode; the carried setter
clears intruder state before changing the mirror. The remaining decider is a
complete cached-mode/intruder writer history, including admissible mobile
YARD_OPEN and save-loader post-load paths [04 R-COLL-01 §4]. Do not implement
that consumer from an assumed attach-only history.

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

Landing admission reads both the ground and air occupant words under every
footprint cell, ignoring the aircraft's own identity in either plane. The
mapping early accept remains before that walk `[04 R-AIR-01 §6a]`. Reading
only the ground word allowed overlapping aircraft to descend together. The
later touchdown was refused with grounded request mode and airborne accepted
mirror; a following construction patrol then skipped takeoff while the flight
integrator remained inactive. `landing_overlap_test.go` locks both-plane
admission, the mapping exception, and landing followed by construction patrol
for overlapping and separated aircraft in Strict and Modern.
The stock-asset counterpart, `TestRetailLandedAircraftResumePatrolAssistance`,
checks that each aircraft then contributes an actual nanoframe fraction
reduction through the composed construction service.

Air attack preparation uses the inhibit-all slot verb, not target binding.
The strafing and hover release phases and the bomber release phase bind only
slot zero; dogfight phase one inhibits all, then releases and binds slot zero.
The bomber break phase clears its primary target without changing control or
Aim state. These verbs pass through the existing combat adapter
`[04 R-AIR-01 §8]` `[06 §3.2]`.

Hover attack's first approach reads the current target position while retaining
its issued goal, so a queued attack follows a ground unit that moved during
the wait. Strafing and bombing retain their existing cached-goal paths. The
dogfight facing test extracts each negated component's signed whole part
before multiplying; on arrival near a sideways target this releases the
current marker and restarts pursuit instead of installing straight flight. The ordinary
evasion constructor clears its target, so that inserted record completes
before its random break leg runs. `air_target_input_test.go` checks the
queue/executor paths, markers, pursuit counter and bounded RNG use `[04 R-AIR-01 §8]`. Stock Brawler
and Rapier definitions admit the hover path, and stock fighter definitions
admit the dogfight path; occurrence frequency in stock battles is unmeasured.

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
order `[04 §10.2]`. Its final scalar gate accepts zero and unordered values;
direct boundary fixtures lock that consumer without claiming a naturally
generated NaN or a complete restore-to-command history. `legVTOLPickup` and
`legVTOLUnload` are the two air
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
latched and keeps taking slices while the combined call budget is positive,
charging its owner's accumulator even below zero. Positive owner credit is
required only when choosing a new request. Negative balances carry forward
and receive the next call's equal share. The scheduler runs once per tick,
before the per-player unit sweeps `[04 §7.3]` `[04 R-PATH-01 §6]`
`[04 R-PATH-01 §10]`.

The movement provider reads eligibility from the session player record at the
actual slot index; the participant count remains only the equal-share divisor.
Sparse restored slots are never compacted or represented by a count cutoff
`[04 R-PATH-01 §6]`.

The follower's last-request tick records an admitted scheduler poll. Goal
installation preserves that tick when under ten ticks old and clears it when
at least ten ticks old (before tick ten, always); activation and request staging never replace it with the current tick
`[04 R-PATH-01 §8]`. The scheduler alone applies the inclusive
`lastRequestTick + 60 <= tick` admission test and stamps a positive poll
`[04 R-MOV-01 §7]`; the 60 is `Rules.RepathDelay`, which Modern answers with
fifteen ([Modern prompt re-routing](#modern-prompt-re-routing)). This distinction matters for `RepairUnit`, which refreshes
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

The follower arms a retry for a bound goal only when blocked or when its
stored point count is below two `[04 R-MOV-03 §2]`. Route inactivity alone
does not arm it: an empty publication clears the retry flag while retaining
the old count and points `[04 §7.3]` C14. In particular, an unblocked mobile
builder may proceed from an in-range failure to work with two stale points
and an inactive route; work must not restart that route at the next poll
`[05 R-WORK-01 §14]`. Modern changes the delay through `Rules.RepathDelay`,
while retaining this arming predicate.
Waypoint pruning also reads the stored count regardless of inactivity and
precedes the arming test; reaching a stale point can reduce the count below
two and legitimately re-arm the follower `[04 R-MOV-03 §2]`.

**Measured follower correction (2026-09-30).** Both full path matrices passed
543 case/rule/size combinations with three deterministic timed repeats each.
Of 541 combinations with matching inputs, Strict and Community outcomes were
unchanged; 22 Modern outcomes changed, including four crowd cases with fewer
proximity hits. Some affected units stopped short and others remained pending;
the reports do not identify every completion branch. Aggregate median cost
was roughly unchanged, but Modern `traffic/choke_wide` at size 64 had 15 to 10
proximity hits and median thread CPU of 0.282 to 0.423 ms per tick, with noisy
candidate repeats. These are quality and local cost limits of the correction,
not evidence for an inactive-route retry condition. Two Modern wave cases
(sizes 256 and 1500) derive later single-unit goals from current positions;
their changed runtime inputs prevent a direct cost comparison. See
[PATH_BENCHMARK](PATH_BENCHMARK.md) for the reporting-radius and timing limits.

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

**Empty mobile occupancy.** The resolved mobile footprint retains authored
zero dimensions [04 R-P0-08-C]. Collision anchor bias, strict bounds, sector
filing, stamp, clear, restamp and carried-position updates use that exact
pair. An empty cell loop cannot observe a blocker or change either occupancy
plane; ordinary classless mover creation already retains the FBI scratch
profile. The commit bounds gate runs even when there is no covered cell to
invoke its per-cell callback. This closes the inert factory-product lifecycle;
it does not change path-layer classification or route generation for empty
footprints, nor admit malformed negative extents or empty buildings.

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

**Air-order distances.** Attack legs use `numeric.TruncatedDistance` with
wrapping signed raw deltas and a signed low-word result, then each caller's
comparison or signed halving `[04 R-AIR-01 §8]`. The dogfight intercept reads
the signed high word before its whole-unit threshold. The tests in
`retail_distance_test.go` preserve these boundaries and confirm the threshold
arm spends no RNG draw. Modern path and traffic rules are unchanged. The
Modern repair-pad queue ranks bases and threats by its own exact,
non-wrapping distance (`repairPlanarDistance`), not by this retail helper,
whose signed low word reads negative from 32,768 world units.

This O22 correction changed only the Strict pool-lock fingerprint of the time
(the seed-7 benchmark scene at step 4,500) from `8ea359e7670a471c` to
`7ff5d4238edd1720`. The dogfight no longer installs an intercept for distances
from 160 through just below 161 whole units. A test overlay restoring only the
old raw-distance comparison reproduced the old hash exactly; the other fifteen
locks remained unchanged. This explains a retail correction, not a Modern
policy departure. That scene then no longer filled its effect pool, so the pool
lock moved to seed 5 (DESIGN_MULTIPLAYER §16.1 M1-C9).

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

Pickup lowering installs the no-piece follow-piece marker: it follows the
cargo origin and requires matching headings. Its altitude offset is sampled
once from the carrier's oriented piece offset, without adding the carrier
origin [04 R-AIR-01 §9]. Selected landing pads instead resolve their mapped
piece hierarchy and target orientation on every goal update through the
existing COB `ComposePiece` surface. Plain follow and invalid piece indices
keep the target origin [04 R-AIR-01 §14.1][04 R-REV-02]. These shared geometry
rules leave Modern pad reservation and admission unchanged. The focused
transport audit fixtures cover callback/RNG preservation, heading arrival,
nonidentity piece mapping, live animation and a consumer-only tilted pose.

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

### Modern repair-pad queue

**Nanolathe Modern policy (user-authorized 2026-09-25).** Aircraft seeking
repair keep their landing order until a pad can take them. They reserve
individual landing pieces before approaching, circle the base on retail's
landing loiter when its pieces are occupied, and clear their piece after
repair. `movement.Rules`
owns the `RepairPadQueue` answer, selected by the existing session rule set:
Modern enables it; Strict 3.1, Community 3.9 and an unbound system bypass it.
The rule object remains stateless.

**Strict baseline.** The free-piece predicate counts attached cargo only.
Approaching aircraft can select the same piece. Losing it in the later
landing phases restarts the landing sequence; losing the target abandons it.
While no piece is free, phase 1 flies a loiter leg [04 R-AIR-01 §6]: a point
marker at the pad offset by the loiter bearing, at a radius equal to the
first weapon slot's range, with horizontal arrival radius 128 and no altitude
setter, so its height follows the cruise-altitude rule [04 R-AIR-01 §4]. The
record wakes on the marker's movement outcomes or target removal, never on a
timer, and each wake re-queries the pad and advances the bearing a quarter
turn. The bearing is the machine's only random draw, made once in phase 0.
In retail an unarmed slot holds the `[noweapon]` sentinel, whose range is 16
world units [06 R-WPN-05 §2][06 R-DMG-01 §5]; Nanolathe's first-weapon reader
answers 0 for that slot. Either way every loiter point of an unarmed aircraft
lies inside the arrival radius, over the pad itself. The seven phases, query
ordering and single takeoff bearing draw are established in
[04 R-AIR-01 §6]. Automatic repair seeking and its random base
selection remain [04 R-AIR-01 §11]. This policy does not change those callers
or their health threshold.

**Modern behavior.** Empty aircraft landing on a builder airbase join a FIFO
in the movement system on their first landing dispatch, with same-tick ties
resolved by the ordinary unit visit order. Completed, activated bases must
be owned by the aircraft or mutually allied, and not themselves carried.
The base's script supplies its actual landing-piece identifiers. Existing
reservations survive queries and marker replacement; free pieces are granted
to waiting requests in queue order, in authored query order. Duplicate piece
outputs never create extra capacity. Actual cargo occupancy always wins.
Approach, descent and attachment use the ordinary landing machine; loss of a
piece returns the aircraft to the queue rather than to its suspended order.

A waiting record stays at the queue head and flies the Strict baseline's
phase-1 loiter leg (issue 32, user-chosen 2026-09-28): the same marker,
arrival radius, gate and quarter-turn bearing step, from the bearing phase 0
drew, about the base (or its last known position). A new leg starts when the
aircraft begins waiting or its base changes, and on retail's wake set — a
marker outcome or target removal — so arrival alone turns the circuit and the
aircraft never stops at a corner. The queue adds one thing to the gate: a
claim retry every 30 ticks, which only asks for a piece again and leaves the
leg and bearing untouched, so a granted piece is taken within 30 ticks rather
than at the next corner. Nothing else writes the record's parameters; the
second parameter stays unused.

The loiter radius is the first weapon slot's range, as in retail, but never
less than 192 world units. The floor is the Modern departure from retail's
leg: an unarmed aircraft's range puts every loiter point inside the 128-unit
arrival radius, over the landing pieces, so it hangs over the pad. Measured on
Ashap Plateau with stock scouts (`ARMPEEP`) queued at one pad, the unfloored
leg left them hovering 54–90% of their wait directly above the pad centre; with
the floor they never stopped and, once on the circuit, stayed at least 124
world units from it. 192 is the radius the queue's stations already used: its
quarter-turn chord, about 271 world units, exceeds the arrival radius, so
every leg is real travel. Every armed stock aircraft has a first-weapon range
of at least 300, so the floor changes only unarmed ones.

Waiting aircraft no longer hold fixed stations: they circle regardless of
visible threats and may share a circuit, since aircraft in flight do not block
one another. The threat-scored station search now chooses only the repaired
aircraft's departure point (below). There it considers sixteen
cardinal/diagonal stations in deterministic order, starting 192 world units
from the base with 64-unit rings, which must fit the aircraft's full footprint
inside the map and be clear on the air occupancy plane. Among feasible
stations it prefers those beyond the longest weapon range of every currently
visible armed hostile, then the greatest minimum clearance from those ranges.
Hidden enemy positions are never read for scoring. With no waiter holding a
station, the old separation term has nothing to separate and is gone. If every
station is blocked the aircraft departs to its current position. These
distances are Modern tuning, not retail facts. This is conservative local
avoidance, not a promise of safety at a besieged base or a global flight-path
search.

Boundaries. The circuit is retail's, so its size follows the weapon: 370
world units for a Brawler, 510 for a Freedom Fighter, 1,280 for a bomber. A
circuit near the map edge leaves the map and comes back, as retail's does.
A wider circuit means a longer approach once a piece is granted. With seven
aircraft queued at one pad on Ashap Plateau, each patient took 13–43% longer
to reach the pad than with fixed stations for Brawlers and Freedom Fighters.
With four Thunder bombers it took about 1.7 times as long: they left the
circuit 900–1,200 world units out rather than about 150. Service stays
strictly first come, first served.

If a base dies, is carried, changes allegiance or becomes unavailable, choose
the nearest eligible live base, with the ordinary unit order breaking equal
distances. Full bases remain eligible: fullness never causes queue hopping.
With no eligible base, keep the landing and wait around its last known
location until another base becomes available or the player cancels.

Touchdown still creates the ordinary `SelfRepair` on the patient and bills
the pad through the existing construction/economy admission
[05 R-WORK-01 §3]. Waiting requests heal nothing and request no resources.
For a damaged patient the landing also inserts an ordinary `VTOL_Move`
behind `SelfRepair`, ahead of suspended orders, to leave the piece for the
departure point after repair. This frees the pad even when no subsequent
order existed. A healthy explicit parking command stays parked; loaded
transports and non-repair landing targets retain their existing behavior.
The queue, loiter, departure scoring and reassignment make no direct RNG
draws; the ordinary phase-0 bearing draw and authored script execution remain
intact.

**Lifetime and persistence.** Entries belong to one movement system and bind
exact unit, order and pad identities. Canceling/replacing the order or freeing
the aircraft releases its reservation. A temporary record ahead of the same
landing keeps its reservation, because the retained landing marker can still
steer the aircraft. Target removal invalidates the pad identity before handle
reuse. Switching to Strict/Community clears bookkeeping at the next tick and
runs their ordinary landing sequence; switching back re-admits live requests.
There is no migration that undoes earlier Modern movement or issued moves.

Reservations are transient and introduce no save schema. The ordinary landing
phase, marker, bearing, gate, retry deadline, repair and departure records
already persist. After load, live landings rejoin in normal dispatch order and
reacquire pieces before a fresh approach, including saved descent/touchdown
phases. A waiter restored in phase 1 that still owns its bound marker keeps
flying that leg until it arrives, so a load neither skips nor repeats a corner;
a waiter re-admitted after a switch from Strict keeps its retail leg the same
way. The pre-save FIFO order is not retained. No pad script query runs during
restore.

**Verification.** `movement.TestModernRepairQueueReservesAuthoredPiecesInOrder`
locks exclusivity, FIFO, the unarmed loiter floor, no healing while waiting
and RNG; `TestModernRepairWaitersFlyTheRetailLoiter` locks the retail leg's
geometry, the arrival-only quarter turn, a claim retry that keeps the leg,
the armed radius and zero draws beyond phase 0's;
`TestModernRepairLoiterSurvivesSaveRestore` saves a waiter mid-circuit and
checks that the restored leg, bearing and gate continue without a draw;
`TestRepairLoiterStrictBypass` keeps Strict, Community and an unbound system
on the unfloored retail leg with no claim retry; and
`TestRepairQueueStrictBypassAndSwitches` locks the retail collision and draw
behavior and both switch directions. The queue lifecycle, departure
visible-threat and restore tests cover cancellation, suspension, identity
reuse, lost bases, occupied departure stations and re-admission before
touchdown.
`airdiag.TestModernAircraftRepairQueueDrains` runs five retail aircraft through
one retail pad with real flight, COB, resource admission and departure.
Run the movement/order/airdiag contracts, both repository gates and the
displayless simulation-cost benchmark. Ground path search and its policies
are unchanged; this queue never submits a path request.

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

### 3.4.2 Modern aircraft tactics — design proposal for issue 101

**Status: design first, requested 2026-10-08; implementation and tuning pending.**
The goal is useful firing opportunities with less time in avoidable danger.
This proposal changes decisions about flight goals, not unit speed, turning,
weapon reload, burst count, damage, ammunition or resource costs. It is a
proposed **Nanolathe Modern policy**, disabled by Strict 3.1 and Community 3.9.
The already approved bomber pass completion and repair-pad queue remain their
own contracts. No new aircraft behavior is enabled by this section.

**Baseline and falsification.** Retail has separate bombing, strafing, hover
and dogfight executors [04 R-AIR-01 §8]. Strafers already alternate attack and
break legs; hover attackers already alternate standoff points; dogfight has
intercept and give-up transitions. The traced ordinary dogfight give-up does
not retain a target for its inserted evasion record, so that record ends before
its sideways break phases. This is an established producer/consumer result,
not evidence that every retail aircraft never evades. Issue 101's bomber miss
also reproduced in the reporter's OTA test. These facts falsify a blanket
parity-bug classification; they do not make the requested improvement
unnecessary. Implementation starts with the real-session `internal/airdiag`
harness and role-specific observations, so an aiming, projectile, content or
order wiring defect is corrected at its owner before adding tactics. Retail
physics remain described by [04 §10.1], [04 R-AIR-01 §2–§4] and the weapon
contracts in DESIGN_WEAPONS_PROJECTILES. Zero-gravity cancellation remains
SC23 until a separate policy is approved.

**Orders and knowledge.** An explicit attack commits to attacking its issued
unit or position; tactics may change the approach and recovery, and interrupt
briefly for evasion, but may not silently retarget or abandon it. Explicit move,
transport, construction and pad landing keep their assignment. Autonomous
engagements retain the existing Hold Fire and movement-stance admission and
return contracts. Hold Position does not acquire a roaming attack; Maneuver
returns to its existing anchor; Roam permits continued pursuit. Stance does
not grant hidden information. Approach scoring may read the existing
owner-visible danger contacts and anonymous impact cues described in
DESIGN_UNITS_ORDERS_COB "Modern danger response". A radar-only contact is not
a precise firing solution. Hidden AA positions, reload timers, projectile
targets and future paths are not inputs. A later pre-impact projectile dodge
needs a separately documented observation port; current impact cues support
reacting to fire already received, not knowing an unseen incoming missile.

**Proposed role decisions.** Select roles from the existing command resolver
and authored capabilities, never unit names:

| Executor | Modern objective | Proposed change |
|---|---|---|
| `AirStrike` | Release a useful bomb pass and get clear | Choose an approach aligned with the existing release calculation and actual flight state; prefer the less exposed approach when both admit a pass. Finish admitted burst release and overflight, then choose a shorter feasible recovery to the next pass. |
| `AirToGround` | Keep a useful firing window while passing | Bind the target early enough for ordinary aiming, preserve the existing weapon gates, and choose recovery from actual turn/speed constraints rather than an unnecessarily long fixed excursion. |
| `AirToGroundHover` | Maintain aim from useful standoff positions | Rank feasible standoff points by weapon admission and visible exposure; use deterministic lateral motion when under fire, while maintaining authored tolerance and target-facing requirements. |
| `AirToAir` | Intercept without a repeated dead give-up loop | Derive the intercept from observed position/velocity and feasible turn time. Insert an actual bounded evasion leg when pursuit geometry fails or received fire warrants a break, retaining the original attack for resumption. |

"Attack faster" means reducing unproductive approach/recovery time and target
binding delay. It never means firing through a denied aiming gate, spending a
burst twice, shortening reload or accelerating a unit beyond its definition.
Bomb release changes need traces of the launch point, aircraft velocity,
projectile gravity, impact point and damage outcome for moving and stationary
targets. Hit rate alone cannot distinguish a release bug from the authored
accuracy and ordinary misses. Recovery must let the admitted burst finish or
use the ordinary cancellation semantics; changing goals cannot restart it.

**Candidate search and evasion.** Use a fixed, bounded candidate sequence,
integer/fixed-point scoring and stable identity ties. Candidate courses must
be reachable under current velocity, authored turn rate, acceleration and
altitude constraints; an aircraft cannot teleport, stop instantly or make a
right-angle turn by changing its marker. Rank weapon-admitted courses first,
then visible exposure and time to the next admitted firing window. Received
fire may select a short lateral break with hysteresis to avoid changing sides
every tick. The break's lifetime, observation expiry, prediction horizon,
candidate count and evaluation cadence are **open tuning decisions**, to be
settled by the authored scenarios below and reviewed before implementation.
No default constants or projectile-specific dodge guarantees are approved
here. A poor forecast may still lose an aircraft to homing missiles, area
damage or dense AA; the policy provides feasible choices, not immunity.

**Composition and state.** The air executors live in orders, so extend their
existing `orders.Rules` seam with a bounded air-tactics decision there, through
the current `session.RuleSet`. Both reserved bypass implementations preserve
the current handlers and RNG order. Movement continues owning goal updates,
flight integration, occupancy and the repair-pad queue; combat continues owning
aiming, projectile admission and resource/reload effects. Add a movement seam
only if an identified integrator decision cannot be expressed by ordinary goal
installation, and justify it under DESIGN_GAMEPLAY_RULES §9. Do not introduce
a flight controller registry, per-unit random generator, background planner or
new gameplay booleans. Any tactical state belongs to the session/order owner,
has explicit cancellation, mode-switch and save/load rules, and makes no direct
RNG draws. Ordinary newly reached flight and weapon branches may naturally
change Modern draw order and resource use; Strict fingerprints must remain.

**Delivery and acceptance.** First capture baseline traces for each executor
with stationary and moving targets, low/high turn rates, long/short bursts,
out-of-range or unaffordable weapons and targets removed mid-pass. Then
implement approach/recovery separately from evasion, so each change has an
independent cause and measurable result. Use authored scenarios for a visible
AA screen, no visible AA, unseen fire, homing and unguided projectiles,
crossfire, map edges, terrain altitude, repeated explicit orders, all fire and
move stances, and return to a patrol/guard/repair assignment. Compare launch
opportunities, first-shot latency, cycle time, hits, time in visible firing
range and losses over identical seeds; also compare target-binding and burst
traces. Require repeated deterministic hashes, stable bounded work, unchanged
Strict/Community decisions and RNG, ordinary resource costs, and no mode-switch
or save/load loss of an explicit assignment. Run the applicable focused tests,
`tools/check`, `tools/check-retail`, the air diagnostic scenarios and
`tools/sim-bench`. Inspect flight captures for implausible steering as well as
the measurements. A throughput gain that comes from suppressing legitimate
weapon or movement work does not satisfy this design.

### 3.5 Goal-family wiring — `[OW-3-P]`

`[OW-3-P]` is the seam between an order and a path goal: which order produces
which goal family, with which radii, and which families have no producer. The
rule that governs it is that this package supplies *structure* and never a
radius. Every radius is the handler's own, computed from authored data and
installed through the record's payload installer; the wiring below consults the
bound payload first and only falls back to a family when a record's payload is
not bound during save reconstruction or a direct call without an installed
object. Battle queue transitions do not reactivate a retained successor's
object `[04 §7.2]` `[04 §7.4]` `[04 §3.5]`.

Ground installers apply cancellation, route acceptance and synthetic fallback
during each handler visit, using the queue's authoritative current tick. Several
installs in one pump pass each see the preceding install's mutations. Ground
service, arrival, scheduler polling and publication follow the bound object
rather than the primary head; session reconciliation preserves that binding.
Construction approach maintenance checks retained record ownership before any
new installation, so it cannot revive a displaced object when its record is
exposed again. Fresh approaches still install, and Modern site-clearance moves
keep their existing phase-owned installs.
A null handoff keeps the points/count but clears active/repath, applies the
unsigned inclusive ten-tick request-age reset and marks the route dirty
[04 R-PATH-01 §8]. `ground_handoff_parity_test.go` checks all three goal
families, intermediate handoffs, completion-flag scope, age/wrap boundaries,
bound-owner outcomes and stale publication. The session binding test checks a
goal-less gated head ahead of the owner.

Factory `QMove` and `QPatrol` records are rally markers with a 60-tick delayed
rotate, not movement goals. Session activation excludes them; `GetBuilt`
resolves their move/patrol operations against each product `[04 R-ORD-01 §2]`
`[04 §3.8]`. Arrival-handle binding never clears an order's dynamic gate or
deadline, including at phase 0: the constructor already starts ungated, and an
armed wait belongs to the handler or pump `[04 §3.3]` `[04 R-ORD-01 §1]`.
Clearing that wait after production cancellation made the rally queue rotate
every tick and its displayed connectors jump. The factory-cancellation
regression checks the committed queue and overlay across repeated deadline
cycles in all three reserved modes.

| Order | Family | Radii |
|---|---|---|
| `Move_Ground` | point at the order's goal | first general parameter plus 4; ordinary mouse move uses 4 `[04 R-ORD-01 §4]` |
| Patrol legs and everything not listed below | point, radius 0 | none `[04 §7.2]` |
| `Attack_Chase` | whatever its own maneuver phase installed — a point goal for five of the six live substates, an annulus for the other two | every radius sized from the slot's weapon range: `d`, `d/2`, `trunc(d/4)`, `0`, annulus `(d, d/2)`, annulus `(2d, d)` `[04 R-ORD-01 §3]` `[06 R-WPN-05 §1]` |
| `Follow_Ground` (the ground guard's follow) | point at the ward's position plus the record's stored anchor offset | arrival radius is half the handler's `(FootPrintX(me) + FootPrintX(ward) + 2) · 16` `[04 R-ORD-01 §8]` |
| `HelpBuild` phase 0 | annulus at the target | outer `builddistance + half`, inner `half`, where `half` is the assist approach term over the **target's** footprint and `builddistance` is the builder's own `[04 R-ORD-01 §12]` `[05 R-WORK-01 §2]` |
| `RepairUnit` phase 1, `Capture` phase 0 | rectangle perimeter from the target's committed anchor cell and footprint | grown by the mover's own footprint `[04 R-PATH-01 §12]` |
| `Reclaim` phase 0, `Resurrect` phase 0 | rectangle perimeter from the **feature's** origin cell and size | the anchor already is the origin: a feature's stamp writes its index on the anchor and the fringe sentinel across the rest `[04 R-ORD-01 §5]` `[05 R-ECO-02 §2]` `[05 R-FEAT-01 §3]` |
| `MobileBuild` approach | rectangle perimeter at the product's anchor cell and footprint | no candidate generator, no range filter and no sort: the candidate set is the search's own border enumeration and the selection is the border cell it closes first `[04 R-PATH-01 §12]` `[04 R-PATH-01 §13]` `[04 R-MOV-03 §9]`; installing it runs the goal installer's acceptance rule like every other ground goal, so the builder walks the synthetic line at the rectangle's goal point while the search runs, and a last-request tick at least ten ticks old is zeroed `[04 R-PATH-01 §8]` |
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
* **Air-goal chaining for patrol.** Patrol legs — `Patrol`,
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
allocates a class, with every fingerprint unchanged.)

A layer is built for each class a registered ground mover (movement byte 1,
not an aircraft) carries, not at the class's first path request; a
structure never searches and gets none: `EnsureUnit` queues the unit and
`BuildPendingLayers` builds any missing layer at the end of battle
composition and at the next tick start. Retail creates every class record
with the map `[04 R-PATH-01 §14]`; building early changes no layer value,
because every occupancy writer maintains the allocated layers and a fresh
layer's zero watermark keeps the occupant-age gate closed until its first
revision. Every Strict, Community and Modern fingerprint is unchanged. A
lazy build ran the full-map stamp inside the first search of its class, on
the same tick as that search burst: in the simulation benchmark's battle the
slowest early tick built two layers atop a burst of first requests.

Each full rebuild classifies each source cell once
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

The same read-only `DangerRouteFeasible` query admits local destinations for
[Modern firing positions](DESIGN_UNITS_ORDERS_COB.md#modern-firing-positions).
That order policy chooses the candidate and retains its attack node; the normal
point-goal payload, scheduler and traffic rules execute the accepted movement.
It adds no second path search or movement rule selector.

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
instead of waiting out the throttle, was considered and not adopted: it would
be a second departure, and the measurement above bounds what it could recover
at the blocked share of the trip. This policy never shortens the throttle.
The throttle itself is the separate
[Modern prompt re-routing](#modern-prompt-re-routing)'s, fifteen ticks since
2026-09-29; the measurements in this section were made under sixty.

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
`path.Kernel` still opens a retail search over the same passability port,
and what changes is one input to that port
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

**Retired from Modern on 2026-09-29.**
[Modern prompt re-routing](#modern-prompt-re-routing) replaced it: Modern's
throttle is fifteen ticks for every unit. The stagger is kept as
`OverlapRules.RepathDelay` for the pathfinding laboratory's baseline, and no
reserved rule set answers it. The text below describes the policy as it
stood; where it says Modern, read the baseline.

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
ten, still falling (`TestRepathStaggerSeparatesACohort`); Strict keeps all
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
admits at `last+60`), `TestStaggeredRepathDelayBoundsAndDeterminism`,
`TestRepathStaggerSeparatesACohort`, `TestModernPollHonoursTheRepathDelay`
(refused one tick before the delay, admitted and stamped on it) and
`TestRepathDueDoesNotAllocate`; the Strict, Community and Modern
`headless` fingerprint locks cover RNG and resource effects over whole
battles.

### Modern group-order spreading

**Nanolathe Modern policy.** When one player's order gives sixteen or more
units their first route request on the same scheduler tick, the requests are
admitted over three consecutive ticks instead of one: nearest goal first, cut
so that each tick carries about a third of the group's summed goal distance.
It is a load-balancing policy, built to complete
[re-route staggering](#modern-re-route-staggering), since retired, which
spread re-requests and deliberately left the first request alone. It changes only the tick a request is admitted on, never the request,
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

**Composition with the re-route throttle.** A held request is admitted on its
hold tick and stamped with it; from then on the follower is under the
ordinary throttle, so its next re-request is `RepathDelay` after the held
admission: fifteen ticks under
[Modern prompt re-routing](#modern-prompt-re-routing), and 60–67 under the
stagger this policy was measured with. Both share `repathDue`, so staging and
admission agree.

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
   restarts at the next call. Idle-run batching stops at the full sweep;
   the ordinary admission loop performs the next poll, which proves the
   cutoff, and credits the remainder before advancing to the next player.
   Batching must preserve the cursors and later admission order of individual
   polling.

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
the sweep), `TestSchedulerIdleBatchingMatchesSinglePolls` (bounded and
unbounded polling preserve searches, publications, cursors and accounting
through replenishment),
`movement.TestPathProviderBoundedIdleBatchingMatchesSinglePolls` (an idle call
leaves the same physical-slot cursors and next-call admission order),
`movement.TestPathWorkBoundAnswers` (Strict, Community, unbound,
Modern; the answer does not allocate); the Strict, Community and Modern
`headless` fingerprint locks cover RNG and resource effects over whole battles.

### Modern allied pass-through

**Retired from Modern on 2026-09-29.** [Modern traffic](#modern-traffic)
replaced it: friendly units never share cells, and two movers that meet
head-on steer past each other. The policy is kept as
`OverlapRules.AlliedPassThrough` for the pathfinding laboratory's baseline,
and no reserved rule set answers it. The text below describes the policy as
it stood; where it says Modern, read the baseline.

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
reject; the baseline passes same-owner and mutually allied head-on movers and
commits into the passed cell; an unallied owner, same-direction traffic, a
routeless blocker and a mover near its route end are rejected by that
blocker), `TestAlliedPassThroughAnswers` (every reserved rule set answers off
and the baseline on; a Strict tick never resolves the alliance query).

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

### Modern jam release

**Retired from Modern on 2026-09-29.** [Modern traffic](#modern-traffic)
replaced it: friendly units never share cells, a held mover steers round
what holds it and asks for a route again after half a second, and a unit its
friends have boxed in is given a
[route through them](#modern-routes-through-friends) that it follows as far
as they let it. The policy is kept as `OverlapRules.JamRelease` for the
pathfinding laboratory's baseline, and no reserved rule set answers it. A
release after five seconds as a last resort was measured beside Modern
traffic and left out. The text below describes the policy as it stood; where
it says Modern, read the baseline.

**Nanolathe Modern policy.** A ground mover that friendly units have held in
place for one second stops colliding with friendly ground units for three
seconds, and its searches during that window look through every mobile unit.
A friendly jam — two columns wedged in a choke, a unit walled in by parked
friends, a crowd around a factory exit — therefore always drains. Terrain,
features, structures and enemies block exactly as before, and a friendly unit
moving the same way ahead is still a queue to wait in.

**Strict 3.1 behavior.** The commit validator rejects a proposal whose
footprint holds any other occupant; the blocked mover halves its speed, clamps
against its old footprint and proposes the same step next tick
`[04 R-COLL-01 §1]` `[04 R-COLL-01 §2]`. Only the 60-tick repath and the
occupant-age gate change anything: a unit a stationary friendly group has
surrounded searches a layer in which those friends are walls and publishes
nothing, and two columns wedged at an angle in an aperture stay wedged.
[Allied pass-through](#modern-allied-pass-through) resolves only the head-on
case between two routed movers. `StrictRules.JamRelease` answers `(0, 0)`;
Community inherits it.

**Modern behavior.** `ModernRules.JamRelease` answers `(30, 90)`.

1. *Jammed ticks.* After each ground commit of a unit with a movement head,
   the movement system counts the tick as jammed when the commit was rejected
   by a friendly ground unit (`System.friendlyMover`: live, grounded,
   uncarried, not a structure, of the mover's owner or a mutually allied
   owner, through the same per-tick alliance query as allied pass-through)
   that is not a *same-way mover* — a unit with an active route heading
   within 0x2AAA (about 60°) of the mover — or that is one the mover already
   overlaps: a pair wedged into each other would otherwise each wait behind
   the other for ever. A rejected unit with no route counts the tick as
   jammed when a friendly ground unit holds a cell of or around its
   footprint: its search failed because friends walled it in. A unit with no
   route that stands inside a friend counts every tick as jammed even though
   its commit reads no rejection — it proposes no step, so it never
   revalidates, and only a release's search plans it out. Any other tick
   resets the count.
2. *Release.* On the thirtieth consecutive jammed tick the unit is released
   for 90 ticks, unless its previous release ended fewer than 60 ticks ago.
   Within 128 world units of its movement goal or its route's final point
   (or with neither), a release starts only when this tick's commit was
   rejected by a friendly mover that is not a same-way mover and no parked
   friend (a friendly ground unit with no active route) holds the unit's goal
   footprint — that destination is [crowded arrival](#modern-crowded-arrival)'s
   to finish — or when the unit already stands inside a friend; the end rule below then closes it at
   the first commit clear of every friend, so near the destination a release
   lasts only while the unit passes through the friend in its way. This is
   what frees two units that wedge each other beside their goals at the edge
   of a packed formation. The release raises the route's repath request and
   clears its request tick, so the next search is admitted promptly. The
   count restarts when the unit's order ends.

   *Work sites* (issue #31). For a ground work approach — a `MobileBuild`,
   `HelpBuild`, `Reclaim`, `ReclaimUnit`, `Resurrect`, `RepairUnit` or
   `Capture` head whose annulus or rectangle payload is bound — "the goal
   footprint" is the goal's whole stand region, not the footprint at the
   goal's anchor point, which for these goals is the target's own centre or
   anchor cell, where no helper stands (`System.workStandHeld`). A
   rectangle's region is every cell of the grown rectangle, border and
   interior, so a friend of any footprint standing flush against the target
   holds one of its cells. An annulus's region is every cell of
   `path.StandBounds` on which its heuristic reads zero or its start predicate
   accepts: the follower halts an arriving assister on the first committed
   anchor the start predicate accepts `[04 R-MOV-03 §2]`, and because the two
   bands use different stored radii that anchor can lie a cell outside the
   zero band `[04 R-PATH-01 §9]`. At such a held site the inside-a-friend
   start counts only a parked friend (`System.wedgedInFriend`): an arriving
   builder that an allied pass-through leaves inside a *moving* friend while
   it waits behind a worker is not wedged — the pass-through separates by
   itself — and a release started then would carry it straight into the
   worker. A builder wedged inside a parked friend is still released.
   A builder already working at the site is therefore never passed through by
   a second one arriving. The jammed builder keeps ordinary collision and
   repath: it reaches a free stand cell, or on an empty route its row's own
   failure arm decides — the mobile build's reach test `[05 R-WORK-01 §12]`,
   the assist's cannot-get-there `[04 R-ORD-01 §5]`. An attack chase's band
   and a park rectangle keep the point test; neither is a work site. The scan
   covers only the goal's own rectangle — for an assist band the square of
   its arrival radius, `2·(outer/16) + 1` cells wide — runs only on a jammed
   tick near the destination, reads one occupancy word a cell and allocates
   nothing.
3. *During the release.* In the ground commit's per-cell occupant test, a
   cell held by a friendly ground unit is free when either unit is released
   and the occupant is not a same-way mover, or is one the mover already
   overlaps. Searches opened for a released
   unit read the static view of its class layer — the view the
   [unreachable-move](#modern-unreachable-moves) probe reads, in which
   structures, terrain, features and unexplored ground keep their answers —
   with only friendly mobile units transparent: a mover of neither the unit's
   owner nor a mutually allied owner still walls the anchors it holds
   (`ClassLayer.staticPassableKeeping`), so a release never routes a unit
   into enemies it cannot pass.
4. *End.* The release ends at the first commit within 128 world units of
   the movement goal or the route's final point that leaves the unit's
   footprint clear of every friendly unit, or when its 90 ticks pass. While
   the unit still stands inside a friend (`System.insideFriend`: a friendly
   ground unit holds a cell of its committed footprint) the window is held
   open a tick at a time, to at most 180 ticks after it started: an overlap
   that outlived the release would leave the friend blocking every later
   step. After the window closes the route is asked to re-plan, since the
   one planned over the static view leads into friends that block again.

The stamp's existing overlap arbitration owns any contested cell once the
mover commits (ClaimConflict, `[04 R-COLL-01 §4]`); released units separate
through the ordinary occupant test once their windows close. Integer
arithmetic only, no RNG, no map iteration.

**Cost to the player.** Friendly units visibly overlap while one is released.
A unit may leave a queue that Strict would hold, when the blocker ahead is
parked or heading across it. Three variants were measured and rejected on research
branch `research/path-round4`: releasing past every friendly blocker
regardless of heading let same-way columns overlap through single-file chokes
and into their destinations (one-cell choke 16 → 11 arrivals, clutter 17 →
13); a back-off that steered a jammed unit sideways lost arrivals overall
(920 → 883); and letting a blocked mover pass a waiting friend changed
nothing (960 → 959).

**Measured effect.** Opt-in path benchmark on research branch
`research/path-round5`, one deterministic repeat of every case and size, with
the retail search kernel, this contract against the same tree with jam
release answering `(0, 0)` (artifacts kept in `~/nanolathe-bench/2026-09-23`,
directories `final` and `prod2`): near-goal arrivals 4,001 → 4,145 of 5,756
and pending moves 1,652 → 1,509 over the corpus without the scripted waves;
on the scripted waves 164 → 282 of 2,304 with 256 units across three owners
and 600 → 833 of 13,500 with 1,500. Twenty-two cases gain, the largest being
the friendly four-cell choke with 64 units (2 → 54), opposed columns through a
choke two flea footprints wide (33 → 57), the one-footprint corridor with passing bays
(0 → 16 of 16) and perpendicular flows (52 → 64). Six lose: the hostile
head-on meeting with 64 units a side 25 → 22 (its friends crowd the front
rather than waiting behind it), the 64-unit scripted waves 71 → 68, and four
cases by one arrival. Tick cost
did not rise: on interleaved repeats of the 1,500-unit waves the CPU p95 and
p99 stayed within the host's noise. A released unit's re-plan is an
ordinary search; the release changes which searches run, not their kernel.

The contract's history, from the round-four prototypes (research doc
`docs/PATH_PROTOTYPES_ROUND4.md` on `research/path-round4`) to this form: the
movement-goal guard keeps a stranded unit beside a crowded goal from pushing
through the crowd (`session.TestRetailCapturedCrowdedRallyCompletesOnlyInModern`);
holding the window open while the unit stands inside a friend removed a
permanent block behind a wide parked friend near a goal (review finding;
waves 247 → 293 at 256 and 759 → 886 at 1,500); keeping hostile movers as
walls in the released search recovered the hostile head-on case from 17 to
22; and letting a release start near the destination to pass one blocking
friend — unless a parked friend holds the goal, which crowded arrival
finishes — freed the units that wedged each other at the edge of packed
formations (arrivals 4,108 → 4,145; a 256-unit formation whose last columns
stranded 18 units after 1,200 ticks now strands 5). On its own that start
lowered the 1,500-unit waves from 886 to 833; with
[route straightening](#modern-route-straightening) they reach 1,070.
Treating a same-way friend the unit already overlaps, and a route-less unit
standing inside a friend, as wedged rather than queued freed the pairs that
stayed overlapped mid-route (opt-in path benchmark, whole corpus against the
contract without it: arrivals 4,167 → 4,189, pending 1,486 → 1,465; scripted
waves 68 → 97 at 64 units and 282 → 293 at 256, 1,070 → 1,058 at 1,500;
opposed columns through the two-flea choke 57 → 64, the one-cell choke 13 → 16;
units left overlapping and blocked at the end of a window 6 → 3 pairs). The
near-destination start adds a few overlaps where an order finishes
mid-pass: units left overlapping at the end of a fixture's window rose from
16 to 19 pairs across the corpus without the 1,500-unit waves.

The work-site rule was measured on 136 authored retail scenes with a probe
that was not kept: flat terrain, a construction kbot (`armck` or `armack`)
building an `armsolar`, `armllt`, `armvp` or `armmakr` while four, eight or
twelve more assist it, arriving in six layouts, and every builder handed a
build of its own on the tick after completion. Against the contract without
the rule: ticks on which two builders overlapped before that follow-up
47,533 → 4,849; builder pairs overlapping when it was given 167 → 17;
follow-up builds refused with "I can't reach the construction site" 73 → 19,
72 → 18 of them from a builder standing inside a friend; builders that never
left their post 62 → 10. Products complete 1.4 ticks later on average. The
overlaps and refusals left come from releases that start more than 128 world
units from the goal, from allied pass-through, and from a release into a
parked builder at another site. For construction throughput, units created
at 12,000, 18,000 and 24,000 ticks were compared in Modern headless
skirmishes (three maps, Classic and Modern computer players, seed 7) and
Survival battles with two buddies (two maps, both AIs, seeds 1–7): 17 of 18
skirmish runs and 73 of 84 Survival runs are bit-identical. The rest share
their trajectory up to the first withheld release — at most 51 ticks long in
the runs traced — and then differ by the battle's own spread; over the seven
seeds the buddies' units at 24,000 ticks moved by between −3.4% and 0% per
map and AI.

Counting only a parked friend in the inside-a-friend start at a held site
was measured on the nine-builder retail scene with its first order swept
over ticks 1–600. Before it, 12 of those orders (at ticks 67–85, where the
walks start straight after the path-request throttle) had an assister,
jammed behind a builder at the site, released the moment an arriving friend
passed through it head-on, walk into that builder and stand inside it for
21–22 ticks; with it, none does, and every overlap left in the 600 runs is
allied pass-through between two arriving builders. Making the end rule see
the cell a released unit took from the friend it walked into, without this
start rule, lengthened those four scenes' overlaps with a worker to 245–256
ticks and left a pair overlapping at completion: the longer release carried
the assister further through the builders at the site. On the 136-scene
probe above the start rule lowered overlap ticks before the follow-up from
4,849 to 3,714 and pairs overlapping at it from 17 to 12. Every fingerprint
lock holds, and units created at 12,000, 18,000 and 24,000 ticks are
unchanged in all 138 headless runs compared (Modern skirmishes on three maps
with seeds 1–3 and Survival with two buddies on two maps with seeds 1–7,
Classic and Modern computer players); 137 are bit-identical.

**Boundaries.** Aircraft, carried units, structures and units with no
movement head never count jammed ticks. A unit released while its order ends
stays released until its window closes, and friendly movers may pass through
it meanwhile. The per-handle state (`System.jamReleases`: run, window end,
hold limit, cooldown end, owed re-plan) is written only when the bound rules
release jams, so Strict never allocates it; the run is reset when the order
binding is deactivated, the whole row is cleared when the unit is forgotten
or restored, and it is not saved: a load starts with no unit released, and a
unit loaded inside a friend is freed by the near-destination start above.
A switch to Strict or Community mid-release leaves the state unread; an
overlap then in place is resolved, as in retail, only by the units moving
apart.

**Determinism and fingerprints.** The release reads committed state in the
sweep's slot order; Modern stays deterministic and bit-identical across hosts.
Strict and Community fingerprints do not move, nor does the 6,000-tick
Modern Ashap Plateau lock at the time it landed; the current Modern locks
are listed under [Modern wedge escape](#modern-wedge-escape), after
[Modern route straightening](#modern-route-straightening) moved them again.
Those battles contain friendly jams that now drain. Because the policy can end
a battle sooner, a change to it must run `tools/check-retail --full`, whose
long trajectory is the only lock on the end tick. The work-site rule moves no
lock, the long Modern Ashap run included.

**Verification.** `movement.TestJamRelease` (Strict and Community stay blocked
behind a parked friend; Modern commits on the tick after thirty jammed ticks;
a same-way queue, an enemy, a one-way ally and a mover near its route end
are never released; a Strict run allocates no state), `TestJamReleaseAnswers`,
`TestJamReleaseNeverEndsInsideAFriend` (a 1x1 mover passing a parked 3x3
friend just before its goal is never left inside it),
`TestJamReleaseStateIsCleared` (a deactivated move forgets its run, a
forgotten unit its release), `TestJamReleaseFreesWedgedUnits` (a unit
wedged inside a same-way friend gets past it where one merely behind such a
friend keeps queuing; a route-less unit inside a friend is released; Strict
releases neither), `TestStaticPassableSeesThroughMobilesOnly`
(kept movers wall their anchors); for work sites, `TestJamRelease` (a jammed
builder is never released into a builder at the site, an assister in the
zero band or one resting on the arrival band outside it; one standing
inside a moving friend at a held site is not released, one inside a parked
friend is, and near a point goal the moving friend still frees it; a friend
short of the site and one in an attack chase's band are still passed; Strict
never releases) and `path.TestStandBoundsHoldEveryRestingCell`; in the retail
tier, `session.TestModernBuildersAtASharedSiteNeverStandInsideEachOther`
(nine construction kbots on one collector, the first order issued on eight
ticks before, through and after the path-request throttle's window: no
builder stands inside one already working at the site, none is left inside
another at completion, and each takes its next build on the tick after) and
`TestStrictBuildersAtASharedSiteNeverOverlap`; the Strict, Community and
Modern `headless` fingerprint locks, including the long Modern Ashap end tick
(`tools/check-retail --full`).

#### Modern pocket release

**Retired from Modern on 2026-09-29**, with the jam release it extends. Under
[Modern arrival places](#modern-arrival-places) a unit sealed out of its own
free place settles where it stands instead of passing through the friends
round it. The policy is kept as `OverlapRules.PocketRelease` for the
pathfinding laboratory's baseline, and no reserved rule set answers it. The
text below describes the policy as it stood; where it says Modern, read the
baseline.

**Nanolathe Modern policy.** A ground mover sealed out of its own free
destination — a packed formation filled in around its slot before it got
there, so the slot is free but every way in is held by parked friends — is
granted a jam release into the slot. It is jam release's trigger for a unit
that cannot count jammed ticks.

**Strict 3.1 behavior.** The class layer walls stationary units once their
occupant age passes `[04 R-PATH-01 §14]`, so from outside the ring the setup
ray finds nothing nearer the goal than the start and the request publishes
empty without seeding `[04 R-PATH-01 §4]`, raising cannot-get-there
`[04 R-PATH-01 §7]`. `Move_Ground`, the last primary record, re-arms its
phase-0 retry and keeps re-installing its goal with the synthetic line
suppressed `[04 R-ORD-01 §4]`, and every later search is rejected the same
way, so the unit stands for ever. `StrictRules.PocketRelease` answers
`(0, 0)`; Community inherits it and an unbound `System` answers the same.

**Why the other Modern policies do not reach it.** Jam release counts
rejected commits, and a route-less unit at rest commits nothing;
[crowded arrival](#modern-crowded-arrival) needs the goal footprint itself
held by a stationary friend; [unreachable moves](#modern-unreachable-moves)
certify over a static view in which every mobile is transparent, where the
slot looks reachable. In the opt-in path benchmark's dense group moves, run
past their windows to 2,400 ticks, Modern without this rule leaves 9 of 64
units in `avoid/through_idle_army` and 8 of 256 in `avoid/open_big_a` in
this state from about tick 1,500 to the end; at the standard windows these
units are still in an entrance jam between moving units, which no completion
or release rule addresses (research branch `research/r7-enclosed`,
`docs/PATH_PROTOTYPE_R7_enclosed.md` there).

**Modern behavior.** `ModernRules.PocketRelease` answers `(16, 30)`; the
answer is off whenever `JamRelease` is.

1. *Certificate.* At the one site where an empty publication raises
   cannot-get-there on the live order, after unreachable moves, a move that
   passes the record test the Modern completion policies share
   (`orders.UnreachableMoveArrival`), whose unit stands within 16 cells per
   axis of its goal anchor (and not on it) and against a parked friend, is
   judged by a bounded flood. A *parked friend* is a live, grounded,
   uncarried, complete mobile of the unit's owner or a mutually allied owner,
   with zero speed and no active route. The flood walks footprint anchors in
   a 33×33 window centred on the goal anchor, from the goal footprint, in the
   search's eight directions and testing only the destination footprint
   `[04 §7.1]`. An anchor is open when no cell of its footprint is held by a
   parked friend and it is statically passable in the owner's static view —
   terrain, features and buildings as the unreachable-move probe reads them,
   unexplored ground optimistic. Every other occupant — moving friends,
   enemies, the unit itself — is transparent, so it can only open the pocket.
   The pocket is *sealed* when the goal footprint is open and the flood closes
   without reaching the unit's anchor or one of its eight neighbours and
   without stepping past the window onto the map; off the map is a wall. A
   goal a parked friend holds is crowded arrival's and a statically blocked
   one is unreachable moves'; neither is certified. Re-certification keeps
   the tick of the order's first certificate; a route published for the
   order clears it, except the pocket release's own.
2. *Release.* Thirty ticks after the first certificate — jam release's own
   jammed-tick trigger — the follower's closing check, asked only while some
   certificate is live and never while a release of the unit is running or
   cooling down, runs the flood again from where the unit stands against the
   ring. If the pocket is still sealed it grants a jam release marked as a
   pocket release, with jam release's 90-tick window, hold limit and
   cooldown, and asks for a prompt re-plan. The released search reads jam
   release's static view, so the route leads through the ring; the commit
   passes friendly occupants as any release does, and the unit completes by
   ordinary arrival in its slot.
3. *End.* Jam release's near-destination rule closes a pocket release only
   on the unit's own goal anchor: a unit standing against the ring is already
   clear of every friend, and so is one crossing a free slot inside the
   formation. The window, the hold-open while inside a friend and the
   cooldown are jam release's.
4. *Through the retry.* The move's code-9 retry re-installs its goal every
   30–59 ticks, which drops the released route, and the goal installer keeps a
   request stamp under ten ticks old `[04 R-PATH-01 §8]`; each new activation
   during the release is re-planned at the next poll, once, instead of
   waiting out the re-route throttle behind the grant's own search.
5. *Finish.* After two grants that did not get the unit in, a closing flood
   that still finds the pocket sealed finishes the move where the unit
   stands, through the crowded-arrival completion: phase 1, the arrival gate
   armed, `Arrived`, idle refill
   ([DESIGN_UNITS_ORDERS_COB](DESIGN_UNITS_ORDERS_COB.md#modern-crowded-arrival)).

Integer arithmetic only, no RNG draw, no resource effect, no map iteration.
The flood runs depth first, nearest the unit first — a traversal order that
changes its work and never its verdict — and reuses its scratch.

**Cost to the player.** The released unit visibly passes through the parked
friends between it and its slot, drawn inside one or two at a time. Over the
21 releases in the long runs below the overlap lasted 11–51 ticks, 27 on
average (0.4–1.7 s), and none of the 56 parked friends passed changed
position. Two of those units had been left wedged into friends by earlier
jam releases; the pocket release carries them out as well.

**Measured effect.** Opt-in path benchmark, one deterministic repeat,
`modern-no-pocket` against Modern. Past the windows, to 2,400 ticks, where the
sealed-out units are the end state:

| case / size | near/goals | pending | searches |
|---|---|---|---|
| `avoid/through_idle_army` 64 | 54 → 64 of 64 | 9 → 0 | 968 → 765 |
| `avoid/open_big_a` 256 | 246 → 256 of 256 | 8 → 0 | 1,556 → 1,305 |
| `r7/open_flea_rev` 256 (research branch) | 252 → 256 of 256 | 2 → 0 | 1,139 → 1,074 |

Without the rule the pending units stand still from about tick 1,500 to the
end, each re-requesting a search every 60–67 ticks; with it every one
reaches its own slot. At the standard windows, over the whole corpus without
the scripted waves: near-goal arrivals 4,275 → 4,281 of 5,862 and pending
moves 1,485 → 1,479, three cases gaining (`wreck/wreck_over` 32 26 → 30, the
friendly head-on meeting with 64 units 62 → 63, the friendly four-cell choke
with 64 units 63 → 64) and none losing; the scripted waves at every size and
the three-army simulation-benchmark battle on Town & Country (seeds 7, 11 and
23, 6,000 ticks) are bit-identical, since no unit there is certified long
enough to be released. One more pair of units is left overlapping at the end
of a window across the corpus (538 against 537): in the perpendicular
crossing with 64 units the window closes two ticks before a released unit
reaches its slot.

Tick cost did not rise. On the 256- and 1,500-unit scripted waves, where
nothing is released, two alternating rounds of three repeats each of the
build before this rule and the build with it gave total thread CPU of
611 → 591 ms and 5,620 → 5,614 ms (medians of six), the p95, p99 and
slowest tick overlapping run for run, and identical trajectories; the
certificate row and the flood's reused scratch add 9–14 allocations
(20–31 KB) a run. Where the rule fires, in the two 2,400-tick runs above
(three repeats each), total thread CPU stayed within the repeats' spread
(283 → 289 ms at 64 units, 443 → 424 ms at 256) and allocations fell about
7% with the searches (29,007 → 26,858 and 38,153 → 35,465): the floods and
the released searches cost no more than the rejected searches they replace.
A flood is bounded by its 33×33 window and runs only at a candidate's
rejected publication and when its dwell or a release's cooldown ends.

The dwell was chosen against a 90-tick one on the same runs (research
branch): equal or better everywhere, with every release starting sooner. At
90 ticks one unit in `r7/open_flea_rev` 256 is finished 1.1 cells from its
slot, because a second sealed-out unit holds the slot and crowded arrival's
own 90-tick dwell finishes the first before the holder's release frees it.
The research prototype also measured and rejected finishing sealed-out units
where they stand, or moving their goals to the nearest reachable free
footprint: both change no arrival in any window and leave the formation's
holes and the stuck units' positions exactly as they were.

**Boundaries.** Only mode-1 ground movers with a plain terminal move: queued
moves, patrol, guard, work approaches, attacks, danger responses and aircraft
keep their own handling. Enemies are never passed: a hostile unit is
transparent to the flood, so a ring with an enemy in it is open and nothing
is certified, and a released search keeps hostile movers as walls. Parked
friends are never moved; the release moves only the released unit. The
per-handle certificate (`System.pockets`: order, first-certificate tick,
grants, the activation the release planned for) is written only when the
bound rules release pockets, so Strict never allocates it; it is cleared with
the order binding (`DeactivateMove`, hence `ForgetUnit`), on a restored unit,
and at the first follower visit after a switch to a set that answers off. It
is not saved: a load starts with none and the next cannot-get-there
publication certifies afresh, with a fresh dwell. A switch to Strict or
Community mid-release leaves the running release to jam release's own state,
which Strict never reads ([DESIGN_GAMEPLAY_RULES §5](DESIGN_GAMEPLAY_RULES.md#5-switch-timing)).
**Open item:** units left inside parked friends after allied pass-through or
jam release stay there. `insideFriend` reads grid ownership, and a unit that
won the contested cells under the lower-index claim rule never looks inside
its friend, so jam release's wedge rule does not fire for it; the pocket
release frees such a unit only when it also holds a sealed slot.

**Determinism and fingerprints.** The certificate and the closing check read
committed state in the sweep's slot order and the flood is a pure function of
it; Modern stays deterministic and bit-identical across hosts. No fingerprint
lock moves — Strict, Community and Modern, the long Modern Ashap battle
included (still `partial-v1:d7f48d704d2eb5d3` to its 54,000-tick bound):
none of the locked scenes seals a unit out of a free slot.

**Verification.** `movement.TestPocketReleaseAnswers` (Strict and Community
answer off, Modern on, off whenever jam release is, dispatch allocates
nothing), `TestPocketReleaseLeavesStrictAndCommunityUntouched` (no
certificate, grant, finish or allocation), `TestPocketReleaseCertifiesOnlyASealedPocket`
(only at an empty publication; an open side, a moving or routed friend, an
enemy, a blocked or held goal, a unit not against the ring or beyond the near
bound get nothing), `TestPocketReleaseWindowIsOpenAndTheMapEdgeIsAWall`,
`TestPocketReleaseEndsOnlyOnItsGoal` and `TestPocketReleaseGrantCapFinishesInPlace`;
in the retail tier, on an authored block of parked fleas around a free slot,
`session.TestPocketReleaseTakesASealedOutUnitIntoItsSlot` (the baseline
reaches the slot, Modern without the rule does not, the block never moves),
`TestStrictAndCommunityNeverGrantAPocketRelease`,
`TestPocketReleaseLeavesAHeldGoalToCrowdedArrival` (identical with and
without the rule) and `TestPocketReleaseNeverPassesAnEnemy`; the Strict,
Community and Modern `headless` fingerprint locks
(`tools/check-retail --full`).

### Modern route straightening

**Nanolathe Modern policy.** A finished route has its sawtooth turns removed
before it is published, so a unit whose goal lies a little off its row or
column travels in a straight line instead of weaving between two parallel
lines. Everything else about the route — how it is found, where it ends,
when it publishes — is the retail search's.

Since 2026-09-29 Modern binds `path.SmoothKernel`, which runs this pass and
then [route smoothing](#modern-route-smoothing) on the same slice. The
straightening kernel is unchanged, and the pathfinding laboratory's baseline
binds it alone.

**Strict 3.1 behavior.** The retail search reconstructs a route from the
cells where its grid path turns, converts them to world points and the
publisher keeps the first 20 `[04 R-PATH-01 §7]`. For a goal slightly off
the start's row the grid path can alternate diagonal steps between two
parallel lines a few cells apart, so the route is a sawtooth of up to 64 turn
points of which 20 are published. A slow-turning unit crawls along it,
turning at every point, and reaches the end of the 20 only part of the way
there. In the opt-in path benchmark eight surface ships ordered across open
water (`naval-shallow-surface`) each received a 20-point sawtooth with turns
five cells apart; before [group destination slots](DESIGN_INTERFACE_HUD_INPUT.md#modern-group-destination-slots)
four of them shared a goal, collided and re-planned, which happened to
replace the sawtooth with a straight route, and with slots none collided and
none arrived in the window. Strict 3.1 and Community bind
`path.RetailKernel`.

**Modern behavior.** `path.StraightenKernel`, which the Modern rule set bound
by itself until route smoothing wrapped it,
opens the retail search unchanged; on the slice that search finishes, if the
route has 3 to 64 points, it walks them once: while the points on either side
of an interior turn share a row or a column no more than 16 cells apart, and
every anchor strictly between them reads passable through the request's own
passability port (the same port, and the same view, the search read —
including [learned terrain](#modern-learned-terrain) and a
[jam release](#modern-jam-release)'s static view), the turn is dropped;
otherwise the walk moves on. The result is published on that same slice, so
publication timing is exactly retail's, and the publisher then keeps the
first 20 points as before. Each anchor probed is charged to the player's work
like a heap pop `[04 §7.3]`.

Only turns whose neighbours share a row or a column are removed. A general
line-of-sight shortcut was measured and rejected: it sent each unit of a
column straight at a choke from its own angle, so the column arrived as a
clump instead of the files the retail route forms, and it lost heavily in the
choke cases (opposed columns through a choke two flea footprints wide with 64 units
55 → 36;
one-cell choke with 16 units 16 → 12); keeping one cell of clearance from
obstacles did not help. A 64-cell span also hurt dense open groups (64 units
64 → 30), where straight shortcuts converge.

**Cost to the player.** Units still funnel as their retail routes lead them,
but a straightened route changes their spacing, and small single-lane chokes
lose a little throughput: one-cell choke with 16 units 15 → 13, perpendicular
flows with 64 units 64 → 60, the friendly wide choke with 64 units 61 → 58.

**Measured effect.** Opt-in path benchmark on research branch
`research/path-round5`, one deterministic repeat of every case and size,
against the same tree with the retail kernel (artifacts
`~/nanolathe-bench/2026-09-23/prod2`): near-goal arrivals 4,145 → 4,167 of
5,756 and pending moves 1,509 → 1,486 over the corpus without the scripted
waves, 13 cases gaining and 5 losing; on the scripted waves 282 → 282 of
2,304 with 256 units and 833 → 1,070 of 13,500 with 1,500. The naval crossing
goes 0 → 8 of 8; the friendly four-cell choke with 64 units 54 → 61 and the
idle-army crossing 29 → 36. On interleaved repeats the
straightening probes added about 6% to the 1,500-unit waves' search work with
the tick CPU p95, p99 and total within the host's noise.

**Boundaries.** A route of more than 64 points (never produced: the
reconstruction keeps 64) and a request with no passability port are published
unchanged. Diagonal legs, the first and last points and the route's status
are never altered. A straightened segment is checked against the same
snapshot of passability the search read, at the tick it finished; moving
units are the follower's and the commit validator's business, as for any
retail route. The kernel is zero size and keeps no state across requests.

**Determinism and fingerprints.** Integer arithmetic only, no RNG, no map
iteration; the walk is in route order. Strict and Community fingerprints do
not move. The Modern locks move, together with those of the jam release's
near-destination start that landed with this policy: the 6,000-tick Modern
Ashap lock to `partial-v1:320cbaa11e9fd28a` (it had equalled Community's),
the long Modern Ashap battle runs to its 54,000-tick bound again, and the
benchmark-fixture warm and final locks moved with it. Jam release's wedge
rule then moved them again: the long Modern Ashap battle
`partial-v1:d7f48d704d2eb5d3` (still to 54,000 ticks), which is still
current, and the benchmark fixture `partial-v1:6d06c2320bc9cd10` /
`partial-v1:28977ed152d78088`, which
[Modern wedge escape](#modern-wedge-escape) has since moved.

**Verification.** `path.TestStraightenRemovesSawtoothTurns` (a two-row
sawtooth becomes one row; probes are charged; the route publishes on the
slice the retail search finishes, not before),
`TestStraightenKeepsBlockedAndDiagonalTurns` (a blocked cell keeps the turn;
diagonal approaches and shortcuts longer than 16 cells are never taken);
`session.TestReservedRuleSetsBindTheirSearchKernels`; the Strict, Community
and Modern `headless` fingerprint locks.

### Modern wedge escape

**Nanolathe Modern policy.** A ground mover whose committed footprint covers
ground the commit's static test rejects — in play, a wreck stamped over it —
may step off that ground and is given a route off it. It never steps onto
rejected ground it does not already cover, and a mover the wreck encloses
gets nothing.

**Strict 3.1 behavior.** The feature stamp tests no unit occupancy
`[05 R-FEAT-01 §3]`, and a corpse is stamped at the dying unit's committed
anchor `[05 R-FEAT-01 §13]`, so a wreck can land over a live unit. The
commit validator tests every cell of the proposed footprint, static test
first `[04 R-COLL-01 §2]`: a step whose new anchor still covers a wreck cell
is rejected, and when every neighbouring anchor overlaps the wreck, every
step is. The route search reads the start anchor from the class layer, which
walls it; the setup ray returns the start's own heuristic at once and the
request publishes empty without seeding `[04 R-PATH-01 §4]`, the empty
publication raises cannot-get-there `[04 R-PATH-01 §7]`, and the move's retry
re-installs its goal with the synthetic line suppressed. The unit therefore
stays until the wreck goes, unless a route published before the wreck, or the
first synthetic line of a later order, uncovers every wreck cell in one
anchor step. `StrictRules.WedgeEscape` answers false; Community inherits it
and an unbound `System` answers the same.

**Where wedges come from.** Among ground units only the Arm Jammer's corpse
(3x3 over a 2x2 movement footprint) is larger than its unit in the reference
install; the other larger corpses are ships' and do not block. In battle,
when this policy was measured, wedges came from overlap:
[allied pass-through](#modern-allied-pass-through)
and [jam release](#modern-jam-release) let friendly units overlap, and a unit
that dies inside a friend leaves its wreck over it. Both are retired since
2026-09-29 ([Modern traffic](#modern-traffic)), which leaves the corpse
larger than its unit; the policy is kept for that case and costs nothing
where no unit is wedged. In the three-army
simulation-benchmark battle on Town & Country (clean spawn sites, seeds 7, 11
and 23, 6,000 ticks; research branch `research/r7-wedge`) every one of the 19
wedge events was the wreck of a unit that died overlapping the covered one —
by a row, a column or a single corner cell, and once exactly on top of it.
That covered `corak` stood in the wreck until the battle ended, every
proposal under its attack orders refused.

**Modern behavior.** `ModernRules.WedgeEscape` answers true. Three pieces
apply, all keyed to the mover's committed footprint:

1. *Commit exemption.* In the ground commit's per-cell test, a cell that
   fails the static test (`Profile.IsPassableCommitCell`: feature, depth,
   slope) does not reject when the committed footprint already covers it.
   The rule is asked at the first failing cell of a visit, so a proposal that
   passes the static test never asks. Cells entering the footprint are tested
   as always, and every cell still takes the occupant test, with allied
   pass-through and jam release unchanged. The set of rejected cells a mover
   covers can therefore only shrink: it can leave a wreck and never walk
   further into one.
2. *A way out.* A search opened for a live, grounded, uncarried mobile unit
   whose committed footprint covers such a cell, and equals its profile
   footprint, wraps the request's passability read once at request open. An
   anchor that overlaps the committed footprint and that the request's own
   view reads 0 is re-read cell by cell: the requester's own cells pass, and
   every other cell must pass that view's per-cell chain — the class layer's
   (terrain, features, buildings, stale occupants) for the ordinary and
   [learned-terrain](#modern-learned-terrain) views, and terrain, features,
   buildings and hostile movers for [jam release](#modern-jam-release)'s
   static view — inside the view's own bounds. A passing anchor answers the
   steep tier: passable, and charged the steep cost `[04 R-PATH-01 §3]`, so
   the search prefers the shortest way off. The start now reads passable, so
   the setup ray walks, the search seeds and an ordinary route is published
   through the ordinary scheduler; [route straightening](#modern-route-straightening)
   probes through the same wrapped port. Every other anchor, every anchor the
   view already admits (unexplored ground included) and every request of a
   unit that is not wedged read exactly as before.
3. *Prompt re-plan.* When the static test refused a commit, the mover covers
   rejected ground and its route's first point is not its own anchor's
   published point `[04 R-PATH-01 §7]` — it was wedged under a route planned
   before the wreck, or holds an order's synthetic line — its wants-repath bit
   is set and its request tick cleared, as a jam release does, so the next
   scheduler call admits the search of piece 2 instead of the ordinary
   60–67-tick re-request `[04 R-MOV-01 §7]`. A route planned from where the
   unit stands is left alone: the unit is turning toward the way that search
   found, and re-planning would only repeat it.

The scheduler, its budgets and admission order, the full-or-empty
publication, the follower, arrival and every order rule are unchanged; no
second search, local escape route or push is involved. Integer arithmetic
only, no RNG, no map iteration; the commit's per-cell closure still does not
escape, and the wrapper is one closure per wedged request.

**Cost to the player.** A freed unit walks through its own crowd instead of
standing in a wreck, which shows as blocked ticks where friends are in its
way. A unit straddling a one-cell-thick obstacle — only a stamp, a spawn, an
unload or an overlap claim can put it there — may leave on either side,
because it covers the obstacle already; it never crosses ground it does not
cover. The rule is not specific to wrecks: a unit spawned or unloaded on
terrain its profile rejects walks off it too.

**Measured effect.** Opt-in path benchmark, family `wreck`
([traffic and lifecycle](PATH_BENCHMARK_TRAFFIC.md#wrecks-over-live-units)),
one deterministic repeat, `modern-no-wedge` against Modern:

| case / size | wedged units that leave | near/goals | pending | blocked unit-ticks | searches |
|---|---|---|---|---|---|
| `wreck/jam_corpses` 16 | 0 → 4 of 4 | 10 → 14 of 14 | 4 → 0 | 97 → 246 | 150 → 40 |
| `wreck/jam_corpses` 64 | 0 → 23 of 23 | 29 → 38 of 52 | 23 → 14 | 2,126 → 7,544 | 954 → 670 |
| `wreck/wreck_over` 8 | 0 → 4 of 4 | 4 → 8 of 8 | 4 → 0 | 4 → 28 | 109 → 11 |
| `wreck/wreck_over` 32 | 0 → 16 of 16 | 16 → 26 of 32 | 16 → 6 | 310 → 1,767 | 461 → 178 |

The leave counts come from a per-tick scan of committed footprints over the
same runs on the research branch. Without the policy every wedged unit stands
at its start for the whole window, after its first rejected search, and the
pending count is exactly the wedged count; with it every wedged unit leaves and the objectives still
pending are in the destination crowd. Searches fall because a wedged unit's
request is otherwise repeated and rejected for the rest of the window. The
rest of the corpus is bit-identical — no other case puts a unit on rejected
ground — which was checked on the research branch over all 183 case/size
combinations, the scripted waves included.

In the battle, each of the 19 wedge events was replayed from the identical
state with the policy switched on at the tick the wreck lands (a
command-boundary switch, [DESIGN_GAMEPLAY_RULES §5](DESIGN_GAMEPLAY_RULES.md#5-switch-timing))
and followed for 600 ticks: wedged ticks fell from 2,775 to 1,395 and ticks
spent wedged while trying to move from 1,212 to 652; the two units that never
left in 600 ticks, the corak among them, left in 42 and 30. The pieces were
also measured apart: the commit exemption alone frees none of the 47 fixture
units, because nothing gives them a route (2,427 battle ticks); adding the
search re-read frees all 47 (1,672); the prompt re-plan changes no fixture
result but frees the corak in 30 ticks instead of 321. Over the three whole
battles, units left wedged at the end with a movement order fell from 3 to 0.
Tick cost did not rise: on the 256- and 1,500-unit scripted waves, three
repeats each, the thread CPU p95, p99 and total overlapped repeat for repeat,
with identical trajectories and allocation (measured before the prompt
re-plan was added; it runs only after a refused static step).

**Boundaries.** Only mode-1 ground movers, hover and surface ships included;
structures never reach the commit, aircraft commit on the air plane, and
carried units take the carried branch and are never wrapped. The occupant
test, enemies and structures are unchanged. A mover the wreck encloses —
every neighbouring anchor adds a rejected cell — gets an empty search and no
movement: nothing is fabricated. An idle wedged unit is not moved; it leaves
when ordered. The goal-sealed probe of [unreachable moves](#modern-unreachable-moves)
still reads its static view, in which a wedged start is blocked, so a wedged
unit whose goal is also sealed is certified only once it has stepped off. The
prompt re-plan costs searches: an enclosed unit whose order keeps
re-installing a synthetic line re-plans once per line rather than once per
throttle period, each search rejected at setup for about a hundred charged
steps. A wedged unit hemmed in by parked friends still gets no route, since
they wall every anchor its re-read admits, and jam release does not count it
jammed while an order keeps handing it synthetic lines. The policy keeps no
state: the re-plan writes only the follower's existing wants-repath bit and
request tick, which a save does not carry either `[08 R-SAVE-02 §8]`, and a
switch to Strict applies the retail validator and search from the next commit
and request.

**Determinism and fingerprints.** The commit and re-plan read committed state
in the sweep's slot order, the re-read is a pure function of the request's
own view, and Modern stays deterministic and bit-identical across hosts.
Strict and Community fingerprints do not move, nor do the Modern Ashap
Plateau locks (6,000 ticks `partial-v1:320cbaa11e9fd28a`; the long battle
`partial-v1:d7f48d704d2eb5d3`, still to its 54,000-tick bound). The Modern
benchmark-fixture warm and final locks move to `partial-v1:020c5588af463a71`
and `partial-v1:8566bce851e216f7`: that scene composes 86 mobile units on
cells the static test rejects, and from tick 92 seven computer-player
constructors among them get routes where the retail search rejected them at
setup.

**Verification.** `movement.TestWedgeEscapeAnswers` (Strict, Community and
unbound answer off, Modern on, dispatch allocates nothing),
`TestWedgeEscapeLeavesAWreckStampedOverTheMover` (Strict and Community: the
search from the wedged start publishes nothing and the straight line is
refused, drawing from neither stream; Modern: the straight line is still
refused, its search leads off the wreck and the mover leaves without ever
covering more rejected cells), `TestWedgeEscapeFabricatesNothingWhenEnclosed`,
`TestWedgeEscapeLeavesOtherSearchesAlone` (a mover beside a wreck searches as
it does without the policy), `TestWedgeExitValueKeepsTheViewsOtherCells`
(both views, a stale parked mobile, a building, a kept mover) and
`TestWedgeEscapeReplansAWedgedRoutePromptly` (a route planned elsewhere loses
its throttle at the first refused step, one planned from here keeps it, and
Strict and Community never touch it); in the opt-in `pathbench` build,
`session.TestPathBenchWreckEscape` (in `wreck/wreck_over` with eight units no
wedged unit leaves under Strict or `modern-no-wedge`, all four leave under
Modern); the Strict, Community and Modern `headless` fingerprint locks,
including the long Modern Ashap end tick (`tools/check-retail --full`).

### Modern traffic

**Nanolathe Modern policy (user-authorized 2026-09-29).** Friendly units never
share cells. A ground mover steers round what is ahead of it; its routes are
pulled taut, searched with a weight that follows the load and planned beside
the routes friends already hold; it asks for a route again after half a
second; the units of a group are given places to stand; and a unit that cannot
reach its place settles where it may. The user asked for this in so many
words — no units moving over each other, with full freedom to rebuild Modern's
ground movement, judged by recorded games — and chose it as Modern's default
and only movement after reading the measurements below.

It replaces three policies and one throttle, which are **retired from
Modern**: [allied pass-through](#modern-allied-pass-through),
[jam release](#modern-jam-release) and its
[pocket release](#modern-pocket-release), under which friendly units passed
through each other, and [re-route staggering](#modern-re-route-staggering).
Their code, tests and design text are kept for the pathfinding laboratory's
baseline, `movement.OverlapRules`, so that what Modern was can be measured
beside what it is ([PATHFINDING_LAB](PATHFINDING_LAB.md)). No reserved rule
set answers them and the game links no rule set that does.

The policy has seven parts, each with its own section below:

| Part | What it changes | Where |
|---|---|---|
| [Steering](#modern-steering) | the heading a mover wants | `traffic_steer.go` |
| [Route smoothing](#modern-route-smoothing) | which points a finished route publishes | `path.SmoothKernel` |
| [Search weight](#modern-search-weight) | how greedy a route search is | `System.searchUnder` |
| [Prompt re-routing](#modern-prompt-re-routing) | when a follower may ask again | `ModernRules.RepathDelay` |
| [Route claims](#modern-route-claims) | what a search step costs | `pilot_claims.go` |
| [Arrival places](#modern-arrival-places) | the goal of a plain move | `pilot_arrive.go` |
| [Routes through friends](#modern-routes-through-friends) | what a search reads as a wall, for a unit whose own searches find nothing | `traffic_through.go` |

**What no part changes.** The commit validator. Every part changes what a
unit wants, which route it is given or where its order sends it; a step onto
a held cell is refused as retail refuses it `[04 R-COLL-01 §2]`, and the
blocked response that follows a refusal is retail's `[04 R-COLL-01 §1]`.
Nothing moves a unit, stamps occupancy or writes the visibility grids.

**Strict 3.1 behavior.** Retail has no push, no yield, no sidestep and no
shortcut smoothing `[04 R-COLL-01 §7]`: a refused mover halves its speed,
clamps against its old footprint and proposes the same step again, its
follower asks for a route every sixty ticks `[04 R-MOV-01 §7]`, the search
weighs its heuristic by its player's load `[04 R-PATH-01 §6]`
`[04 R-PATH-01 §10]`, and every unit of a group sent to one point is given
the retail selection offsets `[04 R-STANCE-01 §5]`. `StrictRules.Traffic`
answers the zero value, `StrictRules.RepathDelay` sixty, and
`StrictRuleSet` binds `path.RetailKernel`; Community inherits all three and
an unbound `System` answers the same. Under them nothing steers, nothing is
claimed or placed and no traffic state is written. One counter runs under
every rule set: the search work charged a tick and its average, two integers
on the `System` that no retail path reads.

**The seam.** `movement.Rules.Traffic` answers a `movement.Traffic` value: a
set of switches and numbers, and the `movement.Pilot`s that take part in a
mover's visit, in route searches and in the giving of orders. `ModernRules`
answers the package value `modernTraffic` (`modern_traffic.go`), so the
dispatch allocates nothing. It is asked once a tick in `BeginTick`, before
any unit is visited, and the `System` keeps that answer for the tick; the
command boundary asks once per group command. No existing answer owned these
decisions and no new seam was needed: the questions are asked by this
package's own follower, scheduler and search opening about state this package
owns ([DESIGN_GAMEPLAY_RULES §9](DESIGN_GAMEPLAY_RULES.md#9-extending-the-existing-mechanism)
step 2). The kernel is the existing `session.RuleSet.Path` seam. `Traffic`
holds the policy's own switches and numbers and nothing else; the pathfinding
laboratory's sets answer other values of them. What the laboratory built and
did not choose was removed from the engine after the adoption.

**State.** Rule objects and pilots are values without state; what the policy
remembers lives on the `System`, which is one battle's:

| State | Holds | Written |
|---|---|---|
| `System.traffic`, dense by handle | the side a mover passes on, the heading round and until when; the tick its goal was installed; since when its searches have found nothing, and the reading its route was planned under | in the unit's own visit and at its search's end |
| arrival rows, dense by handle (`System.PilotState`) | the unit's place, how near it has come and when, where it has stood and since when, the exchanges its order has made | at the start of a tick and in the unit's own visit |
| claim grids, one per owner (`System.PilotState`) | per 32-world-unit block and sector, how many routes cross it | recounted every four ticks from the routes units hold |
| `System.workSmooth` | the route search work charged a tick, averaged over eight | at the start of a tick |

All of it is allocated at first use under a rule set that answers the policy,
so a Strict session never allocates it. A unit's rows are dropped when the
unit is forgotten (`ForgetUnit`: death, slot reuse, load reset). **None of it
is saved**, as no Modern transient state is
([DESIGN_GAMEPLAY_RULES §6](DESIGN_GAMEPLAY_RULES.md#6-save-interaction)): a
loaded battle starts with none. Its units take their places up again from
their orders' goals at the first tick, the claims are recounted at the first
tick from the routes the save carries, and what is lost is the side a unit
was passing on and the counts of ticks it had stood, which begin again. After
a switch to Strict 3.1 or Community 3.9 the state is kept and neither read
nor written; a switch back resumes with it. A route published before a switch
is followed to its end in either direction. Waypoint passing beside a corner
is gated by the current tick's steering policy too: retained Modern steering
does not replace retail's five-world-unit pruning radius after a switch.

**Measured effect.** The pathfinding laboratory replays moments cut from
recorded multiplayer games under any rule set and scores each unit's trip:
the ticks from its order until it has come to rest for good at its goal
([PATHFINDING_LAB](PATHFINDING_LAB.md)). Rule sets are compared moment by
moment, three replays each, with standard errors. Settings were chosen on one
set of moments and are reported on another, drawn by lottery, that no setting
was chosen on. The final run (2026-09-29, from main `160834d4`, artifacts
`~/nanolathe-bench/path-redesign/out/final-v4`) measured the policy as the
laboratory rule set `next-v4`, which answers as `ModernRules` does
(`pathlab.TestTheAdoptedSetIsModern`), against Modern as it then was:

The picker used for these historical snippet results filtered its lottery
classes on recorded arrival outcomes or timing. The corrected picker removes
that selection ([PATHFINDING_LAB](PATHFINDING_LAB.md)); the numbers below remain
measurements of the selected corpus. A new comparison without that bias needs
`mine -all` and regenerated snippets, not just another replay of the old ones.

| Held-out groups and single units (592 units) | Ticks to arrive | Against Modern before | Arrived |
|---|---|---|---|
| Strict 3.1 | 818 | +44 ± 6 | 72.7% |
| Modern before, overlap removed and nothing in its place | 811 | +38 ± 6 | 74.5% |
| Modern before | 774 | — | 80.8% |
| Modern before with route smoothing | 737 | −37 ± 4 | 88.1% |
| Modern traffic | 707 | −67 ± 7 | 92.7% |

Refused steps fall from 60 ticks a unit to 42. Over every held-out moment,
armies of 32 units and more included (3,025 units), the difference is
−30 ± 4 ticks; the armies alone, whose windows are too short for most units
to arrive under any rules, get 779 world units nearer their goals in the
window against 739. On the moments the settings were chosen on (1,598 units)
it is −82 ± 7 ticks, 91.7% arriving against 88.4%, with Strict 3.1 at
+118 ± 13. A diagnostic capture of 103 units passing through the trees on
Great Divide, replayed the same way, takes 689 ticks a unit against 848, and
1,063 under Strict 3.1.

Whole games show what half a minute cannot. The computer played itself
through 64 games on eight maps under each movement policy, and every five
seconds the run counted the ground units that were *held*: standing within
three cells of one point for ten seconds while their order's goal lay more
than twenty cells away.

| Ground units held | Six maps of open ground | Two maps of narrow passes |
|---|---|---|
| Retail's movement and search, same brains | 0.28% | 8.6% |
| Modern before | 0.14% | 2.1% |
| Modern traffic | 0.15% | 7.5% |

**Announcement-review corrections.** The corrected idle-poll cutoff and
recipient-passability checks were measured from `9ff1eb8b` to `1ae0358f` on
all 543 path-benchmark case/rule/size combinations, with three identical
outcome repeats per build. Strict and Community retain all 181 outcomes
each. The Modern 256- and 1,500-unit wave cases change their later input
commands because the fixture chooses short goals relative to current unit
positions; those two cases are excluded from paired cost/outcome claims.

Modern results remain mixed. Outside scripted waves, units entering their
reporting radius increase from 3,093 to 3,104 of 5,862 goals, but the
900-tick `avoid/choke_wide_friendly` case at 64 units falls from 18 to 1;
pending goals rise from 46 to 63 and blocked unit-ticks rise by 23.5%.
The fixed inputs and repeated outcomes establish a congestion regression,
not a reporting change. The corrections satisfy the existing bounded-work
and arrival-place contracts; this remaining congestion limitation needs
separate work before claiming uniformly better movement. Historical
measurements above describe their original revisions.

**Cost to the player.**

- *Crowds in narrow passes.* Where wrecks close a pass and the computer
  player goes on sending armies into it, Modern's units now stand in rows
  behind the first instead of walking through each other to the front: three
  and a half times as many are held as before, and about as many as under
  retail's movement. Most of them are on patrol and attack orders to goals
  they cannot reach, which only a plain move can be finished short of.
- *Authored chokes.* On the [path benchmark](PATH_BENCHMARK.md)'s 181
  authored scenes — dense blocks of 64 to 256 units ordered from a standing
  start, opposed columns through gaps one or two units wide, waves of 1,500 —
  4,057 of 22,386 units come near their goals in the window, between
  Strict 3.1's 2,885 and Modern before's 5,908. Where two blocks must pass
  through each other, overlap is hard to beat. The recordings say how often
  that happens: friendly units meeting head-on within three cells of terrain
  are about 2% of blocked time.
- *A parked friend in the way of a single unit* is gone round, where it was
  walked through: in the moments picked for that case the trip is 43 ± 61
  ticks longer on twenty units.
- *Units reverse more.* A unit that goes round something turns one way and
  then the other: 4.0 reversals per 1,000 world units travelled against 1.2.
  Turning per distance is what it was.
- *A unit may finish short of its goal*: at the edge of the group it was sent
  into, or at the nearest ground it can stand on when its goal cannot be
  reached. It reports `Arrived`.
- *Search work.* A refused unit asks again after half a second, and a search
  at weight 1.5 looks at more ground: on the held-out moments 15 searches
  per thousand unit-ticks of movement against 9, and 23 units of search work
  a unit-tick against 11. The [simulation-cost benchmark](SIM_BENCHMARK.md),
  three runs of each in alternation on a machine in use by other work:

  | Tick cost, ms | 750 units: mean | 99th | 1,500 units: mean | 99th |
  |---|---|---|---|---|
  | Modern before | 1.68 | 4.61 | 2.86 | 6.62 |
  | Modern traffic | 1.90 | 4.30 | 3.97 | 7.82 |

  At landing, on a quiet machine, `tools/sim-bench` with seed 7 on the build
  before and the build after, three runs of each in alternation (750 units,
  300 measured ticks): mean 1.39 ms before and 1.79 after, 99th percentile
  2.53 and 3.40, worst tick 4.1 and 4.9.

**Boundaries.** Grounded movers only — vehicles, walkers, ships and
hovercraft on the ground plane — and only while they hold an order. Aircraft,
carried units and structures never reach any part. Only friendly units are
followed, passed behind, claimed against or read as absent; another player's
unit is something to go round and is never looked through. The policy keeps
[learned terrain](#modern-learned-terrain),
[group-order spreading](#modern-group-order-spreading),
[bounded path work](#modern-bounded-path-work),
[group destination slots](DESIGN_INTERFACE_HUD_INPUT.md#modern-group-destination-slots),
[unreachable moves](#modern-unreachable-moves),
[route straightening](#modern-route-straightening),
[wedge escape](#modern-wedge-escape),
[crowded arrival](#modern-crowded-arrival) and the
[repair-pad queue](#modern-repair-pad-queue) as they are, and was measured
with them. A last resort — passing through friends only after five seconds
held — was measured and left out: it buys nothing on recorded games and
brings back the overlap the policy exists to remove.

**Determinism and fingerprints.** Every part reads committed state in the
sweep's slot order, or at the start of the tick in pool order, with integer
arithmetic. The three sorts the arrival places make have total orders that
end in the handle. No map is ranged, neither random stream is drawn, and no
clock is read. Modern stays deterministic and bit-identical across hosts.
Strict and Community fingerprints do not move. Every Modern lock but the
benchmark fixture's initial one moves: the 6,000-tick Ashap Plateau lock to
`partial-v1:824232669152f4bf`, the long battle to
`partial-v1:29ff0dcd82dc2105`, which now ends at tick 40,470, and the
benchmark fixture's warm and final locks to `partial-v1:5a1f17b4776a9c71` and
`partial-v1:b102eac17ad75900`. The build before the adoption, playing the same
scenes under the laboratory's candidate rule set, reached the same values bit
for bit: what was measured is what was adopted.

**Verification.** `movement.TestModernTrafficAnswer` (the answer, value for
value, and that the dispatch does not allocate);
`TestModernGoesRoundAParkedFriendAndStrictRunsIntoIt` (Modern passes a friend
at rest without a refused step and without sharing its cell; Strict,
Community and an unbound `System` run into it, keep no traffic state and draw
from neither stream, nor does Modern draw);
`TestModernHeadOnMoversPartWithoutSharingACell` (the same for two movers
meeting); `TestStrictIgnoresTrafficStateAfterASwitch`;
`TestModernTrafficKeepsItsStateOnTheSystem` (two battles bound to one rules
value do not share state);
`TestTrafficAnswers`, `TestAlliedPassThroughAnswers`,
`TestJamReleaseAnswers` and `TestPocketReleaseAnswers` (every reserved rule
set answers the retired policies off and only the laboratory's baseline
answers them on); `session.TestReservedRuleSetsBindTheirSearchKernels`;
`pathlab.TestTheAdoptedSetIsModern` and `TestTheBaselineIsTheOverlapRules`;
in the retail tier `session.TestModernSealedOutUnitSettlesWhereItStands` and
`TestModernHeldGoalIsGivenTheNearestFreePlace`; the Strict, Community and
Modern `headless` fingerprint locks, which cover RNG and resource effects
over whole battles, the long Modern end tick included
(`tools/check-retail --full`). Each part's own tests are listed with it.

#### Modern steering

**Nanolathe Modern policy.** A ground mover looks ahead along the heading it
wants and, when its footprint could not stand there, wants the nearest
heading to either side along which it could. It turns past what stands in
its way before it reaches it, instead of driving into it, halving its speed
and waiting for a route around.

**Strict 3.1 behavior.** The follower wants the heading to its waypoint and
nothing else `[04 R-MOV-01 §2]`. What is in the way refuses the step when the
unit reaches it `[04 R-COLL-01 §1]`, and what looks like going round emerges
from the follower's repath and the class layer's occupant age
`[04 R-COLL-01 §7]` `[04 R-MOV-01 §7]`.

**Modern behavior.** On each visit of a grounded mover that holds an order
and a waypoint, more than 64 world units from the end of its route
(`System.steerAround`):

1. *Look.* The footprint is walked along the wanted heading from the unit's
   position, sampled four times a cell, for three cells, and no farther than
   the point of its route the unit is making for: what lies beyond that
   point lies off the route, which turns there. Each anchor is tested as the
   commit validator would test it, static ground first and then the
   occupant `[04 R-COLL-01 §2]`.
2. *Keep the way wanted* when the footprint can stand along all of it. One
   exception: a mover whose last step was refused, and which chose a way
   round within the last 24 ticks, keeps that way while it is free. A unit
   pressed against something creeps, and the way it wants looks free on one
   visit and held on the next.
3. *What is ahead decides who steers.* A friendly mover going the same way
   and making way is followed, however slow. A friendly mover crossing is
   passed behind, on the side it comes from. A friendly mover coming the
   other way, its heading 120° or more from the unit's, is passed on the
   right, a rule both units apply, so they part without either knowing the
   other's choice. A friend at rest or held is passed on the side its centre
   is not on. Anything else — ground, a feature, a structure, another
   player's unit — is passed on the right unless a side has been chosen.
4. *Choose.* Headings a sixteenth of a circle apart are tried, up to a
   quarter turn to either side, nearest first and the preferred side first;
   a mover that has chosen a side within the last 24 ticks tries that side
   through before the other. The first along which the footprint can stand
   for the whole look ahead is wanted, and its side is kept for 24 ticks.
   When none is free the mover keeps the heading it wants, and the commit
   refuses its step as retail does.
5. *A waypoint passed beside is taken.* A mover that is steering round
   something, whose last step was not refused, and that is within 56 world
   units of the point it is making for and past it along the leg that
   follows, takes the point as reached: the ordinary test wants it within
   five world units `[04 R-MOV-01 §3]`, and a unit that went round something
   standing on the point would turn back for it.

Steering never brakes and never changes a speed. The wanted heading goes
through the unit's own turn rate as any wanted heading does.

**Boundaries.** The last 64 world units of a route belong to the arrival
rules, and steering is off there. Steering reads the real ground three cells
ahead whether or not the owner has mapped it, as a unit's own commit does;
the route search stays blind to unmapped ground, and
[learned terrain](#modern-learned-terrain) still teaches by the steps that
are refused. A unit with no order, or braking with no waypoint, does not
steer. Rejected as measured: overtaking a slower friend, turning to the
heading with most room when none is free, a longer look ahead, finer
headings, damping, and waiting a few ticks before turning.

**Verification.** `movement.TestSidestepGoesRoundAParkedFriend` (the side a
friend at rest is passed on), `TestSidestepLooksNoFartherThanTheWaypoint`,
`TestWaypointBesideIsTakenFromAUnitGoingRound` (taken from a unit that is
steering; not from one whose side was chosen long ago, that is not steering,
or whose last step was refused), `TestWaypointPassingStopsAfterARuleSwitch`
(Strict, Community and an unbound system ignore retained steering when
consuming a corner; switching back to Modern resumes it without RNG draws),
`TestRefusedMoverKeepsItsWayRound`, and the
contract tests above.

#### Modern route smoothing

**Nanolathe Modern policy.** After [route straightening](#modern-route-straightening)
a finished route is pulled taut: a run of turns is replaced by one straight
leg wherever the mover's footprint can stand all the way along that leg.

**Strict 3.1 behavior.** The retail search turns by eighths of a circle and
publishes the cells where its grid path turns `[04 R-PATH-01 §3]`
`[04 R-PATH-01 §7]`, so a route runs up to a tenth longer than the ground
requires with nothing in the way; the mover itself steers at any heading
`[04 R-MOV-01 §2]`. Strict 3.1 and Community bind `path.RetailKernel`.

**Modern behavior.** The Modern rule set binds `path.SmoothKernel`, which
opens the straightening kernel's search unchanged. On the slice that search
finishes, for a route of 3 to 64 points, it walks the route once: from each
point it keeps, it takes the farthest later point it can reach in a straight
line no longer than 1,024 world units. A leg is judged by samples eight world
units apart along it and six to either side, since a mover does not hold a
line exactly, each at the anchor a mover's commit would hold there: the
commit adds half a cell before it divides `[04 R-COLL-01 §1]`. A leg reads a
friendly unit that is on its own way somewhere as absent, which the search
does not (`Traffic.LegsThroughMovers`): it will not be standing there when
the unit arrives. Friends at rest or at work, other players' units,
structures, features and ground are read as the search reads them. Under
[route claims](#modern-route-claims) a leg may not cross ground dearer than
the stretch it replaces. At most 4,096 anchors are probed for one route, and
each is charged to the player's work like a heap pop `[04 §7.3]`. The result
is published on that same slice, so publication timing is retail's.

**History.** [Route straightening](#modern-route-straightening) records that
a general line-of-sight shortcut was measured and rejected on authored
chokes, where it sends each unit of a column at a gap from its own angle.
That finding stands, and the authored scenes still show it
("Cost to the player" above). It is adopted on the evidence of recorded
games, where it is the largest single gain of the seven parts and improves
Modern before by itself, with steering, route claims and arrival places to
part the units it brings together.

**Boundaries.** The first and last points and the route's status are never
altered. The kernel is zero size and keeps no state across requests.

**Verification.** `path.TestSmoothPullsADogLegTaut`,
`TestSmoothKeepsATurnRoundAWall`, `TestSmoothLeavesLongLegs`,
`TestSmoothLeavesShortRoutes`, `TestSmoothReadsTheLegView` (a leg crosses a
friend on its own way, the search does not),
`TestSmoothJudgesALegAtTheMoversAnchors` (a leg half a cell from a wall is
judged where a mover would stand) and
`TestSmoothKeepsToGroundTheSearchAccepted`;
`session.TestReservedRuleSetsBindTheirSearchKernels`;
`session.TestRuleSetImplementationsAreZeroSizeOrPointers`.

#### Modern search weight

**Nanolathe Modern policy.** A route search weighs its heuristic at one and a
half while route searching is idle and at three while it is busy.

**Strict 3.1 behavior.** The scheduler hands each search its player's
weight: one and a half times six, three or one, chosen anew every 150 ticks
by how much the scheduler polled for that player against the unit limit, the
heaviest for the least `[04 R-PATH-01 §6]` `[04 R-PATH-01 §10]`. A lone
unit's search is the greediest, and a crowd's searches look at the most
ground when there is least time for it.

**Modern behavior.** `System.searchUnder` replaces the weight it is handed
with `Traffic.HeuristicScale`, 1.5 in 16.16, and with `Traffic.BusyScale`, 3,
while the search work charged a tick, averaged over the last eight ticks,
is above `Traffic.BusyWork`, 3,000 units. The average is kept on the `System`
and brought up to date at the start of every tick; the scheduler's admission,
budgets and charges are retail's.

**Measured.** The weight is where route quality and tick cost trade. At 1.5
always, a tick of the 1,500-unit benchmark costs 5.75 ms and its 99th
percentile 14.15; following the load, 3.97 and 7.82, for three ticks a trip
on the tuning moments (−68 ± 7 against −71 ± 8). Lighter than 1.5 was worse:
the shortest routes put a whole group on one line.

**Boundaries.** Every search of a session is weighed alike, whoever asks.
The busy test reads work already charged, so it cannot disagree between
hosts.

**Verification.** `movement.TestBusyScaleWeighsSearchesUnderLoad`.

#### Modern prompt re-routing

**Nanolathe Modern policy.** A follower may ask for a route again fifteen
ticks after its last admission. It replaces
[re-route staggering](#modern-re-route-staggering).

**Strict 3.1 behavior.** The follower's poll admits an armed follower when
`lastRequestTick + 60 <= currentTick`, inclusively `[04 R-MOV-01 §7]`.
`StrictRules.RepathDelay` answers sixty; Community inherits it.

**Modern behavior.** `ModernRules.RepathDelay` answers fifteen for every unit
and tick. The stamp, the inclusive comparison, the flag's writers, the
ten-tick goal-install reset `[04 R-PATH-01 §8]`, the scheduler's single
active request, its charges and its budgets are retail's, and staging and
admission share `System.repathDue` as before.

**Measured.** On the tuning moments −53 ± 8 ticks with it against −27 ± 10
without, the other parts as they were then. Eight and four ticks were no
better than fifteen. It about doubles the number of searches.

**What became of the stagger.** Staggering spread a cohort's re-requests over
eight ticks so that the searches of units admitted together did not fall on
one tick. Under this policy a cohort comes due every fifteen ticks instead,
[bounded path work](#modern-bounded-path-work) and
[group-order spreading](#modern-group-order-spreading) still bound what one
tick admits, and the search weight doubles while searching is busy. The tick
cost above was measured so.

**Boundaries.** It keeps no state, so saves, restores and switches need
nothing: a follower stamped under either mode is judged by the mode bound
when it is polled.

**Verification.** `movement.TestModernRepathDelayIsHalfASecond` (fifteen for
every unit and stamp; refused at `last+14` and admitted at `last+15`; Strict
after a switch refuses at `last+59`), `TestStrictRepathDelayIsRetailsSixty`,
`TestModernPollHonoursTheRepathDelay`, `TestRepathDueDoesNotAllocate`.

#### Modern route claims

**Nanolathe Modern policy.** A route search is told where friendly traffic is
about to go. Two streams that meet plan lanes beside each other instead of
one through the other, and units that lose least by going round are the ones
that go round.

**Strict 3.1 behavior.** A search knows the ground and the units standing on
it `[04 R-PATH-01 §2]` `[04 R-PATH-01 §14]` and nothing of where any unit is
going. Every member of a group is sent down the same line and through the
same gap.

**Modern behavior.** Every four ticks each grounded mover that holds a
searched route claims the ground that route crosses over its next 768 world
units (`ClaimsPilot.BeginTick`): per block of 32 world units, the sector of
the search's eight the route crosses it in. The goal installer's straight
line `[04 R-PATH-01 §8]` claims nothing. When a search is opened it is given
an extra cost of a step (`path.SearchConfig.CostDir`): four for each claim a
unit of the requester's owner holds on the step's anchor along the step's
way, and twenty-four for each claim against it, three sectors or more off,
each counted to six; the requester's own claims are left out. A cardinal
step costs sixteen. The cost joins the terrain term the anchor's first
allocation stores `[04 R-PATH-01 §3]`. A slow-turning unit, one whose
authored turn rate is below 700, pays only for the claims of other
slow-turning units: vehicles keep the direct way and walkers go round them.
The stock walkers turn at 900 and more and the stock vehicles at 512 and
less.

**Measured.** On the tuning moments −71 ± 8 ticks with claims against
−62 ± 9 without. A first version that charged every claim alike cost time on
recorded games; the direction is what made it pay.

**Boundaries.** Claims are per owner: allies' routes are not counted. They
are counted afresh from the routes units hold, so nothing is remembered that
a save would have to carry, and a restored game counts on the ticks the
saved one would have. Nothing here steers, slows or moves a unit.

**Verification.** `movement.TestClaimsChargesForAFriendsRoute`,
`TestClaimsChargeMoreAgainstTheWay`, `TestClaimsRankExemptsSlowTurningUnits`
and `TestClaimsIgnoresTheStraightLineFallback`;
`path.TestExtraCostTurnsARouteAside` and
`TestSmoothKeepsToGroundTheSearchAccepted`.

#### Modern arrival places

**Nanolathe Modern policy.** Every plain ground move has a place: the
footprint its unit will stand on. Units sent to one point are given places
round it, a unit whose place is taken is given another, and a unit that
cannot reach its place settles where it may.

**Strict 3.1 behavior.** A unit's goal is the point it was sent to. Units
ordered to one exact point all make for it, the first takes it and the rest
press toward a held cell; a move whose goal cannot be had retries for as long
as its record is the last primary one `[04 R-ORD-01 §4]`.

**Modern behavior.** Only `Move_Ground` records with no target, on finished
grounded mobile units, are given places (`ArrivePilot`). A proven
[constructor patrol return](DESIGN_UNITS_ORDERS_COB.md#modern-patrol-work) is
excluded: its destination is the saved acquisition position, and settling
elsewhere cannot establish that the helper returned.

1. *Places.* At the start of a tick, before any order installs its goal,
   the plain moves ordered since the last tick are gathered by owner and
   point. Units of one owner ordered on one tick to one exact point, as
   separate orders the command boundary did not spread, are given a block of
   footprints centred on the point: its rows across the way the group
   travels, the units at the front taking the far rows and keeping their
   order across the way, and then any two members of one footprint whose
   straight lines cross exchanging places only when both new footprints
   remain passable under their recipients' movement profiles and the owner's
   terrain knowledge. Equal footprint size alone does not permit a swap
   between, for example, land and amphibious units. A unit sent alone to a point
   another live order reserves, or a parked unit or the ground holds, is
   given the nearest free footprint within ten cells. A group the command
   boundary gave [destination slots](DESIGN_INTERFACE_HUD_INPUT.md#modern-group-destination-slots)
   keeps them. A move first seen after it has begun — the computer player's,
   one that waited behind another order, any move after a load — keeps the
   goal it has for its place.
2. *Exchange.* A unit within 160 world units of its place looks at it every
   eight ticks. When the ground or a parked unit holds it, the unit is given
   the nearest free footprint.
3. *Settling at the edge.* A unit within 160 world units of a place that is
   free, that has come no nearer for twenty ticks, and whose way is held by
   something that will not move of itself — its last step was refused by
   ground, a structure or a unit at rest, or it holds no route — takes the
   footprint it stands on for its place. A friend passing in front of it
   does not make it settle.
4. *Settling short of a sealed goal.* A unit farther than that from its
   place, that has stood within 48 world units of one point for 150 ticks,
   and whose goal the ground closes off from where it stands — the
   goal-sealed probe of [unreachable moves](#modern-unreachable-moves), which
   reads every mobile unit as absent, asked at any distance from the ground
   nearest the goal — takes the footprint it stands on for its place. Those
   are the units the first to reach that ground keep from it.

An order makes at most six such changes. Each is an ordinary goal install:
the order's goal is written and the point goal installed exactly as the
order's own handler does, the follower re-plans at its next activation, and
the move then ends by the ordinary arrival.

**Measured.** On the tuning moments places and exchange are worth nine ticks
a trip (−62 ± 9 against −53 ± 8) and settling at the edge twenty-two
(−90 ± 8 against −68 ± 7). Pairs of units heading for the same exact point
fall from 71,004 to 115. Settling short of a sealed goal costs a trip nothing
and, on the two maps of narrow passes, leaves 0.64 units a game standing on a
move order against 3.65 without it.

**Boundaries.** A place is free when the owner believes the ground passable,
no parked unit stands on it and no other live order reserves it; a parked
unit is a structure, or a unit at rest holding no route, waiting for none and
with nothing to do. Patrol, guard, attack, build and repair approaches keep
their own goals and their own failure handling, which is why a crowd sent to
attack what it cannot reach does not settle. A unit that settles reports
`Arrived`, as under unreachable moves.

**Verification.** `movement.TestArrivePlacesABlock`,
`TestArriveMixedProfilesKeepPassablePlaces` (crossing land and amphibious
units keep passable, distinct places; Strict and Community keep their goals),
`TestArrivePlacesASingleOffAParkedFriend`, `TestArriveExchangesATakenPlace`,
`TestArriveSettlesAtTheEdge` (a unit at rest that refused the step settles
the unit after twenty ticks; a friend on the move does not),
`TestArriveSettlesShortOfASealedGoal`,
`TestArriveSealedWaitsForAUnitThatMoves` and
`TestArriveAdoptsAMoveThatWaited`;
`session.TestModernConstructorPatrolPaidRepairRetail` locks a stock ground
constructor whose return previously settled roughly 100 units short; in the retail tier
`session.TestModernSealedOutUnitSettlesWhereItStands` (a unit parked units
seal out of its free place settles outside them without sharing a cell, a
hostile unit among them changes nothing, and Strict 3.1 keeps the retry) and
`TestModernHeldGoalIsGivenTheNearestFreePlace`.

#### Modern routes through friends

**Nanolathe Modern policy.** A unit whose searches have found nothing for
five seconds is searched for again with friendly units read as absent, so
that a crowd in a narrow place does not hold itself.

**Strict 3.1 behavior.** A route search reads a mobile unit that has stood
for a second as a wall `[04 R-PATH-01 §14]`, and a request whose setup finds
no ground nearer its goal than where the unit stands publishes nothing
`[04 R-PATH-01 §4]`. A unit friends stand round is left without a route for
as long as they stand; being left without one it stands, and is a wall to
the units behind it.

**Modern behavior.** When a ground unit's search for a live order finishes
rejected with no points, and the unit's own searches have found nothing
since at least 150 ticks (`Traffic.ThroughAfter`; the first such search
starts the count and a route its own search finds ends it), the request is
searched again at once under another reading of the ground
(`System.searchUnder`): first with the friendly units that have somewhere to
go read as absent, and, when that finds nothing either, with every friendly
unit read as absent (`Traffic.Through` is two). Friendly means the
requester's owner or an owner mutually allied with it. Another player's
units, structures, features and ground stay what they are. What the first
search cost is charged with the second. The unit follows the route as far as
its friends let it, since the commit refuses a step onto a held cell as
always, and waits behind one that is on its own way.

The failed-search wait is keyed by the installed goal's horizontal position,
including target-only assistance whose order stores no point. Reinstalling the
same position preserves the wait; switching to another position starts it anew.
Strict 3.1 does not use this wait.

**Measured.** After five seconds it costs a trip nothing (−85 ± 7 on the
tuning moments against −82 ± 8 without). Searched the first time a search
fails it costs 28 ticks a trip: friends setting off leave a unit among them
without a route for a moment, and that mends itself.

**Boundaries.** A request whose own search finds a route is not touched.
Nothing here moves a unit or enters a held cell. Asking the friend that
refuses such a unit to stand aside was measured and left out.

**Verification.** `movement.TestThroughPlansThroughTheFriendsThatBoxAUnitIn`,
`TestThroughWaitsBeforeItPlansThroughFriends`,
`TestThroughKeepsAnotherPlayersUnits`, `TestThroughLeavesAFoundRouteAlone` and
`TestTargetOnlyGoalRestartsThroughWait`.
