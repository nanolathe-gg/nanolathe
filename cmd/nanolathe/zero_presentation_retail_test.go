//go:build retail

package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
)

// Installed-content acceptance for the renamed resources and GoK-first side
// order authored by Zero Alpha 5. This does not exercise the shipped patch DLL.
func TestRetailZeroFrontendResourcesAndFactions(t *testing.T) {
	roots := filepath.SplitList(os.Getenv("NANOLATHE_MOD_ROOTS_ZERO"))
	if len(roots) == 0 {
		t.Skip("NANOLATHE_MOD_ROOTS_ZERO is unset")
	}
	retail := testsupport.RetailRoot(t)
	opts := Options{Root: retail, Roots: append([]string{retail}, roots...), Map: "ashap plateau", Seed: 7}
	cs, err := openContent(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	shell, err := newGameShell(opts, cs)
	if err != nil {
		t.Fatal(err)
	}
	if cs.profile != "zero" || shell.skirmishSideCount() != 3 {
		t.Fatalf("profile/sides = %s/%d", cs.profile, shell.skirmishSideCount())
	}
	for _, tc := range []struct {
		path string
		got  *formats.PCX
	}{
		{"bitmaps/frontendz.pcx", shell.assets.panel[modeMenuMain].background},
		{"bitmaps/singlezbg.pcx", shell.assets.panel[modeMenuSingle].background},
		{"bitmaps/loadgamezbg.pcx", shell.assets.loading},
	} {
		source, err := cs.fs.Stat(tc.path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.EqualFold(source.Source.ProviderID(), "TAZ31.gp3") {
			t.Fatalf("%s provider = %s", tc.path, source.Source.ProviderID())
		}
		want, err := formats.LoadPCXFile(cs.fs, tc.path)
		if err != nil {
			t.Fatal(err)
		}
		if tc.got == nil || !bytes.Equal(tc.got.Pixels, want.Pixels) {
			t.Fatalf("%s not selected", tc.path)
		}
	}
	logos, err := formats.LoadGAFFile(cs.fs, "textures/logoz.gaf")
	if err != nil {
		t.Fatal(err)
	}
	assertLogos := func(got *formats.GAF) {
		t.Helper()
		if got == nil {
			t.Fatal("missing team-logo bank")
		}
		have, ok := got.Find("GoKColorTrim1_1")
		want, exists := logos.Find("GoKColorTrim1_1")
		if !ok || !exists || len(have.Frames) != 10 || !bytes.Equal(have.Frames[5].Frame.Pixels, want.Frames[5].Frame.Pixels) {
			t.Fatal("authored Zero team bank not selected")
		}
	}
	assertLogos(shell.assets.logos)
	cl, err := client.New(client.Options{Buffer: &frame.Buffer{}, Width: 640, Height: 480})
	if err != nil {
		t.Fatal(err)
	}
	cl.SetPalette(shell.assets.pal)
	cl.SetFNT(shell.font)
	cl.SetCamera(&camera.Camera{ViewW: 640, ViewH: 480, MapW: 640, MapH: 480})
	cl.SetUIStage(gameShellUIStage{shell: shell})
	capture := func(name string) {
		if dir := os.Getenv("NANOLATHE_ZERO_PRESENTATION_SHOTS"); dir != "" {
			writeShellShot(t, cl, filepath.Join(dir, name+".png"))
		}
	}
	capture("zero-main")
	shell.openMenu(modeMenuSingle)
	capture("zero-single")
	shell.loading = &loadingState{mapName: opts.Map}
	shell.frontend.SetMode(modeLoading)
	capture("zero-loading")
	shell.loading = nil
	shell.setup.Players[0].Side = 0
	shell.openMenu(modeMenuSkirmish)
	p := shell.activePanel()
	for i, name := range []string{"gok", "arm", "core", "gok-return"} {
		if i != 0 {
			clickRowGadget(t, shell, p, cl, "Side0", input.MouseButtonLeft)
		}
		if got := shell.setup.Players[0].Side; got != i%3 {
			t.Fatalf("%s side = %d", name, got)
		}
		index := p.Index("Side0")
		if p.StageAt(index) != i%3 || p.Window.Gadgets[index].Stages != 3 {
			t.Fatalf("%s stage/count = %d/%d", name, p.StageAt(index), p.Window.Gadgets[index].Stages)
		}
		capture("zero-skirmish-" + name)
	}
	cfg := session.SkirmishConfig{MapName: opts.Map, NumPlayers: 2}
	cfg.Players[1].Controller = session.SkirmishControllerComputer
	cfg.ApplyDefaults()
	sess, cat, err := newBattleSessionWithConfig(opts, cs, cfg)
	if err != nil {
		t.Fatal(err)
	}
	hud, err := loadRetailBattleHUD(cs.fs, sess, cat, shell.assets.pal, nil, newBattleWindowContext(cs, nil))
	if err != nil {
		t.Fatal(err)
	}
	assertLogos(hud.logos)

	// These static previews use the same bank selection as --shot-model. Two
	// player colours must select distinct texture frames without advancing time.
	preview, err := client.NewModelPreviewRenderer(cs.unmappedMount, cs.presentation.TeamLogos)
	if err != nil {
		t.Fatal(err)
	}
	definition, err := resolveModelPreviewDefinition(cat, "gokcommander")
	if err != nil {
		t.Fatal(err)
	}
	var first []byte
	for _, owner := range []uint8{0, 5} {
		record, err := preview.RecordModel(client.ModelPreviewOptions{Model: definition.ObjectName, Width: 256, Height: 256, Scale: 2, Owner: owner, Structure: definition.Structure, KeyPlane: definition.ZBuffer})
		if err != nil {
			t.Fatal(err)
		}
		if first == nil {
			first = append([]byte(nil), record.Image.Pix...)
		} else if bytes.Equal(first, record.Image.Pix) {
			t.Fatal("GoK model ignored player colour")
		}
		if dir := os.Getenv("NANOLATHE_ZERO_PRESENTATION_SHOTS"); dir != "" {
			if err := encodeShotPNG(filepath.Join(dir, fmt.Sprintf("zero-gok-colour%d.png", owner)), record.Image); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// Omitting presentation paths retains all four retail resource selections.
func TestRetailFrontendResourceDefaults(t *testing.T) {
	shell, cs, _ := retailAssetShell(t)
	for _, tc := range []struct {
		path string
		got  *formats.PCX
	}{
		{"bitmaps/frontendx.pcx", shell.assets.panel[modeMenuMain].background},
		{"bitmaps/singlebg.pcx", shell.assets.panel[modeMenuSingle].background},
		{"bitmaps/loadgame2bg.pcx", shell.assets.loading},
	} {
		want, err := formats.LoadPCXFile(cs.fs, tc.path)
		if err != nil {
			t.Fatal(err)
		}
		if tc.got == nil || !bytes.Equal(tc.got.Pixels, want.Pixels) {
			t.Fatalf("retail default %s changed", tc.path)
		}
	}
	if shell.assets.logos == nil {
		t.Fatal("retail team-logo bank missing")
	}
	if _, ok := shell.assets.logos.Find("colorslt"); !ok {
		t.Fatal("retail team-logo bank changed")
	}
	if shell.skirmishSideCount() != 2 {
		t.Fatalf("retail side count = %d", shell.skirmishSideCount())
	}
}
