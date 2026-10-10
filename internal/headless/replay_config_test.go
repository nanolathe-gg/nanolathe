package headless

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/survival"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport/retailcat"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// A campaign mission has no battle configuration, and a request with no
// content has nothing to select a schema from.
func TestMatchConfigForFreshBattleRefusals(t *testing.T) {
	fs := vfs.New()
	defer fs.Close()
	for name, request := range map[string]FreshBattleRequest{
		"campaign":   {Kind: ScenarioCampaign, Mission: "ARM01", FS: fs},
		"no content": {Kind: ScenarioSkirmish, Map: "portable"},
		"no map":     {Kind: ScenarioSkirmish, FS: fs},
	} {
		if _, err := MatchConfigForFreshBattle(request, session.MatchRoomInputs{}); err == nil {
			t.Errorf("%s: a configuration was produced", name)
		}
	}
}

// The configuration MatchConfigForFreshBattle describes a fresh skirmish or
// Survival request with composes, through admission, the battle
// ComposeFreshBattle composes from that request: the same frozen content,
// the same state at entry and after half a minute of play. The room is left
// empty, so every filled value is exercised.
func TestMatchConfigForFreshBattleComposesTheFreshBattle(t *testing.T) {
	cat, fs := retailcat.Shared(t)
	const mapName = "ashap plateau"
	builder := orders.DefaultBuilderOptions()
	builder.Guard[0] = orders.GuardStay
	restrictions, err := content.ParseRestrictions(map[string]int{"armpw": 3, "armham": 0})
	if err != nil {
		t.Fatal(err)
	}
	var overrides session.AIOverrides
	overrides.All = "jitter=0"
	// A Modern computer player needs the think step only a command links
	// (mods/aikit); the replay package's retail test plays one.
	modern := session.DirectSkirmishConfig(mapName)
	modern.Players[1].AI = ai.ControllerClassic
	modern.Location = 0
	for _, c := range []struct {
		name    string
		request FreshBattleRequest
	}{
		{"Strict 3.1", FreshBattleRequest{Kind: ScenarioSkirmish, Gameplay: gameplay.Strict31, Skirmish: session.DirectSkirmishConfig(mapName)}},
		{"Modern with options, mutators and restrictions", FreshBattleRequest{Kind: ScenarioSkirmish, Gameplay: gameplay.Modern, Skirmish: modern,
			Difficulty: 2, BuilderOptions: &builder, AIOverrides: overrides, Restrictions: restrictions,
			Mutators: content.Mutators{Income: content.Factor{Num: 3, Den: 2}}}},
		{"Survival with a buddy", FreshBattleRequest{Kind: ScenarioSurvival, Gameplay: gameplay.Modern,
			Skirmish: session.SurvivalSkirmishConfig(mapName, 1, session.SurvivalOptions{Pace: survival.PaceRelaxed})}},
	} {
		r := c.request
		r.Map, r.FS, r.Catalog, r.LocalOwner = mapName, fs, cat, -1
		r.SimulationSeed, r.CRTSeed = 7, 5006
		fresh, err := ComposeFreshBattle(r)
		if err != nil {
			t.Fatalf("%s: fresh entry: %v", c.name, err)
		}
		config, err := MatchConfigForFreshBattle(r, session.MatchRoomInputs{})
		if err != nil {
			t.Fatalf("%s: configuration: %v", c.name, err)
		}
		if got := config.Request(); got.SimulationSeed != 7 || got.CRTSeed != 5006 || got.MapName != mapName ||
			(len(got.UnitRestrictions) != 0) != !r.Restrictions.IsZero() || got.ContentProfile.Name != "retail" {
			t.Fatalf("%s: configuration %+v", c.name, got)
		}
		inputs, err := session.FreezeMatchInputs(fs, cat, config, nil)
		if err != nil {
			t.Fatalf("%s: freeze: %v", c.name, err)
		}
		if digest, ok := fresh.Session.SimulationContentDigest(); ok && digest != inputs.Digest() {
			t.Fatalf("%s: the configuration froze other content than fresh entry", c.name)
		}
		admitted, err := session.NewAdmittedSkirmish(inputs, config, nil)
		if err != nil {
			t.Fatalf("%s: admitted entry: %v", c.name, err)
		}
		local := fresh.Session
		for tick := 0; tick <= 900; tick++ {
			if tick%300 == 0 {
				a, err := admitted.PartialStateFingerprint()
				if err != nil {
					t.Fatal(err)
				}
				l, err := local.PartialStateFingerprint()
				if err != nil {
					t.Fatal(err)
				}
				if a != l {
					t.Fatalf("%s: after %d steps the admitted battle is %s, fresh entry's %s", c.name, tick, a, l)
				}
			}
			local.Step(local.Clock.ScaledAnchor + 1)
			admitted.Step(admitted.Clock.ScaledAnchor + 1)
		}
		if local.Clock.GlobalTick < 850 || admitted.UnitStateChecksum() != local.UnitStateChecksum() {
			t.Fatalf("%s: ran to tick %d", c.name, local.Clock.GlobalTick)
		}
	}
}
