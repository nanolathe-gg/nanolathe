package aikit

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
)

// The Modern AI's controller builds the survival brain only for a Survival
// battle's computer buddy, and only while survival is not switched off:
// a manager with no scenario (every other battle), the human's own manager
// and survival=0 all get the skirmish brain exactly as before.
func TestSurvivalBrainOnlyForComputerSurvivors(t *testing.T) {
	info := &ai.SurvivalInfo{CentreX: 2048, CentreZ: 2048, Attacker: 2,
		Team: []uint8{0, 1}, Computer: []bool{false, true}, Starts: [][2]int32{{2048, 2048}, {2368, 2048}}}
	for _, c := range []struct {
		name   string
		m      *ai.Manager
		params string
		want   string
	}{
		{"skirmish", &ai.Manager{Player: 1}, "", "util+tac"},
		{"survivor", &ai.Manager{Player: 1, Survival: info}, "", "survival"},
		{"configured survivor", &ai.Manager{Player: 1, Survival: info}, "style=eco,sv_tower=30", "survival"},
		{"survival off", &ai.Manager{Player: 1, Survival: info}, "survival=0", "util+tac"},
		{"human", &ai.Manager{Player: 0, Survival: info}, "", "util+tac"},
		{"attacker", &ai.Manager{Player: 2, Survival: info}, "", "util+tac"},
	} {
		c.m.ControllerParams = c.params
		if got := newUtilTacHost(c.m).Brain().Name(); got != c.want {
			t.Errorf("%s: plays %s, want %s", c.name, got, c.want)
		}
	}
}

// The survival keys join the strict vocabulary with their ranges.
func TestValidateParamsReadsTheSurvivalKeys(t *testing.T) {
	if _, err := ValidateParamsText("survival=0,sv_tower=40,sv_walls=0,sv_claim=50,style=eco"); err != nil {
		t.Fatalf("a valid set was refused: %v", err)
	}
	for _, bad := range []string{"survival=2", "sv_tower=101", "sv_walls=01", "sv_claim=-1", "sv_towers=10"} {
		if _, err := ValidateParamsText(bad); err == nil {
			t.Fatalf("%q was accepted", bad)
		}
	}
}

// A warning is visible only to observations at or after the tick it was
// published, a list already handed out never changes, and the feed returns
// the same warnings however often it is asked.
func TestSurvivalWarningsFollowTheObservationTick(t *testing.T) {
	info := &ai.SurvivalInfo{}
	f := &survivalFeed{info: info}
	info.PublishWarning(ai.SurvivalWarning{Wave: 1, Tick: 100, Arrive: 1000, Groups: []ai.SurvivalApproach{{Angle: 16384, Domain: "air"}}})
	if got := f.Warnings(99, nil); len(got) != 0 {
		t.Fatalf("tick 99 sees %d warnings", len(got))
	}
	first := f.Warnings(100, nil)
	if len(first) != 1 || !first[0].Groups[0].Air || first[0].Groups[0].Angle != 16384 {
		t.Fatalf("tick 100 sees %+v", first)
	}
	held := info.Warnings(100, nil)
	info.PublishWarning(ai.SurvivalWarning{Wave: 2, Tick: 5000, Arrive: 5900, Groups: []ai.SurvivalApproach{{Angle: 0, Domain: "ground"}}})
	if len(held) != 1 || held[0].Wave != 1 {
		t.Fatalf("a published list changed: %+v", held)
	}
	if got := f.Warnings(4999, nil); len(got) != 1 {
		t.Fatalf("tick 4999 sees %d warnings", len(got))
	}
	if got := f.Warnings(5000, nil); len(got) != 2 || !got[1].Groups[0].Other {
		t.Fatalf("tick 5000 sees %+v", got)
	}
}
