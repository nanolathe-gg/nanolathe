package save

import "testing"

func TestStateV1RoundTripPreservesInBuildStance(t *testing.T) {
	state := &StateV1{
		Version: StateV1VersionConst,
		Units: []UnitRecord{{
			Slot:          1,
			DefName:       "factory",
			InBuildStance: true,
		}},
	}
	decoded, err := UnmarshalStateV1(MarshalStateV1(state), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Units) != 1 || !decoded.Units[0].InBuildStance {
		t.Fatalf("decoded stance=%v, want true", decoded.Units)
	}
}
