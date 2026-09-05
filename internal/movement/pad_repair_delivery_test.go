package movement_test

// This file is an external test package on purpose. The record `VTOL_Landing`
// phase 6 spawns is worked by internal/orders against internal/construction's
// repair helper, and internal/construction imports internal/movement — so the
// only place the whole delivery path can be driven from movement's own test
// directory is a `_test` package.

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/combat"
	"github.com/nanolathe/nanolathe/internal/construction"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/units"
)

// padRepairFixture is a completed `isairbase`+`builder` pad and a damaged
// aircraft attached to it, with the session's own repair binding: the order
// pump reaches internal/construction's repair helper, which bills the repairer
// through the one-resource energy admission and heals the patient with a
// kind-10 packet [05 R-WORK-01 §3].
func padRepairFixture(t *testing.T) (*units.World, *economy.Service, *units.Unit, *units.Unit) {
	t.Helper()
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	padDef := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("repairpad")},
		UnitName:         "repairpad",
		Builder:          true, IsAirBase: true,
		WorkerTime: 300, BuildTime: 100, BuildCostEnergy: 600,
		MaxDamage: 1000, FootprintX: 1, FootprintZ: 1,
	}
	landerDef := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("padlander")},
		UnitName:         "padlander",
		CanFly:           true, CanMove: true, BMCode: true,
		BuildTime: 100, BuildCostEnergy: 600,
		MaxDamage: 100, FootprintX: 1, FootprintZ: 1,
	}
	// Strict allocation refuses a definition with no COB program. These two
	// need no script body — nothing in this test runs one — so they carry the
	// smallest authored program that satisfies the gate: a single `return`
	// word, written by us [fmt cob].
	for _, d := range []*content.UnitDef{padDef, landerDef} {
		d.Script = &cob.Program{Code: []uint32{0x10065000}, Scripts: map[string]int{}}
		cat.Units[d.CanonicalKey] = d
	}

	w := units.NewSliced(16, cat)
	ph, err := w.Create(padDef, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("create pad: %v", err)
	}
	lh, err := w.Create(landerDef, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("create lander: %v", err)
	}
	pad, lander := w.Unit(ph), w.Unit(lh)
	pad.Activated, pad.Remaining = true, 0
	lander.Activated, lander.Remaining = true, 0
	lander.Health = 50

	econ := &economy.Service{}
	svc := construction.NewService(nil, cat, w, econ)
	svc.Combat = &combat.Service{}
	binding := &orders.QueueBinding{Economy: econ, Lookup: w.Unit}
	// The session's binding, verbatim in shape [internal/session/composition.go]:
	// the builder is the unit billed, the patient the unit healed, and the
	// quantum is the BUILDER's `workertime/30` [05 R-WORK-01 §3].
	binding.Work = &orders.WorkAdapter{
		Repair: func(builder, patient *units.Unit, _ *orders.Node, _ uint32) bool {
			if builder == nil || builder.Def == nil {
				return false
			}
			return svc.Repair(builder, patient, construction.WorkerQuantum(builder.Def.WorkerTime))
		},
	}
	q := orders.QueueForUnit(lander)
	q.SetBinding(binding)
	q.Push(orders.Lookup("SelfRepair"), orders.Node{Owner: lh, Target: ph})
	return w, econ, pad, lander
}

// TestAttachedLanderIsRepairedThroughResourceAdmission drives the record
// `VTOL_Landing` phase 6 spawns through the order pump and the real repair
// helper. Both of the helper's terms clamp to exactly one whenever positive, so
// an admitted work visit is worth one health point and one energy unit — and
// the energy is the PAD's, because `SelfRepair` lives on the patient and names
// the repairer in its target [05 R-WORK-01 §3][04 R-AIR-01 §6].
func TestAttachedLanderIsRepairedThroughResourceAdmission(t *testing.T) {
	_, econ, pad, lander := padRepairFixture(t)
	q := orders.QueueForUnit(lander)

	q.Pump(lander, 1)
	if lander.Health != 51 {
		t.Fatalf("health = %d after one work visit, want 51: the heal term clamps to exactly one [05 R-WORK-01 §3]", lander.Health)
	}
	if b := econ.UnitBuckets(pad.Handle); b[economy.Energy].Requested != 1 || b[economy.Energy].Accepted != 1 {
		t.Fatalf("pad energy requested/accepted = %v/%v, want 1/1: the repairer pays [05 R-WORK-01 §3]",
			b[economy.Energy].Requested, b[economy.Energy].Accepted)
	}
	if b := econ.UnitBuckets(lander.Handle); b[economy.Energy].Requested != 0 {
		t.Fatalf("lander energy requested = %v, want 0: the patient pays nothing [05 R-WORK-01 §3]", b[economy.Energy].Requested)
	}

	// Once per work visit, not once per call: the row arms `deadline 1` and
	// holds, so re-pumping the same tick heals nothing more [04 R-ORD-01 §2].
	q.Pump(lander, 1)
	if lander.Health != 51 {
		t.Fatalf("health = %d after re-pumping the same tick, want 51: the visit is gated by its own deadline [04 R-ORD-01 §2]", lander.Health)
	}
	q.Pump(lander, 2)
	if lander.Health != 52 {
		t.Fatalf("health = %d on the next tick, want 52: one point per admitted visit [05 R-WORK-01 §3]", lander.Health)
	}
}

// TestStalledPadHealsNothing locks the admission gate: the helper admits only
// while the repairer's energy carry is non-positive, so a pad whose owner is in
// deficit records the demand and heals nothing [05 R-WORK-01 §3].
func TestStalledPadHealsNothing(t *testing.T) {
	_, econ, pad, lander := padRepairFixture(t)
	econ.UnitBuckets(pad.Handle)[economy.Energy].Carry = 1

	orders.QueueForUnit(lander).Pump(lander, 1)

	if lander.Health != 50 {
		t.Fatalf("health = %d with the repairer's energy carry positive, want 50: healing follows admission [05 R-WORK-01 §3]", lander.Health)
	}
	if b := econ.UnitBuckets(pad.Handle); b[economy.Energy].Requested != 1 || b[economy.Energy].Accepted != 0 {
		t.Fatalf("pad energy requested/accepted = %v/%v, want 1/0: a refused visit still records the demand [05 R-WORK-01 §3]",
			b[economy.Energy].Requested, b[economy.Energy].Accepted)
	}
}
