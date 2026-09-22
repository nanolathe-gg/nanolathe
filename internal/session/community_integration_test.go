package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

func TestCommunityAreaIndexStartsEmptyAfterStrictSwitch(t *testing.T) {
	s := newLoopTestSession(t, 0)
	s.SetGameplay(gameplay.Community39)
	def := s.Catalog.Units["armcom"]
	h, err := s.Units.Create(def, 0, numeric.FixedFromInt(16), 0, numeric.FixedFromInt(16))
	if err != nil {
		t.Fatal(err)
	}
	u := s.Units.Unit(h)
	u.Move.ModeMirror = 2
	u.CachedOccupancyX, u.CachedOccupancyZ = 5, 5
	u.FootprintSizeX, u.FootprintSizeZ = 1, 1

	collect := func(x, z int32) []pool.Handle {
		var got []pool.Handle
		s.Rules.Combat.AreaVictims(combat.AreaVictimQuery{
			Service: s.Combat, World: s.Units, Terrain: s.World,
			Tick: 2, CellX: x, CellZ: z,
		}, func(victim pool.Handle) {
			got = append(got, victim)
		})
		return got
	}

	s.stepCommunityTickTail(1)
	if got := collect(5, 5); len(got) != 1 || got[0] != h {
		t.Fatalf("initial Community overflow victims = %v, want [%d]", got, h)
	}

	s.SetGameplay(gameplay.Strict31)
	u.CachedOccupancyX, u.CachedOccupancyZ = 6, 6
	s.SetGameplay(gameplay.Community39)
	if got := collect(5, 5); len(got) != 0 {
		t.Fatalf("first re-enabled tick consumed stale overflow victims %v", got)
	}
	if got := collect(6, 6); len(got) != 0 {
		t.Fatalf("first re-enabled tick rebuilt overflow eagerly: %v", got)
	}

	s.stepCommunityTickTail(2)
	if got := collect(6, 6); len(got) != 1 || got[0] != h {
		t.Fatalf("post-tail Community overflow victims = %v, want [%d]", got, h)
	}
}

func TestCommunitySchemaSuppressionSkipsOnlyFallbackCommanderValidation(t *testing.T) {
	cat := communitySchemaCatalog()
	delete(cat.Units, content.CanonicalKey("ARMCOM"))
	s := newCommunitySchemaSession(cat)
	setSchemaPlayers(s, 2)
	cfg := SkirmishConfig{NumPlayers: 2, Location: 1}
	cfg.Players[0] = SkirmishPlayer{Controller: SkirmishControllerHuman, Side: 0}
	cfg.Players[1] = SkirmishPlayer{Controller: SkirmishControllerComputer, Side: 1}
	m := &mission.Mission{
		Units: []mission.UnitPlacement{{UnitName: "CaseUnit", Player: 1}},
		Specials: []mission.Special{
			{Kind: 1, ID: 0, X: 32, Z: 32},
			{Kind: 1, ID: 1, X: 64, Z: 64},
		},
	}
	assignment := map[int]int{0: 0, 1: 1}
	s.configureCommunitySchemaStarts(cfg, m, []int{0, 1}, assignment)

	if err := validateSkirmishCommanders(cat, cfg, s, assignment); err != nil {
		t.Fatalf("suppressed missing fallback commander: %v", err)
	}
	badSide := cfg
	badSide.Players[0].Side = len(cat.Sides)
	if err := validateSkirmishCommanders(cat, badSide, s, assignment); err == nil {
		t.Fatal("schema suppression bypassed the roster side-index validation")
	}
	if err := skirmishReconstructUnits(s, cfg, m); err != nil {
		t.Fatalf("schema replacement with missing fallback commander: %v", err)
	}
	if got := s.Units.LiveCountForPlayer(0); got != 1 {
		t.Fatalf("schema replacement owner 0 live=%d, want 1", got)
	}
	if got := s.Units.LiveCountForPlayer(1); got != 1 {
		t.Fatalf("unsuppressed fallback owner 1 live=%d, want 1", got)
	}

	s.Community.SchemaUnits = false
	if err := validateSkirmishCommanders(cat, cfg, s, assignment); err == nil {
		t.Fatal("Strict-shaped schema bypass accepted a missing fallback commander")
	}
}
