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
func TestNanoframeOutlineLiesOnTheBodyItOutlines(t *testing.T) {
	c := orientationClient()
	for _, heading := range orientationHeadings {
		draw := orientationDraw(heading)
		anchorX, anchorY := c.modelAnchor(draw)

		// Where the body composition puts each vertex on the framebuffer.
		var wantMinX, wantMinY, wantMaxX, wantMaxY int32
		type point struct{ x, y int32 }
		var want []point
		for pi := range draw.Pieces {
			for _, v := range draw.Pieces[pi].WorldVertices {
				lx, ly, _ := modelLocalVertex(v, draw.WorldPos)
				lx, ly = c.scaleModelLocal(lx, ly)
				p := point{anchorX + lx, anchorY + ly}
				if len(want) == 0 {
					wantMinX, wantMaxX, wantMinY, wantMaxY = p.x, p.x, p.y, p.y
				}
				want = append(want, p)
				if p.x < wantMinX {
					wantMinX = p.x
				}
				if p.x > wantMaxX {
					wantMaxX = p.x
				}
				if p.y < wantMinY {
					wantMinY = p.y
				}
				if p.y > wantMaxY {
					wantMaxY = p.y
				}
			}
		}
		if len(want) == 0 {
			t.Fatalf("heading %d: the pose produced no outline vertices", heading)
		}

		clearIndexed(c)
		c.drawModelOutline(draw, orientationOutlineColor, nil)

		// Every vertex is an endpoint of two drawn edges, and the line walker
		// writes its endpoints, so each must carry the outline colour.
		for i, p := range want {
			if p.x < 0 || p.x >= int32(c.width) || p.y < 0 || p.y >= int32(c.height) {
				t.Fatalf("heading %d: vertex %d projects off the test framebuffer at (%d,%d)", heading, i, p.x, p.y)
			}
			if got := c.indexed[p.y*int32(c.width)+p.x]; got != orientationOutlineColor {
				t.Fatalf("heading %d: no outline pixel where the body puts vertex %d, at (%d,%d): found index %d",
					heading, i, p.x, p.y, got)
			}
		}

		// The drawn extent is the extent of those endpoints: a Bresenham edge
		// never leaves the box its two ends span. Comparing extents rather than
		// counting pixels keeps this a relationship assertion.
		gotMinX, gotMinY, gotMaxX, gotMaxY, found := outlineExtent(c)
		if !found {
			t.Fatalf("heading %d: the outline drew nothing", heading)
		}
		if gotMinX != wantMinX || gotMaxX != wantMaxX || gotMinY != wantMinY || gotMaxY != wantMaxY {
			t.Fatalf("heading %d: outline extent x[%d,%d] y[%d,%d], want x[%d,%d] y[%d,%d]",
				heading, gotMinX, gotMaxX, gotMinY, gotMaxY, wantMinX, wantMaxX, wantMinY, wantMaxY)
		}
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

// outlineExtent reports the bounding box of the outline colour in the
// framebuffer.
func outlineExtent(c *Client) (minX, minY, maxX, maxY int32, found bool) {
	for y := 0; y < c.height; y++ {
		for x := 0; x < c.width; x++ {
			if c.indexed[y*c.width+x] != orientationOutlineColor {
				continue
			}
			ix, iy := int32(x), int32(y)
			if !found {
				minX, maxX, minY, maxY, found = ix, ix, iy, iy, true
				continue
			}
			if ix < minX {
				minX = ix
			}
			if ix > maxX {
				maxX = ix
			}
			if iy < minY {
				minY = iy
			}
			if iy > maxY {
				maxY = iy
			}
		}
	}
	return
}
