# Design — Units, orders and the COB machine

`internal/units`, `internal/orders`, `internal/cob` and `internal/model`. The
unit pool and the record every other simulation package reaches a unit through,
the death latch and the runtime status word; the sixty-eight order descriptors,
the two queue segments, the pump and its two result-code tables, and the handler
that runs each row; the compiled-script virtual machine, its eight threads, the
engine ports and the engine→script callbacks; and the 3DO piece hierarchy those
scripts animate.

This is one of the design documents listed by [ARCHITECTURE.md](ARCHITECTURE.md);
that document owns package boundaries, the tick, and the citation forms. A bare
`Cn` in these four packages is a contract of §3 below, numbered as its source
plan numbered it.

## 1. Purpose and boundary

These four packages answer four questions:

* **What is a unit?** One fixed pool, one record per live slot, one runtime
  status word, and a death that is a latch before it is a free `[04 §2.3]`
  `[04 §2.4]`.
* **What is a unit doing?** One descriptor table, two queue segments per unit,
  one pump, and one handler body per descriptor row — the order record is the
  only place a unit's intent lives `[04 §3.1]` `[04 §3.3]`.
* **What does the unit's script see?** Twenty engine ports, a fixed callback
  vocabulary, and a virtual machine whose scheduling — eight thread slots,
  sleeps, waits and signals — is itself behavior `[04 §4.2]` `[04 §4.4]`
  `[04 §5.1]`.
* **Where are the unit's pieces?** A piece hierarchy the script rotates and
  translates by name, composed into world transforms that never see the unit's
  position `[03 §2.4]`.

The most dangerous mistake here is conflating a descriptor's **static mask**
with a record's **dynamic gate**. The static mask is insertion metadata — which
segment the record belongs to, whether it was constructed with a target or a
goal, whether the row is nanolathe class `[04 §3.1]` `[04 R-DOC04-C]`. The
dynamic gate is what the record is *waiting for* `[04 §3.3]`. Seeding one from
the other parks a freshly issued order on a gate nothing will ever satisfy, and
because the pump papers over that for whatever the record's own handler clears
first, the failure is silent.

The boundary runs at six places:

* **The tick belongs to `internal/session`.** The per-unit sweep is the second
  phase; the orders and work pump runs inside the fifth, per player ascending
  `[01 §4.4]` `[04 R-MOV-03 §1]`. `internal/units` publishes the traversal — the
  pre-update stage at the front of a visit, the death finalizer at slot end —
  and the session composes the stages between them. None of these packages reads
  a clock.
* **Weapons belong to `internal/combat`.** The unit record carries the three
  weapon slots because the record is where retail keeps them and because
  `internal/combat` imports this package, not the other way round; the slot's
  reload, targeting and firing decisions are combat's `[06 §1.2]`
  `[06 R-WPN-05 §3]`. What `internal/cob` owns of a weapon is the aim
  handshake's completion latch `[04 §5.3]` `[06 §3.3]`.
* **Movement belongs to `internal/movement` and `internal/path`.** An order row
  installs a goal payload through the movement adapter the queue binding
  carries and reads the route's outcome back as pending bits; it never steers
  `[04 §7.2]` `[04 R-PATH-01 §12]` `[04 R-AIR-01 §4]`.
* **Construction belongs to `internal/construction`.** The build rows are order
  records in the primary segment, and the construction service registers their
  bodies on the queue it owns; this package defines no second queue and no
  second record `[04 R-FAC-02 §4]` `[05 "Factory production lifecycle"]`.
* **Presentation is a seam, never a caller.** `emit-sfx`, the piece-flag writes
  and the acknowledgement captions are published through optional sinks; a nil
  sink means no effect and changes no authoritative state `[04 R-COB-03 §6]`
  [I6].
* **The model's draw geometry is `internal/render`'s.** `internal/model` owns
  the hierarchy, the load-time reordering and the transform composition; what
  is done with the resulting vertices is presentation's `[03 R-RAST-01 §2]`
  `[03 R-COMP-02 §3]`.

## 2. Packages and key types

### 2.1 `internal/units`

**The record** (`units.go`, `types.go`). `Unit` is one live instance. Retail's
record has a fixed byte identity; Go stores named fields in a slot-indexed array
parallel to the pool and reproduces no packed layout [I13] `[04 §2.3]`
`[04 R-P0-16-A]`. `World` holds the pool, the ten per-player slices, the
catalog, the terrain and visibility handles, and the creation/death/capture
hooks the session installs.

**The runtime status word.** `Unit.Flags` is retail's one status word and the
constants beside it are its named bits: the classifier-eligibility bit the
allocator initializer sets and death finalization clears, the selection bit,
the building-class and armed bits written once from the definition, the two
occupancy-overlap bookkeeping bits, the mission immunity bit, the cloak-request
bit, the two two-bit standing-order fields, the build-page indicator and page
number, and the static `isairbase` mirror the selection predicate reads off a
*carrier* `[04 R-UNIT-06 §3]` `[04 R-STANCE-01 §2]` `[04 R-COLL-01 §4]`
`[07 §9]`. Adding a bit here without a located writer is how dead branches get
built; every constant in that block names its writer.

**The order-event word.** `Unit.Pending` is the word the pump merges with each
record's own satisfied bits before intersecting the record's gate
`[04 §3.3]` `[04 R-ORD-01 §0]`. Its producers are the damage-intake observer
notice, the weapon layer's fired/could-not-fire bits, the under-construction
wait, and the script-touched marker `[06 R-WPN-05 §6]` `[04 R-MOV-03 §7]`
`[04 R-ORD-01 §11]` `[04 R-COB-06]`. Retail merges it as a zero-extended
sixteen-bit value, which is why gate bit `0x10000` can only ever be satisfied
from a record's own word — the standing question of what writes bit 16 into the
*capability* word answers itself: nothing can `[04 R-ORD-01 §0]`
`[06 R-WPN-05 §6]`.

**Creation and the death latch** (`units.go`, `sweep.go`). `Create` allocates
lowest-free with slot 0 null, seeds the status word from the definition, takes
the allocator's random draws in retail's order — the build angle, then the hover
bob phase — samples the extractor yield over the stamped footprint once, and
binds the script `[01 §6.1]` `[04 R-MOV-01 §5c]` `[04 R-P28-ANG-01R §2]`
`[05 R-PROD-01 §6]`. `Destroy` sets `Dying` and records the cause; `Alive`
survives until the slot-end finalizer, so a unit killed in the projectile phase
is still visible to every later phase of the same tick `[04 §2.4]`.
`DeathCauseFromKind` derives this build's coarse label from retail's damage-kind
byte rather than casting it, because the two enumerations do not share their
numbering `[06 §12.1]` `[08 R-SAVE-02 §6]`.

**The sweep** (`sweep.go`). `VisitActiveSlots` is the one deterministic
traversal: players 0..9 ascending, then slots ascending inside each player's
slice, with the alive flag read at the moment the slot is reached — so a unit
created ahead of the cursor is visited in the same tick and one created behind
it waits `[01 §6.2]` `[04 R-MOV-03 §1]` [I1]. `StepPreUpdate` is the front
boundary and `FinalizeDeath` the slot-end boundary; the session runs weapon,
script, order and movement work between them.

**The script binding** (`cob_binding.go`). One production binding per unit: the
compiled program, the model piece list linked against the script's piece table,
the per-unit port handlers, the callback bridge and the simulation stream. A
definition whose program is missing is rejected at catalog compile rather than
allowed to create a scriptless unit, because retail faults at creation instead
`[04 R-COB-04 §8]` `[04 R-COB-01 §3]`.

Catalog linking retains both that immutable program and the winning COB
provenance. Unit creation passes this asset into strict binding, which links the
piece names and allocates fresh VM threads, statics and piece state without
rereading or recompiling the script. A definition from another catalog or VFS
overlay carries its own provenance and program; mutable VM state is never
shared `[04 §4.1]`.

**Save boxes** (`retail_save.go`, `retail_restore.go`). The detached unit image
and its restore, including the fields whose consumers belong to later phases and
the three packed status bits retail copies from its writer stack and that no
runtime state can supply — those are an explicit scratch argument, never
silently zeroed `[08 R-SAVE-UNIT-01]` `[08 R-SAVE-02 §6]` `[08 R-SAVE-02 §14]`.

### 2.2 `internal/orders`

**The descriptor table** (`table.go`). `Descriptor` carries the canonical name,
the interface state label, the overlay draw mask, the acknowledgement/icon byte,
the static gate mask, the goal-resolution presentation-helper identity, the
handler, and a `Driver` value that names what advances a row with no handler of
its own `[04 §3.1]` `[04 R-DOC04-C]` `[07 R-P0-11 §3]`. The table is built from
four static batches of 23, 22, 22 and 1 records, re-sorted after each batch;
`Lookup` is a binary search with the same comparator `[04 R-STANCE-01 §9]`.

**The record** (`pump.go`, `node.go`). `Node` is one order record: descriptor
identity, handler-private phase byte, dynamic gate, absolute deadline (−1 for
none), owner and target handles, the 16.16 goal triple, the guard anchor and
cached target position, three general parameters, the static-mask copy with its
runtime bits, the creation tick, the accumulated satisfied word and the flag
bits `[04 §3.2]`. `NewNodeForOrder` and its typed wrappers are the one
constructor every producer enters through — the HUD, the computer player, the
mission spawner, COB, rally inheritance and factory completion — so the queue
modifier, the goal payload and the build product identity are formed in one
place `[04 §3.4]`.

**The two segments** (`pump.go`). `Queue` holds a primary and a secondary
slice, the per-queue binding, the per-queue diagnostics, and the per-row owned
handlers. The rear segment is exclusively `BuildWeapon` and `SelfDestruct`,
selected by the descriptor's rear-segment static bit `[04 §3.1]`
`[04 R-ORDER-02 §1]`. Storage is dynamic: the stock corpus reaches queues
longer than any fixed cap (SC17), so the only bound is an out-of-memory guard
far outside stock.

**Producer insertion and the active marker** (`pump.go`). `Push` is the
producer-side insertion. It constructs the record, drops leading auto/default
records when the new record is not rear-segment bound, arms the one-shot
caption-pending flag on a non-queued (Replace) issue only, and then takes one of
two branches on the new record's static-mask copy: head-insert into the segment
the rear-segment bit selects, inheriting the displaced head's auto flag and
writing no marker at all; or insert immediately after the active marker — the
tail when nothing carries it — and move the marker onto the new record
`[04 R-ORD-01 §13]`. The marker has exactly that one writer. Removals do not
hand it on: `releaseMarkerOnRemoval` only clears a duplicate, because freeing the
record that carried it leaves the segment unmarked and the next insertion then
appends at the tail. `PushHead` is the handler-side spawn, which writes the
link, the owner and the inherited auto flag and nothing else.

**The queued-order toggle.** `CancelFrontMost` is the removal half of retail's
Shift-click duplicate test: it walks the primary segment front to back, unlinks
the first whole matching node — no count decrement — and reports that one went,
in which case the producer issues nothing `[07 R-P0-11 §6]`. The match itself is
the session's, which supplies the resolved order identity, the optional target
and a whole-cell inclusive per-axis tolerance.

**The pump** (`pump.go`). `Pump` runs the primary walk, then the secondary walk;
the two are adjacent calls with no test between them, and a blocked front head
stops only its own segment `[04 R-ORD-01 §10]`. The primary walk is head-only: after every
non-returning result code it reloads the front record and re-applies the gate
test, so "continue" never means "visit the record behind this one"
`[04 R-ORD-01 §10]`. Dispatch is one seam — the descriptor's handler, or the
owned handler a subsystem registered on this queue — and the pump applies the
gate test, the two-sided bit consumption and the gate clear identically for both.
`applyPrimaryResultCode` and `applySecondaryResultCode` are deliberately
separate tables: the segments share the code values and not their effects.

**Handler registration** (`table.go`, `queue_handlers.go`). Retail compiles a
handler into every static descriptor. This build assembles the same table from
files that cannot all initialise before the table does, so each family owns an
installer and `handlerInstallers` is the single ordered list that runs them — a
slice, not a map, because registration order is a contract [I1]. Every installer
assigns only where a descriptor's handler is still nil, so the list is
idempotent and its order is what settles a row two families both name.

Rows whose bodies belong to a package `internal/orders` cannot import register
on the queue instead. `OwnedHandler` returns a result code and a boolean: `true`
means it ran the row's body and the pump applies the code through the ordinary
epilogue; `false` means the registering subsystem advances the record from its
own per-unit step and owns the record's phase, gate and deadline, so the pump
writes none of them and ends the pass. That second form is not a courtesy —
applying a result code over a live state machine parks a factory's record for
30 to 44 ticks mid-build. `SetGetBuiltHandler` is the named form for the one row
whose owner always advances it inside the pump visit; `SetExternallyDrivenHandler`
is the second form's shorthand. An owned record is not a missing handler and is
not diagnosed as one.

**The handler families.** `standing.go` (the trivial, standing, cloak, wait,
paralyze, teleport and standby rows), `selfdestruct.go`, `work.go` (capture,
reclaim, resurrect, assist and the repair trio), `vtolwork.go` (the five VTOL
work twins), `combat.go` (the stationary attack rows and the stationary guard),
`vtolair.go` (the air executors and the four air-attack rows), `patrol.go` (the
queued-move pair, both ground patrols and the two air moves), `transport.go`,
`park.go`, `stop.go`, `activation.go`, `selectable.go` and the stockpile row.
`resolve.go` owns command resolution and the chase/guard rows; `scans.go` owns
the deterministic visitors those rows share; `callbacks.go` owns the removal
cleanup callbacks.

**Idle refill** (`pump.go`). A primary segment that empties is handed the
definition's `defaultmissiontype` as an auto-flagged head insert, gated on the
owner's controller state being one of the two active values, and the pump
returns — the record is dispatched on the next visit, never the same one
`[04 §3.3]` `[02 R-KEYS-01 §1]`. Every stock aircraft authors `VTOL_Standby`
there, which is how an idle aircraft comes home.

**Save boxes** (`retail_save.go`, `retail_restore.go`). The writer walks the
primary segment then the secondary, continuing one sequence across both, and
carries the handler-private phase byte verbatim because handlers own its
interpretation `[08 R-SAVE-ORDER-01]` `[08 R-SAVE-02 §6]`.

### 2.3 `internal/cob`

**The program** (`load.go`). `Program` is the immutable compiled script: the
opcode word array, the script-name map, the script index array in table order,
the ordered piece-name table, the static count and the content checksum
`[fmt cob]` `[02 "Compiled script archive (COB)"]` `[04 §4.1]`.
The declared static count has an explicit host-safety byte budget because it is
not backed by a file span; parser, external binding and restore validate it
before mutable VM storage is allocated. The budget is not a retail constant
`[fmt cob "Header"]`.

**The machine** (`vm.go`). `VM` is one unit's instance: eight `Thread` records,
the piece animation state, the statics, the bound ports and sinks. A thread
carries its status, its word-indexed program counter, its window, its stack
pointer, its sleep counter, the piece and axis it waits on, the thread it is
blocked behind and its signal mask `[04 §4.2]` `[04 §4.3]`. `Start` allocates
the lowest free slot; `Call`/`CallQuery` is the synchronous query form that
leaves a blocked thread allocated; `Signal` terminates threads by mask; `Drain`
runs due threads in fixed slot order and then one piece-interpolation pass
`[04 §4.6]` `[04 R-COB-02 §2]`.

**Ports** (`ports.go`). `PortTable` is the twenty engine ports, verbatim, read
column and write column `[04 §4.4]` `[04 R-COB-03 §1]` `[04 R-COB-03 §2]`. Reads
outside 1..20 answer zero. Writes bind exactly six arms — activation, in-build
stance, busy, yard open, bugger off, armored — and an identifier with no arm
still sets the script-touched marker, as does every arm `[04 §4.7]`
`[04 R-P0-10]` `[04 R-COB-03 §4]`. That marker is not opaque: it is the unit's
pending bit `0x4`, and the order pump's satisfied merge is its only consumer, so
a script's engine write is the only thing that re-polls the in-build-stance and
transport-busy waits `[04 R-COB-06]`.

**Callbacks** (`ports.go`, `bridge.go`). `CallbackBridge` is the one bridge per
unit, retained for its life so that sinks a caller installed survive a callback
run. The arithmetic is here: the rock and hit-by-weapon impulse arguments
through the shared fixed-point trig table, the local death-severity query, the
reload-time conversion, and the two synchronous query seeds `[04 §5.1]`
`[04 §5.3]` `[04 R-CB-01 §2]` `[04 R-CB-01 §3]` `[04 R-CB-01 §4]`.

**The aim handshake** (`ports.go`). `AimSlot` is the seam between the script and
the weapon: an issue bit set immediately after an `Aim*` start, which gates
re-issue and authorizes nothing, and a ready latch granted only by a nonzero
value delivered to the completion receiver. A failed start delivers zero through
the same receiver; a signal or abnormal termination never invokes it
`[04 R-CB-01 §6]` `[04 R-COB-04 §9]` `[06 §3.3]` `[06 R-P0-07]`.

**The binding** (`binding.go`). `BindingRequest` is the strict production bind:
program, model, piece list, required entry points, streams and sinks. Its
diagnostics are coded, not prose-matched, so composition and asset preflight can
classify a missing program, a piece-count mismatch or a failed `Create` start
without parsing text.

**Save boxes** (`retail_save.go`, `retail_restore.go`). The per-piece image and
the thread windows. Two of the writer's per-piece words are uninitialized frame
residue in retail — a value that is not a function of game state and therefore
has nothing to clone — so they are a caller-owned scratch argument
`[08 R-SAVE-02 §9]`.

### 2.4 `internal/model`

`Piece` is one 3DO object: name, parent, children, the authored parent
translation and the vertices, both in 16.16 after the half-turn pass, and the
primitives after the load-time reorder `formats` already applied `[03 §2.4]`
`[fmt 3do]`. `Model` is the immutable hierarchy. `PieceState` carries the three
`uint16` rotation accumulators and the script translation lanes; `Compose`
builds a piece's world transform by composing ancestors after descendants. A
leaf with a vertex and no primitive is a valid attachment and emission point,
not a defect.

## 3. Contracts

### 3.1 Units — C1…C3

**C1 — record identity, not layout.** Retail's unit record has a fixed byte
identity; Nanolathe uses named Go fields in a slot-indexed array parallel to the
pool and never packs or reinterprets a Go struct as that record. Allocation is
lowest-free with slot 0 null, no generation tags, immediate reuse
`[01 §6.1]` `[04 §2.3]` [I13].

**C2 — the sweep order, and alive versus dying.** The tick sweep visits players
ascending then slots ascending `[01 §6.2]`. Alive state and the death mark are
separate fields: `Destroy` sets the mark and the slot-end finalizer clears alive,
fires the death hook exactly once and frees the slot `[04 §2.4]`
`[04 R-MOV-03 §1]`.

**C3 — remaining, not progress.** Build progress is a `remaining` fraction
running 1 → 0. It is `float32` on the I2 allowlist, and `internal/construction`
is its only writer `[04 §2.3]` `[05 "Construction target state"]`.

### 3.2 Orders — C4…C9

**C4 — the descriptor table.** Four static batches of 23, 22, 22 and 1 records;
after every batch the whole table re-sorts ascending by canonical name, and an
order's identity is its index in the final sorted table. The empty name sorts to
0 and is the reject sentinel `[04 §3.1]` `[04 R-DOC04-C]`. Names, labels,
classes, acknowledgement bytes and gate masks are transcribed, never derived.

The comparator is **case-insensitive**, the C runtime's fold, and this corrects
the contract as PLAN 06 wrote it: it said a case-sensitive byte comparison, and
that put seven rows in the wrong order — the `Attack*` cluster and the `Build*`
pair invert under the fold, because lowercasing moves the underscore below every
letter instead of between the two letter ranges. A case-sensitive sort under a
case-insensitive search does not merely disagree: the search cannot find
`Attack_Chase` at all, which disables the chase attack `[04 R-STANCE-01 §9]`
`[04 §3.5]`.

The small class byte's reader is also now known, which retires PLAN 06's
standing question about it: it is the order-queue overlay's draw mask, and the
Shift-gated overlay walker dispatches five helpers off it — the build-site
marker, the travelling dash chain, the segmented circle, the queued-order icon
and the labeled range rings. Every observed value lies inside the five-bit mask
`[04 §3.1]` `[07 R-P0-11 §3]`. The acknowledgement byte is the same field
`[07 R-P0-11 §3]` calls the descriptor's icon byte; icon 0 encodes "no icon".

**C5 — the order record.** Retail's record has a fixed byte identity; Go uses
the named fields and per-family parameter meanings of `[04 §3.2]`, not a packed
layout [I13]. A unit keeps a primary segment and a rear segment, the rear
exclusively for `BuildWeapon` and `SelfDestruct`, each with its own anchor
`[04 §3.1]` `[04 R-ORDER-02 §1]`.

**C6 — the pump's gate test.** An arrived deadline clears and raises the record's
lowest satisfied bit `[04 R-ORD-01 §0]`. Satisfied is `(record.satisfied |
unit.pending) & record.gate`; a **nonzero gate with nothing satisfied stops the
walk for this tick**, stalling everything behind it in its own segment; the
rear segment's walk follows regardless `[04 R-ORD-01 §10]`. Otherwise the delivered bits are consumed from both
words, the dynamic gate is cleared, and the handler runs `[04 §3.3]`.

The record constructor does **not** seed the dynamic gate from the descriptor's
static mask. They are different fields with different meanings, and conflating
them parked every row whose handler did not happen to clear the gate first. With
the constructor correct, a fresh record's gate is zero and step 3 passes on its
own; the pump's former phase-0 pre-dispatch clear is gone with it, because
`[04 §3.3]` gives the pump exactly one gate clear — step 4's, on the record it is
about to dispatch — and clearing at phase 0 would discard a wait a handler armed
`[04 §3.1]` `[04 §3.3]` `[04 R-ORD-01 §1]`.

**C7 — the primary result-code table.** Code 0 resets the phase; 1 advances it;
2 and 4 continue, which under the head-only walk means reload the front record
`[04 R-ORD-01 §10]`; 3 arms the lowest gate bit and deadline `tick + 30 +
rand(15)`; 5 and 8 unlink and free; 6 rotates the record to the segment tail as
a link move only, writing no flag — the marker travels with the record that
holds it; 7 frees every record on both segments and returns; 9 sets the
completion flag and, on a record with nothing after it in the segment, resets
the phase and re-arms with `tick + 30 + rand(30)`, otherwise unlinks; anything
above 9 is the single-node expiry helper, with no draw and no whole-queue cancel
`[04 §3.3]` `[04 R-P0-01]`.

"Last" means having no record after it, which is not the same as being the only
record: a handler that head-inserted a spawned record is behind that record and
can still be the tail `[04 R-ORD-01 §1]`.

**C8 — the secondary segment and its table.** The rear segment holds only
`BuildWeapon` (gate `0xc0140`) and `SelfDestruct` (gate `0x40040`); factory
products live in the **primary** segment. It is walked front to back, skipping
records whose gate is non-empty and whose deadline has not arrived — the compare
is unsigned, so the −1 sentinel reads as not due — and the handler is invoked
with an **empty** satisfied set: no expiry bit, no capability-word read
`[04 R-ORDER-02 §1]`. Codes 6 and 7 both remove the single record and return,
with no tail-yield and no cancel-all; 5, 8, 9 and above 9 are plain
unlink-and-free removals that continue the walk, and secondary code 9 never
re-arms and never draws `[04 §3.3]` `[05 "Queue pumping and result codes"]`.

**C9 — insertion, coalescing, cancellation and cleanup.** Producer insertion goes
after the active marker and moves the marker onto the new record; the
head-insert branch, taken on the head-insert or rear-segment static bits, writes
no marker at all `[04 R-ORD-01 §13]` `[04 R-ORD-01 §16]`. Counted adds coalesce
tail-only; `CancelTailMost` matches the tail-most record and tombstones it;
`CancelFrontMost` is the queued-order duplicate removal, front-most first, whole
node, no decrement `[07 R-P0-11 §6]`.

Cleanup order on every removal path is restore → handler cancel notification →
`StopBuilding` → release payload → `TargetCleared` **only if not tombstoned**.
The tombstone is set on every freed record except the one standing as the
primary segment's front head at that moment, and a rear-segment record is always
tombstoned because the test compares against the front anchor regardless of
segment — so `BuildWeapon` and `SelfDestruct` removals never emit
`TargetCleared` `[04 R-ORDER-02 §2]`. The cancel notification is arbitrary work
and can re-enter the queue, so the unlink re-locates the record by identity
after the cleanup rather than trusting an index taken before it.

In queue-modifier terms Replace is a non-queued purge, Append and Shift-queue
both insert after the marker without purging, and the pump's idle refill is the
auto head insert `[04 §3.3]` `[04 §3.4a]`. Purge survivorship is the static
gate's second bit `[04 R-MOV-03 §6]`.

### 3.3 COB — C10…C19, C25, C26

**C10 — instruction encoding.** One 32-bit word; bit 28 is always set; the
dispatch key is the word masked with `0x100FF000`, which selects bit 28 plus the
eight bits the tabulated opcode values vary. The low twelve bits are ignored
except by push and pop, where the low three are the addressing mode `[04 §4.3]`.

**C11 — the dispatch set.** Exactly 57 dispatched values. An unmatched key takes
the kill path: clear the thread's status, decrement the instance's active thread
count, yield the drain. There is no default handler and no retail diagnostic
`[04 §4.3]` `[04 R-COB-01 §1]`.

**C12 — operand shapes.** Six shapes, and the program counter is a **word**
index `[04 §4.3]`.

**C13 — threads.** Eight thread records per unit; the lowest clear thread-mask
bit is selected and the scan order is fixed. Each record carries 32 physical
window words for save and restore, while authored opcode stack and local
operations enforce a separate ten-value semantic limit. A `sleep 0` still costs
one tick: the sleep occupies its truncated tick count plus one guard decrement
`[01 §6.1]` `[04 §4.2]` `[04 §4.6]`.

**C14 — retail's undefined behavior is reproduced, not defended.** An unknown
pop addressing mode pops nothing and advances; divide has no zero and no
minimum-integer guard and faults; there is no modulo and no shift opcode;
`start-script` with a full pool retains its arguments; `call-script` with no free
slot waits on a sentinel and leaks `[04 §4.3]` `[04 §4.4]` `[04 R-COB-01 §1]`.
The fault census is closed: retail kills a thread only for an unrecognized
opcode value and for the thread-return opcode, which also fires the completion
receiver; a piece index is never bounds-checked.

**C15 — ports and callbacks.** Engine ports are numbered 1 to 20, and the write
opcode binds exactly six arms — there is no stand-ground, no per-weapon and no
cloaked write port; the compiled form pushes identifier then value, so the
opcode pops the value first `[04 §4.4]` `[04 §4.7]` `[04 R-P0-10]`
`[04 R-COB-03 §1]`. The callback arithmetic `[04 §5.1]` `[04 §5.3]`
`[04 R-CB-01 §2]`:

* the rock impulse is `(−cos(rel) × 800, −sin(rel) × 800)`, both negative, with
  no completion receiver;
* the hit impulse is `(cos(dir) × 400, sin(dir) × 400)`, applied before the
  damage callback, where `dir` is the packet's direction byte shifted into the
  full angle domain;
* the local death query's severity is `((−health × 100) / maxHealth + prior) / 2`
  as an unsigned divide, clamped to 1..100, where `prior` is the **previous**
  30-tick window's health sample — the unit tick keeps two adjacent sample bytes
  and shifts the current into the prior at each boundary, so the query consumes
  the older of the two;
* the maximum reload time is reported as `trunc(maxReload × 1000 / 30)`, scanned
  over all three slots and issued after `Create` so it lands outside `Create`'s
  own immediate drain;
* the transport query seeds one negative sentinel and three zeros; the landing-pad
  query seeds four negative sentinels, candidates are tried in order and the
  first free one wins.

**C16 — aim-ready.** Granted **only** by a nonzero value delivered to the `Aim*`
completion receiver. A failed start — script name absent, invalid identity, all
eight thread slots occupied — delivers zero through the same receiver; signal or
abnormal termination never invokes it. Zero neither grants nor revokes. There is
no timeout: a weapon whose `Aim*` never completes nonzero stays unable to fire
until the producer clears the aim state and re-issues. This is retail
`[04 §5.3]` `[04 R-CB-01 §6]` `[04 R-COB-04 §9]` `[06 §3.3]`.

PLAN 06 listed "an absent script" as a third zero case; that case cannot arise.
Every creator runs the synchronous primary-weapon query through the VM reference
with no null test, so retail faults at unit creation instead. Nanolathe rejects
such a definition at catalog compile with the standard diagnostic shape and never
creates a scriptless unit `[04 R-COB-04 §8]` `[04 R-COB-01 §3]`.

**C17 — same-tick windows.** In order: unit update (queues the direction and
speed callbacks) → weapon update (queues target-cleared, the aim and fire
callbacks, the rock callback) → the normal drain (delta 1, eight thread slots
then one piece pass) → orders and build work → movement integration (the
immediate move-rate and occupancy callbacks) → slot-end death handling. Deferred
callbacks produced before the normal pass run in the same visit; an immediate
wake start performs its own all-slot delta-zero pass, which can run a later one
earlier `[04 §5.3]` `[04 R-COB-02 §2]` [I7].

**C18 — move-rate tiers.** Category 0 when the inhibit bit is set, the unit is
attached, or both magnitudes are zero; otherwise category 1 at or below the lower
definition threshold, category 2 above the lower and at or below the upper, and
category 3 above both. The start-moving callback is emitted before the tier
callback, with an immediate-drain barrier `[04 §5.3]` `[04 R-CB-01 §5]`.

**C19 — the effect vocabulary.** Vector types 0 to 5 are piece-direction
effects; the three point types are white smoke, black smoke and sub-bubbles;
anything above them is ignored. Presentation-only and visibility-gated
`[04 R-COB-03 §6]` [I6].

**C25 — two trig implementations, never shared.** Callback arguments use the
fixed-point path: the 512-entry table, cosine a quarter turn ahead, products
rounded to nearest before truncation `[04 §5.1]`. The model draw path uses float
trig (C21). Sharing one implementation between them is wrong in both directions
[I2].

**C26 — the normal-kind damage packet's callback order.** Against an active,
not-yet-dying victim: **health is subtracted first**; then the hit callback
starts asynchronously with the impulse of C15; then the damage callback starts
independently with the post-hit percentage `clamp(health × 100 / maxHealth, 0,
100)` as an unsigned division. Either starter can fail separately. Heal and
paralyze kinds skip the pair entirely, and lethal damage against a
movement-category-1 or -2 victim sets the death latch and returns with **no**
callbacks `[04 §5.1]` `[04 R-CB-01 §3]`.

### 3.4 Model — C20…C24

**C20 — load-time order is fixed at load.** After relocation and before any
draw: if the object declares a selection primitive, swap it with primitive zero
and rewrite the selection index to zero; bubble-sort the remaining primitives
ascending by the integer mean of their vertices' second coordinate. Then a
recursive pass negates the first and third vertex coordinates and the first and
third parent translations of every object — a half-turn about the vertical axis.
A per-frame sort does not reproduce retail's tie order `[03 §2.4]`.

**C21 — composition.** `world(v) = M_root · … · M_leaf · v` with `M_i = T(t_i) ·
R_i`: each piece rotates about its own origin **first**, then translates. The
per-node translation is the componentwise sum of the script translation lanes and
the authored parent translation. Rotation composes three `uint16` accumulators
applied Z, then X, then Y, evaluated in floating point with round-to-nearest —
**not** through the fixed-point trig tables, which serve simulation velocity
integration only `[03 §2.4]` [I2].

**C22 — one adapter for three verbs.** Turn, turn-now and spin converge on the
same accumulators: one angle per axis, last writer wins, no separate aim stage
`[03 §2.4]` `[04 §4.6]`.

**C23 — vertex-only leaves are real.** A leaf piece with a vertex and no
primitive is a valid attachment and emission point `[03 §2.4]`.

**C24 — the unit's attitude folds into the root piece.** Bank, heading and pitch
occupy the root piece's Z, Y and X angle slots respectively and compose as the
outermost factor of the chain. **Unit position never enters piece math** —
pieces transform around the model origin, and position enters only at final
screen placement. Projectile models reuse the identical helper with yaw in the Y
slot and pitch in the X slot, each carrying a constant negative half-circle
authored model-facing offset `[03 §2.4]` `[03 §5.2]`.

### 3.5 The handler census

Retail compiles a handler into all 68 descriptors. This table is the same census
for this build: 67 named rows plus the sentinel, each named exactly once, and no
row without an owner. A package test walks the built table and fails on any
named descriptor carrying neither a descriptor handler nor an owning subsystem's
queue registration, so the census is checked rather than asserted.

Two of the four in the last group carry a zero static gate and therefore
dispatch on sight — `SelfDestructFG`, which a live mission reaches through the
`InitialMission` interpreter, and `VTOL_Evade`.

| Owner | Descriptors |
|---|---|
| `standing.go` | `Cloak_Off`, `Cloak_On`, `Paralyze`, `Standby`, `Standby_Mine`, `Standing_FireOrder`, `Standing_MoveOrder`, `Teleport`, `Wait`, `WaitForAttack` |
| `work.go` | `Capture`, `HelpBuild`, `Reclaim`, `RepairUnit`, `RepairUnitNoMove`, `Resurrect`, `SelfRepair` |
| `vtolwork.go` | `VTOL_HelpBuild`, `VTOL_Reclaim`, `VTOL_RepairPatrol`, `VTOL_RepairUnit` |
| `combat.go` | `AttackSpecial`, `AttackUType`, `Attack_Kamikaze`, `Attack_NoMove`, `Guard_NoMove`, `Suppress` |
| `vtolair.go` | `AirStrike`, `AirToAir`, `AirToGround`, `AirToGroundHover`, `VTOL_Evade`, `VTOL_GetRepaired`, `VTOL_LandIfCan`, `VTOL_Landing`, `VTOL_SeekAttack`, `VTOL_SeekGuard` |
| `patrol.go` | `Patrol`, `QMove`, `QPatrol`, `RepairPatrol`, `VTOL_Move`, `VTOL_Patrol` |
| `transport.go` | `BeCarried`, `Ground_Pickup`, `Ground_Unload`, `VTOL_Pickup`, `VTOL_Unload` |
| `resolve.go` | `Attack_Chase`, `Follow_Ground`, `VTOL_Follow` |
| `selfdestruct.go` | `SelfDestruct`, `SelfDestructFG` |
| `pump.go` | `Move_Ground` |
| `park.go` | `Park` |
| `stop.go` | `Stop` |
| `activation.go` | `Activate`, `Deactivate` |
| `selectable.go` | `MakeSelectable` |
| the stockpile file | `BuildWeapon` |
| `internal/construction`, per queue, inside the pump visit | `GetBuilt` |
| `internal/construction`, per queue, from its own per-unit step | `BuildingBuild`, `MobileBuild`, `VTOL_MobileBuild`, `ReclaimUnit`, `VTOL_ReclaimUnit` |
| `internal/movement`, per queue, from its own per-unit step | `VTOL_Standby` |
| none — the reject sentinel | the empty name |

Notes the table cannot carry:

* `Move_Ground` alone is installed by the move-family installer. It once
  installed that one row's body on all eight names of the family, which is why a
  `Patrol` walked to its first waypoint and completed and a `RepairPatrol`
  neither patrolled nor repaired: a family installer assigns only where the
  handler is still nil, and that list ran first `[04 R-ORD-01 §4]`
  `[04 R-ORD-02 §2]`.
* `QMove` and `QPatrol` are rally markers, not moves: their body is a 60-tick
  delayed tail rotate with no goal binding, and the factory's `GetBuilt` copies
  them onto each finished product `[04 R-ORD-01 §2]` `[04 §3.8]`
  `[04 R-P0-09]`.
* The two mobile-build rows carry an owned handler rather than the
  externally-driven shorthand, because their approach phase parks on the gate
  `0xE0` — the follower reached the goal, an empty route was published away from
  it, a goal object was released — and the pump's satisfied set *is* that wake.
  The body still reports it did not advance the record on every arm but the
  row's abandon `[04 R-ORD-01 §0]` `[05 R-WORK-01 §13]`.
* `internal/orders` publishes the mobile-build blocked-area budget as two pure
  helpers the construction service calls, because the reach test measures against
  the product footprint: a blocked visit notifies "Waiting for target area to
  clear", increments the record's counter and waits **exactly 30 ticks with no
  random draw** while the counter is at most 10, and the first blocked visit past
  that notifies "Target area was blocked" and abandons; an approach the search
  cannot satisfy ends only on the route publisher's "cannot get there" bit, with
  the text "I can't reach the construction site" `[04 R-ORDER-02 §1]`
  `[04 R-ORD-01 §5]`.

### 3.6 Not implemented

* **The empty-name descriptor's handler is the reject sentinel, and nothing
  else.** Retail's row 0 has a handler; its body is not a behavior any producer
  can reach, because `Lookup` returns 0 exactly on a miss `[04 §3.1]`
  `[04 R-ORD-01 §12]`.
* **`RepairPatrol`'s repair scan and feature pairing, and `VTOL_Patrol`'s
  opportunity scan, take their established "none" arm.** The rows are wired and
  the terminals are right; what is missing is not the contract but the draws
  that belong with those candidate lists, which must come from the queue
  binding's own enumerator and never from a process-wide stream
  `[04 R-ORD-01 §4]` `[04 R-ORD-02 §2]` [I4].
* **`canstop` has no simulation reader, and `teleporter` is inert.** The
  `Teleport` order is ungated and free, and the interface latch that would arm it
  has no writer anywhere in retail — it is consumer-only `[04 R-SPEC-01 §2]`
  `[04 R-STANCE-01 §8]` `[07 §9]`.
* **The bugger-off port is a stored bit with no engine reader.** It is written by
  the write arm, read back by the read arm, cleared at creation and serialized,
  and nothing in the simulation consults it `[04 R-COB-05]`.
* **The unit-record fields whose retail consumers are still Unknown are carried,
  not interpreted.** They round-trip through the save boundary under neutral
  names and no code branches on them `[08 R-SAVE-02 §6]`
  `[08 R-SAVE-UNIT-01]`.

## 4. Retail behaviour that is not a bug

* **Units piling up at a factory exit are not waiting for a broadcast.** The
  script port usually read as a "bugger off" flag has no engine reader at all
  `[04 R-COB-05]`. What retail does is the silent 15-tick exit retry, the
  refused yard close and an ordinary blocked mover `[04 R-FAC-02 §5]`
  `[04 R-P0-08]`. Do not build a scatter or crowd-avoidance rule on that flag.
* **A column of products behind a factory is a defect, not retail.**
  `[04 R-EGRESS-01]` composed the column from four traces and concluded it was
  retail; **`[05 R-EGRESS-02]` supersedes it on that verdict**. Retail's no-rally
  products fan out around the whole border of the shared park rectangle; a column
  forms only when the follower's route-acceptance rule is applied at route
  *publication* instead of at the follower's goal installer `[04 R-PATH-01 §8]`.
  The prohibitions that still stand are the rest of `[05 R-EGRESS-02]`: nothing
  pushes a mover already parked, and the goal point is not made per-mover.
* **A no-rally aircraft hovering above its plant is retail.** Park becomes a
  vertical move to the aircraft's own position, so the order completes at the top
  of the initial climb `[04 R-AIR-02]`.
* **A map authoring zero gravity cancels every `AirStrike` order** (SC23,
  `[04 R-AIR-01 §8]`). Bombers idle there in retail too.
* **A weapon whose `Aim*` returns zero can never fire.** There is no timeout and
  no fallback; the latch is granted only by a nonzero completion. Treating a
  missing `Aim*` as success inverts the contract and turns every script-less
  turret into an always-ready one `[04 R-CB-01 §6]` `[06 §3.3]`.
* **A tight non-yielding script loop wedges the drain.** There is no per-visit
  iteration cap, and retail wedges the same way. So does a handler that loops
  through the continuing result codes: every handler returning 2 or 4 has first
  armed a gate or changed the segment head, or the walk does not terminate
  `[04 §4.3]` `[04 R-ORD-01 §10]`.
* **One blocked record stalls everything behind it, including the rear
  segment.** A nonzero gate with nothing satisfied ends the pass; the secondary
  walk does not run behind a blocked primary head `[04 §3.3]`.
* **A build angle of 4096 scatters every freshly built unit's heading by about
  20° around down-screen.** That is the authored field, applied as authored
  `[04 §2.3b]` `[04 R-P28-ANG-01R §2]`.
* **A paralyzed unit is stopped by its order row, not by its flag.** The stun is
  binary — fully stopped for exactly the credited ticks, no speed scaling, no
  movement-class change, no per-tick decrement — and what stops the unit is the
  head wait task plus the release verb and unconditional target clear on all
  three slots that the `Paralyze` row performs. The flag's only two readers test
  a *candidate*, in the shared autonomous target search and the computer player's
  picker `[04 R-ORD-01 §2]` `[06 R-DMG-01 §11]`.
* **A construction KBot's initial pose comes from its own script, not from a
  pose the engine imposes** `[04 R-P28-COB-01R]`.
* **`sortbias` is inert and `digger` is presentation only.** Neither has a
  simulation reader `[04 R-SPEC-01 §3]` `[04 R-SPEC-01 §7]`.
* **A restored order's phase byte is meaningless outside its handler.** The save
  carries it verbatim precisely because handlers own its interpretation; do not
  normalise it on load `[08 R-SAVE-ORDER-01]`.

## 5. Divergences

* **SC8 — the two code-9 re-arm jitters are distinct arms.** Doc 04's result-code
  table gives code 3 the base 30 plus a draw below 15; doc 05 gives code 9's
  last-record arm the base 30 plus a draw below 30. Both are right about their own
  arm: retail reaches the shared deadline calculation through two paths with
  different bounds `[04 R-P0-01]` `[04 §3.3]`. Both pumps use the same split, and
  it is observable only as the re-arm cadence of a completed last order.
* **SC16 — the active-state bit and the empty current-task field.** Closed. The
  bit is the classifier-eligibility bit of the runtime status word, written by the
  allocator initializer, cleared by death finalization and by the `InitialMission`
  postlude, and set again by `MakeSelectable`; nothing collides with it. The
  "empty current-task field" is the **remaining-build fraction**, not an order
  field — no routine of the order subsystem stores to it — so the selection
  predicate is the eligibility bit plus construction complete, and reads no order
  state at all `[04 §3.6]` `[07 R-WGT-01 §10]` `[08 R-TRIG-01 §3]`.
* **SC17 — the order-queue caps were inside stock behavior.** A census of the
  reference install finds stock `InitialMission` scripts producing well over the
  former 64-record primary cap, so that cap changed a retail mission. Both caps
  are replaced by dynamic storage matching retail's heap-linked list; the only
  remaining divergence is an out-of-memory guard set far outside anything stock
  or any plausible player shift-queue, and a pump iteration guard the same census
  shows is unreachable [I11].
* **SC21 — `BMcode` marks structures, not factories.** A stock factory authors
  `CanMove = 1`, so any factory-versus-mobile branch keyed on mobility is wrong.
  `BMcode == 0` and "has a yard map" are the same set. The building-class status
  bit is set from the authored `BMcode` at creation, and the interface's
  placement-versus-queue branch keys on the **product's** `BMcode` `[04 §6.2]`
  `[07 §9]`.
* **SC18 — the allocator's zero-fill length and the COB abort path are open.**
  Retail's exact fill length for the order record and the unit record is not
  traced, and retail may abort through its allocator where a clean implementation
  terminates the affected script. Go zero-initializes both records, so the fill
  length is unobservable here; the COB loader bounds-checks its header offsets and
  reports a diagnostic where retail does not [I11].
* **The interpreter bounds-checks the authored stack.** Pushes, pops and local
  indices are checked against the ten-value authored semantic limit instead of
  writing past it the way retail's unchecked frame arithmetic does. A Go slice
  write cannot reproduce memory unsafety, so an overflow deterministically loses
  the operation. This is the I11-sanctioned bounds-check exception, and thread-kill
  recovery is not claimed as retail behavior `[04 §4.3]` `[04 R-COB-01 §1]`.
* **Two divides retail leaves unguarded are clamped.** The death-severity query
  and the health-percentage read divide by the definition's maximum-damage field
  with no guard, so an authored zero faults retail's process outright. Both clamp
  here, named as I11 divergences rather than traced behavior `[04 §5.1]`
  `[04 §4.3]`.

## 6. Research map

| Behaviour | Owning research |
|---|---|
| Player slots, definitions, instance state, flags and transitions | `[04 §2]`, `[04 §2.3]`, `[04 §2.4]` |
| Player-slice order at battle entry | `[04 R-P0-16-A]` |
| The per-player unit sweep, step by step; queue helpers and the purge-survivor bit; the target-hit observer bit | `[04 R-MOV-03 §1]`, `[04 R-MOV-03 §6]`, `[04 R-MOV-03 §7]` |
| Unit initialization: the draw order, the build angle, the bob phase | `[04 R-P28-ANG-01R §2]`, `[04 R-MOV-01 §5c]` |
| The special-behavior FBI keys and their reader census | `[04 R-SPEC-01 §0]`–`[04 R-SPEC-01 §15]` |
| Guard assistance: retargeting, sizing, the wake producers | `[04 R-UNIT-06 §1]`, `[04 R-UNIT-06 §5]`, `[04 R-ORD-01 §8]` |
| The activation edge and its two callbacks | `[04 R-UNIT-06 §2]` |
| The `isairbase` mirror and the carrier clause | `[04 R-UNIT-06 §3]` |
| Stance values, the writer chain, the standing gates, defaults, inheritance, save, RNG | `[04 R-STANCE-01 §1]`–`[04 R-STANCE-01 §8]` |
| The descriptor table's sort comparator and lookup | `[04 R-STANCE-01 §9]` |
| The 68-descriptor table, verbatim, and the static mask's census | `[04 §3.1]`, `[04 R-DOC04-C]` |
| The order record and its per-family parameter meanings | `[04 §3.2]` |
| The queue pump, both result-code tables, the idle refill | `[04 §3.3]`, `[04 §3.4a]`, `[05 "Queue pumping and result codes"]` |
| The code-3 and code-9 deadline draws | `[04 R-P0-01]`, `[04 §8.3]` |
| The head-only primary walk; the secondary walk's skip | `[04 R-ORD-01 §10]` |
| The pending word's bits and who arms them | `[04 R-ORD-01 §0]`, `[04 R-ORD-01 §6]` |
| The shared handler vocabulary every body is written in | `[04 R-ORD-01 §1]` |
| Trivial, standing and wait handlers; combat; ground movement; work; the VTOL twins | `[04 R-ORD-01 §2]`, `[04 R-ORD-01 §3]`, `[04 R-ORD-01 §4]`, `[04 R-ORD-01 §5]`, `[04 R-ORD-01 §7]` |
| The weapon-slot control byte and the attack-chase slot pick | `[04 R-ORD-01 §7]`, `[06 R-WPN-05 §3]` |
| One bound payload per mover, and where a rebind's release bit lands | `[04 R-ORD-01 §9]` |
| The under-construction wake bit and the decay window | `[04 R-ORD-01 §11]` |
| Six handler details re-read: the annihilate reject, descriptor 0, self-repair, the guard bit, the repair arm, the assist footprint | `[04 R-ORD-01 §12]` |
| The record constructor, the four runtime bits of the static-mask copy, and their writers | `[04 R-ORD-01 §13]` |
| The self-destruct row's count word | `[04 R-ORD-01 §14]` |
| The factory product's own queue, and the head-insert branch | `[04 R-ORD-01 §15]` |
| The static head-insert column, row by row | `[04 R-ORD-01 §16]` |
| `VTOL_HelpBuild`'s third precondition; the build rows' unchecked product index | `[04 R-ORD-01 §17]`, `[04 R-ORD-01 §18]` |
| Command resolution exactly; the VTOL rows; the scan visitors and tail append | `[04 §3.4]`, `[04 §3.5]`, `[04 R-ORD-02 §1]`–`[04 R-ORD-02 §7]` |
| Handler retry and pre-reject mapping; tombstone, target-clear and stop-building cleanup | `[04 R-ORDER-02 §1]`, `[04 R-ORDER-02 §2]` |
| Factory completion, activation and rally inheritance | `[04 §3.8]`, `[04 R-P0-09]` |
| The product is cargo: attach, carry, detach, and the order form after release | `[04 R-FAC-02 §1]`–`[04 R-FAC-02 §4]` |
| The blocked exit's silent retry and the placement predicate the build rows wait on | `[04 §6.4]`, `[04 R-P0-08]`, `[04 R-FAC-02 §5]` |
| Release-boundary audits at the factory | `[04 R-FAC-01]`, `[04 R-FAC-01B]` |
| Why no-rally products queue at an exit — superseded on the verdict by `[05 R-EGRESS-02]` | `[04 R-EGRESS-01]` |
| The queued-order duplicate toggle and the overlay draw mask | `[07 R-P0-11 §3]`, `[07 R-P0-11 §6]` |
| The selection eligibility predicate | `[07 R-WGT-01 §10]`, `[07 §9]` |
| Goal families and the work rows' approach gate | `[04 §7.2]`, `[04 R-PATH-01 §12]`, `[05 R-WORK-01 §13]` |
| The air marker family, the takeoff preamble, standby and the seek states | `[04 R-AIR-01 §4]`, `[04 R-AIR-01 §6]`, `[04 R-AIR-01 §7]`, `[04 R-AIR-01 §8]` |
| COB loading, relocation, the script and piece tables | `[04 §4.1]`, `[fmt cob]`, `[02 "Compiled script archive (COB)"]` |
| The thread scheduler, the eight slots, sleeps and waits | `[04 §4.2]`, `[04 §4.6]` |
| Opcode encoding, the six operand shapes, the dispatch set, the kill path | `[04 §4.3]` |
| VM initialization, engine-call frames, the random-draw census, the single script boundary | `[04 R-COB-01 §1]`, `[04 R-COB-01 §2]`, `[04 R-COB-01 §3]` |
| The engine port set, read arithmetic, packed halves, write arms, transport reads, the effect opcode | `[04 §4.4]`, `[04 R-COB-03 §1]`–`[04 R-COB-03 §6]` |
| Engine-port write semantics, the factory stance handshake, the thread-start masks | `[04 §4.7]`, `[04 R-P0-10]` |
| The script-touched marker is order gate bit `0x4` | `[04 R-COB-06]` |
| The bugger-off port has no engine reader | `[04 R-COB-05]` |
| Explode, debris, shatter, bitmap explosions, the reserved opcode, statics as raw heap | `[04 R-COB-04 §6]`, `[04 R-COB-04 §7]`, `[04 R-COB-04 §1]`–`[04 R-COB-04 §5]` |
| The scriptless crash at creation; the speed and direction units; the aim handshake | `[04 R-COB-04 §8]`, `[04 R-COB-04 §9]` |
| The complete callback table, three corrected readings, the creation sequence, the aim completion target | `[04 §5.1]`, `[04 §5.3]`, `[04 R-CB-01 §2]`, `[04 R-CB-01 §3]`, `[04 R-CB-01 §4]`, `[04 R-CB-01 §6]` |
| The two unrelated producers of the direction and speed callbacks | `[04 R-CB-01 §5]` |
| Vertical-slice callback and port mappings; the same-tick integration trace | `[04 R-COB-02 §1]`, `[04 R-COB-02 §2]` |
| The construction-KBot initial pose boundary | `[04 R-P28-COB-01R]` |
| The weapon-query path, the query seeds, aim dispatch | `[06 R-P0-07]`, `[06 §3.3]` |
| The slot control byte and the "could not fire" bit | `[06 §1.2]`, `[06 R-WPN-05 §3]`, `[06 R-WPN-05 §6]` |
| The damage-intake observer notice and the damage flash | `[06 R-WPN-04 §2]` |
| What the stunned bit gates, and why the stun is binary | `[06 R-DMG-01 §11]` |
| Death causes and the death packet | `[06 §12.1]` |
| The stockpile row's encoding and launch-before-production ordering | `[06 §11.1]` |
| The piece hierarchy, load-time reordering, the half-turn pass, composition | `[03 §2.4]`, `[fmt 3do]` |
| Projectile model composition | `[03 §5.2]` |
| Render piece state and the composition-cache purge; the vertex pipeline floors | `[03 R-COMP-02 §3]`, `[03 R-RAST-01 §2]` |
| The default-mission-type key | `[02 R-KEYS-01 §1]` |
| The unit, order and script save boxes | `[08 R-SAVE-UNIT-01]`, `[08 R-SAVE-ORDER-01]`, `[08 R-SAVE-02 §6]`, `[08 R-SAVE-02 §9]`, `[08 R-SAVE-02 §14]` |
| The selection and eligibility clauses the trigger layer spells out | `[08 R-TRIG-01 §3]` |

## 7. Not implemented and open

One `TODO` marker stands in these four packages, and two more stand at the
producer that drives this package's queued-order toggle.

* `TODO(T23)` in `internal/orders/resolve.go`: the contextual-click resolver has
  two traced variants selected by the `Interface Type` option, and this build can
  only reach the default. The option is a settable single-player preference —
  the options page's two-stage button, the chat command and the persisted
  registry value all write one word, and the same word gates both the click
  dispatch and the cursor resolver — but `internal/settings` carries no field for
  it, so nothing can produce the other value. Binding it to the options page is
  what closes this; it must not become a build flag or a per-call parameter
  `[04 R-ORD-02 §1]` `[07 R-CAM-01 §5]` `[07 §8]`.
* `TODO(question)` at the queued-order duplicate toggle's producer
  (`internal/session/commands.go`, calling `Queue.CancelFrontMost`): whether the
  world-click producer receives a goal point alongside a target handle. The match
  rule states both arguments optional and does not say which the click supplies
  for a target-click order; the click is documented as issuing at the pointer's
  world point, so this boundary supplies both and the goal term participates. If
  retail passes no goal there, a repeat Shift-attack-click on a target that has
  moved more than a cell would remove the order where this build re-queues it. A
  trace of the world-click handler's call into the producer settles it
  `[07 R-P0-11 §6]` `[07 §9]`.
* `TODO(question)` at the same site: whether the interface's non-world-click
  queued issues share that producer. The rule is scoped to "every world order the
  interface issues", and the two world-click boundaries are its only callers here;
  the side panel's own buttons issue no world point, and nothing says whether a
  Shift-held press of one runs the test — which would make a second Shift-press
  cancel the first. A trace of those button handlers settles it `[07 R-P0-11 §6]`.

The questions the contracts above still carry, each with the observation that
would settle it:

* Which static gate-mask bits beyond the four named ones mean anything. The
  remaining bits have no located reader in the census; the raw mask is stored
  verbatim and never interpreted. Adding a reader needs a new finding, not a
  guess `[04 §3.1]` `[04 R-DOC04-C]`.
* The two uninitialized per-piece words the script save writer emits. Their value
  is not a function of game state, so there is nothing to clone; what goes there
  is a save-format policy the caller owns `[08 R-SAVE-02 §9]`.
* The exact fill length retail's allocator uses for the order and unit records,
  and whether its COB abort path is observable (SC18). Both are unobservable in
  this build.
