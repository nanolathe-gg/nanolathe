package world

import (
	"strings"
	"testing"
)

// stubViewer is the build-cursor player record of [04 R-P0-08-B §1].
type stubViewer struct {
	w, h    int32
	visible map[[2]int32]bool
	explore map[[2]int32]bool
	mapping bool
	seen    [][2]int32
}

func (v *stubViewer) ExploredExtent() (int32, int32) { return v.w, v.h }
func (v *stubViewer) LocallyVisible(vx, vz int32) bool {
	v.seen = append(v.seen, [2]int32{vx, vz})
	return v.visible[[2]int32{vx, vz}]
}
func (v *stubViewer) Explored(vx, vz int32) bool { return v.explore[[2]int32{vx, vz}] }
func (v *stubViewer) MappingOption() bool        { return v.mapping }

// TestYardBitZeroIsTheStructureYardMark locks [04 R-P0-08-B §1]: for a covered
// cell whose yard byte carries bit 0, the blocker rejects when the cell's
// flag-byte bit 1 — the structure-yard mark a building stamp sets — is set. It
// is a building-versus-building test: it reads no occupant identity, no LOS
// word and no fog surface, and a yard byte without bit 0 does not consult it.
func TestYardBitZeroIsTheStructureYardMark(t *testing.T) {
	ter := legalityTerrain(t, 4, 4, 100)
	extent, _ := NewFootprintExtent(2, 2)
	rect, _ := NewFootprintRect(NewFootprintAnchor(1, 1), extent)
	withBit := []YardCell{0x01, 0x01, 0x01, 0x01}
	withoutBit := []YardCell{0x00, 0x00, 0x00, 0x00}

	if _, err := ter.CheckPlacement(PlacementQuery{Rect: rect, Yard: withBit}); err != nil {
		t.Fatalf("an unmarked footprint was rejected: %v", err)
	}
	ter.PlotAt(2, 2).SetStructureYard(true)
	if _, err := ter.CheckPlacement(PlacementQuery{Rect: rect, Yard: withBit}); err == nil || !strings.Contains(err.Error(), "building yard") {
		t.Fatalf("marked cell rejection = %v, want a building-yard error", err)
	}
	if _, err := ter.CheckPlacement(PlacementQuery{Rect: rect, Yard: withoutBit}); err != nil {
		t.Fatalf("a yard byte without bit 0 consulted the mark: %v", err)
	}
}

// TestKnownSiteGateAppliesOnlyToAViewer locks the blocker's fourth argument
// [04 R-P0-08-B §1]: a null player applies the occupancy rejections
// unconditionally and never projects anything; a player record projects the
// footprint centre onto the LOS grid with the height shear, rejects an off-grid
// or currently unseen site, and only then lets the mapping option decide
// whether the occupancy rejections apply.
func TestKnownSiteGateAppliesOnlyToAViewer(t *testing.T) {
	newTerrain := func() *Terrain {
		ter := legalityTerrain(t, 8, 8, 0)
		ter.PlotAt(2, 2).SetStructureYard(true)
		return ter
	}
	extent, _ := NewFootprintExtent(2, 2)
	rect, _ := NewFootprintRect(NewFootprintAnchor(1, 1), extent)
	yard := []YardCell{0x01, 0x01, 0x01, 0x01}

	// The centre of a 2×2 footprint anchored at (1,1) is
	// ((2 + 2·1)·8, (2 + 2·1)·8) = (32,32) world units; the terrain is flat at
	// height 0, so the projection is (32>>5, 32>>5) = (1,1).
	const wantVX, wantVZ = int32(1), int32(1)

	// Null player: rejection applies, and nothing is projected.
	if _, err := newTerrain().CheckPlacement(PlacementQuery{Rect: rect, Yard: yard}); err == nil {
		t.Fatal("a null player skipped the structure-yard rejection")
	}

	// A viewer whose grid does not contain the projected cell rejects outright.
	small := &stubViewer{w: 1, h: 1}
	if _, err := newTerrain().CheckPlacement(PlacementQuery{Rect: rect, Yard: yard, Viewer: small}); err == nil || !strings.Contains(err.Error(), "not currently visible") {
		t.Fatalf("off-grid projection = %v, want the known-site rejection", err)
	}

	// A visible-but-unseen cell rejects at step 3.
	unseen := &stubViewer{w: 4, h: 4}
	if _, err := newTerrain().CheckPlacement(PlacementQuery{Rect: rect, Yard: yard, Viewer: unseen}); err == nil || !strings.Contains(err.Error(), "not currently visible") {
		t.Fatalf("unseen site = %v, want the known-site rejection", err)
	}
	if len(unseen.seen) != 1 || unseen.seen[0] != [2]int32{wantVX, wantVZ} {
		t.Fatalf("projected cells %v, want exactly one at (%d,%d)", unseen.seen, wantVX, wantVZ)
	}

	visible := map[[2]int32]bool{{wantVX, wantVZ}: true}
	// Seen, no mapping option: the occupancy rejections always apply.
	plain := &stubViewer{w: 4, h: 4, visible: visible}
	if _, err := newTerrain().CheckPlacement(PlacementQuery{Rect: rect, Yard: yard, Viewer: plain}); err == nil || !strings.Contains(err.Error(), "building yard") {
		t.Fatalf("seen site without the mapping option = %v, want the structure-yard rejection", err)
	}
	// Seen, mapping option, explored: the rejections still apply.
	explored := &stubViewer{w: 4, h: 4, visible: visible, mapping: true, explore: visible}
	if _, err := newTerrain().CheckPlacement(PlacementQuery{Rect: rect, Yard: yard, Viewer: explored}); err == nil || !strings.Contains(err.Error(), "building yard") {
		t.Fatalf("explored site = %v, want the structure-yard rejection", err)
	}
	// Seen, mapping option, NEVER explored: the one case that skips them. It
	// cannot arise in ordinary play, because an unexplored site is not visible
	// at step 3; the branch is locked so it is not quietly dropped.
	unexplored := &stubViewer{w: 4, h: 4, visible: visible, mapping: true}
	if _, err := newTerrain().CheckPlacement(PlacementQuery{Rect: rect, Yard: yard, Viewer: unexplored}); err != nil {
		t.Fatalf("unexplored-under-mapping site = %v, want the occupancy rejections skipped", err)
	}
}

// TestKnownSiteGateProjectionCarriesTheHeightShear locks step 1's shear: the
// visibility row is `(worldZ − (height >> 1)) >> 5`, not `worldZ >> 5`.
func TestKnownSiteGateProjectionCarriesTheHeightShear(t *testing.T) {
	ter := legalityTerrain(t, 8, 8, 64)
	for i := range ter.Plot {
		ter.Plot[i].SetHeight(64)
	}
	extent, _ := NewFootprintExtent(2, 2)
	rect, _ := NewFootprintRect(NewFootprintAnchor(1, 1), extent)
	yard := []YardCell{0x00, 0x00, 0x00, 0x00}
	// Centre (32,32); height 64 shears the row by 32, so vz = (32-32)>>5 = 0
	// while vx stays 32>>5 = 1.
	v := &stubViewer{w: 4, h: 4}
	_, _ = ter.CheckPlacement(PlacementQuery{Rect: rect, Yard: yard, Viewer: v})
	if len(v.seen) != 1 || v.seen[0] != [2]int32{1, 0} {
		t.Fatalf("sheared projection %v, want one probe at (1,0)", v.seen)
	}
}
