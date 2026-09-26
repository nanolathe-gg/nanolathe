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
	// PassiveRepairWork owns passive healing's cadence, completion admission
	// and work amount. The session retains the health and owner admission.
	PassiveRepairWork(s *Service, target *units.Unit, tick uint32) (int32, bool)
	// RepairContribution owns an active repair or passive healtime visit.
	RepairContribution(s *Service, builder, target *units.Unit, worker int32, passive bool) bool
	// BuildProducts borrows immutable compiled membership. Strict preserves
	// the retail append cutoff; Modern admits every resolved authored entry
	// (DESIGN_ECONOMY_CONSTRUCTION, "Modern authored build membership").
	BuildProducts(*content.BuildMenuPage) []string
	// AllowedFacings owns CP-CON-5's gameplay decision. Strict returns south;
	// Community and Modern may expose the authored mask when enabled.
	AllowedFacings(*Service, *content.UnitDef) content.FacingMask
	// AdmitSiteOccupants owns CP-CON-1's command/preview placement exception.
	// The allocator still requires the site to be physically clear.
	AdmitSiteOccupants(SiteOccupantRequest) bool
	// BlockedSiteLimit is the largest counter value which still takes another
	// 30-tick wait at a blocked mobile construction site.
	BlockedSiteLimit(*Service) uint32
	// KickoutEnabled gates the sourced manual order rewrite. Automatic
	// evacuation remains selected by YieldObstruction.
	KickoutEnabled(*Service) bool

	// YieldObstruction is called on each blocked construction attempt with the
	// rectangle that must become clear: the product exit rectangle, the cells
	// a refused close selected, or the snapped build-site footprint.
	// closingYard is the yard-cell window of a refused close and is nil for
	// the other two; urgent marks the build-site caller, whose blocker route
	// is staged for priority. tick is the authoritative tick the resulting
	// order is created on.
	//
	// An implementation may only issue ordinary orders. It must not write
	// transforms, occupancy or resources. Community automatic kickout is the
	// approved exception to the otherwise draw-free seam: it consumes Q3's one
	// CRT draw per admitted occupant before the protected-worker decision;
	// Modern's existing yielding remains draw-free (DESIGN_COMMUNITY_PATCH §11).
	YieldObstruction(s *Service, requester *units.Unit, clear world.FootprintRect, closingYard []world.YardCell, tick uint32, urgent bool)

	// FinalizeResurrection answers whether a failed post-allocation wreck
	// reread may continue through the ordinary resurrection transplant. Retail
	// abandons the allocated unit; Community may recover from the validated
	// pre-allocation snapshot (DESIGN_COMMUNITY_PATCH §4.5; CP-FIX-1).
	FinalizeResurrection(s *Service, f ResurrectionFinalization) bool
}

// StrictRules is the retail baseline: no clearance request is ever made, so
// blocked production keeps the retail allocation retry and the refused close
// unchanged [04 R-ORDER-02 §1]. It is zero-size, so holding it in a Rules
// never allocates.
type StrictRules struct{}

// CommunityRules is the reserved Community 3.9 layer. It embeds StrictRules so
// every unchanged answer remains retail's; Community construction contracts
// override only the questions they own without changing the other layers.
type CommunityRules struct{ StrictRules }

// SiteOccupantRequest is one placement-cell occupancy decision. Rules must not
// retain it; the feature enablement is supplied by the service owner at the
// command/preview boundary [community patch engine behavior §5.6].
type SiteOccupantRequest struct {
	Builder  *units.Unit
	Occupant *units.Unit
	Enabled  bool
}

// YieldObstruction does nothing under Strict 3.1: retail issues no clearance
// order, writes no state and draws no random number here.
func (StrictRules) YieldObstruction(*Service, *units.Unit, world.FootprintRect, []world.YardCell, uint32, bool) {
}

// FinalizeResurrection preserves retail's failed-reread answer: the newly
// allocated unit remains present, but the resurrection transplant stops
// before its remaining fraction and health are finalized [05 R-WORK-01 §7].
func (StrictRules) FinalizeResurrection(*Service, ResurrectionFinalization) bool { return false }

// AdmitSiteOccupants keeps retail's occupied-site rejection.
func (StrictRules) AdmitSiteOccupants(SiteOccupantRequest) bool { return false }

// BlockedSiteLimit keeps retail's waits while counter <= 10.
func (StrictRules) BlockedSiteLimit(*Service) uint32 { return 10 }

// KickoutEnabled keeps the Community order rewrite unreachable under Strict.
func (StrictRules) KickoutEnabled(*Service) bool { return false }

// AdmitSiteOccupants adopts CP-CON-1's placement half only for a live unit of
// the ordering player whose definition is classified MOBILE. The mobility
// restriction is Nanolathe's settled Q2 policy; the source itself admits own
// buildings too [DESIGN_COMMUNITY_PATCH §4.3, §11].
func (CommunityRules) AdmitSiteOccupants(q SiteOccupantRequest) bool {
	return q.Enabled && q.Builder != nil && q.Occupant != nil && q.Occupant.Alive && !q.Occupant.Dying &&
		q.Occupant.Def != nil && q.Occupant.Def.BMCode != 0 && q.Builder.Owner == q.Occupant.Owner
}

// BlockedSiteLimit applies the patch's replacement operand only when the
// resolved feature table enables CP-CON-1.
func (CommunityRules) BlockedSiteLimit(s *Service) uint32 {
	if s != nil && s.Community.ConstructionKickout {
		return 20
	}
	return 10
}

func (CommunityRules) KickoutEnabled(s *Service) bool {
	return s != nil && s.Community.ConstructionKickout
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

// AdmitSiteOccupant is the placement-boundary projection of CP-CON-1. It does
// not waive terrain, feature or later allocation checks.
func (s *Service) AdmitSiteOccupant(builder, occupant *units.Unit) bool {
	return s.rules().AdmitSiteOccupants(SiteOccupantRequest{
		Builder: builder, Occupant: occupant,
		Enabled: s != nil && s.Community.ConstructionKickout,
	})
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

// AllowedFacings retains the retail fixed-building answer.
func (StrictRules) AllowedFacings(*Service, *content.UnitDef) content.FacingMask {
	return content.FacingSouth
}

// AllowedFacings exposes authored facings only for building definitions while
// CP-CON-5 is enabled in this service's projected Community table.
func (CommunityRules) AllowedFacings(s *Service, def *content.UnitDef) content.FacingMask {
	if s == nil || !s.Community.StructureRotation || def == nil || def.BMCode != 0 {
		return content.FacingSouth
	}
	return def.Rotations | content.FacingSouth
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
