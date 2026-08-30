package visibility

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/world"
)

// TestP0I07_LOSRequiresCoverage locks that enemy outside LOS cannot be acquired without sensor.
func TestP0I07_LOSRequiresCoverage(t *testing.T) {
	terrain := &world.Terrain{CellW: 128, CellH: 128}
	s := newTestService(terrain, ModeHistoryEnabled|ModeCurrentEnabled)
	s.SetLocal(0)
	// Owner 0 publishes at 20,20 radius 320 (10 tiles)
	s.Publish(0, 20, 20, 0, 320)
	inside := Target{Owner: 1, X: tileWorld(20), Z: tileWorld(20)}
	if !s.IsVisible(0, inside) {
		t.Fatalf("target inside published footprint should be visible")
	}
	outside := Target{Owner: 1, X: tileWorld(50), Z: tileWorld(50)}
	if s.IsVisible(0, outside) {
		t.Fatalf("target outside sight should not be visible without sensor")
	}
}

// TestP0I07_TruthTable covers cloaked, underwater, jammer, allied [03 §3.2] C8 [03 §3.4] P0-11.
func TestP0I07_TruthTable(t *testing.T) {
	terrain := &world.Terrain{CellW: 128, CellH: 128, SeaLevel: 20}
	s := newTestService(terrain, ModeHistoryEnabled|ModeCurrentEnabled)
	s.SetLocal(0)
	s.Publish(0, 10, 10, 0, 320)
	at := func(owner PlayerID, y int32, hidden bool, status uint32) Target {
		return Target{
			Owner:  owner,
			X:      tileWorld(10),
			Y:      numeric.Fixed(int64(y) * 65536),
			Z:      tileWorld(10) + numeric.Fixed(5*65536),
			Hidden: hidden,
			Status: status,
		}
	}
	// 1. Cloaked reject unless decloaked bit 0x1000.
	cloaked := at(1, 30, true, 0)
	if s.IsVisible(0, cloaked) {
		t.Fatalf("cloaked enemy without decloak should be rejected")
	}
	decloaked := at(1, 30, true, DecloakBit)
	if !s.IsVisible(0, decloaked) {
		t.Fatalf("cloaked enemy with decloak bit 0x1000 should be visible through predicate (within 90 ticks)")
	}
	// Owner bypass sees own cloaked.
	ownCloaked := at(0, 30, true, 0)
	if !s.IsVisible(0, ownCloaked) {
		t.Fatalf("owner bypass should see own cloaked unit")
	}
	// 2. Underwater without sonar/exempt reject.
	sub := at(1, 10, false, 0)
	if s.IsVisible(0, sub) {
		t.Fatalf("submerged enemy without 0x200 should be rejected")
	}
	subExempt := at(1, 10, false, underwaterExempt)
	if !s.IsVisible(0, subExempt) {
		t.Fatalf("submerged enemy with 0x200 exempt should be visible if in LOS")
	}
	// 3. Jammer on minimap not LOS: jammer circles never author word mask [03 §3.4] C11.
	s2 := newTestService(&world.Terrain{CellW: 64, CellH: 64}, ModeHistoryEnabled|ModeCurrentEnabled)
	s2.SetLocal(0)
	// The viewer sights the enemy jammer's tile. Circles are drawn by the
	// contacts pass, only for units passing its blip gate [03 §3.9][03 §3.10];
	// without coverage this enemy fails all four disjuncts and would emit
	// nothing, which is not what this case is about. The word-mask snapshot is
	// taken after the publish, so the assertion below still isolates the sensor
	// phase's own writes.
	s2.Publish(0, 10, 10, 0, 320)
	before := append([]uint16(nil), s2.wordMask...)
	surf := &recordingSurfaces{}
	s2.SetSurfaces(surf)
	var st uint32
	s2.SensorTick(0, 2, nil, []SensorUnit{{
		Owner: 1, Status: &st, Alive: true, Active: true,
		X: tileWorld(10), Z: tileWorld(10),
		RadarDistance: 0, SonarDistance: 0, RadarJam: 200,
	}})
	for i := range before {
		if s2.wordMask[i] != before[i] {
			t.Fatalf("jammer circle wrote LOS word mask at %d", i)
		}
	}
	if len(surf.radarJam) != 1 {
		t.Fatalf("jammer should rasterize to presentation surface, got %d", len(surf.radarJam))
	}
	// Even with jammer present, enemy outside LOS still not visible via IsVisible.
	if s.IsVisible(0, Target{Owner: 1, X: tileWorld(50), Z: tileWorld(50)}) {
		t.Fatalf("jammer should not make outside-LOS target visible via predicate")
	}
	// 4. Allied not targetable via OR: publishing as ally 1 must not make viewer 0 see enemy 2.
	s3 := newTestService(&world.Terrain{CellW: 128, CellH: 128}, ModeHistoryEnabled|ModeCurrentEnabled)
	s3.SetLocal(0)
	s3.Publish(1, 20, 20, 0, 320)
	alliedTarget := Target{Owner: 2, X: tileWorld(20), Z: tileWorld(20)}
	if s3.IsVisible(0, alliedTarget) {
		t.Fatalf("ally vision OR'd: viewer 0 saw unit only ally 1 covers [03 §3.2] C9")
	}
	if !s3.IsVisible(1, alliedTarget) && !s3.IsVisible(2, alliedTarget) {
		// viewer 2 (owner) bypass should succeed.
		if !s3.IsVisible(2, Target{Owner: 2, X: tileWorld(20), Z: tileWorld(20)}) {
			t.Fatalf("owner bypass broken")
		}
	}
}

// TestP0I07_MovementRefreshThreshold verifies throttling [03 §3.2] C6.
// In terrain-ray mode, refresh occurs only when cell/2 changes or height byte >5;
// in sprite-mask mode, when quantized radius changes.
// This fixture uses sprite-mask mode (terrain-ray needs LOSTables) so the threshold is cell/2 or quantized radius.
func TestP0I07_MovementRefreshThreshold(t *testing.T) {
	terrain := &world.Terrain{CellW: 64, CellH: 64}
	s := newTestService(terrain, ModeHistoryEnabled|ModeCurrentEnabled)
	s.SetLocal(0)
	// Initial publish via Refresh (stores footprint).
	s.Refresh(1, Observer{Owner: 0, CX: 10, CZ: 10, HeightByte: 10, Radius: 160})
	// Capture byte grid state after first publish. Sprite mask with radius 160 → shape index 0 covers 11x11.
	idx := int(10*s.W + 10)
	if s.byteGrids[0][idx] == 0 {
		t.Fatalf("initial publish should set byte grid")
	}
	before := s.byteGrids[0][idx]
	// Move within same cell/2 and same quantized radius should be throttled (no extra inc).
	s.Refresh(1, Observer{Owner: 0, CX: 10, CZ: 10, HeightByte: 12, Radius: 160}) // same cell, same quantized radius
	if s.byteGrids[0][idx] != before {
		t.Fatalf("throttled refresh within same cell/quantized radius should not republish, got %d want %d", s.byteGrids[0][idx], before)
	}
	// Move far enough that old and new footprints do not overlap (shape 11x11 radius 5, so move 15+ cells).
	// This verifies throttling gate correctly triggers on cell change and cleans old footprint.
	s.Refresh(1, Observer{Owner: 0, CX: 30, CZ: 10, HeightByte: 10, Radius: 160})
	oldIdx := int(10*s.W + 10)
	newIdx := int(10*s.W + 30)
	if s.byteGrids[0][oldIdx] != 0 {
		t.Fatalf("old footprint should have been removed after far move, got %d (overlap case would still be 1, but far move must clear)", s.byteGrids[0][oldIdx])
	}
	if s.byteGrids[0][newIdx] == 0 {
		t.Fatalf("new footprint should be present after far cell change")
	}
	// Document TODO if threshold not exact: our implementation follows C6 exactly (cell/2 and height>5 for ray, quantized radius for sprite).
	// TODO(question): exact retail threshold for sprite vs ray and height vs radius is C6 as implemented; publish on every movement tick would also be correct but less efficient.
}

// TestP0I07_SaveLoadRebuildOrder verifies RebuildAll before publish [03 §3.3] C10.
func TestP0I07_SaveLoadRebuildOrder(t *testing.T) {
	terrain := &world.Terrain{CellW: 32, CellH: 32}
	s := newTestService(terrain, ModeHistoryEnabled|ModeCurrentEnabled)
	s.SetLocal(0)
	// Simulate save: capture observers
	s.Publish(0, 5, 5, 0, 160)
	s.Publish(1, 10, 10, 0, 160)
	observers := []Observer{
		{Owner: 0, CX: 5, CZ: 5, HeightByte: 0, Radius: 160},
		{Owner: 1, CX: 10, CZ: 10, HeightByte: 0, Radius: 160},
	}
	// Simulate load: RebuildAll before mapping read, then republish.
	// RebuildAll with no observers should clear grids (history enabled => zero).
	s.RebuildAll(nil)
	if s.wordMask[5*s.W+5] != 0 {
		t.Fatalf("RebuildAll should clear word mask when history enabled")
	}
	// Now republish synchronously before loader returns — no empty frame.
	s.RebuildAll(observers)
	if s.wordMask[5*s.W+5] == 0 {
		t.Fatalf("after RebuildAll with observers, word mask should have coverage")
	}
	if s.byteGrids[0][5*s.W+5] == 0 {
		t.Fatalf("byte grid for owner 0 should be restored")
	}
	if s.byteGrids[1][10*s.W+10] == 0 {
		t.Fatalf("byte grid for owner 1 should be restored")
	}
	// Fog invalidated on rebuild and on local publish.
	if s.FogCacheValid() {
		t.Fatalf("fog should be invalid after rebuild+publish until RebuildFog")
	}
	s.RebuildFog(0, 0)
	if !s.FogCacheValid() {
		t.Fatalf("fog should be valid after RebuildFog")
	}
}

// TestP0I07_SensorGateRequiresTwoPlayers already covered in sensors_test but re-assert.
func TestP0I07_SensorGate(t *testing.T) {
	s := newTestService(&world.Terrain{CellW: 32, CellH: 32}, ModeHistoryEnabled|ModeCurrentEnabled)
	surf := &recordingSurfaces{}
	s.SetSurfaces(surf)
	var st uint32
	u := []SensorUnit{{Owner: 0, Status: &st, Alive: true, Active: true, RadarDistance: 100, X: tileWorld(5), Z: tileWorld(5)}}
	s.SensorTick(0, 1, nil, u)
	if surf.wipes != 0 {
		t.Fatalf("sensor should not run with 1 player")
	}
	s.SensorTick(0, 2, nil, u)
	if surf.wipes != 1 {
		t.Fatalf("sensor should run with 2 players")
	}
}
