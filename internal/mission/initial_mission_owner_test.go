package mission

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/movement"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/world"
)

// TestMissionRecordsCarryTheirOwningUnit locks the field [04 §3.2] lists as
// part of every order record and [04 R-ORD-01 §1] hands to every handler body:
// the owning unit. [04 §3.6] says the pump consumes a mission-script queue
// "exactly as if a player had issued the orders", so a record this interpreter
// queues has to name its unit the way an interface- or AI-issued one does.
//
// Two units, one `p` verb each. The two Patrol records must name the two
// different units, and the movement controller's single goal slot
// [04 R-ORD-01 §9] must therefore be a different slot for each: installing for
// the second unit's record leaves the first unit's binding intact, so a later
// detach of the first record still finds its own payload and raises `0x80`.
//
// Before WU-19-69 every record this interpreter built left the owner at the
// null handle, so both records addressed the single map entry at handle 0: the
// second install evicted the first record's payload, and the detach below
// found nothing to release.
func TestMissionRecordsCarryTheirOwningUnit(t *testing.T) {
	w := newMissionFixtureWorld(5, nil)
	hA, err := w.Create(testDef("ARMCOM"), 0, world.CellToWorld(2), 0, world.CellToWorld(2))
	if err != nil {
		t.Fatalf("create A: %v", err)
	}
	hB, err := w.Create(testDef("ARMCK"), 0, world.CellToWorld(4), 0, world.CellToWorld(4))
	if err != nil {
		t.Fatalf("create B: %v", err)
	}
	uA, uB := atPlacement(w.Unit(hA), 0), atPlacement(w.Unit(hB), 1)

	m := &Mission{Type: TypeCampaign, Units: []UnitPlacement{
		{UnitName: "ARMCOM", Ident: "alpha", InitialMission: "p 100,100,5"},
		{UnitName: "ARMCK", Ident: "beta", InitialMission: "p 200,200,5"},
	}}
	RunInitialMissionsWithCatalog(m, w, testInitialCatalog)

	nA := findNode(uA, "Patrol")
	nB := findNode(uB, "Patrol")
	if nA == nil || nB == nil {
		t.Fatalf("p did not queue a Patrol on both units: A=%v B=%v", primaryNodes(uA), primaryNodes(uB))
	}
	if nA.Owner != hA {
		t.Fatalf("first record's owner = %v, want the acting unit %v [04 §3.2]", nA.Owner, hA)
	}
	if nB.Owner != hB {
		t.Fatalf("second record's owner = %v, want the acting unit %v [04 §3.2]", nB.Owner, hB)
	}
	if nA.Owner == nB.Owner {
		t.Fatalf("both mission records name owner %v; two units' records must not share a controller slot [04 R-ORD-01 §9]", nA.Owner)
	}

	// Every other record the interpreter queued — the tail `MakeSelectable` is
	// suppressed after `p`, so this covers whatever else a script queues — must
	// carry the same unit, never the null handle.
	for _, u := range []*struct {
		unit  string
		nodes []*orders.Node
	}{{"A", primaryNodes(uA)}, {"B", primaryNodes(uB)}} {
		for i, n := range u.nodes {
			if n == nil {
				continue
			}
			if n.Owner == 0 {
				t.Fatalf("unit %s record %d (%s) carries the null owner", u.unit, i, orders.DescriptorFor(n.ID).Name)
			}
		}
	}

	// Independent goal buckets: the controller slot is addressed by the owner
	// handle, so B's install must not displace A's payload [04 R-ORD-01 §9].
	sys := movement.NewSystem(missionOwnerTerrain(), movement.Profile{
		FootPrintX: 1, FootPrintZ: 1, MaxWaterDepth: 12, MinWaterDepth: -10000, MaxSlope: 50,
	}, movement.NewOccupancyGrid())
	sys.BindWorld(w)

	if !sys.InstallPointGoal(orders.PointGoalRequest{Owner: nA.Owner, Node: nA, X: world.CellToWorld(6), Z: world.CellToWorld(6)}) {
		t.Fatal("install for the first unit's record was refused")
	}
	if !sys.InstallPointGoal(orders.PointGoalRequest{Owner: nB.Owner, Node: nB, X: world.CellToWorld(9), Z: world.CellToWorld(9)}) {
		t.Fatal("install for the second unit's record was refused")
	}
	if nA.Satisfied&0x80 != 0 {
		t.Fatalf("first record's pending word = %#x after the OTHER unit installed; the rebind bit must not cross units [04 R-ORD-01 §9]", nA.Satisfied)
	}
	// A still owns its own slot, so its own detach is the one that raises the bit.
	if !sys.ReleaseGoal(nA) {
		t.Fatal("release of the first unit's record was refused")
	}
	if nA.Satisfied&0x80 == 0 {
		t.Fatalf("first record's pending word = %#x after its own detach, want `0x80`: its payload was evicted by the other unit's install", nA.Satisfied)
	}
	if nB.Satisfied&0x80 != 0 {
		t.Fatalf("second record's pending word = %#x after the first unit detached; the two buckets are not independent", nB.Satisfied)
	}
}

// missionOwnerTerrain is a flat authored plot, big enough for the two goals
// above. Nothing in this test depends on its heights.
func missionOwnerTerrain() *world.Terrain {
	tr := &world.Terrain{CellW: 20, CellH: 20, SeaLevel: 0, Plot: make([]world.PlotCell, 400)}
	for i := range tr.Plot {
		tr.Plot[i].SetFeature(world.PlotFeatureNone)
		tr.Plot[i].SetHeight(10)
		tr.Plot[i].SetMinHeight(10)
		tr.Plot[i].SetMaxHeight(10)
	}
	return tr
}
