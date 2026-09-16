package visibility

import (
	"github.com/nanolathe-gg/nanolathe/internal/content"
)

// Authored raster inputs [03 §3.2].
//
// Neither raster synthesizes its shape. The sprite-mask path indexes the
// visibility-mask GAF compiled into content.SightShapes; the terrain-ray path
// walks the line lists of the LOS.TDF table its radius selects.

// step is one position along a spoke. dist counts from one [03 §3.2] C5.
type step struct {
	dx, dz int32
	dist   int32
}

// SetShapes binds the authored sight shapes [03 §3.2].
func (s *Service) SetShapes(sh *content.SightShapes) {
	if s != nil {
		s.shapes = sh
	}
}

// SetRayTables binds the parsed LOS.TDF tables and rebuilds the spoke cache
// [03 §3.2].
func (s *Service) SetRayTables(lt *content.LOSTables) {
	if s == nil {
		return
	}
	s.rayTables = lt
	s.spokeCache = nil
}

// rayTableCount is the clamp bound for the terrain-ray group index.
//
// It is the DECLARED numtables and nothing else: the reference install declares
// nine and ships twelve, and the declared value is the one the loader sizes its
// table list to, with an empty record wherever a declared slot has no section
// (docs/SPEC_CONFLICTS.md SC9, [03 R-COMP-02 §1]). Clamping by len(Tables) would
// reach three tables retail never loads, and would also shrink the bound for
// content that declares more tables than it ships, where retail keeps the
// declared bound and finds the missing slots empty.
func (s *Service) rayTableCount() int {
	if s == nil || s.rayTables == nil {
		return 0
	}
	n := int(s.rayTables.NumTables)
	if n < 0 {
		n = 0
	}
	return n
}

// raySpokes returns the spoke list held in a zero-based table SLOT, building it
// once.
//
// The argument is a storage slot, not a group: the table-by-index accessor is
// one-based, so the caller passes g-1 for group g [03 R-COMP-02 §1]. Slot d was
// filled from the section named TABLE d+1 — the loader builds that name from
// the slot — so the two off-by-ones cancel and group g walks TABLE g, whose
// authored extent is exactly g cells. The surviving skew is at the top: the
// clamp stops at numtables-1, so the last loaded table, TABLE numtables, is
// never selected.
//
// The len(Tables) bound is a guard against a hand-built table list shorter than
// its declared count, not a second clamp: a compiled list always materializes
// every slot up to its highest authored section, and a slot past that end reads
// as the same empty line list.
func (s *Service) raySpokes(slot int) [][]step {
	if s == nil || s.rayTables == nil || slot < 0 || slot >= s.rayTableCount() {
		return nil
	}
	if slot >= len(s.rayTables.Tables) {
		return nil
	}
	if s.spokeCache == nil {
		s.spokeCache = make([][][]step, len(s.rayTables.Tables))
	}
	if s.spokeCache[slot] != nil {
		return s.spokeCache[slot]
	}
	built := buildSpokes(s.rayTables.Tables[slot])
	if built == nil {
		// Non-nil marks a line-less slot as built, so the empty result is
		// cached instead of re-derived for every observer.
		built = [][]step{}
	}
	s.spokeCache[slot] = built
	return built
}

// buildSpokes converts one LOS.TDF table into walkable spokes [03 §3.2].
//
// A line is `count, (dx, dz) × count` — offsets from the observer in order of
// increasing distance, so the i-th pair carries step distance i. The authored
// offsets are all non-negative and span exactly north through east, one
// quadrant: TABLE2's four lines are (0,1)(0,2), (0,1)(1,2), (1,1) and
// (1,0)(2,1).
//
// Authored pairs are absolute positions from the observer, and each line is
// expanded by four 90-degree rotations [03 §3.2]. Axis spokes can therefore
// occur twice when the source table contains both orientations; publish and
// unpublish walk the same expanded list, keeping reference counts balanced.
func buildSpokes(tb content.LOSTable) [][]step {
	var out [][]step
	for _, line := range tb.Lines {
		if len(line) < 3 {
			continue
		}
		count := int(line[0])
		if count <= 0 || len(line) < 1+2*count {
			continue
		}
		base := make([]step, 0, count)
		for i := 0; i < count; i++ {
			base = append(base, step{
				dx:   line[1+2*i],
				dz:   line[2+2*i],
				dist: int32(i + 1), // step distances count from one [C5]
			})
		}
		// Four 90-degree rotations: (dx,dz) -> (dz,-dx) -> (-dx,-dz) -> (-dz,dx).
		for rot := 0; rot < 4; rot++ {
			spoke := make([]step, len(base))
			for i, st := range base {
				dx, dz := st.dx, st.dz
				for r := 0; r < rot; r++ {
					dx, dz = dz, -dx
				}
				spoke[i] = step{dx: dx, dz: dz, dist: st.dist}
			}
			out = append(out, spoke)
		}
	}
	return out
}
