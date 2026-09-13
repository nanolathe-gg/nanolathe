package orders

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
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
	setTestSeaLevel(u, 0) // explicit authored fixture map [04 R-ORD-02 §1]
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

// setTestBuildList binds command code 14's build-list query [04 R-ORD-02 §1].
func setTestBuildList(u *units.Unit, fn func(*content.UnitDef) bool) {
	q := QueueForUnit(u)
	b := q.Binding()
	if b == nil {
		b = &QueueBinding{}
	}
	b.BuildList = fn
	q.SetBinding(b)
}

// setTestAdmission binds the carriable test — §10.2's nine-reject transport
// admission — onto the CARRIER's queue [04 §10.2][04 R-ORD-02 §1].
func setTestAdmission(u *units.Unit, fn func(carrier, candidate *units.Unit) bool) {
	q := QueueForUnit(u)
	b := q.Binding()
	if b == nil {
		b = &QueueBinding{}
	}
	b.TransportAdmission = fn
	q.SetBinding(b)
}

// setTestSeaLevel binds the map's sea level, which nano-reach's water clause
// reads through the world adapter [04 R-ORD-01 §7].
func setTestSeaLevel(u *units.Unit, level uint8) {
	q := QueueForUnit(u)
	b := q.Binding()
	if b == nil {
		b = &QueueBinding{}
	}
	if b.World == nil {
		b.World = &WorldQueryAdapter{}
	}
	b.World.SeaLevel = func() uint8 { return level }
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
	// The default interface type "never turns a click on a damaged friendly
	// into a repair (only an unfinished one into assistance)"
	// [04 R-ORD-02 §1]: a damaged COMPLETE friendly falls past step 3's
	// assistance half, past the own-unit reject (this fixture carries no
	// selectable bit — see TestContextualDefaultVariantRows for the reject
	// itself), past the feature tests, and out at the move.
	friendlyDamaged := mkUnit(3, 0, "ARM", 50, 100, true, 0, mkDef(nil))
	setTestHostility(actor, func(a, b *units.Unit) bool { return false })
	assertResolve(1, friendlyDamaged, nil, "Move_Ground")
	friendlyFrame := mkUnit(30, 0, "ARM", 50, 100, true, 0.5, mkDef(nil))
	assertResolve(1, friendlyFrame, nil, "HelpBuild")
	transportable := mkUnit(4, 1, "CORE", 100, 100, true, 0, mkDef(func(d *content.UnitDef) {
		d.CantBeTransported = false
	}))
	actorNoAttack := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) {
		d.CanMove = true
		d.CanAttack = false
		d.Builder = false
	}))
	setTestHostility(actorNoAttack, func(a, b *units.Unit) bool { return true })
	// Carriable is §10.2's nine-reject admission, asked of the carrier's queue
	// binding [04 R-ORD-02 §1]; the ladder itself is exercised against
	// internal/movement's owner in resolve_contracts_test.go.
	setTestAdmission(actorNoAttack, func(_, candidate *units.Unit) bool {
		return candidate != nil && candidate.Def != nil && !candidate.Def.CantBeTransported
	})
	// The default variant "never resolves pickup, follow, or landing
	// contextually; those need the explicit codes" [04 R-ORD-02 §1]. This
	// fixture's hostility predicate answers true, and the actor authors
	// `canreclamate`, so the click is consumed by step 2 — code 12 with a
	// target and no feature at the position. Code 6 still picks it up.
	id := Resolve(1, actorNoAttack, transportable, nil)
	if got := DescriptorFor(id).Name; got != "ReclaimUnit" {
		t.Fatalf("code1 hostile carriable want ReclaimUnit got %q", got)
	}
	if got := DescriptorFor(Resolve(6, actorNoAttack, transportable, nil)).Name; got != "Ground_Pickup" {
		t.Fatalf("code6 transportable want Ground_Pickup got %q", got)
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
	if got := DescriptorFor(id).Name; got != "Resurrect" {
		t.Fatalf("code1 reclaimable feature want Resurrect got %q", got)
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
	// A nanoframe's health is scaled with its remaining fraction [04 §2.3], so
	// a half-built frame is below `maxdamage` — which is one of nano-reach's
	// four terms and therefore part of what the assist arm admits
	// [04 R-ORD-01 §7][04 R-ORD-02 §1].
	friendlyUnfinished := mkUnit(7, 0, "ARM", 50, 100, true, 0.5, mkDef(nil))
	id = Resolve(2, actor, friendlyUnfinished, nil)
	if got := DescriptorFor(id).Name; got != "HelpBuild" {
		t.Fatalf("code2 friendly build want HelpBuild got %q", got)
	}
	// "I am `canfly`, friendly, target `isairbase` → `VTOL_Landing`"
	// [04 R-ORD-02 §1] code 2: all three terms are required. A ground actor on
	// the same pad falls through to the pickup arm below it.
	padTarget := mkUnit(8, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) { d.IsAirBase = true }))
	padFlyer := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) {
		d.CanMove = true
		d.CanFly = true
	}))
	setTestHostility(padFlyer, func(a, b *units.Unit) bool { return false })
	id = Resolve(2, padFlyer, padTarget, nil)
	if got := DescriptorFor(id).Name; got != "VTOL_Landing" {
		t.Fatalf("code2 pad want VTOL_Landing got %q", got)
	}
	id = Resolve(2, actor, padTarget, nil)
	if got := DescriptorFor(id).Name; got == "VTOL_Landing" {
		t.Fatalf("code2 ground actor must not land on a pad [04 R-ORD-02 §1]")
	}
	setTestHostility(actor, func(a, b *units.Unit) bool { return false })
	// Code 2's pickup arm asks the same carriable question code 6 does — the
	// nine-reject admission, of the acting unit as carrier [04 R-ORD-02 §1].
	setTestAdmission(actor, func(_, candidate *units.Unit) bool {
		return candidate != nil && candidate.Def != nil && !candidate.Def.CantBeTransported
	})
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
	// The hover fork is the ACTOR's `hoverattack` [04 R-ORD-02 §1]; the
	// target's `canhover` selects nothing.
	actorHoverAttack := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) {
		d.CanAttack = true
		d.CanFly = true
		d.HoverAttack = true
	}))
	targetHover := mkUnit(15, 1, "CORE", 100, 100, true, 0, mkDef(func(d *content.UnitDef) { d.CanFly = false; d.CanHover = true }))
	id = Resolve(3, actorHoverAttack, targetHover, nil)
	if got := DescriptorFor(id).Name; got != "AirToGroundHover" {
		t.Fatalf("code3 hover want AirToGroundHover got %q", got)
	}
	id = Resolve(3, actorAir, targetHover, nil)
	if got := DescriptorFor(id).Name; got != "AirToGround" {
		t.Fatalf("code3 canhover target without hoverattack actor want AirToGround got %q", got)
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
	// Code 6's whole gate is the carriable test, asked of the acting carrier
	// [04 R-ORD-02 §1]; reject 1 stands in for the ladder here.
	admitByKey := func(_, candidate *units.Unit) bool {
		return candidate != nil && candidate.Def != nil && !candidate.Def.CantBeTransported
	}
	setTestAdmission(actorLoad, admitByKey)
	setTestAdmission(actorLoadVTOL, admitByKey)
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

	// Code 8's gate is nano-reach, whose first term is the `canreclamate`
	// mirror bit — not the authored `builder` key [04 R-ORD-02 §1]
	// [04 R-ORD-01 §7]. This assertion used to clear `Builder`, which the
	// corrected gate does not read at all.
	actorNoMirrorBit := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) { d.CanReclamate = false }))
	if id := Resolve(8, actorNoMirrorBit, friendlyDamaged, nil); id != 0 {
		t.Fatalf("code8 without the canreclamate mirror bit should reject, got %q", DescriptorFor(id).Name)
	}
	if id := Resolve(8, actor, nil, nil); id != 0 {
		t.Fatalf("code8 no target should reject")
	}
	// Health below `maxdamage` is nano-reach's own health term, and a partly
	// built frame's health is scaled with its remaining fraction [04 §2.3].
	unfinished := mkUnit(21, 0, "ARM", 50, 100, true, 0.5, mkDef(nil))
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
	if got := DescriptorFor(id).Name; got != "Resurrect" {
		t.Fatalf("code12 reclaimable feature want Resurrect got %q", got)
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
	// "hostility is **not** tested — an allied unit of another player is
	// capturable by this code", correcting §3.4's row 13 [04 R-ORD-02 §1]. Only
	// the owner-differs term rejects.
	setTestHostility(actor, func(a, b *units.Unit) bool { return false })
	if id := Resolve(13, actor, hostileTarget, nil); DescriptorFor(id).Name != "Capture" {
		t.Fatalf("code13 friendly differently-owned want Capture got %q", DescriptorFor(id).Name)
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

	// Code 14 gates on the definition's compiled build list being non-empty,
	// never on the authored `builder` key [04 R-ORD-02 §1]. The query is the
	// session-owned catalog seam; an empty page is a reject.
	actorEmptyList := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) { d.Builder = true }))
	setTestBuildList(actorEmptyList, func(*content.UnitDef) bool { return false })
	if id := Resolve(14, actorEmptyList, nil, nil); id != 0 {
		t.Fatalf("code14 with an empty build list should reject, got %q", DescriptorFor(id).Name)
	}
	setTestBuildList(actor, func(*content.UnitDef) bool { return true })
	id = Resolve(14, actor, nil, nil)
	if got := DescriptorFor(id).Name; got != "MobileBuild" {
		t.Fatalf("code14 want MobileBuild got %q", got)
	}
	actorMobileVTOL := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) { d.Builder = true; d.CanFly = true }))
	setTestBuildList(actorMobileVTOL, func(*content.UnitDef) bool { return true })
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

// TestGuardAssistOrdering locks the phase-1 leg order of [04 R-UNIT-06 §1] as
// that section corrects it, and the absence of any latch between visits.
//
// The build-assist branch that used to sit above leg 3 is gone: §1's correction
// says the top of phase 1 is a combat join, and an unfinished ward is leg 3's
// business because command code 8 forks to help-build while unfinished and to
// repair otherwise [04 R-ORD-02 §1].
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
	BindQueue(actor, &Queue{})
	BindQueue(ward, &Queue{})
	// The ward's own front order is a nanolathe-class build elsewhere, which is
	// leg 4's precondition.
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
	// follow radius and draws the anchor direction [04 R-ORD-01 §8].
	n := &Node{
		ID:     Lookup("Follow_Ground"),
		Target: 20,
		Param1: 64,
		Phase:  1,
		Owner:  10,
	}

	q := QueueForUnit(actor)
	names := func() []string {
		var s []string
		for _, nn := range q.primary {
			s = append(s, DescriptorFor(nn.ID).Name)
		}
		return s
	}

	// Leg 3 wins: the ward is damaged and the guard has the builder bit, so
	// command code 8 resolves. The ward is unfinished, so code 8's fork gives
	// HelpBuild — the leg the retired build-assist branch used to serve.
	q.primary = nil
	q.secondary = nil
	n.DynamicGate = 0x19
	code := guardHandler(actor, n, 0, 0)
	if code != Code(3) {
		t.Fatalf("leg 3 should return the wait code 3, got %d", code)
	}
	if len(q.primary) == 0 || DescriptorFor(q.primary[0].ID).Name != "HelpBuild" {
		t.Fatalf("leg 3 on an unfinished ward should enqueue HelpBuild, got %v", names())
	}
	// "clear the dynamic gate ... so a guard that resumes re-enters phase 1
	// with an empty gate" [04 R-ORD-01 §8 point 4].
	if n.DynamicGate != 0 {
		t.Fatalf("a spawning leg must clear the record gate, got %#x", n.DynamicGate)
	}

	// No latch: the very next visit takes leg 3 again. Retail keeps "no dedup
	// array and no latch in either guard handler" [04 R-UNIT-06 §1]; re-enqueue
	// discipline is the pump's deadline cadence, not a per-ward memory.
	q.primary = nil
	q.secondary = nil
	if code = guardHandler(actor, n, 0, 0); code != Code(3) {
		t.Fatalf("no latch: the second visit must take leg 3 again, got %d", code)
	}
	if len(q.primary) == 0 || DescriptorFor(q.primary[0].ID).Name != "HelpBuild" {
		t.Fatalf("no latch: second visit should enqueue HelpBuild again, got %v", names())
	}

	// Leg 4 is reached only when leg 3 declines. A full-health finished ward
	// declines it (health equal to maximum-damage), and both definitions carry
	// the builder bit, so the guard joins the ward's own build.
	ward.Health, ward.MaxHealth = 100, 100
	ward.Remaining = 0
	q.primary = nil
	if code = guardHandler(actor, n, 0, 0); code != Code(3) {
		t.Fatalf("leg 4 should return the wait code 3, got %d", code)
	}
	if len(q.primary) == 0 || DescriptorFor(q.primary[0].ID).Name != "HelpBuild" {
		t.Fatalf("leg 4 should enqueue HelpBuild toward the ward's build, got %v", names())
	}
	// "toward the front order's target with the front order's goal position"
	// [04 R-UNIT-06 §1] leg 4 — not toward the ward.
	if q.primary[0].Target != 99 {
		t.Fatalf("leg 4 must target the ward's front-order target 99, got %d", q.primary[0].Target)
	}

	// Leg 4's self-target exclusion: "its target is not the guard itself".
	wardQ.primary[0].Target = actor.Handle
	q.primary = nil
	if code = guardHandler(actor, n, 0, 700); code != Code(2) {
		t.Fatalf("a ward building the guard must not spawn a self help-build; want hold 2, got %d", code)
	}
	if len(q.primary) != 0 {
		t.Fatalf("leg 4 self-target exclusion enqueued %v", names())
	}
	wardQ.primary[0].Target = 99

	// Leg 5, the follow maintenance, on the visit that falls through: a point
	// goal at ward+offset, deadline tick+30 fixed, gate 0x19 on the way out,
	// hold — and the record's goal triple still holds the OFFSET.
	ward.Def.Builder = false // leg 4 needs the WARD's builder bit too
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
	ward.Def.Builder = true

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

// TestResolveAirAttackVariants locks the four air variants of code 3 against
// [04 R-ORD-02 §1]: the only weapon term is the primary's `dropped` flag, the
// only target term is its `canfly`, and the hover fork is the ACTOR's
// `hoverattack`. It also locks the record-0 inactive sentinel
// [02 §5 R-CONTENT-02]: a flyer whose primary link resolved to the [noweapon]
// record is not a bomber even though the link is non-nil.
func TestResolveAirAttackVariants(t *testing.T) {
	sentinelDropped := &content.WeaponDef{ID: 0, Dropped: true}
	sentinelDropped.CanonicalKey = "noweapon"
	bomb := &content.WeaponDef{ID: 3, Dropped: true}
	bomb.CanonicalKey = "armthunder_bomb"
	gun := &content.WeaponDef{ID: 4}
	gun.CanonicalKey = "armfig_gun"

	ground := mkUnit(2, 1, "CORE", 100, 0, true, 0, mkDef(nil))
	flying := mkUnit(3, 1, "CORE", 100, 0, true, 0, mkDef(func(d *content.UnitDef) { d.CanFly = true }))

	mkFlyer := func(w *content.WeaponDef, hoverAttack bool) *units.Unit {
		d := mkDef(func(d *content.UnitDef) {
			d.CanFly = true
			d.Builder = false
			d.HoverAttack = hoverAttack
		})
		d.Weapon1Def = w
		return mkUnit(1, 0, "ARM", 100, 0, true, 0, d)
	}

	// A dropped primary is the AirStrike fork; against a flying target the same
	// primary rejects outright ("a `dropped` *W1* against a flying target →
	// reject").
	if got := resolveAttack(mkFlyer(bomb, false), ground); got != "AirStrike" {
		t.Fatalf("dropped vs ground gave %q, want AirStrike [04 R-ORD-02 §1]", got)
	}
	if got := resolveAttack(mkFlyer(bomb, false), flying); got != "" {
		t.Fatalf("dropped vs flying gave %q, want reject [04 R-ORD-02 §1]", got)
	}
	// A non-dropped primary against a flyer is AirToAir; against ground the
	// hover fork is the ACTOR's hoverattack, not any property of the target.
	if got := resolveAttack(mkFlyer(gun, false), flying); got != "AirToAir" {
		t.Fatalf("gun vs flying gave %q, want AirToAir [04 R-ORD-02 §1]", got)
	}
	if got := resolveAttack(mkFlyer(gun, false), ground); got != "AirToGround" {
		t.Fatalf("gun vs ground gave %q, want AirToGround [04 R-ORD-02 §1]", got)
	}
	if got := resolveAttack(mkFlyer(gun, true), ground); got != "AirToGroundHover" {
		t.Fatalf("hoverattack vs ground gave %q, want AirToGroundHover [04 R-ORD-02 §1]", got)
	}
	// The inactive sentinel is not a dropped weapon, so it takes the ordinary
	// ground run rather than the bombing run.
	if got := resolveAttack(mkFlyer(sentinelDropped, false), ground); got != "AirToGround" {
		t.Fatalf("sentinel weapon1 gave %q, want AirToGround [02 §5 R-CONTENT-02]", got)
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

// TestContextualDefaultVariantRows walks the `Interface Type = 0` variant's six
// steps in order [04 R-ORD-02 §1] code 1. This is retail's default;
// the rows it locks are the ones the
// `1` variant would answer differently, so a silent slip back to that variant
// fails here.
func TestContextualDefaultVariantRows(t *testing.T) {
	newActor := func(handle pool.Handle) *units.Unit {
		a := mkUnit(handle, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) {
			d.CanAttack = true
			d.CanReclamate = true
			d.CanMove = true
			d.CanGuard = true // the `1` variant would follow; this one must not
			d.CanLoad = true  // ... nor pick up
			d.CanFly = false
			d.MaxWaterDepth = 255
		}))
		setTestHostility(a, func(_, target *units.Unit) bool { return target.Owner != a.Owner })
		setTestAdmission(a, func(_, candidate *units.Unit) bool { return candidate != nil })
		return a
	}
	name := func(a, target *units.Unit, pos *ResolvePos) string {
		return DescriptorFor(Resolve(1, a, target, pos)).Name
	}

	// Step 3, first half: an unfinished friendly is assistance, resolved as
	// code 8 — the one repair-family answer the default variant gives.
	frame := mkUnit(10, 0, "ARM", 30, 100, true, 0.5, mkDef(nil))
	if got := name(newActor(1), frame, nil); got != "HelpBuild" {
		t.Fatalf("unfinished friendly = %q, want HelpBuild [04 R-ORD-02 §1] code 1 step 3", got)
	}

	// Step 3, second half — the own-unit reject. A complete, selectable unit of
	// my own slot is a selection, not an order, so the click resolves NOTHING,
	// even though the actor could move, guard and carry it.
	own := mkUnit(11, 0, "ARM", 40, 100, true, 0, mkDef(nil))
	own.Flags |= units.ClassifierEligibleStatus
	if id := Resolve(1, newActor(2), own, nil); id != 0 {
		t.Fatalf("own complete selectable target resolved %q, want the reject identity"+
			" [04 R-ORD-02 §1] code 1 step 3", DescriptorFor(id).Name)
	}
	// Each clause of the reject in turn: drop one and the click falls through to
	// the move arm [07 R-WGT-01 §9].
	notSelectable := mkUnit(12, 0, "ARM", 40, 100, true, 0, mkDef(nil))
	if got := name(newActor(3), notSelectable, nil); got != "Move_Ground" {
		t.Fatalf("target without the selectable bit = %q, want Move_Ground", got)
	}
	otherSlot := mkUnit(13, 2, "ARM", 40, 100, true, 0, mkDef(nil))
	otherSlot.Flags |= units.ClassifierEligibleStatus
	actorAllied := newActor(4)
	setTestHostility(actorAllied, func(_, _ *units.Unit) bool { return false })
	if got := name(actorAllied, otherSlot, nil); got != "Move_Ground" {
		t.Fatalf("another slot's unit = %q, want Move_Ground (the reject is own-slot only)", got)
	}

	// The carrier clause: cargo aboard an ordinary transport is not a selection,
	// cargo attached to a carrier carrying the cargo-selectable bit is
	// [04 R-UNIT-06 §3][07 R-WGT-01 §9].
	plainCarrier := mkUnit(20, 0, "ARM", 100, 100, true, 0, mkDef(nil))
	padCarrier := mkUnit(21, 0, "ARM", 100, 100, true, 0, mkDef(nil))
	padCarrier.Flags |= units.CargoSelectableStatus
	carried := mkUnit(22, 0, "ARM", 40, 100, true, 0, mkDef(nil))
	carried.Flags |= units.ClassifierEligibleStatus
	carried.Attachment.Carrier = plainCarrier.Handle
	actorCarry := newActor(5)
	setTestLookup(actorCarry, func(h pool.Handle) *units.Unit {
		switch h {
		case plainCarrier.Handle:
			return plainCarrier
		case padCarrier.Handle:
			return padCarrier
		}
		return nil
	})
	if got := name(actorCarry, carried, nil); got != "Move_Ground" {
		t.Fatalf("cargo aboard an ordinary transport = %q, want Move_Ground", got)
	}
	carried.Attachment.Carrier = padCarrier.Handle
	if id := Resolve(1, actorCarry, carried, nil); id != 0 {
		t.Fatalf("cargo on a cargo-selectable carrier resolved %q, want the reject identity",
			DescriptorFor(id).Name)
	}

	// Step 1: a hostile unit an actor can attack.
	enemy := mkUnit(30, 1, "CORE", 100, 100, true, 0, mkDef(func(d *content.UnitDef) {
		d.Side = "CORE"
		d.CanMove = true
	}))
	enemy.Move.Mode = 1
	if got := name(newActor(6), enemy, nil); got != "Attack_Chase" {
		t.Fatalf("hostile target = %q, want Attack_Chase [04 R-ORD-02 §1] code 1 step 1", got)
	}

	// Step 2: with no attack capability, a `canreclamate` actor strips the same
	// hostile target — resolved as code 12, so the feature at the position wins
	// over the unit when there is one.
	stripper := newActor(7)
	stripper.Def.CanAttack = false
	stripper.Flags &^= units.ArmedStatus
	if got := name(stripper, enemy, nil); got != "ReclaimUnit" {
		t.Fatalf("hostile target, no attack = %q, want ReclaimUnit [04 R-ORD-02 §1] code 1 step 2", got)
	}
	feature := &ResolvePos{HasFeature: true}
	if got := name(stripper, enemy, feature); got != "Reclaim" {
		t.Fatalf("hostile target over a feature = %q, want Reclaim — code 12 resolves the"+
			" feature first [04 R-ORD-02 §1] code 1 step 2", got)
	}

	// Steps 4, 5 and 6 with no target.
	reviver := newActor(8)
	reviver.Def.CanResurrect = true
	wreck := &ResolvePos{HasFeature: true, IsWreck: true, FeatureResurrectable: true}
	if got := name(reviver, nil, wreck); got != "Resurrect" {
		t.Fatalf("resurrectable wreck = %q, want Resurrect [04 R-ORD-02 §1] code 1 step 4", got)
	}
	if got := name(newActor(9), nil, feature); got != "Reclaim" {
		t.Fatalf("feature = %q, want Reclaim [04 R-ORD-02 §1] code 1 step 5", got)
	}
	// Step 5 carries its own `canreclamate` gate: a unit with no nanolathe does
	// not answer a click on a tree with a reclaim.
	bare := mkUnit(40, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) {
		d.CanAttack = false
		d.CanReclamate = false
		d.CanMove = true
	}))
	bare.Flags &^= units.ArmedStatus
	setTestHostility(bare, func(_, _ *units.Unit) bool { return false })
	if got := name(bare, nil, feature); got != "Move_Ground" {
		t.Fatalf("feature, actor without canreclamate = %q, want Move_Ground"+
			" [04 R-ORD-02 §1] code 1 step 5", got)
	}
	if got := name(newActor(10), nil, nil); got != "Move_Ground" {
		t.Fatalf("open ground = %q, want Move_Ground [04 R-ORD-02 §1] code 1 step 6", got)
	}
}
