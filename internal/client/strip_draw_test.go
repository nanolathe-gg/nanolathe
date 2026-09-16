package client

import (
	"bytes"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// stripTestBankName is the bank every strip family's identity pair names
// [06 R-WFX-01 §1].
const stripTestBankName = "fx"

// stripTestClient builds a windowless client whose framebuffer, camera and
// palette are all known, plus one authored two-entry bank standing in for
// `anims/fx.gaf`. The bank is authored here, never copied from retail.
//
// The ALP table is the identity on its source axis (`ALP[src×256 + dst] =
// src`), so a tinted blit writes the source index and a test can see exactly
// which pixels it touched.
func stripTestClient(t *testing.T) *Client {
	t.Helper()
	art := formats.GAFWriteFrame{Width: 2, Height: 2, Duration: 1, Pixels: []byte{0x41, 0x42, 0x43, 0x44}}
	second := art
	second.Pixels = []byte{0x51, 0x52, 0x53, 0x54}
	data, err := formats.EncodeGAF([]formats.GAFWriteEntry{
		{Name: "smoke 1", Frames: []formats.GAFWriteFrame{art, second}},
		{Name: "flamestream", Frames: []formats.GAFWriteFrame{art}},
	})
	if err != nil {
		t.Fatal(err)
	}
	bank, err := formats.LoadGAF(data)
	if err != nil {
		t.Fatal(err)
	}
	pal := &palette.Tables{}
	for src := 0; src < 256; src++ {
		for dst := 0; dst < 256; dst++ {
			pal.Alpha[src*256+dst] = uint8(src)
		}
	}
	return &Client{
		width: 64, height: 64,
		indexed: make([]uint8, 64*64),
		cam:     &camera.Camera{},
		pal:     pal,
		// New installs the full Enhanced effect selection (§30); a directly
		// constructed fixture has to match it or every effect reads as off.
		effects:     drawlist.AllEffects(),
		effectBanks: map[string]*formats.GAF{stripTestBankName: bank},
	}
}

// stripTestFrame carries one all-visible coverage grid so the gating families
// are admitted; visible=false makes every tile dark instead.
func stripTestFrame(visible bool, views ...frame.StripView) *frame.Frame {
	grid := make([]uint8, 16)
	if visible {
		for i := range grid {
			grid[i] = 1
		}
	}
	return &frame.Frame{
		Strips: views,
		Visibility: frame.VisibilityView{
			Valid: true, W: 4, H: 4, CoverageBytes: true, Visible: grid,
		},
	}
}

func stripPainted(c *Client) int {
	painted := 0
	for _, p := range c.indexed {
		if p != 0 {
			painted++
		}
	}
	return painted
}

// TestStripBarrierDrawsBothFormsAtTheirOwnSlot locks the two per-sub-record
// draw forms of [03 R-STRIP-01 §2] and the slot each is drawn at [03 §1]: a
// blitting family stamps its entry's current frame at the projected point, a
// filling family paints a two-by-two rectangle in its own raw palette byte,
// and neither is drawn at another barrier.
func TestStripBarrierDrawsBothFormsAtTheirOwnSlot(t *testing.T) {
	c := stripTestClient(t)
	puff := frame.StripView{
		Strip: 9, Family: frame.StripFamilySmokePuff,
		Bank: stripTestBankName, Entry: "smoke 1", Frame: 0,
		X: numeric.FixedFromInt(20), Z: numeric.FixedFromInt(20),
	}
	sprinkle := frame.StripView{
		Strip: 2, Family: frame.StripFamilySprinkle, Fill: 0x67,
		X: numeric.FixedFromInt(40), Z: numeric.FixedFromInt(40),
	}
	cur := stripTestFrame(true, sprinkle, puff)

	// The sprinkle's own barrier, which must not draw the strip-9 puff.
	if stats := c.drawStripBarrier(cur, 2); stats.Filled != 1 || stats.Blitted != 0 || stats.Unresolved != 0 {
		t.Fatalf("strip 2 drew %+v, want one filled record", stats)
	}
	c.replayForTest()
	filled := 0
	for _, p := range c.indexed {
		if p == 0x67 {
			filled++
		}
	}
	if filled != stripParticleSize*stripParticleSize {
		t.Fatalf("the sprinkle painted %d pixels, want the two-by-two rectangle [03 R-FX-01 §3]", filled)
	}
	if got := c.indexed[40*c.width+40]; got != 0x67 {
		t.Fatalf("the sprinkle painted %#x at its own projected point, want its raw palette byte", got)
	}
	if stripPainted(c) != filled {
		t.Fatal("strip 2 drew a record belonging to another barrier")
	}

	// The puff's barrier: the tinted blit writes the frame's own indices. Reset
	// the list first so the replay executes only this barrier's record over the
	// surface the strip-2 replay already composed.
	c.resetListForTest()
	if stats := c.drawStripBarrier(cur, 9); stats.Blitted != 1 || stats.Filled != 0 || stats.Unresolved != 0 {
		t.Fatalf("strip 9 drew %+v, want one blitted record", stats)
	}
	c.replayForTest()
	if got := c.indexed[20*c.width+20]; got != 0x41 {
		t.Fatalf("the puff's first frame put %#x at its projected point, want the frame's own pixel", got)
	}
	// The cursor selects the frame: the same record one frame on draws the
	// second frame's pixels [03 R-FX-02 §3].
	advanced := puff
	advanced.Frame = 1
	c2 := stripTestClient(t)
	c2.drawStripBarrier(stripTestFrame(true, advanced), 9)
	c2.replayForTest()
	if got := c2.indexed[20*c2.width+20]; got != 0x51 {
		t.Fatalf("cursor 1 drew %#x, want the entry's second frame", got)
	}

	// An empty committed channel draws nothing at all.
	c3 := stripTestClient(t)
	if stats := c3.drawStripBarrier(stripTestFrame(true), 9); stats != (StripDrawStats{}) {
		t.Fatalf("an empty strip channel drew %+v", stats)
	}
	c3.replayForTest()
	if stripPainted(c3) != 0 {
		t.Fatal("an empty strip channel painted pixels")
	}
}

// Smoke visibility belongs to each puff, while geothermal steam has no coverage
// gate [03 R-FX-01 §3][03 R-FX-02 §3]. Rejecting the sprite before recording
// protects both executors; classic replay also verifies the resulting pixels.
func TestSmokeCoverageAndVentException(t *testing.T) {
	for _, tc := range []struct {
		name     string
		family   frame.StripFamily
		visible  bool
		wantDraw bool
	}{
		{"hidden weapon smoke", frame.StripFamilySmokePuff, false, false},
		{"visible weapon smoke", frame.StripFamilySmokePuff, true, true},
		{"hidden geothermal steam", frame.StripFamilyVentSteam, false, true},
		{"visible geothermal steam", frame.StripFamilyVentSteam, true, true},
		{"hidden flame trail", frame.StripFamilyFlameTrail, false, false},
		{"visible flame trail", frame.StripFamilyFlameTrail, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := frame.StripView{Strip: 9, Family: tc.family, Bank: stripTestBankName,
				Entry: "smoke 1", X: numeric.FixedFromInt(20), Z: numeric.FixedFromInt(20)}
			if tc.family == frame.StripFamilyVentSteam {
				v.Strip = 4
			}
			if tc.family == frame.StripFamilyFlameTrail {
				v.Entry = "flamestream"
			}
			c := stripTestClient(t)
			stats := c.drawStripBarrier(stripTestFrame(tc.visible, v), v.Strip)
			sprites := 0
			c.list.VisitSprites(func(drawlist.Sprite) { sprites++ })
			if (sprites != 0) != tc.wantDraw || (stats.Blitted != 0) != tc.wantDraw || (stats.Gated == 0) != tc.wantDraw {
				t.Fatalf("draw = %+v, sprites = %d, want drawn = %t", stats, sprites, tc.wantDraw)
			}
			c.replayForTest()
			if (stripPainted(c) != 0) != tc.wantDraw {
				t.Fatalf("classic painted = %d, want drawn = %t", stripPainted(c), tc.wantDraw)
			}
		})
	}
}

// The sheared puff position selects the tile, and current sight takes precedence
// over exploration. The word-grid fallback tests the viewing player's own bit
// [03 R-FX-01 §3]. Two adjacent puffs must be admitted independently.
func TestSmokeCoverageUsesEachShearedPointAndViewer(t *testing.T) {
	puff := frame.StripView{Strip: 9, Family: frame.StripFamilySmokePuff,
		Bank: stripTestBankName, Entry: "smoke 1", X: numeric.FixedFromInt(20),
		Y: numeric.FixedFromInt(64), Z: numeric.FixedFromInt(52)}
	hidden := puff
	hidden.X = numeric.FixedFromInt(52)
	for _, bytes := range []bool{true, false} {
		cur := stripTestFrame(false, puff, hidden)
		cur.ViewingPlayer = 2
		cur.Visibility.CoverageBytes = bytes
		cur.Visibility.WordVisible = make([]uint16, 16)
		cur.Visibility.WordVisible[0] = 1 << 2
		cur.Visibility.WordVisible[1] = 1 << 1
		cur.Visibility.Visible[0] = 1
		if bytes {
			cur.Visibility.WordVisible[1] |= 1 << 2
		}
		c := stripTestClient(t)
		stats := c.drawStripBarrier(cur, 9)
		if stats.Blitted != 1 || stats.Gated != 1 {
			t.Fatalf("byte coverage = %t: %+v, want only the first puff", bytes, stats)
		}
		c.replayForTest()
		if c.indexed[20*c.width+20] != 0x41 || c.indexed[20*c.width+52] != 0 {
			t.Fatalf("byte coverage = %t: wrong puff pixels", bytes)
		}
	}
}

// TestUnresolvableStripIdentityIsCountedNotGuessed locks the identity contract:
// the pair is (bank, entry) and a pair that names no art draws nothing and is
// counted, rather than falling back to some other entry [06 R-WFX-01 §1][I9].
func TestUnresolvableStripIdentityIsCountedNotGuessed(t *testing.T) {
	base := frame.StripView{
		Strip: 9, Family: frame.StripFamilySmokePuff,
		Bank: stripTestBankName, Entry: "smoke 1",
		X: numeric.FixedFromInt(20), Z: numeric.FixedFromInt(20),
	}
	for _, tc := range []struct {
		name string
		view frame.StripView
	}{
		{"entry the bank does not hold", func() frame.StripView { v := base; v.Entry = "smoke 3"; return v }()},
		{"bank that does not load", func() frame.StripView { v := base; v.Bank = "nosuchbank"; return v }()},
		{"half a pair", func() frame.StripView { v := base; v.Entry = ""; return v }()},
	} {
		c := stripTestClient(t)
		stats := c.drawStripBarrier(stripTestFrame(true, tc.view), 9)
		if stats.Unresolved != 1 || stats.Blitted != 0 {
			t.Fatalf("%s drew %+v, want one unresolved record", tc.name, stats)
		}
		if stripPainted(c) != 0 {
			t.Fatalf("%s painted pixels; an unresolved identity draws nothing", tc.name)
		}
	}
}

// TestStripBarrierRunSelectsOneBarrier locks the committed order the draw
// depends on: strips ascending, so one barrier's records are a contiguous run
// [03 §1][I1].
func TestStripBarrierRunSelectsOneBarrier(t *testing.T) {
	views := []frame.StripView{
		{Strip: 2, Fill: 1}, {Strip: 2, Fill: 2},
		{Strip: 4, Fill: 3},
		{Strip: 9, Fill: 4}, {Strip: 9, Fill: 5}, {Strip: 9, Fill: 6},
	}
	for _, tc := range []struct {
		strip int8
		want  int
	}{{0, 0}, {2, 2}, {3, 0}, {4, 1}, {9, 3}} {
		if got := len(stripBarrierRun(views, tc.strip)); got != tc.want {
			t.Fatalf("barrier %d selected %d records, want %d", tc.strip, got, tc.want)
		}
	}
	if got := stripBarrierRun(nil, 4); got != nil {
		t.Fatalf("an empty channel selected %v", got)
	}
}

// TestCommittedFrameDrawsStripObjectsAtTheirBarrier is the composition proof:
// a strip record on a committed frame reaches the framebuffer through the
// production composer, at the barrier [03 §1] gives it, and an empty channel
// leaves the composed frame untouched.
//
// It goes through drawCommittedFrame rather than the barrier helper so the
// wiring itself is covered: before this unit the committed frame had no strip
// channel and no strip object was ever drawn.
func TestCommittedFrameDrawsStripObjectsAtTheirBarrier(t *testing.T) {
	compose := func(views ...frame.StripView) *Client {
		t.Helper()
		buf := &frame.Buffer{}
		write := buf.BeginWrite()
		*write = *stripTestFrame(true, views...)
		write.Tick = 1
		if err := buf.Publish(1); err != nil {
			t.Fatal(err)
		}
		c := stripTestClient(t)
		c.buffer = buf
		c.drawCommittedFrame(write, true)
		c.replayForTest() // drawCommittedFrame records; the replay is the single execution (WU-1.8)
		return c
	}

	// A puff at world (20, 0, 20) projects to the framebuffer point of the
	// same name once the retail origins are removed [03 §2.5][03 R-FX-01 §3].
	drawn := compose(frame.StripView{
		Strip: 9, Family: frame.StripFamilySmokePuff,
		Bank: stripTestBankName, Entry: "smoke 1",
		X: numeric.FixedFromInt(20), Z: numeric.FixedFromInt(20),
	})
	if got := drawn.indexed[20*drawn.width+20]; got == 0 {
		t.Fatal("the composed frame has background where the strip object projects; no strip object reached the screen")
	}
	if stripPainted(drawn) == 0 {
		t.Fatal("the composed frame painted nothing at all")
	}

	// The same composition with an empty strip channel paints nothing.
	empty := compose()
	if painted := stripPainted(empty); painted != 0 {
		t.Fatalf("an empty strip channel composed %d non-background pixels", painted)
	}
}

// TestNanolatheCommittedSnapshotDoesNotDependOnPresentationCadence verifies
// that a strip-6 particle is a committed value, not client state. A fresh
// client and a client that presents the same tick five times must paint the
// same bytes; drawing must not advance, reseed, create or expire particles
// [03 R-STRIP-01 §2][03 §5.5][I6].
func TestNanolatheCommittedSnapshotDoesNotDependOnPresentationCadence(t *testing.T) {
	render := func(c *Client, cur *frame.Frame) []byte {
		clearIndexed(c)
		c.resetListForTest()
		c.drawEffects(cur)
		c.replayForTest()
		return append([]byte(nil), c.indexed...)
	}

	// The first particle is visible only in earlier committed frames. Its
	// absence from final catches stale client-owned particle retention; the
	// surviving particle moves and changes colour each frame.
	snapshots := []*frame.Frame{
		stripTestFrame(true,
			frame.StripView{Strip: 6, Family: frame.StripFamilyNano, Fill: 0xa3, X: numeric.FixedFromInt(20), Z: numeric.FixedFromInt(20)},
			frame.StripView{Strip: 6, Family: frame.StripFamilyNano, Fill: 0xa6, X: numeric.FixedFromInt(28), Z: numeric.FixedFromInt(20)}),
		stripTestFrame(true,
			frame.StripView{Strip: 6, Family: frame.StripFamilyNano, Fill: 0xa4, X: numeric.FixedFromInt(24), Z: numeric.FixedFromInt(20)},
			frame.StripView{Strip: 6, Family: frame.StripFamilyNano, Fill: 0xa7, X: numeric.FixedFromInt(32), Z: numeric.FixedFromInt(20)}),
		stripTestFrame(true,
			frame.StripView{Strip: 6, Family: frame.StripFamilyNano, Fill: 0xa1, X: numeric.FixedFromInt(36), Z: numeric.FixedFromInt(20)}),
		stripTestFrame(true,
			frame.StripView{Strip: 6, Family: frame.StripFamilyNano, Fill: 0xa2, X: numeric.FixedFromInt(40), Z: numeric.FixedFromInt(20)}),
		stripTestFrame(true,
			frame.StripView{Strip: 6, Family: frame.StripFamilyNano, Fill: 0xa3, X: numeric.FixedFromInt(44), Z: numeric.FixedFromInt(20)}),
	}

	// A client that presents all intervening commits ends at the same pixels as
	// a fresh client that sees only the final commit.
	presented := stripTestClient(t)
	var afterSequence []byte
	for i, cur := range snapshots {
		cur.Tick = uint32(i + 1)
		afterSequence = render(presented, cur)
	}
	fresh := render(stripTestClient(t), snapshots[len(snapshots)-1])
	if !bytes.Equal(fresh, afterSequence) {
		t.Fatal("a fresh client and a client that saw earlier nano snapshots painted the final commit differently")
	}
	if got := afterSequence[20*presented.width+20]; got != 0 {
		t.Fatalf("a particle absent from the final snapshot remained at its old point as %#x", got)
	}

	// Re-presenting one committed tick also remains inert: presentation cadence
	// cannot change the final snapshot.
	repeated := stripTestClient(t)
	var afterFive []byte
	for i := 0; i < 5; i++ {
		afterFive = render(repeated, snapshots[len(snapshots)-1])
	}
	if !bytes.Equal(fresh, afterFive) {
		t.Fatal("the same committed nano snapshot painted differently after repeated presentation")
	}
	if got := stripPainted(repeated); got != stripParticleSize*stripParticleSize {
		t.Fatalf("repeated presentation painted %d pixels, want the final committed particle only", got)
	}
}

// TestNanolatheCommittedParticleUsesTheCoverageGate keeps the raw two-by-two
// mark behind the particle's individual visibility test. The strip itself is
// present in a committed frame, but an unseen particle neither writes its raw
// palette colour nor leaks an offscreen construction effect [03 §5.5][03
// R-STRIP-01 §2].
func TestNanolatheCommittedParticleUsesTheCoverageGate(t *testing.T) {
	particle := frame.StripView{
		Strip: 6, Family: frame.StripFamilyNano, Fill: 0xa3,
		X: numeric.FixedFromInt(20), Z: numeric.FixedFromInt(20),
	}
	c := stripTestClient(t)
	if stats := c.drawStripBarrier(stripTestFrame(false, particle), 6); stats.Gated != 1 || stats.Filled != 0 {
		t.Fatalf("unseen nano particle drew %+v, want one gated record", stats)
	}
	c.replayForTest()
	if painted := stripPainted(c); painted != 0 {
		t.Fatalf("unseen nano particle painted %d pixels", painted)
	}
}

// A flame-stream trail is a spark in flight and a standing flame is a burning
// place: the producer family alone separates them, and each carries the
// remaining life the terrain receiver takes its pool out on (§31.7).
func TestFlameFamiliesCarryTheirOwnLightingIdentityAndFade(t *testing.T) {
	c := stripTestClient(t)
	for _, v := range []frame.StripView{
		{Family: frame.StripFamilyFlame, Bank: "fx", Entry: "flamestream", Remaining: 30, HasRemaining: true},
		{Family: frame.StripFamilyFlameTrail, Bank: "fx", Entry: "flamestream", Remaining: 30, HasRemaining: true},
		{Family: frame.StripFamilyFlameTrail, Bank: "fx", Entry: "flamestream", Remaining: 1, HasRemaining: true},
		{Family: frame.StripFamilyFlameTrail, Bank: "fx", Entry: "flamestream", Remaining: 0, HasRemaining: true},
		// No deadline at all: the sweep treats this particle as immortal, so it
		// must not be read as one expiring this tick.
		{Family: frame.StripFamilyFlameTrail, Bank: "fx", Entry: "flamestream"},
	} {
		c.blitStripFrame(v)
	}
	var got []drawlist.Sprite
	c.list.VisitSprites(func(s drawlist.Sprite) { got = append(got, s) })
	if len(got) != 5 {
		t.Fatalf("emitted %d sprites, want 5", len(got))
	}
	if got[0].LightingKind != drawlist.SpriteLightingFire {
		t.Fatalf("standing flame kind = %v", got[0].LightingKind)
	}
	for _, s := range got[1:] {
		if s.LightingKind != drawlist.SpriteLightingSpark {
			t.Fatalf("trail kind = %v, want a spark", s.LightingKind)
		}
	}
	// A source with life to spare emits fully; one tick from expiry it is two
	// thirds; on its last drawn tick it is still a third, because it is still
	// drawn. The presence flag separates all of those from a record that
	// carries no deadline, which must claim no fade at all.
	for i, want := range []float32{1, 1, 2.0 / 3, 1.0 / 3} {
		if !got[i].HasLightingFade || got[i].LightingFade != want {
			t.Fatalf("sprite %d fade = %v (present %v), want %v", i, got[i].LightingFade, got[i].HasLightingFade, want)
		}
	}
	if got[4].HasLightingFade {
		t.Fatalf("a record with no deadline claimed a fade of %v", got[4].LightingFade)
	}
	// The flicker clock belongs to the standing flame: a trail moves, and the
	// flicker phase is a position hash that re-rolls under anything that does.
	if got[1].LightingTime != 0 {
		t.Fatal("a spark took the standing flame's flicker clock")
	}
}

// The receiver height the Enhanced ground pass subtracts: the terrain under the
// source, in the recording units WorldHeight uses. Off the map the terrain
// returns its raw −1 sentinel, which is a marker and not a height, and with no
// terrain bound there is nothing to report; both leave the pass on the sea datum
// it used before (DESIGN_GPU_RENDERER §31.7).
func TestLightingGroundReportsTerrainHeightAndRefusesTheSentinel(t *testing.T) {
	c := &Client{}
	if got := c.lightingGround(numeric.FixedFromInt(32), numeric.FixedFromInt(32), 1); got != 0 {
		t.Fatalf("no terrain bound reported %v, want 0", got)
	}
	// A four-by-four plot at a uniform height byte. HeightAt needs cx+1 and
	// cz+1 in range, so the last row and column are the sentinel's territory.
	const cells = 4
	terrain := &world.Terrain{CellW: cells, CellH: cells, Plot: make([]world.PlotCell, cells*cells)}
	for i := range terrain.Plot {
		terrain.Plot[i][4] = 20
	}
	c.SetTerrain(terrain)
	inside := c.lightingGround(numeric.FixedFromInt(32), numeric.FixedFromInt(32), 1)
	if inside <= 0 {
		t.Fatalf("a source over flat terrain reported %v, want its height", inside)
	}
	// The view scale multiplies it, exactly as it multiplies WorldHeight.
	if doubled := c.lightingGround(numeric.FixedFromInt(32), numeric.FixedFromInt(32), 2); doubled != inside*2 {
		t.Fatalf("scaled height %v, want %v", doubled, inside*2)
	}
	// Off the map: the sentinel must never reach the pass as a height.
	for _, p := range [][2]int64{{-64, -64}, {4096, 32}, {32, 4096}} {
		if got := c.lightingGround(numeric.FixedFromInt(p[0]), numeric.FixedFromInt(p[1]), 1); got != 0 {
			t.Fatalf("off-map (%d,%d) reported %v, want 0", p[0], p[1], got)
		}
	}
}
