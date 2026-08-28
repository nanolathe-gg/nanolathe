package units

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/model"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/vfs"
)

// TestBindCOBForUnitRunsCreateOnceBeforeAttach checks the production unit
// binding path: instance ports are installed, mode-I Create runs once, and
// only then can the binding be attached to a playable unit [04 §5.1].
func TestBindCOBForUnitRunsCreateOnceBeforeAttach(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "scripts", "testunit.cob"), unitCreateCOB(), 0o644); err != nil {
		t.Fatal(err)
	}
	fs := vfs.New()
	if err := fs.MountDirectory(root, 1); err != nil {
		t.Fatal(err)
	}

	def := &content.UnitDef{UnitName: "testunit", DefinitionHeader: content.DefinitionHeader{CanonicalKey: content.CanonicalKey("testunit")}}
	mdl := &model.Model{Name: "testunit", Root: 0, Pieces: []model.Piece{{Name: "base", Parent: -1}}}
	u := &Unit{Def: def}
	sim := rng.NewSimulation(1)
	binding, err := BindCOBWithPortsAndVisibilityForUnit(fs, u, mdl, &sim, nil, nil)
	if err != nil {
		t.Fatalf("BindCOBWithPortsAndVisibilityForUnit: %v", err)
	}
	if !binding.CreateInvoked || binding.Callbacks == nil || !binding.Callbacks.CreateInvoked() {
		t.Fatalf("binding did not record one completed Create: %+v", binding)
	}
	if binding.VM.DrainCalls != 1 {
		t.Fatalf("mode-I Create used %d VM drains, want one delta-zero barrier", binding.VM.DrainCalls)
	}
	if err := u.AttachCOBBinding(binding); err != nil {
		t.Fatalf("AttachCOBBinding: %v", err)
	}
	if u.COBBinding() != binding || u.GetScript() != binding.VM {
		t.Fatal("unit did not retain the initialized strict binding")
	}
}

// unitCreateCOB is a small authored fixture containing only a returning
// Create entry point and one model piece [fmt cob].
func unitCreateCOB() []byte {
	const (
		offCode          = 44
		offScriptIndex   = offCode + 4
		offScriptNames   = offScriptIndex + 4
		offPieceNames    = offScriptNames + 4
		stringsStart     = offPieceNames + 4
		createNameOffset = stringsStart
		pieceNameOffset  = stringsStart + len("Create") + 1
	)
	data := make([]byte, pieceNameOffset+len("base")+1)
	binary.LittleEndian.PutUint32(data[0:], 4) // COB version signature
	binary.LittleEndian.PutUint32(data[4:], 1) // one script
	binary.LittleEndian.PutUint32(data[8:], 1) // one piece
	binary.LittleEndian.PutUint32(data[12:], 1)
	binary.LittleEndian.PutUint32(data[24:], offScriptIndex)
	binary.LittleEndian.PutUint32(data[28:], offScriptNames)
	binary.LittleEndian.PutUint32(data[32:], offPieceNames)
	binary.LittleEndian.PutUint32(data[36:], offCode)
	binary.LittleEndian.PutUint32(data[40:], createNameOffset)
	binary.LittleEndian.PutUint32(data[offCode:], 0x10065000) // return
	binary.LittleEndian.PutUint32(data[offScriptIndex:], 0)
	binary.LittleEndian.PutUint32(data[offScriptNames:], createNameOffset)
	binary.LittleEndian.PutUint32(data[offPieceNames:], uint32(pieceNameOffset))
	copy(data[createNameOffset:], "Create\x00")
	copy(data[pieceNameOffset:], "base\x00")
	return data
}
