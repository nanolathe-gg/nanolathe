//go:build retail

package session

import (
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/checkpoint"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport/retailcat"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// perSeatConfig is the admitted configuration of a three-seat battle on one
// host: the human at 0 and two computers at 1 and 2 with the given kinds and
// difficulty words (docs/DESIGN_MULTIPLAYER.md §6.6).
func perSeatConfig(t *testing.T, fs vfs.FSOps, cat *content.Catalog, mode gameplay.Mode, kinds [2]ai.Controller, words [2]uint8) EffectiveMatchConfig {
	t.Helper()
	cfg := DirectSkirmishConfig(admittedSkirmishMap)
	cfg.Gameplay = mode
	cfg.NumPlayers = 3
	cfg.Players[2] = cfg.Players[1]
	cfg.Players[2].Side, cfg.Players[2].Color = 0, 2
	cfg.Players[1].AI, cfg.Players[2].AI = kinds[0], kinds[1]
	cfg.RNGSimSeed, cfg.RNGCrtSeed = 7, 5006
	room := matchTestRoom()
	room.MapSchema = admittedRoomSchema(t, fs, cat, admittedSkirmishMap, cfg.NumPlayers)
	r, err := NewMatchConfigRequest(cfg, SkirmishEntryOptions{}, room)
	if err != nil {
		t.Fatalf("adapter: %v", err)
	}
	r.Seats[1].Difficulty, r.Seats[2].Difficulty = words[0], words[1]
	return resolveMatch(t, r)
}

// perSeatBattle composes an admitted configuration and publishes its opening
// frame, ready for checkpoint capture.
func perSeatBattle(t *testing.T, fs vfs.FSOps, cat *content.Catalog, config EffectiveMatchConfig) *Session {
	t.Helper()
	inputs, err := FreezeMatchInputs(fs, cat, config, nil)
	if err != nil {
		t.Fatalf("freeze the configuration's content: %v", err)
	}
	s, err := NewAdmittedSkirmish(inputs, config, nil)
	if err != nil {
		t.Fatalf("admitted entry: %v", err)
	}
	if !s.PublishOpeningFrame() {
		t.Fatal("opening publication")
	}
	return s
}

// perSeatLiveUnit is the lowest-handle live unit owner holds.
func perSeatLiveUnit(t *testing.T, s *Session, owner uint8) *units.Unit {
	t.Helper()
	for _, u := range s.Units.Iter() {
		if u != nil && u.Owner == owner && u.Alive && !u.Dying && u.Def != nil {
			return u
		}
	}
	t.Fatalf("player %d has no live unit", owner)
	return nil
}

// M5 per-computer difficulty (docs/DESIGN_MULTIPLAYER.md §6.6): one battle,
// composed through the admitted path, with two computers on one host at
// different difficulties — a Classic and a Modern one, and two Classic ones —
// in which every reader takes its own seat's word: the Classic plan gate
// [08 R-AI-01 §12], the Modern persona (ControllerDifficulty), the ledger's
// production discount [05 R-ECO-01 §3], the construction refunds' discount
// [05 R-ECO-01 §11], the transfer recipient's discount [05 R-SHARE-01 §2],
// the unit-reclaim refund's discount [05 R-ECO-01 §11] and the
// computer-income projection. The battle is deterministic, and a
// configuration differing only in one computer's difficulty is another
// configuration with another opening checkpoint.
func TestAdmittedSeatDifficultiesRetail(t *testing.T) {
	cat, fs := retailcat.Shared(t)
	for _, c := range []struct {
		name  string
		mode  gameplay.Mode
		kinds [2]ai.Controller
		words [2]uint8
	}{
		{"Classic easy, Modern hard", gameplay.Modern, [2]ai.Controller{ai.ControllerClassic, ai.ControllerModern}, [2]uint8{0, 2}},
		{"Classic easy, Classic hard", gameplay.Community39, [2]ai.Controller{ai.ControllerClassic, ai.ControllerClassic}, [2]uint8{0, 2}},
	} {
		t.Run(c.name, func(t *testing.T) {
			config := perSeatConfig(t, fs, cat, c.mode, c.kinds, c.words)
			s := perSeatBattle(t, fs, cat, config)
			if !s.Skirmish.SeatDifficulty || s.Skirmish.Difficulty != SkirmishDefaultDifficulty {
				t.Fatalf("setup seat words %t battle word %d", s.Skirmish.SeatDifficulty, s.Skirmish.Difficulty)
			}
			names := [3]ai.Difficulty{ai.DifficultyEasy, ai.DifficultyMedium, ai.DifficultyHard}
			discount := [3]float32{500, 700, 1000} // of 1000, per word [05 R-ECO-01 §3]
			twin := perSeatBattle(t, fs, cat, config)

			// The plan gate and the persona: each computer's manager holds a
			// profile of its own word; the human's never-dispatched manager
			// keeps the battle's.
			if got := s.AI[0].Profile.Plan; got != ai.DifficultyMedium {
				t.Fatalf("the human's profile plan %q, want the battle's medium", got)
			}
			if s.AI[1].Profile == s.AI[2].Profile || s.AI[0].Profile == s.AI[1].Profile || s.AI[0].Profile == s.AI[2].Profile {
				t.Fatal("seats of different words share a profile")
			}
			for seat := 1; seat <= 2; seat++ {
				word := c.words[seat-1]
				m := s.AI[seat]
				if m.Controller != c.kinds[seat-1] {
					t.Fatalf("seat %d controller %v, want %v", seat, m.Controller, c.kinds[seat-1])
				}
				if m.Profile.Plan != names[word] || ControllerDifficulty(m.Profile) != names[word] {
					t.Fatalf("seat %d plan %q persona %q, want %q", seat, m.Profile.Plan, ControllerDifficulty(m.Profile), names[word])
				}
			}

			// Another difficulty for one computer is another configuration.
			other := c.words
			other[0] = 1
			changed := perSeatConfig(t, fs, cat, c.mode, c.kinds, other)
			if changed.Digest() == config.Digest() {
				t.Fatal("one computer's difficulty left the configuration's identity unchanged")
			}
			// Determinism: the twin composed from the same configuration
			// opens on the same checkpoint, and the changed configuration on
			// another, which differs in the AI and economy owners. The
			// package's test build registers the Modern step without
			// checkpoint authority, so only an all-Classic battle captures
			// (TestCheckpointModernPlannerRegistrationRefusals).
			if c.kinds[1] == ai.ControllerClassic {
				moved := perSeatBattle(t, fs, cat, changed)
				for _, b := range []*Session{s, twin, moved} {
					if err := b.EnableCheckpoints(); err != nil {
						t.Fatal(err)
					}
				}
				if !reflect.DeepEqual(s.CheckpointHistory(), twin.CheckpointHistory()) {
					t.Fatal("one configuration opened on two checkpoints")
				}
				opening := s.CheckpointHistory().Records[0].Digests
				got := moved.CheckpointHistory().Records[0].Digests
				if got.Full == opening.Full || got.Owners[checkpoint.OwnerComputersScenario-1] == opening.Owners[checkpoint.OwnerComputersScenario-1] ||
					got.Owners[checkpoint.OwnerEconomy-1] == opening.Owners[checkpoint.OwnerEconomy-1] {
					t.Fatal("one computer's difficulty left the opening checkpoint's AI or economy owner unchanged")
				}
			}
			for tick := 1; tick <= 600; tick++ {
				s.Step(s.Clock.ScaledAnchor + 1)
				twin.Step(twin.Clock.ScaledAnchor + 1)
			}
			if a, b := admittedFingerprint(t, c.name, "first", s), admittedFingerprint(t, c.name, "twin", twin); a != b {
				t.Fatalf("one configuration played to %s and %s", a, b)
			}

			// Each seat's plan-gated tables are exactly the profile file's
			// under its own word, whether or not its planner applied them in
			// the twenty seconds played.
			computers := 0
			for p := range s.Econ.Players {
				if s.Econ.Players[p].Exists && s.Econ.Players[p].ControllerState == 2 {
					computers++
				}
			}
			for seat := 1; seat <= 2; seat++ {
				word := c.words[seat-1]
				fresh, err := loadSkirmishAIProfile(s.frozenSimulationInputs().Filesystem(), battleAIProfileName(s.Mission))
				if err != nil {
					t.Fatal(err)
				}
				fresh.SetDifficulty(names[word])
				fresh.ApplyUnitDefinitionsForPlayers(s.Catalog, computers)
				own := s.AI[seat].Profile
				own.ApplyUnitDefinitionsForPlayers(s.Catalog, computers)
				if !reflect.DeepEqual(own.Weight, fresh.Weight) || !reflect.DeepEqual(own.Limit, fresh.Limit) {
					t.Fatalf("seat %d's plan-gated tables are not the profile's under %q", seat, names[word])
				}
			}
			// The reference profile gates easy and hard differently, so the
			// comparison above tells the seats' words apart.
			if a, b := s.AI[1].Profile, s.AI[2].Profile; reflect.DeepEqual(a.Weight, b.Weight) && reflect.DeepEqual(a.Limit, b.Limit) {
				t.Fatal("the easy and hard seats hold the same plan-gated tables")
			}

			// The ledger's production discount, the construction refunds'
			// word and a transfer to each computer: its own word, except
			// that a Modern seat is paid in full.
			for seat := 1; seat <= 2; seat++ {
				word := int(c.words[seat-1])
				p := uint8(seat)
				if got, ok := s.Econ.PlayerSelector(p); !ok || got != word {
					t.Fatalf("seat %d ledger word %d (%t), want %d", seat, got, ok, word)
				}
				if got := s.Build.RefundSelector(p); got != word {
					t.Fatalf("seat %d refund word %d, want %d", seat, got, word)
				}
				want := discount[word]
				if c.kinds[seat-1] == ai.ControllerModern {
					want = 1000
				}
				handle := pool.Handle(10*s.Skirmish.UnitLimit + 1 + seat) // past the unit pool
				s.Econ.CreditFeatureReclaim(handle, p, 1000, 0)
				if got := s.Econ.UnitBuckets(handle)[economy.Metal].Production; got != want {
					t.Fatalf("seat %d was credited %v of 1000, want %v", seat, got, want)
				}
				s.Econ.Players[0].Stock[economy.Energy] = 5000
				before := s.Econ.Players[p].Mirror[economy.Energy].Production
				s.Econ.Transfer(0, p, economy.Energy, 1000)
				if got := s.Econ.Players[p].Mirror[economy.Energy].Production - before; got != want {
					t.Fatalf("seat %d received %v of a 1000 transfer, want %v", seat, got, want)
				}
				// A lethal reclaim of a human unit by one of the seat's own:
				// the death-side refund is discounted on the reclaimer's
				// owner's record and word [05 R-ECO-01 §11].
				killer, victim := perSeatLiveUnit(t, s, p), perSeatLiveUnit(t, s, 0)
				saved := *victim
				costly := *victim.Def
				costly.BuildCostMetal = 1000
				victim.Def, victim.Remaining = &costly, 0
				victim.LastDamageCause, victim.EngagementTarget = 5, killer.Handle
				s.Econ.UnitBuckets(killer.Handle)[economy.Metal].Production = 0
				s.finalizeReclaimRefund(victim)
				if got := s.Econ.UnitBuckets(killer.Handle)[economy.Metal].Production; got != want {
					t.Fatalf("seat %d's reclaimer was refunded %v of 1000, want %v", seat, got, want)
				}
				*victim = saved
			}
			if _, ok := s.Econ.PlayerSelector(0); ok {
				t.Fatal("the human carries a word of its own")
			}

			// The computer-income projection follows every bind: a
			// full-income set pays every seat the undiscounted word, and the
			// battle's own set restores each seat's.
			bound := s.Rules
			s.BindRules(fullIncomeTestSet())
			for p := uint8(1); p <= 2; p++ {
				if got, _ := s.Econ.PlayerSelector(p); got != 2 || s.Build.RefundSelector(p) != 2 {
					t.Fatalf("full income: seat %d word %d", p, got)
				}
			}
			s.BindRules(bound)
			for seat := 1; seat <= 2; seat++ {
				if got, _ := s.Econ.PlayerSelector(uint8(seat)); got != int(c.words[seat-1]) {
					t.Fatalf("rebound: seat %d word %d, want %d", seat, got, c.words[seat-1])
				}
			}
		})
	}
}
