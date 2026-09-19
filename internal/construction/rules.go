package construction

import (
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// Rules is the construction service's gameplay seam: authored product admission
// and whether a blocked exit, refused yard close or blocked site may ask idle
// units of the same player to walk away. Strict retains retail membership and
// issues no clearance requests [02 R-CAT-01 §8][04 R-FAC-02 §5–§6].
//
// The implementation is chosen once when the session binds a rule set; the
// call sites below never build a closure or select a mode per call. Every
// implementation is therefore a zero-size value or a pointer to one, so
// dispatch allocates nothing; scratch belongs to the service or request.
type Rules interface {
	// BuildProducts borrows immutable compiled membership. Strict preserves
	// the retail append cutoff; Modern admits every resolved authored entry
	// (DESIGN_ECONOMY_CONSTRUCTION, "Modern authored build membership").
	BuildProducts(*content.BuildMenuPage) []string

	// YieldObstruction is called on each blocked construction attempt with the
	// rectangle that must become clear: the product exit rectangle, the cells
	// a refused close selected, or the snapped build-site footprint.
	// closingYard is the yard-cell window of a refused close and is nil for
	// the other two; urgent marks the build-site caller, whose blocker route
	// is staged for priority. tick is the authoritative tick the resulting
	// order is created on.
	//
	// An implementation may only issue ordinary orders. It must not write
	// transforms, occupancy or resources, and must not draw RNG.
	YieldObstruction(s *Service, requester *units.Unit, clear world.FootprintRect, closingYard []world.YardCell, tick uint32, urgent bool)
}

// StrictRules is the retail baseline: no clearance request is ever made, so
// blocked production keeps the retail allocation retry and the refused close
// unchanged [04 R-ORDER-02 §1]. It is zero-size, so holding it in a Rules
// never allocates.
type StrictRules struct{}

// YieldObstruction does nothing under Strict 3.1: retail issues no clearance
// order, writes no state and draws no random number here.
func (StrictRules) YieldObstruction(*Service, *units.Unit, world.FootprintRect, []world.YardCell, uint32, bool) {
}

// strictRules is the shared Strict value, converted to the interface once at
// package initialisation so the default path cannot allocate.
var strictRules Rules = StrictRules{}

// rules returns the configured rule set. An unset field is the retail
// baseline, which keeps fixtures and headless composition Strict by default.
func (s *Service) rules() Rules {
	if s == nil || s.Rules == nil {
		return strictRules
	}
	return s.Rules
}

// BuildProducts borrows the selected list without allocating or changing state.
// Unbound callers retain the retail baseline [02 R-CAT-01 §8].
func BuildProducts(rules Rules, menu *content.BuildMenuPage) []string {
	if menu == nil {
		return nil
	}
	if rules == nil {
		return menu.Buttons
	}
	return rules.BuildProducts(menu)
}

func (StrictRules) BuildProducts(menu *content.BuildMenuPage) []string {
	if menu == nil {
		return nil
	}
	return menu.Buttons
}

func (*ModernRules) BuildProducts(menu *content.BuildMenuPage) []string {
	if menu == nil {
		return nil
	}
	if menu.AuthoredButtons != nil {
		return menu.AuthoredButtons
	}
	return menu.Buttons
}
