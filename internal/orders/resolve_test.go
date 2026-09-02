package orders

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
)

func mkDef(overrides func(*content.UnitDef)) *content.UnitDef {
	d := &content.UnitDef{}
	d.CanMove = true
	d.CanAttack = true
	d.CanGuard = true
	d.CanPatrol = true
	d.CanReclamate = true
	d.CanCapture = true
	d.CanDGun = true
	d.Builder = true
	d.CanFly = false
	d.CanHover = false
	d.MaxDamage = 100
	d.Side = "ARM"
	if overrides != nil {
		overrides(d)
	}
	return d
}

func mkUnit(handle pool.Handle, owner uint8, side string, health, max int32, alive bool, remaining float32, def *content.UnitDef) *units.Unit {
	if def == nil {
		def = mkDef(nil)
	}
	if side != "" {
		def.Side = side
	}
	u := &units.Unit{
		Handle:    handle,
		Owner:     owner,
		Def:       def,
		Health:    health,
		MaxHealth: max,
		Alive:     alive,
		Remaining: remaining,
		X:         numeric.Fixed(0),
		Y:         numeric.Fixed(0),
		Z:         numeric.Fixed(0),
	}
	if max == 0 {
		u.MaxHealth = 100
		u.Health = 100
	}
	// These fixtures build units directly rather than through the allocator, so
	// the runtime status word the resolver reads has to be seeded here.
	// ArmedStatus is the allocator's "at least one weapon slot resolved" bit,
	// and code 3's whole armed branch is gated on it [R-ORD-02 §1]; a fixture
	// definition carries no weapon records, so CanAttack stands in for it. That
	// is a fixture convenience, not a claim that retail derives one from the
	// other.
	if def.CanAttack {
		u.Flags |= units.ArmedStatus
	}
	return u
}

func setTestHostility(u *units.Unit, fn func(*units.Unit, *units.Unit) bool) {
	q := QueueForUnit(u)
	b := q.Binding()
	if b == nil {
		b = &QueueBinding{}
	}
	b.Hostility = fn
	q.SetBinding(b)
}

func setTestLookup(u *units.Unit, fn func(pool.Handle) *units.Unit) {
	q := QueueForUnit(u)
	b := q.Binding()
	if b == nil {
		b = &QueueBinding{}
	}
	b.Lookup = fn
	q.SetBinding(b)
}

func TestResolveFullTable(t *testing.T) {
	actor := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) {
		d.CanMove = true
		d.CanAttack = true
		d.CanGuard = true
		d.CanPatrol = true
		d.CanReclamate = true
		d.CanCapture = true
		d.CanDGun = true
		d.Builder = true
		d.CanFly = false
	}))

	assertResolve := func(code int, target *units.Unit, pos *ResolvePos, wantName string) {
		id := Resolve(code, actor, target, pos)
		got := ""
		if id != 0 {
			got = DescriptorFor(id).Name
		}
		if got != wantName {
			t.Fatalf("code %d target=%v pos=%v want %q got %q (id %d)", code, target, pos, wantName, got, id)
		}
	}

	hostileTarget := mkUnit(2, 1, "CORE", 100, 100, true, 0, mkDef(func(d *content.UnitDef) {
		d.CanMove = true
		d.Side = "CORE"
	}))
	setTestHostility(actor, func(a, b *units.Unit) bool { return a.Def.Side != b.Def.Side })
	assertResolve(1, hostileTarget, nil, "Attack_Chase")
	friendlyDamaged := mkUnit(3, 0, "ARM", 50, 100, true, 0, mkDef(nil))
	setTestHostility(actor, func(a, b *units.Unit) bool { return false })
	assertResolve(1, friendlyDamaged, nil, "RepairUnit")
	transportable := mkUnit(4, 1, "CORE", 100, 100, true, 0, mkDef(func(d *content.UnitDef) {
		d.CantBeTransported = false
	}))
	actorNoAttack := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) {
		d.CanMove = true
		d.CanAttack = false
		d.Builder = false
	}))
	setTestHostility(actorNoAttack, func(a, b *units.Unit) bool { return true })
	id := Resolve(1, actorNoAttack, transportable, nil)
	if got := DescriptorFor(id).Name; got != "Ground_Pickup" {
		t.Fatalf("code1 transportable want Ground_Pickup got %q", got)
	}
	actorCanResurrect := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) {
		d.CanMove = true
		d.CanAttack = false
		d.Builder = false
		d.CanResurrect = true
	}))
	posWreck := &ResolvePos{HasFeature: true, IsWreck: true, FeatureResurrectable: true}
	setTestHostility(actorCanResurrect, nil)
	id = Resolve(1, actorCanResurrect, nil, posWreck)
	if got := DescriptorFor(id).Name; got != "Resurrect" {
		t.Fatalf("code1 feature wreck resurrect want Resurrect got %q", got)
	}
	posFeature := &ResolvePos{HasFeature: true, IsWreck: false}
	id = Resolve(1, actorCanResurrect, nil, posFeature)
	if got := DescriptorFor(id).Name; got != "Reclaim" {
		t.Fatalf("code1 feature reclaim want Reclaim got %q", got)
	}
	actorMove := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) {
		d.CanMove = true
		d.CanFly = false
		d.CanAttack = false
		d.Builder = false
	}))
	id = Resolve(1, actorMove, nil, nil)
	if got := DescriptorFor(id).Name; got != "Move_Ground" {
		t.Fatalf("code1 otherwise move want Move_Ground got %q", got)
	}
	actorVTOL := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) {
		d.CanMove = true
		d.CanFly = true
		d.CanAttack = false
	}))
	id = Resolve(1, actorVTOL, nil, nil)
	if got := DescriptorFor(id).Name; got != "VTOL_Move" {
		t.Fatalf("code1 vtol move want VTOL_Move got %q", got)
	}

	actor = mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) {
		d.CanMove = true
		d.CanAttack = true
		d.CanGuard = true
		d.CanPatrol = true
		d.CanReclamate = true
		d.CanCapture = true
		d.CanDGun = true
		d.Builder = true
	}))
	actorNoMove := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) { d.CanMove = false }))
	assertRejectForActor := func(code int, a *units.Unit, tgt *units.Unit) {
		if id := Resolve(code, a, tgt, nil); id != 0 {
			t.Fatalf("code %d with no-move actor should reject got %q", code, DescriptorFor(id).Name)
		}
	}
	assertRejectForActor(2, actorNoMove, nil)
	// A target that exists but lacks the alive bit rejects every code before
	// the switch [04 R-ORD-02 §1]. This used to assert QMove, reading §3.4's
	// summary row ("a dead unit target becomes a queued move") as the
	// condition; the queued-move arm is the live-mover test, exercised below.
	dead := mkUnit(6, 1, "CORE", 0, 100, false, 0, mkDef(nil))
	if id = Resolve(2, actor, dead, nil); id != 0 {
		t.Fatalf("code2 dead target should reject, got %q", DescriptorFor(id).Name)
	}
	actorCapture := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) {
		d.CanMove = true
		d.CanCapture = true
		d.CanReclamate = false
	}))
	setTestHostility(actorCapture, func(a, b *units.Unit) bool { return true })
	id = Resolve(2, actorCapture, hostileTarget, nil)
	if got := DescriptorFor(id).Name; got != "Capture" {
		t.Fatalf("code2 hostile capture want Capture got %q", got)
	}
	setTestHostility(actor, func(a, b *units.Unit) bool { return false })
	friendlyUnfinished := mkUnit(7, 0, "ARM", 100, 100, true, 0.5, mkDef(nil))
	id = Resolve(2, actor, friendlyUnfinished, nil)
	if got := DescriptorFor(id).Name; got != "HelpBuild" {
		t.Fatalf("code2 friendly build want HelpBuild got %q", got)
	}
	padTarget := mkUnit(8, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) { d.IsAirBase = true }))
	id = Resolve(2, actor, padTarget, nil)
	if got := DescriptorFor(id).Name; got != "VTOL_Landing" {
		t.Fatalf("code2 pad want VTOL_Landing got %q", got)
	}
	setTestHostility(actor, func(a, b *units.Unit) bool { return false })
	carriableUnfinishedFalse := mkUnit(9, 1, "CORE", 100, 100, true, 0, mkDef(func(d *content.UnitDef) { d.CantBeTransported = false; d.IsAirBase = false }))
	id = Resolve(2, actor, carriableUnfinishedFalse, nil)
	if got := DescriptorFor(id).Name; got != "Ground_Pickup" {
		t.Fatalf("code2 carriable want Ground_Pickup got %q", got)
	}
	followable := mkUnit(10, 1, "CORE", 100, 100, true, 0, mkDef(func(d *content.UnitDef) { d.CantBeTransported = true; d.IsAirBase = false }))
	id = Resolve(2, actor, followable, nil)
	if got := DescriptorFor(id).Name; got != "Follow_Ground" {
		t.Fatalf("code2 follow want Follow_Ground got %q", got)
	}
	id = Resolve(2, actor, nil, nil)
	if got := DescriptorFor(id).Name; got != "Move_Ground" {
		t.Fatalf("code2 ground move want Move_Ground got %q", got)
	}
	actorVTOLMove := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) {
		d.CanMove = true
		d.CanFly = true
	}))
	id = Resolve(2, actorVTOLMove, nil, nil)
	if got := DescriptorFor(id).Name; got != "VTOL_Move" {
		t.Fatalf("code2 vtol move want VTOL_Move got %q", got)
	}
	actorNoAttack = mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) { d.CanAttack = false }))
	assertRejectForActor(3, actorNoAttack, hostileTarget)
	// Code 3's ground tail keys on the ACTOR, and the kamikaze arm is the
	// fall-through an UNARMED unit reaches, not a test that precedes the mover
	// tests [R-ORD-02 §1]. An armed kamikaze mover therefore chases; a
	// weaponless one — which is what a stock suicide unit is — detonates.
	targetGround := mkUnit(11, 1, "CORE", 100, 100, true, 0, mkDef(func(d *content.UnitDef) { d.CanMove = true; d.CanFly = false }))
	actorKamikazeArmed := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) {
		d.CanAttack = true
		d.Kamikaze = true
		d.CanFly = false
	}))
	id = Resolve(3, actorKamikazeArmed, targetGround, nil)
	if got := DescriptorFor(id).Name; got != "Attack_Chase" {
		t.Fatalf("code3 armed kamikaze mover want Attack_Chase got %q", got)
	}
	actorKamikaze := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) {
		d.CanAttack = true
		d.Kamikaze = true
		d.CanFly = false
	}))
	actorKamikaze.Flags &^= units.ArmedStatus // no weapon slot resolved
	id = Resolve(3, actorKamikaze, targetGround, nil)
	if got := DescriptorFor(id).Name; got != "Attack_Kamikaze" {
		t.Fatalf("code3 unarmed kamikaze want Attack_Kamikaze got %q", got)
	}
	actorNormal := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) { d.CanAttack = true; d.CanFly = false; d.Kamikaze = false }))
	// A mobile attacker chases a STRUCTURE too: the no-move variant is chosen
	// by the actor's own state bit 29, never by the target's class.
	structure := mkUnit(12, 1, "CORE", 100, 100, true, 0, mkDef(func(d *content.UnitDef) { d.CanMove = false; d.CanFly = false }))
	id = Resolve(3, actorNormal, structure, nil)
	if got := DescriptorFor(id).Name; got != "Attack_Chase" {
		t.Fatalf("code3 mobile attacker vs structure want Attack_Chase got %q", got)
	}
	immobileActor := mkUnit(14, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) { d.CanAttack = true; d.CanFly = false }))
	immobileActor.Flags |= units.BuildingClassStatus
	id = Resolve(3, immobileActor, targetGround, nil)
	if got := DescriptorFor(id).Name; got != "Attack_NoMove" {
		t.Fatalf("code3 immobile actor want Attack_NoMove got %q", got)
	}
	mobileTarget := mkUnit(13, 1, "CORE", 100, 100, true, 0, mkDef(func(d *content.UnitDef) { d.CanMove = true }))
	id = Resolve(3, actorNormal, mobileTarget, nil)
	if got := DescriptorFor(id).Name; got != "Attack_Chase" {
		t.Fatalf("code3 chase want Attack_Chase got %q", got)
	}
	actorAir := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) { d.CanAttack = true; d.CanFly = true }))
	targetAir := mkUnit(14, 1, "CORE", 100, 100, true, 0, mkDef(func(d *content.UnitDef) { d.CanFly = true }))
	id = Resolve(3, actorAir, targetAir, nil)
	if got := DescriptorFor(id).Name; got != "AirToAir" {
		t.Fatalf("code3 airtoair want AirToAir got %q", got)
	}
	targetHover := mkUnit(15, 1, "CORE", 100, 100, true, 0, mkDef(func(d *content.UnitDef) { d.CanFly = false; d.CanHover = true }))
	id = Resolve(3, actorAir, targetHover, nil)
	if got := DescriptorFor(id).Name; got != "AirToGroundHover" {
		t.Fatalf("code3 hover want AirToGroundHover got %q", got)
	}
	targetGroundAir := mkUnit(16, 1, "CORE", 100, 100, true, 0, mkDef(func(d *content.UnitDef) { d.CanFly = false; d.CanHover = false }))
	id = Resolve(3, actorAir, targetGroundAir, nil)
	if got := DescriptorFor(id).Name; got != "AirToGround" {
		t.Fatalf("code3 airtoground want AirToGround got %q", got)
	}
	// `Suppress` is code 3's POSITION-ONLY arm [R-ORD-02 §1]; a code-3 call
	// carrying a target never reaches it. The retired form of this assertion
	// keyed on a `suppress` entry in the definition's unparsed leftovers, which
	// no asset authors.
	actorSuppress := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) {
		d.CanAttack = true
		d.CanFly = false
	}))
	id = Resolve(3, actorSuppress, nil, &ResolvePos{})
	if got := DescriptorFor(id).Name; got != "Suppress" {
		t.Fatalf("code3 position-only ground attack want Suppress got %q", got)
	}
	targetSuppress := mkUnit(17, 1, "CORE", 100, 100, true, 0, mkDef(func(d *content.UnitDef) { d.CanFly = false; d.CanMove = true }))
	id = Resolve(3, actorSuppress, targetSuppress, nil)
	if got := DescriptorFor(id).Name; got != "Attack_Chase" {
		t.Fatalf("code3 with a target must not resolve Suppress, got %q", got)
	}

	actorNoDGun := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) { d.CanDGun = false }))
	if id := Resolve(4, actorNoDGun, nil, nil); id != 0 {
		t.Fatalf("code4 no DGun should reject")
	}
	actorDGun := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) { d.CanDGun = true }))
	id = Resolve(4, actorDGun, nil, nil)
	if got := DescriptorFor(id).Name; got != "AttackSpecial" {
		t.Fatalf("code4 want AttackSpecial got %q", got)
	}

	actorNoLoad := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) { d.CanLoad = false }))
	if id := Resolve(5, actorNoLoad, nil, nil); id != 0 {
		t.Fatalf("code5 no load should reject")
	}
	actorLoad := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) { d.CanLoad = true; d.CanFly = false }))
	id = Resolve(5, actorLoad, nil, nil)
	if got := DescriptorFor(id).Name; got != "Ground_Unload" {
		t.Fatalf("code5 ground unload want Ground_Unload got %q", got)
	}
	actorLoadVTOL := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) { d.CanLoad = true; d.CanFly = true }))
	id = Resolve(5, actorLoadVTOL, nil, nil)
	if got := DescriptorFor(id).Name; got != "VTOL_Unload" {
		t.Fatalf("code5 vtol unload want VTOL_Unload got %q", got)
	}
	id = Resolve(5, actorLoadVTOL, padTarget, nil)
	if got := DescriptorFor(id).Name; got != "VTOL_Landing" {
		t.Fatalf("code5 pad want VTOL_Landing got %q", got)
	}

	carriableT := mkUnit(18, 1, "CORE", 100, 100, true, 0, mkDef(func(d *content.UnitDef) { d.CantBeTransported = false }))
	if id := Resolve(6, actor, nil, nil); id != 0 {
		t.Fatalf("code6 no target should reject")
	}
	nonCarriable := mkUnit(19, 1, "CORE", 100, 100, true, 0, mkDef(func(d *content.UnitDef) { d.CantBeTransported = true }))
	if id := Resolve(6, actor, nonCarriable, nil); id != 0 {
		t.Fatalf("code6 non carriable should reject")
	}
	id = Resolve(6, actorLoad, carriableT, nil)
	if got := DescriptorFor(id).Name; got != "Ground_Pickup" {
		t.Fatalf("code6 ground pickup want Ground_Pickup got %q", got)
	}
	id = Resolve(6, actorLoadVTOL, carriableT, nil)
	if got := DescriptorFor(id).Name; got != "VTOL_Pickup" {
		t.Fatalf("code6 vtol pickup want VTOL_Pickup got %q", got)
	}

	actorNoGuard := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) { d.CanGuard = false }))
	if id := Resolve(7, actorNoGuard, friendlyDamaged, nil); id != 0 {
		t.Fatalf("code7 no guard should reject")
	}
	friendly := mkUnit(20, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) { d.Side = "ARM" }))
	setTestHostility(actor, func(a, b *units.Unit) bool { return false })
	id = Resolve(7, actor, friendly, nil)
	if got := DescriptorFor(id).Name; got != "Follow_Ground" {
		t.Fatalf("code7 follow ground want Follow_Ground got %q", got)
	}
	actorGuardVTOL := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) { d.CanGuard = true; d.CanFly = true }))
	id = Resolve(7, actorGuardVTOL, friendly, nil)
	if got := DescriptorFor(id).Name; got != "VTOL_Follow" {
		t.Fatalf("code7 vtol follow want VTOL_Follow got %q", got)
	}
	setTestHostility(actor, func(a, b *units.Unit) bool { return true })
	if id := Resolve(7, actor, friendly, nil); id != 0 {
		t.Fatalf("code7 hostile should reject")
	}
	setTestHostility(actor, nil)

	actorNonBuilder := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) { d.Builder = false }))
	if id := Resolve(8, actorNonBuilder, friendlyDamaged, nil); id != 0 {
		t.Fatalf("code8 non builder should reject")
	}
	if id := Resolve(8, actor, nil, nil); id != 0 {
		t.Fatalf("code8 no target should reject")
	}
	unfinished := mkUnit(21, 0, "ARM", 100, 100, true, 0.5, mkDef(nil))
	id = Resolve(8, actor, unfinished, nil)
	if got := DescriptorFor(id).Name; got != "HelpBuild" {
		t.Fatalf("code8 unfinished want HelpBuild got %q", got)
	}
	damagedFriendly := mkUnit(22, 0, "ARM", 50, 100, true, 0, mkDef(nil))
	id = Resolve(8, actor, damagedFriendly, nil)
	if got := DescriptorFor(id).Name; got != "RepairUnit" {
		t.Fatalf("code8 damaged want RepairUnit got %q", got)
	}
	actorAssistVTOL := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) { d.Builder = true; d.CanFly = true }))
	id = Resolve(8, actorAssistVTOL, unfinished, nil)
	if got := DescriptorFor(id).Name; got != "VTOL_HelpBuild" {
		t.Fatalf("code8 vtol help want VTOL_HelpBuild got %q", got)
	}

	actorNoPatrol := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) { d.CanPatrol = false }))
	if id := Resolve(9, actorNoPatrol, nil, nil); id != 0 {
		t.Fatalf("code9 no patrol should reject")
	}
	// Code 9's queued arm is the live-mover test, not "no target", and the
	// repair-patrol gate is the definition parser's `canreclamate` mirror, not
	// the Builder flag [04 R-ORD-02 §1][04 R-ORD-01 §7]. Both assertions here
	// used to encode the older readings.
	id = Resolve(9, actor, nil, nil) // mobile + canreclamate
	if got := DescriptorFor(id).Name; got != "RepairPatrol" {
		t.Fatalf("code9 mobile reclaimer want RepairPatrol got %q", got)
	}
	id = Resolve(9, actor, friendly, nil)
	if got := DescriptorFor(id).Name; got != "RepairPatrol" {
		t.Fatalf("code9 builder repair patrol want RepairPatrol got %q", got)
	}
	actorPatrolImmobile := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) { d.CanPatrol = true }))
	actorPatrolImmobile.Flags |= units.BuildingClassStatus
	if got := DescriptorFor(Resolve(9, actorPatrolImmobile, nil, nil)).Name; got != "QPatrol" {
		t.Fatalf("code9 immobile want QPatrol got %q", got)
	}
	actorPatrolNonBuilder := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) {
		d.CanPatrol = true
		d.CanReclamate = false
		d.CanFly = false
	}))
	id = Resolve(9, actorPatrolNonBuilder, friendly, nil)
	if got := DescriptorFor(id).Name; got != "Patrol" {
		t.Fatalf("code9 patrol want Patrol got %q", got)
	}
	actorPatrolVTOL := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) {
		d.CanPatrol = true
		d.CanReclamate = false
		d.CanFly = true
	}))
	id = Resolve(9, actorPatrolVTOL, friendly, nil)
	if got := DescriptorFor(id).Name; got != "VTOL_Patrol" {
		t.Fatalf("code9 vtol patrol want VTOL_Patrol got %q", got)
	}
	actorPatrolBuilderVTOL := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) {
		d.CanPatrol = true
		d.CanReclamate = true
		d.CanFly = true
	}))
	id = Resolve(9, actorPatrolBuilderVTOL, friendly, nil)
	if got := DescriptorFor(id).Name; got != "VTOL_RepairPatrol" {
		t.Fatalf("code9 vtol repair patrol want VTOL_RepairPatrol got %q", got)
	}

	id = Resolve(10, actor, nil, nil)
	if id != 0 {
		t.Fatalf("code10 should reject got %q", DescriptorFor(id).Name)
	}

	id = Resolve(11, actor, nil, nil)
	if got := DescriptorFor(id).Name; got != "Teleport" {
		t.Fatalf("code11 want Teleport got %q", got)
	}

	actorNoReclaim := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) { d.CanReclamate = false; d.CanResurrect = false }))
	if id := Resolve(12, actorNoReclaim, nil, nil); id != 0 {
		t.Fatalf("code12 no reclaim should reject")
	}
	actorReclaim := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) { d.CanReclamate = true; d.CanResurrect = true }))
	posWreckRes := &ResolvePos{HasFeature: true, IsWreck: true, FeatureResurrectable: true}
	id = Resolve(12, actorReclaim, nil, posWreckRes)
	if got := DescriptorFor(id).Name; got != "Resurrect" {
		t.Fatalf("code12 wreck res want Resurrect got %q", got)
	}
	posFeatureOnly := &ResolvePos{HasFeature: true, IsWreck: false}
	id = Resolve(12, actorReclaim, nil, posFeatureOnly)
	if got := DescriptorFor(id).Name; got != "Reclaim" {
		t.Fatalf("code12 feature reclaim want Reclaim got %q", got)
	}
	actorReclaimVTOL := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) { d.CanReclamate = true; d.CanFly = true }))
	id = Resolve(12, actorReclaimVTOL, nil, posFeatureOnly)
	if got := DescriptorFor(id).Name; got != "VTOL_Reclaim" {
		t.Fatalf("code12 vtol reclaim want VTOL_Reclaim got %q", got)
	}
	unitTarget := mkUnit(23, 1, "CORE", 100, 100, true, 0, mkDef(nil))
	id = Resolve(12, actorReclaim, unitTarget, nil)
	if got := DescriptorFor(id).Name; got != "ReclaimUnit" {
		t.Fatalf("code12 unit reclaim want ReclaimUnit got %q", got)
	}
	id = Resolve(12, actorReclaimVTOL, unitTarget, nil)
	if got := DescriptorFor(id).Name; got != "VTOL_ReclaimUnit" {
		t.Fatalf("code12 vtol unit reclaim want VTOL_ReclaimUnit got %q", got)
	}

	actorNoCapture := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) { d.CanCapture = false }))
	if id := Resolve(13, actorNoCapture, hostileTarget, nil); id != 0 {
		t.Fatalf("code13 no capture should reject")
	}
	setTestHostility(actor, func(a, b *units.Unit) bool { return false })
	if id := Resolve(13, actor, hostileTarget, nil); id != 0 {
		t.Fatalf("code13 friendly should reject")
	}
	setTestHostility(actor, func(a, b *units.Unit) bool { return true })
	friendlySameOwner := mkUnit(24, 0, "CORE", 100, 100, true, 0, mkDef(func(d *content.UnitDef) { d.Side = "CORE" }))
	friendlySameOwner.Owner = 0
	if id := Resolve(13, actor, friendlySameOwner, nil); id != 0 {
		t.Fatalf("code13 same owner should reject")
	}
	hostileDiffOwner := mkUnit(25, 1, "CORE", 100, 100, true, 0, mkDef(func(d *content.UnitDef) { d.Side = "CORE" }))
	id = Resolve(13, actor, hostileDiffOwner, nil)
	if got := DescriptorFor(id).Name; got != "Capture" {
		t.Fatalf("code13 capture want Capture got %q", got)
	}
	setTestHostility(actor, nil)

	actorNonBuilder2 := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) { d.Builder = false }))
	if id := Resolve(14, actorNonBuilder2, nil, nil); id != 0 {
		t.Fatalf("code14 non builder should reject")
	}
	id = Resolve(14, actor, nil, nil)
	if got := DescriptorFor(id).Name; got != "MobileBuild" {
		t.Fatalf("code14 want MobileBuild got %q", got)
	}
	actorMobileVTOL := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) { d.Builder = true; d.CanFly = true }))
	id = Resolve(14, actorMobileVTOL, nil, nil)
	if got := DescriptorFor(id).Name; got != "VTOL_MobileBuild" {
		t.Fatalf("code14 vtol want VTOL_MobileBuild got %q", got)
	}

	if id := Resolve(0, actor, nil, nil); id != 0 {
		t.Fatalf("code0 out of range should reject")
	}
	if id := Resolve(15, actor, nil, nil); id != 0 {
		t.Fatalf("code15 out of range should reject")
	}
	if id := Resolve(2, nil, nil, nil); id != 0 {
		t.Fatalf("nil actor should reject")
	}
}

// TestAttackChaseOrbit locks `Attack_Chase` against [04 R-ORD-01 §3]: the
// pre-check order, the phase-0 admission, and the substate cycle with its
// standoff taken from the slot's weapon range [06 R-WPN-05 §1].
func TestAttackChaseOrbit(t *testing.T) {
	const weaponRange = int32(180)
	weapon := &content.WeaponDef{Range: weaponRange}
	actor := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) {
		d.CanMove = true
		d.CanFly = false
		d.CanAttack = true
	}))
	actor.Flags |= units.ArmedStatus
	actor.SlotAt(0).Weapon = weapon
	actor.X, actor.Y, actor.Z = 0, 0, 0
	targetUnit := mkUnit(2, 1, "CORE", 100, 100, true, 0, mkDef(nil))
	targetUnit.X = numeric.Fixed(400 * 65536)
	targetUnit.Y = 0
	targetUnit.Z = 0
	setTestLookup(actor, func(h pool.Handle) *units.Unit {
		if h == 99 {
			return targetUnit
		}
		return nil
	})
	defer setTestLookup(actor, nil)

	newChase := func(phase uint8, p2 uint32) *Node {
		return &Node{ID: Lookup("Attack_Chase"), Phase: phase, Param2: p2, Target: 99, Owner: 1}
	}

	// Substate 0 installs a point goal at the target with radius d, and is the
	// only arm that leaves p2 at 1.
	n := newChase(2, 0)
	if code := attackChaseHandler(actor, n, 0, 0); code != Code(1) {
		t.Fatalf("substate 0 want advance Code(1) got %d", code)
	}
	if n.Param2 != 1 {
		t.Fatalf("substate 0 should set p2 = 1, got %d", n.Param2)
	}
	if n.GoalX != targetUnit.X || n.GoalZ != targetUnit.Z {
		t.Fatalf("substate 0 goal want the target position, got %v,%v", n.GoalX, n.GoalZ)
	}

	// The strafe arm (1-4) with the two units level does NOT advance p2: it is
	// the repeating state, and the reachable cycle is 0 -> 1 -> 6 -> 7 -> 8 -> 0
	// [04 R-ORD-01 §3]'s correction to §3.5.
	for i := 0; i < 3; i++ {
		if code := attackChaseHandler(actor, n, 0, 0); code != Code(1) {
			t.Fatalf("strafe want advance Code(1) got %d", code)
		}
		if n.Param2 != 1 {
			t.Fatalf("strafe must leave p2 at 1, got %d after %d visits", n.Param2, i+1)
		}
	}
	// The strafe goal stands one standoff off the target, so it is never the
	// target's own position.
	if n.GoalX == targetUnit.X && n.GoalZ == targetUnit.Z {
		t.Fatalf("strafe goal should be offset from the target")
	}

	// More than eight world units of vertical separation jumps to substate 6.
	targetUnit.Y = numeric.Fixed(9 * 65536)
	if code := attackChaseHandler(actor, n, 0, 0); code != Code(1) {
		t.Fatalf("vertical jump want advance Code(1) got %d", code)
	}
	if n.Param2 != 6 {
		t.Fatalf("vertical separation > 8 should jump to substate 6, got %d", n.Param2)
	}
	// Exactly eight is NOT more than eight: the compare is strict.
	targetUnit.Y = numeric.Fixed(8 * 65536)
	n.Param2 = 1
	if code := attackChaseHandler(actor, n, 0, 0); code != Code(1) || n.Param2 != 1 {
		t.Fatalf("vertical separation of exactly 8 must stay on the strafe arm, got code %d p2 %d", code, n.Param2)
	}
	targetUnit.Y = 0

	// 6 -> 7 -> 8 -> 0 close the cycle.
	for _, step := range []struct{ from, want uint32 }{{6, 7}, {7, 8}, {8, 0}} {
		n.Param2 = step.from
		if code := attackChaseHandler(actor, n, 0, 0); code != Code(1) {
			t.Fatalf("substate %d want advance Code(1) got %d", step.from, code)
		}
		if n.Param2 != step.want {
			t.Fatalf("substate %d should move to %d, got %d", step.from, step.want, n.Param2)
		}
	}

	// p2 >= 9 and phase > 3 both cancel the whole queue.
	n.Param2 = 9
	if code := attackChaseHandler(actor, n, 0, 0); code != Code(7) {
		t.Fatalf("substate >= 9 want cancel-all Code(7) got %d", code)
	}
	if code := attackChaseHandler(actor, newChase(4, 0), 0, 0); code != Code(7) {
		t.Fatalf("phase > 3 want cancel-all Code(7) got %d", code)
	}

	// Pre-checks, in [04 R-ORD-01 §3]'s order.
	if code := attackChaseHandler(actor, newChase(2, 0), 0x800, 0); code != Code(5) {
		t.Fatalf("pending 0x800 want complete Code(5) got %d", code)
	}
	if code := attackChaseHandler(actor, &Node{ID: Lookup("Attack_Chase"), Target: 0}, 0, 0); code != Code(5) {
		t.Fatalf("null target want complete Code(5) got %d", code)
	}
	if code := attackChaseHandler(actor, newChase(2, 0), pendTargetRemoved, 0); code != Code(5) {
		t.Fatalf("target removed want complete Code(5) got %d", code)
	}
	if code := attackChaseHandler(actor, newChase(2, 0), pendTargetCloaked, 0); code != Code(5) {
		t.Fatalf("target cloaked want complete Code(5) got %d", code)
	}
	// A satisfied word carrying only the deadline bit is NOT a pre-check hit —
	// the retired placeholder masks aliased exactly these bits.
	if code := attackChaseHandler(actor, newChase(2, 0), 0x1, 0); code == Code(5) {
		t.Fatalf("the deadline bit must not complete the chase")
	}
	if code := attackChaseHandler(actor, newChase(2, 0), 0x6, 0); code == Code(5) {
		t.Fatalf("movement bits must not complete the chase")
	}

	// The leash of [R-STANCE-01 §4], inclusive.
	far := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(nil))
	far.Flags |= units.ArmedStatus
	far.X = numeric.Fixed(100 * 65536)
	nLeash := &Node{ID: Lookup("Attack_Chase"), Phase: 2, Target: 99, Param3: 10}
	if code := attackChaseHandler(far, nLeash, 0, 0); code != Code(5) {
		t.Fatalf("leash exceeded want complete Code(5) got %d", code)
	}

	// Phase 0 admission: canfly, an unarmed unit, and a building all cancel;
	// a live armed ground mover advances and seeds the goal and the slot.
	air := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) { d.CanFly = true }))
	air.Flags |= units.ArmedStatus
	if code := attackChaseHandler(air, newChase(0, 0), 0, 0); code != Code(7) {
		t.Fatalf("canfly want cancel-all Code(7) got %d", code)
	}
	unarmed := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(nil))
	unarmed.Flags &^= units.ArmedStatus
	if code := attackChaseHandler(unarmed, newChase(0, 0), 0, 0); code != Code(7) {
		t.Fatalf("unarmed want cancel-all Code(7) got %d", code)
	}
	building := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(nil))
	building.Flags |= units.ArmedStatus | units.BuildingClassStatus
	if code := attackChaseHandler(building, newChase(0, 0), 0, 0); code != Code(7) {
		t.Fatalf("no mover reference want cancel-all Code(7) got %d", code)
	}

	admit := newChase(0, 5)
	admit.GoalX = numeric.Fixed(999)
	actor.X = numeric.Fixed(7 * 65536)
	if code := attackChaseHandler(actor, admit, 0, 0); code != Code(1) {
		t.Fatalf("phase 0 admit want advance Code(1) got %d", code)
	}
	if admit.GoalX != actor.X || admit.Param2 != 0 {
		t.Fatalf("admit should seed goal from own position and reset p2, got %v p2=%d", admit.GoalX, admit.Param2)
	}
	if admit.Phase != 0 {
		t.Fatalf("the handler must not advance its own phase; the pump does")
	}
}

func TestGuardAssistOrdering(t *testing.T) {
	actor := mkUnit(10, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) {
		d.CanFly = false
		d.Builder = true
		d.CanReclamate = true
	}))
	ward := mkUnit(20, 0, "ARM", 50, 100, true, 0.5, mkDef(func(d *content.UnitDef) {
		d.Side = "ARM"
	}))
	ward.X = numeric.Fixed(100 * 65536)
	ward.Y = numeric.Fixed(0)
	ward.Z = numeric.Fixed(100 * 65536)
	ward.Handle = 20
	actor.Handle = 10
	actor.GuardLatches = units.GuardLatches{}
	BindQueue(actor, &Queue{})
	BindQueue(ward, &Queue{})
	// Ward has build order for (d): push HelpBuild onto ward
	wardQ := QueueForUnit(ward)
	wardQ.Push(Lookup("HelpBuild"), Node{Target: 99, GoalX: ward.X, GoalY: ward.Y, GoalZ: ward.Z})
	setTestLookup(actor, func(h pool.Handle) *units.Unit {
		if h == 20 {
			return ward
		}
		return nil
	})
	defer setTestLookup(actor, nil)
	setTestHostility(actor, func(a, b *units.Unit) bool { return false })
	defer setTestHostility(actor, nil)

	// Phase 1 is the assist evaluation; phase 0 is the admit that computes the
	// follow radius and draws the anchor direction [04 R-ORD-01 §8], so the
	// leg ordering below runs from phase 1 (WU-19-6: these calls used to reach
	// the legs at phase 0, which now admits instead).
	n := &Node{
		ID:     Lookup("Follow_Ground"),
		Target: 20,
		Param1: 64,
		Phase:  1,
		Owner:  10,
	}

	q := QueueForUnit(actor)
	q.primary = nil
	q.secondary = nil
	// First call should pick (a) build assist – top priority (ward unfinished, friendly)
	code := guardHandler(actor, n, 0, 0)
	if code != Code(3) {
		t.Fatalf("(a) first call should return Code(3) wait got %d", code)
	}
	if len(q.primary) == 0 || DescriptorFor(q.primary[0].ID).Name != "HelpBuild" {
		t.Fatalf("(a) should enqueue HelpBuild got %v", func() []string {
			var s []string
			for _, nn := range q.primary {
				s = append(s, DescriptorFor(nn.ID).Name)
			}
			return s
		}())
	}
	// Second call: (a) is deduped (same ward 20), (b) is stub false pending WU-06-7, so should fall to (c) repair assist
	// TODO(question): auto-fire (b) pending WU-06-7; stub returns false, so ordering skips (b)
	q.primary = nil
	q.secondary = nil
	code = guardHandler(actor, n, 0, 0)
	if code != Code(3) {
		t.Fatalf("(c) after (a) dedup should return Code(3) got %d", code)
	}
	if len(q.primary) == 0 || DescriptorFor(q.primary[0].ID).Name != "RepairUnit" {
		t.Fatalf("(c) should enqueue RepairUnit got %v", func() []string {
			var s []string
			for _, nn := range q.primary {
				s = append(s, DescriptorFor(nn.ID).Name)
			}
			return s
		}())
	}
	// Third: (a) deduped, (c) deduped, should fall to (d) join ward's build
	q.primary = nil
	code = guardHandler(actor, n, 0, 0)
	if code != Code(3) {
		t.Fatalf("(d) after (a)(c) dedup should return 3 got %d", code)
	}
	if len(q.primary) == 0 || DescriptorFor(q.primary[0].ID).Name != "HelpBuild" {
		t.Fatalf("(d) should enqueue HelpBuild for join ward build got %v", func() []string {
			var s []string
			for _, nn := range q.primary {
				s = append(s, DescriptorFor(nn.ID).Name)
			}
			return s
		}())
	}
	// Fourth: the assist legs all decline => the follow maintenance of
	// [04 R-ORD-01 §8 points 3 and 4]: a point goal at ward+offset, deadline
	// tick+30 fixed, gate 0x19 on the way out, hold — and the record's goal
	// triple still holds the OFFSET it was given, not the installed position.
	q.primary = nil
	offset := numeric.Fixed(48 << 16)
	n.GoalX, n.GoalY, n.GoalZ = offset, 0, -offset
	n.DynamicGate = 0
	code = guardHandler(actor, n, 0, 700)
	if code != Code(2) {
		t.Fatalf("maintenance should *hold* (2) got %d", code)
	}
	if n.GoalX != offset || n.GoalZ != -offset {
		t.Fatalf("maintenance must keep the stored offset, got %v/%v", n.GoalX, n.GoalZ)
	}
	if n.Deadline != 730 || n.DynamicGate != 0x19 {
		t.Fatalf("maintenance want deadline 730 gate 0x19 got %d/%#x", n.Deadline, n.DynamicGate)
	}
	if n.Phase != 1 {
		t.Fatalf("maintenance must leave the phase at 1 got %d", n.Phase)
	}
	if len(q.primary) != 0 {
		t.Fatalf("maintenance should not enqueue new order, got %d", len(q.primary))
	}

	// Verify top-down order: if (a) not deduped, it wins even when lower conditions also true
	actor.GuardLatches.BuildAssist = [units.GuardLatchSize]pool.Handle{}
	q.primary = nil
	code = guardHandler(actor, n, 0, 0)
	if code != Code(3) || len(q.primary) == 0 || DescriptorFor(q.primary[0].ID).Name != "HelpBuild" {
		t.Fatalf("top-down: (a) should win when not deduped")
	}

	// Handle 0 early return: slot 0 is null [01 §6.1], no assist paths fire,
	// only the follow maintenance.
	actorZero := mkUnit(0, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) {
		d.Builder = true
		d.CanFly = false
	}))
	actorZero.Handle = 0
	BindQueue(actorZero, &Queue{})
	setTestLookup(actorZero, func(h pool.Handle) *units.Unit {
		if h == 20 {
			return ward
		}
		return nil
	})
	// ward still 20, unfinished/damaged so (a) and (c) would be true, but Handle 0 should skip them
	n2 := &Node{ID: Lookup("Follow_Ground"), Target: 20, Param1: 64, Phase: 1, Owner: 0}
	q2 := QueueForUnit(actorZero)
	q2.primary = nil
	code = guardHandler(actorZero, n2, 0, 0)
	if code != Code(2) {
		t.Fatalf("Handle 0 should still *hold* via the maintenance leg got %d", code)
	}
	if n2.DynamicGate != 0x19 {
		t.Fatalf("Handle 0 maintenance want gate 0x19 got %#x", n2.DynamicGate)
	}
	if len(q2.primary) != 0 {
		t.Fatalf("Handle 0 should not enqueue assist, only maintenance")
	}
}

func TestGuardHandlerMissingWard(t *testing.T) {
	actor := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(nil))
	n := &Node{ID: Lookup("Follow_Ground"), Target: 0}
	code := guardHandler(actor, n, 0, 0)
	if code != Code(5) {
		t.Fatalf("missing ward should abandon code 5 got %d", code)
	}
	setTestLookup(actor, func(h pool.Handle) *units.Unit { return nil })
	defer setTestLookup(actor, nil)
	n.Target = 99
	code = guardHandler(actor, n, 0, 0)
	if code != Code(5) {
		t.Fatalf("nil ward lookup should abandon")
	}
}

func TestResolveHandlerRegistration(t *testing.T) {
	EnsureHandlers()
	chaseID := Lookup("Attack_Chase")
	if chaseID == 0 {
		t.Fatalf("Attack_Chase lookup failed")
	}
	if DescriptorFor(chaseID).Handler == nil {
		t.Fatalf("Attack_Chase handler not registered")
	}
	followID := Lookup("Follow_Ground")
	if DescriptorFor(followID).Handler == nil {
		t.Fatalf("Follow_Ground handler not registered")
	}
	vtolFollowID := Lookup("VTOL_Follow")
	if DescriptorFor(vtolFollowID).Handler == nil {
		t.Fatalf("VTOL_Follow handler not registered")
	}
	guardID := Lookup("Guard_NoMove")
	if DescriptorFor(guardID).Handler == nil {
		t.Fatalf("Guard_NoMove handler not registered")
	}
}

// TestResolveAttackSkipsInactiveSentinelWeapon locks the air-attack-variant
// gate against the record-0 inactive sentinel [02 §5 R-CONTENT-02]: a flyer
// whose primary link resolved to the [noweapon] record is not an air-attack
// platform, so the variant falls through to AirToGround even though the link
// is non-nil. An active ToAirWeapon link is unchanged.
func TestResolveAttackSkipsInactiveSentinelWeapon(t *testing.T) {
	sentinel := &content.WeaponDef{ID: 0, ToAirWeapon: true}
	sentinel.CanonicalKey = "noweapon"
	active := &content.WeaponDef{ID: 3, ToAirWeapon: true}
	active.CanonicalKey = "armthunder"
	flyer := mkDef(func(d *content.UnitDef) { d.CanFly = true; d.Builder = false })
	target := mkUnit(2, 1, "CORE", 100, 0, true, 0, mkDef(nil))

	flyer.Weapon1Def = sentinel
	if got := resolveAttack(mkUnit(1, 0, "ARM", 100, 0, true, 0, flyer), target); got != "AirToGround" {
		t.Fatalf("sentinel weapon1 gave %q, want AirToGround [02 §5 R-CONTENT-02]", got)
	}

	flyer.Weapon1Def = active
	if got := resolveAttack(mkUnit(1, 0, "ARM", 100, 0, true, 0, flyer), target); got != "AirToAir" {
		t.Fatalf("active ToAirWeapon changed behavior: got %q, want AirToAir", got)
	}
}

func TestResolvePositionOnlyAttackUsesCanonicalCodeThreeArm(t *testing.T) {
	pos := &ResolvePos{X: numeric.FixedFromInt(30), Y: numeric.FixedFromInt(7), Z: numeric.FixedFromInt(40)}

	ground := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) {
		d.CanAttack = true
		d.CanFly = false
	}))
	ground.Flags |= units.ArmedStatus
	if got := DescriptorFor(Resolve(3, ground, nil, pos)).Name; got != "Suppress" {
		t.Fatalf("ground position attack resolved %q, want Suppress", got)
	}

	dropped := &content.WeaponDef{Dropped: true}
	flyer := mkUnit(2, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) {
		d.CanAttack = true
		d.CanFly = true
		d.Weapon1Def = dropped
	}))
	flyer.Flags |= units.ArmedStatus
	if got := DescriptorFor(Resolve(3, flyer, nil, pos)).Name; got != "AirStrike" {
		t.Fatalf("dropped-weapon position attack resolved %q, want AirStrike", got)
	}
	flyer.Def.Weapon1Def = nil
	if got := DescriptorFor(Resolve(3, flyer, nil, pos)).Name; got != "AirToGround" {
		t.Fatalf("ordinary air position attack resolved %q, want AirToGround", got)
	}

	unarmed := mkUnit(3, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) {
		d.CanAttack = true
		d.CanFly = false
	}))
	unarmed.Flags &^= units.ArmedStatus
	if got := Resolve(3, unarmed, nil, pos); got != 0 {
		t.Fatalf("unarmed position attack id=%d, want reject sentinel", got)
	}
}
