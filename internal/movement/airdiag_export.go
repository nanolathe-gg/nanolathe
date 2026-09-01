package movement

import "github.com/nanolathe/nanolathe/internal/pool"

// AirExecutorSnapshot is a read-only copy of the movement-side air executor
// state for one unit [04 R-AIR-01 §1]. It exists so a diagnostic harness can
// observe the executor's phase and its gate/arrival latches without this
// package exporting its mutable state. Nothing in the simulation reads it.
type AirExecutorSnapshot struct {
	Bound    bool
	Phase    uint8
	Waiting  bool
	Arrived  bool
	Done     bool
	PadPiece uint16
	Bearing  uint16
	HasOrder bool
}

// AirExecutorState returns the air executor snapshot for a handle. A false
// Bound field means the mover tick has never dispatched an air executor for
// that unit.
func (s *System) AirExecutorState(h pool.Handle) AirExecutorSnapshot {
	if s == nil || s.airOrders == nil {
		return AirExecutorSnapshot{}
	}
	st := s.airOrders[h]
	if st == nil {
		return AirExecutorSnapshot{}
	}
	return AirExecutorSnapshot{
		Bound:    true,
		Phase:    st.phase,
		Waiting:  st.waiting,
		Arrived:  st.arrived,
		Done:     st.done,
		PadPiece: st.padPiece,
		Bearing:  st.bearing,
		HasOrder: st.order != nil,
	}
}

// AirGoalPayload returns the goal payload currently installed on a unit's
// flight command block, or nil. Diagnostic only.
func (s *System) AirGoalPayload(h pool.Handle) GoalPayload {
	if s == nil {
		return nil
	}
	fl := s.Flights[h]
	if fl == nil || fl.Command == nil {
		return nil
	}
	return fl.Command.Payload
}

// AirMarkerSnapshot describes an installed air path marker [04 R-AIR-01 §4].
type AirMarkerSnapshot struct {
	IsMarker      bool
	Flags         uint16
	ArrivalRadius uint16
	AltOffset     int16
	Heading       uint16
	Goal          Vec3
}

// AirMarkerState describes the installed payload when it is a path marker.
func (s *System) AirMarkerState(h pool.Handle) AirMarkerSnapshot {
	m, ok := s.AirGoalPayload(h).(*airMarker)
	if !ok || m == nil {
		return AirMarkerSnapshot{}
	}
	return AirMarkerSnapshot{
		IsMarker:      true,
		Flags:         m.flags,
		ArrivalRadius: m.radius,
		AltOffset:     m.altOffset,
		Heading:       m.heading,
		Goal:          m.goal,
	}
}
