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

func TestFillUnitScriptsRequiresLoadableProgram(t *testing.T) {
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
			err := fillUnitScripts(tt.fs, map[string]*UnitDef{"test": unitScript("TEST")})
			want := "nanolathe: unit script missing: logical path scripts/test.cob, providers searched [" + tt.providers + "], expected COB program"
			if err == nil || err.Error() != want {
				t.Fatalf("fillUnitScripts error = %v, want %q", err, want)
			}
		})
	}
}

func TestFillUnitScriptsRejectsUnreadableWithProvider(t *testing.T) {
	base := newFixtureFS(t, fixtureFile{path: "scripts/test.cob", data: string(contentTestCOB([]uint32{0}))})
	fs := unreadableScriptFS{fixtureFS: base}
	err := fillUnitScripts(fs, map[string]*UnitDef{"test": unitScript("TEST")})
	if err == nil {
		t.Fatal("fillUnitScripts accepted an unreadable COB")
	}
	if !strings.Contains(err.Error(), "logical path scripts/test.cob") || !strings.Contains(err.Error(), "expected COB program") {
		t.Fatalf("unreadable COB diagnostic = %v", err)
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
