package main

import (
	"fmt"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/movement"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/render"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// These are authored benchmark composition choices, not gameplay rules.
var coastalLandRoster = [][]string{
	{"armflash", "armstump", "armpw", "armrock", "armham", "armzeus", "armwar", "armbull", "armfido", "armmav", "armmerl", "armmart"},
	{"corraid", "corlevlr", "corak", "corstorm", "corthud", "corpyro", "correap", "corgol", "corcan", "corsumo", "corvroc", "corhrk"},
}

// Lead with submarines; surface ships that stop to fire must not block their approach.
var coastalSeaRoster = [][]string{
	{"armsub", "armsub", "armbats", "armcrus", "armroy", "armpt"},
	{"corsub", "corsub", "corbats", "corcrus", "corroy", "corpt"},
}
var coastalAirRoster = [][]string{
	{"armfig", "armthund", "armhawk", "armbrawl"},
	{"corveng", "corshad", "corvamp", "corape"},
}
var coastalLandBuildings = [][]string{
	{"armsolar", "armlab", "armllt", "armrad"},
	{"corsolar", "corlab", "corllt", "corrad"},
}
var coastalWaterBuildings = [][]string{
	{"armtide", "armuwms", "armfrt", "armtl"},
	{"cortide", "coruwms", "corfrt", "cortl"},
}

func coastalBenchmarkMobiles() [][]string {
	roster := make([][]string, 2)
	for side := range roster {
		roster[side] = append(roster[side], coastalLandRoster[side]...)
		roster[side] = append(roster[side], coastalSeaRoster[side]...)
		roster[side] = append(roster[side], coastalAirRoster[side]...)
	}
	return roster
}

type coastalBenchmarkScene struct {
	X, Z             int32
	LandPerSide      int           `json:"land_per_side"`
	NavalPerSide     int           `json:"naval_per_side"`
	AirPerSide       int           `json:"air_per_side"`
	BuildingsPerSide int           `json:"buildings_per_side"`
	WaterBuildings   [][]string    `json:"water_buildings"`
	Factories        []*units.Unit `json:"-"`
}

func stageCoastalBenchmark(opts Options, s *session.Session) (*coastalBenchmarkScene, error) {
	if !strings.EqualFold(opts.Map, "expanded confluence") {
		return nil, fmt.Errorf("nanolathe: benchmark map mismatch: logical path %s, providers searched [fixture], expected Expanded Confluence", opts.Map)
	}
	scene := &coastalBenchmarkScene{X: 7296, Z: 10592, LandPerSide: 120, NavalPerSide: 24, AirPerSide: 16, BuildingsPerSide: 20, WaterBuildings: coastalWaterBuildings}
	cx, cz := scene.X, scene.Z
	type box struct{ x0, z0, x1, z1 int32 }
	var reserved []box
	// Search a small, deterministic neighbourhood of each formation slot. The
	// definition's own terrain rules admit the whole footprint; reserve separate
	// rectangles so a direct-created fixture cannot overlap yards or ships.
	create := func(name string, side int, x, z int32) (*units.Unit, error) {
		def, ok := s.Catalog.Unit(name)
		if !ok {
			return nil, fmt.Errorf("nanolathe: benchmark unit missing: logical path %s, providers searched [catalog], expected an authored unit definition", name)
		}
		fx, fz := numeric.Fixed(x)<<16, numeric.Fixed(z)<<16
		fy := s.World.HeightAt(fx, fz)
		if !def.CanFly {
			fw, fd := world.FootprintForUnit(s.Catalog, def)
			found := false
			best := int64(1 << 62)
			var chosen box
			var siteHeight int32
			for dz := int32(-192); dz <= 192; dz += 16 {
				for dx := int32(-192); dx <= 192; dx += 16 {
					px, pz := numeric.Fixed(x+dx)<<16, numeric.Fixed(z+dz)<<16
					ax, az := world.PlacementAnchor(px, pz, fw, fd)
					px, pz = world.PlacementCenter(ax, az, fw, fd)
					if (side == 0 && px >= numeric.Fixed(cx-32)<<16) || (side == 1 && px <= numeric.Fixed(cx+32)<<16) {
						continue
					}
					if pz < numeric.Fixed(cz-624)<<16 || pz > numeric.Fixed(cz+544)<<16 {
						continue
					}
					candidate := box{ax, az, ax + fw, az + fd}
					overlap := false
					for _, r := range reserved {
						if candidate.x0 < r.x1 && candidate.x1 > r.x0 && candidate.z0 < r.z1 && candidate.z1 > r.z0 {
							overlap = true
							break
						}
					}
					if overlap {
						continue
					}
					distance := int64(px-fx)*int64(px-fx) + int64(pz-fz)*int64(pz-fz)
					if distance >= best {
						continue
					}
					placement, err := coastalBenchmarkPlacement(s, def, px, pz, 0)
					if err != nil {
						continue
					}
					best, chosen, found = distance, candidate, true
					siteHeight = placement.SiteHeight
				}
			}
			if !found {
				return nil, fmt.Errorf("nanolathe: coastal benchmark placement failed: logical path %s at %d,%d, providers searched [map], expected a free authored footprint", name, x, z)
			}
			reserved = append(reserved, chosen)
			fx, fz = world.PlacementCenter(chosen.x0, chosen.z0, fw, fd)
			fy = numeric.Fixed(siteHeight) << 16
		}
		mode := units.CreatedMoverMode
		if def.CanFly {
			// Seed the fixture already airborne. Subsequent motion uses the ordinary
			// flight integrator and patrol orders [04 §10.1][04 R-AIR-01 §6].
			fy = movement.CruiseAltitudeForOffset(s.World, fx, fz, def.CruiseAlt)
			mode = 2
		} else if def.Floater {
			// Authored surface draft, including submerged mobile units [04 R-MOV-01 §9].
			fy = numeric.Fixed(int64(s.World.SeaLevel)-int64(def.Waterline)) << 16
		}
		h, err := s.Units.CreateWithMoverMode(def, uint8(side), fx, fy, fz, mode)
		if err != nil {
			return nil, err
		}
		u := s.Units.Unit(h)
		s.Movement.EnsureUnit(u)
		s.BindStagedOrderQueue(u)
		return u, nil
	}
	// Buildings first, so the mobile grid cannot consume their yards.
	for side := 0; side < 2; side++ {
		facing := int32(side*2 - 1)
		for i := 0; i < 20; i++ {
			name := coastalLandBuildings[side][i%4]
			x, z := cx+facing*(736+int32(i%2)*112), cz-32+int32(i/2)*64
			if i >= 16 {
				name = coastalWaterBuildings[side][i-16]
				x = cx + facing*768
				z = cz - 432 + int32(i-16)*88
			}
			u, err := create(name, side, x, z)
			if err != nil {
				return nil, err
			}
			if opts.BenchmarkFactories && (name == "armlab" || name == "corlab") {
				product := "armpw"
				if side == 1 {
					product = "corak"
				}
				if err := construction.QueueFactoryBuild(u, product, 10, s.Catalog); err != nil {
					return nil, err
				}
				// Products inherit a patrol through ordinary rally inheritance
				// [05 "Rally inheritance"], so completed infantry joins the fight.
				q := orders.QueueForUnit(u)
				id := orders.Lookup("QPatrol")
				gx := numeric.Fixed(cx-facing*160) << 16
				q.Push(id, orders.NewNodeForOrder(id, 0, gx, s.World.HeightAt(gx, u.Z), u.Z, s.Clock.GlobalTick, u.Handle, false))
				scene.Factories = append(scene.Factories, u)
			}
		}
	}
	for side := 0; side < 2; side++ {
		facing := int32(side*2 - 1)
		for i := 0; i < 160; i++ {
			name := coastalLandRoster[side][i%12]
			x, z := cx+facing*(96+int32(i%12)*48), cz-32+int32(i/12)*48
			switch {
			case i >= 144:
				j := int32(i - 144)
				name = coastalAirRoster[side][j%4]
				x, z = cx+facing*(200+(j*97)%500), cz-400+(j*67)%760
				// Half the flight is already on its return leg over the opposing
				// formation. Its normal LOS uncovers combat while leaving a fog fringe.
				if j%2 == 0 {
					x = cx - facing*(180+(j*97)%450)
				}
			case i >= 120:
				j := int32(i - 120)
				name = coastalSeaRoster[side][j%6]
				x, z = cx+facing*(160+j%6*88), cz-544+j/6*64
			}
			u, err := create(name, side, x, z)
			if err != nil {
				return nil, err
			}
			goalX := cx - facing*160
			if u.Def.CanFly {
				goalX = cx - facing*560
				if i%2 == 0 {
					goalX = cx + facing*560
				}
			}
			filmOrder(s, u, 9, numeric.Fixed(goalX)<<16, u.Z)
		}
	}
	fmt.Printf("coastal center=%d,%d mobiles_per_side=160 buildings_per_side=20\n", cx, cz)
	return scene, nil
}

// Surviving fixture cohorts with geometric in-view anchor counts, not pixel
// visibility, whole-model submergence, or proof of a reflection.
func addCoastalBenchmarkCensus(sample map[string]any, f *frame.Frame, s *session.Session, cam *camera.Camera) {
	counts := map[string]int{"airborne_units": 0, "surface_ships": 0, "submarines": 0, "water_buildings": 0, "in_view_airborne_units": 0, "in_view_surface_ships": 0, "in_view_submarines": 0, "in_view_water_buildings": 0, "view_fog_cells": 0, "fogged_view_cells": 0, "unexplored_view_cells": 0}
	for _, u := range f.Units {
		def, ok := s.Catalog.Unit(u.DefName)
		if !ok {
			continue
		}
		key := ""
		switch {
		case def.CanFly && u.MoverMode == 2:
			key = "airborne_units"
		case def.MinWaterDepth > 0 && def.BMCode == 0:
			key = "water_buildings"
		case strings.EqualFold(u.DefName, "armsub") || strings.EqualFold(u.DefName, "corsub"):
			key = "submarines"
		case def.MinWaterDepth > 0:
			key = "surface_ships"
		}
		if key == "" {
			continue
		}
		counts[key]++
		x, y := cam.WorldToScreen(u.X, u.Y, u.Z)
		if x >= camera.OriginX && x < 1920 && y >= camera.OriginY && y < 1048 {
			counts["in_view_"+key]++
		}
	}
	// Count projected fog-cell centres as a diagnostic proxy, not pixel coverage.
	fog := f.Fog
	if fog.Valid {
		for z := int32(0); z < fog.H; z++ {
			for x := int32(0); x < fog.W; x++ {
				x0, y0, x1, y1 := render.FogScreenRect(cam, fog.OriginX+x, fog.OriginZ+z)
				sx, sy := (x0+x1)/2, (y0+y1)/2
				if sx < camera.OriginX || sx >= 1920 || sy < camera.OriginY || sy >= 1048 {
					continue
				}
				i := int(z*fog.W + x)
				counts["view_fog_cells"]++
				if fog.Ch0[i] != 0 || fog.Ch1[i] != 0 {
					counts["fogged_view_cells"]++
				}
				if fog.Ch0[i] != 0 {
					counts["unexplored_view_cells"]++
				}
			}
		}
	}
	for key, value := range counts {
		sample[key] = value
	}
}

// Use the same footprint, yard and terrain admission as normal construction;
// only formation selection is benchmark policy.
func coastalBenchmarkPlacement(s *session.Session, def *content.UnitDef, x, z numeric.Fixed, self uint16) (world.PlacementResult, error) {
	fw, fd := world.FootprintForUnit(s.Catalog, def)
	extent, err := world.NewFootprintExtent(fw, fd)
	if err != nil {
		return world.PlacementResult{}, err
	}
	ax, az := world.PlacementAnchor(x, z, fw, fd)
	rect, err := world.NewFootprintRect(world.NewFootprintAnchor(ax, az), extent)
	if err != nil {
		return world.PlacementResult{}, err
	}
	rules, err := world.PlacementRulesForUnit(s.Catalog, def)
	if err != nil {
		return world.PlacementResult{}, err
	}
	q := world.PlacementQuery{Rect: rect, Rules: rules, Mobile: def.BMCode != 0, Self: self}
	if !q.Mobile {
		q.Yard, err = world.ParseYardMap(def.YardMap, int(fw), int(fd))
		if err != nil {
			return world.PlacementResult{}, err
		}
	}
	return s.World.CheckPlacement(q)
}
