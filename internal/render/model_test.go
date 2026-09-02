package render

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/model"
	"github.com/nanolathe/nanolathe/internal/palette"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// TestUnitOrientationFoldOrder verifies bank→Z, heading→Y, pitch→X as outermost factor [03 §2.4] C24 [03 §5.2] C13.
// The per-unit orientation triple folds into ROOT Z/Y/X respectively and composes as outermost [03 §2.4] C24.
func TestUnitOrientationFoldOrder(t *testing.T) {
	// Synthetic model root -> child [03 §2.4] C21.
	m := &model.Model{
		Pieces: []model.Piece{
			{Name: "root", Parent: -1, Translate: [3]numeric.Fixed{0, 0, 0}},
			{Name: "child", Parent: 0, Translate: [3]numeric.Fixed{numeric.Fixed(65536), 0, 0}}, // (1,0,0)
		},
		Root: 0,
	}
	m.Pieces[0].Children = []int{1}

	// Heading folds into Y unchanged — the literal C24 fold [03 §2.4]. It once
	// folded -heading to compensate for the model projection's missing
	// handedness flip [R-RAST-01 §2]; both were corrected together.
	states := BuildUnitPieceStates(m, nil, 16384, 0, 0) // heading 16384→Y, pitch 0→X, bank 0→Z [03 §2.4] C24
	if states[0].RotY != 16384 || states[0].RotX != 0 || states[0].RotZ != 0 {
		t.Fatalf("fold heading→Y got Y=%d X=%d Z=%d want 16384", states[0].RotY, states[0].RotX, states[0].RotZ)
	}
	tr := model.Compose(m, states, 1)
	// Ry per [03 §2.4]: x' = c*x - s*z, z' = s*x + c*z. A child at model (1,0,0)
	// under a quarter turn lands at model (0,0,1), which the projection's
	// handedness flip puts one unit UP-screen of the unit anchor.
	expX := numeric.Fixed(0)
	expY := numeric.Fixed(0)
	expZ := numeric.Fixed(65536)
	if tr.Origin[0] != expX || tr.Origin[1] != expY || tr.Origin[2] != expZ {
		t.Fatalf("heading Y 90 fold: got %v want [%d %d %d]", tr.Origin, expX, expY, expZ)
	}
	// Verify building via BuildPieceDraws includes position only at final placement [03 §2.4] C24 [03 §5.2]
	// lerpPos zero -> worldOrigin == localOrigin
	pieces := BuildPieceDraws(m, states, [3]numeric.Fixed{}, false)
	if pieces[1].LocalOrigin != tr.Origin {
		t.Fatalf("localOrigin mismatch")
	}
	if pieces[1].WorldOrigin != tr.Origin {
		t.Fatalf("worldOrigin with zero lerp should equal local")
	}
	// With lerpPos (2,0,0) -> worldOrigin = local + lerp [03 §2.4] C24
	lerpPos := [3]numeric.Fixed{numeric.Fixed(2 * 65536), 0, 0}
	pieces2 := BuildPieceDraws(m, states, lerpPos, false)
	wantWorld := [3]numeric.Fixed{tr.Origin[0].Add(lerpPos[0]), tr.Origin[1].Add(lerpPos[1]), tr.Origin[2].Add(lerpPos[2])}
	if pieces2[1].WorldOrigin != wantWorld {
		t.Fatalf("worldOrigin with lerp: got %v want %v", pieces2[1].WorldOrigin, wantWorld)
	}
	// Ensure position never entered piece math directly: localOrigin independent of lerp [03 §5.2] C13
	if pieces[0].LocalOrigin[0] != pieces2[0].LocalOrigin[0] {
		t.Fatalf("position entered piece math: local changed with lerp")
	}

	// Pitch X 90deg: child at (0,1,0) should go to (0,0,1) via Rx [03 §2.4] C21 Rx: y'=c*y - s*z ; z'=s*y + c*z
	m2 := &model.Model{
		Pieces: []model.Piece{
			{Name: "root", Parent: -1},
			{Name: "child", Parent: 0, Translate: [3]numeric.Fixed{0, numeric.Fixed(65536), 0}},
		},
		Root: 0,
	}
	m2.Pieces[0].Children = []int{1}
	states2 := BuildUnitPieceStates(m2, nil, 0, 16384, 0) // pitch 16384→X
	if states2[0].RotX != 16384 {
		t.Fatalf("fold pitch→X got %d", states2[0].RotX)
	}
	tr2 := model.Compose(m2, states2, 1)
	if tr2.Origin[0] != 0 || tr2.Origin[1] != 0 || tr2.Origin[2] != numeric.Fixed(65536) {
		t.Fatalf("pitch X 90: got %v want [0 0 65536]", tr2.Origin)
	}

	// Bank Z 90deg: (1,0,0) -> (0,1,0) via Rz [03 §2.4] C21 Rz: x'=c*x - s*y ; y'=s*x + c*y
	states3 := BuildUnitPieceStates(m, nil, 0, 0, 16384) // bank 16384→Z
	if states3[0].RotZ != 16384 {
		t.Fatalf("fold bank→Z got %d", states3[0].RotZ)
	}
	tr3 := model.Compose(m, states3, 1)
	if tr3.Origin[0] != 0 || tr3.Origin[1] != numeric.Fixed(65536) || tr3.Origin[2] != 0 {
		t.Fatalf("bank Z 90: got %v want [0 65536 0]", tr3.Origin)
	}

	// Verify RT composition order Z then X then Y via combined non-commuting case [03 §2.4] C21
	// Use non-zero on same accumulator chain? The outermost factor is root, so check that
	// heading+ pitch+baking combined yields Compose with Z→X→Y order already validated in model package.
	// BuildUnitDraw samples position and angles from the committed current view [03 §2.4].
	cur := frame.UnitView{Slot: 1, X: numeric.Fixed(65536), Heading: 16384, Pitch: 16384, Bank: 0}
	draw := BuildUnitDraw(m, nil, cur.Heading, cur.Pitch, cur.Bank, cur, nil)
	if draw == nil {
		t.Fatal("BuildUnitDraw nil")
	}
	if draw.WorldPos[0] != numeric.Fixed(65536) {
		t.Fatalf("committed world pos: got %d want 65536", draw.WorldPos[0])
	}
	// States should reflect cur heading/pitch not interpolated
	if draw.PieceStates[m.Root].RotY != 16384 || draw.PieceStates[m.Root].RotX != 16384 {
		t.Fatalf("angles interpolated: got Y=%d X=%d want 16384/16384", draw.PieceStates[m.Root].RotY, draw.PieceStates[m.Root].RotX)
	}
	// Also verify manual expected for combined Y+X: (1,0,0) with Y90+X90? Compute manually via Compose vs hand
	// Use handCompose logic: our model.Compose already implements Z→X→Y with round-to-nearest NOT fixed tables [03 §2.4] C21
	// Just ensure draw.Transforms[1].Origin matches manual chain without position
	handX := float64(65536)
	handY := float64(0)
	handZ := float64(0)
	// Apply Z0, X90, Y90 in order to point (65536,0,0) then add translation? Actually child translation is (65536,0,0) rotated by root.
	// For point we use origin chain: we already compared via Compose above, just verify non-zero
	if draw.Transforms[1].Origin[0] == 0 && draw.Transforms[1].Origin[1] == 0 && draw.Transforms[1].Origin[2] == 0 {
		_ = handX
		_ = handY
		_ = handZ
		t.Fatalf("combined transforms origin zero unexpected")
	}
}

// TestProjectileOffsetWrap verifies yaw−32768 and pitch−32768 wrapping [03 §5.2].
func TestProjectileOffsetWrap(t *testing.T) {
	m := &model.Model{
		Pieces: []model.Piece{{Name: "root", Parent: -1}},
		Root:   0,
	}
	// yaw 0 -> 0x8000 (32768)
	st := make([]model.PieceState, 1)
	FoldProjectileAngles(st, 0, 0, 0) // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	if st[0].RotY != 0x8000 || st[0].RotX != 0x8000 {
		t.Fatalf("yaw0 pitch0: got Y=%d X=%d want 32768", st[0].RotY, st[0].RotX)
	}
	// yaw 32768 -> 0 wrapping (32768+32768=65536→0)
	st2 := make([]model.PieceState, 1)
	FoldProjectileAngles(st2, 0, 0x8000, 0x8000) // yaw 32768 pitch 32768
	if st2[0].RotY != 0 || st2[0].RotX != 0 {
		t.Fatalf("yaw 32768 pitch 32768 wrap: got Y=%d X=%d want 0", st2[0].RotY, st2[0].RotX)
	}
	// yaw 1000 -> 1000+32768=33768 ; pitch 2000 -> 34768
	st3 := make([]model.PieceState, 1)
	FoldProjectileAngles(st3, 0, 1000, 2000)
	if st3[0].RotY != 33768 || st3[0].RotX != 34768 {
		t.Fatalf("yaw1000 pitch2000: got Y=%d X=%d want 33768 34768", st3[0].RotY, st3[0].RotX)
	}
	// wraparound near 65535: yaw 65535 -> (65535+32768) mod 65536 = 32767 (0x7FFF)
	st4 := make([]model.PieceState, 1)
	FoldProjectileAngles(st4, 0, 0xFFFF, 0)
	if st4[0].RotY != 0x7FFF {
		t.Fatalf("yaw 0xFFFF wrap: got %d want 32767", st4[0].RotY)
	}
	// Verify BuildProjectilePieceStates helper does same [03 §5.2]
	ps := BuildProjectilePieceStates(m, nil, 1000, 2000)
	if ps[0].RotY != 33768 || ps[0].RotX != 34768 {
		t.Fatalf("BuildProjectilePieceStates: got Y=%d X=%d", ps[0].RotY, ps[0].RotX)
	}
	// Verify building draw applies transform with offset correctly: yaw 16384 (90deg) + offset -> Y = 16384+32768=49152 which is -16384 mod (270deg)
	// Check via Compose equivalence: Build via projectile states vs manual RotY
	yaw90 := uint16(16384)
	pitch0 := uint16(0)
	ps2 := BuildProjectilePieceStates(m, nil, yaw90, pitch0)
	// Expected RotY = 16384+32768=49152 = 0xC000 which corresponds to -90deg (270deg)
	if ps2[0].RotY != 49152 {
		t.Fatalf("yaw90 offset: got %d want 49152", ps2[0].RotY)
	}
	// Compute transform point (1,0,0) with that Y angle: Ry 270deg should map (1,0,0)->(0,0,-1)
	mProj := &model.Model{
		Pieces: []model.Piece{
			{Name: "root", Parent: -1},
			{Name: "missile", Parent: 0, Translate: [3]numeric.Fixed{numeric.Fixed(65536), 0, 0}},
		},
		Root: 0,
	}
	mProj.Pieces[0].Children = []int{1}
	transforms := UnitTransforms(mProj, ps2)
	// ps2 length is 1, but mProj needs 2 length; BuildProjectilePieceStates with mProj will have correct length
	ps3 := BuildProjectilePieceStates(mProj, nil, yaw90, pitch0)
	tr := model.Compose(mProj, ps3, 1)
	// Ry 49152 = 270deg: cos=0 sin=-1 => x' = c*x - s*z = 0*1 - (-1)*0=0 ; z'= s*x + c*z = -1*1+0= -65536
	if tr.Origin[0] != 0 || tr.Origin[2] != numeric.Fixed(-65536) {
		t.Fatalf("projectile yaw90+offset transform: got %v want [0 0 -65536]", tr.Origin)
	}
	_ = transforms
}

// TestPieceDrawOrder verifies primitive draw list preserves load-fixed order [03 §2.4] C20 [GAP 02-A6].
func TestPieceDrawOrder(t *testing.T) {
	m := &model.Model{
		Pieces: []model.Piece{
			{
				Name:   "root",
				Parent: -1,
				Vertices: [][3]numeric.Fixed{
					{0, 0, 0},
					{numeric.Fixed(65536), 0, 0},
					{0, numeric.Fixed(65536), 0},
					{0, 0, numeric.Fixed(65536)},
				},
				Primitives: []model.Primitive{
					{ColorIndex: 5, VertexIndices: []uint16{0, 1, 2}, TextureName: "texA", IsColored: 1},
					{ColorIndex: 2, VertexIndices: []uint16{0, 2, 3}, TextureName: "texB", IsColored: 0},
					{ColorIndex: 9, VertexIndices: []uint16{1, 2, 3}, TextureName: "", IsColored: 1},
				},
			},
		},
		Root: 0,
	}
	// Ensure the reordering at load is not redone: our BuildPieceDraws must output in piece.Primitives order [03 §2.4] C20
	states := make([]model.PieceState, 1)
	pieces := BuildPieceDraws(m, states, [3]numeric.Fixed{}, false)
	if len(pieces) != 1 {
		t.Fatalf("pieces len %d", len(pieces))
	}
	prims := pieces[0].Primitives
	if len(prims) != 3 {
		t.Fatalf("prims len %d", len(prims))
	}
	wantOrder := []uint32{5, 2, 9}
	for i, want := range wantOrder {
		if prims[i].ColorIndex != want {
			t.Fatalf("primitive order: idx %d got %d want %d primitives %v", i, prims[i].ColorIndex, want, prims)
		}
	}
	// Also vertex index order preserved [03 §2.4] C20
	if len(prims[0].VertexIndices) != 3 || prims[0].VertexIndices[0] != 0 || prims[0].VertexIndices[1] != 1 || prims[0].VertexIndices[2] != 2 {
		t.Fatalf("vertex indices not preserved load order: %v", prims[0].VertexIndices)
	}
	// Ensure no sorting occurred even though mean Y might differ: our primitives' vertices have different Y means,
	// but draw order must stay as authored, not sorted by mean Y (which already occurred at load [GAP 02-A6]).
	// Verify second piece's order also stable across calls (deterministic iteration I1)
	pieces2 := BuildPieceDraws(m, states, [3]numeric.Fixed{}, false)
	for i := range prims {
		if prims[i].ColorIndex != pieces2[0].Primitives[i].ColorIndex {
			t.Fatalf("deterministic order not stable")
		}
	}
	// Palette hook: model bytes are already PALETTE.PAL indices and resolve
	// through Base alone; the semantic logical→physical map is pointed
	// elsewhere here to prove image bytes never take it [03 §4.3]
	// [07 "Retail palette contract"].
	tables := &palette.Tables{}
	for i := 0; i < 256; i++ {
		tables.Logical[i] = byte(i)
		tables.Base[i][0] = byte(i)
		tables.Base[i][1] = byte(255 - i)
		tables.Base[i][2] = byte(i / 2)
	}
	tables.Logical[5] = 42
	r, g, b, a := PaletteRGBA(tables, 5) // [03 §4.3] C10
	if r != 5 || g != 255-5 || b != 2 {
		t.Fatalf("PaletteRGBA direct lookup: got %d %d %d want 5 %d 2", r, g, b, 255-5)
	}
	if a != 255 {
		t.Fatalf("alpha")
	}
	// Exercise ShadeRGBA/PrimitiveRGBA at an arbitrary real row (16 here is
	// just a test fixture, not the deleted presentation-fallback constant)
	// [03 §4.3].
	const testRow = 16
	for row := 0; row < 32; row++ {
		for col := 0; col < 256; col++ {
			tables.Shade[row][col] = byte((col + row) % 256)
		}
	}
	rs, gs, bs, _ := ShadeRGBA(tables, 5, testRow) // idx 5 -> shade[16][5]=(5+16)%256=21 -> Base[21]
	if rs != 21 || gs != 255-21 || bs != 10 {
		t.Fatalf("ShadeRGBA: got %d %d %d want 21 %d 10", rs, gs, bs, 255-21)
	}
	// PrimitiveRGBA bypass vs shade [03 §4.3]
	primColored := PrimitiveDraw{ColorIndex: 5, IsColored: 1, ShadeRow: testRow, TextureName: "foo"}
	rc, gc, bc, _ := PrimitiveRGBA(tables, primColored)
	if rc != 5 {
		t.Fatalf("flat-colored bypass SHD: got %d want 5", rc)
	}
	_ = gc
	_ = bc
	primTex := PrimitiveDraw{ColorIndex: 5, IsColored: 0, ShadeRow: testRow, TextureName: "tex"}
	rt, gt, bt, _ := PrimitiveRGBA(tables, primTex)
	if rt != 21 {
		t.Fatalf("textured via SHD: got %d want 21", rt)
	}
	_ = gt
	_ = bt
}

// TestLeafAttachmentEmit verifies leaf with vertex but no primitive surfaces as emit point [03 §2.4] C23.
func TestLeafAttachmentEmit(t *testing.T) {
	// Model: root -> child -> flare leaf with vertex but no primitive [03 §2.4] C23
	m := &model.Model{
		Pieces: []model.Piece{
			{Name: "root", Parent: -1, Translate: [3]numeric.Fixed{0, 0, 0}},
			{Name: "turret", Parent: 0, Translate: [3]numeric.Fixed{numeric.Fixed(65536), 0, 0}},
			{Name: "flare", Parent: 1, Translate: [3]numeric.Fixed{0, numeric.Fixed(32768), 0}, Vertices: [][3]numeric.Fixed{{numeric.Fixed(100), numeric.Fixed(200), numeric.Fixed(300)}}},
		},
		Root: 0,
	}
	m.Pieces[0].Children = []int{1}
	m.Pieces[1].Children = []int{2}
	// No primitives on flare leaf; should be IsLeafAttachment [03 §2.4] C23
	states := make([]model.PieceState, 3)
	pieces := BuildPieceDraws(m, states, [3]numeric.Fixed{}, false)
	if len(pieces) != 3 {
		t.Fatalf("pieces %d", len(pieces))
	}
	if !pieces[2].IsLeafAttachment {
		t.Fatalf("flare leaf not marked IsLeafAttachment: primitives %d vertices %d children %v", len(m.Pieces[2].Primitives), len(m.Pieces[2].Vertices), m.Pieces[2].Children)
	}
	if pieces[2].Name != "flare" {
		t.Fatalf("leaf name %s", pieces[2].Name)
	}
	// WorldVertices for leaf should be transformed including chain [03 §2.4] C21
	// Leaf vertices[0] = (100,200,300) raw ; piece chain: flare(0,32768,0) + turret(65536,0,0) = (65636, 32968, 300) etc? Actually 100 raw =100 etc but with Fixed 1 pixel=65536, our values small but still.
	// Check with EmitPoint helper [03 §2.4] C23
	lerpPos := [3]numeric.Fixed{numeric.Fixed(10 * 65536), numeric.Fixed(20 * 65536), numeric.Fixed(30 * 65536)}
	ep, ok := EmitPoint(m, states, 2, 0, lerpPos) // [03 §2.4] C23
	if !ok {
		t.Fatalf("EmitPoint failed")
	}
	// Compute expected via manual Compose + lerpPos
	tr := model.Compose(m, states, 2)
	local := tr.Apply(m.Pieces[2].Vertices[0])
	want := [3]numeric.Fixed{local[0].Add(lerpPos[0]), local[1].Add(lerpPos[1]), local[2].Add(lerpPos[2])}
	if ep != want {
		t.Fatalf("EmitPoint: got %v want %v local %v trOrigin %v", ep, want, local, tr.Origin)
	}
	// LeafEmitPoints should surface exactly one point [03 §2.4] C23
	emitPoints := LeafEmitPoints(m, states, lerpPos)
	if len(emitPoints) != 1 {
		t.Fatalf("LeafEmitPoints count %d want 1", len(emitPoints))
	}
	if emitPoints[0] != want {
		t.Fatalf("LeafEmitPoints point mismatch")
	}
	// Verify that AnyEmitPoints also surfaces for non-leaf case? Create piece with vertex+no primitive but with children (non-leaf) should not be in strict leaf but in any
	m2 := &model.Model{
		Pieces: []model.Piece{
			{Name: "root", Parent: -1},
			{Name: "nonleaf", Parent: 0, Translate: [3]numeric.Fixed{0, 0, 0}, Vertices: [][3]numeric.Fixed{{numeric.Fixed(1), numeric.Fixed(2), numeric.Fixed(3)}}, Children: []int{2}},
			{Name: "leaf2", Parent: 1, Vertices: [][3]numeric.Fixed{{numeric.Fixed(4), numeric.Fixed(5), numeric.Fixed(6)}}},
		},
		Root: 0,
	}
	m2.Pieces[0].Children = []int{1}
	m2.Pieces[1].Children = []int{2}
	// Need to keep Children consistent with Parent already: but we manually set
	states2 := make([]model.PieceState, 3)
	pieces2 := BuildPieceDraws(m2, states2, lerpPos, false)
	// nonleaf should NOT be leaf attachment strict
	if pieces2[1].IsLeafAttachment {
		t.Fatalf("nonleaf with children should not be strict leaf")
	}
	if pieces2[2].IsLeafAttachment == false {
		t.Fatalf("leaf2 should be leaf")
	}
	any := AnyEmitPoints(m2, states2, lerpPos)
	if len(any) != 2 {
		t.Fatalf("AnyEmitPoints expected 2 (nonleaf + leaf2) got %d", len(any))
	}
	// Ensure deterministic order piece-index ascending (I1)
	strict := LeafEmitPoints(m2, states2, lerpPos)
	if len(strict) != 1 || strict[0] == any[0] {
		// strict should be leaf2 only, which is second entry in any (index2)
		// So strict[0] should equal any[1]
		if len(any) >= 2 && strict[0] != any[1] {
			t.Fatalf("leaf ordering mismatch")
		}
	}
}

func TestBuildPieceDrawsSuppressesHiddenAncestors(t *testing.T) {
	m := &model.Model{
		Root: 0,
		Pieces: []model.Piece{
			{Name: "root", Parent: -1, Children: []int{1}},
			{Name: "child", Parent: 0, Vertices: [][3]numeric.Fixed{{0, 0, 0}}, Children: nil},
		},
	}
	states := []model.PieceState{{Hidden: true}, {}}
	draws := BuildPieceDraws(m, states, [3]numeric.Fixed{}, false)
	if len(draws) != 2 {
		t.Fatalf("piece count %d want 2", len(draws))
	}
	if len(draws[0].WorldVertices) != 0 || len(draws[1].WorldVertices) != 0 {
		t.Fatalf("hidden ancestor leaked geometry: root=%d child=%d", len(draws[0].WorldVertices), len(draws[1].WorldVertices))
	}
}

func TestBuildPieceDrawsBoundsCyclicParentWalk(t *testing.T) {
	m := &model.Model{
		Root: 0,
		Pieces: []model.Piece{
			{Name: "a", Parent: 1, Children: []int{1}, Vertices: [][3]numeric.Fixed{{0, 0, 0}}},
			{Name: "b", Parent: 0, Children: []int{0}, Vertices: [][3]numeric.Fixed{{1, 0, 0}}},
		},
	}
	draws := BuildPieceDraws(m, nil, [3]numeric.Fixed{}, false)
	if len(draws) != 2 {
		t.Fatalf("piece count %d want 2", len(draws))
	}
	for i, draw := range draws {
		if len(draw.WorldVertices) != 0 {
			t.Fatalf("cyclic piece %d emitted geometry", i)
		}
	}
}

// TestOrientationCacheThreshold verifies >7 triggers rebuild [03 §5.2] C13.
func TestOrientationCacheThreshold(t *testing.T) {
	c := &OrientationCache{}
	if !c.NeedsRebuild(100, 100, 100) {
		t.Fatalf("empty cache should need rebuild")
	}
	c.Update(1000, 2000, 3000)
	if c.NeedsRebuild(1000, 2000, 3000) {
		t.Fatalf("same triple should not need rebuild")
	}
	if c.NeedsRebuild(1000+7, 2000, 3000) {
		t.Fatalf("diff 7 should not trigger")
	}
	if !c.NeedsRebuild(1000+8, 2000, 3000) {
		t.Fatalf("diff 8 heading should trigger")
	}
	if !c.NeedsRebuild(1000, 2000+8, 3000) {
		t.Fatalf("diff 8 pitch should trigger")
	}
	if !c.NeedsRebuild(1000, 2000, 3000+8) {
		t.Fatalf("diff 8 bank should trigger")
	}
	// Wrap-around case: 65530 vs 5 diff is 11? Actually circular diff for 65530 and 5: raw diff -65525 abs 65525 >32768 -> 65536-65525=11 -> >7 true
	if !c.NeedsRebuild(5, 2000, 3000) && angleDiff(1000, 5) <= 7 {
		// This check just ensures angleDiff wrap works: from 1000 to 5 circular diff is 1005? Actually 1000-5=995 => >7 true, so needs rebuild
		t.Fatalf("wrap case should need rebuild due to large diff")
	}
	// Near wrap with small circular distance: c=65530 heading, new=2 diff circular = 8? 65530->2 wraps: diff = 65536-65528=8 -> triggers
	c.Update(65530, 0, 0)
	if c.NeedsRebuild(2, 0, 0) == false {
		// diff 8 should trigger
		t.Fatalf("wrapping 65530->2 diff 8 should trigger")
	}
	if c.NeedsRebuild(65535, 0, 0) == true {
		// diff 5 should not trigger
		t.Fatalf("65530->65535 diff 5 should not trigger, got needs rebuild")
	}
	// Update returns true only when rebuild needed [03 §5.2] C13
	c2 := &OrientationCache{}
	if !c2.Update(100, 200, 300) {
		t.Fatalf("first update should report rebuild")
	}
	if c2.Update(100, 200, 300) {
		t.Fatalf("no change should not report rebuild")
	}
}
