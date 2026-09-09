package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// TestDamagePathArmsTheMinimapBlink locks the producer half of [06 R-WPN-04 §2]:
// an accepted non-heal packet writes the victim's minimap blink byte, and it
// writes the value the unit sweep reads as -16 so the blink lasts exactly
// sixteen unit visits ([04 R-MOV-03 §1] step 5). Every hit re-arms it outright —
// there is no accumulation and no maximum — and a heal never arms it at all.
func TestDamagePathArmsTheMinimapBlink(t *testing.T) {
	f := newReactionFixture(t)
	terrain := &world.Terrain{CellW: 100, CellH: 100, Plot: make([]world.PlotCell, 100*100)}
	// The sweep's candidates are the plot cells' occupancy words [06 §9.3], and
	// a combat fixture has no movement system to fill them.
	stampGroundOccupancy(t, terrain, f.victim)
	if f.victim.BlinkSuppress != 0 {
		t.Fatalf("victim spawned with blink byte %d, want 0 [06 R-WPN-04 §2]", f.victim.BlinkSuppress)
	}
	weapon := &content.WeaponDef{ID: 1, AreaOfEffect: 200, DamageDefault: 10, EdgeEffectiveness: 0}
	f.svc.ExplodeWeaponAt(f.w, terrain, weapon, Vec3{X: f.victim.X, Y: f.victim.Y, Z: f.victim.Z}, f.attacker.Handle, 5)
	if f.victim.BlinkSuppress != DamageFlashByte {
		t.Fatalf("blink byte after a hit = %d, want %d [06 R-WPN-04 §2]", f.victim.BlinkSuppress, DamageFlashByte)
	}
	// A part-spent blink is re-armed to the full sixteen visits by the next hit,
	// not extended or clamped.
	f.victim.BlinkSuppress = -3
	f.svc.ExplodeWeaponAt(f.w, terrain, weapon, Vec3{X: f.victim.X, Y: f.victim.Y, Z: f.victim.Z}, f.attacker.Handle, 6)
	if f.victim.BlinkSuppress != DamageFlashByte {
		t.Fatalf("blink byte after a re-hit = %d, want a full re-arm of %d [06 R-WPN-04 §2]", f.victim.BlinkSuppress, DamageFlashByte)
	}
	// "Healing produces no flash" [06 §9.1] holds structurally rather than by a
	// branch: the healing path is a pure arithmetic helper with no unit argument,
	// so there is no site at which a heal could reach the byte.
	f.victim.BlinkSuppress = 0
	if got := ApplyHealing(10, 100, 5); got != 15 || f.victim.BlinkSuppress != 0 {
		t.Fatalf("ApplyHealing(10,100,5) = %d and blink byte %d, want 15 and 0 [06 §9.1]", got, f.victim.BlinkSuppress)
	}
}
