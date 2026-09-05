package session

import "testing"

// TestExportedPlayerRecordReadersSurviveAnEmptySetupRecord is the same load
// contract TestPostEntryReadersSurviveAnEmptySetupRecord asserts for the
// package-internal readers [08 R-SKIR-01 §2] "Save persistence", applied to the
// two exported ones.
//
// The three callers outside this package — the headless runner's watching gate
// and the two side lookups in cmd/nanolathe — could not reach the internal
// readers and were indexing `Skirmish.Players[owner]` instead. A load restores
// into the setup record only the five rule words and the map name, so every
// setup row reads back as controller 0, side 0: a restored Core player got the
// Arm interface and a restored observer was treated as a participant. Blanking
// the setup rows of a live battle is exactly that state.
func TestExportedPlayerRecordReadersSurviveAnEmptySetupRecord(t *testing.T) {
	cat := minimalCatalogForStrict()
	fs := fsWithMap(t, "[GlobalHeader]\n{\n[Schema 0]\n{\nType=Network 1;\n[specials]\n{\n[special0]\n{\nspecialwhat=StartPos1;\nXPos=0;\nZPos=0;\n}\n[special1]\n{\nspecialwhat=StartPos2;\nXPos=10;\nZPos=10;\n}\n[special2]\n{\nspecialwhat=StartPos3;\nXPos=20;\nZPos=20;\n}\n[special3]\n{\nspecialwhat=StartPos4;\nXPos=30;\nZPos=30;\n}\n}\n}\n}\n")
	cfg := SkirmishConfig{MapName: "test", NumPlayers: 4}
	cfg.ApplyDefaults()
	// Slot 1 is a Core computer; slot 3 is the observer. Both are the cases the
	// setup-row readers got wrong after a load.
	cfg.Players[0].Controller, cfg.Players[0].AllyGroup, cfg.Players[0].Side = SkirmishControllerHuman, 1, 0
	cfg.Players[1].Controller, cfg.Players[1].AllyGroup, cfg.Players[1].Side = SkirmishControllerComputer, 1, 1
	cfg.Players[2].Controller, cfg.Players[2].AllyGroup, cfg.Players[2].Side = SkirmishControllerComputer, 2, 1
	cfg.Players[3].Controller, cfg.Players[3].AllyGroup, cfg.Players[3].Side = SkirmishControllerObserver, 5, 0
	s, err := NewSyntheticSkirmishForTest(fs, cat, cfg)
	if err != nil {
		t.Fatalf("NewSyntheticSkirmishForTest: %v", err)
	}

	type answers struct {
		side     [4]int
		sideOK   [4]bool
		observer [4]bool
	}
	read := func() answers {
		var a answers
		for i := 0; i < 4; i++ {
			a.side[i], a.sideOK[i] = s.SideForOwner(i)
			a.observer[i] = s.OwnerIsObserver(i)
		}
		return a
	}

	before := read()
	if before.side[1] != 1 || !before.sideOK[1] {
		t.Fatalf("SideForOwner(1) = (%d,%v) before the blank, want the authored Core side (1,true)", before.side[1], before.sideOK[1])
	}
	if !before.observer[3] {
		t.Fatal("OwnerIsObserver(3) is false before the blank; the fixture's observer never reached the record")
	}
	if before.observer[0] {
		t.Fatal("OwnerIsObserver(0) is true; the human seat is not an observer")
	}

	// The restored state: the setup rows are gone, the player records are not.
	s.Skirmish.Players = [10]SkirmishPlayer{}

	if after := read(); after != before {
		t.Fatalf("exported readers changed when the setup record was blanked:\n before %+v\n after  %+v\n[08 R-SKIR-01 §2] \"Save persistence\"", before, after)
	}
}
