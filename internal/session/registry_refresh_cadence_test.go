package session

import (
	"testing"
)

// The per-side target registry rebuild and the 30-tick strategic refresh are
// ONE retail routine on one object per player slot, reached from the per-player
// phase through a single cadence gate whose bound-30 draw is the routine's one
// draw [06 §3.1][08 R-AI-01 §16]. This build splits the routine across
// internal/combat (the candidate lists and the secondary-list gate) and
// internal/ai (the census, the centroid and the draw), so the contract that has
// to survive the split is that the two halves are rebuilt on the SAME TICK for
// every slot. Before WU-19-126 each half kept its own cadence word, reached
// from a different phase, and they could drift apart by up to thirty ticks.
//
// The gate is also null-checked on the slot's strategic state, "never
// controller-checked", and that state exists for every non-remote slot, "human
// slots included" — so the human slot's registry is rebuilt on exactly the
// ticks the computer slot's is [06 §3.1 "Which slots draw"].
func TestRegistryAndStrategicRefreshShareOneCadence(t *testing.T) {
	sess := aiE2ESkirmish(t, "ashap plateau", aiE2ESeed)
	if sess.Combat == nil {
		t.Fatal("the skirmish composed without a combat service")
	}

	// Ten dues is enough to catch a drift that only opens after the first one.
	const ticks = 10 * 30
	scaled := sess.Clock.ScaledAnchor
	seen := 0
	for sess.Clock.GlobalTick < ticks {
		scaled += 5
		sess.Step(scaled)
		tick := sess.Clock.GlobalTick
		for slot := 0; slot < 10; slot++ {
			mgr := sess.AI[slot]
			if mgr == nil {
				// No strategic state: the gate is null-checked, so this slot
				// neither refreshes nor rebuilds [06 §3.1].
				if got := sess.Combat.TargetRegistryRebuildTick(uint8(slot)); got != 0 {
					t.Fatalf("slot %d has no strategic state but its registry rebuilt at tick %d", slot, got)
				}
				continue
			}
			refresh := mgr.Strategic.LastRefreshTick
			rebuild := sess.Combat.TargetRegistryRebuildTick(uint8(slot))
			if refresh != rebuild {
				t.Fatalf("slot %d drifted at tick %d: the census half last ran at %d and the candidate-list half at %d; one routine, one gate [06 §3.1]",
					slot, tick, refresh, rebuild)
			}
			if refresh != 0 {
				if refresh%30 != 0 {
					t.Fatalf("slot %d rebuilt at tick %d, which is not on the 30-tick cadence [06 §3.1]", slot, refresh)
				}
				seen++
			}
		}
	}
	if seen == 0 {
		t.Fatal("no slot rebuilt at all inside ten dues; the fixture proves nothing")
	}

	// Both battle slots must have run, and the human's on the same cadence as
	// the computer's: "the local human's slot draws on the same 30-tick cadence
	// as a computer slot" [06 §3.1 "Which slots draw"].
	human, computer := sess.Combat.TargetRegistryRebuildTick(0), sess.Combat.TargetRegistryRebuildTick(1)
	if human == 0 || computer == 0 {
		t.Fatalf("human slot rebuilt at %d and computer slot at %d; both non-remote slots rebuild [06 §3.1]", human, computer)
	}
	if human != computer {
		t.Fatalf("human slot rebuilt at %d and computer slot at %d; both are visited in the same per-player phase every thirty ticks", human, computer)
	}
}

// One due, one draw. The routine takes "exactly one simulation draw (bound 30)"
// per side per rebuild [06 §3.1], so a two-slot battle takes two draws per
// thirty ticks and no more — which is what stops the split from consuming the
// same retail draw twice (I4).
func TestRegistryRebuildTakesOneDrawPerSlotPerDue(t *testing.T) {
	sess := aiE2ESkirmish(t, "ashap plateau", aiE2ESeed)
	if sess.SimRNG() == nil {
		t.Fatal("the skirmish composed without a simulation stream")
	}

	slots := 0
	for slot := 0; slot < 10; slot++ {
		if sess.AI[slot] != nil {
			slots++
		}
	}
	if slots == 0 {
		t.Fatal("no slot carries strategic state; the fixture proves nothing")
	}

	// Walk from one due to the next and read the stream across the window. The
	// registry routine is not the only consumer of the stream, so the assertion
	// is a floor and a ceiling on ITS contribution: over a window in which
	// every slot rebuilds exactly once, the routine adds exactly one draw per
	// slot, and the whole tick's draws can never be fewer than that.
	scaled := sess.Clock.ScaledAnchor
	// One scaled unit at a time so the walk lands exactly on the boundary
	// ticks; a coarser step would overshoot the window under test.
	step := func(to uint32) {
		for sess.Clock.GlobalTick < to {
			scaled++
			sess.Step(scaled)
		}
		if sess.Clock.GlobalTick != to {
			t.Fatalf("stepped past tick %d to %d", to, sess.Clock.GlobalTick)
		}
	}
	step(30)
	rebuiltAt30 := 0
	for slot := 0; slot < 10; slot++ {
		if sess.Combat.TargetRegistryRebuildTick(uint8(slot)) == 30 {
			rebuiltAt30++
		}
	}
	if rebuiltAt30 != slots {
		t.Fatalf("%d of %d slots with strategic state rebuilt at tick 30, want every one: the first rebuild of every such slot fires at tick 30 [06 §3.1]", rebuiltAt30, slots)
	}

	// Ticks 31..59 are inside the window: no slot may rebuild again.
	before := sess.SimRNG().Draws()
	step(59)
	for slot := 0; slot < 10; slot++ {
		if got := sess.Combat.TargetRegistryRebuildTick(uint8(slot)); got != 0 && got != 30 {
			t.Fatalf("slot %d rebuilt again at tick %d inside the thirty-tick window [06 §3.1]", slot, got)
		}
	}
	if after := sess.SimRNG().Draws(); after < before {
		t.Fatalf("the simulation stream went backwards: %d then %d", before, after)
	}

	// Tick 60 is the next due: every slot rebuilds once more, and the stream
	// advances by at least one draw per slot over the tick that carries them.
	beforeDue := sess.SimRNG().Draws()
	step(60)
	afterDue := sess.SimRNG().Draws()
	for slot := 0; slot < 10; slot++ {
		if sess.AI[slot] == nil {
			continue
		}
		if got := sess.Combat.TargetRegistryRebuildTick(uint8(slot)); got != 60 {
			t.Fatalf("slot %d last rebuilt at tick %d, want the due at 60 [06 §3.1]", slot, got)
		}
	}
	if afterDue-beforeDue < uint64(slots) {
		t.Fatalf("tick 60 consumed %d draws for %d rebuilding slots; each rebuild takes its own bound-30 draw (I4)[06 §3.1]", afterDue-beforeDue, slots)
	}
}
