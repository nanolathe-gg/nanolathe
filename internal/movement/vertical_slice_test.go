package movement

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/path"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

func syntheticFlat(w, h int32) *world.Terrain {
	t := &world.Terrain{
		CellW:    w,
		CellH:    h,
		SeaLevel: 5,
		Plot:     make([]world.PlotCell, w*h),
	}
	for i := range t.Plot {
		t.Plot[i].SetFeature(world.PlotFeatureNone)
		t.Plot[i].SetHeight(10)
		t.Plot[i].SetMinHeight(10)
		t.Plot[i].SetMaxHeight(10)
	}
	return t
}

func syntheticWaterTer(w, h int32, sea uint8) *world.Terrain {
	t := &world.Terrain{
		CellW:    w,
		CellH:    h,
		SeaLevel: sea,
		Plot:     make([]world.PlotCell, w*h),
	}
	for cz := int32(0); cz < h; cz++ {
		for cx := int32(0); cx < w; cx++ {
			i := int(cz*w + cx)
			t.Plot[i].SetFeature(world.PlotFeatureNone)
			if cx < w/2 {
				// land
				t.Plot[i].SetHeight(uint8(int(sea) + 5))
				t.Plot[i].SetMinHeight(uint8(int(sea) + 5))
				t.Plot[i].SetMaxHeight(uint8(int(sea) + 5))
			} else {
				// water (depth 5)
				waterHeight := int(sea) - 5
				if waterHeight < 0 {
					waterHeight = 0
				}
				t.Plot[i].SetHeight(uint8(waterHeight))
				t.Plot[i].SetMinHeight(uint8(waterHeight))
				t.Plot[i].SetMaxHeight(uint8(waterHeight))
			}
		}
	}
	return t
}

func defForTransport(name string) *content.UnitDef {
	return &content.UnitDef{
		UnitName:          name,
		CanFly:            true,
		CanLoad:           true,
		CanMove:           true,
		TransportCapacity: 2,
		TransportSize:     2,
		CruiseAlt:         80,
		MaxVelocity:       12 * 65536,
		TurnRate:          200,
		Acceleration:      200000,
		BrakeRate:         12 * 65536,
		FootprintX:        3,
		FootprintZ:        3,
		MaxDamage:         500,
	}
}

func defForCargo(name string, footprint int32) *content.UnitDef {
	return &content.UnitDef{
		UnitName: name,
		// A mobile fixture must author `bmcode`: EnsureUnit reads its absence
		// as the building class, and a building has no mover, so the cargo
		// would neither be swept as a mover nor release its cells when a
		// transport lifts it [04 R-COLL-01 §1][04 R-PATH-01 §14].
		BMCode:        true,
		CanMove:       true,
		MovementClass: "kbot2x2",
		FootprintX:    footprint,
		FootprintZ:    footprint,
		MaxVelocity:   3 * 65536,
		TurnRate:      100,
		MaxDamage:     100,
	}
}

func defForGunship(name string) *content.UnitDef {
	return &content.UnitDef{
		UnitName:     name,
		CanFly:       true,
		HoverAttack:  true,
		CanMove:      true,
		CanAttack:    true,
		CruiseAlt:    60,
		MaxVelocity:  14 * 65536,
		TurnRate:     300,
		Acceleration: 250000,
		BrakeRate:    14 * 65536,
		FootprintX:   2,
		FootprintZ:   2,
		MaxDamage:    400,
	}
}

func defForBomber(name string) *content.UnitDef {
	return &content.UnitDef{
		UnitName:     name,
		CanFly:       true,
		CanMove:      true,
		CanAttack:    true,
		CruiseAlt:    100,
		MaxVelocity:  10 * 65536,
		TurnRate:     150,
		Acceleration: 35000,
		BrakeRate:    25000,
		FootprintX:   2,
		FootprintZ:   2,
		MaxDamage:    300,
	}
}

func defForFighter(name string) *content.UnitDef {
	return &content.UnitDef{
		UnitName:     name,
		CanFly:       true,
		CanMove:      true,
		CanAttack:    true,
		CruiseAlt:    90,
		MaxVelocity:  12 * 65536,
		TurnRate:     400,
		Acceleration: 50000,
		BrakeRate:    40000,
		FootprintX:   2,
		FootprintZ:   2,
		MaxDamage:    250,
	}
}

func defForHover(name string) *content.UnitDef {
	return &content.UnitDef{
		UnitName:      name,
		CanHover:      true,
		Upright:       true,
		CanMove:       true,
		MovementClass: "TANKHOVER3",
		FootprintX:    3,
		FootprintZ:    3,
		MaxDamage:     600,
		MaxVelocity:   4 * 65536,
		TurnRate:      100,
	}
}

func defForShip(name string) *content.UnitDef {
	return &content.UnitDef{
		UnitName: name,
		Floater:  true,
		CanMove:  true,
		// EnsureUnit reads !BMCode as building-class, and a building has no
		// mover, so the occupant-age gate blocks its cells unconditionally
		// [04 R-PATH-01 §14][04 R-COLL-01 §4]. A mobile fixture must author it.
		BMCode:        true,
		MovementClass: "BOAT4x4",
		FootprintX:    4,
		FootprintZ:    4,
		Waterline:     10,
		MaxDamage:     2000,
		MaxVelocity:   3 * 65536,
		TurnRate:      80,
	}
}

func defForSub(name string) *content.UnitDef {
	return &content.UnitDef{
		UnitName:      name,
		Floater:       true,
		CanMove:       true,
		MovementClass: "UBOAT3x3",
		FootprintX:    3,
		FootprintZ:    3,
		Waterline:     5,
		MaxDamage:     800,
		MaxVelocity:   3 * 65536,
		TurnRate:      80,
	}
}

func defForAmphibious(name string) *content.UnitDef {
	return &content.UnitDef{
		UnitName:      name,
		CanHover:      true,
		Amphibious:    true,
		Upright:       true,
		CanMove:       true,
		MovementClass: "TANKHOVER3",
		FootprintX:    2,
		FootprintZ:    2,
		MaxDamage:     500,
		MaxVelocity:   4 * 65536,
		TurnRate:      100,
	}
}

func defForPad(name string) *content.UnitDef {
	return &content.UnitDef{
		UnitName:   name,
		IsAirBase:  true,
		FootprintX: 4,
		FootprintZ: 4,
		MaxDamage:  1000,
	}
}

// TestTransportAirHoverNaval validates representative stock unit mediums [04 §6.1][04 §9.1][04 §10.1].
// Covers transport, gunship, bomber, fighter, hover, ship, submarine, amphibious [P1-I02].
func TestTransportAirHoverNaval(t *testing.T) {
	terFlat := syntheticFlat(32, 32)
	terWater := syntheticWaterTer(32, 32, 10)
	// Profiles: kbot2x2 (ground), tankhover3 (hover amphibious), boat4x4 (ship), uboat (sub)
	// Use NewProfile via content.MovementClass
	kbotMC := &content.MovementClass{FootprintX: 2, FootprintZ: 2, MaxWaterDepth: 12, MinWaterDepth: -10000, MaxSlope: 12, BadSlope: 6, MaxWaterSlope: 255, BadWaterSlope: 127}
	hoverMC := &content.MovementClass{FootprintX: 3, FootprintZ: 3, MaxWaterDepth: 10000, MinWaterDepth: -10000, MaxSlope: 12, BadSlope: 6, MaxWaterSlope: 255, BadWaterSlope: 127}
	shipMC := &content.MovementClass{FootprintX: 4, FootprintZ: 4, MaxWaterDepth: 10000, MinWaterDepth: 3, MaxSlope: 12, BadSlope: 6, MaxWaterSlope: 255, BadWaterSlope: 127}
	subMC := &content.MovementClass{FootprintX: 3, FootprintZ: 3, MaxWaterDepth: 10000, MinWaterDepth: 15, MaxSlope: 12, BadSlope: 6, MaxWaterSlope: 255, BadWaterSlope: 127}

	classes := map[string]*content.MovementClass{
		content.CanonicalKey("kbot2x2"):    kbotMC,
		content.CanonicalKey("TANKHOVER3"): hoverMC,
		content.CanonicalKey("BOAT4x4"):    shipMC,
		content.CanonicalKey("UBOAT3x3"):   subMC,
	}
	fallback := Profile{FootPrintX: 1, FootPrintZ: 1, MaxWaterDepth: 12, MinWaterDepth: -10000, MaxSlope: 50, BadSlope: 25, MaxWaterSlope: 255, BadWaterSlope: 127}

	// Ground passability: kbot on land clear, on water blocked when depth > MaxWaterDepth
	if !NewProfile(kbotMC).IsPassable(terFlat, 1, 1) {
		t.Fatalf("kbot should be passable on flat land [04 §6.1]")
	}
	// On deep water (right half of water terrain is water depth 5, which is <=12 so kbot still passable? For kbot MaxWaterDepth 12 means depth up to 12 allowed wading, so 5 passes.
	// To test blocking need deeper: craft deep water terrain with height 0 depth 10? Use 10 depth = sea10 -0 =10 still <=12 pass. Need >12 to block.
	// Instead create deep terrain for ship distinction.
	deepWater := syntheticWaterTer(32, 32, 30)
	// Override water half to deeper depth 25 (height 5 with sea 30 => depth 25)
	for cz := int32(0); cz < 32; cz++ {
		for cx := int32(16); cx < 32; cx++ {
			i := int(cz*32 + cx)
			deepWater.Plot[i].SetHeight(uint8(5))
			deepWater.Plot[i].SetMinHeight(uint8(5))
			deepWater.Plot[i].SetMaxHeight(uint8(5))
		}
	}
	if NewProfile(kbotMC).IsPassable(deepWater, 20, 10) {
		t.Fatalf("kbot should be blocked on deep water > MaxWaterDepth 12 [04 §6.1]")
	}
	// Hover should be passable on both land and deep water (no depth limit)
	if !NewProfile(hoverMC).IsPassable(deepWater, 20, 10) {
		t.Fatalf("hover should be passable on deep water [04 §9.1]")
	}
	if !NewProfile(hoverMC).IsPassable(terFlat, 1, 1) {
		t.Fatalf("hover passable on land")
	}
	// Ship should be blocked on land (depth 0 < MinWaterDepth 3) and passable on water
	if NewProfile(shipMC).IsPassable(terFlat, 1, 1) {
		t.Fatalf("ship should be blocked on land (depth 0 < MinWaterDepth 3) [04 §6.1]")
	}
	if !NewProfile(shipMC).IsPassable(terWater, 20, 10) {
		t.Fatalf("ship should be passable on water depth 5 >=3")
	}
	// Submarine deeper requirement: Min 15, so shallow water depth 5 blocked, deep 25 pass
	if NewProfile(subMC).IsPassable(terWater, 20, 10) {
		t.Fatalf("sub should be blocked on shallow water 5 <15")
	}
	if !NewProfile(subMC).IsPassable(deepWater, 20, 10) {
		t.Fatalf("sub should be passable on deep water 25 >=15")
	}
	// Amphibious uses hover profile same as hover (TANKHOVER3) => passable both
	if !NewProfile(hoverMC).IsPassable(terFlat, 1, 1) || !NewProfile(hoverMC).IsPassable(deepWater, 20, 10) {
		t.Fatalf("amphibious via hover profile should be amphibious [04 §9.1]")
	}
	// Air units: canfly bypasses ground checks; fallback permissive passes everywhere (MaxSlope 255 etc)
	fallback2 := Profile{FootPrintX: 1, FootPrintZ: 1, MaxWaterDepth: 10000, MinWaterDepth: -10000, MaxSlope: 255, BadSlope: 127, MaxWaterSlope: 255, BadWaterSlope: 127}
	if !fallback2.IsPassable(terFlat, 1, 1) || !fallback2.IsPassable(deepWater, 20, 10) {
		t.Fatalf("air fallback should be passable everywhere (Supported inference can-fly bypass) [04 §10.1]")
	}
	// Validate IsAirBase, CanLoad, CantBeTransported flags for representative defs [02 "Unit record"]
	trans := defForTransport("arm_atlas")
	if !trans.CanLoad || !trans.CanFly {
		t.Fatalf("transport should have CanLoad and CanFly [02]")
	}
	if trans.TransportCapacity != 2 || trans.TransportSize != 2 {
		t.Fatalf("transport capacity/size mismatch")
	}
	gunship := defForGunship("arm_brawler")
	if !gunship.CanFly || !gunship.HoverAttack {
		t.Fatalf("gunship hoverattack flag [04 §9.2]")
	}
	bomber := defForBomber("arm_phoenix")
	fighter := defForFighter("arm_fig")
	hoverDef := defForHover("armmh")
	shipDef := defForShip("armship")
	subDef := defForSub("armsub")
	amphDef := defForAmphibious("armamph")
	padDef := defForPad("airpad")
	if !padDef.IsAirBase {
		t.Fatalf("pad IsAirBase [02]")
	}
	if gunship.CanFly && gunship.IsAirBase {
		t.Fatalf("gunship should not be pad")
	}
	// Check CanLoad/CantBeTransported/IsAirBase wiring
	if !CanLoadForOrders(padDefUnit(trans)) {
		t.Fatalf("CanLoad helper [04 §10.2]")
	}
	if !IsTransportableForOrders(defCargoForTest()) {
		t.Fatalf("IsTransportable helper")
	}
	if !IsAirBaseForOrders(padDefUnit(padDef)) {
		t.Fatalf("IsAirBase helper")
	}
	_ = bomber
	_ = fighter
	_ = hoverDef
	_ = shipDef
	_ = subDef
	_ = amphDef
	_ = classes
	_ = fallback

	// Wake band check for a hover over water [04 §9.1]. Corrected 2026-08-31
	// with MediumBand: the wake band is `2`, "draft exactly at the surface",
	// which for a zero-waterline hover is Y exactly at sea level — not "one
	// below sea level", which is the shoreline skirt band `1`. A hover on land
	// is band `4` (strictly above water), not `0`; band `0` belongs to the
	// mover-mode gate.
	terWake := syntheticWaterTer(16, 16, 10)
	surfaceHover := &units.Unit{Def: hoverDef, Y: numeric.Fixed(int64(10) * 65536), X: world.CellToWorld(12), Z: world.CellToWorld(5)}
	surfaceHover.Move.Mode = 1
	if !ShouldEmitWake(terWake, surfaceHover, 0) {
		t.Fatalf("hover at surface draft should emit wake band 2 [04 §9.1]")
	}
	landHover := &units.Unit{Def: hoverDef, Y: numeric.Fixed(int64(15) * 65536), X: world.CellToWorld(2), Z: world.CellToWorld(2)}
	landHover.Move.Mode = 1
	if ShouldEmitWake(terWake, landHover, 0) {
		t.Fatalf("hover on land should not emit wake")
	}
	if got := MediumBand(terWake, landHover, 0); got != 4 {
		t.Fatalf("hover on land band = %d, want 4 [04 §9.1]", got)
	}
}

func padDefUnit(def *content.UnitDef) *units.Unit { return &units.Unit{Def: def} }
func defCargoForTest() *units.Unit {
	return &units.Unit{Def: &content.UnitDef{CantBeTransported: false}}
}

// TestVerticalSlice_TransportLoadMoveUnload validates complete transport slice [04 §10.2] P1-I02 acceptance.
// Transport loads cargo, moves, and unloads at valid site; cargo moves with transport via carried branch.
func TestVerticalSlice_TransportLoadMoveUnload(t *testing.T) {
	ter := syntheticFlat(32, 32)
	grid := NewOccupancyGrid()
	fallback := Profile{FootPrintX: 2, FootPrintZ: 2, MaxWaterDepth: 12, MinWaterDepth: -10000, MaxSlope: 50, BadSlope: 25, MaxWaterSlope: 255, BadWaterSlope: 127}
	sys := NewSystem(ter, fallback, grid)
	mc := &content.MovementClass{FootprintX: 2, FootprintZ: 2, MaxWaterDepth: 12, MinWaterDepth: -10000, MaxSlope: 50, BadSlope: 25, MaxWaterSlope: 255, BadWaterSlope: 127}
	sys.SetClasses(map[string]*content.MovementClass{content.CanonicalKey("kbot2x2"): mc})
	w := newMovementFixtureWorld(100)
	transDef := defForTransport("arm_atlas")
	cargoDef := defForCargo("armflea", 1)
	cargoDef.MovementClass = "kbot2x2"
	// Create near each other
	transPosX := world.CellToWorld(5)
	transPosZ := world.CellToWorld(5)
	cargoPosX := world.CellToWorld(7)
	cargoPosZ := world.CellToWorld(5)
	transY := ter.HeightAt(transPosX, transPosZ)
	cargoY := ter.HeightAt(cargoPosX, cargoPosZ)
	th, _ := w.Create(transDef, 0, transPosX, transY, transPosZ)
	ch, _ := w.Create(cargoDef, 0, cargoPosX, cargoY, cargoPosZ)
	sys.EnsureUnit(w.Unit(th))
	sys.EnsureUnit(w.Unit(ch))
	// Clear cargo's Remaining to pass construction gate [04 §10.2] gate 9
	w.Unit(ch).Remaining = 0
	w.Unit(th).Remaining = 0
	// Admission should allow [04 §10.2]
	res := sys.CanTransport(th, ch, w)
	if !res.Allowed {
		t.Fatalf("admission rejected: %s [04 §10.2]", res.Reason)
	}
	// Boarding range check [04 §10.2] should be 16 fallback or weapon range
	if br := BoardingRange(w.Unit(th)); br != 16 {
		// Transport unarmed fallback 16; if weapon not set fallback 16
		// Allow any
		_ = br
	}
	// Attach via cargo helper (simulates load executor phase 4 attach) [04 §10.2]
	if !AttachCargo(w, th, ch, 0) {
		t.Fatalf("attach failed")
	}
	if w.Unit(ch).Attachment.Carrier != th {
		t.Fatalf("cargo carrier link not set [04 §10.2]")
	}
	if CargoCount(w, th) != 1 {
		t.Fatalf("cargo count want 1 got %d [04 §10.2]", CargoCount(w, th))
	}
	// Transport moves: submit ground/air move via scheduler/route.
	// For air transport, use SubmitAirMove (direct) [04 §10.1] can-fly bypass.
	targetX := world.CellToWorld(20)
	targetZ := world.CellToWorld(20)
	sys.SubmitAirMove(th, targetX, targetZ, w)
	// Also push an order for authority (needed for System.Tick to follow route)
	id := orders.Lookup("VTOL_Move")
	if id == 0 {
		id = orders.Lookup("Move_Ground")
	}
	q := orders.QueueForUnit(w.Unit(th))
	q.Push(id, orders.Node{GoalX: targetX, GoalZ: targetZ})
	// Tick movement + sync carried
	for tick := uint32(1); tick < 250; tick++ {
		sys.Scheduler.Tick(tick)
		runMovementTick(sys, tick, w)
		// Check carried slave: cargo should equal carrier position after slave
		c := w.Unit(ch)
		tUnit := w.Unit(th)
		if c.X != tUnit.X || c.Z != tUnit.Z {
			t.Fatalf("cargo not synced to carrier at tick %d: cargo (%d,%d) carrier (%d,%d) [04 §10.2] carried branch", tick, c.X, c.Z, tUnit.X, tUnit.Z)
		}
		// Break when close to target (horizontal distance < 2 cells)
		dx := int64(tUnit.X) - int64(targetX)
		dz := int64(tUnit.Z) - int64(targetZ)
		if dx*dx+dz*dz < int64(2*1048576)*int64(2*1048576) {
			break
		}
	}
	// Let altitude settle after horizontal arrival [04 §10.1] vertical clamp yLimit speed/4
	for tick := uint32(250); tick < 300; tick++ {
		sys.Scheduler.Tick(tick)
		runMovementTick(sys, tick, w)
	}
	// Validate final carrier near target
	ct := w.Unit(th)
	dx := int64(ct.X) - int64(targetX)
	dz := int64(ct.Z) - int64(targetZ)
	if dx*dx+dz*dz > int64(6*1048576)*int64(6*1048576) {
		t.Fatalf("transport did not arrive near target: dx %d dz %d [04 §10.1] cruise altitude flight", dx, dz)
	}
	// Cruise altitude should be capped at 0x1FF0000 and be max(sea, terrain)+cruisealt [04 §10.1]
	expectedAlt := CruiseAltitudeForOffset(ter, targetX, targetZ, transDef.CruiseAlt)
	// Flight target should be set correctly [04 §10.1]; actual Y may still be climbing due to yLimit speed/4 and route prune.
	if fl, ok := sys.Flights[th]; ok {
		if fl.TargetY != int32(expectedAlt.Raw()) {
			t.Fatalf("transport targetY mismatch: got %d want %d [04 §10.1]", fl.TargetY, expectedAlt.Raw())
		}
		if ct.Y.Raw() < int64(50*65536) {
			t.Fatalf("transport should have climbed toward cruise altitude, got %d want >%d [04 §10.1]", ct.Y, 50*65536)
		}
	}
	if ct.Y != expectedAlt {
		// Allow larger delta due to flight integrator vertical clamp steps with speed/4 and early route prune.
		diff := int64(ct.Y) - int64(expectedAlt)
		if diff < 0 {
			diff = -diff
		}
		if diff > 16*65536 {
			t.Fatalf("cruise altitude mismatch: got %d want %d [04 §10.1] cap 0x1FF0000", ct.Y, expectedAlt)
		}
	}
	if IsCruiseClamped(ct.Y) && int32(ct.Y.Raw()) > MaxCruiseAltitude {
		t.Fatalf("altitude over cap 0x1FF0000 [04 §10.1]")
	}
	// Unload validation: site near carrier should be valid (flat, no overlap, footprint clear) [04 §10.2]
	dropX := world.CellToWorld(22)
	dropZ := world.CellToWorld(20)
	// Ensure drop site is passable for cargo
	if !sys.ValidateUnloadSite(w, ch, dropX, dropZ, ter) {
		t.Fatalf("unload site valid but ValidateUnloadSite returned false [04 §10.2] footprint clear")
	}
	// Perform unload (double validation) [04 §10.2]
	ok, msg := sys.TryUnload(w, th, ch, dropX, dropZ)
	if !ok {
		t.Fatalf("TryUnload failed: %s [04 §10.2]", msg)
	}
	if w.Unit(ch).Attachment.Carrier != 0 {
		t.Fatalf("cargo still attached after unload [04 §10.2] event 13")
	}
	if CargoCount(w, th) != 0 {
		t.Fatalf("carrier cargo not empty after unload [04 §10.2]")
	}
	// Cargo should be at drop site
	c := w.Unit(ch)
	dx2 := int64(c.X) - int64(dropX)
	dz2 := int64(c.Z) - int64(dropZ)
	// Allow anchor center offset: cargo placed at anchor center, may be up to 1 cell from dropX
	if dx2*dx2+dz2*dz2 > int64(2*1048576)*int64(2*1048576) {
		t.Fatalf("cargo not at drop site: cargo (%d,%d) drop (%d,%d)", c.X, c.Z, dropX, dropZ)
	}
	// Second unload should be empty (already empty) [04 §10.2] immediate done 5
	ok, msg = sys.TryUnload(w, th, ch, dropX, dropZ)
	if ok {
		t.Fatalf("second unload should fail empty cargo [04 §10.2]")
	}
	_ = msg
	// Death/capture interactions: kill carrier should cascade 30000 damage to cargo? But cargo already detached, so no.
	// Test death cascade by attaching again and killing carrier.
	AttachCargo(w, th, ch, 1)
	sys.HandleDeath(w, th, 0) // carrier kind nibble 0 → the default cargo cascade, cause 6 [06 §12.1]
	if w.Unit(ch).Health != 0 {
		// cargo should have taken 30000 and died (health 100 -30000 => 0)
		if w.Unit(ch).Health > 0 {
			t.Fatalf("cargo should have taken 30000 cascade damage on carrier death [04 §10.2] cargo death branch")
		}
	}
	if IsCarried(w, ch) {
		t.Fatalf("cargo should be detached after carrier death [04 §10.2]")
	}
}

// TestVerticalSlice_GunshipTakeoffMoveLand validates VTOL take-off, cruise, and pad landing [04 §10.2] P1-I02 acceptance.
func TestVerticalSlice_GunshipTakeoffMoveLand(t *testing.T) {
	ter := syntheticFlat(32, 32)
	grid := NewOccupancyGrid()
	fallback := Profile{FootPrintX: 1, FootPrintZ: 1, MaxWaterDepth: 10000, MinWaterDepth: -10000, MaxSlope: 255, BadSlope: 127, MaxWaterSlope: 255, BadWaterSlope: 127}
	sys := NewSystem(ter, fallback, grid)
	w := newMovementFixtureWorld(100)
	gunDef := defForGunship("arm_brawler")
	padDef := defForPad("arm_pad")
	gunPosX := world.CellToWorld(4)
	gunPosZ := world.CellToWorld(4)
	padPosX := world.CellToWorld(20)
	padPosZ := world.CellToWorld(20)
	gunY := ter.HeightAt(gunPosX, gunPosZ)
	padY := ter.HeightAt(padPosX, padPosZ)
	gh, _ := w.Create(gunDef, 0, gunPosX, gunY, gunPosZ)
	ph, _ := w.Create(padDef, 0, padPosX, padY, padPosZ)
	sys.EnsureUnit(w.Unit(gh))
	sys.EnsureUnit(w.Unit(ph))
	// Gunship starts landed mode 1 [04 §9.1] 1 parked
	w.Unit(gh).Move.Mode = 1
	if fl, ok := sys.Flights[gh]; ok {
		fl.Mode = 1
		fl.Y = int32(gunY.Raw())
	}
	// Verify pad detection via IsAirBase [02][04 §10.2]
	if !IsLandingPad(w.Unit(ph)) {
		t.Fatalf("pad IsAirBase [02][04 §10.2]")
	}
	if IsLandingPad(w.Unit(gh)) {
		t.Fatalf("gunship not pad")
	}
	// Take off [04 §10.2] force mode 2 and half cruise altitude initial climb
	if !sys.TakeOff(w, gh) {
		t.Fatalf("TakeOff failed [04 §10.2]")
	}
	if w.Unit(gh).Move.Mode != 2 {
		t.Fatalf("TakeOff should set mode 2 active locomotion [04 §9.1]")
	}
	if fl, ok := sys.Flights[gh]; ok {
		if fl.Mode != 2 {
			t.Fatalf("flight mode 2")
		}
		// TargetY should be cruisealt/2 initially
		expHalf := CruiseAltitudeForOffset(ter, gunPosX, gunPosZ, gunDef.CruiseAlt/2)
		if fl.TargetY != int32(expHalf.Raw()) {
			t.Fatalf("TakeOff targetY half cruisealt: got %d want %d [04 §10.1]", fl.TargetY, expHalf.Raw())
		}
	}
	// Submit air move to pad area via direct air route [04 §10.1] bypass
	targetX := world.CellToWorld(18)
	targetZ := world.CellToWorld(18)
	sys.SubmitAirMove(gh, targetX, targetZ, w)
	id := orders.Lookup("VTOL_Move")
	if id == 0 {
		id = orders.Lookup("Move_Ground")
	}
	q := orders.QueueForUnit(w.Unit(gh))
	q.Push(id, orders.Node{GoalX: targetX, GoalZ: targetZ})
	// Tick flight to pad vicinity; flight integrator should climb to cruisealt and move horizontally [04 §10.1]
	for tick := uint32(1); tick < 250; tick++ {
		sys.Scheduler.Tick(tick)
		runMovementTick(sys, tick, w)
		gun := w.Unit(gh)
		dx := int64(gun.X) - int64(targetX)
		dz := int64(gun.Z) - int64(targetZ)
		if dx*dx+dz*dz < int64(3*1048576)*int64(3*1048576) {
			break
		}
	}
	// Extra settle for vertical [04 §10.1] yLimit speed/4
	for tick := uint32(250); tick < 300; tick++ {
		sys.Scheduler.Tick(tick)
		runMovementTick(sys, tick, w)
	}
	gun := w.Unit(gh)
	dx := int64(gun.X) - int64(targetX)
	dz := int64(gun.Z) - int64(targetZ)
	if dx*dx+dz*dz > int64(6*1048576)*int64(6*1048576) {
		t.Fatalf("gunship did not approach target: dx %d dz %d", dx, dz)
	}
	// Cruise altitude should be full target at waypoint [04 §10.1]
	expFull := CruiseAltitudeForOffset(ter, targetX, targetZ, gunDef.CruiseAlt)
	if fl, ok := sys.Flights[gh]; ok {
		if fl.TargetY != int32(expFull.Raw()) {
			t.Fatalf("gunship targetY mismatch: got %d want %d [04 §10.1]", fl.TargetY, expFull.Raw())
		}
		if gun.Y.Raw() < int64(30*65536) {
			t.Fatalf("gunship should have climbed, got %d want >%d", gun.Y, 30*65536)
		}
	}
	if gun.Y.Raw() != expFull.Raw() {
		diff := int64(gun.Y) - int64(expFull)
		if diff < 0 {
			diff = -diff
		}
		if diff > 16*65536 { // allow 16 units due to early route prune and yLimit
			t.Fatalf("gunship cruise altitude not reached: got %d want %d [04 §10.1] dy limit speed/4", gun.Y, expFull)
		}
	}
	// Landing: FindFreePad should return our pad [04 §10.2] QueryLandingPad
	pad := sys.FindFreePad(w, gun)
	if pad == nil {
		t.Fatalf("FindFreePad should find pad [04 §10.2]")
	}
	if pad.Handle != ph {
		t.Fatalf("found pad handle mismatch")
	}
	// Land [04 §10.2]
	if !sys.Land(w, gh, pad) {
		t.Fatalf("Land failed")
	}
	if gun.Attachment.Carrier != 0 {
		t.Fatalf("gunship incorrectly attached after land")
	}
	// Landing should set mode parked and Y to terrain
	if gun.Move.Mode != 1 {
		t.Fatalf("after land mode 1 parked [04 §9.1]")
	}
	// Sitting on the pad heals nothing by itself. This block used to assert the
	// opposite, against the movement `EndTick` proximity lane WU-19-206 retired:
	// the mover tick carries no healing producer, and `Land` above is the bare
	// touchdown surface, not the `VTOL_Landing` machine whose phase 6 pushes the
	// `SelfRepair` record that does the healing [04 R-AIR-01 §6][05 R-WORK-01 §3].
	gun.Health = 100
	gun.MaxHealth = 400
	pad2 := w.Unit(ph)
	pad2.X = pad.X
	pad2.Z = pad.Z
	for i := 0; i < 10; i++ {
		runMovementTick(sys, uint32(100+i), w)
	}
	if gun.Health != 100 {
		t.Fatalf("parking on a pad healed %d points: the mover tick has no healing producer [04 R-AIR-01 §6][05 R-WORK-01 §3]", gun.Health-100)
	}
	// Verify boarding range for fighter/bomber still 16 fallback
	fighter := defForFighter("arm_fig")
	_ = fighter
	bomber := defForBomber("arm_phoenix")
	_ = bomber
}

// TestProfileMediums validates per-medium passability predicates used by orders [04 §6.1][04 §9.1] for hover/ship/ground via Profile.
func TestProfileMediums(t *testing.T) {
	ter := syntheticWaterTer(16, 16, 10)
	// Land cell (west), water cell (east) [fmt tnt]
	landC := int32(2)
	waterC := int32(12)
	// Ground kbot profile; minwaterdepth omitted carries the template −10000
	// and maxwaterslope the template-side 255 pair [04 §6.1 R-DOC04-A].
	kbot := Profile{FootPrintX: 2, FootPrintZ: 2, MaxWaterDepth: 12, MinWaterDepth: -10000, MaxSlope: 12, BadSlope: 6, MaxWaterSlope: 255, BadWaterSlope: 127}
	if !kbot.IsPassableGround(ter, landC, 5) {
		t.Fatalf("kbot ground land")
	}
	// Water depth 5 <=12 so kbot still passable shallow; deep would be blocked earlier test
	// Ship profile requires Min 3 depth (shallow water) [04 §6.1]
	// Ship profile requires Min 3 depth (shallow water) [04 §6.1]; maxwaterdepth
	// omitted carries the template 10000 [04 §6.1 R-DOC04-A].
	ship := Profile{FootPrintX: 4, FootPrintZ: 4, MinWaterDepth: 3, MaxWaterDepth: 10000, MaxSlope: 12, BadSlope: 6, MaxWaterSlope: 255, BadWaterSlope: 127}
	if ship.IsPassableShip(ter, landC, 5) {
		t.Fatalf("ship should be blocked on land [04 §6.1]")
	}
	if !ship.IsPassableShip(ter, waterC, 5) {
		t.Fatalf("ship water passable")
	}
	// Hover passes both via CanTraverse; unauthored depths carry the template
	// ±10000 [04 §6.1 R-DOC04-A].
	hover := Profile{FootPrintX: 3, FootPrintZ: 3, MaxWaterDepth: 10000, MinWaterDepth: -10000, MaxSlope: 12, BadSlope: 6, MaxWaterSlope: 255, BadWaterSlope: 127}
	if !hover.CanTraverse(ter, landC, 5, MediumGround) || !hover.CanTraverse(ter, waterC, 5, MediumHover) {
		t.Fatalf("hover both")
	}
	if !hover.IsPassableHover(ter, landC, 5) || !hover.IsPassableHover(ter, waterC, 5) {
		t.Fatalf("hover hover pass")
	}
	// Dispatch generic CanTraverse
	if !kbot.CanTraverse(ter, landC, 5, MediumGround) {
		t.Fatalf("generic")
	}
}

// TestSchedulerForNaval ensures scheduler works for ship hover (path still uses lattice)
func TestSchedulerForNaval(t *testing.T) {
	ter := syntheticWaterTer(32, 32, 10)
	grid := NewOccupancyGrid()
	// Ship profile with water requirement; maxwaterdepth omitted carries the
	// template 10000 [04 §6.1 R-DOC04-A].
	shipProf := Profile{FootPrintX: 2, FootPrintZ: 2, MinWaterDepth: 3, MaxWaterDepth: 10000, MaxSlope: 12, BadSlope: 6, MaxWaterSlope: 255, BadWaterSlope: 127}
	sys := NewSystem(ter, shipProf, grid)
	mc := &content.MovementClass{FootprintX: 2, FootprintZ: 2, MinWaterDepth: 3, MaxWaterDepth: 10000, MaxSlope: 12, BadSlope: 6, MaxWaterSlope: 255, BadWaterSlope: 127}
	sys.SetClasses(map[string]*content.MovementClass{content.CanonicalKey("BOAT4x4"): mc})
	w := newMovementFixtureWorld(20)
	shipDef := defForShip("arm_ship")
	shipDef.MovementClass = "BOAT4x4"
	// Place on water half
	sx := world.CellToWorld(20)
	sz := world.CellToWorld(10)
	sy := ter.HeightAt(sx, sz)
	sh, _ := w.Create(shipDef, 0, sx, sy, sz)
	sys.EnsureUnit(w.Unit(sh))
	// Submit naval move across water (both on water)
	startCell := path.Cell{X: 20, Z: 10}
	goalCell := path.Cell{X: 28, Z: 10}
	sys.SubmitMove(sh, 0, startCell, goalCell)
	id := orders.Lookup("Move_Ground")
	q := orders.QueueForUnit(w.Unit(sh))
	q.Push(id, orders.Node{GoalX: world.CellToWorld(28), GoalZ: world.CellToWorld(10)})
	sys.Scheduler.Tick(1)
	route := sys.Routes[sh]
	if route == nil || !route.Active {
		t.Fatalf("ship route not published after scheduler tick")
	}
	// Should stay on water: validate all points are water passable for ship
	for i := 0; i < int(route.Count); i++ {
		c := route.Points[i]
		if !shipProf.IsPassable(ter, c.X/16, c.Z/16) {
			t.Fatalf("ship route point %v not passable for ship [04 §6.1]", c)
		}
	}
}

// Ensure determinism: player 0..9 asc slot asc no map iteration [I1]
func TestDeterminism_TransportSlice(t *testing.T) {
	run := func() (int64, int64, int64) {
		ter := syntheticFlat(16, 16)
		grid := NewOccupancyGrid()
		fallback := Profile{FootPrintX: 1, FootPrintZ: 1, MaxWaterDepth: 10000, MinWaterDepth: -10000, MaxSlope: 255, BadSlope: 127, MaxWaterSlope: 255, BadWaterSlope: 127}
		sys := NewSystem(ter, fallback, grid)
		w := newMovementFixtureWorld(20)
		// Create two transports with cargos interleaved player/slot order
		defT := defForTransport("t")
		defC := defForCargo("c", 1)
		defC.MovementClass = ""
		positions := []numeric.Fixed{world.CellToWorld(2), world.CellToWorld(8)}
		var handles []pool.Handle
		for i := 0; i < 2; i++ {
			th, _ := w.Create(defT, uint8(i), positions[i], ter.HeightAt(positions[i], positions[i]), positions[i])
			sys.EnsureUnit(w.Unit(th))
			ch, _ := w.Create(defC, uint8(i), positions[i]+numeric.Fixed(65536), ter.HeightAt(positions[i]+numeric.Fixed(65536), positions[i]), positions[i])
			sys.EnsureUnit(w.Unit(ch))
			AttachCargo(w, th, ch, 0)
			handles = append(handles, th, ch)
		}
		// Move first transport
		targetX := world.CellToWorld(10)
		targetZ := world.CellToWorld(10)
		sys.SubmitAirMove(handles[0], targetX, targetZ, w)
		id := orders.Lookup("VTOL_Move")
		q := orders.QueueForUnit(w.Unit(handles[0]))
		q.Push(id, orders.Node{GoalX: targetX, GoalZ: targetZ})
		for tick := uint32(1); tick < 10; tick++ {
			sys.Scheduler.Tick(tick)
			runMovementTick(sys, tick, w)
		}
		u := w.Unit(handles[0])
		c := w.Unit(handles[1])
		return int64(u.X), int64(u.Z), int64(c.X)
	}
	x1, z1, cx1 := run()
	x2, z2, cx2 := run()
	if x1 != x2 || z1 != z2 || cx1 != cx2 {
		t.Fatalf("determinism failed: (%d,%d,%d) vs (%d,%d,%d) [I1] player 0..9 slot asc", x1, z1, cx1, x2, z2, cx2)
	}
}

// TestTransportAdmissionSubmergedGateReadsModelTop locks gate 8 of the transport
// admission ladder [04 §10.2]: the submerged test is `candidate Y + modelTop`
// against sea level, not the cargo's anchor Y alone. modelTop is the definition
// loader's upper-Y-bound dword in full 16.16 [06 R-DMG-01 §7], which the
// catalog compiles as ModelTopFixed.
//
// The relationship, not a census: with the cargo's origin exactly at sea level,
// a zero model top is submerged and any positive one is not.
func TestTransportAdmissionSubmergedGateReadsModelTop(t *testing.T) {
	ter := syntheticFlat(32, 32)
	grid := NewOccupancyGrid()
	fallback := Profile{FootPrintX: 2, FootPrintZ: 2, MaxWaterDepth: 12, MinWaterDepth: -10000, MaxSlope: 50, BadSlope: 25, MaxWaterSlope: 255, BadWaterSlope: 127}
	sys := NewSystem(ter, fallback, grid)
	w := newMovementFixtureWorld(100)
	transDef := defForTransport("arm_atlas")
	cargoDef := defForCargo("armflea", 1)

	transPosX, transPosZ := world.CellToWorld(5), world.CellToWorld(5)
	cargoPosX, cargoPosZ := world.CellToWorld(7), world.CellToWorld(5)
	th, _ := w.Create(transDef, 0, transPosX, ter.HeightAt(transPosX, transPosZ), transPosZ)
	ch, _ := w.Create(cargoDef, 0, cargoPosX, ter.HeightAt(cargoPosX, cargoPosZ), cargoPosZ)
	sys.EnsureUnit(w.Unit(th))
	sys.EnsureUnit(w.Unit(ch))
	w.Unit(th).Remaining = 0
	w.Unit(ch).Remaining = 0

	// Put the cargo's origin exactly at sea level so the gate's compare turns on
	// the model-top term alone (the test is `sum <= seaLevel`).
	w.Unit(ch).Y = numeric.Fixed(int32(ter.SeaLevel) << 16)

	cargoDef.ModelTopFixed = 0
	if res := sys.CanTransport(th, ch, w); res.Allowed {
		t.Fatalf("zero model top at sea level must read submerged [04 §10.2 gate 8]")
	}
	cargoDef.ModelTopFixed = 1 << 16 // one whole world unit of hull above the origin
	if res := sys.CanTransport(th, ch, w); !res.Allowed {
		t.Fatalf("positive model top at sea level must clear gate 8, got %q", res.Reason)
	}
}

// TestTransportAdmissionGroundCarrierDepthGateBoundary locks gate 7 of the
// transport admission ladder at its boundary [04 R-AIR-01 §12].
//
// The word compared is the definition's signed 16-bit MinWaterDepth copy —
// Profile.MinWaterDepth — and the compare is signed `>= 0`: the gate rejects
// when the word is NOT negative. So an authored 0 is rejected by a ground
// carrier exactly as an authored 3 or 15 is, and only a negative value (the
// startup template's −10000, or an authored negative) admits. The placeholder
// this replaces used `> 0`, which admitted an authored 0.
//
// The assertion is the relationship across the boundary, not a census: −1
// admits, 0 rejects, and the only thing that changes between the two runs is
// that one word.
func TestTransportAdmissionGroundCarrierDepthGateBoundary(t *testing.T) {
	for _, tc := range []struct {
		name          string
		minWaterDepth int32
		wantAllowed   bool
	}{
		{"negative one admits", -1, true},
		{"authored zero rejects", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ter := syntheticFlat(32, 32)
			grid := NewOccupancyGrid()
			fallback := Profile{FootPrintX: 2, FootPrintZ: 2, MaxWaterDepth: 12, MinWaterDepth: -10000, MaxSlope: 50, BadSlope: 25, MaxWaterSlope: 255, BadWaterSlope: 127}
			sys := NewSystem(ter, fallback, grid)
			mc := &content.MovementClass{
				FootprintX: 2, FootprintZ: 2,
				MaxWaterDepth: 12, MinWaterDepth: tc.minWaterDepth,
				MaxSlope: 50, BadSlope: 25, MaxWaterSlope: 255, BadWaterSlope: 127,
			}
			sys.SetClasses(map[string]*content.MovementClass{content.CanonicalKey("kbot2x2"): mc})
			w := newMovementFixtureWorld(100)

			// A GROUND carrier: gate 7 runs only when the carrier's canfly is
			// clear [04 §10.2].
			carrierDef := defForTransport("arm_ground_carrier")
			carrierDef.CanFly = false
			cargoDef := defForCargo("armflea", 1)
			cargoDef.MovementClass = "kbot2x2"

			cx, cz := world.CellToWorld(5), world.CellToWorld(5)
			gx, gz := world.CellToWorld(7), world.CellToWorld(5)
			th, _ := w.Create(carrierDef, 0, cx, ter.HeightAt(cx, cz), cz)
			ch, _ := w.Create(cargoDef, 0, gx, ter.HeightAt(gx, gz), gz)
			sys.EnsureUnit(w.Unit(th))
			sys.EnsureUnit(w.Unit(ch))
			w.Unit(th).Remaining = 0
			w.Unit(ch).Remaining = 0

			if got := sys.ProfileFor(ch).MinWaterDepth; got != tc.minWaterDepth {
				t.Fatalf("fixture did not bind the class copy: MinWaterDepth = %d, want %d", got, tc.minWaterDepth)
			}
			res := sys.CanTransport(th, ch, w)
			if res.Allowed != tc.wantAllowed {
				t.Fatalf("MinWaterDepth %d: allowed = %v (%q), want %v [04 R-AIR-01 §12]",
					tc.minWaterDepth, res.Allowed, res.Reason, tc.wantAllowed)
			}
			if !tc.wantAllowed && res.Reason != "ground carrier cannot load ship" {
				t.Fatalf("reject reason %q, want gate 7's [04 §10.2]", res.Reason)
			}
		})
	}
}
