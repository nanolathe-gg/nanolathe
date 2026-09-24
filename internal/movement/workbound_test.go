package movement

import "testing"

// Strict and Community keep retail's unbounded carry and full polling; Modern
// bounds the carry and stops futile sweeps; an unbound System answers as
// Strict (DESIGN_MOVEMENT_PATH "Modern bounded path work") [04 R-PATH-01 §6].
func TestPathWorkBoundAnswers(t *testing.T) {
	for _, rules := range []Rules{StrictRules{}, CommunityRules{}} {
		if carry, stop := rules.PathWorkBound(nil); carry != 0 || stop {
			t.Fatalf("%T PathWorkBound = (%d, %v), want retail (0, false)", rules, carry, stop)
		}
	}
	if carry, stop := (&ModernRules{}).PathWorkBound(nil); carry != modernCarryShares || !stop {
		t.Fatalf("Modern PathWorkBound = (%d, %v), want (%d, true)", carry, stop, modernCarryShares)
	}
	var unbound System
	p := &pathProvider{system: &unbound}
	if carry, stop := p.PathWorkBound(); carry != 0 || stop {
		t.Fatalf("unbound provider answered (%d, %v), want retail", carry, stop)
	}
	unbound.Rules = &ModernRules{}
	if carry, stop := p.PathWorkBound(); carry != modernCarryShares || !stop {
		t.Fatalf("Modern-bound provider answered (%d, %v)", carry, stop)
	}
	if n := testing.AllocsPerRun(100, func() { _, _ = p.PathWorkBound() }); n != 0 {
		t.Fatalf("PathWorkBound allocated %g times", n)
	}
}

// Destination slots are off under Strict and Community and on under Modern
// (DESIGN_INTERFACE_HUD_INPUT "Modern group destination slots").
func TestGroupDestinationSlotsAnswers(t *testing.T) {
	for _, rules := range []Rules{StrictRules{}, CommunityRules{}} {
		if rules.GroupDestinationSlots(nil) {
			t.Fatalf("%T gives group destination slots", rules)
		}
	}
	if !(&ModernRules{}).GroupDestinationSlots(nil) {
		t.Fatal("Modern does not give group destination slots")
	}
	var unbound System
	unbound.AssignGroupDestinations(nil, []GroupDestination{{H: 1}, {H: 2}}, 0, 0) // no world, no rules: no-op
}
