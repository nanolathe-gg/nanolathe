package main

import (
	"bytes"
	"hash/fnv"
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
)

func radarPixelHash(pixels []byte) uint64 {
	h := fnv.New64a()
	_, _ = h.Write(pixels)
	return h.Sum64()
}

// A live-battle save carries the Summary's `Radar Image` box, and the load
// screen's summary panel reads it back for the selected file: an 8-byte
// width/height header and then the rows of palette bytes [08 "Summary"]
// [08 R-SAVE-02 §3]. A save written without the box — every save this engine
// wrote before the box had a producer, and every continuation — selects and
// draws with no preview and no failure.
func TestBattleSaveRadarPreviewRoundTrip(t *testing.T) {
	resetSaveLoadScreenState(t)
	opts := Options{Root: testsupport.RetailRoot(t), Map: "ashap plateau", Seed: 7}
	cs, err := openContent(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	previous := clPtr
	defer func() { clPtr = previous }()
	shell, cl, err := newDirectBattleView(opts, cs)
	if err != nil {
		t.Fatal(err)
	}
	defer shell.teardownBattle(cl)
	shell.opts.Root = t.TempDir()
	saveDir := shell.saveLoadDir()
	// The save screen creates SAVEGAME\ when it opens; this test writes into it
	// directly [08 R-SAVE-02 §1].
	if err := os.MkdirAll(saveDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Run the battle, composing each frame so the rail keeps FINAL — the raster
	// the box is made of — up to date.
	millis := &shotMillisSource{}
	for i := 0; i < 60; i++ {
		if shell.battle != nil {
			shell.battle.millisSource = millis
		}
		millis.step++
		cl.Step(1.0 / 30)
		cl.ComposeFrame()
	}
	b := shell.battle
	captureSaveLoadUI(t, cl, "battle-before-save")
	playW, playH, ok := b.sess.PlayArea()
	if !ok {
		t.Fatal("battle has no play area")
	}
	layout := camera.LayoutMinimap(playW, playH)
	surface := &b.hud.radarFinal
	if len(surface.Bits) == 0 {
		t.Fatal("the rail composed no radar surface to preview")
	}
	wantHash := radarPixelHash(surface.Bits)

	box := shell.battleRadarPreviewBox()
	w, h, pixels, ok := save.DecodeRadarImage(box)
	if !ok {
		t.Fatalf("the producer wrote no readable box (%d bytes)", len(box))
	}
	// The written extent is the aspect-fitted radar picture, which is the
	// surface the rail draws; the fit itself is camera.LayoutMinimap's.
	if w != int(layout.W) || h != int(layout.H) || w != surface.W || h != surface.H {
		t.Fatalf("box extent %dx%d, want the radar surface %dx%d (layout %dx%d)", w, h, surface.W, surface.H, layout.W, layout.H)
	}
	if got := radarPixelHash(pixels); got != wantHash {
		t.Fatalf("box pixels hash %#x, want the composed surface's %#x", got, wantHash)
	}

	if err := shell.writeBattleSave(session.RetailSavePath(saveDir, "preview"), "preview"); err != nil {
		t.Fatalf("battle save: %v", err)
	}
	summary, ok, err := save.ReadSummaryFileWithBoxes(session.RetailSavePath(saveDir, "preview"))
	if err != nil || !ok {
		t.Fatalf("reading the saved Summary = (%v,%v)", ok, err)
	}
	fileW, fileH, filePixels, ok := save.DecodeRadarImage(summary.RadarImage)
	if !ok || fileW != w || fileH != h || radarPixelHash(filePixels) != wantHash {
		t.Fatalf("the file's box = (%d,%d,%v), want %dx%d with hash %#x", fileW, fileH, ok, w, h, wantHash)
	}

	// A save with no box at all: the panel must select it without a preview.
	plain := save.NewBuilder()
	save.WriteSummary(plain, save.Summary{Description: "older save", Gametype: 2, Players: 2, IsBattle: true})
	if err := os.WriteFile(filepath.Join(saveDir, "OLDER.SAV"), plain.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	// A third slot whose box is an authored non-square pattern, so the capture
	// shows both that the gadget draws the box and that a picture wider than it
	// is tall keeps its aspect inside the authored rectangle.
	const patternW, patternH = 96, 48
	pattern := make([]byte, patternW*patternH)
	for y := 0; y < patternH; y++ {
		for x := 0; x < patternW; x++ {
			pattern[y*patternW+x] = byte(160 + (x/8+y/8)%2*40)
		}
	}
	patterned := save.NewBuilder()
	save.WriteSummary(patterned, save.Summary{
		Description: "pattern", Gametype: 2, Players: 2, IsBattle: true,
		RadarImage: save.EncodeRadarImage(patternW, patternH, pattern, patternW),
	})
	if err := os.WriteFile(filepath.Join(saveDir, "PATTERN.SAV"), patterned.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := shell.openSaveLoadScreen(loadScreenMode, saveLoadFromBattle); err != nil {
		t.Fatalf("open load screen: %v", err)
	}
	if saveLoadPanel == nil || saveLoadPanel.Window.GadgetIndex("RADAR") < 0 {
		t.Fatal("the authored load window has no RADAR surface")
	}
	rows := saveLoadUI.Descriptions()
	if len(rows) != 3 {
		t.Fatalf("load list = %v, want all three saves", rows)
	}
	for index, description := range rows {
		shell.selectSaveLoadRow(index)
		gotPixels, gotW, gotH, has := saveLoadUI.radarPreview()
		switch description {
		case "preview":
			if !has || gotW != w || gotH != h || radarPixelHash(gotPixels) != wantHash {
				t.Fatalf("selected preview = (%d,%d,%v), want %dx%d with hash %#x", gotW, gotH, has, w, h, wantHash)
			}
		case "older save":
			if has {
				t.Fatalf("a save with no box produced a %dx%d preview", gotW, gotH)
			}
		case "pattern":
			if !has || gotW != patternW || gotH != patternH || !bytes.Equal(gotPixels, pattern) {
				t.Fatalf("authored pattern preview = (%d,%d,%v), want %dx%d", gotW, gotH, has, patternW, patternH)
			}
		default:
			t.Fatalf("unexpected row %q", description)
		}
		// Drawing the panel must survive all three cases.
		cl.Step(1.0 / 30)
		cl.ComposeFrame()
		captureSaveLoadUI(t, cl, "load-radar-"+description)
	}
	t.Logf("radar preview %dx%d, pixel hash %#x", w, h, wantHash)
}
