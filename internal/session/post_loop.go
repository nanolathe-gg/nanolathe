package session

// PostLoopHooks are the seams for the three outer executor structures. The
// retail image establishes their order but leaves the deadline-ring owner and
// pending-record payload outside this package's current model [01 §4.4][01
// §6.2]. Nil hooks are explicit no-ops for the single-thread runtime.
type PostLoopHooks struct {
	Barrier           func(index int, lastTick uint32)
	SlideDeadlineRing func(lastTick uint32)
	CompactPending    func(lastTick uint32)
}

// TODO(question): which subsystem owns deadline records and pending-expiry
// callbacks, and what remaining payload do those records carry? Keep
// production ownership behind these hooks; only the established minimal
// deadline/callback behavior is represented here.

type postLoopState struct {
	hooks            PostLoopHooks
	trace            []string
	publicationCount uint32
	ring             deadlineRing
	pending          pendingList
	eyeballs         eyeballList // temporary-sight records [01 R-PLAT-02 §5]
}

func postLoopStateFor(s *Session) *postLoopState {
	if s == nil {
		return nil
	}
	if s.postLoop == nil {
		s.postLoop = &postLoopState{}
	}
	return s.postLoop
}

// SetPostLoopHooks installs the optional network/runtime callbacks used by the
// post-loop tail. It is intentionally separate from authoritative phase
// registration: all hooks run after the full catch-up batch [01 §4.4].
func (s *Session) SetPostLoopHooks(hooks PostLoopHooks) {
	state := postLoopStateFor(s)
	if state == nil {
		return
	}
	state.hooks = hooks
}

// PostLoopTrace returns the ordered outer-tail events since the session was
// first used. The trace is diagnostic only and never enters phaseTrace, so it
// cannot make the twelve-phase registry appear to contain outer work [I6].
func (s *Session) PostLoopTrace() []string {
	state := postLoopStateFor(s)
	if state == nil {
		return nil
	}
	return append([]string(nil), state.trace...)
}

// PublicationCount reports successful per-subtick publication boundaries
// observed by Session.Step. It is diagnostic state only and remains separate
// from the phase trace [03 §2.4][I6].
func (s *Session) PublicationCount() uint32 {
	state := postLoopStateFor(s)
	if state == nil {
		return 0
	}
	return state.publicationCount
}

func (s *Session) recordPublication(tick uint32) {
	state := postLoopStateFor(s)
	if state == nil || s.Snapshot == nil {
		return
	}
	if current := s.Snapshot.Current(); current != nil && current.Tick == tick {
		state.publicationCount++
	}
}

func (s *Session) runRetailPostLoopTail(lastTick uint32) {
	state := postLoopStateFor(s)
	if state == nil {
		return
	}
	state.barrierOne(lastTick)
	state.barrierTwo(lastTick)
	state.barrierThree(lastTick)
	state.trace = append(state.trace, "deadline-ring-slide")
	state.ring.slide(lastTick)
	if state.hooks.SlideDeadlineRing != nil {
		state.hooks.SlideDeadlineRing(lastTick)
	}
	state.trace = append(state.trace, "pending-compact")
	state.pending.compact(lastTick)
	if state.hooks.CompactPending != nil {
		state.hooks.CompactPending(lastTick)
	}
	// The temporary-sight expiry pass is the tail's last step, after the
	// deadline-ring slide [03 R-COMP-02 §2][01 R-PLAT-02 §5].
	state.trace = append(state.trace, "eyeball-expire")
	state.eyeballs.expire(s.Vis, lastTick)
}

func (s *postLoopState) barrierOne(lastTick uint32) {
	s.trace = append(s.trace, "barrier-1")
	if s.hooks.Barrier != nil {
		s.hooks.Barrier(1, lastTick)
	}
}

func (s *postLoopState) barrierTwo(lastTick uint32) {
	s.trace = append(s.trace, "barrier-2")
	if s.hooks.Barrier != nil {
		s.hooks.Barrier(2, lastTick)
	}
}

func (s *postLoopState) barrierThree(lastTick uint32) {
	s.trace = append(s.trace, "barrier-3")
	if s.hooks.Barrier != nil {
		s.hooks.Barrier(3, lastTick)
	}
}

const deadlineRingSize = 30

// deadlineRing is the fixed-size outer deadline window. count and head are
// container bookkeeping, not claims about the unresolved retail record
// payload or owner. Each stored value is an already-resolved effective
// deadline (record value plus its window offset); the executor advances while
// the head is due and leaves record production behind the owner seam [01 §6.2].
type deadlineRing struct {
	count             uint8
	head              uint8
	effectiveDeadline [deadlineRingSize]uint32
}

func (r *deadlineRing) slide(lastTick uint32) {
	if r == nil || r.count == 0 {
		return
	}
	for r.count > 0 && r.effectiveDeadline[r.head] < lastTick {
		r.effectiveDeadline[r.head] = 0
		r.head = (r.head + 1) % deadlineRingSize
		r.count--
	}
}

// pendingList contains only live pending deadlines and their expiry callback;
// every stored entry is live, so no invented active bit is needed. Expired
// entries are removed stably and their callbacks run once [01 §6.2].
type pendingList struct {
	entries []pendingEntry
}

type pendingEntry struct {
	deadline uint32
	expire   func()
}

func (p *pendingList) compact(lastTick uint32) {
	if p == nil {
		return
	}
	write := 0
	for _, entry := range p.entries {
		if entry.deadline < lastTick {
			if entry.expire != nil {
				entry.expire()
			}
			continue
		}
		p.entries[write] = entry
		write++
	}
	p.entries = p.entries[:write]
}
