package construction

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/units"
)

// TestCaptureEligible_FivePredicates locks the phase-0 admission ladder of
// [05 R-WORK-01 §6] as [05 R-WORK-01 §10] closed it.
//
// Three things it pins that the build used to get wrong:
//
//   - `Remaining == 0` IS the retail predicate, not a proxy for an idleness
//     sentinel: it is a float32 compare with a literal zero whose only accepted
//     outcome is equal [05 R-WORK-01 §10]. A finished unit carries zero; a
//     nanoframe carries 1.0 and is the "cloud of vapor".
//   - the "victim immunity" the build could not locate is predicate 4, the
//     TARGET's own `cancapture` bit — the same bit predicate 3 requires of the
//     builder, so anything that can capture cannot be captured.
//   - the same-owner and dying-victim rejects the build added are NOT in the
//     ladder, which has exactly five predicates. A busy, moving or damaged
//     finished unit is captured normally.
func TestCaptureEligible_FivePredicates(t *testing.T) {
	captor := &content.UnitDef{UnitName: "captor", CanCapture: true}
	plain := &content.UnitDef{UnitName: "plain"}

	mk := func(def *content.UnitDef, owner uint8, remaining float32) *units.Unit {
		return &units.Unit{Def: def, Owner: owner, Alive: true, Remaining: remaining}
	}

	cases := []struct {
		name    string
		builder *units.Unit
		victim  *units.Unit
		want    bool
	}{
		{"finished enemy unit is captured", mk(captor, 0, 0), mk(plain, 1, 0), true},
		{"1: a null target handle rejects", mk(captor, 0, 0), nil, false},
		{"2: an unlinked builder rejects", nil, mk(plain, 1, 0), false},
		{"3: a builder without cancapture rejects", mk(plain, 0, 0), mk(plain, 1, 0), false},
		{"4: a target that can itself capture rejects", mk(captor, 0, 0), mk(captor, 1, 0), false},
		{"5: a nanoframe is a cloud of vapor", mk(captor, 0, 0), mk(plain, 1, 1), false},
		{"5: a partly built target is a cloud of vapor", mk(captor, 0, 0), mk(plain, 1, 0.5), false},
		// Not in the ladder [05 R-WORK-01 §10]: retail's phase 0 has exactly
		// the five predicates above, so neither of these is a reject here.
		{"same owner is not a phase-0 reject", mk(captor, 0, 0), mk(plain, 0, 0), true},
		{"a death-latched victim is not a phase-0 reject", mk(captor, 0, 0), func() *units.Unit {
			u := mk(plain, 1, 0)
			u.Dying = true
			return u
		}(), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CaptureEligible(tc.builder, tc.victim); got != tc.want {
				t.Fatalf("CaptureEligible = %v, want %v [05 R-WORK-01 §6][05 R-WORK-01 §10]", got, tc.want)
			}
		})
	}

	// Negative zero compares equal to the literal zero and is accepted
	// [05 R-WORK-01 §10]. The negation is a runtime one so the compiler cannot
	// fold it back to +0 the way it folds the constant -0.0.
	var zero float32
	negZero := mk(plain, 1, -zero)
	if !CaptureEligible(mk(captor, 0, 0), negZero) {
		t.Fatal("negative zero compares equal to literal zero and is accepted [05 R-WORK-01 §10]")
	}
}
