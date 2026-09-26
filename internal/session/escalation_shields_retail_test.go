//go:build retail

package session

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/content/profiles"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

func loadEscalationShields(t *testing.T) (vfs.FSOps, *content.Catalog, content.Limits, profiles.Profile) {
	t.Helper()
	value := strings.TrimSpace(os.Getenv("NANOLATHE_MOD_ROOTS_ESCALATION"))
	if value == "" {
		t.Skip("NANOLATHE_MOD_ROOTS_ESCALATION is unset: Gold 10.2.0 shield test needs authored content")
	}
	roots := []string{testsupport.RetailRoot(t)}
	for _, root := range strings.Split(value, string(os.PathListSeparator)) {
		if root = strings.TrimSpace(root); root != "" {
			roots = append(roots, root)
		}
	}
	fs := vfs.New()
	t.Cleanup(func() { fs.Close() })
	if err := fs.MountGameDirectories(roots); err != nil {
		t.Fatal(err)
	}
	profile, err := profiles.Resolve(fs, "")
	if err != nil {
		t.Fatal(err)
	}
	if profile.Name != "escalation" {
		t.Fatalf("profile = %s", profile.Name)
	}
	view := profile.Layout().Apply(fs)
	limits := content.LimitsFromProfile(profile.Limits)
	cat, err := content.CompileWithOptions(view, content.Options{Limits: limits})
	if err != nil {
		t.Fatal(err)
	}
	return view, cat, limits, profile
}

// TestEscalationShieldScripts runs the authored Gold 10.2.0 callbacks. It
// delivers ordinary accepted damage packets through the real combat receiver;
// no script callback, port result, armor state or economy gate is substituted.
// Scope and the distinct fusion branch: [research/extensions/escalation-shields.md].
func TestEscalationShieldScripts(t *testing.T) {
	fs, cat, limits, profile := loadEscalationShields(t)
	limit := 100
	sources := CommunitySources{Content: profile.GameplaySources(), Player: community.Overrides{UnitLimit: &limit}}
	newBattle := func(t *testing.T) *Session {
		t.Helper()
		cfg := DirectSkirmishConfig("ashap plateau")
		cfg.ApplyDefaults()
		cfg.Gameplay = gameplay.Modern
		cfg.RNGSimSeed, cfg.RNGCrtSeed = 7, 7
		s, err := NewSkirmishWithEntryOptions(fs, cat, cfg, SkirmishEntryOptions{ContentLimits: limits, CommunitySources: sources})
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range s.AI {
			if a != nil {
				for j := range a.Deadlines {
					a.Deadlines[j] = ^uint32(0)
				}
			}
		}
		return s
	}
	for _, tc := range []struct {
		key             string
		inside, outside int32
	}{{"ARMSHGEN", 977, 978}, {"CORSHGEN", 1105, 1106}} {
		t.Run(tc.key, func(t *testing.T) {
			s := newBattle(t)
			place := func(key string, owner uint8, x, z int32) *units.Unit {
				return placeCompleteRetailUnit(t, s, key, owner, numeric.FixedFromInt(int64(x)), numeric.FixedFromInt(int64(z)))
			}
			provider := place(tc.key, 0, 600, 600)
			mex := place("ARMMEX", 0, 700, 600)
			inner := place("ARMMEX", 0, tc.inside, 600)
			outer := place("ARMMEX", 0, tc.outside, 600)
			mobile := place("ARMCK", 0, 700, 700)
			fusion := place("ARMFUS", 0, 700, 800)
			advanceEscalationShieldTicks(t, s, 600)
			for _, u := range []*units.Unit{provider, mex, inner, fusion} {
				if !u.Armored {
					t.Fatalf("%s at %d,%d did not become armored", u.Def.UnitName, u.X.Int(), u.Z.Int())
				}
				if diags := u.Script.Diagnostics(); len(diags) != 0 {
					t.Fatalf("%s script diagnostics: %v", u.Def.UnitName, diags)
				}
			}
			if outer.Armored || mobile.Armored {
				t.Fatalf("coverage leaked: outside=%v mobile=%v", outer.Armored, mobile.Armored)
			}
			if provider.Activated {
				t.Fatal("idle shield is drawing hit-interval upkeep")
			}
			assertEscalationDamage(t, s, mex, 100, 25)
			if provider.Activated {
				t.Fatal("damage to a recipient activated the generator")
			}

			// A second completed source adds coverage, not another damage multiplier.
			second := place(tc.key, 0, 600, 700)
			advanceEscalationShieldTicks(t, s, 600)
			assertEscalationDamage(t, s, mex, 100, 25)

			// The fusion's SmokeUnit owns its health gate, separately from Detect's
			// coverage indicator. An ordinary hit that crosses below 66% removes
			// its armor at the next authored smoke polling visit.
			assertEscalationDamage(t, s, fusion, 6000, 1500)
			advanceEscalationShieldTicks(t, s, 180)
			if fusion.Armored {
				t.Fatal("unupgraded fusion retained shield armor below its authored health threshold")
			}

			// Fund one ordinary hit interval. The real settlement must consume
			// the authored upkeep without leaving an unpaid provider balance.
			s.Econ.Players[0].Capacity[economy.Energy] = 50000
			s.Econ.Players[0].Stock[economy.Energy] = 50000
			consumed := s.Econ.Players[0].TotalConsumed[economy.Energy]
			assertEscalationDamage(t, s, provider, 100, 25)
			advanceEscalationShieldTicks(t, s, 45)
			if s.Econ.Players[0].TotalConsumed[economy.Energy]-consumed < float64(provider.Def.EnergyUse) || s.Econ.UnitBuckets(provider.Handle)[economy.Energy].Carry != 0 {
				t.Fatal("funded shield interval did not settle its authored energy use")
			}

			// Both generators charge only when they themselves are hit. Admission
			// cannot pay the authored 8000/10000 upkeep from an empty player stock;
			// it leaves carry while the authored armor and timer continue.
			s.Econ.Players[0].Stock[economy.Energy] = 0
			assertEscalationDamage(t, s, provider, 100, 25)
			advanceEscalationShieldTicks(t, s, 15)
			if !provider.Activated || !provider.Armored || !mex.Armored {
				t.Fatal("hit interval did not retain armor with exhausted energy")
			}
			assertEscalationDamage(t, s, provider, 100, 25)
			advanceEscalationShieldTicks(t, s, 20)
			if !provider.Activated {
				t.Fatal("retrigger did not extend the single hit interval")
			}
			if s.Econ.UnitBuckets(provider.Handle)[economy.Energy].Carry <= 0 {
				t.Fatal("unpaid shield upkeep was not recorded")
			}

			// Save in the live hit interval; COB stacks/statics, polling deadlines,
			// piece flags and ordinary unit operational state survive restoration.
			if tc.key == "ARMSHGEN" {
				summary := RetailBattleSummary(s, "Escalation shield acceptance", "0", s.Skirmish.UnitLimit)
				inputs, err := s.RetailBattleSaveInputs(summary, save.Camera{})
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(t.TempDir(), "SHIELD.SAV")
				if err := s.WriteRetailSave(path, inputs); err != nil {
					t.Fatal(err)
				}
				loaded, err := LoadRetailSavePath(path, RetailLoadDeps{FS: fs, Catalog: cat, ContentLimits: limits, CommunitySources: sources, Gameplay: gameplay.Modern, SimSeed: 7, CRTSeed: 7, UnitLimit: s.Skirmish.UnitLimit})
				if err != nil {
					t.Fatal(err)
				}
				if loaded.Battle == nil || loaded.Battle.Session == nil {
					t.Fatal("shield save did not restore a battle session")
				}
				dst := loaded.Battle.Session
				restored := dst.Units.Unit(loaded.Battle.StableUnit[uint16(provider.Handle)])
				restoredMex := dst.Units.Unit(loaded.Battle.StableUnit[uint16(mex.Handle)])
				if restored == nil || restoredMex == nil || !restored.Activated || !restored.Armored || !restoredMex.Armored {
					t.Fatal("restored shield state is absent")
				}
				before, err := cob.RetailScriptImage(provider.Script)
				if err != nil {
					t.Fatal(err)
				}
				after, err := cob.RetailScriptImage(restored.Script)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(before, after) {
					t.Fatal("active shield script state changed across save/load")
				}
				advanceEscalationShieldTicks(t, dst, 30)
				if restored.Activated || !restored.Armored || !restoredMex.Armored {
					t.Fatal("restored hit interval did not expire while retaining coverage")
				}
			}
			advanceEscalationShieldTicks(t, s, 30)
			if provider.Activated || !provider.Armored || !mex.Armored {
				t.Fatal("hit interval did not expire while retaining coverage")
			}

			// Reclaim removal avoids a nuclear death blast obscuring coverage. The
			// script observes the ordinary pool lifecycle on its next scan.
			for _, p := range []*units.Unit{provider, second} {
				r := s.acceptDamage(s.Clock.GlobalTick, combat.DamageInput{Victim: p.Handle, Nominal: 30000, Kind: 5})
				if !r.DeathLatched {
					t.Fatal("reclaim damage did not remove a provider")
				}
				advanceEscalationShieldTicks(t, s, 600)
				if p.Alive {
					t.Fatal("provider was not finalized")
				}
				if p == provider && !mex.Armored {
					t.Fatal("removing one overlapping provider removed all coverage")
				}
			}
			if mex.Armored {
				t.Fatal("coverage survived removal of the final provider past the authored scan interval")
			}
			assertEscalationDamage(t, s, mex, 100, 100)
		})
	}
	t.Run("alliance_direction", func(t *testing.T) {
		for _, allied := range []bool{false, true} {
			s := newBattle(t)
			s.Econ.Players[1].Allies[0] = allied
			provider := placeCompleteRetailUnit(t, s, "ARMSHGEN", 0, numeric.FixedFromInt(600), numeric.FixedFromInt(600))
			mex := placeCompleteRetailUnit(t, s, "ARMMEX", 1, numeric.FixedFromInt(700), numeric.FixedFromInt(600))
			advanceEscalationShieldTicks(t, s, 300)
			if !provider.Armored || mex.Armored != allied {
				t.Fatalf("recipient alliance=%v: provider armor=%v recipient armor=%v", allied, provider.Armored, mex.Armored)
			}
		}
	})
}

func assertEscalationDamage(t *testing.T, s *Session, u *units.Unit, nominal int32, want uint16) {
	t.Helper()
	before := u.Health
	r := s.acceptDamage(s.Clock.GlobalTick, combat.DamageInput{Victim: u.Handle, Nominal: nominal, Kind: combat.KindOrdinary})
	if !r.Accepted || r.Amount != want || u.Health != before-int32(want) {
		t.Fatalf("%s damage=%+v health %d -> %d; want %d", u.Def.UnitName, r, before, u.Health, want)
	}
}

func advanceEscalationShieldTicks(t *testing.T, s *Session, ticks int) {
	t.Helper()
	advanced := 0
	for dispatch := 0; advanced < ticks && dispatch < ticks+8; dispatch++ {
		before := s.Clock.GlobalTick
		s.Step(s.Clock.ScaledAnchor + 1)
		advanced += int(s.Clock.GlobalTick - before)
	}
	if advanced != ticks {
		t.Fatalf("advanced %d shield scenario ticks, want %d", advanced, ticks)
	}
}
