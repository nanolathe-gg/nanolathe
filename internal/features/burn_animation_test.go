package features

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
)

// TestBurnSmokeJitterAddends locks the arithmetic of [05 R-FEAT-01 §10] pass
// 3a: the two addends the burning-feature smoke position takes, scaled by the
// current burn frame's width and height, with every division an integer part
// and the result truncated to 16 bits.
//
//	x += (draw·(w/2))/32768 - frame.xoff + w/4
//	y += 2·(frame.yoff - (draw·(h/2))/32768) - 2·(h/4)
func TestBurnSmokeJitterAddends(t *testing.T) {
	for _, tc := range []struct {
		name           string
		frame          burnFrameGeometry
		drawX, drawY   int32
		wantDX, wantDY int32
	}{
		{
			// Maximum horizontal draw, minimum vertical: the scaled term
			// reaches w/2 only asymptotically, so 32767·10/32768 truncates to
			// 9, not 10.
			name:  "max horizontal draw truncates below half the width",
			frame: burnFrameGeometry{W: 20, H: 12, XOff: 7, YOff: 5},
			drawX: 32767, drawY: 0,
			wantDX: 9 - 7 + 5,
			wantDY: 2*(5-0) - 2*3,
		},
		{
			// Minimum horizontal draw, maximum vertical.
			name:  "max vertical draw truncates below half the height",
			frame: burnFrameGeometry{W: 20, H: 12, XOff: 7, YOff: 5},
			drawX: 0, drawY: 32767,
			wantDX: 0 - 7 + 5,
			wantDY: 2*(5-5) - 2*3,
		},
		{
			// Odd dimensions: w/2, w/4, h/2 and h/4 are integer parts taken
			// before the scaling, so 5 gives 2 and 1, and 7 gives 3 and 1.
			name:  "odd frame dimensions take integer parts",
			frame: burnFrameGeometry{W: 5, H: 7, XOff: 2, YOff: 3},
			drawX: 32767, drawY: 32767,
			wantDX: 1 - 2 + 1,
			wantDY: 2*(3-2) - 2*1,
		},
		{
			// A frame with no offsets still shifts by the quarter terms, which
			// is what recentres the puff on the sprite.
			name:  "zero offsets keep the quarter terms",
			frame: burnFrameGeometry{W: 16, H: 16, XOff: 0, YOff: 0},
			drawX: 16384, drawY: 16384,
			wantDX: 4 - 0 + 4,
			wantDY: 2*(0-4) - 2*4,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dx, dy := burnSmokeJitter(tc.frame, tc.drawX, tc.drawY)
			if dx != tc.wantDX || dy != tc.wantDY {
				t.Fatalf("jitter addends (%d,%d), want (%d,%d) [05 R-FEAT-01 §10 pass 3a]", dx, dy, tc.wantDX, tc.wantDY)
			}
		})
	}
}

// burningFeatureService builds a one-cell service holding a burning sprite
// instance whose countdown is far from zero, so no burn event fires and the
// only stream traffic in a lifecycle tick is the smoke jitter.
func burningFeatureService(t *testing.T, crt *rng.CRT, sim *rng.Simulation, cells ...[2]int) *Service {
	t.Helper()
	terrain := newEmptyTerrain(8, 8)
	svc := NewService(terrain, sim, crt, nil)
	def := featureDef("burnJitter", 0, 0, 10)
	def.Flamable = true
	def.SeqNameBurn = "burn"
	for _, c := range cells {
		if svc.spawnFeatureAt(c[0], c[1], def) == nil {
			t.Fatalf("spawn at (%d,%d) rejected", c[0], c[1])
		}
		inst := svc.InstanceAt(c[0], c[1])
		inst.IsBurning = true
		inst.BurnCountdown = 1000
		inst.BurnDuration = 1000
	}
	return svc
}

// containerDraw stands for the producer the session binds: the strip-5
// smoke-puff container's constructor spawns its single puff at the producer,
// and that spawn takes one CRT draw — the puff's last frame
// [05 R-FEAT-01 §16][03 R-STRIP-01 §2]. The features package owns no strip
// table, so the third draw of an emission is modelled here by the seam.
func containerDraw(crt *rng.CRT) func([3]numeric.Fixed) {
	return func([3]numeric.Fixed) { crt.Rand() }
}

// TestBurnSmokeDrawsThreeCRTPerBurningInstanceEveryThirdTick locks the draw
// budget of [05 R-FEAT-01 §16]: exactly three CRT draws per burning instance on
// a tick whose index is a multiple of three — the X jitter, the Y jitter, and
// the container's last-frame draw inside the producer — none on any other tick,
// and none on the simulation stream. [01 §7.5]'s phase-6 row counted the call
// site's two only and is corrected to three by that section.
func TestBurnSmokeDrawsThreeCRTPerBurningInstanceEveryThirdTick(t *testing.T) {
	crt := rng.CRTFromState(0x1234567)
	sim := rng.SimulationFromState(99)
	svc := burningFeatureService(t, &crt, &sim, [2]int{1, 1}, [2]int{5, 3})
	svc.BurnSmoke = containerDraw(&crt)

	for tick := uint32(0); tick < 7; tick++ {
		beforeCRT := crt.Draws()
		beforeSim := sim.Draws()
		svc.TickLifecycle(tick)
		gotCRT := crt.Draws() - beforeCRT
		wantCRT := uint64(0)
		if tick%3 == 0 {
			wantCRT = 6 // two burning instances, three draws each
		}
		if gotCRT != wantCRT {
			t.Fatalf("tick %d consumed %d CRT draws, want %d [05 R-FEAT-01 §16]", tick, gotCRT, wantCRT)
		}
		if got := sim.Draws() - beforeSim; got != 0 {
			t.Fatalf("tick %d consumed %d simulation draws for smoke; the jitter is CRT-only [05 R-FEAT-01 §10 pass 3a]", tick, got)
		}
	}
}

// TestBurnSmokeSiteTakesItsTwoJitterDrawsWithNoProducer is the other half of
// the same budget: the two jitter draws belong to the call site and are taken
// before the seam is consulted, so a service with no producer bound still
// advances the stream by two. A battle always has a producer — the composer
// binds one — so this is the fixture case, not a second retail behavior.
func TestBurnSmokeSiteTakesItsTwoJitterDrawsWithNoProducer(t *testing.T) {
	crt := rng.CRTFromState(0x1234567)
	sim := rng.SimulationFromState(99)
	svc := burningFeatureService(t, &crt, &sim, [2]int{1, 1})
	before := crt.Draws()
	svc.TickLifecycle(0)
	if got := crt.Draws() - before; got != 2 {
		t.Fatalf("unbound producer consumed %d CRT draws, want the site's own 2 [05 R-FEAT-01 §10 pass 3a]", got)
	}
}

// TestBurnSmokeDrawsAreConsecutiveOnTheCRTStream locks the ORDER as well as
// the count: the three values are the next three of the CRT stream, taken back
// to back and in the order X jitter, Y jitter, container last frame, so a
// reference stream advanced three times lands on the same state
// [05 R-FEAT-01 §16][01 §7.2].
func TestBurnSmokeDrawsAreConsecutiveOnTheCRTStream(t *testing.T) {
	const seed = 0x2468ace
	crt := rng.CRTFromState(seed)
	sim := rng.SimulationFromState(7)
	svc := burningFeatureService(t, &crt, &sim, [2]int{2, 2})
	// Record which value the producer sees, to pin it as the THIRD draw rather
	// than one taken before the jitter pair.
	var atProducer uint32
	svc.BurnSmoke = func([3]numeric.Fixed) { atProducer = uint32(crt.Rand()) }
	svc.TickLifecycle(0)

	ref := rng.CRTFromState(seed)
	ref.Rand()
	ref.Rand()
	wantThird := uint32(ref.Rand())
	if atProducer != wantThird {
		t.Fatalf("the producer drew %#x, want the stream's third value %#x [05 R-FEAT-01 §16]", atProducer, wantThird)
	}
	if crt.State != ref.State {
		t.Fatalf("CRT state %#x after the emission, want %#x — the three draws must be consecutive [05 R-FEAT-01 §16]", crt.State, ref.State)
	}
}

// dieAnimationService attaches a die or reclaim animation record to a sprite
// feature, the way [05 R-FEAT-01 §5] step 5 would once the transition entry
// grows that branch: burning bit clear, animation-record flag set, the save
// family's selector recording which sequence plays.
func dieAnimationService(t *testing.T, selector uint8, duration int32) (*Service, *content.FeatureDef, *content.FeatureDef) {
	t.Helper()
	terrain := newEmptyTerrain(4, 4)
	dead := featureDef("smudgedead", 0, 0, 10)
	reclamate := featureDef("smudgereclamate", 0, 0, 10)
	src := featureDef("dyingTree", 0, 0, 10)
	src.SeqNameDie = "die"
	src.SeqNameReclamate = "reclamate"
	src.FeatureDeadDef = dead
	src.FeatureReclamateDef = reclamate
	terrain.FeatureDefs = []*content.FeatureDef{src, dead, reclamate}
	svc := NewService(terrain, nil, nil, nil)
	if svc.spawnFeatureAt(1, 1, src) == nil {
		t.Fatal("spawn rejected")
	}
	inst := svc.InstanceAt(1, 1)
	inst.IsBurning = false
	inst.IsAnimating = true
	inst.AnimationSelector = selector
	inst.BurnDuration = duration
	return svc, dead, reclamate
}

// TestDieAndReclaimAnimationsRetireOnTheVisitThatEndsTheSequence locks the
// retirement condition of [05 R-FEAT-01 §10] pass 3: the animation ends on the
// visit whose cursor advance clears the sequence pointer — the visit after the
// last frame's delay expires, never earlier — and that visit runs §5 step 6's
// replacement at the anchor. Step 6 takes `featuredead` for the argument the
// animation end passes, EXCEPT that a record whose reclaim-animation bit is
// set promotes the successor to `featurereclamate` regardless.
func TestDieAndReclaimAnimationsRetireOnTheVisitThatEndsTheSequence(t *testing.T) {
	for _, tc := range []struct {
		name     string
		selector uint8
		want     string
	}{
		{name: "death animation takes featuredead", selector: featureAnimSelectorDie, want: "smudgedead"},
		{name: "reclaim animation promotes to featurereclamate", selector: featureAnimSelectorReclaim, want: "smudgereclamate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const duration = 3
			svc, _, _ := dieAnimationService(t, tc.selector, duration)
			inst := svc.InstanceAt(1, 1)
			for visit := int32(1); visit < duration; visit++ {
				svc.TickLifecycle(uint32(visit))
				if svc.InstanceAt(1, 1) != inst {
					t.Fatalf("visit %d retired the record early [05 R-FEAT-01 §10 pass 3]", visit)
				}
				if inst.BurnTicks != visit {
					t.Fatalf("visit %d advanced the cursor to %d, want %d", visit, inst.BurnTicks, visit)
				}
			}
			svc.TickLifecycle(duration)
			got := svc.InstanceAt(1, 1)
			if got == nil || got.Def == nil {
				t.Fatalf("the ending visit left no successor at the anchor, want %q [05 R-FEAT-01 §5 step 6]", tc.want)
			}
			if string(got.Def.CanonicalKey) != tc.want {
				t.Fatalf("successor %q, want %q [05 R-FEAT-01 §5 step 6]", got.Def.CanonicalKey, tc.want)
			}
		})
	}
}

// TestSpriteAnimationConsumesNoDraws locks that the die/reclaim branch is
// stream-silent: [01 §7.5]'s feature-phase rows give the CRT stream only the
// burning smoke and the simulation stream only reproduction and burn spread.
func TestSpriteAnimationConsumesNoDraws(t *testing.T) {
	svc, _, _ := dieAnimationService(t, featureAnimSelectorDie, 2)
	crt := rng.CRTFromState(11)
	sim := rng.SimulationFromState(11)
	svc.Crt = &crt
	svc.Sim = &sim
	for tick := uint32(0); tick < 3; tick++ {
		svc.TickLifecycle(tick)
	}
	if crt.Draws() != 0 || sim.Draws() != 0 {
		t.Fatalf("sprite die animation consumed %d CRT and %d simulation draws, want none [01 §7.5]", crt.Draws(), sim.Draws())
	}
}

// TestRestingSpriteIsNotAnAnimationRecord guards the discriminator. Retail
// attaches an active-list slot only for an event animation; a sprite feature
// at rest is driven by its catalog record's rest cursor, which owns no
// instance [05 R-FEAT-01 §10] pass 1. Nanolathe attaches an Instance to every
// stamped anchor, so a resting feature must never take the die/reclaim branch
// — otherwise every tree on the map would replace itself.
func TestRestingSpriteIsNotAnAnimationRecord(t *testing.T) {
	terrain := newEmptyTerrain(4, 4)
	def := featureDef("restingTree", 0, 0, 10)
	def.FeatureDeadDef = featureDef("stump", 0, 0, 10)
	svc := NewService(terrain, nil, nil, nil)
	if svc.spawnFeatureAt(2, 2, def) == nil {
		t.Fatal("spawn rejected")
	}
	inst := svc.InstanceAt(2, 2)
	// A length is present but the record is not an animation: nothing advances.
	inst.BurnDuration = 1
	for tick := uint32(0); tick < 5; tick++ {
		svc.TickLifecycle(tick)
	}
	if svc.InstanceAt(2, 2) != inst {
		t.Fatal("a resting sprite feature was replaced by the die/reclaim branch [05 R-FEAT-01 §10 pass 1]")
	}
	if inst.BurnTicks != 0 {
		t.Fatalf("a resting sprite feature advanced a cursor %d times [05 R-FEAT-01 §10 pass 1]", inst.BurnTicks)
	}
}

// transitionService builds a sprite feature that names both event sequences,
// with the death/reclaim length seam bound, so the transition of
// [05 R-FEAT-01 §5] can take steps 4-5 instead of step 3.
func transitionService(t *testing.T, visits int32, namesSequences bool) (*Service, *content.FeatureDef) {
	t.Helper()
	terrain := newEmptyTerrain(4, 4)
	dead := featureDef("stumpdead", 0, 0, 10)
	reclamate := featureDef("smudgereclaim", 0, 0, 10)
	src := featureDef("standingtree", 0, 0, 10)
	src.Reclaimable = true
	if namesSequences {
		src.SeqNameDie = "die"
		src.SeqNameReclamate = "reclamate"
	}
	src.FeatureDeadDef = dead
	src.FeatureReclamateDef = reclamate
	terrain.FeatureDefs = []*content.FeatureDef{src, dead, reclamate}
	svc := NewService(terrain, nil, nil, nil)
	if visits > 0 {
		svc.SetAnimationTicks(func(def *content.FeatureDef, selector uint8) int32 { return visits })
	}
	if svc.spawnFeatureAt(1, 1, src) == nil {
		t.Fatal("spawn rejected")
	}
	return svc, src
}

// TestReclaimRemovalPlaysTheReclaimSequenceThenReplaces locks [05 R-FEAT-01 §5]
// steps 4-5 on the reclaim cause: a definition naming `seqnamereclamate` does
// not replace at once, it attaches the animation record with the
// reclaim-animation bit set, and the feature phase replaces it with
// `featurereclamate` when the sequence ends (§10 pass 3).
func TestReclaimRemovalPlaysTheReclaimSequenceThenReplaces(t *testing.T) {
	const visits = 3
	svc, src := transitionService(t, visits, true)
	before := svc.InstanceAt(1, 1)
	svc.RemoveFeatureAt(1, 1, CauseReclaim)
	inst := svc.InstanceAt(1, 1)
	if inst == nil || inst != before {
		t.Fatal("the reclaim transition replaced instead of attaching the sequence [05 R-FEAT-01 §5 step 5]")
	}
	if inst.Def != src {
		t.Fatalf("the anchor holds %v, want the source definition while its sequence plays", inst.Def)
	}
	if !inst.IsAnimating || inst.IsBurning || inst.AnimationSelector != featureAnimSelectorReclaim {
		t.Fatalf("attached record animating=%v burning=%v selector=%d, want an animating reclaim record", inst.IsAnimating, inst.IsBurning, inst.AnimationSelector)
	}
	for visit := uint32(1); visit < visits; visit++ {
		svc.TickLifecycle(visit)
		if svc.InstanceAt(1, 1) != inst {
			t.Fatalf("visit %d replaced before the sequence ended", visit)
		}
	}
	svc.TickLifecycle(visits)
	got := svc.InstanceAt(1, 1)
	if got == nil || got.Def == nil || string(got.Def.CanonicalKey) != "smudgereclaim" {
		t.Fatalf("successor %v, want smudgereclaim [05 R-FEAT-01 §5 step 6]", got)
	}
}

// TestRemovalWithoutSequenceReplacesImmediately is step 3: a definition naming
// neither event sequence replaces at once, which is what every removal did
// before the animation records existed.
func TestRemovalWithoutSequenceReplacesImmediately(t *testing.T) {
	svc, _ := transitionService(t, 3, false)
	svc.RemoveFeatureAt(1, 1, CauseReclaim)
	got := svc.InstanceAt(1, 1)
	if got == nil || got.Def == nil || string(got.Def.CanonicalKey) != "smudgereclaim" {
		t.Fatalf("successor %v, want an immediate smudgereclaim [05 R-FEAT-01 §5 step 3]", got)
	}
}

// TestDeathRemovalPlaysTheDeathSequence is the same transition on the other
// cause: `seqnamedie` attaches a death record, which retires to `featuredead`.
func TestDeathRemovalPlaysTheDeathSequence(t *testing.T) {
	const visits = 2
	svc, _ := transitionService(t, visits, true)
	svc.RemoveFeatureAt(1, 1, CauseDead)
	inst := svc.InstanceAt(1, 1)
	if inst == nil || !inst.IsAnimating || inst.AnimationSelector != featureAnimSelectorDie {
		t.Fatalf("death transition attached %#v, want an animating death record [05 R-FEAT-01 §5 step 5]", inst)
	}
	for visit := uint32(1); visit <= visits; visit++ {
		svc.TickLifecycle(visit)
	}
	got := svc.InstanceAt(1, 1)
	if got == nil || got.Def == nil || string(got.Def.CanonicalKey) != "stumpdead" {
		t.Fatalf("successor %v, want stumpdead", got)
	}
}

// TestUnboundAnimationLengthReplacesImmediately locks the seam's absence
// behaviour: with no length source a sequence cannot be run, so the transition
// falls back to step 3 rather than attaching a record that could never finish.
func TestUnboundAnimationLengthReplacesImmediately(t *testing.T) {
	svc, _ := transitionService(t, 0, true) // sequences named, no length seam
	svc.RemoveFeatureAt(1, 1, CauseDead)
	got := svc.InstanceAt(1, 1)
	if got == nil || got.Def == nil || string(got.Def.CanonicalKey) != "stumpdead" {
		t.Fatalf("successor %v, want an immediate stumpdead when no length is known", got)
	}
}

// TestReclaimPayoutRefusesWhileAnimationRuns locks the same-tick precedence of
// [05 R-FEAT-01 §5]: a cell already playing an event animation is inert to
// every further cause, the reclaim payout included [05 R-FEAT-01 §15]. Without
// it one tree would pay out once per visit of its own reclaim animation.
func TestReclaimPayoutRefusesWhileAnimationRuns(t *testing.T) {
	svc, src := transitionService(t, 5, true)
	src.Metal, src.Energy = 100, 250
	inst := svc.InstanceAt(1, 1)
	metal, energy := svc.Reclaim(nil, inst, 0)
	if metal != 100 || energy != 250 {
		t.Fatalf("first payout (%v, %v), want the full pools", metal, energy)
	}
	if !inst.IsAnimating {
		t.Fatal("the payout did not attach the reclaim animation [05 R-FEAT-01 §5 step 5]")
	}
	if metal, energy := svc.Reclaim(nil, inst, 0); metal != 0 || energy != 0 {
		t.Fatalf("second payout (%v, %v) while the animation ran, want none [05 R-FEAT-01 §15]", metal, energy)
	}
}

// TestBurnSmokeEmitsAtTheFootprintCentre locks pass 3a's base point: the puff
// goes to the footprint centre at the sampled terrain height, the same centre
// the burn weapon fires at, `((footprintx + 2x)·8, (footprintz + 2z)·8)`
// [05 R-FEAT-01 §11 step 3].
func TestBurnSmokeEmitsAtTheFootprintCentre(t *testing.T) {
	crt := rng.CRTFromState(3)
	sim := rng.SimulationFromState(3)
	svc := burningFeatureService(t, &crt, &sim, [2]int{3, 4})
	var got [][3]numeric.Fixed
	svc.BurnSmoke = func(pos [3]numeric.Fixed) { got = append(got, pos) }

	svc.TickLifecycle(0) // gated tick: one puff
	svc.TickLifecycle(1) // ungated: none
	if len(got) != 1 {
		t.Fatalf("%d puffs over one gated tick and one ungated, want 1 [05 R-FEAT-01 §10 pass 3a]", len(got))
	}
	wantX := numeric.FixedFromInt(3*16 + 8)
	wantZ := numeric.FixedFromInt(4*16 + 8)
	wantY := svc.Terrain.CoarseHeightAt(3, 4)
	if got[0][0] != wantX || got[0][2] != wantZ || got[0][1] != wantY {
		t.Fatalf("puff at %v, want the footprint centre (%v, %v, %v) [05 R-FEAT-01 §11 step 3]", got[0], wantX, wantY, wantZ)
	}
}

// TestBurnSmokeJitterMovesWorldXAndHeight encodes an INFERENCE, not a traced
// contract: [05 R-FEAT-01 §10] pass 3a names the two jittered words `x` and
// `y`, and the smoke-puff family's own position triple has `y` as the vertical
// word (its per-tick update adds the authored gravity to `y` and the wind to
// `x` and `z`, [03 §5.5 "Smoke-puff family"]), so the addends land on world X
// and world HEIGHT and the puff's Z stays at the footprint centre. The factor
// of two on the y term agrees with the projection's half-height shear
// [03 §2.5]. A trace of the producer site would settle it; until then this
// test locks the reading, not a fact.
func TestBurnSmokeJitterMovesWorldXAndHeightInference(t *testing.T) {
	const seed = 0x51ee7
	frame := burnFrameGeometry{W: 20, H: 12, XOff: 7, YOff: 5}

	crtA := rng.CRTFromState(seed)
	simA := rng.SimulationFromState(1)
	plain := burningFeatureService(t, &crtA, &simA, [2]int{3, 4})
	var gotPlain [][3]numeric.Fixed
	plain.BurnSmoke = func(pos [3]numeric.Fixed) { gotPlain = append(gotPlain, pos) }
	plain.TickLifecycle(0)

	crtB := rng.CRTFromState(seed)
	simB := rng.SimulationFromState(1)
	jittered := burningFeatureService(t, &crtB, &simB, [2]int{3, 4})
	jittered.BurnFrameGeometry = func(*content.FeatureDef, int32) (int32, int32, int32, int32) {
		return frame.W, frame.H, frame.XOff, frame.YOff
	}
	var gotJitter [][3]numeric.Fixed
	jittered.BurnSmoke = func(pos [3]numeric.Fixed) { gotJitter = append(gotJitter, pos) }
	jittered.TickLifecycle(0)

	if len(gotPlain) != 1 || len(gotJitter) != 1 {
		t.Fatalf("puff counts %d and %d, want one each", len(gotPlain), len(gotJitter))
	}
	ref := rng.CRTFromState(seed)
	wantDX, wantDY := burnSmokeJitter(frame, ref.Rand(), ref.Rand())
	if wantDX == 0 && wantDY == 0 {
		t.Fatal("the fixture's draws produce no offset; pick another seed")
	}
	if got := gotJitter[0][0] - gotPlain[0][0]; got != numeric.FixedFromInt(int64(wantDX)) {
		t.Fatalf("world X moved by %v, want %d whole units", got, wantDX)
	}
	if got := gotJitter[0][1] - gotPlain[0][1]; got != numeric.FixedFromInt(int64(wantDY)) {
		t.Fatalf("world height moved by %v, want %d whole units", got, wantDY)
	}
	if gotJitter[0][2] != gotPlain[0][2] {
		t.Fatalf("world Z moved to %v; pass 3a jitters two words, not three", gotJitter[0][2])
	}
}

// TestBurnSmokeDrawsThreeCRTWithTheGeometrySeamBound repeats the draw budget
// with both seams bound: the geometry seam must not change what the emission
// takes from the CRT stream, because the jitter draws happen before any seam is
// consulted and the third is the producer's own [05 R-FEAT-01 §16].
func TestBurnSmokeDrawsThreeCRTWithTheGeometrySeamBound(t *testing.T) {
	crt := rng.CRTFromState(0x1234567)
	sim := rng.SimulationFromState(99)
	svc := burningFeatureService(t, &crt, &sim, [2]int{1, 1}, [2]int{5, 3})
	svc.BurnFrameGeometry = func(*content.FeatureDef, int32) (int32, int32, int32, int32) {
		return 16, 24, 3, 9
	}
	puffs := 0
	svc.BurnSmoke = func([3]numeric.Fixed) {
		puffs++
		crt.Rand() // the container's last-frame draw [03 R-STRIP-01 §2]
	}
	for tick := uint32(0); tick < 7; tick++ {
		before := crt.Draws()
		svc.TickLifecycle(tick)
		want := uint64(0)
		if tick%3 == 0 {
			want = 6 // two burning instances, three draws each
		}
		if got := crt.Draws() - before; got != want {
			t.Fatalf("tick %d consumed %d CRT draws with the seams bound, want %d [05 R-FEAT-01 §16]", tick, got, want)
		}
	}
	if puffs != 6 { // three gated ticks (0, 3, 6) x two instances
		t.Fatalf("%d puffs, want 6 [05 R-FEAT-01 §10 pass 3a]", puffs)
	}
}

// authoredSequence is a test stand-in for one GAF entry: the per-frame delays
// the client's resolver reads and the geometry pass 3a scales by. The visit
// cadence is [05 R-FEAT-01 §10]'s — each frame holds for max(delay, 1) visits.
type authoredSequence struct {
	delays []int32
	frames []burnFrameGeometry
}

func (a authoredSequence) visits() int32 {
	total := int32(0)
	for _, d := range a.delays {
		if d < 1 {
			d = 1
		}
		total += d
	}
	return total
}

func (a authoredSequence) at(visit int32) burnFrameGeometry {
	elapsed := int32(0)
	for i, d := range a.delays {
		if d < 1 {
			d = 1
		}
		elapsed += d
		if visit < elapsed {
			return a.frames[i]
		}
	}
	return a.frames[len(a.frames)-1]
}

// TestDeathAnimationRetiresAfterTheAuthoredVisitTotal drives the transition
// with a length that comes from authored frame delays rather than a round
// number: delays 4, 0, 1 hold for 4 + 1 + 1 = 6 visits [05 R-FEAT-01 §10], and
// the feature's successor is stamped on the sixth, not the fifth or seventh.
func TestDeathAnimationRetiresAfterTheAuthoredVisitTotal(t *testing.T) {
	seq := authoredSequence{
		delays: []int32{4, 0, 1},
		frames: []burnFrameGeometry{{W: 20, H: 12, XOff: 7, YOff: 5}, {W: 5, H: 7, XOff: 2, YOff: 3}, {W: 16, H: 24, XOff: -3, YOff: 9}},
	}
	if got := seq.visits(); got != 6 {
		t.Fatalf("fixture lifetime %d, want 6", got)
	}
	svc, _ := transitionService(t, 0, true) // sequences named, seam bound below
	svc.SetAnimationTicks(func(def *content.FeatureDef, selector uint8) int32 {
		if selector != featureAnimSelectorDie {
			return 0
		}
		return seq.visits()
	})
	svc.RemoveFeatureAt(1, 1, CauseDead)
	inst := svc.InstanceAt(1, 1)
	if inst == nil || !inst.IsAnimating {
		t.Fatalf("death transition attached %#v, want an animating record", inst)
	}
	for visit := int32(1); visit < seq.visits(); visit++ {
		svc.TickLifecycle(uint32(visit))
		if svc.InstanceAt(1, 1) != inst {
			t.Fatalf("visit %d of %d replaced early [05 R-FEAT-01 §10 pass 3]", visit, seq.visits())
		}
	}
	svc.TickLifecycle(uint32(seq.visits()))
	got := svc.InstanceAt(1, 1)
	if got == nil || got.Def == nil || string(got.Def.CanonicalKey) != "stumpdead" {
		t.Fatalf("successor %v after %d visits, want stumpdead", got, seq.visits())
	}
}

// TestBurnSmokeWithAuthoredGeometryKeepsTheDrawBudget runs the jitter against a
// sequence whose geometry CHANGES as the burn cursor advances — the resolver's
// job — and locks that the CRT budget is unaffected: three draws per burning
// instance on every third tick and none on any other [05 R-FEAT-01 §10 pass
// 3a][05 R-FEAT-01 §16].
func TestBurnSmokeWithAuthoredGeometryKeepsTheDrawBudget(t *testing.T) {
	seq := authoredSequence{
		delays: []int32{3, 0, 2},
		frames: []burnFrameGeometry{{W: 20, H: 12, XOff: 7, YOff: 5}, {W: 5, H: 7, XOff: 2, YOff: 3}, {W: 16, H: 24, XOff: -3, YOff: 9}},
	}
	crt := rng.CRTFromState(0xabcdef)
	sim := rng.SimulationFromState(5)
	svc := burningFeatureService(t, &crt, &sim, [2]int{2, 2})
	svc.BurnFrameGeometry = func(_ *content.FeatureDef, visit int32) (int32, int32, int32, int32) {
		f := seq.at(visit)
		return f.W, f.H, f.XOff, f.YOff
	}
	var puffs [][3]numeric.Fixed
	svc.BurnSmoke = func(pos [3]numeric.Fixed) {
		puffs = append(puffs, pos)
		crt.Rand() // the container's last-frame draw [03 R-STRIP-01 §2]
	}

	ref := rng.CRTFromState(0xabcdef)
	baseX := numeric.FixedFromInt(2*16 + 8)
	baseY := svc.Terrain.CoarseHeightAt(2, 2)
	baseZ := numeric.FixedFromInt(2*16 + 8)
	for tick := uint32(0); tick < 9; tick++ {
		before := crt.Draws()
		visit := int32(tick) // BurnTicks is the count of completed visits
		svc.TickLifecycle(tick)
		want := uint64(0)
		if tick%3 == 0 {
			want = 3
		}
		if got := crt.Draws() - before; got != want {
			t.Fatalf("tick %d consumed %d CRT draws with real geometry, want %d [05 R-FEAT-01 §16]", tick, got, want)
		}
		if want == 0 {
			continue
		}
		dx, dy := burnSmokeJitter(seq.at(visit), ref.Rand(), ref.Rand())
		ref.Rand() // the producer's draw keeps the reference stream aligned
		last := puffs[len(puffs)-1]
		wantPos := [3]numeric.Fixed{baseX.Add(numeric.FixedFromInt(int64(dx))), baseY.Add(numeric.FixedFromInt(int64(dy))), baseZ}
		if last != wantPos {
			t.Fatalf("tick %d puff at %v, want %v (frame %v)", tick, last, wantPos, seq.at(visit))
		}
	}
	if len(puffs) != 3 { // ticks 0, 3, 6
		t.Fatalf("%d puffs over nine ticks, want 3", len(puffs))
	}
}

// TestReclaimAtPaysOutAndPlaysTheReclaimSequence locks the payout entry the
// order executor calls [05 R-WORK-01 §5]: the credit is the definition's own
// metal and energy pools, unchanged from the terrain-only transition it
// replaces, and the cell goes through [05 R-FEAT-01 §5] — a definition naming
// `seqnamereclamate` plays it out and the successor is stamped when the
// sequence ends, one naming none replaces at once.
func TestReclaimAtPaysOutAndPlaysTheReclaimSequence(t *testing.T) {
	const visits = 4
	for _, tc := range []struct {
		name       string
		sequence   bool
		wantLive   bool
		wantDefKey string
	}{
		{name: "with a reclaim sequence the feature plays it", sequence: true, wantLive: true, wantDefKey: "standingtree"},
		{name: "without one it replaces at once", sequence: false, wantLive: false, wantDefKey: "smudgereclaim"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, src := transitionService(t, visits, tc.sequence)
			src.Metal, src.Energy = 100, 250
			metal, energy, ok := svc.ReclaimAt(1, 1)
			if !ok || metal != 100 || energy != 250 {
				t.Fatalf("payout (%v, %v, %v), want the full pools [05 R-WORK-01 §5]", metal, energy, ok)
			}
			got := svc.InstanceAt(1, 1)
			if got == nil || got.Def == nil || string(got.Def.CanonicalKey) != tc.wantDefKey {
				t.Fatalf("anchor holds %v, want %q", got, tc.wantDefKey)
			}
			if got.IsAnimating != tc.wantLive {
				t.Fatalf("animating=%v, want %v", got.IsAnimating, tc.wantLive)
			}
			if !tc.wantLive {
				return
			}
			// The successor lands when the sequence ends, not before.
			for visit := uint32(1); visit < visits; visit++ {
				svc.TickLifecycle(visit)
			}
			if svc.InstanceAt(1, 1) != got {
				t.Fatal("the successor was stamped before the sequence ended")
			}
			svc.TickLifecycle(visits)
			final := svc.InstanceAt(1, 1)
			if final == nil || final.Def == nil || string(final.Def.CanonicalKey) != "smudgereclaim" {
				t.Fatalf("successor %v, want smudgereclaim [05 R-FEAT-01 §5 step 6]", final)
			}
		})
	}
}

// TestReclaimAtRefusesTwice locks the payout guard [05 R-FEAT-01 §15]: a cell
// already playing its reclaim sequence pays nothing more, so a second visit of
// the executor cannot double-credit while the animation runs.
func TestReclaimAtRefusesTwice(t *testing.T) {
	svc, src := transitionService(t, 4, true)
	src.Metal, src.Energy = 60, 90
	if _, _, ok := svc.ReclaimAt(1, 1); !ok {
		t.Fatal("first payout refused")
	}
	if metal, energy, ok := svc.ReclaimAt(1, 1); ok || metal != 0 || energy != 0 {
		t.Fatalf("second payout (%v, %v, %v) while the sequence ran, want none", metal, energy, ok)
	}
}

// TestSparkCountdownHalvesTheCompiledTicks locks the correction of
// [05 R-FEAT-01 §9] step 4: the compiled `sparktime` field already holds
// seconds × 30 truncated, so the countdown halves THAT — the shipped 5 stores
// 150 and gives 75..149 visits, not the 2 or 3 the superseded text claimed.
func TestSparkCountdownHalvesTheCompiledTicks(t *testing.T) {
	for seed := uint32(1); seed <= 24; seed++ {
		terrain := newEmptyTerrain(4, 4)
		def := featureDef("torchtree", 0, 0, 10)
		def.Flamable = true
		def.SeqNameBurn = "burn"
		def.SparkTime = 150 // the shipped 5 seconds, compiled to ticks
		sim := rng.SimulationFromState(seed)
		svc := NewService(terrain, &sim, nil, nil)
		if svc.spawnFeatureAt(1, 1, def) == nil {
			t.Fatal("spawn rejected")
		}
		before := sim.Draws()
		if !svc.Ignite(1, 1, 1, 5) {
			t.Fatalf("seed %d: ignition refused", seed)
		}
		if got := sim.Draws() - before; got != 1 {
			t.Fatalf("seed %d: ignition consumed %d draws, want one", seed, got)
		}
		inst := svc.InstanceAt(1, 1)
		if inst == nil || !inst.IsBurning {
			t.Fatalf("seed %d: not burning", seed)
		}
		if inst.BurnCountdown < 75 || inst.BurnCountdown > 149 {
			t.Fatalf("seed %d: countdown %d, want 75..149 [05 R-FEAT-01 §9]", seed, inst.BurnCountdown)
		}
	}
}
