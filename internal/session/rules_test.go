package session

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/movement"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/path"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// reservedRuleSets are the three sets the session ships. Every structural rule
// below is checked against all three, because every derivation layer is held
// to the same allocation contract
// (docs/DESIGN_GAMEPLAY_RULES.md "Allocation rules").
func reservedRuleSets() []RuleSet {
	return []RuleSet{StrictRuleSet(), CommunityRuleSet(), ModernRuleSet()}
}

// A bound implementation must be zero size or used by pointer, so projecting a
// rule set onto a service never boxes a value and no call site can allocate by
// asking a question. Anything else — a non-empty struct, a map, a slice — would
// pay a copy or an allocation at the seam.
func TestRuleSetImplementationsAreZeroSizeOrPointers(t *testing.T) {
	for _, set := range reservedRuleSets() {
		if set.Name == "" {
			t.Fatal("a rule set must name itself; the name is the persisted vocabulary")
		}
		value := reflect.ValueOf(set)
		for i := 0; i < value.NumField(); i++ {
			field := value.Type().Field(i)
			if field.Type.Kind() != reflect.Interface {
				continue
			}
			seam := value.Field(i)
			if seam.IsNil() {
				t.Fatalf("%s leaves %s unbound; an absent seam silently answers Strict", set.Name, field.Name)
			}
			impl := seam.Elem().Type()
			switch impl.Kind() {
			case reflect.Pointer:
			case reflect.Struct:
				if impl.Size() != 0 {
					t.Fatalf("%s.%s is %s of %d bytes; a bound value must be zero size or a pointer", set.Name, field.Name, impl, impl.Size())
				}
			default:
				t.Fatalf("%s.%s is %s (%s); a bound implementation must be zero size or a pointer", set.Name, field.Name, impl, impl.Kind())
			}
		}
	}
}

// The reserved sets carry the mode vocabulary, so a session's name and its
// persisted word agree and the unit-limit seam answers the way its owning
// contract states (DESIGN_SESSIONS_AI_SAVE "Modern save unit limits").
func TestReservedRuleSetsMatchTheModeVocabulary(t *testing.T) {
	for _, tc := range []struct {
		mode        gameplay.Mode
		name        string
		unitLimited bool
	}{
		{gameplay.Strict31, StrictRuleSetName, false},
		{gameplay.Community39, CommunityRuleSetName, true},
		{gameplay.Modern, ModernRuleSetName, true},
		{"", ModernRuleSetName, true},
	} {
		set := RuleSetForMode(tc.mode)
		if set.Name != tc.name {
			t.Fatalf("mode %q selected rule set %q, want %q", tc.mode, set.Name, tc.name)
		}
		if set.UnitLimit.RecordsLiveUnitLimit() != tc.unitLimited || set.UnitLimit.RestoresSavedUnitLimit() != tc.unitLimited {
			t.Fatalf("%s unit-limit policy = (%v,%v), want both %v", set.Name, set.UnitLimit.RecordsLiveUnitLimit(), set.UnitLimit.RestoresSavedUnitLimit(), tc.unitLimited)
		}
	}
}

// Binding projects each seam onto the service that asks it, including a queue
// binding composed before the switch. An unbound session answers Strict at the
// order seam, which is the fallback a reconstructed queue relies on.
func TestBindRulesProjectsEverySeam(t *testing.T) {
	s := &Session{Combat: &combat.Service{}, Vis: &visibility.Service{}, Build: &construction.Service{OrderBinding: &orders.QueueBinding{}}, Movement: &movement.System{}}
	if _, strict := s.orderRules().(orders.StrictRules); !strict {
		t.Fatal("an unbound session must answer Strict at the order seam")
	}
	for _, set := range reservedRuleSets() {
		s.BindRules(set)
		if s.Rules.Name != set.Name {
			t.Fatalf("session kept rule set %q after binding %q", s.Rules.Name, set.Name)
		}
		if s.Vis.Rules != set.Visibility || s.Combat.Rules != set.Combat || s.Build.Rules != set.Construction || s.Build.OrderBinding.Rules != set.Orders {
			t.Fatalf("%s did not reach every service", set.Name)
		}
		if s.Movement.Kernel != set.Path {
			t.Fatalf("%s did not reach the movement search kernel", set.Name)
		}
		if s.Movement.Rules != set.Movement {
			t.Fatalf("%s did not reach the movement policy seam", set.Name)
		}
		if s.orderRules() != set.Orders || s.unitLimitRules() != set.UnitLimit {
			t.Fatalf("%s accessors did not read the bound set", set.Name)
		}
	}
}

// Strict 3.1 and Community bind the retail search kernel; Modern binds the
// retail search behind route straightening (DESIGN_MOVEMENT_PATH "Modern route
// straightening"). The scheduler owns when a route publishes under every kernel
// (docs/DESIGN_GAMEPLAY_RULES.md "The path search kernel"), and this test is
// what makes changing a reserved set's kernel a deliberate edit.
func TestReservedRuleSetsBindTheirSearchKernels(t *testing.T) {
	for _, set := range reservedRuleSets() {
		if set.Name == ModernRuleSetName {
			if _, ok := set.Path.(path.StraightenKernel); !ok {
				t.Fatalf("%s bound search kernel %T, want route straightening", set.Name, set.Path)
			}
			continue
		}
		if _, retail := set.Path.(path.RetailKernel); !retail {
			t.Fatalf("%s bound search kernel %T, want the retail kernel", set.Name, set.Path)
		}
	}
}

// The composer projects the path seam onto the movement system it creates, so
// a composed session searches with its rule set's kernel rather than the
// package fallback. The projection has to survive the order the composer
// builds in: the movement system is created after the first rule-set
// selection, so only the re-projection at the end of composition can reach it.
func TestCompositionProjectsTheSearchKernelOntoMovement(t *testing.T) {
	cat := &content.Catalog{
		Units:    map[string]*content.UnitDef{"armflea": {UnitName: "armflea", MaxDamage: 100, FootprintX: 1, FootprintZ: 1, MaxVelocity: 65536, TurnRate: 100}},
		Sides:    []*content.SideDef{{Commander: "armflea"}},
		Maps:     map[string]*content.MapHeader{},
		Movement: map[string]*content.MovementClass{},
		Features: map[string]*content.FeatureDef{},
	}
	terrain := &world.Terrain{CellW: 20, CellH: 20, Plot: make([]world.PlotCell, 400)}
	for i := range terrain.Plot {
		terrain.Plot[i].SetFeature(world.PlotFeatureNone)
		terrain.Plot[i].SetHeight(10)
		terrain.Plot[i].SetMinHeight(10)
		terrain.Plot[i].SetMaxHeight(10)
	}
	terrain.ApplySchema(nil, 0)
	w, err := newSlicedWorld(cat)
	if err != nil {
		t.Fatal(err)
	}
	s := &Session{
		Catalog: cat,
		World:   terrain,
		Units:   w,
		Mission: &mission.Mission{Type: mission.TypeSkirmish, TerrainKey: "test", Schema: mission.Schema{Name: "test"}},
	}
	if err := createAndBindServicesForTest(t, s); err != nil {
		t.Fatalf("createAndBindServices: %v", err)
	}
	if s.Movement == nil {
		t.Fatal("composition created no movement system")
	}
	if s.Movement.Kernel == nil || s.Movement.Kernel != s.Rules.Path {
		t.Fatalf("movement holds kernel %T, the bound set holds %T", s.Movement.Kernel, s.Rules.Path)
	}
	// The default session is Modern, so the composed movement system carries
	// the learned-terrain policy and not the unbound Strict fallback
	// (DESIGN_MOVEMENT_PATH "Modern learned terrain").
	if _, modern := s.Movement.Rules.(*movement.ModernRules); !modern || s.Movement.Rules != s.Rules.Movement {
		t.Fatalf("movement holds policy %T, the bound set holds %T", s.Movement.Rules, s.Rules.Movement)
	}
}

// ruleDispatchSink keeps each measured answer live so the dispatches below are
// not optimized away.
var ruleDispatchSink bool

// Rule dispatch through a bound set must cost one indirect call and nothing
// else. Binding happens outside the tick, so this probe binds once and then
// measures the three package dispatches a tick performs — the whole-tick
// counter is dominated by publication traffic and would not attribute a
// regression to the seam. The construction question is asked with no service,
// the argument shape that makes both implementations return without work, so
// what is measured is the dispatch and not the clearance search.
func TestBoundRuleDispatchDoesNotAllocate(t *testing.T) {
	extent, err := world.NewFootprintExtent(2, 2)
	if err != nil {
		t.Fatal(err)
	}
	rect, err := world.NewFootprintRect(world.NewFootprintAnchor(4, 4), extent)
	if err != nil {
		t.Fatal(err)
	}
	var shooter units.Unit
	shooter.Flags &^= units.StandingFieldMask << units.StandingFireShift
	s := &Session{Clock: &clock.State{GlobalTick: 10}, Combat: &combat.Service{}, Build: &construction.Service{OrderBinding: &orders.QueueBinding{}}}
	for _, set := range reservedRuleSets() {
		t.Run(set.Name, func(t *testing.T) {
			s.BindRules(set)
			if allocs := testing.AllocsPerRun(200, func() {
				ruleDispatchSink = s.Combat.Rules.HoldsFire(&shooter, false)
				ruleDispatchSink = s.Build.OrderBinding.Rules.HoldsFire(&shooter) || ruleDispatchSink
				s.Build.Rules.YieldObstruction(nil, nil, rect, nil, 7, false)
			}); allocs != 0 {
				t.Fatalf("%s rule dispatch allocated %v per tick's worth of questions", set.Name, allocs)
			}
			if set.Name == ModernRuleSetName && !ruleDispatchSink {
				t.Fatal("Modern answered false for a held shooter; the measured dispatch was elided")
			}
		})
	}
}

// The computer player's think step reaches every manager the session owns,
// and all reserved sets bind the retail step: no alternate planner exists, and a
// replacement would change the simulation stream's call order and therefore
// the whole battle, so it needs its own approved policy first
// (docs/DESIGN_GAMEPLAY_RULES.md "The computer player's think step").
//
// The walk is player-indexed with nil holes, so a session with only two
// computer players is the shape to bind against.
func TestBindRulesProjectsThePlannerOntoEveryComputerPlayer(t *testing.T) {
	s := &Session{Combat: &combat.Service{}, Build: &construction.Service{OrderBinding: &orders.QueueBinding{}}}
	s.AI[0] = &ai.Manager{Player: 0}
	s.AI[3] = &ai.Manager{Player: 3}
	for _, set := range reservedRuleSets() {
		if _, retail := set.Planner.(ai.RetailPlanner); !retail {
			t.Fatalf("%s binds planner %T; all reserved sets run the retail step", set.Name, set.Planner)
		}
		s.BindRules(set)
		for player, mgr := range s.AI {
			if mgr == nil {
				continue
			}
			if mgr.Planner != set.Planner {
				t.Fatalf("%s did not reach the computer player in slot %d", set.Name, player)
			}
		}
	}
	// One indirect call per player per tick and nothing else. The gate the
	// economy record closes is the argument shape that makes the retail step
	// return without work, so what is measured is the dispatch.
	var econ economy.Service
	econ.Players[0].Exists = true
	mgr := s.AI[0]
	if allocs := testing.AllocsPerRun(200, func() { mgr.Tick(100, nil, &econ) }); allocs != 0 {
		t.Fatalf("the projected think step allocated %v per dispatch", allocs)
	}
}

// A registered set, the third form of the gameplay vocabulary. It is declared
// here rather than by importing mods/example, because nothing under internal/
// may import the mod list (internal/architecture.TestOnlyCommandsImportTheModList);
// it is the same shape that package uses — Modern with one order answer
// replaced — so the registry contract is exercised in this package's own
// build.
const testRuleSetName = "session-test-order-override"

// testOrderRules embeds the shipped Modern order policy and shadows one
// promoted method, which is the whole of a third-party override.
type testOrderRules struct{ orders.ModernRules }

func (*testOrderRules) HoldsFire(*units.Unit) bool { return false }

// testRuleSetBuilds counts constructor calls, so the caching contract can be
// asserted rather than assumed.
var testRuleSetBuilds int

// buildTestRuleSet states only the seam it changes; the registry names the set
// and fills the rest from the declared base, which defaults to Modern.
func buildTestRuleSet() RuleSet {
	testRuleSetBuilds++
	return RuleSet{Orders: &testOrderRules{}}
}

func init() { RegisterRuleSet(testRuleSetName, buildTestRuleSet) }

// The reserved sets come first and in the vocabulary's own order — the default
// down through its derivation layers — so a diagnostic and a host menu agree,
// and the registered names follow sorted rather than in link order.
func TestRuleSetNamesListTheReservedSetsFirst(t *testing.T) {
	names := RuleSetNames()
	if len(names) < 4 || names[0] != ModernRuleSetName || names[1] != CommunityRuleSetName || names[2] != StrictRuleSetName {
		t.Fatalf("rule set names = %v, want the three reserved names first", names)
	}
	registered := names[3:]
	if !slices.Contains(registered, testRuleSetName) {
		t.Fatalf("registered names %v do not include %q", registered, testRuleSetName)
	}
	if !slices.IsSorted(registered) {
		t.Fatalf("registered names %v are not sorted", registered)
	}
}

// Registration is a build-time act, so a name collision is a panic and not a
// value a session could carry: a reserved name would shadow a set every retail
// fingerprint and Modern contract is written against, and a duplicate would
// make selection depend on link order.
func TestRegisterRuleSetRefusesReservedAndDuplicateNames(t *testing.T) {
	for _, tc := range []struct{ what, name string }{
		{"the Modern name", ModernRuleSetName},
		{"the Community 3.9 name", CommunityRuleSetName},
		{"the Strict 3.1 name", StrictRuleSetName},
		{"an already registered name", testRuleSetName},
		{"no name", ""},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatalf("registering %s was accepted", tc.what)
				}
			}()
			RegisterRuleSet(tc.name, buildTestRuleSet)
		}()
	}
	if _, ok := LookupRuleSet(testRuleSetName); !ok {
		t.Fatal("a refused registration disturbed the registry")
	}
}

// A registered set may derive from the Community layer without taking any
// Modern policy. Its unstated seams retain Community's concrete implementation
// types, and unchanged questions continue to promote the Strict answers.
func TestCompleteRuleSetFillsFromCommunityBase(t *testing.T) {
	set := completeRuleSet("community-derived-test", RuleSet{Base: gameplay.Community39})
	want := CommunityRuleSet()
	if set.Base != gameplay.Community39 {
		t.Fatalf("base = %q, want %q", set.Base, gameplay.Community39)
	}
	for _, seam := range []struct {
		name      string
		got, want any
	}{
		{name: "Combat", got: set.Combat, want: want.Combat},
		{name: "Visibility", got: set.Visibility, want: want.Visibility},
		{name: "Orders", got: set.Orders, want: want.Orders},
		{name: "Construction", got: set.Construction, want: want.Construction},
		{name: "UnitLimit", got: set.UnitLimit, want: want.UnitLimit},
		{name: "ScriptPorts", got: set.ScriptPorts, want: want.ScriptPorts},
		{name: "Movement", got: set.Movement, want: want.Movement},
		{name: "Path", got: set.Path, want: want.Path},
		{name: "Planner", got: set.Planner, want: want.Planner},
	} {
		if reflect.TypeOf(seam.got) != reflect.TypeOf(seam.want) {
			t.Fatalf("%s is %T, want Community base %T", seam.name, seam.got, seam.want)
		}
	}
	if set.Combat.HoldsFire(&units.Unit{}, false) || set.Orders.HoldsFire(&units.Unit{}) {
		t.Fatal("the empty Community layer did not preserve Strict Hold Fire answers")
	}
}

// A lookup builds the set once and keeps it, and completes what the
// constructor left unstated from the base set rather than from the owning
// packages' nil fallbacks — those answer Strict 3.1, which would silently
// contradict a Modern-based set.
func TestLookupRuleSetBuildsOnceAndCompletesFromItsBase(t *testing.T) {
	first, ok := LookupRuleSet(testRuleSetName)
	if !ok {
		t.Fatalf("%q is not selectable", testRuleSetName)
	}
	builds := testRuleSetBuilds
	second, _ := LookupRuleSet(testRuleSetName)
	if testRuleSetBuilds != builds {
		t.Fatalf("a second lookup rebuilt the set (%d builds, was %d)", testRuleSetBuilds, builds)
	}
	if first.Orders != second.Orders || first.Combat != second.Combat {
		t.Fatal("a second lookup returned different implementations; the built set is not cached")
	}
	if first.Name != testRuleSetName {
		t.Fatalf("set name %q, want the registered name %q", first.Name, testRuleSetName)
	}
	if first.Base != gameplay.Modern {
		t.Fatalf("declared base %q, want the default %q", first.Base, gameplay.Modern)
	}
	modern := ModernRuleSet()
	for _, seam := range []struct {
		name       string
		got, want  any
		overridden bool
	}{
		{name: "Combat", got: first.Combat, want: modern.Combat},
		{name: "Visibility", got: first.Visibility, want: modern.Visibility},
		{name: "Construction", got: first.Construction, want: modern.Construction},
		{name: "UnitLimit", got: first.UnitLimit, want: modern.UnitLimit},
		{name: "ScriptPorts", got: first.ScriptPorts, want: modern.ScriptPorts},
		{name: "Movement", got: first.Movement, want: modern.Movement},
		{name: "Path", got: first.Path, want: modern.Path},
		{name: "Planner", got: first.Planner, want: modern.Planner},
		{name: "Orders", got: first.Orders, want: modern.Orders, overridden: true},
	} {
		same := reflect.TypeOf(seam.got) == reflect.TypeOf(seam.want)
		if same == seam.overridden {
			t.Fatalf("%s is %T; want %s the base's %T", seam.name, seam.got, map[bool]string{true: "not", false: ""}[seam.overridden], seam.want)
		}
	}
	if first.Orders.HoldsFire(&units.Unit{}) {
		t.Fatal("the override did not reach the built set")
	}
}

// The registry hands the same built set to every session in the process, so an
// implementation may not hold session state. Zero size is the property that
// makes that safe, and it is the same rule the bound-seam guard applies — this
// one covers the registered sets as well, because they are the ones a future
// author writes.
func TestCachedRuleSetImplementationsHoldNoState(t *testing.T) {
	for _, name := range RuleSetNames() {
		set, ok := LookupRuleSet(name)
		if !ok {
			t.Fatalf("%q is listed but not selectable", name)
		}
		value := reflect.ValueOf(set)
		for i := 0; i < value.NumField(); i++ {
			field := value.Type().Field(i)
			if field.Type.Kind() != reflect.Interface {
				continue
			}
			impl := value.Field(i).Elem().Type()
			if impl.Kind() == reflect.Pointer {
				impl = impl.Elem()
			}
			if impl.Size() != 0 {
				t.Fatalf("%s.%s is %s of %d bytes; a cached implementation is shared by every session in the process and must hold no state", name, field.Name, impl, impl.Size())
			}
		}
	}
}

// Selection by name binds the whole set and leaves the session carrying the
// set's base as its mode word, which is the answer every non-seam gameplay
// question needs. An unknown name is reported in the repository's diagnostic
// shape and changes nothing.
func TestSetRulesSelectsByNameAndReportsAnUnknownOne(t *testing.T) {
	s := &Session{Gameplay: gameplay.Strict31, Combat: &combat.Service{}, Build: &construction.Service{OrderBinding: &orders.QueueBinding{}}}
	if err := s.SetRules(testRuleSetName); err != nil {
		t.Fatalf("SetRules(%q): %v", testRuleSetName, err)
	}
	if s.Rules.Name != testRuleSetName {
		t.Fatalf("bound set %q, want %q", s.Rules.Name, testRuleSetName)
	}
	if s.Gameplay != gameplay.Modern {
		t.Fatalf("session mode word %q, want the set's base %q", s.Gameplay, gameplay.Modern)
	}
	if s.Build.OrderBinding.Rules != s.Rules.Orders || s.Combat.Rules != s.Rules.Combat {
		t.Fatal("selection by name did not project onto the services")
	}
	err := s.SetRules("no-such-rule-set")
	if err == nil {
		t.Fatal("an unknown name was accepted")
	}
	for _, want := range []string{"nanolathe: unknown gameplay rule set", "providers searched", ModernRuleSetName, CommunityRuleSetName, StrictRuleSetName, testRuleSetName} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("diagnostic %q does not carry %q", err, want)
		}
	}
	if s.Rules.Name != testRuleSetName {
		t.Fatalf("a refused selection replaced the bound set with %q", s.Rules.Name)
	}
}

// The composer projects the bound set onto services it creates lazily, once
// per allocated unit. That step must re-project what is bound and never
// re-derive a set from the mode word: the word is the bound set's base, so
// deriving from it would replace a selected set with its base the first time a
// unit is created.
func TestUnitCreationKeepsTheSelectedRuleSet(t *testing.T) {
	def := &content.UnitDef{BMCode: 1, MaxDamage: 100}
	def.CanonicalKey = "builder"
	cat := &content.Catalog{Units: map[string]*content.UnitDef{"builder": def}}
	w := newSessionFixtureWorld(4, cat)
	h, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	s := &Session{Units: w, Catalog: cat}
	if err := s.SetRules(testRuleSetName); err != nil {
		t.Fatalf("SetRules(%q): %v", testRuleSetName, err)
	}
	selected := s.Rules.Orders
	s.bindOrderQueue(w.Unit(h))
	if s.Rules.Name != testRuleSetName || s.Rules.Orders != selected {
		t.Fatalf("creating a unit replaced the selected set with %q", s.Rules.Name)
	}
	if s.Build.OrderBinding.Rules != selected {
		t.Fatal("the queue binding the composer created carries another set's order rules")
	}
	if rules := s.orderRules(); rules != selected {
		t.Fatalf("the session's order accessor answers %T, not the selected set's", rules)
	}
}

// Rebinding is idempotent and selects only when nothing is bound yet, which is
// what lets the composer call it after every allocation.
func TestRebindRulesSelectsOnlyWhenNothingIsBound(t *testing.T) {
	s := &Session{Gameplay: gameplay.Strict31, Combat: &combat.Service{}, Build: &construction.Service{OrderBinding: &orders.QueueBinding{}}}
	s.RebindRules()
	if s.Rules.Name != StrictRuleSetName {
		t.Fatalf("an unbound session selected %q from the word %q", s.Rules.Name, gameplay.Strict31)
	}
	if err := s.SetRules(testRuleSetName); err != nil {
		t.Fatal(err)
	}
	set := s.Rules
	for range 3 {
		s.RebindRules()
		if s.Rules.Name != set.Name || s.Rules.Orders != set.Orders || s.Build.OrderBinding.Rules != set.Orders {
			t.Fatalf("rebinding changed the bound set to %q", s.Rules.Name)
		}
	}
}

// The vocabulary's third form: the session installs its registry in the
// gameplay package, so a host word is parsed and normalized against the sets
// this build actually links.
func TestGameplayVocabularyKnowsTheRegisteredNames(t *testing.T) {
	for _, word := range []string{ModernRuleSetName, CommunityRuleSetName, StrictRuleSetName, testRuleSetName} {
		mode, err := gameplay.Parse(word)
		if err != nil || string(mode) != word {
			t.Fatalf("Parse(%q) = %q, %v; want the word itself", word, mode, err)
		}
		if got := gameplay.Mode(word).Normalize(); string(got) != word {
			t.Fatalf("Normalize(%q) = %q; a selectable word must survive a settings round trip", word, got)
		}
	}
	if _, err := gameplay.Parse("no-such-rule-set"); err == nil {
		t.Fatal("an unknown word parsed")
	} else if !strings.Contains(err.Error(), testRuleSetName) {
		t.Fatalf("diagnostic %q does not list the registered names", err)
	}
	if got := gameplay.Mode("no-such-rule-set").Normalize(); got != gameplay.Modern {
		t.Fatalf("Normalize of an unselectable word = %q, want %q", got, gameplay.Modern)
	}
}

// A host control asks which reserved set a selection derives from; a
// registered set answers with its base.
func TestBaseModeOfReducesASelectionToAReservedWord(t *testing.T) {
	for _, tc := range []struct {
		mode gameplay.Mode
		want gameplay.Mode
	}{
		{gameplay.Strict31, gameplay.Strict31},
		{gameplay.Community39, gameplay.Community39},
		{gameplay.Modern, gameplay.Modern},
		{"", gameplay.Modern},
		{testRuleSetName, gameplay.Modern},
		{"no-such-rule-set", gameplay.Modern},
	} {
		if got := BaseModeOf(tc.mode); got != tc.want {
			t.Fatalf("BaseModeOf(%q) = %q, want %q", tc.mode, got, tc.want)
		}
	}
}
