package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

func TestStagedBuilderTranslatesFragmentsAfterWholeCaptionAndArt(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "gamedata"), 0o755); err != nil {
		t.Fatal(err)
	}
	tableText := "[Pair] { english=Off|On; }\n[Off] { english=Disabled; }\n[On] { english=Enabled; }\n[Single] { english=Middle; }\n[Middle] { english=Final; }\n"
	if err := os.WriteFile(filepath.Join(root, "gamedata", "translate.tdf"), []byte(tableText), 0o644); err != nil {
		t.Fatal(err)
	}
	guiText := "[GADGET0] { [COMMON] { id=0; name=PANEL; width=32; height=24; } }\n[GADGET1] { [COMMON] { id=1; name=BUTTON; } text=Pair; stages=2; quickkey=K; }\n[GADGET2] { [COMMON] { id=1; name=SINGLE; } text=Single; stages=2; quickkey=S; }\n"
	if err := os.WriteFile(filepath.Join(root, "stage.gui"), []byte(guiText), 0o644); err != nil {
		t.Fatal(err)
	}
	fs := vfs.New()
	if err := fs.MountDirectory(root, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	table, err := content.LoadTranslationTable(fs, "english")
	if err != nil {
		t.Fatal(err)
	}
	cs := testContentSet(fs)
	cs.translations = table
	for _, name := range []string{"named", "checkbox", "missing art", "external bypass"} {
		t.Run(name, func(t *testing.T) {
			w, err := cs.loadGUI("stage.gui")
			if err != nil {
				t.Fatal(err)
			}
			if w.Gadgets[1].Text != "Off|On" || w.Gadgets[2].Text != "Middle" {
				t.Fatal("fixture did not pass through whole-caption parser localization")
			}
			g := &gameShell{cs: cs, quickKeyPreclearDisabled: true, assets: &menuAssets{}}
			var own *formats.GAF
			switch name {
			case "named":
				own = &formats.GAF{Entries: []formats.GAFEntry{widgetArtEntry("BUTTON", 3, 5, 7, 9)}}
			case "checkbox":
				w.Gadgets[1].Attribs = guiAttribCheckbox
				g.assets.common = &formats.GAF{Entries: []formats.GAFEntry{widgetArtEntry("CHECKBOX", 3, 5, 7, 9)}}
			case "external bypass":
				w.Gadgets[1].GAFFile = 1
			}
			g.installRetailWindowButtonArt(w, own)
			want := []string{"Disabled", "Enabled"}
			if name == "external bypass" {
				want = []string{"Off", "On"}
			} else if w.Gadgets[1].QuickKey != 0 {
				t.Fatal("staged assignment retained a key")
			}
			if !slices.Equal(w.Gadgets[1].Labels, want) {
				t.Fatalf("labels=%q, want %q", w.Gadgets[1].Labels, want)
			}
			if !slices.Equal(w.Gadgets[2].Labels, []string{"Final"}) {
				t.Fatalf("single fragment did not receive the builder translation: %q", w.Gadgets[2].Labels)
			}
		})
	}
}
