package features

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
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

// TestBurnSmokeDrawsTwoCRTPerBurningInstanceEveryThirdTick locks the draw
// budget of [05 R-FEAT-01 §10] pass 3a and its census row [01 §7.5] ("6
// features | CRT | 2 per fire-effect emission"): exactly two CRT draws per
// burning instance on a tick whose index is a multiple of three, none on any
// other tick, and none on the simulation stream.
func TestBurnSmokeDrawsTwoCRTPerBurningInstanceEveryThirdTick(t *testing.T) {
	crt := rng.CRTFromState(0x1234567)
	sim := rng.SimulationFromState(99)
	svc := burningFeatureService(t, &crt, &sim, [2]int{1, 1}, [2]int{5, 3})

	for tick := uint32(0); tick < 7; tick++ {
		beforeCRT := crt.Draws()
		beforeSim := sim.Draws()
		svc.TickLifecycle(tick)
		gotCRT := crt.Draws() - beforeCRT
		wantCRT := uint64(0)
		if tick%3 == 0 {
			wantCRT = 4 // two burning instances, two draws each
		}
		if gotCRT != wantCRT {
			t.Fatalf("tick %d consumed %d CRT draws, want %d [05 R-FEAT-01 §10 pass 3a]", tick, gotCRT, wantCRT)
		}
		if got := sim.Draws() - beforeSim; got != 0 {
			t.Fatalf("tick %d consumed %d simulation draws for smoke; the jitter is CRT-only [05 R-FEAT-01 §10 pass 3a]", tick, got)
		}
	}
}

// TestBurnSmokeDrawsAreConsecutiveOnTheCRTStream locks the ORDER as well as
// the count: the pair the site consumes is the next two values of the CRT
// stream, taken back to back, so a reference stream advanced twice lands on
// the same state [01 §7.2].
func TestBurnSmokeDrawsAreConsecutiveOnTheCRTStream(t *testing.T) {
	const seed = 0x2468ace
	crt := rng.CRTFromState(seed)
	sim := rng.SimulationFromState(7)
	svc := burningFeatureService(t, &crt, &sim, [2]int{2, 2})
	svc.TickLifecycle(0)

	ref := rng.CRTFromState(seed)
	ref.Rand()
	ref.Rand()
	if crt.State != ref.State {
		t.Fatalf("CRT state %#x after the smoke pair, want %#x — the two draws must be consecutive [05 R-FEAT-01 §10 pass 3a]", crt.State, ref.State)
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
