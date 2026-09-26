//go:build retail

package session

import (
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/features"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// These cases exercise authored features, weather, aircraft and AI through the
// existing battle services. Their content contract is the identified Alpha 5
// and Map Pack 1f package, not equivalence with an unspecified historical patch
// [research/extensions/ta-zero-engine.md "Map Pack 1f: composition and authored map requirements"]
// [research/extensions/ta-zero-engine.md "Package acceptance cases"].
func TestZeroAlpha5WorldAcceptance(t *testing.T) {
	f := loadZeroPackage(t)
	t.Run("crystal_successors", func(t *testing.T) {
		s := f.enter(t, "2P Crystal Gorge", 0)
		// The TNT places the pCrystal art variants. The plain Crystal chain
		// is also authored, but does not stand in for the actual map features.
		for _, reclaim := range []bool{true, false} {
			inst := zeroMapFeature(t, s, "taz_gorge_pcrystal43")
			x, z := inst.CX, inst.CZ
			for _, stage := range []struct {
				key           string
				metal, energy float32
				blocking      bool
			}{
				{"taz_gorge_pcrystal43", 60, 300, true},
				{"taz_gorge_pcrystal42", 60, 300, true},
				{"taz_gorge_pcrystal41", 30, 150, false},
			} {
				current := s.Features.InstanceAt(x, z)
				if current == nil || current.Def.CanonicalKey != stage.key || current.Def.Blocking != stage.blocking {
					t.Fatalf("crystal successor at %d,%d = %+v, want %s (blocking %v)", x, z, current, stage.key, stage.blocking)
				}
				if reclaim {
					m, e, ok := s.Features.ReclaimAt(x, z)
					if !ok || m != stage.metal || e != stage.energy {
						t.Fatalf("%s reclaim = %v/%v/%v, want %v/%v/true", stage.key, m, e, ok, stage.metal, stage.energy)
					}
				} else if !s.Features.DamageFeature(x, z, current.Def.Damage) {
					t.Fatalf("%s did not reach its death successor at its authored damage threshold", stage.key)
				}
			}
			if s.Features.InstanceAt(x, z) != nil || !s.World.PlotAt(int32(x), int32(z)).IsEmpty() {
				t.Fatal("the final crystal stage did not release its anchor")
			}
		}
	})

	for _, mapName := range []string{"2P Metallurgy", "2P Power Core"} {
		t.Run(mapName, func(t *testing.T) {
			s := f.enter(t, mapName, 0)
			deposit := zeroMapFeature(t, s, "taz_metallurgy_metal1")
			x, z := deposit.CX, deposit.CZ
			// Permanent metal belongs to terrain extraction, not reclaim income
			// [05 R-FEAT-01 §7][05 "Terrain metal extraction"].
			if !deposit.Def.Indestructible || deposit.Def.Reclaimable {
				t.Fatal("invisible deposit lost its authored permanent flags")
			}
			for dz := int32(0); dz < deposit.Def.FootprintZ; dz++ {
				for dx := int32(0); dx < deposit.Def.FootprintX; dx++ {
					if got := s.World.PlotAt(int32(x)+dx, int32(z)+dz).Metal(); got != 254 {
						t.Fatalf("deposit metal byte = %d, want 254", got)
					}
				}
			}
			extractor := zeroWorldUnitDef(t, f.cat, "ArmT1Mex")
			rate, sum, err := s.World.SampleMetalWithFootprintSum(int32(x), int32(z), 1, 1, float32(extractor.ExtractsMetal))
			if err != nil || sum != 255 || rate != 255*float32(extractor.ExtractsMetal) {
				t.Fatalf("deposit extraction = %v/%d/%v", rate, sum, err)
			}
			if _, _, ok := s.Features.ReclaimAt(x, z); ok || s.Features.DamageFeature(x, z, 65535) || s.Features.InstanceAt(x, z) != deposit {
				t.Fatal("permanent deposit accepted reclaim or destruction")
			}

			vent := zeroMapFeature(t, s, "taz_metallurgy_vent1")
			for _, key := range []string{"ArmT1Geo", "CoreT1Geo", "GoKT1Geo"} {
				def := zeroWorldUnitDef(t, f.cat, key)
				yard, err := world.ParseYardMap(def.YardMap, int(def.FootprintX), int(def.FootprintZ))
				if err != nil {
					t.Fatal(err)
				}
				rules, err := world.PlacementRulesForUnit(f.cat, def)
				if err != nil {
					t.Fatal(err)
				}
				extent, err := world.NewFootprintExtent(def.FootprintX, def.FootprintZ)
				if err != nil {
					t.Fatal(err)
				}
				check := func(offset int32) error {
					rect, err := world.NewFootprintRect(world.NewFootprintAnchor(int32(vent.CX)+offset, int32(vent.CZ)+offset), extent)
					if err != nil {
						return err
					}
					_, err = s.World.CheckPlacement(world.PlacementQuery{Rect: rect, Yard: yard, Rules: rules})
					return err
				}
				// All three author a central 3x3 G region in a 5x5 yard.
				// A vent under the corner is insufficient [05 "Geothermal requirement"].
				if err := check(-1); err != nil {
					t.Fatalf("%s refused its inner G cells over the vent: %v", key, err)
				}
				if err := check(0); err == nil || !strings.Contains(err.Error(), "geothermal requirement") {
					t.Fatalf("%s accepted a vent outside its G cells: %v", key, err)
				}
			}
		})
	}

	t.Run("weather_damage", func(t *testing.T) {
		s := f.enter(t, "ashap plateau", 0)
		for i, tc := range []struct {
			key                 string
			damage, firestarter int32
			paralyzer           bool
		}{
			{"FireRain", 10, 100, false}, {"Hailstorm", 1, 0, false}, {"Tempest", 0, 0, true},
		} {
			x, z := int32(64+i*10), int32(40)
			u := placeCompleteRetailUnit(t, s, "CoreT1PGen", 0, world.CellToWorld(x), world.CellToWorld(z))
			weapon, ok := f.cat.Weapon(tc.key)
			if !ok || !weapon.Meteor || weapon.DamageDefault != tc.damage || weapon.Paralyzer != tc.paralyzer || weapon.Firestarter != tc.firestarter {
				t.Fatalf("%s weather definition changed", tc.key)
			}
			health, stock := u.Health, s.Econ.Players[0].Stock
			if _, ok := combat.SpawnMeteor(s.Combat, s.CrtRNG(), s.Clock.GlobalTick, weapon, x, z, x, z, 0); !ok {
				t.Fatal("meteor allocation failed")
			}
			// Tick the production projectile service in isolation: unit self-repair
			// can conceal Hailstorm's one-point hit in a whole-session wait.
			for tick := s.Clock.GlobalTick + 1; tick < s.Clock.GlobalTick+101; tick++ {
				s.Combat.TickProjectiles(tick, s.Units, s.World, s.Wind, s.Features, s.Vis, s.Econ, s.Catalog, s.SimRNG(), s.CrtRNG())
			}
			if u.Health != health-tc.damage || s.Econ.Players[0].Stock != stock {
				t.Fatalf("%s contact changed health %d→%d or stock %v→%v", tc.key, health, u.Health, stock, s.Econ.Players[0].Stock)
			}
			if tc.paralyzer {
				n := orders.QueueForUnit(u).Head()
				if n == nil || orders.DescriptorFor(n.ID).Name != "Paralyze" || n.Param1 != 0 {
					t.Fatal("zero-damage Tempest did not deliver zero paralyze credit")
				}
				advanceZeroTicks(t, s, 1)
				if u.Stunned || u.ParalyzeExpire != 0 {
					t.Fatal("zero-credit Tempest acquired a stun duration")
				}
			}
		}
	})

	t.Run("construction_aircraft", func(t *testing.T) {
		s := f.enter(t, "ashap plateau", 0)
		for i, tc := range []struct{ builder, product string }{
			{"ArmT1AirCon", "ArmT1Solar"}, {"CoreT1AirCon", "CoreT1PGen"}, {"GoKT1AirCon", "GoKT1PGen"},
			{"ArmT1AirCon_AI", "ArmT1Solar_AI"}, {"CoreT1AirCon_AI", "CoreT1PGen_AI"}, {"GoKT1AirCon_AI", "GoKT1PGen_AI"},
		} {
			def := zeroWorldUnitDef(t, f.cat, tc.builder)
			zeroWorldUnitDef(t, f.cat, tc.product)
			if def.CanLoad == strings.HasSuffix(tc.builder, "_AI") {
				t.Fatalf("%s lost its authored transport distinction", tc.builder)
			}
			u := placeCompleteRetailUnit(t, s, tc.builder, 0, numeric.FixedFromInt(int64(800+i*180)), numeric.FixedFromInt(800))
			x, z := retailBuildSite(t, s, f.cat, u, tc.product)
			if err := construction.QueueMobileBuild(u, tc.product, x, z, 1, f.cat); err != nil {
				t.Fatal(err)
			}
			for ticks := 0; ticks < 600 && retailUnit(s, 0, tc.product) == nil; ticks++ {
				s.Econ.Players[0].Stock = [2]float32{10000, 10000}
				advanceZeroTicks(t, s, 1)
			}
			product := retailUnit(s, 0, tc.product)
			if product == nil || !product.Alive || product.COBBinding() == nil || len(u.GetScript().Diagnostics()) != 0 {
				t.Fatalf("%s did not create a bound %s nanoframe without script errors", tc.builder, tc.product)
			}
		}
	})

	t.Run("construction_aircraft_transport", func(t *testing.T) {
		s := f.enter(t, "ashap plateau", 0)
		carrier := placeCompleteRetailUnit(t, s, "ArmT1AirCon", 0, numeric.FixedFromInt(800), numeric.FixedFromInt(600))
		cargo := placeCompleteRetailUnit(t, s, "ArmT1InfKbot", 0, numeric.FixedFromInt(880), numeric.FixedFromInt(600))
		if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanOrder, Order: HumanOrderCommand{Handles: []pool.Handle{carrier.Handle}, Code: 6, Target: cargo.Handle, Position: orders.ResolvePos{X: cargo.X, Y: cargo.Y, Z: cargo.Z}}}); err != nil {
			t.Fatal(err)
		}
		for ticks := 0; ticks < 900 && cargo.Attachment.Carrier == 0; ticks++ {
			advanceZeroTicks(t, s, 1)
		}
		if cargo.Attachment.Carrier != carrier.Handle {
			t.Fatal("construction aircraft did not attach its cargo")
		}
		x, z := carrier.X.Add(numeric.FixedFromInt(120)), carrier.Z
		if !s.Movement.ValidateUnloadSite(s.Units, cargo.Handle, x, z, s.World) {
			t.Fatal("transport fixture drop site is invalid")
		}
		if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanOrder, Order: HumanOrderCommand{Handles: []pool.Handle{carrier.Handle}, Code: 5, Position: orders.ResolvePos{X: x, Y: 0, Z: z}}}); err != nil {
			t.Fatal(err)
		}
		for ticks := 0; ticks < 900 && cargo.Attachment.Carrier != 0; ticks++ {
			advanceZeroTicks(t, s, 1)
		}
		if cargo.Attachment.Carrier != 0 || len(carrier.Attachment.Cargo) != 0 || len(carrier.GetScript().Diagnostics()) != 0 {
			t.Fatal("construction aircraft did not unload without script errors")
		}
	})

	t.Run("map_ai_profile", func(t *testing.T) {
		s := f.enter(t, "2P Scramble", 0)
		if s.AI[1] == nil || s.AI[1].Profile.Name() != "AirBattle" {
			t.Fatal("Scramble did not bind its authored AirBattle profile")
		}
		for _, tc := range []struct {
			name                         string
			weight, easyLimit, hardLimit int32
		}{{"Default", 40, 2, 4}, {"AirBattle", 60, 4, 6}} {
			for _, difficulty := range []ai.Difficulty{ai.DifficultyEasy, ai.DifficultyHard} {
				p, err := ai.LoadProfile(f.fs, tc.name)
				if err != nil {
					t.Fatal(err)
				}
				p.SetDifficulty(difficulty)
				p.ApplyUnitDefinitions(f.cat)
				limit := tc.hardLimit
				if difficulty == ai.DifficultyEasy {
					limit = tc.easyLimit
				}
				for _, key := range []string{"ArmT1AF_AI", "CoreT1AF_AI", "GoKT1AF_AI"} {
					zeroWorldUnitDef(t, f.cat, key)
					if p.WeightFor(key) != tc.weight || p.LimitFor(key) != limit {
						t.Fatalf("%s %s %s weight/limit=%d/%d, want %d/%d", tc.name, difficulty, key, p.WeightFor(key), p.LimitFor(key), tc.weight, limit)
					}
				}
			}
		}
	})
}

func zeroMapFeature(t *testing.T, s *Session, key string) *features.Instance {
	t.Helper()
	for _, inst := range s.Features.Instances() {
		if inst.Def != nil && inst.Def.CanonicalKey == key {
			return inst
		}
	}
	t.Fatalf("map has no feature %s", key)
	return nil
}

func zeroWorldUnitDef(t *testing.T, cat *content.Catalog, key string) *content.UnitDef {
	t.Helper()
	def, ok := cat.Unit(key)
	if !ok || def == nil {
		t.Fatalf("authored unit %s is absent", key)
	}
	return def
}
