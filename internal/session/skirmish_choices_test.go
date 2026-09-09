package session

import "testing"

// Side and colour zero are explicit row choices after preference initialization
// [08 R-SKIR-01 §1, §2]. Battle entry must copy them to the player and commander.
func TestSkirmishExplicitZeroChoicesReachBattle(t *testing.T) {
	cfg := DirectSkirmishConfig("test")
	cfg.Players[0].Side, cfg.Players[0].Color = 1, 4
	cfg.Players[1].Side, cfg.Players[1].Color = 0, 0
	for i := 0; i < 2; i++ {
		cfg.ApplyDefaults()
		if err := cfg.Normalize(); err != nil {
			t.Fatal(err)
		}
		if cfg.Players[1].Side != 0 || cfg.Players[1].Color != 0 {
			t.Fatalf("explicit choices rewritten: %+v", cfg.Players[1])
		}
	}
	fs := fsFromMapSkirmish(t, map[string]string{
		"maps/test.ota":  "[GlobalHeader]{[Schema 0]{Type=Network 1;[specials]{[special0]{specialwhat=StartPos1;XPos=32;ZPos=32;}[special1]{specialwhat=StartPos2;XPos=128;ZPos=128;}}}}",
		"ai/default.txt": "plan any\nweight FALLBACK 0.5\n",
	})
	s, err := NewSyntheticSkirmishForTest(fs, strictMinimalCatalog(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if p := s.Econ.Players[1]; p.Side != 0 || p.Logo != 0 {
		t.Fatalf("player choices = side %d colour %d", p.Side, p.Logo)
	}
	found := false
	for _, u := range s.Units.Iter() {
		if u != nil && u.Alive && u.Owner == 1 {
			found = true
			if u.Def.UnitName != "armcom" {
				t.Fatalf("chosen Arm commander = %s", u.Def.UnitName)
			}
		}
	}
	if !found {
		t.Fatal("chosen player's commander missing")
	}
}
