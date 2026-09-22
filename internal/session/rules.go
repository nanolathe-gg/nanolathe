// The session's gameplay rule set: one implementation per seam, bound once so
// no tick ever selects a policy. docs/DESIGN_GAMEPLAY_RULES.md owns the
// contract; each policy keeps its own design document.

package session

import (
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/movement"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/path"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
)

// RuleSet bundles one implementation per gameplay seam. It is bound once at
// session composition and again only at the phase-1 command boundary, so a
// simulation phase reads an already chosen implementation and never consults
// the mode word [docs/INVARIANTS.md I11].
//
// Every field is filled: an absent seam would leave the owning package on its
// own nil fallback, which is Strict 3.1 and would silently disagree with the
// set's name. Name is the vocabulary the host and the settings file carry, and
// a set the registry builds is completed and named by the registry itself, so
// a third-party set states only what it changes.
//
// A whole set is bound at once and a swap replaces every seam together. There
// is no partial binding: a caller that wants one different answer registers a
// set that composes the shipped implementations, so the session can never hold
// half of one policy and half of another.
type RuleSet struct {
	Name string
	// Features declares composition-time overrides; cached rules hold no mutable session state.
	Features community.Overrides
	// Base names the reserved set this one derives its gameplay semantics
	// from, and is the word the session persists and reports while
	// the set is bound. The zero value is Modern, the default, so a
	// third-party set that adds to Modern need not state it. Logic that only
	// understands the reserved behaviors reads this, never the set's name.
	Base         gameplay.Mode
	Combat       combat.Rules
	Visibility   visibility.Rules
	Orders       orders.Rules
	Construction construction.Rules
	UnitLimit    UnitLimitRules
	ScriptPorts  ScriptPortRules
	// Movement is the movement system's policy seam: whether a ground mover
	// rejected by static ground teaches its owner the blocks the route search
	// had read as unexplored (DESIGN_MOVEMENT_PATH "Modern learned terrain").
	// It is separate from Path below, which replaces the search itself.
	Movement movement.Rules
	// Path is the search kernel one route request is opened with. It is a
	// whole-subsystem seam rather than a policy one: it decides how a route
	// is found, while the scheduler keeps admission order, the per-player
	// step allowance and the publication boundary, so a replacement cannot
	// move when a route becomes visible. Both reserved sets bind the retail
	// kernel; there is no Modern kernel yet.
	Path path.Kernel
	// Planner is the computer player's per-tick think step, projected onto
	// every manager this session owns. The retail manager keeps owning its
	// own state, its save and its restore; this seam is only the decision it
	// makes with that state on a dispatched tick (docs/DESIGN_GAMEPLAY_RULES.md
	// "The computer player's think step").
	Planner ai.Planner
}

// UnitLimitRules is the save/restore unit-limit policy seam
// (DESIGN_SESSIONS_AI_SAVE "Modern save unit limits"). It is the one gameplay
// decision no simulation package owns: both questions are asked by this
// package, once per save and once per load, outside any tick.
type UnitLimitRules interface {
	// RecordsLiveUnitLimit reports whether a skirmish summary records the
	// live session limit in place of the host's configured word. Retail
	// writes the configured word `[08 R-SESS-01 §9]`.
	RecordsLiveUnitLimit() bool

	// RestoresSavedUnitLimit reports whether a skirmish battle load may size
	// its pool from a present, nonzero saved limit instead of the pre-load
	// setting. Retail sizes from the pre-load setting `[08 R-ENTRY-01 §6]`.
	// A missing, zero or out-of-range saved word is the caller's rule and is
	// not asked here.
	RestoresSavedUnitLimit() bool
}

// StrictUnitLimit answers both questions as retail: the writer emits the
// configured word and the reader sizes from the pre-load setting. It is zero
// size, so holding it in the interface never allocates.
type StrictUnitLimit struct{}

// RecordsLiveUnitLimit is retail's answer: the summary item is this build's
// copy of the configured limit, not the live pool width `[08 R-SESS-01 §9]`.
func (StrictUnitLimit) RecordsLiveUnitLimit() bool { return false }

// RestoresSavedUnitLimit is retail's answer: battle entry sizes the pool from
// the configured word and the saved item only affects the next battle
// `[08 R-ENTRY-01 §6]`.
func (StrictUnitLimit) RestoresSavedUnitLimit() bool { return false }

// ModernUnitLimit carries the approved policy of DESIGN_SESSIONS_AI_SAVE
// "Modern save unit limits": record the live layout, and restore a saved one
// so slot identities survive a changed preference. It is zero size and holds
// no state, because both answers are constants of the policy.
type ModernUnitLimit struct{}

// RecordsLiveUnitLimit records the actual session limit, so a chain of saves
// stays consistent when the caller's configured word differs.
func (ModernUnitLimit) RecordsLiveUnitLimit() bool { return true }

// RestoresSavedUnitLimit prefers the saved layout, which is what preserves
// saved slot identities across a preference change.
func (ModernUnitLimit) RestoresSavedUnitLimit() bool { return true }

// The three reserved set names are the gameplay vocabulary, so a set's name
// and the persisted mode word are the same string for the shipped sets.
const (
	StrictRuleSetName    = string(gameplay.Strict31)
	CommunityRuleSetName = string(gameplay.Community39)
	ModernRuleSetName    = string(gameplay.Modern)
)

// StrictRuleSet is the retail baseline: every seam answers as the executable
// does, with no work, no state and no draw from any stream.
func StrictRuleSet() RuleSet {
	return RuleSet{
		Name:         StrictRuleSetName,
		Base:         gameplay.Strict31,
		Combat:       combat.StrictRules{},
		Visibility:   visibility.StrictRules{},
		Orders:       orders.StrictRules{},
		Construction: construction.StrictRules{},
		UnitLimit:    StrictUnitLimit{},
		ScriptPorts:  StrictScriptPorts{},
		Movement:     movement.StrictRules{},
		Path:         path.RetailKernel{},
		Planner:      ai.RetailPlanner{},
	}
}

// CommunityRuleSet is the reserved Community 3.9 compatibility layer. Each
// package implementation embeds its Strict layer and overrides only the
// answers owned by an adopted Community contract.
func CommunityRuleSet() RuleSet {
	return RuleSet{
		Name:         CommunityRuleSetName,
		Base:         gameplay.Community39,
		Combat:       &combat.CommunityRules{},
		Visibility:   visibility.CommunityRules{},
		Orders:       &orders.CommunityRules{},
		Construction: &construction.CommunityRules{},
		UnitLimit:    StrictUnitLimit{},
		ScriptPorts:  CommunityScriptPorts{},
		Movement:     &movement.CommunityRules{},
		Path:         path.RetailKernel{},
		Planner:      ai.RetailPlanner{},
	}
}

// ModernRuleSet is the default: the approved Nanolathe Modern policies, each
// documented in its owning design document and disabled by Strict 3.1 [I11].
// The three package implementations are held by pointer so a later set may
// embed one and override a single answer without copying the policy.
func ModernRuleSet() RuleSet {
	return RuleSet{
		Name:         ModernRuleSetName,
		Base:         gameplay.Modern,
		Combat:       &combat.ModernRules{},
		Visibility:   visibility.ModernRules{},
		Orders:       &orders.ModernRules{},
		Construction: &construction.ModernRules{},
		UnitLimit:    ModernUnitLimit{},
		ScriptPorts:  ModernScriptPorts{},
		Movement:     &movement.ModernRules{},
		// The retail search is what Modern means for pathfinding too: no
		// approved Modern policy touches how a route is found.
		Path: path.RetailKernel{},
		// The retail step, because no Modern planner exists: a replacement
		// would change the simulation stream's call order and therefore the
		// whole battle, so it needs its own approved policy and its own
		// contract before it can be the default [I11].
		Planner: ai.RetailPlanner{},
	}
}

// ruleSetEntry is one selectable set: the constructor a build registered and
// the set it produced. The set is built at most once and then reused, so
// asking for a name twice costs one map lookup rather than a fresh
// implementation per lookup — the reserved constructors return fresh pointers,
// and a lookup happens on the composition and command-boundary paths.
type ruleSetEntry struct {
	build func() RuleSet
	once  sync.Once
	set   RuleSet
}

// The registry. Reserved entries are seeded here so a lookup is one code path;
// RegisterRuleSet refuses those three names, so nothing can replace them.
//
// ruleSets is only ever keyed, never ranged: iteration order of a Go map is
// unspecified, and this package is authoritative [docs/INVARIANTS.md I1].
// ruleSetOrder carries the sorted registered names for the listings, so every
// diagnostic and every host menu sees one order.
var (
	ruleSetsMu sync.Mutex
	ruleSets   = map[string]*ruleSetEntry{
		StrictRuleSetName:    {build: StrictRuleSet},
		CommunityRuleSetName: {build: CommunityRuleSet},
		ModernRuleSetName:    {build: ModernRuleSet},
	}
	ruleSetOrder []string
)

// ruleSetNameView is the session's answer to the gameplay vocabulary's
// question "can this build select that word". The mode package is a leaf and
// cannot import this one, so it holds this view instead.
type ruleSetNameView struct{}

func (ruleSetNameView) Known(name string) bool {
	ruleSetsMu.Lock()
	defer ruleSetsMu.Unlock()
	return ruleSets[name] != nil
}

func (ruleSetNameView) Names() []string { return RuleSetNames() }

// The vocabulary is installed before any word is parsed: a package's init runs
// after every package it imports has been initialized, and both commands, the
// settings reader and the mod list all sit above this package.
func init() { gameplay.UseNameRegistry(ruleSetNameView{}) }

// RegisterRuleSet adds a selectable rule set under name. It is an init-time
// call — `mods/` is the list of sets a build links — and it panics rather than
// reporting, because a duplicate or reserved name is a build mistake that must
// not survive to a running session:
//
//   - a reserved name would shadow Strict 3.1, Community 3.9 or Modern, and
//     every fingerprint and policy contract is written against those sets;
//   - a duplicate name would make selection depend on link order.
//
// build is called at most once, the first time the name is selected, and its
// result is completed from its base set and named after the registered name,
// so a set states only the seams it changes.
func RegisterRuleSet(name string, build func() RuleSet) {
	if name == "" || build == nil {
		panic("nanolathe: rule set registration needs a name and a constructor: logical path <mods>, providers searched [session rule sets], expected a nonempty name")
	}
	if name == StrictRuleSetName || name == CommunityRuleSetName || name == ModernRuleSetName {
		panic("nanolathe: reserved rule set name " + name + ": logical path <mods>, providers searched [session rule sets], expected a non-reserved name")
	}
	ruleSetsMu.Lock()
	defer ruleSetsMu.Unlock()
	if ruleSets[name] != nil {
		panic("nanolathe: duplicate rule set name " + name + ": logical path <mods>, providers searched [session rule sets], expected one registration per name")
	}
	ruleSets[name] = &ruleSetEntry{build: build}
	ruleSetOrder = append(ruleSetOrder, name)
	slices.Sort(ruleSetOrder)
}

// LookupRuleSet returns the set a name selects, building it once and keeping
// it. It is consulted when a set is selected — composition and the phase-1
// command boundary — and never inside a tick, which is what keeps the map out
// of the authoritative path [I1].
func LookupRuleSet(name string) (RuleSet, bool) {
	ruleSetsMu.Lock()
	entry := ruleSets[name]
	ruleSetsMu.Unlock()
	if entry == nil {
		return RuleSet{}, false
	}
	// The constructor runs outside the registry lock, so a set that composes
	// another registered set can look that one up from its own constructor.
	entry.once.Do(func() { entry.set = completeRuleSet(name, entry.build()) })
	return entry.set, true
}

// RuleSetNames lists every selectable name: the three reserved sets first,
// from the default down through its derivation layers, then the registered
// names in sorted order. Selection is by exact name, so this exists for
// diagnostics and for a host that offers a choice.
func RuleSetNames() []string {
	ruleSetsMu.Lock()
	defer ruleSetsMu.Unlock()
	names := make([]string, 0, 3+len(ruleSetOrder))
	names = append(names, ModernRuleSetName, CommunityRuleSetName, StrictRuleSetName)
	return append(names, ruleSetOrder...)
}

// completeRuleSet fills what a registered set left unstated. A third-party set
// composes the shipped implementations and overrides what it changes, so an
// unset seam means "as my base set answers it" — not the owning package's own
// nil fallback, which is Strict 3.1 and would silently contradict a
// Modern-based set. The registered name always wins over whatever the
// constructor wrote, so a set cannot claim another name.
func completeRuleSet(name string, set RuleSet) RuleSet {
	set.Name = name
	set.Base = reservedBase(set.Base)
	base := ModernRuleSet()
	switch set.Base {
	case gameplay.Strict31:
		base = StrictRuleSet()
	case gameplay.Community39:
		base = CommunityRuleSet()
	}
	if set.Visibility == nil {
		set.Visibility = base.Visibility
	}
	if set.Combat == nil {
		set.Combat = base.Combat
	}
	if set.Orders == nil {
		set.Orders = base.Orders
	}
	if set.Construction == nil {
		set.Construction = base.Construction
	}
	if set.UnitLimit == nil {
		set.UnitLimit = base.UnitLimit
	}
	if set.ScriptPorts == nil {
		set.ScriptPorts = base.ScriptPorts
	}
	if set.Movement == nil {
		set.Movement = base.Movement
	}
	if set.Path == nil {
		set.Path = base.Path
	}
	if set.Planner == nil {
		set.Planner = base.Planner
	}
	return set
}

// reservedBase reduces a declared base to one of the three reserved words. The
// zero value remains Modern, the default.
func reservedBase(mode gameplay.Mode) gameplay.Mode {
	switch mode {
	case gameplay.Strict31, gameplay.Community39:
		return mode
	default:
		return gameplay.Modern
	}
}

// RuleSetForMode answers with the set a selection word names: a registered
// name selects its own set, and anything else falls back the way the
// vocabulary does, to its reserved set when named and to Modern otherwise.
func RuleSetForMode(mode gameplay.Mode) RuleSet {
	if set, ok := LookupRuleSet(string(mode)); ok {
		return set
	}
	switch mode {
	case gameplay.Strict31:
		return StrictRuleSet()
	case gameplay.Community39:
		return CommunityRuleSet()
	default:
		return ModernRuleSet()
	}
}

// BaseModeOf answers the reserved base for a selection word, so a host control
// can show which of the three shipped layers a third-party selection derives
// from.
func BaseModeOf(mode gameplay.Mode) gameplay.Mode { return RuleSetForMode(mode).Base }

// SetRules selects a set by name and binds it, and is the one entry that can
// report an unknown name. The session then carries the set's base as its mode
// word, because that is the reserved base every non-seam question needs;
// the selected name stays on the bound set.
func (s *Session) SetRules(name string) error {
	if s == nil {
		return nil
	}
	set, ok := LookupRuleSet(name)
	if !ok {
		return fmt.Errorf("nanolathe: unknown gameplay rule set %q: logical path <session>, providers searched [session rule sets, mods], expected one of %s", name, strings.Join(RuleSetNames(), ", "))
	}
	features, err := resolveCommunity(set, s.CommunitySources)
	if err != nil {
		return err
	}
	s.Gameplay = set.Base
	s.Community = features
	s.bindRuleServices(set)
	return nil
}

// RebindRules projects the session's selected set onto services the composer
// created after the selection. It re-projects the bound set rather than
// deriving one from the mode word, because the word is the set's base and
// deriving from it would replace a selected third-party set with its base set;
// it selects only when nothing has been bound yet.
//
// Binding is idempotent, so the composer may call this after every allocation
// it performs. Selection reaches the registry once per session.
func (s *Session) RebindRules() {
	if s == nil {
		return
	}
	if s.Rules.Name == "" {
		s.SetGameplay(s.Gameplay)
		return
	}
	s.bindRuleServices(s.Rules)
}

// BindRules stores the set and projects each seam onto the service that asks
// it. Binding is a composition step and a command-boundary step, never a tick
// step [I1]: after it returns, every call site holds a concrete
// implementation and the mode word is read only for diagnostics and
// persistence.
func (s *Session) BindRules(set RuleSet) {
	if s == nil {
		return
	}
	features, err := resolveCommunity(set, s.CommunitySources)
	if err != nil {
		panic(err)
	}
	s.Community = features
	s.bindRuleServices(set)
}

func (s *Session) bindRuleServices(set RuleSet) {
	s.Rules = set
	s.projectCommunity()
	if s.Vis != nil {
		s.Vis.Rules = set.Visibility
	}
	if s.Combat != nil {
		s.Combat.Rules = set.Combat
	}
	if s.Build != nil {
		s.Build.Rules = set.Construction
		if s.Build.OrderBinding != nil {
			s.Build.OrderBinding.Rules = set.Orders
		}
	}
	if s.Movement != nil {
		s.Movement.Kernel = set.Path
		s.Movement.Rules = set.Movement
	}
	// The computer players are player-indexed with nil holes, so this is a
	// direct indexed walk and never a map range [RS-02][I1]. A manager
	// composed after the binding takes the same field from the bound set at
	// construction (initializeBattleAI), which is what makes re-projection
	// idempotent here.
	for player := range s.AI {
		if s.AI[player] != nil {
			s.AI[player].Planner = set.Planner
			s.AI[player].ConstructionRules = set.Construction
		}
	}
}

// orderRules is the rule set a newly composed queue binding carries. A session
// that never bound a set answers as Strict 3.1, the same fallback the order
// package applies to a binding whose field is unset, so a reconstructed queue
// runs the retail path.
func (s *Session) orderRules() orders.Rules {
	if s == nil || s.Rules.Orders == nil {
		return orders.StrictRules{}
	}
	return s.Rules.Orders
}

// unitLimitRules is the save/restore policy of the bound set. A session that
// never bound one answers by its persisted mode word, so a detached fixture
// writes the same summary a composed session does; the mode's zero value is
// Modern.
func (s *Session) unitLimitRules() UnitLimitRules {
	if s == nil {
		return StrictUnitLimit{}
	}
	if s.Rules.UnitLimit != nil {
		return s.Rules.UnitLimit
	}
	return RuleSetForMode(s.Gameplay).UnitLimit
}

// unitLimitRulesForMode answers the load-time questions, which are asked
// before a session exists: the staging path carries only the mode word its
// caller selected.
func unitLimitRulesForMode(mode gameplay.Mode) UnitLimitRules {
	return RuleSetForMode(mode).UnitLimit
}

// buildProducts reads immutable membership through the bound construction rule.
func (s *Session) buildProducts(builder string) []string {
	if s == nil || s.Catalog == nil {
		return nil
	}
	return construction.BuildProducts(s.Rules.Construction, s.Catalog.BuildMenus[content.CanonicalKey(builder)])
}
