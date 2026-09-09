package render

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/sim/numeric"
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
