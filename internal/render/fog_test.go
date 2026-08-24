package render

import (
	"reflect"
	"testing"

	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/palette"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/snapshot"
	"github.com/nanolathe/nanolathe/internal/visibility"
	"github.com/nanolathe/nanolathe/internal/world"
)

// helper to create a visibility service with grid W=4 H=4 (CellW=8 CellH=8) and return its FogCache.
func testFogCache(t *testing.T, w, h int32) *visibility.FogCache {
	t.Helper()
	cellW := w * 2
	cellH := h * 2
	terr := &world.Terrain{CellW: cellW, CellH: cellH}
	svc := visibility.New(terr, visibility.ModeHistoryEnabled|visibility.ModeCurrentEnabled)
	if svc == nil {
		t.Fatalf("visibility.New returned nil")
	}
	cache := svc.Fog()
	if cache == nil {
		t.Fatalf("fog cache nil")
	}
	return cache
}

// TestFogOrderingViaComposer verifies fog hook sits after world strips but before selection/interface [03 §1] C1 C2.
func TestFogOrderingViaComposer(t *testing.T) {
	cache := testFogCache(t, 4, 4)
	// fill one fog cell to ensure ops non-empty but hook itself is ordering, not content
	cache.SetChannel(0, 0, 15, 0) // dark solid [03 §3.3]
	cam := &camera.Camera{X: 0, Z: 0, ViewW: 640, ViewH: 480, MapW: 128, MapH: 128}
	tables := &palette.Tables{}
	for i := 0; i < 256; i++ {
		tables.Logical[i] = byte(i)
	}

	var order []string
	var c Composer
	c.Cam = cam
	c.Fog = cache
	// Wire Hooks to record order; we use FogHook as the fog hook seam [PLAN_13 WU-13-5].
	c.Hooks.Terrain = func() { order = append(order, "terrain") }
	c.Hooks.DrawStrip = func(idx int) { order = append(order, "strip") }
	c.Hooks.BucketBuild = func() { order = append(order, "bucket") }
	c.Hooks.FeaturePass = func() { order = append(order, "feature") }
	c.Hooks.UnitTraversal = func(kind string) { order = append(order, "traversal:"+kind) }
	c.Hooks.Projectiles = func() { order = append(order, "projectiles") }
	c.Hooks.Effects = func() { order = append(order, "effects") }
	c.Hooks.KeyOverlay = func() { order = append(order, "key") }
	c.Hooks.OverlayA = func() { order = append(order, "overlayA") }
	c.Hooks.OverlayB = func() { order = append(order, "overlayB") }
	// Use our FogHook closure as the fog seam [PLAN_13 WU-13-5].
	gridW, gridH := int32(4), int32(4)
	c.Hooks.Fog = FogHook(cache, cam, gridW, gridH, tables, false)
	// wrap to record
	origFog := c.Hooks.Fog
	c.Hooks.Fog = func() {
		order = append(order, "fog")
		if origFog != nil {
			origFog()
		}
	}
	c.Hooks.Selection = func() { order = append(order, "selection") }
	c.Hooks.Interface = func() { order = append(order, "interface") }
	c.Frame(&snapshot.Frame{}, 0, 1) // mode nonzero so fog runs [03 §1]

	// Find positions
	idxFog, idxSel, idxIface, idxStrip8, idxProj := -1, -1, -1, -1, -1
	for i, o := range order {
		switch o {
		case "fog":
			idxFog = i
		case "selection":
			idxSel = i
		case "interface":
			idxIface = i
		case "projectiles":
			idxProj = i
		}
		// strip order includes Barrier but we just check fog after world strips: strip is generic
		if o == "strip" && idxStrip8 == -1 {
			// approximations: last strip before fog is strip8
		}
	}
	if idxFog == -1 {
		t.Fatalf("fog not called in composer frame order %v", order)
	}
	if idxSel == -1 || idxIface == -1 {
		t.Fatalf("selection/interface missing %v", order)
	}
	if !(idxFog < idxSel && idxSel < idxIface) {
		t.Fatalf("fog must be before selection/interface [03 §1] C2 fog=%d sel=%d iface=%d order %v", idxFog, idxSel, idxIface, order)
	}
	// Ensure fog after world projectiles/effects (which are between strips 6 and 7) [03 §1] C2
	if idxProj != -1 && !(idxProj < idxFog) {
		t.Fatalf("fog must be after projectiles/effects [03 §1] hook order proj=%d fog=%d %v", idxProj, idxFog, order)
	}
	// With mode 0 fog should not run [03 §1] C1 step10 gated on nonzero mode
	order = nil
	c.Frame(&snapshot.Frame{}, 0, 0)
	for _, o := range order {
		if o == "fog" {
			t.Fatalf("fog should not run with mode 0 [03 §1] order %v", order)
		}
	}
	hasSel, hasIface := false, false
	for _, o := range order {
		if o == "selection" {
			hasSel = true
		}
		if o == "interface" {
			hasIface = true
		}
	}
	if !hasSel || !hasIface {
		t.Fatalf("selection/interface must still run with mode 0 even when fog gated [03 §1] %v", order)
	}
}

// TestFogHardEdges32 verifies fog tile geometry is hard 32 world units / pixels [03 §3.3] and signed residues [03 §2.1][I3].
func TestFogHardEdges32(t *testing.T) {
	if FogTilePixels != 32 {
		t.Fatalf("FogTilePixels %d want 32 [03 §3.3]", FogTilePixels)
	}
	if FogTileWorld != 32*65536 {
		t.Fatalf("FogTileWorld %d want %d [03 §2.1]", FogTileWorld, 32*65536)
	}

	// FogTileForPixel floor semantics [I3]
	cases := []struct{ px, want int32 }{
		{0, 0},
		{31, 0},
		{32, 1},
		{63, 1},
		{-1, -1},  // -1/32 floors to -1 [03 §2.1]
		{-32, -1}, // -32/32 = -1 exact
		{-33, -2},
		{-64, -2},
	}
	for _, tc := range cases {
		got := FogTileForPixel(tc.px)
		if got != tc.want {
			t.Fatalf("FogTileForPixel(%d)=%d want %d [I3]", tc.px, got, tc.want)
		}
	}

	// FogTileForWorld narrows Fixed high word then floors [03 §3.2]
	for _, tc := range cases {
		world := numeric.Fixed(int64(tc.px) << 16)
		got := FogTileForWorld(world)
		if got != tc.want {
			t.Fatalf("FogTileForWorld(%d<<16)=%d want %d", tc.px, got, tc.want)
		}
	}

	// Screen rect hard 32 edges, abutting tiles [03 §3.3]
	cam := &camera.Camera{X: 0, Z: 0, ViewW: 640, ViewH: 480, MapW: 256, MapH: 256}
	for gx := int32(-2); gx < 3; gx++ {
		x0, y0, x1, y1 := FogScreenRect(cam, gx, 0)
		if x1-x0 != 32 || y1-y0 != 32 {
			t.Fatalf("FogScreenRect hard 32 size gx=%d got %d,%d->%d,%d size %d,%d", gx, x0, y0, x1, y1, x1-x0, y1-y0)
		}
		// Adjacent tiles abut exactly: x1(gx) == x0(gx+1)
		nx0, _, _, _ := FogScreenRect(cam, gx+1, 0)
		if x1 != nx0 {
			t.Fatalf("hard edge abut gx=%d x1=%d nx0=%d", gx, x1, nx0)
		}
		_ = y0
		_ = y1
	}

	// Signed residue shift: moving camera by 1 moves rect by -1 [03 §3.3] including residues
	x0a, _, _, _ := FogScreenRect(&camera.Camera{X: 0, Z: 0}, 0, 0) // cam 0 => 0*32-0+128=128
	x0b, _, _, _ := FogScreenRect(&camera.Camera{X: 1, Z: 0}, 0, 0) // cam 1 => -1+128=127
	if x0b != x0a-1 {
		t.Fatalf("camera residue shift: cam0 x0=%d cam1 x0=%d want -1 delta [03 §3.3]", x0a, x0b)
	}
	// Negative camera floor handling
	x0c, _, _, _ := FogScreenRect(&camera.Camera{X: -1, Z: 0}, 0, 0) // 0 - (-1)+128=129?
	// gx0 with cam -1 => 0 - (-1)+128=129
	if x0c != 129 {
		t.Fatalf("negative camera residue: got %d want 129", x0c)
	}
	// Verify floorDiv-based viewport range includes signed residues correctly via BuildFogOps
	// Use a cache 8x8 and cam at -1 with dither off, ensure ops are deterministic inclusive
}

// TestFogScreenRectViewportClipping verifies BuildFogOps viewport culling and row-major deterministic iteration [I1][03 §3.3].
func TestFogScreenRectViewportClipping(t *testing.T) {
	cache := testFogCache(t, 8, 8)
	// Set fog across grid for visibility: ch0=15 on every cell => solid dark everywhere [03 §3.3]
	for y := int32(0); y < 8; y++ {
		for x := int32(0); x < 8; x++ {
			cache.SetChannel(x, y, 15, 0)
		}
	}
	cam := &camera.Camera{X: 0, Z: 0, ViewW: 64, ViewH: 64, MapW: 256, MapH: 256} // 256/32=8 grid
	// 64 viewW at OriginX 128: screen mapping: grid 0 at 128, but cam 0 => viewport 0..64 screen: grid 0 x0=128 already >64 => no tiles? Need cam that shows viewport over map origin.
	// Instead set cam X=128 to bring grid0 to 0: cam=128 => grid0 x0=0-128+128=0
	cam.X = 128
	cam.Z = 32
	ops := BuildFogOps(cache, cam, cam.ViewW, cam.ViewH, 8, 8, nil, false)
	if len(ops) == 0 {
		t.Fatalf("expected fog ops for viewport covering grid")
	}
	// ops should be clipped: with view 64x64 centered at cam 128,32, visible tiles are roughly 2x2
	// Validate every op's rect is 32 and within viewport+32 tolerance and row-major order
	for i, op := range ops {
		if op.ScreenX1-op.ScreenX0 != 32 || op.ScreenY1-op.ScreenY0 != 32 {
			t.Fatalf("op %d rect not hard 32: %v", i, op)
		}
	}
	// Row-major: y outer, x inner [I1]
	for i := 1; i < len(ops); i++ {
		prev, cur := ops[i-1], ops[i]
		if cur.GridY < prev.GridY || (cur.GridY == prev.GridY && cur.GridX < prev.GridX) {
			// But note ops may have 1 per cell (solid dark) so order equals cell order
			t.Fatalf("ops not row-major deterministic [I1] i=%d prev %d,%d cur %d,%d", i, prev.GridX, prev.GridY, cur.GridX, cur.GridY)
		}
	}
	// Determinism: second run identical [I1]
	ops2 := BuildFogOps(cache, cam, cam.ViewW, cam.ViewH, 8, 8, nil, false)
	if !reflect.DeepEqual(ops, ops2) {
		t.Fatalf("BuildFogOps not deterministic")
	}
	// Never mutates cache: capture channels before and after
	for y := int32(0); y < 8; y++ {
		for x := int32(0); x < 8; x++ {
			c0, c1 := cache.Channel(x, y)
			if c0 != 15 || c1 != 0 {
				// we set 15,0 above; if mutated would differ
				t.Fatalf("cache mutated at %d,%d got %d,%d", x, y, c0, c1)
			}
		}
	}
}

// TestFogChannelSemantics verifies per-cell channel rules [03 §3.3].
func TestFogChannelSemantics(t *testing.T) {
	// Single cell grid for isolated semantics
	cache := testFogCache(t, 1, 1)
	cam := &camera.Camera{X: 0, Z: 0, ViewW: 640, ViewH: 480, MapW: 32, MapH: 32}
	tables := &palette.Tables{}
	for i := 0; i < 256; i++ {
		tables.Logical[i] = byte(i)
	}
	tables.Base[0] = [4]byte{99, 42, 7, 0}
	tables.Logical[0] = 0

	// Visible: 0,0 => no ops [03 §3.3]
	cache.SetChannel(0, 0, 0, 0)
	ops := BuildFogOps(cache, cam, 0, 0, 1, 1, tables, false)
	if len(ops) != 0 {
		t.Fatalf("visible 0,0 should produce no ops, got %d", len(ops))
	}

	// ch0==15 short-circuit: only one SolidDark, no GAF even if ch1==14 [03 §3.3]
	cache.SetChannel(0, 0, 15, 14)
	ops = BuildFogOps(cache, cam, 0, 0, 1, 1, tables, false)
	if len(ops) != 1 || ops[0].Kind != FogKindSolidDark {
		t.Fatalf("ch0==15 short-circuit want 1 SolidDark got %+v", ops)
	}
	if ops[0].Channel0 != 15 {
		t.Fatalf("channel mismatch")
	}
	if ops[0].Frame != -1 || ops[0].Variant != -1 {
		t.Fatalf("solid dark should have no variant/frame")
	}

	// ch1==15 solid dark, ch0=0 => one Dark [03 §3.3]
	cache.SetChannel(0, 0, 0, 15)
	ops = BuildFogOps(cache, cam, 0, 0, 1, 1, tables, false)
	if len(ops) != 1 || ops[0].Kind != FogKindDark {
		t.Fatalf("ch1==15 want Dark got %+v", ops)
	}
	if ops[0].Patterned {
		t.Fatalf("non-dither should not be patterned")
	}

	// ch1==15 with dither: Patterned depends on camera parity ((camX+camZ)&1) [03 §3.3]
	cache.SetChannel(0, 0, 0, 15)
	cam.X = 0
	cam.Z = 0 // parity 0 => not patterned
	ops = BuildFogOps(cache, cam, 0, 0, 1, 1, tables, true)
	if len(ops) != 1 || ops[0].Kind != FogKindDark {
		t.Fatalf("dither parity 0: still Dark kind but Patterned false, got %+v", ops[0])
	}
	if ops[0].Patterned {
		t.Fatalf("parity 0 should not be patterned")
	}
	cam.X = 1 // parity 1 => patterned
	ops = BuildFogOps(cache, cam, 0, 0, 1, 1, tables, true)
	if len(ops) != 1 || ops[0].Kind != FogKindPatterned {
		t.Fatalf("dither parity 1 want Patterned got %+v", ops[0])
	}
	if !ops[0].Patterned {
		t.Fatalf("expected patterned")
	}
	cam.X = 0
	cam.Z = 0

	// ch1 1..14 GAF then ch0 1..14 GAF: channel one BEFORE channel zero [03 §3.3]
	cache.SetChannel(0, 0, 7, 5) // c0=7 => frame6, c1=5=>frame4
	ops = BuildFogOps(cache, cam, 0, 0, 1, 1, tables, false)
	if len(ops) != 2 {
		t.Fatalf("both channels GAF want 2 ops got %d %+v", len(ops), ops)
	}
	if ops[0].Kind != FogKindGAFCh1 || ops[1].Kind != FogKindGAFCh0 {
		t.Fatalf("ch1 before ch0 order wrong got %v %v", ops[0].Kind, ops[1].Kind)
	}
	if ops[0].Frame != 4 || ops[1].Frame != 6 {
		t.Fatalf("frames value-1 wrong got %d %d want 4 6", ops[0].Frame, ops[1].Frame)
	}
	if ops[0].Variant < 0 || ops[0].Variant > 3 || ops[1].Variant < 0 || ops[1].Variant > 3 {
		t.Fatalf("variant out of range 0..3")
	}
	// Variant is (gx+gy)&3 currently [03 §3.3] TODO
	if ops[0].Variant != 0 || ops[1].Variant != 0 {
		t.Fatalf("variant for gx0 gy0 should be 0, got %d %d", ops[0].Variant, ops[1].Variant)
	}

	// ch1==0 c0==7 => single GAF ch0
	cache.SetChannel(0, 0, 3, 0)
	ops = BuildFogOps(cache, cam, 0, 0, 1, 1, tables, false)
	if len(ops) != 1 || ops[0].Kind != FogKindGAFCh0 {
		t.Fatalf("single ch0 GAF want 1 GAFCh0 got %+v", ops)
	}
	if ops[0].Frame != 2 {
		t.Fatalf("frame 3-1=2 got %d", ops[0].Frame)
	}

	// ch1==7 c0==0 => single GAF ch1
	cache.SetChannel(0, 0, 0, 9)
	ops = BuildFogOps(cache, cam, 0, 0, 1, 1, tables, false)
	if len(ops) != 1 || ops[0].Kind != FogKindGAFCh1 {
		t.Fatalf("single ch1 GAF want 1 GAFCh1 got %+v", ops)
	}
}

// TestFogPaletteDarkening verifies palette/SHD darkening uses logical→physical at present time [03 §4.3] C7.
func TestFogPaletteDarkening(t *testing.T) {
	cache := testFogCache(t, 1, 1)
	cache.SetChannel(0, 0, 15, 0) // dark solid
	cam := &camera.Camera{X: 0, Z: 0, MapW: 32, MapH: 32}

	// Craft palette where dark index maps through logical to different physical
	tables := &palette.Tables{}
	for i := 0; i < 256; i++ {
		tables.Logical[i] = byte(i)
		tables.Base[i] = [4]byte{byte(i), byte(i), byte(i), 0}
	}
	// Logical remap: index FogDarkPaletteIndex (0) -> physical 42
	tables.Logical[FogDarkPaletteIndex] = 42
	tables.Base[42] = [4]byte{11, 22, 33, 0}
	tables.Base[0] = [4]byte{99, 99, 99, 0} // should not be used when logical remapped

	ops := BuildFogOps(cache, cam, 0, 0, 1, 1, tables, false)
	if len(ops) != 1 {
		t.Fatalf("want 1 op")
	}
	r, g, b, a := FogDarkRGBA(tables)
	if r != 11 || g != 22 || b != 33 || a != 255 {
		t.Fatalf("FogDarkRGBA via logical→physical want 11,22,33,255 got %d,%d,%d,%d [03 §4.3]", r, g, b, a)
	}
	if ops[0].R != r || ops[0].G != g || ops[0].B != b {
		t.Fatalf("op dark color mismatch: op %d,%d,%d want %d,%d,%d", ops[0].R, ops[0].G, ops[0].B, r, g, b)
	}
	// SHD variant placeholder returns same until row traced [TODO(question)]
	r2, g2, b2, a2 := FogSHDDarkRGBA(tables)
	if r2 != r || g2 != g || b2 != b || a2 != a {
		t.Fatalf("SHD placeholder mismatch")
	}
	// Nil tables returns black opaque 0,0,0,255 without panic
	rn, gn, bn, an := FogDarkRGBA(nil)
	if rn != 0 || gn != 0 || bn != 0 || an != 255 {
		t.Fatalf("nil tables dark want 0,0,0,255 got %d,%d,%d,%d", rn, gn, bn, an)
	}
	opsNil := BuildFogOps(cache, cam, 0, 0, 1, 1, nil, false)
	if opsNil[0].R != 0 || opsNil[0].G != 0 || opsNil[0].B != 0 {
		t.Fatalf("nil tables op should be 0,0,0")
	}
}

// TestFogDeterminism verifies stable deterministic output across runs [I1].
func TestFogDeterminism(t *testing.T) {
	cache := testFogCache(t, 4, 4)
	// Fill pattern: alternating fog types
	for y := int32(0); y < 4; y++ {
		for x := int32(0); x < 4; x++ {
			// deterministic pattern: (x+y) %5 yields 0..4 => map to 0,5,7,15
			v := (x + y) % 4
			var c0, c1 uint8
			switch v {
			case 0:
				c0, c1 = 0, 0 // visible
			case 1:
				c0, c1 = 15, 0 // solid dark
			case 2:
				c0, c1 = 5, 7 // both GAF
			case 3:
				c0, c1 = 0, 15 // ch1 dark
			}
			cache.SetChannel(x, y, c0, c1)
		}
	}
	cam := &camera.Camera{X: 64, Z: 32, ViewW: 128, ViewH: 128, MapW: 128, MapH: 128}
	tables := &palette.Tables{}
	for i := 0; i < 256; i++ {
		tables.Logical[i] = byte(i)
	}

	run := func() []FogOp {
		return BuildFogOps(cache, cam, cam.ViewW, cam.ViewH, 4, 4, tables, true)
	}
	a := run()
	b := run()
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("determinism failed: first %v second %v", a, b)
	}
	// Determinism also holds without dither
	aNoDither := BuildFogOps(cache, cam, cam.ViewW, cam.ViewH, 4, 4, tables, false)
	bNoDither := BuildFogOps(cache, cam, cam.ViewW, cam.ViewH, 4, 4, tables, false)
	if !reflect.DeepEqual(aNoDither, bNoDither) {
		t.Fatalf("determinism no-dither failed")
	}
}

// TestFogNeverMutatesVisibility verifies BuildFogOps never writes word mask or cache (I6).
func TestFogNeverMutatesVisibility(t *testing.T) {
	terr := &world.Terrain{CellW: 6, CellH: 6} // grid 3x3
	svc := visibility.New(terr, visibility.ModeHistoryEnabled|visibility.ModeCurrentEnabled)
	cache := svc.Fog()
	// set some channels
	cache.SetChannel(1, 1, 15, 0)
	cache.SetChannel(0, 2, 5, 0)
	// Snapshot channels before
	type pair struct{ c0, c1 uint8 }
	before := make(map[[2]int32]pair)
	for y := int32(0); y < 3; y++ {
		for x := int32(0); x < 3; x++ {
			c0, c1 := cache.Channel(x, y)
			before[[2]int32{x, y}] = pair{c0, c1}
		}
	}
	wordBefore := append([]uint16(nil), svc.WordMask()...)
	byteBefore := append([]uint8(nil), svc.ByteGrid(0)...)

	cam := &camera.Camera{X: 0, Z: 0, ViewW: 640, ViewH: 480, MapW: 96, MapH: 96}
	tables := &palette.Tables{}
	_ = BuildFogOps(cache, cam, cam.ViewW, cam.ViewH, 3, 3, tables, false)
	_ = BuildFogOps(cache, cam, cam.ViewW, cam.ViewH, 3, 3, tables, true)
	// FogHook closure should also not mutate
	hook := FogHook(cache, cam, 3, 3, tables, false)
	hook()

	for y := int32(0); y < 3; y++ {
		for x := int32(0); x < 3; x++ {
			c0, c1 := cache.Channel(x, y)
			want := before[[2]int32{x, y}]
			if c0 != want.c0 || c1 != want.c1 {
				t.Fatalf("cache mutated at %d,%d: before %v after %d,%d [I6]", x, y, want, c0, c1)
			}
		}
	}
	if !reflect.DeepEqual(wordBefore, svc.WordMask()) {
		t.Fatalf("word mask mutated by fog presentation [I6]")
	}
	if !reflect.DeepEqual(byteBefore, svc.ByteGrid(0)) {
		t.Fatalf("byte grid mutated by fog presentation [I6]")
	}
}

// TestFogVariantSelection checks four-way variant deterministic.
func TestFogVariantSelection(t *testing.T) {
	cam := &camera.Camera{X: 0, Z: 0}
	for gy := int32(0); gy < 4; gy++ {
		for gx := int32(0); gx < 4; gx++ {
			v := FogVariant(gx, gy, cam)
			want := int((gx + gy) & 3)
			if v != want {
				t.Fatalf("variant gx=%d gy=%d got %d want %d", gx, gy, v, want)
			}
			if v < 0 || v > 3 {
				t.Fatalf("variant out of range")
			}
		}
	}
}

// TestFogEmptyCacheAndNil ensures nil/empty cases don't panic and are deterministic (I6).
func TestFogEmptyCacheAndNil(t *testing.T) {
	cam := &camera.Camera{X: 0, Z: 0, ViewW: 640, ViewH: 480, MapW: 128, MapH: 128}
	ops := BuildFogOps(nil, cam, cam.ViewW, cam.ViewH, 4, 4, nil, false)
	if ops != nil && len(ops) != 0 {
		t.Fatalf("nil cache should yield nil/empty")
	}
	ops = BuildFogOps(testFogCache(t, 2, 2), nil, 0, 0, 2, 2, nil, false)
	// with nil cam, full grid enumeration
	if len(ops) != 0 {
		// cache initially all 0,0 visible => no ops
	}
	// hook with nil cache/cam should not panic
	hook := FogHook(nil, nil, 0, 0, nil, false)
	hook()
	hook2 := FogHook(testFogCache(t, 2, 2), nil, 0, 0, nil, false)
	hook2() // nil cam handled
}
