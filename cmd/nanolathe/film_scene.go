package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/units"

	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/film"
	"github.com/nanolathe-gg/nanolathe/internal/movement"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// The film scene fixture. Like the benchmark's, it places units directly
// rather than opening a normal skirmish, and its spacing and composition are
// capture choices, not retail rules (docs/FILM_CAPTURE.md "Scenes").
//
// It deliberately does not share the benchmark's stager: that fixture's
// composition is pinned by the performance baselines it feeds, and a film
// wants to reframe its armies without moving those numbers.

// filmSideUnits is the mobile roster each side fields, in placement order.
var filmSideUnits = [2][]string{
	{"armflash", "armstump", "armpw", "armrock", "armham", "armzeus", "armfav", "armwar", "armfig", "armthund"},
	{"corraid", "corlevlr", "corak", "corstorm", "corthud", "corpyro", "corfav", "correap", "corveng", "corshad"},
}

// Roster selections are capture composition choices; definitions, movement and
// weapons still come from the installed catalog.
func filmRoster(name string) ([2][]string, error) {
	switch name {
	case "", "mixed":
		return filmSideUnits, nil
	case "armor":
		return [2][]string{
			{"armflash", "armstump", "armzeus", "armwar"},
			{"corraid", "corlevlr", "correap", "corthud"},
		}, nil
	case "air":
		return [2][]string{{"armfig", "armthund"}, {"corveng", "corshad"}}, nil
	case "naval":
		return [2][]string{
			{"armbats", "armcrus", "armroy", "armpt", "armcrus", "armroy"},
			{"corbats", "corcrus", "corroy", "corpt", "corcrus", "corroy"},
		}, nil
	case "heavy":
		return [2][]string{
			{"armbull", "armfido", "armzeus", "armmav", "armmerl", "armstump"},
			{"corgol", "corcan", "correap", "corsumo", "corvroc", "corraid"},
		}, nil
	case "kbots":
		return [2][]string{
			{"armpw", "armrock", "armham", "armwar", "armzeus", "armjeth"},
			{"corak", "corstorm", "corthud", "corpyro", "corcan", "corcrash"},
		}, nil
	case "flame":
		return [2][]string{
			{"armpw", "armham", "armflash", "armwar"},
			{"corpyro", "corpyro", "corak", "corthud"},
		}, nil
	default:
		return [2][]string{}, fmt.Errorf("nanolathe: film: unknown roster %q", name)
	}
}

// filmBattleCentre allows deliberately authored coast framing without changing
// the benchmark's dry-land search or guessing a naval placement rule.
func filmBattleCentre(scene film.Scene, terrain *world.Terrain) (int32, int32, int32, error) {
	if scene.Anchor == nil {
		if x, z, relief, err := benchmarkBattleCentre(terrain); err == nil {
			return x, z, relief, nil
		}
		x, z, err := filmCoverageCentre(terrain, false)
		return x, z, 0, err
	}
	if len(scene.Anchor) != 2 || scene.Anchor[0] < 0 || scene.Anchor[0] >= terrain.PlayRight || scene.Anchor[1] < 0 || scene.Anchor[1] >= terrain.PlayBottom {
		return 0, 0, 0, fmt.Errorf("nanolathe: film: scene anchor %v is outside the playable map", scene.Anchor)
	}
	return scene.Anchor[0], scene.Anchor[1], 0, nil
}

// filmSideBuildings is the rear line: something to build, power and defend.
var filmSideBuildings = [2][]string{
	{"armsolar", "armlab", "armllt", "armrad"},
	{"corsolar", "corlab", "corllt", "corrad"},
}

// filmAircraft fly cover over any roster; fighters first, then bombers.
var filmAircraft = [2][]string{
	{"armfig", "armthund", "armhawk", "armpnix", "armbrawl"},
	{"corveng", "corshad", "corvamp", "corhurc", "corape"},
}

// filmBuilderWork is what each side's construction units raise, in order.
var filmBuilderWork = [2][]string{
	{"armsolar", "armllt", "armrad", "armmstor", "armestor"},
	{"corsolar", "corllt", "corrad", "cormstor", "corestor"},
}

var filmBuilderUnits = [2][]string{{"armcv", "armck"}, {"corcv", "corck"}}

// filmWaterCentre finds the widest open water for a naval fixture: the
// authored-anchor alternative for maps nobody has measured by hand.
func filmWaterCentre(t *world.Terrain) (int32, int32, error) {
	return filmCoverageCentre(t, true)
}

// filmCoverageCentre picks the window holding the most water, or the most dry
// ground. Broken maps — lava fields, archipelagos — have no clean rectangle
// for the strict search, and the placement filter drops whatever falls off.
func filmCoverageCentre(t *world.Terrain, water bool) (int32, int32, error) {
	const halfX, halfZ = int32(760), int32(420)
	bestX, bestZ, best := int32(0), int32(0), -1
	for z := halfZ + 32; z <= t.PlayBottom-halfZ-32; z += 64 {
		for x := halfX + 32; x <= t.PlayRight-halfX-32; x += 64 {
			wet := 0
			for dz := -halfZ; dz <= halfZ; dz += 40 {
				for dx := -halfX; dx <= halfX; dx += 40 {
					h := int32(t.HeightAt(numeric.Fixed(x+dx)<<16, numeric.Fixed(z+dz)<<16).Int())
					if water && h+12 < int32(t.SeaLevel) || !water && h > int32(t.SeaLevel) {
						wet++
					}
				}
			}
			if wet > best {
				bestX, bestZ, best = x, z, wet
			}
		}
	}
	if best <= 0 {
		return 0, 0, fmt.Errorf("nanolathe: film: the map has no area fit for this scene")
	}
	return bestX, bestZ, nil
}

// filmPlaceable reports whether the unit's own movement class accepts the
// spot, so a formation drawn over a shoreline loses the units that would have
// stood in the sea rather than staging them there.
func filmPlaceable(s *session.Session, def *content.UnitDef, x, z numeric.Fixed) bool {
	fx, fz := world.FootprintForUnit(s.Catalog, def)
	extent, err := world.NewFootprintExtent(fx, fz)
	if err != nil {
		return false
	}
	cx, cz := world.PlacementAnchor(x, z, fx, fz)
	rect, err := world.NewFootprintRect(world.NewFootprintAnchor(cx, cz), extent)
	if err != nil {
		return false
	}
	rules, err := world.PlacementRulesForUnit(s.Catalog, def)
	if err != nil {
		return false
	}
	_, err = s.World.CheckPlacement(world.PlacementQuery{Rect: rect, Rules: rules, Mobile: true, SkipTerrainAggregates: def.BMCode == 0})
	return err == nil
}

// filmOrder queues a move (code 2) or a patrol (code 9) under the descriptor
// the unit's own class uses. The player's resolver is not usable here: before
// the first tick a staged unit has no live mover, and the resolver records a
// factory-style rally marker instead of a move [04 R-ORD-02 §1].
func filmOrder(s *session.Session, u *units.Unit, code int, x, z numeric.Fixed) {
	q := orders.QueueForUnit(u)
	if q == nil {
		return
	}
	name := "Move_Ground"
	switch fly := u.Def != nil && u.Def.CanFly; {
	case fly && code == 9:
		name = "VTOL_Patrol"
	case fly:
		name = "VTOL_Move"
	case code == 9:
		name = "Patrol"
	}
	id := orders.Lookup(name)
	q.Push(id, orders.NewNodeForOrder(id, 0, x, s.World.HeightAt(x, z), z, s.Clock.GlobalTick, u.Handle, false))
}

// stageFilmScene populates the session and returns the scene anchor in world
// pixels — the point a shot's scene-relative camera keys are measured from.
func stageFilmScene(scene film.Scene, s *session.Session) (cx, cz int32, err error) {
	if scene.Kind == "skirmish" {
		return filmSkirmishAnchor(s)
	}
	roster, err := filmRoster(scene.Roster)
	if err != nil {
		return 0, 0, err
	}
	var centreX, centreZ, relief int32
	if scene.Roster == "naval" && scene.Anchor == nil {
		centreX, centreZ, err = filmWaterCentre(s.World)
	} else {
		centreX, centreZ, relief, err = filmBattleCentre(scene, s.World)
	}
	if err != nil {
		return 0, 0, err
	}
	fmt.Fprintf(os.Stderr, "nanolathe: film: scene centre=%d,%d terrain_relief=%d per_side=%d\n", centreX, centreZ, relief, scene.PerSide)

	// A scene built in code skips the parser's defaults.
	columns, pitch, standIn := max(scene.Columns, 1), int32(scene.Pitch), int32(scene.Gap)
	if scene.Columns <= 0 {
		columns = 8
	}
	if pitch <= 0 {
		pitch = 48
	}
	if standIn <= 0 {
		standIn = 320
	}
	buildingPitch := int32(96)
	for side := range filmSideBuildings {
		for _, name := range filmSideBuildings[side] {
			if def, ok := s.Catalog.Unit(name); ok {
				buildingPitch = max(buildingPitch, int32(def.FootprintX)*16+32)
			}
		}
	}
	placed, skipped := 0, 0
	// create stages one unit and reports nil for a spot its class refuses.
	create := func(name string, side int, x, z int32, airborne bool) *units.Unit {
		def, ok := s.Catalog.Unit(name)
		if !ok || x < 32 || x >= s.World.PlayRight-32 || z < 32 || z >= s.World.PlayBottom-32 {
			skipped++
			return nil
		}
		fx, fz := numeric.Fixed(int64(x)<<16), numeric.Fixed(int64(z)<<16)
		if def.BMCode == 0 {
			ax, az := world.PlacementAnchor(fx, fz, int32(def.FootprintX), int32(def.FootprintZ))
			fx, fz = world.PlacementCenter(ax, az, int32(def.FootprintX), int32(def.FootprintZ))
		}
		var fy numeric.Fixed
		var h pool.Handle
		var e error
		if airborne && def.CanFly {
			// In flight from the first frame, through the existing airborne
			// creator and cruise-altitude seam [04 §10.1].
			fy = movement.CruiseAltitudeForOffset(s.World, fx, fz, def.CruiseAlt)
			h, e = s.Units.CreateWithMoverMode(def, uint8(side), fx, fy, fz, 2)
		} else {
			if !filmPlaceable(s, def, fx, fz) {
				skipped++
				return nil
			}
			fy = s.World.HeightAt(fx, fz)
			if def.BMCode != 0 && fy < s.World.SeaLevelWorld() && def.Floater {
				fy = s.World.SeaLevelWorld()
			}
			h, e = s.Units.Create(def, uint8(side), fx, fy, fz)
		}
		if e != nil {
			skipped++
			return nil
		}
		u := s.Units.Unit(h)
		if s.Movement != nil {
			s.Movement.EnsureUnit(u)
		}
		s.BindStagedOrderQueue(u)
		placed++
		return u
	}
	fixed := func(v int32) numeric.Fixed { return numeric.Fixed(int64(v) << 16) }

	for side := 0; side < 2; side++ {
		facing := int32(side*2 - 1) // -1 draws up from the left, +1 from the right
		rows := int32((scene.PerSide + columns - 1) / columns)
		for i := 0; i < scene.PerSide; i++ {
			name := roster[side][i%len(roster[side])]
			// Ranks are staggered and the line bows toward the enemy, so two
			// scenes with different column counts do not read as one grid.
			col, row := int32(i%columns), int32(i/columns)
			x := centreX + facing*standIn + facing*col*pitch + (row%2)*pitch/2*facing
			z := centreZ + row*pitch - rows/2*pitch + (col%2)*pitch/3
			u := create(name, side, x, z, true)
			if u == nil {
				continue
			}
			goalX := fixed(centreX - facing*standIn/2)
			switch {
			case u.Def != nil && u.Def.CanFly:
				// Aircraft never get a final goal: a patrol leg over the enemy
				// line and back keeps them in the air for the whole scene.
				filmOrder(s, u, 9, fixed(centreX-facing*(standIn+300)), fixed(z))
			case scene.Patrol:
				filmOrder(s, u, 9, goalX, fixed(z))
			default:
				filmOrder(s, u, 2, goalX, fixed(z))
			}
		}
		for j := 0; j < scene.Buildings; j++ {
			name := filmSideBuildings[side][j%len(filmSideBuildings[side])]
			x := centreX + facing*(standIn+int32(columns)*pitch+120) + facing*int32(j%3)*buildingPitch
			z := centreZ + int32(j/3)*buildingPitch - int32(scene.Buildings/6)*buildingPitch
			u := create(name, side, x, z, false)
			if u == nil || !scene.Factories || (name != "armlab" && name != "corlab") {
				continue
			}
			product := "armpw"
			if side == 1 {
				product = "corak"
			}
			if err := construction.QueueFactoryBuild(u, product, 12, s.Catalog); err != nil {
				return 0, 0, fmt.Errorf("nanolathe: film: factory queue: %w", err)
			}
		}
		for j := 0; j < scene.Air; j++ {
			name := filmAircraft[side][j%len(filmAircraft[side])]
			// Scattered in depth and width behind the line, each with its own
			// turning point, so waves cross the frame through the whole shot
			// rather than arriving as one rank.
			k := int32(j)
			x := centreX + facing*(standIn+150+(k*137)%650)
			z := centreZ + (k*211)%760 - 380
			if u := create(name, side, x, z, true); u != nil {
				filmOrder(s, u, 9, fixed(centreX-facing*(standIn+250+(k*53)%400)), fixed(z+(k*97)%240-120))
			}
		}
		for j := 0; j < scene.Builders; j++ {
			name := filmBuilderUnits[side][j%len(filmBuilderUnits[side])]
			x := centreX + facing*(standIn+int32(columns)*pitch+60)
			z := centreZ + (int32(j)-int32(scene.Builders)/2)*130
			u := create(name, side, x, z, false)
			if u == nil {
				continue
			}
			for k := 0; k < 3; k++ {
				product := filmBuilderWork[side][(j+k)%len(filmBuilderWork[side])]
				def, ok := s.Catalog.Unit(product)
				if !ok {
					continue
				}
				bx, bz := fixed(x-facing*(70+int32(k)*100)), fixed(z+int32(k%2)*40)
				ax, az := world.PlacementAnchor(bx, bz, int32(def.FootprintX), int32(def.FootprintZ))
				bx, bz = world.PlacementCenter(ax, az, int32(def.FootprintX), int32(def.FootprintZ))
				if err := construction.QueueMobileBuild(u, product, bx, bz, 1, s.Catalog); err != nil {
					fmt.Fprintf(os.Stderr, "nanolathe: film: builder %s cannot queue %s: %v\n", name, product, err)
				}
			}
		}
	}
	fmt.Fprintf(os.Stderr, "nanolathe: film: staged %d units, skipped %d unplaceable\n", placed, skipped)
	return centreX, centreZ, nil
}

// filmOpeningMex has the local commander build an extractor on the nearest
// permanent deposit, through the player's own command path. It reports the
// site so the shot can be framed around both.
func filmOpeningMex(s *session.Session) {
	var commander *units.Unit
	for _, u := range s.Units.Iter() {
		if u != nil && u.Alive && u.Owner == s.LocalOwner && u.Def != nil && u.Def.Commander {
			commander = u
			break
		}
	}
	if commander == nil {
		return
	}
	product := "armmex"
	if _, ok := s.Catalog.Unit(product); !ok || commander.Def.Side != "ARM" {
		if strings.EqualFold(commander.Def.Side, "CORE") {
			product = "cormex"
		}
	}
	t := s.World
	var bestX, bestZ numeric.Fixed
	best := int64(-1)
	for z := int32(0); z < t.CellH; z++ {
		for x := int32(0); x < t.CellW; x++ {
			cell := t.PlotAt(x, z)
			if cell == nil || cell.Feature() >= 0xfffb {
				continue
			}
			def, ok := t.FeatureDefAt(cell.Feature())
			if !ok || def.Metal == 0 || !def.Indestructible {
				continue
			}
			wx, wz := world.PlacementCenter(x, z, int32(def.FootprintX), int32(def.FootprintZ))
			dx, dz := int64(wx-commander.X)>>16, int64(wz-commander.Z)>>16
			if d := dx*dx + dz*dz; best < 0 || d < best {
				bestX, bestZ, best = wx, wz, d
			}
		}
	}
	if best < 0 {
		fmt.Fprintln(os.Stderr, "nanolathe: film: opening found no metal deposit; the commander will only land")
		return
	}
	fmt.Fprintf(os.Stderr, "nanolathe: film: opening commander=%d,%d extractor=%d,%d (%s)\n",
		commander.X.Int(), commander.Z.Int(), bestX.Int(), bestZ.Int(), product)
	_ = s.EnqueueHumanCommand(session.HumanCommand{Kind: session.HumanMobileBuild, MobileBuild: session.HumanMobileBuildCommand{
		Builder: commander.Handle, Product: product, WX: bestX, WY: t.HeightAt(bestX, bestZ), WZ: bestZ,
	}})
}

// filmSkirmishAnchor frames the viewing player's own start, which is the one
// point on an arbitrary map that is certainly land, clear and in line of sight.
func filmSkirmishAnchor(s *session.Session) (int32, int32, error) {
	for _, u := range s.Units.Iter() {
		if u == nil || !u.Alive || u.Owner != s.LocalOwner {
			continue
		}
		return int32(u.X.Int()), int32(u.Z.Int()), nil
	}
	return 0, 0, fmt.Errorf("nanolathe: film: the skirmish scene has no local unit to frame")
}

// revealFilmScene lifts the viewing player's fog for the capture. A film shows
// the battle, not one side's knowledge of it; this is the `+nowisee` developer
// command's own path and changes no gameplay rule.
func revealFilmScene(s *session.Session) {
	if s == nil {
		return
	}
	_ = s.EnqueueHumanCommand(session.HumanCommand{
		Kind:       session.HumanVisibility,
		Visibility: session.HumanVisibilityCommand{ClearMask: visibility.ModeHistoryEnabled | visibility.ModeCurrentEnabled},
	})
}
