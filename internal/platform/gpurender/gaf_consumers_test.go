package gpurender

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
)

func TestRawGlyphUsesDestinationLightStreamWithModeKeyAndClip(t *testing.T) {
	r, _ := schedulerFixture(t)
	f := &formats.GAFFrame{Width: 5, Height: 1, ColorKey: 9, Pixels: []byte{9, 2, 7, 32, 7}, Transparent: []bool{true, false, false, false, false}}
	r.Sprite(drawlist.Sprite{Frame: f, X: 2, Y: 3, Kind: drawlist.BlitLit, LightRow: 2, Pal: &palette.Tables{}, HasClip: true, Clip: drawlist.Rect{X: 2, Y: 3, W: 4, H: 1}})
	cover := litCoverage(r, 64, 64)
	if len(cover) != 2 || cover[[2]int{2, 3}] != 1 || cover[[2]int{4, 3}] != 1 {
		t.Fatalf("raw glyph light coverage %v, want mode-key skip, authored-key draw, unsafe-row skip and target clip", cover)
	}
	if len(r.sched.classVerts(schedOpaque)) != 0 {
		t.Fatal("raw glyph used opaque source-remap stream")
	}
}

func TestFogAtlasLeavesCompositeToOrderedPathAndGatesCompressedGray(t *testing.T) {
	skipAfterDeviceLoop(t)
	leaf := &formats.GAFFrame{Width: 1, Height: 1, Pixels: []byte{7}, Transparent: []bool{false}}
	composite := &formats.GAFFrame{Width: 1, Height: 1, Subframes: []*formats.GAFFrame{leaf}}
	rle := *leaf
	rle.Compressed = 1
	gray := [4]*formats.GAFEntry{{Frames: []formats.GAFFrameRef{{Frame: composite}, {Frame: &rle}}}}
	black := [4]*formats.GAFEntry{{Frames: []formats.GAFFrameRef{{Frame: &rle}}}}
	r := &Renderer{}
	r.fog.ensureAtlas(gray, black, camera.ViewScaleNative)
	if r.fog.slotPresent[0] || r.fog.slotPresent[1] {
		t.Fatal("gray atlas admitted composite or compressed frame")
	}
	if !r.fog.slotPresent[fogSlots] {
		t.Fatal("ordinary black family wrongly rejected RLE")
	}
	if r.FogContentError() != nil {
		t.Fatal("composite fallback reported an unsupported operation")
	}
}
