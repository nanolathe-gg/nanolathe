package client

import (
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/palette"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
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
		indexed:     make([]uint8, 64*64),
		cam:         &camera.Camera{},
		pal:         pal,
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

// TestPuffDrawHasNoCoverageGateAndFlameDoesLocks the difference [03 R-FX-02 §3]
// records between the families: "this family's draw walk tests nothing before
// blitting, unlike the flame and sprinkle families". Doc 06's per-puff sentence
// says the opposite [06 R-WFX-01 §5]; §3 is the instruction-level read of the
// class's own draw and names the disagreement.
func TestPuffDrawHasNoCoverageGateAndFlameDoes(t *testing.T) {
	dark := func(v frame.StripView) *frame.Frame { return stripTestFrame(false, v) }

	puff := frame.StripView{
		Strip: 9, Family: frame.StripFamilySmokePuff,
		Bank: stripTestBankName, Entry: "smoke 1",
		X: numeric.FixedFromInt(20), Z: numeric.FixedFromInt(20),
	}
	c := stripTestClient(t)
	if stats := c.drawStripBarrier(dark(puff), 9); stats.Blitted != 1 || stats.Gated != 0 {
		t.Fatalf("a puff over unseen ground drew %+v; the puff class gates on nothing [03 R-FX-02 §3]", stats)
	}

	trail := puff
	trail.Family = frame.StripFamilyFlameTrail
	trail.Entry = "flamestream"
	c2 := stripTestClient(t)
	if stats := c2.drawStripBarrier(dark(trail), 9); stats.Gated != 1 || stats.Blitted != 0 {
		t.Fatalf("a flame trail over unseen ground drew %+v; the flame families gate [03 R-FX-02 §2]", stats)
	}
	if stripPainted(c2) != 0 {
		t.Fatal("a gated flame segment still painted pixels")
	}
	// The same segment over seen ground draws.
	c3 := stripTestClient(t)
	if stats := c3.drawStripBarrier(stripTestFrame(true, trail), 9); stats.Blitted != 1 {
		t.Fatalf("a flame trail over seen ground drew %+v", stats)
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
