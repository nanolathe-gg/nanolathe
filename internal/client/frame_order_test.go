package client

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/audio"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/palette"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

type frameOrderUIStage struct {
	value byte
}

type frameAudioOutputSpy struct {
	volumes []float64
}

func (s *frameAudioOutputSpy) PlaySample(_ *audio.Sample, volume, _ float64) error {
	s.volumes = append(s.volumes, volume)
	return nil
}

func (s frameOrderUIStage) DrawUI(c *Client, _ UIFrame) {
	// Record a one-pixel fill the way a production UI stage records its draws; the
	// single deferred replay lands it after the fog composite, which is the [03 §1]
	// ordering this test locks (WU-1.8). A UI stage no longer reads composed pixels
	// during the recording pass — destination reads happen in the sink at replay.
	c.UIFillRect(0, 0, 1, 1, s.value)
}

func TestFrameRefreshesAudioViewportBeforeDrain(t *testing.T) {
	buf := frame.NewBuffer()
	w := buf.BeginWrite()
	w.Events = []frame.EventView{{Kind: frame.EventKindAudio, AudioPositional: true, AudioAudible: true, Sound: "shot", X: numeric.FixedFromInt(40), Z: numeric.FixedFromInt(50)}}
	if err := buf.Publish(1); err != nil {
		t.Fatal(err)
	}
	c, err := New(Options{Buffer: buf, Width: 64, Height: 64})
	if err != nil {
		t.Fatal(err)
	}
	service := audio.NewService(nil)
	if _, err := service.Cache.Put("shot", []byte{128, 129}); err != nil {
		t.Fatal(err)
	}
	service.Registry.SetCache(service.Cache)
	c.SetAudioService(service)
	c.SetCamera(&camera.Camera{X: 32, Z: 48, ViewW: 64, ViewH: 64, MapW: 256, MapH: 256})
	old := audio.GlobalOutput()
	spy := &frameAudioOutputSpy{}
	audio.SetGlobalOutput(spy)
	t.Cleanup(func() { audio.SetGlobalOutput(old) })
	c.Frame()
	volumes := spy.volumes
	if len(volumes) != 1 {
		t.Fatalf("played volumes=%v, want one positional playback", volumes)
	}
	if volumes[0] != audio.VolumeFromAttenuation(audio.VolInView) {
		t.Fatalf("audio drained before camera viewport refresh: volume=%v want in-view %v", volumes[0], audio.VolumeFromAttenuation(audio.VolInView))
	}
}

func TestCommittedFrameShakeDoesNotMutateClientCamera(t *testing.T) {
	buf := frame.NewBuffer()
	w := buf.BeginWrite()
	w.ShakeOffsetX = 9
	w.ShakeOffsetY = -4
	if err := buf.Publish(1); err != nil {
		t.Fatal(err)
	}
	c, err := New(Options{Buffer: buf, Width: 8, Height: 8})
	if err != nil {
		t.Fatal(err)
	}
	cam := &camera.Camera{X: 12, Z: 20, ViewW: 8, ViewH: 8, MapW: 64, MapH: 64}
	c.SetCamera(cam)
	c.ComposeFrame()
	if cam.X != 12 || cam.Z != 20 {
		t.Fatalf("frame repaint mutated camera to (%d,%d); shake belongs to battle camera owner", cam.X, cam.Z)
	}
}

func TestDrawWorldPassProcessesAllDrawablesInPainterOrder(t *testing.T) {
	c := newTestClient(t)
	c.models["world_order"] = syntheticModel(
		[]pieceInfo{{name: "root", parent: -1}},
		[]syntheticTri{makeTriangle(0, "root", [3][3]float64{{0, 0, 0}, {4, 0, 0}, {0, 0, 4}}, 17, 0)},
		0,
	)
	cur := &frame.Frame{
		Selection: frame.SelectionView{LocalPlayer: 0},
		Units: []frame.UnitView{
			{Slot: 1, Owner: 0, Z: numeric.Fixed(20 << 16), Model: "world_order"},
			{Slot: 2, Owner: 0, Z: numeric.Fixed(10 << 16), Model: "world_order"},
			{Slot: 3, Owner: 0, Z: numeric.Fixed(10 << 16), Model: "world_order"},
		},
	}

	// These units carry no mover, so all three belong to pass B; the bucket
	// build and pass A run first because pass B walks that build
	// [03 R-RAST-01 §7].
	c.drawWorldPass(cur, true)
	c.drawWorldPassB(cur, true)
	want := []pool.Handle{2, 3, 1}
	if len(c.selectionChrome) != len(want) {
		t.Fatalf("drawn units = %d, want %d", len(c.selectionChrome), len(want))
	}
	for i, w := range want {
		if got := c.selectionChrome[i].view.Slot; got != w {
			t.Fatalf("draw order[%d] = %d, want %d", i, got, w)
		}
	}
}

func TestWorldBucketsStableEqualRows(t *testing.T) {
	var b worldBuckets
	b.add(worldDrawable{row: 10, screenX: 1})
	b.add(worldDrawable{row: 5, screenX: 2})
	b.add(worldDrawable{row: 10, screenX: 3})
	var got []int32
	for _, v := range b.ordered() {
		got = append(got, v.screenX)
	}
	want := []int32{2, 1, 3}
	if len(got) != len(want) {
		t.Fatalf("ordered length %d want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ordered[%d] = %d want %d", i, got[i], want[i])
		}
	}
	// Reusing the bucket does not change the source-order contract.
	b.reset()
	b.add(worldDrawable{row: 10, screenX: 4})
	b.add(worldDrawable{row: 10, screenX: 5})
	got = got[:0]
	for _, v := range b.ordered() {
		got = append(got, v.screenX)
	}
	if len(got) != 2 || got[0] != 4 || got[1] != 5 {
		t.Fatalf("reused equal-row bucket = %v, want [4 5]", got)
	}
}

func TestWorldBucketsReuseWithoutPerFrameAllocation(t *testing.T) {
	var b worldBuckets
	for i := 0; i < 8; i++ {
		b.add(worldDrawable{row: int32(i % 3), screenX: int32(i)})
	}
	_ = b.ordered()
	allocs := testing.AllocsPerRun(100, func() {
		b.reset()
		for i := 0; i < 8; i++ {
			b.add(worldDrawable{row: int32(i % 3), screenX: int32(i)})
		}
		_ = b.ordered()
	})
	if allocs != 0 {
		t.Fatalf("warm world bucket pass allocations = %f, want 0", allocs)
	}
}

func TestCommittedFrameFogGateAndInterfacePrecedence(t *testing.T) {
	buf := &frame.Buffer{}
	write := buf.BeginWrite()
	*write = frame.Frame{
		Tick: 1,
		Fog:  frame.FogView{Valid: true, W: 1, H: 1, Ch0: []byte{0}, Ch1: []byte{15}},
	}
	if err := buf.Publish(1); err != nil {
		t.Fatal(err)
	}
	// Center the one fog cell at the shell origin: FogScreenRect places its
	// rebased rectangle at (16-cam.X, 16-cam.Z) [03 §3.3].
	newClient := func() *Client {
		c, err := New(Options{Buffer: buf, Width: 4, Height: 4})
		if err != nil {
			t.Fatal(err)
		}
		c.SetCamera(&camera.Camera{X: 16, Z: 16, ViewW: 4, ViewH: 4, MapW: 16, MapH: 16})
		c.pal = &palette.Tables{}
		c.pal.Gray[0] = 123
		return c
	}

	// [03 §1] item 10: fog is composited after the ten strips and before the
	// interface. With no interface stage the never-explored viewport pixel keeps
	// the fog gray remap of 0 -> Gray[0] = 123. Recording writes nothing; the
	// single replay executes the whole frame (WU-1.8).
	c := newClient()
	c.drawCommittedFrame(write, true)
	c.replayForTest()
	if got := c.indexed[0]; got != 123 {
		t.Fatalf("fog gray remap = %d, want 123 composited after the strips [03 §1]", got)
	}

	// The interface stage records its draws after the fog composite, so where it
	// overlaps the fog its pixel wins. Had the interface drawn before the fog, the
	// gray remap would have folded 77 through Gray[77] (0 here), not left it 77 —
	// so 77 proves the fog-then-interface order of [03 §1].
	c2 := newClient()
	c2.SetUIStage(frameOrderUIStage{value: 77})
	c2.drawCommittedFrame(write, true)
	c2.replayForTest()
	if got := c2.indexed[0]; got != 77 {
		t.Fatalf("interface pixel = %d, want 77 drawn after the fog composite [03 §1]", got)
	}

	// Every committed frame reruns the composite: a repeat re-lands the fog and
	// then the interface, not a stale surface.
	c2.resetListForTest()
	c2.drawCommittedFrame(write, true)
	c2.replayForTest()
	if got := c2.indexed[0]; got != 77 {
		t.Fatalf("second committed frame interface pixel = %d, want 77 [03 §1]", got)
	}
}

func TestSelectionChromeKeepsFogWhenNoUnitBracketIsEmitted(t *testing.T) {
	// Match the committed-frame fixture's cell-center camera so the fog cell
	// covers pixel (0,0) after the retail-origin rebase [03 §3.3].
	c := &Client{width: 4, height: 4, indexed: make([]uint8, 16), pal: &palette.Tables{}, cam: &camera.Camera{X: 16, Z: 16, ViewW: 4, ViewH: 4, MapW: 16, MapH: 16}}
	c.pal.Gray[0] = 123
	f := &frame.Frame{Fog: frame.FogView{Valid: true, W: 1, H: 1, Ch0: []byte{0}, Ch1: []byte{15}}}
	c.drawFog(f)
	c.replayForTest()
	if c.indexed[0] != 123 {
		t.Fatalf("fog pixel = %d, want 123 before selection", c.indexed[0])
	}
	// Reset before the selection stage so its replay executes only that stage over
	// the fogged surface, not the fog op a second time (WU-1.8).
	c.resetListForTest()
	c.selectionChrome = []selectionChrome{{view: frame.UnitView{Flags: hud.SelectionFlag, FootX: 1, FootZ: 1}, screenX: 8, screenY: 8}}
	c.drawSelectionStage()
	c.replayForTest()
	if c.indexed[0] != 123 {
		t.Fatalf("selection stage emitted an unsupported unit bracket over fog: got %d", c.indexed[0])
	}
}

// The former TestSelectedFullHealthKeepsHealthBarWithoutUnitBracket is gone
// with the bar it locked: the selection stage's "selected or damaged" health
// bar was invented and is deleted with drawUnitChrome. Retail's bar is the
// label walk of [03 R-FX-01 §6], drawn before the fog composite and covered by
// healthbar_test.go; the remaining half of the old assertion — that the stage
// emits no footprint bracket — is TestSelectionChromeKeepsFogWhenNoUnitBracketIsEmitted
// above.

func TestLensDoesNotNeedPublishedGAFArt(t *testing.T) {
	c := &Client{width: 640, height: 480, indexed: make([]uint8, 640*480), cam: &camera.Camera{}}
	views := []frame.ProjectileView{
		{Handle: 1, X: numeric.FixedFromInt(160), Z: numeric.FixedFromInt(64), RenderType: render.RenderTypeGlobalGAF, AssetID: "unpublished"},
		{Handle: 2, RenderType: render.RenderTypeBeam, PrimaryColor: 7, HasPrimaryColor: true},
	}
	stats := c.DrawProjectileViews(views, 1, func(frame.ProjectileView) bool { return true }, func(frame.ProjectileView) bool { return true }, c.projectileDispatchOptions())
	if stats.Aborted {
		t.Fatal("lens without GAF art aborted unrelated projectile dispatch")
	}
}
