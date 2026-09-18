package orders

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// TestCode14ReadsTheBuildListPresence locks command code 14's gate: "the
// definition's compiled build-option list is present and a live mover exists
// → MobileBuild or air twin; else reject" [04 R-ORD-02 §1]. The first term is an
// existence test on the list block, and retail's catalog compiler allocates
// that block for every definition whose authored `builder` key is set — with no
// CANBUILD section, with no entry naming the builder, or with a populated page
// alike [07 §8]. So a builder whose compiled menu is empty resolves, and only a
// non-builder definition is rejected by this term.
func TestCode14ReadsTheBuildListPresence(t *testing.T) {
	mkBuilder := func(id pool.Handle, builder bool) *units.Unit {
		return mkUnit(id, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) {
			d.CanonicalKey = content.CanonicalKey("fixture")
			d.Builder = builder
		}))
	}

	// The eight shipped builders with an empty menu (ARMASP, CORASP, ARMCARRY,
	// CORCARRY, ARMDECOM, CORDECOM, ARMFARK, CORNECRO) are this case: `builder=1`
	// and no compiled buttons. Retail resolves them.
	emptyMenu := mkBuilder(1, true)
	setTestBuildList(emptyMenu, func(def *content.UnitDef) bool { return def != nil && def.Builder })
	if got := DescriptorFor(Resolve(14, emptyMenu, nil, nil)).Name; got != "MobileBuild" {
		t.Fatalf("builder with an empty compiled menu = %q, want MobileBuild [04 R-ORD-02 §1][07 §8]", got)
	}

	// A definition without the `builder` key has no list block at all.
	notBuilder := mkBuilder(2, false)
	setTestBuildList(notBuilder, func(def *content.UnitDef) bool { return def != nil && def.Builder })
	if id := Resolve(14, notBuilder, nil, nil); id != 0 {
		t.Fatalf("non-builder definition = %q, want reject [04 R-ORD-02 §1]", DescriptorFor(id).Name)
	}

	// With no binding the definition answers for itself: the question is the
	// `builder` flag, not a catalog lookup.
	unbound := mkBuilder(3, true)
	if got := DescriptorFor(Resolve(14, unbound, nil, nil)).Name; got != "MobileBuild" {
		t.Fatalf("unbound builder = %q, want MobileBuild [04 R-ORD-02 §1]", got)
	}

	// The mover term of the same sentence: a factory authors CanMove on a
	// building-class definition and is itself `builder`, and still rejects.
	factory := mkBuilder(4, true)
	factory.Flags |= units.BuildingClassStatus
	setTestBuildList(factory, func(def *content.UnitDef) bool { return def != nil && def.Builder })
	if id := Resolve(14, factory, nil, nil); id != 0 {
		t.Fatalf("building-class builder = %q, want reject [04 R-ORD-02 §1]", DescriptorFor(id).Name)
	}
}

// TestCode8NanoReachTerms fails each of nano-reach's four terms on its own and
// asserts code 8 rejects for each, then passes them all and asserts the two
// resolved names [04 R-ORD-02 §1][04 R-ORD-01 §7].
//
//	the `canreclamate` mirror bit on me; the target's health differs from
//	`maxdamage`; the target's mover mode is not airborne (≠ 2); and the water
//	clause.
func TestCode8NanoReachTerms(t *testing.T) {
	const seaLevel = 20

	// Each case names the term it breaks; everything else is admissible.
	newPair := func(mutate func(actor, target *units.Unit)) (*units.Unit, *units.Unit) {
		actor := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) {
			d.CanReclamate = true
			d.CanFly = false
			d.Amphibious = false
			d.MaxWaterDepth = 12
		}))
		target := mkUnit(2, 0, "ARM", 50, 100, true, 0, mkDef(func(d *content.UnitDef) {
			d.MaxDamage = 100
			d.ModelTop = 4
		}))
		// Whole-unit top = floor(Y) + ModelTop = 30 + 4, clear of sea level 20.
		target.Y = numeric.Fixed(int64(30) << 16)
		target.Move.Mode, target.Move.ModeMirror = 1, 1
		setTestHostility(actor, func(*units.Unit, *units.Unit) bool { return false })
		setTestSeaLevel(actor, seaLevel)
		if mutate != nil {
			mutate(actor, target)
		}
		return actor, target
	}

	if actor, target := newPair(nil); DescriptorFor(Resolve(8, actor, target, nil)).Name != "RepairUnit" {
		t.Fatalf("all four terms satisfied = %q, want RepairUnit [04 R-ORD-02 §1]",
			DescriptorFor(Resolve(8, actor, target, nil)).Name)
	}

	cases := []struct {
		term   string
		mutate func(actor, target *units.Unit)
	}{
		{"canreclamate mirror bit clear", func(a, _ *units.Unit) { a.Def.CanReclamate = false }},
		{"health equals maxdamage", func(_, tg *units.Unit) { tg.Health = tg.Def.MaxDamage }},
		{"target airborne (mover mode 2)", func(_, tg *units.Unit) { tg.Move.ModeMirror = 2 }},
		{"water clause: target top below my reach", func(_, tg *units.Unit) {
			// sea − MaxWaterDepth = 20 − 12 = 8; a top of 7 is out of reach.
			tg.Y = numeric.Fixed(int64(3) << 16)
			tg.Def.ModelTop = 4
		}},
	}
	for _, tc := range cases {
		actor, target := newPair(tc.mutate)
		if id := Resolve(8, actor, target, nil); id != 0 {
			t.Fatalf("%s: code 8 = %q, want reject [04 R-ORD-01 §7]", tc.term, DescriptorFor(id).Name)
		}
	}

	// An unfinished target takes the assist name; the gate is the same one.
	actor, target := newPair(func(_, tg *units.Unit) { tg.Remaining = 0.5 })
	if got := DescriptorFor(Resolve(8, actor, target, nil)).Name; got != "HelpBuild" {
		t.Fatalf("unfinished target = %q, want HelpBuild [04 R-ORD-02 §1]", got)
	}

	// For an aircraft the clause reduces to `seaLevel <= targetTop`: its own
	// maximum water depth is not consulted [04 R-ORD-01 §7].
	flyer, submerged := newPair(func(a, tg *units.Unit) {
		a.Def.CanFly = true
		a.Def.MaxWaterDepth = 10000
		tg.Y = numeric.Fixed(int64(3) << 16)
		tg.Def.ModelTop = 4
	})
	if id := Resolve(8, flyer, submerged, nil); id != 0 {
		t.Fatalf("aircraft over a submerged target = %q, want reject [04 R-ORD-01 §7]", DescriptorFor(id).Name)
	}
	amphib, alsoSubmerged := newPair(func(a, tg *units.Unit) {
		a.Def.CanFly = true
		a.Def.Amphibious = true
		tg.Y = numeric.Fixed(int64(3) << 16)
		tg.Def.ModelTop = 4
	})
	if got := DescriptorFor(Resolve(8, amphib, alsoSubmerged, nil)).Name; got != "VTOL_RepairUnit" {
		t.Fatalf("amphibious aircraft over a submerged target = %q, want VTOL_RepairUnit [04 R-ORD-01 §7]", got)
	}
}

// TestHostilityIsThePlayerAllianceRow locks the correction [04 R-ORD-02 §1]
// makes to §3.4: hostility is the acting PLAYER's diplomacy byte toward the
// target's side — row A of the player slot's alliance rows [05 R-SHARE-01 §1]
// — and never the definition's side string.
func TestHostilityIsThePlayerAllianceRow(t *testing.T) {
	econ := &economy.Service{}
	// Slots 0 and 1 share an ally group; slot 2 is in neither. Slot
	// initialization sets each row's self entry [05 R-SHARE-01 §1].
	for i := 0; i < 3; i++ {
		econ.Players[i].Exists = true
		econ.Players[i].Allies[i] = true
	}
	econ.Players[0].Allies[1] = true
	econ.Players[1].Allies[0] = true
	// The session's read, one-directional on row A [05 R-SHARE-01 §1].
	rowA := func(actor, target *units.Unit) bool {
		if actor == nil || target == nil {
			return false
		}
		if actor.Owner == target.Owner {
			return false
		}
		return !econ.Players[actor.Owner].Allies[target.Owner]
	}

	// Allied, and deliberately of a DIFFERENT side: friendly.
	actor := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) {
		d.CanGuard = true
		d.CanAttack = true
		d.CanReclamate = false
	}))
	setTestHostility(actor, rowA)
	alliedOtherSide := mkUnit(2, 1, "CORE", 100, 100, true, 0, mkDef(nil))
	if got := DescriptorFor(Resolve(7, actor, alliedOtherSide, nil)).Name; got != "Follow_Ground" {
		t.Fatalf("allied target of another side = %q, want Follow_Ground [04 R-ORD-02 §1]", got)
	}

	// Same side, different player, no alliance row: hostile.
	hostileSameSide := mkUnit(3, 2, "ARM", 100, 100, true, 0, mkDef(nil))
	if id := Resolve(7, actor, hostileSameSide, nil); id != 0 {
		t.Fatalf("unallied target of my own side = %q, want reject (hostile) [04 R-ORD-02 §1]", DescriptorFor(id).Name)
	}
	if got := DescriptorFor(Resolve(3, actor, hostileSameSide, nil)).Name; got != "Attack_Chase" {
		t.Fatalf("unallied target of my own side = %q, want Attack_Chase [04 R-ORD-02 §1]", got)
	}

	// With no rows bound at all the resolver falls back to the rows as slot
	// initialization leaves them — self entry one, everything else zero
	// [05 R-SHARE-01 §1] — and the definition's side is not consulted.
	unbound := mkUnit(4, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) {
		d.CanGuard = true
		d.CanAttack = true
		d.CanReclamate = false
	}))
	setTestHostility(unbound, nil)
	sameOwnerOtherSide := mkUnit(5, 0, "CORE", 100, 100, true, 0, mkDef(nil))
	if got := DescriptorFor(Resolve(7, unbound, sameOwnerOtherSide, nil)).Name; got != "Follow_Ground" {
		t.Fatalf("own-player unit of another side = %q, want Follow_Ground (friendly) [05 R-SHARE-01 §1]", got)
	}
	otherOwnerSameSide := mkUnit(6, 1, "ARM", 100, 100, true, 0, mkDef(nil))
	if got := DescriptorFor(Resolve(3, unbound, otherOwnerSameSide, nil)).Name; got != "Attack_Chase" {
		t.Fatalf("other-player unit of my own side = %q, want Attack_Chase (hostile) [05 R-SHARE-01 §1]", got)
	}
}

// TestResolveDrawsNoRandomness locks I4's call order across this unit: command
// resolution is a pure classification and consumes neither stream, so no
// resolution added or moved here can shift a later draw.
func TestResolveDrawsNoRandomness(t *testing.T) {
	rng.SeedGlobal(1, 0)
	simBefore, crtBefore := rng.Global.Sim.Draws(), rng.Global.Crt.Draws()

	actor := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) {
		d.CanLoad = true
		d.CanReclamate = true
	}))
	setTestHostility(actor, func(*units.Unit, *units.Unit) bool { return false })
	setTestBuildList(actor, func(*content.UnitDef) bool { return true })
	setTestAdmission(actor, func(*units.Unit, *units.Unit) bool { return true })
	setTestSeaLevel(actor, 0)
	target := mkUnit(2, 1, "CORE", 50, 100, true, 0, mkDef(nil))
	pos := &ResolvePos{HasFeature: true}

	for code := 0; code <= 15; code++ {
		Resolve(code, actor, target, pos)
		Resolve(code, actor, nil, pos)
		Resolve(code, actor, target, nil)
	}
	if got, want := rng.Global.Sim.Draws(), simBefore; got != want {
		t.Fatalf("simulation draws = %d, want %d [I4]", got, want)
	}
	if got, want := rng.Global.Crt.Draws(), crtBefore; got != want {
		t.Fatalf("CRT draws = %d, want %d [I4]", got, want)
	}
}
