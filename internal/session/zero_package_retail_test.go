//go:build retail

package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/content/profiles"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

type zeroPackageFixture struct {
	fs      vfs.FSOps
	cat     *content.Catalog
	limits  content.Limits
	profile profiles.Profile
	sources CommunitySources
}

// The optional roots contain Base, Alpha 5 and Map Pack 1f, in that order,
// after the separately opted-in retail install. Assets never enter the repo.
func loadZeroPackage(t *testing.T) zeroPackageFixture {
	t.Helper()
	value := strings.TrimSpace(os.Getenv("NANOLATHE_MOD_ROOTS_ZERO"))
	if value == "" {
		t.Skip("NANOLATHE_MOD_ROOTS_ZERO is unset: Zero acceptance needs Base, Alpha 5 and Map Pack 1f roots")
	}
	fs := vfs.New()
	t.Cleanup(func() { fs.Close() })
	roots := append([]string{testsupport.RetailRoot(t)}, filepath.SplitList(value)...)
	if err := fs.MountGameDirectories(roots); err != nil {
		t.Fatal(err)
	}
	profile, err := profiles.Resolve(fs, "")
	if err != nil {
		t.Fatal(err)
	}
	if profile.Name != "zero" {
		t.Fatalf("detected profile %q, want zero", profile.Name)
	}
	f := zeroPackageFixture{fs: profile.Layout().Apply(fs), profile: profile,
		limits: content.LimitsFromProfile(profile.Limits), sources: CommunitySources{Content: profile.GameplaySources()}}
	f.cat, err = content.CompileWithOptions(f.fs, content.Options{Limits: f.limits})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f zeroPackageFixture) enter(t *testing.T, mapName string, side int) *Session {
	t.Helper()
	cfg := DirectSkirmishConfig(mapName)
	cfg.ApplyDefaults()
	cfg.Gameplay = gameplay.Modern
	cfg.RNGSimSeed, cfg.RNGCrtSeed = 7, 7
	cfg.Players[0].Side, cfg.Players[1].Side = side, side
	cfg.UnitLimit = f.profile.Limits.UnitLimit
	s, err := NewSkirmishWithEntryOptions(f.fs, f.cat, cfg, SkirmishEntryOptions{
		ContentLimits: f.limits, CommunitySources: f.sources,
	})
	if err != nil {
		t.Fatal(err)
	}
	advanceZeroTicks(t, s, 2)
	return s
}

func (f zeroPackageFixture) restore(t *testing.T, s *Session) *RetailBattleStage {
	t.Helper()
	inputs, err := s.RetailBattleSaveInputs(RetailBattleSummary(s, "Zero acceptance", "0", s.Skirmish.UnitLimit), save.Camera{})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "ZERO.SAV")
	if err := s.WriteRetailSave(path, inputs); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadRetailSavePath(path, RetailLoadDeps{
		FS: f.fs, Catalog: f.cat, ContentLimits: f.limits, CommunitySources: f.sources,
		Gameplay: gameplay.Modern, SimSeed: 7, CRTSeed: 7, UnitLimit: s.Skirmish.UnitLimit,
	})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Route != RetailLoadRouteBattleRestoration || loaded.Battle == nil || loaded.Battle.Session == nil {
		t.Fatal("Zero save did not restore a battle")
	}
	dst := loaded.Battle.Session
	if dst.Clock.GlobalTick != s.Clock.GlobalTick || dst.Rules.Name != s.Rules.Name || dst.Community != s.Community || dst.EntryCommunity != s.EntryCommunity {
		t.Fatal("Zero restore changed the tick or explicitly supplied gameplay selection")
	}
	return loaded.Battle
}

// This locks authored package integration, not historical DLL equivalence,
// the visual interface, shield mechanics or legacy .zsv interoperability
// [research/extensions/ta-zero-engine.md "Package acceptance cases"].
func TestZeroAlpha5PackageAcceptance(t *testing.T) {
	f := loadZeroPackage(t)
	for side, tc := range []struct{ side, commander, intGAF, factory string }{
		{"GOK", "GoKCommander", "GOKINT", "GoKT1GF"},
		{"ARM", "ArmCommander", "ARMINT", "ArmT1LGF"},
		{"CORE", "CoreCommander", "CORINT", "CoreT1GF"},
	} {
		t.Run(tc.side, func(t *testing.T) {
			if side >= len(f.cat.Sides) || f.cat.Sides[side] == nil {
				t.Fatalf("authored side %d is absent", side)
			}
			def := f.cat.Sides[side]
			if def.Name != tc.side || def.Commander != tc.commander || def.IntGAF != tc.intGAF {
				t.Fatalf("side %d = %s/%s/%s", side, def.Name, def.Commander, def.IntGAF)
			}
			s := f.enter(t, "ashap plateau", side)
			for owner := uint8(0); owner < 2; owner++ {
				u := retailUnit(s, owner, tc.commander)
				if u == nil || u.COBBinding() == nil || !u.COBBinding().CreateInvoked || !strings.EqualFold(u.Def.Provenance.ProviderID, "TAZ31.gp3") {
					t.Fatalf("owner %d did not enter with the authored, script-bound %s", owner, tc.commander)
				}
			}
			if s.AI[1] == nil || s.Gameplay != gameplay.Modern || !s.Community.ScriptPorts {
				t.Fatal("Zero session lost its AI manager or profile-supplied port table")
			}

			builder := retailUnit(s, 0, tc.commander)
			x, z := retailBuildSite(t, s, f.cat, builder, tc.factory)
			if err := construction.QueueMobileBuild(builder, tc.factory, x, z, 1, f.cat); err != nil {
				t.Fatal(err)
			}
			// An explicit human attack establishes weapon integration without
			// making a claim about the package's autonomous target selection.
			shooter := placeCompleteRetailUnit(t, s, tc.commander, 0, numeric.FixedFromInt(600), numeric.FixedFromInt(600))
			target := placeCompleteRetailUnit(t, s, "CoreCommander", 1, numeric.FixedFromInt(600), numeric.FixedFromInt(700))
			health := target.Health
			if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanOrder, Order: HumanOrderCommand{
				Handles: []pool.Handle{shooter.Handle}, Code: 3, Target: target.Handle,
				Position: orders.ResolvePos{X: target.X, Y: target.Y, Z: target.Z},
			}}); err != nil {
				t.Fatal(err)
			}
			var frame *units.Unit
			for ticks := 0; ticks < 300 && (frame == nil || target.Health == health); ticks++ {
				s.Econ.Players[0].Stock = [2]float32{10000, 10000}
				advanceZeroTicks(t, s, 1)
				if u := retailUnit(s, 0, tc.factory); u != nil && u.Remaining > 0 {
					frame = u
				}
			}
			if frame == nil || frame.COBBinding() == nil || target.Health >= health {
				t.Fatal("authored commander did not raise a bound factory frame and damage its ordered target")
			}
			loaded := f.restore(t, s)
			dst := loaded.Session
			restored := dst.Units.Unit(loaded.StableUnit[uint16(frame.Handle)])
			if restored == nil || restored.Remaining != frame.Remaining || restored.Health != frame.Health || restored.Def.CanonicalKey != frame.Def.CanonicalKey {
				t.Fatal("saved factory nanoframe changed at the restoration boundary")
			}
			remaining := restored.Remaining
			for ticks := 0; ticks < 90 && restored.Remaining == remaining; ticks++ {
				dst.Econ.Players[0].Stock = [2]float32{10000, 10000}
				advanceZeroTicks(t, dst, 1)
			}
			if !restored.Alive || restored.Remaining >= remaining {
				t.Fatal("restored factory construction did not continue alive")
			}
		})
	}

	t.Run("factory_direction_and_script_ports", func(t *testing.T) {
		s := f.enter(t, "ashap plateau", 1)
		factory := placeCompleteRetailUnit(t, s, "ArmT1LGF", 0, numeric.FixedFromInt(1000), numeric.FixedFromInt(600))
		plate := zeroBuildpad(t, factory)
		if !factory.Def.CanDGun || !strings.EqualFold(factory.Def.Weapon3, "FactoryDir") {
			t.Fatal("ArmT1LGF lost its authored Direct command weapon")
		}
		point := func(dx int64) uint16 {
			if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanOrder, Order: HumanOrderCommand{
				Handles: []pool.Handle{factory.Handle}, Code: 4,
				Position: orders.ResolvePos{X: factory.X.Add(numeric.FixedFromInt(dx)), Y: factory.Y, Z: factory.Z},
			}}); err != nil {
				t.Fatal(err)
			}
			advanceZeroTicks(t, s, 90)
			return factory.GetScript().Pieces[plate].RotY
		}
		west, east := point(-200), point(200)
		if west == east || point(-200) != west || point(200) != east {
			t.Fatal("opposite Direct commands did not select two repeatable buildpad directions")
		}
		loaded := f.restore(t, s)
		restored := loaded.Session.Units.Unit(loaded.StableUnit[uint16(factory.Handle)])
		if restored == nil || restored.GetScript().Pieces[zeroBuildpad(t, restored)].RotY != east {
			t.Fatal("saved factory direction did not survive the Nanolathe bank boundary")
		}
		advanceZeroTicks(t, loaded.Session, 30)
		if restored.GetScript().Pieces[zeroBuildpad(t, restored)].RotY != east {
			t.Fatal("restored factory direction changed during idle continuation")
		}

		// Observe the authored callbacks without replacing their answers.
		// Their older shipped recorder's exact boundary behavior remains
		// unknown [research/extensions/ta-zero-engine.md "Unknown"].
		ports := &zeroObservedPorts{inner: s.Rules.ScriptPorts, calls: make(map[string][2]int), limits: make(map[string]int32)}
		s.Rules.ScriptPorts = ports
		for i, key := range []string{"GoKT1GF_AI", "ArmT1LGF_AI", "CoreT1GF_AI"} {
			if _, ok := f.cat.Unit(key); !ok {
				t.Fatalf("authored AI factory %s absent", key)
			}
			placeCompleteRetailUnit(t, s, key, 1, numeric.FixedFromInt(1000+int64(i)*300), numeric.FixedFromInt(1000))
		}
		advanceZeroTicks(t, s, 300)
		for _, key := range []string{"GoKT1GF_AI", "ArmT1LGF_AI", "CoreT1GF_AI"} {
			calls := ports.calls[key]
			if calls[0] == 0 || calls[1] == 0 || ports.limits[key] != int32(s.Skirmish.UnitLimit*10) {
				t.Fatalf("%s did not consume the bound unit-range and alliance ports: calls=%v limit=%d", key, calls, ports.limits[key])
			}
		}
	})

	// These values are authored by Map Pack 1f, not inferred from filenames
	// [research/extensions/ta-zero-engine.md "Map Pack 1f: composition and authored map requirements"].
	t.Run("map_weather", func(t *testing.T) {
		for _, tc := range []struct {
			mapName, weapon         string
			radius, duration, delay int32
		}{
			{"2P Brimstone Steppes", "FireRain", 4000, 60, 2},
			{"2P Hiemal Duel", "Hailstorm", 2000, 300, 1},
			{"2P Raindance", "Tempest", 4000, 90, 1},
			{"4P Gathering", "Tempest", 6000, 90, 0},
		} {
			t.Run(tc.mapName, func(t *testing.T) {
				info, err := f.fs.Stat("maps/" + tc.mapName + ".ota")
				if err != nil || !strings.EqualFold(info.Source.ProviderID(), "TA_Zero_Maps.ufo") {
					t.Fatalf("Map Pack 1f OTA missing for %s: %v", tc.mapName, err)
				}
				s := f.enter(t, tc.mapName, 0)
				m := s.Meteor
				if !m.Enabled || !m.Initialized || !m.Active || m.Weapon == nil || m.WeaponName != tc.weapon || m.Radius != tc.radius || m.DurationTicks != tc.duration || m.PerHitDelay != tc.delay {
					t.Fatalf("%s weather did not retain its authored weapon/scheduling values: %+v", tc.mapName, m)
				}
				if !strings.EqualFold(m.Weapon.Provenance.ProviderID, "TAZ31.gp3") {
					t.Fatal("map weather did not resolve Alpha 5's weapon")
				}
				if (tc.mapName == "2P Hiemal Duel" || tc.mapName == "4P Gathering") && s.Mission.Schema.StartPositions != 10 {
					t.Fatal("map filename replaced its ten authored start positions")
				}
			})
		}
	})
}

func zeroBuildpad(t *testing.T, u *units.Unit) int {
	t.Helper()
	if binding := u.COBBinding(); binding != nil {
		for i, name := range binding.Program.Pieces {
			if strings.EqualFold(name, "buildpad") {
				return i
			}
		}
	}
	t.Fatalf("%s has no authored buildpad piece", u.Def.UnitName)
	return -1
}

type zeroObservedPorts struct {
	inner  ScriptPortRules
	calls  map[string][2]int
	limits map[string]int32
}

func (p *zeroObservedPorts) ReadScriptPort(s *Session, u *units.Unit, port cob.Port, args [4]int32) int32 {
	value := p.inner.ReadScriptPort(s, u, port, args)
	counts := p.calls[u.Def.UnitName]
	if port == 70 {
		counts[0]++
		p.limits[u.Def.UnitName] = value
	} else if port == 74 {
		counts[1]++
	}
	p.calls[u.Def.UnitName] = counts
	return value
}

func advanceZeroTicks(t *testing.T, s *Session, ticks int) {
	t.Helper()
	start := s.Clock.GlobalTick
	for dispatch := 0; int(s.Clock.GlobalTick-start) < ticks && dispatch < ticks+8; dispatch++ {
		s.Step(s.Clock.ScaledAnchor + 1)
	}
	if got := int(s.Clock.GlobalTick - start); got != ticks {
		t.Fatalf("Zero session advanced %d ticks, want %d (state %v)", got, ticks, s.State)
	}
}
