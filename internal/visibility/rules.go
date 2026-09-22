package visibility

// Rules is the gameplay-policy seam owned by visibility. It decides whether
// one jammer suppresses the viewing player's contacts and answers one complete
// unit-visibility request. Both questions are asked at their request boundary;
// implementations hold no session state and draw no random numbers
// [DESIGN_COMMUNITY_PATCH §4.4] [DESIGN_GAMEPLAY_RULES §9].
type Rules interface {
	JammerSuppresses(s *Service, viewer, jammerOwner PlayerID) bool
	Visible(s *Service, viewer PlayerID, target Target) bool
}

// StrictRules is the retail baseline. It is zero size, so binding it and asking
// either question adds no allocation [03 §3.2] [03 R-VIS-01 §5].
type StrictRules struct{}

// CommunityRules is the Community 3.9 layer. It embeds StrictRules so every
// unchanged answer remains retail's [DESIGN_COMMUNITY_PATCH §2].
type CommunityRules struct{ StrictRules }

// ModernRules currently inherits both community visibility contracts without
// another override [DESIGN_COMMUNITY_PATCH §4.4].
type ModernRules struct{ CommunityRules }

// CommunityState is the resolved feature-table projection visibility owns.
// Allied reads the viewing player's alliance row; it is consulted only when
// AlliedJammingIgnored is enabled. The zero value is the Strict answer.
type CommunityState struct {
	AlliedJammingIgnored      bool
	OffMapAircraftMarginTiles int
	Allied                    func(viewer, other PlayerID) bool
	// OffMap reads the canonical movement sort-bucket filing for a unit id.
	OffMap func(unitID uint16) bool
}

// strictRules is converted once so an unbound Service takes the retail path
// without constructing an interface value per request.
var strictRules Rules = StrictRules{}

func (s *Service) rules() Rules {
	if s == nil || s.Rules == nil {
		return strictRules
	}
	return s.Rules
}

// JammerSuppresses retains retail's owner-only exemption. Pass 3 asks this
// before either jammer callback, so the answer applies equally to radar and
// sonar [03 R-VIS-01 §5].
func (StrictRules) JammerSuppresses(_ *Service, viewer, jammerOwner PlayerID) bool {
	return viewer != jammerOwner
}

// Visible evaluates the complete retail unit predicate.
func (StrictRules) Visible(s *Service, viewer PlayerID, target Target) bool {
	return s.strictVisible(viewer, target)
}

// JammerSuppresses exempts a jammer when the feature is enabled and the
// viewing player's alliance row declares its owner allied. The predicate is
// deliberately one-directional [community patch engine behavior CP-FIX-6].
func (CommunityRules) JammerSuppresses(s *Service, viewer, jammerOwner PlayerID) bool {
	if s == nil || !s.Community.AlliedJammingIgnored || s.Community.Allied == nil {
		return StrictRules{}.JammerSuppresses(s, viewer, jammerOwner)
	}
	if viewer == jammerOwner {
		return false
	}
	return !s.Community.Allied(viewer, jammerOwner)
}

// Visible keeps the retail ordered gate unless the community off-map aircraft
// substitute owns this request. The substitute itself is in predicate.go,
// beside the source grids and projection arithmetic it reads
// [community patch engine behavior CP-ENV-1(a)].
func (CommunityRules) Visible(s *Service, viewer PlayerID, target Target) bool {
	if s == nil || s.Community.OffMapAircraftMarginTiles <= 0 {
		return StrictRules{}.Visible(s, viewer, target)
	}
	return s.communityVisible(viewer, target)
}
