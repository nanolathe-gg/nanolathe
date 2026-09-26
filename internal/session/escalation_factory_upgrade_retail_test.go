//go:build retail

package session

import (
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/features"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// The factory and product programs execute unmodified through ordinary work
// and birth callbacks. Scope: research/extensions/escalation-script-systems.md,
// "Factory and mobile upgrade"; this does not certify other factory pairs.
func TestEscalationFactoryUpgrade(t *testing.T) {
	s := escalationSystemSession(t)
	for _, key := range []string{"ARMAVP", "ALL_L2", "ARMBULL", "ARMFARK"} {
		if _, ok := s.Catalog.Unit(key); !ok {
			t.Fatalf("missing authored %s", key)
		}
	}
	factory := placeCompleteRetailUnit(t, s, "ARMAVP", 0, numeric.FixedFromInt(600), numeric.FixedFromInt(600))
	step := func(n int) {
		for range n {
			s.Econ.Players[0].Stock = [2]float32{1e6, 1e6}
			s.Econ.Players[0].Capacity = [2]float32{1e6, 1e6}
			advanceProTATicks(t, s, 1)
		}
	}
	build := func(key string) *units.Unit {
		t.Helper()
		menu := s.Catalog.BuildMenus[factory.Def.CanonicalKey]
		listed := false
		if menu != nil {
			for _, button := range menu.AuthoredButtons {
				listed = listed || strings.EqualFold(button, key)
			}
		}
		if !listed {
			t.Fatalf("%s is absent from the authored factory build list", key)
		}
		before := make(map[pool.Handle]bool)
		for _, u := range s.Units.Iter() {
			before[u.Handle] = true
		}
		if err := construction.QueueFactoryBuild(factory, key, 1, s.Catalog); err != nil {
			t.Fatal(err)
		}
		for range 16000 {
			step(1)
			for _, u := range s.Units.Iter() {
				if !before[u.Handle] && u.Def.UnitName == key && u.Remaining == 0 {
					// Let ordinary release and the marker watcher's next wake run.
					step(90)
					return u
				}
			}
		}
		t.Fatalf("%s did not finish: queue=%+v diagnostics=%v admission=%+v", key, orders.QueueForUnit(factory).Head(), factory.Script.Diagnostics(), s.Build.AdmissionDiagnostics())
		return nil
	}
	move := func(u *units.Unit, x, z int64, ticks int) {
		t.Helper()
		if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanOrder, Order: HumanOrderCommand{
			Handles: []pool.Handle{u.Handle}, Code: int(input.LatchMove), AssignedPosition: true,
			Position: orders.ResolvePos{X: numeric.FixedFromInt(x), Z: numeric.FixedFromInt(z)},
		}}); err != nil {
			t.Fatal(err)
		}
		step(ticks)
	}
	assertUpgrade := func(u *units.Unit, upgraded bool) {
		t.Helper()
		if !u.Alive || u.Remaining != 0 || escalationCAPiece(t, u, "turret2") != upgraded || escalationCAPiece(t, u, "turret1") == upgraded {
			t.Fatalf("Bulldog %d does not have expected completed upgrade state %v", u.Handle, upgraded)
		}
		if diagnostics := u.GetScript().Diagnostics(); len(diagnostics) != 0 {
			t.Fatal(diagnostics)
		}
	}

	old := build("ARMBULL")
	assertUpgrade(old, false)
	move(old, 400, 600, 600)
	upgrade := build("ALL_L2")
	if !upgrade.Alive || !upgrade.Armored || upgrade.Attachment.Carrier != factory.Handle {
		t.Fatal("factory did not retain the completed armored upgrade")
	}
	newUnit := build("ARMBULL")
	assertUpgrade(newUnit, true)
	assertUpgrade(old, false)
	if newUnit.Def != old.Def || newUnit.MaxHealth != old.MaxHealth {
		t.Fatal("script upgrade replaced the mobile definition or changed maximum health")
	}

	move(old, 400, 800, 1200)
	move(newUnit, 800, 800, 1200)
	weapon := old.SlotAt(0).Weapon
	if weapon == nil || weapon.CanonicalKey != "cannon_bull" || weapon.ReloadTime != 28 || newUnit.SlotAt(0).Weapon != weapon {
		t.Fatal("Bulldogs do not share the authored CANNON_BULL and nominal reload")
	}
	for _, sample := range []struct {
		unit     *units.Unit
		interval uint32
	}{{old, 36}, {newUnit, 28}} {
		u := sample.unit
		var fires []uint32
		// Observe the actual firing callback without calling or rescheduling it.
		u.COBBinding().Callbacks.SetLifecycleSink(func(e cob.LifecycleEvent) {
			if e.Name == "FirePrimary" && e.Phase == "start" {
				fires = append(fires, s.Clock.GlobalTick)
			}
		})
		x, z := u.X, u.Z+numeric.FixedFromInt(200)
		if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanOrder, Order: HumanOrderCommand{
			Handles: []pool.Handle{u.Handle}, Code: int(input.LatchAttack),
			Position: orders.ResolvePos{X: x, Y: s.World.HeightAt(x, z), Z: z},
		}}); err != nil {
			t.Fatal(err)
		}
		step(100)
		fires = nil
		step(400)
		u.COBBinding().Callbacks.SetLifecycleSink(nil)
		if len(fires) < 8 {
			t.Fatalf("insufficient repeated fire from Bulldog %d: %v", u.Handle, fires)
		}
		for i := 1; i < len(fires); i++ {
			if fires[i]-fires[i-1] != sample.interval {
				t.Fatalf("Bulldog %d interval differs from %d: %v", u.Handle, sample.interval, fires)
			}
		}
		t.Logf("Bulldog %d repeated shot interval: %d ticks", u.Handle, sample.interval)
	}
	assertUpgrade(newUnit, true)
	assertUpgrade(old, false)

	// Ordinary damage creates the actual wreck. The interval between the two
	// hits lets the health sample settle before the final Killed query.
	move(newUnit, 1000, 1000, 1200)
	x, z := newUnit.X, newUnit.Z
	if x-upgrade.X < numeric.FixedFromInt(200) {
		t.Fatal("casualty did not move clear of the upgrade marker")
	}
	s.acceptDamage(s.Clock.GlobalTick, combat.DamageInput{Victim: newUnit.Handle, Nominal: newUnit.Health - 50, Kind: combat.KindOrdinary})
	step(90)
	if r := s.acceptDamage(s.Clock.GlobalTick, combat.DamageInput{Victim: newUnit.Handle, Nominal: newUnit.Health, Kind: combat.KindOrdinary}); !r.DeathLatched {
		t.Fatalf("ordinary lethal damage failed: %+v", r)
	}
	step(90)
	wreck, _, _, ok := features.FeatureAt(s.World, x, z)
	if !ok || !wreck.Reclaimable || construction.FeatureNameToDefName(wreck.CanonicalKey) != "armbull" {
		t.Fatal("casualty did not leave a resurrectable Bulldog wreck")
	}
	reviver := placeCompleteRetailUnit(t, s, "ARMFARK", 0, x+numeric.FixedFromInt(64), z)
	if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanOrder, Order: HumanOrderCommand{
		Handles: []pool.Handle{reviver.Handle}, Code: 12,
		Position: orders.ResolvePos{X: x, Z: z, HasFeature: true},
	}}); err != nil {
		t.Fatal(err)
	}
	step(1200)
	var revived *units.Unit
	for _, u := range s.Units.Iter() {
		if u.Def == old.Def && u != old && u != newUnit {
			revived = u
			break
		}
	}
	if revived == nil || revived.Owner != old.Owner {
		t.Fatal("ordinary resurrection did not produce the replacement Bulldog")
	}
	if _, _, _, remains := features.FeatureAt(s.World, x, z); remains {
		t.Fatal("resurrection did not consume the actual wreck")
	}
	// TODO(question): Does historical Gold suppress the birth scan when
	// resurrecting next to a marker? A bounded manual Gold observation would
	// settle its readme's blanket exclusion; this case is outside the radius.
	assertUpgrade(revived, false)
	for _, u := range []*units.Unit{factory, upgrade, reviver} {
		if !u.Alive {
			t.Fatalf("fixture unit %s died unexpectedly", u.Def.UnitName)
		}
		if diagnostics := u.GetScript().Diagnostics(); len(diagnostics) != 0 {
			t.Fatal(diagnostics)
		}
	}
}
