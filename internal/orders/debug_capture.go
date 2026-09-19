package orders

import (
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

type DebugState struct {
	Queue                              SnapshotQueue
	LastPumpTick, SecondaryTick        uint32
	PrimaryCapacity, SecondaryCapacity int
	Diagnostics                        []string
	OwnedHandlerCount                  int
	Danger                             DebugDangerState
}

// DebugDangerState copies transient Modern policy state without aging contacts,
// selecting an action or retaining a live unit/order pointer. It is diagnostic
// data only; the ordinary save projection deliberately omits this state.
type DebugDangerState struct {
	Contacts                     [4]DebugDangerContact
	Impacts                      [4]DebugDangerImpact
	Response, Resume, ReturnMove *DebugDangerOrder
	AnchorX, AnchorY, AnchorZ    numeric.Fixed
	Anchored, Withdrew           bool
	NextDecision, QuietUntil     uint32
	OpportunityTarget            pool.Handle
	EligibleHead, ProtectedWork  bool
}

type DebugDangerContact struct {
	Present                                                         bool
	Handle                                                          pool.Handle
	ObservedTick, FailedUntil                                       uint32
	X, Z                                                            numeric.Fixed
	IdentityChecked, IdentityCurrent, VisibilityChecked, VisibleNow bool
}

type DebugDangerImpact struct {
	Valid        bool
	Sector       uint8
	ObservedTick uint32
	X, Z         numeric.Fixed
}

type DebugDangerOrder struct {
	DescriptorID                   ID
	PrimaryIndex                   int // -1 means the retained identity is no longer in the primary queue
	Target                         pool.Handle
	GoalX, GoalY, GoalZ            numeric.Fixed
	AutomaticWork, AutomaticAttack bool
}

func (q *Queue) DebugSnapshot(unit pool.Handle) *DebugState {
	if q == nil {
		return nil
	}
	out := &DebugState{
		Queue: SnapshotQueueOf(q, unit, nil), LastPumpTick: q.lastPumpTick,
		SecondaryTick: q.secondaryTick, PrimaryCapacity: cap(q.primary),
		SecondaryCapacity: cap(q.secondary), Diagnostics: append([]string(nil), q.diagnostics...),
		OwnedHandlerCount: len(q.ownedHandlers),
	}
	d := &q.danger
	out.Danger = DebugDangerState{
		Response: q.debugDangerOrder(d.response), Resume: q.debugDangerOrder(d.resume),
		ReturnMove: q.debugDangerOrder(d.returnMove), AnchorX: d.anchorX, AnchorY: d.anchorY, AnchorZ: d.anchorZ,
		Anchored: d.anchored, Withdrew: d.withdrew, NextDecision: d.nextDecision,
		QuietUntil: d.quietUntil, OpportunityTarget: d.opportunityTarget,
		EligibleHead: dangerEligibleHead(q), ProtectedWork: protectedDangerWork(q),
	}
	for i, c := range d.contacts {
		copy := DebugDangerContact{Present: c.unit != nil, Handle: c.handle, ObservedTick: c.tick, FailedUntil: c.failedUntil, X: c.x, Z: c.z}
		if b := q.Binding(); c.unit != nil && b != nil && b.Lookup != nil {
			copy.IdentityChecked, copy.IdentityCurrent = true, b.Lookup(c.handle) == c.unit
			if observer := b.Lookup(unit); copy.IdentityCurrent && observer != nil && b.DangerVisible != nil {
				copy.VisibilityChecked, copy.VisibleNow = true, b.DangerVisible(observer, c.unit)
			}
		}
		out.Danger.Contacts[i] = copy
	}
	for i, p := range d.impacts {
		out.Danger.Impacts[i] = DebugDangerImpact{p.valid, p.sector, p.tick, p.x, p.z}
	}
	return out
}

func (q *Queue) debugDangerOrder(n *Node) *DebugDangerOrder {
	if n == nil {
		return nil
	}
	index := -1
	for i, candidate := range q.primary {
		if candidate == n {
			index = i
			break
		}
	}
	return &DebugDangerOrder{n.ID, index, n.Target, n.GoalX, n.GoalY, n.GoalZ, n.automaticWork, n.automaticAttack}
}
