package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// TestDetailArtOnlyWhileEnhancedPresents locks a Nanolathe presentation rule,
// not retail behaviour: the synthesized 2x art is an Enhanced feature, so the
// provider's tiles and frames are recorded only while the modern executor
// presents, and Original gets the authored art nearest-doubled at the detail
// scale (DESIGN_GPU_RENDERER §14.3).
func TestDetailArtOnlyWhileEnhancedPresents(t *testing.T) {
	source := &formats.GAFFrame{Width: 2, Height: 1, ColorKey: 9, Pixels: []byte{1, 2}, Transparent: []bool{false, false}}
	source.PlainPixels, source.PlainTransparent = source.Pixels, source.Transparent
	variant := &formats.GAFFrame{Width: 4, Height: 2, ColorKey: 9, Pixels: []byte{5, 5, 6, 6, 5, 5, 6, 6}, Transparent: make([]bool, 8)}
	variant.PlainPixels, variant.PlainTransparent = variant.Pixels, variant.Transparent
	loaded := &formats.GAF{Entries: []formats.GAFEntry{{Name: "rock1", Frames: []formats.GAFFrameRef{{Frame: source}}}}}
	detail := &formats.GAF{Entries: []formats.GAFEntry{{Name: "rock1", Frames: []formats.GAFFrameRef{{Frame: variant}}}}}
	terrain := &world.Terrain{TileSet: make([][1024]byte, 1)}
	tiles := make([][detailTilePixels]byte, 1)

	c := &Client{cam: &camera.Camera{Scale: 2}, terrain: terrain, featureGAFs: map[string]*formats.GAF{"rocks": loaded}}
	c.SetDetailArt(&DetailArt{Tiles: tiles, Banks: map[string]*formats.GAF{"rocks": detail}})

	// Original: the provider is installed but not consulted.
	if got := c.viewFrame(source); got == variant || got.Width != 4 || got.Pixels[0] != 1 {
		t.Fatalf("Original at scale 2 must draw the doubled authored frame, got %+v", got)
	}
	if c.detailTiles() != nil {
		t.Fatal("Original must record no detail tiles")
	}

	// Enhanced: the provider's variant and tiles.
	c.SetEnhanced(true)
	if got := c.viewFrame(source); got != variant {
		t.Fatalf("Enhanced at scale 2 must draw the provider's variant, got %+v", got)
	}
	if c.detailTiles() == nil {
		t.Fatal("Enhanced must record the provider's detail tiles")
	}

	// Back to Original, the doubled frame is the one cached earlier.
	c.SetEnhanced(false)
	first := c.viewFrame(source)
	if first == variant || c.viewFrame(source) != first {
		t.Fatal("Original must return the one cached doubled frame after a switch")
	}

	// Native scale is the identity regardless.
	c.cam.Scale = 1
	c.SetEnhanced(true)
	if c.viewFrame(source) != source {
		t.Fatal("scale 1 must draw the authored frame itself")
	}
}
