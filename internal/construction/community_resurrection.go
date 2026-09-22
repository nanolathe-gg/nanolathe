package construction

import (
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// ResurrectionRequest carries the order-owned identities that allocation may
// change. BindTarget installs the allocated unit as the order target and then
// returns the target actually stored on that order. A nil callback means there
// is no recorded order. The construction service owns the wreck snapshot and
// derives it before allocation (DESIGN_COMMUNITY_PATCH §4.5; CP-FIX-1).
type ResurrectionRequest struct {
	Heading     uint16 // Wreck orientation, consumed before creation for CP-CON-5.
	PriorTarget pool.Handle
	OrderedType uint32
	BindTarget  func(*units.Unit) pool.Handle
}

// ResurrectionResult distinguishes allocation from finalization. A failed
// post-create wreck lookup still leaves Unit allocated; Finalized reports
// whether the ordinary transplant may continue.
type ResurrectionResult struct {
	Unit      *units.Unit
	Finalized bool
}

// ResurrectionSnapshot is the validated pre-allocation wreck state used only
// by the Community recovery. Definition is the feature-table identity and
// Animation is the root cell's animation word. Valid is true only for a root
// whose definition exists and carries the retail wreck/reclaimable flag.
type ResurrectionSnapshot struct {
	Root       *world.PlotCell
	Definition uint16
	Animation  uint16
	Valid      bool
}

// ResurrectionFinalization is the complete answer presented to the gameplay
// seam after a failed post-allocation wreck reread. CreatedType is the stable
// unit-catalog identity resolved from the order's post-bind target; zero means
// that target was absent or its identity could not be established.
type ResurrectionFinalization struct {
	Snapshot      ResurrectionSnapshot
	Product       *units.Unit
	PriorTarget   pool.Handle
	CurrentTarget pool.Handle
	OrderedType   uint32
	CreatedType   uint32
	OrderPresent  bool
}

// FinalizeResurrection implements CP-FIX-1. It is deliberately a value-receiver
// method on a zero-size rule object; the feature switch is projected onto the
// construction service at composition. Recovery requires every source guard,
// then restores only the animation word. It never restores the feature
// definition, so a wreck consumed by allocation remains absent.
func (CommunityRules) FinalizeResurrection(s *Service, f ResurrectionFinalization) bool {
	if s == nil || !s.Community.ResurrectionFinalization || !f.Snapshot.Valid || f.Snapshot.Root == nil {
		return false
	}
	if !f.OrderPresent || f.CurrentTarget == 0 || f.CurrentTarget == f.PriorTarget {
		return false
	}
	if f.OrderedType == 0 || f.CreatedType == 0 || f.OrderedType != f.CreatedType {
		return false
	}
	f.Snapshot.Root.SetAnchorWord(f.Snapshot.Animation)
	return true
}
