package gpurender

import (
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/drawlist"
	"github.com/nanolathe/nanolathe/internal/palette"
)

func TestCompositeLeafCommandsReachGPUKeyedAndTintStages(t *testing.T) {
	pal := &palette.Tables{}
	pal.Alpha[7*256+5] = 8
	plain := &formats.GAFFrame{Width: 1, Height: 1, Pixels: []byte{5}, Transparent: []bool{false}}
	tinted := &formats.GAFFrame{Width: 1, Height: 1, Pixels: []byte{7}, Transparent: []bool{false}}
	r, err := NewChecked(pal, 8, 8)
	if err != nil {
		t.Skipf("GPU shaders unavailable: %v", err)
	}
	// The classic composite test emits this exact leaf sequence and proves its
	// indexed result. Replay the same command order through the production GPU
	// sink so keyed and destination-reading leaves both populate their GPU assets.
	r.Clear()
	r.Sprite(drawlist.Sprite{Frame: plain, X: 2, Y: 3, Kind: drawlist.BlitKeyed, Anchored: true})
	r.Sprite(drawlist.Sprite{Frame: tinted, X: 2, Y: 3, Kind: drawlist.BlitTinted})
	if len(r.gafImages) != 2 {
		t.Fatalf("GPU leaf image cache = %d, want keyed and tinted leaves", len(r.gafImages))
	}
	// The alternate leaf's target clip reaches the destination-reading GPU
	// stage. Its left source texel is outside the window, so the submitted quad
	// begins at both destination x=3 and source x=1.
	clipped := &formats.GAFFrame{Width: 2, Height: 1, Pixels: []byte{7, 7}, Transparent: []bool{false, false}}
	r.Sprite(drawlist.Sprite{Frame: clipped, X: 2, Y: 3, Kind: drawlist.BlitTinted, HasClip: true, Clip: drawlist.Rect{X: 3, Y: 3, W: 1, H: 1}})
	if len(r.verts) != 4 || r.verts[0].DstX != 3 || r.verts[0].SrcX != 1 || r.verts[3].DstX != 4 || r.verts[3].SrcX != 2 {
		t.Fatalf("GPU tinted clip quad = %#v", r.verts)
	}
}
