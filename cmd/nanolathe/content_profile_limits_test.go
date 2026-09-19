package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// authoredDefinitionCount is deliberately above the retail 511 usable IDs and
// small enough to keep the compile in the fast tier.
const authoredDefinitionCount = 600

// authorWideInstall publishes an install whose unit tree holds more
// definitions than the retail domain can number, under TA Zero's directory
// names together with that profile's two markers. Unit definitions must come
// from a mounted archive to pass the loose-file admission gate [02 R-CAT-01
// §4], so the families the compiler reads are authored into one archive at the
// install root; the markers stay loose beside it. Every byte is authored here;
// none is copied from a content set.
func authorWideInstall(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	files := []vfs.ArchiveFile{
		{Path: "ZI/default.txt", Data: []byte("plan 0\nweight UNIT0000 100\n")},
		{Path: "anims/vismasks.gaf", Data: authoredVisMaskGAF(t)},
		{Path: "ZBuildMenu/notes.txt", Data: []byte("authored fixture: no placements\n")},
		{Path: "features/scratch.tdf", Data: []byte("[SCRATCHROCK]\n{\nworld=Scratch World;\ndescription=Rock;\ncategory=rocks;\nfootprintx=1;\nfootprintz=1;\nheight=10;\nblocking=1;\n}\n")},
		{Path: "ZGameDat/los.tdf", Data: []byte("[TABLEINFO]\n{\nnumtables=1;\n}\n[TABLE1]\n{\nnumlines=1;\nline1=1, 0, 1;\n}\n")},
		{Path: "ZGameDat/moveinfo.tdf", Data: []byte("[CLASS0]\n{\nName=TANK3;\nFootprintX=3;\nFootprintZ=3;\nMinWaterDepth=0;\nMaxWaterDepth=0;\nMaxSlope=15;\n}\n")},
		{Path: "ZGameDat/sidedata.tdf", Data: []byte("[SIDE0]\n{\nname=ARM;\ncommander=UNIT0000;\nfont=scratch.fnt;\n" + authoredSideAnchors() + "}\n")},
		{Path: "ZGui/notes.txt", Data: []byte("authored fixture: no build menus\n")},
		{Path: "maps/notes.txt", Data: []byte("authored fixture: no maps\n")},
		{Path: "objects3d/scratch.3do", Data: authoredScratchModel(t)},
		{Path: "ZUnitPic/notes.txt", Data: []byte("authored fixture: no pictures\n")},
		{Path: "ZWeapon/scratch.tdf", Data: []byte("[SCRATCHGUN]\n{\nid=1;\nname=Scratch Gun;\nrange=100;\nreloadtime=1;\nweapontimer=1;\nweaponvelocity=200;\n[DAMAGE]\n{\ndefault=10;\n}\n}\n")},
	}
	for i := 0; i < authoredDefinitionCount; i++ {
		name := fmt.Sprintf("UNIT%04d", i)
		body := "[UNITINFO]\n{\nUnitName=" + name + ";\nname=Scratch " + name + ";\nside=ARM;\nobjectname=SCRATCH;\n" +
			"BuildCostEnergy=1;\nBuildCostMetal=1;\nMaxDamage=100;\nFootprintX=3;\nFootprintZ=3;\n" +
			"movementclass=TANK3;\nMaxVelocity=1;\nBuildTime=1;\nweapon1=SCRATCHGUN;\n}\n"
		files = append(files, vfs.ArchiveFile{Path: "ZUnits/" + strings.ToLower(name) + ".fbi", Data: []byte(body)})
	}

	var archive bytes.Buffer
	if err := vfs.WriteArchive(&archive, files, vfs.ArchiveWriteOptions{}); err != nil {
		t.Fatalf("author archive: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "authored.hpi"), archive.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	// The two markers detection asks for: the named file, and the renamed unit
	// tree. The tree carries a note rather than a definition, because a loose
	// FBI would shadow the archived one and then be dropped by the admission
	// gate.
	loose := map[string]string{
		"TAZ31.gp3":        "authored marker, not an archive\n",
		"ZUnits/notes.txt": "authored marker tree\n",
	}
	for name, body := range loose {
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

// authoredVisMaskGAF authors the ten-frame `vismask` entry the sight-shape
// compiler requires.
func authoredVisMaskGAF(t *testing.T) []byte {
	t.Helper()
	frames := make([]formats.GAFWriteFrame, 10)
	for i := range frames {
		frames[i] = formats.GAFWriteFrame{Width: 3, Height: 1, Pixels: []byte{7, 7, 7}, Transparent: []bool{true, true, true}}
	}
	data, err := formats.EncodeGAF([]formats.GAFWriteEntry{{Name: "vismask", Frames: frames}})
	if err != nil {
		t.Fatalf("author vismask GAF: %v", err)
	}
	return data
}

// authoredScratchModel is a one-vertex root piece: enough for the named-model
// requirement, with no geometry claim of its own.
func authoredScratchModel(t *testing.T) []byte {
	t.Helper()
	data, err := formats.EncodeThreeDO(&formats.ThreeDO{Root: 0, Objects: []formats.ThreeDOObject{{
		Version: 1, Name: "root", Selection: -1, Parent: -1, FirstChild: -1, NextSibling: -1,
		Vertices: []formats.ThreeDOVertex{{Y: 1 << 16}},
	}}})
	if err != nil {
		t.Fatalf("author 3DO: %v", err)
	}
	return data
}

// TestWindowedCompileUsesTheProfileLimits is the contract this unit exists
// for: the graphical command compiles the one catalog under the resolved
// content profile's table limits, so a content set built for a patched
// executable admits every definition it ships instead of stopping at retail's
// 512-bit domain (docs/DESIGN_CONTENT_VFS.md §5 "Content profiles"). The
// retail baseline is locked on the same bytes, so the test also proves the
// fixture really is past the retail domain rather than merely compiling.
func TestWindowedCompileUsesTheProfileLimits(t *testing.T) {
	cs, err := openContent(Options{Roots: []string{authorWideInstall(t)}})
	if err != nil {
		t.Fatalf("mount the authored wide install: %v", err)
	}
	defer cs.Close()

	if cs.profile != "zero" {
		t.Fatalf("resolved profile = %q, want zero", cs.profile)
	}
	if cs.limits == content.RetailLimits() {
		t.Fatalf("resolved limits = %+v, want the profile's raised tables", cs.limits)
	}

	catalog, err := cs.compileCatalog(nil)
	if err != nil {
		t.Fatalf("compile %d definitions under the profile's limits: %v", authoredDefinitionCount, err)
	}
	if got := len(catalog.Units); got != authoredDefinitionCount {
		t.Fatalf("compiled definitions = %d, want %d", got, authoredDefinitionCount)
	}
	if catalog.Limits != cs.limits {
		t.Fatalf("catalog limits = %+v, want the content set's %+v", catalog.Limits, cs.limits)
	}
	last := catalog.Units[content.CanonicalKey(fmt.Sprintf("UNIT%04d", authoredDefinitionCount-1))]
	if last == nil {
		t.Fatal("the last authored definition is absent from the catalog")
	}
	if int(last.UnitDefID) <= content.RetailDefinitionDomain-1 {
		t.Fatalf("last definition ID = %d, want an ID past the retail domain", last.UnitDefID)
	}

	// The same bytes under the retail baseline stop on the retail domain with
	// the retail diagnostic, which is what a content profile is needed for.
	_, err = content.CompileWithOptions(cs.fs, content.Options{Limits: content.RetailLimits()})
	if err == nil {
		t.Fatal("the retail domain admitted more definitions than it can number")
	}
	want := fmt.Sprintf("category: %d unit definitions exceed %d usable IDs in %d-bit domain",
		authoredDefinitionCount, content.RetailDefinitionDomain-1, content.RetailDefinitionDomain)
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("retail diagnostic = %q, want it to contain %q", err, want)
	}
}
