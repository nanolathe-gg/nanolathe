// WU-19-107: the two rally-point playtest reports, end to end on retail
// content. Both tests skip when ~/TotalAnnihilation is absent.
package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/path"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// wu19107Battle composes the seed-7 skirmish both tests use and steps it into
// battle. It returns the session, a one-tick step function and the local
// player's commander.
func wu19107Battle(t *testing.T) (*Session, func(), *units.Unit) {
	t.Helper()
	sess := aiE2ESkirmish(t, "ashap plateau", aiE2ESeed)
	driver := int32(1 << 20)
	step := func() { sess.Step(driver); driver++ }
	for i := 0; i < 60 && sess.State != StateBattle; i++ {
		step()
	}
	if sess.State != StateBattle {
		t.Fatal("session never entered battle")
	}
	for _, u := range sess.Units.IterSliced() {
		if u != nil && u.Alive && u.Def != nil && u.Def.Commander && u.Owner == sess.LocalOwner {
			return sess, step, u
		}
	}
	t.Fatal("no local commander")
	return nil, nil, nil
}

// wu19107HeadName reports the name of a unit's front-segment head record.
func wu19107HeadName(u *units.Unit) string {
	q := orders.QueueForUnit(u)
	if q == nil || q.LenPrimary() == 0 {
		return ""
	}
	return orders.DescriptorFor(q.Primary()[0].ID).Name
}

// TestAirFactoryRallyProductsLeaveThePadRetail is the regression for playtest
// report 4: "planes are not moving off the factory properly and are piling up,
// making it impossible to build more until manually moving them."
//
// The configuration that produces it is an aircraft plant carrying a rally
// point. `GetBuilt` resolves the plant's `QMove` marker AGAINST THE PRODUCT
// ([04 R-FAC-02 §4]), so a `canfly` product must receive `VTOL_Move`. When it
// received the ground `Move_Ground` instead, the record could never complete
// (an aircraft is never admitted to the ground path scheduler,
// [04 R-PATH-01 §9]) and the takeoff preamble never ran, so the product kept
// mover mode 1 and held the plant's ground exit cells — which is exactly the
// occupancy the next product's state-2 test and the yard-close admission gate
// wait on ([04 R-AIR-02] step 3, [04 R-FAC-02 §5][§6]). The plant then stopped
// after its first product until the aircraft were moved by hand.
//
// The assertion is the observable: every queued product is built, and every
// one of them leaves the plant's footprint for the rally.
func TestAirFactoryRallyProductsLeaveThePadRetail(t *testing.T) {
	sess, step, com := wu19107Battle(t)
	local := sess.LocalOwner

	plantDef, ok := sess.Catalog.Unit(content.CanonicalKey("armap"))
	if !ok {
		t.Skip("armap absent from the reference install")
	}
	productKey := content.CanonicalKey("armpeep")
	if _, ok := sess.Catalog.Unit(productKey); !ok {
		t.Skip("armpeep absent from the reference install")
	}
	plantHandle, err := sess.Units.Create(plantDef, uint8(local), com.X+world.CellToWorld(10), 0, com.Z)
	if err != nil {
		t.Fatalf("create aircraft plant: %v", err)
	}
	plant := sess.Units.Unit(plantHandle)
	plantCX, plantCZ := world.WorldToCell(plant.X), world.WorldToCell(plant.Z)

	// Fund the run so the build rate rather than the ledger bounds the test.
	fund := func() {
		sess.Econ.Players[local].Capacity[0] = 100000
		sess.Econ.Players[local].Capacity[1] = 100000
		sess.Econ.Players[local].Stock[0] = 100000
		sess.Econ.Players[local].Stock[1] = 100000
	}
	fund()

	const wanted = 6
	if err := sess.EnqueueHumanCommand(HumanCommand{
		Kind:         HumanFactoryBuild,
		FactoryBuild: HumanFactoryBuildCommand{Builder: plantHandle, Product: "armpeep", Count: wanted},
	}); err != nil {
		t.Fatalf("queue production: %v", err)
	}
	// The rally click: command code 2 on an immobile builder records `QMove`
	// [04 R-ORD-02 §1]. Twenty cells out, clear of the plant's own footprint.
	rallyX := plant.X + world.CellToWorld(20)
	if err := sess.EnqueueHumanCommand(HumanCommand{
		Kind: HumanOrder,
		Order: HumanOrderCommand{
			Handles:  []pool.Handle{plantHandle},
			Code:     2,
			Position: orders.ResolvePos{X: rallyX, Z: plant.Z},
		},
	}); err != nil {
		t.Fatalf("set rally: %v", err)
	}

	produced := map[pool.Handle]bool{}
	for i := 0; i < 12000; i++ {
		step()
		fund()
		for _, u := range sess.Units.IterSliced() {
			if u != nil && u.Alive && u.Def != nil && u.Owner == local &&
				u.Def.CanonicalKey == productKey && u.Remaining <= 0 {
				produced[u.Handle] = true
			}
		}
	}
	if len(produced) < wanted {
		t.Fatalf("the plant produced %d of %d queued aircraft; a rally-inherited ground move on a "+
			"canfly product jams the pad [04 R-FAC-02 §4][04 R-AIR-02]", len(produced), wanted)
	}
	// The plant's footprint plus a one-cell margin: nothing may still be
	// sitting on the pad it was built on.
	margin := plantDef.FootprintX
	if plantDef.FootprintZ > margin {
		margin = plantDef.FootprintZ
	}
	for h := range produced {
		u := sess.Units.Unit(h)
		if u == nil || !u.Alive {
			continue
		}
		dx := world.WorldToCell(u.X) - plantCX
		dz := world.WorldToCell(u.Z) - plantCZ
		if dx < 0 {
			dx = -dx
		}
		if dz < 0 {
			dz = -dz
		}
		if dx <= margin && dz <= margin {
			t.Fatalf("product %d is still on the plant at cell (%d,%d) with head %q; a rally product "+
				"must fly to the rally [04 R-FAC-02 §4]", h, world.WorldToCell(u.X), world.WorldToCell(u.Z),
				wu19107HeadName(u))
		}
	}
}

// TestGroundRallyProductArrivesAndRetiresRetail covers the arrival half of
// playtest report 3: units sent to one rally point must complete their record
// on arrival rather than re-arming against it forever.
//
// [04 R-ORD-01 §4]: `Move_Ground` phase 1 completes on satisfied `0x20` with
// status 6 `Arrived`; only an unsatisfied visit re-arms. The mover that
// reaches the goal cell must therefore retire its record and fall to its
// standing auto-op, and it must not keep an active move record afterwards.
//
// The FOLLOWERS of a shared rally point are WU-19-115's
// TestRallyMoversSettleRetail next door; this test asserts the arriving mover
// only. The marker that stood here is closed: `internal/movement`'s two
// goal-install sites passed [04 R-PATH-01 §8] step 5.3's gate as an
// unconditional `true`, so every 30-59-tick re-arm handed a blocked mover a
// fresh straight line at a goal it cannot occupy and it lurched once per
// re-arm forever. The gate now reads the retiring flag — [05 R-EGRESS-02]'s
// code-9 completion flag, `orders.FlagRetryMark` — and the blocked followers
// idle in place as [04 R-EGRESS-01] describes.
func TestGroundRallyProductArrivesAndRetiresRetail(t *testing.T) {
	sess, step, com := wu19107Battle(t)
	local := sess.LocalOwner

	def, ok := sess.Catalog.Unit(content.CanonicalKey("armflash"))
	if !ok {
		t.Skip("armflash absent from the reference install")
	}
	baseCX := world.WorldToCell(com.X) + 6
	baseCZ := world.WorldToCell(com.Z)
	var made []pool.Handle
	for i := int32(0); i < 5; i++ {
		h, err := sess.Units.Create(def, uint8(local), world.CellToWorld(baseCX+2*i), 0, world.CellToWorld(baseCZ))
		if err != nil {
			t.Fatalf("create product %d: %v", i, err)
		}
		sess.Movement.EnsureUnit(sess.Units.Unit(h))
		made = append(made, h)
	}
	var goal path.Cell
	found := false
	for r := int32(18); r < 40 && !found; r++ {
		c := path.Cell{X: baseCX, Z: baseCZ + r}
		if sess.Movement.IsGoalCellPassable(made[0], c) {
			goal, found = c, true
		}
	}
	if !found {
		t.Skip("no reachable rally cell on this start position")
	}
	if err := sess.EnqueueHumanCommand(HumanCommand{
		Kind: HumanOrder,
		Order: HumanOrderCommand{
			Handles:  made,
			Code:     2,
			Position: orders.ResolvePos{X: world.CellToWorld(goal.X), Z: world.CellToWorld(goal.Z)},
		},
	}); err != nil {
		t.Fatalf("issue rally move: %v", err)
	}

	arrived := pool.Handle(0)
	arrivedTick := 0
	for i := 0; i < 1500 && arrived == 0; i++ {
		step()
		for _, h := range made {
			u := sess.Units.Unit(h)
			if u == nil || !u.Alive {
				continue
			}
			if world.WorldToCell(u.X) == goal.X && world.WorldToCell(u.Z) == goal.Z &&
				wu19107HeadName(u) != "Move_Ground" {
				arrived, arrivedTick = h, i
			}
		}
	}
	if arrived == 0 {
		t.Fatal("no mover reached the rally cell and retired its Move_Ground record " +
			"[04 R-ORD-01 §4][04 R-MOV-03 §1]")
	}
	// Once retired, the record must stay retired: the arrival is a completion
	// (code 5), not a re-arm, so it must never come back on later ticks.
	for i := 0; i < 600; i++ {
		step()
		if name := wu19107HeadName(sess.Units.Unit(arrived)); name == "Move_Ground" {
			t.Fatalf("the arrived mover re-acquired a Move_Ground record %d ticks after completing "+
				"at tick %d; arrival completes the record [04 R-ORD-01 §4]", i, arrivedTick)
		}
	}
}
