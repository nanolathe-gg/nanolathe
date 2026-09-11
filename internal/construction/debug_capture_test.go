package construction

import (
	"errors"
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// The history bound is diagnostic policy. Crossing it must preserve the silent
// indefinite 15-tick retry contract [04 R-FAC-02 §6][05 "Factory production lifecycle"].
func TestAdmissionCaptureAcrossBlockedRetries(t *testing.T) {
	newBlocked := func() (*Service, *units.Unit, *orders.Node) {
		terrain := &world.Terrain{CellW: 10, CellH: 10, Plot: make([]world.PlotCell, 100)}
		for i := range terrain.Plot {
			terrain.Plot[i].SetFeature(world.PlotFeatureNone)
		}
		terrain.Plot[4*10+4].SetOccupantA(9)
		facDef := newFactoryDef("armfac", 2, 2, 300)
		prodDef := newProductDef("armflash", 2, 2, 100, 100)
		prodDef.YardMap = "oooo"
		cat := &content.Catalog{Units: map[string]*content.UnitDef{"armfac": facDef, "armflash": prodDef}}
		w := newTestWorld(20)
		h, err := w.Create(facDef, 0, world.CellToWorld(5), 0, world.CellToWorld(5))
		if err != nil {
			t.Fatal(err)
		}
		factory := w.Unit(h)
		bindConstructionFixture(factory, trivialModel(1, nil), true)
		q := orders.QueueForUnit(factory)
		q.Push(orders.Lookup("BuildingBuild"), orders.Node{BuildDefKey: "armflash", Param2: 1, Phase: uint8(State2), Deadline: -1})
		return NewService(terrain, cat, w, &economy.Service{}), factory, q.Primary()[0]
	}
	observed, factory, node := newBlocked()
	control, controlFactory, controlNode := newBlocked()
	identity := uint64(55)
	observed.DebugBuilderIdentity = func(u *units.Unit) uint64 {
		if u != factory {
			t.Fatal("identity query used another occupant")
		}
		return identity
	}
	const attempts = admissionHistoryCapacity*3 + 7
	for i := 0; i < attempts; i++ {
		tick := uint32(10 + 15*i)
		observed.Pump(factory, tick)
		control.Pump(controlFactory, tick)
		if node.Deadline != int32(tick+15) || node.DynamicGate != WakeBit1|WakeBit2 || node.Phase != uint8(State2) || node.Target != 0 {
			t.Fatalf("retry changed at %d: %+v", tick, node)
		}
		before := *node
		first := observed.DebugAdmissionSnapshot()
		if !reflect.DeepEqual(first, observed.DebugAdmissionSnapshot()) || !reflect.DeepEqual(before, *node) {
			t.Fatal("capture changed history or the order")
		}
		// Mutating caller-owned records must not alter retained diagnostics or
		// the next retry. There are no nested slices or live pointers in entries.
		first.Recent[0].Footprint.MinX = -99
		first.Recent[0].Reason = "caller mutation"
		first.Recent[0].BuilderIdentity = 999
		if !reflect.DeepEqual(*node, *controlNode) || !reflect.DeepEqual(observed.Terrain.Plot, control.Terrain.Plot) {
			t.Fatal("observed and unobserved construction diverged")
		}
	}
	if len(observed.Messages()) != 0 || len(control.Messages()) != 0 {
		t.Fatal("blocked retry emitted a message")
	}
	identity = 99 // A later identity must never relabel an earlier attempt.
	snapshot := observed.DebugAdmissionSnapshot()
	if snapshot.Total != attempts || snapshot.Dropped != attempts-uint64(len(snapshot.Recent)) || len(snapshot.Recent) != snapshot.Capacity || cap(observed.admissions) != snapshot.Capacity {
		t.Fatalf("history is not bounded with accurate totals: %+v", snapshot)
	}
	wantRect := AdmissionFootprint{Known: true, MinX: 4, MinZ: 4, MaxX: 6, MaxZ: 6}
	for i, d := range snapshot.Recent {
		wantTick := uint32(10 + 15*(attempts-snapshot.Capacity+i))
		if d.Tick != wantTick || d.Builder != factory.Handle || d.BuilderIdentity != 55 || d.Product != "armflash" || d.Status != AdmissionBlockedTransiently || d.Reason == "" || d.Reason == "caller mutation" || d.Footprint != wantRect {
			t.Fatalf("record %d lost attempt data or chronological order: %+v", i, d)
		}
	}
	trace := observed.AdmissionDiagnostics()
	if !reflect.DeepEqual(trace, snapshot.Recent) {
		t.Fatal("legacy diagnostics are not in visit order")
	}
	trace[0].Tick = 0
	if observed.AdmissionDiagnostics()[0].Tick == 0 {
		t.Fatal("legacy diagnostics alias retained storage")
	}
	// An eviction also cannot prevent admission on the first due clear visit.
	observed.Terrain.Plot[4*10+4].SetOccupantA(0)
	control.Terrain.Plot[4*10+4].SetOccupantA(0)
	tick := uint32(10 + 15*attempts)
	observed.Pump(factory, tick)
	control.Pump(controlFactory, tick)
	if node.Phase != uint8(State3) || !reflect.DeepEqual(*node, *controlNode) {
		t.Fatalf("clear exit diverged after history eviction: observed=%+v control=%+v admissions=%+v", node, controlNode, observed.DebugAdmissionSnapshot().Recent[snapshot.Capacity-1])
	}
	last := observed.DebugAdmissionSnapshot().Recent[snapshot.Capacity-1]
	if last.Status != AdmissionAdmitted || last.BuilderIdentity != 99 || last.Footprint != wantRect {
		t.Fatalf("admitted placement lost attempt data: %+v", last)
	}
}

func TestPermanentAdmissionSharesBoundAndPreservesDedupe(t *testing.T) {
	svc := &Service{}
	factory := &units.Unit{Handle: 7}
	node := &orders.Node{BuildDefKey: "BROKEN"}
	err := errors.New("missing movement profile")
	for i := 0; i < admissionHistoryCapacity*2; i++ {
		node.BuildDefKey += "x" // Distinct malformed inputs cannot bypass the bound.
		svc.rejectPermanent(factory, node, uint32(i), err)
		svc.rejectPermanent(factory, node, uint32(i+1), err)
	}
	d := svc.DebugAdmissionSnapshot()
	if d.Total != admissionHistoryCapacity*2 || d.Dropped != admissionHistoryCapacity || len(d.Recent) != d.Capacity || cap(svc.admissions) != d.Capacity {
		t.Fatalf("permanent outcomes bypassed bound/dedupe: %+v", d)
	}
	for i, entry := range d.Recent {
		if entry.Tick != uint32(admissionHistoryCapacity+i) || entry.Footprint.Known || entry.BuilderIdentity != 0 {
			t.Fatalf("permanent outcome invented placement/identity or changed order: %+v", entry)
		}
	}
}
