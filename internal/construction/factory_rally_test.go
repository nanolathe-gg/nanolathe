package construction

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
)

// TestFactoryMoveOrderInstallsRallyAndKeepsProduction locks the whole rally
// path a player walks: a factory with a product queued, given an ordinary
// (non-queued) move order, must keep producing and must hand the recorded
// destination to each finished product.
//
// Three contracts meet here and each was broken independently (playtest
// PT5: "factories with a move order do not build"):
//
//   - Command code 2 requires `canmove` and then asks for a live mover; with
//     none it resolves `QMove`, before any target test [04 R-ORD-02 §1]. Stock
//     factories author `CanMove=1` on a `BMcode=0` definition
//     ([03 R-RND-02A] asset census), so they take exactly this arm — the
//     resolver used to hand them `Move_Ground`.
//   - A non-queued issue purges every front-segment record whose static gate
//     lacks bit 2 [04 §3.3][04 R-MOV-03 §6]. `BuildingBuild` carries bit 2
//     (`0x10010c`) and survives; `QMove` (`0x400`) does not, so a second rally
//     click replaces the first.
//   - `GetBuilt` copies each `QMove`/`QPatrol` off the builder's primary queue
//     onto the product with the record's goal triple, in walk order, and falls
//     back to `Park` only when there is none [04 §3.8][04 R-FAC-02 §4].
func TestFactoryMoveOrderInstallsRallyAndKeepsProduction(t *testing.T) {
	qMoveID := orders.Lookup("QMove")
	moveID := orders.Lookup("Move_Ground")
	buildID := orders.Lookup("BuildingBuild")
	parkID := orders.Lookup("Park")
	if qMoveID == 0 || moveID == 0 || buildID == 0 || parkID == 0 {
		t.Skip("descriptor table incomplete")
	}

	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	facDef := newFactoryDef("armvp", 3, 3, 300)
	// Stock factories are can-move building-class definitions; that pairing is
	// what makes the rally arm reachable at all [03 R-RND-02A].
	facDef.CanMove = true
	cat.Units[content.CanonicalKey("armvp")] = facDef
	prodDef := newProductDef("armflash", 2, 2, 100, 100)
	prodDef.BMCode = true // a vehicle product: bmcode 1 and a ground class
	prodDef.MovementClass = "tank2"
	cat.Units[content.CanonicalKey("armflash")] = prodDef
	cat.Movement = map[string]*content.MovementClass{
		content.CanonicalKey("tank2"): {DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("tank2")}, FootprintX: 2, FootprintZ: 2},
	}

	w := newConstructionFixtureWorld(20, cat)
	fh, err := w.Create(facDef, 0, world0(4), 0, world0(4))
	if err != nil {
		t.Fatal(err)
	}
	factory := w.Unit(fh)
	if factory.Flags&units.BuildingClassStatus == 0 {
		t.Fatal("a bmcode-zero factory must carry the building-class status bit")
	}

	if err := QueueFactoryBuild(factory, "armflash", 1, cat); err != nil {
		t.Fatalf("queue factory build: %v", err)
	}

	// The order the player gives: command code 2 at a ground point, no shift.
	rallyX, rallyZ := numeric.Fixed(90<<16), numeric.Fixed(37<<16)
	id := orders.Resolve(2, factory, nil, &orders.ResolvePos{X: rallyX, Z: rallyZ})
	if id != qMoveID {
		t.Fatalf("code 2 on an immobile builder resolved %q, want QMove [04 R-ORD-02 §1]", orders.DescriptorFor(id).Name)
	}

	q := orders.QueueForUnit(factory)
	q.PurgeUnprotected() // the non-queued (Replace) modifier [04 §3.3]
	q.DropLeadingAutoOps()
	q.Push(id, orders.NewNodeForOrder(id, 0, rallyX, 0, rallyZ, 1, factory.Handle, false))

	prim := q.Primary()
	if len(prim) != 2 || prim[0].ID != buildID || prim[1].ID != qMoveID {
		t.Fatalf("factory queue=%v, want [BuildingBuild QMove] — the rally must not purge production", queueNames(prim))
	}
	if prim[0].Flags&orders.FlagPurgeSurvivor == 0 {
		t.Fatal("BuildingBuild carries static gate bit 2 and must be marked a purge survivor")
	}
	if prim[1].Flags&orders.FlagPurgeSurvivor != 0 {
		t.Fatal("QMove does not carry static gate bit 2 and must not survive the next Replace")
	}

	// A second rally click replaces the first marker and still leaves
	// production standing.
	secondX, secondZ := numeric.Fixed(120<<16), numeric.Fixed(48<<16)
	q.PurgeUnprotected()
	q.DropLeadingAutoOps()
	q.Push(qMoveID, orders.NewNodeForOrder(qMoveID, 0, secondX, 0, secondZ, 2, factory.Handle, false))
	prim = q.Primary()
	if len(prim) != 2 || prim[0].ID != buildID || prim[1].ID != qMoveID || prim[1].GoalX != secondX {
		t.Fatalf("factory queue after a second rally=%v, want production plus the newer marker", queueNames(prim))
	}

	// Completion hands the marker to the product as a real move.
	svc := NewService(nil, cat, w, &economy.Service{})
	ph, err := w.Create(prodDef, 0, world0(8), 0, world0(8))
	if err != nil {
		t.Fatal(err)
	}
	product := w.Unit(ph)
	svc.rallyInheritance(factory, product, 0)

	pprim := orders.QueueForUnit(product).Primary()
	if len(pprim) != 1 || pprim[0].ID != moveID {
		t.Fatalf("product queue=%v, want one inherited Move_Ground [04 §3.8]", queueNames(pprim))
	}
	if pprim[0].GoalX != secondX || pprim[0].GoalZ != secondZ {
		t.Fatalf("inherited goal=(%d,%d), want the rally marker's (%d,%d)",
			pprim[0].GoalX.Raw(), pprim[0].GoalZ.Raw(), secondX.Raw(), secondZ.Raw())
	}
}

// world0 converts whole world units to 16.16 for the fixture's placements.
func world0(units int32) numeric.Fixed { return numeric.Fixed(units) << 16 }

func queueNames(nodes []*orders.Node) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		if n == nil {
			out = append(out, "<nil>")
			continue
		}
		out = append(out, orders.DescriptorFor(n.ID).Name)
	}
	return out
}
