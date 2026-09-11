package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
)

// Registry rebuilds see the unit word at their position in the player walk;
// the viewing player's sensor pass can run between them [06 §3.1]
// [03 R-SENSOR-01]. Already-built lists retain their separate cadence.
func TestRegistrySeesSensorChangeBetweenPlayers(t *testing.T) {
	f := newRegistryFixture(t, true)
	f.shooter.Activated = false
	f.sensorTick(30)
	s := &Service{}
	s.rebuildTargetRegistry(30, 0, f.world, f.vis, f.terrain, f.econ)
	if len(s.targets.secondaryList(0)) != 0 {
		t.Fatal("fixture begins seen")
	}
	f.shooter.Activated = true
	f.sensorTick(30)
	if f.enemy.Flags&visibility.SeenBit == 0 {
		t.Fatal("sensor did not detect enemy")
	}
	s.rebuildTargetRegistry(30, 1, f.world, f.vis, f.terrain, f.econ)
	found := false
	for _, h := range s.targets.secondaryList(1) {
		if h == f.enemy.Handle {
			found = true
		}
	}
	if !found {
		t.Fatal("later player used the earlier player's stale seen cache")
	}
	if len(s.targets.secondaryList(0)) != 0 {
		t.Fatal("sensor update rebuilt an earlier player's list")
	}
}

// A retained sensor snapshot is not the status word of a newly allocated
// occupant of the same slot [03 R-VIS-01 §4 Gate][06 §3.1].
func TestRegistryDoesNotGiveReusedSlotOldSensorContact(t *testing.T) {
	f := newRegistryFixture(t, true)
	f.sensorTick(1)
	old := f.enemy
	if old.Flags&visibility.SeenBit == 0 {
		t.Fatal("fixture enemy unseen")
	}
	f.world.Destroy(old.Handle, units.DeathKilled)
	f.world.FinalizeDeath(old.Handle, 2)
	h, err := f.world.Create(old.Def, old.Owner, old.X, old.Y, old.Z)
	if err != nil || h != old.Handle {
		t.Fatalf("slot not reused: %v %v", h, err)
	}
	replacement := f.world.Unit(h)
	replacement.Hidden = true
	if replacement.Flags&visibility.SeenBit != 0 {
		t.Fatal("constructor inherited seen bit")
	}
	s := &Service{}
	s.rebuildTargetRegistry(30, 0, f.world, f.vis, f.terrain, f.econ)
	for _, candidate := range s.targets.secondaryList(0) {
		if candidate == h {
			t.Fatal("new occupant inherited removed unit's sensor contact")
		}
	}
}
