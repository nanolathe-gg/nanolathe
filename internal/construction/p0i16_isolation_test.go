package construction

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/units"
)

func TestP0I16_LimitCheckerIsolation(t *testing.T) {
	svcA := NewService(nil, nil, nil, nil)
	svcB := NewService(nil, nil, nil, nil)
	svcA.LimitChecker = func(f *units.Unit, key string) bool { return false } // always refuse
	svcB.LimitChecker = func(f *units.Unit, key string) bool { return true }  // always allow

	f := &units.Unit{Handle: 1, Owner: 0}
	if svcA.CheckLimit(f, "any") {
		t.Fatalf("svcA should refuse")
	}
	if !svcB.CheckLimit(f, "any") {
		t.Fatalf("svcB should allow")
	}
	// Interleaved
	if svcA.CheckLimit(f, "any") != false {
		t.Fatalf("interleaved A contaminated")
	}
	if svcB.CheckLimit(f, "any") != true {
		t.Fatalf("interleaved B contaminated")
	}
	// Save/reload simulation
	saved := svcA.LimitChecker
	svcA2 := NewService(nil, nil, nil, nil)
	svcA2.LimitChecker = func(f *units.Unit, key string) bool { return true }
	// Destroy svcA2, reload svcA
	reloaded := NewService(nil, nil, nil, nil)
	reloaded.LimitChecker = saved
	if reloaded.CheckLimit(f, "any") {
		t.Fatalf("reloaded should still refuse")
	}
}
