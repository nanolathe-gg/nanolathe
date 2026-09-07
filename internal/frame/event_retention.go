package frame

// The committed-frame buffer carries two kinds of payload with two different
// lifetimes. Unit rows, visibility words, fog channels and the rest are
// current STATE: a later publication supersedes an earlier one, and a
// presentation consumer that reads only the newest slot is right to do so.
// Events are not state. Each one is a single occurrence raised inside one
// authoritative tick — a weapon cue, a status request, an impact — and
// [03 R-AUD-01 §7] permits carrying the raise across the publication boundary
// only if "each committed tick's events are applied exactly once in raise
// order". Session.Step publishes one frame per sub-tick and may run several
// sub-ticks before the client draws (a frame hitch, or the 2x/3x speed
// setting), so a consumer reading only the current slot silently loses every
// superseded tick's events — cue delivery would depend on render cadence,
// which [03 §8.3] and [I6] do not permit.
//
// The retained queue below is therefore independent of the two-slot rotation:
// publication appends, the presentation drain removes. Nothing here reaches
// authoritative state, and no playback happens on this side of the boundary.

// retainedEventCapacity bounds the retained queue. It is a Nanolathe safety
// bound with no retail counterpart — retail raises its cues inside the
// simulation and has no committed-event queue at all. Overflow drops the
// newest submission and is reported, matching EventBuffer's admission policy
// rather than silently rewriting history. Its appropriate capacity requires a
// measured event-rate budget before it is changed.
const retainedEventCapacity = 4096

// retainCommittedEvents appends one committed tick's events to the retained
// queue in raise order. Duration slices are cloned because the frame slot they
// came from is reset and reused by the next BeginWrite.
func (b *Buffer) retainCommittedEvents(events []EventView) {
	if b == nil {
		return
	}
	for i := range events {
		if len(b.pendingEvents) >= retainedEventCapacity {
			b.pendingOverflow = true
			b.pendingDropped += uint64(len(events) - i)
			return
		}
		e := events[i]
		e.DurationsA = cloneDurations(e.DurationsA)
		e.DurationsB = cloneDurations(e.DurationsB)
		b.pendingEvents = append(b.pendingEvents, e)
	}
}

func cloneDurations(src []int32) []int32 {
	if len(src) == 0 {
		return nil
	}
	dst := make([]int32, len(src))
	copy(dst, src)
	return dst
}

// DrainCommittedEvents moves every retained event into dst, oldest tick first
// and in raise order within a tick, and empties the queue. The caller owns the
// returned slice until its next drain; passing the previous result back reuses
// its capacity. Draining twice with no publication in between returns nothing,
// which is the "exactly once" half of [03 R-AUD-01 §7].
func (b *Buffer) DrainCommittedEvents(dst []EventView) []EventView {
	dst = dst[:0]
	if b == nil || len(b.pendingEvents) == 0 {
		return dst
	}
	if cap(dst) < len(b.pendingEvents) {
		dst = make([]EventView, 0, len(b.pendingEvents))
	}
	dst = append(dst, b.pendingEvents...)
	clear(b.pendingEvents)
	b.pendingEvents = b.pendingEvents[:0]
	return dst
}

// PendingCommittedEvents reports how many retained events are waiting for the
// presentation drain.
func (b *Buffer) PendingCommittedEvents() int {
	if b == nil {
		return 0
	}
	return len(b.pendingEvents)
}

// RetainedEventsDropped reports events the retained queue could not hold, and
// whether it has ever overflowed. A bounded presentation stream must not fail
// silently.
func (b *Buffer) RetainedEventsDropped() (uint64, bool) {
	if b == nil {
		return 0, false
	}
	return b.pendingDropped, b.pendingOverflow
}
