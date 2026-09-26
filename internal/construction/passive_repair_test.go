package construction

import (
	"math"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// The Gold caller uses a low-byte mask rather than a modulus period, and
// sign-extends the definition word before deriving work. These authored
// examples distinguish each arithmetic boundary [escalation-shields
// "Passive generator healing"].
func TestPassiveRepairMaskAndSignedQuantum(t *testing.T) {
	s := &Service{Community: community.Features{HealTimeBitmask: true}}
	u := &units.Unit{Def: &content.UnitDef{}}
	for _, tc := range []struct {
		healTime int32
		tick     uint32
		work     int32
		admitted bool
	}{
		{0, 0, 0, false}, {1, 1, 0, false}, {1, 2, 1, true},
		{2, 1, 2, true}, {2, 2, 0, false}, {3, 3, 0, false}, {3, 4, 3, true},
		{15, 16, 16, true}, {27, 4, 28, true}, {27, 8, 0, false},
		{256, 255, 273, true}, {32767, 256, 34951, true},
		{32768, 1, -34952, true}, {65535, 256, -1, true}, {65536, 0, 0, false},
	} {
		u.Def.HealTime = tc.healTime
		work, admitted := (CommunityRules{}).PassiveRepairWork(s, u, tc.tick)
		if work != tc.work || admitted != tc.admitted {
			t.Fatalf("HealTime %d tick %d: (%d,%v), want (%d,%v)", tc.healTime, tc.tick, work, admitted, tc.work, tc.admitted)
		}
	}
	u.Def.HealTime = 1
	for _, remaining := range []float32{1, 0.01, math.Float32frombits(1 << 31)} {
		u.Remaining = remaining
		if _, admitted := (CommunityRules{}).PassiveRepairWork(s, u, 2); admitted {
			t.Fatalf("nonzero construction bits admitted: %08x", math.Float32bits(remaining))
		}
	}
}

func TestPassiveRepairStrictAndDisabledCaller(t *testing.T) {
	u := &units.Unit{Def: &content.UnitDef{HealTime: 27}, Remaining: 0.5}
	for _, tc := range []struct {
		rules   Rules
		enabled bool
	}{{StrictRules{}, true}, {CommunityRules{}, false}, {&ModernRules{}, false}} {
		s := &Service{Community: community.Features{HealTimeBitmask: tc.enabled}}
		if work, admitted := tc.rules.PassiveRepairWork(s, u, 8); !admitted || work != 7 {
			t.Fatalf("%T lost retail work/completion behavior: %d,%v", tc.rules, work, admitted)
		}
		if _, admitted := tc.rules.PassiveRepairWork(s, u, 4); admitted {
			t.Fatalf("%T lost retail cadence", tc.rules)
		}
	}
}
