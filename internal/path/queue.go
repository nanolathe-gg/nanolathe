package path

import (
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// DefaultBase is the compiled-in heuristic base [04 R-PATH-01 §10].
const DefaultBase int32 = 0x18000

const (
	popsPerRequest      = 100  // [04 §7.3] C11 each active request limited to 100 heap pops per scheduler call
	replenishInterval   = 150  // [04 §7.3] C11 the global scheduler counter rebuilds the quanta every 150 scheduler calls
	retailStepAllowance = 1333 // [04 R-PATH-01 §10]
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

// tickCandidateProvider is an optional timing boundary for providers whose
// live candidate state includes the scheduler tick. Keeping it optional
// preserves the public CandidateProvider surface used by standalone schedulers.
type tickCandidateProvider interface {
	SetPathTick(tick uint32)
}

// idleCandidateProvider is an optional batching boundary. A provider that can
// tell, without polling, that a player's next polls will be idle lets the
// scheduler charge a whole block of them at once. It changes the cost of the
// admission loop and nothing it computes: see skipIdleRounds.
type idleCandidateProvider interface {
	// IdleRun reports how many of the player's next polls, up to limit, are
	// certain to admit nothing and change nothing but the provider's own
	// cursor for that player. It may under-report; it must never over-report.
	IdleRun(player int, limit int32) int32
	// SkipIdle performs n polls that IdleRun reported idle.
	SkipIdle(player int, n int32)
}

// PollResult is what one poll of a player's candidate provider produced.
type PollResult uint8

const (
	// PollNoUnit means the player has no candidate to offer this visit.
	PollNoUnit PollResult = iota
	// PollVisited means a candidate was inspected but wanted no route.
	PollVisited
	// PollRequest means the accompanying request is ready to search.
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
	// workspace is the search's per-cell table, owned here because the
	// scheduler is what decides which search is running: it latches one
	// request at a time [04 R-PATH-01 §6] and keeps it until that search
	// reports done. Handing the table out from here is what makes a shared
	// generation-stamped table safe -- a second search cannot be given the
	// table the first is still using, it is refused and keeps its own map.
	workspace    Workspace
	traceEnabled bool
	traces       []Trace
	traceLimit   int
	traceDropped bool
}

// Workspace returns the scheduler's per-cell search table for a SearchConfig
// to offer to the session it opens. It is storage and never behaviour: a
// session that is refused it produces the same visit order and the same route
// [04 §7.2].
func (s *Scheduler) Workspace() *Workspace {
	if s == nil {
		return nil
	}
	return &s.workspace
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
	Base      int32
	BaseSet   bool
	HaveLast  bool
	CallCount uint32
	Scales    [10]int32
	Pending   [10]int
	Requests  []RequestTrace
}

// TraceState returns a copy of the live scheduler fields exposed to diagnostics.
// It is available whether or not optional history capture is enabled; observing
// it does not advance the scheduler or retain a trace record.
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
		stepAllowance: retailStepAllowance,
	}
}

// SetStepAllowance fixes the per-call path work allowance selected at battle
// entry. Zero restores retail's 1333 steps; positive values through MaxInt32
// are accepted. Invalid values leave the current allowance unchanged
// [04 R-PATH-01 §10][CP-LIM-2].
func (s *Scheduler) SetStepAllowance(allowance int) {
	if s == nil {
		return
	}
	if allowance == 0 {
		s.stepAllowance = retailStepAllowance
		return
	}
	if allowance > 0 && int64(allowance) <= int64(1<<31-1) {
		s.stepAllowance = int32(allowance)
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

// CurrentRequest returns the admitted search, independently of optional
// history tracing. A staged provider request has not reached this boundary.
func (s *Scheduler) CurrentRequest(unit pool.Handle) *Request {
	if s == nil || s.active == nil || s.active.Unit != unit {
		return nil
	}
	copy := *s.active
	return &copy
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
	if p, ok := s.provider.(tickCandidateProvider); ok {
		p.SetPathTick(tick)
	}
	s.callCount++
	// The counter is incremented first and the rebuild fires on the call whose
	// incremented value REACHES the interval, so the period is exactly
	// replenishInterval calls [04 §7.3]. A strict `>` here made it 151.
	if s.callCount >= replenishInterval {
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
				// A budget-exhausted slice ends the ITERATION, not the call.
				// [04 R-PATH-01 §6]: "a single long search consumes 100-step
				// slices from its own player's accumulator until that
				// accumulator goes non-positive, at which point the loop ends
				// for the tick with the request still latched, and resumes
				// next tick." The loop guard above and `total` are what end it;
				// breaking here capped the whole session at one 100-pop slice
				// per tick instead of the ~`stepAllowance / playerCount` steps
				// the equal share buys, so a search costing a few thousand pops
				// held the single global working set for tens of ticks and
				// every other mover's request queued behind it. Followers that
				// re-arm on the blocked bit then waited far longer than the
				// 60-tick throttle of [04 R-MOV-01 §7] for the route that frees
				// them, which is the "at most one throttle period per stage"
				// jostle of [04 R-PATH-01 §14] item 2 turning into a stall of
				// hundreds of ticks.
				s.accumulator[s.activePlayer] -= charge
				total -= charge
				// Termination rests on every iteration charging something. A
				// live heap always pops, so a continuation that reports no
				// setup steps and no pops without finishing is a search that
				// made no progress; end the call rather than spin on it. This
				// is the same outcome a budget boundary produces — the request
				// stays latched and resumes next call.
				if charge <= 0 {
					break
				}
				continue
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
		if idle, ok := s.provider.(idleCandidateProvider); ok {
			total = s.skipIdleRounds(idle, total)
			if total <= 0 {
				break
			}
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

// skipIdleRounds charges whole rounds of idle polls at once and returns the
// remaining total.
//
// With no request latched, each iteration of the admission loop visits the
// next player in cursor order whose accumulator is at least one and polls one
// of its candidates; an idle poll charges one unit to that player's service
// count, its accumulator and the total, and moves only that player's cursor.
// The players the loop visits, and their order, stay the same from round to
// round for as long as every poll is idle, every accumulator stays positive and
// the total stays positive: nothing in an idle poll can change eligibility or
// raise an accumulator. So m such rounds end in exactly the state the loop
// reaches by polling them one at a time -- each visited player's cursor m
// polls on, its service count m higher and its accumulator m lower, the total
// m times the round's size lower, and the player cursor just past the round's
// last member. m is bounded so that every one of those polls happens with the
// total and the polled player's accumulator still positive, and so that none
// of them reaches a poll the provider cannot vouch for; the loop takes the
// next poll itself. Under a large step allowance almost every poll is idle,
// and this is what keeps the loop's cost proportional to the requests it
// finds rather than to the allowance [04 R-PATH-01 §6].
func (s *Scheduler) skipIdleRounds(idle idleCandidateProvider, total int32) int32 {
	var round [10]int
	n := 0
	for k := 0; k < 10; k++ {
		p := (s.playerCursor + k) % 10
		if s.accumulator[p] >= 1 && s.provider.Eligible(p) {
			round[n] = p
			n++
		}
	}
	if n == 0 {
		return total
	}
	m := total / int32(n)
	for _, p := range round[:n] {
		m = min(m, s.accumulator[p])
	}
	for _, p := range round[:n] {
		if m <= 0 {
			return total
		}
		m = min(m, idle.IdleRun(p, m))
	}
	if m <= 0 {
		return total
	}
	for _, p := range round[:n] {
		idle.SkipIdle(p, m)
		s.serviceCount[p] += m
		s.accumulator[p] -= m
	}
	s.playerCursor = (round[n-1] + 1) % 10
	return total - m*int32(n)
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
