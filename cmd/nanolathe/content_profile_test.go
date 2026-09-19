package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	contentprofiles "github.com/nanolathe-gg/nanolathe/internal/content/profiles"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// authorRenamedInstall publishes a loose install whose families sit under TA
// Zero's directory names, without a version-specific archive filename.
// Every byte is authored here; none is copied from a content set.
func authorRenamedInstall(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"ZGameDat/moveinfo.tdf": "[CLASS0]\n{\nName=TANK3;\nFootprintX=3;\nFootprintZ=3;\nMinWaterDepth=0;\nMaxWaterDepth=0;\nMaxSlope=15;\n}\n",
		"ZGameDat/sidedata.tdf": "[SIDE0]\n{\nname=ARM;\ncommander=ARMCOM;\nfont=scratch.fnt;\n" + authoredSideAnchors() + "}\n",
		"ZGameDat/allsound.tdf": "[PROBECUE]\n{\nsound=probe;\n}\n",
		"ZUnits/armcom.fbi":     "[UNITINFO]\n{\nUnitName=ARMCOM;\n}\n",
		"ZWeapon/scratch.tdf":   "[SCRATCHGUN]\n{\nid=1;\n}\n",
		"ZGui/probe.gui":        "[GADGET0]\n{\n[COMMON]\n{\nid=0;\n}\n}\n",
		"ZUnitPic/armcom.pcx":   "authored placeholder\n",
		"ZBuildMenu/probe.tdf":  "[MENU]\n{\n}\n",
		"ZI/default.txt":        "plan 0\n",
	}
	for name, body := range files {
		full := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// authoredSideAnchors writes the thirty mandatory anchors CompileSides needs,
// each one distinct so a compile failure is about the table and not a value.
func authoredSideAnchors() string {
	names := []string{
		"LOGO", "ENERGYBAR", "ENERGYNUM", "ENERGYMAX", "ENERGY0",
		"METALBAR", "METALNUM", "METALMAX", "METAL0", "TOTALUNITS",
		"TOTALTIME", "ENERGYPRODUCED", "ENERGYCONSUMED", "METALPRODUCED",
		"METALCONSUMED", "LOGO2", "UNITNAME", "DAMAGEBAR", "UNITMETALMAKE",
		"UNITMETALUSE", "UNITENERGYMAKE", "UNITENERGYUSE", "MISSIONTEXT",
		"UNITNAME2", "DAMAGEBAR2", "NAME", "DESCRIPTION", "RELOAD1",
		"RELOAD2", "RELOAD3",
	}
	var b strings.Builder
	for i, name := range names {
		b.WriteString("[" + name + "]\n{\nx1=1;\ny1=1;\nx2=2;\ny2=2;\nindex=")
		b.WriteByte(byte('0' + i%10))
		b.WriteString(";\n}\n")
	}
	return b.String()
}

// TestWindowedMountAppliesTheContentProfileTable is the contract this unit
// exists for: the graphical command's one mount boundary resolves the profile
// and hands every content reader the view that carries its directory table, so
// a loader asking for a retail directory reaches the tree the content set
// actually ships (docs/DESIGN_CONTENT_VFS.md §5 "Content profiles").
func TestWindowedMountAppliesTheContentProfileTable(t *testing.T) {
	t.Setenv(settings.EnvPath, filepath.Join(t.TempDir(), "settings.json"))
	cs, err := openContent(Options{Roots: []string{authorRenamedInstall(t)}})
	if err != nil {
		t.Fatalf("mount renamed install: %v", err)
	}
	defer cs.Close()

	if cs.profile != "zero" {
		t.Fatalf("resolved profile = %q, want zero", cs.profile)
	}
	if cs.fs == vfs.FSOps(cs.unmappedMount) {
		t.Fatal("a profile with a directory table left the mount unwrapped")
	}

	// The catalog family: the compiler asks for the retail name and the view
	// answers from the renamed tree. Provenance stays retail-named, because
	// the catalog hash and every diagnostic are built from these paths.
	sides, err := content.CompileSides(cs.fs)
	if err != nil {
		t.Fatalf("compile sides through the profile view: %v", err)
	}
	if len(sides) != 1 || sides[0].Name != "ARM" {
		t.Fatalf("sides = %+v", sides)
	}
	info, err := cs.fs.Stat("gamedata/sidedata.tdf")
	if err != nil {
		t.Fatalf("stat through the profile view: %v", err)
	}
	if info.Path != "gamedata/sidedata.tdf" {
		t.Fatalf("provenance path = %q, want the retail name", info.Path)
	}

	// The sound family reads the same renamed gamedata tree.
	aliases, _, err := content.CompileSoundAliasesOrdered(cs.fs)
	if err != nil {
		t.Fatalf("compile sound aliases through the profile view: %v", err)
	}
	if _, ok := aliases["probecue"]; !ok {
		t.Fatalf("sound aliases = %v, want the authored cue", aliases)
	}

	// The GUI family goes through the content set's own loader, which is the
	// only path the front end and the HUD use. A parse verdict is not the
	// point; reaching the authored file instead of a missing one is.
	if _, err := cs.loadGUI("guis/probe.gui"); errors.Is(err, vfs.ErrNotFound) {
		t.Fatalf("GUI loader missed the renamed tree: %v", err)
	}

	// The unit, weapon, build-menu, picture and AI families resolve the same
	// way, which is what makes the compiler's enumeration profile-agnostic.
	for _, logical := range []string{
		"units/armcom.fbi", "weapons/scratch.tdf",
		"download/probe.tdf", "unitpics/armcom.pcx", "ai/default.txt",
	} {
		if _, err := cs.fs.Stat(logical); err != nil {
			t.Fatalf("stat %s through the profile view: %v", logical, err)
		}
		if _, err := cs.unmappedMount.Stat(logical); err == nil {
			t.Fatalf("%s resolved on the raw mount, so the fixture does not prove the table", logical)
		}
	}
}

// TestRetailMountKeepsTheConcreteOverlay locks the no-change half: an install
// with no markers resolves `retail`, whose table is empty, and an empty table
// returns the mounted overlay itself — so a retail run reads exactly what it
// read before content profiles existed.
func TestRetailMountKeepsTheConcreteOverlay(t *testing.T) {
	t.Setenv(settings.EnvPath, filepath.Join(t.TempDir(), "settings.json"))
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "gamedata"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"moveinfo.tdf", "sidedata.tdf"} {
		if err := os.WriteFile(filepath.Join(root, "gamedata", name), []byte("[TEST] { value=base; }"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cs, err := openContent(Options{Roots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	if cs.profile != contentprofiles.RetailName {
		t.Fatalf("resolved profile = %q, want %s", cs.profile, contentprofiles.RetailName)
	}
	if cs.fs != vfs.FSOps(cs.unmappedMount) {
		t.Fatal("an empty directory table wrapped the mounted overlay")
	}
}

// TestConcreteMountFamiliesAreNeverRedirected guards the readers this mount
// boundary still hands the concrete overlay: the presentation model cache
// (objects3d, textures, anims) and the OTA map census (maps). The menu's TNT
// preview uses the layout view.
// Both are typed on the overlay rather than on a read view, so they read those
// families unmapped — correct only while no profile renames them. A profile
// that did would need those two surfaces widened to a read view first, so this
// test turns that into a failure rather than into missing art.
func TestConcreteMountFamiliesAreNeverRedirected(t *testing.T) {
	concrete := []string{"objects3d", "textures", "anims", "maps"}
	for _, name := range contentprofiles.Names() {
		profile, err := contentprofiles.Lookup(name)
		if err != nil {
			t.Fatalf("lookup %s: %v", name, err)
		}
		for _, row := range profile.Layout().Names() {
			for _, family := range concrete {
				if strings.EqualFold(row[0], family) {
					t.Fatalf("profile %s redirects %s, which the windowed command still reads through the concrete mount", name, family)
				}
			}
		}
	}
}

func TestContentSelectorPrecedenceAndDetectionDoesNotPersist(t *testing.T) {
	root := authorRenamedInstall(t)
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	t.Setenv(settings.EnvPath, settingsPath)
	cs, err := openContent(Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	cs.Close()
	if _, err := os.Stat(settingsPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("automatic detection wrote settings: %v", err)
	}
	// A saved selector is intentional, even if these new roots would detect
	// another layout. The explicit flag remains the strongest override.
	stored := settings.Defaults()
	stored.ContentProfile = "retail"
	if err := stored.Save(); err != nil {
		t.Fatal(err)
	}
	if cs, err := openContent(Options{Root: root}); err == nil {
		cs.Close()
		t.Fatal("saved retail selection was silently replaced by detection")
	}
	cs, err = openContent(Options{Root: root, ContentProfile: "zero"})
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	if cs.profile != "zero" {
		t.Fatalf("explicit profile = %q", cs.profile)
	}
}
