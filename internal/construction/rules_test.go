package construction

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// clearanceRules selects the rule set a Modern/Strict fixture table runs under.
// Strict is the zero value of the seam, so a nil field means the same thing.
func clearanceRules(modern bool) Rules {
	if modern {
		return &ModernRules{}
	}
	return StrictRules{}
}

// The seam must not cost an allocation on the retail path. Both the explicit
// Strict value and an unset field dispatch through the same shared interface
// value, so neither boxes anything per call.
func TestStrictClearanceDispatchAllocatesNothing(t *testing.T) {
	extent, err := world.NewFootprintExtent(2, 2)
	if err != nil {
		t.Fatal(err)
	}
	rect, err := world.NewFootprintRect(world.NewFootprintAnchor(4, 4), extent)
	if err != nil {
		t.Fatal(err)
	}
	yard := make([]world.YardCell, 4)
	for _, tc := range []struct {
		name  string
		rules Rules
	}{
		{"unset", nil},
		{"explicit", StrictRules{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Service{Rules: tc.rules}
			if got := testing.AllocsPerRun(200, func() {
				s.rules().YieldObstruction(s, nil, rect, nil, 7, false)
				s.rules().YieldObstruction(s, nil, rect, yard, 7, true)
			}); got != 0 {
				t.Fatalf("Strict clearance dispatch allocated %v per run, want 0", got)
			}
		})
	}
}
