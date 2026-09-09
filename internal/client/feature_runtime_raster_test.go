package client

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
)

type runtimeSpriteCollector struct{ sprites []drawlist.Sprite }

func (s *runtimeSpriteCollector) Clear()                   {}
func (s *runtimeSpriteCollector) Terrain(drawlist.Terrain) {}
func (s *runtimeSpriteCollector) Sprite(v drawlist.Sprite) { s.sprites = append(s.sprites, v) }
func (s *runtimeSpriteCollector) Glyphs(drawlist.Glyphs)   {}
func (s *runtimeSpriteCollector) Fill(drawlist.Fill)       {}
func (s *runtimeSpriteCollector) Line(drawlist.Line)       {}
func (s *runtimeSpriteCollector) Points(drawlist.Points)   {}
func (s *runtimeSpriteCollector) Model(drawlist.Model)     {}
func (s *runtimeSpriteCollector) Fog(drawlist.Fog)         {}
func (s *runtimeSpriteCollector) Surface(drawlist.Surface) {}
func (s *runtimeSpriteCollector) Cursor(drawlist.Cursor)   {}
func (s *runtimeSpriteCollector) Expand()                  {}

func recordedRuntimeFeatureSprites(c *Client) []drawlist.Sprite {
	collector := &runtimeSpriteCollector{}
	c.list.Replay(collector)
	return collector.sprites
}

type runtimeRasterCapture struct {
	name   string
	pixels []byte
}

// writeRuntimeRasterCapture is an opt-in visual QA artifact. Each panel is the
// actual replayed indexed surface enlarged with nearest-neighbour pixels; no
// draw-list command is used as a substitute for the final raster result.
func writeRuntimeRasterCapture(path string, captures []runtimeRasterCapture, width, height int) error {
	const scale = 24
	imageOut := image.NewRGBA(image.Rect(0, 0, len(captures)*width*scale, height*scale))
	for panel, capture := range captures {
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				index := capture.pixels[y*width+x]
				pixel := color.RGBA{R: index, G: index, B: index, A: 0xff}
				for dy := 0; dy < scale; dy++ {
					for dx := 0; dx < scale; dx++ {
						imageOut.Set(panel*width*scale+x*scale+dx, y*scale+dy, pixel)
					}
				}
			}
		}
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return png.Encode(file, imageOut)
}

func TestFeatureRuntimeRasterRoute(t *testing.T) {
	c, _, _ := newFeatureRasterClient(t)
	var captures []runtimeRasterCapture
	for _, tc := range []struct {
		name             string
		view             frame.FeatureView
		featureShadows   bool
		wantKinds        []drawlist.BlitKind
		wantTranslucency []bool
	}{
		{
			name: "static cursor retains authored blitters",
			view: func() frame.FeatureView {
				v := featureRasterView()
				v.SeqName = "transparent-body"
				v.ShadTrans, v.AnimTrans = true, true
				return v
			}(),
			featureShadows:   true,
			wantKinds:        []drawlist.BlitKind{drawlist.BlitFeatureShadow, drawlist.BlitFeatureNormal},
			wantTranslucency: []bool{true, true},
		},
		{
			name: "live record without event cursor has no rest fallback",
			view: func() frame.FeatureView {
				v := featureRasterView()
				v.SeqName = "body"
				v.RuntimeLive, v.ShadowEnabled = true, true
				return v
			}(),
			featureShadows: true,
		},
		{
			name: "runtime shadow bit gates shadow while body remains opaque",
			view: func() frame.FeatureView {
				v := featureRasterView()
				v.RuntimeLive = true
				v.EventSeqName, v.EventSeqNameShad = "event-body", "event-shadow"
				v.ShadTrans, v.AnimTrans = true, true
				return v
			}(),
			featureShadows:   true,
			wantKinds:        []drawlist.BlitKind{drawlist.BlitFeatureNormal},
			wantTranslucency: []bool{false},
		},
		{
			name: "runtime opaque shadow needs record bit and preference",
			view: func() frame.FeatureView {
				v := featureRasterView()
				v.RuntimeLive, v.ShadowEnabled = true, true
				v.EventSeqName, v.EventSeqNameShad = "transparent-body", "event-shadow"
				return v
			}(),
			featureShadows:   true,
			wantKinds:        []drawlist.BlitKind{drawlist.BlitFeatureShadow, drawlist.BlitFeatureNormal},
			wantTranslucency: []bool{false, false},
		},
		{
			name: "runtime shadow preference suppresses an enabled record shadow",
			view: func() frame.FeatureView {
				v := featureRasterView()
				v.RuntimeLive, v.ShadowEnabled = true, true
				v.EventSeqName, v.EventSeqNameShad = "transparent-body", "event-shadow"
				return v
			}(),
			featureShadows:   false,
			wantKinds:        []drawlist.BlitKind{drawlist.BlitFeatureNormal},
			wantTranslucency: []bool{false},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c.SetFeatureShadows(tc.featureShadows)
			resetFeatureRaster(c)
			c.drawFeature(&tc.view)
			got := recordedRuntimeFeatureSprites(c)
			if len(got) != len(tc.wantKinds) {
				t.Fatalf("recorded %d sprites, want %d: %#v", len(got), len(tc.wantKinds), got)
			}
			for i := range got {
				if got[i].Kind != tc.wantKinds[i] || got[i].Trans != tc.wantTranslucency[i] {
					t.Fatalf("sprite %d = (%v, trans=%v), want (%v, trans=%v)", i, got[i].Kind, got[i].Trans, tc.wantKinds[i], tc.wantTranslucency[i])
				}
			}
			c.replayForTest()
			captures = append(captures, runtimeRasterCapture{name: tc.name, pixels: append([]byte(nil), c.indexed...)})
		})
	}
	if path := os.Getenv("NANOLATHE_FEATURE_RUNTIME_RASTER_PNG"); path != "" {
		if err := writeRuntimeRasterCapture(path, captures, c.width, c.height); err != nil {
			t.Fatalf("write runtime raster capture: %v", err)
		}
	}
}
