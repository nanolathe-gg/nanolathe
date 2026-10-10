package orders

import (
	"errors"
	"fmt"

	"github.com/nanolathe-gg/nanolathe/internal/sim/checkpoint"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// CheckpointContext borrows the allocation table and owns each queue/node
// identity once for this capture (DESIGN_MULTIPLAYER §16.3.6).
type CheckpointContext struct {
	Units            *units.CheckpointContext
	Queues           checkpoint.References[*Queue]
	Nodes            checkpoint.References[*Node]
	bindings         *checkpointBindingSources
	handlerSources   [3]checkpointHandlerRegistration
	handlerAuthority *checkpoint.BindingAuthority
}

func NewCheckpointContext(u *units.CheckpointContext) *CheckpointContext {
	return &CheckpointContext{Units: u}
}

// CollectCheckpointReferences scans the discovered allocations, including
// retired ones, without creating queues. Session repeats owner collection in
// section order until no owner adds an object (DESIGN_MULTIPLAYER §16.3.6).
func (p *Pump) CollectCheckpointReferences(c *CheckpointContext) (added int, err error) {
	if p == nil || c == nil || c.Units == nil {
		return 0, orderCheckpointError("orders", errors.New("missing pump or allocation context"))
	}
	if err := c.validateCheckpointHandlerSources(); err != nil {
		return 0, err
	}
	if c.bindings != nil {
		if err := c.ValidateBinding(c.bindings.binding); err != nil {
			return 0, err
		}
	}
	for i, u := range c.Units.Allocations.Values() {
		path := fmt.Sprintf("orders.roots[%d].Orders", i)
		q, err := checkpointQueue(u)
		if err != nil {
			return added, orderCheckpointError(path, err)
		}
		n, err := addOrderCheckpointReference(&c.Queues, q)
		added += n
		if err != nil {
			return added, orderCheckpointError(path, err)
		}
	}
	for i, q := range c.Queues.Values() {
		path := fmt.Sprintf("orders.queues[%d]", i)
		if field, err := validateCheckpointQueue(q, c); err != nil {
			return added, orderCheckpointError(path+"."+field, err)
		}
		// Queue edges follow lexical field order, then retained sequence order.
		for j, contact := range q.danger.contacts {
			n, err := addOrderCheckpointReference(&c.Units.Allocations, contact.unit)
			added += n
			if err != nil {
				return added, orderCheckpointError(fmt.Sprintf("%s.danger.contacts[%d].unit", path, j), err)
			}
		}
		for _, edge := range []struct {
			field string
			node  *Node
		}{
			{"danger.response", q.danger.response},
			{"danger.resume", q.danger.resume},
			{"danger.returnMove", q.danger.returnMove},
			{"firingPosition.node", q.firingPosition.node},
		} {
			n, err := addOrderCheckpointReference(&c.Nodes, edge.node)
			added += n
			if err != nil {
				return added, orderCheckpointError(path+"."+edge.field, err)
			}
		}
		for _, edge := range []struct {
			field string
			unit  *units.Unit
		}{
			{"firingPosition.owner", q.firingPosition.owner},
			{"firingPosition.target", q.firingPosition.target},
		} {
			n, err := addOrderCheckpointReference(&c.Units.Allocations, edge.unit)
			added += n
			if err != nil {
				return added, orderCheckpointError(path+"."+edge.field, err)
			}
		}
		for _, segment := range []struct {
			field string
			nodes []*Node
		}{{"primary", q.primary}, {"secondary", q.secondary}} {
			for j, node := range segment.nodes {
				n, err := addOrderCheckpointReference(&c.Nodes, node)
				added += n
				if err != nil {
					return added, orderCheckpointError(fmt.Sprintf("%s.%s[%d]", path, segment.field, j), err)
				}
			}
		}
	}
	// Other owners may retain a node after its queue has released it.
	for i, n := range c.Nodes.Values() {
		if field, err := validateCheckpointNode(n); err != nil {
			return added, orderCheckpointError(fmt.Sprintf("orders.nodes[%d].%s", i, field), err)
		}
		// Node edges follow lexical field order, including detached continuations.
		for _, edge := range []struct {
			field string
			node  *Node
		}{{"workAssignment", n.workAssignment}, {"workReturn", n.workReturn}} {
			count, err := addOrderCheckpointReference(&c.Nodes, edge.node)
			added += count
			if err != nil {
				return added, orderCheckpointError(fmt.Sprintf("orders.nodes[%d].%s", i, edge.field), err)
			}
		}
	}
	return added, nil
}

func addOrderCheckpointReference[T comparable](r *checkpoint.References[T], value T) (int, error) {
	if _, ok := r.Find(value); ok {
		return 0, nil
	}
	if _, err := r.Add(value); err != nil {
		return 0, err
	}
	return 1, nil
}

func orderCheckpointError(path string, err error) error {
	return fmt.Errorf("nanolathe: checkpoint owner orders: logical path %s, providers searched [], expected canonical checkpoint: %w", path, err)
}

func checkpointQueue(u *units.Unit) (*Queue, error) {
	switch q := u.Orders.(type) {
	case nil:
		return nil, nil
	case *Queue:
		if q == nil {
			return nil, errors.New("typed-nil order queue")
		}
		return q, nil
	default:
		return nil, fmt.Errorf("unsupported order value %T", u.Orders)
	}
}

func validateCheckpointQueue(q *Queue, c *CheckpointContext) (string, error) {
	if q.checkpointObserver != nil {
		return "checkpointObserver", errors.New("order application observation is in progress")
	}
	if q.detachedNode != nil {
		return "detachedNode", errors.New("order cleanup is in progress")
	}
	if q.detachedHasSuccessor {
		return "detachedHasSuccessor", errors.New("order cleanup successor is retained")
	}
	if err := c.ValidateBinding(q.binding); err != nil {
		return "binding", err
	}
	if _, field, err := q.validateCheckpointHandlers(c); err != nil {
		return field, err
	}
	return "", nil
}

func validateCheckpointNode(n *Node) (string, error) {
	if n.GoalSupplied {
		return "GoalSupplied", errors.New("order goal insertion input has not been consumed")
	}
	if n.QueuedIssue {
		return "QueuedIssue", errors.New("order queue insertion input has not been consumed")
	}
	return "", nil
}

// WriteCheckpoint writes section 3: allocation-to-queue roots, table 2, then
// table 3. The pump has no additional retained record. References are lookup
// only; a missing discovery is an error, never an implicit collection pass.
func (p *Pump) WriteCheckpoint(e *checkpoint.Encoder, c *CheckpointContext) error {
	e.Field("orders")
	if p == nil || c == nil || c.Units == nil {
		e.Fail(errors.New("missing pump or allocation context"))
		return e.Err()
	}
	if err := c.validateCheckpointHandlerSources(); err != nil {
		e.Fail(err)
		return e.Err()
	}
	if c.bindings != nil {
		if err := c.ValidateBinding(c.bindings.binding); err != nil {
			e.Fail(err)
			return e.Err()
		}
	}
	allocations := c.Units.Allocations.Values()
	e.Field("orders.roots")
	e.Count(len(allocations))
	for i, u := range allocations {
		path := fmt.Sprintf("orders.roots[%d]", i)
		q, err := checkpointQueue(u)
		if err != nil {
			e.Field(path + ".Orders")
			e.Fail(err)
			return e.Err()
		}
		writeOrderCheckpointReference(e, path+".allocation", 1, &c.Units.Allocations, u)
		writeOrderCheckpointReference(e, path+".queue", 2, &c.Queues, q)
	}
	queues := c.Queues.Values()
	e.Field("orders.queues")
	e.U16(2)
	e.Count(len(queues))
	for i, q := range queues {
		path := fmt.Sprintf("orders.queues[%d]", i)
		if field, err := validateCheckpointQueue(q, c); err != nil {
			e.Field(path + "." + field)
			e.Fail(err)
			return e.Err()
		}
		writeQueueCheckpoint(e, c, q, path)
	}
	nodes := c.Nodes.Values()
	e.Field("orders.nodes")
	e.U16(3)
	e.Count(len(nodes))
	for i, n := range nodes {
		path := fmt.Sprintf("orders.nodes[%d]", i)
		if field, err := validateCheckpointNode(n); err != nil {
			e.Field(path + "." + field)
			e.Fail(err)
			return e.Err()
		}
		writeNodeCheckpoint(e, n, path)
		e.Field(path + ".patrolReturn")
		e.Bool(n.patrolReturn)
		e.Field(path + ".patrolReturnArrived")
		e.Bool(n.patrolReturnArrived)
		writeOrderCheckpointReference(e, path+".workAssignment", 3, &c.Nodes, n.workAssignment)
		writeOrderCheckpointReference(e, path+".workReturn", 3, &c.Nodes, n.workReturn)
	}
	return e.Err()
}

func writeOrderCheckpointReference[T comparable](e *checkpoint.Encoder, path string, table uint16, r *checkpoint.References[T], value T) {
	e.Field(path)
	id, ok := r.Find(value)
	if !ok {
		e.Fail(errors.New("undiscovered reference"))
		return
	}
	e.U16(table)
	e.U32(uint32(id))
}

// Expanded Queue fields: binding, danger, firingPosition, lastPumpTick,
// ownedHandlers, patrolWorkPaused, primary, secondary. Binding carries its
// attested payload;
// handlers retain numeric row/kind pairs; empty/all-nil storage stays absent.
// detachedNode/detachedHasSuccessor are forbidden. diagnostics/secondaryTick
// are excluded (DESIGN_MULTIPLAYER §16.3.5–§16.3.6).
func writeQueueCheckpoint(e *checkpoint.Encoder, c *CheckpointContext, q *Queue, path string) {
	e.Field(path + ".binding")
	if err := q.binding.WriteCheckpoint(e, c); err != nil {
		return
	}
	writeDangerCheckpoint(e, c, &q.danger, path+".danger")
	f := &q.firingPosition
	// Expanded firingPositionState: active, nextAttempt, node, owner, started, target.
	e.Field(path + ".firingPosition.active")
	e.Bool(f.active)
	e.Field(path + ".firingPosition.nextAttempt")
	e.U32(f.nextAttempt)
	writeOrderCheckpointReference(e, path+".firingPosition.node", 3, &c.Nodes, f.node)
	writeOrderCheckpointReference(e, path+".firingPosition.owner", 1, &c.Units.Allocations, f.owner)
	e.Field(path + ".firingPosition.started")
	e.U32(f.started)
	writeOrderCheckpointReference(e, path+".firingPosition.target", 1, &c.Units.Allocations, f.target)
	e.Field(path + ".lastPumpTick")
	e.U32(q.lastPumpTick)
	q.writeCheckpointHandlers(e, c, path+".ownedHandlers")
	e.Field(path + ".patrolWorkPaused")
	e.Bool(q.patrolWorkPaused)
	for _, segment := range []struct {
		field string
		nodes []*Node
	}{{"primary", q.primary}, {"secondary", q.secondary}} {
		e.Field(path + "." + segment.field)
		e.Count(len(segment.nodes))
		for i, n := range segment.nodes {
			writeOrderCheckpointReference(e, fmt.Sprintf("%s.%s[%d]", path, segment.field, i), 3, &c.Nodes, n)
		}
	}
}

// Expanded dangerState: anchorX, anchorY, anchorZ, anchored, contacts,
// impacts, nextDecision, opportunityTarget, quietUntil, response, resume,
// returnMove, withdrew. Both fixed arrays retain every slot, including invalid
// records. Contacts: failedUntil, handle, tick, unit, x, z. Impacts: sector,
// tick, valid, x, z (DESIGN_MULTIPLAYER §16.3.5–§16.3.6).
func writeDangerCheckpoint(e *checkpoint.Encoder, c *CheckpointContext, d *dangerState, path string) {
	e.Field(path + ".anchorX")
	e.I64(int64(d.anchorX))
	e.Field(path + ".anchorY")
	e.I64(int64(d.anchorY))
	e.Field(path + ".anchorZ")
	e.I64(int64(d.anchorZ))
	e.Field(path + ".anchored")
	e.Bool(d.anchored)
	for i, contact := range d.contacts {
		p := fmt.Sprintf("%s.contacts[%d]", path, i)
		e.Field(p + ".failedUntil")
		e.U32(contact.failedUntil)
		e.Field(p + ".handle")
		e.U32(uint32(contact.handle))
		e.Field(p + ".tick")
		e.U32(contact.tick)
		writeOrderCheckpointReference(e, p+".unit", 1, &c.Units.Allocations, contact.unit)
		e.Field(p + ".x")
		e.I64(int64(contact.x))
		e.Field(p + ".z")
		e.I64(int64(contact.z))
	}
	for i, impact := range d.impacts {
		p := fmt.Sprintf("%s.impacts[%d]", path, i)
		e.Field(p + ".sector")
		e.U8(impact.sector)
		e.Field(p + ".tick")
		e.U32(impact.tick)
		e.Field(p + ".valid")
		e.Bool(impact.valid)
		e.Field(p + ".x")
		e.I64(int64(impact.x))
		e.Field(p + ".z")
		e.I64(int64(impact.z))
	}
	e.Field(path + ".nextDecision")
	e.U32(d.nextDecision)
	e.Field(path + ".opportunityTarget")
	e.U32(uint32(d.opportunityTarget))
	e.Field(path + ".quietUntil")
	e.U32(d.quietUntil)
	writeOrderCheckpointReference(e, path+".response", 3, &c.Nodes, d.response)
	writeOrderCheckpointReference(e, path+".resume", 3, &c.Nodes, d.resume)
	writeOrderCheckpointReference(e, path+".returnMove", 3, &c.Nodes, d.returnMove)
	e.Field(path + ".withdrew")
	e.Bool(d.withdrew)
}

// Expanded Node fields: BuildDefKey, BuildFacing, CachedX, CachedY,
// CaptionPending, CreationTick, Deadline, DynamicGate, Flags, GoalX, GoalY,
// GoalZ, GuardX, GuardY, HumanMoveSequence, ID, MoveState, Owner, Param1,
// Param2, Param3, PathStatus, Phase, Satisfied, StaticGate, Target,
// automaticAttack, automaticWork, crowdedArrival, nextAutomaticTargetTick.
// The full graph writer follows these values with patrolReturn,
// patrolReturnArrived, workAssignment and workReturn in lexical order.
// Synchronous value receipts retain their legacy schema without lifecycle
// provenance or graph IDs (DESIGN_MULTIPLAYER §16.3.21).
// RetailSubtype* is reconstructed restore/save staging. GoalSupplied and
// QueuedIssue must be consumed. All raw handles are u32, while Fixed retains
// its int64 width (DESIGN_MULTIPLAYER §16.3.5–§16.3.6).
func writeNodeCheckpoint(e *checkpoint.Encoder, n *Node, path string) {
	e.Field(path + ".BuildDefKey")
	e.String(n.BuildDefKey)
	e.Field(path + ".BuildFacing")
	e.U8(uint8(n.BuildFacing))
	e.Field(path + ".CachedX")
	e.I16(n.CachedX)
	e.Field(path + ".CachedY")
	e.I16(n.CachedY)
	e.Field(path + ".CaptionPending")
	e.Bool(n.CaptionPending)
	e.Field(path + ".CreationTick")
	e.U32(n.CreationTick)
	e.Field(path + ".Deadline")
	e.I32(n.Deadline)
	e.Field(path + ".DynamicGate")
	e.U32(n.DynamicGate)
	e.Field(path + ".Flags")
	e.U32(n.Flags)
	e.Field(path + ".GoalX")
	e.I64(int64(n.GoalX))
	e.Field(path + ".GoalY")
	e.I64(int64(n.GoalY))
	e.Field(path + ".GoalZ")
	e.I64(int64(n.GoalZ))
	e.Field(path + ".GuardX")
	e.I16(n.GuardX)
	e.Field(path + ".GuardY")
	e.I16(n.GuardY)
	e.Field(path + ".HumanMoveSequence")
	e.U64(n.HumanMoveSequence)
	e.Field(path + ".ID")
	e.U8(uint8(n.ID))
	e.Field(path + ".MoveState")
	e.U8(n.MoveState)
	e.Field(path + ".Owner")
	e.U32(uint32(n.Owner))
	e.Field(path + ".Param1")
	e.U32(n.Param1)
	e.Field(path + ".Param2")
	e.U32(n.Param2)
	e.Field(path + ".Param3")
	e.U32(n.Param3)
	e.Field(path + ".PathStatus")
	e.U32(n.PathStatus)
	e.Field(path + ".Phase")
	e.U8(n.Phase)
	e.Field(path + ".Satisfied")
	e.U32(n.Satisfied)
	e.Field(path + ".StaticGate")
	e.U32(n.StaticGate)
	e.Field(path + ".Target")
	e.U32(uint32(n.Target))
	e.Field(path + ".automaticAttack")
	e.Bool(n.automaticAttack)
	e.Field(path + ".automaticWork")
	e.Bool(n.automaticWork)
	// Expanded crowdedArrivalState: active, goalX, goalZ, lastTick, since, x, z.
	a := &n.crowdedArrival
	e.Field(path + ".crowdedArrival.active")
	e.Bool(a.active)
	e.Field(path + ".crowdedArrival.goalX")
	e.I64(int64(a.goalX))
	e.Field(path + ".crowdedArrival.goalZ")
	e.I64(int64(a.goalZ))
	e.Field(path + ".crowdedArrival.lastTick")
	e.U32(a.lastTick)
	e.Field(path + ".crowdedArrival.since")
	e.U32(a.since)
	e.Field(path + ".crowdedArrival.x")
	e.I32(a.x)
	e.Field(path + ".crowdedArrival.z")
	e.I32(a.z)
	e.Field(path + ".nextAutomaticTargetTick")
	e.U32(n.nextAutomaticTargetTick)
}
