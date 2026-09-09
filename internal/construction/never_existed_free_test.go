// The third abnormal end of factory production. [04 R-FAC-02 §3] names three:
// cancel-current kills the product with damage cause 9, a dying factory kills
// everything on its cargo list, and "a product freed by pool exhaustion or
// limit never existed". The last one is a free, not a death — no kill record,
// no death cause, no counters, the slot back in the pool — and these tests lock
// the two rollback sites that had been filing it as a kill.
package construction

import (
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/movement"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// TestReservePlacementRollbackFreesWithoutDeath drives the allocation site
// directly: the exit rectangle is already stamped by another identity, so
// reservePlacement refuses after the record exists. The record must leave no
// trace — [04 R-FAC-02 §3] "never existed" — which by [05 R-SHARE-01 §8] step 5
// and [08 R-SKIR-01 §3] "Counters" includes both per-player counters, since a
// refusal inside retail's allocator returns before either is incremented.
func TestReservePlacementRollbackFreesWithoutDeath(t *testing.T) {
	facDef := newFactoryDef("nefac", 4, 4, 3000)
	prodDef := exitMobileDef("neprod", 1, 1)
	cat := exitCatalog(facDef, prodDef)
	cat.Movement["exitmove"].MinWaterDepth = -10000
	terrain := exitTerrain(24, 24)
	svc, w := exitService(t, terrain, cat)

	fh, err := w.Create(facDef, 0, world.CellToWorld(10), 0, world.CellToWorld(10))
	if err != nil {
		t.Fatalf("create factory: %v", err)
	}
	factory := w.Unit(fh)

	liveBefore := w.LiveCountForPlayer(0)
	createdBefore := w.CreatedCountForPlayer(0)

	// A foreign ground-word occupant inside the product's rectangle is exactly
	// what reservePlacement refuses [04 R-COLL-01 §2].
	rect := exitRect(6, 6, 1)
	terrain.PlotAt(6, 6).SetOccupantA(int16(fh))

	pos := world.NewModelWorldPosition(world.CellToWorld(6), numeric.Fixed(0), world.CellToWorld(6))
	prod, err := svc.allocateNanoframe(factory, prodDef, rect, pos)
	if err == nil {
		t.Fatalf("allocation succeeded onto an occupied cell; product=%d", prod.Handle)
	}
	if prod != nil {
		t.Fatalf("refused allocation returned a product %d", prod.Handle)
	}

	if got := w.LiveCountForPlayer(0); got != liveBefore {
		t.Fatalf("live count = %d after a never-existed free, want the pre-allocation %d [08 R-SKIR-01 §3]", got, liveBefore)
	}
	if got := w.CreatedCountForPlayer(0); got != createdBefore {
		t.Fatalf("units-ever-created = %d after a never-existed free, want the pre-allocation %d: retail's allocator refuses before step 5 counts [05 R-SHARE-01 §8]", got, createdBefore)
	}
	for _, u := range w.Iter() {
		if u == nil || u.Handle == fh {
			continue
		}
		t.Fatalf("unit %d survives the rollback (dying=%v cause=%d)", u.Handle, u.Dying, u.DeathCause)
	}

	// The slot is back in the pool: the next allocation on the same rectangle,
	// now clear, succeeds and the counters move exactly once [P0-16 §6.3].
	terrain.PlotAt(6, 6).SetOccupantA(0)
	prod, err = svc.allocateNanoframe(factory, prodDef, rect, pos)
	if err != nil {
		t.Fatalf("second allocation refused: %v", err)
	}
	if prod == nil || prod.Handle == 0 {
		t.Fatal("second allocation returned no product")
	}
	if got := w.CreatedCountForPlayer(0); got != createdBefore+1 {
		t.Fatalf("units-ever-created = %d after one real allocation, want %d", got, createdBefore+1)
	}
	if got := w.LiveCountForPlayer(0); got != liveBefore+1 {
		t.Fatalf("live count = %d after one real allocation, want %d", got, liveBefore+1)
	}
}

// TestSuccessEpilogueRollbackFreesWithoutDeath takes the other site. The
// epilogue's own contract calls its failure "an explicit rejected allocation"
// [04 R-FAC-02 §1]; a rejected allocation is the never-existed product of
// [04 R-FAC-02 §3]. The attachment gate is made to refuse by carrying the
// factory itself, which the gate list rejects outright.
func TestSuccessEpilogueRollbackFreesWithoutDeath(t *testing.T) {
	facDef := newFactoryDef("neeplab", 4, 4, 3000)
	facDef.YardMap = "yccy yccy yccy yccy"
	prodDef := exitMobileDef("neepprod", 1, 1)
	prodDef.BuildTime = 1
	prodDef.BuildCostEnergy = 1
	prodDef.BuildCostMetal = 1
	cat := exitCatalog(facDef, prodDef)
	cat.Movement["exitmove"].MinWaterDepth = -10000
	svc, w := exitService(t, exitTerrain(24, 24), cat)
	svc.Movement = &movement.System{Grid: movement.NewOccupancyGrid()}
	stockEverything(svc.Economy)

	fh, err := w.Create(facDef, 0, world.CellToWorld(10), 0, world.CellToWorld(10))
	if err != nil {
		t.Fatalf("create factory: %v", err)
	}
	factory := w.Unit(fh)
	bindConstructionFixture(factory, trivialModel(1, [][3]int64{{0, 0, 0}}), true)
	if err := svc.RegisterBuildingPlacement(factory); err != nil {
		t.Fatalf("RegisterBuildingPlacement: %v", err)
	}
	if !svc.YardOpenTransaction(factory, true) {
		t.Fatal("factory yard refused to open")
	}
	// The attachment gate refuses a carrier that is itself carried
	// [04 R-FAC-02 §1], which is the only reachable failure of successEpilogue.
	factory.Attachment.Carrier = fh + 1

	q := orders.QueueForUnit(factory)
	q.Push(orders.Lookup("BuildingBuild"), orders.Node{BuildDefKey: prodDef.CanonicalKey, Param1: prodIdx(cat, prodDef.CanonicalKey), Param2: 1, Phase: uint8(State2), Deadline: -1})
	node := q.Primary()[0]

	liveBefore := w.LiveCountForPlayer(0)
	createdBefore := w.CreatedCountForPlayer(0)

	svc.handleState2(factory, node, 1)

	// Prove the visit actually allocated and then rolled back rather than
	// stopping at an earlier gate: only the epilogue's rejection reaches
	// rejectPermanent from this handler carrying the attachment reason.
	rolledBack := false
	for _, d := range svc.AdmissionDiagnostics() {
		if d.Status == AdmissionRejectedPermanentDefinition && strings.Contains(d.Reason, "attachment gates rejected allocation") {
			rolledBack = true
		}
	}
	if !rolledBack {
		t.Fatalf("no epilogue rejection recorded; the visit never reached the rollback: %+v", svc.AdmissionDiagnostics())
	}

	if got := w.LiveCountForPlayer(0); got != liveBefore {
		t.Fatalf("live count = %d after the epilogue rollback, want %d [08 R-SKIR-01 §3]", got, liveBefore)
	}
	if got := w.CreatedCountForPlayer(0); got != createdBefore {
		t.Fatalf("units-ever-created = %d after the epilogue rollback, want %d [05 R-SHARE-01 §8]", got, createdBefore)
	}
	for _, u := range w.Iter() {
		if u == nil || u.Handle == fh {
			continue
		}
		t.Fatalf("product %d survives the rollback (dying=%v cause=%d damageCause=%d)", u.Handle, u.Dying, u.DeathCause, u.LastDamageCause)
	}
	if len(factory.Attachment.Cargo) != 0 {
		t.Fatalf("factory keeps %d cargo entries after the rollback", len(factory.Attachment.Cargo))
	}
	if node.Target != 0 {
		t.Fatalf("the order node keeps a product link %d after the rollback", node.Target)
	}
}

// TestNeverExistedFreeLeavesNoDeathCause is the negative half of the cause-9
// contract locked by TestCancelCurrentStampsCauseNine: the cancel path stamps a
// damage kind, the never-existed free must stamp nothing at all [06 §12.1].
func TestNeverExistedFreeLeavesNoDeathCause(t *testing.T) {
	facDef := newFactoryDef("nedcfac", 4, 4, 3000)
	prodDef := exitMobileDef("nedcprod", 1, 1)
	cat := exitCatalog(facDef, prodDef)
	terrain := exitTerrain(24, 24)
	svc, w := exitService(t, terrain, cat)

	fh, err := w.Create(facDef, 3, world.CellToWorld(10), 0, world.CellToWorld(10))
	if err != nil {
		t.Fatalf("create factory: %v", err)
	}
	factory := w.Unit(fh)

	// Allocate once so the record exists, then free it through the same helper
	// the two rollbacks use.
	rect := exitRect(6, 6, 1)
	pos := world.NewModelWorldPosition(world.CellToWorld(6), numeric.Fixed(0), world.CellToWorld(6))
	prod, err := svc.allocateNanoframe(factory, prodDef, rect, pos)
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}
	handle := prod.Handle

	svc.freeNeverExistedProduct(prod)

	if prod.Dying {
		t.Fatal("the freed product carries a death latch [04 R-FAC-02 §3]")
	}
	if prod.DeathCause != 0 {
		t.Fatalf("the freed product carries death cause %d, want none", prod.DeathCause)
	}
	if prod.LastDamageCause != 0 {
		t.Fatalf("the freed product carries damage kind %d, want none [06 §12.1]", prod.LastDamageCause)
	}
	if w.Unit(handle) != nil {
		t.Fatalf("slot %d still holds a record after the free", handle)
	}
	if _, ok := svc.PlacementForProduct(handle); ok {
		t.Fatalf("slot %d keeps its placement record after the free [04 R-COLL-01 §4]", handle)
	}
	if terrain.PlotAt(6, 6).OccupantA() != 0 {
		t.Fatalf("cell 6,6 keeps the freed identity's ground word")
	}
}

func stockEverything(e *economy.Service) {
	for i := range e.Players {
		e.Players[i].Stock[0] = 1e9
		e.Players[i].Stock[1] = 1e9
		e.Players[i].Capacity[0] = 2e9
		e.Players[i].Capacity[1] = 2e9
	}
}
