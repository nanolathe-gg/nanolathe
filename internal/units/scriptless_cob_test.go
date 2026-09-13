package units

// These fixtures exercise the strict catalog and production creation
// boundaries without introducing a missing-program compatibility path.

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/vfs"
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

// minimalThreeDO authors the smallest valid 3DO for the strict catalog
// fixture. It is intentionally geometry-only: this test exercises COB
// publication, while the named object still must satisfy catalog validation.
func minimalThreeDO(t *testing.T) []byte {
	t.Helper()
	data, err := formats.EncodeThreeDO(&formats.ThreeDO{Root: 0, Objects: []formats.ThreeDOObject{{
		Version: 1, Name: "root", Selection: -1, Parent: -1, FirstChild: -1, NextSibling: -1,
		Vertices: []formats.ThreeDOVertex{{}},
	}}})
	if err != nil {
		t.Fatalf("EncodeThreeDO: %v", err)
	}
	return data
}

// compileFixtureCatalog runs the real content compile over a minimal authored
// install with a loadable COB for every unit definition.
func compileFixtureCatalog(t *testing.T, missing ...string) *content.Catalog {
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
	fbi := "[UNITINFO]\n{\nunitname=%s;\nobjectname=%s;\nmaxdamage=100;\nVersion=3.1;\nCopyright=Copyright 1997 Humongous Entertainment. All rights reserved.;\n}\n"
	write("units/armtest.fbi", strings.ReplaceAll(fbi, "%s", "armtest"))
	write("units/armless.fbi", strings.ReplaceAll(fbi, "%s", "armless"))
	write("units/armnone.fbi", strings.ReplaceAll(fbi, "%s", "armnone"))
	for _, name := range []string{"armtest", "armless", "armnone"} {
		write("objects3d/"+name+".3do", string(minimalThreeDO(t)))
	}
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
	for _, name := range []string{"armtest", "armless", "armnone"} {
		if len(missing) != 0 && name == missing[0] {
			continue
		}
		write("scripts/"+name+".cob", string(minimalCOB(t)))
	}
	mounted := vfs.New()
	t.Cleanup(func() { _ = mounted.Close() })
	if err := mounted.MountDirectory(root, 10); err != nil {
		t.Fatalf("MountDirectory: %v", err)
	}
	cat, err := content.Compile(archivedFixtureFS{mounted})
	if err != nil {
		t.Fatalf("content.Compile: %v", err)
	}
	return cat
}

// Available scripts remain immutable catalog programs [R-COB-04 §8].
func TestDefinitionLoadRequiresLoadableCOB(t *testing.T) {
	cat := compileFixtureCatalog(t)
	for _, name := range []string{"armtest", "armless", "armnone"} {
		def, ok := cat.Unit(name)
		if !ok || def == nil || def.Script == nil || len(def.Script.Code) == 0 {
			t.Fatalf("definition %s lacks a loadable COB: %#v", name, def)
		}
	}
	armed, _ := cat.Unit("armtest")
	if got, ok := armed.Script.Scripts["Create"]; !ok || got != 0 {
		t.Fatalf("stored program scripts = %v, want Create at word 0", armed.Script.Scripts)
	}
}

// Missing programs remain discoverable but cannot become fresh, captured,
// unfinished or restored instances. Refusal precedes slots and random draws
// under the host policy of DESIGN_UNITS_ORDERS_COB §2.1 [R-COB-04 §8].
func TestCatalogMissingScriptRefusesAllCreationPaths(t *testing.T) {
	cat := compileFixtureCatalog(t, "armnone")
	bad, ok := cat.Unit("armnone")
	if !ok || bad.Script != nil {
		t.Fatal("catalog dropped unavailable definition or invented its script")
	}
	foundWarning := false
	for _, warning := range cat.Warnings {
		foundWarning = foundWarning || strings.Contains(warning, "logical path scripts/armnone.cob")
	}
	good, _ := cat.Unit("armtest")
	if !foundWarning || good.Script == nil {
		t.Fatal("catalog lost warning or unrelated valid program")
	}
	for _, tc := range []struct {
		name   string
		create func(*World) (pool.Handle, error)
	}{
		{"fresh", func(w *World) (pool.Handle, error) { return w.Create(bad, 0, 0, 0, 0) }},
		{"nanoframe", func(w *World) (pool.Handle, error) { return w.CreateNanoframe(bad, 0, 0, 0, 0) }},
		{"capture", func(w *World) (pool.Handle, error) { return w.CreateWithMoverMode(bad, 0, 0, 0, 0, 1) }},
		{"restore", func(w *World) (pool.Handle, error) { return w.CreateWithForcedSlot(bad, 0, 0, 0, 0, 1) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := NewSliced(4, cat)
			sim := rng.NewSimulation(7)
			w.SetSimulationRNG(&sim)
			h, err := tc.create(w)
			if h != 0 || err == nil || !strings.Contains(err.Error(), "unit script missing") {
				t.Fatalf("unavailable program admitted: handle=%d err=%v", h, err)
			}
			if w.Used() != 0 || sim.Draws() != 0 {
				t.Fatal("rejected program consumed allocation or RNG")
			}
		})
	}
}

// TestLoaderMissRejectsProduction locks that a configured production source
// rejects a missing script rather than fabricating an empty substitute VM
// [R-COB-04 §8].
func TestLoaderMissRejectsProduction(t *testing.T) {
	world := NewSliced(4, nil)
	world.SetCOBSource(vfs.New(), cob.NewCachedLoader())
	def := &content.UnitDef{UnitName: "nosuchscript", MaxDamage: 100, Limit: -1}
	h, err := world.Create(def, 0, 0, 0, 0)
	if err == nil {
		t.Fatalf("missing-script creation succeeded with handle %d", h)
	}
	if !strings.Contains(err.Error(), "unit script missing") {
		t.Fatalf("missing-script rejection = %v, want missing-script diagnostic", err)
	}
	if world.Used() != 0 {
		t.Fatalf("missing-script rejection consumed a pool slot: used=%d", world.Used())
	}
}

func TestUnconfiguredWorldRejectsMissingCOBBeforeAllocation(t *testing.T) {
	world := NewSliced(4, nil)
	def := &content.UnitDef{UnitName: "unconfigured-missing", MaxDamage: 100, Limit: -1}
	if _, err := world.Create(def, 0, 0, 0, 0); err == nil {
		t.Fatal("unconfigured world accepted a missing COB")
	} else if !strings.Contains(err.Error(), "unit script missing") {
		t.Fatalf("unconfigured missing-script rejection = %v, want missing-script diagnostic", err)
	}
	if world.Used() != 0 {
		t.Fatalf("unconfigured rejection consumed a pool slot: used=%d", world.Used())
	}
}

func TestEmptyProgramRejectsBeforeAllocation(t *testing.T) {
	world := NewSliced(4, nil)
	world.SetCOBSource(vfs.New(), cob.NewCachedLoader())
	def := &content.UnitDef{UnitName: "emptyprogram", MaxDamage: 100, Limit: -1, Script: &cob.Program{}}
	if _, err := world.Create(def, 0, 0, 0, 0); err == nil {
		t.Fatal("empty program creation succeeded")
	} else if !strings.Contains(err.Error(), "unit script missing") {
		t.Fatalf("empty-program rejection = %v, want missing-script diagnostic", err)
	}
	if world.Used() != 0 {
		t.Fatalf("empty-program rejection consumed a pool slot: used=%d", world.Used())
	}
}

func TestConfiguredLoaderReplacesEmptyDefinitionProgram(t *testing.T) {
	world := NewSliced(4, nil)
	world.SetCOBSource(fixtureCOBFS{}, cob.NewCachedLoader())
	def := &content.UnitDef{UnitName: "loaderfixture", MaxDamage: 100, Limit: -1, Script: &cob.Program{}}
	h, err := world.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("empty definition program with loadable source: %v", err)
	}
	if world.Unit(h) == nil || world.Unit(h).GetScript() == nil {
		t.Fatal("configured loader did not attach its loadable COB")
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

// archivedFixtureFS presents a loose fixture tree as archive content.
//
// Retail enumerates loose `units\*.FBI` and `weapons\*.tdf`, parses them, and
// then always drops them: a definition record survives only when its file came
// from a mounted archive on an installed build [02 R-CAT-01 §4 "The loose-file
// gate"]. The content compiler implements that gate, so a fixture that writes
// authored files into a plain directory compiles to an empty catalog.
//
// Packing a real HPI here would test the archive reader, not the strict COB
// boundary these fixtures exist for, so the wrapper instead states the
// provenance the gate reads — the seam discoverArchiveContent documents for
// exactly this case. It deliberately does not implement the overlay's mount
// surface: there is no loose winner shadowing an archive entry to recover.
type archivedFixtureFS struct{ vfs.FSOps }

func (f archivedFixtureFS) ReadDir(name string) ([]vfs.EntryInfo, error) {
	entries, err := f.FSOps.ReadDir(name)
	if err != nil {
		return nil, err
	}
	for i := range entries {
		if entries[i].IsDir {
			continue
		}
		entries[i].Source.ProviderType = "hpi"
		entries[i].Source.SourcePath = "fixture.hpi"
	}
	return entries, nil
}
