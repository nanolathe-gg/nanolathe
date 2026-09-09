package session

import (
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// bigBrotherState belongs to the battle, never the save account. Fresh zero
// state is equivalent to retail's disabled retained counter: enabling always
// overwrites that counter before any read [07 R-CAM-01 §12].
type bigBrotherState struct {
	enabled      bool
	countdown    int16
	shiftHeld    bool
	cycle        bool
	resetVisited bool
	cancelFollow bool
}

func (s *Session) resetBigBrotherEvents() {
	s.bigBrother.cycle = false
	s.bigBrother.resetVisited = false
	s.bigBrother.cancelFollow = false
}

// stepBigBrother runs at the unit sweep tail, before phase 3. Publication
// delivers the selector's unconditional change notice and follow repick to
// the same tick's presentation consumer [04 R-MOV-03 §1][07 R-CAM-01 §12].
func (s *Session) stepBigBrother() {
	b := &s.bigBrother
	if !b.enabled || b.shiftHeld {
		return
	}
	b.countdown--
	if b.countdown >= 1 {
		return
	}
	b.countdown = 90
	b.cycle = true
	if s.Units == nil {
		return
	}
	var first, next *units.Unit
	selectedSeen := false
	s.Units.ForEachPlayerSliceLive(int(s.LocalOwner), func(u *units.Unit) {
		if next != nil || !s.Units.SelectionReady(u) {
			return
		}
		if first == nil {
			first = u
		}
		if selectedSeen {
			next = u
			return
		}
		if u.Flags&units.SelectedStatus == 0 {
			return
		}
		selectedSeen = true
		// No ownership or readiness gate applies to the clear. Raw records
		// include retained dead slots; absent/free records expose no flags and
		// are initialized afresh on reuse, so skipping nil is equivalent.
		for slot := 0; slot < s.Units.TotalRecords(); slot++ {
			if record := s.Units.RawUnitRecord(pool.Handle(slot)); record != nil {
				record.Flags &^= 0xD0
			}
		}
		// The consumer also requests force-zero command-panel closure here;
		// page bits on individual units are not changed by this request.
		b.resetVisited = true
	})
	if next == nil {
		next = first
	}
	if next != nil {
		next.Flags |= units.SelectedStatus
	}
}
