package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func communityAreaFixture(t *testing.T, n int, rules Rules) (*Service, *units.World, *world.Terrain, []pool.Handle) {
	t.Helper()
	w := newCombatFixtureWorld(64, nil)
	terrain := &world.Terrain{CellW: 4, CellH: 4, Plot: make([]world.PlotCell, 16)}
	s := &Service{Rules: rules, Community: community.Features{AreaDamageOverflow: true, AreaDamageDedupCap: true}}
	def := &content.UnitDef{UnitName: "stacked-air", MaxDamage: 100, Limit: -1, FootprintX: 1, FootprintZ: 1, ModelTopFixed: int32(numeric.FixedFromInt(16))}
	handles := make([]pool.Handle, 0, n)
	for i := 0; i < n; i++ {
		u := splashUnit(t, w, def, 1, 24, 0, 24)
		u.Move.ModeMirror = 2
		handles = append(handles, u.Handle)
	}
	return s, w, terrain, handles
}

func collectAreaVictims(s *Service, w *units.World, terrain *world.Terrain, tick uint32) []pool.Handle {
	var got []pool.Handle
	s.rules().AreaIndexTick(AreaVictimQuery{Service: s, World: w, Terrain: terrain, Tick: tick})
	saved := s.beginCommunityArea()
	s.rules().AreaVictims(AreaVictimQuery{Service: s, World: w, Terrain: terrain, Tick: tick, CellX: 1, CellZ: 1}, func(h pool.Handle) {
		got = append(got, h)
	})
	s.endCommunityArea(saved)
	return got
}

func TestCommunityAreaOverflowCapAndModernLift(t *testing.T) {
	communitySvc, w, terrain, handles := communityAreaFixture(t, 8, CommunityRules{})
	got := collectAreaVictims(communitySvc, w, terrain, 7)
	if len(got) != 6 {
		t.Fatalf("Community victims=%v, want first six", got)
	}
	for i := range got {
		if got[i] != handles[i] {
			t.Fatalf("Community victim %d=%d, want stable slot %d", i, got[i], handles[i])
		}
	}
	if got := communitySvc.CommunityAreaSaturations(); got != 2 {
		t.Fatalf("Community saturations=%d, want one for each of two failed cell insertions", got)
	}

	modernSvc, modernWorld, modernTerrain, modernHandles := communityAreaFixture(t, 8, &ModernRules{})
	modernGot := collectAreaVictims(modernSvc, modernWorld, modernTerrain, 7)
	if len(modernGot) != len(modernHandles) {
		t.Fatalf("Modern victims=%v, want all %v", modernGot, modernHandles)
	}
	if got := modernSvc.CommunityAreaSaturations(); got != 0 {
		t.Fatalf("Modern lifted cap counted %d saturations", got)
	}
}

func TestCommunityAreaGenerationSurvivesNestedBlast(t *testing.T) {
	s, w, terrain, handles := communityAreaFixture(t, 3, CommunityRules{})
	var got []pool.Handle
	s.rules().AreaIndexTick(AreaVictimQuery{Service: s, World: w, Terrain: terrain, Tick: 9})
	outerSaved := s.beginCommunityArea()
	s.rules().AreaVictims(AreaVictimQuery{Service: s, World: w, Terrain: terrain, Tick: 9, CellX: 1, CellZ: 1}, func(h pool.Handle) {
		got = append(got, h)
		if len(got) != 1 {
			return
		}
		innerSaved := s.beginCommunityArea()
		s.rules().AreaVictims(AreaVictimQuery{Service: s, World: w, Terrain: terrain, Tick: 9, CellX: 1, CellZ: 1}, func(inner pool.Handle) {
			got = append(got, inner)
		})
		s.endCommunityArea(innerSaved)
	})
	s.endCommunityArea(outerSaved)

	want := []pool.Handle{handles[0], handles[0], handles[1], handles[2]}
	if len(got) != len(want) {
		t.Fatalf("nested victims=%v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("nested victims=%v, want %v", got, want)
		}
	}
}

func TestStrictAreaVictimsIgnoreEnabledCommunityTable(t *testing.T) {
	s, w, terrain, handles := communityAreaFixture(t, 3, StrictRules{})
	terrain.PlotAt(1, 1).SetOccupantB(int16(handles[2]))
	got := collectAreaVictims(s, w, terrain, 3)
	if len(got) != 1 || got[0] != handles[2] {
		t.Fatalf("Strict victims=%v, want only stock air word %d", got, handles[2])
	}
}
