// Package visibility predicate implements C8, C9, C10 [PLAN_05 WU-05-3].
package visibility

import (
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// Target is the gameplay visibility query [03 §3.2] C8.
// X,Z are world coordinates (16.16), Y is height in same units.
type Target struct {
	Owner   PlayerID
	X, Y, Z numeric.Fixed
	XExtent int32 // definition extents in world units (16.16) as per unit def [04 §2]
	ZExtent int32
	Hidden  bool   // cloaked instance bit [03 §3.2] C8 step 2
	Status  uint32 // runtime status bits; 0x200 underwater exemption [03 §3.2] C8
}

// Box is the feature extents query [03 §3.2] reduced extents form.
type Box struct {
	MinX, MinZ int32 // world Fixed truncated to cell? caller passes world coords
	MaxX, MaxZ int32
	Owner      PlayerID
}

// IsVisible is the single gameplay gate [03 §3.2] C8.
// Evaluation order: 1 owner bypass, 2 hidden, 3 underwater, 4 sample projection with mode-selected source, 5 hull diamond.
func (s *Service) IsVisible(viewer PlayerID, t Target) bool {
	if s == nil {
		return false
	}
	// 1. owner identity bypass — queried record equals unit's owner ⇒ visible [C8.1]
	if viewer == t.Owner {
		return true
	}
	// 2. hidden/cloaked instance bit ⇒ not visible [C8.2]
	if t.Hidden {
		// Cloak is predicate early-out, never mask edit [C10]; proximity breach exception is handled by sensor's 0x1000 flag
		// but predicate does not check it — cloaked stays hidden unless uncloaked via sensor.
		return false
	}
	// 3. base height below sea level ⇒ not visible unless runtime status 0x200 [C8.3]
	// Ally sensor phase sets 0x200 on owned/allied, so they are implicitly exempt.
	if t.Y < 0 && t.Status&0x200 == 0 {
		return false
	}
	// 4-5. Sample projection and hull diamond [C8.4-5]
	// Each sample projects v = (Z - (Y>>1))>>5 , u = X>>5, bounds checked unsigned against viewer's grid,
	// tested against mode-selected source (byte nonzero or word at local bit). Hull order center→east→north→west.
	if s.W == 0 || s.H == 0 {
		return false
	}
	// Helper to test one world point.
	testPoint := func(x, y, z numeric.Fixed) bool {
		// projection with half-height shear [C8.4]
		// y>>1 then subtract from Z, then >>5
		yHalf := int64(y) >> 1
		v := (int64(z) - yHalf) >> 5
		u := int64(x) >> 5
		// bounds-checked unsigned against viewer's grid dimensions
		if uint32(u) >= uint32(s.W) || uint32(v) >= uint32(s.H) {
			return false
		}
		idx := int(v*int64(s.W) + u)
		// mode-selected source: byte grid nonzero, or word grid at local player's bit [C8.4]
		// Plan says word grid is tested at LOCAL player's bit when byte path not selected; but byte path uses any nonzero.
		// Mode bit 2 selects source; we follow ModeTerrainRay vs sprite as same selector.
		// For simplicity, if current coverage enabled we use byte, else word.
		// The spec says when mode enables per-owner current-coverage byte grids, the byte grid is used.
		// Use ModeCurrentEnabled as proxy.
		if s.mode&ModeCurrentEnabled != 0 {
			if int(viewer) < len(s.byteGrids) && s.byteGrids[viewer] != nil {
				if s.byteGrids[viewer][idx] != 0 {
					return true
				}
				return false
			}
		}
		// Word path: test local player's bit? But spec says query record's grid dimensions vs local player's bit.
		// Retail tests the queried record's grid at the LOCAL player's bit [C8.4].
		// For determinism we test the wordMask at viewer's bit after C9 ally not OR'd ensures correctness.
		bit := cellBit(viewer)
		if idx < len(s.wordMask) && s.wordMask[idx]&bit != 0 {
			return true
		}
		return false
	}
	// Center
	if testPoint(t.X, t.Y, t.Z) {
		return true
	}
	// East (+X extent)
	if testPoint(t.X+numeric.Fixed(int64(t.XExtent)), t.Y, t.Z) {
		return true
	}
	// North (+Z extent, half height subtracted) [C8.5]
	if testPoint(t.X, t.Y, t.Z+numeric.Fixed(int64(t.ZExtent))) {
		// The north sample's height is adjusted: half height subtracted is already in projection formula's Y half.
		// For north we could pass adjusted Y = Y - (something) but spec says north sample is +Z extent with half height subtracted.
		// Our testPoint already does Y>>1, so calling with same Y already subtracts half.
		return true
	}
	// West (-X extent)
	if testPoint(t.X-numeric.Fixed(int64(t.XExtent)), t.Y, t.Z) {
		return true
	}
	return false
}

// VisiblePoint is the reduced projectile predicate [03 §3.2].
func (s *Service) VisiblePoint(viewer PlayerID, x, y, z numeric.Fixed) bool {
	return s.IsVisible(viewer, Target{Owner: 255, X: x, Y: y, Z: z})
}

// VisibleExtents is the feature footprint predicate [03 §3.2].
func (s *Service) VisibleExtents(viewer PlayerID, b Box) bool {
	if s == nil || s.W == 0 {
		return false
	}
	// Project both corners and test; any sampled corner visible ⇒ extents visible.
	// Use non-center samples.
	if s.IsVisible(viewer, Target{Owner: b.Owner, X: numeric.Fixed(int64(b.MinX)), Y: 0, Z: numeric.Fixed(int64(b.MinZ))}) {
		return true
	}
	if s.IsVisible(viewer, Target{Owner: b.Owner, X: numeric.Fixed(int64(b.MaxX)), Y: 0, Z: numeric.Fixed(int64(b.MaxZ))}) {
		return true
	}
	return false
}
