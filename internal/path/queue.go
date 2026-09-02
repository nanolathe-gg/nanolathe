package path

import (
	"github.com/nanolathe/nanolathe/internal/pool"
)

// DefaultBase is the compiled-in heuristic base [04 R-PATH-01 §10].
const DefaultBase int32 = 0x18000

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

// WorkResult is one scheduler work slice. SetupSteps is nonzero only on the
// admission slice; Pops is the number of heap entries actually removed. The
// scheduler charges exactly these reported units [04 R-PATH-01 §5–§6].
type WorkResult struct {
	Points     []Point
	Status     Status
	Done       bool
	SetupSteps int
	Pops       int
}

// SearchFunc is the exact scheduler search boundary. It owns one request's
// resumable search and reports setup-ray work and actual heap pops [04
// R-PATH-01 §5–§7].
// scale is the per-player quantum (base * weight) [04 §7.2][04 §7.3] C11.
// budget is the maximum heap pops allowed this call (100) [04 §7.3] C11.
// Returns points, status, done. If done is false, budget was exhausted and the
// heap plus request remain ACTIVE with no publication [04 §7.3] C12 (full-or-empty).
// If done is true with empty points, the heap was exhausted and an empty route is published [04 §7.3] C12.
type SearchFunc func(r Request, scale int32, budget int) WorkResult

// PublishFunc observes route publication.
// It is called only when search reports done (success or heap exhaustion), never on budget exhaustion [04 §7.3] C12.
type PublishFunc func(r Request, points []Point, status Status)

// CandidateProvider supplies the established scheduler admission surface. It
// owns stable per-player cursor advancement and eligibility; path never
// derives either value from queued requests [04 R-PATH-01 §6].
type CandidateProvider interface {
	PlayerCount() int
	UnitLimit() int32
	Eligible(player int) bool
	Poll(player int) (Request, PollResult)
}

// mutableCandidateProvider is the scheduler-owned cancellation/inspection
// extension used by the movement follower. An admitted request no longer
// resides in CandidateProvider, so Scheduler must include its active request
// in both operations [04 R-PATH-01 §8].
type mutableCandidateProvider interface {
	CandidateProvider
	Cancel(unit pool.Handle) bool
	HasRequest(unit pool.Handle) bool
}

type PollResult uint8

const (
	PollNoUnit PollResult = iota
	PollVisited
	PollRequest
)

// Scheduler is the deterministic single-active-request scheduler [04 R-PATH-01
// §6–§7].
type Scheduler struct {
	base    int32
	baseSet bool
	search  SearchFunc
	publish PublishFunc

	scales [10]int32

	callCount uint32
	haveLast  bool // false preserves the constructor's initial 6x quantum
	active    *Request
	// activeReq is the storage `active` points at. The admission loop used to
	// take the address of a loop-local request, which makes Go's escape
	// analysis heap-allocate that local on *every* poll iteration — including
	// the overwhelming majority that reject the candidate and never admit it.
	// At roughly a thousand polls a tick that was three quarters of the whole
	// run's allocated objects and a large share of its GC time. Pointing at a
	// field of the scheduler is behaviourally identical: there is one active
	// request at a time [04 R-PATH-01 §6], it is cleared to nil on completion
	// or cancellation, and no caller retains the pointer across an admission —
	// TraceState and TraceFor both copy through it immediately.
	activeReq     Request
	activePlayer  int
	activeScale   int32
	playerCursor  int
	serviceCount  [10]int32
	accumulator   [10]int32
	stepAllowance int32
	unitLimit     int32
	playerCount   int
	provider      CandidateProvider
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
	CallCount     uint32
	Scales        [10]int32
	Pending       [10]int
	Requests      []RequestTrace
}

func (s *Scheduler) TraceState() SchedulerTraceState {
	var out SchedulerTraceState
	if s == nil {
		return out
	}
	out.Base, out.BaseSet, out.HaveLast, out.CallCount, out.Scales = s.base, s.baseSet, s.haveLast, s.callCount, s.scales
	if s.active != nil {
		out.Pending[s.activePlayer]++
		request := *s.active
		out.Requests = append(out.Requests, RequestTrace{Unit: request.Unit, Player: request.Player, Start: request.Start, Goal: DescribeGoal(request.Goal), Activation: request.Activation})
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
	case *airWorkGoal:
		if g == nil {
			return GoalTrace{Unknown: true}
		}
		return GoalTrace{Kind: 4}
	case *airMovingGoal:
		if g == nil {
			return GoalTrace{Unknown: true}
		}
		return GoalTrace{Kind: 5}
	default:
		// Unknown external Goal implementations are retained as an explicit
		// residual; no pointer formatting or guessed parameters enter a
		// diagnostic hash.
		// This is not a retail unknown: the three A*-facing goal families are
		// closed and exhaustive [04 R-PATH-01 §9], and all three are typed
		// above. A Goal implemented outside this package has no parameter
		// encoding path knows, so it hashes as an explicit typed residual
		// rather than through pointer formatting or guessed parameters. An
		// external implementer that wants a stable trace supplies its own
		// adapter.
		return GoalTrace{Unknown: true}
	}
}

// NewScheduler creates a Scheduler with the given search and publish callbacks.
// Either callback may be nil; a nil search causes Tick to do no work, a nil publish suppresses observation.
func NewScheduler(search SearchFunc, publish PublishFunc) *Scheduler {
	return &Scheduler{
		search:        search,
		publish:       publish,
		stepAllowance: 1333, // [04 R-PATH-01 §10]
	}
}

// SetUnitLimit supplies the required lobby per-player unit limit used by the
// quantum tier divisor [04 R-PATH-01 §6]. Until set, scheduling is inert.
func (s *Scheduler) SetUnitLimit(limit int32) {
	if limit > 0 {
		s.unitLimit = limit
	}
}

// SetPlayerCount supplies the session player count for equal-share work
// replenishment [04 R-PATH-01 §6]. Zero means no eligible players.
func (s *Scheduler) SetPlayerCount(count int) {
	if count >= 0 && count <= 10 {
		s.playerCount = count
	}
}

// SetCandidateProvider binds the production candidate poll and explicit
// session limits. A nil provider disables provider polling.
func (s *Scheduler) SetCandidateProvider(p CandidateProvider) {
	s.provider = p
	if p != nil {
		s.playerCount = p.PlayerCount()
		s.SetUnitLimit(p.UnitLimit())
	}
}

// HasRequest reports queued or active work for unit.
func (s *Scheduler) HasRequest(unit pool.Handle) bool {
	if s == nil {
		return false
	}
	if s.active != nil && s.active.Unit == unit {
		return true
	}
	p, ok := s.provider.(mutableCandidateProvider)
	return ok && p.HasRequest(unit)
}

// Cancel removes queued work or releases the single active working set owned
// by unit [04 R-PATH-01 §8].
func (s *Scheduler) Cancel(unit pool.Handle) bool {
	if s == nil {
		return false
	}
	if s.active != nil && s.active.Unit == unit {
		s.active = nil
		return true
	}
	p, ok := s.provider.(mutableCandidateProvider)
	return ok && p.Cancel(unit)
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

// ResetTrace clears diagnostic request/result records without touching provider state,
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
	if s.unitLimit <= 0 {
		return 0
	}
	if !s.haveLast {
		return s.effectiveBase() * 6
	}
	return s.scales[player]
}

func (s *Scheduler) effectiveBase() int32 {
	if s.baseSet {
		return s.base
	}
	return DefaultBase // [04 R-PATH-01 §10]
}

func (s *Scheduler) replenish() bool {
	if s.unitLimit <= 0 {
		return false
	}
	base := s.effectiveBase()
	for p := 0; p < 10; p++ {
		tier := s.serviceCount[p] / s.unitLimit // [04 R-PATH-01 §6]
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
		s.serviceCount[p] = 0
	}
	return true
}

// Tick performs one scheduler call. There is one active request for the whole
// session; a live request receives a 100-pop continuation and no other request
// may be admitted until it publishes [04 R-PATH-01 §6–§7].
func (s *Scheduler) Tick(tick uint32) {
	if s == nil {
		return
	}
	players := s.playerCount
	// Player count is the first admission gate. An empty/invalid session must
	// not advance cadence or trigger a replenishment [04 R-PATH-01 §6].
	if players <= 0 || players > 10 {
		return
	}
	if s.unitLimit <= 0 {
		return
	}
	s.callCount++
	if s.callCount > replenishInterval {
		s.callCount = 0
		if !s.replenish() {
			return
		}
		s.haveLast = true
	}
	if s.search == nil {
		return
	}
	share := s.stepAllowance / int32(players)
	total := int32(0)
	for p := 0; p < 10; p++ {
		// An admitted request is removed from the provider while the global
		// working set owns it. Its player's accumulator must still receive this
		// tick's equal share or a search that outlives the previously accumulated
		// work can never resume [04 R-PATH-01 §6].
		activePlayer := s.active != nil && s.activePlayer == p
		if activePlayer || s.provider != nil && s.provider.Eligible(p) {
			s.accumulator[p] += share
		}
		total += s.accumulator[p]
	}
	for total > 0 {
		if s.active != nil {
			if s.accumulator[s.activePlayer] <= 0 {
				break
			}
			req := *s.active
			work := s.search(req, s.activeScale, popsPerRequest)
			points, status, done := work.Points, work.Status, work.Done
			charge := int32(work.SetupSteps + work.Pops)
			if s.traceEnabled {
				s.recordTrace(Trace{Request: RequestTrace{Unit: req.Unit, Player: req.Player, Start: req.Start, Goal: DescribeGoal(req.Goal), Activation: req.Activation}, Goal: DescribeGoal(req.Goal), Points: append([]Point(nil), points...), Status: status, Done: done, Tick: tick})
			}
			if !done {
				s.accumulator[s.activePlayer] -= charge
				total -= charge
				break
			}
			s.accumulator[s.activePlayer] -= charge
			total -= charge
			if s.publish != nil {
				s.publish(req, points, status)
			}
			s.active = nil
			// Heap exhaustion itself has no charge [04 R-PATH-01 §6].
			continue
		}
		player := s.nextPlayer()
		if player < 0 {
			break
		}
		var req Request
		var poll PollResult
		if s.provider != nil {
			if !s.provider.Eligible(player) {
				s.accumulator[player] = 0
				continue
			}
			req, poll = s.provider.Poll(player)
		}
		s.serviceCount[player]++ // every visited candidate consumes one poll [04 R-PATH-01 §6]
		s.accumulator[player]--
		total--
		if poll != PollRequest || s.accumulator[player] < 0 {
			continue
		}
		s.activeReq = req
		s.active, s.activePlayer = &s.activeReq, player
		s.activeScale = s.ScaleFor(uint8(player))
		// Admission initializes the request and ray only. Heap pops begin in
		// the active continuation below when the remaining allowance permits.
		work := s.search(req, s.activeScale, 0)
		points, status, done := work.Points, work.Status, work.Done
		charge := int32(100 + work.SetupSteps + work.Pops)
		if s.traceEnabled {
			s.recordTrace(Trace{Request: RequestTrace{Unit: req.Unit, Player: req.Player, Start: req.Start, Goal: DescribeGoal(req.Goal), Activation: req.Activation}, Goal: DescribeGoal(req.Goal), Points: append([]Point(nil), points...), Status: status, Done: done, Tick: tick})
		}
		// Admission consumes the fixed 100-unit setup charge, then exact ray
		// setup steps and actual heap pops [04 R-PATH-01 §6].
		s.accumulator[player] -= charge
		total -= charge
		if done {
			if s.publish != nil {
				s.publish(req, points, status)
			}
			s.active = nil
		} else if total <= 0 {
			break
		}
	}
}

func (s *Scheduler) nextPlayer() int {
	for n := 0; n < 10; n++ {
		p := (s.playerCursor + n) % 10
		if s.accumulator[p] < 1 {
			continue
		}
		if s.provider != nil {
			if s.provider.Eligible(p) {
				s.playerCursor = (p + 1) % 10
				return p
			}
			continue
		}
	}
	return -1
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
	if pending == nil && s.active != nil && s.active.Unit == unit {
		copy := *s.active
		pending = &copy
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
