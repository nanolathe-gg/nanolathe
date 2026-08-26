package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/units"
)

// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// emitter = clamp(worldY_high + def[0x170], 0, 255) with worldY first clamped
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// tileX = worldX >> 5, tileZ = (worldZ - emitter/2) >> 5.
//
// The model-top addend is load-bearing: with the emitter at ground level the
// terrain-ray horizon test ties on flat ground and rejects every step past the
// first, which collapses every unit's sight to a single ring.
func TestObserverEmitterHeight(t *testing.T) {
	def := &content.UnitDef{ModelTop: 39} // ARMCOM's 3DO top in whole world units
	u := &units.Unit{Def: def}
	u.X = 400 << 16
	u.Y = 86 << 16
	u.Z = 1264 << 16

	if got := heightByteAt(u, 0); got != 125 {
		t.Fatalf("emitter = %d, want 86+39 = 125", got)
	}
	// Sea level clamps the world Y up before the addend applies.
	if got := heightByteAt(u, 200); got != 240 {
		t.Fatalf("sea-clamped emitter = %d, want 201+39 = 240", got)
	}
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	tall := &units.Unit{Def: &content.UnitDef{ModelTop: 200}}
	tall.Y = 100 << 16
	if got := heightByteAt(tall, 0); got != 255 {
		t.Fatalf("emitter = %d, want the 255 clamp", got)
	}

	cx, cz := observerTile(u, 125)
	if cx != 400>>5 {
		t.Fatalf("tileX = %d, want %d", cx, 400>>5)
	}
	if want := (1264 - 125/2) >> 5; cz != int32(want) {
		t.Fatalf("tileZ = %d, want (1264-62)>>5 = %d", cz, want)
	}
	// A unit with no def sights from its own world Y, which the sea clamp
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	bare := &units.Unit{}
	bare.Z = 64 << 16
	if got := heightByteAt(bare, 0); got != 1 {
		t.Fatalf("def-less emitter = %d, want the SeaLevel+1 floor of 1", got)
	}
	if _, z := observerTile(bare, 0); z != 2 {
		t.Fatalf("def-less tileZ = %d, want 2", z)
	}
}
