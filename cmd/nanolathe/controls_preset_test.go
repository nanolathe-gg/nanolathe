package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/modlibrary"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

func presetTestShell(t *testing.T) *gameShell {
	t.Helper()
	t.Cleanup(func() { applyRetailAudioOptions(settings.DefaultAudio()) })
	g := &gameShell{presentation: settings.DefaultPresentation(), audioPrefs: settings.DefaultAudio()}
	g.setup.NumPlayers = settings.DefaultNumPlayers
	return g
}

// TestCommunityControlsPresetContents locks ProTA's recommended settings
// (docs/DESIGN_MODS_MUTATORS.md §4.3): the Community host options plus the
// preferences ProTA.ini pins, each written as its own stored value.
func TestCommunityControlsPresetContents(t *testing.T) {
	g := presetTestShell(t)
	g.applyControlsPreset(controlsPresetCommunity)
	p := g.presentation
	for name, value := range map[string]int{
		"communitySelection": p.CommunitySelection, "doubleClickSelection": p.DoubleClickSelection,
		"queuedOrderDrag": p.QueuedOrderDrag, "communityCounters": p.CommunityCounters,
		"reloadBars": p.ReloadBars, "veteranLabels": p.VeteranLabels, "groupNumbers": p.GroupNumbers,
		"weatherReport": p.WeatherReport, "overview": p.Overview, "megamapWheel": p.MegamapWheel,
		"megamapWheelMove": p.MegamapWheelMove, "megamapFlash": p.MegamapFlash, "victoryCue": p.VictoryCue,
		"alliedDotSwatches": p.AlliedDotSwatches,
	} {
		if value != 1 {
			t.Errorf("presentation.%s = %d, want 1", name, value)
		}
	}
	if p.MegamapDoubleClickMove != 0 || p.MegamapRadarMinimum != 0 || p.MegamapSonarMinimum != 0 || p.MegamapSonarJamMinimum != 0 || p.MegamapAntiNukeMinimum != 0 {
		t.Error("the megamap double-click move and ring minimums are not ProTA's zeros")
	}
	if p.PlayerDotColors != [10]int{227, 249, 18, 250, 67, 149, 208, 117, 210, 34} {
		t.Errorf("dot colours = %v, want ProTA.ini's", p.PlayerDotColors)
	}
	if !g.switchAlt || !g.clockVisible {
		t.Errorf("switchAlt %v, clock %v; want both set", g.switchAlt, g.clockVisible)
	}
	if a := g.audioPrefs; a.SoundMode != settings.SoundMode3D || a.MixingBuffers != 128 || a.CDMode != 2 {
		t.Errorf("audio = sound mode %d, mixing buffers %d, cd mode %d; want 2, 128, 2", a.SoundMode, a.MixingBuffers, a.CDMode)
	}
	if g.setup.NumPlayers != settings.MaxPlayers {
		t.Errorf("skirmish rows = %d, want %d", g.setup.NumPlayers, settings.MaxPlayers)
	}
	// Nothing outside the table moves: the player's other choices stay.
	if p.AlliedResources != 0 || p.Renderer != settings.DefaultPresentation().Renderer || g.audioPrefs.FXVol != settings.DefaultFXVol {
		t.Error("the preset wrote a setting outside its table")
	}
	rows, changes := g.controlsOfferRows(controlsPresetCommunity)
	if len(rows) != len(controlsPresetRows) || changes != 0 {
		t.Errorf("after applying, the offer lists %d rows with %d changes; want %d and 0", len(rows), changes, len(controlsPresetRows))
	}
}

// TestRetailControlsPresetKeepsSkirmishRows: the retail preset restores the
// retail defaults but never removes skirmish rows.
func TestRetailControlsPresetKeepsSkirmishRows(t *testing.T) {
	g := presetTestShell(t)
	g.applyControlsPreset(controlsPresetCommunity)
	g.applyControlsPreset(controlsPresetRetail)
	p := g.presentation
	if p.CommunitySelection != 0 || p.QueuedOrderDrag != 0 || p.GroupNumbers != 0 || g.switchAlt || g.clockVisible {
		t.Error("the retail preset left a Community option on")
	}
	if a := g.audioPrefs; a.SoundMode != settings.DefaultSoundMode || a.MixingBuffers != settings.DefaultMixingBuffers || a.CDMode != settings.DefaultCDMode {
		t.Errorf("audio = %+v, want the retail defaults", a)
	}
	if g.setup.NumPlayers != settings.MaxPlayers {
		t.Errorf("the retail preset changed the skirmish rows to %d", g.setup.NumPlayers)
	}
	if p.Overview != settings.OverviewZoom || p.VictoryCue != 0 || p.AlliedDotSwatches != 0 || p.PlayerDotColors != settings.DefaultPlayerDotColors {
		t.Errorf("overview %d, victory cue %d, dot colours %v; want Zoom, off and the draw engine's defaults", p.Overview, p.VictoryCue, p.PlayerDotColors)
	}
	// The megamap's own preferences are the player's, whichever overview.
	g.presentation.MegamapFlash, g.presentation.MegamapRadarMinimum = 0, 64
	g.applyControlsPreset(controlsPresetRetail)
	if g.presentation.MegamapFlash != 0 || g.presentation.MegamapRadarMinimum != 64 {
		t.Error("the retail preset changed a megamap preference")
	}
	p = g.presentation
	g.applyControlsPreset("unknown")
	if g.presentation != p {
		t.Error("an unknown preset changed a setting")
	}
}

// TestDotColoursRowRoundTrip: the dot colour table is one row naming its
// two tables. Each survives a settings round trip, and any other table
// reads as Custom, which the ProTA preset replaces.
func TestDotColoursRowRoundTrip(t *testing.T) {
	var row controlsPresetRow
	for _, r := range controlsPresetRows {
		if r.label == "Dot colours" {
			row = r
		}
	}
	if row.get == nil {
		t.Fatal("no Dot colours row")
	}
	t.Setenv(settings.EnvPath, filepath.Join(t.TempDir(), "settings.json"))
	for _, preset := range []string{controlsPresetCommunity, controlsPresetRetail} {
		g := presetTestShell(t)
		g.applyControlsPreset(preset)
		stored := settings.Defaults()
		stored.Presentation = g.presentation
		if err := stored.Save(); err != nil {
			t.Fatal(err)
		}
		loaded, err := settings.Load()
		if err != nil {
			t.Fatal(err)
		}
		g.presentation = loaded.Presentation
		if got, want := row.get(g), row.presetValue(preset); got != want {
			t.Errorf("%s: reloaded row = %s, want %s", preset, row.valueText(got), row.valueText(want))
		}
	}
	g := presetTestShell(t)
	g.presentation.PlayerDotColors[3] = 1
	if got := row.get(g); got != presetCustom || row.valueText(got) != "Custom" {
		t.Fatalf("an edited table reads %d", got)
	}
	rows, _ := g.controlsOfferRows(controlsPresetCommunity)
	if !slices.Contains(rows, "Dot colours: ProTA (now Custom)") {
		t.Fatalf("offer rows %q", rows)
	}
}

// TestControlsOfferIsRememberedPerMod: the offer's key is the mod id, or the
// content profile when no mod is mounted; once recorded it is not due again,
// and the saved list survives a settings round trip without duplicates.
func TestControlsOfferIsRememberedPerMod(t *testing.T) {
	mod := &modlibrary.Mod{Metadata: modlibrary.Metadata{ID: "local-prota-4-8", Name: "ProTA4.8", Controls: "community"}}
	for _, tc := range []struct {
		cs      *contentSet
		preset  string
		wantKey string
	}{
		{&contentSet{mod: mod, profile: "prota", profileControls: "community"}, "community", "local-prota-4-8"},
		{&contentSet{mod: &modlibrary.Mod{Metadata: modlibrary.Metadata{ID: "plain"}}, profileControls: "community"}, "", ""},
		{&contentSet{profile: "prota", profileControls: "community"}, "community", "profile:prota"},
		{&contentSet{profile: "retail"}, "", ""},
	} {
		g := &gameShell{cs: tc.cs}
		preset, key, _ := g.controlsPresetOffer()
		if preset != tc.preset || key != tc.wantKey {
			t.Errorf("offer = %q/%q, want %q/%q", preset, key, tc.preset, tc.wantKey)
		}
	}

	g := &gameShell{cs: &contentSet{mod: mod}}
	g.markControlsOffered("local-prota-4-8")
	g.markControlsOffered("local-prota-4-8")
	if len(g.controlsOffered) != 1 || !settings.ControlsWereOffered(g.controlsOffered, "local-prota-4-8") || settings.ControlsWereOffered(g.controlsOffered, "prota") {
		t.Fatalf("offered = %q", g.controlsOffered)
	}
	path := filepath.Join(t.TempDir(), "settings.json")
	t.Setenv(settings.EnvPath, path)
	stored := settings.Defaults()
	stored.ControlsOffered = []string{"prota", " prota", "", "local-prota-4-8"}
	if err := stored.Save(); err != nil {
		t.Fatal(err)
	}
	loaded, err := settings.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.ControlsOffered; len(got) != 2 || got[0] != "prota" || got[1] != "local-prota-4-8" {
		t.Fatalf("round trip = %q", got)
	}
	// A shell that cannot write its settings never offers: the answer could
	// not be remembered.
	g.settingsWritable = false
	if g.wantsControlsOffer() {
		t.Fatal("an unwritable shell wants to offer")
	}
}

// TestStrategicIconDiscoveryOrder: an empty preference searches the running
// mod's directory, or a manual stack from its last root to its first; a root
// without a configuration is passed over silently, the first root holding one
// wins, and an explicit preference always wins.
func TestStrategicIconDiscoveryOrder(t *testing.T) {
	writeConfig := func(root, rel string) string {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("[Option]\nUseDefaultIcon=true\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	base, first, last := t.TempDir(), t.TempDir(), t.TempDir()
	firstConfig := writeConfig(first, "Icon/iconcfg.ini")

	manual := &contentSet{manualRoots: true, roots: []string{base, first, last}}
	roots := strategicIconSearchRoots(manual)
	if len(roots) != 3 || roots[0] != last || roots[2] != base {
		t.Fatalf("manual stack search order = %q", roots)
	}
	if got, err := automaticStrategicIconConfig(roots); err != nil || got != firstConfig {
		t.Fatalf("found %q/%v, want the only configuration %q", got, err, firstConfig)
	}
	lastConfig := writeConfig(last, "iconcfg.ini")
	if got, err := automaticStrategicIconConfig(roots); err != nil || got != lastConfig {
		t.Fatalf("found %q/%v, want the last root's %q", got, err, lastConfig)
	}
	writeConfig(last, "ZIcon/iconcfg.ini")
	if _, err := automaticStrategicIconConfig(roots); err == nil {
		t.Fatal("an ambiguous root was not reported")
	}

	if got := strategicIconSearchRoots(&contentSet{roots: []string{base}}); got != nil {
		t.Fatalf("the base install alone is searched: %q", got)
	}
	withMod := &contentSet{roots: []string{base, first}, mod: &modlibrary.Mod{Dir: first}}
	if got := strategicIconSearchRoots(withMod); len(got) != 1 || got[0] != first {
		t.Fatalf("mod search roots = %q", got)
	}
	if got, err := automaticStrategicIconConfig([]string{base}); err != nil || got != "" {
		t.Fatalf("a root without a configuration gave %q/%v", got, err)
	}
	if icons, err := battleStrategicIcons(nil, "", []string{base}); icons == nil || err != nil {
		t.Fatalf("none found = %v/%v, want the generated catalog silently", icons, err)
	}
	// The explicit preference is loaded even where discovery would fail.
	if icons, err := battleStrategicIcons(nil, firstConfig, roots); icons == nil || err != nil {
		t.Fatalf("explicit preference = %v/%v", icons, err)
	}
}
