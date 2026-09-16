// Insertion and removal for the order queue: the record constructor, the
// producer insertion of [04 §3.3] and [05 "Queue insertion"], the two other
// insertion shapes of [04 R-ORD-01 §13] (the handler head insert and the shared
// tail append), the removals of [05 "Queue subtraction"], and the cleanup every
// removal runs. Moved out of pump.go by CL-5 with no other change; the pump
// itself — the walk, the result codes and the idle refill — stays there.

package orders

import (
	"fmt"
	"slices"

	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// staticPurgeSurvivor is bit 2 of a descriptor's static gate mask. It is the
// purge-survivor bit: the keep-survivors purge a non-queued (Replace) issue
// runs removes every front-segment record whose static-mask copy lacks it
// [04 §3.3][04 R-MOV-03 §6]. The nine descriptors that carry it are
// `MakeSelectable`, `Wait`, `AttackUType`, `WaitForAttack`, `GetBuilt`,
// `BeCarried`, `Paralyze`, `SelfRepair` and `BuildingBuild` [04 §3.1].
const staticPurgeSurvivor uint32 = 0x4

// staticHeadInsert is bit 5 of a descriptor's static gate mask. It is the
// head-insert selector of the producer insertion: a record carrying it skips
// the after-marker path entirely and is head-inserted into the segment bit 18
// selects, with no write to the active marker [04 R-ORD-01 §13]. The ten
// descriptors that carry it are `Activate`, `Deactivate`, `Cloak_On`,
// `Cloak_Off`, `Standing_MoveOrder`, `Standing_FireOrder`, `Paralyze`,
// `GetBuilt`, `BeCarried` and `Guard_NoMove` [04 §3.1].
//
// Closed (RWU-19-197, [04 R-ORD-01 §16]): the template arrays were re-read
// row by row and the mask column is exactly the ten above. [04 R-ORD-01 §13]'s
// prose list also named `SelfRepair` and `WaitForAttack`; that list was wrong
// and is corrected in place — their words are 0x1000204 and 0x204 (table.go),
// so a producer-inserted record of either takes the after-marker path. The
// branch below reads the record's own mask, which was right all along.
const staticHeadInsert uint32 = 0x20

// staticRearSegment is bit 18: "marks a record that belongs in the rear queue
// segment" [04 §3.1]. It selects the segment for every insertion shape —
// producer, handler spawn, patrol append and the pump's own idle refill — and
// only `BuildWeapon` and `SelfDestruct` carry it.
const staticRearSegment uint32 = 0x40000

// newNode is the record constructor every insertion path goes through.
//
// Correction (WU-18-0). This function used to seed the record's DYNAMIC gate
// from the descriptor's STATIC mask:
//
//	if nn.DynamicGate == 0 { nn.DynamicGate = desc.StaticGate }
//
// The two are different fields with different meanings and must not be
// conflated. The static mask is insertion metadata: [04 §3.1]'s census names
// bit 9 (0x200) "constructed without a target unit clears it", bit 10 (0x400)
// the same for a goal position, bit 18 (0x40000) rear-segment selection, and
// bit 20 (0x100000) the nanolathe/build-site class; every other static bit has
// no located reader and is stored opaque. Not one of them is a thing to wait
// for. The dynamic gate is the opposite field: [04 §3.3] step 2 intersects it
// with the record's own satisfied bits and the unit's capability word, and
// step 3 stops the whole walk when the gate is nonzero and nothing in it is
// satisfied. Seeding it with insertion metadata therefore made a record ask to
// be woken by bits nothing raises — a `Capture` or `Reclaim` record parked at
// the head of its unit's primary queue forever, taking every order behind it
// down with it, and never reaching the pump's missing-handler diagnostic.
//
// The contract is explicit: "the record constructor zeroes the dynamic gate and
// the pending word, so a freshly inserted record is dispatched on its very next
// pump visit with an empty satisfied set" [04 R-ORD-01 §1]. A record waits only
// for what a handler asks it to wait for; the copy of the static mask is kept,
// because [04 §3.2] gives the record a static-mask copy field of its own.
func newNode(id ID, n Node) *Node {
	desc := DescriptorFor(id)
	nn := n
	nn.ID = id
	if nn.StaticGate == 0 {
		nn.StaticGate = desc.StaticGate
	}
	// [04 §3.1]'s constructor clear: static bit 9 (0x200, staticTargetObserver
	// — "this record was issued against a target") is cleared on the record's
	// own static-mask copy when no target unit was supplied to the
	// constructor, closed by [04 R-MOV-03 §7] ("clears 0x200 from the
	// static-mask copy when no target was supplied"). Target zero (pool.Handle's
	// null) is this build's "no target supplied", matching every existing
	// target-presence test in the package [04 §3.2]. This is what lets a
	// reader distinguish "issued without a target" from "issued against a
	// target that has since gone" by the bit alone, e.g. the shared air-attack
	// entry's step 2 [04 R-AIR-01 §16].
	//
	// The same established fact lists a second constructor clear — bit 10
	// (0x400, staticGoalObserver) when no goal position was supplied. Retail
	// reads presence off an optional argument; this build passes the goal by
	// value, so the producer states presence in Node.GoalSupplied and the clear
	// reads that, never the coordinates. A zero-coordinate test would be a
	// guess, and a wrong one: the fixed-point origin is a legal map position,
	// so it would strip the bit from a record whose goal really is (0,0,0).
	// The bit's one reader is the guard's leg-4 copy arm in guard.go, which
	// takes "this record has a goal" from it [04 R-ORD-01 §13].
	if nn.Target == 0 {
		nn.StaticGate &^= staticTargetObserver
	}
	if !nn.GoalSupplied {
		nn.StaticGate &^= staticGoalObserver
	}
	if nn.StaticGate&staticTargetObserver == 0 {
		nn.BindTarget(0) // constructor unlinks the reference [04 R-MOV-03 §7]
	}
	// Survivorship is a property of the record's descriptor, not of the queue
	// modifier that inserted it. This used to be set from the caller's
	// queued/non-queued flag instead, which had the two halves of [04 §3.3]
	// backwards: a shift-queued `Move_Ground` survived a later Replace it
	// should not have, and a factory's `BuildingBuild` — which carries bit 2
	// — was purged by the first plain move order the player gave the factory,
	// which is what stopped a factory with a rally point from producing.
	if nn.StaticGate&staticPurgeSurvivor != 0 {
		nn.Flags |= FlagPurgeSurvivor
	}
	if nn.Deadline == 0 {
		nn.Deadline = -1
	}
	// The caption-pending flag is deliberately NOT armed here. The record
	// constructor writes the static-mask copy from the descriptor's own mask and
	// arms no runtime bit at all [04 R-ORD-01 §13]; the arming belongs to the
	// producer-side insertion helper, which is Push / PushSecondary below.
	//
	// The queue modifier is an argument to that helper, not a record field
	// [04 §3.2], so the stored record never carries it. The goal-presence
	// argument is the same shape: it has been consumed into the static-mask
	// copy above, and retail's record has no field for it [04 §3.2].
	nn.QueuedIssue = false
	nn.GoalSupplied = false
	node := &Node{}
	*node = nn
	return node
}

// Diagnostics returns this queue's dispatch failures.
func (q *Queue) Diagnostics() []string {
	if q == nil {
		return nil
	}
	return append([]string(nil), q.diagnostics...)
}

// ClearDiagnostics drops the recorded dispatch failures.
func (q *Queue) ClearDiagnostics() {
	if q != nil {
		q.diagnostics = nil
	}
}

func (q *Queue) recordDiagnostic(msg string) {
	if q == nil {
		return
	}
	//  bound diagnostics to 256 entries to prevent per-tick unbounded growth when descriptors have nil handlers by design.
	const maxDiagnostics = 256
	if len(q.diagnostics) >= maxDiagnostics {
		copy(q.diagnostics, q.diagnostics[1:])
		q.diagnostics = q.diagnostics[:maxDiagnostics-1]
	}
	q.diagnostics = append(q.diagnostics, msg)
}

// cancelAll frees every record on both segments [04 §3.3] result code 7 and
// [05 "Queue pumping and result codes"]. Non-head primary records and every
// secondary record are tombstoned, which is what suppresses their
// weapon-target-clear notification [05 "Queue subtraction"].
func (q *Queue) cancelAll() {
	// Cancellation can remove a construction record from inside its cleanup.
	// Snapshot the traversal so that splice cannot skip the following record
	// or visit a shifted tail twice [04 R-MOV-03 §6][04 R-ORDER-02 §2].
	primary := slices.Clone(q.primary)
	for i, n := range primary {
		if !slices.Contains(q.primary, n) {
			continue // an earlier cancel notice already removed and cleaned it
		}
		if i != 0 {
			n.Flags |= FlagTombstone
		}
		q.cleanupNode(n)
		q.spliceOutPrimary(n)
	}
	for _, n := range slices.Clone(q.secondary) {
		if !slices.Contains(q.secondary, n) {
			continue
		}
		n.Flags |= FlagTombstone
		q.cleanupNode(n)
		q.spliceOutSecondary(n)
	}
	q.primary = nil
	q.secondary = nil // via the pair-removal helper [05]
}

// ownerUnit resolves a record's owning unit through the queue's binding
// lookup. It returns nil when no lookup is installed (bare fixtures), which
// leaves every callback arrange a no-op.
func (q *Queue) ownerUnit(n *Node) *units.Unit {
	if q == nil || n == nil || n.Owner == 0 {
		return nil
	}
	binding := q.Binding()
	if binding == nil || binding.Lookup == nil {
		return nil
	}
	return binding.Lookup(n.Owner)
}

// cleanupNode runs the strict record-removal cleanup order [R-ORDER-02 §2]:
//
//  1. restore the record identity — records are named Go fields (I13),
//     nothing to restore;
//  2. when the record's dynamic gate mask — the same field the pump consumes
//     — still holds bit 1 (value 2) at removal, invoke the operation handler
//     with that cancel-notification mask: a record removed while waiting on
//     that bit delivers the cancel-current notification through its own
//     handler. The return code is ignored; the record is already being freed.
//     This step runs regardless of the tombstone;
//  3. emit the StopBuilding counterpart when the record carries the pending
//     flag — on every removal path and NOT tombstone-gated;
//  4. release the presentation payload — records carry none today, and the
//     owner's displayed-payload latch has no record payload to point at;
//  5. ONLY for a non-tombstoned record, run the weapon-target-clear helper
//     (TargetCleared). The tombstone is set at removal time on every freed
//     record except the primary segment's front head at that moment; the
//     comparison is always against the front anchor regardless of which
//     segment the record occupied, so rear-segment records are always
//     tombstoned and never emit it.
func (q *Queue) cleanupNode(n *Node) {
	if n == nil {
		return
	}
	u := q.ownerUnit(n)
	if n.DynamicGate&2 != 0 && u != nil { // cancel-notification guard: dynamic gate bit 1 (value 2) [R-ORDER-02 §2]
		if h := DescriptorFor(n.ID).Handler; h != nil {
			_ = h(u, n, 2, q.lastPumpTick)
		} else if q.binding != nil && q.binding.Work != nil && q.binding.Work.CancelNotice != nil {
			// A record another package's state machine runs has no descriptor
			// handler here, so the notification goes to that package's receiver
			// instead. Retail draws no distinction: it invokes the operation
			// handler compiled into the descriptor, and for the three
			// construction rows that handler IS the factory production machine.
			_ = q.binding.Work.CancelNotice(u, n, q.lastPumpTick)
		}
	}
	emitStopBuilding(u, n)
	if q.binding != nil && q.binding.Movement != nil && q.binding.Movement.Release != nil {
		q.binding.Movement.Release(n)
	}
	// "The record destructor returns all three slots (with their targets
	// cleared) for every removed record whose static-mask copy lacks bit 16"
	// [04 R-UNIT-06 §5 part 3] — which is what hands a completed or purged
	// attack's slots back to autonomous acquisition. Bit 16 excludes the rows
	// that never took a slot in the first place: `Activate`, `Deactivate`, the
	// two cloak toggles, the two standing-order rows and `BuildingBuild`, whose
	// removal must not wipe the weapon targets an acquisition put there.
	if n.Flags&FlagTombstone == 0 && n.StaticGate&staticSlotKeeper == 0 {
		clearWeaponBuildTargets(u)
	}
}

// PurgeUnprotected is the Replace purge of [05 "Queue insertion"]: issuing a
// non-queued order first frees every front-segment record lacking the
// purge-survivor bit. It writes no active marker — the producer insertion that
// follows does [04 R-ORD-01 §13].
func (q *Queue) PurgeUnprotected() {
	if q == nil || len(q.primary) == 0 {
		return
	}
	// [05 "Queue insertion"] non-queued issue purges primary nodes lacking the protected flag.
	//
	// The doomed records are chosen from a snapshot and unlinked one at a time
	// by identity, because each cleanup re-enters the queue (see
	// spliceOutPrimary): the segment the loop started with is not the segment
	// that exists after the first notification. The filter-in-place form that
	// stood here compacted survivors into the same backing array it was still
	// reading, so its front-head test compared against a slot a survivor had
	// already been written into, and its final assignment reinstated whatever
	// a notification had removed.
	doomed := make([]*Node, 0, len(q.primary))
	head := q.primary[0]
	for _, n := range q.primary {
		if n != nil && n.Flags&FlagPurgeSurvivor == 0 {
			doomed = append(doomed, n)
		}
	}
	for _, n := range doomed {
		// non-head gets tombstone per [04 §3.3]; the head at the moment the
		// removal was decided is the anchor, so the test is made here and not
		// after the cleanup has moved records around.
		if n != head {
			n.Flags |= FlagTombstone
		}
		q.cleanupNode(n)
		q.spliceOutPrimary(n)
	}
	// No marker write. The purge helper "removes every front-chain record whose
	// static-mask copy lacks bit 2 … every removal tombstones unless the record
	// is the front head, runs the cleanup, and frees" [04 R-MOV-03 §6] — that is
	// the whole helper, and [04 R-ORD-01 §13]'s runtime-bit census gives the
	// active marker one writer, the producer insertion's after-marker branch.
	//
	// Correction (WU-19-232). This used to end by moving the marker onto
	// primary[0]. When the purge freed the marked record the segment is simply
	// left unmarked, and [04 §3.1]'s insertion rule then takes its other arm
	// ("with no marked record it appends at the tail") — which is what a
	// Replace wants: the single new record lands behind whatever survivors the
	// purge kept, not in front of them. Same defect shape as WU-19-227's.
	//
	// [04 R-ORD-01 §15] names the exact queue this used to corrupt: a factory
	// product's is `[GetBuilt, BeCarried]` — both purge survivors — "with
	// neither record carrying the active marker". A non-queued order issued to
	// such a unit therefore appends behind both; the invented head marker put
	// it between them.
}

// hasLeadingAutoOp reports whether either segment leads with an auto/default
// record. It is only a fast path for Push: since WU-19-232 the drop writes no
// active marker, so calling it on a queue that leads with no auto record is a
// no-op either way [04 R-ORD-01 §13].
func (q *Queue) hasLeadingAutoOp() bool {
	if q == nil {
		return false
	}
	if len(q.primary) > 0 && q.primary[0].Flags&FlagAutoOp != 0 {
		return true
	}
	return len(q.secondary) > 0 && q.secondary[0].Flags&FlagAutoOp != 0
}

// DropLeadingAutoOps is the leading-auto drop of [05 "Queue insertion"]:
// "issuing any primary order also drops leading auto/default-op nodes wherever
// they currently live". It is a step inside the producer insertion, which is
// why Push calls it rather than each producer.
func (q *Queue) DropLeadingAutoOps() {
	if q == nil {
		return
	}
	// [05 "Queue insertion"] issuing any primary order drops leading auto/default-op nodes – leading RUN at front of each segment
	// Each drop is unlinked by identity after its cleanup, for the reason
	// spliceOutPrimary documents: the cleanup's cancel notification re-enters
	// the queue, so re-slicing past slot 0 afterwards would drop whichever
	// record has since taken the front — or run off an emptied segment.
	for len(q.primary) > 0 && q.primary[0].Flags&FlagAutoOp != 0 {
		n := q.primary[0]
		q.cleanupNode(n)      // head not tombstoned
		q.spliceOutPrimary(n) // no-op when the notification already unlinked it
	}
	for len(q.secondary) > 0 && q.secondary[0].Flags&FlagAutoOp != 0 {
		n := q.secondary[0]
		n.Flags |= FlagTombstone // secondary always tombstoned [04 §3.3]
		q.cleanupNode(n)
		q.spliceOutSecondary(n) // no-op when the notification already unlinked it
	}
	// No marker write, for the same reason PurgeUnprotected has none. The drop
	// is a step *inside* the producer insertion — "after the purge and the
	// leading-auto drop it sets bit 0, conditionally sets bit 13, and then
	// branches" [04 R-ORD-01 §13] — and the marker is written only by the
	// branch that follows, on the record the producer is inserting. Dropping a
	// leading auto record that happened to carry the marker leaves the segment
	// unmarked, and the insertion then appends at the tail (WU-19-232).
}

// Push is the producer-side insertion — the one helper the HUD, the AI, COB,
// the mission spawner, rally inheritance and factory completion all enter
// through [04 §3.3]. It constructs the record, drops leading auto/default
// records, arms the caption-pending flag on a non-queued issue, and then takes
// ONE OF TWO branches on the new record's static-mask copy [04 R-ORD-01 §13]:
//
//	bit 5 and bit 18 both clear — insert immediately after the active marker
//	(tail when nothing carries it), and move the marker onto the new record;
//
//	bit 5 or bit 18 set — HEAD-insert into the segment bit 18 selects,
//	inheriting the displaced head's auto-op flag, with NO marker write at all.
//
// Bit 5 (staticHeadInsert) is carried by `Paralyze`, `BeCarried`, `GetBuilt`,
// `Guard_NoMove`, the cloak pair, the activation pair and the two
// standing-order descriptors; bit 18 (staticRearSegment) by `BuildWeapon` and
// `SelfDestruct` [04 §3.1]. So a paralyzer hit lands at the FRONT of the queue
// — which is what makes [04 R-ORD-01 §2]'s "later paralyzer hits add to p1 of
// the waiting head record" reachable — a stance or cloak toggle takes effect
// before the order the unit is running, and a player-issued `BuildWeapon`
// lands on the REAR segment where its own pump drives it, instead of blocking
// the silo's front queue.
//
// Retail's helper also runs the Replace purge itself, gated on the new
// record's bit 6; in this build the purge is caller-side (PurgeUnprotected),
// which is why Push does not run it here. That split predates this unit.
func (q *Queue) Push(id ID, n Node) {
	if q == nil {
		return
	}
	queued := n.QueuedIssue // the modifier is an argument, never record state [04 R-ORD-01 §13]
	node := newNode(id, n)  // [04 §3.3][05 "Queue insertion"] C9
	segment := &q.primary
	if node.StaticGate&staticRearSegment != 0 {
		segment = &q.secondary
	}
	// [P1-I09] dynamic storage: retail has no cap (NEGATIVE-BOUNDED); previous
	// 64/32 caps were inside stock (corpus max 105 raw tokens -> 64 truncated).
	// Now unbounded with OOM guard far outside stock (10000 >> 105).
	if len(*segment) >= OOMGuardQueue {
		q.recordDiagnostic(fmt.Sprintf("orders: queue OOM guard (%d), dropping %s", len(*segment), DescriptorFor(id).Name))
		return
	}
	// "Issuing a front-segment record drops leading auto/default records (those
	// carrying the auto-op flag) wherever they live" [04 §3.3]. Push is the
	// common insertion path every producer enters through, so the drop belongs
	// here and not at each producer: without it the pump's own idle refill
	// (refillIdle below) parks a standing `Standby` / `VTOL_Standby` record at
	// the head, and every later order queues behind a record whose gate no
	// producer in this build can satisfy.
	//
	// The drop is skipped for a record bound for the REAR segment: retail's
	// step runs only when the new record's static-mask copy lacks bit 18
	// [04 R-ORD-01 §13], so queueing a nuke does not silence a silo's standing
	// record.
	if node.StaticGate&staticRearSegment == 0 && q.hasLeadingAutoOp() {
		q.DropLeadingAutoOps()
	}
	// Arm the one-shot caption-pending flag. The producer-side insertion helper
	// is its only writer, and it arms the bit only on the NON-QUEUED (Replace)
	// issue [04 R-ORD-01 §13] — a Shift-queued order is inserted silent.
	if !queued {
		node.CaptionPending = true
	}
	if node.StaticGate&(staticHeadInsert|staticRearSegment) != 0 {
		// Head insert into the selected segment, inheriting the displaced
		// head's auto-op flag. The active marker is not read and not written:
		// it stays exactly where it was, including "nowhere" [04 R-ORD-01 §13].
		node.Flags &^= FlagActive
		if len(*segment) > 0 {
			node.Flags |= (*segment)[0].Flags & FlagAutoOp
		}
		*segment = append([]*Node{node}, *segment...)
		return
	}
	// The inserted record takes the active marker unconditionally, and the
	// record that held it loses it [04 R-ORD-01 §13]. That is what makes
	// repeated interface adds queue FIFO behind the running order; when nothing
	// held the marker the record still lands at the TAIL, and still takes the
	// marker, so the following add queues behind it rather than at the head.
	act := findActive(q)
	if act >= 0 {
		pos := act + 1
		q.primary = append(q.primary, nil)
		copy(q.primary[pos+1:], q.primary[pos:])
		q.primary[pos] = node
		q.primary[act].Flags &^= FlagActive
	} else {
		q.primary = append(q.primary, node)
	}
	node.Flags |= FlagActive
}

// PushHead is the handler-side head insert [04 R-ORD-01 §1]: a record a
// handler spawns goes to the FRONT of the primary segment, so the spawned
// order runs before the spawning one resumes, and the displaced head's
// auto/default-operation flag is inherited by the new head.
//
// It is deliberately not Push. Push is the interface insertion, which places a
// new record immediately AFTER the active marker so that repeated player adds
// queue first-in-first-out behind the running order [04 §3.1]. A spawn is the
// other shape: `Stop`'s `VTOL_LandIfCan`, the kamikaze arrival's
// `SelfDestruct`, the guard's auto-engage attack — all of them run before the
// record that asked for them [04 R-ORD-01 §2, §3].
//
// The spawned record's dynamic gate is the caller's value verbatim. A freshly
// allocated record awaits nothing [04 R-ORD-01 §1], and every documented spawn
// site that does wait on something states its own gate ("gate = 0",
// "gate |= 0xE0"). This used to be a difference from Push, whose records took
// the descriptor's static mask; since WU-18-0 corrected newNode both insertion
// paths produce a record with an empty gate unless the caller asks for one.
//
// Settled by trace (WU-19-159). The head-insert helper writes the link, the
// owner and the inherited auto-op flag and **nothing else** — it never reads or
// writes the active-marker word [04 R-ORD-01 §13]. So the marker stays exactly
// where it was, including "nowhere": a spawn into an empty segment leaves the
// segment unmarked, and the next producer insertion then appends at the tail.
// The ensureSingleActive call that used to close this function contradicted
// that — it handed the marker to the new head whenever no record held one,
// which is a marker retail does not create.
func (q *Queue) PushHead(id ID, n Node) *Node {
	if q == nil {
		return nil
	}
	if len(q.primary) >= OOMGuardQueue {
		q.recordDiagnostic(fmt.Sprintf("orders: primary queue OOM guard (%d), dropping spawned %s", len(q.primary), DescriptorFor(id).Name))
		return nil
	}
	node := newNode(id, n)
	node.DynamicGate = n.DynamicGate // verbatim: the descriptor's static mask is insertion metadata, not a wait [04 §3.1]
	node.Flags &^= FlagActive
	if len(q.primary) > 0 {
		node.Flags |= q.primary[0].Flags & FlagAutoOp // inherit the displaced head's auto flag [04 R-ORD-01 §1]
	}
	q.primary = append([]*Node{node}, q.primary...)
	return node
}

// appendTail is the shared tail append of [04 R-ORD-02 §4] — the helper the
// patrol-chain setup and `VTOL_Follow`'s hand-off to `VTOL_SeekGuard` both use:
// walk the segment the record's rear-segment flag selects to its last link and
// append, setting the record's owner and clearing its next link.
//
// It is not the producer insertion, so it writes no active marker.
// [04 R-ORD-02 §4] states the rule for this helper directly — "no flag is
// inherited (contrast the head insert of [R-ORD-01 §1])" — and [04 R-ORD-01
// §13]'s runtime-bit census gives bit 12 exactly ONE writer, the producer
// insertion's after-marker branch in Push. The three insertion shapes it
// enumerates are the producer insertion, the handler head insert and the
// internal-auto creation; the census names this helper only in the bit-13
// paragraph, as one of the paths that "leave it clear".
//
// Correction (this unit). The append used to hand the marker to the record it
// created whenever the primary segment had been empty, which made it a second
// writer of bit 12. That is the same invented head marker WU-19-227 and
// WU-19-232 removed from the removals, the tail rotate, the Replace purge, the
// leading-auto drop and the save restore: "no record carries the marker" is a
// state retail reaches and relies on, because [04 §3.1]'s insertion rule then
// takes its other arm and appends at the tail. A marker invented here on a
// patrol waypoint or a seek-guard hand-off put the next Shift-queued order at
// index 1, in front of the rest of the queue.
func (q *Queue) appendTail(id ID, n Node) *Node {
	if q == nil {
		return nil
	}
	segment := &q.primary
	if isSecondary(id) {
		segment = &q.secondary
	}
	if len(*segment) >= OOMGuardQueue {
		q.recordDiagnostic(fmt.Sprintf("orders: queue OOM guard (%d), dropping patrol waypoint %s", len(*segment), DescriptorFor(id).Name))
		return nil
	}
	node := newNode(id, n)
	node.Flags &^= FlagActive
	*segment = append(*segment, node)
	return node
}

// PushSecondary head-inserts into the rear segment, inheriting the displaced
// head's auto-op flag [04 §3.3]. The rear segment has no active marker.
func (q *Queue) PushSecondary(id ID, n Node) {
	if q == nil {
		return
	}
	// [P1-I09] dynamic: OOM guard far outside stock (maxSecondary 1 in corpus >> 32 old cap not hit but dynamic is correct retail).
	if len(q.secondary) >= OOMGuardQueue {
		q.recordDiagnostic(fmt.Sprintf("orders: secondary queue OOM guard (%d), dropping %s", len(q.secondary), DescriptorFor(id).Name))
		return
	}
	node := newNode(id, n)
	if len(q.secondary) > 0 && q.secondary[0].Flags&FlagAutoOp != 0 {
		node.Flags |= FlagAutoOp // [05 "Queue insertion"] inherit old head's auto flag
	}
	q.secondary = append([]*Node{node}, q.secondary...)
}

// CoalesceTail is [05 "Queue insertion"]'s positive insertion, whole: select
// the list from the descriptor's secondary-list bit, walk to that list's tail,
// and "if the tail matches both the operation byte and the product type, add to
// that tail's remaining count and return"; otherwise construct and insert.
//
// Coalescing is tail-only — "it never merges distinct products and never merges
// an identical product separated by other products" — and the product type is
// the pair the build record carries, its catalog index and its canonical
// BuildDefKey, together with the site the mobile row carries, because distinct
// sites are distinct orders [P0-I05].
//
// The insertion half is Push, the producer-side insertion, and not a private
// tail append. It used to be one, with its own marker write on an empty
// segment, which made a build order the one producer whose record did not land
// where [04 §3.1]'s insertion rule puts it — immediately after the active
// marker, taking the marker — and made the queue's second bit-12 writer
// [04 R-ORD-01 §13].
func (q *Queue) CoalesceTail(id ID, n Node) {
	if q == nil {
		return
	}
	segment := q.primary
	if isSecondary(id) {
		segment = q.secondary
	}
	if len(segment) > 0 {
		tail := segment[len(segment)-1]
		if tail.ID == id && tail.Param1 == n.Param1 && tail.BuildDefKey == n.BuildDefKey && tail.GoalX == n.GoalX && tail.GoalZ == n.GoalZ {
			add := n.Param2
			if add == 0 {
				add = 1
			}
			// [P2-03] arithmetic overflow: tail Param2 wraps int32 low32 like retail add/sub.
			tail.Param2 += add
			return
		}
	}
	q.Push(id, n)
}

// CancelFrontMost removes the first matching node walking the primary queue
// from the front, and reports whether one went [07 R-P0-11 §6].
//
// This is the queued-order duplicate removal, not a counted subtraction, and
// it differs from CancelTailMost in both directions that matter:
//
//   - It scans front to back. Retail's producer walks the acting unit's
//     primary chain from the head and returns on the first match, so the
//     oldest queued order at a point is the one that goes.
//   - It unlinks the whole node. There is no count decrement here: the
//     traced path frees the matched node outright whatever its count field
//     says. The decrement belongs to the factory producer's negative-count
//     path [R-P0-11 §1], which is a different routine.
//
// The scan is over the primary segment only, which is what retail walks; the
// secondary segment is not searched. Tombstoning follows the same rule as
// every other removal: the node is tombstoned unless it is the list head
// [04 §3.3].
func (q *Queue) CancelFrontMost(match func(Node) bool) bool {
	if q == nil || match == nil {
		return false
	}
	for i := 0; i < len(q.primary); i++ {
		if !match(*q.primary[i]) {
			continue
		}
		n := q.primary[i]
		if i != 0 {
			n.Flags |= FlagTombstone // [04 §3.3]
		}
		q.cleanupNode(n) // [05 "Queue subtraction"]
		// By identity after the cleanup, never by the index taken before it:
		// the cleanup's cancel notification re-enters the queue (see
		// spliceOutPrimary).
		q.spliceOutPrimary(n)
		q.releaseMarkerOnRemoval() // the marker has no removal-side writer [04 §3.3]
		return true
	}
	return false
}

// CancelTailMost is the counted subtraction of [05 "Queue subtraction"]: it
// walks from the tail, decrements a matching record's remaining count when it
// holds more than one, and otherwise unlinks and cleans the record up.
func (q *Queue) CancelTailMost(match func(Node) bool) bool {
	if q == nil || match == nil {
		return false
	}
	for i := len(q.primary) - 1; i >= 0; i-- {
		if match(*q.primary[i]) {
			n := q.primary[i]
			if n.Param2 > 1 {
				n.Param2--
				return true
			}
			isHead := i == 0
			if !isHead {
				n.Flags |= FlagTombstone // [04 §3.3]
			}
			q.cleanupNode(n) // [05 "Queue subtraction"]
			// By identity after the cleanup, never by the index taken before
			// it: the cleanup's cancel notification re-enters the queue (see
			// spliceOutPrimary).
			q.spliceOutPrimary(n)
			q.releaseMarkerOnRemoval() // the marker has no removal-side writer [04 §3.3]
			return true
		}
	}
	for i := len(q.secondary) - 1; i >= 0; i-- {
		if match(*q.secondary[i]) {
			n := q.secondary[i]
			if n.Param2 > 1 {
				n.Param2--
				return true
			}
			n.Flags |= FlagTombstone // secondary always effectively tombstoned [04 §3.3]
			q.cleanupNode(n)
			q.spliceOutSecondary(n) // by identity after the cleanup
			return true
		}
	}
	return false
}
