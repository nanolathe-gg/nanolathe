//go:build retail

package session

// R06's regression: the bulk terrain, exploration and shower boxes the decoder
// already carries have to reach the restored world. The round-trip test next
// door covers units, orders and economy; nothing covered these four accounts,
// and the restore returned success while dropping every one of them.

import (
	"path/filepath"
	"testing"

	"github.com/nanolathe/nanolathe/internal/save"
	"github.com/nanolathe/nanolathe/internal/visibility"
)

const (
	restoreStateSeed      = 7
	restoreStateSaveTick  = 120
	restoreStateMetalByte = 77
	restoreStatePlacer    = 4
	// The marked word carries two players' explored bits, so a restore that
	// merely re-derived coverage from the local observer could not produce it.
	restoreStateExplored = uint16(0x0003)
)

// terrainExplorationShowerFixture is a stepped retail skirmish with the four
// bulk accounts deliberately moved off their derived values: a metal byte and
// a placer nibble the terrain loader did not produce, an explored word no
// observer covers, and an armed shower mid-storm.
func terrainExplorationShowerFixture(t *testing.T) (*retailFixture, *Session, int) {
	t.Helper()
	f := loadRetailFixture(t)
	f.cfg.RNGSimSeed, f.cfg.RNGCrtSeed = restoreStateSeed, restoreStateSeed
	s := f.session(t)
	stepRetail(s, restoreStateSaveTick)

	if s.Vis.Mode()&visibility.ModeHistoryEnabled == 0 {
		t.Fatalf("fixture ran with explored-memory history disabled (mode %#x); the Mapping box would be a constant fill", s.Vis.Mode())
	}
	words := s.Vis.WordMask()
	marked := -1
	for i := len(words) - 1; i >= 0; i-- {
		if words[i] == 0 { // never explored by any slot, so no live observer covers it
			marked = i
			break
		}
	}
	if marked < 0 {
		t.Fatal("every mapping word is explored; the fixture cannot mark a cell outside every observer")
	}
	words[marked] = restoreStateExplored

	// Cells 0 and 1 share one packed PlayerFeatures byte, so this exercises
	// both nibble halves [08 R-SAVE-02 §12].
	s.World.Plot[0].SetMetal(restoreStateMetalByte)
	flags := s.World.Plot[1].FlagByte()
	s.World.Plot[1].SetFlagByte((flags &^ 0x78) | (restoreStatePlacer << 3))
	if s.World.Plot[0].Metal() != restoreStateMetalByte || s.World.Plot[1].PlacerNibble() != restoreStatePlacer {
		t.Fatalf("fixture terrain edit did not take: metal %d placer %d", s.World.Plot[0].Metal(), s.World.Plot[1].PlacerNibble())
	}

	if !s.Meteor.Initialized {
		t.Fatal("the shower phase never installed the map's authored meteor parameters")
	}
	s.Meteor.Enabled, s.Meteor.Active = true, true
	s.Meteor.NextStrike = 9000
	s.Meteor.StrikeEnds = s.Clock.GlobalTick + 3000
	s.Meteor.NextHit = s.Clock.GlobalTick + 3000
	s.Meteor.OriginX, s.Meteor.OriginZ = 11, 22
	s.Meteor.TargetX, s.Meteor.TargetZ = 33, 44
	return f, s, marked
}

// TestRetailRestoreCarriesTerrainExplorationAndShower is R06's gate. Every
// assertion is checked at the restore boundary and again after one tick, so a
// restore that installs the state and a first phase that overwrites it are
// both caught [08 R-SAVE-02 §11] [08 R-SAVE-02 §12] [03 §3.3].
func TestRetailRestoreCarriesTerrainExplorationAndShower(t *testing.T) {
	f, src, marked := terrainExplorationShowerFixture(t)

	summary := RetailBattleSummary(src, "restore-state", "0", SkirmishDefaultUnitLimit)
	in, err := src.RetailBattleSaveInputs(summary, save.Camera{})
	if err != nil {
		t.Fatalf("battle save inputs: %v", err)
	}
	path := filepath.Join(t.TempDir(), "RESTORESTATE.SAV")
	if err := src.WriteRetailSave(path, in); err != nil {
		t.Fatalf("write retail battle save: %v", err)
	}

	result, err := LoadRetailSavePath(path, RetailLoadDeps{
		FS: f.fs, Catalog: f.cat,
		SimSeed: restoreStateSeed, CRTSeed: restoreStateSeed,
		UnitLimit: src.Skirmish.UnitLimit,
	})
	if err != nil {
		t.Fatalf("load retail battle save: %v", err)
	}
	dst := result.Battle.Session

	// Exploration: the whole word grid, which is the only carrier of history —
	// the observer publication that follows can only OR bits that are already
	// set in the source, so equality is the contract, not a superset.
	srcWords, dstWords := src.Vis.WordMask(), dst.Vis.WordMask()
	if len(srcWords) != len(dstWords) {
		t.Fatalf("restored %d mapping words, want %d", len(dstWords), len(srcWords))
	}
	diff := 0
	for i := range srcWords {
		if srcWords[i] != dstWords[i] {
			if diff == 0 {
				t.Errorf("mapping word %d restored as %#04x, want %#04x", i, dstWords[i], srcWords[i])
			}
			diff++
		}
	}
	if diff != 0 {
		t.Fatalf("%d of %d mapping words differ after restore", diff, len(srcWords))
	}
	if dstWords[marked] != restoreStateExplored {
		t.Fatalf("explored word outside every observer restored as %#04x, want %#04x", dstWords[marked], restoreStateExplored)
	}
	// Current coverage is derived and must still have been rebuilt from the
	// restored observers [08 R-SAVE-02 §11].
	covered := 0
	for _, b := range dst.Vis.ByteGrid(visibility.PlayerID(dst.LocalOwner)) {
		if b != 0 {
			covered++
		}
	}
	if covered == 0 {
		t.Fatal("restored session has no current coverage: the observer rebuild did not run")
	}

	if got := dst.World.Plot[0].Metal(); got != restoreStateMetalByte {
		t.Fatalf("restored metal byte %d, want %d", got, restoreStateMetalByte)
	}
	if got := dst.World.Plot[1].PlacerNibble(); got != restoreStatePlacer {
		t.Fatalf("restored placer nibble %d, want %d", got, restoreStatePlacer)
	}

	wantShower := src.Meteor
	assertShower := func(when string) {
		t.Helper()
		got := dst.Meteor
		if !got.Initialized {
			t.Fatalf("%s: restored shower carries no authored parameters", when)
		}
		if got.Enabled != wantShower.Enabled || got.Active != wantShower.Active ||
			got.NextStrike != wantShower.NextStrike || got.StrikeEnds != wantShower.StrikeEnds ||
			got.NextHit != wantShower.NextHit ||
			got.OriginX != wantShower.OriginX || got.OriginZ != wantShower.OriginZ ||
			got.TargetX != wantShower.TargetX || got.TargetZ != wantShower.TargetZ {
			t.Fatalf("%s: shower scalars restored as enabled=%t active=%t next=%d ends=%d hit=%d origin=(%d,%d) target=(%d,%d), want enabled=%t active=%t next=%d ends=%d hit=%d origin=(%d,%d) target=(%d,%d)",
				when, got.Enabled, got.Active, got.NextStrike, got.StrikeEnds, got.NextHit, got.OriginX, got.OriginZ, got.TargetX, got.TargetZ,
				wantShower.Enabled, wantShower.Active, wantShower.NextStrike, wantShower.StrikeEnds, wantShower.NextHit, wantShower.OriginX, wantShower.OriginZ, wantShower.TargetX, wantShower.TargetZ)
		}
		// The authored half comes from the map, not the bank, and a restore
		// that skipped it would leave a shower with no weapon or radius.
		if got.WeaponName != wantShower.WeaponName || got.Radius != wantShower.Radius ||
			got.PerHitDelay != wantShower.PerHitDelay ||
			got.DurationTicks != wantShower.DurationTicks || got.IntervalTicks != wantShower.IntervalTicks {
			t.Fatalf("%s: authored shower parameters restored as weapon=%q radius=%d delay=%d duration=%d interval=%d, want weapon=%q radius=%d delay=%d duration=%d interval=%d",
				when, got.WeaponName, got.Radius, got.PerHitDelay, got.DurationTicks, got.IntervalTicks,
				wantShower.WeaponName, wantShower.Radius, wantShower.PerHitDelay, wantShower.DurationTicks, wantShower.IntervalTicks)
		}
		if (got.Weapon == nil) != (wantShower.Weapon == nil) {
			t.Fatalf("%s: restored shower weapon resolution differs (got nil=%t)", when, got.Weapon == nil)
		}
	}
	assertShower("at the restore boundary")

	// One tick: phase 9 must find an initialized, mid-storm shower rather than
	// re-running battle entry's installer. The saved next-hit deadline is far
	// enough ahead that nothing legitimately changes.
	dst.Step(int32(dst.Clock.GlobalTick) + 1)
	assertShower("after one tick")
	if got := dst.World.Plot[0].Metal(); got != restoreStateMetalByte {
		t.Fatalf("metal byte %d one tick after restore, want %d", got, restoreStateMetalByte)
	}
	if got := dst.World.Plot[1].PlacerNibble(); got != restoreStatePlacer {
		t.Fatalf("placer nibble %d one tick after restore, want %d", got, restoreStatePlacer)
	}
	if got := dst.Vis.WordMask()[marked]; got&restoreStateExplored != restoreStateExplored {
		t.Fatalf("explored word %#04x one tick after restore lost bits of %#04x", got, restoreStateExplored)
	}
}
