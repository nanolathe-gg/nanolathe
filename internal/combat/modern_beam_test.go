package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport/retailcat"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func beamTestTerrain() *world.Terrain {
	terrain := &world.Terrain{CellW: 160, CellH: 64}
	terrain.Plot = make([]world.PlotCell, int(terrain.CellW*terrain.CellH))
	for i := range terrain.Plot {
		terrain.Plot[i].SetFeature(world.PlotFeatureNone)
	}
	return terrain
}

func stampBeamTarget(terrain *world.Terrain, target *units.Unit) {
	ax, az := beamAnchor(target.X, target.Z, int32(target.Def.FootprintX), int32(target.Def.FootprintZ))
	stampGroundRect(terrain, ax, az, int32(target.Def.FootprintX), int32(target.Def.FootprintZ), target.Handle)
}

func beamFixture(t *testing.T) (*Service, *units.World, *world.Terrain, *units.Unit, *units.Unit, *content.WeaponDef) {
	s, w, _, shooter, target, weapon := modernCombatFixture(t)
	terrain := beamTestTerrain()
	weapon.BeamWeapon, weapon.Accuracy, weapon.DamageDefault = true, 0, 2500
	weapon.WeaponVelocity, weapon.Range = 2184533, 2000
	target.Def = &content.UnitDef{UnitName: "mobile", BMCode: 1, FootprintX: 2, FootprintZ: 2, ModelTopFixed: 16 << 16, DamageModifier: 65536}
	target.Move.Mode = 1
	shooter.X, shooter.Y, shooter.Z = numeric.FixedFromInt(128), numeric.FixedFromInt(4), numeric.FixedFromInt(256)
	target.X, target.Y, target.Z = numeric.FixedFromInt(928), 0, shooter.Z
	stampBeamTarget(terrain, target)
	return s, w, terrain, shooter, target, weapon
}

func TestModernBeamLongMobileCoverageAndRevalidation(t *testing.T) {
	for _, moving := range []bool{false, true} {
		s, w, terrain, shooter, target, weapon := beamFixture(t)
		if moving {
			target.Move.Speed, target.Move.VelZ = numeric.FixedFromInt(2), numeric.FixedFromInt(2)
			target.Move.Heading = 32768
		}
		aim := Vec3{X: target.X, Y: numeric.FixedFromInt(4), Z: target.Z}
		if moving {
			aim.Z += numeric.FixedFromInt(48)
		}
		p := Projectile{Shooter: shooter.Handle, ShooterSide: shooter.Owner}
		InitOrdinary(&p, weapon, 10, Vec3{shooter.X, shooter.Y, shooter.Z}, aim, target.Handle)
		eta := s.reliableETA(p, weapon, target, w, terrain, 10)
		if eta <= 6 || eta > modernBeamHorizon {
			t.Fatalf("moving=%v ETA=%d", moving, eta)
		}
		h, ok := s.Reserve()
		if !ok {
			t.Fatal("reserve")
		}
		s.Records[int(h)-1] = p
		s.incoming[int(h)-1] = incomingShot{target: target, shooter: shooter, weapon: weapon, motion: beamMotion(target)}
		if got := s.incomingDamage(shooter, target, w, terrain, 10, 60); got < int64(target.Health) {
			t.Fatalf("moving=%v coverage=%d", moving, got)
		}
		if got := s.incomingDamage(shooter, target, w, terrain, 10, 1); got != 0 {
			t.Fatal("slow beam suppressed a faster shot")
		}
		target.Move.Heading++
		if got := s.incomingDamage(shooter, target, w, terrain, 10, 60); got != 0 {
			t.Fatal("turn retained coverage")
		}
		target.Move.Heading--
		if got := s.incomingDamage(shooter, target, w, terrain, 10, 60); got != 0 {
			t.Fatal("invalidated shot regained its old promise")
		}
	}
}

func TestModernBeamMissObstructionAndHorizon(t *testing.T) {
	s, w, terrain, shooter, target, weapon := beamFixture(t)
	p := Projectile{Shooter: shooter.Handle, ShooterSide: shooter.Owner}
	InitOrdinary(&p, weapon, 10, Vec3{shooter.X, shooter.Y, shooter.Z}, Vec3{target.X, target.Y, target.Z}, target.Handle)
	target.Move.Speed, target.Move.VelZ = numeric.FixedFromInt(2), numeric.FixedFromInt(2)
	if got := s.reliableETA(p, weapon, target, w, terrain, 10); got != 0 {
		t.Fatal("unled transverse miss reserved damage")
	}
	target.Move.Speed, target.Move.VelZ = 0, 0
	terrain.PlotAt(20, 16).SetMinHeight(64)
	if got := s.reliableETA(p, weapon, target, w, terrain, 10); got != 0 {
		t.Fatal("ridge ignored")
	}
	terrain.PlotAt(20, 16).SetMinHeight(0)
	p.ExpiryTick = 11
	if got := s.reliableETA(p, weapon, target, w, terrain, 10); got != 0 {
		t.Fatal("expiry ignored")
	}
	for _, steps := range []int{60, 61} {
		terrain = beamTestTerrain()
		weapon.WeaponVelocity = 16 << 16
		target.X = shooter.X + numeric.FixedFromInt(int64(steps*16+8))
		stampBeamTarget(terrain, target)
		InitOrdinary(&p, weapon, 10, Vec3{shooter.X, 0, shooter.Z}, Vec3{target.X, 0, target.Z}, target.Handle)
		got := s.reliableETA(p, weapon, target, w, terrain, 10)
		if (got != 0) != (steps == 60) {
			t.Fatalf("horizon steps=%d ETA=%d", steps, got)
		}
	}
}

func TestModernBeamMotionAndIdentityRelease(t *testing.T) {
	for _, change := range []func(*units.Unit){
		func(u *units.Unit) { u.Move.Speed = 1 },
		func(u *units.Unit) { u.Move.VelX = 1 },
		func(u *units.Unit) { u.Move.Mode = 2 },
		func(u *units.Unit) { u.Dying = true },
		func(u *units.Unit) { u.Attachment.Carrier = 1 },
	} {
		s, w, terrain, shooter, target, weapon := beamFixture(t)
		random := rng.NewSimulation(77)
		// A horizontal, unspread beam directly reaches the grounded target.
		shooter.Y = 0
		h, fired := modernLaunch(t, s, w, terrain, shooter, target, weapon, &random)
		if !fired || s.incomingDamage(shooter, target, w, terrain, 10, 60) == 0 {
			t.Fatal("missing initial coverage")
		}
		change(target)
		if s.incomingDamage(shooter, target, w, terrain, 10, 60) != 0 {
			t.Fatal("changed mobile retained coverage")
		}
		s.MarkDead(h)
		s.Compact(nil)
		if s.Count() != 0 {
			t.Fatal("dead beam survived compaction")
		}
	}
}

func TestModernBeamConfidenceFamilyBoundary(t *testing.T) {
	for _, change := range []func(*units.Unit, *content.WeaponDef){
		func(_ *units.Unit, weapon *content.WeaponDef) { weapon.BeamWeapon = false },
		func(_ *units.Unit, weapon *content.WeaponDef) { weapon.Accuracy = 1 },
		func(_ *units.Unit, weapon *content.WeaponDef) { weapon.SprayAngle = 1 },
		func(_ *units.Unit, weapon *content.WeaponDef) { weapon.WeaponAcceleration = 1 },
		func(_ *units.Unit, weapon *content.WeaponDef) { weapon.WeaponVelocity = 16<<16 - 1 },
		func(target *units.Unit, _ *content.WeaponDef) { target.Def.CanFly = true },
		func(target *units.Unit, _ *content.WeaponDef) { target.Def.CanHover = true },
		func(target *units.Unit, _ *content.WeaponDef) { target.Def.Floater = true },
	} {
		s, w, terrain, shooter, target, weapon := beamFixture(t)
		change(target, weapon)
		p := Projectile{Shooter: shooter.Handle, ShooterSide: shooter.Owner}
		InitOrdinary(&p, weapon, 10, Vec3{shooter.X, 0, shooter.Z}, Vec3{target.X, 0, target.Z}, target.Handle)
		if s.reliableETA(p, weapon, target, w, terrain, 10) != 0 {
			t.Fatal("unsupported family promised mobile damage")
		}
	}
}

func TestModernInstalledAnnihilatorMobileEnergy(t *testing.T) {
	catalog, _ := retailcat.Shared(t)
	for _, moving := range []bool{false, true} {
		for _, strict := range []bool{false, true} {
			for _, distance := range []int32{600, 1000} {
				w := newCombatFixtureWorld(10, catalog)
				terrain := beamTestTerrain()
				makeUnit := func(name string, owner uint8, x, z int32) *units.Unit {
					h, err := w.Create(catalog.Units[name], owner, numeric.FixedFromInt(int64(x)), 0, numeric.FixedFromInt(int64(z)))
					if err != nil {
						t.Fatal(err)
					}
					return w.Unit(h)
				}
				first := makeUnit("armanni", 0, 128, 256)
				second := makeUnit("armanni", 0, 128, 320)
				target := makeUnit("armflash", 1, 128+distance, 256)
				target.Move.Mode = 1
				if moving {
					target.Move.Speed = numeric.Fixed(target.Def.MaxVelocity)
					target.Move.VelZ = target.Move.Speed
					target.Move.Heading = 32768
					first.Kills, second.Kills = 7, 7 // authored lead becomes available after five kills
				}
				stampBeamTarget(terrain, target)
				s := &Service{Rules: &ModernRules{}, Visibility: func(_ visibility.PlayerID, _ visibility.Target) bool { return true }, Reaction: &ReactionSeams{Allied: func(a, b uint8) bool { return a == b }}}
				if strict {
					s.Rules = StrictRules{}
				}
				econ := &economy.Service{}
				econ.Players[0].Stock[economy.Energy] = 10000
				random := rng.NewSimulation(77)
				for _, u := range []*units.Unit{first, second} {
					u.SlotAt(0).Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
					u.SlotAt(0).Flags &^= units.SlotFlagAutonomous
					u.SlotAt(0).Aim.IssueBit = true
					u.SlotAt(0).Aim.Ready = true
					yaw, pitch, ok := turretAimGeometry(u, u.SlotAt(0).Weapon, 0, s.callbackBridgeForUnit(u), PreFireLeadPoint(u, target, u.SlotAt(0), u.SlotAt(0).Weapon, s.unitTargetPoint(target)), terrain)
					if !ok {
						t.Fatal("aim geometry")
					}
					u.SlotAt(0).DesiredYaw, u.SlotAt(0).DesiredPitch = yaw, pitch
				}
				s.rules().CombatTick(s, 10, false)
				if sum := s.StepWeaponsForUnit(first, 10, w, nil, terrain, econ, catalog, &random, nil); sum.Fired != 1 {
					t.Fatalf("first shot strict=%v distance=%d summary=%+v flags=%d standing=%d slots=%+v", strict, distance, sum, first.Flags, first.Def.StandingFireOrder, first.SlotAt(0))
				}
				beforeRNG, beforePending := random, second.Pending
				sum := s.StepWeaponsForUnit(second, 10, w, nil, terrain, econ, catalog, &random, nil)
				wantFired, wantEnergy := 0, float32(8000)
				if strict {
					wantFired, wantEnergy = 1, 6000
				}
				if int(sum.Fired) != wantFired || econ.Players[0].Stock[economy.Energy] != wantEnergy {
					t.Fatalf("second strict=%v distance=%d summary=%+v energy=%v eta=%d", strict, distance, sum, econ.Players[0].Stock[economy.Energy], s.reliableETA(s.Records[0], first.SlotAt(0).Weapon, target, w, terrain, 10))
				}
				if !strict {
					if random != beforeRNG || second.Pending != beforePending || second.SlotAt(0).Reload != 0 {
						t.Fatal("covered beam spent firing state or raised blocked feedback")
					}
					alternative := makeUnit("armflash", 1, 128+distance, 384)
					alternative.Move.Mode = 1
					stampBeamTarget(terrain, alternative)
					q := modernTargetQuery(s, w, terrain, second, target, alternative)
					if got, ok := s.rules().SelectTarget(s, &q); !ok || got != alternative.Handle {
						t.Fatalf("covered target prevented alternative acquisition: %d %v", got, ok)
					}
				} else if second.SlotAt(0).Reload == 0 {
					t.Fatal("Strict beam lost its ordinary reload")
				}

				if !strict {
					for tick := uint32(10); tick < 70 && target.Health > 0; tick++ {
						if moving {
							ax, az := beamAnchor(target.X, target.Z, int32(target.Def.FootprintX), int32(target.Def.FootprintZ))
							stampGroundRect(terrain, ax, az, int32(target.Def.FootprintX), int32(target.Def.FootprintZ), 0)
							target.Z += target.Move.VelZ
							stampBeamTarget(terrain, target)
						}
						s.TickProjectiles(tick, w, terrain, nil, nil, nil, econ, catalog, &random, nil)
					}
					if target.Health > 0 {
						t.Fatalf("reserved beam missed: moving=%v distance=%d hp=%d", moving, distance, target.Health)
					}
				}
			}
		}
	}
}
