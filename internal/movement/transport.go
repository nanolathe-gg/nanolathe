// Package movement — the two air transport executors, `VTOL_Pickup` and
// `VTOL_Unload` [04 §10.2][04 R-AIR-01 §9][04 R-UNIT-06 §3].
//
// Both are pump-driven legs in the sense of airorders.go's "pump-driven air
// executors" note: internal/orders owns the descriptor handler and the record
// entry sequence, and this file owns the legs, because every command the two
// phase tables queue is one of the air path markers of [04 R-AIR-01 §4] and
// that family lives here.
//
// This file previously held `TransportState`/`TransportUnloadState`, a pair of
// isolated phase counters with no world, no marker and no callbacks: every
// phase advanced and its side effects were left under a `TODO(question)`
// reading "not simulated beyond phase advance". Nothing outside their own test
// constructed one, so a transport order reached a machine that could not fly,
// attach or release. They are replaced here rather than kept beside the real
// executors, because two machines for one order is exactly what I11 forbids.
//
// What §10.2 leaves open is marked, not guessed: the unload gate words and the
// unload interrupt bit are not quoted by any section, and the cargo's Y after
// release is RWU-19-6's.
package movement

import (
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/units"
)

// Verbatim retail diagnostics [04 §10.2][04 R-AIR-01 §9]. Every one of these
// is quoted by the spec and is reproduced exactly, trailing spelling included.
const (
	TransportFailedMessage = "Transport mission failed"       // load entry gates 1–3, result 8 [04 §10.2]
	HeavyTransportMessage  = "Unit is too heavy to transport" // the air load's phase-0 size gate [04 §10.2]
	UnableUnloadMessage    = "Unable to unload unit"          // both unload validator failures, result 9 [04 §10.2]
)

// Status kinds this family raises, from [04 R-ORD-01 §1]'s twenty-three-kind
// table. Kind 5 (`ok`) is the one-shot caption setter every transport caption
// goes through — [04 R-AIR-01 §9] states that `Loading unit` and `Loading`
// "go through the same one-shot caption setter on status slot 5" — and kind 7
// (`cant`) is the rejection cue the ground twin's identical
// `Transport mission failed` uses.
//
// Kinds 12 (`load`) and 13 (`unload`) are the two event codes §10.2 names: the
// load table's phase 4 "emit event code 12" and the unload table's phase 3
// "emits event code 13 with no text payload". Both kinds carry no default text
// in that table, so passing no text is what "no text payload" means — the kind
// still reaches presentation, which owns the sound side.
const (
	transportStatusCaption uint8 = 5
	transportStatusCant    uint8 = 7
	TransportEventAttach   uint8 = 12 // successful load attach [04 §10.2]
	TransportEventDetach   uint8 = 13 // successful unload release [04 §10.2]
)

// The load executor's gate words, exactly the values §10.2's phase table
// writes. [04 R-UNIT-06 §3] establishes that these are writes to the ORDER
// RECORD's dynamic gate, not to a unit status word: the executor re-arms its
// own record with the movement-service satisfied bits so the record
// re-dispatches when the queued movement reports arrival, release or rebind.
const (
	transportGateApproach uint32 = 0x100E8 // load phases 1 and 2 [04 §10.2]
	transportGateHang     uint32 = 0x100EA // load phase 3 [04 §10.2]
)

// transportLoadEntryMask is the load executor's per-phase entry gate 2: "the
// executor flags word must hold none of mask 0x10048" [04 §10.2]. The mask
// decomposes exactly into pending-word bits of [04 R-ORD-01 §0] — 0x10000 the
// slot-clear/interrupt bit, 0x40 the empty-route "cannot get there" bit, and
// 0x8 the target-removed interrupt — which is the word the handler is handed
// as its satisfied set, so that is the word tested.
const transportLoadEntryMask uint32 = 0x10048

// transportLoadInterruptMask is phase 4's "interrupt-flag combination present
// (`flags & 0x42`)" [04 §10.2]: 0x40 the empty-route bit and 0x2 the
// cancel-current notification [04 R-ORD-01 §0].
const transportLoadInterruptMask uint32 = 0x42

// transportUnloadInterruptMask is the unload phase-2 "unload interrupt flag"
// that returns 9 before the second validation [04 §10.2].
//
// TODO(question): §10.2 names the load executor's two masks numerically
// (0x10048 at entry, 0x42 at phase 4) but writes the unload one only as "an
// unload interrupt flag", and no other section quotes it. Placeholder: the
// target-removed/abandon bit 0x8, which is the bit the ground twin's entry
// tests for the same "stop unloading" condition ([04 R-AIR-01 §9]
// `Ground_Unload` entry). What would settle it: the unload executor's phase-2
// mask constant read directly, the way §10.2's load masks were.
const transportUnloadInterruptMask uint32 = 0x8

// legVTOLPickup is `VTOL_Pickup`, the canonical air load executor of
// [04 §10.2].
//
// Every phase first re-checks the four entry gates, in order. Gate failures
// one and two share the `Transport mission failed` terminal with result 8;
// gate three emits that same message directly, also 8; gate four returns 8
// with NO message.
//
// The phase table, verbatim from §10.2:
//
//	0  live carrier mover and `canfly` (else 7); the size gate; caption
//	   `Loading`; the shared takeoff preamble (self-detach, `Activate`, mode 2,
//	   a point marker on the carrier's own X/Z at `cruisealt/2` with no arrival
//	   radius, gate |= 0xE0).                                              -> 1
//	1  a follow command toward the target at the full `cruisealt` offset with
//	   horizontal arrival radius 0x30; gate = 0x100E8.                     -> 1
//	2  caption `Preparing for transport`; the synchronous four-output
//	   `QueryTransport` with cell 0 seeded −1; retain output 0 as the attach
//	   piece; gate = 0x100E8.                                              -> 1
//	3  asynchronous one-argument `BeginTransport` carrying the cargo
//	   definition's model total-height dword; a follow marker on the cargo,
//	   installed as the CARRIER's goal, whose altitude offset is the negated
//	   integer part of the attach piece's model-frame Y; gate = 0x100EA.   -> 1
//	4  interrupted (`flags & 0x42`): deferred `EndTransport`, no attach.    -> 8
//	   success: attach on the queried piece with request mode 0; event 12;
//	   the climb-away point marker on the carrier's own X/Z at `cruisealt`
//	   with no radius; gate |= 0xE0.                                       -> 1
//	5  no work.                                                            -> 5
//	   other.                                                              -> 7
//
// Successful-load callback order is exactly `QueryTransport` (synchronous) →
// `BeginTransport` (asynchronous) → attachment → event 12, and no successful
// load runs `EndTransport` [04 §10.2].
func (s *System) legVTOLPickup(u *units.Unit, n *orders.Node, satisfied uint32, tick uint32) orders.Code {
	_ = tick
	if code, ok := s.transportLoadGates(u, n, satisfied); ok {
		return code
	}
	target := s.unitFor(n.Target)
	switch n.Phase {
	case 0:
		if !s.airMoverReady(u) {
			return 7 // *cancel-all*: no live mover, or not `canfly` [04 §10.2]
		}
		// The size gate compares the TARGET's cached footprint-X word, signed,
		// against the CARRIER definition's `transportsize` byte zero-extended.
		// [04 R-AIR-01 §9] re-verifies both operands against the ground twin
		// and records that the earlier "byte of the target against a word of
		// the carrier" reading was inverted.
		if int32(s.transportFootprintX(target)) > int32(uint8(u.Def.TransportSize)) {
			orders.NotifyStatus(u, transportStatusCant, HeavyTransportMessage)
			return 8 // *abandon* [04 §10.2]
		}
		orders.NotifyStatus(u, transportStatusCaption, "Loading")
		// The row's remaining clauses — detach the carrier from its own parent
		// when carried, raise `Activate`, force mover mode 2 from mode 1, and
		// the `cruisealt/2` point marker with no arrival radius and gate
		// |= 0xE0 — are steps 2 through 4 of the one shared takeoff preamble
		// [04 R-AIR-01 §6], which every air executor that must leave the ground
		// inlines verbatim. An airborne carrier builds no marker and the phase
		// still advances, so a mid-air load does not reset the climb goal.
		s.takeoffPreamble(u, n)
		return 1
	case 1:
		m := s.newFollowUnitMarker(u, n.Target)
		m.setAltitudeOffset(int16(u.Def.CruiseAlt))
		m.setArrivalRadius(0x30)
		s.installAirGoal(u, n, m)
		n.DynamicGate = transportGateApproach
		return 1
	case 2:
		orders.NotifyStatus(u, transportStatusCaption, "Preparing for transport")
		// The query runs on the CARRIER's script — every transport callback
		// does, and the cargo's script receives nothing on these paths
		// [04 R-UNIT-06 §3]. Cell 0 is seeded −1 by the bridge's
		// `QueryTransport` seed, so a carrier with no script leaves −1, the
		// root-piece fallback.
		piece := int32(-1)
		if bridge := u.ScriptBridge(); bridge != nil {
			piece = bridge.QueryTransport().Values[0]
		}
		n.Param1 = uint32(piece)
		n.DynamicGate = transportGateApproach
		return 1
	case 3:
		if bridge := u.ScriptBridge(); bridge != nil && target != nil && target.Def != nil {
			// Arity 1, cell 0 = the cargo definition's model total-height
			// dword — the 16.16 word the engine derives from the 3DO bounds at
			// definition load, not an authored FBI key — with the wake flag set
			// [04 R-UNIT-06 §3]. `ModelTopFixed` is that dword; `ModelTop` is
			// its high half.
			bridge.DeferredWake("BeginTransport", []int32{target.Def.ModelTopFixed}, nil)
		}
		m := s.newFollowUnitMarker(u, n.Target)
		m.setAltitudeOffset(s.transportHangOffset(u, int32(n.Param1)))
		s.installAirGoal(u, n, m)
		n.DynamicGate = transportGateHang
		return 1
	case 4:
		if satisfied&transportLoadInterruptMask != 0 {
			// The one edge on which a load runs `EndTransport`: deferred,
			// zero-argument, and no attachment happens [04 §10.2].
			if bridge := u.ScriptBridge(); bridge != nil {
				bridge.Deferred("EndTransport", nil, nil)
			}
			return 8 // *abandon* [04 §10.2]
		}
		// Request mode 0 — the attached/parked mode — written straight into the
		// committed mover-mode pair, so the attach never zeroes velocity, never
		// levels bank and pitch, and never raises `Deactivate`
		// [04 R-AIR-01 §9][04 R-AIR-01 §3]. Mode 0 writes no occupancy word
		// [04 R-COLL-01 §4], which is what makes the cargo vanish from the
		// ground plane for the whole carry.
		AttachCargoMode(s.world, u.Handle, n.Target, int(int32(n.Param1)), 0)
		s.armBeCarried(target, u.Handle)
		orders.NotifyStatus(u, TransportEventAttach, "")
		m := s.newPointMarker(u, Vec3{X: u.X, Y: u.Y, Z: u.Z})
		m.setAltitudeOffset(int16(u.Def.CruiseAlt))
		s.installAirGoal(u, n, m)
		n.DynamicGate |= airLegGate
		return 1
	case 5:
		return 5 // *complete* [04 §10.2]
	default:
		return 7 // *cancel-all* [04 §10.2]
	}
}

// transportLoadGates is the four re-checks every phase of the load executor
// runs before doing work, in this order [04 §10.2]:
//
//  1. the order's target reference must be non-null;
//  2. the executor flags word must hold none of mask 0x10048;
//  3. the target's Y plus its definition's model total-height value must be
//     SIGNED greater than sea level shifted into 16.16;
//  4. the carrier's cargo-list head must be null — the air-carrier executor
//     requires an EMPTY cargo list even though general admission only compares
//     count against capacity.
//
// Gate failures one and two share the `Transport mission failed` terminal with
// result 8; gate three emits that same message directly, also 8; gate four
// returns 8 with NO message.
func (s *System) transportLoadGates(u *units.Unit, n *orders.Node, satisfied uint32) (orders.Code, bool) {
	target := s.unitFor(n.Target)
	if n.Target == 0 || target == nil {
		orders.NotifyStatus(u, transportStatusCant, TransportFailedMessage)
		return 8, true
	}
	if satisfied&transportLoadEntryMask != 0 {
		orders.NotifyStatus(u, transportStatusCant, TransportFailedMessage)
		return 8, true
	}
	// Gate 3 is written in 16.16 on both sides: the sea-level byte is shifted
	// into 16.16 and the model total-height operand is the definition's dword,
	// whose high half is the height in whole world units [04 R-AIR-01 §9]. The
	// compare is signed and strict, so a submerged candidate is rejected.
	if target.Def != nil && s.Terrain != nil {
		if int64(target.Y)+int64(target.Def.ModelTopFixed) <= int64(s.Terrain.SeaLevelWorld()) {
			orders.NotifyStatus(u, transportStatusCant, TransportFailedMessage)
			return 8, true
		}
	}
	if len(u.Attachment.Cargo) != 0 {
		return 8, true // gate 4 returns 8 with NO message [04 §10.2]
	}
	return 0, false
}

// transportFootprintX is the target's cached footprint-X word the size gates of
// both load executors read [04 §10.2][04 R-AIR-01 §9]. The unit keeps a copy of
// the definition's footprint-size pair [04 R-ORD-01 §1]; the movement profile
// carries the resolved class width when one is bound.
func (s *System) transportFootprintX(target *units.Unit) int16 {
	if target == nil {
		return 0
	}
	if prof := s.ProfileFor(target.Handle); prof.FootPrintX > 0 {
		return prof.FootPrintX
	}
	if target.Def != nil {
		return int16(target.Def.FootprintX)
	}
	return 0
}

// transportHangOffset is the load phase-3 altitude offset [04 R-AIR-01 §9]:
//
//	the transform it evaluates is the piece-hierarchy evaluator WITHOUT the
//	unit-origin addition, so the value is the attach piece's Y in the CARRIER's
//	own model frame, not a world Y; and the marker it builds is a follow-unit
//	marker on the cargo, installed as the CARRIER's movement goal, so the
//	negated offset lowers the carrier until its attach piece meets the cargo.
//	The value used is the signed 16-bit integer part of that model-frame Y,
//	negated.
//
// The model frame is the unrotated one, so the carrier's own heading, pitch and
// bank are deliberately not applied. A negative piece index is the root-piece
// fallback and hangs nothing.
func (s *System) transportHangOffset(u *units.Unit, piece int32) int16 {
	if piece < 0 {
		return 0
	}
	binding := u.COBBinding()
	if binding == nil {
		return 0
	}
	origin, ok := binding.ComposePiece(int(piece), 0, 0, 0)
	if !ok {
		return 0
	}
	return -int16(int64(origin[1]) >> 16)
}

// legVTOLUnload is `VTOL_Unload`, the canonical air unload executor of
// [04 §10.2]. It returns done immediately when the cargo list is already
// empty, then dispatches on the phase byte:
//
//	0  live `canfly` carrier mover (else 7); caption `Unloading`; record the
//	   cargo reference; a point command toward the stored drop point with
//	   altitude `cruisealt` AND horizontal arrival radius 0x140.           -> 1
//	1  convert the drop point to a footprint anchor from the cargo's packed
//	   footprint dimensions and validate the cargo definition through the
//	   standard placement validator in mode 1; failure emits
//	   `Unable to unload unit` and returns 9; success queues the lowering
//	   command at the same X/Z with the signed altitude offset the cargo
//	   definition's model total-height integer gives, and no radius.       -> 1
//	2  an unload interrupt returns 9 BEFORE the second validation; a second
//	   anchor recompute plus validator call follows, and a failed
//	   revalidation emits the same message and returns 9; success starts the
//	   deferred zero-argument `EndTransport` FIRST, then detaches the cargo
//	   (reserved no-piece index), then constructs the climb-away point command
//	   at the CARRIER's current X/Z with altitude `cruisealt` — release order
//	   is exactly callback → detach → climb-away.                          -> 1
//	3  emit event code 13 with no text payload and finish.                 -> 5
//
// The placement validator therefore runs once before the final lowering
// command and again immediately before the detach: double validation.
func (s *System) legVTOLUnload(u *units.Unit, n *orders.Node, satisfied uint32, tick uint32) orders.Code {
	_ = tick
	// "The canonical unload executor returns done (result 5) immediately when
	// the cargo list is ALREADY empty, then dispatches on the order's phase
	// byte" [04 §10.2]. The word is load-bearing: the phase-2 release detaches
	// the cargo, so a check that ran on every visit would fire before phase 3
	// and §10.2's "phase 3 emits event code 13" would be unreachable in every
	// successful unload. The check is therefore the entry condition it is
	// written as — nothing was ever aboard — and phase 0's recorded cargo
	// reference is what says the executor has started.
	if n.Param1 == 0 && len(u.Attachment.Cargo) == 0 {
		return 5 // *complete*: nothing to unload [04 §10.2]
	}
	// The drop point is the record's goal. [04 R-AIR-01 §9] settles the
	// storage for the ground twin — `Ground_Unload`'s `TransportDrop` cell 1 is
	// "the record's goal X truncated to whole world units in the high half, the
	// goal Z integer part in the low half" — and the `u x,y` mission verb
	// writes the same pair on the record it queues [04 §3.6].
	//
	// TODO(question): §10.2 writes the air executor's drop point only as "the
	// stored drop point" and does not say the record's goal triple is where it
	// is stored. What would settle it (RWU-19-6): the phase-0 marker
	// constructor's source operand read directly.
	dropX, dropZ := n.GoalX, n.GoalZ
	cargoHandle := s.transportCargoHead(u, n)
	cargo := s.unitFor(cargoHandle)
	switch n.Phase {
	case 0:
		if !s.airMoverReady(u) {
			return 7 // *cancel-all* [04 §10.2]
		}
		orders.NotifyStatus(u, transportStatusCaption, "Unloading")
		n.Param1 = uint32(cargoHandle)
		m := s.newPointMarker(u, Vec3{X: dropX, Y: n.GoalY, Z: dropZ})
		m.setAltitudeOffset(int16(u.Def.CruiseAlt))
		m.setArrivalRadius(0x140)
		s.installAirGoal(u, n, m)
		// TODO(question): §10.2 quotes a gate word for every row of the LOAD
		// table (`|= 0xE0`, `= 0x100E8`, `= 0x100EA`) and none for any row of
		// the unload table. Placeholder: the three movement outcomes `0xE0`,
		// which is what [04 R-UNIT-06 §3] says these writes are for — "the
		// record re-dispatches when the queued movement reports arrival" — and
		// without which a horizontal arrival radius of 0x140 would decide
		// nothing, because the next phase would run before the carrier arrived.
		// What would settle it: the unload executor's per-phase gate writes read
		// the way the load table's were.
		n.DynamicGate |= airLegGate
		return 1
	case 1:
		if cargo == nil || !s.ValidateUnloadSite(s.world, cargoHandle, dropX, dropZ, s.Terrain) {
			orders.NotifyStatus(u, transportStatusCant, UnableUnloadMessage)
			return 9 // *retry* [04 §10.2]
		}
		// The lowering offset is the high half of the cargo definition's model
		// total-height dword — the model's height in whole world units, and it
		// is positive. Composed with the marker's terrain-derived altitude rule
		// this places the carrier at
		// `max(seaLevel, terrainHeightAtDropPoint) + cargoModelHeight`, which is
		// exactly the height at which cargo suspended below the carrier touches
		// the ground [04 R-AIR-01 §9]. §10.2's earlier "model-bottom value"
		// wording is superseded there: there is no authored model-bottom key.
		m := s.newPointMarker(u, Vec3{X: dropX, Y: n.GoalY, Z: dropZ})
		m.setAltitudeOffset(int16(cargo.Def.ModelTop))
		s.installAirGoal(u, n, m)
		n.DynamicGate |= airLegGate
		return 1
	case 2:
		if satisfied&transportUnloadInterruptMask != 0 {
			return 9 // BEFORE the second validation [04 §10.2]
		}
		if cargo == nil || !s.ValidateUnloadSite(s.world, cargoHandle, dropX, dropZ, s.Terrain) {
			orders.NotifyStatus(u, transportStatusCant, UnableUnloadMessage)
			return 9 // *retry* [04 §10.2]
		}
		if ok, _ := s.TryUnload(s.world, u.Handle, cargoHandle, dropX, dropZ); !ok {
			orders.NotifyStatus(u, transportStatusCant, UnableUnloadMessage)
			return 9
		}
		m := s.newPointMarker(u, Vec3{X: u.X, Y: u.Y, Z: u.Z})
		m.setAltitudeOffset(int16(u.Def.CruiseAlt))
		s.installAirGoal(u, n, m)
		n.DynamicGate |= airLegGate
		return 1
	case 3:
		orders.NotifyStatus(u, TransportEventDetach, "")
		return 5 // *complete* [04 §10.2]
	default:
		return 7 // *cancel-all* [04 §10.2]
	}
}

// armBeCarried is the "becarried re-arm" the attachment half wakes for a child
// whose player slot state byte is 1 or 2 and whose parent's definition does not
// carry `isairbase` [04 R-AIR-01 §9]'s correction to [04 R-UNIT-06 §3]. The
// carried unit's own order is the two-phase carried wait of [04 R-ORD-01 §2],
// which completes on its next expiry once the carrier link goes null — so the
// release needs no counterpart here.
//
// It is armed at this call site rather than inside the attachment helper
// because the factory's product link, the helper's fourth caller, already
// pushes its own `BeCarried` ahead of `GetBuilt` [04 R-FAC-02 §1]; centralising
// the re-arm would give that product two. Moving it is an upstream change this
// unit does not own.
//
// TODO(question): [04 R-UNIT-06 §3] says the re-arm "purges the carried unit's
// queue through the ordinary cleanup" before the record exists. Placeholder:
// head-insert `BeCarried` without purging, so an order the cargo already owns
// survives the lift instead of being silently dropped. What would settle it:
// which cleanup entry point the re-arm calls, and whether it spares protected
// records the way `PurgeUnprotected` does.
func (s *System) armBeCarried(cargo *units.Unit, carrier pool.Handle) {
	if cargo == nil {
		return
	}
	q := orders.QueueForUnit(cargo)
	if q == nil {
		return
	}
	id := orders.Lookup("BeCarried")
	if id == 0 {
		return
	}
	for _, existing := range q.Primary() {
		if existing.ID == id {
			return
		}
	}
	q.PushHead(id, orders.Node{Owner: cargo.Handle, Target: carrier, Deadline: -1})
}

// transportCargoHead resolves the cargo the unload executor is releasing:
// phase 0 "records the cargo reference" on the record, and the later phases
// read it back. The list is LIFO — the attachment helper links each child as
// the new head of the parent's cargo list, so the release detaches the most
// recently attached cargo first [04 R-UNIT-06 §3].
func (s *System) transportCargoHead(u *units.Unit, n *orders.Node) pool.Handle {
	if h := pool.Handle(n.Param1); h != 0 {
		if cargo := s.unitFor(h); cargo != nil && cargo.Attachment.Carrier == u.Handle {
			return h
		}
	}
	if len(u.Attachment.Cargo) == 0 {
		return 0
	}
	return u.Attachment.Cargo[0]
}
