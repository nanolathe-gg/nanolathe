package units

// UNIT-04 missing/empty COB policy [R-COB-01 §1]: at definition load the
// loader stores a null program for a missing, unreadable, or empty script
// and accepts the definition — no diagnostic, no substitution — and unit
// creation takes the explicit scriptless branch: no VM instance, render
// table still built from the model, no Create started. The residual
// crash-policy question for a scriptless unit whose producer path runs is
// carried as TODO(question) at the attachCOB site, not resolved here.

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/vfs"
)

// sideAnchorFixtures is the 30 mandatory side anchors [02 §6] C8; the
// definition-load fixture needs a sides file every side compiler accepts.
var sideAnchorFixtures = [30]string{
	"LOGO", "ENERGYBAR", "ENERGYNUM", "ENERGYMAX", "ENERGY0",
	"METALBAR", "METALNUM", "METALMAX", "METAL0", "TOTALUNITS",
	"TOTALTIME", "ENERGYPRODUCED", "ENERGYCONSUMED", "METALPRODUCED",
	"METALCONSUMED", "LOGO2", "UNITNAME", "DAMAGEBAR", "UNITMETALMAKE",
	"UNITMETALUSE", "UNITENERGYMAKE", "UNITENERGYUSE", "MISSIONTEXT",
	"UNITNAME2", "DAMAGEBAR2", "NAME", "DESCRIPTION", "RELOAD1",
	"RELOAD2", "RELOAD3",
}

// minimalCOB builds the smallest byte-valid COB: one script named Create
// whose entry is one code word [fmt cob] (44-byte header, 11 × u32 LE).
func minimalCOB(t *testing.T) []byte {
	t.Helper()
	buf := make([]byte, 64)
	u32 := func(off uint32, v uint32) { binary.LittleEndian.PutUint32(buf[off:], v) }
	const headerSize = 44
	u32(0x00, 4)                     // VersionSignature 4 [fmt cob]
	u32(0x04, 1)                     // NumberOfScripts
	u32(0x08, 0)                     // NumberOfPieces
	u32(0x0C, 1)                     // CodeLength words
	u32(0x10, 0)                     // NumberOfStatics
	u32(0x14, 0)                     // Always_0
	u32(0x18, headerSize)            // OffsetToScriptCodeIndexArray
	u32(0x1C, headerSize+4)          // OffsetToScriptNameOffsetArray
	u32(0x20, 0)                     // OffsetToPieceNameOffsetArray (count 0)
	u32(0x24, headerSize+8)          // OffsetToScriptCode
	u32(0x28, 0)                     // OffsetToFirstScriptName (purpose unknown [fmt cob])
	u32(headerSize, 0)               // script 0 code index 0
	u32(headerSize+4, headerSize+12) // script 0 name offset
	u32(headerSize+8, 0x10000000)    // one code word, bit 28 set [04 §4.3]
	copy(buf[headerSize+12:], "Create\x00")
	return buf
}

// compileFixtureCatalog runs the real content compile over a minimal authored
// install: three unit definitions, one with a COB, one with an empty COB
// file, one with no script file at all.
func compileFixtureCatalog(t *testing.T) *content.Catalog {
	t.Helper()
	root := t.TempDir()
	write := func(logical, data string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(logical))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", logical, err)
		}
		if err := os.WriteFile(full, []byte(data), 0o644); err != nil {
			t.Fatalf("write %s: %v", logical, err)
		}
	}
	fbi := "[UNITINFO]\n{\nunitname=%s;\nobjectname=%s;\nmaxdamage=100;\n}\n"
	write("units/armtest.fbi", strings.ReplaceAll(fbi, "%s", "armtest"))
	write("units/armless.fbi", strings.ReplaceAll(fbi, "%s", "armless"))
	write("units/armnone.fbi", strings.ReplaceAll(fbi, "%s", "armnone"))
	write("gamedata/moveinfo.tdf", "[MOVER]\n{\n}\n")
	// content.Compile requires authored sight/LOS resources (retail semantic
	// coverage); author the minimal one-table form the compiler accepts.
	write("gamedata/los.tdf", "[TABLEINFO]\n{\nnumtables=1;\n}\n[TABLE1]\n{\nnumlines=1;\nline1=1,0,1;\n}\n")
	write("anims/vismasks.gaf", string(testVismaskGAF()))
	var b strings.Builder
	b.WriteString("[SIDE0]\n{\nname=ARM;\ncommander=armtest;\nfont=fnt00x.fnt;\n")
	for _, a := range sideAnchorFixtures {
		b.WriteString("[" + a + "]\n{\nx1=0;\ny1=0;\nx2=1;\ny2=1;\n}\n")
	}
	b.WriteString("}\n")
	write("gamedata/sidedata.tdf", b.String())
	write("gamedata/sound.tdf", "[SOUNDS]\n{\n}\n")
	write("gamedata/allsound.tdf", "[ALIASES]\n{\n}\n")
	write("ai/default.txt", "plan any\n")
	// Directories the compilers walk; empty is valid.
	for _, dir := range []string{"maps", "features", "weapons"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	write("scripts/armtest.cob", string(minimalCOB(t)))
	write("scripts/armless.cob", "") // present but empty: the null program form
	fs := vfs.New()
	if err := fs.MountDirectory(root, 10); err != nil {
		t.Fatalf("MountDirectory: %v", err)
	}
	cat, err := content.Compile(fs)
	if err != nil {
		t.Fatalf("content.Compile: %v", err)
	}
	return cat
}

// TestDefinitionLoadStoresNullProgramForMissingOrEmptyCOB locks the
// definition-load half of [R-COB-01 §1]: missing and empty scripts store the
// null program silently and the definition is accepted.
func TestDefinitionLoadStoresNullProgramForMissingOrEmptyCOB(t *testing.T) {
	cat := compileFixtureCatalog(t)
	for _, name := range []string{"armtest", "armless", "armnone"} {
		if _, ok := cat.Unit(name); !ok {
			t.Fatalf("definition %s was not accepted by the loader [R-COB-01 §1]", name)
		}
	}
	// A present, loadable script is stored on the definition.
	armed, _ := cat.Unit("armtest")
	if armed.Script == nil {
		t.Fatal("loadable COB was not stored on its definition at definition load")
	}
	if got, ok := armed.Script.Scripts["Create"]; !ok || got != 0 {
		t.Fatalf("stored program scripts = %v, want Create at word 0", armed.Script.Scripts)
	}
	// Missing and empty both store the null program — no substitute.
	for _, name := range []string{"armless", "armnone"} {
		def, _ := cat.Unit(name)
		if def.Script != nil {
			t.Fatalf("%s: definition carried %T, want the null program [R-COB-01 §1]", name, def.Script)
		}
	}
	// No diagnostic was emitted for the missing or empty script.
	for _, w := range cat.Warnings {
		if strings.Contains(w, ".cob") || strings.Contains(w, "script") {
			t.Fatalf("definition load emitted a COB diagnostic %q; policy is silence [R-COB-01 §1]", w)
		}
	}
}

// TestScriptlessCreationSkipsVMAttach locks the creation half of
// [R-COB-01 §1]: a scriptless definition creates a live unit with no VM and
// no Create, while an armed definition binds its program.
func TestScriptlessCreationSkipsVMAttach(t *testing.T) {
	cat := compileFixtureCatalog(t)
	world := NewSliced(len(cat.Units), cat)
	armlessDef, _ := cat.Unit("armless")
	armless, err := world.Create(armlessDef, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("scriptless definition was rejected at creation: %v [R-COB-01 §1]", err)
	}
	u := world.Unit(armless)
	if u == nil || !u.Alive {
		t.Fatal("scriptless unit is not live")
	}
	if u.GetScript() != nil {
		t.Fatal("scriptless unit received a VM instance; policy attaches none [R-COB-01 §1]")
	}
	if u.COBBinding() != nil {
		t.Fatal("scriptless unit received a binding")
	}
	if u.Flags == 0 {
		t.Fatal("scriptless unit was not initialized")
	}
	if !u.EconomyActive() {
		t.Fatal("scriptless unit skipped its normal instance initialization")
	}
	// The armed definition binds its program and starts Create in mode I.
	armedDef, _ := cat.Unit("armtest")
	armed, err := world.Create(armedDef, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("armed definition rejected: %v", err)
	}
	if world.Unit(armed).GetScript() == nil {
		t.Fatal("armed definition did not bind a VM")
	}
}

// TestLoaderMissIsScriptlessNotSubstituted locks that the fixture loader
// path follows the same policy: a missing script leaves the unit scriptless
// instead of fabricating an empty substitute VM [R-COB-01 §1].
func TestLoaderMissIsScriptlessNotSubstituted(t *testing.T) {
	world := NewSliced(4, nil)
	world.SetCOBSource(vfs.New(), cob.NewCachedLoader())
	def := &content.UnitDef{UnitName: "nosuchscript", MaxDamage: 100, Limit: -1}
	h, err := world.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("missing-script creation was rejected: %v [R-COB-01 §1]", err)
	}
	u := world.Unit(h)
	if u == nil || !u.Alive {
		t.Fatal("missing-script unit is not live")
	}
	if u.GetScript() != nil {
		t.Fatal("missing script produced a substitute VM; policy stores none [R-COB-01 §1]")
	}
}

// testVismaskGAF authors the minimal plural visibility-mask GAF the sight
// compiler requires: one decoy-free entry set with a "vismask" entry of ten
// 1x1 frames. Byte layout mirrors the authored fixture builder in the
// content package's tests (GAF v1.0: 12-byte header, entry table, 40-byte
// entry definitions, 8-byte frame references, 24-byte frame headers).
func testVismaskGAF() []byte {
	const entryTableOffset = 12
	const entrySize = 40
	const frameRefSize = 8
	const frameSize = 24
	const frames = 10
	entryOffset := entryTableOffset + 4
	frameDataOffset := entryOffset + entrySize + frames*frameRefSize + frames*frameSize
	out := make([]byte, frameDataOffset+frames)
	binary.LittleEndian.PutUint32(out[4:], 1) // one entry
	binary.LittleEndian.PutUint32(out[entryTableOffset:], uint32(entryOffset))
	binary.LittleEndian.PutUint16(out[entryOffset:], frames)
	copy(out[entryOffset+8:], "vismask")
	for i := 0; i < frames; i++ {
		ref := entryOffset + entrySize + i*frameRefSize
		frame := entryOffset + entrySize + frames*frameRefSize + i*frameSize
		binary.LittleEndian.PutUint32(out[ref:], uint32(frame))
		binary.LittleEndian.PutUint16(out[frame:], 1)   // width
		binary.LittleEndian.PutUint16(out[frame+2:], 1) // height
		binary.LittleEndian.PutUint16(out[frame+4:], 3) // compressed
		binary.LittleEndian.PutUint16(out[frame+6:], 0xfffe)
		out[frame+8] = 9 // palette index
		binary.LittleEndian.PutUint32(out[frame+16:], uint32(frameDataOffset+i))
		out[frameDataOffset+i] = 7 // one compressed run
	}
	return out
}
