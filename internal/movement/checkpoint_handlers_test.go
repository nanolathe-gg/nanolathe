package movement

import (
	"bytes"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/checkpoint"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func movementHandlerOrdersBytes(t *testing.T, c *orders.CheckpointContext, q *orders.Queue) []byte {
	t.Helper()
	if _, err := c.Queues.Add(q); err != nil {
		t.Fatal(err)
	}
	p := &orders.Pump{}
	for {
		n, err := p.CollectCheckpointReferences(c)
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			break
		}
	}
	return movementCheckpointWrite(t, func(e *checkpoint.Encoder) { _ = p.WriteCheckpoint(e, c) })
}

func movementHandlerOrdersRefused(t *testing.T, c *orders.CheckpointContext, q *orders.Queue) {
	t.Helper()
	if _, err := c.Queues.Add(q); err != nil {
		t.Fatal(err)
	}
	if _, err := (&orders.Pump{}).CollectCheckpointReferences(c); err == nil {
		t.Fatal("orders collection admitted unsupported handler")
	}
	var out bytes.Buffer
	if err := (&orders.Pump{}).WriteCheckpoint(checkpoint.NewEncoder(&out), c); err == nil {
		t.Fatal("orders writer admitted unsupported handler")
	}
}

func movementHandlerRefused(t *testing.T, s *System, c *CheckpointContext) {
	t.Helper()
	if _, err := s.CollectCheckpointReferences(c); err == nil {
		t.Fatal("movement collection admitted unsupported handler")
	}
	var out bytes.Buffer
	if err := s.WriteCheckpoint(checkpoint.NewEncoder(&out), c); err == nil || out.Len() != 0 {
		t.Fatal("movement writer did not refuse before bytes")
	}
}

func TestMovementCheckpointHandlersLazyPresenceVectorAndPurity(t *testing.T) {
	s, c, a := movementPathBindingFixture(t, nil)
	if s.airLegHandler != nil || s.checkpointAirLegHandler.Handler() != nil {
		t.Fatal("constructor initialized lazy handler")
	}
	before := movementPathBindingBytes(t, s, c)
	for range 2 {
		if err := s.RegisterCheckpointOrderHandlers(c.Orders, a); err != nil {
			t.Fatal(err)
		}
		if after := movementPathBindingBytes(t, s, c); !bytes.Equal(after, before) {
			t.Fatal("source registration or capture changed bytes")
		}
	}
	s.RegisterOrderHandlers(nil)
	if s.airLegHandler != nil || s.checkpointAirLegHandler.Handler() != nil {
		t.Fatal("capture or nil queue registration created handler")
	}
	q := &orders.Queue{}
	s.RegisterOrderHandlers(q)
	if s.airLegHandler == nil || s.checkpointAirLegHandler.Handler() == nil {
		t.Fatal("lazy creation omitted copied proof")
	}
	// Authored prefix through airLegHandler: the constructed null-handle rows
	// each contain one nil; all ten air-base lists are empty (§16.3.14/.64).
	prefix := movementCheckpointVector(t,
		uint8(0), uint8(0), uint32(1), uint8(0), [143]uint8{}, uint8(0), [16]uint8{}, uint32(1), uint8(0),
		uint8(0), uint8(1), int64(1), int32(1), uint16(6), uint32(0), uint8(0), uint32(1), uint8(0),
		uint8(0), uint8(1), uint32(1), uint8(0), uint8(0), uint32(1), uint8(0), [10]uint32{}, uint8(1))
	after := movementPathBindingBytes(t, s, c)
	if !bytes.HasPrefix(after, prefix) {
		t.Fatalf("movement handler prefix\ngot  %x\nwant %x", after[:len(prefix)], prefix)
	}
	want := bytes.Clone(before)
	want[len(prefix)-1] = 1
	if !bytes.Equal(after, want) {
		t.Fatal("lazy handler changed bytes beyond its existing presence field")
	}
	// An empty queue's final fields are handler framing, patrol pause, primary/secondary
	// counts, then the empty node table. Row 64 is VTOL_Standby [04 §3.1].
	queueTail := []byte{1, 1, 0, 0, 0, 64, 3, 0, 0, 0, 0, 0, 0, 0, 0, 0, 3, 0, 0, 0, 0, 0}
	// Movement capture reads its own proof and cannot silently register the
	// producer into the lower orders context.
	unregistered := movementCheckpointContext()
	if err := unregistered.SetPathBindings(s, nil, a); err != nil {
		t.Fatal(err)
	}
	movementPathBindingBytes(t, s, unregistered)
	movementHandlerOrdersRefused(t, unregistered.Orders, q)
	if err := s.RegisterCheckpointOrderHandlers(unregistered.Orders, a); err != nil {
		t.Fatal(err)
	}
	movementHandlerOrdersBytes(t, unregistered.Orders, q)
	if got := movementHandlerOrdersBytes(t, c.Orders, q); !bytes.HasSuffix(got, queueTail) {
		t.Fatalf("queue handler tail %x", got)
	}
	proof := s.checkpointOrderHandlers
	for range 3 {
		s.RegisterOrderHandlers(q)
		if err := s.RegisterCheckpointOrderHandlers(c.Orders, a); err != nil {
			t.Fatal(err)
		}
		if got := movementPathBindingBytes(t, s, c); !bytes.Equal(got, after) || s.checkpointOrderHandlers != proof {
			t.Fatal("repeat registration or capture changed retained state")
		}
	}
	if allocs := testing.AllocsPerRun(100, func() { s.RegisterOrderHandlers(q) }); allocs != 0 {
		t.Fatalf("repeat registration allocated %g times", allocs)
	}
}

func TestMovementCheckpointHandlersLateQueuesAndInheritance(t *testing.T) {
	a := checkpoint.NewBindingAuthority()
	s := NewSystemWithCheckpointBindings(nil, Profile{}, nil, a)
	c := movementCheckpointContext().Orders
	if err := s.RegisterCheckpointOrderHandlers(c, a); err != nil {
		t.Fatal(err)
	}
	prior := &orders.Queue{}
	s.RegisterOrderHandlers(prior)
	baseline := movementHandlerOrdersBytes(t, c, prior)
	u := &units.Unit{Orders: prior}
	inherited := &orders.Queue{}
	orders.BindQueue(u, inherited)
	other := movementCheckpointContext().Orders
	if err := s.RegisterCheckpointOrderHandlers(other, a); err != nil {
		t.Fatal(err)
	}
	if got := movementHandlerOrdersBytes(t, other, inherited); !bytes.Equal(got, baseline) {
		t.Fatal("late inherited queue lost copied proof")
	}
	prior.SetOwnedHandler(airStandbyRowID, prior.OwnedHandlerFor(airStandbyRowID))
	movementHandlerOrdersRefused(t, c, prior)
	if got := movementHandlerOrdersBytes(t, other, inherited); !bytes.Equal(got, baseline) {
		t.Fatal("original row replacement invalidated inherited copy")
	}
	// The original producer's repeated visit restores its same immutable value.
	s.RegisterOrderHandlers(prior)
	if got := movementHandlerOrdersBytes(t, c, prior); !bytes.Equal(got, baseline) {
		t.Fatal("producer reinstallation changed queue")
	}
	late := &orders.Queue{}
	s.RegisterOrderHandlers(late)
	fresh := movementCheckpointContext().Orders
	if err := s.RegisterCheckpointOrderHandlers(fresh, a); err != nil {
		t.Fatal(err)
	}
	if got := movementHandlerOrdersBytes(t, fresh, late); !bytes.Equal(got, baseline) {
		t.Fatal("late queue got different handler")
	}
}

func TestMovementCheckpointHandlerRegistrationRefusals(t *testing.T) {
	a, foreign := checkpoint.NewBindingAuthority(), checkpoint.NewBindingAuthority()
	s := NewSystemWithCheckpointBindings(nil, Profile{}, nil, a)
	copyBefore := *s
	q := &orders.Queue{}
	s.RegisterOrderHandlers(q)
	copyAfter := *s
	for _, invalid := range []*System{nil, {}, NewSystem(nil, Profile{}, nil), NewSystemWithCheckpointBindings(nil, Profile{}, nil, nil), &copyBefore, &copyAfter} {
		if invalid.RegisterCheckpointOrderHandlers(movementCheckpointContext().Orders, a) == nil {
			t.Fatal("ordinary, nil or copied constructor admitted")
		}
	}
	for _, token := range []*checkpoint.BindingAuthority{nil, foreign} {
		c := movementCheckpointContext().Orders
		if s.RegisterCheckpointOrderHandlers(c, token) == nil {
			t.Fatal("missing or foreign authority admitted")
		}
		if err := s.RegisterCheckpointOrderHandlers(c, a); err != nil {
			t.Fatal("failed registration changed context", err)
		}
	}
	if s.RegisterCheckpointOrderHandlers(nil, a) == nil {
		t.Fatal("nil context admitted")
	}
	c := movementCheckpointContext().Orders
	if err := s.RegisterCheckpointOrderHandlers(c, a); err != nil {
		t.Fatal(err)
	}
	other := NewSystemWithCheckpointBindings(nil, Profile{}, nil, a)
	if other.RegisterCheckpointOrderHandlers(c, a) == nil {
		t.Fatal("different same-authority owner replaced source")
	}
	movementHandlerOrdersBytes(t, c, q)
	foreignContext := movementCheckpointContext().Orders
	if err := foreignContext.SetBindings(&orders.QueueBinding{}, nil, nil, foreign); err != nil {
		t.Fatal(err)
	}
	if s.RegisterCheckpointOrderHandlers(foreignContext, a) == nil {
		t.Fatal("different binding authority admitted")
	}
	// Copies execute through the ordinary row path; their inherited closure
	// cannot be attested as a closure capturing the copied system.
	copyQueue := &orders.Queue{}
	copyAfter.RegisterOrderHandlers(copyQueue)
	movementHandlerOrdersRefused(t, movementCheckpointContext().Orders, copyQueue)
}

func TestMovementCheckpointHandlersNoLateBlessingOrCompositionRelaxation(t *testing.T) {
	s, c, a := movementPathBindingFixture(t, nil)
	s.airLegHandler = func(*units.Unit, *orders.Node, uint32, uint32) (orders.Code, bool) {
		panic("capture or installation invoked cached handler")
	}
	q := &orders.Queue{}
	s.RegisterOrderHandlers(q)
	if s.checkpointAirLegHandler.Handler() != nil || s.RegisterCheckpointOrderHandlers(c.Orders, a) == nil {
		t.Fatal("ordinary cached handler acquired proof")
	}
	movementHandlerOrdersRefused(t, c.Orders, q)
	movementHandlerRefused(t, s, c)
	for _, mutate := range []func(*System){
		func(s *System) { s.checkpointOrderHandlers = nil },
		func(s *System) { s.checkpointAirLegHandler = orders.CheckpointOwnedHandler{} },
		func(s *System) { s.airLegHandler = nil },
		func(s *System) { s.checkpointOrderHandlers.source = nil },
		func(s *System) { s.checkpointOrderHandlers.authority = nil },
	} {
		s, c, a := movementPathBindingFixture(t, nil)
		s.RegisterOrderHandlers(&orders.Queue{})
		mutate(s)
		if s.RegisterCheckpointOrderHandlers(c.Orders, a) == nil {
			t.Fatal("malformed producer metadata registered")
		}
		movementHandlerRefused(t, s, c)
	}
	// An admitted standby never blesses another, still-unfinished binding.
	s, c, a = movementPathBindingFixture(t, nil)
	s.RegisterOrderHandlers(&orders.Queue{})
	if err := s.RegisterCheckpointOrderHandlers(c.Orders, a); err != nil {
		t.Fatal(err)
	}
	s.Classes = map[string]*content.MovementClass{}
	movementHandlerRefused(t, s, c)
	// Plain no-handler fixtures retain their existing absent representation.
	movementCheckpointBytes(t, &System{})
}

func TestMovementCheckpointHandlerStandbyDispatchUnchanged(t *testing.T) {
	for _, admitted := range []bool{false, true} {
		original, w, u := airFixture(t)
		s := original
		if admitted {
			s = NewSystemWithCheckpointBindings(original.Terrain, original.Fallback, NewOccupancyGrid(), checkpoint.NewBindingAuthority())
			s.BindWorld(w)
			s.EnsureUnit(u)
		}
		q := orders.QueueOfUnit(u)
		n := pushAirOrder(t, u, "VTOL_Standby", 0, 0)
		u.X, u.Z = 73<<16|0x9000, 91<<16|0x4000
		s.RegisterOrderHandlers(q)
		before := q.Binding().SimRNG.Draws()
		q.Pump(u, 100)
		// The existing phase-0 result advances once, stores integer post and
		// arms tick+1 without drawing; the deadline setter adds gate bit 1
		// [04 §3.3][04 R-AIR-01 §7][04 R-ORD-01 §0].
		if n.Phase != 1 || n.DynamicGate != 0x10001 || n.Deadline != 101 || n.GuardX != 73 || n.GuardY != 91 || q.Binding().SimRNG.Draws() != before {
			t.Fatalf("admitted=%t phase=%d gate=%x deadline=%d post=(%d,%d)", admitted, n.Phase, n.DynamicGate, n.Deadline, n.GuardX, n.GuardY)
		}
		q.Pump(u, 100)
		if n.Phase != 1 || n.Deadline != 101 || q.Binding().SimRNG.Draws() != before {
			t.Fatal("blocked standby changed before deadline")
		}
	}
}
