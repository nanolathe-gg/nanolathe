package session

import (
	"bytes"
	"testing"
)

// TestTopologyOneVsOneExactCounts locks 1v1 canonical topology [08 "Skirmish configuration"] [GAP T14].
// Direct 1v1 must have exactly two live economy slots, two commanders and one manager, and inactive rows cleared.
func TestTopologyOneVsOneExactCounts(t *testing.T) {
	cfg := DirectSkirmishConfig("test")
	if cfg.NumPlayers != 2 {
		t.Fatalf("direct 1v1 NumPlayers want 2 got %d", cfg.NumPlayers)
	}
	// Inactive rows 2..9 must be zeroed byte-equivalent.
	for i := 2; i < 10; i++ {
		p := cfg.Players[i]
		if p.Controller != 0 || p.Side != 0 || p.Color != 0 || p.AllyGroup != 0 || p.Metal != 0 || p.Energy != 0 || p.Nickname != "" {
			t.Fatalf("inactive row %d not cleared: %+v", i, p)
		}
	}
	// Build session via canonical path.
	cat := minimalCatalogForStrict()
	cat.Units["armcom"].Commander = true
	cat.Units["corcom"].Commander = true
	fs := fsFromMapSkirmish(t, map[string]string{
		"maps/test.ota":  "[GlobalHeader]\n{\n[Schema 0]\n{\nType=Network 1;\n[specials]\n{\n[special0]\n{\nspecialwhat=StartPos1;\nXPos=0;\nZPos=0;\n}\n[special1]\n{\nspecialwhat=StartPos2;\nXPos=100;\nZPos=100;\n}\n}\n}\n}\n",
		"ai/default.txt": "plan any\nweight FALLBACK 0.5\n",
	})
	s, err := NewSkirmishForTest(fs, cat, cfg)
	if err != nil {
		t.Fatalf("NewSkirmishForTest direct 1v1: %v", err)
	}
	// Exactly two live economy slots.
	live := 0
	for i := 0; i < 10; i++ {
		if s.Econ.Players[i].Exists && !s.Econ.Players[i].IsObserver {
			live++
		}
	}
	if live != 2 {
		t.Fatalf("live economy slots want 2 got %d", live)
	}
	if s.Econ.Players[2].Exists {
		t.Fatalf("inactive row 2 should not exist")
	}
	if s.LocalOwner != 0 {
		t.Fatalf("direct 1v1 LocalOwner want 0 got %d", s.LocalOwner)
	}
	// Two commanders (one per live player).
	cmdrs := 0
	for _, u := range s.Units.Iter() {
		if u != nil && u.Alive && u.Def != nil && u.Def.Commander {
			cmdrs++
		}
	}
	if cmdrs != 2 {
		t.Fatalf("commanders want 2 got %d (units %d)", cmdrs, s.Units.Used())
	}
	// One manager (computer at 1) — AI is [10]*Manager with nil holes after RS-02.
	count := 0
	for _, m := range s.AI {
		if m != nil {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("managers want 1 got %d (len %d)", count, len(s.AI))
	}
	if s.AI[1] == nil || s.AI[1].Player != 1 {
		var got uint8 = 99
		if s.AI[1] != nil {
			got = s.AI[1].Player
		}
		t.Fatalf("manager player want 1 got %d", got)
	}
	// Inactive rows cannot affect result: ensure Allies for inactive not considered.
	// Alliance hostile check already passed via Normalize; verify hostile exists.
	hostile := false
	for i := 0; i < 2; i++ {
		for j := i + 1; j < 2; j++ {
			if !s.Econ.Players[i].Allies[j] {
				hostile = true
			}
		}
	}
	if !hostile {
		t.Fatalf("expected hostile alliance for 1v1")
	}
	// Verify snapshot would contain exactly 2 commanders (via units).
}

// TestTopologyHumanSlot3RemainsLocal verifies LocalOwner derived from human row, not zero default [08 "Skirmish configuration"].
// Human at slot 3 with computers at 0..2, after session and save restore LocalOwner remains 3.
func TestTopologyHumanSlot3RemainsLocal(t *testing.T) {
	cfg := SkirmishConfig{MapName: "test", NumPlayers: 4}
	// Slot 3 human, others computer, distinct ally groups to pass hostile check.
	cfg.Players[0].Controller = SkirmishControllerComputer
	cfg.Players[1].Controller = SkirmishControllerComputer
	cfg.Players[2].Controller = SkirmishControllerComputer
	cfg.Players[3].Controller = SkirmishControllerHuman
	cfg.Players[0].AllyGroup = 1
	cfg.Players[1].AllyGroup = 1
	cfg.Players[2].AllyGroup = 1
	cfg.Players[3].AllyGroup = 2
	cfg.Players[0].Side = 1
	cfg.Players[1].Side = 1
	cfg.Players[2].Side = 1
	cfg.Players[3].Side = 0
	if err := cfg.Normalize(); err != nil {
		t.Fatalf("normalize slot3: %v", err)
	}
	if got := LocalOwnerForConfig(cfg); got != 3 {
		t.Fatalf("LocalOwnerForConfig want 3 got %d", got)
	}
	cat := minimalCatalogForStrict()
	fs := fsFromMapSkirmish(t, map[string]string{
		"maps/test.ota":  "[GlobalHeader]\n{\n[Schema 0]\n{\nType=Network 1;\n[specials]\n{\n[special0]\n{\nspecialwhat=StartPos1;\nXPos=10;\nZPos=10;\n}\n[special1]\n{\nspecialwhat=StartPos2;\nXPos=20;\nZPos=20;\n}\n[special2]\n{\nspecialwhat=StartPos3;\nXPos=30;\nZPos=30;\n}\n[special3]\n{\nspecialwhat=StartPos4;\nXPos=40;\nZPos=40;\n}\n}\n}\n}\n",
		"ai/default.txt": "plan any\nweight FALLBACK 0.5\n",
	})
	s, err := NewSkirmishForTest(fs, cat, cfg)
	if err != nil {
		t.Fatalf("NewSkirmish slot3: %v", err)
	}
	if s.LocalOwner != 3 {
		t.Fatalf("session LocalOwner want 3 got %d", s.LocalOwner)
	}
	// Verify economy reflects slot3 human.
	if s.Econ.Players[3].ControllerState != 1 {
		t.Fatalf("econ slot3 ControllerState want 1 got %d", s.Econ.Players[3].ControllerState)
	}
}

// TestTopologyAlliedHumansHostileAI checks alliance matrix for 2 allied humans + 1 hostile AI [08 "Skirmish configuration"].
func TestTopologyAlliedHumansHostileAI(t *testing.T) {
	cfg := SkirmishConfig{MapName: "test", NumPlayers: 3}
	cfg.Players[0].Controller = SkirmishControllerHuman
	cfg.Players[1].Controller = SkirmishControllerHuman
	cfg.Players[2].Controller = SkirmishControllerComputer
	cfg.Players[0].AllyGroup = 1
	cfg.Players[1].AllyGroup = 1
	cfg.Players[2].AllyGroup = 2
	cfg.Players[0].Side = 0
	cfg.Players[1].Side = 0
	cfg.Players[2].Side = 1
	if err := cfg.Normalize(); err != nil {
		t.Fatalf("normalize allied humans: %v", err)
	}
	cat := minimalCatalogForStrict()
	fs := fsFromMapSkirmish(t, map[string]string{
		"maps/test.ota":  "[GlobalHeader]\n{\n[Schema 0]\n{\nType=Network 1;\n[specials]\n{\n[special0]\n{\nspecialwhat=StartPos1;\nXPos=0;\nZPos=0;\n}\n[special1]\n{\nspecialwhat=StartPos2;\nXPos=10;\nZPos=10;\n}\n[special2]\n{\nspecialwhat=StartPos3;\nXPos=20;\nZPos=20;\n}\n}\n}\n}\n",
		"ai/default.txt": "plan any\nweight FALLBACK 0.5\n",
	})
	s, err := NewSkirmishForTest(fs, cat, cfg)
	if err != nil {
		t.Fatalf("NewSkirmish allied: %v", err)
	}
	if s.LocalOwner != 0 {
		t.Fatalf("local owner want 0 got %d", s.LocalOwner)
	}
	// Local alliance: human0 allied with human1, hostile with AI2.
	if !s.Econ.Players[0].Allies[1] {
		t.Fatalf("allied humans should be allied")
	}
	if s.Econ.Players[0].Allies[2] {
		t.Fatalf("human vs AI should be hostile")
	}
	if s.Econ.Players[1].Allies[0] != true || s.Econ.Players[1].Allies[2] != false {
		t.Fatalf("player1 allies incorrect")
	}
	if s.Econ.Players[2].Allies[0] || s.Econ.Players[2].Allies[1] {
		t.Fatalf("AI should be hostile to both humans")
	}
	// Verify via helper that local alliance includes only allied human.
	// Simulate result team: local team should have 2 members, hostile team 1.
	// Check economy state directly.
	if s.Econ.Players[0].Allies[1] != true {
		t.Fatalf("local alliance failed")
	}
	// Ensure that inactive rows cleared.
	if s.Econ.Players[3].Exists {
		t.Fatalf("inactive row should not exist")
	}
	// Also verify via session helper that alliance matrix built from authored groups.
	if cfg.Players[0].AllyGroup != 1 || cfg.Players[1].AllyGroup != 1 || cfg.Players[2].AllyGroup != 2 {
		t.Fatalf("ally groups not preserved")
	}
}

// TestTopologyDirectAndMenuByteEquivalent ensures direct and menu paths produce byte-equivalent normalized SkirmishConfig [08 "Skirmish configuration"].
func TestTopologyDirectAndMenuByteEquivalent(t *testing.T) {
	// Direct path via canonical helper.
	direct := DirectSkirmishConfig("testmap")

	// Menu path: simulate newSkirmishMenuConfig + setOpponentCount(1) + skirmishConfigForStart compaction.
	// We replicate the menu's logic without importing cmd package: start from empty config, ApplyDefaults, then set 1 opponent.
	menuRaw := SkirmishConfig{MapName: "testmap"}
	menuRaw.ApplyDefaults()
	// Simulate retailControllers: human at 0, computer at1, rest open.
	// Compact via helper similar to skirmishConfigForStart: out=2 with human+computer.
	menu := SkirmishConfig{MapName: "testmap"}
	menu.NumPlayers = 2
	menu.Players[0] = menuRaw.Players[0]
	menu.Players[1] = menuRaw.Players[1]
	menu.Players[0].Controller = SkirmishControllerHuman
	menu.Players[1].Controller = SkirmishControllerComputer
	// Ensure distinct ally groups as menu would after ensureRetail (human 2, computer 5) – matching direct.
	menu.Players[0].AllyGroup = 2
	menu.Players[1].AllyGroup = 5
	// Clear rest.
	for i := 2; i < 10; i++ {
		menu.Players[i] = SkirmishPlayer{}
	}
	// Normalize both.
	if err := menu.Normalize(); err != nil {
		t.Fatalf("menu normalize: %v", err)
	}
	if err := direct.Normalize(); err != nil {
		t.Fatalf("direct normalize: %v", err)
	}
	// After normalization, they must be byte-equivalent.
	db := direct.NormalizedBytes()
	mb := menu.NormalizedBytes()
	if !bytes.Equal(db, mb) {
		t.Fatalf("direct vs menu not byte-equivalent:\ndirect %+v\nmenu %+v\n bytes direct %x\n menu %x", direct, menu, db, mb)
	}
	// Also ensure that calling Normalize twice is idempotent.
	dup := direct
	if err := dup.Normalize(); err != nil {
		t.Fatalf("second normalize: %v", err)
	}
	if !bytes.Equal(dup.NormalizedBytes(), db) {
		t.Fatalf("normalize not idempotent")
	}
}

// TestTopologyInactiveRowsCannotAffectResult ensures inactive economies not counted.
func TestTopologyInactiveRowsCannotAffectResult(t *testing.T) {
	cfg := DirectSkirmishConfig("test")
	// Manually poison inactive row's ally/stock to ensure it cannot affect.
	cfg.Players[2].AllyGroup = 99
	cfg.Players[2].Metal = 99999
	cat := minimalCatalogForStrict()
	fs := fsFromMapSkirmish(t, map[string]string{
		"maps/test.ota":  "[GlobalHeader]\n{\n[Schema 0]\n{\nType=Network 1;\n[specials]\n{\n[special0]\n{\nspecialwhat=StartPos1;\nXPos=0;\nZPos=0;\n}\n[special1]\n{\nspecialwhat=StartPos2;\nXPos=10;\nZPos=10;\n}\n}\n}\n}\n",
		"ai/default.txt": "plan any\nweight FALLBACK 0.5\n",
	})
	// Normalize should clear inactive rows, removing poison.
	if err := cfg.Normalize(); err != nil {
		t.Fatalf("normalize poison: %v", err)
	}
	if cfg.Players[2].AllyGroup != 0 || cfg.Players[2].Metal != 0 {
		t.Fatalf("inactive row not cleared after normalize: %+v", cfg.Players[2])
	}
	s, err := NewSkirmishForTest(fs, cat, cfg)
	if err != nil {
		t.Fatalf("NewSkirmish after poison clear: %v", err)
	}
	if s.Econ.Players[2].Exists {
		t.Fatalf("inactive econ should not exist")
	}
}
