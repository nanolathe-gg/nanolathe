//go:build retail

package session

import (
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// These use Alpha 5's compiled scripts, ordinary damage intake, engine ports,
// and callback bridge. Authored contracts are independently described in
// [research/extensions/ta-zero-engine.md "Authored combat abilities and callback contracts"].
// Draining only this unit isolates script behaviour from AI, movement and
// passive repair; these checks do not establish historical DLL equivalence.
func zeroAbilityUnit(t *testing.T, f zeroPackageFixture, key string) (*Session, *units.Unit) {
	t.Helper()
	s := f.enter(t, "ashap plateau", 2)
	if _, ok := f.cat.Unit(key); !ok {
		t.Fatalf("Alpha 5 unit %s is absent", key)
	}
	u := placeCompleteRetailUnit(t, s, key, 0, numeric.FixedFromInt(1000), numeric.FixedFromInt(1000))
	if u.COBBinding() == nil {
		t.Fatalf("%s has no compiled script binding", key)
	}
	zeroAbilityDrain(u, 1000) // Includes the commanders' authored arrival/weapon lock.
	return s, u
}

func zeroAbilityDrain(u *units.Unit, ticks int) {
	for range ticks {
		u.COBBinding().Callbacks.Drain(1)
	}
}

func zeroAbilityHit(t *testing.T, s *Session, u *units.Unit, nominal int32) uint16 {
	t.Helper()
	result := s.Combat.AcceptDamage(s.Units, s.Clock.GlobalTick, combat.DamageInput{
		Victim: u.Handle, Nominal: nominal, Kind: combat.KindOrdinary,
	})
	if !result.Accepted || result.DeathLatched {
		t.Fatal("ability probe did not accept a surviving ordinary hit")
	}
	return result.Amount
}

func zeroAbilityPieceShown(t *testing.T, u *units.Unit, name string) bool {
	t.Helper()
	for i, p := range u.COBBinding().Program.Pieces {
		if strings.EqualFold(p, name) {
			return u.GetScript().SnapshotFlags()[i]&1 != 0
		}
	}
	t.Fatalf("%s has no %s piece", u.Def.UnitName, name)
	return false
}

func TestZeroAlpha5PlasmaShieldCallbacks(t *testing.T) {
	f := loadZeroPackage(t)
	for _, tc := range []struct {
		key            string
		shieldedDamage uint16
		recoverBy      int
	}{
		{"CoreCommander", 50, 70}, {"CoreT2BTank", 50, 40},
		{"CoreT2GF", 50, 40}, {"CoreT2GF_AI", 50, 40},
		{"CoreT2Gunship", 50, 40}, {"CoreT2HDTurret", 50, 40},
		{"CoreT2LasKbot", 50, 40}, {"CoreT2Mex", 50, 70},
		{"CoreT2PGen", 50, 70}, {"CoreT2PGen_AI", 50, 70},
		{"CoreT2Radar", 50, 70}, {"CoreT2Shield", 0, 40}, {"CoreT2Shield2", 0, 40},
	} {
		t.Run(tc.key, func(t *testing.T) {
			s, u := zeroAbilityUnit(t, f, tc.key)
			if tc.key == "CoreT2Radar" {
				if u.Armored {
					t.Fatal("active radar retained its retracted shield")
				}
				u.COBBinding().Callbacks.Deactivate()
				zeroAbilityDrain(u, 1)
			}
			if !u.Armored || zeroAbilityPieceShown(t, u, "shield") {
				t.Fatal("ready shield lacks armour or displays its impact piece")
			}
			before := u.Health
			if got := zeroAbilityHit(t, s, u, 100); got != tc.shieldedDamage || u.Health != before-int32(tc.shieldedDamage) {
				t.Fatalf("ready shield damage = %d, want %d", got, tc.shieldedDamage)
			}
			// Intake scales health before the deferred HitByWeapon consumes this
			// shield, including the generators' zero-damage hit [04 R-CB-01 §3].
			if !u.Armored {
				t.Fatal("deferred hit changed armour before the script drain")
			}
			zeroAbilityDrain(u, 1)
			if strings.Contains(tc.key, "Shield") && !u.YardOpen {
				t.Fatal("generator did not open its shield perimeter on impact")
			}
			if u.Armored || !zeroAbilityPieceShown(t, u, "shield") {
				t.Fatal("hit did not consume the shield and show its impact piece")
			}
			if got := zeroAbilityHit(t, s, u, 100); got != 100 {
				t.Fatalf("unshielded hit = %d, want 100", got)
			}
			zeroAbilityDrain(u, 4)
			if zeroAbilityPieceShown(t, u, "shield") {
				t.Fatal("impact piece outlived its authored sleep")
			}
			zeroAbilityDrain(u, tc.recoverBy)
			if !u.Armored {
				t.Fatal("authored shield did not recharge")
			}
			if strings.Contains(tc.key, "Shield") && u.YardOpen {
				t.Fatal("generator did not close its perimeter before rearming")
			}
			switch tc.key {
			case "CoreT2Gunship":
				u.COBBinding().Callbacks.MoveRate3()
				zeroAbilityDrain(u, 1)
				if u.Armored {
					t.Fatal("Thor retained its shield at its highest movement tier")
				}
				u.COBBinding().Callbacks.MoveRate2()
				zeroAbilityDrain(u, 40)
				if !u.Armored {
					t.Fatal("Thor did not recharge after slowing down")
				}
			case "CoreT2Radar":
				u.COBBinding().Callbacks.Activate()
				zeroAbilityDrain(u, 1)
				if u.Armored {
					t.Fatal("active radar retained its retracted shield")
				}
			case "CoreT2GF", "CoreT2GF_AI":
				u.COBBinding().Callbacks.Activate()
				zeroAbilityDrain(u, 90)
				if u.Armored {
					t.Fatal("opened factory retained its closed shield")
				}
				u.COBBinding().Callbacks.Deactivate()
				zeroAbilityDrain(u, 330)
				if !u.Armored {
					t.Fatal("closed factory did not restore its shield")
				}
			case "CoreT2Shield", "CoreT2Shield2":
				u.COBBinding().Callbacks.Deactivate()
				zeroAbilityDrain(u, 90)
				if u.Armored || !u.YardOpen {
					t.Fatal("disabled generator rearmed or closed its perimeter")
				}
				u.COBBinding().Callbacks.Activate()
				zeroAbilityDrain(u, 90)
				if !u.Armored || u.YardOpen {
					t.Fatal("reactivated generator failed to close and rearm")
				}
			}
			if d := u.GetScript().DebugSnapshot().Diagnostics; len(d) != 0 {
				t.Fatalf("script diagnostics: %v", d)
			}
		})
	}
}

func TestZeroAlpha5AdaptiveArmourCallbacks(t *testing.T) {
	f := loadZeroPackage(t)
	for _, key := range []string{"CoreT2AAGunship", "CoreT2AmpTank", "CoreT2AsKbot", "CoreT2PDTurret"} {
		t.Run(key, func(t *testing.T) {
			s, u := zeroAbilityUnit(t, f, key)
			if key == "CoreT2AmpTank" {
				if !u.Armored {
					t.Fatal("stowed Raider lacks its static armour")
				}
				u.COBBinding().Callbacks.Aim(cob.WeaponPrimary, 0, 0, nil)
				zeroAbilityDrain(u, 1)
			}
			if u.Armored {
				t.Fatal("adaptive armour was active before a hit")
			}
			if got := zeroAbilityHit(t, s, u, 100); got != 100 {
				t.Fatalf("first hit = %d, want 100", got)
			}
			zeroAbilityDrain(u, 8)
			if !u.Armored {
				t.Fatal("controller did not enable adaptive armour after the hit")
			}
			if got := zeroAbilityHit(t, s, u, 100); got != 75 {
				t.Fatalf("adapted hit = %d, want 75", got)
			}
			zeroAbilityDrain(u, 70)
			if u.Armored {
				t.Fatal("adaptive armour did not expire without another hit")
			}
		})
	}
}

func TestZeroAlpha5AntiAirAimHandoff(t *testing.T) {
	f := loadZeroPackage(t)
	for _, key := range []string{
		"ArmT1AAHover", "ArmT1AATurret", "ArmT1FAATurret", "ArmT2AATank", "ArmT2AATurret",
		"CoreT1AAShip", "CoreT1AATank", "CoreT1AATurret", "CoreT1FAATurret", "CoreT2AASpider", "CoreT2AATurret",
		"GoKT1AAHover", "GoKT1AATurret", "GoKT1FAATurret", "GoKT2AALauncher", "GoKT2AARpod",
	} {
		t.Run(key, func(t *testing.T) {
			_, u := zeroAbilityUnit(t, f, key)
			bridge := u.COBBinding().Callbacks
			primary, tertiary := false, false
			bridge.Aim(cob.WeaponPrimary, 0, 0, func(v cob.CallbackReturn) { primary = v.Value != 0 })
			bridge.Aim(cob.WeaponTertiary, 0, 0, func(v cob.CallbackReturn) { tertiary = v.Value != 0 })
			zeroAbilityDrain(u, 1)
			if primary {
				t.Fatal("primary completed before its one-tick handoff wait")
			}
			for i := 0; i < 60 && !tertiary; i++ {
				zeroAbilityDrain(u, 1)
			}
			floating := strings.Contains(key, "FAATurret")
			// Alpha 5 omits the one-tick wait in all three floating
			// turret scripts; both callbacks can grant in that content.
			if primary != floating || !tertiary {
				t.Fatalf("same-visit primary/tertiary grants = %v/%v", primary, tertiary)
			}
			bridge.Fire(cob.WeaponTertiary)
			for i := 0; i < 180 && !primary; i++ {
				zeroAbilityDrain(u, 1)
			}
			if !primary {
				t.Fatal("primary did not resume after the tertiary fired")
			}
		})
	}
}

// Names come from the released authored source; values come from the actual
// compiled VM. This avoids treating the BOS's fractional literals as compiled
// arithmetic (the commander's 12.5 is compiled as integer 12).
func zeroAbilityStatic(t *testing.T, f zeroPackageFixture, u *units.Unit, name string) int32 {
	t.Helper()
	data, err := f.fs.ReadFileLimit("scripts/"+u.Def.UnitName+".bos", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	declaration := regexp.MustCompile(`(?s)static-var\s+([^;]+);`).FindSubmatch(data)
	if len(declaration) != 2 {
		t.Fatalf("%s has no authored static declaration", u.Def.UnitName)
	}
	for i, field := range strings.Split(string(declaration[1]), ",") {
		if strings.EqualFold(strings.TrimSpace(field), name) {
			return u.GetScript().DebugSnapshot().Statics[i]
		}
	}
	t.Fatalf("%s has no authored static %s", u.Def.UnitName, name)
	return 0
}

func TestZeroAlpha5CommanderVoidShieldOvercharge(t *testing.T) {
	f := loadZeroPackage(t)
	s, u := zeroAbilityUnit(t, f, "GoKCommander")
	reserve := func() int32 { return zeroAbilityStatic(t, f, u, "ShieldPower") }
	boost := func() int32 { return zeroAbilityStatic(t, f, u, "ShieldBoost") }
	if reserve() != 1000 || boost() != 0 || !u.Armored {
		t.Fatal("commander did not finish with its full ordinary shield")
	}
	// A 25-point actual hit changes the integer health percentage by three:
	// 36*3 = 108, then the heavy-shield reduction produces 100+(108-100)/2.
	zeroAbilityHit(t, s, u, 100)
	zeroAbilityDrain(u, 1)
	if reserve() != 896 {
		t.Fatalf("post-hit reserve = %d, want 896", reserve())
	}
	zeroAbilityDrain(u, 15)
	if !zeroAbilityPieceShown(t, u, "gema") {
		t.Fatal("upper reserve did not select the upper charge gem")
	}
	if reserve() != 908 {
		t.Fatalf("ordinary recharge = %d, want 908", reserve())
	}
	// Exhaustion is measured from post-hit health percentages. No health is
	// refunded: the reserve changes armour on later accepted packets only.
	zeroAbilityHit(t, s, u, 1200)
	zeroAbilityDrain(u, 1)
	zeroAbilityHit(t, s, u, 1200)
	zeroAbilityDrain(u, 1)
	if u.Armored || reserve() > 0 {
		t.Fatalf("exhausted shield armour/reserve = %v/%d", u.Armored, reserve())
	}
	if !zeroAbilityPieceShown(t, u, "gemd") {
		t.Fatal("exhausted shield did not select the disabled charge gem")
	}
	u.COBBinding().Callbacks.Fire(cob.WeaponTertiary)
	zeroAbilityDrain(u, 1)
	if !u.Armored || reserve() != 200 || boost() != 1 {
		t.Fatal("VSOC did not restore armour and the minimum 200 reserve")
	}
	zeroAbilityHit(t, s, u, 800)
	// Run the new callback before advancing the controller's health-poll
	// sleep; otherwise that poll may replace the preceding health sample.
	u.COBBinding().Callbacks.Drain(0)
	if !u.Armored || reserve() >= 0 || boost() != 1 {
		t.Fatalf("VSOC armour/reserve/boost = %v/%d/%d", u.Armored, reserve(), boost())
	}
	loaded := f.restore(t, s)
	restored := loaded.Session.Units.Unit(loaded.StableUnit[uint16(u.Handle)])
	if restored == nil || restored.Armored != u.Armored || !reflect.DeepEqual(restored.GetScript().DebugSnapshot().Statics, u.GetScript().DebugSnapshot().Statics) {
		t.Fatal("save lost VSOC armour or authored script state")
	}
	for range 200 {
		zeroAbilityDrain(u, 1)
		zeroAbilityDrain(restored, 1)
	}
	if boost() != 0 || !reflect.DeepEqual(restored.GetScript().DebugSnapshot().Statics, u.GetScript().DebugSnapshot().Statics) {
		t.Fatal("VSOC expiry or recharge diverged after save restoration")
	}
}

func TestZeroAlpha5VSOCWeaponPayment(t *testing.T) {
	f := loadZeroPackage(t)
	s, u := zeroAbilityUnit(t, f, "GoKCommander")
	slot := u.SlotAt(2)
	if slot.Weapon == nil || slot.Weapon.Burst != 24 || slot.Weapon.ReloadTime != 900 || slot.Weapon.EnergyPerShot != 1000 || !slot.Weapon.Paralyzer {
		t.Fatal("VSOC lost its authored weapon contract")
	}
	target := placeCompleteRetailUnit(t, s, "ArmT1InfKbot", 1, u.X, u.Z.Add(numeric.FixedFromInt(40)))
	health := target.Health
	slot.Target = units.Target{Kind: units.TargetGround, X: target.X, Z: target.Z}
	slot.Flags |= units.SlotFlagEnabled
	firedCallbacks := 0
	u.COBBinding().Callbacks.SetLifecycleSink(func(e cob.LifecycleEvent) {
		if e.Name == "FireTertiary" && e.Phase == "start" {
			firedCallbacks++
		}
	})
	step := func(tick uint32) int {
		sum := s.Combat.StepWeaponsForUnit(u, tick, s.Units, s.Vis, s.World, s.Econ, s.Catalog, s.SimRNG(), s.CrtRNG())
		zeroAbilityDrain(u, 1)
		return sum.Fired
	}
	s.Econ.Players[0].Stock[economy.Energy] = 999
	for tick := uint32(1); tick <= 5; tick++ {
		if step(tick) != 0 {
			t.Fatal("VSOC fired without its full energy cost")
		}
	}
	if firedCallbacks != 0 || zeroAbilityStatic(t, f, u, "ShieldBoost") != 0 {
		t.Fatal("unpaid VSOC invoked its ability callback")
	}
	s.Econ.Players[0].Stock[economy.Energy] = 1000
	fired := 0
	for tick := uint32(6); tick <= 10 && fired == 0; tick++ {
		fired += step(tick)
	}
	if fired != 1 || firedCallbacks != 1 || s.Econ.Players[0].Stock[economy.Energy] != 0 || zeroAbilityStatic(t, f, u, "ShieldBoost") != 1 {
		t.Fatalf("VSOC shot/callbacks/energy/boost = %d/%d/%v/%d", fired, firedCallbacks, s.Econ.Players[0].Stock[economy.Energy], zeroAbilityStatic(t, f, u, "ShieldBoost"))
	}
	// The real root and its burst clones pass through the ordinary projectile
	// driver and area-paralyzer intake; the target's order row raises the stun.
	impacts := 0
	oldEvents := s.Combat.Events
	s.Combat.Events = func(e combat.Event) {
		if oldEvents != nil {
			oldEvents(e)
		}
		if e.Kind == combat.EventExplosion && strings.EqualFold(e.Graphic, "Weapon_VSOC1") {
			impacts++
		}
	}
	stunned := false
	for tick := uint32(11); tick <= 210; tick++ {
		s.Combat.TickProjectiles(tick, s.Units, s.World, s.Wind, s.Features, s.Vis, s.Econ, s.Catalog, s.SimRNG(), s.CrtRNG())
		orders.QueueForUnit(target).Pump(target, tick)
		stunned = stunned || target.Stunned
		zeroAbilityDrain(u, 1)
	}
	if !stunned || target.Health != health || impacts != int(slot.Weapon.Burst) {
		t.Fatalf("VSOC contact stun/health/impacts = %v/%d/%d", stunned, target.Health, impacts)
	}
	if firedCallbacks != 1 || s.Econ.Players[0].Stock[economy.Energy] != 0 {
		t.Fatal("VSOC burst clones repeated the root callback or payment")
	}
}

func TestZeroAlpha5VoidShieldFormulaFamilies(t *testing.T) {
	f := loadZeroPackage(t)
	// Distinct authored arithmetic, with thresholds measured by integer
	// post-hit health percentage. This is a representative set, not all units.
	for _, tc := range []struct {
		key                      string
		nominal                  int32
		damage                   uint16
		capacity, loss, recharge int32
	}{
		{"GoKT1AAHover", 40, 10, 100, 28, 2},
		{"GoKT2ATRpod", 400, 100, 600, 215, 15},
		{"GoKT3SupRpod", 400, 100, 3000, 225, 37},
		{"GoKT1SNode", 100, 4, 300, 80, 3},
	} {
		t.Run(tc.key, func(t *testing.T) {
			s, u := zeroAbilityUnit(t, f, tc.key)
			power := func() int32 { return zeroAbilityStatic(t, f, u, "ShieldPower") }
			if !u.Armored || power() != tc.capacity {
				t.Fatal("representative shield did not initialize")
			}
			if got := zeroAbilityHit(t, s, u, tc.nominal); got != tc.damage {
				t.Fatalf("shield damage %d, want %d", got, tc.damage)
			}
			zeroAbilityDrain(u, 1)
			if power() != tc.capacity-tc.loss {
				t.Fatalf("reserve %d, want %d", power(), tc.capacity-tc.loss)
			}
			zeroAbilityDrain(u, 15)
			if power() != tc.capacity-tc.loss+tc.recharge {
				t.Fatalf("recharged reserve %d", power())
			}
		})
	}
	for _, key := range []string{"GoKT2Shield", "GoKT2Shield2"} {
		t.Run(key, func(t *testing.T) {
			s, u := zeroAbilityUnit(t, f, key)
			power := func() int32 { return zeroAbilityStatic(t, f, u, "ShieldPower") }
			if !u.Armored || power() != 40 {
				t.Fatal("extended void shield did not initialize")
			}
			if zeroAbilityHit(t, s, u, 100) != 0 {
				t.Fatal("extended void shield passed damage")
			}
			zeroAbilityDrain(u, 1)
			if !u.Armored || power() != 38 || u.YardOpen {
				t.Fatal("one hit did not spend two reserve while keeping the perimeter")
			}
			for i := 0; i < 30 && u.Armored; i++ {
				zeroAbilityHit(t, s, u, 100)
				zeroAbilityDrain(u, 3)
			}
			if u.Armored || !u.YardOpen {
				t.Fatal("exhausted extended void shield did not open its perimeter")
			}
			zeroAbilityDrain(u, 330)
			if !u.Armored || u.YardOpen || power() < 20 {
				t.Fatal("extended void shield failed its reserve and yard reactivation")
			}
		})
	}
}

func TestZeroAlpha5ExtendedShieldProjectileContact(t *testing.T) {
	f := loadZeroPackage(t)
	for _, key := range []string{"CoreT2Shield", "CoreT2Shield2", "GoKT2Shield", "GoKT2Shield2"} {
		t.Run(key, func(t *testing.T) {
			s, u := zeroAbilityUnit(t, f, key)
			weapon, ok := s.Catalog.Weapon("Arm_MarEMG")
			if !ok || !weapon.LineOfSight || weapon.AreaOfEffect != 0 {
				t.Fatal("expected the authored Marine's direct-contact projectile")
			}
			// Place a projectile at an outer occupied cell, away from the
			// generator centre. This isolates contact with the authored yard
			// perimeter from aiming, projectile travel, and area damage.
			cx, cz := int32(u.X.Int())/16, int32(u.Z.Int())/16
			var point combat.Vec3
			found := false
			for dx := int32(-20); dx < -4 && !found; dx++ {
				cell := s.World.PlotAt(cx+dx, cz)
				if cell == nil || cell.OccupantA() != int16(u.Handle) {
					continue
				}
				point = combat.Vec3{
					X: numeric.FixedFromInt(int64((cx+dx)*16 + 8)),
					Y: u.Y.Add(numeric.Fixed(u.Def.ModelTopFixed / 2)),
					Z: numeric.FixedFromInt(int64(cz*16 + 8)),
				}
				found = point.Y > s.World.HeightAt(point.X, point.Z)
			}
			if !found || !u.Armored || u.YardOpen {
				t.Fatal("shield has no ready outer perimeter contact cell")
			}
			hits := 0
			u.COBBinding().Callbacks.SetLifecycleSink(func(e cob.LifecycleEvent) {
				if e.Name == "HitByWeapon" && e.Phase == "start" {
					hits++
				}
			})
			shoot := func(tick uint32) {
				t.Helper()
				h, ok := s.Combat.Reserve()
				if !ok {
					t.Fatal("reserve contact projectile")
				}
				p := &s.Combat.Records[int(h)-1]
				*p = combat.Projectile{WeaponID: weapon.ID, ShooterSide: 1, Pos: point, StartPos: point, ExpiryTick: tick + 10}
				p.Velocity.X = numeric.FixedFromInt(1)
				s.Combat.TickProjectiles(tick, s.Units, s.World, s.Wind, s.Features, s.Vis, s.Econ, s.Catalog, s.SimRNG(), s.CrtRNG())
				zeroAbilityDrain(u, 1)
			}
			health := u.Health
			shoot(1)
			if s.Combat.Count() != 0 || hits != 1 || u.Health != health {
				t.Fatalf("closed perimeter projectile/hit/health = %d/%d/%d", s.Combat.Count(), hits, u.Health)
			}
			u.COBBinding().Callbacks.Deactivate()
			zeroAbilityDrain(u, 200)
			if !u.YardOpen || u.Armored {
				t.Fatal("deactivation did not release the shield perimeter")
			}
			shoot(2)
			if s.Combat.Count() != 1 || hits != 1 || u.Health != health {
				t.Fatalf("open perimeter projectile/hit/health = %d/%d/%d", s.Combat.Count(), hits, u.Health)
			}
		})
	}
}
