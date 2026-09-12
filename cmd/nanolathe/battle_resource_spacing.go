package main

// Queue avoidance is modern input policy (DESIGN_INTERFACE_HUD_INPUT §3.10).
// It resolves a site before sending the ordinary typed construction command.

import (
	"cmp"
	"slices"

	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

type resourceRect struct{ x, z, w, h int32 }
type resourceCell struct{ x, z int32 }

func (a resourceRect) overlaps(b resourceRect) bool {
	return a.x < b.x+b.w && b.x < a.x+a.w && a.z < b.z+b.h && b.z < a.z+a.h
}

// Include every local builder, even when unselected, and builds awaiting the
// next tick. Reading copied input intent closes the gap between two gestures
// before publication; neither live order nodes nor simulation state are read.
func (b *battleSession) resourceReservations() ([]resourceRect, bool) {
	f, ok := b.currentSnapshot()
	if !ok {
		return nil, false
	}
	local := make(map[pool.Handle]bool)
	for _, u := range f.Units {
		local[u.Slot] = u.Owner == b.sess.LocalOwner
	}
	var rects []resourceRect
	seen := make(map[resourceRect]bool)
	add := func(product string, wx, wz numeric.Fixed) bool {
		def, ok := b.cat.Unit(product)
		if !ok || def == nil {
			return false
		}
		if def.BMCode != 0 {
			return true // mobile products do not reserve a building footprint
		}
		fx, fz := footprintCellsForCatalog(b.cat, def)
		x, z := world.PlacementAnchor(wx, wz, fx, fz)
		r := resourceRect{x, z, fx, fz}
		if !seen[r] {
			seen[r] = true
			rects = append(rects, r)
		}
		return true
	}
	for _, q := range f.OrderQueues {
		if !local[q.Unit] {
			continue
		}
		if q.PrimaryTruncated || q.SecondaryTruncated {
			return nil, false // an incomplete queue cannot prove a site clear
		}
		for _, list := range [][]frame.OrderView{q.Primary, q.Secondary} {
			for _, o := range list {
				if orders.IsMobileBuild(orders.ID(o.DescriptorID)) && !add(o.BuildProduct, o.GoalX, o.GoalZ) {
					return nil, false
				}
			}
		}
	}
	for _, c := range b.sess.PendingHumanCommands() {
		if c.Kind == session.HumanMobileBuild && local[c.MobileBuild.Builder] && !add(c.MobileBuild.Product, c.MobileBuild.WX, c.MobileBuild.WZ) {
			return nil, false
		}
	}
	return rects, true
}

// Candidate anchors lie on the outside edges of the connected group of
// queued footprints covering the click. Expand each obstacle by the new
// footprint to express its forbidden anchors. This searches actual building
// edges without an arbitrary radius or a scan of the whole map.
func resourceEdgeCandidates(origin resourceRect, reserved []resourceRect) []resourceCell {
	var connected []resourceRect
	used := make([]bool, len(reserved))
	for i, r := range reserved {
		if origin.overlaps(r) {
			used[i] = true
			connected = append(connected, resourceRect{r.x - origin.w, r.z - origin.h, r.w + origin.w, r.h + origin.h})
		}
	}
	for n := 0; n < len(connected); n++ {
		a := connected[n]
		for i, r := range reserved {
			if used[i] {
				continue
			}
			e := resourceRect{r.x - origin.w, r.z - origin.h, r.w + origin.w, r.h + origin.h}
			if a.x <= e.x+e.w && e.x <= a.x+a.w && a.z <= e.z+e.h && e.z <= a.z+a.h {
				used[i] = true
				connected = append(connected, e)
			}
		}
	}
	var cells []resourceCell
	seen := make(map[resourceCell]bool)
	add := func(x, z int32) {
		p := resourceCell{x, z}
		if !seen[p] {
			seen[p] = true
			cells = append(cells, p)
		}
	}
	for _, r := range connected {
		for x := r.x; x <= r.x+r.w; x++ {
			add(x, r.z)
			add(x, r.z+r.h)
		}
		for z := r.z + 1; z < r.z+r.h; z++ {
			add(r.x, z)
			add(r.x+r.w, z)
		}
	}
	distance := func(p resourceCell) int64 {
		dx, dz := int64(p.x)-int64(origin.x), int64(p.z)-int64(origin.z)
		return dx*dx + dz*dz
	}
	slices.SortFunc(cells, func(a, b resourceCell) int {
		if c := cmp.Compare(distance(a), distance(b)); c != 0 {
			return c
		}
		if c := cmp.Compare(a.z, b.z); c != 0 {
			return c
		}
		return cmp.Compare(a.x, b.x)
	})
	return cells
}

func (b *battleSession) spaceResourceBuild(site resourceBuildSite, builder pool.Handle) (resourceBuildSite, world.PlacementResult, bool) {
	reserved, complete := b.resourceReservations()
	if !complete {
		return site, world.PlacementResult{}, false
	}
	fx, fz := footprintCellsForCatalog(b.cat, site.product)
	origin := resourceRect{site.x, site.z, fx, fz}
	clear := func(r resourceRect) bool {
		for _, q := range reserved {
			if r.overlaps(q) {
				return false
			}
		}
		return true
	}
	candidates := []resourceCell{{site.x, site.z}}
	if !clear(origin) {
		candidates = resourceEdgeCandidates(origin, reserved)
	}
	for _, p := range candidates {
		r := resourceRect{p.x, p.z, fx, fz}
		if !clear(r) || (site.deposit.w > 0 && !r.overlaps(site.deposit)) {
			continue
		}
		result, err := b.checkProductPlacement(p.x, p.z, site.product, fx, fz, uint16(builder))
		if err == nil {
			site.x, site.z = p.x, p.z
			return site, result, true
		}
	}
	return site, world.PlacementResult{}, false
}
