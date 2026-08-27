package client

import (
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/frame"
)

func TestModelTexturePlayersAreInstanceLocal(t *testing.T) {
	c := &Client{}
	c2 := &Client{}
	entry := &formats.GAFEntry{Name: "animated", Frames: []formats.GAFFrameRef{
		{Value: 1, Frame: &formats.GAFFrame{Pixels: []byte{1}}},
		{Value: 1, Frame: &formats.GAFFrame{Pixels: []byte{2}}},
	}}
	ref := texRef{kind: texAnimated, key: "textures/default.gaf|animated", entry: entry, frame: entry.Frames[0].Frame}
	if got := c.modelAnimatedFrame(ref, 0, 10); got != entry.Frames[0].Frame {
		t.Fatal("new instance did not start at authored first frame")
	}
	if got := c.modelAnimatedFrame(ref, 0, 11); got != entry.Frames[0].Frame {
		t.Fatal("second instance did not start at authored first frame")
	}
	c.TickTextureAnimators(1)
	if got := c.modelAnimatedFrame(ref, 0, 10); got != entry.Frames[1].Frame {
		t.Fatal("first instance did not advance on simulation tick")
	}
	if got := c.modelAnimatedFrame(ref, 0, 11); got != entry.Frames[1].Frame {
		t.Fatal("second instance did not advance on simulation tick")
	}
	if got := c.modelAnimatedFrame(ref, 0, 12); got != entry.Frames[0].Frame {
		t.Fatal("new instance inherited another instance's animation phase")
	}
	if got := c2.modelAnimatedFrame(ref, 0, 10); got != entry.Frames[0].Frame {
		t.Fatal("second client inherited the first client's animation phase")
	}
	if c.modelPresentation[modelTextureKey{kind: 0, id: 10, tex: ref.key}] == c2.modelPresentation[modelTextureKey{kind: 0, id: 10, tex: ref.key}] {
		t.Fatal("clients share model cursor ownership")
	}

	zeroA := frame.UnitView{Slot: 1}
	zeroB := frame.UnitView{Slot: 2}
	if c.orientationCache(unitPresentationID(zeroA)) == c.orientationCache(unitPresentationID(zeroB)) {
		t.Fatal("zero-ID units with distinct stable slots share an orientation cache")
	}
	if c.orientationCache(unitPresentationID(zeroA)) == c2.orientationCache(unitPresentationID(zeroA)) {
		t.Fatal("orientation caches are shared across clients")
	}
}

func TestTextureResolutionUsesSideBeforeFallback(t *testing.T) {
	side := map[string]texRef{"panel": {key: "side|panel"}}
	fallback := map[string]texRef{"panel": {key: "default|panel"}}
	got, ok := resolveTextureRef(side, fallback, "PANEL")
	if !ok || got.key != "side|panel" {
		t.Fatalf("side texture did not win: %+v %v", got, ok)
	}
}
