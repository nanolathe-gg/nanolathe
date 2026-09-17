package session

import (
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// CaptureUnit transfers ownership and republishes coverage under the new owner [P0-11].
// It removes the old owner's byte refcount and publishes under the new owner synchronously.
// It also fires the capture-transfer notification exactly once [08 "Evaluation"] slot 2
// via units.World.NotifyCapture → OnCapture → triggers.NotifyAll.
//
// The write is in place: the record keeps the pool slot, and therefore the
// slice, it was created in. That is what the sweep expects — it visits the
// physical slot and re-reads the owner record per unit rather than filtering
// the slice by runtime owner [04 R-MOV-03 §1 "the player gate"]. The stock
// capture path does not come through here: it builds a replacement record in
// the new owner's slice [05 R-WORK-01 §15].
func (s *Session) CaptureUnit(h pool.Handle, newOwner uint8) {
	if s == nil || s.Units == nil || s.Vis == nil {
		return
	}
	u := s.Units.Unit(h)
	if u == nil {
		return
	}
	oldOwner := u.Owner
	if oldOwner == newOwner {
		return
	}
	unpublishOne(s, u)
	u.Owner = newOwner
	publishOne(s, u)
	if s.Units != nil {
		s.Units.NotifyCapture(h, oldOwner, newOwner)
	}
}

// CompleteUnit republishes a nanoframe that just finished construction [05 "Construction arithmetic"].
// The footprint radius may have changed from the nanoframe's placeholder to the finished unit's sight distance.
func (s *Session) CompleteUnit(h pool.Handle) {
	if s == nil || s.Units == nil || s.Vis == nil {
		return
	}
	u := s.Units.Unit(h)
	if u == nil {
		return
	}
	// Ensure movement state exists before publishing.
	if s.Movement != nil && s.Movement.Routes != nil {
		s.Movement.EnsureUnit(u)
	}
	publishOne(s, u)
}
