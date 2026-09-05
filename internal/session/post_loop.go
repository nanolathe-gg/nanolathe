package session

// PostLoopHooks are the seams for the outer executor tail, whose order is
// Established [01 §4.4][01 R-PLAT-02 §7]. Nil hooks are explicit no-ops for
// the single-thread runtime.
//
// RetireMessageLine is the tail's second step: the in-battle message ring's
// text-scroll retire — at most one line per call, when the line at the
// display index was posted more than `(textscroll + 1) × 30` ticks ago
// [01 R-PLAT-02 §8][07 R-CAM-01 §7]. The ring is presentation state owned by
// the frame package (frame.MessageRing.Expire), never simulation state, so
// the session carries no copy of it; the hook lets a presentation layer that
// wants retail's cadence retire on the session's pump rather than its own.
type PostLoopHooks struct {
	Barrier           func(index int, lastTick uint32)
	RetireMessageLine func(lastTick uint32)
	CompactPending    func(lastTick uint32)
}

// The record owner of the tail's 30-entry ring is settled: it is the in-battle
// message ring, indexed by a producer index the poster advances and a display
// index the retire advances, each record a 64-byte line plus its post tick,
// source unit, silence byte and class nibble [01 R-PLAT-02 §8]. The
// `TODO(question)` that stood here read it as the network receive-frame
// window (a layout guess) and this package modelled a generic 30-entry
// deadline window with no producer; both are gone. The tail is three steps —
// barriers, message-ring retire, temporary-sight expiry — not four
// [01 R-PLAT-02 §7][01 R-PLAT-02 §8].
//
// **Correction (kept for the audit trail).** An earlier marker asked the same
// question about the *pending-expiry* list as well. [01 R-PLAT-02 §5]
// establishes that list completely: it is the temporary-sight ("eyeball")
// observer list, 20 records of 36 bytes allocated at battle entry, produced by
// the central unit-death handler in EVERY session kind, with the throttled LOS
// refresh as its expiry callback and an in-place compaction after the pass —
// modelled here as the eyeballs field.

type postLoopState struct {
	hooks            PostLoopHooks
	trace            []string
	publicationCount uint32
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
	// The message-ring retire is presentation work [01 R-PLAT-02 §8]; the
	// session only keeps its place in the tail.
	state.trace = append(state.trace, "message-ring-retire")
	if state.hooks.RetireMessageLine != nil {
		state.hooks.RetireMessageLine(lastTick)
	}
	state.trace = append(state.trace, "pending-compact")
	state.pending.compact(lastTick)
	if state.hooks.CompactPending != nil {
		state.hooks.CompactPending(lastTick)
	}
	// The temporary-sight expiry pass is the tail's last step, after the
	// message-ring retire [03 R-COMP-02 §2][01 R-PLAT-02 §5].
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
