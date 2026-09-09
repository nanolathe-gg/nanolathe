package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// TestReloadBoundaryUsesTheDecrementedWord locks the slot pipeline's updated
// counter test: an otherwise ready reload of one fires on this visit, whereas
// two requires a second visit [06 §3.3][06 §4.2].
func TestReloadBoundaryUsesTheDecrementedWord(t *testing.T) {
	for _, tc := range []struct {
		start, visit int16
	}{
		{start: 0, visit: 1},
		{start: 1, visit: 1},
		{start: 2, visit: 2},
	} {
		t.Run("reload", func(t *testing.T) {
			w, terrain, shooter, target := newTestWorldAndUnits(t)
			weapon := weaponNonTurret(902)
			shooter.InstallWeapon(0, weapon)
			slot := shooter.SlotAt(0)
			slot.Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
			slot.Reload = int32(tc.start)
			catalog := &content.Catalog{Weapons: map[string]*content.WeaponDef{"reload-boundary": weapon}}
			catalog.RebuildWeaponIndex()
			var svc Service
			for tick := int16(1); tick <= tc.visit; tick++ {
				sum := svc.StepWeaponsForUnit(shooter, uint32(tick), w, nil, terrain, nil, catalog, nil, nil)
				if tick < tc.visit && sum.Fired != 0 {
					t.Fatalf("reload %d fired on visit %d, want visit %d", tc.start, tick, tc.visit)
				}
				if tick == tc.visit && sum.Fired != 1 {
					t.Fatalf("reload %d fired %d on visit %d, want one shot", tc.start, sum.Fired, tick)
				}
			}
		})
	}
}
