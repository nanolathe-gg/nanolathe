package movement

import (
	"encoding/binary"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// moveRateProbeProgram is an authored probe program, not retail bytes. Every
// move-rate callback name maps to a body that sleeps and then returns, so a
// callback that is started leaves one live thread behind and the VM's active
// thread count reports it; `Create` maps to a bare return, so spawning the unit
// leaves nothing running. `setSFXoccupy` is deliberately absent: whether its
// band cache is restored is an open question (see seedRestoredMoveTier), and an
// absent script name starts nothing, which keeps this probe about the
// move-rate edge alone.
func moveRateProbeProgram() *cob.Program {
	return &cob.Program{
		Code: []uint32{
			0x10021001, 1000, // push constant: sleep duration in milliseconds
			0x10013000, // sleep
			0x10065000, // return
			0x10065000, // return — the Create body
		},
		Pieces: []string{"base"},
		// The thread starter admits an entry only when it appears in the id
		// table, so both bodies must be listed [04 §4.3].
		ScriptsByID: []int{0, 4},
		Scripts: map[string]int{
			"Create":      4,
			"StartMoving": 0,
			"StopMoving":  0,
			"MoveRate1":   0,
			"MoveRate2":   0,
			"MoveRate3":   0,
		},
	}
}

// TestRestoredMoverEmitsNoMoveRateEdgeOnFirstTick locks the load seam of the
// move-rate classifier. The tier is persisted state — retail keeps the
// classifier's verdict in the packed unit word the save writes and reads back
// ([08 R-SAVE-02 §6], move-rate tier at word bits 6-7) — and [04 §5.2] emits
// only on a change of category. A unit restored in category 1 that is still in
// category 1 on the first movement tick after the load must therefore wake no
// callback at all.
//
// The regression this locks: the emitter's cache lives on the movement side, so
// before the restore seeded it, every restored mover with a nonzero tier read a
// zero cache once and woke StartMoving plus MoveRateN. Stock scripts act on
// StartMoving, so the loaded battle diverged from the unloaded one.
func TestRestoredMoverEmitsNoMoveRateEdgeOnFirstTick(t *testing.T) {
	cell := int32(worldUnitsPerCell)
	sys := NewSystem(syntheticTerrainForIntegrate(), Profile{
		FootPrintX: 1, FootPrintZ: 1, MinWaterDepth: -10000, MaxSlope: 255,
	}, NewOccupancyGrid())
	w := newMovementFixtureWorld(4)
	def := &content.UnitDef{
		UnitName: "restore-tier", FootprintX: 1, FootprintZ: 1, BMCode: 1,
		MaxVelocity: cell / 4, Acceleration: cell / 16, BrakeRate: cell / 16, TurnRate: 65535,
		// One wide band, so every speed this fixture can reach classifies as
		// category 1 and the tick under test cannot change category [04 §5.2].
		MoveRate1: cell, MoveRate2: 2 * cell,
		Script: moveRateProbeProgram(),
	}
	x, z := world.CellToWorld(5), world.CellToWorld(5)
	h, err := w.Create(def, 0, x, sys.Terrain.HeightAt(x, z), z)
	if err != nil {
		t.Fatalf("create mover: %v", err)
	}
	sys.BindWorld(w)
	u := w.Unit(h)
	vm := u.GetScript()
	if vm == nil {
		t.Fatal("probe unit has no script VM")
	}
	if vm.ActiveThreadCount() != 0 {
		t.Fatalf("spawn left %d live threads; the probe's Create must return at once", vm.ActiveThreadCount())
	}

	// The saved unit record: mover mode 1 in flags bits 0-1 (packed word bits
	// 4-5) and move-rate tier 1 in flags bits 2-3 (packed word bits 6-7)
	// [08 R-SAVE-02 §6].
	base := make([]byte, 0xB8)
	binary.LittleEndian.PutUint32(base[0xB4:], 1<<4|1<<6)
	if err := units.RetailUnitBase(u, base); err != nil {
		t.Fatalf("restore unit record: %v", err)
	}
	if u.MoveTier != 1 {
		t.Fatalf("restored move tier = %d, want 1 [08 R-SAVE-02 §6]", u.MoveTier)
	}

	// The saved mover record: a scalar speed inside category 1, mover mode 1,
	// blocked bit clear [08 R-SAVE-02 §8].
	mover := make([]byte, 35)
	binary.LittleEndian.PutUint32(mover[0x18:], uint32(cell/16))
	mover[34] = 1
	if err := sys.RestoreMover(h, mover); err != nil {
		t.Fatalf("restore mover: %v", err)
	}
	if got := handleRow(sys.prevMoveTier, h); got != 1 {
		t.Fatalf("move-rate cache after restore = %d, want the restored tier 1 [08 R-SAVE-02 §6][04 §5.2]", got)
	}

	// A mover that is still under way on the tick after the load: the restore
	// drops the derived route, so republish one and re-activate the head, the
	// way the order layer does once the restored queue is bound.
	queue := orders.QueueForUnit(u)
	queue.Push(orders.Lookup("Move_Ground"), orders.Node{GoalX: world.CellToWorld(8), GoalZ: z, GoalSupplied: true})
	head := queue.Head()
	handleRow(sys.Routes, h).Publish([]Point{{X: 5 * 16, Z: 5 * 16}, {X: 8 * 16, Z: 5 * 16}})
	setHandleRow(&sys.activeOrders, h, &activeMove{order: head, token: 41})
	sys.nextActivation = 41

	sys.BeginTick(1)
	sys.StepUnit(h, 1)
	sys.EndTick(1)

	if u.MoveTier != 1 {
		t.Fatalf("category changed to %d on the first post-load tick; the fixture must hold category 1 "+
			"for this test to say anything about the load seam [04 §5.2]", u.MoveTier)
	}
	if vm.ActiveThreadCount() != 0 {
		t.Fatalf("%d move-rate callbacks woke on the first tick after a load, want 0: the restored "+
			"category did not change [04 §5.2][08 R-SAVE-02 §6]", vm.ActiveThreadCount())
	}
}
