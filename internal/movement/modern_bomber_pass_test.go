package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Nanolathe Modern policy: DESIGN_MOVEMENT_PATH §3.4.1. A close-target
// maneuver attack must reach release before the leash can return it to post.
func TestModernBomberCompletesCloseTargetPass(t *testing.T) {
	for _, modern := range []bool{false, true} {
		sys, w, u := wideAirFixture(t)
		sys.BindAirOrderLegs()
		target := airTargetFor(t, sys, w, 30, 16)
		u.Def.CanAttack = true
		u.Def.ManeuverLeashLength = 1280
		// An authored long overflight makes the end-of-pass return observable.
		u.Def.AttackRunLength = 3500
		u.Def.Weapon1Def = &content.WeaponDef{ID: 1, Dropped: true, Range: 1000, ReloadTime: 5, EnergyPerShot: 5, MetalPerShot: 7}
		u.InstallWeapon(0, u.Def.Weapon1Def)
		u.Flags |= units.ArmedStatus
		u.Flags = (u.Flags & ^uint32((3<<units.StandingMoveShift)|(3<<units.StandingFireShift))) | 1<<units.StandingMoveShift | 2<<units.StandingFireShift
		q := orders.QueueForUnit(u)
		q.Binding().World = &orders.WorldQueryAdapter{SeaLevel: func() uint8 { return 0 }}
		q.Binding().ModernBomberPass = modern
		ledger := &economy.Service{}
		ledger.Players[0].Stock = [2]float32{10000, 10000}
		catalog := &content.Catalog{Weapons: map[string]*content.WeaponDef{"bomb": u.Def.Weapon1Def}}
		catalog.RebuildWeaponIndex()
		var svc combat.Service
		initialRNG := *q.Binding().SimRNG
		releases := 0
		q.Binding().Weapons = &orders.WeaponAdapter{InhibitSlot: combat.InhibitWeaponSlot, ReleaseSlot: combat.ReleaseWeaponSlot, FirePoint: combat.FireWeaponPoint, StopFiring: combat.StopWeaponFiring}
		if !orders.AutonomousEngage(u, target) {
			t.Fatal("engagement refused")
		}
		n := q.Head()
		post := q.Primary()[1]
		if n.Param3 != 1280 {
			t.Fatal("fixture must carry captured maneuver leash")
		}
		last := n.Phase
		ended := false
		for tick := uint32(1); tick <= 3000; tick++ {
			fired := svc.StepWeaponsForUnit(u, tick, w, nil, sys.Terrain, ledger, catalog, q.Binding().SimRNG, nil).Fired
			releases += fired
			q.Pump(u, tick)
			runMovementTick(sys, tick, w)
			if q.Head() != n {
				t.Logf("modern=%v attack ended tick=%d phase=%d pos=(%d,%d) releases=%d next=%s targetAlive=%v", modern, tick, last, u.X.Int(), u.Z.Int(), releases, orders.DescriptorFor(post.ID).Name, target.Alive)
				if q.Head() != post {
					t.Fatal("attack did not resume its saved return move")
				}
				ended = true
				break
			}
			if n.Phase != last {
				t.Logf("modern=%v tick=%d phase=%d pos=(%d,%d)", modern, tick, n.Phase, u.X.Int(), u.Z.Int())
				last = n.Phase
			}
		}
		if !modern && (!ended || releases != 0) {
			t.Fatalf("maneuver did not reproduce abort: ended=%v releases=%d", ended, releases)
		}
		if modern && (releases == 0 || !ended || last != 6) {
			t.Fatalf("Modern failed to release and finish overflight: releases=%d ended=%v phase=%d", releases, ended, last)
		}
		if u.SlotAt(0).Target.Kind != units.TargetNone {
			t.Fatal("return move retained bomber firing target")
		}
		if !modern && *q.Binding().SimRNG != initialRNG {
			t.Fatal("Strict setup cancellation consumed approach or shot RNG")
		}
		if modern && q.Binding().SimRNG.Draws() <= initialRNG.Draws() {
			t.Fatal("Modern did not reach ordinary approach randomness")
		}
		want := [2]float32{10000 - float32(releases)*7, 10000 - float32(releases)*5}
		if ledger.Players[0].Stock != want {
			t.Fatalf("resources=%v, want %v for %d releases", ledger.Players[0].Stock, want, releases)
		}
	}
}
