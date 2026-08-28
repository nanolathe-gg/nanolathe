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
	return u
}

func TestResolveFullTable(t *testing.T) {
	// P0-I16: per-queue hostility
	SetHostilityFunc(nil)
	defer SetHostilityFunc(nil)

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
	SetHostilityFunc(func(a, b *units.Unit) bool { return a.Def.Side != b.Def.Side })
	assertResolve(1, hostileTarget, nil, "Attack_Chase")
	friendlyDamaged := mkUnit(3, 0, "ARM", 50, 100, true, 0, mkDef(nil))
	SetHostilityFunc(func(a, b *units.Unit) bool { return false })
	assertResolve(1, friendlyDamaged, nil, "RepairUnit")
	transportable := mkUnit(4, 1, "CORE", 100, 100, true, 0, mkDef(func(d *content.UnitDef) {
		d.CantBeTransported = false
	}))
	actorNoAttack := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) {
		d.CanMove = true
		d.CanAttack = false
		d.Builder = false
	}))
	SetHostilityFunc(func(a, b *units.Unit) bool { return true })
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
	SetHostilityFunc(nil)
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
	SetHostilityFunc(nil)
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
	SetHostilityFunc(nil)

	actorNoMove := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) { d.CanMove = false }))
	assertRejectForActor := func(code int, a *units.Unit, tgt *units.Unit) {
		if id := Resolve(code, a, tgt, nil); id != 0 {
			t.Fatalf("code %d with no-move actor should reject got %q", code, DescriptorFor(id).Name)
		}
	}
	assertRejectForActor(2, actorNoMove, nil)
	dead := mkUnit(6, 1, "CORE", 0, 100, false, 0, mkDef(nil))
	id = Resolve(2, actor, dead, nil)
	if got := DescriptorFor(id).Name; got != "QMove" {
		t.Fatalf("code2 dead want QMove got %q", got)
	}
	SetHostilityFunc(func(a, b *units.Unit) bool { return true })
	actorCapture := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) {
		d.CanMove = true
		d.CanCapture = true
		d.CanReclamate = false
	}))
	id = Resolve(2, actorCapture, hostileTarget, nil)
	if got := DescriptorFor(id).Name; got != "Capture" {
		t.Fatalf("code2 hostile capture want Capture got %q", got)
	}
	SetHostilityFunc(func(a, b *units.Unit) bool { return false })
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
	SetHostilityFunc(func(a, b *units.Unit) bool { return false })
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
	SetHostilityFunc(nil)

	actorNoAttack = mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) { d.CanAttack = false }))
	assertRejectForActor(3, actorNoAttack, hostileTarget)
	actorKamikaze := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) {
		d.CanAttack = true
		d.Kamikaze = true
		d.CanFly = false
	}))
	targetGround := mkUnit(11, 1, "CORE", 100, 100, true, 0, mkDef(func(d *content.UnitDef) { d.CanMove = true; d.CanFly = false }))
	id = Resolve(3, actorKamikaze, targetGround, nil)
	if got := DescriptorFor(id).Name; got != "Attack_Kamikaze" {
		t.Fatalf("code3 kamikaze want Attack_Kamikaze got %q", got)
	}
	actorNormal := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) { d.CanAttack = true; d.CanFly = false; d.Kamikaze = false }))
	structure := mkUnit(12, 1, "CORE", 100, 100, true, 0, mkDef(func(d *content.UnitDef) { d.CanMove = false; d.CanFly = false }))
	id = Resolve(3, actorNormal, structure, nil)
	if got := DescriptorFor(id).Name; got != "Attack_NoMove" {
		t.Fatalf("code3 structure want Attack_NoMove got %q", got)
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
	actorSuppress := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) {
		d.CanAttack = true
		d.CanFly = false
		d.Weapon1 = ""
		d.Unknown = map[string]string{"suppress": "1"}
	}))
	targetSuppress := mkUnit(17, 1, "CORE", 100, 100, true, 0, mkDef(func(d *content.UnitDef) { d.CanFly = false; d.CanMove = true }))
	id = Resolve(3, actorSuppress, targetSuppress, nil)
	if got := DescriptorFor(id).Name; got != "Suppress" {
		t.Fatalf("code3 suppress want Suppress got %q", got)
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
	SetHostilityFunc(func(a, b *units.Unit) bool { return false })
	id = Resolve(7, actor, friendly, nil)
	if got := DescriptorFor(id).Name; got != "Follow_Ground" {
		t.Fatalf("code7 follow ground want Follow_Ground got %q", got)
	}
	actorGuardVTOL := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) { d.CanGuard = true; d.CanFly = true }))
	id = Resolve(7, actorGuardVTOL, friendly, nil)
	if got := DescriptorFor(id).Name; got != "VTOL_Follow" {
		t.Fatalf("code7 vtol follow want VTOL_Follow got %q", got)
	}
	SetHostilityFunc(func(a, b *units.Unit) bool { return true })
	if id := Resolve(7, actor, friendly, nil); id != 0 {
		t.Fatalf("code7 hostile should reject")
	}
	SetHostilityFunc(nil)

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
	id = Resolve(9, actor, nil, nil)
	if got := DescriptorFor(id).Name; got != "QPatrol" {
		t.Fatalf("code9 no target want QPatrol got %q", got)
	}
	id = Resolve(9, actor, friendly, nil)
	if got := DescriptorFor(id).Name; got != "RepairPatrol" {
		t.Fatalf("code9 builder repair patrol want RepairPatrol got %q", got)
	}
	actorPatrolNonBuilder := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) { d.CanPatrol = true; d.Builder = false; d.CanFly = false }))
	id = Resolve(9, actorPatrolNonBuilder, friendly, nil)
	if got := DescriptorFor(id).Name; got != "Patrol" {
		t.Fatalf("code9 patrol want Patrol got %q", got)
	}
	actorPatrolVTOL := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) { d.CanPatrol = true; d.Builder = false; d.CanFly = true }))
	id = Resolve(9, actorPatrolVTOL, friendly, nil)
	if got := DescriptorFor(id).Name; got != "VTOL_Patrol" {
		t.Fatalf("code9 vtol patrol want VTOL_Patrol got %q", got)
	}
	actorPatrolBuilderVTOL := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) { d.CanPatrol = true; d.Builder = true; d.CanFly = true }))
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
	SetHostilityFunc(func(a, b *units.Unit) bool { return false })
	if id := Resolve(13, actor, hostileTarget, nil); id != 0 {
		t.Fatalf("code13 friendly should reject")
	}
	SetHostilityFunc(func(a, b *units.Unit) bool { return true })
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
	SetHostilityFunc(nil)

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

func TestAttackChaseOrbit(t *testing.T) {
	actor := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) {
		d.CanMove = true
		d.CanFly = false
		d.CanAttack = true
	}))
	actor.X = numeric.Fixed(0)
	actor.Y = numeric.Fixed(0)
	actor.Z = numeric.Fixed(0)
	targetUnit := mkUnit(2, 1, "CORE", 100, 100, true, 0, mkDef(nil))
	targetUnit.Y = numeric.Fixed(0)
	BindTargetLookup(func(h pool.Handle) *units.Unit {
		if h == 99 {
			return targetUnit
		}
		return nil
	})
	defer BindTargetLookup(nil)

	n := &Node{
		ID:          Lookup("Attack_Chase"),
		Phase:       2,
		Param1:      0,
		Param2:      0,
		Param3:      0,
		Target:      99,
		GuardX:      0,
		GuardY:      0,
		Owner:       1,
		DynamicGate: 0,
	}
	for expected := uint32(0); expected <= 8; expected++ {
		if n.Param2 != expected {
			t.Fatalf("orbit substate want %d got %d before handler", expected, n.Param2)
		}
		code := attackChaseHandler(actor, n, 0)
		if code == Code(7) {
			t.Fatalf("substate %d should not cancel", expected)
		}
		// The orbit cadence is unestablished (TODO(question) in the handler);
		// it waits like its neighbours rather than returning Code(2), which
		// would re-dispatch the same head forever.
		if code != Code(3) {
			t.Fatalf("orbit expected Code(3) got %d", code)
		}
	}
	if n.Param2 != 0 {
		t.Fatalf("after 0..8 walk, substate should wrap to 0 got %d", n.Param2)
	}
	if n.GoalX.Raw() == 0 && n.GoalZ.Raw() == 0 {
		t.Fatalf("orbit should set goal")
	}
	n.Param2 = 9
	code := attackChaseHandler(actor, n, 0)
	if code != Code(7) {
		t.Fatalf("substate >=9 should cancel-all Code(7) got %d", code)
	}
	n.Param2 = 0
	n.Phase = 4
	code = attackChaseHandler(actor, n, 0)
	if code != Code(7) {
		t.Fatalf("phase >3 should cancel-all got %d", code)
	}
	n2 := &Node{ID: Lookup("Attack_Chase"), Phase: 0, Target: 0}
	code = attackChaseHandler(actor, n2, 0)
	if code != Code(5) {
		t.Fatalf("missing target should abandon Code(5) got %d", code)
	}
	n3 := &Node{ID: Lookup("Attack_Chase"), Phase: 0, Target: 99}
	code = attackChaseHandler(actor, n3, chaseAbandonMask)
	if code != Code(5) {
		t.Fatalf("abandon satisfied should return 5 got %d", code)
	}
	n4 := &Node{
		ID: Lookup("Attack_Chase"), Phase: 0, Target: 99, Param3: 10, GuardX: 0, GuardY: 0,
	}
	actorFar := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(nil))
	actorFar.X = numeric.Fixed(100 * 65536)
	actorFar.Z = numeric.Fixed(0)
	code = attackChaseHandler(actorFar, n4, 0)
	if code != Code(5) {
		t.Fatalf("leash exceeded should abandon got %d", code)
	}
	actorAir := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) { d.CanFly = true }))
	n5 := &Node{ID: Lookup("Attack_Chase"), Phase: 0, Target: 99}
	code = attackChaseHandler(actorAir, n5, 0)
	if code != Code(5) {
		t.Fatalf("admit requiring ground unit with CanFly should abandon got %d", code)
	}
	actorGround := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) { d.CanFly = false }))
	n6 := &Node{ID: Lookup("Attack_Chase"), Phase: 0, Target: 99, Param1: 0}
	n6.GoalX = numeric.Fixed(999)
	code = attackChaseHandler(actorGround, n6, 0)
	if code != Code(1) || n6.Phase != 1 {
		t.Fatalf("phase 0 admit should advance to 1 got code %d phase %d", code, n6.Phase)
	}
	if n6.GoalX.Raw() != actorGround.X.Raw() {
		t.Fatalf("admit should reset goal to own position")
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
	BindTargetLookup(func(h pool.Handle) *units.Unit {
		if h == 20 {
			return ward
		}
		return nil
	})
	defer BindTargetLookup(nil)
	SetHostilityFunc(func(a, b *units.Unit) bool { return false })
	defer SetHostilityFunc(nil)

	n := &Node{
		ID:     Lookup("Follow_Ground"),
		Target: 20,
		Param1: 30,
		Owner:  10,
	}

	q := QueueForUnit(actor)
	q.primary = nil
	q.secondary = nil
	// First call should pick (a) build assist – top priority (ward unfinished, friendly)
	code := guardHandler(actor, n, 0)
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
	code = guardHandler(actor, n, 0)
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
	code = guardHandler(actor, n, 0)
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
	// Fourth: (a)(c)(d) deduped => (e) follow maintenance => sets banded goal and waits
	q.primary = nil
	n.GoalX = numeric.Fixed(0)
	n.GoalZ = numeric.Fixed(0)
	code = guardHandler(actor, n, 0)
	if code != Code(3) {
		t.Fatalf("(e) fallback should return 3 got %d", code)
	}
	if n.GoalX.Raw() == 0 && n.GoalZ.Raw() == 0 {
		t.Fatalf("(e) should refresh banded goal")
	}
	if len(q.primary) != 0 {
		t.Fatalf("(e) should not enqueue new order, got %d", len(q.primary))
	}

	// Verify top-down order: if (a) not deduped, it wins even when lower conditions also true
	actor.GuardLatches.BuildAssist = [units.GuardLatchSize]pool.Handle{}
	q.primary = nil
	code = guardHandler(actor, n, 0)
	if code != Code(3) || len(q.primary) == 0 || DescriptorFor(q.primary[0].ID).Name != "HelpBuild" {
		t.Fatalf("top-down: (a) should win when not deduped")
	}

	// Handle 0 early return: slot 0 is null [01 §6.1], no assist paths fire, only (e) maintenance
	actorZero := mkUnit(0, 0, "ARM", 100, 100, true, 0, mkDef(func(d *content.UnitDef) {
		d.Builder = true
		d.CanFly = false
	}))
	actorZero.Handle = 0
	BindQueue(actorZero, &Queue{})
	// ward still 20, unfinished/damaged so (a) and (c) would be true, but Handle 0 should skip them
	n2 := &Node{ID: Lookup("Follow_Ground"), Target: 20, Param1: 30, Owner: 0}
	n2.GoalX = numeric.Fixed(0)
	q2 := QueueForUnit(actorZero)
	q2.primary = nil
	code = guardHandler(actorZero, n2, 0)
	if code != Code(3) {
		t.Fatalf("Handle 0 should still return Code(3) via (e) got %d", code)
	}
	if n2.GoalX.Raw() == 0 {
		t.Fatalf("Handle 0 should still refresh banded goal via (e)")
	}
	if len(q2.primary) != 0 {
		t.Fatalf("Handle 0 should not enqueue assist, only maintenance")
	}
}

func TestGuardHandlerMissingWard(t *testing.T) {
	actor := mkUnit(1, 0, "ARM", 100, 100, true, 0, mkDef(nil))
	n := &Node{ID: Lookup("Follow_Ground"), Target: 0}
	code := guardHandler(actor, n, 0)
	if code != Code(5) {
		t.Fatalf("missing ward should abandon code 5 got %d", code)
	}
	BindTargetLookup(func(h pool.Handle) *units.Unit { return nil })
	defer BindTargetLookup(nil)
	n.Target = 99
	code = guardHandler(actor, n, 0)
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
