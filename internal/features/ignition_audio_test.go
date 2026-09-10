package features

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
)

// Success alone requests sound, after binding the burn. X/Z are the anchor
// corner [05 R-FEAT-01 §9]; zero Y locks only the documented host placeholder.
func TestIgnitionSoundSuccessAndRefusals(t *testing.T) {
	for _, refusal := range []string{"", "no sequence", "pool full", "already burning", "off map"} {
		t.Run(refusal, func(t *testing.T) {
			sim, crt := rng.NewSimulation(41), rng.NewCRT(43)
			s := NewService(newEmptyTerrain(16, 16), &sim, &crt, nil)
			def := featureDef("tree", 0, 0, 10)
			def.Filename, def.SeqNameBurn = "trees", "burn"
			def.FootprintX, def.FootprintZ, def.SparkTime = 3, 2, 150
			stubSequences(s, longBurn(), nil, nil)
			inst := s.PlaceAt(4, 6, def)
			if inst == nil {
				t.Fatal("placement failed")
			}
			cx, cz := 4, 6
			switch refusal {
			case "no sequence":
				stubSequences(s, nil, nil, nil)
			case "pool full":
				s.arenaHeld = FeatureAnimSlots
			case "already burning":
				startBurning(s, inst, longBurn(), 100)
			case "off map":
				cx, cz = -1, -1
			}
			var positions [][3]numeric.Fixed
			s.BurnSound = func(pos [3]numeric.Fixed) {
				if !inst.IsBurning || !s.Terrain.PlotAt(4, 6).Occupied() {
					t.Fatal("sound raised before burn attachment")
				}
				positions = append(positions, pos)
			}
			beforeSim, beforeCRT := sim.Draws(), crt.Draws()
			ok := s.igniteAt(cx, cz, def)
			if refusal != "" {
				if ok || len(positions) != 0 || sim.Draws() != beforeSim || crt.Draws() != beforeCRT {
					t.Fatal("refused ignition produced sound or consumed RNG")
				}
				return
			}
			want := [3]numeric.Fixed{numeric.FixedFromInt(64), 0, numeric.FixedFromInt(96)}
			if !ok || len(positions) != 1 || positions[0] != want {
				t.Fatalf("sound positions %v, want one at %v", positions, want)
			}
			if sim.Draws()-beforeSim != 1 || crt.Draws() != beforeCRT {
				t.Fatal("sound changed ignition's single simulation-draw budget")
			}
		})
	}
}

// Only selector zero re-enters ignition [08 R-SAVE-FEATURE-01].
func TestRestoredBurnSelectorRequestsIgnitionSound(t *testing.T) {
	for selector := byte(0); selector <= 2; selector++ {
		sim, crt := rng.NewSimulation(41), rng.NewCRT(43)
		s := NewService(newEmptyTerrain(16, 16), &sim, &crt, nil)
		def := featureDef("tree", 0, 0, 10)
		def.Filename, def.SeqNameBurn = "trees", "burn"
		def.SeqNameDie, def.SeqNameReclamate = "die", "reclaim"
		def.SparkTime = 150
		stubSequences(s, longBurn(), longBurn(), longBurn())
		var positions [][3]numeric.Fixed
		s.BurnSound = func(pos [3]numeric.Fixed) { positions = append(positions, pos) }
		data := make([]byte, RetailRestorePayloadSize(1))
		data[9] = 0x50 | selector
		before := sim.Draws()
		inst, err := s.RestoreAt(4, 6, def, 1, data)
		if err != nil {
			t.Fatal(err)
		}
		if selector == 0 {
			if len(positions) != 1 || positions[0][0] != numeric.FixedFromInt(64) || positions[0][2] != numeric.FixedFromInt(96) {
				t.Fatalf("restored burn sound positions: %v", positions)
			}
			if inst.BurnCountdown != 0x50 || sim.Draws()-before != 1 {
				t.Fatal("restored burn lost countdown overwrite or fresh ignition draw")
			}
		} else if len(positions) != 0 || sim.Draws() != before {
			t.Fatalf("selector %d produced ignition sound or RNG", selector)
		}
	}
}
