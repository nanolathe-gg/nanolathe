package main

import (
	"fmt"
	"os"

	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/film"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
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

// filmSideBuildings is the rear line: something to build, power and defend.
var filmSideBuildings = [2][]string{
	{"armsolar", "armlab", "armllt", "armrad"},
	{"corsolar", "corlab", "corllt", "corrad"},
}

// stageFilmScene populates the session and returns the scene anchor in world
// pixels — the point a shot's scene-relative camera keys are measured from.
func stageFilmScene(scene film.Scene, s *session.Session) (cx, cz int32, err error) {
	if scene.Kind == "skirmish" {
		return filmSkirmishAnchor(s)
	}
	centreX, centreZ, relief, err := benchmarkBattleCentre(s.World)
	if err != nil {
		return 0, 0, err
	}
	fmt.Fprintf(os.Stderr, "nanolathe: film: scene centre=%d,%d terrain_relief=%d per_side=%d\n", centreX, centreZ, relief, scene.PerSide)

	const (
		columns = 8
		pitch   = 48
		standIn = 320 // half the gap between the two formations
	)
	buildingPitch := int32(96)
	for side := range filmSideBuildings {
		for _, name := range filmSideBuildings[side] {
			if def, ok := s.Catalog.Unit(name); ok {
				buildingPitch = max(buildingPitch, int32(def.FootprintX)*16+32)
			}
		}
	}
	for side := 0; side < 2; side++ {
		facing := int32(side*2 - 1) // -1 draws up from the left, +1 from the right
		rows := (scene.PerSide + columns - 1) / columns
		for i := 0; i < scene.PerSide+scene.Buildings; i++ {
			building := i >= scene.PerSide
			name := filmSideUnits[side][i%len(filmSideUnits[side])]
			if building {
				name = filmSideBuildings[side][(i-scene.PerSide)%len(filmSideBuildings[side])]
			}
			def, ok := s.Catalog.Unit(name)
			if !ok {
				return 0, 0, fmt.Errorf("nanolathe: film: unit %q is not in the catalog: logical path %s, providers searched [catalog], expected an authored unit definition", name, name)
			}
			x := centreX + facing*standIn + int32(i%columns)*pitch - int32(columns/2)*pitch
			z := centreZ + int32(i/columns)*pitch - int32(rows/2)*pitch
			if building {
				j := int32(i - scene.PerSide)
				x = centreX + facing*(standIn+340) + (j%2)*buildingPitch
				z = centreZ + (j/2)*buildingPitch - int32(scene.Buildings/4)*buildingPitch
			}
			fx, fz := numeric.Fixed(int64(x)<<16), numeric.Fixed(int64(z)<<16)
			fy := s.World.HeightAt(fx, fz)
			h, e := s.Units.Create(def, uint8(side), fx, fy, fz)
			if e != nil {
				return 0, 0, fmt.Errorf("nanolathe: film: create %s: %w", name, e)
			}
			u := s.Units.Unit(h)
			if s.Movement != nil {
				s.Movement.EnsureUnit(u)
			}
			if building && scene.Factories && (name == "armlab" || name == "corlab") {
				product := "armpw"
				if side == 1 {
					product = "corak"
				}
				if err := construction.QueueFactoryBuild(u, product, 12, s.Catalog); err != nil {
					return 0, 0, fmt.Errorf("nanolathe: film: factory queue: %w", err)
				}
			}
			if building {
				continue
			}
			// Order every mobile unit across the gap. Combat, pathfinding and
			// the COB scripts then run ordinary production code.
			q, ok := u.Orders.(*orders.Queue)
			if !ok {
				continue
			}
			id := orders.Lookup("Move_Ground")
			goalX := numeric.Fixed(int64(centreX-facing*standIn/2) << 16)
			goalY := s.World.HeightAt(goalX, fz)
			q.Push(id, orders.NewNodeForOrder(id, 0, goalX, goalY, fz, s.Clock.GlobalTick, h, false))
		}
	}
	return centreX, centreZ, nil
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
