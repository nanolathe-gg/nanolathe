package orders

import "github.com/nanolathe-gg/nanolathe/internal/pool"

type DebugState struct {
	Queue                              SnapshotQueue
	LastPumpTick, SecondaryTick        uint32
	PrimaryCapacity, SecondaryCapacity int
	Diagnostics                        []string
	OwnedHandlerCount                  int
}

func (q *Queue) DebugSnapshot(unit pool.Handle) *DebugState {
	if q == nil {
		return nil
	}
	return &DebugState{SnapshotQueueOf(q, unit, nil), q.lastPumpTick, q.secondaryTick, cap(q.primary), cap(q.secondary), append([]string(nil), q.diagnostics...), len(q.ownedHandlers)}
}
