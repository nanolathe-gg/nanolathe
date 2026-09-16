package client

import (
	"fmt"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/frame"
	presentationrender "github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
)

// debrisTrailView places one debris record inside the strip fixture's 64x64
// framebuffer with the two draw-only engine bits set as asked.
func debrisTrailView(smoke, fire bool) frame.DebrisView {
	return frame.DebrisView{
		Smoke: smoke, Fire: fire,
		X: numeric.FixedFromInt(20), Y: numeric.FixedFromInt(0), Z: numeric.FixedFromInt(20),
	}
}

// TestDebrisDrawMakesOnePuffAndOneFireContainer locks the producer census of
// [04 R-COB-04 §2] at the client seam: a drawn debris piece makes one strip-9
// smoke-puff container under its SMOKE bit and one flame-stream trail container
// under its FIRE bit, and nothing when the bit is clear. The puff's own last
// frame costs one presentation draw and the fire particle's jitter and life
// cost four [03 R-FX-01 §3][03 R-STRIP-01 §3].
func TestDebrisDrawMakesOnePuffAndOneFireContainer(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		smoke, fire            bool
		wantSmoke, wantFire    int
		wantPresentationDrawsN uint64
	}{
		{"neither", false, false, 0, 0, 0},
		{"smoke only", true, false, 1, 0, 1},
		{"fire only", false, true, 0, 1, 4},
		{"both", true, true, 1, 1, 5},
	} {
		c := stripTestClient(t)
		crt := rng.NewCRT(1)
		c.SetPresentationCRT(&crt)
		before := c.crt.Draws()

		smoke, fire := c.emitDebrisTrails(debrisTrailView(tc.smoke, tc.fire))
		if smoke != tc.wantSmoke || fire != tc.wantFire {
			t.Fatalf("%s: made %d puff containers and %d fire containers, want %d and %d [04 R-COB-04 §2]",
				tc.name, smoke, fire, tc.wantSmoke, tc.wantFire)
		}
		if got := len(c.debrisTrails.live); got != tc.wantSmoke+tc.wantFire {
			t.Fatalf("%s: the store holds %d containers, want %d: a container persists, it is not blitted and forgotten",
				tc.name, got, tc.wantSmoke+tc.wantFire)
		}
		if got := c.crt.Draws() - before; got != tc.wantPresentationDrawsN {
			t.Fatalf("%s: spent %d presentation CRT draws, want %d [03 R-FX-01 §3]",
				tc.name, got, tc.wantPresentationDrawsN)
		}
	}
}

// TestDebrisTrailEmissionRunsOncePerCommittedTick locks the cadence this build
// admits containers at. Retail's producer runs per rendered frame; this build
// composes several frames per committed tick (interpolation, the pre-record's
// re-record after a miss, the two-renderer shot route) while the containers are
// stepped by the TICK, so the producers run once per burning piece per
// committed tick - see the divergence note on emitDebrisTrails and
// docs/DESIGN_PRESENTATION_CLIENT.md C2.2.
func TestDebrisTrailEmissionRunsOncePerCommittedTick(t *testing.T) {
	c := stripTestClient(t)
	crt := rng.NewCRT(7)
	c.SetPresentationCRT(&crt)
	v := debrisTrailView(true, true)

	c.frameTick = 12
	for i := 0; i < 3; i++ {
		smoke, fire := c.emitDebrisTrails(v)
		if i == 0 && (smoke != 1 || fire != 1) {
			t.Fatalf("the first render of a tick made %d/%d containers, want 1 and 1", smoke, fire)
		}
		if i > 0 && (smoke != 0 || fire != 0) {
			t.Fatalf("re-rendering the same committed tick made %d/%d more containers, want none", smoke, fire)
		}
	}
	if got := c.crt.Draws(); got != 5 {
		t.Fatalf("three renders of one committed tick spent %d presentation draws, want 5: a repeat spends nothing", got)
	}

	// Three distinct committed ticks are three sets of containers.
	for tick := uint32(13); tick <= 14; tick++ {
		c.frameTick = tick
		if smoke, fire := c.emitDebrisTrails(v); smoke != 1 || fire != 1 {
			t.Fatalf("tick %d made %d/%d containers, want 1 and 1", tick, smoke, fire)
		}
	}
	if got := len(c.debrisTrails.live); got != 6 {
		t.Fatalf("three committed ticks left %d containers in the store, want 6", got)
	}
}

// TestDebrisTrailsNeverTouchTheSessionStream is the DET-01 half: the client
// binds a COPY of the presentation stream, so neither its emissions nor the
// store's per-tick sweep can move the stream the session owns [01 §7.2][I4].
func TestDebrisTrailsNeverTouchTheSessionStream(t *testing.T) {
	c := stripTestClient(t)
	session := rng.NewCRT(99)
	c.SetPresentationCRT(&session)
	c.emitDebrisTrails(debrisTrailView(true, true))
	for tick := uint32(0); tick < 40; tick++ {
		cur := stripTestFrame(true)
		cur.Tick = tick
		c.stepDebrisTrails(cur)
	}
	if session.Draws() != 0 || session.State != rng.NewCRT(99).State {
		t.Fatalf("the session stream moved to state %#x after %d draws: presentation binds a copy (DET-01)",
			session.State, session.Draws())
	}
}

// TestDebrisTrailStoreStepsOncePerCommittedTick locks the pre-record and
// interpolation rule: the store's sweep is keyed on the COMMITTED tick, so
// composing the same tick again - a re-record after a pre-record miss, the
// two-renderer shot route, or any interpolated frame - does not step a
// container twice. See docs/DESIGN_PRESENTATION_CLIENT.md C2.2.
func TestDebrisTrailStoreStepsOncePerCommittedTick(t *testing.T) {
	c := stripTestClient(t)
	crt := rng.NewCRT(3)
	c.SetPresentationCRT(&crt)
	born := stripTestFrame(true)
	born.Tick = 5
	c.stepDebrisTrails(born)
	c.frameTick = 5
	c.emitDebrisTrails(debrisTrailView(false, true))
	if len(c.debrisTrails.live) != 1 {
		t.Fatalf("the fire producer made %d containers, want 1", len(c.debrisTrails.live))
	}

	// Ten renders of tick 6 are one sweep: the container lays exactly one more
	// segment, not ten.
	for i := 0; i < 10; i++ {
		cur := stripTestFrame(true)
		cur.Tick = 6
		c.stepDebrisTrails(cur)
	}
	if got := c.debrisTrails.live[0].Count; got != 2 {
		t.Fatalf("ten renders of one committed tick left %d segments, want 2: the sweep is keyed on the tick", got)
	}
	if got := c.debrisTrails.tick; got != 6 {
		t.Fatalf("the store stepped to tick %d, want 6", got)
	}
}

// TestDebrisTrailStoreRetiresOnADiscontinuity locks the reset rule: a committed
// tick that moves backwards, or forwards by more than the catch-up bound, is a
// load or a new battle, and the retained containers are dropped rather than
// replayed into it.
func TestDebrisTrailStoreRetiresOnADiscontinuity(t *testing.T) {
	c := stripTestClient(t)
	crt := rng.NewCRT(3)
	c.SetPresentationCRT(&crt)
	c.frameTick = 100
	cur := stripTestFrame(true)
	cur.Tick = 100
	c.stepDebrisTrails(cur)
	c.emitDebrisTrails(debrisTrailView(true, true))
	if len(c.debrisTrails.live) != 2 {
		t.Fatalf("the producers made %d containers, want 2", len(c.debrisTrails.live))
	}
	jump := stripTestFrame(true)
	jump.Tick = 100 + debrisTrailCatchUp + 1
	c.stepDebrisTrails(jump)
	if len(c.debrisTrails.live) != 0 {
		t.Fatalf("%d containers survived a %d-tick jump, want none", len(c.debrisTrails.live), debrisTrailCatchUp+1)
	}
}

// TestDebrisTrailStoreIsBounded locks the eviction of [03 "Strip storage and
// lifecycle"][03 R-STRIP-01 §1]: when the pre-insert count exceeds the steady
// cap the OLDEST container is destroyed first, so the store holds at most
// cap + 1 and the survivors keep insertion order.
func TestDebrisTrailStoreIsBounded(t *testing.T) {
	var s debrisTrailStore
	const extra = 50
	for i := 0; i < debrisTrailSteadyCap+extra; i++ {
		s.append(presentationrender.DebrisTrailContainer{Born: uint32(i)})
	}
	if len(s.live) != debrisTrailSteadyCap+1 {
		t.Fatalf("the store holds %d containers, want %d: eviction runs when the pre-insert count exceeds the cap",
			len(s.live), debrisTrailSteadyCap+1)
	}
	newest := uint32(debrisTrailSteadyCap + extra - 1)
	if s.live[len(s.live)-1].Born != newest {
		t.Fatalf("newest survivor was born at %d, want %d", s.live[len(s.live)-1].Born, newest)
	}
	if s.live[0].Born != newest-uint32(debrisTrailSteadyCap) {
		t.Fatalf("oldest survivor was born at %d, want %d: eviction takes the oldest and the rest keep insertion order",
			s.live[0].Born, newest-uint32(debrisTrailSteadyCap))
	}
	if s.evicted != extra-1 {
		t.Fatalf("the store evicted %d containers, want %d", s.evicted, extra-1)
	}
}

// debrisTrailCaptureOrigin keeps the captured piece clear of the map origin.
// The barrier-9 draw puts every sub-record through the one-point coverage gate,
// and the fire particle's jitter is ±1 WHOLE world unit, so a piece sitting on
// the origin can produce a container at a negative tile - off-map, and the gate
// fails an off-map tile [03 R-FX-01 §3].
const debrisTrailCaptureOrigin = 32

// debrisTrailCaptureFrame is one committed frame whose coverage admits the
// whole capture area, so the barrier-9 one-point gate never hides a puff the
// capture is meant to show.
func debrisTrailCaptureFrame(tick uint32) *frame.Frame {
	grid := make([]uint8, 64*64)
	for i := range grid {
		grid[i] = 1
	}
	return &frame.Frame{
		Tick: tick,
		Visibility: frame.VisibilityView{
			Valid: true, W: 64, H: 64, CoverageBytes: true, Visible: grid,
		},
	}
}

// TestDebrisTrailCapture is the visual evidence for [04 R-COB-04 §2]: the same
// whole-piece debris record drawn with each combination of its two draw-only
// engine bits, through the real palette and the real `fx` bank.
//
// The flags-clear capture is what every debris piece looked like while the bits
// were dropped at the publication boundary, so the four pictures are the
// before/after pair. The assertion is the relationship - a set bit adds covered
// pixels, a clear bit adds none - not a census.
func TestDebrisTrailCapture(t *testing.T) {
	c, _ := captureClient(t, 128, 128)
	// Keep the subject centred after the origin offset above.
	c.cam.X += debrisTrailCaptureOrigin
	c.cam.Z += debrisTrailCaptureOrigin
	crt := rng.NewCRT(11)
	c.SetPresentationCRT(&crt)

	view := frame.DebrisView{
		Model: "ARMCOM",
		// Piece 0 is the model's ground plate and composes nothing; the torso
		// is a solid chunk of the kind a death script detaches.
		PieceIndex: 6,
		X:          numeric.FixedFromInt(debrisTrailCaptureOrigin),
		Y:          numeric.FixedFromInt(0),
		Z:          numeric.FixedFromInt(debrisTrailCaptureOrigin),
	}
	// A mid-grey ground makes the translucent blend visible: the tinted blitter
	// resolves every source pixel against the destination, so a puff over index
	// zero can resolve back to zero.
	const ground = 0x28
	cur := debrisTrailCaptureFrame(0)
	frames := map[string][]uint8{}
	for _, tc := range []struct {
		name        string
		smoke, fire bool
	}{
		{"neither", false, false},
		{"smoke", true, false},
		{"fire", false, true},
		{"both", true, true},
	} {
		c.debrisTrails.reset()
		for i := range c.indexed {
			c.indexed[i] = ground
		}
		v := view
		v.Smoke, v.Fire = tc.smoke, tc.fire
		c.resetListForTest()
		c.frameTick = cur.Tick
		if !c.drawDebrisModel(v) {
			t.Skipf("the debris piece composed no geometry in this install")
		}
		c.drawDebrisTrails(cur)
		c.replayForTest()
		frames[tc.name] = append([]uint8(nil), c.indexed...)
		writeCapture(t, c, "ds41-debris-"+tc.name)
	}
	changed := func(name string) int {
		n := 0
		for i := range frames["neither"] {
			if frames[name][i] != frames["neither"][i] {
				n++
			}
		}
		return n
	}
	if changed("smoke") == 0 {
		t.Fatal("the SMOKE bit changed no pixel against the same record with it clear")
	}
	if changed("fire") == 0 {
		t.Fatal("the FIRE bit changed no pixel against the same record with it clear")
	}
}

// TestDebrisTrailPersistenceCapture is the visual evidence for the persistence
// contract of [03 R-FX-01 §3]: a burning piece crossing ten committed ticks
// leaves a trail of containers behind it, each further into its own animation
// than the one in front of it. It composes the same piece at ten successive
// committed ticks, each composed from scratch, so everything behind the piece
// in the last picture is a container an earlier tick made.
func TestDebrisTrailPersistenceCapture(t *testing.T) {
	c, _ := captureClient(t, 192, 192)
	c.cam.X += debrisTrailCaptureOrigin
	c.cam.Z += debrisTrailCaptureOrigin
	crt := rng.NewCRT(5)
	c.SetPresentationCRT(&crt)

	const ground = 0x28
	live := make([]int, 0, 10)
	for tick := uint32(0); tick < 10; tick++ {
		// Each picture is one committed tick composed from scratch, so what is
		// behind the piece is the retained containers and nothing else.
		for i := range c.indexed {
			c.indexed[i] = ground
		}
		cur := debrisTrailCaptureFrame(tick)
		c.stepDebrisTrails(cur)
		c.frameTick = tick
		v := frame.DebrisView{
			Model: "ARMCOM", PieceIndex: 6, Smoke: true, Fire: true,
			// The piece flies up and across, the way a detached piece does.
			X: numeric.FixedFromInt(debrisTrailCaptureOrigin + int64(tick)*3),
			Y: numeric.FixedFromInt(int64(tick) * 2),
			Z: numeric.FixedFromInt(debrisTrailCaptureOrigin + int64(tick)*3),
		}
		c.resetListForTest()
		if !c.drawDebrisModel(v) {
			t.Skipf("the debris piece composed no geometry in this install")
		}
		c.drawDebrisTrails(cur)
		c.replayForTest()
		live = append(live, len(c.debrisTrails.live))
		writeCapture(t, c, fmt.Sprintf("ds41-debris-persist-tick%02d", tick))
	}
	// The store must GROW: a container made on tick 0 is still live on tick 9,
	// which is the whole point of the persistence.
	if live[9] <= live[0] {
		t.Fatalf("the store held %v containers over ten ticks: a container must survive the tick that made it", live)
	}
	// Every container of the run must be reachable at barrier 9 on the last
	// tick, not just the newest one.
	var views []frame.StripView
	for i := range c.debrisTrails.live {
		views = c.debrisTrails.live[i].AppendViews(views)
	}
	if len(views) < 10 {
		t.Fatalf("barrier 9 saw %d sub-records on the last tick, want at least ten: ten ticks of a burning piece", len(views))
	}
	var smokeFrames []int32
	for _, v := range views {
		if v.Family == frame.StripFamilySmokePuff {
			smokeFrames = append(smokeFrames, v.Frame)
		}
	}
	if len(smokeFrames) < 2 {
		t.Fatalf("only %d smoke puffs survived ten ticks, want the whole run", len(smokeFrames))
	}
	// The oldest puff is the furthest into its animation.
	if smokeFrames[0] <= smokeFrames[len(smokeFrames)-1] {
		t.Fatalf("the puff animation cursors read %v oldest-first, want the oldest strictly furthest along", smokeFrames)
	}
}
