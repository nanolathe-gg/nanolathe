package features

import "testing"

// The presentation boundary has to tell the two feature draw cases apart: a
// cell with a live instance blits the INSTANCE's cursor frames, a cell with
// none blits the definition's rest cursor [03 R-RAST-01 §6]. This build
// attaches an Instance to every stamped anchor, so the discriminator is the
// animation record, and the sequence is chosen by the record's own selector
// [05 R-FEAT-01 §10] pass 3.
func TestEventSequenceReportsTheRecordsOwnSequence(t *testing.T) {
	svc, src := transitionService(t, 4, true)
	src.SeqNameReclamateShad = "reclamateshad"

	// At rest the accessor reports nothing, so the rest cursor stays in charge.
	inst := svc.InstanceAt(1, 1)
	if _, _, _, ok := inst.EventSequence(); ok {
		t.Fatal("a resting feature reported an event sequence")
	}

	if _, _, ok := svc.ReclaimAt(1, 1); !ok {
		t.Fatal("reclaim payout refused")
	}
	name, shadow, visit, ok := inst.EventSequence()
	if !ok || name != "reclamate" || shadow != "reclamateshad" || visit != 0 {
		t.Fatalf("reclaim record reports (%q, %q, %d, %v), want the reclaim pair at visit 0", name, shadow, visit, ok)
	}

	// The visit index is the cursor's own count, so the drawn frame follows the
	// simulation's cursor rather than a presentation-side timer.
	svc.TickLifecycle(1)
	if _, _, visit, _ := inst.EventSequence(); visit != 1 {
		t.Fatalf("after one feature-phase visit the cursor reports visit %d, want 1", visit)
	}

	// The death selector picks the other pair.
	svc2, src2 := transitionService(t, 4, true)
	src2.SeqNameDieShad = "dieshad"
	svc2.RemoveFeatureAt(1, 1, CauseDead)
	if name, shadow, _, ok := svc2.InstanceAt(1, 1).EventSequence(); !ok || name != "die" || shadow != "dieshad" {
		t.Fatalf("death record reports (%q, %q, %v), want the death pair", name, shadow, ok)
	}
}
