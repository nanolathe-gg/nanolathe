package visibility

import (
	"github.com/nanolathe/nanolathe/internal/content"
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
// It is the DECLARED numtables, not the number of TABLE sections discovered:
// the reference install declares nine and supplies twelve, and the declared
// value is the one the engine's table object reports (docs/SPEC_CONFLICTS.md
// SC9). Clamping by len(Tables) would reach three tables retail cannot select.
func (s *Service) rayTableCount() int {
	if s == nil || s.rayTables == nil {
		return 0
	}
	n := int(s.rayTables.NumTables)
	if n > len(s.rayTables.Tables) {
		n = len(s.rayTables.Tables)
	}
	if n < 0 {
		n = 0
	}
	return n
}

// raySpokes returns the spoke list for a LOS table index, building it once.
func (s *Service) raySpokes(index int) [][]step {
	if s == nil || index < 0 || index >= s.rayTableCount() {
		return nil
	}
	if s.spokeCache == nil {
		s.spokeCache = make([][][]step, len(s.rayTables.Tables))
	}
	if s.spokeCache[index] != nil {
		return s.spokeCache[index]
	}
	s.spokeCache[index] = buildSpokes(s.rayTables.Tables[index])
	return s.spokeCache[index]
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
