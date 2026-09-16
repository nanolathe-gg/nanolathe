package content

import (
	"encoding/binary"
	"errors"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/vfs"
)

func contentTestCOB(code []uint32) []byte {
	const scriptIndexOffset = 44
	const scriptNameOffsetArray = 48
	const pieceNameOffsetArray = 52
	const codeOffset = 56
	codeBytes := len(code) * 4
	scriptName := codeOffset + codeBytes
	pieceName := scriptName + len("Create") + 1
	data := make([]byte, pieceName+len("base")+1)
	binary.LittleEndian.PutUint32(data[0:], 4)
	binary.LittleEndian.PutUint32(data[4:], 1)
	binary.LittleEndian.PutUint32(data[8:], 1)
	binary.LittleEndian.PutUint32(data[12:], uint32(len(code)))
	binary.LittleEndian.PutUint32(data[16:], 0)
	binary.LittleEndian.PutUint32(data[20:], 0)
	binary.LittleEndian.PutUint32(data[24:], scriptIndexOffset)
	binary.LittleEndian.PutUint32(data[28:], scriptNameOffsetArray)
	binary.LittleEndian.PutUint32(data[32:], pieceNameOffsetArray)
	binary.LittleEndian.PutUint32(data[36:], codeOffset)
	binary.LittleEndian.PutUint32(data[40:], uint32(scriptName))
	binary.LittleEndian.PutUint32(data[scriptIndexOffset:], 0)
	binary.LittleEndian.PutUint32(data[scriptNameOffsetArray:], uint32(scriptName))
	binary.LittleEndian.PutUint32(data[pieceNameOffsetArray:], uint32(pieceName))
	for i, word := range code {
		binary.LittleEndian.PutUint32(data[codeOffset+i*4:], word)
	}
	copy(data[scriptName:], "Create\x00")
	copy(data[pieceName:], "base\x00")
	return data
}

type unreadableScriptFS struct{ *fixtureFS }

func (f unreadableScriptFS) ReadFileLimit(name string, max int64) ([]byte, error) {
	if strings.HasPrefix(strings.ToLower(name), "scripts/") {
		return nil, errors.New("fixture: script read failed")
	}
	return f.fixtureFS.ReadFileLimit(name, max)
}

func unitScript(name string) *UnitDef {
	return &UnitDef{UnitName: name}
}

func TestFillUnitScriptsWarnsWithoutInventingProgram(t *testing.T) {
	tests := []struct {
		name      string
		fs        vfs.FSOps
		providers string
	}{
		{name: "missing", fs: newFixtureFS(t), providers: ""},
		{name: "empty file", fs: newFixtureFS(t, fixtureFile{path: "scripts/test.cob"}), providers: "unknown"},
		{name: "malformed", fs: newFixtureFS(t, fixtureFile{path: "scripts/test.cob", data: "not a COB"}), providers: "unknown"},
		{name: "empty code", fs: newFixtureFS(t, fixtureFile{path: "scripts/test.cob", data: string(contentTestCOB(nil))}), providers: "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := unitScript("TEST")
			warnings := fillUnitScripts(tt.fs, map[string]*UnitDef{"test": u})
			want := "nanolathe: unit script missing: logical path scripts/test.cob, providers searched [" + tt.providers + "], expected COB program"
			if len(warnings) != 1 || !strings.HasPrefix(warnings[0], want) {
				t.Fatalf("fillUnitScripts warnings = %v, want %q", warnings, want)
			}
			if u.Script != nil || u.ScriptProvenance.LogicalPath != "scripts/test.cob" {
				t.Fatal("unavailable script gained a program or lost its logical path")
			}
		})
	}
}

func TestFillUnitScriptsRejectsUnreadableWithProvider(t *testing.T) {
	base := newFixtureFS(t, fixtureFile{path: "scripts/test.cob", data: string(contentTestCOB([]uint32{0}))})
	fs := unreadableScriptFS{fixtureFS: base}
	warnings := fillUnitScripts(fs, map[string]*UnitDef{"test": unitScript("TEST")})
	if len(warnings) != 1 || !strings.Contains(warnings[0], "logical path scripts/test.cob") || !strings.Contains(warnings[0], "fixture: script read failed") {
		t.Fatalf("unreadable COB diagnostic = %v", warnings)
	}
}

func TestFillUnitScriptsStoresProgram(t *testing.T) {
	fs := newFixtureFS(t, fixtureFile{path: "scripts/test.cob", data: string(contentTestCOB([]uint32{0}))})
	defs := map[string]*UnitDef{"test": unitScript("TEST")}
	if err := fillUnitScripts(fs, defs); err != nil {
		t.Fatalf("fillUnitScripts: %v", err)
	}
	if defs["test"].Script == nil || len(defs["test"].Script.Code) != 1 {
		t.Fatalf("loaded script = %#v, want one code word", defs["test"].Script)
	}
	if got := defs["test"].ScriptProvenance.LogicalPath; got != "scripts/test.cob" {
		t.Fatalf("script provenance logical path = %q", got)
	}
}

func TestFillUnitScriptKeepsEmptyBasename(t *testing.T) {
	fs := newFixtureFS(t, fixtureFile{path: "scripts/.cob", data: string(contentTestCOB([]uint32{0}))})
	u := &UnitDef{}
	if err := fillUnitRecordScripts(fs, []*UnitDef{u}); err != nil {
		t.Fatal(err)
	}
	if u.Script == nil || u.ScriptProvenance.LogicalPath != "scripts/.cob" {
		t.Fatal("empty name lost script or provenance")
	}
	if warnings := fillUnitRecordScripts(newFixtureFS(t), []*UnitDef{{}}); len(warnings) != 1 || !strings.Contains(warnings[0], "scripts/.cob") {
		t.Fatalf("empty script diagnostic = %v", warnings)
	}
}

// A broken candidate must not prevent a later definition from receiving its
// immutable program. The broken definition is left with no program at all,
// which is what refuses it where a unit is created [04 R-COB-04 §8].
func TestScriptMissRetainsLaterProgram(t *testing.T) {
	fs := newFixtureFS(t, fixtureFile{path: "scripts/valid.cob", data: string(contentTestCOB([]uint32{0}))})
	bad, good := unitScript("missing"), unitScript("valid")
	warnings := fillUnitRecordScripts(fs, []*UnitDef{bad, good})
	if len(warnings) != 1 || bad.Script != nil || good.Script == nil {
		t.Fatalf("candidate boundary lost: warnings=%v, bad=%v, good=%v", warnings, bad.Script, good.Script)
	}
}

// A callback directory cannot make an empty code body usable: the definition
// keeps its warning and receives no program [04 R-COB-04 §8].
func TestEmptyScriptCodeRetainsWarningAndNoProgram(t *testing.T) {
	fs := newFixtureFS(t, fixtureFile{path: "scripts/empty.cob", data: string(contentTestCOB(nil))})
	u := unitScript("empty")
	warnings := fillUnitRecordScripts(fs, []*UnitDef{u})
	if len(warnings) != 1 || u.Script != nil {
		t.Fatalf("empty code admission: warnings=%v program=%v", warnings, u.Script)
	}
}
