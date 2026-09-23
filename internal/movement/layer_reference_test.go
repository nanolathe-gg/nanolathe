package movement

// The per-anchor classifier, kept as the reference the restamp's tier cache
// is tested against (layer_restamp_test.go) and the form the classifier tests
// state single anchors in. Production classifies anchors through RestampRect,
// which reads each cell's tier once per restamp (fillRestampTiers,
// classifyTiers); this is the same rule, one anchor at a time.

// classify stamps one candidate anchor for this movement class. Every covered
// cell is classified on its own derived pair and the anchor takes the MINIMUM
// tier over the footprint; a clear result is then demoted to steep when any
// cell of the surrounding one-cell ring is non-clear [04 R-SLOPE-01 §3]
// [04 R-PATH-01 §2][04 R-MOV-03 §3].
//
// This is the closed form of the map-load builder's two separable window
// minima: 0 iff any footprint cell is 0, 3 iff every cell of the
// (fx+2) × (fz+2) footprint-plus-ring rectangle is 3, 1 otherwise
// [04 R-SLOPE-01 §3 item 1]. Corrected by WU-19-46: the footprint used to be
// classified on one min-of-mins/max-of-maxes height span, a form that belongs
// to the structure placement validator alone and that judged a 2×2 class on
// the height range of a 3×3 corner window.
//
// The bound is the map-load builder's: 0 exactly when the footprint leaves the
// map. RestampRect adds the restamp's stricter one on top [04 R-SLOPE-01 §3].
func (l *ClassLayer) classify(cx, cz int32) uint8 {
	fx, fz := l.footprintSize()
	if l.Terrain == nil || cx < 0 || cz < 0 || cx+fx > l.W || cz+fz > l.H {
		return LayerBlocked
	}
	result := l.classifyRect(cx, cz, cx+fx-1, cz+fz-1)
	if result != LayerClear {
		return result
	}
	// The four strips cover the complete ring. Overlapping corner reads do not
	// change the all-clear predicate [04 R-MOV-03 §3].
	if l.classifyRect(cx-1, cz-1, cx+fx, cz-1) != LayerClear ||
		l.classifyRect(cx+fx, cz-1, cx+fx, cz+fz) != LayerClear ||
		l.classifyRect(cx-1, cz+fz, cx+fx, cz+fz) != LayerClear ||
		l.classifyRect(cx-1, cz-1, cx-1, cz+fz) != LayerClear {
		return LayerSteep
	}
	return LayerClear
}

// classifyRect runs the per-cell chain on each cell of an inclusive rectangle
// and returns the MINIMUM tier over those cells [04 R-SLOPE-01 §3 item 2]: a
// blocked cell returns immediately, a steep cell lowers a running clear to
// steep. Cells outside the map are tier 0, so a rectangle leaving the map is
// blocked.
func (l *ClassLayer) classifyRect(x1, z1, x2, z2 int32) uint8 {
	if l.Terrain == nil || x2 < x1 || z2 < z1 {
		return LayerBlocked
	}
	result := LayerClear
	for z := z1; z <= z2; z++ {
		for x := x1; x <= x2; x++ {
			switch l.classifyCell(x, z) {
			case LayerBlocked:
				return LayerBlocked
			case LayerSteep:
				result = LayerSteep
			}
		}
	}
	return result
}
