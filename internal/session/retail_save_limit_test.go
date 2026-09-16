package session

import (
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/save"
)

// Nanolathe Modern policy; the Strict branch retains [08 R-SESS-01 §9].
func TestModernSaveUnitLimitSelection(t *testing.T) {
	m := &mission.Mission{OTA: &formats.OTA{Global: &formats.Section{
		Name: "GlobalHeader", Items: []formats.Item{{Kind: formats.Assignment, Key: "maxunits", Value: "200"}},
	}}}
	for _, tc := range []struct {
		name              string
		mode              gameplay.Mode
		saved             int32
		present, campaign bool
		want              int
		bad               bool
	}{
		{name: "saved layout", saved: 250, present: true, want: 250},
		{name: "missing", want: 1000},
		{name: "legacy zero", present: true, want: 1000},
		{name: "small restored layout", saved: 1, present: true, want: 1},
		{name: "largest safe layout", saved: 3276, present: true, want: 3276},
		{name: "oversized", saved: 3277, present: true, bad: true},
		{name: "negative", saved: -1, present: true, bad: true},
		{name: "strict configured", mode: gameplay.Strict31, saved: 250, present: true, want: 1000},
		{name: "strict bypass", mode: gameplay.Strict31, saved: 3277, present: true, want: 1000},
		{name: "campaign authored", saved: 250, present: true, campaign: true, want: 200},
		{name: "strict campaign", mode: gameplay.Strict31, saved: 250, present: true, campaign: true, want: 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			summary := save.Summary{Gametype: GametypeMultiplayer, MaxUnits: tc.saved, HasMaxUnits: tc.present}
			if tc.campaign {
				summary.Gametype = GametypeCampaign
			}
			got, err := retailStageUnitLimit(summary, RetailLoadDeps{Gameplay: tc.mode, UnitLimit: 1000}, m)
			if (err != nil) != tc.bad || got != tc.want {
				t.Fatalf("limit=%d err=%v, want %d error=%v", got, err, tc.want, tc.bad)
			}
		})
	}
}

// The owner-1 commander occupies a different slice in each layout. Checking
// only owner 0 would miss the original regression entirely.
func TestModernSaveLoadsAcrossUnitLimits(t *testing.T) {
	f := loadRetailFixture(t)
	for _, limit := range []int{250, 1000} {
		f.cfg.UnitLimit = limit
		src := f.session(t)
		other := 1250 - limit
		inputs, err := src.RetailBattleSaveInputs(RetailBattleSummary(src, "saved layout", "1", other), save.Camera{})
		if err != nil {
			t.Fatal(err)
		}
		if inputs.Summary.MaxUnits != int32(limit) {
			t.Fatalf("saved %d, want actual layout %d", inputs.Summary.MaxUnits, limit)
		}
		projection, err := src.RetailProjection(inputs)
		if err != nil {
			t.Fatal(err)
		}
		data, err := projection.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		bank, err := save.OpenBytes(data)
		if err != nil {
			t.Fatal(err)
		}
		deps := RetailLoadDeps{FS: f.fs, Catalog: f.cat, SimSeed: 7, CRTSeed: 9, UnitLimit: other, Gameplay: gameplay.Modern}
		loaded, err := LoadRetailSaveWithDeps(bank, deps)
		if err != nil {
			t.Fatalf("saved %d configured %d: %v", limit, other, err)
		}
		dst := loaded.Battle.Session
		if dst.Skirmish.UnitLimit != limit || dst.Units.TotalRecords() != src.Units.TotalRecords() {
			t.Fatal("restored player slices differ")
		}
		for _, u := range src.Units.IterSliced() {
			if u == nil || !u.Alive {
				continue
			}
			got := dst.Units.Unit(u.Handle)
			if got == nil || !got.Alive || got.Owner != u.Owner || got.Def.UnitName != u.Def.UnitName || got.X != u.X || got.Y != u.Y || got.Z != u.Z || got.Health != u.Health {
				t.Fatalf("unit %d changed identity or state", u.Handle)
			}
		}
		if frame := dst.Snapshot.Current(); frame == nil || len(frame.Units) != src.Units.Used() {
			t.Fatal("initial frame missing saved units")
		}
		deps.Gameplay = gameplay.Strict31
		if _, err := LoadRetailSaveWithDeps(bank, deps); err == nil {
			t.Fatal("Strict accepted a mismatched layout")
		}
		deps.UnitLimit = limit
		baseline, err := LoadRetailSaveWithDeps(bank, deps)
		if err != nil {
			t.Fatal(err)
		}
		ref := baseline.Battle.Session
		if !reflect.DeepEqual(dst.SimRNG(), ref.SimRNG()) || !reflect.DeepEqual(dst.CrtRNG(), ref.CrtRNG()) {
			t.Fatal("automatic limit selection changed RNG state or draws")
		}
		for i := range dst.Econ.Players {
			if dst.Econ.Players[i].Stock != ref.Econ.Players[i].Stock {
				t.Fatalf("player %d stocks changed", i)
			}
		}
		if dst.Clock.SaveBox() != ref.Clock.SaveBox() {
			t.Fatal("automatic limit selection changed scheduler")
		}
	}
}
