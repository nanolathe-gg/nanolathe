package session

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
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

func TestCompositionCreationInitializesEconomyAccountBeforePublicationAndSlotReuse(t *testing.T) {
	root := t.TempDir()
	writeCompositionModel(t, root, "fixture", 1)
	writeCompositionCOB(t, root, "testunit", []string{"modelroot", "modelchild"})
	fs := vfs.New()
	if err := fs.MountDirectory(root, 10); err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "testunit"}, UnitName: "testunit", ObjectName: "fixture", MaxDamage: 10, Limit: -1, BMCode: true}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{def.CanonicalKey: def}}
	w, err := newSlicedWorldWithCOB(cat, fs)
	if err != nil {
		t.Fatal(err)
	}
	s := &Session{Catalog: cat, World: minimalTerrain(), Mission: syntheticMission(), Units: w, Econ: &economy.Service{}}
	s.InitBattleWindForSession()
	if err := createAndBindServicesForTest(t, s); err != nil {
		t.Fatalf("createAndBindServices: %v", err)
	}
	var observedSaveable bool
	w.OnCreate = func(h pool.Handle, _ *units.Unit) {
		_, err := s.Econ.RetailUnitAccountImage(h)
		observedSaveable = err == nil
	}
	h, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	if !observedSaveable {
		t.Fatal("OnCreate observed no saveable zero economy account")
	}
	if !s.Econ.RestoreUnitEconomy(h, [2]economy.Bucket{{Production: 7}}, [2]economy.ArchivedBucket{{Requested: 9}}) {
		t.Fatal("seed account state")
	}
	w.FreeImmediate(h)
	reused, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("reused create: %v", err)
	}
	if reused != h {
		t.Fatalf("reused handle = %d, want %d", reused, h)
	}
	image, err := s.Econ.RetailUnitAccountImage(reused)
	if err != nil {
		t.Fatalf("reused account image: %v", err)
	}
	for i, b := range image {
		if b != 0 {
			t.Fatalf("reused account byte %d = %#x, want zero", i, b)
		}
	}
}

func TestSessionCOBCreateReceivesQueriesAndActivationContext(t *testing.T) {
	root := t.TempDir()
	writeCompositionModel(t, root, "fixture", 1)
	x, z := numeric.FixedFromInt(3), numeric.FixedFromInt(4)
	packed := uint32(uint16(int16(x>>16)))<<16 | uint32(uint16(int16(z>>16)))
	code := []uint32{
		0x10021001, 16, 0x10021001, packed, 0x10021001, 0, 0x10021001, 0, 0x10021001, 0, 0x10043000, 0x10023002, 1,
		0x10021001, 5, 0x10021001, 1, 0x10082000,
		0x10021001, 9, 0x10021001, 1, 0x10021001, 0, 0x10021001, 0, 0x10021001, 0, 0x10043000, 0x10023002, 2,
		0x10021001, 6, 0x10021001, 1, 0x10082000,
		0x10021001, 1, 0x10021001, 1, 0x10082000, 0x10021001, 1, 0x10021001, 0, 0x10082000, 0x10065000,
		0x10021001, 19, 0x10021001, 1, 0x10082000, 0x10065000,
		0x10021001, 20, 0x10021001, 1, 0x10082000, 0x10065000,
	}
	writeCompositionCOBProgram(t, root, "testunit", code, []string{"Create", "Activate", "Deactivate"}, []uint32{0, 47, 53}, []string{"modelroot", "modelchild"})
	fs := vfs.New()
	if err := fs.MountDirectory(root, 10); err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "testunit"}, UnitName: "testunit", ObjectName: "fixture", MaxDamage: 10, Limit: -1, BMCode: true}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{def.CanonicalKey: def}}
	w, err := newSlicedWorldWithCOB(cat, fs)
	if err != nil {
		t.Fatal(err)
	}
	s := &Session{Catalog: cat, World: minimalTerrain(), Mission: syntheticMission(), Units: w, Econ: &economy.Service{}}
	s.InitBattleWindForSession()
	if err := createAndBindServicesForTest(t, s); err != nil {
		t.Fatal(err)
	}
	h, err := w.Create(def, 0, x, 0, z)
	if err != nil {
		t.Fatal(err)
	}
	u := w.Unit(h)
	if u == nil || !u.InBuildStance || !u.Busy || u.Activated || !u.BuggerOff || !u.Armored {
		t.Fatalf("Create context result = %#v", u)
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
	writeCompositionCOBProgram(t, root, name, []uint32{0x10065000}, []string{"Create"}, []uint32{0}, pieces)
}

func writeCompositionCOBProgram(t *testing.T, root, name string, code []uint32, scripts []string, indexes []uint32, pieces []string) {
	t.Helper()
	dir := filepath.Join(root, "scripts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".cob"), fixtureCOBBytes(code, scripts, indexes, pieces), 0o644); err != nil {
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

func fixtureCOBBytes(code []uint32, scripts []string, indexes []uint32, pieces []string) []byte {
	const header = 44
	nScripts, nPieces := len(scripts), len(pieces)
	offScriptIndex := uint32(header + len(code)*4)
	offScriptNames := offScriptIndex + uint32(nScripts*4)
	offPieceNames := offScriptNames + uint32(nScripts*4)
	strStart := offPieceNames + uint32(nPieces*4)
	stringsLen := 0
	for _, script := range scripts {
		stringsLen += len(script) + 1
	}
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
	for i, word := range code {
		put(header+i*4, word)
	}
	cur := strStart
	for i, script := range scripts {
		put(int(offScriptIndex)+i*4, indexes[i])
		put(int(offScriptNames)+i*4, cur)
		copy(data[cur:], script+"\x00")
		cur += uint32(len(script) + 1)
	}
	for i, piece := range pieces {
		put(int(offPieceNames)+i*4, cur)
		copy(data[cur:], piece+"\x00")
		cur += uint32(len(piece) + 1)
	}
	return data
}
