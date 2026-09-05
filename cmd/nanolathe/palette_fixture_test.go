package main

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/palette"
)

// retailPaletteForTest loads the shared retail palette tables the way the
// battle constructor does, and fails the test with the loader's own diagnostic
// if they are missing or malformed [02 §6].
//
// A loadPalette wrapper used to sit in cmd/nanolathe beside loadPaletteStrict,
// discarding the error and returning a possibly-nil *palette.Tables. Only tests
// called it, and what it bought them was a nil the caller then had to guess
// about: three call sites answered it with t.Skip, so a genuinely broken
// PALETTE.PAL would have reported as a skipped test rather than a failure,
// while the rest passed the nil into loadRetailBattleHUD. Every caller of this
// helper has already resolved a retail install, so an unreadable palette is a
// failure, not an absent asset.
func retailPaletteForTest(t *testing.T, cs *contentSet) *palette.Tables {
	t.Helper()
	tables, err := loadPaletteStrict(cs)
	if err != nil {
		t.Fatalf("retail palette: %v", err)
	}
	return tables
}
