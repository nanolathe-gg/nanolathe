package session

import "testing"

// TestPostEntryReadersSurviveAnEmptySetupRecord is the load contract of
// [08 R-SKIR-01 §2] "Save persistence": a load "restores the five [rule words]
// into the setup record and the map name", and nothing else. Every other setup
// row — controller, colour, side, ally group, nickname — is gone, while the
// player records the `Player%i` accounts carry are intact.
//
// So the test blanks the setup rows of a live battle, which is exactly the
// state a restored battle is in, and asserts that every reader that runs after
// battle entry answers the same thing it answered before. It fails on any
// reader that consults `Skirmish.Players[]`: before this unit the participation
// gate preferred those rows whenever the player count was non-zero (the count
// IS restored, from the Summary's `Players` item), so a restored observer slot
// read back as controller 0 — a human — and was counted as a participant, the
// allied pair split into two teams, and the score rows lost their colours.
func TestPostEntryReadersSurviveAnEmptySetupRecord(t *testing.T) {
	cat := minimalCatalogForStrict()
	fs := fsWithMap(t, "[GlobalHeader]\n{\n[Schema 0]\n{\nType=Network 1;\n[specials]\n{\n[special0]\n{\nspecialwhat=StartPos1;\nXPos=0;\nZPos=0;\n}\n[special1]\n{\nspecialwhat=StartPos2;\nXPos=10;\nZPos=10;\n}\n[special2]\n{\nspecialwhat=StartPos3;\nXPos=20;\nZPos=20;\n}\n[special3]\n{\nspecialwhat=StartPos4;\nXPos=30;\nZPos=30;\n}\n}\n}\n}\n")
	cfg := SkirmishConfig{MapName: "test", NumPlayers: 4}
	cfg.ApplyDefaults()
	// An allied human pair, one computer opponent, and an observer.
	cfg.Players[0].Controller, cfg.Players[0].AllyGroup, cfg.Players[0].Side, cfg.Players[0].Color = SkirmishControllerHuman, 1, 0, 4
	cfg.Players[1].Controller, cfg.Players[1].AllyGroup, cfg.Players[1].Side, cfg.Players[1].Color = SkirmishControllerComputer, 1, 1, 5
	cfg.Players[2].Controller, cfg.Players[2].AllyGroup, cfg.Players[2].Side, cfg.Players[2].Color = SkirmishControllerComputer, 2, 1, 6
	cfg.Players[3].Controller, cfg.Players[3].AllyGroup, cfg.Players[3].Side, cfg.Players[3].Color = SkirmishControllerObserver, 5, 0, 7
	s, err := NewSyntheticSkirmishForTest(fs, cat, cfg)
	if err != nil {
		t.Fatalf("NewSyntheticSkirmishForTest: %v", err)
	}

	type answers struct {
		team, side [4]int
		controller [4]uint8
		colour     [4]uint8
		eligible   [4]bool
		observer   [4]bool
		alliedTo0  [4]bool
		scoreRows  int
		scoreLogos [4]uint8
	}
	read := func() answers {
		var a answers
		for i := 0; i < 4; i++ {
			a.team[i] = s.teamForOwner(i)
			a.side[i], _ = s.sideForOwner(i)
			a.controller[i] = s.controllerForOwner(i)
			a.colour[i], _ = s.colourForOwner(i)
			a.eligible[i] = s.resultOwnerEligible(i)
			a.observer[i] = s.ownerIsObserver(i)
			a.alliedTo0[i] = s.ownersAllied(0, i)
		}
		for _, row := range s.collectScores(s.teamForOwner(0), false) {
			a.scoreRows++
			if row.Player >= 0 && row.Player < 4 {
				a.scoreLogos[row.Player] = row.Logo
			}
		}
		return a
	}

	before := read()
	// The battle is set up the way the test asked for it, so the "after" halves
	// below are asserting something.
	if before.team[0] != before.team[1] || before.team[0] == before.team[2] {
		t.Fatalf("setup: the allied pair must share a team and the opponent must not: %v", before.team)
	}
	if before.side[1] != 1 || before.colour[2] != 6 || before.controller[1] != 2 {
		t.Fatalf("setup: record fields not written: %+v", before)
	}
	if before.eligible[3] || !before.observer[3] {
		t.Fatalf("setup: the observer slot must not be a participant: %+v", before)
	}
	if before.scoreRows != 3 {
		t.Fatalf("setup: score rows = %d, want the three non-observing slots [08 R-CAMP-01 §7]", before.scoreRows)
	}

	// What a load leaves behind: the five rule words, the map name and the
	// player count survive in the setup record; the rows do not.
	s.Skirmish.Players = [10]SkirmishPlayer{}

	if after := read(); after != before {
		t.Fatalf("a reader consulted the setup record:\nbefore %+v\nafter  %+v", before, after)
	}
}
