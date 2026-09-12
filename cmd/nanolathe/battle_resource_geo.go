package main

import (
	"slices"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Geothermal capability comes from the parsed yard's requirement bit, not a
// name or energy-output guess [05 "Geothermal requirement"][fmt fbi]. Parsing
// matters: a trailing G beyond the footprint does not require a vent.
func resourceGeoYard(u *content.UnitDef) []world.YardCell {
	if u == nil || u.BMCode != 0 || u.Builder || !strings.Contains(u.YardMap, "G") {
		return nil
	}
	yard, err := world.ParseYardMap(u.YardMap, int(u.FootprintX), int(u.FootprintZ))
	if err != nil || !slices.ContainsFunc(yard, func(y world.YardCell) bool { return y&0x80 != 0 }) {
		return nil
	}
	return yard
}

// Only a G cell over the chosen vent qualifies. Merely covering a vent with
// another part of a custom yard, or moving onto a different vent, is not the
// same construction site [05 "Geothermal requirement"].
func resourceCoversVent(r, vent resourceRect, yard []world.YardCell) bool {
	for i, y := range yard {
		if y&0x80 != 0 && (resourceRect{r.x + int32(i)%r.w, r.z + int32(i)/r.w, 1, 1}).overlaps(vent) {
			return true
		}
	}
	return false
}

func resourceVentSite(product *content.UnitDef, vent resourceRect) (resourceBuildSite, bool) {
	yard := resourceGeoYard(product)
	if len(yard) == 0 {
		return resourceBuildSite{}, false
	}
	fx, fz := product.FootprintX, product.FootprintZ
	wx, wz := world.PlacementCenter(vent.x, vent.z, vent.w, vent.h)
	cx, cz := world.PlacementAnchor(wx, wz, fx, fz)
	var best resourceBuildSite
	var bestDistance int64
	found := false
	// Choose the closest valid yard alignment to a centered footprint. Row
	// order resolves ties, matching modern spacing policy (§3.10).
	for z := vent.z - fz + 1; z < vent.z+vent.h; z++ {
		for x := vent.x - fx + 1; x < vent.x+vent.w; x++ {
			if !resourceCoversVent(resourceRect{x, z, fx, fz}, vent, yard) {
				continue
			}
			dx, dz := int64(x-cx), int64(z-cz)
			d := dx*dx + dz*dz
			if !found || d < bestDistance {
				best = resourceBuildSite{product: product, x: x, z: z, deposit: vent, geothermal: true}
				bestDistance, found = d, true
			}
		}
	}
	return best, found
}

// "Near" is the authored plant footprint around the clicked ground point,
// not an invented screen-space radius. Permanent vent identity survives fog,
// like metal deposits; normal placement visibility still decides admission.
func (b *battleSession) nearbyResourceVent(f *frame.Frame, product *content.UnitDef, wx, wz numeric.Fixed) (resourceBuildSite, bool) {
	fx, fz := footprintCellsForCatalog(b.cat, product)
	x, z := world.PlacementAnchor(wx, wz, fx, fz)
	click := resourceRect{x, z, fx, fz}
	var nearest resourceRect
	var bestDistance int64
	found := false
	for _, v := range f.Features {
		fd := b.cat.Features[content.CanonicalKey(v.DefName)]
		vent := resourceRect{v.CX, v.CZ, int32(v.FootX), int32(v.FootZ)}
		if fd == nil || !fd.Geothermal || !click.overlaps(vent) {
			continue
		}
		vx, vz := world.PlacementCenter(vent.x, vent.z, vent.w, vent.h)
		// Keep fractional terrain-picking coordinates: flooring signed deltas
		// separately can make a farther vent tie with the nearest one.
		dx, dz := int64(wx-vx), int64(wz-vz)
		d := dx*dx + dz*dz
		if !found || d < bestDistance {
			nearest, bestDistance, found = vent, d, true
		}
	}
	if !found {
		return resourceBuildSite{}, false
	}
	return resourceVentSite(product, nearest)
}
