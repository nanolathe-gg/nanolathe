package economy

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// smartMakerFixture is one human player with two Moho-like makers (16 metal
// for 800 energy) and three small makers (1 for 60), all marked by the Smart
// makers mutator and created on, in that slot order. The player's storage is
// 10,000 energy; each test sets stock and the pass counters itself.
func smartMakerFixture(t *testing.T, owner uint8) (*Service, *units.World, []*units.Unit) {
	t.Helper()
	def := func(name string, makes int32, use float64) *content.UnitDef {
		d := economyFixtureDef(&content.UnitDef{UnitName: name, MakesMetal: makes, EnergyUse: use, MaxDamage: 100, BuildTime: 100,
			OnOffable: true, ActivateWhenBuilt: true, SmartMaker: true})
		d.CanonicalKey = content.CanonicalKey(name)
		return d
	}
	moho, small := def("moho", 16, 800), def("small", 1, 60)
	w := units.NewSliced(10, &content.Catalog{})
	svc := &Service{}
	activePlayer(&svc.Players[owner])
	svc.Players[owner].Capacity[Energy] = 10000
	var out []*units.Unit
	for _, d := range []*content.UnitDef{moho, moho, small, small, small} {
		h, err := w.Create(d, owner, 0, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, w.Unit(h))
	}
	return svc, w, out
}

func setAll(us []*units.Unit, on bool) {
	for _, u := range us {
		u.SetActivationEdge(on)
	}
}

func activation(us []*units.Unit) [5]bool {
	var out [5]bool
	for i, u := range us {
		out[i] = u.Activated
	}
	return out
}

func setPass(p *Player, stock, produced, consumed float32) {
	p.Stock[Energy], p.PassProduced[Energy], p.PassConsumed[Energy] = stock, produced, consumed
}

// TestSmartMakersSwitchOnMostEfficientWithinTheBudget: at or above 80% of
// storage the controller switches makers on, most efficient first, while
// their upkeep fits in the pass's surplus plus the stock above 80%
// (docs/DESIGN_MODS_MUTATORS.md "Smart makers"). 8,900 stored with an 800
// surplus is a budget of 1,700: both Mohos (1,600), then the first small
// maker (60) in slot order; the remaining 40 fits nothing. Exactly 80% is
// already the on band.
func TestSmartMakersSwitchOnMostEfficientWithinTheBudget(t *testing.T) {
	svc, w, makers := smartMakerFixture(t, 0)
	setAll(makers, false)
	setPass(&svc.Players[0], 8900, 1000, 200)
	svc.manageSmartMakers(0, w)
	if got := activation(makers); got != [5]bool{true, true, true, false, false} {
		t.Fatalf("switched on %v, want both Mohos and the first small maker", got)
	}

	setAll(makers, false)
	setPass(&svc.Players[0], 8000, 60, 0)
	svc.manageSmartMakers(0, w)
	if got := activation(makers); got != [5]bool{false, false, true, false, false} {
		t.Fatalf("at exactly 80%% with a 60 surplus: %v, want one small maker", got)
	}

	// Storage above 80% that is already falling as fast as its excess allows
	// switches nothing more.
	setAll(makers, false)
	setPass(&svc.Players[0], 9000, 0, 1000)
	svc.manageSmartMakers(0, w)
	if got := activation(makers); got != [5]bool{} {
		t.Fatalf("with no budget: %v, want none", got)
	}
}

// TestSmartMakersSwitchOffLeastEfficientUntilCovered: below 40% the
// controller switches makers off, least efficient first, until their upkeep
// covers the shortfall plus the gap up to 40%. 3,100 stored with a level pass
// needs 900: the three small makers free 180, then the second Moho; the first
// Moho stays on. A player already recovering faster than the gap switches
// nothing.
func TestSmartMakersSwitchOffLeastEfficientUntilCovered(t *testing.T) {
	svc, w, makers := smartMakerFixture(t, 0)
	setPass(&svc.Players[0], 3100, 1000, 1000)
	svc.manageSmartMakers(0, w)
	if got := activation(makers); got != [5]bool{true, false, false, false, false} {
		t.Fatalf("switched to %v, want only the first Moho left on", got)
	}

	setAll(makers, true)
	setPass(&svc.Players[0], 3000, 2500, 1000)
	svc.manageSmartMakers(0, w)
	if got := activation(makers); got != [5]bool{true, true, true, true, true} {
		t.Fatalf("recovering: %v, want every maker left on", got)
	}

	// Just below 40% with a small shortfall: one small maker covers it.
	setPass(&svc.Players[0], 3999, 1000, 1010)
	svc.manageSmartMakers(0, w)
	if got := activation(makers); got != [5]bool{true, true, true, true, false} {
		t.Fatalf("small shortfall: %v, want the last small maker off", got)
	}
}

// TestSmartMakersShedUnderTheGenerationHold: the Modern Generation hold
// stops makers making metal but not their upkeep, so below 40% the
// controller still sheds them as it does without the hold.
func TestSmartMakersShedUnderTheGenerationHold(t *testing.T) {
	svc, w, makers := smartMakerFixture(t, 0)
	svc.Rules = ModernRules{}
	svc.HoldActive[0][ActivityGeneration] = true
	setPass(&svc.Players[0], 3100, 1000, 1000)
	svc.manageSmartMakers(0, w)
	if got := activation(makers); got != [5]bool{true, false, false, false, false} {
		t.Fatalf("switched to %v, want only the first Moho left on", got)
	}
}

// TestSmartMakersHoldBetweenTheThresholds: from 40% up to 80% nothing
// changes, whatever the makers' state, which is the hysteresis that stops a
// maker toggling every pass.
func TestSmartMakersHoldBetweenTheThresholds(t *testing.T) {
	svc, w, makers := smartMakerFixture(t, 0)
	makers[1].SetActivationEdge(false)
	makers[3].SetActivationEdge(false)
	want := activation(makers)
	for _, stock := range []float32{4000, 6000, 7999} {
		setPass(&svc.Players[0], stock, 0, 5000)
		svc.manageSmartMakers(0, w)
		if got := activation(makers); got != want {
			t.Fatalf("at %v stored: %v, want unchanged %v", stock, got, want)
		}
	}
}

// TestSmartMakersLeaveOthersAlone: only a human player that is not an
// observer is managed, only finished units of marked definitions are
// switched, a player with no storage has no fraction to keep, a player
// whose upkeep is paused under the Modern activity holds keeps the pause,
// and under the Generation hold, where a maker makes no metal, none is
// switched on.
func TestSmartMakersLeaveOthersAlone(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*Service, []*units.Unit)
	}{
		{"computer player", func(s *Service, _ []*units.Unit) { s.Players[0].ControllerState = 2 }},
		{"observer", func(s *Service, _ []*units.Unit) { s.Players[0].IsObserver = true }},
		{"no storage", func(s *Service, _ []*units.Unit) { s.Players[0].Capacity[Energy] = 0 }},
		{"paused upkeep", func(s *Service, _ []*units.Unit) {
			s.Rules = ModernRules{}
			s.HoldActive[0][ActivityUpkeep] = true
		}},
		{"paused generation", func(s *Service, _ []*units.Unit) {
			s.Rules = ModernRules{}
			s.HoldActive[0][ActivityGeneration] = true
		}},
		{"unfinished makers", func(_ *Service, us []*units.Unit) {
			for _, u := range us {
				u.Remaining = 0.5
			}
		}},
		{"unmarked makers", func(_ *Service, us []*units.Unit) {
			plain := *us[0].Def
			plain.SmartMaker = false
			for _, u := range us {
				u.Def = &plain
			}
		}},
	} {
		svc, w, makers := smartMakerFixture(t, 0)
		setAll(makers, false)
		tc.setup(svc, makers)
		setPass(&svc.Players[0], 10000, 5000, 0)
		svc.manageSmartMakers(0, w)
		if got := activation(makers); got != [5]bool{} {
			t.Errorf("%s: switched %v", tc.name, got)
		}
	}
}

// TestSmartMakersKeepStorageAboveTheLowThreshold runs the settlement itself:
// a 500-energy-per-pass generator, 5,000 storage and the fixture's makers,
// all on and draining 1,720 a pass. Unmanaged, they empty storage and then
// stall. Managed, every switch is made on a settled pass, storage never
// falls further below 40% than one pass's drain, it never empties, and the
// makers keep converting the surplus — a relationship, not a census.
func TestSmartMakersKeepStorageAboveTheLowThreshold(t *testing.T) {
	run := func(marked bool) (lowest float32, metal float64, switches int) {
		svc, w, makers := smartMakerFixture(t, 0)
		gen := economyFixtureDef(&content.UnitDef{UnitName: "gen", EnergyMake: 500, EnergyStorage: 5000, MaxDamage: 100, BuildTime: 100})
		gen.CanonicalKey = content.CanonicalKey("gen")
		if _, err := w.Create(gen, 0, 0, 0, 0); err != nil {
			t.Fatal(err)
		}
		if !marked {
			plain := *makers[0].Def
			plain.SmartMaker = false
			for _, u := range makers {
				u.Def = &plain
			}
		}
		p := &svc.Players[0]
		p.Stock[Energy] = 5000
		lowest = p.Stock[Energy]
		last := activation(makers)
		for tick := uint32(0); tick < 30*120; tick++ {
			svc.TickPlayer(0, tick, w, nil)
			lowest = min(lowest, p.Stock[Energy])
			if now := activation(makers); now != last {
				if tick%30 != 0 {
					t.Fatalf("a maker switched at tick %d, between settlements", tick)
				}
				for i := range now {
					if now[i] != last[i] {
						switches++
					}
				}
				last = now
			}
		}
		return lowest, p.TotalProduced[Metal], switches
	}
	plainLowest, _, plainSwitches := run(false)
	lowest, metal, switches := run(true)
	if plainLowest != 0 || plainSwitches != 0 {
		t.Fatalf("unmanaged makers: lowest %v with %d switches, want an empty store and none", plainLowest, plainSwitches)
	}
	if lowest < 2000-(1720-500) || lowest <= 0 {
		t.Fatalf("managed storage fell to %v, below 40%% less one pass's drain", lowest)
	}
	if switches == 0 || metal <= 0 {
		t.Fatalf("managed makers: %d switches, %v metal", switches, metal)
	}
	t.Logf("managed: lowest %v, %v metal, %d switches; unmanaged lowest %v", lowest, metal, switches, plainLowest)
}
