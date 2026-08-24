package path

import (
	"github.com/nanolathe/nanolathe/internal/pool"
)

// DefaultBase is the stock default heuristic base scale placeholder.
// base = int(atof(setting) * 65536.0) taken once at settings-application time [04 §7.2].
// TODO(question): stock default value of the settings string that seeds base is not established [04 §7.2]; placeholder uses 1.0 (65536).
const DefaultBase int32 = 65536 // TODO(question): stock default setting value not established [04 §7.2]

// PressureDivisor converts pending per-player pressure to tier.
// tier = pending per-player pressure divided by unit-cap divisor [04 §7.2].
// TODO(question): unit-cap divisor and exact pressure counter definition not established [04 §7.2][04 §7.3]; placeholder value.
const PressureDivisor int32 = 10 // TODO(question): unit-cap divisor not established [04 §7.2][04 §7.3]

const (
	popsPerRequest    = 100 // [04 §7.3] C11 each active request limited to 100 heap pops per scheduler call
	replenishInterval = 150 // [04 §7.3] C11 global scheduler counter replenishes every 150 ticks
)

// globalBase is the package-wide heuristic base used when a Scheduler has no explicit base.
// It is set via SetBase at settings-application time [04 §7.2].
var globalBase int32 = DefaultBase

// SetBase sets the global heuristic base scale [04 §7.2].
// base = int(atof(setting) * 65536.0) taken once at settings-application time.
func SetBase(b int32) {
	globalBase = b
}

// GetBase returns the global heuristic base.
func GetBase() int32 {
	return globalBase
}

// Request is a pathfinding request [plan Public API].
type Request struct {
	Unit   pool.Handle
	Player uint8
	Start  Cell
	Goal   Goal
}

// SearchFunc is the injected path search function.
// scale is the per-player quantum (base * weight) [04 §7.2][04 §7.3] C11.
// budget is the maximum heap pops allowed this call (100) [04 §7.3] C11.
// Returns points, status, done. If done is false, budget was exhausted and the
// heap plus request remain ACTIVE with no publication [04 §7.3] C12 (full-or-empty).
// If done is true with empty points, the heap was exhausted and an empty route is published [04 §7.3] C12.
type SearchFunc func(r Request, scale int32, budget int) (points []Point, status Status, done bool)

// PublishFunc observes route publication.
// It is called only when search reports done (success or heap exhaustion), never on budget exhaustion [04 §7.3] C12.
type PublishFunc func(r Request, points []Point, status Status)

// Scheduler manages path requests with budgeted scheduling [04 §7.3] C11 C12.
// It maintains per-player queues, replenishes quanta every 150 ticks, weights
// scales 6x/3x/1x by pressure tier, and enforces 100 pops per request per call.
type Scheduler struct {
	base    int32
	baseSet bool
	search  SearchFunc
	publish PublishFunc

	queues [10][]Request
	scales [10]int32

	lastReplenish uint32
	haveLast      bool
}

// NewScheduler creates a Scheduler with the given search and publish callbacks.
// Either callback may be nil; a nil search causes Tick to do no work, a nil publish suppresses observation.
func NewScheduler(search SearchFunc, publish PublishFunc) *Scheduler {
	return &Scheduler{
		search:  search,
		publish: publish,
	}
}

// SetBase sets the heuristic base for this scheduler [04 §7.2].
// If not set, the global base (SetBase) is used.
func (s *Scheduler) SetBase(b int32) {
	s.base = b
	s.baseSet = true
}

// SetSearch sets the injected search function.
func (s *Scheduler) SetSearch(fn SearchFunc) {
	s.search = fn
}

// SetPublish sets the publish callback.
func (s *Scheduler) SetPublish(fn PublishFunc) {
	s.publish = fn
}

// ScaleFor reports the current per-player scale quantum [04 §7.2][04 §7.3].
func (s *Scheduler) ScaleFor(player uint8) int32 {
	if int(player) >= 10 {
		return 0
	}
	if !s.haveLast {
		return s.effectiveBase() * 6
	}
	return s.scales[player]
}

// Pending reports the number of pending requests for the player.
func (s *Scheduler) Pending(player uint8) int {
	if int(player) >= 10 {
		return 0
	}
	return len(s.queues[player])
}

// TotalPending reports total pending requests across all players.
func (s *Scheduler) TotalPending() int {
	n := 0
	for p := 0; p < 10; p++ {
		n += len(s.queues[p])
	}
	return n
}

func (s *Scheduler) effectiveBase() int32 {
	if s.baseSet {
		return s.base
	}
	return globalBase
}

func (s *Scheduler) replenish() {
	base := s.effectiveBase()
	for p := 0; p < 10; p++ {
		pending := int32(len(s.queues[p])) // TODO(question): pressure counter definition not established [04 §7.3]; using pending count as placeholder
		tier := pending / PressureDivisor  // [04 §7.2] tier = pending per-player pressure divided by unit-cap divisor
		var weight int32
		switch {
		case tier < 1:
			weight = 6 // [04 §7.2][04 §7.3] per-player quanta 6x when pressure below 1
		case tier < 2:
			weight = 3 // [04 §7.2][04 §7.3] 3x when below 2
		default:
			weight = 1 // [04 §7.2][04 §7.3] 1x when >=2
		}
		s.scales[p] = base * weight // [04 §7.2] scale = base * {6,3,1}
	}
}

func (s *Scheduler) insertSorted(player int, r Request) {
	q := s.queues[player]
	idx := len(q)
	for i, existing := range q {
		if r.Unit < existing.Unit {
			idx = i
			break
		}
	}
	q = append(q, Request{})
	copy(q[idx+1:], q[idx:])
	q[idx] = r
	s.queues[player] = q
}

// Submit enqueues a request. Duplicate Unit overwrites the existing request's goal [plan API note][04 §7.3] C12.
func (s *Scheduler) Submit(r Request) {
	if int(r.Player) >= 10 {
		return
	}
	for p := 0; p < 10; p++ {
		for i, q := range s.queues[p] {
			if q.Unit == r.Unit {
				if p != int(r.Player) {
					s.queues[p] = append(s.queues[p][:i], s.queues[p][i+1:]...)
					s.insertSorted(int(r.Player), r)
				} else {
					s.queues[p][i].Goal = r.Goal
					s.queues[p][i].Start = r.Start
				}
				return
			}
		}
	}
	s.insertSorted(int(r.Player), r)
}

// Tick performs budgeted scheduling for the given tick [04 §7.3] C11 C12.
// The global counter replenishes every 150 ticks; per-player scales are recomputed then [04 §7.2][04 §7.3].
// Each active request is limited to 100 heap pops per call, passed as budget to the injected search [04 §7.3] C11.
// Requests are full-or-empty: budget exhaustion leaves heap+request ACTIVE without publication;
// heap exhaustion publishes an empty route [04 §7.3] C12.
func (s *Scheduler) Tick(tick uint32) {
	if s == nil {
		return
	}
	if !s.haveLast {
		s.replenish()
		s.lastReplenish = tick
		s.haveLast = true
	} else if tick-s.lastReplenish >= replenishInterval {
		s.replenish()
		s.lastReplenish = tick
	}
	if s.search == nil {
		return
	}
	for player := 0; player < 10; player++ {
		scale := s.scales[player]
		idx := 0
		for idx < len(s.queues[player]) {
			req := s.queues[player][idx]
			points, status, done := s.search(req, scale, popsPerRequest)
			if !done {
				idx++
				continue
			}
			if s.publish != nil {
				s.publish(req, points, status)
			}
			copy(s.queues[player][idx:], s.queues[player][idx+1:])
			s.queues[player] = s.queues[player][:len(s.queues[player])-1]
		}
	}
}
