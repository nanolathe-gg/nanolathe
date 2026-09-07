package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
)

// TestBurnWeaponFiresThroughTheSharedSplashEntry is the burn-weapon census:
// the `burnweapon` of [05 R-FEAT-01 §11] step 3 must reach an actual combat
// request in the production composition, not an emission list. The session
// binds the feature service's BurnWeapon seam to combat.ExplodeWeaponAt with a
// null shooter [06 §13.1], so when a burning tree's spark countdown reaches
// zero its weapon's area damage lands on a resting tree inside the radius
// through the ordinary feature walk of the blast [05 R-FEAT-01 §8] step 6. A
// name the catalog does not resolve fires nothing.
func TestBurnWeaponFiresThroughTheSharedSplashEntry(t *testing.T) {
	for _, tc := range []struct {
		name       string
		weapon     string
		wantDamage uint16
	}{
		{name: "resolved burnweapon lands its default damage", weapon: "burnfx", wantDamage: 3},
		{name: "unresolved burnweapon fires nothing", weapon: "nosuchweapon", wantDamage: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs := featureLifecycleFS(t)
			cat := featureLifecycleCatalog()
			burnfx := &content.WeaponDef{ID: 5, AreaOfEffect: 48, DamageDefault: 3}
			burnfx.CanonicalKey = "burnfx"
			cat.Weapons = map[string]*content.WeaponDef{burnfx.CanonicalKey: burnfx}
			tree := cat.Features["tree1"]
			tree.BurnWeapon = tc.weapon
			s := featureLifecycleSession(t, fs, cat)
			if s.Features.BurnWeapon == nil {
				t.Fatal("composition left the burn weapon seam unbound")
			}
			if s.Features.PlaceAt(6, 6, tree) == nil || s.Features.PlaceAt(7, 6, tree) == nil {
				t.Fatal("tree placement failed")
			}
			if !s.Features.Ignite(6, 6, 1, 0) {
				t.Fatal("ignition refused")
			}
			inst := s.Features.InstanceAt(6, 6)
			inst.BurnCountdown = 1 // the next visit runs the burn event
			s.Features.TickLifecycle(1)
			if inst.BurnCountdown != 0 {
				t.Fatalf("countdown %d after the visit, want 0 (event fired)", inst.BurnCountdown)
			}
			// The neighbour at (7,6) rests inside the 24-unit radius of the
			// footprint centre (6·16+8, 6·16+8): the blast's feature walk adds
			// the weapon's default word to its anchor accumulator.
			if got := s.World.PlotAt(7, 6).AnchorWord(); got != tc.wantDamage {
				t.Fatalf("neighbour accumulator %d after the burn event, want %d [05 R-FEAT-01 §11 step 3][06 §13.1]", got, tc.wantDamage)
			}
			// The burning tree itself discards blast damage [05 R-FEAT-01 §8
			// step 8] and keeps burning.
			if got := s.Features.InstanceAt(6, 6); got != inst || !got.IsBurning {
				t.Fatal("the burning tree was disturbed by its own burn weapon")
			}
		})
	}
}
