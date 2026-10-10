package session

import (
	"encoding/binary"
	"errors"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/survival"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// admitFixtureMap is an authored map with two network schemas: the first
// places two players and the second three, so the seat count selects the
// schema (mission.SelectNetworkSchema).
const admitFixtureMap = "admit"

const admitFixtureOTA = `[GlobalHeader]
{
[Schema 0]
{
Type=Network 1;
[specials]
{
[special0]
{
specialwhat=StartPos1;
XPos=16;
ZPos=16;
}
[special1]
{
specialwhat=StartPos2;
XPos=48;
ZPos=48;
}
}
}
[Schema 1]
{
Type=Network 2;
[specials]
{
[special0]
{
specialwhat=StartPos1;
XPos=16;
ZPos=16;
}
[special1]
{
specialwhat=StartPos2;
XPos=48;
ZPos=48;
}
[special2]
{
specialwhat=StartPos3;
XPos=16;
ZPos=48;
}
}
}
}
`

// admitFixtureTNT is a terrain file header alone [fmt tnt]: enough for the
// map census, which reads only the header, and for nothing that loads
// terrain.
func admitFixtureTNT() string {
	head := make([]byte, 0x40)
	binary.LittleEndian.PutUint32(head[0x00:], 0x2000)
	binary.LittleEndian.PutUint32(head[0x04:], 64)
	binary.LittleEndian.PutUint32(head[0x08:], 64)
	return string(head)
}

// admitFixture is an authored content set over that map: two sides and no
// unit, so the content can be captured, prepared and frozen, but no battle
// composed from it.
type admitFixture struct {
	fs  *vfs.FS
	cat *content.Catalog
}

func newAdmitFixture(t *testing.T) admitFixture {
	t.Helper()
	fs := fsFromMapSkirmish(t, map[string]string{
		"maps/" + admitFixtureMap + ".ota": admitFixtureOTA,
		"maps/" + admitFixtureMap + ".tnt": admitFixtureTNT(),
		"ai/default.txt":                   "plan any\n",
	})
	maps, err := content.CompileMaps(fs)
	if err != nil {
		t.Fatal(err)
	}
	if maps[admitFixtureMap] == nil || len(maps[admitFixtureMap].Schemas) != 2 {
		t.Fatalf("fixture map census: %+v", maps)
	}
	move := &content.MovementClass{FootprintX: 1, FootprintZ: 1, MaxWaterDepth: 10, MinWaterDepth: -10000, MaxSlope: 10, BadSlope: 5, MaxWaterSlope: 255, BadWaterSlope: 127}
	move.CanonicalKey = "testmove"
	return admitFixture{fs: fs, cat: &content.Catalog{
		Movement: map[string]*content.MovementClass{"testmove": move},
		Sides:    []*content.SideDef{{Name: "ARM", Commander: "armcom"}, {Name: "CORE", Commander: "corcom"}},
		Maps:     maps,
		Units:    map[string]*content.UnitDef{},
		Features: map[string]*content.FeatureDef{},
		Limits:   content.RetailLimits(),
	}}
}

// admitConfig resolves the local adapter's configuration of a setup, with the
// schema index the room selected.
func admitConfig(t *testing.T, cfg SkirmishConfig, options SkirmishEntryOptions, schema uint32) EffectiveMatchConfig {
	t.Helper()
	room := matchTestRoom()
	room.MapSchema = schema
	r, err := NewMatchConfigRequest(cfg, options, room)
	if err != nil {
		t.Fatal(err)
	}
	return resolveMatch(t, r)
}

func (f admitFixture) freeze(t *testing.T, c EffectiveMatchConfig) *content.SimulationInputs {
	t.Helper()
	inputs, err := FreezeMatchInputs(f.fs, f.cat, c, nil)
	if err != nil {
		t.Fatalf("freeze the configuration's content: %v", err)
	}
	return inputs
}

var matchAdmissionKinds = []error{ErrMatchMapMismatch, ErrMatchRulesMismatch, ErrMatchContentMismatch, ErrMatchConfigurationRejected, ErrMatchNeedsMultiSeat}

// requireAdmissionKinds checks that err wraps exactly the wanted categories.
func requireAdmissionKinds(t *testing.T, name string, err error, want ...error) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: admitted", name)
	}
	for _, kind := range matchAdmissionKinds {
		wanted := false
		for _, w := range want {
			wanted = wanted || w == kind
		}
		if errors.Is(err, kind) != wanted {
			t.Fatalf("%s: %v; wraps %q is %v, want %v", name, err, kind, !wanted, wanted)
		}
	}
	if !strings.Contains(err.Error(), "logical path ") || !strings.Contains(err.Error(), "providers searched [match configuration v1, simulation content ") {
		t.Fatalf("%s: diagnostic shape: %v", name, err)
	}
}

// A configuration is admitted against the content frozen for it, and the
// content frozen from the configuration is the content single-player battle
// entry freezes for the setup the configuration came from: the same capture,
// preparation, map selection and identity.
func TestMatchAdmissionAcceptsItsOwnContent(t *testing.T) {
	f := newAdmitFixture(t)
	survivalCfg := SurvivalSkirmishConfig(admitFixtureMap, 1, SurvivalOptions{Pace: survival.PaceRelaxed})
	survivalCfg.Players[1].AI = ai.ControllerModern
	strict := DirectSkirmishConfig(admitFixtureMap)
	strict.Gameplay = gameplay.Strict31
	for _, c := range []struct {
		name    string
		cfg     SkirmishConfig
		options SkirmishEntryOptions
		schema  uint32
	}{
		{"Modern skirmish", DirectSkirmishConfig(admitFixtureMap), SkirmishEntryOptions{}, 0},
		{"Strict 3.1 skirmish", strict, SkirmishEntryOptions{}, 0},
		{"mutators", DirectSkirmishConfig(admitFixtureMap), SkirmishEntryOptions{Mutators: content.Mutators{Income: content.Factor{Num: 3, Den: 2}, Health: content.Factor{Num: 1, Den: 1}}}, 0},
		{"Survival with a buddy", survivalCfg, SkirmishEntryOptions{}, 1},
	} {
		config := admitConfig(t, c.cfg, c.options, c.schema)
		inputs := f.freeze(t, config)
		if err := ValidateMatchInputs(config, inputs); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		local, err := prepareSkirmishEntry(f.fs, f.cat, c.cfg, c.options)
		if err != nil {
			t.Fatalf("%s: single-player front half: %v", c.name, err)
		}
		if local.inputs.Digest() != inputs.Digest() {
			t.Fatalf("%s: the configuration froze other content than its setup", c.name)
		}
		if got := inputs.Mutators(); got != config.Request().Mutators {
			t.Fatalf("%s: frozen mutators %q, configuration %q", c.name, got.String(), config.Request().Mutators.String())
		}
	}
}

// Each comparison refuses a configuration the frozen content does not hold,
// in its own category, and a configuration that fails several reports each.
func TestMatchAdmissionReportsEachMismatch(t *testing.T) {
	f := newAdmitFixture(t)
	base := admitConfig(t, DirectSkirmishConfig(admitFixtureMap), SkirmishEntryOptions{}, 0)
	inputs := f.freeze(t, base)
	third := MatchSeat{Role: MatchRoleComputer, Side: 0, Color: 2, AllyGroup: 3, Metal: 1000, Energy: 1000,
		HostSeat: 0, ComputerKind: ai.ControllerClassic, Difficulty: 1, BuilderOptions: orders.DefaultBuilderOptions()}
	for _, c := range []struct {
		name   string
		change func(*MatchConfigRequest)
		want   []error
	}{
		{"another map", func(r *MatchConfigRequest) { r.MapName = "elsewhere" }, []error{ErrMatchMapMismatch}},
		{"another schema", func(r *MatchConfigRequest) { r.MapSchema = 1 }, []error{ErrMatchMapMismatch}},
		{"a seat count that selects another schema", func(r *MatchConfigRequest) {
			r.Seats = append(r.Seats, third)
			r.MapSchema = 1
		}, []error{ErrMatchMapMismatch}},
		{"another Community table", func(r *MatchConfigRequest) { r.Community.Veterancy = !r.Community.Veterancy }, []error{ErrMatchRulesMismatch}},
		{"mutators the content was not prepared with", func(r *MatchConfigRequest) {
			r.Mutators.Income = content.Factor{Num: 3, Den: 2}
		}, []error{ErrMatchContentMismatch}},
		{"a side the content lacks", func(r *MatchConfigRequest) { r.Seats[1].Side = 2 }, []error{ErrMatchContentMismatch}},
		{"other content limits", func(r *MatchConfigRequest) { r.ContentProfile.Units = 4096 }, []error{ErrMatchContentMismatch}},
		{"unit restrictions the content was not restricted with", func(r *MatchConfigRequest) {
			r.UnitRestrictions = []MatchUnitRestriction{{DefinitionID: 1, Unit: "armcom", Limit: 1}}
		}, []error{ErrMatchContentMismatch}},
		{"two at once", func(r *MatchConfigRequest) {
			r.MapSchema = 1
			r.Mutators.Damage = content.Factor{Num: 2, Den: 1}
		}, []error{ErrMatchMapMismatch, ErrMatchContentMismatch}},
	} {
		r := base.Request()
		c.change(&r)
		for i := range r.Seats {
			r.Seats[i].SharedVictory = matchTeamOfTwo(r.Seats, i)
		}
		err := ValidateMatchInputs(resolveMatch(t, r), inputs)
		requireAdmissionKinds(t, c.name, err, c.want...)
	}
	requireAdmissionKinds(t, "no configuration", ValidateMatchInputs(EffectiveMatchConfig{}, inputs), ErrMatchConfigurationRejected)
	requireAdmissionKinds(t, "no inputs", ValidateMatchInputs(base, nil), ErrMatchContentMismatch)
	if err := ValidateMatchInputs(base, inputs); err != nil {
		t.Fatalf("validation changed the inputs it read: %v", err)
	}
}

// NewAdmittedSkirmish refuses before composing anything: a configuration the
// content does not admit with admission's own refusal, and an admissible
// configuration only multi-seat composition can run with the M5 gate rather
// than through the single-human path.
func TestNewAdmittedSkirmishRefusesBeforeComposing(t *testing.T) {
	f := newAdmitFixture(t)
	base := admitConfig(t, DirectSkirmishConfig(admitFixtureMap), SkirmishEntryOptions{}, 0)
	inputs := f.freeze(t, base)
	r := base.Request()
	r.Mutators.Income = content.Factor{Num: 3, Den: 2}
	if s, err := NewAdmittedSkirmish(inputs, resolveMatch(t, r), nil); s != nil || !errors.Is(err, ErrMatchContentMismatch) {
		t.Fatalf("an inadmissible configuration composed: %v", err)
	}
	if s, err := NewAdmittedSkirmish(nil, base, nil); s != nil || !errors.Is(err, ErrMatchContentMismatch) {
		t.Fatalf("a configuration without inputs composed: %v", err)
	}

	human := MatchSeat{Role: MatchRoleHuman, Side: 1, Color: 2, AllyGroup: 3, Nickname: "Ben", Metal: 1000, Energy: 1000,
		Participant: matchTestID(2), HostSeat: MatchHostNone, BuilderOptions: orders.DefaultBuilderOptions()}
	watcher := human
	watcher.Role = MatchRoleWatcher
	// Computers of different difficulty compose: each seat's readers take
	// its own word (§6.6; TestAdmittedSeatDifficultiesRetail).
	for _, c := range []struct {
		name   string
		change func(*MatchConfigRequest)
	}{
		{"a second human", func(r *MatchConfigRequest) { r.Seats = append(r.Seats, human) }},
		{"a watcher", func(r *MatchConfigRequest) {
			r.WatchingAllowed = true
			r.Seats = append(r.Seats, watcher)
		}},
	} {
		r := base.Request()
		c.change(&r)
		r.MapSchema = 1
		for i := range r.Seats {
			r.Seats[i].SharedVictory = matchTeamOfTwo(r.Seats, i)
		}
		config := resolveMatch(t, r)
		multi := f.freeze(t, config)
		if err := ValidateMatchInputs(config, multi); err != nil {
			t.Fatalf("%s: not admissible: %v", c.name, err)
		}
		s, err := NewAdmittedSkirmish(multi, config, nil)
		if s != nil {
			t.Fatalf("%s: composed", c.name)
		}
		requireAdmissionKinds(t, c.name, err, ErrMatchNeedsMultiSeat)
		if !strings.Contains(err.Error(), "M5") {
			t.Fatalf("%s: the refusal does not name M5: %v", c.name, err)
		}
	}
}

// The admitted setup is the inverse of the local adapter: for every setup the
// adapter describes, it carries what battle entry composes from the
// normalized setup and options — the rows, the rule words, the seeds, the
// difficulty word, Survival, the human's builder options, each computer's
// merged parameters, the mutators and the Community table — with only
// spellings composition does not read allowed to differ.
func TestMatchSkirmishSetupInvertsTheLocalAdapter(t *testing.T) {
	builder := orders.DefaultBuilderOptions()
	builder.Guard[1] = orders.GuardStay
	modern := DirectSkirmishConfig("Great Divide")
	modern.Players[1].AI = ai.ControllerModern
	modern.Location, modern.Mapping, modern.LineOfSight, modern.LOSType = 0, 3, 0, 1
	modern.Difficulty = 2
	modern.RNGSimSeed, modern.RNGCrtSeed = 9, 0
	strict := DirectSkirmishConfig("Great Divide")
	strict.Gameplay = gameplay.Strict31
	strict.Location = 7
	buddies := SurvivalSkirmishConfig("Great Divide", 2, SurvivalOptions{Pace: survival.PaceRelentless, NoNaval: true})
	buddies.Players[2].AI = ai.ControllerModern
	alone := SurvivalSkirmishConfig("Great Divide", 0, SurvivalOptions{})
	var overrides AIOverrides
	overrides.All = "jitter=1"
	overrides.Difficulty[2] = "style=eco"
	overrides.Players[1] = "jitter=0"
	for _, c := range []struct {
		name    string
		cfg     SkirmishConfig
		options SkirmishEntryOptions
	}{
		{"Modern with options", modern, SkirmishEntryOptions{BuilderOptions: &builder, AIOverrides: overrides,
			Mutators: content.Mutators{Sight: content.Factor{Num: 1, Den: 1}, Radar: content.Factor{Num: 2, Den: 1}}}},
		{"Strict 3.1", strict, SkirmishEntryOptions{}},
		{"Survival with buddies", buddies, SkirmishEntryOptions{AIOverrides: overrides}},
		{"Survival alone", alone, SkirmishEntryOptions{}},
	} {
		room := matchTestRoom()
		r, err := NewMatchConfigRequest(c.cfg, c.options, room)
		if err != nil {
			t.Fatal(err)
		}
		config := resolveMatch(t, r)
		admitted := config.Request()
		got, options := matchSkirmishSetup(&admitted)

		// The single-player path's own front-half steps.
		local := c.cfg
		features, err := ResolveCommunity(local.Gameplay, c.options.CommunitySources)
		if err != nil {
			t.Fatal(err)
		}
		if features.UnitLimit != 0 {
			local.UnitLimit = features.UnitLimit
		}
		if err := local.Normalize(); err != nil {
			t.Fatal(err)
		}
		set := RuleSetForMode(local.Gameplay)
		if RuleSetForMode(got.Gameplay).Name != set.Name || got.Gameplay.Normalize() != local.Gameplay.Normalize() {
			t.Fatalf("%s: rule set %q, want %q", c.name, got.Gameplay, local.Gameplay)
		}
		if resolved, err := resolveCommunity(set, options.CommunitySources); err != nil || resolved != features {
			t.Fatalf("%s: the admitted sources resolve to another table: %v", c.name, err)
		}
		if got.MapName != local.MapName || got.NumPlayers != local.NumPlayers || got.UnitLimit != local.UnitLimit ||
			got.RNGSimSeed != local.RNGSimSeed || got.RNGCrtSeed != local.RNGCrtSeed || got.Survival != local.Survival {
			t.Fatalf("%s: setup %+v, want %+v", c.name, got, local)
		}
		if (got.Location != 0) != (local.Location != 0) || CommanderDeathMode(got.CommanderDeath) != CommanderDeathMode(local.CommanderDeath) ||
			got.Mapping != local.Mapping&1 || got.LineOfSight != local.LineOfSight&1 || got.LOSType != local.LOSType&1 {
			t.Fatalf("%s: rule words %+v, want %+v", c.name, got, local)
		}
		attacker := local.survivalAttacker()
		computers := false
		for i := 0; i < local.NumPlayers; i++ {
			g, l := got.Players[i], local.Players[i]
			if g.IsHuman() != l.IsHuman() || g.IsObserver() != l.IsObserver() || g.IsComputer() != l.IsComputer() ||
				g.Side != l.Side || g.Color != l.Color || g.AllyGroup != l.AllyGroup {
				t.Fatalf("%s: row %d %+v, want %+v", c.name, i, g, l)
			}
			if i != attacker && (g.Metal != l.Metal || g.Energy != l.Energy) {
				t.Fatalf("%s: row %d resources %d/%d, want %d/%d", c.name, i, g.Metal, g.Energy, l.Metal, l.Energy)
			}
			if i == attacker || !l.IsComputer() {
				continue
			}
			computers = true
			want, err := c.options.AIOverrides.For(uint8(i), matchAIDifficulty(uint8(local.Difficulty)))
			if err != nil {
				t.Fatal(err)
			}
			if have, _ := options.AIOverrides.For(uint8(i), matchAIDifficulty(uint8(got.Difficulty))); g.AI != l.AI || have != want {
				t.Fatalf("%s: computer %d %v %q, want %v %q", c.name, i, g.AI, have, l.AI, want)
			}
		}
		if computers && got.Difficulty != local.Difficulty {
			t.Fatalf("%s: difficulty %d, want %d", c.name, got.Difficulty, local.Difficulty)
		}
		human := RuleSetForMode(local.Gameplay).Orders.DefaultBuilderOptions()
		if c.options.BuilderOptions != nil {
			human = *c.options.BuilderOptions
		}
		if options.BuilderOptions == nil || *options.BuilderOptions != human {
			t.Fatalf("%s: builder options %v, want %v", c.name, options.BuilderOptions, human)
		}
		if options.Mutators.String() != c.options.Mutators.String() {
			t.Fatalf("%s: mutators %q, want %q", c.name, options.Mutators.String(), c.options.Mutators.String())
		}
	}
}
