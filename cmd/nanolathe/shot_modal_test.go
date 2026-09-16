package main

import (
	"strings"
	"testing"
)

// `--shot-modal` presses authored `ARMOPT` buttons, in order, on the open
// options root. `MISSION` is one button whose child is the session kind's, so
// `settings` and `briefing` name it and refuse the session that would open the
// other child [07 R-FE-01 §7].
func TestShotModalRouteNamesAuthoredButtons(t *testing.T) {
	for _, tc := range []struct {
		modal    string
		campaign bool
		want     []string
	}{
		{modal: "options"},
		{modal: "exit", want: []string{"EXIT"}},
		{modal: "confirm", want: []string{"EXIT", "MAINMENU"}},
		{modal: "help", want: []string{"HELP"}},
		{modal: "help", campaign: true, want: []string{"HELP"}},
		{modal: "settings", want: []string{"MISSION"}},
		{modal: "briefing", campaign: true, want: []string{"MISSION"}},
	} {
		route, err := shotModalRoute(tc.modal, tc.campaign)
		if err != nil {
			t.Fatalf("--shot-modal %s (campaign=%v): %v", tc.modal, tc.campaign, err)
		}
		if len(route) != len(tc.want) {
			t.Fatalf("--shot-modal %s (campaign=%v) routes %v, want %v", tc.modal, tc.campaign, route, tc.want)
		}
		for i := range route {
			if route[i] != tc.want[i] {
				t.Fatalf("--shot-modal %s (campaign=%v) routes %v, want %v", tc.modal, tc.campaign, route, tc.want)
			}
		}
	}
}

// A session whose `MISSION` opens the other child is refused: the capture
// would otherwise record the window the reviewer did not ask for
// [07 R-FE-01 §7].
func TestShotModalRouteRefusesTheOtherMissionChild(t *testing.T) {
	for _, tc := range []struct {
		modal    string
		campaign bool
		mentions string
	}{
		{modal: "settings", campaign: true, mentions: "--shot-modal briefing"},
		{modal: "briefing", campaign: false, mentions: "--mission"},
	} {
		route, err := shotModalRoute(tc.modal, tc.campaign)
		if err == nil {
			t.Fatalf("--shot-modal %s (campaign=%v) routed %v, want a refusal", tc.modal, tc.campaign, route)
		}
		if !strings.Contains(err.Error(), tc.mentions) {
			t.Errorf("--shot-modal %s (campaign=%v) refused with %q, which does not name %s", tc.modal, tc.campaign, err, tc.mentions)
		}
	}
	if _, err := shotModalRoute("armopt", false); err == nil {
		t.Fatal("an unknown --shot-modal value was accepted")
	}
}
