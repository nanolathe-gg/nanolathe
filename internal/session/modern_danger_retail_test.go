package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Reproduce a Roam Flash taking a hidden rocket tower's fire through the real
// COB, projectile, damage, order and movement pipeline. After a real launch,
// stage its flight near the victim to isolate response from corridor accuracy.
// Movement must actually advance after the projectile delivers damage.
func TestModernRetailFlashMovesAfterHiddenRocketImpact(t *testing.T) {
	f := loadRetailFixture(t)
	f.cfg.MapName = "Great Divide"
	for _, mode := range []gameplay.Mode{gameplay.Modern, gameplay.Strict31} {
		t.Run(string(mode), func(t *testing.T) {
			s := f.session(t)
			s.SetGameplay(mode)
			stepRetail(s, 2)
			for _, ai := range s.AI {
				if ai != nil {
					for i := range ai.Deadlines {
						ai.Deadlines[i] = ^uint32(0)
					}
				}
			}
			eligible, observers := visibilityModeRefreshInputs(s, 3)
			s.Vis.RefreshMode(3, true, eligible, observers)
			flash := placeCompleteRetailUnit(t, s, "ARMFLASH", 0, numeric.FixedFromInt(1472), numeric.FixedFromInt(2416))
			tower := placeCompleteRetailUnit(t, s, "CORRL", 1, numeric.FixedFromInt(968), numeric.FixedFromInt(2792))
			// Give the enemy a spotting footprint without adding another attack target
			// to the Flash's local scene. Owner zero still cannot see the tower.
			s.Vis.Publish(1, int32(flash.X.Int()/32), int32((flash.Z.Int()-flash.Y.Int()/2)/32), 0, 320)
			flash.Flags = (flash.Flags &^ (units.StandingFieldMask<<units.StandingMoveShift | units.StandingFieldMask<<units.StandingFireShift)) | 2<<units.StandingMoveShift | 2<<units.StandingFireShift
			s.bindOrderQueue(flash)
			orders.QueueOfUnit(flash).Push(orders.Lookup("Standby"), orders.Node{Owner: flash.Handle, Flags: orders.FlagAutoOp})
			tower.SlotAt(0).Flags &^= units.SlotFlagEnabled
			stepRetail(s, 5) // settle initial mover footprint centering before measuring
			tower.SlotAt(0).Flags |= units.SlotFlagEnabled
			combat.ReleaseWeaponSlot(tower, 0)
			combat.SetManualWeaponTarget(tower, 0, flash.Handle)
			x, z, health := flash.X, flash.Z, flash.Health
			var impactX, impactZ numeric.Fixed
			var impactBearing numeric.Angle
			impactSeen := false
			originalNotice := s.Combat.ImpactNotice
			s.Combat.ImpactNotice = func(v, a *units.Unit, bearing numeric.Angle, tick uint32) {
				if v == flash && a == tower && !impactSeen {
					impactSeen = true
					impactX, impactZ, impactBearing = v.X, v.Z, bearing
				}
				originalNotice(v, a, bearing, tick)
			}
			hitTick := uint32(0)
			moved, staged, retreatGoal := false, false, false
			for i := 0; i < 600; i++ {
				s.Step(s.Clock.ScaledAnchor + 1)
				if !staged {
					s.Combat.ForEachAliveInEntrySpan(func(_ pool.Handle, p *combat.Projectile) {
						if staged || p.Shooter != tower.Handle {
							return
						}
						// Preserve the real shot's authored weapon, owner and target; position it
						// just west of the victim so real splash and motion create the damage cue.
						aim := combat.Vec3{X: flash.X, Y: flash.Y + numeric.FixedFromInt(2), Z: flash.Z}
						muzzle := aim
						muzzle.X -= numeric.FixedFromInt(24)
						combat.InitOrdinary(p, tower.SlotAt(0).Weapon, s.Clock.GlobalTick, muzzle, aim, flash.Handle)
						staged = true
					})
				}
				if s.dangerVisible(flash, tower) || flash.SlotAt(0).Target.Unit == tower.Handle {
					t.Fatalf("tick=%d visible=%v hidden=%v target=%+v", s.Clock.GlobalTick, s.dangerVisible(flash, tower), tower.Hidden, flash.SlotAt(0).Target)
				}
				if flash.Health < health && hitTick == 0 {
					hitTick = s.Clock.GlobalTick
				}
				// The terrain corridor may require a sideways detour. Verify a
				// separating goal AND actual travel beyond footprint centering.
				if impactSeen {
					hx := impactX + numeric.FixedFromInt(int64(numeric.MulRound(numeric.Sin(impactBearing), 256)))
					hz := impactZ + numeric.FixedFromInt(int64(numeric.MulRound(numeric.Cos(impactBearing), 256)))
					if n := orders.QueueOfUnit(flash).Head(); n != nil && n.ID == orders.Lookup("Move_Ground") && n.Target == 0 {
						dx, dz := (n.GoalX - hx).Int(), (n.GoalZ - hz).Int()
						if dx*dx+dz*dz > 256*256 {
							retreatGoal = true
						}
					}
				}
				dx, dz := (flash.X - x).Int(), (flash.Z - z).Int()
				if impactSeen && dx*dx+dz*dz > 16*16 {
					moved = true
				}
				if hitTick != 0 && s.Clock.GlobalTick-hitTick >= 165 {
					break
				}
			}
			if !staged || hitTick == 0 {
				t.Fatalf("authored rocket never hit Flash: tower=%+v flash=(%v,%v) start=(%v,%v)", tower.SlotAt(0), flash.X, flash.Z, x, z)
			}
			if moved != (mode == gameplay.Modern) || impactSeen != (mode == gameplay.Modern) || retreatGoal != (mode == gameplay.Modern) {
				t.Fatalf("mode=%s moved away=%v bearing=%d impact=(%v,%v) Flash start=(%v,%v) end=(%v,%v)", mode, moved, impactBearing, impactX, impactZ, x, z, flash.X, flash.Z)
			}
		})
	}
}
