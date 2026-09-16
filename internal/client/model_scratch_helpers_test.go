package client

// Allocation helpers the tests use to stand in for the recorder's scratch. The
// recording path borrows its polygons from polyScratch.next and its images from
// Client.borrowModelImage; neither shape is convenient for a test that wants one
// standalone face or one standalone composition image, so the standalone forms
// live here rather than in production source.

// newScreenPoly allocates one face's corner lanes out of a single backing
// array, the same lane layout polyScratch.next carves out of its arena.
func newScreenPoly(n int) screenPoly {
	buf := make([]int32, n*(polyLanes+spanAttrs))
	p := screenPoly{
		x:         buf[0:n:n],
		y:         buf[n : 2*n : 2*n],
		x2:        buf[2*n : 3*n : 3*n],
		y2:        buf[3*n : 4*n : 4*n],
		oddHeight: make([]bool, n),
		heights:   make([]float32, n),
	}
	for k := 0; k < spanAttrs; k++ {
		lo := (polyLanes + k) * n
		p.attr[k] = buf[lo : lo+n : lo+n]
	}
	return p
}

// newModelTarget is newModelImage at the origin with a height plane, the shape
// a full-framebuffer composition target takes.
func newModelTarget(width, height int) *modelTarget {
	return newModelImage(width, height, 0, 0, 0, 0, true, 1)
}

// modelExtent measures an already projected polygon envelope the way
// Client.projectedModelExtent measures an unprojected one: extrema seeded at
// the model origin, with the composition margin on every side
// [03 R-REN-03A §1]. The recording path measures before it projects, so this
// form exists only for raster tests that start from corners.
func modelExtent(polys []screenPoly) (width, height int, originX, originY int32) {
	var minX, minY, maxX, maxY int32 // seeded at the model origin, not at a vertex
	for i := range polys {
		xs, ys := polys[i].x, polys[i].y
		for k := range xs {
			x, y := xs[k], ys[k]
			minX, maxX = min(minX, x), max(maxX, x)
			minY, maxY = min(minY, y), max(maxY, y)
		}
	}
	originX = modelTargetMargin - minX
	originY = modelTargetMargin - minY
	return int(maxX - minX + 2*modelTargetMargin), int(maxY - minY + 2*modelTargetMargin), originX, originY
}
