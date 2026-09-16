package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// These tests lock the Enhanced reflection design, not retail behavior (§26).
func reflectionScene(t *testing.T) *Client {
	t.Helper()
	c := newTestClient(t)
	c.enhanced = true
	c.cam.Scale = camera.ViewScaleDetail
	c.terrain = &world.Terrain{CellW: 4, CellH: 4, SeaLevel: 10, Plot: make([]world.PlotCell, 16)}
	publishSeaLevel(t, c, 1, 10, 0)
	return c
}

func TestReflectionModelRefreshKeepsPhysicalSeaPlane(t *testing.T) {
	c := reflectionScene(t)
	g := &drawlist.ModelGeometry{Supersample: &drawlist.ModelGeometry{}}
	draw := &render.UnitDraw{WorldPos: [3]numeric.Fixed{16 * numeric.FixedOne, 5 * numeric.FixedOne, 16 * numeric.FixedOne}}
	c.setModelLightingHeight(g, draw)
	// A submerged origin remains eligible for its above-water pieces.
	for _, packet := range []*drawlist.ModelGeometry{g, g.Supersample, g.Clone()} {
		if !packet.ReflectWater || packet.WorldHeight != 10 || packet.ReflectionSea != 20 {
			t.Fatalf("reflection packet lost physical scale: %+v", packet)
		}
	}
	// Reusing the same geometry must clear its water admission on dry ground.
	for i := range c.terrain.Plot {
		c.terrain.Plot[i].SetHeight(10)
	}
	c.setModelLightingHeight(g, draw)
	if g.ReflectWater || g.Supersample.ReflectWater {
		t.Fatal("retained geometry kept reflection after reaching dry ground")
	}
}

func TestReflectionWaterRejectsInvalidAndHotTerrain(t *testing.T) {
	for _, name := range []string{"missing", "empty", "west", "edge", "dry", "lava", "damage"} {
		t.Run(name, func(t *testing.T) {
			c := reflectionScene(t)
			x, z := 16*numeric.FixedOne, 16*numeric.FixedOne
			switch name {
			case "missing":
				c.terrain = nil
			case "empty":
				c.terrain.Plot = nil
			case "west":
				x = -1
			case "edge":
				x = 48 * numeric.FixedOne
			case "dry":
				for i := range c.terrain.Plot {
					c.terrain.Plot[i].SetHeight(10)
				}
			case "lava":
				c.terrain.LavaWorld = true
			case "damage":
				c.terrain.WaterDoesDamage, c.terrain.WaterDamage = 1, 5
			}
			if c.reflectionWaterAt(x, z) {
				t.Fatal("invalid or nonordinary water admitted")
			}
		})
	}
}

func TestReflectionBeamEndpointHeightsFollowStrokeOrder(t *testing.T) {
	c := reflectionScene(t)
	v := frame.ProjectileView{X: 16 * numeric.FixedOne, Y: 15 * numeric.FixedOne, Z: 16 * numeric.FixedOne, TailX: -numeric.FixedOne, TailY: 8 * numeric.FixedOne, TailZ: 16 * numeric.FixedOne}
	c.resetListForTest()
	c.drawProjectileBeam(render.ProjectileDraw{Color: 7, Color2: 8}, v)
	clone := c.list.Clone()
	var lines []drawlist.Line
	clone.VisitLines(func(line drawlist.Line) { lines = append(lines, line) })
	if len(lines) != 2 || !lines[0].ReflectWater || !lines[1].ReflectWater {
		t.Fatalf("beam crossing water was not admitted: %+v", lines)
	}
	// Both strokes follow the sorted major-axis endpoints. Pixel offsets
	// change geometry alone, never the committed world heights.
	for _, line := range lines {
		if line.ReflectionHeight0 != -4 || line.ReflectionHeight1 != 10 || line.WorldHeight0 != 16 || line.WorldHeight1 != 30 {
			t.Fatalf("beam heights did not follow sorted endpoints: %+v", line)
		}
	}
	c.resetListForTest()
	d := render.ProjectileDraw{Segments: []render.ProjectilePoint{{X: v.X, Y: v.Y, Z: v.Z}, {X: v.TailX, Y: v.TailY, Z: v.TailZ}}}
	d.Segments2 = d.Segments
	c.drawProjectileSegments(d)
	c.drawProjectileSegmentsSecond(d)
	c.list.VisitLines(func(line drawlist.Line) {
		if !line.ReflectWater || line.ReflectionHeight0 != 10 || line.ReflectionHeight1 != -4 {
			t.Fatalf("segment lost endpoint heights: %+v", line)
		}
	})
}

func TestReflectionSpriteAdmissionExcludesShadowAndHiddenProjectile(t *testing.T) {
	c := reflectionScene(t)
	art := &formats.GAFFrame{Width: 1, Height: 1, Pixels: []byte{7}}
	opts := render.ProjectileDispatchOptions{
		FrameCount: func(frame.ProjectileView) (int, bool) { return 1, true },
		ResolveGAF: func(render.ProjectileGAFRequest) (*formats.GAFFrame, bool) { return art, true },
	}
	views := []frame.ProjectileView{{Handle: 1, RenderType: render.RenderTypeSelectorGAF, X: 16 * numeric.FixedOne, Y: 15*numeric.FixedOne + numeric.FixedOne/4, Z: 16 * numeric.FixedOne, FloorHeightValid: true}}
	for _, visible := range []bool{true, false} {
		c.resetListForTest()
		c.DrawProjectileViews(views, 1, func(frame.ProjectileView) bool { return visible }, nil, opts)
		clone := c.list.Clone()
		var sprites []drawlist.Sprite
		clone.VisitSprites(func(sprite drawlist.Sprite) { sprites = append(sprites, sprite) })
		if !visible {
			if len(sprites) != 0 {
				t.Fatal("hidden projectile emitted reflection source")
			}
			continue
		}
		if len(sprites) != 2 || sprites[0].ReflectWater || !sprites[1].ReflectWater || sprites[1].ReflectionHeight != 10.5 {
			t.Fatalf("sprite and ground shadow reflection metadata: %+v", sprites)
		}
	}
}
