package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// The two tests here lock the kind-2 (skirmish) start-position contract of
// [08 R-ENTRY-01 §5] "Kind 2 (skirmish), no save file": the stamp helper takes
// NO simulation draw of its own, and a `StartPos` miss is fatal with retail's
// verbatim diagnostic.
//
// Before WU-19-178 this build drew two simulation values per eligible slot at
// the placement site — a "jitter" that belongs to the kind-3 path alone, and
// whose bounds there are world units rather than the cells the code asserted
// ([08 R-ENTRY-01 §10] correction 2) — and kept the jittered point as a silent
// fallback on a miss. Both are easy to reintroduce without anything failing:
// the fallback looks like robustness, and a stray draw only shows up as a hash
// that moved. Hence the draw-count assertion and the exact-text assertion.

// wu19178Config builds the two-slot identity-placement skirmish these tests
// use: Location non-zero means no CRT shuffle, so slot i takes StartPos<i+1>
// [08 R-ENTRY-01 §5] step 3.
func wu19178Config() SkirmishConfig {
	cfg := SkirmishConfig{NumPlayers: 2, Location: 1}
	cfg.Players[0].Side = 0 // ARM in the fixture catalog
	cfg.Players[1].Side = 1 // CORE
	return cfg
}

// wu19178Mission returns a synthetic map carrying the named start positions.
// Special.ID is the STORED number, one less than the authored label: StartPos1
// is stored 0 [08 R-TRIG-01 §9].
func wu19178Mission(specials ...mission.Special) *mission.Mission {
	m := strictSyntheticMission()
	m.Specials = specials
	return m
}

// TestSkirmishPlacementTakesNoSimulationDraw is the draw-order half. The stamp
// resolves both slots' `StartPos` records and creates two commanders; the only
// simulation traffic permitted is the common allocator's own, taken inside
// Create ([04 §2.3b]). The assertion is a relationship rather than a census —
// how many draws one allocation costs depends on the definition — so the
// control run creates the same two commanders directly and the placement run
// must consume exactly that and no more. The removed jitter was two extra
// draws per eligible slot, so it would show here as four.
func TestSkirmishPlacementTakesNoSimulationDraw(t *testing.T) {
	control := strictNewSessionWithUnits(t, 0, 7, 11)
	controlBefore := control.SimRNG().Draws()
	for _, spec := range []struct {
		name  string
		owner uint8
	}{{"armcom", 0}, {"corcom", 1}} {
		if _, err := control.Units.Create(control.Catalog.Units[spec.name], spec.owner, 0, 0, 0); err != nil {
			t.Fatalf("control Create(%s): %v", spec.name, err)
		}
	}
	allocatorDraws := control.SimRNG().Draws() - controlBefore

	s := strictNewSessionWithUnits(t, 0, 7, 11)
	m := wu19178Mission(
		mission.Special{Kind: 1, ID: 0, X: 12, Z: 20, Name: "StartPos1"},
		mission.Special{Kind: 1, ID: 1, X: 24, Z: 28, Name: "StartPos2"},
	)
	beforeSim := s.SimRNG().Draws()
	beforeCrt := s.CrtRNG().Draws()
	if err := skirmishReconstructUnits(s, wu19178Config(), m); err != nil {
		t.Fatalf("skirmishReconstructUnits: %v", err)
	}
	created := s.Units.Iter()
	if len(created) != 2 {
		t.Fatalf("placed %d commanders, want one per eligible slot", len(created))
	}
	// The allocator's draws and nothing else [08 R-ENTRY-01 §5] ("No simulation
	// draw is made by the stamp or the grant themselves").
	if got := s.SimRNG().Draws() - beforeSim; got != allocatorDraws {
		t.Fatalf("placement consumed %d simulation draws, want %d — the two commander allocations and no stamp draw [08 R-ENTRY-01 §5]", got, allocatorDraws)
	}
	// Identity placement takes no CRT draw either: the Fisher-Yates walk and
	// its gate draw belong to the Location==0 branch [08 R-ENTRY-01 §5].
	if got := s.CrtRNG().Draws() - beforeCrt; got != 0 {
		t.Fatalf("identity placement consumed %d CRT draws, want 0 [08 R-ENTRY-01 §5]", got)
	}
	// The commanders sit exactly on the authored records, not near them.
	for _, want := range []struct {
		owner uint8
		x, z  int32
	}{{0, 12, 20}, {1, 24, 28}} {
		found := false
		for _, u := range created {
			if u == nil || u.Owner != want.owner {
				continue
			}
			found = true
			wx := numeric.Fixed(want.x * 65536)
			wz := numeric.Fixed(want.z * 65536)
			if u.X != wx || u.Z != wz {
				t.Fatalf("slot %d commander at (%v, %v), want the authored StartPos (%v, %v) [08 R-ENTRY-01 §5]",
					want.owner, u.X, u.Z, wx, wz)
			}
		}
		if !found {
			t.Fatalf("slot %d has no commander", want.owner)
		}
	}
}

// TestSkirmishMissingStartPositionIsFatal is the diagnostic half. The map
// authors StartPos1 only, so slot 1's lookup for stored number 1 (label
// StartPos2) misses and battle entry fails with retail's verbatim text. The
// number in the message is the STORED one, zero-based — slot 1 reports 1
// [08 R-ENTRY-01 §5] step 4.
func TestSkirmishMissingStartPositionIsFatal(t *testing.T) {
	s := strictNewSessionWithUnits(t, 0, 7, 11)
	m := wu19178Mission(mission.Special{Kind: 1, ID: 0, X: 12, Z: 20, Name: "StartPos1"})
	err := skirmishReconstructUnits(s, wu19178Config(), m)
	if err == nil {
		t.Fatal("a missing StartPos placed a commander anyway; the kind-2 miss is fatal [08 R-ENTRY-01 §5]")
	}
	const want = "Error: Could not find start position number 1 on the map!"
	if err.Error() != want {
		t.Fatalf("diagnostic %q, want the verbatim %q [08 R-ENTRY-01 §5]", err.Error(), want)
	}
}

// TestSkirmishFirstStartPosLabelIsSlotZero pins the off-by-one that separates
// the stored number from the authored label, and the alias it creates: slot 0
// looks for stored number 0, which `StartPos1` and `StartPos0` both carry
// ("a suffix of 0 stays 0", [08 R-TRIG-01 §9]), while `StartPos2` is stored 1
// and belongs to slot 1.
//
// Until WU-19-205 the decoder kept the authored label and this lookup added one
// to it, so a map whose first record was `StartPos0` failed with the fatal
// diagnostic. This test locked that rejection in; it asserts the alias now
// (review finding R10).
func TestSkirmishFirstStartPosLabelIsSlotZero(t *testing.T) {
	s := strictNewSessionWithUnits(t, 0, 7, 11)
	m := wu19178Mission(
		mission.Special{Kind: 1, ID: 0, X: 12, Z: 20, Name: "StartPos0"},
		mission.Special{Kind: 1, ID: 1, X: 24, Z: 28, Name: "StartPos2"},
	)
	if err := skirmishReconstructUnits(s, wu19178Config(), m); err != nil {
		t.Fatalf("StartPos0 was rejected for slot 0; it stores the same number as StartPos1 [08 R-TRIG-01 §9]: %v", err)
	}
	created := s.Units.Iter()
	if len(created) != 2 {
		t.Fatalf("placed %d commanders, want one per eligible slot", len(created))
	}
	for _, want := range []struct {
		owner uint8
		x, z  int32
	}{{0, 12, 20}, {1, 24, 28}} {
		found := false
		for _, u := range created {
			if u == nil || u.Owner != want.owner {
				continue
			}
			found = true
			wx, wz := numeric.Fixed(want.x*65536), numeric.Fixed(want.z*65536)
			if u.X != wx || u.Z != wz {
				t.Fatalf("slot %d commander at (%v, %v), want (%v, %v) [08 R-ENTRY-01 §5]", want.owner, u.X, u.Z, wx, wz)
			}
		}
		if !found {
			t.Fatalf("slot %d has no commander", want.owner)
		}
	}
}
