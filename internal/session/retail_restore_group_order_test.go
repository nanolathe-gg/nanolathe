package session

import (
	"encoding/binary"
	"slices"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/save"
)

// References complete before the current unit's group append. A live-record
// back-reference is skipped, so engagement cycles append each unit once
// [08 R-SAVE-02 §6].
func TestRetailRestoreGroupsFollowRecursiveReferences(t *testing.T) {
	for _, tc := range []struct {
		name       string
		engagement []int
		carrier    []int
		want       []int
	}{
		{"forward attacker", []int{1, -1}, nil, []int{1, 0}},
		{"carrier", []int{-1, -1, -1}, []int{2, -1, -1}, []int{2, 0, 1}},
		{"engagement cycle", []int{1, 0}, nil, []int{1, 0}},
		{"self reference", []int{0, -1}, nil, []int{0, 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, fixtures := newRestoreCoreFixture(t, len(tc.engagement))
			image := &save.BattleImage{}
			for i, fixture := range fixtures {
				s.Units.Unit(fixture.handle).Group = 7 // UI groups are independent of the saved AI group.
				data := unitRecordData(false)
				if target := tc.engagement[i]; target >= 0 {
					binary.LittleEndian.PutUint16(data[0x8B:], fixtures[target].stableID)
				}
				if len(tc.carrier) != 0 && tc.carrier[i] >= 0 {
					binary.LittleEndian.PutUint16(data[0x89:], fixtures[tc.carrier[i]].stableID)
				}
				binary.LittleEndian.PutUint32(data[0x9F:], 3)
				image.Units.Records = append(image.Units.Records, save.UnitRecord{StableID: fixture.stableID, Data: data})
			}
			s.AI[0] = &ai.Manager{Player: 0}
			stage := &RetailBattleStage{Session: s, StableUnit: stableUnitMap(fixtures), Image: image}
			if err := RestoreRetailBattleCore(stage); err != nil {
				t.Fatal(err)
			}
			var want []pool.Handle
			for _, i := range tc.want {
				want = append(want, fixtures[i].handle)
				u := s.Units.Unit(fixtures[i].handle)
				if u.RestoredAIGroup != 3 || u.Group != 7 {
					t.Fatalf("AI/UI groups = %d/%d, want 3/7", u.RestoredAIGroup, u.Group)
				}
			}
			if got := s.AI[0].GroupMembers(3); !slices.Equal(got, want) {
				t.Fatalf("restored group = %v, want recursive append order %v", got, want)
			}
		})
	}
}
