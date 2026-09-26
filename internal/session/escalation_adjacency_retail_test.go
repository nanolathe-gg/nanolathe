//go:build retail

package session

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/content/profiles"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

func loadEscalationAdjacency(t *testing.T) (vfs.FSOps, *content.Catalog, content.Limits, profiles.Profile) {
	t.Helper()
	value := strings.TrimSpace(os.Getenv("NANOLATHE_MOD_ROOTS_ESCALATION"))
	if value == "" {
		t.Skip("NANOLATHE_MOD_ROOTS_ESCALATION is unset: adjacency checks need Escalation Gold 10.2.0")
	}
	fs := vfs.New()
	t.Cleanup(func() { fs.Close() })
	roots := append([]string{testsupport.RetailRoot(t)}, strings.Split(value, string(os.PathListSeparator))...)
	if err := fs.MountGameDirectories(roots); err != nil {
		t.Fatal(err)
	}
	profile, err := profiles.Resolve(fs, "escalation")
	if err != nil {
		t.Fatal(err)
	}
	view := profile.Layout().Apply(fs)
	limits := content.LimitsFromProfile(profile.Limits)
	cat, err := content.CompileWithOptions(view, content.Options{Limits: limits})
	if err != nil {
		t.Fatal(err)
	}
	return view, cat, limits, profile
}

// These tests execute the shipped scripts through ordinary session ticks and
// inspect settled per-unit accounts. They author no substitute detector or
// extension-port answer (research/extensions/escalation-adjacency.md).
func TestEscalationResourcePairing(t *testing.T) {
	fs, cat, limits, profile := loadEscalationAdjacency(t)
	for _, tc := range []struct {
		first, second  string
		separation     int64
		before, paired [2]float32
	}{
		{"ARMFUS", "CORFUS", 80, [2]float32{0, 1000}, [2]float32{0, 1200}},
		{"ARMUWFUS", "CORUWFUS", 80, [2]float32{0, 1200}, [2]float32{0, 1600}},
		{"ARMSFUS", "CORSFUS", 160, [2]float32{2, 5000}, [2]float32{2, 6250}},
		{"ARMESTOR", "CORESTOR", 64, [2]float32{}, [2]float32{0, 25}},
		{"ARMUWES", "CORUWES", 64, [2]float32{}, [2]float32{0, 25}},
		{"ARMSES", "CORSES", 96, [2]float32{}, [2]float32{0, 125}},
		{"ARMUWCS", "CORUWCS", 128, [2]float32{}, [2]float32{0, 125}},
		{"ARMUWMFUS", "CORUWMFUS", 64, [2]float32{1, 100}, [2]float32{2, 100}},
		{"ARMFORGE", "CORVAULT", 208, [2]float32{20, 500}, [2]float32{30, 500}},
	} {
		t.Run(tc.first, func(t *testing.T) {
			s := newEscalationAdjacencySession(t, fs, cat, limits, profile, gameplay.Community39)
			a := placeCompleteRetailUnit(t, s, tc.first, 0, numeric.FixedFromInt(1000), numeric.FixedFromInt(1000))
			advanceProTATicks(t, s, 360)
			assertEscalationIncome(t, s, a, tc.before)
			b := placeCompleteRetailUnit(t, s, tc.second, 0, numeric.FixedFromInt(1000+tc.separation), numeric.FixedFromInt(1000))
			advanceProTATicks(t, s, 660)
			assertEscalationIncome(t, s, a, tc.paired)
			// Reclaim removal avoids a reactor explosion damaging the survivor;
			// the normal unit phase owns death completion and script cleanup.
			s.Units.Destroy(b.Handle, units.DeathReclaimed)
			advanceProTATicks(t, s, 660)
			assertEscalationIncome(t, s, a, tc.before)
		})
	}
}

func TestEscalationChargingAdmission(t *testing.T) {
	fs, cat, limits, profile := loadEscalationAdjacency(t)
	for _, tc := range []struct {
		name, provider                           string
		separation                               int64
		owner                                    uint8
		allied, reverseOnly, unfinished, boosted bool
	}{
		{name: "field_inside", provider: "ARMFIELD", separation: 377, boosted: true},
		{name: "field_outside", provider: "ARMFIELD", separation: 378},
		{name: "carrier_inside", provider: "ARMVCAR", separation: 672, boosted: true},
		{name: "carrier_outside", provider: "ARMVCAR", separation: 673},
		{name: "fleet_carrier_global", provider: "ARMUSCAR", separation: 900, boosted: true},
		{name: "allied", provider: "CORFIELD", separation: 300, owner: 1, allied: true, boosted: true},
		{name: "reverse_alliance_only", provider: "CORFIELD", separation: 300, owner: 1, reverseOnly: true},
		{name: "enemy", provider: "CORFIELD", separation: 300, owner: 1},
		{name: "unfinished", provider: "ARMFIELD", separation: 300, unfinished: true},
		{name: "pair_last_integer_inside", provider: "CORFUS", separation: 86, boosted: true},
		{name: "pair_first_integer_outside", provider: "CORFUS", separation: 87},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newEscalationAdjacencySession(t, fs, cat, limits, profile, gameplay.Community39)
			s.Econ.Players[0].Allies[1] = tc.allied
			s.Econ.Players[1].Allies[0] = tc.allied || tc.reverseOnly
			a := placeCompleteRetailUnit(t, s, "ARMFUS", 0, numeric.FixedFromInt(1000), numeric.FixedFromInt(1000))
			x, z := numeric.FixedFromInt(1000+tc.separation), numeric.FixedFromInt(1000)
			if tc.unfinished {
				def, ok := cat.Unit(tc.provider)
				if !ok {
					t.Fatalf("missing %s", tc.provider)
				}
				if _, err := s.Units.CreateNanoframe(def, tc.owner, x, s.World.HeightAt(x, z), z); err != nil {
					t.Fatal(err)
				}
			} else {
				placeCompleteRetailUnit(t, s, tc.provider, tc.owner, x, z)
			}
			advanceProTATicks(t, s, 700)
			want := float32(1000)
			if tc.boosted {
				want = 1200
			}
			assertEscalationIncome(t, s, a, [2]float32{0, want})
		})
	}
}

func TestEscalationPairingChargingOverlapAndSave(t *testing.T) {
	fs, cat, limits, profile := loadEscalationAdjacency(t)
	s := newEscalationAdjacencySession(t, fs, cat, limits, profile, gameplay.Community39)
	a := placeCompleteRetailUnit(t, s, "ARMFUS", 0, numeric.FixedFromInt(1000), numeric.FixedFromInt(1000))
	field := placeCompleteRetailUnit(t, s, "ARMFIELD", 0, numeric.FixedFromInt(1200), numeric.FixedFromInt(1000))
	advanceProTATicks(t, s, 360)
	assertEscalationIncome(t, s, a, [2]float32{0, 1200})
	pair := placeCompleteRetailUnit(t, s, "CORFUS", 0, numeric.FixedFromInt(1080), numeric.FixedFromInt(1000))
	secondField := placeCompleteRetailUnit(t, s, "CORFIELD", 0, numeric.FixedFromInt(1000), numeric.FixedFromInt(1200))
	advanceProTATicks(t, s, 660)
	assertEscalationIncome(t, s, a, [2]float32{0, 1200})
	s.Units.Destroy(field.Handle, units.DeathReclaimed)
	s.Units.Destroy(secondField.Handle, units.DeathReclaimed)
	advanceProTATicks(t, s, 660)
	assertEscalationIncome(t, s, a, [2]float32{0, 1200})

	inputs, err := s.RetailBattleSaveInputs(RetailBattleSummary(s, "Escalation adjacency", "0", s.Skirmish.UnitLimit), save.Camera{})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "ADJACENCY.SAV")
	if err := s.WriteRetailSave(path, inputs); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadRetailSavePath(path, RetailLoadDeps{
		FS: fs, Catalog: cat, ContentLimits: limits, CommunitySources: CommunitySources{Content: profile.GameplaySources()},
		Gameplay: gameplay.Community39, SimSeed: 7, CRTSeed: 7, UnitLimit: s.Skirmish.UnitLimit,
	})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Battle == nil || loaded.Battle.Session == nil {
		t.Fatal("save did not restore a battle")
	}
	dst := loaded.Battle.Session
	restored := dst.Units.Unit(loaded.Battle.StableUnit[uint16(a.Handle)])
	restoredPair := dst.Units.Unit(loaded.Battle.StableUnit[uint16(pair.Handle)])
	if restored == nil || restoredPair == nil {
		t.Fatal("paired units missing after restore")
	}
	if restored.Activated != a.Activated || !reflect.DeepEqual(restored.GetScript().DebugSnapshot().Statics, a.GetScript().DebugSnapshot().Statics) {
		t.Fatal("save did not preserve activation and detector static state")
	}
	advanceProTATicks(t, dst, 660)
	assertEscalationIncome(t, dst, restored, [2]float32{0, 1200})
	dst.Units.Destroy(restoredPair.Handle, units.DeathReclaimed)
	advanceProTATicks(t, dst, 660)
	assertEscalationIncome(t, dst, restored, [2]float32{0, 1000})
}

func TestEscalationResourceRuleModes(t *testing.T) {
	fs, cat, limits, profile := loadEscalationAdjacency(t)
	for _, mode := range []gameplay.Mode{gameplay.Strict31, gameplay.Community39, gameplay.Modern} {
		t.Run(string(mode), func(t *testing.T) {
			s := newEscalationAdjacencySession(t, fs, cat, limits, profile, mode)
			a := placeCompleteRetailUnit(t, s, "ARMFUS", 0, numeric.FixedFromInt(1000), numeric.FixedFromInt(1000))
			placeCompleteRetailUnit(t, s, "CORFUS", 0, numeric.FixedFromInt(1080), numeric.FixedFromInt(1000))
			placeCompleteRetailUnit(t, s, "ARMFIELD", 0, numeric.FixedFromInt(1200), numeric.FixedFromInt(1000))
			advanceProTATicks(t, s, 700)
			want := float32(1200)
			if mode == gameplay.Strict31 {
				want = 1000
			}
			assertEscalationIncome(t, s, a, [2]float32{0, want})
		})
	}
}

func newEscalationAdjacencySession(t *testing.T, fs vfs.FSOps, cat *content.Catalog, limits content.Limits, profile profiles.Profile, mode gameplay.Mode) *Session {
	t.Helper()
	cfg := DirectSkirmishConfig("ashap plateau")
	cfg.ApplyDefaults()
	// Keep a hostile third side so the allied-provider case cannot end the
	// battle and suspend settlement before its last detector pass.
	cfg.NumPlayers = 3
	cfg.Players[2] = cfg.Players[1]
	cfg.Gameplay, cfg.UnitLimit = mode, 20
	cfg.RNGSimSeed, cfg.RNGCrtSeed = 7, 7
	s, err := NewSkirmishWithEntryOptions(fs, cat, cfg, SkirmishEntryOptions{ContentLimits: limits, CommunitySources: CommunitySources{Content: profile.GameplaySources()}})
	if err != nil {
		t.Fatal(err)
	}
	advanceProTATicks(t, s, 2)
	return s
}

func assertEscalationIncome(t *testing.T, s *Session, u *units.Unit, want [2]float32) {
	t.Helper()
	if !u.Alive || u.Dying {
		t.Fatalf("%s is not a live recipient", u.Def.UnitName)
	}
	accounts := s.Econ.UnitArchived(u.Handle)
	for resource := range want {
		got := accounts[resource].Production - accounts[resource].Requested
		if got != want[resource] {
			t.Fatalf("%s settled resource %d net %v, want %v (account=%+v activated=%v)", u.Def.UnitName, resource, got, want[resource], accounts[resource], u.Activated)
		}
	}
}
