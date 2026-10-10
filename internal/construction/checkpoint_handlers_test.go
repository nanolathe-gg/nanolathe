package construction

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/checkpoint"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func constructionHandlerContext(t *testing.T, q *orders.Queue) *orders.CheckpointContext {
	t.Helper()
	c := orders.NewCheckpointContext(units.NewCheckpointContext(nil))
	if q != nil {
		if _, err := c.Queues.Add(q); err != nil {
			t.Fatal(err)
		}
	}
	return c
}

func constructionHandlerBytes(t *testing.T, c *orders.CheckpointContext) []byte {
	t.Helper()
	p := &orders.Pump{}
	if _, err := p.CollectCheckpointReferences(c); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := p.WriteCheckpoint(checkpoint.NewEncoder(&out), c); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func constructionHandlerRefused(t *testing.T, c *orders.CheckpointContext) {
	t.Helper()
	p := &orders.Pump{}
	if _, err := p.CollectCheckpointReferences(c); err == nil {
		t.Fatal("unproved handler passed collection")
	}
	var out bytes.Buffer
	if err := p.WriteCheckpoint(checkpoint.NewEncoder(&out), c); err == nil {
		t.Fatal("unproved handler passed writing")
	}
}

func TestCheckpointConstructionHandlerLazySourceAndVector(t *testing.T) {
	a := checkpoint.NewBindingAuthority()
	s := NewServiceWithCheckpointBinding(nil, nil, nil, nil, a)
	c := constructionHandlerContext(t, nil)
	for range 2 {
		if err := s.RegisterCheckpointOrderHandlers(c, a); err != nil {
			t.Fatal(err)
		}
	}
	if s.rowsResolved || s.boundConstructionWake != nil || s.boundGetBuilt != nil || s.checkpointHandlers.wake.Handler() != nil || s.checkpointHandlers.built.Handler() != nil {
		t.Fatal("constructor or capture registration initialized lazy handlers")
	}
	u := &units.Unit{}
	q := s.queueForUnit(u)
	if q == nil || u.Orders != q || !s.rowsResolved {
		t.Fatal("ordinary first queue creation changed")
	}
	if _, err := c.Queues.Add(q); err != nil {
		t.Fatal(err)
	}
	got := constructionHandlerBytes(t, c)
	baseline := constructionHandlerBytes(t, constructionHandlerContext(t, &orders.Queue{}))
	// The queue tail holds handler framing, patrol pause, then two empty
	// segments; table 3 follows with no nodes. Fix descriptor IDs independently.
	want := append([]byte(nil), baseline[:len(baseline)-16]...)
	want = append(want, 1, 6, 0, 0, 0, 12, 1, 19, 2, 25, 1, 33, 1, 54, 1, 59, 1)
	want = append(want, baseline[len(baseline)-15:]...)
	if !bytes.Equal(got, want) {
		t.Fatalf("handler payload\ngot %x\nwant %x", got, want)
	}
	for range 2 {
		if !bytes.Equal(got, constructionHandlerBytes(t, c)) {
			t.Fatal("capture changed retained queue bytes")
		}
	}
	if n := testing.AllocsPerRun(50, func() {
		s.registerGetBuilt(q)
		s.RegisterOrderHandlers(q)
		if err := s.RegisterCheckpointOrderHandlers(c, a); err != nil {
			t.Fatal(err)
		}
	}); n != 0 {
		t.Fatalf("repeat registration rebuilt caches: %g allocations", n)
	}
}

func TestCheckpointConstructionHandlerInheritance(t *testing.T) {
	a := checkpoint.NewBindingAuthority()
	s := NewServiceWithCheckpointBinding(nil, nil, nil, nil, a)
	u := &units.Unit{}
	prior := s.queueForUnit(u)
	capture := func(q *orders.Queue) []byte {
		c := constructionHandlerContext(t, q)
		if err := s.RegisterCheckpointOrderHandlers(c, a); err != nil {
			t.Fatal(err)
		}
		return constructionHandlerBytes(t, c)
	}
	want := capture(prior)
	replacement := &orders.Queue{}
	orders.BindQueue(u, replacement)
	if !bytes.Equal(want, capture(replacement)) {
		t.Fatal("late queue replacement lost copied proof")
	}
	row := orders.Lookup(MobileBuildOrder)
	prior.SetOwnedHandler(row, prior.OwnedHandlerFor(row))
	c := constructionHandlerContext(t, prior)
	if err := s.RegisterCheckpointOrderHandlers(c, a); err != nil {
		t.Fatal(err)
	}
	constructionHandlerRefused(t, c)
	if !bytes.Equal(want, capture(replacement)) {
		t.Fatal("ordinary source-row replacement changed inherited proof")
	}
	// The existing producer installation can transfer its original copied value
	// again; the getter/reinstall above cannot mint equivalent provenance.
	s.RegisterOrderHandlers(prior)
	if !bytes.Equal(want, capture(prior)) {
		t.Fatal("repeated producer registration lost its copied value")
	}
	late := s.queueForUnit(&units.Unit{})
	if !bytes.Equal(want, capture(late)) {
		t.Fatal("late-created unit queue differs from first registration")
	}
}

func TestCheckpointConstructionHandlerCaptureDoesNotDispatch(t *testing.T) {
	a := checkpoint.NewBindingAuthority()
	s := NewServiceWithCheckpointBinding(nil, nil, nil, nil, a)
	s.ensureRegistrationRows()
	// Authored cached values make any accidental dispatch during registration
	// or capture observable; their source is the real constructor's source.
	panicHandler := func(*units.Unit, *orders.Node, uint32, uint32) (orders.Code, bool) {
		panic("capture dispatched construction")
	}
	s.boundConstructionWake, s.boundGetBuilt = panicHandler, panicHandler
	s.checkpointHandlers.wake = orders.NewCheckpointOwnedHandler(panicHandler, orders.CheckpointConstructionWake, s.checkpointHandlers.source)
	s.checkpointHandlers.built = orders.NewCheckpointOwnedHandler(panicHandler, orders.CheckpointGetBuilt, s.checkpointHandlers.source)
	c := constructionHandlerContext(t, s.queueForUnit(&units.Unit{}))
	if err := s.RegisterCheckpointOrderHandlers(c, a); err != nil {
		t.Fatal(err)
	}
	constructionHandlerBytes(t, c)
}

func TestCheckpointConstructionHandlerOwnerAndConflicts(t *testing.T) {
	a := checkpoint.NewBindingAuthority()
	for _, s := range []*Service{nil, {}, NewService(nil, nil, nil, nil), NewServiceWithCheckpointBinding(nil, nil, nil, nil, nil)} {
		if err := s.RegisterCheckpointOrderHandlers(constructionHandlerContext(t, nil), a); err == nil {
			t.Fatal("ordinary or absent owner acquired proof")
		}
		if s != nil {
			q := s.queueForUnit(&units.Unit{})
			constructionHandlerRefused(t, constructionHandlerContext(t, q))
			if s.checkpointHandlers.source != nil || s.checkpointHandlers.wake.Handler() != nil || s.checkpointHandlers.built.Handler() != nil {
				t.Fatal("ordinary cached handlers acquired proof")
			}
		}
	}
	for _, initialized := range []bool{false, true} {
		s := NewServiceWithCheckpointBinding(nil, nil, nil, nil, a)
		if initialized {
			s.ensureRegistrationRows()
		}
		copyService := *s
		if err := copyService.RegisterCheckpointOrderHandlers(constructionHandlerContext(t, nil), a); err == nil {
			t.Fatal("copied service reused original source")
		}
		q := copyService.queueForUnit(&units.Unit{})
		c := constructionHandlerContext(t, q)
		if err := s.RegisterCheckpointOrderHandlers(c, a); err != nil {
			t.Fatal(err)
		}
		constructionHandlerRefused(t, c)
	}
	s := NewServiceWithCheckpointBinding(nil, nil, nil, nil, a)
	for _, authority := range []*checkpoint.BindingAuthority{nil, checkpoint.NewBindingAuthority()} {
		if err := s.RegisterCheckpointOrderHandlers(constructionHandlerContext(t, nil), authority); err == nil {
			t.Fatal("changed authority accepted")
		}
	}
	if err := s.RegisterCheckpointOrderHandlers(nil, a); err == nil {
		t.Fatal("nil context accepted")
	}
	// A conflict in kind 2 must not leave kind 1 newly installed.
	other := NewServiceWithCheckpointBinding(nil, nil, nil, nil, a)
	c := constructionHandlerContext(t, nil)
	if err := orders.RegisterCheckpointHandlerSource(c, orders.CheckpointGetBuilt, other.checkpointHandlers.source, other, a); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterCheckpointOrderHandlers(c, a); err == nil {
		t.Fatal("conflicting second source accepted")
	}
	if err := other.RegisterCheckpointOrderHandlers(c, a); err != nil {
		t.Fatal("failed registration partially changed context", err)
	}
	if s.rowsResolved || other.rowsResolved {
		t.Fatal("source validation initialized dispatch rows")
	}
}

func TestCheckpointConstructionHandlerRegistrationIndependentOfBindings(t *testing.T) {
	a := checkpoint.NewBindingAuthority()
	terrain, cat, w, econ := &world.Terrain{}, &content.Catalog{}, &units.World{}, &economy.Service{}
	s := NewServiceWithCheckpointBinding(terrain, cat, w, econ, a)
	s.Allocator = func(uint8, *content.UnitDef, numeric.Fixed, numeric.Fixed, numeric.Fixed) (*units.Unit, error) {
		panic("registration called allocator")
	}
	s.SetModelForUnit(func(*units.Unit) *model.Model { panic("registration called model lookup") })
	c := constructionHandlerContext(t, nil)
	if err := s.RegisterCheckpointOrderHandlers(c, a); err != nil {
		t.Fatal("handler registration depended on unrelated binding admission", err)
	}
	if s.Terrain != terrain || s.Catalog != cat || s.World != w || s.Economy != econ || s.rowsResolved {
		t.Fatal("registration changed construction inputs")
	}
	// Handler metadata remains excluded from the construction payload. Its
	// lower queue payload owns the admitted rows; other boundaries still refuse.
	cc, _ := constructionCheckpointFixture(t)
	if _, err := s.CollectCheckpointReferences(cc); err == nil {
		t.Fatal("handler admission relaxed unrelated construction bindings")
	}
	plain := NewService(nil, nil, nil, nil)
	admitted := NewServiceWithCheckpointBinding(nil, nil, nil, nil, a)
	admitted.ensureRegistrationRows()
	if !bytes.Equal(constructionCheckpointBytes(t, plain, cc), constructionCheckpointBytes(t, admitted, cc)) {
		t.Fatal("handler metadata entered construction wire state")
	}
}

// Exercise the real pump's GetBuilt gate/deadline arms and the reclaim
// forwarding arm. The diagnostic installation must preserve their dispatch
// decisions and simulation-stream consumption [04 §3.3][04 R-FAC-02 §4].
func TestCheckpointConstructionHandlerDispatchUnchanged(t *testing.T) {
	type result struct {
		node    orders.Node
		pending uint32
		sim     rng.Simulation
	}
	for _, row := range []orders.ID{orders.Lookup(GetBuiltOrder), orders.Lookup("ReclaimUnit")} {
		var baseline []result
		for _, admitted := range []bool{false, true} {
			var authority *checkpoint.BindingAuthority
			if admitted {
				authority = checkpoint.NewBindingAuthority()
			}
			s := NewServiceWithCheckpointBinding(nil, nil, nil, nil, authority)
			u := &units.Unit{Remaining: 1}
			q := s.queueForUnit(u)
			sim := rng.NewSimulation(31)
			q.SetBinding(&orders.QueueBinding{SimRNG: &sim})
			q.Push(row, orders.Node{Deadline: -1})
			var got []result
			for _, tick := range []uint32{10, 309, 310, 340} {
				if row == orders.Lookup("ReclaimUnit") {
					u.Pending = InterruptStop
					q.Head().DynamicGate = InterruptStop
				}
				q.Pump(u, tick)
				if q.Head() == nil {
					t.Fatal("owned continuing row was removed")
				}
				got = append(got, result{*q.Head(), u.Pending, sim})
			}
			if !admitted {
				baseline = got
			} else if !reflect.DeepEqual(got, baseline) {
				t.Fatal("admitted handler changed dispatch or RNG")
			}
			if row == orders.Lookup(GetBuiltOrder) {
				if got[0].node.Phase != 1 || got[0].node.Deadline != 310 || got[1].node.Deadline != 310 || got[2].node.Phase != 2 || got[2].node.Deadline != 340 || got[3].node.Deadline != 351 {
					t.Fatal("GetBuilt authored deadline sequence changed")
				}
			} else if got[0].node.Satisfied != InterruptStop || got[0].node.DynamicGate != InterruptStop {
				t.Fatal("reclaim delivery no longer retained for its work window")
			}
		}
	}
}
