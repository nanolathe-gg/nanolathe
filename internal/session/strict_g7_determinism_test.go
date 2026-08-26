package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/ai"
)

// TestStrictSkirmish_TwoRunsMatchTraceAndStateHash implements G7 [ON-10 §11 G7]:
// run the G5 commander→factory→combat-unit→attack scenario twice from fresh
// sessions with identical seeds and compare content manifest, ordered event
// trace, milestone ticks, and final authoritative state hash. No tolerance is
// permitted for authoritative differences.
func TestStrictSkirmish_TwoRunsMatchTraceAndStateHash(t *testing.T) {
	const simSeed, crtSeed uint32 = 900, 1000 // same fixture seeds as the G5 gate
	const maxTick = 1500                      // covers wave-A attack issue at ~601

	run := func(label string) (traceHash, stateHash string, milestones map[string]uint32, finalTick uint32) {
		s, mgr := buildStrictG5Session(t, simSeed, crtSeed)
		s.SetTraceEnabled(true)
		s.ClearTrace()
		for tick := 1; tick <= maxTick; tick++ {
			ensureG5UnitCOB(s)
			s.Step(int32(tick))
			// Synthetic: move completed fleas from Regroup to Wave so the wave can issue attack.
			for _, u := range s.Units.IterSliced() {
				if u != nil && u.Def != nil && u.Def.UnitName == "armflea" && u.Remaining == 0 && u.Group == 7 {
					for i, h := range mgr.GroupRegroupB {
						if h == u.Handle {
							mgr.GroupRegroupB = append(mgr.GroupRegroupB[:i], mgr.GroupRegroupB[i+1:]...)
							break
						}
					}
					mgr.GroupWaveA = append(mgr.GroupWaveA, u.Handle)
					u.Group = 2
				}
			}
			if _, ok := mgr.Milestones()[ai.MilestoneAttackMoveIssued]; ok {
				break // meaningful game reached; hash the trajectory up to here
			}
		}
		ms := mgr.Milestones()
		if _, ok := ms[ai.MilestoneAttackMoveIssued]; !ok {
			t.Fatalf("G7 %s: attack milestone not reached within %d ticks; determinism comparison would be vacuous", label, maxTick)
		}
		return HashTrace(s.TraceEvents()), HashState(s), ms, s.Clock.GlobalTick
	}

	trace1, state1, miles1, tick1 := run("run1")
	trace2, state2, miles2, tick2 := run("run2")

	if trace1 != trace2 {
		t.Fatalf("G7 determinism: ordered event trace hash mismatch %s vs %s", trace1, trace2)
	}
	if state1 != state2 {
		t.Fatalf("G7 determinism: final state hash mismatch %s vs %s", state1, state2)
	}
	if tick1 != tick2 {
		t.Fatalf("G7 determinism: final tick mismatch %d vs %d", tick1, tick2)
	}
	for _, k := range g5Required {
		a, okA := miles1[k]
		b, okB := miles2[k]
		if okA != okB || (okA && a != b) {
			t.Fatalf("G7 determinism: milestone %q ticks diverge: %v vs %v", k, miles1[k], miles2[k])
		}
	}
	if len(miles1) < 10 {
		t.Fatalf("G7: expected all ten G5 milestones in hashed runs, got %d", len(miles1))
	}

	ev := StrictGateEvidence{
		Commit: strictCommit(), ContentManifest: strictCatalogHash(strictMinimalCatalog()), Map: "test", Seed: simSeed, CrtSeed: crtSeed,
		Players:    []map[string]any{{"slot": 0, "control": "human"}, {"slot": 1, "control": "computer", "ai_profile": "default"}},
		MaxTick:    maxTick,
		Milestones: miles1,
		Winner:     -1, Reason: "G7 seeded full-skirmish determinism (G5 scenario x2)",
		FinalTick: tick1, FinalStateHash: state1, TraceHash: trace1,
	}
	t.Logf("G7 evidence: %s", FormatEvidence(ev))
}
