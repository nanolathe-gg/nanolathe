// The order queue pump [04 §3.3][05 "Queue pumping and result codes"]: the
// per-unit walk, the result codes and the idle refill, with the record and
// queue types the rest of the package hangs off.

package orders

import (
	"fmt"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Code is the handler result code [04 §3.3].
type Code uint8

const FlagActive uint32 = 0x1000 // active marker – exactly one primary node carries it [04 §3.3][plan C9]

const (
	FlagAutoOp uint32 = 1 << iota // existence established [04 §3.3][05 "Queue subtraction"]; numeric values not established
	// FlagPurgeSurvivor marks a record the Replace purge keeps: retail's purge
	// removes every front-segment record whose static-mask copy lacks bit 2,
	// and newNode copies that bit here at insertion [04 §3.3][04 R-MOV-03 §6].
	FlagPurgeSurvivor
	// FlagTombstone marks a record unlinked away from the compared head. Its
	// cleanup skips the target-cleared notification [05 "Queue subtraction"].
	FlagTombstone
	// FlagRetryMark is the pump's code-9 completion flag
	// [04 §3.3][R-ORDER-02 §2]. Nothing in this package reads it: no pump,
	// cleanup or handler arm branches on it.
	//
	// Corrected 2026-09-02 (WU-19-107). This comment used to end "write-only
	// state — no reader may be invented"; that is now wrong, and it stood
	// against the reader research has since located. [04 R-PATH-01 §8] step 5.3
	// gates the ground goal installer's synthetic straight-line fallback on
	// "the unit has a current order record and that record's retiring flag is
	// clear", and [05 R-EGRESS-02] names that flag as exactly this one — the
	// fallback is suppressed for records the pump has already declared
	// complete. That is what leaves a re-armed move standing still against a
	// goal it cannot occupy instead of lurching at it once per re-arm. The
	// reader belongs to internal/movement's goal installer, not to this file.
	FlagRetryMark
	// FlagStopBuildingPending marks a record whose StartBuilding emitter ran
	// (EmitStartBuilding, the flag's only writer [R-ORDER-02 §2]); cleanup
	// emits the StopBuilding counterpart on every removal path.
	FlagStopBuildingPending
)

// The record's movement state, as the movement outcome bits of
// [04 R-ORD-01 §0] leave it: nothing asked for, a route is being followed, the
// goal was reached, or the follower reported it cannot get there.
const (
	MoveNone    uint8 = 0
	MoveEnRoute uint8 = 1
	MoveArrived uint8 = 2
	MoveBlocked uint8 = 3
)

// Node is an 86-byte retail order record identity [04 §3.2] C5 (I13).
type Node struct {
	ID           ID            // descriptor identity [04 §3.2]
	Phase        uint8         // handler-private phase byte [04 §3.2]
	DynamicGate  uint32        // dynamic gate mask [04 §3.2]
	Deadline     int32         // deadline tick, -1 for none [04 §3.2]
	Owner        pool.Handle   // owning unit [04 §3.2]
	Target       pool.Handle   // target smart-reference [04 §3.2]
	GoalX        numeric.Fixed // goal position three 16.16 [04 §3.2]
	GoalY        numeric.Fixed
	GoalZ        numeric.Fixed
	GuardX       int16 // guard/fight anchor [04 §3.2]
	GuardY       int16
	CachedX      int16 // cached target position [04 §3.2]
	CachedY      int16
	Param1       uint32 // three general parameters [04 §3.2]
	Param2       uint32
	Param3       uint32 // build progress; for a mobile build the blocked-area retry counter [04 §3.2][R-ORDER-02 §1]
	StaticGate   uint32 // copy of descriptor static gate [04 §3.2]
	CreationTick uint32 // creation-tick snapshot [04 §3.2]
	Satisfied    uint32 // accumulated satisfied-gate bits [04 §3.2]
	Flags        uint32 // flag bits [04 §3.3][05]
	// Nanolathe path status extension [P0-I03][04 §7][04 §3.5]: published back from
	// the movement scheduler/route lifecycle so the pump and HUD can observe
	// en route / arrived / blocked without re-reading the movement grid.
	MoveState  uint8  // 0 none, 1 en route, 2 arrived, 3 blocked [P0-I03]
	PathStatus uint32 // copy of path.Status (0 success, 0x100 already, 0x200 rejected) [04 §7.2]
	// Retired (WU-19-225): two fields, ApproachWake and ApproachRetired, stood
	// here to carry the work approach's movement outcome to a state machine
	// running outside this pump, and to remember that the approach had already
	// consumed one. Neither was retail record state. Retail's mobile-build row
	// parks on gate `0xE0` and reads the satisfied set the pump hands its
	// handler [05 R-WORK-01 §13]; the record's own handler-private PHASE byte —
	// saved at `0x09`, "handlers own its interpretation" [08 R-SAVE-ORDER-01] —
	// is what remembers that phase 1 has run. internal/construction now arms
	// `0xE0` in the approach phase, receives the wake through the ordinary
	// OwnedHandler argument and advances the phase, so both fields are gone and
	// a builder saved mid-approach no longer restarts its walk after a load.
	//
	// P0-I05 authoritative construction payloads [05 "Factory production lifecycle"][05 "Construction arithmetic"].
	// BuildDefKey is the canonical catalog key for factory/mobile products; it
	// survives save/load and maps to a stable catalog index in Param1 via
	// Catalog.UnitDefIndex. Using string+index avoids FNV-1a collisions (N04)
	// and provides the established name→index table at load [P0-I05].
	// Factory product: BuildDefKey+Param1(index)+Param2(count)+Phase progress [05].
	// Mobile build: BuildDefKey+Param1(index)+GoalX/Z site; Param3 is the
	// blocked-area retry counter [04 §3.2][R-ORDER-02 §1].
	// Assist/repair/reclaim/capture/resurrection: Target + operation-specific progress in Param2/3 [05].
	BuildDefKey string // canonical unit key for build products [P0-I05][02 §5]
	// RetailSubtypeCode and RetailSubtype preserve the optional handler payload
	// attached to a saved order.  The payload is deliberately opaque here: its
	// owning handler performs any typed fix-up, while this node keeps every word
	// available across a catalog/session restore [08 R-SAVE-02 §10].
	RetailSubtypeCode    uint32
	RetailSubtype        []byte
	RetailSubtypeUnitA   pool.Handle
	RetailSubtypeUnitB   pool.Handle
	RetailSubtypeWords16 []uint16
	RetailSubtypeWords32 []uint32
	// CaptionPending is the ONE-SHOT caption-pending flag of [04 §3.2] — one
	// of the runtime bits the record's static-mask copy carries that no static
	// descriptor mask sets. The shared caption clear tests it, clears it, and
	// only then emits status kind 5 (`ok`) [04 R-ORD-01 §1]. Without it a
	// record that re-arms forever re-emits the acknowledgement voice on every
	// phase-0 re-entry, which [R-PATH-01 §14]'s composition (item 4) states the
	// steady state must NOT do: "silent and unbounded ... no motion, no engine
	// cue".
	//
	// Its writer is now Established (WU-19-159, [04 R-ORD-01 §13]) and it is
	// NOT the record constructor: the constructor leaves the static-mask copy
	// at the descriptor's own mask. The bit is armed by the ONE producer-side
	// insertion helper — this package's Push — and only there. The handler head
	// insert (PushHead), the patrol-chain tail append and the pump's own idle
	// refill all leave it clear, so a spawned or auto record never speaks.
	//
	// Retail's producer insertion arms the bit before it chooses a segment, so
	// a rear-segment record issued that way is armed too. PushSecondary does
	// not arm it, because in this build that one method serves both the
	// producer insertion and the handler spawn, and only the former should.
	// The distinction is inert: the two rear-segment descriptors are
	// `BuildWeapon` and `SelfDestruct` [04 R-ORDER-02 §1], and neither calls
	// the caption clear in [04 R-ORD-01 §2]'s contracts.
	//
	// It is a field rather than a local Flags bit because local runtime flag
	// values are an implementation representation. The retail save codec maps
	// this state explicitly to the canonical caption-pending wire bit and back,
	// while retaining unrelated static-mask bits unchanged [04 R-ORD-01 §13]
	// [08 R-SAVE-ORDER-01].
	//
	// The arming is conditional, and the condition is now wired (WU-19-181):
	// the insertion helper takes a queued/non-queued argument and arms the bit
	// only on the NON-QUEUED (Replace) issue, so a Shift-queued order is
	// inserted silent [04 R-ORD-01 §13]. QueuedIssue below carries that
	// argument into Push.
	CaptionPending bool

	// QueuedIssue is the queue modifier of [04 §3.3] — false for a non-queued
	// (Replace) issue, true for a queued (Append / Shift-queue) one. It is an
	// INSERTION-TIME INPUT to Push, not record state: retail passes it as an
	// argument to the producer insertion and the 86-byte record has no field
	// for it [04 §3.2][04 R-ORD-01 §13]. newNode therefore clears it on the
	// stored record, so nothing downstream can mistake it for a persisted bit.
	//
	// Every producer that already knows its modifier passes it through
	// NewNodeForOrder's `queued` argument, so no caller signature changed. The
	// producers that do not yet distinguish the two — the build-page adds of
	// internal/construction/queue.go, the mission spawner and the AI planner —
	// keep the non-queued default, which is what they issue today.
	QueuedIssue bool
}

// Queue holds the two segments [04 §3.2] C5.
type Queue struct {
	primary   []*Node
	secondary []*Node

	// diagnostics records dispatch failures for this unit's queue. It is per
	// queue rather than package-global so two worlds in one process cannot
	// interleave their logs and so a queue's diagnostics die with it
	// [AGENTS.md §Diagnostics].
	diagnostics []string

	// secondaryTick is the per-queue tick published by the secondary walk. It
	// remains queue-owned state for handlers that need the most recent rear
	// segment visit; callers obtain session inputs from binding instead of
	// mirrored queue fields [06 §11.1][RS-P0-018].
	secondaryTick uint32

	binding *QueueBinding

	// lastPumpTick is the tick this queue was last pumped at. It is the tick a
	// handler invoked OUTSIDE a pump visit is given — the cancel notification
	// of [R-ORDER-02 §2], which cleanupNode delivers through the record's own
	// handler at removal time. Retail's handler bodies read the engine's
	// current tick [04 R-ORD-01 §1]; a removal reaches this package either
	// from inside a pump (where this is that pump's tick) or from a command
	// that ran in the same tick as the unit's last pump, so it is the current
	// tick in both. It is deliberately NOT secondaryTick: that state is the
	// queue's rear-walk marker
	// state the construction service transfers across a queue rebind, and
	// writing it from the primary walk would destroy that transfer.
	lastPumpTick uint32

	// ownedHandlers is the per-row registration seam of queue_handlers.go: the
	// handlers a subsystem outside this package installs for the rows whose
	// bodies it owns. Keeping them on the queue preserves the ordinary ordered
	// primary walk without introducing package-global session state
	// [04 R-FAC-02 §4][P0-I16].
	ownedHandlers []OwnedHandler
}

// [P2-03][P1-I09] Queue storage is dynamic, matching retail's heap-linked list
// (NEGATIVE-BOUNDED 3901 boundaries found no cap). The previous 64/32 caps
// were inside stock-reachable behavior: corpus measurement over 275 maps /
// 278 units / 175 campaign missions shows a retail InitialMission can queue
// 105 raw tokens (Silent Slayers carry1: g ms1,g ms2,m...w...) and would
// require >64 primary nodes uncapped; the capped run truncated to 64.
// The secondary max in corpus is 1, but 32 is an arbitrary divergence.
// Retail has no located cap, so Nanolathe uses dynamic slice growth with an
// OOM guard only at a very large threshold far outside stock (OOMGuardQueue
// below, applied at content admission in Push/PushSecondary/CoalesceTail).
//
// There is deliberately NO pump-iteration cap and NO queue-code guard that
// changes behavior mid-walk (ORD-02): retail can wedge on a tight
// script/order loop, and a defensive cap would alter queue state, RNG use,
// and later updates — reproducing the wedge is the contract [04 §3.3][I11].
// Memory safety belongs at admission, not in the running queue.
// Corpus: TestCorpusQueueCaps_Retail (internal/orders/corpus_caps_test.go)
// measures maxPrimary 105+ uncapped and maxSecondary 1.
// Retired (WU-19-159). A T23 marker stood here asking for "the exact allocator
// zero-fill byte count for order nodes", on the premise that retail might have
// memset some prefix of the 86-byte record [04 §3.2] and left the rest holding
// heap residue. There is no such count and no such residue: the allocation is a
// plain untyped 86-byte request and the record constructor then writes EVERY
// field of the record — no memset participates. The two fields that are not
// simply zeroed are the deadline, which starts at the -1 sentinel, and the
// static-mask copy, which starts as the descriptor's own mask [04 R-ORD-01 §13].
const OOMGuardQueue = 10000

// LenPrimary is the number of records in the front segment.
func (q *Queue) LenPrimary() int {
	if q == nil {
		return 0
	}
	return len(q.primary)
}

// LenSecondary is the number of records in the rear segment, the one static
// bit 18 selects [04 §3.1].
func (q *Queue) LenSecondary() int {
	if q == nil {
		return 0
	}
	return len(q.secondary)
}

// HasIssuedWork reports whether the primary segment still holds a record that
// was ISSUED — by the interface, by a producer, or by a handler spawn — as
// opposed to one the pump created for itself.
//
// An empty primary segment is not the same thing as an idle unit, and a caller
// asking "is this unit still working?" wants this predicate rather than
// `LenPrimary() == 0`. The pump refills an idle unit's front list from the
// definition's `defaultmissiontype` and constructs that record with the
// auto/default-operation flag [04 §3.3, "Closed — the idle-queue refill from
// `defaultmissiontype`"], so the segment does not stay empty: the stock ground
// units author `defaultmissiontype = Standby` (ARMCOM, CORCOM and ARMPW read
// back from the reference install; ARMMINE1 authors `Standby_Mine`), and on the
// tick after a `Move_Ground` retires a flagged `Standby` record stands at the
// head in its place. Such a record is not work — the next issued order drops it on
// the way in, which is why Push begins by clearing leading auto-op records
// [04 §3.3].
func (q *Queue) HasIssuedWork() bool {
	if q == nil {
		return false
	}
	for _, n := range q.primary {
		if n != nil && n.Flags&FlagAutoOp == 0 {
			return true
		}
	}
	return false
}

// Primary returns a copy of the front segment's record pointers, so a caller
// walking it cannot be tripped by an insertion or a removal. The records
// themselves are shared, not copied.
func (q *Queue) Primary() []*Node {
	if q == nil {
		return nil
	}
	out := make([]*Node, len(q.primary))
	copy(out, q.primary)
	return out
}

// Secondary returns a copy of the rear segment's record pointers; see Primary.
func (q *Queue) Secondary() []*Node {
	if q == nil {
		return nil
	}
	out := make([]*Node, len(q.secondary))
	copy(out, q.secondary)
	return out
}

// SetPrimary replaces the primary segment [P0-I16][P0-I05].
// Exported accessor replaces reflect/unsafe inspection (ON-02).
func (q *Queue) SetPrimary(primary []*Node) {
	if q == nil {
		return
	}
	q.primary = primary
}

// SetSecondary replaces the secondary segment [P0-I16][04 §3.2].
// Exported accessor replaces reflect/unsafe inspection (ON-02).
func (q *Queue) SetSecondary(secondary []*Node) {
	if q == nil {
		return
	}
	q.secondary = secondary
}

// NewQueueWith constructs a queue with the given segments [04 §3.2] C5.
// Exported constructor for construction service to avoid reflect/unsafe (ON-02).
func NewQueueWith(primary []*Node, secondary []*Node) *Queue {
	return &Queue{primary: primary, secondary: secondary}
}

// Pump is the handle-addressed pump seam [04 §3.3]
// [05 "Queue pumping and result codes"]. It operates on one handle per call, so
// stepping builder A does not advance builder B and the two keep their own
// worker and economy buckets; the session's unit phase walks the pool and calls
// PumpUnit for each live unit in slot order [I1].
//
// It dispatches through Queue.Pump, which is the walk itself.
type Pump struct {
	World *units.World // authoritative unit pool (fixed pools, slot 0 null) [01 §6.1][P0-16]
}

// PumpResult reports the outcome of a single-unit pump [04 §3.3][05 "Queue pumping and result codes"] (ON-02).
type PumpResult struct {
	Handle       pool.Handle // requested handle
	Found        bool        // unit existed and was alive
	HadQueue     bool        // queue had at least one node before pumping
	PrimaryLen   int         // primary length after pump
	SecondaryLen int         // secondary length after pump
	Err          error       // explicit error for missing unit or other failure, nil on success
	Diagnostics  []string    // queue diagnostics captured during pump
}

// PumpUnit advances only the named unit's existing queue/work state [04 §3.3][05 "Queue pumping and result codes"] (ON-02).
// It preserves the primary head-blocking restart-from-head and secondary skip-not-due contracts:
// primary restarts from the head after each dispatch, secondary scans front-to-back skipping not-due.
// Only the named unit's queue advances; other builders are untouched. An absent
// queue remains absent: allocation belongs to the command/order producer, not
// to the per-tick unit visit [04 §3.3][04 §3.5].
func (p *Pump) PumpUnit(handle pool.Handle, tick uint32) PumpResult {
	if p == nil || p.World == nil {
		return PumpResult{Handle: handle, Err: fmt.Errorf("orders: nil pump or world")}
	}
	u := p.World.Unit(handle)
	if u == nil {
		return PumpResult{Handle: handle, Found: false, Err: fmt.Errorf("orders: unit %d not found or dead", handle)}
	}
	q := QueueOfUnit(u)
	if q == nil {
		return PumpResult{Handle: handle, Found: true, HadQueue: false, PrimaryLen: 0, SecondaryLen: 0}
	}
	had := q.LenPrimary()+q.LenSecondary() > 0
	// Preserve existing Queue.Pump semantics exactly: primary head-blocking, secondary skip-not-due.
	q.Pump(u, tick)
	prim := q.LenPrimary()
	sec := q.LenSecondary()
	// No order-guard write. [07 R-WGT-01 §10]'s store census over the word the
	// eligibility sites compare finds no writer anywhere in the order subsystem
	// — "not the order-record constructor, not the primary or secondary pump,
	// not the handler return-code epilogue, not cancel-all, not the
	// single-record expiry helper, and not the idle-queue refill". The word is
	// the remaining-build fraction, which construction owns.
	diags := q.Diagnostics()
	return PumpResult{Handle: handle, Found: true, HadQueue: had, PrimaryLen: prim, SecondaryLen: sec, Diagnostics: append([]string(nil), diags...)}
}

func isSecondary(id ID) bool {
	return DescriptorFor(id).StaticGate&staticRearSegment != 0 // [04 §3.1] rear-segment selection flag
}

func (q *Queue) simForJitter() *rng.Simulation {
	if q != nil {
		if binding := q.Binding(); binding != nil {
			return binding.SimRNG
		}
	}
	return nil
}

func (q *Queue) randBelow15() uint32 {
	if q.simForJitter() == nil {
		panic("orders: simulation RNG not injected [DET-01]")
	}
	return q.simForJitter().Uint32n(15) // gameplay jitter uses simulation stream [I4][04 §3.3]
}

func (q *Queue) randBelow30() uint32 {
	if q.simForJitter() == nil {
		panic("orders: simulation RNG not injected [DET-01]")
	}
	return q.simForJitter().Uint32n(30) // [R-P0-01] code 9's last re-arm draws RNG(30), a distinct draw site from code 3's RNG(15)
}

// moveGroundGoalRadius is the arrival radius `Move_Ground` phase 0 binds with
// its point goal: "radius `(int16)payloadType + 4`" [04 R-ORD-01 §4], the
// record's first general parameter word read as a SIGNED 16-bit value.
//
// MoveGroundGoalRadius below is the read-back seam for the movement layer, the
// same shape PatrolGoalRadius and ParkGoalRect already give it: the handler
// authors the geometry and the movement layer reads it back rather than
// restating the arithmetic beside a descriptor name it does not own.
func moveGroundGoalRadius(n *Node) int32 {
	if n == nil {
		return 4
	}
	return int32(int16(uint16(n.Param1))) + 4
}

// MoveGroundGoalRadius reports the arrival radius `Move_Ground` binds, and
// whether n is that row [04 R-ORD-01 §4].
func MoveGroundGoalRadius(n *Node) (int32, bool) {
	if n == nil || DescriptorFor(n.ID).Name != "Move_Ground" {
		return 0, false
	}
	return moveGroundGoalRadius(n), true
}

// moveGroundHandler is `Move_Ground`, and since WU-18-8 that descriptor alone
// (see ensureMoveHandlers below for what else used to run this body and why it
// was wrong).
//
// Row [04 R-ORD-01 §4][R-P0-01]: phase 0: carried -> cancel-all; caption clear;
// point goal at the record's goal with radius `(int16)payloadType + 4`; gate =
// 0xE0; advance. Phase 1: satisfied 0x20 -> status 6 (`Arrived`), complete;
// else *re-arm* (9), which resets the phase and rebinds from phase 0 after
// 30..59 ticks. Other phase: cancel-all — a phase byte outside the machine
// cancels the whole queue [R-ORDER-02 §1].
//
// Corrected 2026-09-02 (WU-19-97). Phase 0 armed the gate and nothing else: it
// ran neither the row's caption clear nor its point-goal install, so the ONE
// row of the whole table that is the ordinary move was the one row that owned
// no goal payload. Everything downstream had to work around that. The
// controller's slot stayed empty for an ordinary move, so the follower's
// arrival step had no payload to ask [04 R-MOV-03 §2 step 1] and the arrival
// bit `0x20` this handler's phase 1 waits on had to be produced from a
// name-keyed handle instead; the follower's repath arm ([04 R-MOV-03 §2] step
// 3, "with a payload installed") could not be gated as the section writes it,
// because gating it would have stopped every ordinary move from re-pathing;
// and an install by any OTHER record could not displace this record's object
// from the slot, because there was none, so the `0x80` rebind raise of
// [04 R-ORD-01 §9] never reached a `Move_Ground` record.
//
// The install is the same helper every combat and work row already reaches —
// installPointGoal (combat.go) — so this row now clears pending `0x20`-`0x200`,
// releases its own previous object and binds the new one exactly as they do.
//
// The radius is the row's own: `(int16)payloadType + 4`, the record's first
// general parameter word read as a SIGNED 16-bit value [04 R-ORD-01 §4]. It is
// 0 for interface- and most AI-issued moves (radius 4, handle threshold
// floor(4/16)² = 0 — arrival on the exact goal cell) and 160 for the AI wave
// task's gather broadcast [08 R-AI-01 §19]. internal/movement's
// goalRadiusParamFor reads the same word for the fallback handle it builds
// before this handler has run.
func moveGroundHandler(u *units.Unit, n *Node, satisfied uint32, _ uint32) Code {
	if u != nil && u.Attachment.Carrier != 0 {
		return 7 // reject while attached [R-P0-01]
	}
	if n.Phase == 0 {
		captionClear(u, n) // [04 R-ORD-01 §4] "caption clear" [04 R-ORD-01 §1]
		// "point goal at the record's goal with radius `(int16)payloadType + 4`"
		// [04 R-ORD-01 §4].
		installPointGoal(u, n, n.GoalX, n.GoalY, n.GoalZ, moveGroundGoalRadius(n))
		n.DynamicGate = 0xE0 // [R-P0-01] phase 0 arms gate 0xE0
		return 1
	}
	if n.Phase > 1 {
		return 7 // cancel-all: a phase outside the machine [R-ORDER-02 §1]
	}
	if satisfied&0x20 != 0 { // [R-P0-01] combined&0x20 -> ack + return 5
		// The acknowledgement is the row's "status 6 (`Arrived`)"
		// [04 R-ORD-01 §4]: the shared status emitter, whose three-clause
		// producer gate (owner is the local viewing player, alive bit set,
		// silenced bit clear) and default-caption substitution live in the
		// session-owned adapter behind workStatus [04 R-ORD-01 §1]
		// [03 R-AUD-01 §3]. It reaches presentation as a committed status
		// event, never as sim audio [I6]. This is the same call the two other
		// kind-6 raisers make — `Attack_Kamikaze` phase 1 (combat.go) and
		// `VTOL_Move` phase 2 (patrol.go).
		workStatus(u, statusArrived, "Arrived")
		return 5
	}
	return 9 // [R-P0-01] drop when further records else 30+RNG30 wait
}

// ensureMoveHandlers installs moveGroundHandler on the one descriptor whose row
// it is.
//
// Correction (WU-18-8). This installer used to name all eight members of the
// move family — `Move_Ground`, `VTOL_Move`, `QMove`, `Patrol`, `QPatrol`,
// `VTOL_Patrol`, `RepairPatrol`, `VTOL_RepairPatrol` — and give every one of
// them the ground move's body. Only the first IS that body. [04 R-ORD-01 §4]
// gives `Patrol` and `RepairPatrol` their own multi-phase machines built on the
// patrol-chain setup; [04 R-ORD-01 §2] gives the queued-move pair a single row
// of its own ("Deadline 60, *rotate*") that owns no goal and reads no target;
// [04 R-ORD-02 §2] gives the two air forms bodies that end differently from the
// ground move; and [04 R-ORD-01 §7] gives `VTOL_RepairPatrol` a body vtolwork.go
// had already written. The over-claim was not inert: a family installer assigns
// only where a descriptor's handler is still nil and this list runs first, so
// claiming the seven other names took them out of the reach of the installers
// that owned them. A `Patrol` walked to its first waypoint and completed, a
// `RepairPatrol` neither patrolled nor repaired, and `VTOL_RepairPatrol`'s
// written and tested body could never install. The seven rows now live in
// patrol.go and vtolwork.go.
func ensureMoveHandlers() {
	id := rowMoveGround
	if id != 0 && int(id) < len(table) && table[int(id)].Handler == nil {
		table[int(id)].Handler = moveGroundHandler
	}
}

func init() {
	// Attempt early install; if table not yet built (init order) the lazy ensure will retry on first pump.
	ensureMoveHandlers()
}

func findActive(q *Queue) int {
	if q == nil {
		return -1
	}
	for i, n := range q.primary {
		if n.Flags&FlagActive != 0 {
			return i
		}
	}
	return -1
}

// The active marker has ONE writer: the producer insertion's after-marker
// branch in Push, which sets bit 12 on the record it creates and clears it on
// the record that held it [04 R-ORD-01 §13]. Nothing else in the traced code
// writes the bit — not a removal, not the tail rotate, not the Replace purge,
// not the leading-auto drop, not the save restore.
//
// There used to be an `ensureSingleActive` helper here whose head fallback
// handed the marker to primary[0] whenever no record carried one, called from
// each of those paths. Its four remaining callers were removed by WU-19-227
// (the removals) and WU-19-232 (the rotate, the purge, the drop and the
// restore), because "no record carries the marker" is a state retail reaches
// and relies on: [04 §3.1]'s insertion rule reads "with no marked record it
// appends at the tail". Inventing a marker there moved the insertion point to
// index 1 and put the next Shift-queued order at the FRONT of the queue.
//
// Only its other half survives, in releaseMarkerOnRemoval below: a duplicate
// marker is cleared.

// releaseMarkerOnRemoval is what a removal does to the active marker: it lets
// it go.
//
// Correction (WU-19-227). Every removal routine used to close with
// ensureSingleActive, commented "mark moves to the successor [04 §3.3]". There
// is no such rule. [04 §3.3]'s runtime-bit census of the static-mask copy
// ("The active marker's writers, and the head insert's silence about it")
// gives bit 12 — the active marker — exactly ONE writer: the producer
// insertion's after-marker branch, which sets it on the record it creates and
// clears it on the record that held it. Nothing else in the traced code writes
// the bit, removals included. So freeing the record that carried the marker
// leaves the segment unmarked, and [04 §3.1]'s insertion rule then takes its
// other arm: "with no marked record it appends at the tail".
//
// The invented fallback was the play-test defect. A Shift-click that removes
// the most recently queued order removes exactly the record holding the marker
// ([07 R-P0-11 §6]'s duplicate toggle); ensureSingleActive then handed the
// marker to q.primary[0], so the next Shift-queued order was inserted at index
// 1 — at the FRONT of the remaining queue instead of at its end.
//
// Only ensureSingleActive's other half is kept: a duplicate marker is cleared.
// A removal cannot create one, but a cleanup notification that re-enters the
// queue can, and repairing it here costs one walk of a short segment.
func (q *Queue) releaseMarkerOnRemoval() {
	if q == nil {
		return
	}
	seen := false
	for _, n := range q.primary {
		if n == nil || n.Flags&FlagActive == 0 {
			continue
		}
		if seen {
			n.Flags &^= FlagActive
			continue
		}
		seen = true
	}
}

// Pump runs one unit's queue for one tick [04 §3.3]
// [05 "Queue pumping and result codes"]: the primary walk, then the secondary
// walk — two independent per-unit calls, in that order, unconditionally.
//
// Correction (AU-13). A gate test used to stand between the two calls here:
// after pumpPrimary, if the front-segment head carried a nonzero dynamic gate
// with nothing satisfied, Pump returned and the rear segment was never pumped,
// commented "blocked primary front prevents ALL secondary dispatch [04 §3.3]".
// There is no such coupling. [04 R-ORD-01 §10] gives step 3's `return` as a
// return from the PRIMARY LOOP, not from the per-unit tick; the per-unit tick
// calls the two pumps back to back with no test between them, and the secondary
// loop never reads the front head's gate at all — the only front-segment state
// it touches is the choice of which list head to unlink from. §3.3 step 3's
// clause "rear-segment records sit behind any front blocker" was wrong with it,
// and has been corrected in place.
//
// The coupling was not merely a missed dispatch. The secondary code-3 arm
// spends a sim(15) draw exactly as the primary's does, so a unit sitting behind
// a gated front head with a rear record moved the simulation stream's position
// in retail and did not here — every stock aircraft, via the VTOL_Standby
// refill that refillIdle head-inserts into the rear segment [04 §3.3].
//
// This is the pump. The comment that used to stand here called it a
// "non-authoritative compatibility wrapper" and pointed at Pump.PumpUnit, but
// PumpUnit is the handle-addressed seam that calls THIS, and the session's unit
// phase reaches it that way; a caller holding the queue calls it directly.
func (q *Queue) Pump(u *units.Unit, tick uint32) {
	if q == nil || u == nil {
		return
	}
	q.lastPumpTick = tick
	// No order-guard write here either; see PumpUnit above and
	// [07 R-WGT-01 §10].
	q.pumpPrimary(u, tick)
	q.pumpSecondary(u, tick) // unconditional: the two segments are separate lists [04 R-ORD-01 §10]
}

// IdleRefillMission resolves the standing task the primary pump creates for
// an idle unit [04 §3.3, "Closed — the idle-queue refill from
// `defaultmissiontype`"][02 R-KEYS-01 §1].
//
// Established, re-verified 2026-08-31: the definition's 100-byte
// `defaultmissiontype` string is converted through the ORDER DESCRIPTOR
// registry's own case-insensitive name lookup — the same binary search over the
// 25-byte descriptor records that §3.1 sorts, returning the record's table
// index as a byte, and 0 (the reject sentinel) for an empty or unrecognised
// name. The pump's condition is three terms: the front list is empty, the
// owner's controller state is 1 or 2, and the code is non-zero.
//
// The controller state is passed in because this package cannot see the player
// ledger. Values 1 and 2 are the two ACTIVE player states — 1 human, 2 computer
// ([04 R-SPEC-01 §5] identifies 2 as the computer player) — not, as §3.3 used to
// say, "one of the two computer-player states": the same 1-or-2 test gates the
// whole per-unit sweep that runs this pump, with 3 the eliminated/watch state
// whose units are skipped entirely ([04 §8.3, "Closed — compact ground
// controller"]). A human player's idle aircraft is therefore refilled exactly
// like a computer player's, which is what makes the stock
// `defaultmissiontype = VTOL_Standby` reachable at all.
func IdleRefillMission(u *units.Unit, controllerState uint8) (ID, bool) {
	if u == nil || u.Def == nil {
		return 0, false
	}
	if controllerState != 1 && controllerState != 2 {
		return 0, false
	}
	name := u.Def.DefaultMissionType
	if name == "" {
		return 0, false
	}
	id := Lookup(name)
	if id == 0 {
		// The registry comparator is case-insensitive [04 §3.1], so an authored
		// name that differs only in case still resolves.
		for i := range table {
			if i != 0 && strings.EqualFold(table[i].Name, name) {
				id = ID(i)
				break
			}
		}
	}
	if id == 0 {
		return 0, false
	}
	return id, true
}

// controllerStateOf reads the owner's controller state through the queue's
// economy binding. The binding carries the economy service under its stockpile
// name; with none bound there is no ledger and no refill.
func (q *Queue) controllerStateOf(u *units.Unit) uint8 {
	if q == nil || u == nil {
		return 0
	}
	if q.binding == nil {
		return 0
	}
	svc, ok := q.binding.Economy.(*economy.Service)
	if !ok || svc == nil {
		return 0
	}
	owner := int(u.Owner)
	if owner < 0 || owner >= len(svc.Players) {
		return 0
	}
	return svc.Players[owner].ControllerState
}

// refillIdle is the pump's own record creation for an idle unit [04 §3.3]:
// "Idle default-operation records are created by the primary pump itself —
// never by insertion — only when its list is empty ... such a node is allocated
// in non-queued mode, constructed with the auto flag, and head-inserted into
// the list the op's descriptor selects (a secondary-class default op therefore
// lands in the rear segment)." The pump returns after the insert; the record is
// dispatched on the next visit.
//
// This is the seam the standby → `VTOL_LandIfCan` → landing chain hangs from:
// every stock aircraft authors `defaultmissiontype = VTOL_Standby`, so with no
// refill no `VTOL_Standby` record ever existed and a plane that finished a move
// hovered where it stopped forever.
func (q *Queue) refillIdle(u *units.Unit) bool {
	if q == nil {
		return false
	}
	return q.refillIdleWithState(u, q.controllerStateOf(u))
}

// refillIdleWithState is refillIdle with the controller state supplied, so the
// insertion half can be exercised without an economy ledger behind the queue.
func (q *Queue) refillIdleWithState(u *units.Unit, controllerState uint8) bool {
	if q == nil || u == nil || len(q.primary) != 0 {
		return false
	}
	id, ok := IdleRefillMission(u, controllerState)
	if !ok {
		return false
	}
	n := Node{Owner: u.Handle, Deadline: -1}
	if isSecondary(id) {
		q.PushSecondary(id, n)
		if len(q.secondary) > 0 {
			q.secondary[0].Flags |= FlagAutoOp
		}
		return true
	}
	node := q.PushHead(id, n)
	if node == nil {
		return false
	}
	node.Flags |= FlagAutoOp // "constructed with the auto flag" [04 §3.3]
	return true
}

func (q *Queue) pumpPrimary(u *units.Unit, tick uint32) {
	if q == nil || u == nil {
		return
	}
	q.lastPumpTick = tick
	// No iteration cap here (ORD-02): a handler looping through the continue
	// codes wedges exactly as retail's does [04 §3.3][I11].
	//
	// The walk is HEAD-ONLY [04 R-ORD-01 §10]: after every non-returning result
	// code the loop reloads the front head and applies steps 1 to 4 to it.
	// There is no cursor, because "continue walking" never means "visit the
	// record behind this one" — a record behind the head is reached in a pass
	// only when the head is unlinked (codes 5, 8, 9-not-last, the above-9
	// helper), rotated to the tail (code 6), or replaced by a handler's head
	// insert ([04 R-ORD-01 §1]).
	//
	// Retired (WU-19-73): this loop used to carry `for cursor := 0; cursor <
	// len(q.primary);` with `cursor++` on a hold and `cursor = 0` on every
	// other continuing code. The cursor was WU-19-4's generalisation of
	// [04 R-FAC-02 §4]'s "a *hold* (code 2) does NOT stop the walk — the next
	// record is visited in the same pass", which §4's own 2026-09-02 correction
	// withdraws: the primary pump reloads the head after every code, so a
	// code-2 hold re-runs whatever is at the head, never the record behind it.
	for {
		if len(q.primary) == 0 {
			// §10's first line: "if rec is null: (auto-order spawn for an idle
			// mover, §3.4a) return". The idle refill from
			// `defaultmissiontype` [04 §3.3]. A unit whose primary segment has
			// emptied is handed its standing auto-op record — for every stock
			// aircraft that is `VTOL_Standby`, whose no-cargo arm pushes
			// `VTOL_LandIfCan`, which is how an idle aircraft comes home.
			//
			// This was written, exported and tested but deliberately not called,
			// because the landing-legality predicate `VTOL_LandIfCan` depends on was
			// a placeholder and a factory's first aircraft product landed on its own
			// plant, stalling the plant's build-stance handshake forever. That
			// predicate is now traced and implemented [04 R-AIR-01 §6a], and it
			// refuses a finished building's yard cells, so the product no longer
			// parks on the plant that made it.
			//
			// Corrected (WU-19-73): the refill used to fall through into the
			// walk, so a refilled record was dispatched in the same pass. The
			// refill closure of [04 §3.3] states the opposite outright — "It
			// returns immediately after the insert — the record is dispatched
			// on the unit's next pump visit, never in the same one" — and §10's
			// loop returns on the null head for the same reason.
			q.refillIdle(u)
			return
		}
		n := q.primary[0]
		if n.Deadline != -1 && tick >= uint32(n.Deadline) {
			n.Deadline = -1
			n.Satisfied |= 1 // ordinary deadline expiry raises only bit 0 [04 R-ORD-01 §0]
		}
		desc := DescriptorFor(n.ID)
		// Removed (WU-18-0): a phase-0 pre-dispatch clear used to stand here.
		// When the record was at phase 0 and its dynamic gate still equalled
		// the descriptor's static mask, it wiped the gate, the pending word and
		// the deadline so that the first dispatch was not blocked. It existed
		// only to compensate for newNode seeding the dynamic gate from the
		// static mask (see the correction there), and it is not a clear retail
		// performs. [04 §3.3] gives the pump exactly one gate clear — step 4's,
		// on the record it is about to dispatch, below — and reaches the
		// dispatch-on-first-visit property a different way: the record
		// constructor zeroes the gate and the pending word [04 R-ORD-01 §1], so
		// step 3's block test passes on a fresh record without any pump help.
		// With the constructor corrected the block is unreachable for a fresh
		// record (its gate is 0, so the outer test fails), and for any record
		// that reaches phase 0 with a gate a handler armed — result code 0
		// resets the phase while leaving the gate and deadline standing — it
		// would be actively wrong: it would discard a wait, its deadline, and
		// the pending bits [04 §3.3] steps 1 and 2 require to survive into the
		// satisfied intersection.
		satisfied := (n.Satisfied | u.Pending) & n.DynamicGate // [04 §3.3] C6
		if n.DynamicGate != 0 && satisfied == 0 {
			return // blocked head stalls [04 §3.3] C6
		}
		n.Satisfied &^= satisfied
		u.Pending &^= satisfied
		n.DynamicGate = 0
		if satisfied&pendTargetCloaked != 0 {
			// The interrupt clears every target before dispatch without changing
			// slot posture [04 R-ORD-01 §10][04 R-ORDER-02 §3].
			clearWeaponTargetsUnconditional(u)
		}
		handler := desc.Handler
		owned := q.OwnedHandlerFor(n.ID)
		if handler == nil && owned == nil {
			// What advances a handler-less record is the descriptor's own
			// Driver field, not its spelling.
			switch desc.Driver {
			case DriverMovementRoute:
				// The movement scheduler owns the route lifecycle [04 §7].
				// Synthesize a wait so the pump does not spin and the record is
				// re-dispatched after 30+rand15 [04 §3.3] C3, while the loop's
				// path-submit and movement-integrate drive the route.
				n.DynamicGate = 1
				n.Deadline = int32(tick + 30 + q.randBelow15()) // [04 §3.3][I4]
				n.MoveState = MoveEnRoute
				return
			}
			// Retired (WU-19-4): this arm carried an accepted-blocked marker for descriptors
			// that had no handler and no driver. The census is now zero —
			// TestHandlersAreInstalledBeforeTheFirstPump walks the whole table
			// and fails on any named descriptor that carries neither a
			// descriptor handler nor an owning subsystem's queue registration
			// ([04 R-FAC-02 §4], queue_handlers.go) — so this is a guard
			// against a table that regresses, not a placeholder for behavior
			// we owe.
			//
			// It stays because the alternative to a guard is a jam. Retail has
			// a handler for every named descriptor, so there is no retail
			// behavior to clone here; what the pump owes is an outcome that is
			// bounded and visible. The record is parked with the contract's own
			// wait, code 3 — lowest gate bit, deadline `tick + 30 + random
			// below 15` [04 §3.3] — which stops the walk, keeps the record the
			// player still owns, and re-diagnoses once per wait instead of once
			// per tick. The alternatives are all worse: codes 0 and 1
			// re-dispatch the record from the head and spin, corrupting the
			// phase on the way; 2, 4 and 6 walk past it every tick with no
			// diagnostic; and 5, 7, 8 and 9 free it, turning a missing handler
			// into a silently dropped order.
			q.recordDiagnostic(fmt.Sprintf("orders: no handler for %s, parked for 30..44 ticks", desc.Name))
			q.applyPrimaryResultCode(n, 3, tick)
			return
		}
		var code Code
		if owned != nil {
			// A row whose body belongs to a package this one cannot import:
			// the owning subsystem registered it on this queue and the pump
			// dispatches it exactly as it dispatches a descriptor handler
			// (queue_handlers.go). `GetBuilt` is one of them — the construction
			// service that owns the product lifecycle binds it per queue
			// [04 R-FAC-02 §4] — and the satisfied set it receives is the same
			// one computed above: `GetBuilt`'s gate is `0x8001` and its phase-2
			// body reads the set to choose its arm, `0x8000` holds for another
			// 30 ticks, bit 0 alone decays [04 R-ORD-01 §11].
			//
			// A registration that reports it did not advance the record is the
			// owner saying its own per-unit step does, and that it owns the
			// record's phase, gate and deadline. The pump must leave every one
			// of those fields alone: writing a result code over them is writing
			// over a live state machine.
			ran := false
			if code, ran = owned(u, n, satisfied, tick); !ran {
				return
			}
		} else {
			// Removed (WU-18-7): three by-name cases stood here, calling
			// `beCarriedHandlerAtTick`, `stopHandlerAtTick` and
			// `parkHandlerAtTick` so those three bodies could see the tick that
			// the Handler signature did not carry. The tick is now the
			// handler's fourth argument, so every descriptor gets it the same
			// way — which is what [04 R-ORD-01 §1] describes: the deadline
			// setter stores "current tick + n", and any row with a deadline
			// needs the tick to form one.
			code = handler(u, n, satisfied, tick)
		}
		// [04 §3.3] codes 2 and 4 "continue walking unchanged". Continuing is
		// the head reload of [04 R-ORD-01 §10], so a hold re-runs the record
		// that is at the head *after* the handler returned — the same record
		// when the handler only armed a gate (the reload then finds it blocked
		// and the pass ends), or the record a handler head-inserted or
		// re-identified in its place.
		//
		// Corrected (WU-19-73). WU-19-4 made this arm `cursor++` for every row,
		// on [04 R-FAC-02 §4]'s "The primary pump stops its walk at the first
		// record whose gate is non-zero and whose satisfied set is empty; a
		// *hold* (code 2) does NOT stop the walk — the next record is visited
		// in the same pass." That sentence is withdrawn by §4's own 2026-09-02
		// correction — it was the reasoning behind the retracted `t0 + 301` pad
		// dwell — and [04 R-ORD-01 §10] gives the loop in full: nothing ever
		// resumes at the *next* record. §10 also names the invariant this arm
		// now relies on: every handler returning 2 or 4 has first armed a gate
		// on the record or changed the segment head, or the loop would not
		// terminate. `BeCarried` phase 1 (deadline 10, code 2, every visit) is
		// its canonical example.
		if code == 2 || code == 4 {
			continue
		}
		if desc.Name == "GetBuilt" {
			// The construction service's own result handling. Under the
			// head-only walk this record is always the front head, so the
			// removal never tombstones ([R-ORDER-02 §2]) and the pass always
			// continues into the record the unlink exposed — which is how the
			// rally or `Park` that `handleGetBuiltOrder` appended is dispatched
			// in the same pass [04 R-FAC-02 §4].
			if code == 5 || code == 8 {
				q.RemovePrimaryNode(n, false)
				continue
			}
			return
		}
		if !q.applyPrimaryResultCode(n, code, tick) {
			return
		}
	}
}

// indexOfPrimary locates a record in the primary segment by identity, or -1.
func (q *Queue) indexOfPrimary(n *Node) int {
	for i, p := range q.primary {
		if p == n {
			return i
		}
	}
	return -1
}

// spliceOutPrimary unlinks n from the primary segment by identity and reports
// whether it was still linked. It takes no index from the caller, and that is
// the point: it is called AFTER the removal cleanup, which can re-enter the
// queue.
//
// Every removal path runs cleanupNode, and cleanupNode sends the cancel
// notification of [R-ORDER-02 §2] — "when the record's dynamic gate mask still
// holds bit 1 (value 2) at removal, invoke the operation handler with that
// cancel-notification mask". That handler is arbitrary work: for the three
// construction rows it is the production machine, whose cancel-current epilogue
// removes the head itself; for a descriptor row it may head-insert a spawned
// record ([04 R-ORD-01 §1]) or cancel the whole queue (code 7). So the segment
// the caller measured before the cleanup is not the segment that exists after
// it, and an index taken before is stale.
//
// In retail the queue is a linked list and a record's destructor unlinks the
// node itself — unlinking a node that the notification already unlinked is a
// no-op there, because its links are gone. Our segment is a slice, which has no
// such property: the same double removal drops whichever record has since taken
// slot 0, or runs off the end of an emptied segment. Re-locating the record is
// how the slice keeps the linked list's semantics; it is not a bounds guard,
// and it removes exactly the record the caller named or nothing at all.
func (q *Queue) spliceOutPrimary(n *Node) bool {
	idx := q.indexOfPrimary(n)
	if idx < 0 {
		return false // the cancel notification already unlinked it
	}
	copy(q.primary[idx:], q.primary[idx+1:])
	q.primary = q.primary[:len(q.primary)-1]
	return true
}

// spliceOutSecondary is spliceOutPrimary's rear-segment twin, and exists for
// the same reason: the removal cleanup runs before the unlink and can re-enter
// the queue, so an index measured before it is stale.
func (q *Queue) spliceOutSecondary(n *Node) bool {
	for i, m := range q.secondary {
		if m == n {
			copy(q.secondary[i:], q.secondary[i+1:])
			q.secondary = q.secondary[:len(q.secondary)-1]
			return true
		}
	}
	return false // the cancel notification already unlinked it
}

// unlinkPrimary removes the dispatched record from the primary segment and
// runs the removal cleanup [05 "Queue subtraction"]. The record is found by
// identity rather than assumed to be at the front: a handler that head-inserts
// a spawned record [04 R-ORD-01 §1] is no longer the front record when its own
// result code is applied, and freeing slot 0 there would free the spawned
// order instead of the one that finished.
//
// The tombstone follows [R-ORDER-02 §2]: it is set on every freed record
// except the one that is the primary segment's front head at that moment, and
// it is what suppresses that record's weapon-target-clear notification. The
// tombstone decision is made before the cleanup, because it is about where the
// record stood when the removal was decided; the unlink is made after it, by
// identity, because the cleanup's cancel notification can move or remove
// records (see spliceOutPrimary).
func (q *Queue) unlinkPrimary(n *Node) {
	idx := q.indexOfPrimary(n)
	if idx < 0 {
		return
	}
	if idx != 0 {
		n.Flags |= FlagTombstone
	}
	q.cleanupNode(n)
	q.spliceOutPrimary(n)
	q.releaseMarkerOnRemoval() // the marker has no removal-side writer [04 §3.3]
}

// applyPrimaryResultCode is the PRIMARY result-code table [04 §3.3] C7: it
// maps the handler's return code to queue effects for the head record n.
// Deliberately distinct from applySecondaryResultCode — the segments share
// the code values but not the effects, and re-merging them reintroduces the
// ORD-03 mismatches (secondary code 9 would re-arm, secondary 6/7 would
// tail-yield or cancel-all). Primary specifics here: code 6 rotates to the
// segment tail, code 7 is the exclusive whole-queue cancel, code 9's
// last-record arm re-arms with RNG(30) [R-P0-01].
//
// Returns false only for a returning result. Re-arming n does not establish
// that the current head is blocked: the handler may have inserted another
// record ahead of n. The caller reloads the head [04 R-ORD-01 §10].
func (q *Queue) applyPrimaryResultCode(n *Node, code Code, tick uint32) bool {
	switch code {
	case 0:
		n.Phase = 0 // [04 §3.3]
	case 1:
		n.Phase++ // [04 §3.3]
	case 2, 4:
		// [04 §3.3] continue walking unchanged
	case 3:
		n.DynamicGate = 1                               // [04 §3.3] lowest gate bit
		n.Deadline = int32(tick + 30 + q.randBelow15()) // [04 §3.3][I4] wait 30..44; the only arm drawing RNG(15)
	case 5, 8:
		q.unlinkPrimary(n) // [04 §3.3][05 "Queue subtraction"]
	case 6:
		idx := q.indexOfPrimary(n)
		if idx < 0 {
			return false
		}
		copy(q.primary[idx:], q.primary[idx+1:])
		q.primary = q.primary[:len(q.primary)-1]
		q.primary = append(q.primary, n) // move to segment tail and continue [04 §3.3]
		// The rotate is a link move and nothing else. [04 §3.3]'s code-6 row
		// says "move the record to the tail of its segment and continue" — no
		// flag is named — and [04 R-ORD-01 §13]'s census of the static-mask
		// copy gives the active marker (bit 12) exactly one writer, the
		// producer insertion's after-marker branch. So the rotated record keeps
		// its own bits, including the marker when it held it, and no other
		// record gains one.
		//
		// Correction (WU-19-232). This arm used to clear the marker on the
		// rotated record and then call ensureSingleActive, which handed the
		// marker to the new head. That is the same invented marker write
		// WU-19-227 removed from the removal paths: a `QMove`/`QPatrol` record
		// (whose whole body is a 60-tick delayed rotate, [04 §3.3]) would have
		// moved the queue's insertion point every time it came round, so a
		// Shift-queued order issued after a rotate landed at index 1 instead of
		// at the tail.
	case 7:
		q.cancelAll() // [04 §3.3] free every record on both segments and return; whole-queue cancel is exclusively primary code 7
		return false
	case 9:
		n.Flags |= FlagRetryMark // [04 §3.3][R-ORDER-02 §2] completion flag; its reader is the goal installer of [04 R-PATH-01 §8] step 5.3
		// "Last" is having no record after it in the segment, which is not the
		// same as being the only record: a handler that head-inserts a spawned
		// record [04 R-ORD-01 §1] leaves itself behind that record and can
		// still be the tail.
		if idx := q.indexOfPrimary(n); idx >= 0 && idx == len(q.primary)-1 {
			// [R-P0-01][04 §3.3] last record re-arms: phase reset, wait
			// 30..59 — the distinct RNG(30) arm, not code 3's RNG(15).
			n.Phase = 0
			n.DynamicGate = 1
			n.Deadline = int32(tick + 30 + q.randBelow30())
			return true
		}
		q.unlinkPrimary(n) // [04 §3.3] otherwise unlink and free
	default:
		if code > 9 {
			// [04 §3.3] above 9: single-node expiry helper — unlink, clean,
			// free, and return; no draw, no whole-queue cancel [P0-08].
			// Whole-queue cancel is exclusively code 7 [P0-08] A09.
			q.unlinkPrimary(n)
			return false
		}
		return false
	}
	return true
}

func (q *Queue) pumpSecondary(u *units.Unit, tick uint32) {
	if q == nil || u == nil {
		return
	}
	q.lastPumpTick = tick
	for idx := 0; idx < len(q.secondary); {
		n := q.secondary[idx]
		// Removed (WU-18-0): a per-name normalization stood here, clearing the
		// dynamic gate of a deadline-less `BuildWeapon` record because fresh
		// ones were born carrying their descriptor's static mask (0xc0140) and
		// a rear record with a gate never dispatches. It was the rear-segment
		// half of the same conflation newNode has now dropped, and it was
		// narrower than the defect and wider than the fix: it named one
		// descriptor, and it would equally have cleared a gate the stockpile
		// handler armed without a deadline. A fresh rear record now arrives
		// with an empty gate [04 R-ORD-01 §1], so it is ready by construction.
		//
		// [R-ORDER-02 §1] A rear-segment record is dispatched only when its
		// gate mask is empty or its deadline has arrived; the deadline compare
		// is unsigned, so the -1 sentinel (0xffffffff) reads as not-due.
		deadlineArrived := uint32(n.Deadline) <= tick
		shouldDispatch := n.DynamicGate == 0 || deadlineArrived
		if !shouldDispatch {
			idx++
			continue
		}
		if deadlineArrived {
			n.Deadline = -1 // clear; no expiry bit is set — an arrived deadline satisfies nothing here
		}
		n.DynamicGate = 0
		// [R-ORDER-02 §1] The secondary pump never delivers satisfied bits:
		// the handler is invoked with an EMPTY satisfied set — no expiry bit,
		// no satisfied-word read, and no capability-word consumption. Rear
		// records run purely on their own deadlines; movement or wake bits can
		// never drive them.
		// The per-queue published tick [RS-P0-018]. Every handler now takes the
		// tick as its fourth argument (WU-18-7), so no handler reads this field
		// for its deadlines any more; it stays because it is the queue's own
		// record of the tick it was last pumped at, and the construction service
		// transfers it across a queue rebind [04 R-FAC-02 §4].
		q.secondaryTick = tick
		handler := DescriptorFor(n.ID).Handler
		if handler == nil {
			// The same missing-handler guard as the primary walk above (whose
			// comment carries the reasoning and the census), through the
			// secondary table's code 3 — which parks this record for 30..44
			// ticks and, unlike the primary, continues the walk, so one
			// unrunnable rear-segment record does not hide the records behind
			// it [04 §3.3].
			q.recordDiagnostic(fmt.Sprintf("orders: nil handler for secondary %s, parked for 30..44 ticks", DescriptorFor(n.ID).Name))
			walking := q.applySecondaryResultCode(n, 3, tick)
			if !walking {
				return
			}
			idx = 0 // handled records reload the rear head [04 R-ORD-01 §10]
			continue
		}
		code := handler(u, n, 0, tick)
		walking := q.applySecondaryResultCode(n, code, tick)
		if !walking {
			return // codes 6 and 7: remove the single record and return [04 §3.3] C8
		}
		idx = 0 // handled records reload the rear head [04 R-ORD-01 §10]
	}
}

// applySecondaryResultCode is the SECONDARY result-code table [04 §3.3] C8.
// Deliberately distinct from applyPrimaryResultCode (ORD-03): codes 6 and 7
// remove the single record and return — no tail-yield, no cancel-all — while
// codes 5, 8, 9 and above 9 are plain unlink-and-free removals that continue
// the front-to-back walk. Secondary code 9 sets the completion flag and then
// plainly unlinks and frees with NO re-arm and NO draw, regardless of whether
// the record is last or first [04 §3.3] "Audit note — completion-wait
// ranges"; the above-9 expiry delegate never draws either.
// Returns whether the walk reloads the rear head [04 R-ORD-01 §10].
func (q *Queue) applySecondaryResultCode(n *Node, code Code, tick uint32) bool {
	switch code {
	case 0:
		n.Phase = 0 // [04 §3.3]
		return true
	case 1:
		n.Phase++ // [04 §3.3]
		return true
	case 2, 4:
		return true // [04 §3.3] continue unchanged
	case 3:
		n.DynamicGate = 1                               // [04 §3.3] lowest gate bit
		n.Deadline = int32(tick + 30 + q.randBelow15()) // [04 §3.3][I4] wait 30..44; the only arm drawing RNG(15)
		return true
	case 5, 8:
		q.removeSecondaryRecord(n) // [04 §3.3] C8 plain unlink+free, walk continues
		return true
	case 6:
		q.removeSecondaryRecord(n) // [04 §3.3] C8 remove the single record and return; no tail-yield
		return false
	case 7:
		q.removeSecondaryRecord(n) // [04 §3.3] C8 remove the single record and return; no cancel-all
		return false
	case 9:
		n.Flags |= FlagRetryMark // [04 §3.3][R-ORDER-02 §2] completion flag; its reader is the goal installer of [04 R-PATH-01 §8] step 5.3
		// [04 §3.3] plain unlink+free — no re-arm, no draw, regardless of
		// last/first position (the primary-only last-record re-arm [R-P0-01]
		// does not apply to the secondary pump).
		q.removeSecondaryRecord(n)
		return true
	default:
		if code > 9 {
			// [04 §3.3] C8 expiry delegate: plain unlink+free, no draw, and
			// the walk continues like the other plain removals.
			q.removeSecondaryRecord(n)
			return true
		}
		return false
	}
}

// removeSecondaryRecord unlinks and frees one secondary record [04 §3.3]
// [05 "Queue subtraction"]: the record is always tombstoned because the
// tombstone test compares against the front anchor regardless of segment, so
// BuildWeapon/SelfDestruct removals never emit the weapon-target-clear
// notification.
func (q *Queue) removeSecondaryRecord(n *Node) {
	n.Flags |= FlagTombstone
	q.cleanupNode(n)
	q.spliceOutSecondary(n) // by identity after the cleanup
}

// Mobile-build blocked-area retry budget [R-ORDER-02 §1]. The record's third
// parameter is the blocked-area retry counter [04 §3.2] (the record's
// progress field, reused; the handler's setup path zeroes it, so a fresh or
// re-armed record starts the budget at zero). On a blocked approach visit the
// mobile-build handler notifies "Waiting for target area to clear",
// increments the counter, and waits EXACTLY 30 ticks — a fixed wait with no
// random draw — while the counter is at most 10; the first blocked visit
// whose counter is already above 10 notifies "Target area was blocked" and
// abandons (code 8, remove). Eleven 30-tick waits, then give-up on visit
// twelve.
const (
	// MobileBuildBlockedWaitTicks is the fixed blocked-visit wait; the traced
	// arm draws no random value, unlike the pump's code-3 wait.
	MobileBuildBlockedWaitTicks uint32 = 30 // [R-ORDER-02 §1]
	// MobileBuildBlockedGiveUpAbove is the counter value above which the next
	// blocked visit gives up: waits happen while the counter is at most 10.
	MobileBuildBlockedGiveUpAbove uint32 = 10 // [R-ORDER-02 §1]
)

// Retail notifies these strings verbatim as the blocked-area status text
// [R-ORDER-02 §1].
const (
	MobileBuildWaitingText = "Waiting for target area to clear"
	MobileBuildBlockedText = "Target area was blocked"
	// MobileBuildUnreachableText is the approach-failure text of the same row
	// [04 R-ORD-01 §5, the `MobileBuild` row].
	MobileBuildUnreachableText = "I can't reach the construction site"
)

// MobileBuildUnreachableVisit is the approach-failure arm of the mobile-build
// row [04 R-ORD-01 §5]: "Phase 1: when satisfied has `0x40`, run the reach test
// against the product footprint; out of reach → status 7 `I can't reach the
// construction site`, abandon." `0x40` is the route publisher's "cannot get
// there" notification — an empty publication raised while the mover is not at
// the goal [04 R-PATH-01 §7][04 R-COLL-01 §6] — and it is the ONLY thing that
// ends an approach the search cannot satisfy: the follower re-requests every 60
// ticks forever with no retry ceiling [04 R-MOV-01 §7], so a record that
// ignores the bit walks nowhere and waits for a wake that will never differ.
//
// The caller supplies the reach verdict, because the reach test measures
// against the product's footprint and internal/construction owns the product
// definition; it notifies the returned text through its own status surface and
// applies code 8 (abandon: unlink and free the single record, [04 §3.3]).
// `satisfied` is the record's accumulated pending word. With the bit absent, or
// the mover already in reach, the caller keeps its approach: code 2, continue
// unchanged.
func MobileBuildUnreachableVisit(satisfied uint32, outOfReach bool) (statusText string, code Code) {
	if satisfied&pendNoRoute == 0 || !outOfReach {
		return "", 2
	}
	return MobileBuildUnreachableText, 8
}

// MobileBuildBlockedVisit is one blocked-visit step of the mobile-build
// budget for the record n at tick. It returns the verbatim status text to
// notify and the pump result code the caller returns: code 2 (continue) with
// the wait armed — lowest gate bit plus deadline tick+30 exactly, the pump's
// blocked-head stall re-dispatching on deadline arrival — or code 8
// (abandon/remove) once the counter has passed its budget. The counter lives
// in n.Param3 [04 §3.2]; the caller notifies the returned text through its
// own status surface. A nil record gives up without touching anything.
func MobileBuildBlockedVisit(n *Node, tick uint32) (statusText string, code Code) {
	if n == nil {
		return MobileBuildBlockedText, 8
	}
	if n.Param3 > MobileBuildBlockedGiveUpAbove {
		return MobileBuildBlockedText, 8
	}
	n.Param3++
	// Arm the exact 30-tick wait: lowest gate bit stalls the head, and the
	// pump's deadline expiry sets that bit as satisfied on arrival [04 §3.3].
	// The wait draws no random value [R-ORDER-02 §1].
	n.DynamicGate = 1
	n.Deadline = int32(tick + MobileBuildBlockedWaitTicks)
	return MobileBuildWaitingText, 2
}

// RemoveHead removes the primary segment's front record with the ordinary
// removal cleanup.
//
// Fixed (WU-19-73): this used to drop slot 0 positionally — `q.primary =
// q.primary[1:]` — after the cleanup had already run. It crashed a `Coast to
// Coast` skirmish at tick 3865 with `slice bounds out of range [1:0]`: the head
// was a `MobileBuild` record carrying gate bit 1 (gate `0xa`), so the cleanup
// sent [R-ORDER-02 §2]'s cancel notification, which for a construction row is
// the production machine's cancel-current — and cancel-current's own epilogue
// removes the head. The segment was already empty when the positional re-slice
// ran. On a queue with a record behind the head the same double removal would
// have silently dropped that record instead of crashing. The unlink now goes
// through spliceOutPrimary, which re-locates the record by identity after the
// cleanup and removes nothing when the notification already removed it.
func (q *Queue) RemoveHead() *Node {
	if q == nil || len(q.primary) == 0 {
		return nil
	}
	n := q.primary[0]
	q.cleanupNode(n)
	q.spliceOutPrimary(n)
	q.releaseMarkerOnRemoval() // the marker has no removal-side writer [04 §3.3]
	if n != nil {
		n.MoveState = MoveArrived
	}
	return n
}

// Head is the front segment's first record, the one the primary pump
// dispatches next [04 §3.3]. It is nil for an empty segment.
func (q *Queue) Head() *Node {
	if q == nil || len(q.primary) == 0 {
		return nil
	}
	return q.primary[0]
}

// QueueForUnit returns the unit's order queue, creating an empty one when the
// unit has none. Every producer enters through it; a read-only path must use
// QueueOfUnit instead, which creates nothing.
func QueueForUnit(u *units.Unit) *Queue {
	if u == nil {
		return nil
	}
	if q, ok := u.Orders.(*Queue); ok && q != nil {
		return q
	}
	q := &Queue{}
	u.Orders = q
	return q
}

// QueueOfUnit returns the unit's existing order queue without creating one.
// Read-only paths such as frame publication must use this lookup so observing
// a unit cannot mutate its authoritative order state [03 §1][04 §3.3].
func QueueOfUnit(u *units.Unit) *Queue {
	if u == nil {
		return nil
	}
	q, _ := u.Orders.(*Queue)
	return q
}

// BindQueue installs q as the unit's order queue, replacing any queue the unit
// already carried and releasing that queue's bound goals.
func BindQueue(u *units.Unit, q *Queue) {
	if u == nil {
		return
	}
	if q != nil {
		prior := QueueOfUnit(u)
		if prior != nil && prior != q && prior.binding != nil {
			prior.releaseBoundGoals()
		}
	}
	if q != nil && q.binding == nil {
		// Queue replacement is a lifecycle boundary: preserve the session
		// context from the replaced queue unless the producer supplied a new
		// concrete binding explicitly [P0-00 A.1][04 §3.3].
		if prior := QueueOfUnit(u); prior != nil && prior.binding != nil {
			q.SetBinding(prior.binding)
		}
	}
	if q != nil && q.ownedHandlers == nil {
		// The owned-row registrations of queue_handlers.go transfer across the
		// same lifecycle boundary, for the same reason the binding does: they
		// are the owning subsystems' statement about the unit, not about the
		// queue object, and a replacement that arrived without them would put
		// the pump back to writing result codes over a live state machine
		// until the next bind.
		if prior := QueueOfUnit(u); prior != nil && prior.ownedHandlers != nil {
			q.ownedHandlers = append([]OwnedHandler(nil), prior.ownedHandlers...)
		}
	}
	u.Orders = q
}

// releaseBoundGoals is the queue-replacement lifecycle edge. It only releases
// movement payloads; order-record cleanup remains the removal path's owner.
// Movement checks node identity, so a late cleanup cannot detach a successor.
func (q *Queue) releaseBoundGoals() {
	if q == nil || q.binding == nil || q.binding.Movement == nil || q.binding.Movement.Release == nil {
		return
	}
	for _, n := range q.primary {
		if n != nil {
			q.binding.Movement.Release(n)
		}
	}
	for _, n := range q.secondary {
		if n != nil {
			q.binding.Movement.Release(n)
		}
	}
}

// RemovePrimaryNode removes one primary node in place, preserving queue
// identity and the queue's concrete binding (including Economy), plus its
// secondary tick and diagnostics. Callers that rebuilt the segment into a
// fresh Queue silently dropped those hooks, so successor
// orders lost target lookup and stockpile admission after a construction
// removal.
//
// Removal follows the established subtraction order [04 §3.3][05 "Queue
// subtraction"]: the node is marked per tombstone rules, cleanup runs exactly
// once, and the segment is spliced. The active marker is not rewritten — it
// has no removal-side writer at all, see releaseMarkerOnRemoval.
//
// tombstone selects the marker applied before cleanup. Retail exempts the
// primary head from the tombstone [04 §3.3]; callers that must preserve an
// older unconditional marking pass true explicitly.
//
// The node is matched by pointer identity; when that fails the head is
// accepted if it carries the same order ID and first parameter. Returns the
// removed node, or nil when nothing matched.
func (q *Queue) RemovePrimaryNode(node *Node, tombstone bool) *Node {
	if q == nil || len(q.primary) == 0 {
		return nil
	}
	idx := -1
	for i, n := range q.primary {
		if n == node {
			idx = i
			break
		}
	}
	if idx == -1 {
		head := q.primary[0]
		if node != nil && head != nil && head.ID == node.ID && head.Param1 == node.Param1 {
			idx = 0
		} else {
			return nil
		}
	}
	removed := q.primary[idx]
	if removed != nil {
		if tombstone || idx != 0 {
			removed.Flags |= FlagTombstone
		}
		removed.Flags &^= FlagActive
		q.cleanupNode(removed)
	}
	// By identity after the cleanup, not by the index taken before it: the
	// cleanup's cancel notification can unlink this record itself or insert
	// ahead of it (see spliceOutPrimary).
	q.spliceOutPrimary(removed)
	q.releaseMarkerOnRemoval() // the marker has no removal-side writer [04 §3.3]
	return removed
}

// CancelAll is the exported entry to result code 7's whole-queue cancel
// [04 §3.3][05 "Queue pumping and result codes"]. It preserves queue identity
// and every queue-owned service binding; callers must never express a cancel
// by rebinding a fresh Queue to the unit.
func (q *Queue) CancelAll() {
	if q == nil {
		return
	}
	q.cancelAll()
}
