package orders

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/sim/checkpoint"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

type checkpointHandlerOwner struct{ values []int }
type checkpointOtherHandlerOwner struct{ Values []int }

func checkpointHandlerContext(t *testing.T) (*CheckpointContext, *CheckpointHandlerSource, *checkpointHandlerOwner, *checkpoint.BindingAuthority) {
	t.Helper()
	c := orderCheckpointContext(t)
	owner := &checkpointHandlerOwner{[]int{1}}
	a := checkpoint.NewBindingAuthority()
	source := NewCheckpointHandlerSource(owner, a)
	for _, kind := range []uint8{CheckpointConstructionWake, CheckpointGetBuilt, CheckpointAirStandby} {
		if err := RegisterCheckpointHandlerSource(c, kind, source, owner, a); err != nil {
			t.Fatal(err)
		}
	}
	return c, source, owner, a
}

func checkpointHandlerBytes(t *testing.T, q *Queue, c *CheckpointContext) []byte {
	t.Helper()
	var out bytes.Buffer
	e := checkpoint.NewEncoder(&out)
	q.writeCheckpointHandlers(e, c, "queue.ownedHandlers")
	if err := e.Err(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func checkpointHandlerRefusal(t *testing.T, q *Queue, c *CheckpointContext, path string) {
	t.Helper()
	if _, field, err := q.validateCheckpointHandlers(c); err == nil || !strings.Contains(field, path) {
		t.Fatalf("validation field %s, error %v; want %s", field, err, path)
	}
	var out bytes.Buffer
	e := checkpoint.NewEncoder(&out)
	q.writeCheckpointHandlers(e, c, "queue.ownedHandlers")
	err := e.Err()
	if err == nil || out.Len() != 0 {
		t.Fatalf("writer error %v, bytes %d; expected refusal before bytes", err, out.Len())
	}
	e.U8(99)
	if e.Err() != err || out.Len() != 0 {
		t.Fatal("handler refusal was not sticky")
	}
}

func checkpointPanicHandler(*units.Unit, *Node, uint32, uint32) (Code, bool) {
	panic("capture called owned handler")
}

func TestCheckpointHandlersLiteralVectorAndPosition(t *testing.T) {
	c, source, _, _ := checkpointHandlerContext(t)
	// Numeric identities follow the established sorted descriptor table
	// [04 §3.1]. The vector independently fixes row bytes and schema order.
	rows := []struct {
		id   ID
		name string
		kind uint8
	}{
		{12, "BuildingBuild", 1}, {19, "GetBuilt", 2}, {25, "MobileBuild", 1}, {33, "ReclaimUnit", 1},
		{54, "VTOL_MobileBuild", 1}, {59, "VTOL_ReclaimUnit", 1}, {64, "VTOL_Standby", 3},
	}
	want := []byte{1, 7, 0, 0, 0, 12, 1, 19, 2, 25, 1, 33, 1, 54, 1, 59, 1, 64, 3}
	for _, reverse := range []bool{false, true} {
		q := &Queue{lastPumpTick: 0x12345678}
		var empty bytes.Buffer
		writeQueueCheckpoint(checkpoint.NewEncoder(&empty), c, q, "queue")
		for n := range rows {
			i := n
			if reverse {
				i = len(rows) - 1 - n
			}
			row := rows[i]
			if Lookup(row.name) != row.id {
				t.Fatalf("authored identity %s moved", row.name)
			}
			q.SetOwnedHandlerWithCheckpointBinding(row.id, NewCheckpointOwnedHandler(checkpointPanicHandler, row.kind, source))
		}
		for range 2 {
			if got := checkpointHandlerBytes(t, q, c); !bytes.Equal(got, want) {
				t.Fatalf("handler bytes %x want %x", got, want)
			}
		}
		var full bytes.Buffer
		e := checkpoint.NewEncoder(&full)
		writeQueueCheckpoint(e, c, q, "queue")
		if err := e.Err(); err != nil {
			t.Fatal(err)
		}
		// ownedHandlers precedes the patrol pause and two empty sequence counts.
		baseline := empty.Bytes()
		payload := append([]byte(nil), baseline[:len(baseline)-10]...)
		payload = append(payload, want...)
		payload = append(payload, baseline[len(baseline)-9:]...)
		if !bytes.Equal(full.Bytes(), payload) {
			t.Fatal("handler payload changed another queue field")
		}
		if q.lastPumpTick != 0x12345678 || len(q.ownedHandlers) != len(table) || len(q.checkpointOwnedHandlers) != len(table) {
			t.Fatal("capture changed queue state")
		}
	}
	for _, q := range []*Queue{{}, {ownedHandlers: []OwnedHandler{}}, {ownedHandlers: make([]OwnedHandler, len(table)), checkpointOwnedHandlers: make([]CheckpointOwnedHandler, len(table))}} {
		if !bytes.Equal(checkpointHandlerBytes(t, q, c), []byte{0}) {
			t.Fatal("empty fixture vector changed")
		}
	}
}

func TestCheckpointHandlersInheritanceAndIndependentReplacement(t *testing.T) {
	c, source, _, _ := checkpointHandlerContext(t)
	mobile, built := Lookup("MobileBuild"), Lookup("GetBuilt")
	wakeValue := NewCheckpointOwnedHandler(checkpointPanicHandler, CheckpointConstructionWake, source)
	builtValue := NewCheckpointOwnedHandler(checkpointPanicHandler, CheckpointGetBuilt, source)
	prior := &Queue{}
	prior.SetOwnedHandlerWithCheckpointBinding(mobile, wakeValue)
	prior.SetOwnedHandlerWithCheckpointBinding(built, builtValue)
	u := &units.Unit{Orders: prior}
	replacement := &Queue{}
	BindQueue(u, replacement)
	baseline := checkpointHandlerBytes(t, prior, c)
	if u.Orders != replacement || !bytes.Equal(checkpointHandlerBytes(t, replacement, c), baseline) {
		t.Fatal("inheritance lost handler proof")
	}
	if &prior.ownedHandlers[0] == &replacement.ownedHandlers[0] || &prior.checkpointOwnedHandlers[0] == &replacement.checkpointOwnedHandlers[0] {
		t.Fatal("inheritance shared mutable row backing")
	}
	// The ordinary getter returns no proof, even for this same function value.
	prior.SetOwnedHandler(mobile, prior.OwnedHandlerFor(mobile))
	checkpointHandlerRefusal(t, prior, c, "ownedHandlers[25]")
	if !bytes.Equal(checkpointHandlerBytes(t, replacement, c), baseline) {
		t.Fatal("source queue mutation changed inherited value")
	}
	prior.SetOwnedHandlerWithCheckpointBinding(mobile, wakeValue)
	if !bytes.Equal(checkpointHandlerBytes(t, prior, c), baseline) {
		t.Fatal("retained value lost proof after source-row edit")
	}
	replacement.SetOwnedHandler(built, nil)
	if !bytes.Equal(checkpointHandlerBytes(t, replacement, c), []byte{1, 1, 0, 0, 0, 25, 1}) {
		t.Fatal("clearing one row changed another")
	}
	if !bytes.Equal(checkpointHandlerBytes(t, prior, c), baseline) {
		t.Fatal("destination mutation changed original queue")
	}
	// A value copy remains valid independently of either queue's current row.
	copiedValue := wakeValue
	prior.SetOwnedHandler(mobile, nil)
	q := &Queue{}
	q.SetOwnedHandlerWithCheckpointBinding(mobile, copiedValue)
	checkpointHandlerBytes(t, q, c)
	q.SetOwnedHandler(mobile, copiedValue.Handler())
	checkpointHandlerRefusal(t, q, c, "ownedHandlers[25]")
	// An explicit replacement (including allocated all-nil storage) does not
	// inherit any of the previous queue's handler rows or proof.
	for _, explicit := range []*Queue{{ownedHandlers: make([]OwnedHandler, len(table))}, {}} {
		if explicit.ownedHandlers == nil {
			explicit.SetOwnedHandlerWithCheckpointBinding(mobile, wakeValue)
		}
		before := checkpointHandlerBytes(t, explicit, c)
		u.Orders = prior
		BindQueue(u, explicit)
		if !bytes.Equal(checkpointHandlerBytes(t, explicit, c), before) {
			t.Fatal("explicit replacement acquired prior handlers")
		}
	}
	// Unsupported ordinary rows stay unsupported across the same copy branch.
	prior.SetOwnedHandler(mobile, checkpointPanicHandler)
	u.Orders = prior
	q = &Queue{}
	BindQueue(u, q)
	checkpointHandlerRefusal(t, q, c, "ownedHandlers[25]")
}

func TestCheckpointHandlersClosedKindsAndMalformedState(t *testing.T) {
	c, source, _, _ := checkpointHandlerContext(t)
	for _, row := range []struct {
		id   ID
		kind uint8
	}{
		{Lookup("GetBuilt"), 1}, {Lookup("BuildingBuild"), 2}, {Lookup("MobileBuild"), 3}, {Lookup("Stop"), 1},
		{Lookup("VTOL_Standby"), 0}, {Lookup("VTOL_Standby"), 4},
	} {
		q := &Queue{}
		q.SetOwnedHandlerWithCheckpointBinding(row.id, NewCheckpointOwnedHandler(checkpointPanicHandler, row.kind, source))
		if q.OwnedHandlerFor(row.id) == nil {
			t.Fatal("diagnostic refusal gated ordinary installation")
		}
		checkpointHandlerRefusal(t, q, c, "ownedHandlers[")
	}
	for _, mutate := range []func(*Queue){
		func(q *Queue) { q.checkpointOwnedHandlers = nil },
		func(q *Queue) { q.checkpointOwnedHandlers = make([]CheckpointOwnedHandler, 1) },
		func(q *Queue) { q.ownedHandlers = make([]OwnedHandler, 1) },
		func(q *Queue) { q.ownedHandlers[25] = nil },
		func(q *Queue) { q.ownedHandlers = nil },
		func(q *Queue) { q.checkpointOwnedHandlers[25].handler = nil },
		func(q *Queue) { q.checkpointOwnedHandlers[25].source = nil },
	} {
		q := &Queue{}
		q.SetOwnedHandlerWithCheckpointBinding(25, NewCheckpointOwnedHandler(checkpointPanicHandler, 1, source))
		mutate(q)
		if _, _, err := q.validateCheckpointHandlers(c); err == nil {
			t.Fatal("malformed state admitted")
		}
	}
	q := &Queue{}
	q.SetOwnedHandlerWithCheckpointBinding(25, NewCheckpointOwnedHandler(checkpointPanicHandler, 1, nil))
	checkpointHandlerRefusal(t, q, c, "ownedHandlers[25]")
	// Proof-only residue is refused even when its function field is absent.
	q = &Queue{checkpointOwnedHandlers: make([]CheckpointOwnedHandler, len(table))}
	q.checkpointOwnedHandlers[25] = CheckpointOwnedHandler{kind: 1, source: source}
	checkpointHandlerRefusal(t, q, c, "ownedHandlers[25]")
}

func TestCheckpointHandlerSourceExactOwnerAndCopy(t *testing.T) {
	owner := &checkpointHandlerOwner{[]int{1}}
	a := checkpoint.NewBindingAuthority()
	source := NewCheckpointHandlerSource(owner, a)
	if NewCheckpointHandlerSource((*checkpointHandlerOwner)(nil), a) != nil || NewCheckpointHandlerSource(owner, nil) != nil {
		t.Fatal("missing inputs produced source")
	}
	c := orderCheckpointContext(t)
	ownerCopy := *owner
	for _, attempt := range []func() error{
		func() error { return RegisterCheckpointHandlerSource(c, 0, source, owner, a) },
		func() error { return RegisterCheckpointHandlerSource(c, 4, source, owner, a) },
		func() error { return RegisterCheckpointHandlerSource(c, 1, nil, owner, a) },
		func() error { return RegisterCheckpointHandlerSource(c, 1, source, (*checkpointHandlerOwner)(nil), a) },
		func() error { return RegisterCheckpointHandlerSource(c, 1, source, owner, nil) },
		func() error {
			return RegisterCheckpointHandlerSource(c, 1, source, owner, checkpoint.NewBindingAuthority())
		},
		func() error { return RegisterCheckpointHandlerSource(c, 1, source, &ownerCopy, a) },
		func() error { return RegisterCheckpointHandlerSource(c, 1, source, &checkpointOtherHandlerOwner{}, a) },
		func() error { return RegisterCheckpointHandlerSource[*checkpointHandlerOwner](c, 1, source, &owner, a) },
		func() error { return RegisterCheckpointHandlerSource(nil, 1, source, owner, a) },
	} {
		if attempt() == nil || c.handlerAuthority != nil || c.handlerSources[0].source != nil {
			t.Fatal("invalid source registration changed context")
		}
	}
	q := &Queue{}
	value := NewCheckpointOwnedHandler(checkpointPanicHandler, 1, source)
	q.SetOwnedHandlerWithCheckpointBinding(25, value)
	// A source copy before registration cannot substitute for the source pointer
	// retained with this already-copied handler value.
	copiedSource := *source
	if err := RegisterCheckpointHandlerSource(c, 1, &copiedSource, owner, a); err != nil {
		t.Fatal(err)
	}
	checkpointHandlerRefusal(t, q, c, "ownedHandlers[25]")
	c = orderCheckpointContext(t)
	if err := RegisterCheckpointHandlerSource(c, 1, source, owner, a); err != nil {
		t.Fatal(err)
	}
	if err := RegisterCheckpointHandlerSource(c, 1, source, owner, a); err != nil {
		t.Fatal(err)
	}
	before := c.handlerSources[0].source
	if RegisterCheckpointHandlerSource(c, 1, &copiedSource, owner, a) == nil || c.handlerSources[0].source != before {
		t.Fatal("copied source replaced registered identity")
	}
	checkpointHandlerBytes(t, q, c)
	q.SetOwnedHandlerWithCheckpointBinding(25, NewCheckpointOwnedHandler(checkpointPanicHandler, 1, &copiedSource))
	checkpointHandlerRefusal(t, q, c, "ownedHandlers[25]")
}

func TestCheckpointHandlerSourceOverwriteCannotRefreshContext(t *testing.T) {
	owner, other := &checkpointHandlerOwner{[]int{1}}, &checkpointHandlerOwner{[]int{2}}
	a := checkpoint.NewBindingAuthority()
	for _, after := range []bool{false, true} {
		t.Run(map[bool]string{false: "before", true: "after"}[after], func(t *testing.T) {
			c := orderCheckpointContext(t)
			source := NewCheckpointHandlerSource(owner, a)
			if after {
				if err := RegisterCheckpointHandlerSource(c, 1, source, owner, a); err != nil {
					t.Fatal(err)
				}
			}
			*source = *NewCheckpointHandlerSource(other, a)
			if RegisterCheckpointHandlerSource(c, 1, source, owner, a) == nil {
				t.Fatal("overwritten owner admitted")
			}
			if after {
				if RegisterCheckpointHandlerSource(c, 1, source, other, a) == nil {
					t.Fatal("same pointer refreshed old owner expectation")
				}
				if _, err := (&Pump{}).CollectCheckpointReferences(c); err == nil {
					t.Fatal("collection accepted overwritten source")
				}
				var out bytes.Buffer
				if err := (&Pump{}).WriteCheckpoint(checkpoint.NewEncoder(&out), c); err == nil || out.Len() != 0 {
					t.Fatal("writer accepted overwritten source")
				}
				// A hostile noncomparable value must refuse, never interface-compare.
				source.owner = []int{9}
				if c.validateCheckpointHandlerSources() == nil {
					t.Fatal("nonpointer owner admitted")
				}
				source.owner = owner
				source.authority = checkpoint.NewBindingAuthority()
				if c.validateCheckpointHandlerSources() == nil {
					t.Fatal("overwritten authority admitted")
				}
			} else if c.handlerAuthority != nil || c.handlerSources[0].source != nil {
				t.Fatal("pre-registration refusal changed context")
			}
		})
	}
}

func TestCheckpointHandlerBindingAuthorityBothRegistrationOrders(t *testing.T) {
	owner := &checkpointHandlerOwner{}
	a, foreign := checkpoint.NewBindingAuthority(), checkpoint.NewBindingAuthority()
	source := NewCheckpointHandlerSource(owner, a)
	for _, sourceFirst := range []bool{false, true} {
		c := orderCheckpointContext(t)
		b := &QueueBinding{}
		if sourceFirst {
			if err := RegisterCheckpointHandlerSource(c, 1, source, owner, a); err != nil {
				t.Fatal(err)
			}
			if c.SetBindings(b, nil, nil, foreign) == nil || c.bindings != nil {
				t.Fatal("binding refreshed source authority")
			}
			if err := c.SetBindings(b, nil, nil, a); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := c.SetBindings(b, nil, nil, a); err != nil {
				t.Fatal(err)
			}
			if RegisterCheckpointHandlerSource(c, 1, NewCheckpointHandlerSource(owner, foreign), owner, foreign) == nil || c.handlerAuthority != nil {
				t.Fatal("source refreshed binding authority")
			}
			if err := RegisterCheckpointHandlerSource(c, 1, source, owner, a); err != nil {
				t.Fatal(err)
			}
		}
		if RegisterCheckpointHandlerSource(c, 2, NewCheckpointHandlerSource(owner, foreign), owner, foreign) == nil || c.handlerSources[1].source != nil {
			t.Fatal("another kind changed authority")
		}
		if err := RegisterCheckpointHandlerSource(c, 2, source, owner, a); err != nil {
			t.Fatal(err)
		}
		if c.SetBindings(b, nil, nil, a) != nil {
			t.Fatal("idempotent binding registration refused")
		}
	}
}

func TestCheckpointHandlersNilInvalidAndDispatch(t *testing.T) {
	c, source, _, _ := checkpointHandlerContext(t)
	value := NewCheckpointOwnedHandler(checkpointPanicHandler, 1, source)
	var nilQueue *Queue
	nilQueue.SetOwnedHandlerWithCheckpointBinding(25, value)
	for _, id := range []ID{0, ID(len(table)), 255} {
		q := &Queue{}
		q.SetOwnedHandlerWithCheckpointBinding(id, value)
		if q.ownedHandlers != nil || q.checkpointOwnedHandlers != nil {
			t.Fatal("invalid row allocated storage")
		}
	}
	q := &Queue{}
	q.SetOwnedHandlerWithCheckpointBinding(25, NewCheckpointOwnedHandler(nil, 1, source))
	if q.ownedHandlers != nil || q.checkpointOwnedHandlers != nil {
		t.Fatal("nil handler allocated storage")
	}
	if (CheckpointOwnedHandler{}).Handler() != nil {
		t.Fatal("zero value has handler")
	}
	q.SetOwnedHandlerWithCheckpointBinding(25, value)
	q.SetOwnedHandlerWithCheckpointBinding(25, NewCheckpointOwnedHandler(nil, 1, source))
	if !bytes.Equal(checkpointHandlerBytes(t, q, c), []byte{0}) {
		t.Fatal("nil setter retained proof")
	}
	// Ordinary convenience setters invalidate their same destination row.
	q.SetOwnedHandlerWithCheckpointBinding(25, value)
	q.SetExternallyDrivenHandler(25)
	checkpointHandlerRefusal(t, q, c, "ownedHandlers[25]")
	q.SetOwnedHandlerWithCheckpointBinding(19, NewCheckpointOwnedHandler(checkpointPanicHandler, 2, source))
	q.SetOwnedHandler(25, nil)
	q.SetGetBuiltHandler(func(*units.Unit, *Node, uint32, uint32) Code { return 7 })
	checkpointHandlerRefusal(t, q, c, "ownedHandlers[19]")
	q.SetGetBuiltHandler(nil)
	checkpointHandlerBytes(t, q, c)
	// Admitted installation retains the exact ordinary pump result path.
	q, u := gateFixture()
	calls := 0
	q.SetOwnedHandlerWithCheckpointBinding(25, NewCheckpointOwnedHandler(func(_ *units.Unit, n *Node, satisfied, tick uint32) (Code, bool) {
		calls++
		if satisfied != 0x8001 || tick != 45 {
			t.Fatalf("dispatch operands %#x %d", satisfied, tick)
		}
		n.DynamicGate = 0x8000
		n.Deadline = int32(tick + 30)
		return 1, true
	}, 1, source))
	if calls != 0 {
		t.Fatal("installation invoked handler")
	}
	q.Push(25, Node{Owner: u.Handle})
	n := q.Primary()[0]
	n.DynamicGate = 0x8001
	n.Satisfied = 1
	u.Pending = 0x8000
	q.Pump(u, 45)
	if calls != 1 || n.Phase != 1 || n.Deadline != 75 {
		t.Fatalf("ordinary dispatch changed: calls=%d phase=%d deadline=%d", calls, n.Phase, n.Deadline)
	}
}
