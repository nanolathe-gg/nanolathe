package units

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/model"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/vfs"
)

func TestYardOpenPortWithoutAdmissionRoundTrips(t *testing.T) {
	u := &Unit{}
	port := unitPortHandlers(nil, u)[cob.Port(18)]
	if got := port([]int32{18, 1}); got != 0 {
		t.Fatalf("yard-open write returned %d, want 0", got)
	}
	if !u.YardOpen || port([]int32{18}) != 1 {
		t.Fatal("no-hook yard-open write did not commit and read back one")
	}
	if got := port([]int32{18, 0}); got != 0 {
		t.Fatalf("yard-close write returned %d, want 0", got)
	}
	if u.YardOpen || port([]int32{18}) != 0 {
		t.Fatal("no-hook yard-close write did not commit and read back zero")
	}
}

func TestYardOpenTransactionDelegatesBeforeAcceptedCommit(t *testing.T) {
	u := &Unit{}
	called := 0
	var requested, observedBefore bool
	u.SetYardOpenTransaction(func(next bool) {
		called++
		requested = next
		observedBefore = u.YardOpen
		u.YardOpen = next
	})
	port := unitPortHandlers(nil, u)[cob.Port(18)]
	if got := port([]int32{18, 1}); got != 0 {
		t.Fatalf("accepted yard-open write returned %d, want 0", got)
	}
	if called != 1 || !requested || observedBefore {
		t.Fatalf("admission ordering = calls %d requested %t prior %t, want one/true/false", called, requested, observedBefore)
	}
	if !u.YardOpen || port([]int32{18}) != 1 {
		t.Fatal("accepted yard-open request did not commit")
	}
}

func TestYardOpenTransactionDenialPreservesPriorLevel(t *testing.T) {
	u := &Unit{YardOpen: true}
	called := 0
	var requested, observedBefore bool
	u.SetYardOpenTransaction(func(next bool) {
		called++
		requested = next
		observedBefore = u.YardOpen
	})
	port := unitPortHandlers(nil, u)[cob.Port(18)]
	if got := port([]int32{18, 0}); got != 0 {
		t.Fatalf("denied yard-close write returned %d, want 0", got)
	}
	if called != 1 || requested || !observedBefore {
		t.Fatalf("denied admission ordering = calls %d requested %t prior %t, want one/false/true", called, requested, observedBefore)
	}
	if !u.YardOpen || port([]int32{18}) != 1 {
		t.Fatal("denied yard-close request changed the prior open level")
	}
}

func TestYardOpenTransactionIsActiveDuringStrictCreate(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "scripts", "yardcreate.cob"), yardOpenCreateCOB(), 0o644); err != nil {
		t.Fatal(err)
	}
	fs := vfs.New()
	if err := fs.MountDirectory(root, 1); err != nil {
		t.Fatal(err)
	}

	def := &content.UnitDef{UnitName: "yardcreate", DefinitionHeader: content.DefinitionHeader{CanonicalKey: "yardcreate"}}
	mdl := &model.Model{Name: "yardcreate", Root: 0, Pieces: []model.Piece{{Name: "base", Parent: -1}}}
	u := &Unit{Def: def}
	calledDuringCreate := 0
	u.SetYardOpenTransaction(func(requested bool) {
		calledDuringCreate++
		if !requested {
			t.Fatal("Create requested yard closed, want open")
		}
		if u.GetScript() == nil {
			t.Fatal("yard admission ran without the pre-Create binding context")
		}
	})
	sim := rng.NewSimulation(1)
	binding, err := BindCOBWithPortsAndVisibilityForUnit(fs, u, mdl, &sim, nil, nil)
	if err != nil {
		t.Fatalf("strict binding: %v", err)
	}
	if calledDuringCreate != 1 {
		t.Fatalf("yard admission calls during Create = %d, want 1", calledDuringCreate)
	}
	if u.YardOpen {
		t.Fatal("denied Create-time yard write committed before attachment")
	}
	if u.COBBinding() != binding {
		t.Fatal("Create completed without retaining its pre-Create binding")
	}
}

func yardOpenCreateCOB() []byte {
	code := []uint32{
		0x10021001, 18, // push YARD_OPEN identifier
		0x10021001, 1, // push requested level
		0x10082000, // set engine port
		0x10065000, // return
	}
	const headerSize = 44
	offScriptIndex := headerSize + len(code)*4
	offScriptNames := offScriptIndex + 4
	offPieceNames := offScriptNames + 4
	stringsStart := offPieceNames + 4
	createNameOffset := stringsStart
	pieceNameOffset := createNameOffset + len("Create") + 1
	data := make([]byte, pieceNameOffset+len("base")+1)
	put := func(off int, value uint32) { binary.LittleEndian.PutUint32(data[off:], value) }
	put(0, 4) // TA COB version signature [fmt cob].
	put(4, 1)
	put(8, 1)
	put(12, uint32(len(code)))
	put(24, uint32(offScriptIndex))
	put(28, uint32(offScriptNames))
	put(32, uint32(offPieceNames))
	put(36, headerSize)
	put(40, uint32(createNameOffset))
	for i, word := range code {
		put(headerSize+i*4, word)
	}
	put(offScriptIndex, 0)
	put(offScriptNames, uint32(createNameOffset))
	put(offPieceNames, uint32(pieceNameOffset))
	copy(data[createNameOffset:], "Create\x00")
	copy(data[pieceNameOffset:], "base\x00")
	return data
}
