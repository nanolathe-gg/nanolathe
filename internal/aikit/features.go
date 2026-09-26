package aikit

import (
	"cmp"
	"slices"

	"github.com/nanolathe-gg/nanolathe/internal/features"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Features (trees, rocks, wrecks) as the executor sees them when it keeps
// factory exits open. They are visible map content; the executor reads them
// only around the owner's own factories.

// featureBlock classifies the feature covering cell (cx, cz): none, a
// removable blocker (a reclaimable blocking feature, reported with its
// anchor cell), or a permanent one.
func (e *executor) featureBlock(cx, cz int32) (blocking, removable bool, ax, az int32) {
	t := e.m.Terrain
	cell := t.PlotAt(cx, cz)
	if cell == nil || (!cell.IsRealFeature() && !cell.IsFringe()) {
		return false, false, 0, 0
	}
	def, fx, fz, ok := features.FeatureAt(t, numeric.Fixed(int64(cx*16+8)<<16), numeric.Fixed(int64(cz*16+8)<<16))
	if !ok || def == nil || !def.Blocking {
		return false, false, 0, 0
	}
	return true, def.Reclaimable && !def.Indestructible, int32(fx), int32(fz)
}

// reclaimFeature queues a reclaim of the feature anchored at cell (ax, az)
// on the builder's queue; replace purges the queue first. It reports
// whether the order resolved.
func (e *executor) reclaimFeature(u *units.Unit, q *orders.Queue, ax, az int32, tick uint32, replace bool) bool {
	x := numeric.Fixed(int64(ax*16+8) << 16)
	z := numeric.Fixed(int64(az*16+8) << 16)
	var y numeric.Fixed
	if t := e.m.Terrain; t != nil {
		y = t.HeightAt(x, z)
	}
	id := orders.Resolve(orderCodes[CmdReclaim], u, nil, &orders.ResolvePos{X: x, Y: y, Z: z, HasFeature: true})
	if id == 0 {
		return false
	}
	if replace {
		q.PurgeUnprotected()
		q.DropLeadingAutoOps()
	}
	q.Push(id, orders.NewNodeForOrder(id, 0, x, y, z, tick, u.Handle, !replace))
	return true
}

// reclaimUnit queues a reclaim of one of the owner's own buildings.
func (e *executor) reclaimUnit(u *units.Unit, q *orders.Queue, target *units.Unit, tick uint32, replace bool) bool {
	id := orders.Resolve(orderCodes[CmdReclaim], u, target, &orders.ResolvePos{X: target.X, Y: target.Y, Z: target.Z})
	if id == 0 {
		return false
	}
	if replace {
		q.PurgeUnprotected()
		q.DropLeadingAutoOps()
	}
	q.Push(id, orders.NewNodeForOrder(id, target.Handle, target.X, target.Y, target.Z, tick, u.Handle, !replace))
	return true
}

// buildingAt returns the building whose occupancy covers cell (cx, cz), or
// nil (an empty cell or a mobile unit, which moves).
func (e *executor) buildingAt(w *units.World, cell *world.PlotCell) *units.Unit {
	occ := cell.OccupantA()
	if occ == 0 {
		return nil
	}
	u := w.Unit(pool.Handle(uint16(occ)))
	if u == nil || u.Def == nil || u.Def.BMCode != 0 {
		return nil
	}
	return u
}

// refreshFeatures lists the features near the start into the observation:
// the maxFeatures nearest the start when more stand within FeatureRadius (a
// first-come cut in row order kept only the northern ones, missing lane
// blockers south of the factories), listed in row order. It runs on the
// simulation thread when a batch is applied, so no think is reading the
// observation then.
func (e *executor) refreshFeatures(tick uint32) {
	o, m, t := e.obs, e.mapInfo, e.m.Terrain
	if o == nil || m == nil || t == nil || (o.FeaturesTick != 0 && tick-o.FeaturesTick < FeatureEvery) {
		return
	}
	o.FeaturesTick = tick
	o.Features = o.Features[:0]
	r := int32(FeatureRadius)
	cx0, cz0 := (m.HomeX-r)/16, (m.HomeZ-r)/16
	cx1, cz1 := (m.HomeX+r)/16, (m.HomeZ+r)/16
	if cx0 < 0 {
		cx0 = 0
	}
	if cz0 < 0 {
		cz0 = 0
	}
	if cx1 >= m.CellW {
		cx1 = m.CellW - 1
	}
	if cz1 >= m.CellH {
		cz1 = m.CellH - 1
	}
	lim := int64(r) * int64(r)
	e.featDist = e.featDist[:0]
	for cz := cz0; cz <= cz1; cz++ {
		for cx := cx0; cx <= cx1; cx++ {
			cell := t.PlotAt(cx, cz)
			if cell == nil || !cell.IsRealFeature() {
				continue // anchor cells only: each feature once
			}
			def, ok := t.FeatureDefAt(cell.Feature())
			if !ok || def == nil || (!def.Blocking && def.Metal <= 0) {
				continue
			}
			fx, fz := def.FootprintX, def.FootprintZ
			if fx < 1 {
				fx = 1
			}
			if fz < 1 {
				fz = 1
			}
			x, z := cx*16+fx*8, cz*16+fz*8
			d := Dist2(x, z, m.HomeX, m.HomeZ)
			if d > lim {
				continue
			}
			o.Features = append(o.Features, Feature{X: x, Z: z, FootX: fx, FootZ: fz, Metal: def.Metal, Energy: def.Energy,
				Blocking: def.Blocking, Reclaimable: def.Reclaimable && !def.Indestructible})
			e.featDist = append(e.featDist, d)
		}
	}
	if len(o.Features) <= maxFeatures {
		return
	}
	// Nearest first, row order breaking ties; then keep those in row order.
	e.featOrder = e.featOrder[:0]
	for i := range o.Features {
		e.featOrder = append(e.featOrder, int32(i))
	}
	dist := e.featDist
	slices.SortFunc(e.featOrder, func(a, b int32) int {
		if dist[a] != dist[b] {
			return cmp.Compare(dist[a], dist[b])
		}
		return cmp.Compare(a, b)
	})
	cut := dist[e.featOrder[maxFeatures-1]]
	last := e.featOrder[maxFeatures-1] // the last kept among features at the cut distance
	n := 0
	for i := range o.Features {
		if dist[i] < cut || (dist[i] == cut && int32(i) <= last) {
			o.Features[n] = o.Features[i]
			n++
		}
	}
	o.Features = o.Features[:n]
}
