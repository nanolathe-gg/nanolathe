package main

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/modlibrary"
	"github.com/nanolathe-gg/nanolathe/internal/replay"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// useReplaysTestTemplate replaces the load dialog with an authored window of
// its shape — LOADGAME.GUI's controls in their places — so the screen builds
// without retail assets.
func useReplaysTestTemplate(t *testing.T) {
	t.Helper()
	saved := replaysWindowTemplate
	replaysWindowTemplate = func(*gameShell) (*gui.Window, *formats.PCX, error) {
		label := func(name string, x, y, w, h int32) gui.Gadget {
			return gui.Gadget{Kind: gui.KindLabel, Name: name, Active: 1, ColorF: 15, Attribs: 0x31, Rect: gui.Rect{X: x, Y: y, W: w, H: h}}
		}
		button := func(name, text string, x, y int32) gui.Gadget {
			return gui.Gadget{Kind: gui.KindButton, Name: name, Text: text, Active: 1, ColorF: 15, Attribs: 2, Rect: gui.Rect{X: x, Y: y, W: 96, H: 20}}
		}
		return &gui.Window{Name: "LOADGAME", Rect: gui.Rect{X: 81, Y: 27, W: 494, H: 420}, Header: gui.Header{CrDefault: "LOAD"}, Gadgets: []gui.Gadget{
			{Kind: gui.KindPanel, Name: "HEADER", Active: 1, Rect: gui.Rect{X: 81, Y: 27, W: 494, H: 420}},
			{Kind: gui.KindListBox, Name: "GAMES", Active: 1, Attribs: 1, Assoc: 1, ColorF: 15, Rect: gui.Rect{X: 65, Y: 71, W: 242, H: 176}},
			button("LOAD", "OK", 353, 324),
			button("CANCEL", "Cancel", 353, 359),
			{Kind: gui.KindScrollBar, Name: "SLIDER", Active: 1, Attribs: 2, Assoc: 1, Rect: gui.Rect{X: 317, Y: 63, W: 16, H: 184}},
			{Kind: gui.KindTextBox, Name: "GAMENAME", Text: "Name Of Game to Load", Active: 1, ColorF: 15, MaxChars: 20, Rect: gui.Rect{X: 111, Y: 275, W: 230, H: 20}},
			label("GAMETYPE", 162, 315, 147, 15), label("MISSION", 162, 346, 157, 18), label("TIME", 162, 378, 155, 15), label("SIDE", 162, 330, 157, 18),
			{Kind: gui.KindSurface, Name: "RADAR", Active: 1, Attribs: 0x402, Rect: gui.Rect{X: 348, Y: 71, W: 121, H: 113}},
			label("DIFF", 162, 362, 155, 16),
			button("DELETE", "Delete Game", 367, 207),
			button("SaveGame", "", 48, 17),
		}}, nil, nil
	}
	t.Cleanup(func() { replaysWindowTemplate = saved })
}

// replaysTestShell is a shell on the main menu, with a skirmish map whose
// terrain is present when maps is set, recording to dir.
func replaysTestShell(t *testing.T, dir string, maps bool) *gameShell {
	t.Helper()
	useReplaysTestTemplate(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Cleanup(func() { pendingContentReload, pendingReplayWatch = nil, nil })
	files := map[string][]byte{}
	if maps {
		files["maps/Ashap Plateau.tnt"] = nil
	}
	g := demoSaveShell(t, files)
	window := func(names ...string) *gui.Window {
		w := &gui.Window{Rect: gui.Rect{W: 640, H: 480}, Gadgets: []gui.Gadget{{Kind: gui.KindPanel, Active: 1}}}
		for i, name := range names {
			w.Gadgets = append(w.Gadgets, gui.Gadget{Kind: gui.KindButton, Name: name, Active: 1, Rect: gui.Rect{X: 139, Y: int32(393 + 37*i), W: 96, H: 20}})
		}
		return w
	}
	g.assets = &menuAssets{panel: map[shellMode]*retailPanelAssets{
		modeMenuMain: {window: window("SINGLE", "MULTI")}, modeMenuSkirmish: {window: window("Start")}, modeMenuMap: {window: window("MAPNAMES")},
	}}
	g.maps = []string{"Ashap Plateau"}
	g.opts.ReplayDir = dir
	g.frontend = ui.NewFrontend(modeMenuMain)
	g.openMenu(modeMenuMain)
	return g
}

// writeTestReplay records a finished replay of ticks into dir.
func writeTestReplay(t *testing.T, dir string, h replay.Header, ticks uint32) string {
	t.Helper()
	return recordTestReplay(t, replayTarget{dir: dir}, h, ticks, replay.EndFinished)
}

// The list and the summary say when, where, what, how long and who, and what
// stands in the way: an incomplete recording, the one being recorded now, a
// damaged file.
func TestReplayListingText(t *testing.T) {
	dir := t.TempDir()
	g := &gameShell{cs: testContentSet(vfs.New()), maps: []string{"Ashap Plateau"}}
	started := time.Date(2026, 10, 9, 14, 32, 5, 0, time.Local)
	writeTestReplay(t, dir, fileTestHeader(replay.KindSurvival, "ashap plateau", started), 30*250)
	// A recording a crash cut short after its first flush.
	crashed, err := startReplayRecording(replayTarget{dir: dir}, fileTestHeader(replay.KindOnlineSkirmish, "The Pass", started.Add(-time.Hour)))
	if err != nil {
		t.Fatal(err)
	}
	for tick := uint32(1); tick <= 30*62; tick++ {
		_ = crashed.w.Pump(tick, 1)
	}
	if err := crashed.flush(); err != nil {
		t.Fatal(err)
	}
	close(crashed.stop)
	<-crashed.done
	_ = crashed.file.Close()
	markReplayActive(crashed.part, false)
	crashed.setLive(false)
	// The one being recorded now.
	live, err := startReplayRecording(replayTarget{dir: dir}, fileTestHeader(replay.KindSkirmish, "Lava Run", started.Add(time.Hour)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { live.abort() })
	broken := filepath.Join(dir, "broken.nlreplay")
	if err := os.WriteFile(broken, []byte("not a replay"), 0o644); err != nil {
		t.Fatal(err)
	}
	stamp := started.Add(-48 * time.Hour)
	_ = os.Chtimes(broken, stamp, stamp)
	list, err := listReplays(dir)
	if err != nil || len(list) != 4 {
		t.Fatalf("listing %+v: %v", list, err)
	}
	type want struct {
		row, kind, mapName, players, length, played, note string
	}
	for i, w := range []want{
		{"Oct 9 15:32  Lava Run", "Skirmish", "Lava Run", "Ann, Computer 2", "0:00, recording now", "9 Oct 2026 15:32", "This battle is being recorded now, so its replay cannot be deleted."},
		{"Oct 9 14:32  Ashap Plateau", "Survival", "Ashap Plateau", "Ann, Computer 2", "4:10", "9 Oct 2026 14:32", ""},
		{"Oct 9 13:32  The Pass", "Online skirmish", "The Pass", "Ann, Computer 2", "1:02, incomplete", "9 Oct 2026 13:32", "The recording stopped early. It plays to where it stops."},
		{"Oct 7 14:32  (unreadable file)", "Unreadable", "", "", "", "7 Oct 2026 14:32", "Cannot play: the file is damaged."},
	} {
		l := list[i]
		label := g.replayMapLabel(l.Map())
		fields := replaySummaryFields(l, true, label)
		got := want{replayRowText(l, label), fields["GAMETYPE"], fields["SIDE"], fields["MISSION"], fields["DIFF"], fields["TIME"], g.replayNote(l)}
		if got != w {
			t.Errorf("listing %d (%s):\n got %+v\nwant %+v", i, l.Name, got, w)
		}
	}
	if fields := replaySummaryFields(replayListing{}, false, ""); len(fields) != len(replaysSummaryRows) || fields["GAMETYPE"] != "" {
		t.Fatalf("an empty selection shows %v", fields)
	}
	if replayKindText(replay.KindOnlineSurvival) != "Online Survival" || replayKindText(0) != "Unknown" {
		t.Fatal("kind names")
	}
	for err, text := range map[error]string{
		replay.ErrUnsupportedVersion:                                                "a newer version of Nanolathe recorded it.",
		fmt.Errorf("x: %w", os.ErrNotExist):                                         "the file is no longer there.",
		errors.New("permission denied"):                                             "the file cannot be read.",
		fmt.Errorf("%w: %w", replay.ErrIncompatible, session.ErrMatchMapMismatch):   "your copy of the map differs from the recording's.",
		fmt.Errorf("%w: %w", replay.ErrIncompatible, session.ErrMatchRulesMismatch): "this version plays its rules differently.",
		replay.ErrIncompatible:                                                      "this version cannot set up its battle.",
	} {
		if got := replayOpenProblem(err); got != text {
			t.Errorf("%v: %q, want %q", err, got, text)
		}
	}
}

// REPLAYS sits beside NANOLATHE, the pair centred, and is greyed where
// Skirmish is. It opens the screen over the main menu; an empty directory
// says so and offers nothing to watch or delete, and Cancel returns.
func TestMainMenuReplaysOpensTheScreen(t *testing.T) {
	g := replaysTestShell(t, t.TempDir(), false)
	p := g.activePanel()
	mods, replays := p.Index("MODS"), p.Index(replaysButton)
	if mods < 0 || replays < 0 {
		t.Fatal("the main menu has no NANOLATHE and REPLAYS pair")
	}
	m, r := p.Window.Gadgets[mods].Rect, p.Window.Gadgets[replays].Rect
	if r.Y != m.Y || r.X != m.X+m.W+8 || m.X+(r.X+r.W) != retailScreenW || p.Window.Gadgets[replays].Text != "REPLAYS" {
		t.Fatalf("NANOLATHE at %+v, REPLAYS at %+v", m, r)
	}
	if !greyed(p, replaysButton) {
		t.Fatal("REPLAYS is enabled without a skirmish map")
	}
	g = replaysTestShell(t, t.TempDir(), true)
	if greyed(g.activePanel(), replaysButton) {
		t.Fatal("REPLAYS is greyed where Skirmish is enabled")
	}
	g.activateGadget(replaysButton)
	s := g.replays
	if s == nil || !g.replaysPanelActive() {
		t.Fatal("REPLAYS did not open the screen")
	}
	if !s.panel.ActiveAt(s.panel.Index(replaysEmpty)) || s.panel.TextOf(replaysEmpty) != replaysEmptyText || !greyed(s.panel, "LOAD") || !greyed(s.panel, "DELETE") {
		t.Fatalf("empty screen: %q", s.panel.TextOf(replaysEmpty))
	}
	if s.panel.Index("GAMENAME") >= 0 || s.panel.Index("SaveGame") >= 0 || s.panel.TextOf("LOAD") != "Watch" || s.panel.TextOf(replaysTitle) != "REPLAYS" {
		t.Fatal("the load dialog was not recaptioned for replays")
	}
	g.activateGadget("CANCEL")
	if g.replays != nil || g.activePanel().Index(replaysButton) < 0 {
		t.Fatal("Cancel did not return to the main menu")
	}
}

// Delete asks first and refuses the replay being recorded now; a confirmed
// delete removes the file and refreshes the list.
func TestReplaysScreenDelete(t *testing.T) {
	dir := t.TempDir()
	g := replaysTestShell(t, dir, true)
	started := time.Date(2026, 10, 9, 14, 32, 5, 0, time.Local)
	kept := writeTestReplay(t, dir, fileTestHeader(replay.KindSkirmish, "Ashap Plateau", started), 300)
	live, err := startReplayRecording(replayTarget{dir: dir}, fileTestHeader(replay.KindSkirmish, "Ashap Plateau", started.Add(time.Hour)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { live.abort() })
	g.activateGadget(replaysButton)
	s := g.replays
	if len(s.list) != 2 || s.selected != 0 || !s.list[0].Recording {
		t.Fatalf("listing %+v", s.list)
	}
	if !greyed(s.panel, "DELETE") || greyed(s.panel, "LOAD") {
		t.Fatal("the recording's Delete is offered")
	}
	g.activateGadget("DELETE")
	if s.confirm != nil || !strings.Contains(s.panel.TextOf(replaysStatus), "recorded now") {
		t.Fatalf("Delete of the recording: %q", s.panel.TextOf(replaysStatus))
	}
	if _, err := os.Stat(live.part); err != nil {
		t.Fatal("the recording was deleted")
	}
	// The confirmation's own panel: Cancel keeps the file, Delete removes it.
	g.selectReplayRow(1)
	if greyed(s.panel, "DELETE") {
		t.Fatal("a finished replay cannot be deleted")
	}
	confirm := ui.NewPanel(&gui.Window{Gadgets: []gui.Gadget{{Kind: gui.KindPanel}}})
	s.confirm, s.confirmPath = confirm, kept
	if !g.finishReplayDeleteConfirmation(confirm, "OK") {
		t.Fatal("the confirmation was not the screen's")
	}
	if _, err := os.Stat(kept); err != nil {
		t.Fatal("Cancel deleted the replay")
	}
	s.confirm, s.confirmPath = confirm, kept
	g.finishReplayDeleteConfirmation(confirm, replaysDeleteConfirm)
	if _, err := os.Stat(kept); !errors.Is(err, os.ErrNotExist) || len(s.list) != 1 || s.selected != 0 {
		t.Fatalf("Delete left %v, listing %d", err, len(s.list))
	}
	s.confirm, s.confirmPath = confirm, live.part
	g.finishReplayDeleteConfirmation(confirm, replaysDeleteConfirm)
	if _, err := os.Stat(live.part); err != nil || !strings.Contains(s.panel.TextOf(replaysStatus), "recorded now") {
		t.Fatalf("a confirmed delete of the recording: %v, %q", err, s.panel.TextOf(replaysStatus))
	}
}

// installTestMod installs a one-file mod archive into the test's library.
func installTestMod(t *testing.T) modlibrary.Mod {
	t.Helper()
	var archive bytes.Buffer
	z := zip.NewWriter(&archive)
	w, err := z.Create(modlibrary.MetadataFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(`{"schema":1,"id":"testmod","name":"Test Mod","version":"1"}`)); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "testmod.zip")
	if err := os.WriteFile(path, archive.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	lib, err := openModLibrary()
	if err != nil {
		t.Fatal(err)
	}
	installed, err := lib.InstallArchive(path, modlibrary.InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return installed
}

// Watch says in plain words why a replay cannot play: a damaged file, a map
// that is not installed, a mod that is not or differently installed. A replay
// recorded under an installed mod asks the ordinary content reload to mount
// it and resumes on the shell it builds; if the mod did not mount, it says so.
func TestReplaysScreenWatchRefusalsAndModSwitch(t *testing.T) {
	useOnlineSessionSeams(t, 0)
	dir := t.TempDir()
	g := replaysTestShell(t, dir, true)
	installed := installTestMod(t)
	header := func(mapName string, mod session.MatchMod, at time.Time) replay.Header {
		h := fileTestHeader(replay.KindSkirmish, mapName, at)
		encoded, err := session.EncodeMatchConfig(onlineTestBase(t, mapName, mod, content.Mutators{}))
		if err != nil {
			t.Fatal(err)
		}
		h.Config = encoded
		return h
	}
	started := time.Date(2026, 10, 9, 14, 0, 0, 0, time.Local)
	other := matchModOf(&installed)
	other.Archive[0] ^= 1
	missing := session.MatchMod{ID: "elsewhere", Version: "2", Archive: [32]byte{7}}
	paths := map[string]string{
		"map":     writeTestReplay(t, dir, header("Test Map", session.MatchMod{}, started), 90),
		"mod":     writeTestReplay(t, dir, header("Ashap Plateau", matchModOf(&installed), started.Add(time.Minute)), 90),
		"copy":    writeTestReplay(t, dir, header("Ashap Plateau", other, started.Add(2*time.Minute)), 90),
		"missing": writeTestReplay(t, dir, header("Ashap Plateau", missing, started.Add(3*time.Minute)), 90),
	}
	broken := filepath.Join(dir, "broken.nlreplay")
	if err := os.WriteFile(broken, []byte("not a replay"), 0o644); err != nil {
		t.Fatal(err)
	}
	paths["damaged"] = broken
	g.activateGadget(replaysButton)
	s := g.replays
	watch := func(name string) string {
		t.Helper()
		g.refreshReplaysList(paths[name])
		if l, _ := s.selection(); l.Path != paths[name] {
			t.Fatalf("%s is not selected", name)
		}
		s.status = ""
		g.activateGadget("LOAD")
		if s.load != nil {
			t.Fatalf("%s started loading", name)
		}
		return s.panel.TextOf(replaysStatus)
	}
	for name, want := range map[string]string{
		"damaged": "Cannot play: the file is damaged.",
		"map":     "Cannot play: the map Test Map is not installed.",
		"copy":    "Recorded with a different copy of Test Mod 1 than the one installed.",
		"missing": "Recorded with the mod elsewhere 2, which is not installed. Install it from NANOLATHE, Mods.",
	} {
		if got := watch(name); got != want || pendingContentReload != nil {
			t.Errorf("%s: %q, reload %v; want %q", name, got, pendingContentReload, want)
		}
	}
	g.refreshReplaysList(paths["mod"])
	s.status = ""
	g.refreshReplaysPanel()
	if note := s.panel.TextOf(replaysStatus); note != "Recorded with Test Mod 1. Watch switches to it first." {
		t.Fatalf("the mod's note: %q", note)
	}
	if got := watch("mod"); got != "Loading Test Mod 1 for this replay..." {
		t.Fatalf("mod: %q", got)
	}
	r := pendingContentReload
	if r == nil || r.selector != "testmod@1" || r.mod.ID != "testmod" || r.online != nil || pendingReplayWatch == nil || pendingReplayWatch.path != paths["mod"] {
		t.Fatalf("reload %+v, resume %+v", r, pendingReplayWatch)
	}
	// The window loop performs the reload between steps; here it failed, so
	// the running shell still has the base game and says so.
	pendingContentReload = nil
	g.pollReplays()
	if pendingReplayWatch != nil || g.replays == nil || g.replays.load != nil || !strings.Contains(g.replays.panel.TextOf(replaysStatus), "could not be loaded") {
		t.Fatalf("a failed switch: %q", g.replays.panel.TextOf(replaysStatus))
	}
}
