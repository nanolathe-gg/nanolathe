package path

import (
	"github.com/nanolathe/nanolathe/internal/pool"
)

// DefaultBase is the stock default heuristic base scale placeholder [P0-13].
// base = int(atof(setting) * 65536.0) taken once at settings-application time [04 §7.2][P0-13].
// TODO(question): stock default value of the settings string that seeds base is not established [04 §7.2][P0-13]; placeholder uses 1.0 (65536).
const DefaultBase int32 = 65536 // TODO(question): stock default setting value not established [04 §7.2][P0-13]

// PressureDivisor converts pending per-player pressure to tier [P0-13].
// tier = pending per-player pressure divided by unit-cap divisor [04 §7.2][P0-13].
// TODO(question): unit-cap divisor and exact pressure counter definition not established [04 §7.2][04 §7.3][P0-13]; placeholder value.
const PressureDivisor int32 = 10 // TODO(question): unit-cap divisor not established [04 §7.2][04 §7.3][P0-13]

const (
	popsPerRequest    = 100 // [04 §7.3] C11 each active request limited to 100 heap pops per scheduler call
	replenishInterval = 150 // [04 §7.3] C11 global scheduler counter replenishes every 150 ticks
)

// Request is a pathfinding request [plan Public API].
type Request struct {
	Unit   pool.Handle
	Player uint8
	Start  Cell
	Goal   Goal
	// Activation identifies the order/path boundary that created this
	// request. Zero is reserved for direct callers that do not bind an order;
	// session movement uses a monotonically increasing token so a late result
	// from a canceled/replanned request cannot publish onto a new head.
	Activation uint64
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
	traceEnabled  bool
	traces        []Trace
	traceLimit    int
	traceDropped  bool
}

// Trace is an opt-in copy of a request's scheduler boundary. Points are
// copied when the search returns, so reading it cannot observe or mutate the
// searcher's working storage [04 §7.3].
type Trace struct {
	Request RequestTrace
	Goal    GoalTrace
	Points  []Point
	Status  Status
	Done    bool
	Tick    uint32
}

// RequestTrace is a value-only request copy. The Goal interface is replaced by
// GoalTrace so pointer identity or an implementation's private fields cannot
// enter a deterministic report or hash.
type RequestTrace struct {
	Unit       pool.Handle
	Player     uint8
	Start      Cell
	Goal       GoalTrace
	Activation uint64
}

// GoalTrace is the value-only description of the goal parameters used by a
// request. Keeping it typed avoids hashing interface or pointer identity.
type GoalTrace struct {
	Kind    uint8
	Unknown bool
	Center  Cell
	A, B    int32
	Rect    Rect
	Cells   []Cell
}

// SchedulerTraceState exposes timing/quanta gates as values for deterministic
// diagnostics. It never exposes the mutable queue backing arrays.
type SchedulerTraceState struct {
	Base          int32
	BaseSet       bool
	LastReplenish uint32
	HaveLast      bool
	Scales        [10]int32
	Pending       [10]int
	Requests      []RequestTrace
}

func (s *Scheduler) TraceState() SchedulerTraceState {
	var out SchedulerTraceState
	if s == nil {
		return out
	}
	out.Base, out.BaseSet, out.LastReplenish, out.HaveLast, out.Scales = s.base, s.baseSet, s.lastReplenish, s.haveLast, s.scales
	for player := 0; player < 10; player++ {
		out.Pending[player] = len(s.queues[player])
		for _, request := range s.queues[player] {
			out.Requests = append(out.Requests, RequestTrace{Unit: request.Unit, Player: request.Player, Start: request.Start, Goal: DescribeGoal(request.Goal), Activation: request.Activation})
		}
	}
	return out
}

// DefaultTraceLimit bounds opt-in diagnostic retention. It is a report
// resource limit, not simulation behavior; callers may choose a smaller limit.
const DefaultTraceLimit = 4096

// DescribeGoal returns the value-only parameters of a built-in goal.
func DescribeGoal(goal Goal) GoalTrace {
	switch g := goal.(type) {
	case *pointGoal:
		if g == nil {
			return GoalTrace{Unknown: true}
		}
		return GoalTrace{Kind: 1, Center: g.center, A: g.radius}
	case *annulusGoal:
		if g == nil {
			return GoalTrace{Unknown: true}
		}
		return GoalTrace{Kind: 2, Center: g.center, A: g.inner, B: g.outer}
	case *rectGoal:
		if g == nil {
			return GoalTrace{Unknown: true}
		}
		return GoalTrace{Kind: 3, Rect: g.rect}
	case *savedGoal:
		if g == nil {
			return GoalTrace{Unknown: true}
		}
		return GoalTrace{Kind: 4, Cells: append([]Cell(nil), g.cells...)}
	default:
		// Unknown external Goal implementations are retained as an explicit
		// residual; no pointer formatting or guessed parameters enter a
		// diagnostic hash.
		// TODO(question): external Goal parameter encoding is not owned by path;
		// the typed residual remains Unknown until its owner supplies an adapter.
		return GoalTrace{Unknown: true}
	}
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
// If not set, the established default base is used.
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

// EnableTrace enables deterministic request/result diagnostics. The default
// scheduler has no trace storage and therefore does not allocate or change
// dispatch behavior [04 §7.3][I6].
func (s *Scheduler) EnableTrace() {
	if s == nil {
		return
	}
	s.traceEnabled = true
	if s.traceLimit <= 0 {
		s.traceLimit = DefaultTraceLimit
	}
	if s.traces == nil {
		s.traces = make([]Trace, 0, minTraceCapacity(s.traceLimit))
	}
}

func minTraceCapacity(limit int) int {
	if limit < 8 {
		return limit
	}
	return 8
}

// SetTraceLimit bounds future scheduler trace records. Existing records are
// retained up to the new limit and the oldest records are discarded first.
func (s *Scheduler) SetTraceLimit(limit int) {
	if s == nil {
		return
	}
	if limit < 0 {
		limit = 0
	}
	s.traceLimit = limit
	for len(s.traces) > limit {
		s.traces = s.traces[1:]
	}
}

// ResetTrace clears diagnostic request/result records without touching queues,
// search state, or dispatch counters.
func (s *Scheduler) ResetTrace() {
	if s == nil {
		return
	}
	s.traces = s.traces[:0]
	s.traceDropped = false
}

// TraceDropped reports whether the retention limit discarded a record.
func (s *Scheduler) TraceDropped() bool { return s != nil && s.traceDropped }

// TraceEnabled reports whether request/result capture is active.
func (s *Scheduler) TraceEnabled() bool { return s != nil && s.traceEnabled }

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
	return DefaultBase // P0-I16: no longer falls back to mutable global
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
					s.queues[p][i].Activation = r.Activation
				}
				return
			}
		}
	}
	s.insertSorted(int(r.Player), r)
}

// HasRequest reports whether a request for the unit is pending [P0-I03].
func (s *Scheduler) HasRequest(unit pool.Handle) bool {
	if s == nil {
		return false
	}
	for p := 0; p < 10; p++ {
		for _, q := range s.queues[p] {
			if q.Unit == unit {
				return true
			}
		}
	}
	return false
}

// Cancel removes the pending request for the unit, if any [P0-I03].
func (s *Scheduler) Cancel(unit pool.Handle) bool {
	if s == nil {
		return false
	}
	for p := 0; p < 10; p++ {
		for i, q := range s.queues[p] {
			if q.Unit == unit {
				s.queues[p] = append(s.queues[p][:i], s.queues[p][i+1:]...)
				return true
			}
		}
	}
	return false
}

// Tick performs budgeted scheduling for the given tick [04 §7.3] C11 C12.
// The global counter replenishes every 150 ticks; per-player scales are recomputed then [04 §7.2][04 §7.3].
// Each active request is limited to 100 heap pops per call, passed as budget to the injected search [04 §7.3] C11.
// Requests are full-or-empty: budget exhaustion leaves heap+request ACTIVE without publication;
// heap exhaustion publishes an empty route [04 §7.3] C12.
//
// TODO(question): the decompile shows ONE globally active search
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// over deficit counters (notes/movement/06_path_search.md §11.2); this
// scheduler services every pending request of all ten players each Tick.
// Deterministic and plan-compatible, but a different dispatch order under
// load until a probe settles it.
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
			if s.traceEnabled {
				s.recordTrace(Trace{Request: RequestTrace{Unit: req.Unit, Player: req.Player, Start: req.Start, Goal: DescribeGoal(req.Goal), Activation: req.Activation}, Goal: DescribeGoal(req.Goal), Points: append([]Point(nil), points...), Status: status, Done: done, Tick: tick})
			}
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

func (s *Scheduler) recordTrace(next Trace) {
	for i := range s.traces {
		if s.traces[i].Request.Unit == next.Request.Unit {
			s.traces[i] = next
			return
		}
	}
	if s.traceLimit <= 0 || len(s.traces) >= s.traceLimit {
		s.traceDropped = true
		return
	}
	s.traces = append(s.traces, next)
}

// TraceFor returns copied pending and most recent request/result state. It
// scans the already ordered player queues and trace slice; reads never mutate
// scheduler state or advance a search [04 §7.3][I1].
func (s *Scheduler) TraceFor(unit pool.Handle) (pending *Request, result *Trace) {
	if s == nil || !s.traceEnabled {
		return nil, nil
	}
	for player := 0; player < 10; player++ {
		for i := range s.queues[player] {
			if s.queues[player][i].Unit == unit {
				copy := s.queues[player][i]
				pending = &copy
				break
			}
		}
	}
	for i := range s.traces {
		if s.traces[i].Request.Unit != unit {
			continue
		}
		copy := s.traces[i]
		copy.Points = append([]Point(nil), copy.Points...)
		copy.Goal.Cells = append([]Cell(nil), copy.Goal.Cells...)
		result = &copy
		break
	}
	return pending, result
}

// AllRequests returns a deterministic snapshot of all pending requests [04 §7.3][P0-I11][I1].
func (s *Scheduler) AllRequests() []Request {
	if s == nil {
		return nil
	}
	var out []Request
	for p := 0; p < 10; p++ {
		out = append(out, s.queues[p]...)
	}
	return out
}
