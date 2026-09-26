package gpurender

import (
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"testing"
)

func TestTransientFrameCachePinsCurrentSubmissionAndRetiresHistory(t *testing.T) {
	var cache transientFrames
	released := 0
	release := func(*ebiten.Image) { released++ }
	for generation := uint64(1); generation <= 10; generation++ {
		cache.clock = generation
		for j := 0; j < 3; j++ {
			f := &formats.GAFFrame{Transient: true}
			i := cache.slot(f, transientFrameBytes/2, release)
			cache.slots[i].image = &ebiten.Image{}
		}
		// The three current sources exceed the retained cache bound, but are
		// all needed by pending commands and must survive until submission ends.
		if len(cache.index) != 3 {
			t.Fatal("current source evicted before submission")
		}
		cache.trim(transientFrameBytes, 0, false, release)
		if cache.bytes > transientFrameBytes || len(cache.index) != 2 {
			t.Fatal("completed submission retained overflow")
		}
	}
	if released != 28 || len(cache.slots) != 3 {
		t.Fatalf("retention grew: released=%d slots=%d", released, len(cache.slots))
	}
}

func TestTransientEmitterDoesNotEnterLifetimeColorCache(t *testing.T) {
	r := &Renderer{}
	r.lighting.colors = make(map[*formats.GAFFrame][3]float32)
	r.displayPalette[7] = [4]byte{255, 255, 255, 255}
	for i := 0; i < 20; i++ {
		r.addSpriteLight(drawlist.Sprite{Frame: &formats.GAFFrame{Transient: true, Width: 1, Height: 1, Pixels: []byte{7}}, LightingKind: drawlist.SpriteLightingExplosion})
	}
	if len(r.lighting.colors) != 0 {
		t.Fatal("color cache retained transient art")
	}
}
