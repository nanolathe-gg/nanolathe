package render

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
)

// TestMinimapSubmergedEnemyBlip locks the unit-blip gate for a submerged
// enemy [03 §3.9] "Blip gate" [03 R-MM-01 §3]. Without sonar contact and
// without the seen bit (no line of sight; radar cannot reach a submerged hull
// [03 R-VIS-01 §5]) nothing is drawn. The seen bit alone admits the blip,
// because retail's line-of-sight probe sets it with no sea-level term
// [03 R-VIS-01 §4] pass 5; the gate has no depth term of its own.
func TestMinimapSubmergedEnemyBlip(t *testing.T) {
	m := camera.Minimap{W: 10, H: 10}
	mapped := &RadarSurface{W: 10, H: 10, Pitch: 12, Bits: make([]byte, 100)}
	blits := 0
	blit := func(*RadarSurface, int, int, byte, bool) { blits++ }
	enemy := MinimapContact{WorldX: 50, WorldZ: 50, WorldY: -20, Owner: 1, Palette: 3, LocalPlayer: 0, MinimapMode: 3}

	rebuildFinalExactInto(nil, mapped, m, 100, 100, []MinimapContact{enemy}, BlinkState{Phase: 1}, blit, 0xA0, 0xB0, 0xC0)
	if blits != 0 {
		t.Fatal("minimap blipped a submerged enemy with neither sonar nor sight")
	}
	for _, status := range []uint32{0x100, 0x200} {
		blits = 0
		e := enemy
		e.Status = status
		rebuildFinalExactInto(nil, mapped, m, 100, 100, []MinimapContact{e}, BlinkState{Phase: 1}, blit, 0xA0, 0xB0, 0xC0)
		if blits != 1 {
			t.Fatalf("status %#x: retail blip gate must admit the contact", status)
		}
	}
}
