package render

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

const (
	debrisMaxSignedWord numeric.Fixed = 2147483647
	debrisMinSignedWord numeric.Fixed = -2147483648
)

func debrisRequest(points int) DebrisRequest {
	return DebrisRequest{
		Points:   make([][3]numeric.Fixed, points),
		Position: [3]numeric.Fixed{0, numeric.FixedFromInt(10), 0},
		Lifetime: 900,
	}
}

type debrisImpactLog struct {
	ground []GroundDebrisImpact
	water  []WaterDebrisImpact
}

func (l *debrisImpactLog) GroundDebrisImpact(e GroundDebrisImpact) { l.ground = append(l.ground, e) }
func (l *debrisImpactLog) WaterDebrisImpact(e WaterDebrisImpact)   { l.water = append(l.water, e) }

func testDebrisContext() DebrisStepContext {
	return DebrisStepContext{
		SeaLevel: numeric.FixedFromInt(0),
		TerrainHeight: func(_, _ numeric.Fixed) numeric.Fixed {
			return numeric.FixedFromInt(0)
		},
	}
}

func TestDebrisAdmissionCopiesPointsAndUsesFirstEmptySlot(t *testing.T) {
	p := NewDebrisPool()
	r := debrisRequest(2)
	r.Points[0] = [3]numeric.Fixed{1, 2, 3}
	if !p.Admit(r) {
		t.Fatal("first debris admission failed")
	}
	r.Points[0][0] = 99
	views := p.SnapshotInto(nil)
	if len(views) != 1 || views[0].Slot != 0 {
		t.Fatalf("snapshot = %#v, want first slot", views)
	}
	if got := views[0].Points[0][0]; got != 1 {
		t.Fatalf("admission retained caller point = %d, want copied 1", got)
	}
	views[0].Points[0][0] = 77
	if got := p.SnapshotInto(nil)[0].Points[0][0]; got != 1 {
		t.Fatalf("snapshot exposed arena point = %d, want 1", got)
	}
}

func TestDebrisAdmissionStopsAtFixedSlotLimit(t *testing.T) {
	p := NewDebrisPool()
	for i := 0; i < WholeDebrisSlots; i++ {
		if !p.Admit(debrisRequest(0)) {
			t.Fatalf("admission %d of %d failed", i+1, WholeDebrisSlots)
		}
	}
	if p.Admit(debrisRequest(0)) {
		t.Fatal("admission succeeded after all 100 slots were occupied")
	}
}

func TestDebrisRingEvictsCursorTailThenWraps(t *testing.T) {
	p := NewDebrisPool()
	for _, vertices := range []int{4000, 3000, 2000, 1000, 900} {
		if !p.Admit(debrisRequest(vertices)) {
			t.Fatalf("admission with %d vertices failed", vertices)
		}
	}
	// The third request wraps and evicts slot 0. The next three entries occupy
	// the low range while slot 1 remains in the high tail.
	if got := p.SlotCount(); got != 4 {
		t.Fatalf("slot count after first wrap = %d, want 4", got)
	}
	if !p.Admit(debrisRequest(4400)) {
		t.Fatal("tail-crossing admission failed")
	}
	views := p.SnapshotInto(nil)
	if len(views) != 1 || views[0].Slot != 4 {
		t.Fatalf("cursor-forward tail and head eviction left slot %#v, want only the pre-eviction first-empty slot 4", views)
	}
	if got, want := p.StorageUsed(), 4400*12+debrisMinimumCharge; got != want {
		t.Fatalf("storage charge after wrap = %d, want %d", got, want)
	}
}

func TestDebrisRingRemainderSplitThreshold(t *testing.T) {
	for _, tc := range []struct {
		name       string
		vertices   int
		wantUsed   int
		wantCursor int
	}{
		{name: "split-at-nine-or-more", vertices: 8323, wantUsed: 99986, wantCursor: 99986},
		{name: "absorb-below-nine", vertices: 8324, wantUsed: WholeDebrisStorageCharge, wantCursor: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := NewDebrisPool()
			if !p.Admit(debrisRequest(tc.vertices)) {
				t.Fatal("admission failed")
			}
			if got := p.StorageUsed(); got != tc.wantUsed {
				t.Fatalf("retained charge = %d, want %d", got, tc.wantUsed)
			}
			if got := p.blocks[p.cursor].start; got != tc.wantCursor {
				t.Fatalf("cursor = %d, want %d", got, tc.wantCursor)
			}
		})
	}
}

func TestDebrisEvictionDoesNotClearReusedSlot(t *testing.T) {
	p := NewDebrisPool()
	p.slots[0] = debrisSlot{live: true, generation: 2}
	p.blocks[0] = debrisBlock{occupied: true, slot: 0, generation: 1}
	p.count = 1
	p.evictBlock(0)
	if !p.slots[0].live {
		t.Fatal("stale allocation block cleared a reused debris slot")
	}
}

func TestDebrisTailClearKeepsFreeBlockBoundary(t *testing.T) {
	p := NewDebrisPool()
	for _, vertices := range []int{7374, 851, 5, 783, 8220, 19} {
		if !p.Admit(debrisRequest(vertices)) {
			t.Fatalf("admission with %d vertices failed", vertices)
		}
	}
	if got := p.blocks[p.cursor].start; got != 99090 {
		t.Fatalf("cursor after retained-boundary sequence = %d, want 99090", got)
	}
}

func TestDebrisLifetimeReadZeroDiesBeforeIntegration(t *testing.T) {
	p := NewDebrisPool()
	if !p.Admit(debrisRequest(0)) {
		t.Fatal("admission failed")
	}
	ctx := testDebrisContext()
	for i := 0; i < 900; i++ {
		p.Step(ctx, nil)
		if p.SlotCount() != 1 {
			t.Fatalf("debris died on visit %d; 900 visits must survive", i+1)
		}
	}
	if got := p.SnapshotInto(nil)[0].Lifetime; got != 0 {
		t.Fatalf("lifetime after 900 visits = %d, want 0", got)
	}
	p.Step(ctx, nil)
	if got := p.SlotCount(); got != 0 {
		t.Fatalf("read-zero visit left %d debris slots alive", got)
	}
}

func TestDebrisTerrainEqualityBouncesAndRequestsGroundImpact(t *testing.T) {
	p := NewDebrisPool()
	r := debrisRequest(0)
	r.Position[1] = numeric.FixedFromInt(1)
	r.Velocity[1] = numeric.FixedFromInt(-1)
	r.ExplodeOnHit = true
	if !p.Admit(r) {
		t.Fatal("admission failed")
	}
	log := &debrisImpactLog{}
	p.Step(testDebrisContext(), log)
	if len(log.ground) != 1 || log.ground[0].Graphic != "explosion" || !log.ground[0].AboveSeaFlash || log.ground[0].CalculatedFrameTable != 0 {
		t.Fatalf("ground impact = %#v, want calculated-table-0 above-sea explosion", log.ground)
	}
	if p.SlotCount() != 0 {
		t.Fatal("equal terrain touch should remove slow bounced debris")
	}
}

func TestDebrisBounceThresholdIsStrict(t *testing.T) {
	p := NewDebrisPool()
	r := debrisRequest(0)
	r.Position[1] = numeric.FixedFromInt(4)
	r.Velocity[1] = numeric.FixedFromInt(-4) // post-bounce velocity is exactly 2
	if !p.Admit(r) {
		t.Fatal("admission failed")
	}
	p.Step(testDebrisContext(), nil)
	v := p.SnapshotInto(nil)
	if len(v) != 1 || v[0].Position[1] != numeric.FixedFromInt(6) {
		t.Fatalf("exact threshold bounce = %#v, want surviving piece at y=6", v)
	}
}

func TestDebrisPredictedHeightWrapsSigned32(t *testing.T) {
	p := NewDebrisPool()
	r := debrisRequest(0)
	r.Position[1] = debrisMaxSignedWord
	r.Velocity[1] = 1
	r.ExplodeOnHit = true
	if !p.Admit(r) {
		t.Fatal("admission failed")
	}
	log := &debrisImpactLog{}
	p.Step(testDebrisContext(), log)
	if len(log.ground) != 1 {
		t.Fatal("wrapped predicted height did not enter the terrain-contact arm")
	}
}

func TestDebrisPositionAndGravityWrapSigned32(t *testing.T) {
	t.Run("position", func(t *testing.T) {
		p := NewDebrisPool()
		r := debrisRequest(0)
		r.Position[0] = debrisMaxSignedWord
		r.Velocity[0] = 1
		if !p.Admit(r) {
			t.Fatal("admission failed")
		}
		p.Step(testDebrisContext(), nil)
		if got := p.SnapshotInto(nil)[0].Position[0]; got != debrisMinSignedWord {
			t.Fatalf("wrapped X = %d, want -2147483648", got)
		}
	})
	t.Run("gravity", func(t *testing.T) {
		p := NewDebrisPool()
		r := debrisRequest(0)
		r.Position[1] = numeric.FixedFromInt(10)
		r.Velocity[1] = debrisMinSignedWord
		r.Fall = true
		if !p.Admit(r) {
			t.Fatal("admission failed")
		}
		ctx := testDebrisContext()
		ctx.Gravity = 1
		ctx.TerrainHeight = func(_, _ numeric.Fixed) numeric.Fixed { return debrisMinSignedWord }
		p.Step(ctx, nil)
		if got := p.SnapshotInto(nil)[0].Velocity[1]; got != debrisMaxSignedWord {
			t.Fatalf("wrapped gravity velocity = %d, want 2147483647", got)
		}
	})
}

func TestDebrisWaterRequestsRespectSessionWordAndLava(t *testing.T) {
	for _, tc := range []struct {
		name       string
		waterZero  bool
		lava       bool
		wantEvents int
		graphic    string
	}{
		{name: "suppressed", waterZero: false},
		{name: "water", waterZero: true, graphic: "h2oboom2", wantEvents: 1},
		{name: "lava", waterZero: true, lava: true, graphic: "lavasplash", wantEvents: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := NewDebrisPool()
			r := debrisRequest(0)
			r.Position[1] = 0 // equality is water, not terrain [04 R-COB-04 §2]
			r.ExplodeOnHit = true
			if !p.Admit(r) {
				t.Fatal("admission failed")
			}
			log := &debrisImpactLog{}
			ctx := testDebrisContext()
			ctx.WaterEffectsWordZero = tc.waterZero
			ctx.Lava = tc.lava
			p.Step(ctx, log)
			if got := len(log.water); got != tc.wantEvents {
				t.Fatalf("water events = %d, want %d", got, tc.wantEvents)
			}
			if tc.wantEvents != 0 && (log.water[0].Graphic != tc.graphic || log.water[0].Lava != tc.lava) {
				t.Fatalf("water event = %#v", log.water[0])
			}
			if p.SlotCount() != 0 {
				t.Fatal("water debris must never survive")
			}
		})
	}
}

func TestDebrisBounceIntegratesAndPermutesAngles(t *testing.T) {
	p := NewDebrisPool()
	r := debrisRequest(0)
	r.Position = [3]numeric.Fixed{numeric.FixedFromInt(1), numeric.FixedFromInt(3), numeric.FixedFromInt(2)}
	r.Velocity = [3]numeric.Fixed{numeric.FixedFromInt(4), numeric.FixedFromInt(-5), numeric.FixedFromInt(-3)}
	r.Angles = [3]uint16{65535, 65534, 65533}
	r.AngularRates = [3]uint16{7, 8, 9}
	r.Fall = true
	if !p.Admit(r) {
		t.Fatal("admission failed")
	}
	ctx := testDebrisContext()
	ctx.Gravity = numeric.FixedFromInt(1)
	p.Step(ctx, nil)
	v := p.SnapshotInto(nil)
	if len(v) != 1 {
		t.Fatalf("bounced debris count = %d, want 1", len(v))
	}
	got := v[0]
	if want := [3]numeric.Fixed{numeric.FixedFromInt(3), numeric.FixedFromInt(5).Add(numeric.Fixed(1 << 15)), numeric.FixedFromInt(0).Add(numeric.Fixed(1 << 15))}; got.Position != want {
		t.Fatalf("post-bounce position = %v, want %v", got.Position, want)
	}
	if want := [3]uint16{7, 7, 4}; got.Angles != want {
		t.Fatalf("angles = %v, want %v", got.Angles, want)
	}
	if got.Velocity[1] != numeric.FixedFromInt(1).Add(numeric.Fixed(1<<15)) {
		t.Fatalf("fall gravity after integration left vy=%d, want 1.5", got.Velocity[1])
	}
}

// debrisTestCRT is a presentation CRT stand-in that replays a scripted list of
// draws in order. It is not the retail recurrence: these tests lock the
// arithmetic applied to a draw, not the generator.
type debrisTestCRT struct {
	values []int32
	used   int
}

func (c *debrisTestCRT) Rand() int32 {
	if c.used >= len(c.values) {
		c.used++
		return 0
	}
	v := c.values[c.used]
	c.used++
	return v
}

// TestDebrisViewsCarryTheSmokeAndFireBits locks the publication seam of
// [04 R-COB-04 §2]: the two draw-only engine bits are stored by the arena and
// must reach the committed view, because the client cannot read the arena.
func TestDebrisViewsCarryTheSmokeAndFireBits(t *testing.T) {
	for _, tc := range []struct{ smoke, fire bool }{
		{false, false}, {true, false}, {false, true}, {true, true},
	} {
		p := NewDebrisPool()
		req := debrisRequest(1)
		// SnapshotViewsInto publishes model identity, so the slot needs one.
		req.Model = &model.Model{Name: "debris", Pieces: []model.Piece{{}}}
		req.Smoke, req.Fire = tc.smoke, tc.fire
		if !p.Admit(req) {
			t.Fatalf("smoke=%v fire=%v: admission refused", tc.smoke, tc.fire)
		}
		views := p.SnapshotViewsInto(nil)
		if len(views) != 1 {
			t.Fatalf("smoke=%v fire=%v: %d views, want 1", tc.smoke, tc.fire, len(views))
		}
		if views[0].Smoke != tc.smoke || views[0].Fire != tc.fire {
			t.Fatalf("published smoke=%v fire=%v, want %v/%v: the draw pass is the only reader of these bits [04 R-COB-04 §2]",
				views[0].Smoke, views[0].Fire, tc.smoke, tc.fire)
		}
	}
}

// debrisTestCounts is a stand-in for the two bound entries' frame counts less
// one: twelve `smoke 1` frames and twenty `flamestream` frames in stock
// `fx.gaf` [03 R-FX-01 §3].
var debrisTestCounts = DebrisTrailFrameCounts{Smoke: 11, Flame: 19}

// TestDebrisTrailsMakeOneContainerPerSetBit locks the producer census of
// [04 R-COB-04 §2]: one smoke-puff container under the SMOKE bit, one
// flame-stream trail container under the FIRE bit, in that order, and nothing
// at all when neither is set. The families and the (bank, entry) pairs are
// [03 R-FX-01 §3]'s strip-9 rows, and each container carries its first
// sub-record from its own init.
func TestDebrisTrailsMakeOneContainerPerSetBit(t *testing.T) {
	for _, tc := range []struct {
		name         string
		smoke, fire  bool
		wantFamilies []frame.StripFamily
	}{
		{"neither", false, false, nil},
		{"smoke only", true, false, []frame.StripFamily{frame.StripFamilySmokePuff}},
		{"fire only", false, true, []frame.StripFamily{frame.StripFamilyFlameTrail}},
		{"both", true, true, []frame.StripFamily{frame.StripFamilySmokePuff, frame.StripFamilyFlameTrail}},
	} {
		v := frame.DebrisView{Smoke: tc.smoke, Fire: tc.fire}
		crt := &debrisTestCRT{}
		e := DebrisTrails(v, crt, 100, debrisTestCounts)
		if e.Count != len(tc.wantFamilies) {
			t.Fatalf("%s: %d containers, want %d", tc.name, e.Count, len(tc.wantFamilies))
		}
		var views []frame.StripView
		for i, want := range tc.wantFamilies {
			c := e.Containers[i]
			if c.Family != want {
				t.Fatalf("%s: container %d family %v, want %v", tc.name, i, c.Family, want)
			}
			if c.Count != 1 {
				t.Fatalf("%s: container %d holds %d sub-records at init, want 1: every family spawns once from its init [03 R-FX-01 §3]",
					tc.name, i, c.Count)
			}
			views = c.AppendViews(views)
		}
		for i := range views {
			if views[i].Strip != 9 {
				t.Fatalf("%s: view %d draws at strip %d, want 9 [03 R-FX-01 §3]", tc.name, i, views[i].Strip)
			}
			if views[i].Bank != DebrisTrailBank {
				t.Fatalf("%s: view %d bank %q, want %q", tc.name, i, views[i].Bank, DebrisTrailBank)
			}
			if views[i].Frame != 0 {
				t.Fatalf("%s: view %d starts at frame %d, want 0", tc.name, i, views[i].Frame)
			}
		}
		// One draw for the smoke puff's own last frame, four for the fire
		// particle's three jitter axes and its container life
		// [03 R-FX-01 §3][03 R-STRIP-01 §3].
		wantDraws := 0
		if tc.smoke {
			wantDraws++
		}
		if tc.fire {
			wantDraws += 4
		}
		if crt.used != wantDraws || e.CRTDraws != wantDraws {
			t.Fatalf("%s: %d/%d CRT draws, want %d", tc.name, crt.used, e.CRTDraws, wantDraws)
		}
	}
}

// TestDebrisFireParticleJitterAndLife locks the fire producer's arithmetic of
// [03 R-FX-01 §3]: three per-axis draws of `crtRand·3/0x8000 − 1` WHOLE units
// in X, Y, Z order, then a fourth of `crtRand·3/0x8000 + 1` for the container
// life. The smoke puff stays at the unjittered point, and its own draw comes
// FIRST — the smoke producer is bit 1 and runs before the fire producer.
func TestDebrisFireParticleJitterAndLife(t *testing.T) {
	base := frame.DebrisView{
		Smoke: true, Fire: true,
		X: numeric.FixedFromInt(100), Y: numeric.FixedFromInt(20), Z: numeric.FixedFromInt(300),
	}
	// The first value is the smoke puff's last-frame draw. Then 0 → -1,
	// 0x4000 → +0 (0x4000*3/0x8000 == 1, minus 1), 0x7fff → +1; the life draw
	// 0x7fff gives 3.
	crt := &debrisTestCRT{values: []int32{0, 0, 0x4000, 0x7fff, 0x7fff}}
	e := DebrisTrails(base, crt, 100, debrisTestCounts)
	if e.Count != 2 {
		t.Fatalf("%d containers, want 2", e.Count)
	}
	puff := e.Containers[0].Particles[0]
	if puff.X != base.X || puff.Y != base.Y || puff.Z != base.Z {
		t.Fatalf("smoke puff at (%v,%v,%v), want the piece's own point (%v,%v,%v)",
			puff.X, puff.Y, puff.Z, base.X, base.Y, base.Z)
	}
	fire := e.Containers[1]
	want := [3]numeric.Fixed{
		base.X - numeric.FixedFromInt(1),
		base.Y,
		base.Z + numeric.FixedFromInt(1),
	}
	if fire.Src[0] != want[0] || fire.Src[1] != want[1] || fire.Src[2] != want[2] {
		t.Fatalf("fire container source (%v,%v,%v), want (%v,%v,%v): jitter is crtRand·3/0x8000 − 1 whole units in X, Y, Z order",
			fire.Src[0], fire.Src[1], fire.Src[2], want[0], want[1], want[2])
	}
	if fire.Deadline != 100+3 {
		t.Fatalf("fire container deadline %d, want %d: life is crtRand·3/0x8000 + 1", fire.Deadline, 103)
	}
	// A == B, so every axis of the researched step is zero: a stationary flame
	// sprite [03 R-FX-01 §3].
	if fire.Travel != [3]numeric.Fixed{} {
		t.Fatalf("fire container travel %v, want zero on every axis: its source and target are the same point", fire.Travel)
	}
	if e.CRTDraws != 5 || crt.used != 5 {
		t.Fatalf("spent %d/%d draws, want 5: one for the puff's last frame and four for the fire particle", e.CRTDraws, crt.used)
	}
}

// TestDebrisTrailsWithoutAPresentationCRTMakeNothing keeps both producers
// honest when no presentation stream is bound: the puff's last frame and the
// particle's jitter have no substitute, so neither container is built rather
// than one being built with a substituted value [I4][I9].
func TestDebrisTrailsWithoutAPresentationCRTMakeNothing(t *testing.T) {
	e := DebrisTrails(frame.DebrisView{Smoke: true, Fire: true}, nil, 1, debrisTestCounts)
	if e.Count != 0 {
		t.Fatalf("built %d containers with no stream bound, want none", e.Count)
	}
}

// TestDebrisSmokePuffPersistsAndAnimates is the persistence contract of
// [03 R-FX-01 §3]: the smoke container is NOT a one-frame blit. Its puff holds
// a position on the strip list, the animation clock advances its cursor after
// the authored hold, the wind and gravity words drift the raw position words
// every tick, and the container retires only once the cursor reaches the puff's
// own drawn last frame.
func TestDebrisSmokePuffPersistsAndAnimates(t *testing.T) {
	const born = 500
	// A last-frame draw of 0x7fff gives the family's ceiling against a
	// twelve-frame `smoke 1`: trunc(0x7fff·9/0x8000) + 2 == 10, so the entry's
	// own last frame is never the puff's. Every later draw is the animation hold.
	crt := &debrisTestCRT{values: []int32{0x7fff}}
	e := DebrisTrails(frame.DebrisView{Smoke: true, X: numeric.FixedFromInt(10), Z: numeric.FixedFromInt(10)}, crt, born, debrisTestCounts)
	c := e.Containers[0]
	if c.Particles[0].LastFrame != 10 {
		t.Fatalf("puff last frame %d, want 10: crtRand·(frameCount − 3)/0x8000 + 2 [06 R-WFX-01 §5]", c.Particles[0].LastFrame)
	}

	const windX, windZ, gravity = 40, -24, 112
	startX, startY, startZ := c.Particles[0].X, c.Particles[0].Y, c.Particles[0].Z
	lastFrameSeen := int32(0)
	retiredAt := uint32(0)
	for tick := uint32(born + 1); tick <= born+400; tick++ {
		if c.Retired(tick) {
			retiredAt = tick
			break
		}
		c.Step(tick, windX, windZ, gravity, crt)
		if c.Count != 0 {
			lastFrameSeen = c.Particles[0].Frame
		}
	}
	if retiredAt == 0 {
		t.Fatal("the smoke container never retired: its puff's cursor must reach its own drawn last frame")
	}
	// The retirement is taken on the same pass that the cursor reaches the
	// drawn last frame, so the highest cursor any draw sees is one below it
	// [06 R-WFX-01 §5].
	if lastFrameSeen != 9 {
		t.Fatalf("the puff's cursor last drew frame %d, want 9: the clock must walk it up to one below its drawn last frame of 10", lastFrameSeen)
	}
	// One puff per container and no respawn: the window closed at the birth
	// tick, so the gate refuses every later spawn [03 R-FX-01 §3].
	if c.Count != 0 {
		t.Fatalf("%d sub-records survived retirement, want 0", c.Count)
	}
	// Drift is added to the RAW 16.16 words, so the puff moves a world unit or
	// two over its whole life and rises — it does not travel [03 R-FX-01 §3].
	if startX.Raw() == 0 && startZ.Raw() == 0 {
		t.Fatal("fixture error: the puff started at the origin, so drift cannot be told from it")
	}
	_ = startY
}

// TestDebrisSmokePuffDriftIsRawWords locks the three per-tick adds exactly:
// `x += windX·8`, `z += windZ·8` and `y += authoredGravity·4` against the RAW
// 16.16 words, the strips-5/9 class's scales and not the vent's
// [03 R-FX-01 §3][R-WIND-01].
func TestDebrisSmokePuffDriftIsRawWords(t *testing.T) {
	crt := &debrisTestCRT{values: []int32{0}}
	base := frame.DebrisView{Smoke: true, X: numeric.FixedFromInt(10), Y: numeric.FixedFromInt(4), Z: numeric.FixedFromInt(10)}
	e := DebrisTrails(base, crt, 1, debrisTestCounts)
	c := e.Containers[0]
	const windX, windZ, gravity = 40, -24, 112
	c.Step(2, windX, windZ, gravity, crt)
	p := c.Particles[0]
	if got, want := p.X.Raw(), base.X.Raw()+windX*8; got != want {
		t.Fatalf("puff X raw %d, want %d: windX·8 on the raw word", got, want)
	}
	if got, want := p.Z.Raw(), base.Z.Raw()+windZ*8; got != want {
		t.Fatalf("puff Z raw %d, want %d: windZ·8 on the raw word", got, want)
	}
	if got, want := p.Y.Raw(), base.Y.Raw()+gravity*4; got != want {
		t.Fatalf("puff Y raw %d, want %d: authoredGravity·4 on the raw word — the strips-5/9 scale, not the vent's 16", got, want)
	}
}

// TestDebrisFireContainerLaysLifetimePlusOneSegments locks the trail family's
// spawn gate: one segment from the init and one per tick while
// `nextSpawn ≤ deadline`, so `lifetime + 1` coincident segments in all, every
// one of them at a fresh copy of the source, all extinguished together the tick
// after the deadline. The family spends no draw while it runs
// [03 R-FX-01 §3][03 R-STRIP-01 §3].
func TestDebrisFireContainerLaysLifetimePlusOneSegments(t *testing.T) {
	const born = 40
	// The life draw 0x7fff gives 3, so four segments over ticks 40..43.
	crt := &debrisTestCRT{values: []int32{0x4000, 0x4000, 0x4000, 0x7fff}}
	e := DebrisTrails(frame.DebrisView{Fire: true, X: numeric.FixedFromInt(8), Z: numeric.FixedFromInt(9)}, crt, born, debrisTestCounts)
	c := e.Containers[0]
	spent := crt.used
	counts := []int{c.Count}
	frames := []int32{c.Particles[0].Frame}
	for tick := uint32(born + 1); tick <= born+5; tick++ {
		if c.Retired(tick) {
			counts = append(counts, -1)
			break
		}
		c.Step(tick, 40, -24, 112, crt)
		counts = append(counts, c.Count)
		if c.Count != 0 {
			frames = append(frames, c.Particles[0].Frame)
		}
	}
	want := []int{1, 2, 3, 4, 0, -1}
	if len(counts) != len(want) {
		t.Fatalf("segment census %v, want %v", counts, want)
	}
	for i := range want {
		if counts[i] != want[i] {
			t.Fatalf("segment census %v, want %v: lifetime + 1 segments, all expiring together the tick after the deadline", counts, want)
		}
	}
	if crt.used != spent {
		t.Fatalf("the trail family spent %d draws while it ran, want 0 [03 R-STRIP-01 §3]", crt.used-spent)
	}
	// hold 1, so the first segment's cursor advances every tick and wraps
	// modulo frameCount − 1, never showing the entry's last frame.
	wantFrames := []int32{0, 1, 2, 3}
	for i := range wantFrames {
		if frames[i] != wantFrames[i] {
			t.Fatalf("first segment's cursor walked %v, want %v: hold 1 advances it every tick [03 R-FX-01 §3]", frames, wantFrames)
		}
	}
}

// TestDebrisTrailWindMatchesThePublishedWords locks the drift source: the FIRST
// published word is −2·speed·sin(heading) and feeds world X, the SECOND is
// −2·speed·cos(heading) and feeds world Z [R-WIND-01]. A calm wind moves
// nothing.
func TestDebrisTrailWindMatchesThePublishedWords(t *testing.T) {
	if x, z := DebrisTrailWind(frame.WindView{Strength: 0, Heading: 0x4000}); x != 0 || z != 0 {
		t.Fatalf("a calm wind published (%d,%d), want (0,0)", x, z)
	}
	// Heading 0 is sin 0 / cos 1, so the first word is zero and the second is
	// the whole −2·speed term.
	x, z := DebrisTrailWind(frame.WindView{Strength: 50, Heading: 0})
	if x != 0 {
		t.Fatalf("wind X word %d at heading 0, want 0: the first word is the sine term", x)
	}
	if z != -2*numeric.MulRound(50, numeric.Cos(numeric.Angle(0))) {
		t.Fatalf("wind Z word %d at heading 0, want the −2·speed·cos term", z)
	}
}
