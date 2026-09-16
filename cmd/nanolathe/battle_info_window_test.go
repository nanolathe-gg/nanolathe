package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
)

// The `[Help]` value splits at its first `|` past the opening byte, and a
// value that opens with `|` prints a single space in the key column
// [07 R-FE-01 §7][fmt tdf "HELP.TDF"].
func TestHelpLineSplit(t *testing.T) {
	for _, tc := range []struct {
		value string
		key   string
		desc  string
	}{
		{"CTRL+A|Select all units", "CTRL+A", "Select all units"},
		{"|", " ", ""},
		{"|blank row text", " ", "blank row text"},
		{"F1|Display information on selected unit", "F1", "Display information on selected unit"},
		{"no separator", "no separator", ""},
		{"", "", ""},
	} {
		got := splitBattleHelpLine(tc.value)
		if got.Key != tc.key || got.Description != tc.desc {
			t.Fatalf("%q split to %q/%q, want %q/%q", tc.value, got.Key, got.Description, tc.key, tc.desc)
		}
	}
}

// The overlay names the mode word's two line-of-sight bits: bit 1 clear is
// `Permanent` whatever bit 2 holds [03 R-VIS-01 §1][08 R-SKIR-01 §11].
func TestGameOptionsLineOfSightNames(t *testing.T) {
	for _, tc := range []struct {
		mode visibility.Mode
		want string
	}{
		{0, "Permanent"},
		{visibility.ModeTerrainRay, "Permanent"},
		{visibility.ModeCurrentEnabled, "Circular"},
		{visibility.ModeCurrentEnabled | visibility.ModeTerrainRay, "True"},
	} {
		if got := battleGameOptionsLineOfSight(tc.mode); got != tc.want {
			t.Fatalf("mode %d named %q, want %q", tc.mode, got, tc.want)
		}
	}
}

// The nine single-player rows are printed in the established order with the
// established names [07 R-FE-01 §7][08 R-SKIR-01 §11].
func TestGameOptionsRowOrderAndValues(t *testing.T) {
	sess := &session.Session{}
	sess.Skirmish.CommanderDeath = 2
	sess.Skirmish.Location = 1
	sess.Skirmish.Difficulty = 1
	sess.Skirmish.MapName = "Great Divide"
	sess.Skirmish.Players[0].Metal, sess.Skirmish.Players[0].Energy = 1500, 2500
	b := &battleSession{sess: sess}

	rows := battleGameOptionsRows(b)
	want := []battleGameOptionsRow{
		{"Commander Death:", "Deathmatch"},
		{"Starting Locations:", "Fixed"},
		{"Mapping Mode:", "Mapped"},
		{"Line of Sight:", "Permanent"},
		{"Difficulty:", "Medium"},
		{"Map:", "Great Divide"},
		{"Starting Metal:", "1500"},
		{"Starting Energy:", "2500"},
		{"Max Units:", "0"},
	}
	if len(rows) != len(want) {
		t.Fatalf("printed %d rows, want %d", len(rows), len(want))
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Fatalf("row %d is %v, want %v", i, rows[i], want[i])
		}
	}
}

// Appending a row keeps the authored record set intact, and a refill restores
// it before the next page's labels are appended [07 R-FE-02 §5].
func TestBattleInfoLabelAppendAndTruncate(t *testing.T) {
	window := &gui.Window{
		Rect:    gui.Rect{W: 200, H: 200},
		Gadgets: []gui.Gadget{{Kind: gui.KindPanel, Name: "HEADER"}, {Kind: gui.KindButton, Name: "OK"}},
	}
	authored := len(window.Gadgets)
	appendBattleInfoLabel(window, "row", 18, 90, 110)
	appendBattleInfoLabel(window, "auto", 18, 108, -1)
	if len(window.Gadgets) != authored+2 {
		t.Fatalf("append left %d gadgets", len(window.Gadgets))
	}
	row := window.Gadgets[authored]
	if row.Kind != gui.KindLabel || row.Rect.H != battleInfoLabelHeight || row.ColorF != battleInfoLabelColor || row.Active != 1 {
		t.Fatalf("appended record is %+v", row)
	}
	// Both openers rewrite every appended record's attribute word to the LEFT
	// bit after the append helper stored 2, so a printed row is left-aligned
	// at its column x. A centred row overruns its column on the left and the
	// pen's width limit then eats its tail [07 R-FE-01 §7][03 R-FONT-01 §6].
	if row.Attribs != 1 {
		t.Fatalf("appended record carries attribute word %d, want the left bit 1", row.Attribs)
	}
	if got := window.Gadgets[authored+1].Rect.W; got != int32(200-18-5) {
		t.Fatalf("the -1 width resolved to %d, want panelWidth - x - 5", got)
	}
	truncateBattleInfoWindow(window, authored)
	if len(window.Gadgets) != authored {
		t.Fatalf("truncate left %d gadgets, want the authored %d", len(window.Gadgets), authored)
	}
}
