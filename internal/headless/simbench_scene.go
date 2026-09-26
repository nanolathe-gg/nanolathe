package headless

import (
	"fmt"
	"math"

	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/movement"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// SimBenchSceneVersion identifies the fixture below. Two runs are comparable
// only at the same version: changing the roster, the geometry or the scripted
// orders changes the workload and the RNG consumption, so an older capture is
// not this scene's baseline.
const SimBenchSceneVersion = 1

// The scene is a fixture, exactly like the windowed battle benchmark's: it
// places its armies directly through the ordinary creation, movement and
// production entry points rather than playing a normal skirmish opening. No
// simulation behavior lives here.
const (
	// simBenchTeams is the number of computer armies. Retail's skirmish lobby
	// refuses a setup with no human slot ("There must be at least one player
	// and one computer opponent", [08 "Skirmish configuration"]), so the
	// configuration carries a fourth, human, slot that owns only the commander
	// battle entry stamps for it and issues no command for the whole run. That
	// placeholder is the local/viewing owner; the three measured armies are
	// slots 1..3.
	simBenchTeams        = 3
	simBenchHumanSlot    = 0
	simBenchFirstAISlot  = 1
	simBenchUnitsPerTeam = SimBenchDefaultArmySize
	// Per-unit pitch in world pixels. The largest mobile footprint in the
	// roster is 3 cells (48 px) and the largest building footprint is 8 cells
	// (128 px), so both pitches leave at least one free cell around every
	// placement.
	simBenchMobilePitch   = 56
	simBenchBuildingPitch = 144
	simBenchMobileCols    = 15
	simBenchBuildingCols  = 7
	// Distances from the battle centre to each army's block centre.
	simBenchMobileRadius   = 1050
	simBenchBuildingRadius = 2300
	// Factory production depth and the number of buildings each scripted
	// mobile builder is asked for.
	simBenchFactoryQueue = 20
	simBenchBuilderQueue = 3
)

// simBenchRosterEntry is one row of the per-team composition table. Arm and
// Core name the same role on each side; Count is the number placed per team.
type simBenchRosterEntry struct {
	Role  string `json:"role"`
	Arm   string `json:"arm"`
	Core  string `json:"core"`
	Count int    `json:"count"`
}

// simBenchBuildings is the fixed per-team building composition: energy,
// metal, production, defence, sensors and storage. Fifty rows per team.
var simBenchBuildings = []simBenchRosterEntry{
	{"solar", "armsolar", "corsolar", 14},
	{"wind", "armwin", "corwin", 6},
	{"metal-maker", "armmakr", "cormakr", 6},
	{"kbot-lab", "armlab", "corlab", 2},
	{"vehicle-plant", "armvp", "corvp", 1},
	{"aircraft-plant", "armap", "corap", 1},
	{"defence-tower", "armllt", "corllt", 12},
	{"radar", "armrad", "corrad", 4},
	{"metal-storage", "armmstor", "cormstor", 2},
	{"energy-storage", "armestor", "corestor", 2},
}

// simBenchMobiles is the fixed per-team mobile composition: two hundred rows
// of tanks, kbots, artillery, aircraft and construction units.
var simBenchMobiles = []simBenchRosterEntry{
	{"medium-tank", "armstump", "corraid", 28},
	{"assault-tank", "armflash", "corlevlr", 28},
	{"scout-tank", "armfav", "corfav", 24},
	{"light-kbot", "armpw", "corak", 28},
	{"rocket-kbot", "armrock", "corstorm", 24},
	{"artillery-kbot", "armham", "corthud", 24},
	{"fighter", "armfig", "corveng", 14},
	{"bomber", "armthund", "corshad", 12},
	{"scout-air", "armpeep", "corfink", 6},
	{"construction-kbot", "armck", "corck", 6},
	{"construction-vehicle", "armcv", "corcv", 6},
}

// simBenchFactoryProduct names what each factory role keeps producing. The
// products are ordinary buildable rows of that factory's menu, so production
// runs through the normal construction path.
var simBenchFactoryProduct = map[string][2]string{
	"kbot-lab":       {"armpw", "corak"},
	"vehicle-plant":  {"armstump", "corraid"},
	"aircraft-plant": {"armfig", "corveng"},
}

// simBenchBuilderProduct is what each scripted mobile builder is told to
// build. A solar collector is cheap enough that the order completes inside the
// measured window and the builder takes the next one.
var simBenchBuilderProduct = [2]string{"armsolar", "corsolar"}

// simBenchTeamSides is the side ordinal each measured slot ends up with.
// SkirmishConfig.ApplyDefaults treats a zero Side as absent and installs
// `slot & 1`, so slot 1 and slot 3 are Core and slot 2 is Arm; a slot cannot
// ask for side 0 explicitly. Both sides are therefore exercised without
// fighting the setup record's missing-value rule.
var simBenchTeamSides = [simBenchTeams]int{1, 0, 1}

// simBenchTeamPlacement is where one army was laid out, recorded in the report
// so a run describes its own geometry.
type simBenchTeamPlacement struct {
	Player          int    `json:"player"`
	Side            string `json:"side"`
	AllyGroup       int    `json:"ally_group"`
	MobileCentreX   int32  `json:"mobile_centre_x"`
	MobileCentreZ   int32  `json:"mobile_centre_z"`
	BuildingCentreX int32  `json:"building_centre_x"`
	BuildingCentreZ int32  `json:"building_centre_z"`
	Buildings       int    `json:"buildings"`
	Mobiles         int    `json:"mobiles"`
	Factories       int    `json:"factories"`
	ScriptedMoves   int    `json:"scripted_moves"`
	ScriptedBuilds  int    `json:"scripted_builds"`
}

// simBenchPassiveHumanPlacement records the enlarged fixture's remote human
// commander. The default scene keeps its original map start position.
type simBenchPassiveHumanPlacement struct {
	X int32 `json:"x"`
	Z int32 `json:"z"`
}

// SimBenchScene is the placed fixture: what was created, and where.
type SimBenchScene struct {
	Version      int                            `json:"scene_version"`
	Map          string                         `json:"map"`
	TerrainW     int32                          `json:"terrain_width"`
	TerrainH     int32                          `json:"terrain_height"`
	CentreX      int32                          `json:"battle_centre_x"`
	CentreZ      int32                          `json:"battle_centre_z"`
	Flatness     float64                        `json:"battle_centre_flatness"`
	UnitLimit    int                            `json:"unit_limit"`
	ArmySize     int                            `json:"army_size"`
	MobileRoster []simBenchRosterEntry          `json:"mobile_roster"`
	Teams        []simBenchTeamPlacement        `json:"teams"`
	PassiveHuman *simBenchPassiveHumanPlacement `json:"passive_human,omitempty"`

	factories []*units.Unit
}

// simBenchConfig builds the setup record for the scene. Difficulty and the
// seeds are the caller's; everything else is the fixture's.
func simBenchConfig(mapName string, unitLimit int) session.SkirmishConfig {
	cfg := session.SkirmishConfig{MapName: mapName, NumPlayers: simBenchTeams + 1}
	for i := range cfg.Players {
		cfg.Players[i] = session.SkirmishPlayer{}
	}
	// Slot 0 is the human the lobby rule requires; it never issues a command.
	cfg.Players[simBenchHumanSlot].Controller = session.SkirmishControllerHuman
	cfg.Players[simBenchHumanSlot].AllyGroup = 4
	cfg.Players[simBenchHumanSlot].Color = 0
	cfg.Players[simBenchHumanSlot].Metal = 1000
	cfg.Players[simBenchHumanSlot].Energy = 1000
	for t := 0; t < simBenchTeams; t++ {
		slot := simBenchFirstAISlot + t
		cfg.Players[slot].Controller = session.SkirmishControllerComputer
		// One ally group each: the three armies are mutually hostile, and none
		// of them is allied with the human placeholder.
		cfg.Players[slot].AllyGroup = t
		cfg.Players[slot].Color = slot
		cfg.Players[slot].Side = simBenchTeamSides[t]
		// Enough stock that queued production and the AI's own construction
		// tasks proceed for the whole measured window instead of stalling on
		// an empty ledger.
		cfg.Players[slot].Metal = 32000
		cfg.Players[slot].Energy = 32000
	}
	cfg.UnitLimit = unitLimit
	// Identity start-position assignment: the fixture places its own armies,
	// and a shuffled assignment would move the commanders between runs of the
	// same seed pair for no benefit [P0-04].
	cfg.Location = 1
	// Losing a computer commander must not eliminate its remaining army.
	// The human still loses when its sole unit dies [08 R-SKIR-01 §3].
	cfg.ApplyDefaults()
	cfg.CommanderDeath = int(session.CommanderDeathContinues)
	return cfg
}

// buildSimBenchScene places the three armies and scripts their opening orders.
// It runs before the first tick, so nothing here is on a measured path.
func buildSimBenchScene(sess *session.Session, mapName string, unitLimit, armySize int) (*SimBenchScene, error) {
	if sess == nil || sess.World == nil || sess.Units == nil || sess.Catalog == nil {
		return nil, fmt.Errorf("nanolathe: sim benchmark scene: incomplete session: logical path %s, providers searched [none], expected a composed authoritative session", mapName)
	}
	if armySize+1 > unitLimit {
		return nil, fmt.Errorf("nanolathe: simulation benchmark army exceeds unit limit: logical path %s, providers searched [resolved gameplay rules], expected army size %d plus its commander within the per-player limit %d", mapName, armySize, unitLimit)
	}
	terrain := sess.World
	centreX, centreZ, flatness := simBenchBattleCentre(terrain)
	scene := &SimBenchScene{
		Version: SimBenchSceneVersion, Map: mapName,
		TerrainW: terrain.CellW * 16, TerrainH: terrain.CellH * 16,
		CentreX: centreX, CentreZ: centreZ, Flatness: flatness,
		UnitLimit: unitLimit, ArmySize: armySize,
		MobileRoster: simBenchMobileRoster(armySize),
	}
	for t := 0; t < simBenchTeams; t++ {
		place, err := placeSimBenchTeam(sess, scene, t, centreX, centreZ)
		if err != nil {
			return nil, err
		}
		scene.Teams = append(scene.Teams, place)
	}
	if armySize != SimBenchDefaultArmySize {
		if err := relocateSimBenchPassiveHuman(sess, scene); err != nil {
			return nil, err
		}
	}
	return scene, nil
}

// relocateSimBenchPassiveHuman keeps the enlarged fixture's idle human away
// from the battle. Losing that slot's only unit can end the measured window
// even while all three computer armies remain active. This is authored setup
// geometry, not protection from ordinary damage or result evaluation.
func relocateSimBenchPassiveHuman(sess *session.Session, scene *SimBenchScene) error {
	human := sess.Units.FirstLive(func(u *units.Unit) bool {
		return u.Owner == simBenchHumanSlot && u.Def != nil && u.Def.Commander
	})
	if human == nil || sess.Movement == nil || sess.Movement.Grid == nil || int(human.Handle) >= len(sess.Movement.Collisions) {
		return fmt.Errorf("nanolathe: simulation benchmark passive human placement failed: logical path %s, providers searched [session], expected a live human commander with movement occupancy", scene.Map)
	}
	coll := sess.Movement.Collisions[human.Handle]
	if coll == nil {
		return fmt.Errorf("nanolathe: simulation benchmark passive human placement failed: logical path %s, providers searched [movement], expected the human commander's committed footprint", scene.Map)
	}
	// The normal host binds this world on its first tick; setup needs that
	// same binding before the direct placement entry point can find the unit.
	sess.Movement.BindWorld(sess.Units)
	profile := sess.Movement.ProfileFor(human.Handle)
	bx, bz := coll.HalfBias()
	live := sess.Units.Iter()
	// A coarse interior lattice gives deterministic ties in Z/X order and
	// leaves a margin around every footprint. Maximise distance to the nearest
	// opponent, including the original commanders outside the placed blocks.
	const margin, stride int32 = 256, 128
	var bestX, bestZ int32
	bestDistance := int64(-1)
	for z := margin; z <= scene.TerrainH-margin; z += stride {
		for x := margin; x <= scene.TerrainW-margin; x += stride {
			fx, fz := numeric.FixedFromInt(int64(x)), numeric.FixedFromInt(int64(z))
			if sess.World.HeightAt(fx, fz) <= numeric.FixedFromInt(int64(sess.World.SeaLevel)) {
				continue
			}
			anchor := movement.QuantizedAnchor(int32(fx.Raw()), int32(fz.Raw()), bx, bz)
			if !sess.Movement.Grid.RectOnMap(anchor, coll.FootPrintX, coll.FootPrintZ) ||
				sess.Movement.Grid.FootprintOccupied(anchor, coll.FootPrintX, coll.FootPrintZ, int(human.Handle)) {
				continue
			}
			legal := true
			for dz := int32(0); dz < int32(coll.FootPrintZ) && legal; dz++ {
				for dx := int32(0); dx < int32(coll.FootPrintX); dx++ {
					if !profile.IsPassableCommitCell(sess.World, anchor.X+dx, anchor.Z+dz) {
						legal = false
						break
					}
				}
			}
			if !legal {
				continue
			}
			nearest := int64(1 << 62)
			for _, other := range live {
				if other.Owner == simBenchHumanSlot {
					continue
				}
				dx, dz := int64(other.X>>16)-int64(x), int64(other.Z>>16)-int64(z)
				if distance := dx*dx + dz*dz; distance < nearest {
					nearest = distance
				}
			}
			if nearest > bestDistance {
				bestX, bestZ, bestDistance = x, z, nearest
			}
		}
	}
	if bestDistance < simBenchBuildingRadius*simBenchBuildingRadius {
		return fmt.Errorf("nanolathe: simulation benchmark passive human placement failed: logical path %s, providers searched [terrain and unit occupancy], expected a free land footprint at least %d world units from every opponent", scene.Map, simBenchBuildingRadius)
	}
	x, z := numeric.FixedFromInt(int64(bestX)), numeric.FixedFromInt(int64(bestZ))
	if !sess.Movement.PlaceUnit(orders.PlaceRequest{Unit: human.Handle, X: x, Y: sess.World.HeightAt(x, z), Z: z}) {
		return fmt.Errorf("nanolathe: simulation benchmark passive human placement failed: logical path %s, providers searched [movement], expected a committed remote commander position", scene.Map)
	}
	scene.PassiveHuman = &simBenchPassiveHumanPlacement{X: bestX, Z: bestZ}
	return nil
}

// placeSimBenchTeam lays out one army: buildings in a rear block away from the
// battle centre, mobiles in a front block facing it.
func placeSimBenchTeam(sess *session.Session, scene *SimBenchScene, team int, centreX, centreZ int32) (simBenchTeamPlacement, error) {
	slot := simBenchFirstAISlot + team
	side := simBenchTeamSides[team]
	angle := math.Pi/2 + float64(team)*2*math.Pi/3
	dirX, dirZ := math.Cos(angle), math.Sin(angle)
	mobileX := centreX + int32(dirX*simBenchMobileRadius)
	mobileZ := centreZ + int32(dirZ*simBenchMobileRadius)
	buildingX := centreX + int32(dirX*simBenchBuildingRadius)
	buildingZ := centreZ + int32(dirZ*simBenchBuildingRadius)
	out := simBenchTeamPlacement{
		Player: slot, Side: sideName(side), AllyGroup: team,
		MobileCentreX: mobileX, MobileCentreZ: mobileZ,
		BuildingCentreX: buildingX, BuildingCentreZ: buildingZ,
	}

	index := 0
	buildingTotal := rosterTotal(simBenchBuildings)
	for _, row := range simBenchBuildings {
		name := rosterName(row, side)
		def, ok := sess.Catalog.Unit(name)
		if !ok || def == nil {
			return out, fmt.Errorf("nanolathe: sim benchmark scene: unit definition is missing: logical path %s, providers searched [catalog], expected a compiled %s row", name, row.Role)
		}
		for n := 0; n < row.Count; n++ {
			x, z := blockSlot(buildingX, buildingZ, index, simBenchBuildingCols, simBenchBuildingPitch, buildingTotal)
			unit, err := createSimBenchUnit(sess, def, uint8(slot), x, z)
			if err != nil {
				return out, err
			}
			out.Buildings++
			index++
			product, isFactory := simBenchFactoryProduct[row.Role]
			if !isFactory {
				continue
			}
			if err := construction.QueueFactoryBuild(unit, product[side], simBenchFactoryQueue, sess.Catalog); err != nil {
				return out, fmt.Errorf("nanolathe: sim benchmark scene: factory queue failed: logical path %s, providers searched [catalog], expected a buildable %s product: %w", product[side], row.Role, err)
			}
			scene.factories = append(scene.factories, unit)
			out.Factories++
		}
	}

	index = 0
	total := scene.ArmySize - out.Buildings
	for _, row := range scene.MobileRoster {
		name := rosterName(row, side)
		def, ok := sess.Catalog.Unit(name)
		if !ok || def == nil {
			return out, fmt.Errorf("nanolathe: sim benchmark scene: unit definition is missing: logical path %s, providers searched [catalog], expected a compiled %s row", name, row.Role)
		}
		for n := 0; n < row.Count; n++ {
			x, z := blockSlot(mobileX, mobileZ, index, simBenchMobileCols, simBenchMobilePitch, total)
			unit, err := createSimBenchUnit(sess, def, uint8(slot), x, z)
			if err != nil {
				return out, err
			}
			index++
			if def.Builder {
				// A scripted mobile builder keeps a short queue of ordinary
				// site-anchored build orders behind its own block, so the
				// construction path runs for the whole window without the
				// builder walking into the battle first.
				for q := 0; q < simBenchBuilderQueue; q++ {
					siteX := buildingX + int32((n%3)*simBenchBuildingPitch) - simBenchBuildingPitch
					siteZ := buildingZ + int32((q+1)*simBenchBuildingPitch) + simBenchBuildingPitch*4
					pushSimBenchBuild(sess, unit, simBenchBuilderProduct[side], siteX, siteZ)
					out.ScriptedBuilds++
				}
				continue
			}
			// Everything else marches on the battle centre. The AI's own wave
			// task re-targets these units once it has classified and merged
			// them; the scripted move guarantees the three armies meet inside
			// the measured window even before that happens.
			goalX := centreX + int32(float64(-dirX)*float64(simBenchMobileRadius/4))
			goalZ := centreZ + int32(float64(-dirZ)*float64(simBenchMobileRadius/4))
			jitterX := int32((index%7)*32) - 96
			jitterZ := int32((index%5)*32) - 64
			pushSimBenchMove(sess, unit, goalX+jitterX, goalZ+jitterZ)
			out.ScriptedMoves++
		}
	}
	out.Mobiles = index
	return out, nil
}

// simBenchMobileRoster preserves the default role proportions using cumulative
// integer quotas. The difference of adjacent quotas assigns every mobile once,
// including remainders, and the default size reproduces the original table.
func simBenchMobileRoster(armySize int) []simBenchRosterEntry {
	rows := append([]simBenchRosterEntry(nil), simBenchMobiles...)
	mobileTotal := armySize - rosterTotal(simBenchBuildings)
	baseTotal := rosterTotal(simBenchMobiles)
	cumulative, assigned := 0, 0
	for i := range rows {
		cumulative += simBenchMobiles[i].Count
		quota := cumulative * mobileTotal / baseTotal
		rows[i].Count = quota - assigned
		assigned = quota
	}
	return rows
}

// rosterTotal sums a composition table's rows.
func rosterTotal(rows []simBenchRosterEntry) int {
	total := 0
	for _, row := range rows {
		total += row.Count
	}
	return total
}

// blockSlot maps a placement index onto a centred grid of `cols` columns.
func blockSlot(centreX, centreZ int32, index, cols, pitch, total int) (int32, int32) {
	rows := (total + cols - 1) / cols
	col := index % cols
	row := index / cols
	x := centreX + int32(col*pitch) - int32((cols-1)*pitch)/2
	z := centreZ + int32(row*pitch) - int32((rows-1)*pitch)/2
	return x, z
}

// createSimBenchUnit is the ordinary creation entry point plus the mover
// registration the windowed benchmark's fixture also performs.
func createSimBenchUnit(sess *session.Session, def *content.UnitDef, owner uint8, x, z int32) (*units.Unit, error) {
	fx := numeric.Fixed(int64(x) << 16)
	fz := numeric.Fixed(int64(z) << 16)
	fy := sess.World.HeightAt(fx, fz)
	handle, err := sess.Units.Create(def, owner, fx, fy, fz)
	if err != nil {
		return nil, fmt.Errorf("nanolathe: sim benchmark scene: unit creation failed: logical path %s, providers searched [unit pool], expected a free slot for player %d: %w", def.UnitName, owner, err)
	}
	unit := sess.Units.Unit(handle)
	if unit == nil {
		return nil, fmt.Errorf("nanolathe: sim benchmark scene: unit creation failed: logical path %s, providers searched [unit pool], expected a live record for player %d", def.UnitName, owner)
	}
	if sess.Movement != nil {
		sess.Movement.EnsureUnit(unit)
	}
	return unit, nil
}

func pushSimBenchMove(sess *session.Session, unit *units.Unit, x, z int32) {
	queue, ok := unit.Orders.(*orders.Queue)
	if !ok || queue == nil {
		return
	}
	id := orders.Lookup("Move_Ground")
	fx := numeric.Fixed(int64(x) << 16)
	fz := numeric.Fixed(int64(z) << 16)
	fy := sess.World.HeightAt(fx, fz)
	queue.Push(id, orders.NewNodeForOrder(id, 0, fx, fy, fz, sess.Clock.GlobalTick, unit.Handle, false))
}

func pushSimBenchBuild(sess *session.Session, unit *units.Unit, product string, x, z int32) {
	queue, ok := unit.Orders.(*orders.Queue)
	if !ok || queue == nil {
		return
	}
	fx := numeric.Fixed(int64(x) << 16)
	fz := numeric.Fixed(int64(z) << 16)
	node := orders.NewMobileBuildNode(sess.Catalog, product, fx, fz, 0, 1, sess.Clock.GlobalTick, unit.Handle, true)
	queue.Push(node.ID, node)
}

func rosterName(row simBenchRosterEntry, side int) string {
	if side == 0 {
		return row.Arm
	}
	return row.Core
}

func sideName(side int) string {
	if side == 0 {
		return "ARM"
	}
	return "CORE"
}

// simBenchBattleCentre picks where the three armies meet: the point whose six
// blocks and three approach corridors are the flattest land on this map. It is
// a pure function of the terrain — no random draw, no map iteration — so the
// same map always yields the same centre, and a different map still produces a
// usable scene instead of dropping an army on a cliff.
func simBenchBattleCentre(terrain *world.Terrain) (int32, int32, float64) {
	width := terrain.CellW * 16
	height := terrain.CellH * 16
	margin := int32(simBenchBuildingRadius + 700)
	fallbackX, fallbackZ := width/2, height/2
	if width <= 2*margin || height <= 2*margin {
		return fallbackX, fallbackZ, 0
	}
	bestScore := -1.0
	bestX, bestZ := fallbackX, fallbackZ
	for cz := margin; cz <= height-margin; cz += 256 {
		for cx := margin; cx <= width-margin; cx += 256 {
			score := simBenchCentreScore(terrain, cx, cz)
			if score > bestScore {
				bestScore, bestX, bestZ = score, cx, cz
			}
		}
	}
	if bestScore < 0 {
		return fallbackX, fallbackZ, 0
	}
	return bestX, bestZ, bestScore
}

// simBenchCentreScore is the worst of the nine sampled regions around one
// candidate centre: three mobile blocks, three building blocks, three
// corridors. Taking the worst rather than the mean keeps a single cliff from
// being averaged away by five good blocks.
func simBenchCentreScore(terrain *world.Terrain, centreX, centreZ int32) float64 {
	worst := 1.0
	var mobileX, mobileZ [simBenchTeams]int32
	for t := 0; t < simBenchTeams; t++ {
		angle := math.Pi/2 + float64(t)*2*math.Pi/3
		dirX, dirZ := math.Cos(angle), math.Sin(angle)
		mobileX[t] = centreX + int32(dirX*simBenchMobileRadius)
		mobileZ[t] = centreZ + int32(dirZ*simBenchMobileRadius)
		buildingX := centreX + int32(dirX*simBenchBuildingRadius)
		buildingZ := centreZ + int32(dirZ*simBenchBuildingRadius)
		if v := simBenchBlockFlatness(terrain, mobileX[t], mobileZ[t], 420, 392); v < worst {
			worst = v
		}
		if v := simBenchBlockFlatness(terrain, buildingX, buildingZ, 504, 576); v < worst {
			worst = v
		}
	}
	for t := 0; t < simBenchTeams; t++ {
		if v := simBenchCorridorFlatness(terrain, mobileX[t], mobileZ[t], centreX, centreZ); v < worst {
			worst = v
		}
	}
	return worst
}

func simBenchBlockFlatness(terrain *world.Terrain, centreX, centreZ, halfX, halfZ int32) float64 {
	total, flat := 0, 0
	for z := centreZ - halfZ; z <= centreZ+halfZ; z += 96 {
		for x := centreX - halfX; x <= centreX+halfX; x += 96 {
			total++
			if simBenchFlatAt(terrain, x, z) {
				flat++
			}
		}
	}
	if total == 0 {
		return 0
	}
	return float64(flat) / float64(total)
}

func simBenchCorridorFlatness(terrain *world.Terrain, x0, z0, x1, z1 int32) float64 {
	total, flat := 0, 0
	for step := 0; step <= 32; step++ {
		x := x0 + (x1-x0)*int32(step)/32
		z := z0 + (z1-z0)*int32(step)/32
		for _, offset := range [3]int32{-96, 0, 96} {
			total++
			if simBenchFlatAt(terrain, x+offset, z) {
				flat++
			}
		}
	}
	if total == 0 {
		return 0
	}
	return float64(flat) / float64(total)
}

// simBenchFlatAt is land above sea level whose four neighbouring samples are
// within fourteen height units. It is a scene-placement heuristic for the
// fixture, not a movement predicate: the movement system remains the only
// authority on what a unit can traverse.
func simBenchFlatAt(terrain *world.Terrain, x, z int32) bool {
	sea := numeric.Fixed(int64(terrain.SeaLevel) << 16)
	centre := terrain.HeightAt(numeric.Fixed(int64(x)<<16), numeric.Fixed(int64(z)<<16))
	if centre <= sea {
		return false
	}
	for _, delta := range [4][2]int32{{48, 0}, {-48, 0}, {0, 48}, {0, -48}} {
		neighbour := terrain.HeightAt(numeric.Fixed(int64(x+delta[0])<<16), numeric.Fixed(int64(z+delta[1])<<16))
		difference := neighbour - centre
		if difference < 0 {
			difference = -difference
		}
		if difference >= numeric.Fixed(14)<<16 {
			return false
		}
	}
	return true
}

// simBenchFactoryHandles returns the placed factories' pool handles in
// placement order, for the census.
func (s *SimBenchScene) simBenchFactoryHandles() []pool.Handle {
	out := make([]pool.Handle, 0, len(s.factories))
	for _, factory := range s.factories {
		if factory != nil {
			out = append(out, factory.Handle)
		}
	}
	return out
}
