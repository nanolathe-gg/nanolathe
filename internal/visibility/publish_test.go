package visibility

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// TestRemoveFootprintSpriteBranchHasNoZeroGuard is the WU-19-215 regression for
// [03 R-VIS-01 §2] "Retail edge, stated as a contract": the ray branch's
// removal call carries a storedByte != 0 guard, but the sprite-mask branch
// carries NONE — its stored coverage byte is the quantized shape index, and
// index 0 is a legitimate published shape (any sightdistance below 192 clamps
// to it). Gating removal on the observer's heightByte — a field the sprite
// branch does not even use as its stored byte — used to refuse the removal
// whenever that incidental value was zero, leaking coverage behind every such
// unit that moved.
func TestRemoveFootprintSpriteBranchHasNoZeroGuard(t *testing.T) {
	terrain := &world.Terrain{CellW: 128, CellH: 128}
	// ModeTerrainRay is not set: this is the sprite-mask (Circular LOS) branch.
	s := newTestService(terrain, ModeHistoryEnabled|ModeCurrentEnabled)
	s.SetLocal(0)

	const radius = 100 // clamps to shape index 0 [03 R-VIS-01 §2]
	if idx := s.spriteShapeIndex(radius); idx != 0 {
		t.Fatalf("fixture radius %d selected shape index %d, want 0", radius, idx)
	}

	// HeightByte 0 too: the sprite branch does not consume it as its stored
	// coverage byte, but it is exactly the value the pre-fix guard tested.
	s.Refresh(1, Observer{Owner: 0, CX: 20, CZ: 20, HeightByte: 0, Radius: radius})

	oldIdx := int(20*s.W + 20)
	if s.byteGrids[0][oldIdx] == 0 {
		t.Fatalf("publish at shape index 0 wrote no coverage at the observer tile")
	}

	// Move the observer. The throttle's origin test fires (CX/CZ changed), so
	// the old footprint must be removed before the new one publishes.
	s.Refresh(1, Observer{Owner: 0, CX: 40, CZ: 40, HeightByte: 0, Radius: radius})

	if got := s.byteGrids[0][oldIdx]; got != 0 {
		t.Fatalf("old observer tile count = %d after move, want 0: a shape-index-0 "+
			"sprite footprint was not removed [03 R-VIS-01 §2]", got)
	}
	newIdx := int(40*s.W + 40)
	if s.byteGrids[0][newIdx] == 0 {
		t.Fatalf("new observer tile carries no coverage after the move")
	}
}

// TestRemoveFootprintRayBranchKeepsZeroGuard locks the other half of the same
// contract: the ray branch's removal call keeps its storedByte != 0 guard, so
// a footprint published with heightByte 0 is never removed through it, while a
// nonzero one is [03 R-VIS-01 §2].
//
// The ray table declares zero lines, so only the unconditionally-admitted
// origin cell is ever covered [03 R-VIS-01 §2] "Group 0" — small enough
// radii select that group — which keeps the fixture to one cell per observer.
func TestRemoveFootprintRayBranchKeepsZeroGuard(t *testing.T) {
	terrain := flatTerrain(128, 10)
	s := New(terrain, ModeHistoryEnabled|ModeCurrentEnabled|ModeTerrainRay)
	s.SetRayTables(&content.LOSTables{
		NumTables: 1,
		Tables:    []content.LOSTable{{TableNum: 0, NumLines: 0}},
	})
	s.SetLocal(0)

	// Nonzero stored height byte: the old footprint IS removed on move.
	s.Refresh(1, Observer{Owner: 0, CX: 10, CZ: 10, HeightByte: 20, Radius: 20})
	nzIdx := int(10*s.W + 10)
	if s.byteGrids[0][nzIdx] == 0 {
		t.Fatal("publish wrote no coverage at the observer's origin cell")
	}
	s.Refresh(1, Observer{Owner: 0, CX: 40, CZ: 40, HeightByte: 20, Radius: 20})
	if got := s.byteGrids[0][nzIdx]; got != 0 {
		t.Fatalf("old tile count = %d after move, want 0: a nonzero-height-byte ray "+
			"footprint must be removed [03 R-VIS-01 §2]", got)
	}

	// Zero stored height byte: the guard blocks removal, so the old tile's
	// count is left standing after the move [03 R-VIS-01 §2].
	s.Refresh(2, Observer{Owner: 0, CX: 15, CZ: 15, HeightByte: 0, Radius: 20})
	zIdx := int(15*s.W + 15)
	if s.byteGrids[0][zIdx] == 0 {
		t.Fatal("publish wrote no coverage at the observer's origin cell")
	}
	s.Refresh(2, Observer{Owner: 0, CX: 45, CZ: 45, HeightByte: 0, Radius: 20})
	if got := s.byteGrids[0][zIdx]; got == 0 {
		t.Fatalf("old tile count = %d after move, want it left standing: the ray "+
			"branch's storedByte != 0 guard must block removal of a zero-byte "+
			"footprint [03 R-VIS-01 §2]", got)
	}
}
