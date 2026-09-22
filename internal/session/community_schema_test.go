package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func communitySchemaCatalog() *content.Catalog {
	defs := []*content.UnitDef{
		{UnitName: "ARMCOM", MaxDamage: 3000, BMCode: 1, FootprintX: 1, FootprintZ: 1},
		{UnitName: "CORCOM", MaxDamage: 3000, BMCode: 1, FootprintX: 1, FootprintZ: 1},
		{UnitName: "CaseUnit", MaxDamage: 100, BMCode: 1, FootprintX: 1, FootprintZ: 1},
		{UnitName: "Actor", MaxDamage: 100, BMCode: 1, FootprintX: 1, FootprintZ: 1, CanMove: true, CanGuard: true},
		{UnitName: "Target", MaxDamage: 100, BMCode: 1, FootprintX: 1, FootprintZ: 1},
	}
	cat := &content.Catalog{
		Units: map[string]*content.UnitDef{},
		Sides: []*content.SideDef{{Name: "ARM", Commander: "ARMCOM"}, {Name: "CORE", Commander: "CORCOM"}},
	}
	for _, def := range defs {
		def.CanonicalKey = content.CanonicalKey(def.UnitName)
		cat.Units[def.CanonicalKey] = def
	}
	installFixtureCOB(cat)
	return cat
}

func newCommunitySchemaSession(cat *content.Catalog) *Session {
	w := newSessionFixtureWorld(16, cat)
	sim := rng.NewSimulation(17)
	w.SetSimulationRNG(&sim)
	return &Session{
		Community: community.Features{SchemaUnits: true},
		Catalog:   cat,
		World:     minimalTerrain(),
		Units:     w,
		Econ:      &economy.Service{},
	}
}

func setSchemaPlayers(s *Session, count int) {
	for i := 0; i < count; i++ {
		s.Econ.Players[i].Exists = true
		if i == 0 {
			s.Econ.Players[i].ControllerState = 1
		} else {
			s.Econ.Players[i].ControllerState = 2
		}
	}
}

func TestCommunitySchemaDefinitionAndPlacementFields(t *testing.T) {
	cat := communitySchemaCatalog()
	s := newCommunitySchemaSession(cat)
	cell := s.World.PlotAt(1, 1)
	cell.SetHeight(37)
	s.communitySchema.active = true
	s.communitySchema.mission = &mission.Mission{Units: []mission.UnitPlacement{
		{UnitName: "caseunit", X: 16 << 16, Y: 91 << 16, Z: 16 << 16},
		{UnitName: "CaseUnit", X: 16 << 16, Y: 91 << 16, Z: 16 << 16, HealthPercentage: 1, Angle: 12345, Immune: true},
		{UnitName: "CaseUnit", X: -16 << 16, Y: 73 << 16, Z: 16 << 16},
	}}

	if got := s.spawnCommunitySchemaUnit(0, 0); got != nil {
		t.Fatal("case-folded definition name matched the byte-exact schema scan")
	}
	onMap := s.spawnCommunitySchemaUnit(1, 0)
	if onMap == nil {
		t.Fatal("exact definition name did not spawn")
	}
	if onMap.Y != 37<<16 {
		t.Fatalf("on-map Y = %d, want terrain cell height %d", onMap.Y, 37<<16)
	}
	if onMap.Health != onMap.MaxHealth || onMap.Move.Heading == 12345 || onMap.Flags&units.ImmunityStatus != 0 {
		t.Fatalf("ignored placement fields changed unit: health=%d/%d heading=%d flags=%#x", onMap.Health, onMap.MaxHealth, onMap.Move.Heading, onMap.Flags)
	}
	offMap := s.spawnCommunitySchemaUnit(2, 0)
	if offMap == nil || offMap.Y != 73<<16 {
		t.Fatalf("off-map authored Y = %v, want %d", offMap, 73<<16)
	}
}

func TestCommunitySchemaAttemptSuppressesCommanderAndStrictBypasses(t *testing.T) {
	entry := func(enabled bool) *Session {
		cat := communitySchemaCatalog()
		s := newCommunitySchemaSession(cat)
		s.Community.SchemaUnits = enabled
		setSchemaPlayers(s, 2)
		cfg := SkirmishConfig{NumPlayers: 2, Location: 1}
		cfg.Players[0] = SkirmishPlayer{Controller: SkirmishControllerHuman, Side: 0}
		cfg.Players[1] = SkirmishPlayer{Controller: SkirmishControllerComputer, Side: 1}
		m := &mission.Mission{
			Type:  mission.TypeSkirmish,
			Units: []mission.UnitPlacement{{UnitName: "MISSING", Player: 1}},
			Specials: []mission.Special{
				{Kind: 1, ID: 0, X: 32, Z: 32},
				{Kind: 1, ID: 1, X: 64, Z: 64},
			},
		}
		if err := skirmishReconstructUnits(s, cfg, m); err != nil {
			t.Fatal(err)
		}
		return s
	}

	communitySession := entry(true)
	if got := communitySession.Units.LiveCountForPlayer(0); got != 0 {
		t.Fatalf("attempted missing schema unit left owner 0 commander: live=%d", got)
	}
	if got := communitySession.Units.LiveCountForPlayer(1); got != 1 {
		t.Fatalf("unmatched owner 1 live=%d, want commander", got)
	}
	strictSession := entry(false)
	if got := strictSession.Units.LiveCountForPlayer(0); got != 1 {
		t.Fatalf("Strict applied schema suppression: owner 0 live=%d, want commander", got)
	}
	if strictSession.communitySchema.active {
		t.Fatal("Strict retained active schema state")
	}
}

func TestCommunitySchemaNeutralRandomStartSwap(t *testing.T) {
	s := newCommunitySchemaSession(communitySchemaCatalog())
	cfg := SkirmishConfig{NumPlayers: 2, Location: 0}
	cfg.Players[0].Controller = SkirmishControllerHuman
	cfg.Players[1].Controller = SkirmishControllerComputer
	m := &mission.Mission{Units: []mission.UnitPlacement{{UnitName: "CaseUnit", Player: 11}}}
	assignment := map[int]int{0: 1, 1: 0}
	s.configureCommunitySchemaStarts(cfg, m, []int{0, 1}, assignment)
	if assignment[0] != 0 || assignment[1] != 1 {
		t.Fatalf("random neutral correction assignment = %v, want human 0 AI 1", assignment)
	}
	if s.communitySchema.neutralOwner != 1 || s.communitySchema.playerByStart[1] != 1 {
		t.Fatalf("neutral mapping owner=%d starts=%v, want AI owner 1 at final position", s.communitySchema.neutralOwner, s.communitySchema.playerByStart)
	}
	if !s.spawnInitialCommunitySchema(1, 1) || s.Units.LiveCountForPlayer(1) != 1 {
		t.Fatal("Player 11 did not spawn for the designated neutral computer")
	}
}

func TestCommunitySchemaPlayerTargetsStartPosition(t *testing.T) {
	s := newCommunitySchemaSession(communitySchemaCatalog())
	m := &mission.Mission{Units: []mission.UnitPlacement{{UnitName: "CaseUnit", Player: 1}}}
	var owners [10]int8
	for i := range owners {
		owners[i] = -1
	}
	owners[0] = 3
	s.initCommunitySchema(m, owners, -1)
	if got := s.communitySchema.communitySchemaOwner(m.Units[0]); got != 3 {
		t.Fatalf("Player 1 resolved to owner %d, want owner 3 assigned to start position 1", got)
	}
}

func TestCommunitySchemaInitialScriptsUseSparsePlacementReferences(t *testing.T) {
	cat := communitySchemaCatalog()
	s := newCommunitySchemaSession(cat)
	setSchemaPlayers(s, 2)
	cfg := SkirmishConfig{NumPlayers: 2, Location: 1}
	cfg.Players[0] = SkirmishPlayer{Controller: SkirmishControllerHuman, Side: 0}
	cfg.Players[1] = SkirmishPlayer{Controller: SkirmishControllerComputer, Side: 1}
	m := &mission.Mission{
		Type: mission.TypeSkirmish,
		Units: []mission.UnitPlacement{
			{UnitName: "Actor", Player: 1, InitialMission: "g target"},
			{UnitName: "MISSING", Player: 1, Ident: "hole"},
			{UnitName: "Target", Player: 1, Ident: "target"},
		},
		Specials: []mission.Special{{Kind: 1, ID: 0}, {Kind: 1, ID: 1}},
	}
	if err := skirmishReconstructUnits(s, cfg, m); err != nil {
		t.Fatal(err)
	}
	var actor, target *units.Unit
	for _, u := range s.Units.Iter() {
		switch u.Def.UnitName {
		case "Actor":
			actor = u
		case "Target":
			target = u
		}
	}
	if actor == nil || target == nil {
		t.Fatalf("initial pass units actor=%v target=%v", actor, target)
	}
	found := false
	for _, node := range orders.QueueForUnit(actor).Primary() {
		if node.Target == target.Handle {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("initial script did not resolve sparse Ident target: %v", orders.QueueForUnit(actor).Primary())
	}
}

func TestCommunitySchemaDeferredDueOrderAndNoInitialMission(t *testing.T) {
	s := newCommunitySchemaSession(communitySchemaCatalog())
	m := &mission.Mission{Units: []mission.UnitPlacement{
		{UnitName: "CaseUnit", Player: 1, X: 10 << 16, CreationCountdown: 1, InitialMission: "s"},
		{UnitName: "CaseUnit", Player: 1, X: 20 << 16, CreationCountdown: 1, InitialMission: "s"},
		{UnitName: "CaseUnit", Player: 1, X: 30 << 16, CreationCountdown: 2, InitialMission: "s"},
	}}
	var owners [10]int8
	for i := range owners {
		owners[i] = -1
	}
	owners[0] = 0
	s.initCommunitySchema(m, owners, -1)
	s.stepCommunitySchema(29)
	if got := s.Units.LiveCountForPlayer(0); got != 0 {
		t.Fatalf("countdown fired before whole second: live=%d", got)
	}
	s.stepCommunitySchema(30)
	spawned := s.Units.Iter()
	if len(spawned) != 2 || spawned[0].X != 10<<16 || spawned[1].X != 20<<16 {
		t.Fatalf("equal-countdown authored order = %v", spawned)
	}
	for _, u := range spawned {
		q := orders.QueueForUnit(u)
		if q.LenPrimary()+q.LenSecondary() != 0 {
			t.Fatalf("deferred InitialMission ran for unit at X=%d: %v", u.X, q.Primary())
		}
	}
	s.stepCommunitySchema(60)
	if got := s.Units.LiveCountForPlayer(0); got != 3 {
		t.Fatalf("second deferred deadline live=%d, want 3", got)
	}
	if got := len(s.communitySchema.diagnostics); got != 3 {
		t.Fatalf("attempt diagnostics=%d, want 3", got)
	}
}

func TestCommunitySchemaTickTailRunsAfterGameplayCommand(t *testing.T) {
	s := newCommunitySchemaSession(communitySchemaCatalog())
	m := &mission.Mission{Units: []mission.UnitPlacement{{
		UnitName: "CaseUnit", Player: 1, CreationCountdown: 1,
	}}}
	var owners [10]int8
	for i := range owners {
		owners[i] = -1
	}
	owners[0] = 0
	s.initCommunitySchema(m, owners, -1)
	if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanGameplay, Gameplay: gameplay.Strict31}); err != nil {
		t.Fatal(err)
	}
	s.phaseNetwork(30)
	s.stepCommunityTickTail(30)
	if got := s.Units.LiveCountForPlayer(0); got != 0 {
		t.Fatalf("deferred spawn ran before the phase-1 gameplay command: live=%d", got)
	}
	if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanGameplay, Gameplay: gameplay.Community39}); err != nil {
		t.Fatal(err)
	}
	s.phaseNetwork(31)
	s.stepCommunityTickTail(31)
	if got := s.Units.LiveCountForPlayer(0); got != 1 {
		t.Fatalf("deferred spawn did not resume after Community command: live=%d", got)
	}
}
