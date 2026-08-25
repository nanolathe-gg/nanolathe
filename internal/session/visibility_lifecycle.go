package session

import (
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/units"
)

// AttachForTransport hides a cargo unit's coverage while attached to a carrier [04 §4.4].
// The cargo's footprint is removed from the byte refcount (word mask never decrements) and its
// throttled footprint is forgotten so a later detach republishes without stale height.
func (s *Session) AttachForTransport(carrier, cargo pool.Handle) {
	if s == nil || s.Vis == nil || s.Units == nil {
		return
	}
	u := s.Units.Unit(cargo)
	if u == nil {
		return
	}
	unpublishOne(s, u)
	// TODO(T25): carrier/cargo linkage AttachPiece etc. is owned by units.AttachmentState;
	// this helper currently only handles visibility. Movement and COB attach callbacks remain.
	_ = carrier
	_ = units.GuardLatchSize
}

// DetachFromTransport restores a cargo unit's coverage after transport detach [04 §4.4].
func (s *Session) DetachFromTransport(carrier, cargo pool.Handle) {
	if s == nil || s.Vis == nil || s.Units == nil {
		return
	}
	u := s.Units.Unit(cargo)
	if u == nil {
		return
	}
	publishOne(s, u)
	_ = carrier
}

// CaptureUnit transfers ownership and republishes coverage under the new owner [P0-11].
// It removes the old owner's byte refcount and publishes under the new owner synchronously.
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
