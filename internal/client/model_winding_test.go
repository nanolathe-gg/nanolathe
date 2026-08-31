package client

import (
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/internal/camera"

	compiledmodel "github.com/nanolathe/nanolathe/internal/model"
	presentationrender "github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/testsupport"
	"github.com/nanolathe/nanolathe/vfs"
)

// TestWindingCullKeepsClockwiseRings locks the sense of the cull
// [R-RAST-01 §1] step 7: retail's scan converter walks decreasing indices from
// the top corner as the left chain and increasing indices as the right chain,
// and writes a span only where `xr > xl`. A ring that projects clockwise with
// screen Y increasing downward paints; the same ring reversed produces an
// empty span on every row and paints nothing.
//
// The sign is the whole contract and it is one character wide, so it is the
// kind of thing that regresses silently: inverting it hides every outward face
// behind the interior faces it should occlude.
func TestWindingCullKeepsClockwiseRings(t *testing.T) {
	v := func(x, y, z int64) [3]numeric.Fixed {
		return [3]numeric.Fixed{numeric.Fixed(x << 16), numeric.Fixed(y << 16), numeric.Fixed(z << 16)}
	}
	// modelLocalVertex projects to (x, -z - y/2), so this ring reads
	// (0,0) (4,0) (4,4) (0,4) on screen — clockwise with Y downward.
	vertices := [][3]numeric.Fixed{v(0, 0, 0), v(4, 0, 0), v(4, 0, -4), v(0, 0, -4)}
	var origin [3]numeric.Fixed

	if !modelFacePaints(vertices, []uint16{0, 1, 2, 3}, origin) {
		t.Fatal("clockwise ring was culled; retail's right chain is to the right of its left chain here")
	}
	if modelFacePaints(vertices, []uint16{3, 2, 1, 0}, origin) {
		t.Fatal("counter-clockwise ring painted; retail's span comparison is empty on every row")
	}
	if !modelFacePaints(vertices, []uint16{0, 1, 2}, origin) {
		t.Fatal("clockwise triangle was culled")
	}
	if modelFacePaints(vertices, []uint16{0, 2, 1}, origin) {
		t.Fatal("counter-clockwise triangle painted")
	}
	// Both chains are the same single edge for a two-corner primitive, so
	// `xr == xl` on every row [R-RAST-01 §1] step 7.
	if modelFacePaints(vertices, []uint16{0, 1}, origin) {
		t.Fatal("two-corner primitive painted")
	}
	// A degenerate ring encloses no rows at all.
	if modelFacePaints([][3]numeric.Fixed{v(0, 0, 0), v(1, 0, -1), v(2, 0, -2)}, []uint16{0, 1, 2}, origin) {
		t.Fatal("collinear ring painted")
	}
	// A malformed index suppresses the whole face [fmt 3do].
	if modelFacePaints(vertices, []uint16{0, 1, 9}, origin) {
		t.Fatal("out-of-range index painted")
	}
}

// TestCollectDrawTrisAppliesTheWindingCull checks the cull reaches the
// triangle stream, not just the predicate.
func TestCollectDrawTrisAppliesTheWindingCull(t *testing.T) {
	c := testModelTextureClient()
	front := [][3]numeric.Fixed{
		fixedVertex(0, 0, 0), fixedVertex(4, 0, 0), fixedVertex(4, 0, -4), fixedVertex(0, 0, -4),
	}
	pr := presentationrender.PrimitiveDraw{IsColored: 1, ColorIndex: 56, VertexIndices: []uint16{0, 1, 2, 3}}
	if got := len(c.collectDrawTris(testPrimitiveDraw(pr, front), 0, 1, modelCursorUnit)); got != 2 {
		t.Fatalf("front face emitted %d triangles, want 2", got)
	}
	pr.VertexIndices = []uint16{3, 2, 1, 0}
	if got := len(c.collectDrawTris(testPrimitiveDraw(pr, front), 0, 1, modelCursorUnit)); got != 0 {
		t.Fatalf("back face emitted %d triangles, want 0", got)
	}
}

// TestRetailFlapKeepsItsOuterSkin is the defect this cull was found through
// (playtest PT3-03). `ARMCK`'s two shoulder flaps carry a grey ribbed outer
// face (`noise3a` / `noise3b`) and a team-coloured interior face that is
// coplanar with it and stored at a later primitive index. The height key's
// equal-key tie-break gives the later face the pixel ([R-REN-03A §2]), so with
// both sides drawn the whole flap composed solid team colour and the unit read
// as a blue blob instead of a grey body with blue trim.
//
// The assertion is the relationship — the ribbed skin owns more of the flap
// than the team faces do — not a pixel census, so it survives palette and
// texture-cache changes and fails on a reintroduced double-sided raster.
func TestRetailFlapKeepsItsOuterSkin(t *testing.T) {
	root := testsupport.RetailRoot(t)
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Skipf("mount failed: %v", err)
	}
	defer fs.Close()

	c := &Client{
		width: 1, height: 1, indexed: make([]uint8, 1),
		modelFS: fs, texIndex: map[string]texRef{}, models: map[string]*unitModel{},
		cam: &camera.Camera{ViewW: 1, ViewH: 1, MapW: 4096, MapH: 4096},
	}
	c.buildTextureIndex()
	m := c.unitModelFor("ARMCK")
	if m == nil || m.compiled == nil {
		t.Skip("ARMCK is not in this install")
	}
	flaps := map[int]bool{}
	for i, piece := range m.compiled.Pieces {
		if name := strings.ToLower(piece.Name); name == "lflap" || name == "rflap" {
			flaps[i] = true
		}
	}
	if len(flaps) != 2 {
		t.Skipf("ARMCK in this install has %d flap pieces", len(flaps))
	}

	draw := presentationrender.BuildUnitDrawSimple(m.compiled, make([]compiledmodel.PieceState, len(m.compiled.Pieces)), 0, 0, 0, [3]numeric.Fixed{})
	draw.KeyPlane = true
	tris := c.collectDrawTris(draw, 0, 1, modelCursorUnit)
	if len(tris) == 0 {
		t.Fatal("ARMCK composed no triangles")
	}
	width, height, originX, originY := modelExtent(tris)
	placeTris(tris, originX, originY, 1)
	target := newModelImage(width, height, originX, originY, 0, 0, true, 1)

	owner := make([]int, width*height)
	for i := range owner {
		owner[i] = -1
	}
	previous := make([]uint8, len(target.color))
	copy(previous, target.color)
	for i := range tris {
		if tris[i].frame != nil {
			c.blitTexturedTriTarget(target, &tris[i], tris[i].frame, 1)
		} else {
			c.fillTriTarget(target, &tris[i], tris[i].color, 1)
		}
		for p := range target.color {
			if target.color[p] != previous[p] {
				owner[p] = i
			}
		}
		copy(previous, target.color)
	}

	var skin, team int
	for p, o := range owner {
		if o < 0 || !target.covered[p] || !flaps[tris[o].piece] {
			continue
		}
		switch name := strings.ToLower(tris[o].texture); {
		case strings.HasPrefix(name, "noise3"):
			skin++
		case strings.HasPrefix(name, "color"), name == "32xlogos":
			team++
		}
	}
	if skin == 0 {
		t.Fatal("ARMCK's flap skin owns no composed pixel; the outer face is being occluded again")
	}
	if skin <= team {
		t.Fatalf("flap skin owns %d pixels against %d team pixels; the interior faces are winning the tie-break again", skin, team)
	}
}
