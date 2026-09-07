package client

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/camera"
	presentationrender "github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// Contract locked here: every client path that turns a composed model-space
// vertex into a screen pixel narrows it the same way and anchors it the same
// way. Model space is mirrored in Z against world space, so the screen Y lane
// is built from the high word of the *negated* model Z — `Zn = hi16(-vz)`,
// [R-RAST-01 §2] — and the unit's own position enters only once, at the image
// anchor. The body composition ([R-REN-03A §1]), the structure shadow
// rasterization ([R-REN-03D §2], [R-REN-03D §3]) and the nanoframe outline
// ([R-COMP-01 §3]) are three projections of one model: if any of them drops or
// inverts that flip, the shadow or the wireframe becomes a mirrored copy of the
// body and swings the wrong way as the unit turns.
//
// Three defects this file exists to stop from returning:
//   - the shadow projection narrowing Z without the negation, which mirrored
//     every structure's shadow against its own body;
//   - the outline projecting the composed vertices through the camera's
//     world-space helper, which both mirrored the wireframe and folded the
//     unit's own height into the model's half-height shear;
//   - the selection plate passing composed vertices to
//     render.ModelProjectToScreen, the world-point projection, which mirrored
//     every plate against the body it belongs to.
//
// The plate takes the world-object form of [R-WATER-01 §1] item 3 rather than
// the composition image's anchor-plus-local form, and [R-RAST-01 §2] requires
// those two to stay separate: floor(a) + floor(b) is floor(a+b) or one less, so
// the same vertex can land a pixel apart under the two. The cross-site
// assertion below is therefore "agrees to within one pixel", which is still
// decisive against a mirror — that moves a vertex by twice its model Z.

// orientationHeadings covers the four cardinal facings. A Z mirror is invisible
// on a model that is symmetric about its origin, so the test model below is
// asymmetric and every heading is checked separately.
var orientationHeadings = [4]uint16{0, 16384, 32768, 49152}

// orientationOutlineColor is a palette index no other pass in this test writes,
// so a framebuffer scan for it finds exactly the wireframe.
const orientationOutlineColor uint8 = 200

// orientationFixed converts whole world units to 16.16.
func orientationFixed(v int64) numeric.Fixed { return numeric.Fixed(v << 16) }

// orientationModel is a single-piece model carrying one face that is asymmetric
// on all three axes: it spans 0..6 in X, 0..4 in Y and 0..12 in Z, and never
// reaches negative Z. Mirroring Z about the model origin therefore moves every
// vertex, at every heading.
func orientationModel() *unitModel {
	pieces := []pieceInfo{{name: "body", parent: -1}}
	tri := makeTriangle(0, "body", [3][3]float64{{0, 0, 0}, {6, 0, 0}, {0, 4, 12}}, 42, 0)
	return syntheticModel(pieces, []syntheticTri{tri}, 0)
}

// orientationClient is a headless client whose framebuffer is large enough to
// hold the test model at either Z sign, so a mirrored projection lands on
// screen rather than being clipped away and passing by accident.
func orientationClient() *Client {
	return &Client{
		width: 96, height: 96, indexed: make([]uint8, 96*96),
		cam: &camera.Camera{ViewW: 96, ViewH: 96, MapW: 4096, MapH: 4096},
	}
}

// orientationDraw poses the test model at a heading. The unit sits away from
// the map origin and above the terrain under it, so the anchor, the model's own
// half-height shear and the shadow's terrain shear are all non-zero and a term
// that lands in the wrong one of them is visible.
func orientationDraw(heading uint16) *presentationrender.UnitDraw {
	m := orientationModel()
	worldPos := [3]numeric.Fixed{orientationFixed(30), orientationFixed(12), orientationFixed(30)}
	draw := presentationrender.BuildUnitDrawSimple(m.compiled, nil, heading, 0, 0, worldPos)
	draw.GroundY = orientationFixed(4)
	return draw
}

// TestBodyAndShadowProjectionsAgreeOnTheHandednessFlip locks the shared
// narrowing: both projections take X straight through, both recover the same
// `Zn = hi16(-vz)` before their own shear, and both report the same whole-unit
// height [R-RAST-01 §2][R-REN-03D §2]. Reverting the shadow's negation makes
// the two disagree on Zn at every vertex with a non-zero Z.
func TestBodyAndShadowProjectionsAgreeOnTheHandednessFlip(t *testing.T) {
	for _, heading := range orientationHeadings {
		draw := orientationDraw(heading)
		checked := 0
		for pi := range draw.Pieces {
			for _, v := range draw.Pieces[pi].WorldVertices {
				lx, ly, ry := modelLocalVertex(v, draw.WorldPos)
				sx, sy, sry := shadowLocalVertex(v, draw.WorldPos)
				checked++

				// The height key lane is the same whole-unit height in both.
				if sry != ry {
					t.Fatalf("heading %d: shadow height %d, body height %d", heading, sry, ry)
				}
				// X passes through unchanged in the body; the shadow adds the
				// quarter-height term of the 45-degree light [R-REN-03D §2].
				wantX := int32(v[0].Sub(draw.WorldPos[0]).Floor())
				if lx != wantX {
					t.Fatalf("heading %d: body X %d, want %d", heading, lx, wantX)
				}
				if sx != lx+(ry>>2) {
					t.Fatalf("heading %d: shadow X %d, want body %d plus quarter height %d", heading, sx, lx, ry>>2)
				}
				// Both screen Y lanes are built from hi16(-vz): back out each
				// projection's own shear and the residue must be that one
				// value. This is the assertion a reverted negation trips.
				wantZn := int32((-(v[2].Sub(draw.WorldPos[2]))).Floor())
				if got := ly + (ry >> 1); got != wantZn {
					t.Fatalf("heading %d: body Y implies Zn %d, want %d", heading, got, wantZn)
				}
				if got := sy + (ry >> 2); got != wantZn {
					t.Fatalf("heading %d: shadow Y implies Zn %d, want %d", heading, got, wantZn)
				}
			}
		}
		if checked == 0 {
			t.Fatalf("heading %d: the pose produced no vertices to project", heading)
		}
	}
}

// TestModelIsAsymmetricInZ guards the guard: the assertions above and below
// only detect a mirror because the posed model reaches a non-zero Z at every
// heading. If a later edit makes the test model symmetric, this fails first and
// says why rather than letting the orientation tests pass vacuously.
func TestModelIsAsymmetricInZ(t *testing.T) {
	for _, heading := range orientationHeadings {
		draw := orientationDraw(heading)
		asymmetric := false
		for pi := range draw.Pieces {
			for _, v := range draw.Pieces[pi].WorldVertices {
				if v[2] != draw.WorldPos[2] {
					asymmetric = true
				}
			}
		}
		if !asymmetric {
			t.Fatalf("heading %d: every vertex sits at model Z 0, so a Z mirror would be undetectable", heading)
		}
	}
}

// TestShadowAnchorIsTheBodyAnchorPlusTheDocumentedOffset locks the placement
// relationship rather than any literal pixel: the shadow image goes five pixels
// right of the body image, and it is sheared by the terrain height under the
// unit where the body is sheared by the unit's own height [R-REN-03D §3]
// [R-REN-03A §9].
func TestShadowAnchorIsTheBodyAnchorPlusTheDocumentedOffset(t *testing.T) {
	c := orientationClient()
	for _, heading := range orientationHeadings {
		draw := orientationDraw(heading)
		bx, by := c.modelAnchor(draw)
		sx, sy := c.shadowAnchor(draw)
		if sx-bx != shadowXOffset {
			t.Fatalf("heading %d: shadow anchor X is %d past the body, want %d", heading, sx-bx, shadowXOffset)
		}
		// The camera narrows with an arithmetic shift before halving, so both
		// shear terms are computed the same way here [03 §2.5].
		unitShear := int32(int64(draw.WorldPos[1])>>16) >> 1
		groundShear := int32(int64(draw.GroundY)>>16) >> 1
		if want := unitShear - groundShear; sy-by != want {
			t.Fatalf("heading %d: shadow anchor Y is %d past the body, want %d", heading, sy-by, want)
		}
		if unitShear == groundShear {
			t.Fatalf("heading %d: the pose leaves both shears equal, so a swapped shear would not show", heading)
		}
	}
}

// TestNanoframeOutlineLiesOnTheBodyItOutlines locks the outline path end to
// end: retail projects each outline vertex with the composition image's own
// rule and origin and then blits that image at the unit anchor, so a wireframe
// point is exactly the body projection plus the body anchor [R-COMP-01 §3]
// [R-REN-03A §1].
//
// Both assertions fail if the outline goes back to projecting the composed
// vertices as world coordinates: the wireframe then mirrors in Z about the
// anchor, so the vertex pixels are empty and the drawn extent flips.
func TestNanoframeOutlineIsTheBodysRowExtremes(t *testing.T) {
	c := orientationClient()
	for _, heading := range orientationHeadings {
		draw := orientationDraw(heading)
		body, ok := c.composeModel(draw, 0, teamColor{index: 0, known: true}, 1, modelCursorUnit, nil, 0)
		if !ok || body.image == nil {
			t.Fatalf("heading %d: the body did not compose", heading)
		}
		img := body.image
		// An empty image over the same box and origin receives only the
		// outline, so its pixels can be read against the body's row by row.
		wire := newModelImage(img.width, img.heightPx, img.originX, img.originY, img.anchorX, img.anchorY, false, 1)
		c.outlineModelInto(wire, nil, draw, orientationOutlineColor)

		rows := 0
		for y := 0; y < img.heightPx; y++ {
			left, right := -1, -1
			var got []int
			for x := 0; x < img.width; x++ {
				i := y*img.width + x
				if img.covered[i] {
					if left < 0 {
						left = x
					}
					right = x
				}
				if wire.covered[i] {
					if wire.color[i] != orientationOutlineColor {
						t.Fatalf("heading %d: outline pixel (%d,%d) holds %d", heading, x, y, wire.color[i])
					}
					got = append(got, x)
				}
			}
			if left < 0 {
				// A row the body's chains do not open — the top corner's own
				// row, or one the winding cull closes — gets no outline either.
				if len(got) != 0 {
					t.Fatalf("heading %d: row %d has outline pixels %v but no body", heading, y, got)
				}
				continue
			}
			rows++
			// The body covers [xl, xr) on this row and the outline writes xl
			// and xr: the row's left extreme and one past its right extreme,
			// from the same two chains [R-COMP-01 §3]. A mirrored projection
			// at either site puts the two on different columns.
			if len(got) != 2 || got[0] != left || got[1] != right+1 {
				t.Fatalf("heading %d: row %d outline at %v, want [%d %d] from body columns %d..%d",
					heading, y, got, left, right+1, left, right)
			}
		}
		if rows == 0 {
			t.Fatalf("heading %d: the body composed no rows", heading)
		}
	}
}

// TestNanoframeOutlineIsKeyTested locks the admission of [R-COMP-01 §3]: with a
// key plane an endpoint is written only where the stored key is at or below the
// edge's own, so the wireframe of a face hidden under a higher face fails and
// the outline of the top face passes by equality.
func TestNanoframeOutlineIsKeyTested(t *testing.T) {
	target := newModelTarget(8, 8)
	for i := range target.height {
		target.height[i] = 60 // a body higher than the low face, lower than the high one
	}
	low := []int32{20, 20, 20, 20}
	high := []int32{90, 90, 90, 90}
	xs := []int32{1, 6, 1, 1}
	ys := []int32{1, 1, 6, 1}
	target.outlinePolygon(xs, ys, low, 200, nil, 1, 0, 0)
	for i := range target.covered {
		if target.covered[i] {
			t.Fatalf("a face keyed 20 outlined over a body keyed 60 at pixel %d", i)
		}
	}
	target.outlinePolygon(xs, ys, high, 201, nil, 1, 0, 0)
	wrote := 0
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			i := y*8 + x
			if !target.covered[i] {
				continue
			}
			wrote++
			if target.height[i] != 90 {
				t.Fatalf("outline pixel (%d,%d) stored key %d, want its own 90", x, y, target.height[i])
			}
		}
	}
	// A flat-topped triangle over rows 1..5: two pixels on each, the row of
	// the bottom corner being exclusive.
	if wrote != 10 {
		t.Fatalf("outline wrote %d pixels, want two per open row", wrote)
	}
}

// TestSelectionPlateProjectionAgreesWithTheBody locks the fourth projection
// site to the other three. The selection plate projects composed model vertices
// through render.ModelVertexToScreen, the world-object form of
// [R-WATER-01 §1] item 3; measured against the unit's own projected position it
// must reproduce the body composition's model-relative offset, up to the
// one-pixel sum-before-floor difference [R-RAST-01 §2] names.
//
// Dropping the handedness flip at that site moves a vertex by twice its model
// Z, which this catches at every heading.
func TestSelectionPlateProjectionAgreesWithTheBody(t *testing.T) {
	c := orientationClient()
	for _, heading := range orientationHeadings {
		draw := orientationDraw(heading)
		// The plate's own frame is anchored on the unit's true world position.
		plateAnchorX, plateAnchorY := presentationrender.ModelProjectToScreen(c.cam, draw.WorldPos)
		spread := int32(0)
		for pi := range draw.Pieces {
			for _, v := range draw.Pieces[pi].WorldVertices {
				lx, ly, _ := modelLocalVertex(v, draw.WorldPos)
				px, py := presentationrender.ModelVertexToScreen(c.cam, draw.WorldPos, v)
				if d := absInt32(px - plateAnchorX - lx); d > 1 {
					t.Fatalf("heading %d: plate X offset %d, body offset %d, differ by %d",
						heading, px-plateAnchorX, lx, d)
				}
				if d := absInt32(py - plateAnchorY - ly); d > 1 {
					t.Fatalf("heading %d: plate Y offset %d, body offset %d, differ by %d",
						heading, py-plateAnchorY, ly, d)
				}
				if a := absInt32(ly); a > spread {
					spread = a
				}
			}
		}
		// A mirror is only detectable while some vertex is off the anchor row.
		if spread < 2 {
			t.Fatalf("heading %d: every vertex projects within a pixel of the anchor row, so a mirror would not show", heading)
		}
	}
}

func absInt32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}
