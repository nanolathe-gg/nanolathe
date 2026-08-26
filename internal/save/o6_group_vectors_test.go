package save

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestStateV1GroupVectorsRoundTripInRetailSlotOrder(t *testing.T) {
	state := &StateV1{
		Version: StateV1Version9,
		AI: []AIManagerRecord{{
			Player: 1,
			Groups: AIGroupsSnapshot{
				Resource:     []int32{11, 12},
				WaveA:        []int32{21},
				RegroupA:     []int32{31, 32},
				Construction: []int32{41},
				Null:         []int32{51, 52, 53},
				WaveB:        []int32{61},
				RegroupB:     []int32{71},
				Explore:      []int32{81, 82},
				Rally:        []int32{91},
			},
		}},
	}
	decoded, err := UnmarshalStateV1(MarshalStateV1(state), "", "")
	if err != nil {
		t.Fatalf("UnmarshalStateV1: %v", err)
	}
	got := decoded.AI[0].Groups
	if !equalInt32(got.Resource, state.AI[0].Groups.Resource) ||
		!equalInt32(got.WaveA, state.AI[0].Groups.WaveA) ||
		!equalInt32(got.RegroupA, state.AI[0].Groups.RegroupA) ||
		!equalInt32(got.Construction, state.AI[0].Groups.Construction) ||
		!equalInt32(got.Null, state.AI[0].Groups.Null) ||
		!equalInt32(got.WaveB, state.AI[0].Groups.WaveB) ||
		!equalInt32(got.RegroupB, state.AI[0].Groups.RegroupB) ||
		!equalInt32(got.Explore, state.AI[0].Groups.Explore) ||
		!equalInt32(got.Rally, state.AI[0].Groups.Rally) {
		t.Fatalf("group vectors changed across v9 round trip: got=%+v want=%+v", got, state.AI[0].Groups)
	}
	// The manager record is serialized canonically, so a second encode is
	// byte-identical even when the caller supplied non-canonical input order.
	if again := MarshalStateV1(decoded); !bytes.Equal(again, MarshalStateV1(state)) {
		t.Fatal("v9 group-vector encoding is not deterministic")
	}
}

func TestStateV1Version8GroupVectorsDefaultEmpty(t *testing.T) {
	state := &StateV1{
		Version: StateV1Version8,
		AI: []AIManagerRecord{{
			Player: 0,
			Groups: AIGroupsSnapshot{
				WaveA:    []int32{2},
				WaveB:    []int32{6},
				Explore:  []int32{8},
				Rally:    []int32{9},
				RegroupA: []int32{3},
				RegroupB: []int32{7},
			},
		}},
	}
	decoded, err := UnmarshalStateV1(MarshalStateV1(state), "", "")
	if err != nil {
		t.Fatalf("UnmarshalStateV1(v8): %v", err)
	}
	got := decoded.AI[0].Groups
	if len(got.Resource) != 0 || len(got.Construction) != 0 || len(got.Null) != 0 {
		t.Fatalf("v8 must default new vectors empty: %+v", got)
	}
	if !equalInt32(got.WaveA, state.AI[0].Groups.WaveA) || !equalInt32(got.WaveB, state.AI[0].Groups.WaveB) || !equalInt32(got.Explore, state.AI[0].Groups.Explore) || !equalInt32(got.Rally, state.AI[0].Groups.Rally) || !equalInt32(got.RegroupA, state.AI[0].Groups.RegroupA) || !equalInt32(got.RegroupB, state.AI[0].Groups.RegroupB) {
		t.Fatalf("v8 legacy vectors changed: got=%+v want=%+v", got, state.AI[0].Groups)
	}
}

func TestStateV1RejectsCorruptGroupVectorCount(t *testing.T) {
	state := &StateV1{Version: StateV1Version9, AI: []AIManagerRecord{{Player: 7}}}
	payload := MarshalStateV1(state)
	// With empty pre-AI collections, this is the first group's count: header
	// (version, two empty strings, RNG, clock), then 7 empty collection counts,
	// AI count, manager player/deadlines/strategic fields and four strategic
	// collection counts. Keep this offset derived from the wire fields rather
	// than searching for an incidental byte pattern.
	const economyPlayerBytesV9 = 223
	off := 64 + 6*4 + 10*economyPlayerBytesV9 + 4 + 8 + 4 + 4 + 1 + 12*4 + 5*4 + 4*4
	if off+4 > len(payload) {
		t.Fatalf("computed group count offset %d beyond payload %d", off, len(payload))
	}
	binary.LittleEndian.PutUint32(payload[off:off+4], ^uint32(0))
	if _, err := UnmarshalStateV1(payload, "", ""); err == nil {
		t.Fatal("corrupt group vector count was accepted")
	}
}

func TestStateV1RejectsDuplicateOrNullGroupMember(t *testing.T) {
	tests := []struct {
		name   string
		groups AIGroupsSnapshot
	}{
		{name: "duplicate within vector", groups: AIGroupsSnapshot{Resource: []int32{11, 11}}},
		{name: "duplicate across vectors", groups: AIGroupsSnapshot{Resource: []int32{11}, Construction: []int32{11}}},
		{name: "null handle", groups: AIGroupsSnapshot{Rally: []int32{0}}},
		{name: "negative handle", groups: AIGroupsSnapshot{WaveA: []int32{-1}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := &StateV1{Version: StateV1Version9, AI: []AIManagerRecord{{Player: 1, Groups: tc.groups}}}
			if _, err := UnmarshalStateV1(MarshalStateV1(state), "", ""); err == nil {
				t.Fatal("malformed group membership was accepted")
			}
		})
	}
}

func equalInt32(a, b []int32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
