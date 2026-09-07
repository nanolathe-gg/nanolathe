package client

import (
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/drawlist"
	"github.com/nanolathe/nanolathe/internal/palette"
)

type gafSpriteCollector struct{ sprites []drawlist.Sprite }

func (s *gafSpriteCollector) Clear()                    {}
func (s *gafSpriteCollector) Terrain(drawlist.Terrain)  {}
func (s *gafSpriteCollector) Sprite(sp drawlist.Sprite) { s.sprites = append(s.sprites, sp) }
func (s *gafSpriteCollector) Glyphs(drawlist.Glyphs)    {}
func (s *gafSpriteCollector) Fill(drawlist.Fill)        {}
func (s *gafSpriteCollector) Line(drawlist.Line)        {}
func (s *gafSpriteCollector) Points(drawlist.Points)    {}
func (s *gafSpriteCollector) Model(drawlist.Model)      {}
func (s *gafSpriteCollector) Fog(drawlist.Fog)          {}
func (s *gafSpriteCollector) Surface(drawlist.Surface)  {}
func (s *gafSpriteCollector) Cursor(drawlist.Cursor)    {}
func (s *gafSpriteCollector) Expand()                   {}

func gafLeaf(index uint8, xOffset, yOffset int16) *formats.GAFFrame {
	return &formats.GAFFrame{
		Width: 1, Height: 1, XOffset: xOffset, YOffset: yOffset,
		Pixels: []byte{index}, Transparent: []bool{false},
	}
}

func TestEmitSpriteCompositePreservesPenOrderAndDestination(t *testing.T) {
	c := newTestClient(t)
	c.width, c.height = 20, 20
	c.indexed = make([]uint8, c.width*c.height)
	pal := &palette.Tables{}
	// The first plain child makes destination 5; the second child must resolve
	// ALP against that current value, never a flattened parent/background value.
	pal.Alpha[7*256+5] = 8
	c.SetPalette(pal)
	parent := &formats.GAFFrame{
		Width: 1, Height: 1, XOffset: 10, YOffset: 10,
		Subframes: []*formats.GAFFrame{
			gafLeaf(5, 10, 10),
			{Width: 1, Height: 1, XOffset: 10, YOffset: 10, Pixels: []byte{7}, Transparent: []bool{false}, AlternateBlitter: 0x81},
		},
	}
	c.resetListForTest()
	// A nonanchored caller gives the parent's top-left. Child frames share the
	// derived anchor pen, so the same leaf lands at this original top-left.
	c.emitSprite(drawlist.Sprite{Frame: parent, X: 2, Y: 3, Kind: drawlist.BlitKeyed})
	collector := &gafSpriteCollector{}
	c.list.Replay(collector)
	if len(collector.sprites) != 2 {
		t.Fatalf("leaf record count = %d, want 2", len(collector.sprites))
	}
	for i, sp := range collector.sprites {
		if sp.X != 12 || sp.Y != 13 || !sp.Anchored {
			t.Fatalf("leaf %d pen = (%d,%d), anchored %v; want shared (12,13), true", i, sp.X, sp.Y, sp.Anchored)
		}
	}
	if collector.sprites[0].Kind != drawlist.BlitKeyed || collector.sprites[1].Kind != drawlist.BlitTinted {
		t.Fatalf("leaf kinds = %v,%v, want keyed,tinted", collector.sprites[0].Kind, collector.sprites[1].Kind)
	}
	c.replayForTest()
	if got := c.indexed[3*c.width+2]; got != 8 {
		t.Fatalf("composite result = %d, want ALP[7,5] = 8", got)
	}
}

func TestEmitSpriteCompositeUsesTargetClipNotParentGeometry(t *testing.T) {
	c := newTestClient(t)
	c.width, c.height = 20, 20
	c.indexed = make([]uint8, c.width*c.height)
	// The parent canvas has one pixel at (2,3), but its child shares anchor
	// (12,13) and therefore lands outside that parent geometry. The framebuffer
	// is the target clip for this general composite path.
	parent := &formats.GAFFrame{
		Width: 1, Height: 1, XOffset: 10, YOffset: 10,
		Subframes: []*formats.GAFFrame{gafLeaf(6, 0, 0)},
	}
	c.resetListForTest()
	c.emitSprite(drawlist.Sprite{Frame: parent, X: 2, Y: 3, Kind: drawlist.BlitKeyed, HasClip: true, Clip: drawlist.Rect{X: 12, Y: 13, W: 1, H: 1}})
	c.replayForTest()
	if got := c.indexed[13*c.width+12]; got != 6 {
		t.Fatalf("child outside parent geometry = %d, want 6", got)
	}
	clear(c.indexed)
	c.resetListForTest()
	c.emitSprite(drawlist.Sprite{Frame: parent, X: 2, Y: 3, Kind: drawlist.BlitKeyed, HasClip: true, Clip: drawlist.Rect{X: 2, Y: 3, W: 1, H: 1}})
	c.replayForTest()
	if got := c.indexed[13*c.width+12]; got != 0 {
		t.Fatalf("child escaped target clip with index %d", got)
	}
}

func TestEmitSpriteTintedCompositePropagatesTintAndShadingGate(t *testing.T) {
	parent := &formats.GAFFrame{
		Width: 1, Height: 1, XOffset: 4, YOffset: 5,
		Subframes: []*formats.GAFFrame{{
			Width: 1, Height: 1, XOffset: 4, YOffset: 5,
			Subframes: []*formats.GAFFrame{gafLeaf(4, 4, 5)},
		}},
	}
	c := newTestClient(t)
	c.width, c.height = 20, 20
	c.indexed = make([]uint8, c.width*c.height)
	pal := &palette.Tables{}
	pal.Alpha[4*256+1] = 6
	c.SetPalette(pal)
	c.indexed[3*c.width+2] = 1
	c.resetListForTest()
	c.emitSprite(drawlist.Sprite{Frame: parent, X: 6, Y: 8, Kind: drawlist.BlitTinted})
	c.replayForTest()
	if got := c.indexed[3*c.width+2]; got != 6 {
		t.Fatalf("tinted descendant = %d, want inherited ALP result 6", got)
	}
	clear(c.indexed)
	c.indexed[3*c.width+2] = 1
	c.resetListForTest()
	c.emitSprite(drawlist.Sprite{Frame: parent, X: 6, Y: 8, Kind: drawlist.BlitTinted, HasClip: true, Clip: drawlist.Rect{X: 3, Y: 3, W: 1, H: 1}})
	c.replayForTest()
	if got := c.indexed[3*c.width+2]; got != 1 {
		t.Fatalf("tinted descendant escaped target clip with index %d", got)
	}

	c = newTestClient(t)
	c.width, c.height = 20, 20
	c.indexed = make([]uint8, c.width*c.height)
	c.indexed[3*c.width+2] = 1
	c.resetListForTest()
	c.emitSprite(drawlist.Sprite{Frame: parent, X: 6, Y: 8, Kind: drawlist.BlitTinted})
	c.replayForTest()
	if got := c.indexed[3*c.width+2]; got != 1 {
		t.Fatalf("palette-less tinted descendant = %d, want unchanged destination 1", got)
	}

	plainThenAlternate := &formats.GAFFrame{
		Width: 1, Height: 1, XOffset: 4, YOffset: 5,
		Subframes: []*formats.GAFFrame{
			gafLeaf(5, 4, 5),
			{Width: 1, Height: 1, XOffset: 4, YOffset: 5, Pixels: []byte{7}, Transparent: []bool{false}, AlternateBlitter: 1},
		},
	}
	c = newTestClient(t)
	c.width, c.height = 20, 20
	c.indexed = make([]uint8, c.width*c.height)
	c.SetPalette(pal)
	c.shading = false
	c.resetListForTest()
	c.emitSprite(drawlist.Sprite{Frame: plainThenAlternate, X: 6, Y: 8, Kind: drawlist.BlitKeyed})
	c.replayForTest()
	if got := c.indexed[8*c.width+6]; got != 5 {
		t.Fatalf("shading-off composite = %d, want plain sibling 5", got)
	}
}
