package session

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/testsupport/retailcat"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/vfs"
)

// TestCompositionRetailCOBBindings exercises the production path against the
// authored chain selected by the retail preflight: commander, Peewee, solar,
// mex, and Kbot lab. It is deliberately asset-gated; no synthetic model or
// empty COB is accepted here.
//
// Read-only: it only looks up units and binds independently-constructed COB
// state, it never writes into the catalog, so it shares the process-wide
// compile [internal/testsupport/retailcat].
func TestCompositionRetailCOBBindings(t *testing.T) {
	cat, fs := retailcat.Shared(t)
	manifest, err := content.PreflightSkirmish(fs, cat, "Ashap Plateau", 0)
	if err != nil {
		t.Fatalf("retail preflight: %v", err)
	}
	names := []string{manifest.Commander, manifest.LabProduct, manifest.Solar, manifest.Mex, manifest.KbotLab}
	for _, name := range names {
		def, ok := cat.Unit(name)
		if !ok || def == nil {
			t.Fatalf("preflight unit %q missing from catalog", name)
		}
		mdl, firstProv, err := loadAuthoredModel(fs, def.ObjectName)
		if err != nil {
			t.Fatalf("%s model: %v", name, err)
		}
		mdlAgain, secondProv, err := loadAuthoredModel(fs, def.ObjectName)
		if err != nil || mdlAgain != mdl {
			t.Fatalf("%s model cache identity not stable: first=%p second=%p err=%v", name, mdl, mdlAgain, err)
		}
		if firstProv.ProviderID() == "" || secondProv.ProviderID() == "" || len(mdl.Pieces) == 0 {
			t.Fatalf("%s missing model provenance/pieces: provider=%q pieces=%d", name, firstProv.ProviderID(), len(mdl.Pieces))
		}
		sim := rng.NewSimulation(1)
		binding, err := units.BindCOBWithPorts(fs, def, mdl, &sim, nil)
		if err != nil {
			t.Fatalf("%s strict COB binding: %v", name, err)
		}
		u := &units.Unit{Def: def}
		if err := u.AttachCOBBinding(binding); err != nil {
			t.Fatalf("%s strict COB attachment: %v", name, err)
		}
		if u.COBBinding() != binding || !binding.CreateInvoked || !binding.Callbacks.CreateInvoked() {
			t.Fatalf("%s did not retain exactly-once Create binding", name)
		}
	}
}

func TestCompositionStrictBindingRejectsMissingModel(t *testing.T) {
	fs := vfs.New()
	s := &Session{}
	u := &units.Unit{Def: &content.UnitDef{UnitName: "armcom", ObjectName: "missing"}}
	if err := s.bindUnitCOB(fs, u); err == nil {
		t.Fatal("missing authored model unexpectedly entered production binding")
	}
	if u.GetScript() != nil || u.COBBinding() != nil {
		t.Fatal("failed strict binding left a playable VM attached")
	}
}

func TestCompositionModelCacheIncludesWinningProvider(t *testing.T) {
	rootA, rootB := t.TempDir(), t.TempDir()
	writeCompositionModel(t, rootA, "shared", 1)
	writeCompositionModel(t, rootB, "shared", 9)
	fsA, fsB := vfs.New(), vfs.New()
	if err := fsA.MountDirectory(rootA, 10); err != nil {
		t.Fatal(err)
	}
	if err := fsB.MountDirectory(rootB, 10); err != nil {
		t.Fatal(err)
	}
	mA, pA, err := loadAuthoredModel(fsA, "shared")
	if err != nil {
		t.Fatal(err)
	}
	mB, pB, err := loadAuthoredModel(fsB, "shared")
	if err != nil {
		t.Fatal(err)
	}
	if mA == mB || mA.Pieces[0].Translate == mB.Pieces[0].Translate {
		t.Fatalf("same-name models cross-contaminated: A=%p %v B=%p %v", mA, mA.Pieces[0].Translate, mB, mB.Pieces[0].Translate)
	}
	if pA.SourcePath == pB.SourcePath || pA.MountRoot == pB.MountRoot {
		t.Fatalf("provider provenance was not retained: A=%#v B=%#v", pA, pB)
	}
}

func TestCOBPresentationSinkMapsPieceIdentity(t *testing.T) {
	s := &Session{publication: newPublicationState(frame.NewEventBuffer(frame.Limits{}))}
	sink := &cobPresentationSink{publication: s.publication, clock: s.Clock, source: 1}
	sink.SetCOBPieceMap([]int{1, 0})
	sink.EmitCOBEvent(cob.PresentationEvent{Kind: cob.PresentationSFX, Piece: 0, SFXType: 1})
	sink.EmitCOBEvent(cob.PresentationEvent{Kind: cob.PresentationSFX, Piece: 2, SFXType: 1})
	events := s.publication.events.Events()
	if len(events) != 1 || events[0].Piece != 1 {
		t.Fatalf("mapped presentation events = %#v, want one model piece 1", events)
	}
}

func TestCompositionBinderFutureAllocationIsStrictAndPreCreate(t *testing.T) {
	root := t.TempDir()
	writeCompositionModel(t, root, "fixture", 1)
	writeCompositionCOB(t, root, "testunit", []string{"modelroot", "modelchild"})
	fs := vfs.New()
	if err := fs.MountDirectory(root, 10); err != nil {
		t.Fatal(err)
	}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{}}
	good := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "testunit"}, UnitName: "testunit", ObjectName: "fixture", MaxDamage: 10, Limit: -1}
	cat.Units[good.CanonicalKey] = good
	s := &Session{rngSim: rng.NewSimulation(77), rngCrt: rng.NewCRT(9), rngInitialized: true, publication: newPublicationState(frame.NewEventBuffer(frame.Limits{}))}
	w := units.NewSliced(2, cat)
	w.SetCOBSource(fs, globalCobLoader)
	w.SetCOBBinder(func(u *units.Unit) error { return s.bindUnitCOB(fs, u) })
	var createCalls int
	w.OnCreate = func(_ pool.Handle, u *units.Unit) {
		createCalls++
		if u.COBBinding() == nil || u.COBBinding().SimulationRNG != s.SimRNG() {
			t.Fatalf("OnCreate observed incomplete/non-session binding: %#v", u.COBBinding())
		}
	}
	h, err := w.Create(good, 0, 0, 0, 0)
	if err != nil || h == 0 {
		t.Fatalf("strict future allocation: h=%d err=%v", h, err)
	}
	if createCalls != 1 || w.Unit(h).COBBinding().Model == nil || w.Unit(h).COBBinding().Callbacks.CreateInvoked() == false {
		t.Fatalf("create ordering/count/model: calls=%d unit=%#v", createCalls, w.Unit(h))
	}
	bad := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "badunit"}, UnitName: "badunit", ObjectName: "missing", MaxDamage: 10, Limit: -1}
	if _, err := w.Create(bad, 0, 0, 0, 0); err == nil {
		t.Fatal("missing strict model/script unexpectedly allocated")
	}
	if createCalls != 1 || w.Unit(2) != nil {
		t.Fatalf("failed allocation was observable: calls=%d slot2=%#v", createCalls, w.Unit(2))
	}
}

func writeCompositionModel(t *testing.T, root, name string, rootTranslate int32) {
	t.Helper()
	dir := filepath.Join(root, "objects3d")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".3do"), fixture3DO(rootTranslate), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeCompositionCOB(t *testing.T, root, name string, pieces []string) {
	t.Helper()
	dir := filepath.Join(root, "scripts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".cob"), fixtureCOB(pieces), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fixture3DO(rootTranslate int32) []byte {
	data := make([]byte, 512)
	put := func(off int, v int32) { binary.LittleEndian.PutUint32(data[off:], uint32(v)) }
	putObject := func(off int, translation int32, nameOffset int32, child int32) {
		put(off+0, 1)
		put(off+4, 0)
		put(off+8, 0)
		put(off+12, -1)
		put(off+16, translation*65536)
		put(off+20, 0)
		put(off+24, 0)
		put(off+28, nameOffset)
		put(off+32, 0)
		put(off+36, 0)
		put(off+40, 0)
		put(off+44, 0)
		put(off+48, child)
	}
	putObject(0, rootTranslate, 400, 52)
	putObject(52, 2, 410, 0)
	copy(data[400:], "modelroot\x00")
	copy(data[410:], "modelchild\x00")
	return data
}

func fixtureCOB(pieces []string) []byte {
	const header = 44
	code := []uint32{0x10065000}
	nScripts, nPieces := 1, len(pieces)
	offScriptIndex := uint32(header + len(code)*4)
	offScriptNames := offScriptIndex + 4
	offPieceNames := offScriptNames + 4
	strStart := offPieceNames + uint32(nPieces*4)
	stringsLen := len("Create") + 1
	for _, piece := range pieces {
		stringsLen += len(piece) + 1
	}
	data := make([]byte, int(strStart)+stringsLen)
	put := func(off int, v uint32) { binary.LittleEndian.PutUint32(data[off:], v) }
	put(0, 4)
	put(4, uint32(nScripts))
	put(8, uint32(nPieces))
	put(12, uint32(len(code)))
	put(24, offScriptIndex)
	put(28, offScriptNames)
	put(32, offPieceNames)
	put(36, header)
	put(40, strStart)
	put(44, code[0])
	put(int(offScriptIndex), 0)
	put(int(offScriptNames), strStart)
	cur := strStart
	copy(data[cur:], "Create\x00")
	cur += uint32(len("Create") + 1)
	for i, piece := range pieces {
		put(int(offPieceNames)+i*4, cur)
		copy(data[cur:], piece+"\x00")
		cur += uint32(len(piece) + 1)
	}
	return data
}
