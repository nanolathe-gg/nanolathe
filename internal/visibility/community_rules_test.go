package visibility

import (
	"testing"
	"unsafe"

	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func TestAlliedJammerDecisionAndStrictBypass(t *testing.T) {
	if unsafe.Sizeof(StrictRules{}) != 0 || unsafe.Sizeof(CommunityRules{}) != 0 || unsafe.Sizeof(ModernRules{}) != 0 {
		t.Fatal("visibility rule implementations must remain zero size")
	}

	viewer, alliedOwner, enemyOwner := PlayerID(2), PlayerID(4), PlayerID(5)
	alliedCalls := 0
	s := &Service{Community: CommunityState{
		AlliedJammingIgnored: true,
		Allied: func(gotViewer, gotOwner PlayerID) bool {
			alliedCalls++
			return gotViewer == viewer && gotOwner == alliedOwner
		},
	}}
	if !(StrictRules{}).JammerSuppresses(s, viewer, alliedOwner) {
		t.Fatal("Strict suppressed the retail allied jammer")
	}
	if (CommunityRules{}).JammerSuppresses(s, viewer, alliedOwner) {
		t.Fatal("Community let an allied jammer suppress contacts")
	}
	if !(CommunityRules{}).JammerSuppresses(s, viewer, enemyOwner) {
		t.Fatal("Community exempted an enemy jammer")
	}
	if alliedCalls != 2 {
		t.Fatalf("directional alliance queries = %d, want 2", alliedCalls)
	}
	if (CommunityRules{}).JammerSuppresses(s, viewer, viewer) {
		t.Fatal("Community removed the retail owner exemption")
	}
	if alliedCalls != 2 {
		t.Fatal("owner exemption consulted the alliance predicate")
	}

	s.Community.AlliedJammingIgnored = false
	if !(CommunityRules{}).JammerSuppresses(s, viewer, alliedOwner) {
		t.Fatal("flag-off Community did not answer as Strict")
	}
	if alliedCalls != 2 {
		t.Fatal("flag-off path consulted the alliance predicate")
	}
}

func TestAlliedJammerDoesNotClearSensorContact(t *testing.T) {
	newFixture := func(r Rules, ignored bool) (*Service, []SensorUnit, *uint32) {
		s := New(&world.Terrain{CellW: 32, CellH: 32}, ModeHistoryEnabled|ModeCurrentEnabled)
		s.Rules = r
		s.Community = CommunityState{
			AlliedJammingIgnored: ignored,
			Allied: func(viewer, other PlayerID) bool {
				return viewer == 0 && other == 1
			},
		}
		var sourceStatus, jammerStatus, targetStatus uint32
		units := []SensorUnit{
			{ID: 1, Owner: 0, Status: &sourceStatus, Alive: true, Active: true, RadarDistance: 100},
			{ID: 2, Owner: 1, Status: &jammerStatus, Alive: true, Active: true, RadarJam: 100},
			{ID: 3, Owner: 2, Status: &targetStatus, Alive: true, Hidden: true},
		}
		return s, units, &targetStatus
	}

	strict, units, strictTarget := newFixture(StrictRules{}, false)
	strict.SensorTick(1, 3, units)
	if *strictTarget&SeenBit != 0 || *strictTarget&JammedBit == 0 {
		t.Fatalf("Strict target status = %#x, want jammed and unseen", *strictTarget)
	}

	community, units, communityTarget := newFixture(&CommunityRules{}, true)
	community.SensorTick(1, 3, units)
	if *communityTarget&SeenBit == 0 || *communityTarget&JammedBit != 0 {
		t.Fatalf("Community target status = %#x, want radar contact preserved", *communityTarget)
	}
}

func TestCommunityOffMapVisibilityMarginAndClamp(t *testing.T) {
	const mapCells = 16
	s := New(&world.Terrain{CellW: mapCells, CellH: mapCells}, ModeHistoryEnabled|ModeCurrentEnabled)
	s.Rules = &CommunityRules{}
	s.Community.OffMapAircraftMarginTiles = 1
	s.SetLocal(3) // byte mode still reads the queried player's grid.

	// One terrain tile east of the map projects one visibility cell past its
	// right edge. The substitute reads the nearest cell on that same row.
	const row = 2
	s.byteGrids[0][row*int(s.W)+int(s.W)-1] = 1
	base := Target{
		Owner: 1,
		X:     numeric.Fixed(16 * 16 << 16),
		Y:     0,
		Z:     numeric.Fixed(4 * 16 << 16),

		OriginX: numeric.Fixed(16 * 16 << 16),
		OriginY: 0,
		OriginZ: numeric.Fixed(4 * 16 << 16),
		Flying:  true,
		OffMap:  true,

		FootprintX:     16,
		FootprintZ:     4,
		FootprintSizeX: 1,
		FootprintSizeZ: 1,
	}
	wantInput := base
	if !s.IsVisible(0, base) {
		t.Fatal("aircraft one tile outside the map did not inherit the border cell")
	}
	if base != wantInput {
		t.Fatal("visibility decision mutated its request")
	}

	outside := base
	outside.FootprintX = 17
	if s.IsVisible(0, outside) {
		t.Fatal("aircraft two tiles outside a one-tile margin became visible")
	}
	for _, tc := range []struct {
		name               string
		x, z, sizeX, sizeZ int32
		want               bool
	}{
		{"corner at margin", 16, 16, 1, 1, true},
		{"corner beyond margin", 17, 17, 1, 1, false},
		{"partial west overlap", -1, 4, 2, 1, true},
	} {
		probe := base
		probe.FootprintX, probe.FootprintZ = tc.x, tc.z
		probe.FootprintSizeX, probe.FootprintSizeZ = tc.sizeX, tc.sizeZ
		if got := s.footprintWithinCommunityMargin(probe); got != tc.want {
			t.Errorf("%s within margin = %v, want %v", tc.name, got, tc.want)
		}
	}

	ground := base
	ground.Flying = false
	if s.IsVisible(0, ground) {
		t.Fatal("off-map ground unit used the aircraft substitute")
	}

	s.Community.OffMapAircraftMarginTiles = 0
	if s.IsVisible(0, base) {
		t.Fatal("zero margin did not preserve the Strict off-map rejection")
	}
}

func TestCommunityClampedVisibilityUsesViewingBitInWordMode(t *testing.T) {
	s := New(&world.Terrain{CellW: 16, CellH: 16}, ModeHistoryEnabled)
	s.Rules = &CommunityRules{}
	s.Community.OffMapAircraftMarginTiles = 1
	s.SetViewingPlayer(3)

	target := Target{
		Owner: 1,
		X:     numeric.Fixed(16 * 16 << 16), Z: numeric.Fixed(4 * 16 << 16),
		OriginX: numeric.Fixed(16 * 16 << 16), OriginZ: numeric.Fixed(4 * 16 << 16),
		Flying: true, OffMap: true,
		FootprintX: 16, FootprintZ: 4, FootprintSizeX: 1, FootprintSizeZ: 1,
	}
	idx := 2*int(s.W) + int(s.W) - 1
	s.wordMask[idx] = cellBit(3)
	if !s.IsVisible(0, target) {
		t.Fatal("word mode did not read the viewing player's bit")
	}
	s.wordMask[idx] = cellBit(0)
	if s.IsVisible(0, target) {
		t.Fatal("word mode read the queried player's bit instead of the viewing player's")
	}
}

func TestCommunityVisibilityPrefersShearThenTrueRow(t *testing.T) {
	s := New(&world.Terrain{CellW: 16, CellH: 16}, ModeHistoryEnabled|ModeCurrentEnabled)
	s.Rules = &CommunityRules{}
	s.Community.OffMapAircraftMarginTiles = 1

	// The origin is on-map, but altitude shears its projected row to -1.
	// CP-ENV-1 first substitutes the in-range true row, then reads it.
	target := Target{
		Owner: 1,
		X:     numeric.Fixed(64 << 16), Y: numeric.Fixed(160 << 16), Z: numeric.Fixed(64 << 16),
		OriginX: numeric.Fixed(64 << 16), OriginY: numeric.Fixed(160 << 16), OriginZ: numeric.Fixed(64 << 16),
		Flying:     true,
		FootprintX: 4, FootprintZ: 4, FootprintSizeX: 1, FootprintSizeZ: 1,
	}
	trueRowIndex := 2*int(s.W) + 2
	s.byteGrids[0][trueRowIndex] = 1
	if !s.IsVisible(0, target) {
		t.Fatal("on-map aircraft with an out-of-array sheared row did not use its true row")
	}

	// When the sheared row is valid, it wins even when the true row is lit.
	target.Y = numeric.Fixed(64 << 16)
	target.OriginY = target.Y
	if s.IsVisible(0, target) {
		t.Fatal("valid sheared row was replaced by the true row")
	}
}

func TestVisibilityRuleDispatchAllocatesNothing(t *testing.T) {
	s := New(&world.Terrain{CellW: 16, CellH: 16}, ModeHistoryEnabled|ModeCurrentEnabled)
	target := Target{Owner: 1}
	for _, rules := range []Rules{nil, StrictRules{}, &CommunityRules{}, &ModernRules{}} {
		s.Rules = rules
		if got := testing.AllocsPerRun(100, func() {
			_ = s.IsVisible(0, target)
			_ = s.rules().JammerSuppresses(s, 0, 1)
		}); got != 0 {
			t.Fatalf("%T dispatch allocations = %v, want 0", rules, got)
		}
	}
}
