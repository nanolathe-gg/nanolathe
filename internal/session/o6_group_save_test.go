package session

import (
	"testing"
)

func TestHashStateIncludesInBuildStance(t *testing.T) {
	s := strictNewSessionWithUnits(t, 18, 901, 1001)
	u := s.Units.Unit(3)
	if u == nil {
		t.Fatal("strict fixture missing unit")
	}
	base := HashState(s)
	u.InBuildStance = true
	if got := HashState(s); got == base {
		t.Fatal("INBUILDSTANCE change did not affect authoritative hash")
	}
}
