package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// TestNoProximityHealingBesideAPad is the regression for review finding R04: a
// movement `EndTick` lane healed any grounded aircraft standing near any
// `isairbase` definition, with no attachment, no order record, no completed or
// activated repairer, no friendliness test and no energy admission.
//
// Nothing in research produces healing that way. `[04 R-AIR-01 §6]` phase 6 is
// the only pad-side producer, and what it produces is a `SelfRepair` order
// record on the lander; `[05 R-WORK-01 §3]` then bills the repairer's energy
// through the one-resource admission and applies a kind-10 packet.
// `VTOL_GetRepaired` is a two-phase wait that never calls the helper
// (`[04 R-AIR-01 §6]`, `[05 R-WORK-01 §3]`).
func TestNoProximityHealingBesideAPad(t *testing.T) {
	sys, w, u := airFixture(t)
	u.Health = 50
	u.Move.Mode = 1 // grounded, the state the retired lane keyed on

	// An unfinished, inactive, ENEMY pad one cell east: every reason to refuse.
	pad := spawnAirBasePadFor(t, w, sys.Terrain, "enemypad", 1,
		u.X+numeric.Fixed(16<<16), u.Z)
	pad.Activated = false
	pad.Remaining = 0.5

	sys.BeginTick(1)
	sys.EndTick(1)

	if u.Health != 50 {
		t.Fatalf("health=%d after one tick beside an unfinished inactive enemy pad, want 50: proximity is not a healing producer [05 R-WORK-01 §3][04 R-AIR-01 §6]", u.Health)
	}

	// The same pad made friendly, finished and active still heals nothing: the
	// mover tick has no healing producer at all, whatever the pad is.
	pad.Owner = u.Owner
	pad.Activated = true
	pad.Remaining = 0
	for tick := uint32(2); tick <= 40; tick++ {
		sys.BeginTick(tick)
		sys.EndTick(tick)
	}
	if u.Health != 50 {
		t.Fatalf("health=%d after 39 ticks parked beside a friendly finished pad, want 50: healing needs the order record, not proximity [04 R-AIR-01 §6][05 R-WORK-01 §3]", u.Health)
	}
}

// TestPadRepairsLanderClauses locks `VTOL_Landing` phase 6's three-clause
// repair test [04 R-AIR-01 §6]: the lander below its definition's `MaxDamage`,
// the pad owner's definition carrying both `isairbase` and `builder`, and the
// pad owner not under construction. Each clause is necessary.
func TestPadRepairsLanderClauses(t *testing.T) {
	newLander := func(health int32) *units.Unit {
		return &units.Unit{Def: &content.UnitDef{CanFly: true, MaxDamage: 100}, Health: health}
	}
	newPad := func() *units.Unit {
		return &units.Unit{Def: &content.UnitDef{IsAirBase: true, Builder: true, MaxDamage: 1000}}
	}

	if !padRepairsLander(newLander(50), newPad()) {
		t.Fatal("a damaged lander on a finished builder pad must get its SelfRepair record [04 R-AIR-01 §6 phase 6]")
	}
	if padRepairsLander(newLander(100), newPad()) {
		t.Fatal("a lander already at MaxDamage gets no record [04 R-AIR-01 §6 phase 6]")
	}
	notBuilder := newPad()
	notBuilder.Def.Builder = false
	if padRepairsLander(newLander(50), notBuilder) {
		t.Fatal("a pad whose definition lacks `builder` repairs nothing [04 R-AIR-01 §6 phase 6]")
	}
	notPad := newPad()
	notPad.Def.IsAirBase = false
	if padRepairsLander(newLander(50), notPad) {
		t.Fatal("a host whose definition lacks `isairbase` repairs nothing [04 R-AIR-01 §6 phase 6]")
	}
	unfinished := newPad()
	unfinished.Remaining = 0.25
	if padRepairsLander(newLander(50), unfinished) {
		t.Fatal("a pad still under construction repairs nothing [04 R-AIR-01 §6 phase 6]")
	}
}

// runLandingTick is the session's own two-part per-unit tick in the order
// internal/session/step.go runs it: the order pump for every unit, then the
// movement transaction. `VTOL_Landing` is dispatched by the pump through the
// air-leg runner, so a mover-tick-only loop never retires the record and never
// reaches phase 6's repair spawn.
func runLandingTick(sys *System, tick uint32, w *units.World) {
	sys.BindAirOrderLegs()
	for _, u := range w.IterSliced() {
		if u == nil || !u.Alive {
			continue
		}
		if q := orders.QueueOfUnit(u); q != nil {
			q.Pump(u, tick)
		}
	}
	runMovementTick(sys, tick, w)
}

// TestVTOLLandingPushesSelfRepairOnTouchdown runs the real landing machine to
// its touchdown and locks the producer the retired proximity lane was standing
// in for: phase 6 attaches the lander to the pad piece and pushes a
// `SelfRepair` record on the LANDER naming the PAD as its target
// [04 R-AIR-01 §6]. The record's reversed ends are the whole of `SelfRepair`'s
// identity — the patient runs it, the repairer is billed [05 R-WORK-01 §3].
func TestVTOLLandingPushesSelfRepairOnTouchdown(t *testing.T) {
	sys, w, u := airFixture(t)
	u.Health = 50
	pad := spawnAirBasePadFor(t, w, sys.Terrain, "friendlypad", u.Owner,
		u.X+numeric.Fixed(48<<16), u.Z)
	sys.EnsureUnit(pad)

	q := orderQueueOf(t, u)
	// The repair port the spawned record works through. It refuses the visit,
	// so the record survives its first work phase and can be inspected; what it
	// records is the pair the port was handed, which is the whole of the
	// producer's contract with internal/construction [05 R-WORK-01 §3].
	var billed, healed pool.Handle
	q.Binding().Work = &orders.WorkAdapter{
		Repair: func(builder, patient *units.Unit, _ *orders.Node, _ uint32) bool {
			if builder != nil {
				billed = builder.Handle
			}
			if patient != nil {
				healed = patient.Handle
			}
			return false
		},
	}
	q.Push(orders.Lookup("VTOL_Landing"), orders.Node{Owner: u.Handle, Target: pad.Handle})

	last := uint32(0)
	for tick := uint32(1); tick <= 900; tick++ {
		runLandingTick(sys, tick, w)
		last = tick
		if u.Attachment.Carrier == pad.Handle {
			break
		}
	}
	if u.Attachment.Carrier != pad.Handle {
		t.Fatal("the lander never attached to the pad [04 R-AIR-01 §6 phase 6]")
	}
	// One more tick, so the pump answers the machine's outcome: the landing
	// record is unlinked and the spawned SelfRepair takes the head.
	runLandingTick(sys, last+1, w)
	if u.Health != 50 {
		t.Fatalf("health=%d at touchdown, want 50: the touchdown itself heals nothing [04 R-AIR-01 §6]", u.Health)
	}

	var found *orders.Node
	for _, n := range q.Primary() {
		if n != nil && n.ID == orders.Lookup("SelfRepair") {
			found = n
			break
		}
	}
	if found == nil {
		t.Fatal("no SelfRepair record after touchdown: phase 6 is the only producer of pad repair [04 R-AIR-01 §6][05 R-WORK-01 §3]")
	}
	if found.Owner != u.Handle {
		t.Fatalf("SelfRepair owner=%d, want the lander %d: the record lives on the PATIENT [05 R-WORK-01 §3]", found.Owner, u.Handle)
	}
	if found.Target != pad.Handle {
		t.Fatalf("SelfRepair target=%d, want the pad %d: the target is the REPAIRER [05 R-WORK-01 §3]", found.Target, pad.Handle)
	}
	if billed != pad.Handle || healed != u.Handle {
		t.Fatalf("repair port received builder=%d patient=%d, want pad %d billed and lander %d healed [05 R-WORK-01 §3]",
			billed, healed, pad.Handle, u.Handle)
	}
	// The landing record itself is gone: the pump unlinked it in the same visit
	// that head-inserted the repair, so nothing behind it restarts the machine.
	for _, n := range q.Primary() {
		if n != nil && n.ID == orders.Lookup("VTOL_Landing") {
			t.Fatal("the landing record survived its own completion [04 R-AIR-01 §6][04 R-ORD-01 §10]")
		}
	}
}

// TestVTOLLandingPushesNoSelfRepairAtFullHealth is the same machine with an
// undamaged lander: phase 6's health clause refuses, so no record is pushed
// [04 R-AIR-01 §6].
func TestVTOLLandingPushesNoSelfRepairAtFullHealth(t *testing.T) {
	sys, w, u := airFixture(t)
	u.Health = u.Def.MaxDamage
	pad := spawnAirBasePadFor(t, w, sys.Terrain, "friendlypad", u.Owner,
		u.X+numeric.Fixed(48<<16), u.Z)
	sys.EnsureUnit(pad)

	q := orderQueueOf(t, u)
	q.Push(orders.Lookup("VTOL_Landing"), orders.Node{Owner: u.Handle, Target: pad.Handle})

	last := uint32(0)
	for tick := uint32(1); tick <= 900; tick++ {
		runLandingTick(sys, tick, w)
		last = tick
		if u.Attachment.Carrier == pad.Handle {
			break
		}
	}
	if u.Attachment.Carrier != pad.Handle {
		t.Fatal("the lander never attached to the pad [04 R-AIR-01 §6 phase 6]")
	}
	runLandingTick(sys, last+1, w)
	for _, n := range q.Primary() {
		if n != nil && n.ID == orders.Lookup("SelfRepair") {
			t.Fatal("an undamaged lander must get no SelfRepair record [04 R-AIR-01 §6 phase 6]")
		}
	}
}
