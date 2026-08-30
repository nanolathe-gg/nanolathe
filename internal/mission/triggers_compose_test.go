package mission

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/triggers"
)

// bothSchemaOTA carries a campaign family and a network family, which is the
// shape that made content-sniffing schema selection wrong: a skirmish load
// must not be able to reach "Easy".
const bothSchemaOTA = `[GlobalHeader]
	{
	missionname=Both;
	CommanderKilled=1;
	VictoryTimerRunsOut=120;
	KillUnitType=CORLAB, 3;
	KillUnitType=CORLAB, 4;
	[Schema 0]
		{
		Type=Easy;
		[units]
			{
			[unit0]
				{
				Unitname=ARMCOM;
				XPos=100;
				ZPos=100;
				}
			}
		}
	[Schema 1]
		{
		Type=Network 1;
		[units]
			{
			[unit0]
				{
				Unitname=CORCOM;
				XPos=200;
				ZPos=200;
				}
			}
		}
	}
`

func TestSchemaDispatchFollowsMissionTypeNotContent(t *testing.T) {
	fs := fsFromMapLoad(t, map[string]string{"maps/Both.ota": bothSchemaOTA})

	skirmish, err := LoadWithType(fs, TypeSkirmish, "Both.ota", 0, 0, nil)
	if err != nil {
		t.Fatalf("skirmish load: %v", err)
	}
	if skirmish.Schema.Name != "Schema 1" {
		t.Fatalf("skirmish picked %q, want the Network schema; content-sniffing "+
			"selection would pick Easy at difficulty 0", skirmish.Schema.Name)
	}
	if len(skirmish.Units) == 0 || skirmish.Units[0].UnitName != "CORCOM" {
		t.Fatalf("skirmish placements came from the wrong schema: %+v", skirmish.Units)
	}

	// TypeSaved joins the raw OTA path the same way [08 "Mission type dispatch"].
	saved, err := LoadWithType(fs, TypeSaved, "Both.ota", 0, 0, nil)
	if err != nil {
		t.Fatalf("saved load: %v", err)
	}
	if saved.Schema.Name != "Schema 1" {
		t.Fatalf("saved picked %q, want the Network schema", saved.Schema.Name)
	}
}

func TestAuthoredTriggersReachTheMission(t *testing.T) {
	fs := fsFromMapLoad(t, map[string]string{"maps/Both.ota": bothSchemaOTA})
	m, err := LoadWithType(fs, TypeSkirmish, "Both.ota", 0, 0, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	// KillUnitType and VictoryTimerRunsOut are victory kinds; CommanderKilled
	// is a defeat kind [08 "Victory trigger types"] [08 "Defeat trigger types"].
	if len(m.Victory) != 2 {
		t.Fatalf("victory queue %d, want 2: %+v", len(m.Victory), m.Victory)
	}
	if m.Victory[0].Kind != triggers.KindKillUnitType || m.Victory[0].Type != "CORLAB" || m.Victory[0].Args[0] != 4 {
		t.Fatalf("victory[0] = %+v, want one last-value KillUnitType CORLAB x4", m.Victory[0])
	}
	if m.Victory[1].Kind != triggers.KindVictoryTimerRunsOut || m.Victory[1].Args[0] != 120*30 {
		t.Fatalf("victory[1] = %+v, want the timer stored as seconds*30", m.Victory[1])
	}
	if len(m.Defeat) != 1 || m.Defeat[0].Kind != triggers.KindCommanderKilled {
		t.Fatalf("defeat queue %+v, want one CommanderKilled", m.Defeat)
	}
}

func TestMissionWithNoAuthoredConditionsGetsDefaults(t *testing.T) {
	const bare = `[GlobalHeader]
	{
	missionname=Bare;
	[Schema 0]
		{
		Type=Network 1;
		}
	}
`
	fs := fsFromMapLoad(t, map[string]string{"maps/Bare.ota": bare})
	m, err := LoadWithType(fs, TypeSkirmish, "Bare.ota", 0, 0, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	// Without this the mission loads with two empty queues and can never end
	// [08 "Default triggers"] C16.
	if len(m.Victory) != 1 || m.Victory[0].Kind != triggers.KindDestroyAllUnits {
		t.Fatalf("default victory %+v, want DestroyAllUnits", m.Victory)
	}
	if len(m.Defeat) != 1 || m.Defeat[0].Kind != triggers.KindAllUnitsKilled {
		t.Fatalf("default defeat %+v, want AllUnitsKilled", m.Defeat)
	}
}
