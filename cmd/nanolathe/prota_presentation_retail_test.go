//go:build retail

package main

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// TestRetailProTAPresentationAssets is a bounded installed-content check. It
// proves the ProTA profile resolves the package's authored HUD, portrait and
// palette products and that the same selected package root loads its custom
// strategic icons. Gameplay and the package DLL remain outside this check.
func TestRetailProTAPresentationAssets(t *testing.T) {
	value := os.Getenv("NANOLATHE_MOD_ROOTS_PROTA")
	if strings.TrimSpace(value) == "" {
		t.Skip("NANOLATHE_MOD_ROOTS_PROTA is unset")
	}
	var modRoots []string
	for _, root := range filepath.SplitList(value) {
		if strings.TrimSpace(root) != "" {
			modRoots = append(modRoots, root)
		}
	}
	if len(modRoots) == 0 {
		t.Fatal("NANOLATHE_MOD_ROOTS_PROTA names no roots")
	}
	retail := testsupport.RetailRoot(t)
	cs, err := openContent(Options{Root: retail, Roots: append([]string{retail}, modRoots...)})
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	if cs.profile != "prota" {
		t.Fatalf("detected profile %q, want prota", cs.profile)
	}

	sources, ok := cs.fs.(interface{ Sources(string) []vfs.EntryInfo })
	if !ok {
		t.Fatal("profile view does not report asset provenance")
	}
	assets := []struct{ logical, original string }{
		{"gamedata/sidedata.tdf", "gamedatP/SIDEDATA.TDF"},
		{"guis/armcom1.gui", "guiP/ARMCOM1.GUI"},
		{"guis/corcom1.gui", "guiP/CORCOM1.GUI"},
		{"unitpics/armcom.pcx", "unitpicsP/ARMCOM.PCX"},
		{"unitpics/corcom.pcx", "unitpicsP/CORCOM.PCX"},
		{"palettes/palette.pal", "palettes/PALETTE.PAL"},
		{"palettes/guipal.pal", "palettes/GUIPAL.PAL"},
		{"palettes/guipal.pcx", "palettes/GUIPAL.PCX"},
		{"palettes/palette.shd", "palettes/PALETTE.SHD"},
		{"palettes/palette.lht", "palettes/PALETTE.LHT"},
		{"palettes/palette.alp", "palettes/PALETTE.ALP"},
	}
	for _, asset := range assets {
		rows := sources.Sources(asset.logical)
		if len(rows) == 0 {
			t.Errorf("%s has no mounted source", asset.logical)
			continue
		}
		winner := rows[0]
		if !strings.EqualFold(winner.Source.ProviderID(), "ProTA.gp3") || !strings.EqualFold(winner.OriginalPath, asset.original) {
			t.Errorf("%s source = %s:%s, want ProTA.gp3:%s", asset.logical, winner.Source.ProviderID(), winner.OriginalPath, asset.original)
		}
	}
	logos, err := formats.LoadGAFFile(cs.fs, "textures/logos.gaf")
	if err != nil {
		t.Fatal(err)
	}
	logoEntry, ok := logos.Find(sideLogoEntry)
	if !ok {
		t.Fatal("ProTA logos.gaf has no 32xlogos entry")
	}
	pal := retailPaletteForTest(t, cs)
	dominant := func(frame *formats.GAFFrame) [4]byte {
		counts := [256]int{}
		for p, index := range frame.Pixels {
			if p < len(frame.Transparent) && !frame.Transparent[p] {
				counts[index]++
			}
		}
		best := 0
		for index := 1; index < len(counts); index++ {
			if counts[index] > counts[best] {
				best = index
			}
		}
		return pal.Base[best]
	}
	var pinkFrame, slateFrame *formats.GAFFrame
	for _, ref := range logoEntry.Frames {
		if ref.Frame == nil {
			continue
		}
		ink := dominant(ref.Frame)
		if ink[0] > ink[1] && ink[2] > ink[1] && ink[0] >= 150 {
			pinkFrame = ref.Frame
		}
		if ink[2] > ink[1] && ink[1] > ink[0] && ink[2] < 128 {
			slateFrame = ref.Frame
		}
	}
	if pinkFrame == nil || slateFrame == nil {
		t.Fatalf("ProTA team logos do not contain the documented pink/slate replacement hues")
	}
	if dir := os.Getenv("NANOLATHE_PROTA_PRESENTATION_SHOTS"); dir != "" {
		writeProTAGAFCapture(t, filepath.Join(dir, "prota-team-pink.png"), pinkFrame, pal.Base)
		writeProTAGAFCapture(t, filepath.Join(dir, "prota-team-slate.png"), slateFrame, pal.Base)
	}

	for _, side := range []string{"arm", "cor"} {
		portrait, err := formats.LoadPCXFile(cs.fs, "unitpics/"+side+"com.pcx")
		if err != nil || portrait == nil {
			t.Errorf("%s commander portrait: %v", side, err)
			continue
		}
		if portrait.Width == 0 || portrait.Height == 0 {
			t.Errorf("%s commander portrait is empty: %dx%d", side, portrait.Width, portrait.Height)
		}
		if dir := os.Getenv("NANOLATHE_PROTA_PRESENTATION_SHOTS"); dir != "" {
			writeProTAPCXCapture(t, filepath.Join(dir, "prota-"+side+"com-portrait.png"), portrait)
		}
		window, err := cs.loadGUI("guis/" + side + "com1.gui")
		if err != nil {
			t.Errorf("%s commander page: %v", side, err)
			continue
		}
		slots, products, keyed := 0, 0, 0
		for _, gadget := range window.Gadgets {
			if gadget.Kind == gui.KindButton && (gadget.CommonAttribs&4 != 0 || sidebarEmptySlot(gadget.Name)) {
				slots++
			}
			if gadget.Kind != gui.KindButton || gadget.CommonAttribs&4 == 0 {
				continue
			}
			products++
			if gadget.QuickKey != 0 {
				keyed++
			}
		}
		if slots != 12 || products == 0 || keyed == 0 {
			t.Errorf("%s commander page slots/products/hotkeys = %d/%d/%d, want twelve slots with products and authored hotkeys", side, slots, products, keyed)
		}
	}

	cat, err := cs.compileCatalog(nil)
	if err != nil {
		t.Fatal(err)
	}
	iconRoot := ""
	for _, root := range modRoots {
		if _, err := discoverStrategicIconConfig(root); err == nil {
			iconRoot = root
			break
		}
	}
	if iconRoot == "" {
		t.Fatal("ProTA roots do not contain Icon/iconcfg.ini")
	}
	icons, err := configuredStrategicIcons(cat, iconRoot)
	if icons == nil || err != nil {
		t.Fatalf("ProTA package-root strategic icons = %v/%v", icons, err)
	}
	configuredDefinition := ""
	configuredID := uint32(0)
	var configuredAtlas any
	for _, entry := range icons.Audit() {
		for _, evidence := range entry.Descriptor.Evidence {
			if strings.HasPrefix(evidence, "host icon config:") {
				configuredDefinition = entry.Definition
				configuredID = entry.DefinitionID
				configuredAtlas = entry.Descriptor.Atlas
				break
			}
		}
		if configuredDefinition != "" {
			break
		}
	}
	if configuredDefinition == "" {
		t.Fatal("ProTA icon config decoded without publishing a custom mapping")
	}
	if configuredID > 0xffff {
		t.Fatalf("configured definition ID %d does not fit strategic-icon lookup", configuredID)
	}
	descriptor, ok := icons.Lookup(configuredDefinition, uint16(configuredID))
	if !ok || descriptor.Atlas == nil || descriptor.Atlas != configuredAtlas || descriptor.Rect.W <= 0 || descriptor.Rect.H <= 0 {
		t.Fatalf("published ProTA mapping did not resolve to its custom atlas: ok=%v descriptor=%+v", ok, descriptor)
	}
	// The host-config evidence and matching Lookup atlas prove the authored
	// UseDefaultIcon=false branch decoded and packed its configured PCX art.
}

// TestRetailProTADirectionalCoreShipyardClicks locks the authored-source
// boundary exposed by ProTA 4.8. CORSYE and CORSYW have physical product pages
// whose installed gadget names are authoritative for a human factory queue,
// even though neither unit has a matching CANBUILD section [07 §9].
func TestRetailProTADirectionalCoreShipyardClicks(t *testing.T) {
	value := strings.TrimSpace(os.Getenv("NANOLATHE_MOD_ROOTS_PROTA"))
	if value == "" {
		t.Skip("NANOLATHE_MOD_ROOTS_PROTA is unset")
	}
	retail := testsupport.RetailRoot(t)
	var roots []string
	for _, root := range filepath.SplitList(value) {
		if root = strings.TrimSpace(root); root != "" {
			roots = append(roots, root)
		}
	}
	opts := Options{Root: retail, Roots: append([]string{retail}, roots...), Map: "ashap plateau", Seed: 7}
	cs, err := openContent(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	if cs.profile != "prota" {
		t.Fatalf("detected profile %q, want prota", cs.profile)
	}

	cfg := session.SkirmishConfig{MapName: opts.Map, NumPlayers: 2}
	cfg.Players[0].Side = 1
	cfg.Players[1].Controller = session.SkirmishControllerComputer
	cfg.ApplyDefaults()
	sess, cat, err := newBattleSessionWithConfig(opts, cs, cfg)
	if err != nil {
		t.Fatal(err)
	}
	for step := int32(1); step <= 30; step++ {
		sess.Step(step)
	}
	b := &battleSession{sess: sess, cat: cat}
	b.hud, err = loadRetailBattleHUD(cs.fs, sess, cat, retailPaletteForTest(t, cs), nil, newBattleWindowContext(cs, nil))
	if err != nil {
		t.Fatal(err)
	}

	for n, key := range []string{"CORSYE", "CORSYW"} {
		t.Run(key, func(t *testing.T) {
			def, ok := cat.Unit(key)
			if !ok || def == nil || !strings.EqualFold(def.UnitName, key) {
				t.Fatalf("directional shipyard definition %s is absent or aliased", key)
			}
			if menu := cat.BuildMenus[strings.ToLower(key)]; menu != nil && len(menu.AuthoredButtons) != 0 {
				t.Fatalf("%s unexpectedly acquired CANBUILD membership: %#v", key, menu.AuthoredButtons)
			}
			for _, u := range sess.Units.Iter() {
				if u != nil {
					u.Flags &^= hud.SelectionFlag
				}
			}
			x := numeric.FixedFromInt(int64(800 + n*240))
			z := numeric.FixedFromInt(600)
			handle, err := sess.Units.Create(def, sess.LocalOwner, x, sess.World.HeightAt(x, z), z)
			if err != nil {
				t.Fatal(err)
			}
			yard := sess.Units.Unit(handle)
			if yard == nil {
				t.Fatalf("created %s not in unit pool", key)
			}
			sess.CompleteUnit(handle)
			yard.Flags |= hud.SelectionFlag
			yard.Flags = hud.EncodePageBits(yard.Flags, 1)
			sess.Step(sess.Clock.ScaledAnchor + 1)

			f := sess.Snapshot.Current()
			if f == nil || f.CommandPage.Builder != handle {
				t.Fatalf("%s command page = %#v, want builder %d", key, f, handle)
			}
			if len(f.CommandPage.AllowedProducts) != 0 {
				t.Fatalf("%s published CANBUILD membership %v, want empty", key, f.CommandPage.AllowedProducts)
			}
			w, _, err := b.hud.windowForRequired(b, f)
			if err != nil {
				t.Fatal(err)
			}
			wantWindow := strings.ToLower(key) + "1.gui"
			if w == nil || !strings.HasSuffix(strings.ToLower(w.Name), wantWindow) {
				if w == nil {
					t.Fatalf("%s command window is nil; want %s", key, wantWindow)
				}
				t.Fatalf("%s command window = %q, want suffix %s", key, w.Name, wantWindow)
			}
			productIndex := -1
			for i, gadget := range w.Gadgets {
				if gadget.CommonAttribs&4 != 0 && strings.EqualFold(gadget.Name, "CORCS") {
					productIndex = i
					break
				}
			}
			if productIndex < 0 {
				t.Fatalf("%s has no installed CORCS product gadget", w.Name)
			}
			r := w.PlacedRect(productIndex)
			cx, cy := r.X+r.W/2, r.Y+r.H/2
			if !b.hud.sameButton(b, cx, cy, cx, cy) || !hudConsumeClick(b.hud, b, cx, cy) {
				t.Fatalf("%s CORCS gadget did not activate at its center", key)
			}
			sess.Step(sess.Clock.ScaledAnchor + 1)
			queue := orders.QueueForUnit(yard).Primary()
			if len(queue) == 0 || queue[0] == nil || !strings.EqualFold(queue[0].BuildDefKey, "CORCS") || queue[0].Param2 == 0 {
				t.Fatalf("%s queue = %#v, want one counted CORCS order", key, queue)
			}
		})
	}
}

func writeProTAPCXCapture(t *testing.T, path string, pcx *formats.PCX) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, int(pcx.Width), int(pcx.Height)))
	for y := 0; y < int(pcx.Height); y++ {
		for x := 0; x < int(pcx.Width); x++ {
			index := pcx.Pixels[y*int(pcx.Width)+x]
			img.SetRGBA(x, y, pcx.Palette[index])
		}
	}
	writeProTAPNG(t, path, img)
}

func writeProTAGAFCapture(t *testing.T, path string, frame *formats.GAFFrame, pal [256][4]byte) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, int(frame.Width), int(frame.Height)))
	for y := 0; y < int(frame.Height); y++ {
		for x := 0; x < int(frame.Width); x++ {
			index, opaque := frame.At(x, y)
			if !opaque {
				continue
			}
			p := pal[index]
			img.SetRGBA(x, y, color.RGBA{R: p[0], G: p[1], B: p[2], A: 255})
		}
	}
	writeProTAPNG(t, path, img)
}

func writeProTAPNG(t *testing.T, path string, img image.Image) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	encodeErr := png.Encode(file, img)
	closeErr := file.Close()
	if encodeErr != nil {
		t.Fatal(encodeErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
}
