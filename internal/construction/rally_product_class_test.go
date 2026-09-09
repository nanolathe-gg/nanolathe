package construction

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// TestRallyInheritanceResolvesAgainstTheProduct is WU-19-107's regression for
// playtest report 4 ("planes are not moving off the factory properly and are
// piling up, making it impossible to build more until manually moving them").
//
// [04 R-FAC-02 §4]: `GetBuilt` resolves a `QMove` rally record "as command 2
// (move) and one named `QPatrol` as command 9 (patrol) AGAINST THE PRODUCT
// with the record's goal triple". The descriptor is therefore the product's
// own resolution — `VTOL_Move`/`VTOL_Patrol` for a `canfly` product and
// `Move_Ground`/`Patrol` for a ground one — and not a constant.
//
// The walk used to hard-code the ground pair. An aircraft product then carried
// a ground move record that can never complete (an aircraft is never admitted
// to the ground path scheduler, [04 R-PATH-01 §9]) and never ran the takeoff
// preamble, so its mover mode stayed 1 and its stamp stayed on the ground
// plane — the very cells the next product's state-2 test and the yard-close
// admission gate wait on ([04 R-AIR-02] step 3, [04 R-FAC-02 §5][§6]).
func TestRallyInheritanceResolvesAgainstTheProduct(t *testing.T) {
	qMoveID := orders.Lookup("QMove")
	qPatrolID := orders.Lookup("QPatrol")
	vtolMoveID := orders.Lookup("VTOL_Move")
	vtolPatrolID := orders.Lookup("VTOL_Patrol")
	moveID := orders.Lookup("Move_Ground")
	patrolID := orders.Lookup("Patrol")
	if qMoveID == 0 || qPatrolID == 0 || vtolMoveID == 0 || vtolPatrolID == 0 || moveID == 0 || patrolID == 0 {
		t.Skip("descriptor table incomplete")
	}

	rallyX, rallyZ := numeric.Fixed(90<<16), numeric.Fixed(37<<16)

	// One fixture, two products of the same factory: the only difference is
	// the product definition's `canfly` bit.
	run := func(t *testing.T, canFly bool, rallyID orders.ID) orders.ID {
		t.Helper()
		cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
		facDef := newFactoryDef("armvp", 3, 3, 300)
		facDef.CanMove = true
		cat.Units[content.CanonicalKey("armvp")] = facDef
		prodDef := newProductDef("armflash", 2, 2, 100, 100)
		prodDef.BMCode = 1
		prodDef.CanMove = true
		prodDef.CanPatrol = true
		prodDef.CanFly = canFly
		prodDef.MovementClass = "tank2"
		cat.Units[content.CanonicalKey("armflash")] = prodDef
		cat.Movement = map[string]*content.MovementClass{
			content.CanonicalKey("tank2"): {
				DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("tank2")},
				FootprintX:       2, FootprintZ: 2,
			},
		}

		w := newConstructionFixtureWorld(20, cat)
		fh, err := w.Create(facDef, 0, world0(4), 0, world0(4))
		if err != nil {
			t.Fatal(err)
		}
		factory := w.Unit(fh)
		q := orders.QueueForUnit(factory)
		q.Push(rallyID, orders.NewNodeForOrder(rallyID, 0, rallyX, 0, rallyZ, 1, factory.Handle, false))

		svc := NewService(nil, cat, w, &economy.Service{})
		ph, err := w.Create(prodDef, 0, world0(8), 0, world0(8))
		if err != nil {
			t.Fatal(err)
		}
		product := w.Unit(ph)
		svc.rallyInheritance(factory, product, 0)

		prim := orders.QueueForUnit(product).Primary()
		if len(prim) != 1 {
			t.Fatalf("product queue=%v, want exactly one inherited rally record [04 R-FAC-02 §4]", queueNames(prim))
		}
		if prim[0].GoalX != rallyX || prim[0].GoalZ != rallyZ {
			t.Fatalf("inherited goal=(%d,%d), want the rally marker's (%d,%d)",
				prim[0].GoalX.Raw(), prim[0].GoalZ.Raw(), rallyX.Raw(), rallyZ.Raw())
		}
		return prim[0].ID
	}

	if got := run(t, true, qMoveID); got != vtolMoveID {
		t.Fatalf("canfly product inherited %q from a QMove rally, want VTOL_Move [04 R-FAC-02 §4]",
			orders.DescriptorFor(got).Name)
	}
	if got := run(t, true, qPatrolID); got != vtolPatrolID {
		t.Fatalf("canfly product inherited %q from a QPatrol rally, want VTOL_Patrol [04 R-FAC-02 §4]",
			orders.DescriptorFor(got).Name)
	}
	if got := run(t, false, qMoveID); got != moveID {
		t.Fatalf("ground product inherited %q from a QMove rally, want Move_Ground [04 R-FAC-02 §4]",
			orders.DescriptorFor(got).Name)
	}
	if got := run(t, false, qPatrolID); got != patrolID {
		t.Fatalf("ground product inherited %q from a QPatrol rally, want Patrol [04 R-FAC-02 §4]",
			orders.DescriptorFor(got).Name)
	}
}
