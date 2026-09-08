package gpurender

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/drawlist"
)

// Cache identity is presentation policy: a changed resolved appearance must
// never reuse stale pixels, while placement alone can reuse the same body.
func TestModelCacheAppearanceIdentity(t *testing.T) {
	texture := &formats.GAFFrame{Width: 1, Height: 1, Pixels: []byte{4}}
	g := fixtureGeometry(10, true, fixtureFace(0, 0, 4, 4, 20, 3))
	g.Faces[0].Texture = texture
	var cache modelImageCache
	want := cache.identity(g)
	moved := g.Clone()
	moved.AnchorX, moved.AnchorY, moved.OriginX, moved.OriginY = -40, 20, 2, 3
	moved.Shadow = fixtureGeometry(0, true, fixtureFace(0, 0, 2, 2, 10, 0))
	moved.Children = []drawlist.ModelChild{{Geometry: g.Clone()}}
	if got := cache.identity(moved); got != want {
		t.Fatal("placement, shadow or children invalidated the independent body")
	}
	for _, tc := range []struct {
		name   string
		change func(*drawlist.ModelGeometry)
	}{
		{"pose", func(g *drawlist.ModelGeometry) { g.Faces[0].Vertices[0].X++ }},
		{"key ownership", func(g *drawlist.ModelGeometry) { g.Faces[0].Vertices[0].Key++ }},
		{"texture coordinates", func(g *drawlist.ModelGeometry) { g.Faces[0].Vertices[0].U++ }},
		{"texture animation or team", func(g *drawlist.ModelGeometry) { f := *texture; g.Faces[0].Texture = &f }},
		{"shade", func(g *drawlist.ModelGeometry) { g.Faces[0].Shaded = true; g.Faces[0].Vertices[0].Shade++ }},
		{"construction", func(g *drawlist.ModelGeometry) { g.Reveal = &drawlist.ModelReveal{Line: 20, Above: -2} }},
		{"outline", func(g *drawlist.ModelGeometry) { g.Outline = append(g.Outline, fixtureFace(0, 0, 4, 4, 20, 5)) }},
		{"waterline", func(g *drawlist.ModelGeometry) { g.Waterline = drawlist.ModelWaterlineBlue; g.WaterlineKey = 20 }},
		{"digger", func(g *drawlist.ModelGeometry) { g.Digger = true; g.DiggerKey = 125 }},
		{"structure resolve", func(g *drawlist.ModelGeometry) { g.Supersample = g.Clone(); g.Supersample.Scale = 2 }},
		{"painter mode", func(g *drawlist.ModelGeometry) { g.KeyPlane = false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := g.Clone()
			tc.change(changed)
			if cache.identity(changed) == want {
				t.Fatal("changed appearance reused cached identity")
			}
		})
	}
}

// Runs inside the existing opt-in Metal/device loop. Every repeated frame is
// compared with a renderer whose one-byte budget forces fresh body rasterization.
// This covers keys beneath erased pixels, child order and fresh shadow ground
// as well as translated cached sprites and clipping at the viewport boundary.
func checkCachedModelFrames() error {
	pal := fixturePalette()
	cached, err := NewChecked(&pal, 80, 48)
	if err != nil {
		return err
	}
	fresh, err := NewChecked(&pal, 80, 48)
	if err != nil {
		return err
	}
	fresh.modelCache.budget = 1
	source := fixtureModelList()
	models := source.ModelCommands()
	for i := 0; i < 6; i++ {
		w, h := 80, 48
		if i > 1 {
			w, h = 128, 80
		}
		dx, dy := int32(0), int32(0)
		if i == 2 {
			dx, dy = 17, 11
		}
		if i == 3 {
			dx, dy = -5, -4
		}
		var l drawlist.List
		l.RecordClear()
		l.RecordFill(drawlist.Fill{Rect: drawlist.Rect{W: int32(w), H: int32(h)}, Index: uint8(7 + i), Style: drawlist.FillSolid})
		for _, original := range models {
			cmd := original
			cmd.Geometry = cmd.Geometry.Clone()
			moveCachedFixture(cmd.Geometry, dx, dy)
			if i == 4 && cmd.Geometry != nil {
				g := cmd.Geometry
				if g.Reveal != nil {
					g.Reveal.Line++
					g.Reveal.Above = 16
				}
				g.WaterlineKey++
				g.DiggerKey++
				for j := range g.Children {
					g.Children[j].KeyDelta++
				}
				if g.Supersample != nil {
					g.Supersample.Faces[0].Color++
				}
			}
			l.RecordModel(cmd)
		}
		l.RecordExpand()
		a, b := make([]byte, w*h*4), make([]byte, w*h*4)
		cached.Execute(&l, w, h).ReadPixels(a)
		fresh.Execute(&l, w, h).ReadPixels(b)
		if !bytes.Equal(a, b) {
			return fmt.Errorf("cached model frame %d differs from uncached rasterization", i)
		}
		s := cached.ModelStats()
		if i >= 1 && i <= 3 && (s.CacheHits == 0 || s.CacheMisses != 0 || s.RasterPixels != 0) {
			return fmt.Errorf("unchanged/translated frame %d rerasterized: %+v", i, s)
		}
		if i == 4 && s.CacheMisses == 0 {
			return fmt.Errorf("changed construction/clip/resolve inputs were not rebuilt")
		}
		if fresh.modelCache.bytes != 0 || fresh.ModelStats().CacheEvictions == 0 {
			return fmt.Errorf("oversized model entries survived eviction")
		}
	}
	return nil
}

func moveCachedFixture(g *drawlist.ModelGeometry, dx, dy int32) {
	if g == nil {
		return
	}
	g.AnchorX += dx
	g.AnchorY += dy
	moveCachedFixture(g.Shadow, dx, dy)
	for _, child := range g.Children {
		moveCachedFixture(child.Geometry, dx, dy)
	}
}
