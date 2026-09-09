package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
)

func TestHumanVisibilityCommandRefreshesLiveObservers(t *testing.T) {
	s := &Session{Catalog: minimalCatalogForStrict(), World: minimalTerrain(), Mission: &mission.Mission{Type: mission.TypeSkirmish}}
	w, _ := newSlicedWorld(s.Catalog)
	s.Units = w
	s.Econ = economyForTest()
	s.Econ.Players[0].Exists = true
	s.Econ.Players[0].ControllerState = 1
	if err := createAndBindServicesForTest(t, s); err != nil {
		t.Fatalf("bind: %v", err)
	}

	// Start from a real Circular-mode fill, not the service's initial ray-mode
	// fill. A one-cell sprite and centre-only ray table make the command's old
	// and new raster coverage independently observable.
	s.Vis.SetMode(visibility.ModeHistoryEnabled | visibility.ModeCurrentEnabled)
	s.Vis.SetShapes(&content.SightShapes{Shapes: []content.SightShape{{W: 1, H: 1, Opaque: []bool{true}}}})
	s.Vis.SetRayTables(&content.LOSTables{NumTables: 1, Tables: []content.LOSTable{{TableNum: 0}}})
	s.Vis.RebuildAll(nil)
	for i, got := range s.Vis.WordMask() {
		if got != 0 {
			t.Fatalf("Circular fixture history cell %d = %#x, want empty Unmapped fill", i, got)
		}
	}

	// The two raster coordinate calculations differ at this position: the
	// sprite sees z=5 while the ray emitter shear sees z=4.
	def := &content.UnitDef{UnitName: "mode-refresh", MaxDamage: 10, SightDistance: 20, ModelTop: 20, Script: fixtureCOBProgram()}
	h, err := s.Units.Create(def, 0, numeric.Fixed(32<<16), numeric.Fixed(0), numeric.Fixed(160<<16))
	if err != nil {
		t.Fatalf("create unit: %v", err)
	}
	u := s.Units.Unit(h)
	hb := heightByteAt(u, seaLevelFor(s))
	sx, sz := spriteObserverTile(u, seaLevelFor(s))
	rx, rz := observerTile(u, hb)
	if sx == rx && sz == rz {
		t.Fatalf("fixture failed to separate sprite and ray coordinates: (%d,%d)", sx, sz)
	}
	gridW, _ := s.Vis.GridDimensions()
	spriteIdx := int(sz*gridW + sx)
	rayIdx := int(rz*gridW + rx)
	if got := s.Vis.ByteGrid(0)[rayIdx]; got != 0 {
		t.Fatalf("ray fixture tile precovered by initial fill: %d", got)
	}
	publishOne(s, u)
	if got := s.Vis.ByteGrid(0)[spriteIdx]; got != 1 {
		t.Fatalf("Circular fixture sprite coverage = %d, want 1", got)
	}
	if got := s.Vis.ByteGrid(0)[rayIdx]; got != 0 {
		t.Fatalf("Circular publish covered distinct ray tile: %d", got)
	}

	s.applyHumanCommand(HumanCommand{Kind: HumanVisibility, Visibility: HumanVisibilityCommand{ToggleMask: visibility.ModeTerrainRay}}, 0)
	if !s.Vis.TerrainRay() || !s.Vis.CurrentEnabled() {
		t.Fatalf("LOSType command mode = %#x", s.Vis.Mode())
	}
	if got := s.Vis.ByteGrid(0)[rayIdx]; got != 1 {
		t.Fatalf("ray target tile coverage = %d, want command republish", got)
	}
	if got := s.Vis.ByteGrid(0)[spriteIdx]; got != 0 {
		t.Fatalf("LOSType command retained circular coverage %d after byte reset", got)
	}

	// NowISee clears both semantic bits and resets mapped history. Recreate a
	// partial history under a temporary enabled mode, then restore the same
	// clear-bit mode and issue NowISee again: the repeated command must still
	// carry its history-reset argument.
	nowISee := HumanCommand{Kind: HumanVisibility, Visibility: HumanVisibilityCommand{ClearMask: visibility.ModeHistoryEnabled | visibility.ModeCurrentEnabled}}
	s.applyHumanCommand(nowISee, 0)
	for i, got := range s.Vis.WordMask() {
		if got != 0x03ff {
			t.Fatalf("NowISee history cell %d = %#x, want mapped fill", i, got)
		}
	}
	s.Vis.SetMode(visibility.ModeHistoryEnabled | visibility.ModeCurrentEnabled | visibility.ModeTerrainRay)
	s.Vis.RebuildAll(nil)
	publishOne(s, u)
	if got := s.Vis.WordMask()[rayIdx]; got == 0x03ff {
		t.Fatal("repeat-command precondition did not create partial history")
	}
	s.Vis.SetMode(visibility.ModeTerrainRay)
	s.applyHumanCommand(nowISee, 0)
	if got := s.Vis.WordMask()[rayIdx]; got != 0x03ff {
		t.Fatalf("repeated NowISee did not reset history: %#x", got)
	}
}
