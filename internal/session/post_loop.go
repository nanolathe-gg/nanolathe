package session

// PostLoopHooks are the seams for the outer executor tail, whose order is
// Established [01 §4.4][01 R-PLAT-02 §7]. Nil hooks are explicit no-ops for
// the single-thread runtime.
//
// RetireMessageLine is the tail's second step: the in-battle message ring's
// text-scroll retire — at most one line per call, when the line at the
// display index was posted more than `(textscroll + 1) × 30` ticks ago
// [01 R-PLAT-02 §8][07 R-CAM-01 §7]. The ring is presentation state owned by
// the frame package (frame.MessageRing.RetireOne), never simulation state, so
// the session carries no copy of it; the hook lets a presentation layer that
// wants retail's cadence retire on the session's pump rather than its own.
type PostLoopHooks struct {
	Barrier           func(index int, lastTick uint32)
	RetireMessageLine func(lastTick uint32)
}

// The record owner of the tail's 30-entry ring is settled: it is the in-battle
// message ring, indexed by a producer index the poster advances and a display
// index the retire advances, each record a 64-byte line plus its post tick,
// source unit, silence byte and class nibble [01 R-PLAT-02 §8]. The
// `TODO(question)` that stood here read it as the network receive-frame
// window (a layout guess). The tail is three steps — barriers, message-ring
// retire, temporary-sight expiry — not four
// [01 R-PLAT-02 §7][01 R-PLAT-02 §8].

type postLoopState struct {
	hooks            PostLoopHooks
	messageRetire    func(lastTick uint32) bool
	trace            []string
	traceEnabled     bool
	traceLimit       int
	traceDropped     uint64
	publicationCount uint32
	eyeballs         eyeballList // temporary-sight records [01 R-PLAT-02 §5]
}

const defaultPostLoopTraceLimit = 256

func postLoopStateFor(s *Session) *postLoopState {
	if s == nil {
		return nil
	}
	if s.postLoop == nil {
		s.postLoop = &postLoopState{traceLimit: defaultPostLoopTraceLimit}
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

// BindMessageRetirement installs the presentation-owned message-ring retire
// at the one session/client composition seam. It deliberately preserves any
// optional diagnostic barrier hook already installed [01 R-PLAT-02 §§7,8].
func (s *Session) BindMessageRetirement(retire func(lastTick uint32) bool) {
	state := postLoopStateFor(s)
	if state != nil {
		state.messageRetire = retire
	}
}

// EnablePostLoopTrace enables bounded outer-tail diagnostic recording. Normal
// sessions retain no tail labels [01 §4.4][I6].
func (s *Session) EnablePostLoopTrace() {
	state := postLoopStateFor(s)
	if state == nil {
		return
	}
	state.traceEnabled = true
	state.trace = state.trace[:0]
	state.traceDropped = 0
}

// SetPostLoopTraceLimit bounds a subsequently enabled tail trace. A zero
// limit records no labels and reports every attempted record as dropped.
func (s *Session) SetPostLoopTraceLimit(limit int) {
	state := postLoopStateFor(s)
	if state == nil {
		return
	}
	if limit < 0 {
		limit = 0
	}
	state.traceLimit = limit
	if len(state.trace) > limit {
		dropped := len(state.trace) - limit
		copy(state.trace, state.trace[dropped:])
		state.trace = state.trace[:limit]
		state.traceDropped += uint64(dropped)
	}
}

// PostLoopTrace returns the retained ordered outer-tail events since tracing
// was last enabled. The trace is diagnostic only and never enters phaseTrace, so it
// cannot make the twelve-phase registry appear to contain outer work [I6].
func (s *Session) PostLoopTrace() []string {
	state := postLoopStateFor(s)
	if state == nil {
		return nil
	}
	return append([]string(nil), state.trace...)
}

// PostLoopTraceDropped reports tail labels discarded because the optional
// diagnostic trace reached its configured bound.
func (s *Session) PostLoopTraceDropped() uint64 {
	state := postLoopStateFor(s)
	if state == nil {
		return 0
	}
	return state.traceDropped
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
	// The message-ring retire is presentation work [01 R-PLAT-02 §8]; the
	// session only keeps its place in the tail.
	state.recordTrace("message-ring-retire")
	if state.messageRetire != nil {
		state.messageRetire(lastTick)
	} else if state.hooks.RetireMessageLine != nil {
		state.hooks.RetireMessageLine(lastTick)
	}
	// The temporary-sight expiry pass is the tail's last step, after the
	// message-ring retire [03 R-COMP-02 §2][01 R-PLAT-02 §5].
	state.recordTrace("eyeball-expire")
	state.eyeballs.expire(s.Vis, lastTick)
}

func (s *postLoopState) barrierOne(lastTick uint32) {
	s.recordTrace("barrier-1")
	if s.hooks.Barrier != nil {
		s.hooks.Barrier(1, lastTick)
	}
}

func (s *postLoopState) barrierTwo(lastTick uint32) {
	s.recordTrace("barrier-2")
	if s.hooks.Barrier != nil {
		s.hooks.Barrier(2, lastTick)
	}
}

func (s *postLoopState) barrierThree(lastTick uint32) {
	s.recordTrace("barrier-3")
	if s.hooks.Barrier != nil {
		s.hooks.Barrier(3, lastTick)
	}
}

func (s *postLoopState) recordTrace(event string) {
	if s == nil || !s.traceEnabled {
		return
	}
	if s.traceLimit <= 0 {
		s.traceDropped++
		return
	}
	if len(s.trace) >= s.traceLimit {
		copy(s.trace, s.trace[1:])
		s.trace[len(s.trace)-1] = event
		s.traceDropped++
		return
	}
	s.trace = append(s.trace, event)
}
