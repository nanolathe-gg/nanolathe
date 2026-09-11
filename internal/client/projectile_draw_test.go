package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

type projectileLineCollector struct{ lines []drawlist.Line }

func (c *projectileLineCollector) Clear()                   {}
func (c *projectileLineCollector) Terrain(drawlist.Terrain) {}
func (c *projectileLineCollector) Sprite(drawlist.Sprite)   {}
func (c *projectileLineCollector) Glyphs(drawlist.Glyphs)   {}
func (c *projectileLineCollector) Fill(drawlist.Fill)       {}
func (c *projectileLineCollector) Line(v drawlist.Line)     { c.lines = append(c.lines, v) }
func (c *projectileLineCollector) Points(drawlist.Points)   {}
func (c *projectileLineCollector) Flash(drawlist.Flash)     {}
func (c *projectileLineCollector) Halo(drawlist.Halo)       {}
func (c *projectileLineCollector) Model(drawlist.Model)     {}
func (c *projectileLineCollector) Fog(drawlist.Fog)         {}
func (c *projectileLineCollector) Surface(drawlist.Surface) {}
func (c *projectileLineCollector) Cursor(drawlist.Cursor)   {}
func (c *projectileLineCollector) Expand()                  {}

func TestSegmentedProjectilePassesUsePrimaryPaletteColor(t *testing.T) {
	c := newTestClient(t)
	c.resetListForTest()
	d := render.ProjectileDraw{
		Color:  37,
		Color2: 201,
		Segments: []render.ProjectilePoint{
			{}, {X: numeric.FixedFromInt(10)},
		},
		Segments2: []render.ProjectilePoint{
			{}, {Z: numeric.FixedFromInt(10)},
		},
	}
	if got := c.drawProjectileSegments(d); got != 1 {
		t.Fatalf("first pass lines = %d, want 1", got)
	}
	if got := c.drawProjectileSegmentsSecond(d); got != 1 {
		t.Fatalf("second pass lines = %d, want 1", got)
	}
	collector := &projectileLineCollector{}
	c.list.Replay(collector)
	if len(collector.lines) != 2 {
		t.Fatalf("segmented draw lines = %d, want 2", len(collector.lines))
	}
	if collector.lines[0].X0 == collector.lines[1].X0 && collector.lines[0].Y0 == collector.lines[1].Y0 && collector.lines[0].X1 == collector.lines[1].X1 && collector.lines[0].Y1 == collector.lines[1].Y1 {
		t.Fatalf("passes did not retain distinct jitter geometry: %+v", collector.lines)
	}
	for i, line := range collector.lines {
		if got := line.Index; got != 37 {
			t.Fatalf("pass %d palette index = %d, want primary color 37 [06 R-WFX-01 §4]", i, got)
		}
	}
}

// Beam and segment strokes are the light sources of the Enhanced glow layer,
// so their records carry the emissive mark; the classic executor ignores it
// and the modern one reads it (docs/DESIGN_GPU_RENDERER.md §19).
func TestProjectileStrokesAreRecordedEmissive(t *testing.T) {
	c := newTestClient(t)
	c.resetListForTest()
	d := render.ProjectileDraw{
		Color:     37,
		Color2:    201,
		Segments:  []render.ProjectilePoint{{}, {X: numeric.FixedFromInt(10)}},
		Segments2: []render.ProjectilePoint{{}, {Z: numeric.FixedFromInt(10)}},
	}
	c.drawProjectileSegments(d)
	c.drawProjectileSegmentsSecond(d)
	if got := c.drawProjectileBeam(d, frame.ProjectileView{TailX: numeric.FixedFromInt(16)}); got != 2 {
		t.Fatalf("beam strokes = %d, want 2 (color2 stroke under color)", got)
	}
	collector := &projectileLineCollector{}
	c.list.Replay(collector)
	if len(collector.lines) != 4 {
		t.Fatalf("recorded lines = %d, want 4", len(collector.lines))
	}
	for i, line := range collector.lines {
		if !line.Emissive {
			t.Fatalf("line %d is not marked emissive: %+v", i, line)
		}
	}
}
