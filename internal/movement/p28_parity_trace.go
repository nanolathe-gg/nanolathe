package movement

import (
	"github.com/nanolathe-gg/nanolathe/internal/path"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// CollisionTrace is a copy of the committed collision state. It is kept
// package-local so the later parity bundle can adapt it without coupling the
// movement package to a report schema.
type CollisionTrace struct {
	Tick  uint32
	Slot  pool.Handle
	State CollisionState
}

// MovementTrace is a deterministic copy of movement-owned state at a capture
// boundary. Current and next routes are distinct: the latter comes from the
// scheduler's actual request/result trace, never from a guessed route.
type MovementTrace struct {
	Tick                  uint32
	Slot                  pool.Handle
	X, Z                  int64
	Heading               uint16
	Speed                 int64
	VelocityX, VelocityZ  int32
	CurrentRoute          []Point
	CurrentRouteCount     uint8
	CurrentRouteStorage   [20]Point
	CurrentActive         bool
	CurrentDirty          bool
	CurrentStatus         path.Status
	CurrentStaticRevision uint64
	NextRoute             []Point
	Pending               *path.RequestTrace
	PendingGoal           path.GoalTrace
	Result                *path.Trace
	Collision             *CollisionState
	CollisionHistory      []CollisionTrace
	PathFailure           *PathFailure
}

type collisionHistoryEntry struct {
	Slot  pool.Handle
	State CollisionState
	Tick  uint32
}

const DefaultParityTraceLimit = 4096

// EnableParityTrace enables all movement-owned diagnostic storage. Before it
// is called, EndTick performs no history work and scheduler tracing remains
// disabled [04 §7.3][04 §8.2].
func (s *System) EnableParityTrace() {
	if s == nil {
		return
	}
	s.collisionTraceEnabled = true
	if s.collisionHistoryLimit <= 0 {
		s.collisionHistoryLimit = DefaultParityTraceLimit
	}
	if s.Scheduler != nil {
		s.Scheduler.EnableTrace()
	}
}

// SetParityTraceLimit bounds retained collision and scheduler diagnostics.
func (s *System) SetParityTraceLimit(limit int) {
	if s == nil {
		return
	}
	if limit < 0 {
		limit = 0
	}
	s.collisionHistoryLimit = limit
	for len(s.collisionHistory) > limit {
		s.collisionHistory = s.collisionHistory[1:]
	}
	if s.Scheduler != nil {
		s.Scheduler.SetTraceLimit(limit)
	}
}

// ResetParityTrace clears diagnostic records while leaving movement state,
// routes, queues, and collision commits untouched.
func (s *System) ResetParityTrace() {
	if s == nil {
		return
	}
	s.collisionHistory = s.collisionHistory[:0]
	s.collisionHistoryDropped = false
	if s.Scheduler != nil {
		s.Scheduler.ResetTrace()
	}
}

// ParityTraceDropped reports whether any diagnostic storage overflowed and
// dropped records, so a reader knows the trace is incomplete. It never affects
// simulation state.
func (s *System) ParityTraceDropped() bool {
	return s != nil && (s.collisionHistoryDropped || (s.Scheduler != nil && s.Scheduler.TraceDropped()))
}

func (s *System) recordCollisionHistory(tick uint32) {
	if s == nil || !s.collisionTraceEnabled || s.world == nil {
		return
	}
	// IterSliced is the authoritative player/slot order [01 §6.2]. This is
	// deliberately called only from EndTick, after all collision commits.
	for _, u := range s.world.IterSliced() {
		if u == nil || !u.Alive {
			continue
		}
		if c := handleRow(s.Collisions, u.Handle); c != nil {
			if s.collisionHistoryLimit <= 0 || len(s.collisionHistory) >= s.collisionHistoryLimit {
				s.collisionHistoryDropped = true
				if s.collisionHistoryLimit <= 0 {
					continue
				}
				s.collisionHistory = s.collisionHistory[1:]
			}
			s.collisionHistory = append(s.collisionHistory, collisionHistoryEntry{Slot: u.Handle, State: *c, Tick: tick})
		}
	}
}

// CollisionHistory returns copied committed records for one selected unit.
// It does not lazily capture, clear, or otherwise mutate movement state.
func (s *System) CollisionHistory(slot pool.Handle) []CollisionTrace {
	if s == nil || !s.collisionTraceEnabled {
		return nil
	}
	out := make([]CollisionTrace, 0)
	for _, entry := range s.collisionHistory {
		if entry.Slot == slot {
			out = append(out, CollisionTrace{Tick: entry.Tick, Slot: entry.Slot, State: entry.State})
		}
	}
	return out
}

// ParitySnapshot returns movement state in authoritative unit order. It is an
// opt-in observation surface; repeated reads are pure and do not invoke path
// search or collision validation.
func (s *System) ParitySnapshot(w *units.World, tick uint32) []MovementTrace {
	if s == nil || w == nil {
		return nil
	}
	out := make([]MovementTrace, 0)
	for _, u := range w.IterSliced() {
		if u == nil || !u.Alive {
			continue
		}
		m := MovementTrace{Tick: tick, Slot: u.Handle, X: u.X.Raw(), Z: u.Z.Raw(), Heading: u.Move.Heading, Speed: u.Move.Speed.Raw()}
		if c := handleRow(s.Collisions, u.Handle); c != nil {
			m.VelocityX, m.VelocityZ = c.VX, c.VZ
			copyState := *c
			m.Collision = &copyState
		}
		if r := handleRow(s.Routes, u.Handle); r != nil {
			m.CurrentActive, m.CurrentDirty, m.CurrentStatus, m.CurrentStaticRevision = r.Active, r.Dirty, r.Status, r.StaticRevision
			m.CurrentRouteCount = r.Count
			m.CurrentRouteStorage = r.Points
			if r.Active {
				m.CurrentRoute = append(m.CurrentRoute, r.Points[:r.Count]...)
			}
		}
		if s.Scheduler != nil {
			pending, result := s.Scheduler.TraceFor(u.Handle)
			if pending != nil {
				request := path.RequestTrace{Unit: pending.Unit, Player: pending.Player, Start: pending.Start, Goal: path.DescribeGoal(pending.Goal), Activation: pending.Activation}
				m.Pending = &request
				m.PendingGoal = request.Goal
			}
			m.Result = result
			if m.Result != nil {
				m.NextRoute = append(m.NextRoute, m.Result.Points...)
			}
		}
		if failure := handleRow(s.pathFailures, u.Handle); failure != nil {
			copyFailure := *failure
			m.PathFailure = &copyFailure
		}
		m.CollisionHistory = s.CollisionHistory(u.Handle)
		out = append(out, m)
	}
	return out
}
