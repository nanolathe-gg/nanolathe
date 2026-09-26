package session

import (
	"encoding/json"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/save"
)

// A computer player's parameters are its slot's layer over its difficulty's
// over the layer for every computer player, merged key by key
// (docs/DESIGN_SESSIONS_AI_SAVE.md "Modern AI computer player",
// "Configuration").
func TestAIOverridesMostSpecificLayerWins(t *testing.T) {
	o := AIOverrides{All: "jitter=0,style=eco,w_army=90"}
	o.Difficulty[2] = "style=units,w_army=120"
	o.Players[1] = "style=tower"
	for _, tc := range []struct {
		player     uint8
		difficulty ai.Difficulty
		want       string
	}{
		{1, ai.DifficultyHard, "jitter=0,style=tower,w_army=120"},
		{2, ai.DifficultyHard, "jitter=0,style=units,w_army=120"},
		{1, ai.DifficultyEasy, "jitter=0,style=tower,w_army=90"},
		{3, ai.DifficultyMedium, "jitter=0,style=eco,w_army=90"},
	} {
		got, err := o.For(tc.player, tc.difficulty)
		if err != nil || got != tc.want {
			t.Fatalf("player %d at %s: %q (%v), want %q", tc.player, tc.difficulty, got, err, tc.want)
		}
	}
	if got, err := (AIOverrides{}).For(1, ai.DifficultyHard); err != nil || got != "" {
		t.Fatalf("no configuration gave %q (%v)", got, err)
	}
	// The difficulty layer follows the persona's reading of the profile: a
	// plan other than easy or hard is medium.
	for plan, want := range map[ai.Difficulty]ai.Difficulty{ai.DifficultyEasy: ai.DifficultyEasy, ai.DifficultyHard: ai.DifficultyHard, ai.DifficultyAny: ai.DifficultyMedium} {
		if got := ControllerDifficulty(&ai.Profile{Plan: plan}); got != want {
			t.Fatalf("plan %s plays at %s, want %s", plan, got, want)
		}
	}
	if ControllerDifficulty(nil) != ai.DifficultyMedium {
		t.Fatal("a manager without a profile does not play at medium")
	}
}

// Canonical text is independent of the map's iteration order, so a manager,
// a report and a save's record always spell one set the same way.
func TestCanonicalAIParamsIgnoresMapOrder(t *testing.T) {
	kv := map[string]string{"w_army": "120", "style": "eco", "jitter": "0", "air": "0", "hn": "5", "tower_time": "1", "pv": "main", "micro": "0"}
	const want = "air=0,hn=5,jitter=0,micro=0,pv=main,style=eco,tower_time=1,w_army=120"
	for range 32 {
		got, err := CanonicalAIParams(kv)
		if err != nil || got != want {
			t.Fatalf("canonical %q (%v), want %q", got, err, want)
		}
	}
	back, err := ParseAIParams(" style = eco ,jitter=0")
	if err != nil || len(back) != 2 || back[0] != (AIParam{"style", "eco"}) || back[1] != (AIParam{"jitter", "0"}) {
		t.Fatalf("parse %+v (%v)", back, err)
	}
	for _, bad := range []string{"style", "style=", "=eco", "style=eco,style=units", "style=eco,,jitter=0"} {
		if _, err := ParseAIParams(bad); err == nil {
			t.Fatalf("%q parsed", bad)
		}
	}
	for _, bad := range []map[string]string{{"style": "a,b"}, {"a=b": "1"}, {"": "1"}, {"style": " eco"}} {
		if _, err := CanonicalAIParams(bad); err == nil {
			t.Fatalf("%v was given a canonical spelling", bad)
		}
	}
}

// A save's sidecar carries every computer player's parameters, and a load
// puts them back on the restored managers in place of whatever the load's
// host had configured; a player the record names none for plays the
// defaults, as it did in the saved game.
func TestAIControllerRecordCarriesOverrides(t *testing.T) {
	src := &Session{}
	src.AI[1] = &ai.Manager{Player: 1, BattleSeed: 44, ControllerParams: "jitter=0,style=eco"}
	src.AI[2] = &ai.Manager{Player: 2, BattleSeed: 44}
	src.AI[3] = &ai.Manager{Player: 3, BattleSeed: 44, ControllerParams: "w_army=120"}
	sidecar := SaveSidecar(src)
	data, err := json.Marshal(sidecar)
	if err != nil {
		t.Fatal(err)
	}
	var read save.Sidecar
	if err := json.Unmarshal(data, &read); err != nil {
		t.Fatal(err)
	}
	rec, err := SidecarAIControllers(read.AI)
	if err != nil || rec == nil {
		t.Fatalf("record %s: %v", read.AI, err)
	}
	want := []AIPlayerOverrides{{Player: 1, Params: "jitter=0,style=eco"}, {Player: 3, Params: "w_army=120"}}
	if len(rec.Overrides) != len(want) || rec.Overrides[0] != want[0] || rec.Overrides[1] != want[1] {
		t.Fatalf("recorded overrides %+v, want %+v", rec.Overrides, want)
	}

	dst := &Session{}
	for _, p := range []uint8{1, 2, 3} {
		dst.AI[p] = &ai.Manager{Player: p, BattleSeed: 9, ControllerParams: "style=tower"}
	}
	if err := restoreAIControllers(dst, rec); err != nil {
		t.Fatal(err)
	}
	for p, want := range map[uint8]string{1: "jitter=0,style=eco", 2: "", 3: "w_army=120"} {
		if got := dst.AI[p].ControllerParams; got != want {
			t.Fatalf("player %d restored %q, want %q", p, got, want)
		}
	}
}
