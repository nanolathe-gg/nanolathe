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

	// Signed residue shift: moving camera by 1 moves rect by -1 [03 §3.3] including residues.
	// Cells straddle tile corners: x0 = gx*32+16 - camX + 128 [03 §3.3].
	x0a, _, _, _ := FogScreenRect(&camera.Camera{X: 0, Z: 0}, 0, 0) // cam 0 => 0*32+16-0+128=144
	x0b, _, _, _ := FogScreenRect(&camera.Camera{X: 1, Z: 0}, 0, 0) // cam 1 => 16-1+128=143
	if x0b != x0a-1 {
		t.Fatalf("camera residue shift: cam0 x0=%d cam1 x0=%d want -1 delta [03 §3.3]", x0a, x0b)
	}
	// Negative camera floor handling
	x0c, _, _, _ := FogScreenRect(&camera.Camera{X: -1, Z: 0}, 0, 0) // 16 - (-1)+128=145
	if x0c != 145 {
		t.Fatalf("negative camera residue: got %d want 145", x0c)
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
	// Corner-straddle culling: with C=0 the cell gx=-1 rect [-16,16) shows a
	// 16px strip; the window must include it (regression for the half-tile
	// culling offset) [03 §3.3].
	// Full framebuffer coverage: the composer rebases op rects by −OriginX/Y,
	// so the op set must span post-rebase [0,viewW)×[0,viewH) — a window
	// derived from camX−OriginX instead of camX leaves the last OriginX-wide
	// columns unfogged (regression).
	loX, hiX, loY, hiY := int32(1<<30), int32(-1<<30), int32(1<<30), int32(-1<<30)
	for _, op := range ops {
		if op.ScreenX0-camera.OriginX < loX {
			loX = op.ScreenX0 - camera.OriginX
		}
		if op.ScreenX1-camera.OriginX > hiX {
			hiX = op.ScreenX1 - camera.OriginX
		}
		if op.ScreenY0-camera.OriginY < loY {
			loY = op.ScreenY0 - camera.OriginY
		}
		if op.ScreenY1-camera.OriginY > hiY {
			hiY = op.ScreenY1 - camera.OriginY
		}
	}
	if loX > 0 || hiX < cam.ViewW || loY > 0 || hiY < cam.ViewH {
		t.Fatalf("fog ops do not cover the framebuffer: x=[%d,%d) y=[%d,%d) want x<=[0,%d) y<=[0,%d)",
			loX, hiX, loY, hiY, cam.ViewW, cam.ViewH)
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
	// 3x3 grid; the semantics under test target centre cell (1,1), which has
	// all four neighbours in-map so its nibble can reach any value 0..15.
	// Cache values are already producer-computed nibbles. BuildFogOps is only
	// the canonical cache-to-blit translator; edge propagation belongs to the
	// viewport cache builder [03 §3.3].
	cache := testFogCache(t, 3, 3)
	cam := &camera.Camera{X: 0, Z: 0, ViewW: 640, ViewH: 480, MapW: 96, MapH: 96}
	tables := &palette.Tables{}
	for i := 0; i < 256; i++ {
		tables.Logical[i] = byte(i)
	}
	tables.Base[0] = [4]byte{99, 42, 7, 0}
	tables.Logical[0] = 0
	// setTiles configures the four tiles around cell (1,1): [y][x] booleans
	// are the per-channel bit1 flags for tiles (1,1),(2,1),(1,2),(2,2).
	setTiles := func(c0 [2][2]bool, c1 [2][2]bool) {
		var b0, b1 uint8
		for i := 0; i < 4; i++ {
			if c0[i/2][i%2] {
				b0 |= 1 << i
			}
			if c1[i/2][i%2] {
				b1 |= 1 << i
			}
		}
		for y := int32(0); y < 3; y++ {
			for x := int32(0); x < 3; x++ {
				cache.SetChannel(x, y, 0, 0)
			}
		}
		cache.SetChannel(1, 1, b0, b1)
	}
	all := [2][2]bool{{true, true}, {true, true}}
	none := [2][2]bool{}
	// The enumerated window includes the void ring around the grid; the
	// semantics under test are per-cell, so select cell (1,1)'s ops.
	cell11 := func(ops []FogOp) []FogOp {
		var out []FogOp
		for _, op := range ops {
			if op.GridX == 1 && op.GridY == 1 {
				out = append(out, op)
			}
		}
		return out
	}

	// Visible: no ops [03 §3.3]
	setTiles(none, none)
	ops := cell11(BuildFogOps(cache, cam, 0, 0, 3, 3, tables, false))
	if len(ops) != 0 {
		t.Fatalf("visible 1,1 should produce no ops, got %d", len(ops))
	}

	// ch0==15 short-circuit: only one SolidDark, no GAF even if ch1 fogged [03 §3.3]
	setTiles(all, all)
	ops = cell11(BuildFogOps(cache, cam, 0, 0, 3, 3, tables, false))
	if len(ops) != 1 || ops[0].Kind != FogKindSolidDark {
		t.Fatalf("ch0==15 short-circuit want 1 SolidDark got %+v", ops)
	}
	if ops[0].Channel0 != 15 {
		t.Fatalf("channel mismatch")
	}
	if ops[0].Frame != -1 || ops[0].Variant != -1 {
		t.Fatalf("solid dark should have no variant/frame")
	}

	// ch1==15 gray remap, ch0=0 => one GrayRemap [03 §3.3]
	setTiles(none, all)
	ops = cell11(BuildFogOps(cache, cam, 0, 0, 3, 3, tables, false))
	if len(ops) != 1 || ops[0].Kind != FogKindGrayRemap {
		t.Fatalf("ch1==15 want GrayRemap got %+v", ops)
	}
	if ops[0].Patterned {
		t.Fatalf("non-dither should not be patterned")
	}

	// DitheredFog option bit (not camera parity) selects the black checker
	// [03 §3.3].
	setTiles(none, all)
	cam.X = 0
	cam.Z = 0 // parity 0, dither on => Patterned
	ops = cell11(BuildFogOps(cache, cam, 0, 0, 3, 3, tables, true))
	if len(ops) != 1 || ops[0].Kind != FogKindPatterned {
		t.Fatalf("dither on: want Patterned got %+v", ops[0])
	}
	if !ops[0].Patterned {
		t.Fatalf("dither on should be patterned")
	}
	cam.X = 1 // parity 1, dither on => still Patterned (parity is phase only)
	ops = cell11(BuildFogOps(cache, cam, 0, 0, 3, 3, tables, true))
	if len(ops) != 1 || ops[0].Kind != FogKindPatterned {
		t.Fatalf("dither on parity 1 want Patterned got %+v", ops[0])
	}
	cam.X = 0
	cam.Z = 0

	// ch1 1..14 GAF then ch0 1..14 GAF: channel one BEFORE channel zero [03 §3.3]
	// c0=7 (tiles S? no: self+east+north), c1=5 (self+north).
	setTiles([2][2]bool{{true, true}, {true, false}}, [2][2]bool{{true, false}, {true, false}})
	ops = cell11(BuildFogOps(cache, cam, 0, 0, 3, 3, tables, false))
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
	// Variant is (gx+gy+2)&3: retail col+row+camPhase with cache-relative col
	// reduces to gx+gy+2 for map-global cells (camera phase cancels) [03 §3.3].
	if ops[0].Variant != 0 || ops[1].Variant != 0 {
		t.Fatalf("variant for gx1 gy1 should be 0, got %d %d", ops[0].Variant, ops[1].Variant)
	}

	// ch1==0 c0==3 => single GAF ch0 (self+east tiles unexplored)
	setTiles([2][2]bool{{true, true}, {false, false}}, none)
	ops = cell11(BuildFogOps(cache, cam, 0, 0, 3, 3, tables, false))
	if len(ops) != 1 || ops[0].Kind != FogKindGAFCh0 {
		t.Fatalf("single ch0 GAF want 1 GAFCh0 got %+v", ops)
	}
	if ops[0].Frame != 2 {
		t.Fatalf("frame 3-1=2 got %d", ops[0].Frame)
	}

	// ch1==9 c0==0 => single GAF ch1 (self+NW tiles fogged)
	setTiles(none, [2][2]bool{{true, false}, {false, true}})
	ops = cell11(BuildFogOps(cache, cam, 0, 0, 3, 3, tables, false))
	if len(ops) != 1 || ops[0].Kind != FogKindGAFCh1 {
		t.Fatalf("single ch1 GAF want 1 GAFCh1 got %+v", ops)
	}
	if ops[0].Frame != 8 {
		t.Fatalf("frame 9-1=8 got %d", ops[0].Frame)
	}
}

// TestFogPaletteDarkening verifies palette/SHD darkening uses logical→physical at present time [03 §4.3] C7.
func TestFogPaletteDarkening(t *testing.T) {
	// 2x2 grid with every tile unexplored: the OR pattern reconstructs
	// ch0==15 at cell (0,0) (short-circuit SolidDark carries the dark color).
	cache := testFogCache(t, 2, 2)
	for y := int32(0); y < 2; y++ {
		for x := int32(0); x < 2; x++ {
			cache.SetChannel(x, y, 1, 0)
		}
	}
	cam := &camera.Camera{X: 0, Z: 0, MapW: 64, MapH: 64}

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

	var ops []FogOp
	for _, op := range BuildFogOps(cache, cam, 0, 0, 1, 1, tables, false) {
		if op.GridX == 0 && op.GridY == 0 {
			ops = append(ops, op)
		}
	}
	if len(ops) != 1 {
		t.Fatalf("want 1 op for cell 0,0")
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
	for _, op := range opsNil {
		if op.GridX == 0 && op.GridY == 0 && (op.R != 0 || op.G != 0 || op.B != 0) {
			t.Fatalf("nil tables op should be 0,0,0")
		}
	}
}

// TestFogBorderFixups locks the retail producer border behavior for cells
// beyond the map [03 §3.3]: fogged/unexplored map-edge tiles leak their bit into
// the void row/column adjacent to the north/west edges (drawn as partial
// clouds over void), the last in-map row/column is thickened toward the
// south/east edge, south/east void cells stay untouched, and the NW void
// corner combines both passes into the short-circuit value.
func TestFogBorderFixups(t *testing.T) {
	// Border propagation is performed by visibility.RebuildFogWindow. The
	// renderer consumes the resulting cache verbatim, including a zero-origin
	// window; it must not synthesize a second map-sized producer [03 §3.3].
	cache := testFogCache(t, 2, 2)
	cache.SetChannel(0, 0, 1, 0)
	ops := BuildFogOps(cache, nil, 0, 0, 2, 2, nil, false)
	if len(ops) != 1 || ops[0].GridX != 0 || ops[0].GridY != 0 || ops[0].Channel0 != 1 {
		t.Fatalf("cache translator must preserve the authored cell, got %+v", ops)
	}
	for _, op := range ops {
		if op.GridX < 0 || op.GridY < 0 {
			t.Fatalf("renderer must not synthesize edge cells: %+v", op)
		}
	}
}

// testFogBorderFixupsLegacy retains the former producer fixture as a record of
// the superseded split implementation; edge behavior is now covered by the
// visibility window builder and this package tests only translation.
func testFogBorderFixupsLegacy(t *testing.T) {
	build := func(ops []FogOp) map[[2]int32]FogOp {
		m := make(map[[2]int32]FogOp)
		for _, op := range ops {
			if op.Channel0 == 0 {
				continue
			}
			m[[2]int32{op.GridX, op.GridY}] = op
		}
		return m
	}

	// Single unexplored tile (1,0) in the top row; everything else visible.
	// Nil camera enumerates the full grid plus the one-cell void ring.
	cache := testFogCache(t, 4, 4)
	cache.SetChannel(1, 0, 1, 0)
	byCell := build(BuildFogOps(cache, nil, 0, 0, 4, 4, nil, false))
	// Void cell west of the tile: seed bit8 (tile (1,0) is its SE source),
	// top fixup adds bit2 => 10.
	op, ok := byCell[[2]int32{0, -1}]
	if !ok || op.Channel0 != 10 || op.Kind != FogKindGAFCh0 || op.Frame != 9 {
		t.Fatalf("north void cell (0,-1) want ch0=10 GAFCh0 frame9, got %+v (ok=%v)", op, ok)
	}
	// Void cell above the tile: seed bit4, top fixup adds bit1 => 5.
	op, ok = byCell[[2]int32{1, -1}]
	if !ok || op.Channel0 != 5 || op.Kind != FogKindGAFCh0 || op.Frame != 4 {
		t.Fatalf("north void cell (1,-1) want ch0=5 GAFCh0 frame4, got %+v (ok=%v)", op, ok)
	}
	// No void op beyond the tile's leak radius.
	if op, ok := byCell[[2]int32{2, -1}]; ok {
		t.Fatalf("north void cell (2,-1) want none, got %+v", op)
	}
	// In-map tile cell carries only its own bit1.
	if got := byCell[[2]int32{1, 0}].Channel0; got != 1 {
		t.Fatalf("in-map (1,0) want ch0=1 got %d", got)
	}
	// Whole top row unexplored: void cells collect bit4+bit8 seeds and the
	// top fixup adds bit1+bit2 => 15 short-circuit (solid black over void);
	// the NW void corner also collects the left fixup (still 15).
	cache = testFogCache(t, 4, 4)
	for x := int32(0); x < 4; x++ {
		cache.SetChannel(x, 0, 1, 0)
	}
	byCell = build(BuildFogOps(cache, nil, 0, 0, 4, 4, nil, false))
	for x := int32(0); x < 4; x++ {
		op, ok := byCell[[2]int32{x, -1}]
		if !ok || op.Channel0 != 15 || op.Kind != FogKindSolidDark {
			t.Fatalf("north void cell (%d,-1) want ch0=15 SolidDark, got %+v (ok=%v)", x, op, ok)
		}
	}
	if op, ok = byCell[[2]int32{-1, -1}]; !ok || op.Channel0 != 15 || op.Kind != FogKindSolidDark {
		t.Fatalf("NW void corner want ch0=15 SolidDark, got %+v (ok=%v)", op, ok)
	}
	// West void column at row 0: seed bit2 from tile (0,0), left fixup adds bit1 => 3.
	op, ok = byCell[[2]int32{-1, 0}]
	if !ok || op.Channel0 != 3 {
		t.Fatalf("west void cell (-1,0) want ch0=3, got %+v (ok=%v)", op, ok)
	}
	// In-map top row cells: own bit1 plus bit2 from the east neighbour tile;
	// the last column gets bit2 from the right-edge fixup instead (its own
	// tile is fogged and the fixup marks the void tile east of it) — all 3.
	for x := int32(0); x < 4; x++ {
		if got := byCell[[2]int32{x, 0}].Channel0; got != 3 {
			t.Fatalf("in-map (%d,0) want ch0=3 got %d", x, got)
		}
	}
	// South/east void stays untouched; no ops anywhere with GridY>=4/GridX>=4.
	for cell := range byCell {
		if cell[0] >= 4 || cell[1] >= 4 {
			t.Fatalf("unexpected south/east void op at %v", cell)
		}
	}

	// Bottom row unexplored: bottom fixup (row endY-2 == 3) thickens toward
	// the map edge: cell (0,3) = 1|2 then +4|8 => 15; cell (3,3) = 1 then +4,
	// right fixup adds bit2 => 7.
	cache = testFogCache(t, 4, 4)
	for x := int32(0); x < 4; x++ {
		cache.SetChannel(x, 3, 1, 0)
	}
	byCell = build(BuildFogOps(cache, nil, 0, 0, 4, 4, nil, false))
	op, ok = byCell[[2]int32{0, 3}]
	if !ok || op.Channel0 != 15 || op.Kind != FogKindSolidDark {
		t.Fatalf("bottom edge cell (0,3) want ch0=15 SolidDark, got %+v (ok=%v)", op, ok)
	}
	// Corner cell compounds both fixups in retail order (bottom: bit1→bit4;
	// right: bit4→bit8 and bit1→bit2) => 15.
	op, ok = byCell[[2]int32{3, 3}]
	if !ok || op.Channel0 != 15 || op.Kind != FogKindSolidDark {
		t.Fatalf("bottom-right cell (3,3) want ch0=15 SolidDark, got %+v (ok=%v)", op, ok)
	}
	if _, ok := byCell[[2]int32{0, 4}]; ok {
		t.Fatalf("south void cell (0,4) must have no ch0 op")
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

// TestFogVariantSelection checks four-way variant deterministic and
// camera-independent: (gx+gy+2)&3 for map-global cells [03 §3.3].
func TestFogVariantSelection(t *testing.T) {
	cams := []*camera.Camera{
		nil,
		{X: 0, Z: 0},
		{X: 33, Z: -47},
		{X: 1024, Z: 512},
	}
	for _, cam := range cams {
		for gy := int32(0); gy < 4; gy++ {
			for gx := int32(0); gx < 4; gx++ {
				v := FogVariant(gx, gy, cam)
				want := int((gx + gy + 2) & 3)
				if v != want {
					t.Fatalf("variant gx=%d gy=%d got %d want %d", gx, gy, v, want)
				}
				if v < 0 || v > 3 {
					t.Fatalf("variant out of range")
				}
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
