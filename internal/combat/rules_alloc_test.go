package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// rulesDispatchSink keeps each measured answer live so the dispatch below is
// not optimized away.
var rulesDispatchSink bool

// TestRulesDispatchDoesNotAllocate locks the allocation rule of the rules seam:
// asking a bound rule set costs one indirect call and no heap traffic, whether
// the service is unbound, Strict or Modern. The retail firing pipeline runs
// these questions per weapon slot per tick, so an allocating answer would show
// up as tick garbage rather than as a wrong result [I4].
//
// StrictRules is zero size, so converting a value to Rules never allocates; the
// unbound service answers through the same shared value.
func TestRulesDispatchDoesNotAllocate(t *testing.T) {
	var shooter units.Unit
	shooter.Flags &^= units.StandingFieldMask << units.StandingFireShift
	var query ShotQuery

	for _, tc := range []struct {
		name  string
		rules Rules
	}{
		{"unbound", nil},
		{"strict", StrictRules{}},
		{"modern", &ModernRules{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &Service{Rules: tc.rules}
			if got := testing.AllocsPerRun(100, func() {
				rulesDispatchSink = svc.rules().HoldsFire(&shooter)
			}); got != 0 {
				t.Fatalf("HoldsFire dispatch allocated %v per call", got)
			}
			if tc.name == "modern" {
				// The Modern preview's own allocation behavior is the terrain
				// kernel's, measured against the whole fire attempt elsewhere.
				return
			}
			if got := testing.AllocsPerRun(100, func() {
				rulesDispatchSink = svc.rules().AdmitShot(&query)
			}); got != 0 {
				t.Fatalf("AdmitShot dispatch allocated %v per call", got)
			}
		})
	}
	if !rulesDispatchSink {
		// Modern answers true for a shooter whose standing fire field is zero,
		// so the sink is set. Reading it also keeps it live.
		t.Fatal("held shooter answered false; the measured dispatch was elided")
	}
}

// TestStrictRulesAnswerAsRetail states the Strict contract the fingerprint
// depends on: retail admits every resolved attempt without sampling terrain
// [06 R-WPN-05 §1] and never suppresses a shooter whose target is installed
// [04 R-STANCE-01 §2]. A Strict answer also never draws, so the seam cannot
// move the shared stream.
func TestStrictRulesAnswerAsRetail(t *testing.T) {
	var held units.Unit
	held.Flags &^= units.StandingFieldMask << units.StandingFireShift
	if !(&ModernRules{}).HoldsFire(&held) {
		t.Fatal("Modern lost the Hold Fire contract of DESIGN_WEAPONS_PROJECTILES §2.6.1")
	}
	var strict StrictRules
	if strict.HoldsFire(&held) {
		t.Fatal("Strict 3.1 suppressed a held shooter; retail fires the installed target")
	}
	query := ShotQuery{Blocked: true}
	if !strict.AdmitShot(&query) || !query.Blocked {
		t.Fatal("Strict 3.1 refused a shot or wrote a terrain verdict")
	}
	if previewsShot(strict) || previewsShot(nil) {
		t.Fatal("Strict 3.1 asked for the speculative spread; retail takes no copies")
	}
	if !previewsShot(&ModernRules{}) {
		t.Fatal("Modern must preview, or a refusal would leak the spread draws")
	}
}
