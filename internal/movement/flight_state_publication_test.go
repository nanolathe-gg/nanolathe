package movement

import (
	"encoding/binary"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func TestFlightCommitPublishesMoverSaveWords(t *testing.T) {
	sys, _, u := takeoffFixture(t)
	h := u.Handle
	fl := handleRow(sys.Flights, h)
	fl.X, fl.Y, fl.Z = 11, 12, 13
	fl.VX, fl.VY, fl.VZ = 21, -22, 23
	fl.LeanX, fl.LeanY, fl.LeanZ = 31, -32, 33
	fl.Speed, fl.TurnResidual = 41, -42
	fl.Heading = 43
	sys.commitFlightState(u, fl)

	image, err := sys.RetailMoverImage(h)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []int32{21, -22, 23, 31, -32, 33, 41} {
		if got := int32(binary.LittleEndian.Uint32(image[i*4:])); got != want {
			t.Fatalf("save word %d = %d, want %d", i, got, want)
		}
	}
	if got := int16(binary.LittleEndian.Uint16(image[28:])); got != -42 {
		t.Fatalf("save turn residual = %d, want -42", got)
	}
	if u.Move.VelX != numeric.Fixed(21) || u.Move.VelY != numeric.Fixed(-22) || u.Move.VelZ != numeric.Fixed(23) || u.Move.Speed != numeric.Fixed(41) {
		t.Fatalf("unit mover mirrors = (%d,%d,%d;%d), want committed flight values", u.Move.VelX, u.Move.VelY, u.Move.VelZ, u.Move.Speed)
	}
}

func TestFlightRestoreRepublishesLiveWordsBeforeCallbacks(t *testing.T) {
	sys, _, u := takeoffFixture(t)
	if !sys.SetMoverMode(u, 2) {
		t.Fatal("airborne mode setup did not change the mover")
	}
	h := u.Handle
	fl := handleRow(sys.Flights, h)
	fl.VX, fl.VY, fl.VZ = 3, -4, 5
	fl.LeanX, fl.LeanY, fl.LeanZ = 6, -7, 8
	fl.Speed, fl.TurnResidual = 9, -10
	sys.commitFlightState(u, fl)
	handleRow(sys.Collisions, h).LastStampTick = 11
	image, err := sys.RetailMoverImage(h)
	if err != nil {
		t.Fatal(err)
	}

	fl.VX, fl.VY, fl.VZ = 0, 0, 0
	fl.LeanX, fl.LeanY, fl.LeanZ, fl.Speed, fl.TurnResidual = 0, 0, 0, 0, 0
	u.Move.VelX, u.Move.VelY, u.Move.VelZ, u.Move.Speed = 0, 0, 0, 0
	if err := sys.RestoreMover(h, image); err != nil {
		t.Fatal(err)
	}
	if got := [7]int32{fl.VX, fl.VY, fl.VZ, fl.LeanX, fl.LeanY, fl.LeanZ, fl.Speed}; got != [7]int32{3, -4, 5, 6, -7, 8, 9} || fl.TurnResidual != -10 {
		t.Fatalf("restored flight words = %v residual %d", got, fl.TurnResidual)
	}
	if got := [4]numeric.Fixed{u.Move.VelX, u.Move.VelY, u.Move.VelZ, u.Move.Speed}; got != [4]numeric.Fixed{3, -4, 5, 9} {
		t.Fatalf("callback-visible mover mirrors = %v, want restored words", got)
	}
	u.Def.MoveRate1, u.Def.MoveRate2 = 4, 8
	sys.emitMovementCallbacks(u, fl.Speed)
	if u.MoveTier != 3 {
		t.Fatalf("restored speed classified as tier %d, want 3", u.MoveTier)
	}
}

func TestLandingModePublishesDecayedWordsBeforeDeactivateCue(t *testing.T) {
	sys, _, u := takeoffFixture(t)
	if !sys.SetMoverMode(u, 2) {
		t.Fatal("airborne mode setup did not change the mover")
	}
	fl := handleRow(sys.Flights, u.Handle)
	fl.VX, fl.VY, fl.VZ, fl.Speed = 12, -13, 14, 15
	fl.LeanX, fl.LeanY, fl.LeanZ, fl.TurnResidual = 1<<20, -(1 << 20), 1<<19, -16
	sys.commitFlightState(u, fl)

	var observed []byte
	u.SetStatusCueSink(func(_ *units.Unit, code uint8) {
		if code != units.StatusCueDeactivate {
			return
		}
		var err error
		observed, err = sys.RetailMoverImage(u.Handle)
		if err != nil {
			t.Fatalf("read mover words at deactivate cue: %v", err)
		}
	})
	if !sys.SetMoverMode(u, 1) {
		t.Fatal("landing mode setup did not change the mover")
	}
	if len(observed) != 35 {
		t.Fatal("deactivate cue did not observe a mover image")
	}
	for i, want := range []int32{0, 0, 0, fl.LeanX, fl.LeanY, fl.LeanZ, 0} {
		if got := int32(binary.LittleEndian.Uint32(observed[i*4:])); got != want {
			t.Fatalf("deactivate-cue mover word %d = %d, want %d", i, got, want)
		}
	}
	if got := int16(binary.LittleEndian.Uint16(observed[28:])); got != fl.TurnResidual {
		t.Fatalf("deactivate-cue residual = %d, want %d", got, fl.TurnResidual)
	}
}

func TestFlightSaveRestoreMatchesNextNormalTickAfter120Ticks(t *testing.T) {
	baseline, baselineWorld, baselineUnit := airFixture(t)
	restored, restoredWorld, restoredUnit := airFixture(t)
	goalX, goalZ := world.CellToWorld(26), world.CellToWorld(8)
	pushAirOrder(t, baselineUnit, "VTOL_Move", goalX, goalZ)
	pushAirOrder(t, restoredUnit, "VTOL_Move", goalX, goalZ)
	for tick := uint32(1); tick <= 120; tick++ {
		runMovementTick(baseline, tick, baselineWorld)
		runMovementTick(restored, tick, restoredWorld)
	}
	if baselineUnit.X == world.CellToWorld(8) || baselineUnit.Y == 0 {
		t.Fatal("120-tick flight fixture did not complete ordinary takeoff and travel")
	}
	image, err := restored.RetailMoverImage(restoredUnit.Handle)
	if err != nil {
		t.Fatal(err)
	}
	if err := restored.RestoreMover(restoredUnit.Handle, image); err != nil {
		t.Fatal(err)
	}
	const nextTick = uint32(121)
	runMovementTick(baseline, nextTick, baselineWorld)
	runMovementTick(restored, nextTick, restoredWorld)
	if got, want := [9]int64{
		int64(restoredUnit.X), int64(restoredUnit.Y), int64(restoredUnit.Z),
		int64(restoredUnit.Move.VelX), int64(restoredUnit.Move.VelY), int64(restoredUnit.Move.VelZ),
		int64(restoredUnit.Move.Speed), int64(restoredUnit.Move.Bank), int64(restoredUnit.Move.Pitch),
	}, [9]int64{
		int64(baselineUnit.X), int64(baselineUnit.Y), int64(baselineUnit.Z),
		int64(baselineUnit.Move.VelX), int64(baselineUnit.Move.VelY), int64(baselineUnit.Move.VelZ),
		int64(baselineUnit.Move.Speed), int64(baselineUnit.Move.Bank), int64(baselineUnit.Move.Pitch),
	}; got != want {
		t.Fatalf("post-restore normal tick state = %v, want %v", got, want)
	}
	gotImage, err := restored.RetailMoverImage(restoredUnit.Handle)
	if err != nil {
		t.Fatal(err)
	}
	wantImage, err := baseline.RetailMoverImage(baselineUnit.Handle)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotImage) != string(wantImage) {
		t.Fatalf("post-restore mover image differs:\n got %x\nwant %x", gotImage, wantImage)
	}
}

func TestAirSectorStampUsesFootprintBoundaryAndPriorLink(t *testing.T) {
	sys, _, u := takeoffFixture(t)
	h := u.Handle
	coll := handleRow(sys.Collisions, h)
	if coll.airSector == nil || coll.airOffMap {
		t.Fatal("in-map creation did not retain an ordinary sector")
	}
	prior := coll.airSector
	u.X = numeric.Fixed(-1)
	if coll.airSector != prior {
		t.Fatal("coordinate change replaced prior stamped sector")
	}
	coll.CachedAnchor = Cell{X: 8, Z: 8}
	sys.syncStampedAirSector(u, coll)
	if !coll.airOffMap || coll.airSector != &sys.AirSectors.sentinel {
		t.Fatal("fractional-negative position did not select the sentinel")
	}
	u.X, u.Z = world.CellToWorld(31), world.CellToWorld(8)
	coll.CachedAnchor = Cell{X: 31, Z: 8}
	sys.syncStampedAirSector(u, coll)
	if !coll.airOffMap || coll.airSector != &sys.AirSectors.sentinel {
		t.Fatal("right-edge equality did not select the off-map sentinel")
	}
	u.X, u.Z = world.CellToWorld(8), world.CellToWorld(31)
	coll.CachedAnchor = Cell{X: 8, Z: 31}
	sys.syncStampedAirSector(u, coll)
	if !coll.airOffMap || coll.airSector != &sys.AirSectors.sentinel {
		t.Fatal("bottom-edge equality did not select the off-map sentinel")
	}
	u.X, u.Z = world.CellToWorld(8), world.CellToWorld(8)
	coll.CachedAnchor = Cell{X: 8, Z: 8}
	sys.syncStampedAirSector(u, coll)
	if coll.airOffMap || coll.airSector == &sys.AirSectors.sentinel {
		t.Fatal("in-map restamp did not replace the fractional-negative sentinel")
	}
}

func TestAirSectorStampUsesActualAttributeBounds(t *testing.T) {
	terrain := &world.Terrain{CellW: 9, CellH: 9, Plot: make([]world.PlotCell, 81)}
	g := NewAirSectorGrid(terrain)
	if g == nil || g.Columns != 2 || g.Rows != 2 {
		t.Fatalf("grid = %#v, want a two-sector rounded grid", g)
	}
	last := world.CellToWorld(8)
	if got := g.sectorForStamp(last, last, Cell{X: 8, Z: 8}, 0, 0); got == &g.sentinel {
		t.Fatal("zero footprint acquired an invented sentinel rejection")
	}
	penultimate := world.CellToWorld(7)
	if got := g.sectorForStamp(penultimate, penultimate, Cell{X: 7, Z: 7}, 1, 1); got == &g.sentinel {
		t.Fatal("last in-bounds footprint anchor was rejected")
	}
	if got := g.sectorForStamp(last, last, Cell{X: 9, Z: 8}, 1, 1); got != &g.sentinel {
		t.Fatal("right equality beyond the actual attribute width did not select sentinel")
	}
}

func TestAuthoredZeroMoveRatesAreNotRedefaulted(t *testing.T) {
	sys, _, u := takeoffFixture(t)
	u.Def.MoveRate1, u.Def.MoveRate2 = 0, 0
	sys.emitMovementCallbacks(u, 1)
	if u.MoveTier != 3 {
		t.Fatalf("zero authored thresholds yielded tier %d, want 3", u.MoveTier)
	}
}

func TestFollowMarkerReadsGroundTargetStampedSector(t *testing.T) {
	sys, w, aircraft := airFixture(t)
	padDef := &content.UnitDef{
		UnitName: "stamped-pad", IsAirBase: true, FootprintX: 1, FootprintZ: 1, MaxDamage: 1,
	}
	ph, err := w.Create(padDef, 0, world.CellToWorld(18), 0, world.CellToWorld(8))
	if err != nil {
		t.Fatal(err)
	}
	pad := w.Unit(ph)
	sys.EnsureUnit(pad)
	want, linked := sys.airSectorHeight(pad)
	if !linked {
		t.Fatal("grounded pad did not retain its completed stamp sector")
	}
	m := sys.newFollowPieceMarker(aircraft, pad.Handle, airNoPiece)
	dst := Vec3{X: -1, Y: -1, Z: -1}
	m.UpdateGoal(aircraft, &dst)
	if dst != (Vec3{X: pad.X, Y: pad.Y, Z: pad.Z}) {
		t.Fatalf("follow goal = %+v, want grounded pad position %+v", dst, Vec3{X: pad.X, Y: pad.Y, Z: pad.Z})
	}
	if coll := handleRow(sys.Collisions, pad.Handle); coll == nil || coll.airSector == nil || coll.airSector.Smoothed != want {
		t.Fatal("follow marker did not use the pad's stamped sector link")
	}
}
