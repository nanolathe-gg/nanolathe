package main

import (
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func TestEffectiveClickSnapRadiusUsesTableBounds(t *testing.T) {
	tests := []struct {
		name                          string
		setting, defaultRadius, limit int
		enabled                       bool
		want                          int32
	}{
		{"disabled feature", 3, 3, 3, false, 0},
		{"zero maximum", -1, 3, 0, true, 0},
		{"negative selects default", -1, 3, 5, true, 3},
		{"zero disables", 0, 3, 5, true, 0},
		{"within maximum", 4, 3, 5, true, 4},
		{"above maximum returns default", 6, 3, 5, true, 3},
		{"global cap", 9, 12, 14, true, 9},
		{"default capped to profile maximum", -1, 8, 3, true, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := effectiveClickSnapRadius(tt.setting, tt.defaultRadius, tt.limit, tt.enabled); got != tt.want {
				t.Fatalf("radius = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestChoosePlacementSnapCountDistanceAndScanTies(t *testing.T) {
	counts := map[[2]int32]int{{9, 9}: 2, {10, 9}: 3, {9, 10}: 3, {11, 11}: 1}
	// The raw cursor is exactly between the two count-three candidates after
	// the half-cell bias, so dx-major/dz-minor scan order keeps (9,10).
	got, ok := choosePlacementSnap(10, 10, 1, numeric.FixedFromInt(160), numeric.FixedFromInt(160), func(x, z int32) int {
		return counts[[2]int32{x, z}]
	})
	if !ok || got.cellX != 9 || got.cellZ != 10 || got.count != 3 {
		t.Fatalf("snap = %+v, ok=%t; want cell 9,10 count 3", got, ok)
	}
	if _, ok := choosePlacementSnap(10, 10, 1, 0, 0, func(int32, int32) int { return 0 }); ok {
		t.Fatal("zero-count window produced a snap")
	}
}

func TestCommunityPlacementFacingUsesConstructionRule(t *testing.T) {
	def := &content.UnitDef{BMCode: 0, FootprintX: 2, FootprintZ: 3, YardMap: "oooooo", Rotations: content.FacingSouth | content.FacingEast}
	def.CanonicalKey = "rotatable"
	cat := &content.Catalog{Units: map[string]*content.UnitDef{def.CanonicalKey: def}}
	b := &battleSession{cat: cat, sess: &session.Session{Catalog: cat, Build: &construction.Service{Rules: construction.StrictRules{}}}}
	b.communityPlacement.facing = units.FacingEast

	facing, footX, footZ := b.communityPlacementGeometry(def)
	if facing != units.FacingSouth || footX != 2 || footZ != 3 {
		t.Fatalf("Strict geometry = facing %d %dx%d, want south 2x3", facing, footX, footZ)
	}
	if b.communityPlacement.facing != units.FacingEast {
		t.Fatal("Strict clamp destroyed the retained cursor choice")
	}

	b.sess.Build.Rules = construction.CommunityRules{}
	b.sess.Build.Community = community.Features{StructureRotation: true}
	facing, footX, footZ = b.communityPlacementGeometry(def)
	if facing != units.FacingEast || footX != 3 || footZ != 2 {
		t.Fatalf("Community geometry = facing %d %dx%d, want east 3x2", facing, footX, footZ)
	}
}

func TestCommunityRotationShortcutAllowsShiftAndRejectsCtrl(t *testing.T) {
	in := input.NewState()
	in.ShortcutTokenMode = true
	in.ShortcutToken = input.Token{Kind: input.TokenText, Rune: '?'}
	in.Kbd.SetKey(input.KeyShift, true)
	if !communityRotationShortcut(in, "/") {
		t.Fatal("Shift-held rotation key was rejected")
	}
	in.ShortcutToken.Ctrl = true
	if communityRotationShortcut(in, "/") {
		t.Fatal("Ctrl-composed rotation key was accepted")
	}
}

func TestCommunityBuildSnapCentersAndHonorsBypasses(t *testing.T) {
	cat := testCatalogON05()
	cat.Maps = map[string]*content.MapHeader{"snap": {Schemas: []content.MapSchema{{SurfaceMetal: 0}}}}
	b := newTestBattle(cat, testWorldON05(24, 24))
	b.sess.Mission = &mission.Mission{TerrainKey: "snap"}
	b.sess.Community = community.Features{MexSnap: true, MexSnapRadius: 1, MexSnapRadiusMax: 3}
	prefs := settings.DefaultPresentation()
	b.hostPresentation = &prefs
	b.sess.World.PlotAt(11, 10).SetMetal(1)
	def := &content.UnitDef{ExtractsMetal: 1, FootprintX: 1, FootprintZ: 1}
	cursorX, cursorZ := numeric.FixedFromInt(10*16), numeric.FixedFromInt(10*16)

	x, z := b.communityBuildSnap(def, 10, 10, 1, 1, 0, cursorX, cursorZ)
	if x != 11 || z != 10 {
		t.Fatalf("centred mex snap = (%d,%d), want (11,10)", x, z)
	}
	b.communityPlacement.overrideHeld = true
	if x, z = b.communityBuildSnap(def, 10, 10, 1, 1, 0, cursorX, cursorZ); x != 10 || z != 10 {
		t.Fatalf("override snap = (%d,%d), want raw (10,10)", x, z)
	}
	b.communityPlacement.overrideHeld = false
	b.sess.Community = community.Features{}
	if x, z = b.communityBuildSnap(def, 10, 10, 1, 1, 0, cursorX, cursorZ); x != 10 || z != 10 {
		t.Fatalf("Strict snap = (%d,%d), want raw (10,10)", x, z)
	}
}

func TestCommunityReclaimSnapDispatchPreservesResolvedPositionAndType(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(20, 20))
	w := b.sess.Snapshot.BeginWrite()
	if err := b.sess.Snapshot.Publish(w.Tick); err != nil {
		t.Fatal(err)
	}
	want := orders.ResolvePos{
		X: numeric.FixedFromInt(72), Y: numeric.FixedFromInt(9), Z: numeric.FixedFromInt(88),
		HasFeature: true, IsWreck: true, FeatureResurrectable: true,
	}
	b.communityPlacement.reclaimSnap = want
	b.communityPlacement.reclaimSnapArmed = true
	if !b.issueCommunityReclaimSnap(true) {
		t.Fatal("armed reclaim snap was not issued")
	}
	pending := b.sess.PendingHumanCommands()
	if len(pending) != 1 || pending[0].Kind != session.HumanOrder || pending[0].Order.Code != 12 || !pending[0].Order.Queued {
		t.Fatalf("reclaim snap command = %+v, want one queued code-12 order", pending)
	}
	got := pending[0].Order.Position
	want.InterfaceType = orders.InterfaceTypeLeftClick
	if got != want {
		t.Fatalf("reclaim snap position = %+v, want %+v", got, want)
	}
}

func TestCommunityGeoSnapDefinitionReadsFirst64YardCells(t *testing.T) {
	def := &content.UnitDef{BMCode: 0, FootprintX: 65, FootprintZ: 1, YardMap: strings.Repeat("o", 64) + "G"}
	if communityGeoSnapDefinition(def) {
		t.Fatal("geothermal marker beyond the first 64 yard cells enabled snap")
	}
	def.YardMap = strings.Repeat("o", 63) + "G" + "o"
	if !communityGeoSnapDefinition(def) {
		t.Fatal("geothermal marker in the first 64 yard cells did not enable snap")
	}
}

func TestCommunityClickSnapUsesHorizontalGameBound(t *testing.T) {
	b := &battleSession{}
	if !b.communityClickSnapAllowed(129) {
		t.Fatal("game-rectangle left edge did not admit click snap")
	}
	if b.communityClickSnapAllowed(128) || b.communityClickSnapAllowed(640) {
		t.Fatal("click snap escaped the horizontal game rectangle")
	}
}
