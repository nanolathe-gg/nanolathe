package client

import (
	"bytes"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	compiledmodel "github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	presentationrender "github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// Structure shadows exclude live pieces even during construction, while their bounds
// still include every visible vertex [03 R-REN-03D §2][03 R-REN-03A §1].
func TestStructureShadowExcludesLivePieces(t *testing.T) {
	c := compositionClient(t)
	draw := testPrimitiveDraw(presentationrender.PrimitiveDraw{IsColored: 1, ColorIndex: 7, VertexIndices: []uint16{0, 1, 2, 3}}, [][3]numeric.Fixed{fixedVertex(0, 0, 0), fixedVertex(8, 0, 0), fixedVertex(8, 0, -8), fixedVertex(0, 0, -8)})
	draw.Structure, draw.CastsShadow = true, true
	draw.Model.Pieces = append(draw.Model.Pieces, compiledmodel.Piece{Name: "live"})
	live := draw.Pieces[0]
	live.DontCache = true
	live.WorldVertices = append([][3]numeric.Fixed(nil), live.WorldVertices...)
	for i := range live.WorldVertices {
		live.WorldVertices[i][0] += numeric.Fixed(24 << 16)
	}
	draw.Pieces = append(draw.Pieces, live)
	for _, construction := range []bool{false, true} {
		draw.UnderConstruction = construction
		polys := c.collectShadowPolys(draw)
		if len(polys) != 1 || polys[0].piece != 0 {
			t.Fatalf("construction=%v: shadow includes live piece: %+v", construction, polys)
		}
		shadow := c.buildModelShadow(draw, nil)
		if shadow == nil || shadow.width != 36 {
			t.Fatalf("construction=%v: shadow bounds dropped the visible live vertices", construction)
		}
		g := c.modelShadowGeometry(draw)
		if g == nil || len(g.Faces) != 1 || g.Width != 36 {
			t.Fatalf("construction=%v: shared shadow geometry differs: %+v", construction, g)
		}
	}
}

// A material-less ring still gets an outline, and selection/unreferenced
// vertices still determine the composition allocation [03 R-REN-03A §1/§5].
func TestModelExtentPrecedesMaterialDispatch(t *testing.T) {
	for _, flat := range []bool{false, true} {
		c := compositionClient(t)
		draw := testPrimitiveDraw(presentationrender.PrimitiveDraw{VertexIndices: []uint16{0, 1, 2, 3}}, [][3]numeric.Fixed{fixedVertex(0, 0, 0), fixedVertex(24, 0, 0), fixedVertex(24, 0, -24), fixedVertex(0, 0, -24), fixedVertex(-7, 0, -31)})
		draw.KeyPlane = true
		if flat {
			draw.Pieces[0].Primitives = append(draw.Pieces[0].Primitives, presentationrender.PrimitiveDraw{IsColored: 1, ColorIndex: 7, VertexIndices: []uint16{0, 1, 2}})
		}
		reveal := presentationRevealKeep()
		m, ok := c.composeModel(draw, 0, teamColor{}, 0, modelCursorUnit, &reveal, 170)
		if !ok || m.image.width != 35 || m.image.heightPx != 35 || m.image.originX != 9 {
			t.Fatalf("flat=%v: incorrect vertex bounds: %+v", flat, m.image)
		}
		if got := m.image.color[int(m.image.originY+12)*m.image.width+int(m.image.originX+24)]; got != 170 {
			t.Fatalf("flat=%v: outer clear-face outline=%d, want 170", flat, got)
		}
		g := c.prepareModelGeometry(draw, 0, teamColor{}, 0, modelCursorUnit, &reveal, 170)
		if g == nil || g.Width != 35 || g.Height != 35 || len(g.Outline) == 0 {
			t.Fatalf("flat=%v: missing outline-only geometry: %+v", flat, g)
		}
	}
}

// Reveal uses the resolved image and its nearest-sampled key; recolouring the
// doubled raster first would blend pulse colours into its edges [03 R-P0-19-N].
func TestNanoframeRevealFollowsSupersampleResolve(t *testing.T) {
	c := compositionClient(t)
	c.antiAlias = true
	draw := testPrimitiveDraw(presentationrender.PrimitiveDraw{IsColored: 1, ColorIndex: 7, VertexIndices: []uint16{0, 1, 2}}, [][3]numeric.Fixed{fixedVertex(0, 1, 0), fixedVertex(9, 1, 0), fixedVertex(0, 1, -9)})
	draw.Structure, draw.KeyPlane = true, true
	raw, ok := c.composeModel(draw, 0, teamColor{}, 0, modelCursorUnit, nil, 0)
	if !ok || raw.raster.scale != 2 {
		t.Fatal("fixture must use doubled scratch")
	}
	reveal := presentationrender.NanoframeReveal{Below: 160, Band: 160, Above: 160}
	c.revealModelImage(raw.image, draw, &reveal, 170)
	actual, ok := c.composeModel(draw, 0, teamColor{}, 0, modelCursorUnit, &reveal, 170)
	if !ok || !bytes.Equal(actual.image.color, raw.image.color) || !bytes.Equal(actual.image.height, raw.image.height) {
		t.Fatal("nanoframe differs from post-resolve reveal")
	}
}

// A child's waterline and Digger do not run before composition; its carrier
// alone finalizes the staging union [03 R-REN-03A §4][03 R-RAST-01 §7-A].
func TestChildDefersWaterlineAndDiggerToCarrier(t *testing.T) {
	for _, retained := range []bool{false, true} {
		for _, digger := range []bool{false, true} {
			c, v := cachedLiveRegressionSubject(t)
			c.recordModelGeometry = true
			c.antiAlias = false
			c.pal = &palette.Tables{}
			c.modelScratch.active = retained
			c.pal.Blue[99], c.pal.Blue[31] = 200, 201
			for row := range c.pal.Shade {
				for color := range c.pal.Shade[row] {
					c.pal.Shade[row][color] = byte(color)
				}
			}
			v.Y = numeric.Fixed(-10 << 16)
			v.Digger = digger
			m, ok := c.composeChildModel(v)
			if !ok || bytes.Count(m.image.color, []byte{99})+bytes.Count(m.image.color, []byte{31}) == 0 {
				t.Fatalf("retained=%v digger=%v: child was prematurely tinted/erased", retained, digger)
			}
			if m.geometry != nil && (m.geometry.Waterline != drawlist.ModelWaterlineNone || m.geometry.Digger) {
				t.Fatal("child geometry retained its own final passes")
			}
			// A carrier exactly on the surface with no Digger keeps this raw child.
			carrier := *m.draw
			carrier.WorldPos[1], carrier.DiggerClip = 0, false
			c.finalizeModelImage(m.image, &carrier, v.Owner, modelCursorUnit)
			if bytes.Count(m.image.color, []byte{99})+bytes.Count(m.image.color, []byte{31}) == 0 {
				t.Fatal("surface carrier erased its child")
			}
		}
	}
}

func TestCarrierGeometryDefersChildFinalPasses(t *testing.T) {
	c, carrier := cachedLiveRegressionSubject(t)
	c.geometryOnlyModels = true
	c.pal = &palette.Tables{}
	c.modelScratch.active = true
	child := carrier
	child.InstanceID++
	child.Y = numeric.Fixed(-10 << 16)
	child.Digger = true
	if !c.composeCarrier(carrier, 0, 0, []frame.UnitView{child}) {
		t.Fatal("carrier missing")
	}
	commands := c.list.ModelCommands()
	g := commands[len(commands)-1].Geometry
	if g == nil || len(g.Children) != 1 {
		t.Fatal("child missing")
	}
	ch := g.Children[0].Geometry
	if ch.Waterline != drawlist.ModelWaterlineNone || ch.Digger {
		t.Fatal("staged GPU child performs its own final passes")
	}
}

func TestRetainedOutlineOnlyModelKeepsItsVertexBounds(t *testing.T) {
	c, v := cachedLiveRegressionSubject(t)
	c.geometryOnlyModels = true
	c.modelScratch.active = true
	v.BuildRemaining = 1
	for i := range c.models[v.Model].compiled.Pieces {
		for j := range c.models[v.Model].compiled.Pieces[i].Primitives {
			c.models[v.Model].compiled.Pieces[i].Primitives[j].IsColored = 0
		}
	}
	first, _ := c.unitGeometryPair(v, false)
	if first == nil || len(first.Faces) != 0 || len(first.Outline) == 0 {
		t.Fatal("outline-only retained body missing")
	}
	width := first.Width
	c.modelScratch.reset()
	c.modelScratch.active = true
	next, _ := c.unitGeometryPair(v, false)
	if next == nil || next.Width != width || len(next.Outline) == 0 {
		t.Fatal("retained outline-only model changed bounds")
	}
}

func TestStandaloneNanoframeMatchesRetainedReveal(t *testing.T) {
	c, v := cachedLiveRegressionSubject(t)
	c.antiAlias = true
	c.pal = compositionClient(t).pal
	v.BuildRemaining = 0.8
	standalone, ok := c.composeUnitModel(v)
	if !ok {
		t.Fatal("standalone missing")
	}
	want := append([]byte(nil), standalone.image.color...)
	c.modelScratch.active = true
	retained, ok := c.composeUnitModel(v)
	if !ok || !bytes.Equal(retained.image.color, want) {
		t.Fatal("standalone and retained nanoframe reveal differ")
	}
}
