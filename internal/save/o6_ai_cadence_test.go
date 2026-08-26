package save

import (
	"bytes"
	"testing"
)

func TestStateV1AICadenceRoundTripV10(t *testing.T) {
	state := &StateV1{
		Version: StateV1Version10,
		AI: []AIManagerRecord{{
			Player:     2,
			EntryCount: 29,
			Groups:     AIGroupsSnapshot{WaveA: []int32{11}},
		}},
	}
	payload := MarshalStateV1(state)
	decoded, err := UnmarshalStateV1(payload, "", "")
	if err != nil {
		t.Fatalf("UnmarshalStateV1: %v", err)
	}
	if len(decoded.AI) != 1 || decoded.AI[0].EntryCount != 29 {
		t.Fatalf("cadence counter changed across v10 round trip: %#v", decoded.AI)
	}
	if again := MarshalStateV1(decoded); !bytes.Equal(again, payload) {
		t.Fatal("v10 cadence encoding is not deterministic")
	}
}

func TestStateV1Version9AICadenceDefaultsZero(t *testing.T) {
	state := &StateV1{
		Version: StateV1Version9,
		AI:      []AIManagerRecord{{Player: 2, EntryCount: 29}},
	}
	decoded, err := UnmarshalStateV1(MarshalStateV1(state), "", "")
	if err != nil {
		t.Fatalf("UnmarshalStateV1(v9): %v", err)
	}
	if got := decoded.AI[0].EntryCount; got != 0 {
		t.Fatalf("legacy v9 cadence counter = %d, want zero default", got)
	}
}

func TestStateV1Version9RetainsLegacyUnitLayout(t *testing.T) {
	// Version 9 was allocated by the group-vector writer. INBUILDSTANCE was
	// added in version 10; keeping it out of the v9 wire layout is required so
	// older v9 payloads still decode the extended unit fields at their original
	// offsets.
	state := &StateV1{
		Version: StateV1Version9,
		Units: []UnitRecord{{
			Slot: 1, DefName: "factory", Health: 123, InBuildStance: true,
			MaxHealth: 456, Pending: 789,
		}},
	}
	decoded, err := UnmarshalStateV1(MarshalStateV1(state), "", "")
	if err != nil {
		t.Fatalf("UnmarshalStateV1(v9): %v", err)
	}
	if got := decoded.Units[0]; got.InBuildStance || got.Health != 123 || got.MaxHealth != 456 || got.Pending != 789 {
		t.Fatalf("v9 unit layout changed: %+v", got)
	}
}

func TestStateV1RejectsTruncatedAICadence(t *testing.T) {
	state := &StateV1{
		Version: StateV1Version10,
		AI:      []AIManagerRecord{{Player: 2, EntryCount: 29}},
	}
	payload := MarshalStateV1(state)
	// With one empty AI record, the fixed post-AI tail is visibility (65
	// bytes), latch (5), wind (36), construction count (4), followed by the
	// manager's surface/origin triple (12). Remove that tail and leave the
	// decoder exactly at the new cadence field.
	const postGroupsTail = 65 + 5 + 36 + 4 + 12
	if len(payload) <= postGroupsTail {
		t.Fatal("unexpectedly short StateV1 payload")
	}
	if _, err := UnmarshalStateV1(payload[:len(payload)-postGroupsTail], "", ""); err == nil {
		t.Fatal("truncated v10 cadence payload accepted")
	}
}
