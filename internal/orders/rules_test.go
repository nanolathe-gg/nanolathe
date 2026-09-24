package orders

import (
	"fmt"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// modeRules selects the rule set the central gameplay mode selects, for the
// policy tests that run both branches.
func modeRules(modern bool) Rules {
	if modern {
		return &ModernRules{}
	}
	return StrictRules{}
}

// ruleCalls is every decision on the seam, named as the interface names it.
// The node is an accepted bombing pass so that the bomber-leash decision
// reaches its phase comparison rather than stopping at a missing record.
func ruleCalls(u *units.Unit, n *Node, sink *bool) []struct {
	name string
	call func()
} {
	return []struct {
		name string
		call func()
	}{
		{"HoldsFire", func() { *sink = rulesOfUnit(u).HoldsFire(u) }},
		{"DeferBomberLeash", func() { *sink = rulesOfUnit(u).DeferBomberLeash(u, n) }},
		{"GuardSeeksPad", func() { *sink = rulesOfUnit(u).GuardSeeksPad(u, n, 100) }},
		{"GuardWorksNearby", func() { *sink = rulesOfUnit(u).GuardWorksNearby(u, n, 100) }},
		{"GuardResumesFromPad", func() { *sink = rulesOfUnit(u).GuardResumesFromPad(u) }},
		{"UnreachableMoveArrival", func() { *sink = rulesOfUnit(u).UnreachableMoveArrival(u, n) }},
		{"ScriptAttackSurfaceFire", func() {
			*sink = rulesOfUnit(u).ScriptAttackSurfaceFire(ScriptAttackSurfaceFireRequest{Binding: bindingOfUnit(u), Actor: u})
		}},
	}
}

// A binding composed without a rule set answers as Strict 3.1, so a fixture or
// a reconstructed queue that never set the field runs the retail path and
// draws no randomness.
func TestAbsentRulesAnswerStrictWithoutDrawing(t *testing.T) {
	q, u := gateFixture()
	q.Binding().Rules = nil
	if _, strict := q.Binding().rules().(StrictRules); !strict {
		t.Fatal("absent rule set did not answer as Strict 3.1")
	}
	if _, strict := (*QueueBinding)(nil).rules().(StrictRules); !strict {
		t.Fatal("absent binding did not answer as Strict 3.1")
	}
	n := &Node{ID: Lookup("AirStrike"), Owner: u.Handle, Phase: 2}
	random := *q.Binding().SimRNG
	var answer bool
	for _, c := range ruleCalls(u, n, &answer) {
		c.call()
		if answer {
			t.Fatalf("Strict %s did not answer the retail way", c.name)
		}
	}
	if *q.Binding().SimRNG != random {
		t.Fatal("Strict rule dispatch drew randomness")
	}
}

// The seam must cost nothing per call: Strict is zero-size and Modern is used
// by pointer, so neither the accessor's Strict fallback nor a Modern dispatch
// may allocate [docs/INVARIANTS.md I11]. The Modern guard decisions are
// measured on their own bypass — the position the mode projection used to
// occupy — which is the step this seam replaced.
func TestRuleDispatchDoesNotAllocate(t *testing.T) {
	q, u := gateFixture()
	n := &Node{ID: Lookup("AirStrike"), Owner: u.Handle, Phase: 2}
	var answer bool
	for _, modern := range []bool{false, true} {
		q.Binding().Rules = nil
		if modern {
			q.Binding().Rules = &ModernRules{}
		}
		for _, c := range ruleCalls(u, n, &answer) {
			t.Run(fmt.Sprintf("modern=%v/%s", modern, c.name), func(t *testing.T) {
				allocs := testing.AllocsPerRun(200, c.call)
				t.Logf("%s allocations per call: %v", c.name, allocs)
				if allocs != 0 {
					t.Fatalf("%s allocated %v times per call", c.name, allocs)
				}
			})
		}
	}
}
