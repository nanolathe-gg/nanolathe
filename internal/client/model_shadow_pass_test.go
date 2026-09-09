package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/frame"
	compiledmodel "github.com/nanolathe-gg/nanolathe/internal/model"
	presentationrender "github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// TestShadowProjectionShearsByAQuarterHeight locks retail's shadow shear: a
// quarter of the vertex height added in X and subtracted in screen Y, with no
// half-height term at all [R-REN-03D §2]. The screen Y lane itself is the body
// projection's `Zn = hi16(-vz)` handedness flip [R-RAST-01 §2].
//
// The Z expectations were corrected: they previously read the model Z without
// the flip, which is the code block of [R-REN-03D §2] taken literally against
// [R-RAST-01 §2]'s later narrowing and against the body pass. See the doc
// comment on shadowLocalVertex.
func TestShadowProjectionShearsByAQuarterHeight(t *testing.T) {
	f := func(v int64) numeric.Fixed { return numeric.Fixed(v << 16) }
	origin := [3]numeric.Fixed{0, 0, 0}

	sx, sy, ry := shadowLocalVertex([3]numeric.Fixed{f(10), f(8), f(20)}, origin)
	if sx != 12 || sy != -22 || ry != 8 {
		t.Fatalf("shadow projection = (%d,%d) ry=%d, want (12,-22) ry=8", sx, sy, ry)
	}
	// The quarter is an arithmetic shift, so a negative height floors.
	sx, sy, _ = shadowLocalVertex([3]numeric.Fixed{0, f(-3), 0}, origin)
	if sx != -1 || sy != 1 {
		t.Fatalf("negative height shear = (%d,%d), want (-1,1)", sx, sy)
	}
	// A ground-level vertex is not sheared: the shadow lies flat.
	sx, sy, _ = shadowLocalVertex([3]numeric.Fixed{f(7), 0, f(9)}, origin)
	if sx != 7 || sy != -9 {
		t.Fatalf("ground vertex = (%d,%d), want (7,-9)", sx, sy)
	}
}

// TestShadowGateReadsOptionsAndAuthoredKeys locks the corrected shadow gate: a
// mobile unit needs the master bit, the vehicle-shadow bit, and none of
// noshadow/canhover/floater; a structure needs only the master bit and
// noshadow — SHADING gates neither [R-REN-03D §1, corrected].
func TestShadowGateReadsOptionsAndAuthoredKeys(t *testing.T) {
	c := compositionClient(t)
	c.shadows, c.vehicleShadows, c.shading = true, true, true
	if !c.castsModelShadow(false, false, false, false) {
		t.Fatal("an ordinary mobile unit with all options on must cast a shadow")
	}
	if !c.castsModelShadow(false, false, false, true) {
		t.Fatal("an ordinary structure with all options on must cast a shadow")
	}
	for _, tc := range []struct {
		name                        string
		noShadow, canHover, floater bool
	}{
		{"noshadow", true, false, false},
		{"canhover", false, true, false},
		{"floater", false, false, true},
	} {
		if c.castsModelShadow(tc.noShadow, tc.canHover, tc.floater, false) {
			t.Fatalf("mobile %s must suppress the model shadow", tc.name)
		}
	}
	// A structure's branch tests only the master bit and noshadow: it does
	// not read canhover or floater at all.
	if c.castsModelShadow(true, false, false, true) {
		t.Fatal("structure noshadow must suppress the model shadow")
	}
	if !c.castsModelShadow(false, true, false, true) {
		t.Fatal("structure canhover must not suppress the model shadow")
	}
	if !c.castsModelShadow(false, false, true, true) {
		t.Fatal("structure floater must not suppress the model shadow")
	}
	for _, tc := range []struct {
		name                     string
		master, vehicle, shading bool
	}{
		{"master off", false, true, true},
		{"vehicle off", true, false, true},
	} {
		c.shadows, c.vehicleShadows, c.shading = tc.master, tc.vehicle, tc.shading
		if c.castsModelShadow(false, false, false, false) {
			t.Fatalf("mobile %s must suppress the model shadow", tc.name)
		}
	}
	// A structure does not read the vehicle-shadow bit at all.
	c.shadows, c.vehicleShadows, c.shading = true, false, true
	if !c.castsModelShadow(false, false, false, true) {
		t.Fatal("structure with vehicle shadows off must still cast a shadow")
	}
	// SHADING has no part in the gate for either class: it selects the
	// shaded model renderer only, not shadow casting.
	c.shadows, c.vehicleShadows, c.shading = true, true, false
	if !c.castsModelShadow(false, false, false, false) {
		t.Fatal("mobile unit must still cast a shadow with shading off")
	}
	if !c.castsModelShadow(false, false, false, true) {
		t.Fatal("structure must still cast a shadow with shading off")
	}
}

// TestTintedCommitBlendsThroughALP locks the translucent blit: the destination
// resolves to ALP[src*256 + dst], which for a shadow's index-0 silhouette
// darkens the ground rather than replacing it [R-REN-03D §4].
func TestTintedCommitBlendsThroughALP(t *testing.T) {
	c := compositionClient(t)
	for i := range c.indexed {
		c.indexed[i] = 200 // ground
	}
	img := newModelImage(2, 1, 0, 0, 10, 10, false, 1)
	img.write(0, shadowColorIndex, true)
	// The second pixel stays background and must not touch the ground.
	img.tintedCommit(c.indexed, c.width, c.height, &c.pal.Alpha)

	want := c.pal.Alpha[int(shadowColorIndex)*256+200]
	if got := c.indexed[10*c.width+10]; got != want {
		t.Fatalf("shadow pixel = %d, want ALP[0,200] = %d", got, want)
	}
	if got := c.indexed[10*c.width+11]; got != 200 {
		t.Fatalf("background pixel darkened the ground to %d, want 200 untouched", got)
	}
}

// TestShadowIsFilledWithPaletteIndexZero locks the fill colour every shadow
// face carries [R-REN-03D §2].
func TestShadowIsFilledWithPaletteIndexZero(t *testing.T) {
	if shadowColorIndex != 0 {
		t.Fatalf("shadow fill index = %d, want 0", shadowColorIndex)
	}
	if shadowKeyBias != 25 {
		t.Fatalf("shadow key bias = %d, want 25", shadowKeyBias)
	}
	if shadowXOffset != 5 {
		t.Fatalf("shadow X offset = %d, want 5", shadowXOffset)
	}
}

func TestOriginalDetailShadowKeepsOffsetAndPunchesAtScaledBody(t *testing.T) {
	c := compositionClient(t)
	c.cam.Scale = 2
	c.SetEnhanced(false)
	unit := numeric.Fixed(20 << 16)
	draw := &presentationrender.UnitDraw{WorldPos: [3]numeric.Fixed{unit, 0, unit}, GroundY: 0}
	bodyX, bodyY := c.modelAnchor(draw)
	shadowX, shadowY := c.shadowAnchor(draw)
	if want := bodyX + shadowXOffset*2; shadowX != want {
		t.Fatalf("Original scale-2 shadow X = %d, want %d", shadowX, want)
	}

	body := newModelImage(1, 1, 0, 0, bodyX, bodyY, true, 1)
	body.write(0, 44, true)
	shadow := newModelImage(11, 1, 5, 0, shadowX, shadowY, true, 1)
	for i := range shadow.color {
		shadow.write(i, shadowColorIndex, true)
	}
	c.punchOutModelShadow(shadow, body)
	if shadow.color[0] != shadow.transparent || shadow.covered[0] {
		t.Fatalf("scaled punch left body pixel in shadow: color=%d covered=%v", shadow.color[0], shadow.covered[0])
	}
	for i := range c.indexed {
		c.indexed[i] = 200
	}
	shadow.blit = 2
	shadow.tintedCommit(c.indexed, c.width, c.height, &c.pal.Alpha)
	if got := c.indexed[int(bodyY)*c.width+int(bodyX)]; got != 200 {
		t.Fatalf("scaled shadow darkened the final body position to %d", got)
	}
}

// TestMobileShadowCopiesTheFinishedBody locks the mobile branch: it copies the
// final body image instead of running the structure projector, flattens the
// copied pixels, and removes only the part at or below the positive waterline
// threshold [R-REN-03D §1, §6][R-RAST-01 §4].
func TestMobileShadowCopiesTheFinishedBody(t *testing.T) {
	c := compositionClient(t)
	c.buffer = frame.NewBuffer()
	publishSeaLevel(t, c, 1, 1, 0)
	body := newModelImage(4, 1, 0, 0, 20, 20, true, 1)
	body.write(0, 20, true)
	body.height[0] = 50
	body.write(1, 21, true)
	body.height[1] = 51
	body.write(2, 22, true)
	body.height[2] = 52
	// A live color-key face leaves a covered erase above the clipping plane.
	body.write(3, body.transparent, true)
	body.height[3] = 200

	draw := &presentationrender.UnitDraw{CastsShadow: true}
	shadow := c.buildModelShadow(draw, body)
	if shadow == nil {
		t.Fatal("mobile shadow was not built from its finished body")
	}
	wantAnchorX, wantAnchorY := c.shadowAnchor(draw)
	if shadow.anchorX != wantAnchorX || shadow.anchorY != wantAnchorY {
		t.Fatalf("mobile shadow placement = (%d,%d), want terrain-shadow placement (%d,%d)", shadow.anchorX, shadow.anchorY, wantAnchorX, wantAnchorY)
	}
	if shadow.covered[0] || shadow.covered[1] {
		t.Fatalf("mobile waterline did not erase keys at or below 51: coverage=%v", shadow.covered)
	}
	if !shadow.covered[2] || shadow.color[2] != shadowColorIndex || shadow.height[2] != 52 {
		t.Fatalf("mobile above-water silhouette = color %d covered %v key %d, want black/true/52", shadow.color[2], shadow.covered[2], shadow.height[2])
	}
	if shadow.covered[3] || shadow.color[3] != shadow.transparent {
		t.Fatalf("transparent body background became shadow coverage: color=%d covered=%v", shadow.color[3], shadow.covered[3])
	}
}

// TestDiggerShadowClipsAtItsOriginKey locks both Digger branch selection ahead
// of structure class and its inclusive <=125 buried-half cutoff [R-REN-03D
// §1, §6].
func TestDiggerShadowClipsAtItsOriginKey(t *testing.T) {
	c := compositionClient(t)
	body := newModelImage(3, 1, 0, 0, 20, 20, true, 1)
	for i, key := range []uint8{124, 125, 126} {
		body.write(i, uint8(40+i), true)
		body.height[i] = key
	}

	shadow := c.buildModelShadow(&presentationrender.UnitDraw{CastsShadow: true, Structure: true, DiggerClip: true}, body)
	if shadow == nil {
		t.Fatal("Digger did not select its silhouette branch")
	}
	if shadow.covered[0] || shadow.covered[1] {
		t.Fatalf("Digger shadow kept buried pixels: coverage=%v", shadow.covered)
	}
	if !shadow.covered[2] || shadow.color[2] != shadowColorIndex || shadow.height[2] != 126 {
		t.Fatalf("Digger above-origin silhouette = color %d covered %v key %d, want black/true/126", shadow.color[2], shadow.covered[2], shadow.height[2])
	}
}

// TestStructureShadowDoesNotReuseTheBody locks the remaining structure branch:
// it must continue through its separate projected raster and punch path rather
// than taking the mobile/Digger copy shortcut [R-REN-03D §2, §5].
func TestStructureShadowDoesNotReuseTheBody(t *testing.T) {
	c := compositionClient(t)
	body := newModelImage(1, 1, 0, 0, 20, 20, true, 1)
	body.write(0, 44, true)
	body.height[0] = 99
	shadow := c.buildModelShadow(&presentationrender.UnitDraw{CastsShadow: true, Structure: true}, body)
	if shadow != nil {
		t.Fatal("structure shadow reused the body without any projected model geometry")
	}
}

// TestCollectShadowPolysIgnoresTexturesAndTheSelectionPlate locks that the
// shadow pass fills every face flat, at any arity, and skips the load-time
// selection primitive exactly as the body pass does [R-REN-03D §2].
//
// The faces are whole authored primitives, not fan triangles: the shadow now
// goes through the same two-chain edge walk as the body, so an n-gon stays an
// n-gon and a face's authored index order still decides its two chains
// [R-RAST-01 §1].
func TestCollectShadowPolysIgnoresTexturesAndTheSelectionPlate(t *testing.T) {
	c := compositionClient(t)
	f := func(v int64) numeric.Fixed { return numeric.Fixed(v << 16) }
	// Authored front-facing for the shadow's own quarter-shear projection: a
	// counter-clockwise ring paints nothing there for the same reason it
	// paints nothing in the body pass [R-RAST-01 §1] step 7.
	verts := [][3]numeric.Fixed{{0, 0, 0}, {f(4), 0, 0}, {f(4), 0, -f(4)}, {0, 0, -f(4)}, {f(2), f(4), -f(2)}}
	draw := &presentationrender.UnitDraw{
		Model: &compiledmodel.Model{Pieces: []compiledmodel.Piece{{Selection: true}}},
		Pieces: []presentationrender.PieceDraw{{
			WorldVertices: verts,
			Primitives: []presentationrender.PrimitiveDraw{
				{VertexIndices: []uint16{0, 1, 2, 3}},                       // the selection plate, skipped
				{TextureName: "anything", VertexIndices: []uint16{0, 1, 4}}, // textured: still a flat shadow face
				{IsColored: 1, VertexIndices: []uint16{0, 1, 2, 3, 4}},      // a flat 5-gon
			},
		}},
	}
	polys := c.collectShadowPolys(draw)
	// Primitive 0 is the selection plate and is skipped; primitives 1 and 2
	// each stay one face, the triangle and the 5-gon.
	if len(polys) != 2 {
		t.Fatalf("shadow faces = %d, want 2 (plate skipped, one triangle and one 5-gon)", len(polys))
	}
	if got := len(polys[0].x); got != 3 {
		t.Fatalf("first shadow face has %d corners, want the authored 3", got)
	}
	if got := len(polys[1].x); got != 5 {
		t.Fatalf("second shadow face has %d corners, want the authored 5 — the walk takes n-gons whole", got)
	}
	for i := range polys {
		if polys[i].color != shadowColorIndex {
			t.Fatalf("shadow face %d filled with %d, want %d", i, polys[i].color, shadowColorIndex)
		}
		if polys[i].frame != nil {
			t.Fatalf("shadow face %d carries a texture; the shadow pass never samples one", i)
		}
		if polys[i].useSHD {
			t.Fatalf("shadow face %d carries an SHD row; the shadow pass computes none [R-REN-03D §2]", i)
		}
	}
}

// TestEraseAtOrBelowClipsTheBuriedHalf locks the digger clip [R-REN-03A §8].
func TestEraseAtOrBelowClipsTheBuriedHalf(t *testing.T) {
	img := newModelImage(3, 1, 0, 0, 0, 0, true, 1)
	img.write(0, 40, true)
	img.height[0] = 100 // below the origin for a digger: 25 units down
	img.write(1, 41, true)
	img.height[1] = 125 // exactly at the origin: clipped, the test is inclusive
	img.write(2, 42, true)
	img.height[2] = 126 // above the origin: kept

	img.eraseAtOrBelow(125)

	if img.covered[0] || img.covered[1] {
		t.Fatal("digger clip must erase at and below the threshold")
	}
	if !img.covered[2] || img.color[2] != 42 {
		t.Fatalf("pixel above the threshold was erased: covered=%v colour=%d", img.covered[2], img.color[2])
	}
}
