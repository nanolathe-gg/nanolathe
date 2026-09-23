package client

import (
	"github.com/nanolathe-gg/nanolathe/formats"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	presentationrender "github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// The visible roof winding is the ring locked by model_winding_test.go.
// Enhanced light must face upward even though retail SHD uses the inverse
// face-normal convention (DESIGN_GPU_RENDERER §22.4).
func TestLightingNormalFacesOutwardFromVisibleRoof(t *testing.T) {
	verts := [][3]numeric.Fixed{fixedVertex(0, 0, 0), fixedVertex(4, 0, 0), fixedVertex(4, 0, -4), fixedVertex(0, 0, -4)}
	if got := modelLightingNormal(verts, []uint16{0, 1, 2, 3}); got != [3]float32{0, 0, 1} {
		t.Fatalf("roof outward normal = %v", got)
	}
	if got := modelLightingNormal(verts, []uint16{3, 2, 1, 0}); got != [3]float32{0, 0, -1} {
		t.Fatalf("reverse roof normal = %v", got)
	}
}

func TestLightingRetainedHeightRefreshDoesNotDoublePhysicalMetadata(t *testing.T) {
	c := testModelTextureClient()
	c.enhanced = true
	c.cam.Scale = camera.ViewScaleDetail
	c.modelScratch.active = true
	p := newScreenPoly(3)
	p.normal, p.heights[0] = [3]float32{0, 0, 1}, 3.5
	p.x[1], p.y[2] = 4, 4
	src := modelGeometryPacketAt([]screenPoly{p}, 10, 10, 2, 2, 0, 0, 1, true, drawlist.ModelFallbackNone)
	src.Supersample = fillModelPacket(&drawlist.ModelGeometry{}, nil, []screenPoly{p}, 20, 20, 4, 4, 0, 0, 2, true, drawlist.ModelFallbackNone, &doubledPlacement{originX: 2, originY: 2, oddPx: 1})
	src.WorldHeight = 2
	g := c.borrowRebasedModelGeometry(src, 14, 14, 4, 4, 50, 60, 1, 0)
	draw := &presentationrender.UnitDraw{WorldPos: fixedVertex(0, 11, 0)}
	c.setModelLightingHeight(g, draw)
	for _, lane := range []*drawlist.ModelGeometry{g, g.Supersample} {
		if lane.WorldHeight != 22 || lane.Faces[0].Vertices[0].Height != 3.5 || lane.Faces[0].Normal != p.normal {
			t.Fatalf("rebase changed physical metadata: %+v", lane)
		}
	}
	if src.WorldHeight != 2 || src.Faces[0].Vertices[0].X != 0 {
		t.Fatal("rebase mutated retained geometry")
	}
}

func TestLightingModelHeightUsesRecordScaleBeforeSupersampling(t *testing.T) {
	c := testModelTextureClient()
	c.enhanced, c.recordModelGeometry = true, true
	c.cam.Scale = camera.ViewScaleDetail
	draw := testPrimitiveDraw(presentationrender.PrimitiveDraw{IsColored: 1, ColorIndex: 7, VertexIndices: []uint16{0, 1, 2}},
		[][3]numeric.Fixed{fixedVertex(0, 12, 0), fixedVertex(4, 12, 0), fixedVertex(4, 12, -4)})
	draw.WorldPos = fixedVertex(0, 10, 0)
	polys := c.collectDrawPolys(draw, teamColor{}, 1, modelCursorUnit)
	if len(polys) != 1 || polys[0].heights[0] != 4 || polys[0].normal != [3]float32{0, 0, 1} {
		t.Fatalf("relative height/normal did not survive projection: %+v", polys)
	}
}

func TestLightingExplosionRequiresVisibleLiveExplicitProducer(t *testing.T) {
	v := frame.EffectView{Kind: frame.KindExplosion.String(), ActiveA: true, X: numeric.FixedFromInt(20), Z: numeric.FixedFromInt(20)}
	for _, kind := range []string{frame.KindExplosion.String(), frame.KindImpact.String(), frame.KindWaterImpact.String()} {
		v.Kind = kind
		if !visibleExplosionSource(v, stripTestFrame(true)) {
			t.Fatalf("visible live %s was excluded", kind)
		}
		if visibleExplosionSource(v, stripTestFrame(false)) || visibleExplosionSource(v, nil) || visibleExplosionSource(v, &frame.Frame{}) {
			t.Fatalf("hidden or unknown visibility admitted %s", kind)
		}
	}
	v.ActiveA = false
	if visibleExplosionSource(v, stripTestFrame(true)) {
		t.Fatal("finished primary layer emitted light")
	}
	v.ActiveA, v.Kind = true, frame.KindSmokeStart.String()
	if visibleExplosionSource(v, stripTestFrame(true)) {
		t.Fatal("smoke event became an explosion source")
	}
}

func TestLightingSmokeIdentityComesFromStripFamily(t *testing.T) {
	c := stripTestClient(t)
	for _, family := range []frame.StripFamily{frame.StripFamilySmokePuff, frame.StripFamilyVentSteam, frame.StripFamilyFlameTrail} {
		// Deliberately use identical smoke art: the producer family alone decides.
		c.blitStripFrame(frame.StripView{Family: family, Bank: "fx", Entry: "smoke 1", Y: numeric.FixedFromInt(6)})
	}
	var kinds []drawlist.SpriteLightingKind
	c.list.VisitSprites(func(s drawlist.Sprite) {
		kinds = append(kinds, s.LightingKind)
		if s.WorldHeight != 6 || s.LightingScale != 1 {
			t.Fatal("strip physical height/scale was lost")
		}
	})
	// The two puff families are receivers and the trail family is a spark
	// emitter, from the producer family alone — the art is identical
	// (§23.1, §31, §31.7).
	if len(kinds) != 3 || kinds[0] != drawlist.SpriteLightingSmoke || kinds[1] != drawlist.SpriteLightingSmoke || kinds[2] != drawlist.SpriteLightingSpark {
		t.Fatalf("strip identities = %v", kinds)
	}
}

func TestLightingNamedSourceRequiresResolvedVisibleArt(t *testing.T) {
	for _, tc := range []struct {
		name, graphic   string
		visible, active bool
		wantSprites     int
		wantKind        drawlist.SpriteLightingKind
	}{
		{"visible", "smoke 1", true, true, 1, drawlist.SpriteLightingExplosion},
		{"hidden art still draws", "smoke 1", false, true, 1, drawlist.SpriteLightingNone},
		{"missing art", "absent", true, true, 0, drawlist.SpriteLightingNone},
		{"finished art", "smoke 1", true, false, 0, drawlist.SpriteLightingNone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := stripTestClient(t)
			options := c.effectDrawOptions()
			options.LightingFrame = stripTestFrame(tc.visible)
			v := frame.EffectView{Kind: frame.KindExplosion.String(), Graphic: tc.graphic, AssetID: "fx", ActiveA: tc.active, X: numeric.FixedFromInt(20), Z: numeric.FixedFromInt(20)}
			c.DrawEffectViews([]frame.EffectView{v}, options)
			count := 0
			c.list.VisitSprites(func(s drawlist.Sprite) {
				count++
				if s.LightingKind != tc.wantKind {
					t.Fatalf("source kind = %v, want %v", s.LightingKind, tc.wantKind)
				}
			})
			if count != tc.wantSprites {
				t.Fatalf("sprites = %d, want %d", count, tc.wantSprites)
			}
		})
	}
}

func TestCompositeExplosionRecordsOneRootSource(t *testing.T) {
	c := stripTestClient(t)
	leaf := &formats.GAFFrame{Width: 1, Height: 1, Pixels: []byte{3}}
	root := &formats.GAFFrame{Width: 1, Height: 1, Subframes: []*formats.GAFFrame{leaf, leaf}}
	c.emitSprite(drawlist.Sprite{Frame: root, Kind: drawlist.BlitKeyed, Anchored: true, LightingKind: drawlist.SpriteLightingExplosion})
	sources, sprites := 0, 0
	c.list.VisitLightSources(func(s drawlist.Sprite) {
		sources++
		if s.Frame != root {
			t.Fatal("lost root art")
		}
	})
	c.list.VisitSprites(func(s drawlist.Sprite) {
		sprites++
		if s.LightingKind == drawlist.SpriteLightingExplosion {
			t.Fatal("leaf duplicates emission")
		}
	})
	if sources != 1 || sprites != 2 {
		t.Fatalf("sources %d sprites %d", sources, sprites)
	}
}

// The prototype clock and size travel with the list, independent of replay and
// the current animation frame's changing bounds (GPU design §25).
func TestBlastMetadataUsesAdmissionAgeAndAuthoredSize(t *testing.T) {
	c := stripTestClient(t)
	c.enhanced, c.interpolation = true, true
	c.SetTickFraction(0.5)
	c.cam.Scale = camera.ViewScaleDetail
	options := c.effectDrawOptions()
	options.LightingFrame = stripTestFrame(true)
	options.LightingFrame.Tick = 105
	c.effectBanks = map[string]*formats.GAF{"fx": {Entries: []formats.GAFEntry{{Name: "blast", Frames: []formats.GAFFrameRef{
		{Frame: &formats.GAFFrame{Width: 4, Height: 3, Pixels: make([]byte, 12)}},
		{Frame: &formats.GAFFrame{Width: 80, Height: 60}},
		{Frame: &formats.GAFFrame{Width: 16, Height: 16, Pixels: make([]byte, 256)}},
	}}}}}

	v := frame.EffectView{HasBlastProfile: true, BlastAreaOfEffect: 110, BlastDamage: 350, Kind: frame.KindExplosion.String(), Graphic: "blast", ActiveA: true, StartTick: 100, SeqA: 2, X: numeric.FixedFromInt(20), Z: numeric.FixedFromInt(20)}
	c.DrawEffectViews([]frame.EffectView{v}, options)
	count := 0
	c.list.VisitLightSources(func(sp drawlist.Sprite) {
		count++
		if !sp.HasBlastProfile || sp.BlastAreaOfEffect != 110 || sp.BlastDamage != 350 {
			t.Fatalf("lost authored blast metadata: %+v", sp)
		}
		if !sp.HasLightingAge || sp.LightingAge != 5.5 {
			t.Fatalf("lighting age = %v, known=%v", sp.LightingAge, sp.HasLightingAge)
		}
		if sp.BlastAge != 5.5 || sp.BlastSize != 80 || sp.LightingSize != 80 || sp.LightingScale != 2 {
			t.Fatalf("blast age/size/scale = %v/%v/%v", sp.BlastAge, sp.BlastSize, sp.LightingScale)
		}
	})
	if count != 1 {
		t.Fatalf("sources = %d", count)
	}
	// Disabling refraction must not defer illumination to the larger late
	// frames. The source extent stays in native units at either record scale.
	c.effects.Distortion = false
	for _, scale := range []camera.ViewScale{camera.ViewScaleNative, camera.ViewScaleDetail} {
		c.cam.Scale = scale
		for _, seq := range []int32{0, 2} {
			c.list.Reset()
			v.SeqA = seq
			c.DrawEffectViews([]frame.EffectView{v}, options)
			count = 0
			c.list.VisitLightSources(func(sp drawlist.Sprite) {
				count++
				if !sp.HasLightingAge || sp.LightingAge != 5.5 || sp.LightingSize != 80 || sp.BlastSize != 0 || sp.BlastAge != 0 {
					t.Fatalf("Distortion off changed lighting extent: %+v", sp)
				}
			})
			if count != 1 {
				t.Fatalf("scale %v frame %d lost explosion source", scale, seq)
			}
		}
	}
}

// borrowRebasedModelGeometry copies the retained cached lane into this frame's
// packet arena. The retained entry remains immutable while the output can move
// into the current union box and acquire its current live lane.
//
// hx and hy are this frame's half-pixel offset (modelAnchorDoubled); the
// retained doubled lane carries none, so it is added to the doubled corners
// here.
func (c *Client) borrowRebasedModelGeometry(src *drawlist.ModelGeometry, width, height, originX, originY, anchorX, anchorY, hx, hy int32) *drawlist.ModelGeometry {
	return c.rebaseRetained(nil, src, width, height, originX, originY, anchorX, anchorY, hx, hy)
}
