package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// TestShotTimeGateHasNoTargetSideClause locks [06 R-WPN-05 §9]: the shot-time
// gate is exactly three clauses — range, the shooter-side sea-level clause for
// a non-water weapon, and the ballistic sentinel — and carries NO target-side
// clause of any kind. The merged form this file's fixture exercises used to
// refuse a target below sea level and a non-flying target of a `toairweapon`;
// both belong to the acquisition/order-installation gate alone [06 §3.1].
func TestShotTimeGateHasNoTargetSideClause(t *testing.T) {
	terrain := &world.Terrain{CellW: 64, CellH: 64, SeaLevel: 50}
	def := &content.UnitDef{UnitName: "turret", ModelTop: 20}
	shooter := &units.Unit{Def: def, X: 0, Y: numeric.FixedFromInt(40), Z: 0}

	// A target point 30 world units below sea level, and a shooter whose own
	// top (40 + 20 = 60) clears it. The gate admits: the target's Y is only an
	// operand of the ballistic clause, never a clause of its own.
	drowned := Vec3{X: numeric.FixedFromInt(50), Y: numeric.FixedFromInt(20), Z: 0}
	direct := &content.WeaponDef{Range: 100, LineOfSight: true}
	if !checkAdmission(shooter, direct, drowned, terrain) {
		t.Fatal("the shot-time gate applied a target-side sea-level clause [06 R-WPN-05 §9]")
	}
	// A ground/point target carries no Y at all, which the old merged form
	// read as "at sea level" and refused for every non-water weapon.
	if !checkAdmission(shooter, direct, Vec3{X: numeric.FixedFromInt(50)}, terrain) {
		t.Fatal("a point target was refused by a target-side clause [06 R-WPN-05 §9]")
	}
	// A `toairweapon` against a point admits too: the mover-mode test is the
	// installation gate's [06 R-WPN-05 §1] clause 3.
	toAir := &content.WeaponDef{Range: 100, LineOfSight: true, ToAirWeapon: true}
	if !checkAdmission(shooter, toAir, drowned, terrain) {
		t.Fatal("the shot-time gate applied the toairweapon mover-mode clause [06 R-WPN-05 §9]")
	}

	// Clause 2 is the shooter's whole-unit Y word PLUS its model top height,
	// strictly greater than the sea-level byte. 35 + 20 = 55 > 50 admits;
	// 30 + 20 = 50 is not strictly greater and refuses.
	shooter.Y = numeric.FixedFromInt(35)
	if !checkAdmission(shooter, direct, drowned, terrain) {
		t.Fatal("the shooter clause dropped the model-top addend [06 R-WPN-05 §9]")
	}
	shooter.Y = numeric.FixedFromInt(30)
	if checkAdmission(shooter, direct, drowned, terrain) {
		t.Fatal("the shooter clause is strictly greater than sea level [06 R-WPN-05 §9]")
	}
	// A water weapon stops after clause 1 and admits the same submerged shooter.
	water := &content.WeaponDef{Range: 100, LineOfSight: true, WaterWeapon: true}
	if !checkAdmission(shooter, water, drowned, terrain) {
		t.Fatal("a water weapon ran the shooter-side sea-level clause [06 R-WPN-05 §9]")
	}
}

// TestShotTimeRangeIsInclusiveAtEquality locks clause 1's comparison: the sum
// of the two truncated squared axis terms is admitted when it EQUALS range²
// [06 §3.3][06 R-WPN-05 §9].
func TestShotTimeRangeIsInclusiveAtEquality(t *testing.T) {
	terrain := &world.Terrain{CellW: 64, CellH: 64, SeaLevel: 0}
	shooter := &units.Unit{Def: &content.UnitDef{UnitName: "turret"}, Y: numeric.FixedFromInt(10)}
	weapon := &content.WeaponDef{Range: 100, LineOfSight: true}

	at := Vec3{X: numeric.FixedFromInt(100)}
	if !checkAdmission(shooter, weapon, at, terrain) {
		t.Fatal("a target at exactly range must be admitted — the compare is inclusive [06 §3.3]")
	}
	past := Vec3{X: numeric.FixedFromInt(101)}
	if checkAdmission(shooter, weapon, past, terrain) {
		t.Fatal("a target one world unit past range was admitted")
	}
}

// TestPointTargetFiresThroughTheShotTimeGate is the same fact end to end: a
// slot holding a GROUND target fires. Nothing else changed in the pipeline; the
// old merged gate refused every such shot because a point target's Y is zero
// and zero is never above a sea level of zero.
func TestPointTargetFiresThroughTheShotTimeGate(t *testing.T) {
	w, terrain, shooter, _ := newTestWorldAndUnits(t)
	weapon := &content.WeaponDef{
		ID: 41, Range: 1000, LineOfSight: true,
		WeaponVelocity: 100 * 65536 / 30, Tolerance: wideDriftTolerance,
	}
	shooter.InstallWeapon(0, weapon)
	slot := shooter.SlotAt(0)
	slot.Target = units.Target{Kind: units.TargetGround, X: numeric.FixedFromInt(40), Z: numeric.FixedFromInt(10)}
	cat := &content.Catalog{Weapons: map[string]*content.WeaponDef{"w": weapon}}
	cat.RebuildWeaponIndex()

	var svc Service
	if sum := svc.StepWeaponsForUnit(shooter, 1, w, nil, terrain, nil, cat, nil, nil); sum.Fired != 1 {
		t.Fatalf("a ground target inside range must fire, fired %d [06 R-WPN-05 §9]", sum.Fired)
	}
}

// TestAutonomousScanRequiresTheSlotAutonomyBit locks the per-slot clause of
// [06 §3.2] the scan never read: a slot is skipped unless its enabled flag and
// its tracking flag are both set, and the tracking flag is bit 4 of the slot
// control byte — the bit the *release slot* verb clears when an order takes the
// slot for its own target [04 R-UNIT-06 §5 part 3].
func TestAutonomousScanRequiresTheSlotAutonomyBit(t *testing.T) {
	for _, tc := range []struct {
		name        string
		autonomous  bool
		wantAcquire bool
	}{
		{"a slot left to autonomy acquires", true, true},
		{"a slot an order took acquires nothing", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, shooter, w, terrain, cat := commandFireProbe(t, false, ControlByteHuman)
			if !tc.autonomous {
				shooter.SlotAt(0).Flags &^= units.SlotFlagAutonomous
			}
			if got := runProbeVisits(svc, shooter, w, terrain, cat, simRNGPtr(1)); got != tc.wantAcquire {
				t.Fatalf("after %d slot visits acquired=%v, want %v [06 §3.2]", visitsPerProbe, got, tc.wantAcquire)
			}
		})
	}
}

// TestRetentionDropsAlliedAndBadMaskTargets locks the two retention drops of
// [06 §3.2] the scan was missing: the current slot target is dropped when its
// owning player is allied to the scanning player, and when its definition index
// is in the slot's bad-target mask. Retention is stricter than acquisition,
// which merely buckets a masked candidate as fallback [06 §3.1].
//
// Both are the autonomous scan's, so both are gated on the slot's autonomy bit:
// a slot an attack order holds keeps the target that order installed.
func TestRetentionDropsAlliedAndBadMaskTargets(t *testing.T) {
	// One shared bit, used both as the target's definition identity and as the
	// slot's bad-target mask; the fixture's two units share one definition.
	var oneBit content.CategoryMask
	oneBit.Words[0] = 1 << 3

	setup := func(t *testing.T) (*Service, *units.World, *world.Terrain, *content.Catalog, *units.Unit, *units.Unit) {
		t.Helper()
		w, terrain, shooter, target := newTestWorldAndUnits(t)
		weapon := &content.WeaponDef{ID: 51, Range: 1000, LineOfSight: true, WeaponVelocity: 100 * 65536 / 30}
		shooter.InstallWeapon(0, weapon)
		shooter.SlotAt(0).Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
		cat := &content.Catalog{Weapons: map[string]*content.WeaponDef{"w": weapon}}
		cat.RebuildWeaponIndex()
		return &Service{}, w, terrain, cat, shooter, target
	}

	t.Run("an allied owner drops the target", func(t *testing.T) {
		svc, w, terrain, cat, shooter, _ := setup(t)
		econ := &economy.Service{}
		econ.Players[shooter.Owner].Allies[1] = true // the scanning owner's row
		svc.StepAutonomousForPlayer(shooter.Owner, w, nil, terrain, econ, cat, nil)
		if shooter.SlotAt(0).Target.Kind != units.TargetNone {
			t.Fatal("a target whose owner is now allied must be dropped at retention [06 §3.2]")
		}
	})

	t.Run("the target's reciprocal declaration does not drop it", func(t *testing.T) {
		svc, w, terrain, cat, shooter, target := setup(t)
		econ := &economy.Service{}
		econ.Players[target.Owner].Allies[shooter.Owner] = true
		svc.StepAutonomousForPlayer(shooter.Owner, w, nil, terrain, econ, cat, nil)
		if got := shooter.SlotAt(0).Target; got.Kind != units.TargetUnit || got.Unit != target.Handle {
			t.Fatal("retention read the target owner's reciprocal declaration [06 §3.2]")
		}
	})

	t.Run("the bad-target mask drops the target", func(t *testing.T) {
		svc, w, terrain, cat, shooter, target := setup(t)
		target.Def.UnitMask = oneBit
		shooter.Def.BadTargetCategoryWPRIMask = oneBit
		svc.StepAutonomousForPlayer(shooter.Owner, w, nil, terrain, nil, cat, nil)
		if shooter.SlotAt(0).Target.Kind != units.TargetNone {
			t.Fatal("a target in the slot's bad-target mask must be dropped at retention [06 §3.2]")
		}
	})

	t.Run("a slot an order holds keeps its target", func(t *testing.T) {
		svc, w, terrain, cat, shooter, target := setup(t)
		target.Def.UnitMask = oneBit
		shooter.Def.BadTargetCategoryWPRIMask = oneBit
		econ := &economy.Service{}
		econ.Players[shooter.Owner].Allies[1] = true
		shooter.SlotAt(0).Flags &^= units.SlotFlagAutonomous
		svc.StepAutonomousForPlayer(shooter.Owner, w, nil, terrain, econ, cat, nil)
		if shooter.SlotAt(0).Target.Kind != units.TargetUnit {
			t.Fatal("the retention drops belong to the autonomous scan and must not touch an order's slot [06 §3.2]")
		}
	})

	t.Run("a stale target is dropped on any slot", func(t *testing.T) {
		svc, w, terrain, cat, shooter, target := setup(t)
		shooter.SlotAt(0).Flags &^= units.SlotFlagAutonomous
		target.Alive = false
		svc.StepWeaponsForUnit(shooter, 1, w, nil, terrain, nil, cat, nil, nil)
		if shooter.SlotAt(0).Target.Kind != units.TargetNone {
			t.Fatal("stale/dead resolution is the pipeline's, not the scan's [06 §3.2]")
		}
	})
}
