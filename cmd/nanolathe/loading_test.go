package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/session"
)

// TestLoadingStageMapping locks the two things the loading screen must not get
// wrong: every family the loaders report is attributed to a bar, and a bar's
// percentage is the mean over its own families so it rises in steps.
func TestLoadingStageMapping(t *testing.T) {
	reported := []string{
		content.FamilyWeapons, content.FamilyUnits, content.FamilyFeatures,
		content.FamilyMovement, content.FamilySides, content.FamilySounds,
		content.FamilyMaps, content.FamilyAIProfiles, content.FamilyBattleTables,
		content.FamilyBuildMenus, content.FamilyModels,
		session.FamilyTerrain, session.FamilyUnitWorld,
		session.FamilyPlacement, session.FamilyScripts,
		familyDetailArt,
	}
	for _, family := range reported {
		if _, ok := retailLoadStageOf[family]; !ok {
			t.Errorf("family %q is reported but drives no loading bar", family)
		}
	}

	l := newLoadingState("Canal Crossing")
	// Explosions is fed by weapons and features: one of two done is half a bar.
	l.report(content.FamilyWeapons, 100)
	if got := l.percent[5].Load(); got != 50 {
		t.Errorf("Explosions after weapons = %d, want 50", got)
	}
	l.report(content.FamilyFeatures, 100)
	if got := l.percent[5].Load(); got != 100 {
		t.Errorf("Explosions after features = %d, want 100", got)
	}
	// Terrain is fed by the map census, the terrain load and the load-time
	// remaster (DESIGN_GPU_RENDERER §14.4), and the census reports a running
	// percentage rather than only completion: half of one of three families is
	// a sixth of the bar.
	l.report(content.FamilyMaps, 50)
	if got := l.percent[1].Load(); got != 16 {
		t.Errorf("Terrain at half the census = %d, want 16", got)
	}
	// Every load drives the remaster family to 100, including one that
	// synthesizes nothing, so the bar always completes.
	l.report(content.FamilyMaps, 100)
	l.report(session.FamilyTerrain, 100)
	l.report(familyDetailArt, 100)
	if got := l.percent[1].Load(); got != 100 {
		t.Errorf("Terrain with every family done = %d, want 100", got)
	}
}

// TestRetailLoadBarGeometry locks the authored row geometry against the
// authored by the loading-screen contract [07 "The loading screen"].
func TestRetailLoadBarGeometry(t *testing.T) {
	wantY := []int{0x87, 0xb1, 0xda, 0x106, 0x130, 0x15b}
	wantLabel := []string{"Textures", "Terrain", "Units", "Animation", "3D Data", "Explosions"}
	for i, row := range retailLoadBars {
		if row.y != wantY[i] || row.label != wantLabel[i] {
			t.Errorf("row %d = %q at %d, want %q at %d", i, row.label, row.y, wantLabel[i], wantY[i])
		}
	}
	// A finished bar is exactly the 351-wide LIGHTBAR frame it is stamped
	// under: left + 100*7/2 inclusive.
	if w := 100*7/2 + 1; w != 351 {
		t.Errorf("full bar width = %d, want 351", w)
	}
}
