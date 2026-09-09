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

// TestReloadNegativeSavedWordsStayBlocked exercises signed values that the
// retail unit restore accepts. Every nonzero word decrements before the exact
// zero admission test; the minimum signed value wraps in that 16-bit field
// [06 §3.3][06 §4.2].
func TestReloadNegativeSavedWordsStayBlocked(t *testing.T) {
	for _, tc := range []struct {
		name        string
		start, want int32
	}{
		{name: "minus-one", start: -1, want: -2},
		{name: "minimum-wraps", start: -32768, want: 32767},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, terrain, shooter, target := newTestWorldAndUnits(t)
			weapon := weaponNonTurret(903)
			shooter.InstallWeapon(0, weapon)
			slot := shooter.SlotAt(0)
			slot.Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
			slot.Reload = tc.start
			catalog := &content.Catalog{Weapons: map[string]*content.WeaponDef{"negative-reload": weapon}}
			catalog.RebuildWeaponIndex()
			var svc Service
			sum := svc.StepWeaponsForUnit(shooter, 1, w, nil, terrain, nil, catalog, nil, nil)
			if sum.Fired != 0 || svc.Count() != 0 || slot.Reload != tc.want {
				t.Fatalf("reload %d produced fired=%d count=%d next=%d, want blocked and %d", tc.start, sum.Fired, svc.Count(), slot.Reload, tc.want)
			}
		})
	}
}

// TestReloadStoreNarrowsTheComputedResult keeps the slot's final write at its
// signed-16 width even when a direct fixture supplies a wider definition word.
// The arithmetic helper remains wide so its documented truncation sequence is
// independently observable; this test covers only the destination store
// [06 §4.2].
func TestReloadStoreNarrowsTheComputedResult(t *testing.T) {
	w, terrain, shooter, target := newTestWorldAndUnits(t)
	weapon := weaponNonTurret(904)
	weapon.ReloadTime = 32768
	shooter.InstallWeapon(0, weapon)
	slot := shooter.SlotAt(0)
	slot.Target = units.Target{Kind: units.TargetUnit, Unit: target.Handle}
	catalog := &content.Catalog{Weapons: map[string]*content.WeaponDef{"wide-reload": weapon}}
	catalog.RebuildWeaponIndex()
	var svc Service
	if sum := svc.StepWeaponsForUnit(shooter, 1, w, nil, terrain, nil, catalog, nil, nil); sum.Fired != 1 {
		t.Fatalf("fixture did not fire: %+v", sum)
	}
	if slot.Reload != -32768 {
		t.Fatalf("stored reload=%d, want signed-16 -32768", slot.Reload)
	}
}
