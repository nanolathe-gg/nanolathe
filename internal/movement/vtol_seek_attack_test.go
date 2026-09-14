package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// A successful seek restart must put the resolved attack ahead of the seeker
// before the pump reloads its head [04 R-AIR-01 §7][04 R-STANCE-01 §3].
func TestVTOLSeekAttackTargetRestartMakesProgress(t *testing.T) {
	sys, w, u := wideAirFixture(t)
	target := airTargetFor(t, sys, w, 40, 16)
	u.Def.CanAttack = true
	u.InstallWeapon(0, &content.WeaponDef{Range: 200})
	u.Flags |= units.ArmedStatus | 2<<units.StandingMoveShift | 2<<units.StandingFireShift
	q := orders.QueueForUnit(u)
	b := q.Binding()
	b.Weapons = &orders.WeaponAdapter{SetManualTarget: combat.SetManualWeaponTarget}
	b.Hostility = func(a, b *units.Unit) bool { return a.Owner != b.Owner }
	b.World = &orders.WorldQueryAdapter{SeaLevel: func() uint8 { return 0 }}
	sys.BindAirOrderLegs()
	q.Push(orders.Lookup("VTOL_SeekAttack"), orders.Node{Owner: u.Handle, Target: target.Handle})
	seek := q.Primary()[0]

	// Inspect the first dispatch before pumping: a broken restart must fail
	// this assertion rather than wedge the test process in the queue walk.
	if code := sys.legVTOLSeekAttack(u, seek, 0, 100); code != 0 {
		t.Fatalf("accepted target returned %d, want restart", code)
	}
	if q.Primary()[0] == seek {
		t.Fatal("seek restarted without inserting an attack: the pump would repeat phase zero forever")
	}
	attack := q.Primary()[0]
	if attack.ID != orders.Lookup("AirToGround") || attack.Target != target.Handle || q.Primary()[1] != seek {
		t.Fatalf("seek did not retain the resolved attack ahead of itself: %+v", q.Primary())
	}
	q.Pump(u, 100)
	if q.Primary()[0] != attack || attack.DynamicGate == 0 {
		t.Fatal("resolved attack did not yield on its movement gate")
	}
}

// A bound target refused by the standing policy advances directly to the
// search phase, preserving the orbit scratch and RNG [04 R-AIR-01 §7].
func TestVTOLSeekAttackRefusalSkipsTakeoff(t *testing.T) {
	for _, stance := range []struct {
		name       string
		move, fire uint32
	}{
		{"hold position", 0, 2},
		{"hold fire", 2, 0},
	} {
		t.Run(stance.name, func(t *testing.T) {
			sys, w, u := wideAirFixture(t)
			target := airTargetFor(t, sys, w, 40, 16)
			u.Flags |= stance.move<<units.StandingMoveShift | stance.fire<<units.StandingFireShift
			q := orders.QueueForUnit(u)
			q.Binding().Weapons = &orders.WeaponAdapter{SetManualTarget: combat.SetManualWeaponTarget}
			q.Push(orders.Lookup("VTOL_SeekAttack"), orders.Node{Owner: u.Handle, Target: target.Handle, Param1: 17, Param2: 1})
			n := q.Primary()[0]
			draws := q.Binding().SimRNG.Draws()
			mode := u.Move.Mode
			if code := sys.legVTOLSeekAttack(u, n, 0, 100); code != 1 {
				t.Fatalf("refused target returned %d, want advance", code)
			}
			if q.Primary()[0] != n || n.Param1 != 17 || n.Param2 != 1 || n.DynamicGate != 0 || u.Move.Mode != mode || q.Binding().SimRNG.Draws() != draws {
				t.Fatal("refused target initialized the orbit, took off, or changed the queue")
			}
		})
	}
}

// The search uses sight-distance opportunity acquisition and completes only
// after issuing an attack. Inhibition precedes the scan [04 R-AIR-01 §7].
func TestVTOLSeekAttackSearchIssuesBeforeCompleting(t *testing.T) {
	for _, stance := range []struct {
		name                 string
		move, fire           uint32
		wantScan, wantAttack bool
	}{
		{"fire at will", 2, 2, true, true},
		{"return fire", 2, 1, false, false},
		{"hold fire", 2, 0, false, false},
		{"hold position", 0, 2, true, false},
	} {
		t.Run(stance.name, func(t *testing.T) {
			sys, w, u := wideAirFixture(t)
			target := airTargetFor(t, sys, w, 40, 16)
			u.Def.CanAttack, u.Def.SightDistance = true, 500
			u.InstallWeapon(0, &content.WeaponDef{Range: 200})
			u.Flags |= units.ArmedStatus | stance.move<<units.StandingMoveShift | stance.fire<<units.StandingFireShift
			q := orders.QueueForUnit(u)
			b := q.Binding()
			b.Hostility = func(a, b *units.Unit) bool { return a.Owner != b.Owner }
			b.World = &orders.WorldQueryAdapter{SeaLevel: func() uint8 { return 0 }}
			inhibited, scanned := 0, false
			b.Weapons = &orders.WeaponAdapter{
				SetManualTarget: combat.SetManualWeaponTarget,
				InhibitSlot: func(actor *units.Unit, slot int) bool {
					if slot != inhibited {
						t.Fatal("slot inhibition changed order")
					}
					inhibited++
					return combat.InhibitWeaponSlot(actor, slot)
				},
				Acquire: func(_ *units.Unit, _ int, limit uint32) (pool.Handle, bool) {
					if inhibited != units.NumSlots {
						t.Fatal("acquisition ran before inhibition")
					}
					if limit != uint32(u.Def.SightDistance) {
						t.Fatalf("scan range %d, want sightdistance", limit)
					}
					scanned = true
					return target.Handle, true
				},
			}
			q.Push(orders.Lookup("VTOL_SeekAttack"), orders.Node{Owner: u.Handle, Phase: 1, GoalX: u.X, GoalY: u.Y, GoalZ: u.Z})
			n := q.Primary()[0]
			draws := b.SimRNG.Draws()
			code := sys.legVTOLSeekAttack(u, n, 0, 100)
			if scanned != stance.wantScan {
				t.Fatalf("scanned = %v, want %v", scanned, stance.wantScan)
			}
			if stance.wantAttack {
				if code != 5 || q.Primary()[0].ID != orders.Lookup("AirToGround") || q.Primary()[0].Target != target.Handle || q.Primary()[1] != n {
					t.Fatal("successful search completed without inserting its attack")
				}
				if n.Target != 0 || n.DynamicGate != 0 || b.SimRNG.Draws() != draws {
					t.Fatal("accepted search changed seek state or drew orbit RNG")
				}
			} else if code != 2 || q.Primary()[0] != n || n.DynamicGate == 0 {
				t.Fatal("refused search did not wait on its orbit")
			}
		})
	}
}
