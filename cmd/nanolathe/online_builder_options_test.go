package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/session"
)

// Modern defaults apply at room creation and recomposition, including the
// two-client entry: DESIGN_UNITS_ORDERS_COB "Modern patrol work".
func TestOnlineRoomBuilderDefaults(t *testing.T) {
	useOnlineSessionSeams(t, 10)
	cs := &contentSet{profile: "retail", limits: content.RetailLimits()}
	frozen := onlineCreationFrozen(cs, [2]uint32{1, 2}, content.Mutators{}, content.Restrictions{}, nil)
	settings := onlineSettings{mapName: "Test Map", location: 1, commanderDeath: 1}
	seats := []session.OnlineSeat{{}, {Color: 1}}
	for _, survival := range []bool{false, true} {
		settings.survival = survival
		c, err := onlineConfig(cs, onlineTestCatalog(), settings, seats, frozen)
		if err != nil {
			t.Fatal(err)
		}
		assertOnlineHumanBuilderDefaults(t, c)
		rebuilt, err := onlineConfig(cs, onlineTestCatalog(), settings, seats, onlineFrozenOf(c.Request()))
		if err != nil {
			t.Fatal(err)
		}
		assertOnlineHumanBuilderDefaults(t, rebuilt)
	}
}

func TestOnlineTwoClientBuilderDefaults(t *testing.T) {
	cs := &contentSet{profile: "retail", limits: content.RetailLimits()}
	c, err := onlineMatchConfig(onlineMatchSpec{mapName: "Test Map", simSeed: 1, crtSeed: 2}, cs, 0)
	if err != nil {
		t.Fatal(err)
	}
	assertOnlineHumanBuilderDefaults(t, c)
}

func assertOnlineHumanBuilderDefaults(t *testing.T, c session.EffectiveMatchConfig) {
	t.Helper()
	want := session.RuleSetForMode(gameplay.Modern).Orders.DefaultBuilderOptions()
	for i, seat := range c.Request().Seats {
		if seat.Role == session.MatchRoleHuman && seat.BuilderOptions != want {
			t.Errorf("human seat %d builder options = %+v, want Modern defaults %+v", i, seat.BuilderOptions, want)
		}
	}
}
