package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"

	"github.com/nanolathe-gg/nanolathe/internal/features"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// TestResurrectionPoolRefusalRetainsCorpseForRetry runs phase 5 through the
// production queue. An exhausted owner slice must leave the same corpse in
// place, keep phase 5 parked for 300 ticks, and let the later retry consume it
// after one slot becomes free [05 R-WORK-01 §7].
func TestResurrectionPoolRefusalRetainsCorpseForRetry(t *testing.T) {
	s := wreckOrientationSession(t)
	const corpseX, corpseZ = 12, 12
	corpseDef := s.Catalog.Features["wreckvictim_dead"]
	if corpseDef == nil {
		t.Fatal("missing fixture corpse definition")
	}
	corpse := s.Features.PlaceAt(corpseX, corpseZ, corpseDef)
	if corpse == nil {
		t.Fatal("place corpse")
	}
	corpse.Orientation = features.Orientation{Bank: 0x1234, Heading: 0x5678, Pitch: 0x9abc}

	builderDef := s.Catalog.Units["wreckcon"]
	productDef := s.Catalog.Units["wreckvictim"]
	hBuilder, err := s.Units.Create(builderDef, 0, world.CellToWorld(corpseX-1), 0, world.CellToWorld(corpseZ))
	if err != nil {
		t.Fatalf("create resurrector: %v", err)
	}
	builder := s.Units.Unit(hBuilder)
	s.Movement.EnsureUnit(builder)
	builder.InBuildStance = true

	// The session's sliced pool gives each owner len(catalog.Units) slots. Fill
	// owner zero's remaining slots through the ordinary allocator, so phase 5's
	// failure is the actual pool-exhaustion path rather than a mock refusal.
	var fillers []pool.Handle
	for {
		h, createErr := s.Units.Create(builderDef, 0, 0, 0, 0)
		if createErr != nil {
			break
		}
		fillers = append(fillers, h)
	}
	if len(fillers) == 0 {
		t.Fatal("fixture did not exhaust the resurrector owner slice")
	}

	q := orders.QueueForUnit(builder)
	s.bindExistingOrderQueue(builder)
	resurrect := orders.Lookup("Resurrect")
	if resurrect == 0 {
		t.Fatal("missing Resurrect row")
	}
	q.Push(resurrect, orders.Node{
		Owner: hBuilder,
		Phase: 5,
		GoalX: world.CellToWorld(corpseX),
		GoalZ: world.CellToWorld(corpseZ),
	})
	n := q.Primary()[0]
	q.Pump(builder, 100)
	if n.Phase != 5 || n.Deadline != 400 {
		t.Fatalf("failed phase 5 = phase %d deadline %d, want 5/400 (queue=%d work=%v)", n.Phase, n.Deadline, len(q.Primary()), q.Binding() != nil && q.Binding().Work != nil)
	}
	cell := s.World.PlotAt(corpseX, corpseZ)
	if cell == nil || cell.Feature() == world.PlotFeatureNone {
		t.Fatal("pool refusal removed the corpse")
	}
	if got := s.Features.InstanceAt(corpseX, corpseZ); got != corpse {
		t.Fatalf("pool refusal changed corpse instance: got %p want %p", got, corpse)
	}

	// Retry only becomes eligible when the stored deadline expires. Releasing a
	// slot before then cannot consume the corpse early.
	s.Units.FreeImmediate(fillers[len(fillers)-1])
	q.Pump(builder, 399)
	if cell.Feature() == world.PlotFeatureNone {
		t.Fatal("corpse disappeared before the 300-tick retry deadline")
	}
	q.Pump(builder, 400)

	var product *units.Unit
	for _, u := range s.Units.Iter() {
		if u != nil && u.Def == productDef {
			product = u
			break
		}
	}
	if product == nil {
		t.Fatal("retry did not allocate the resurrected unit")
	}
	if product.X != corpse.X || product.Y != corpse.Y || product.Z != corpse.Z {
		t.Fatalf("product position (%d,%d,%d), want corpse position (%d,%d,%d)",
			product.X.Raw(), product.Y.Raw(), product.Z.Raw(), corpse.X.Raw(), corpse.Y.Raw(), corpse.Z.Raw())
	}
	if product.Move.Bank != corpse.Orientation.Bank || product.Move.Heading != corpse.Orientation.Heading || product.Move.Pitch != corpse.Orientation.Pitch {
		t.Fatalf("product orientation (%#x,%#x,%#x), want corpse orientation", product.Move.Bank, product.Move.Heading, product.Move.Pitch)
	}
	if product.Remaining != 0 || product.Health != 1 {
		t.Fatalf("product state remaining=%v health=%d, want 0/1", product.Remaining, product.Health)
	}
	if cell.Feature() != world.PlotFeatureNone {
		t.Fatalf("successful retry retained corpse feature %#x", cell.Feature())
	}
}

// Successful resurrection must release its feature record before a later
// same-tick stamp, without waiting for the feature phase [05 R-WORK-01 §7]
// [05 R-FEAT-01 §4].
func TestResurrectionImmediatelyReturnsFeatureCapacity(t *testing.T) {
	attrs := make([]formats.TNTAttribute, 64*64)
	for i := range attrs {
		attrs[i] = formats.TNTAttribute{Height: 10, Feature: world.PlotFeatureNone}
	}
	terrain := minimalTerrain()
	terrain.CellW, terrain.CellH = 64, 64
	terrain.Plot = world.ExpandPlot(attrs, 64, 64)
	s := wreckOrientationSession(t, terrain)
	def := s.Catalog.Features["wreckvictim_dead"]
	for i := 0; i < features.FeatureAnimSlots; i++ {
		if s.Features.PlaceAt(i%64, i/64, def) == nil {
			t.Fatalf("fill arena at %d", i)
		}
	}
	h, err := s.Units.Create(s.Catalog.Units["wreckcon"], 0, world.CellToWorld(60), 0, world.CellToWorld(60))
	if err != nil {
		t.Fatal(err)
	}
	builder := s.Units.Unit(h)
	builder.InBuildStance = true
	q := orders.QueueForUnit(builder)
	s.bindExistingOrderQueue(builder)
	q.Push(orders.Lookup("Resurrect"), orders.Node{Owner: h, Phase: 5, GoalX: world.CellToWorld(12), GoalZ: world.CellToWorld(12)})
	q.Pump(builder, 100)
	if !terrain.PlotAt(12, 12).IsEmpty() {
		t.Fatal("resurrection did not consume corpse")
	}
	// A later death or ignition can allocate before TickMotion runs.
	if s.Features.PlaceAt(50, 50, def) == nil {
		t.Fatal("resurrection retained feature capacity until a later phase")
	}
	if s.Features.InstanceAt(12, 12) != nil {
		t.Fatal("removed corpse retained its runtime record")
	}
}
