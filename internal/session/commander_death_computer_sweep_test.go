package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// computerOwnerSweepSession is a two-slot lobby skirmish whose slot 1 is a
// COMPUTER player, unlike newLobbyEndRuleSession's two humans. The owner sweep
// of [08 R-SKIR-01 §3] splits on the owner record's controller byte, and slot 1
// is the only row in the suite that carries the computer value 2.
func computerOwnerSweepSession(t *testing.T, commanderDeath int, extra int) (*Session, []pool.Handle) {
	t.Helper()
	cat := minimalCatalogForStrict()
	ordinary := &content.UnitDef{
		DefinitionHeader: content.DefinitionHeader{CanonicalKey: "corllt"},
		UnitName:         "corllt",
		MaxDamage:        500,
		FootprintX:       1,
		FootprintZ:       1,
	}
	cat.Units[ordinary.CanonicalKey] = ordinary
	fs := fsWithMap(t, "[GlobalHeader]\n{\n[Schema 0]\n{\nType=Network 1;\n[specials]\n{\n[special0]\n{\nspecialwhat=StartPos1;\nXPos=0;\nZPos=0;\n}\n[special1]\n{\nspecialwhat=StartPos2;\nXPos=100;\nZPos=100;\n}\n}\n}\n}\n")
	cfg := SkirmishConfig{MapName: "test", NumPlayers: 2}
	cfg.ApplyDefaults()
	cfg.CommanderDeath = commanderDeath
	cfg.Players[0].Controller = SkirmishControllerHuman
	cfg.Players[1].Controller = SkirmishControllerComputer
	cfg.Players[0].AllyGroup = 2
	s, err := NewSyntheticSkirmishForTest(fs, cat, cfg)
	if err != nil {
		t.Fatalf("NewSyntheticSkirmishForTest: %v", err)
	}
	s.State = StateBattle
	s.RegisterAll()
	var out []pool.Handle
	for i := 0; i < extra; i++ {
		h, err := s.Units.Create(ordinary, 1, numeric.Fixed(int64(120+8*i)<<16), 0, numeric.Fixed(120<<16))
		if err != nil {
			t.Fatalf("create computer unit %d: %v", i, err)
		}
		out = append(out, h)
	}
	return s, out
}

// TestComputerCommanderDeathSweepsThroughTheDamagePath pins which arm of the
// owner sweep a COMPUTER player takes. [08 R-SKIR-01 §3] "Trigger site" splits
// on the owner record: a unit whose owner is inactive or not human/computer "is
// destroyed silently (death kind 3, dying bit set, kill record filed)",
// otherwise it "receives 30000 damage from itself with damage kind 3 — the
// ordinary damage path, so armour and death animations apply and the units die
// over the following ticks, not in the same tick".
//
// A computer row carries controller byte 2, so it takes the damage arm. The two
// arms differ observably in exactly one place, and it is not the dying bit —
// both set that: the silent arm never touches health, while the damage arm
// drives it below zero and stamps the victim's own slot as the damage side. The
// test reads those, and it reads them at the sweep boundary, where the units are
// marked but still alive: the sweep itself removes nothing, and the ordinary
// death lifecycle frees them on a later slot visit.
//
// WU-19-113's play-test report read the same-tick disappearance of a computer
// player's units as the silent arm being taken. It is not: the branch is taken
// correctly (verified against a retail-composed `ashap plateau` skirmish, where
// the row reads Exists/not-observer/controller 2 at the sweep). The units go in
// one tick because phase 2's slot walk is live — the sweep runs from the
// commander's own slot-end death handling, so every swept unit in a HIGHER pool
// slot reaches its own slot-end handling inside the same walk. That is retail's
// structure too, and the ordering is [01 §4.4]'s, not this sweep's.
func TestComputerCommanderDeathSweepsThroughTheDamagePath(t *testing.T) {
	s, extra := computerOwnerSweepSession(t, int(CommanderDeathEnds), 2)
	if p := s.Econ.Players[1]; !p.Exists || p.IsObserver || p.ControllerState != 2 {
		t.Fatalf("computer row = Exists:%v IsObserver:%v ControllerState:%d, want an active computer record [08 R-SKIR-01 §2]",
			p.Exists, p.IsObserver, p.ControllerState)
	}

	// Run the sweep at its own boundary so the phase-2 walk cannot free the
	// marked units before they can be inspected.
	commander := pool.Handle(commanderHandles(s)[1][0])
	killCommander(t, s, 1)
	if res := s.Units.FinalizeDeath(commander, 0); !res.Freed {
		t.Fatal("the computer player's commander was not finalized")
	}
	s.sweepOwnerAfterCommanderDeath(1, 0)

	for i, h := range extra {
		u := s.Units.Unit(h)
		if u == nil {
			t.Fatalf("computer unit %d was removed by the sweep itself", i)
		}
		if !u.Alive || !u.Dying {
			t.Fatalf("computer unit %d alive=%v dying=%v, want marked but not yet torn down [08 R-SKIR-01 §3]", i, u.Alive, u.Dying)
		}
		if u.Health > 0 {
			t.Fatalf("computer unit %d kept health %d: the silent arm was taken, but a controlled owner's units take 30000 self-damage [08 R-SKIR-01 §3]", i, u.Health)
		}
		if u.LastDamageSide != 1 {
			t.Fatalf("computer unit %d damage side = %d, want its own owner 1 — the damage comes from the unit itself [08 R-SKIR-01 §3]", i, u.LastDamageSide)
		}
		if combat.Cause(u.LastDamageCause) != combat.CauseSelfDestruct {
			t.Fatalf("computer unit %d damage cause = %d, want kind 3 [08 R-SKIR-01 §3]", i, u.LastDamageCause)
		}
	}

	// The teardown is the ordinary death lifecycle's, on a later slot visit, and
	// it drives the live count the defeat and victory predicates read.
	if live := s.Units.LiveCountForPlayer(1); live == 0 {
		t.Fatal("the sweep decremented the live count itself; teardown belongs to phase 2 [01 §4.4]")
	}
	s.Step(1)
	if live := s.Units.LiveCountForPlayer(1); live != 0 {
		t.Fatalf("computer live count %d after the sweep's units were finalized, want zero [08 R-SKIR-01 §3]", live)
	}
}

// TestComputerCommanderDeathRuleZeroKeepsEveryUnit is the other arm of the rule
// word: "Rule 0 skips the sweep entirely: the player keeps every unit and
// nothing else happens on commander death" [08 R-SKIR-01 §3]. It is the control
// for the test above — without it, a sweep that never ran would be
// indistinguishable from one that took the wrong branch.
func TestComputerCommanderDeathRuleZeroKeepsEveryUnit(t *testing.T) {
	s, extra := computerOwnerSweepSession(t, int(CommanderDeathContinues), 2)
	killCommander(t, s, 1)
	stepLobbyThrough(t, s, 0, 60)
	for i, h := range extra {
		if u := s.Units.Unit(h); u == nil || !u.Alive || u.Dying {
			t.Fatalf("computer unit %d was swept under rule 0, which skips the sweep entirely [08 R-SKIR-01 §3]", i)
		}
	}
}
