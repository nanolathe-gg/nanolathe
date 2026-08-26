package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/ai"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/save"
)

func TestAIGroupVectorsCaptureRestoreAndHashCoherence(t *testing.T) {
	s := strictNewSessionWithUnits(18, 901, 1001)
	m := &ai.Manager{Player: 1}
	// The strict fixture's sliced pool has two owner-1 units. Unit.Group is the
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// vectors must agree with their member's group value.
	groups := [][]pool.Handle{
		{3}, {4}, nil, nil, nil, nil, nil, nil, nil,
	}
	for i, members := range groups {
		if len(members) == 0 {
			continue
		}
		u := s.Units.Unit(members[0])
		if u == nil || u.Owner != 1 {
			t.Fatalf("fixture handle %d is not owner-1 unit", members[0])
		}
		u.Group = uint8(i + 1)
	}
	m.GroupResource = groups[0]
	m.GroupWaveA = groups[1]
	s.AI[1] = m

	beforeHash := HashState(s)
	st := s.CaptureStateV1()
	if st == nil || st.Version != 9 {
		t.Fatalf("CaptureStateV1 version=%v want 9", st)
	}
	decoded, err := saveStateRoundTrip(st)
	if err != nil {
		t.Fatalf("StateV1 round trip: %v", err)
	}
	s2 := strictNewSessionWithUnits(18, 901, 1001)
	if err := s2.RestoreStateV1(decoded); err != nil {
		t.Fatalf("RestoreStateV1: %v", err)
	}
	if got := HashState(s2); got != beforeHash {
		t.Fatalf("vector-aware state hash changed after restore: got %s want %s", got, beforeHash)
	}
	if got := s2.AI[1].GroupWaveA; len(got) != 1 || got[0] != pool.Handle(4) || s2.Units.Unit(4).Group != 2 {
		t.Fatalf("wave vector/unit group mismatch: vectors=%v unit=%d", got, s2.Units.Unit(4).Group)
	}
	// A future AI decision can depend on membership order, so changing one
	// vector must change the authoritative hash even when units are unchanged.
	s2.AI[1].GroupWaveA[0] = pool.Handle(3)
	if got := HashState(s2); got == beforeHash {
		t.Fatal("AI group vector change did not affect authoritative hash")
	}
	// Unit.Group is also authoritative even when the unit is currently in the
	// ungrouped record (or is dying and therefore absent from a task vector).
	ungrouped := s2.Units.Unit(5)
	if ungrouped == nil {
		t.Fatal("strict fixture missing ungrouped unit")
	}
	hashBeforeGroup := HashState(s2)
	ungrouped.Group = 9
	if got := HashState(s2); got == hashBeforeGroup {
		t.Fatal("unit group change did not affect authoritative hash")
	}
}

func TestValidateSavedAIGroupsAllowsUngroupedAndDying(t *testing.T) {
	state := &save.StateV1{
		Version: save.StateV1Version9,
		Units: []save.UnitRecord{
			{Slot: 3, Owner: 1, Group: 0},
			{Slot: 4, Owner: 1, Group: 2, Dying: true},
		},
		AI: []save.AIManagerRecord{{Player: 1, Groups: save.AIGroupsSnapshot{WaveA: []int32{4}}}},
	}
	if err := validateSavedAIGroups(state); err != nil {
		t.Fatalf("ungrouped/dead state rejected: %v", err)
	}
}

func TestValidateSavedAIGroupsRejectsDuplicateMembership(t *testing.T) {
	state := &save.StateV1{
		Version: save.StateV1Version9,
		Units:   []save.UnitRecord{{Slot: 3, Owner: 1, Group: 1}},
		AI:      []save.AIManagerRecord{{Player: 1, Groups: save.AIGroupsSnapshot{Resource: []int32{3}, Construction: []int32{3}}}},
	}
	if err := validateSavedAIGroups(state); err == nil {
		t.Fatal("duplicate AI group membership accepted")
	}
}

func saveStateRoundTrip(state *save.StateV1) (*save.StateV1, error) {
	return save.UnmarshalStateV1(save.MarshalStateV1(state), "", "")
}
