package session

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/community"
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
		{name: "community saved layout", mode: gameplay.Community39, saved: 250, present: true, want: 250},
		{name: "community missing", mode: gameplay.Community39, want: 1000},
		{name: "community legacy zero", mode: gameplay.Community39, present: true, want: 1000},
		{name: "community oversized", mode: gameplay.Community39, saved: 3277, present: true, bad: true},
		{name: "community negative", mode: gameplay.Community39, saved: -1, present: true, bad: true},
		{name: "community campaign", mode: gameplay.Community39, saved: 250, present: true, campaign: true, want: 200},
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
	for _, mode := range []gameplay.Mode{gameplay.Modern, gameplay.Community39} {
		for _, limit := range []int{250, 1500} {
			t.Run(fmt.Sprintf("%s/%d", mode, limit), func(t *testing.T) {
				f.cfg.Gameplay = mode
				f.cfg.UnitLimit = limit
				configuredLimit := 0 // Exercise the configured-limit override, not the shipped Community table.
				src, err := NewSkirmishWithEntryOptions(f.fs, f.cat, f.cfg, SkirmishEntryOptions{CommunitySources: CommunitySources{Player: community.Overrides{UnitLimit: &configuredLimit}}})
				if err != nil {
					t.Fatal(err)
				}
				other := 1750 - limit
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
				for _, destination := range []gameplay.Mode{gameplay.Modern, gameplay.Community39} {
					deps := RetailLoadDeps{FS: f.fs, Catalog: f.cat, SimSeed: 7, CRTSeed: 9, UnitLimit: other, Gameplay: destination}
					loaded, err := LoadRetailSaveWithDeps(bank, deps)
					if err != nil {
						t.Fatalf("saved %d configured %d: %v", limit, other, err)
					}
					dst := loaded.Battle.Session
					if dst.Gameplay != destination || dst.Rules.Name != string(destination) {
						t.Fatal("loading changed the requested gameplay mode")
					}
					// The load must win over the table's default without rewriting the table.
					features, err := ResolveCommunity(destination, CommunitySources{})
					if err != nil || dst.EntryCommunity.UnitLimit != features.UnitLimit {
						t.Fatal("loading changed the selected entry feature table")
					}
					again, err := dst.RetailBattleSaveInputs(RetailBattleSummary(dst, "resaved layout", "2", other), save.Camera{})
					if err != nil || again.Summary.MaxUnits != int32(limit) {
						t.Fatalf("save after load lost layout: limit=%d err=%v", again.Summary.MaxUnits, err)
					}
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
			})
		}
	}
}
