package profiles_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/content/profiles"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// The authored fixture install below is the smallest content set
// content.Compile accepts: one unit, one weapon, one feature, the two required
// gamedata tables, the LOS tables, the visibility-mask GAF, the default AI
// profile, the named model, and the directories the compiler enumerates. Every
// byte is authored here; none is copied from a retail install.
//
// The same bytes are then published twice — once under the retail directory
// names, once under TA: Escalation's renamed trees — and the two compiles must
// agree, because a directory rename is a packaging fact and not a definition.

func sideAnchors() string {
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
		b.WriteString("[" + name + "]\n{\nx1=")
		b.WriteString(itoa(i))
		b.WriteString(";\ny1=")
		b.WriteString(itoa(i))
		b.WriteString(";\nx2=")
		b.WriteString(itoa(i + 1))
		b.WriteString(";\ny2=")
		b.WriteString(itoa(i + 1))
		b.WriteString(";\n}\n")
	}
	return b.String()
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var digits [4]byte
	n := len(digits)
	for v > 0 {
		n--
		digits[n] = byte('0' + v%10)
		v /= 10
	}
	return string(digits[n:])
}

// visMaskGAF authors the ten-frame `vismask` entry the sight-shape compiler
// requires.
func visMaskGAF(t *testing.T) []byte {
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

// authoredModel3DO is a one-vertex root piece: enough for the named-model
// requirement, with no geometry claim of its own.
func authoredModel3DO(t *testing.T) []byte {
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

func authoredInstall(t *testing.T) []vfs.ArchiveFile {
	t.Helper()
	return []vfs.ArchiveFile{
		{Path: "ai/default.txt", Data: []byte("plan 0\nweight ARMCOM 100\n")},
		{Path: "anims/vismasks.gaf", Data: visMaskGAF(t)},
		{Path: "download/notes.txt", Data: []byte("authored fixture: no placements\n")},
		{Path: "features/scratch.tdf", Data: []byte("[SCRATCHROCK]\n{\nworld=Scratch World;\ndescription=Rock;\ncategory=rocks;\nfootprintx=1;\nfootprintz=1;\nheight=10;\nblocking=1;\n}\n")},
		{Path: "gamedata/los.tdf", Data: []byte("[TABLEINFO]\n{\nnumtables=1;\n}\n[TABLE1]\n{\nnumlines=1;\nline1=1, 0, 1;\n}\n")},
		{Path: "gamedata/moveinfo.tdf", Data: []byte("[CLASS0]\n{\nName=TANK3;\nFootprintX=3;\nFootprintZ=3;\nMinWaterDepth=0;\nMaxWaterDepth=0;\nMaxSlope=15;\n}\n")},
		{Path: "gamedata/sidedata.tdf", Data: []byte("[SIDE0]\n{\nname=ARM;\ncommander=ARMCOM;\nfont=scratch.fnt;\n" + sideAnchors() + "}\n")},
		{Path: "guis/notes.txt", Data: []byte("authored fixture: no build menus\n")},
		{Path: "maps/notes.txt", Data: []byte("authored fixture: no maps\n")},
		{Path: "objects3d/armcom.3do", Data: authoredModel3DO(t)},
		{Path: "unitpics/notes.txt", Data: []byte("authored fixture: no pictures\n")},
		{Path: "units/armcom.fbi", Data: []byte("[UNITINFO]\n{\nUnitName=ARMCOM;\nname=Scratch Commander;\nside=ARM;\nobjectname=ARMCOM;\nBuildCostEnergy=1;\nBuildCostMetal=1;\nMaxDamage=100;\nFootprintX=3;\nFootprintZ=3;\nmovementclass=TANK3;\nMaxVelocity=1;\nBuildTime=1;\nweapon1=SCRATCHGUN;\n}\n")},
		{Path: "weapons/scratch.tdf", Data: []byte("[SCRATCHGUN]\n{\nid=1;\nname=Scratch Gun;\nrange=100;\nreloadtime=1;\nweapontimer=1;\nweaponvelocity=200;\n[DAMAGE]\n{\ndefault=10;\n}\n}\n")},
	}
}

// renameTrees republishes the authored install with each mapped first segment
// replaced, the way a content set built for a patched executable ships it.
func renameTrees(files []vfs.ArchiveFile, table map[string]string, extra ...vfs.ArchiveFile) []vfs.ArchiveFile {
	out := make([]vfs.ArchiveFile, 0, len(files)+len(extra))
	for _, file := range files {
		renamed := file
		if cut := strings.IndexByte(file.Path, '/'); cut >= 0 {
			if target, ok := table[strings.ToLower(file.Path[:cut])]; ok {
				renamed.Path = target + file.Path[cut:]
			}
		}
		out = append(out, renamed)
	}
	return append(out, extra...)
}

// mountArchive publishes one authored archive as the only provider, which also
// satisfies the retail loose-file admission gate for unit definitions.
func mountArchive(t *testing.T, name string, files []vfs.ArchiveFile) *vfs.FS {
	t.Helper()
	var archive bytes.Buffer
	if err := vfs.WriteArchive(&archive, files, vfs.ArchiveWriteOptions{}); err != nil {
		t.Fatalf("author archive %s: %v", name, err)
	}
	fs := vfs.New()
	t.Cleanup(func() { fs.Close() })
	if _, err := fs.MountArchiveReader(name, bytes.NewReader(archive.Bytes()), int64(archive.Len()), 10, vfs.ArchiveOptions{}); err != nil {
		t.Fatalf("mount archive %s: %v", name, err)
	}
	return fs
}

// TestRenamedTreesCompileToTheRetailCatalogHash is the contract E1 exists for:
// the catalog is a function of the definitions, not of the directories they
// were shipped in. It also locks detection — the renamed install presents TA:
// Escalation's two markers and must be recognised as that profile — and the
// provenance rule, because a logical path that leaked a mod directory name
// would reach every diagnostic and every manifest built from it.
func TestRenamedTreesCompileToTheRetailCatalogHash(t *testing.T) {
	retail := authoredInstall(t)
	retailCatalog, err := content.Compile(mountArchive(t, "authored-retail.hpi", retail))
	if err != nil {
		t.Fatalf("compile retail-named fixture: %v", err)
	}

	escalation, err := profiles.Lookup("escalation")
	if err != nil {
		t.Fatalf("look up the escalation profile: %v", err)
	}
	// The marker file sits beside the renamed trees, as the content set ships
	// it; the renamed `unitsE` tree is the second marker.
	renamedFS := mountArchive(t, "authored-renamed.hpi", renameTrees(retail, escalation.Directories,
		vfs.ArchiveFile{Path: "TAESC.gp3", Data: []byte("authored fixture marker\n")}))

	detected, err := profiles.Detect(renamedFS)
	if err != nil {
		t.Fatalf("detect the renamed fixture's profile: %v", err)
	}
	if detected.Name != "escalation" {
		t.Fatalf("detected profile = %q, want escalation", detected.Name)
	}

	view := detected.Layout().Apply(renamedFS)
	renamedCatalog, err := content.Compile(view)
	if err != nil {
		t.Fatalf("compile renamed fixture through the profile's directory table: %v", err)
	}

	if renamedCatalog.Hash != retailCatalog.Hash {
		t.Fatalf("catalog hash moved with the directory names:\n renamed %s\n retail  %s",
			renamedCatalog.Hash, retailCatalog.Hash)
	}

	// Provenance is what every diagnostic and every identity built from a
	// logical path reads, so the mod directory must not survive into it.
	unit := renamedCatalog.Units[content.CanonicalKey("ARMCOM")]
	if unit == nil {
		t.Fatal("renamed fixture produced no ARMCOM definition")
	}
	if unit.Provenance.LogicalPath != "units/armcom.fbi" {
		t.Fatalf("unit provenance = %q, want the retail-named units/armcom.fbi", unit.Provenance.LogicalPath)
	}
	weapon := renamedCatalog.Weapons[content.CanonicalKey("SCRATCHGUN")]
	if weapon == nil {
		t.Fatal("renamed fixture produced no SCRATCHGUN definition")
	}
	if weapon.Provenance.LogicalPath != "weapons/scratch.tdf" {
		t.Fatalf("weapon provenance = %q, want the retail-named weapons/scratch.tdf", weapon.Provenance.LogicalPath)
	}
	if got := unit.ScriptProvenance.LogicalPath; got != "scripts/armcom.cob" {
		t.Fatalf("script provenance = %q, want scripts/armcom.cob", got)
	}

	// A retail-named install is not a mod: detection answers `retail` and the
	// layout it carries wraps nothing, so the overlay reaches the compiler as
	// the concrete type its optional views are asserted for.
	retailFS := mountArchive(t, "authored-retail-detect.hpi", retail)
	plain, err := profiles.Detect(retailFS)
	if err != nil {
		t.Fatalf("detect the retail-named fixture's profile: %v", err)
	}
	if plain.Name != profiles.RetailName {
		t.Fatalf("detected profile = %q, want %s", plain.Name, profiles.RetailName)
	}
	if applied := plain.Layout().Apply(retailFS); applied != vfs.FSOps(retailFS) {
		t.Fatal("the retail profile wrapped the overlay; an empty layout must return it unchanged")
	}
}
