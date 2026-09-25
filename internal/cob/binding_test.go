package cob

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/vfs"
)

type countingBindingFS struct {
	vfs.FSOps
	reads int
}

func (fs *countingBindingFS) ReadFileLimit(name string, limit int64) ([]byte, error) {
	fs.reads++
	return fs.FSOps.ReadFileLimit(name, limit)
}

func bindingFS(t *testing.T, data []byte) *vfs.FS {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if data != nil {
		if err := os.WriteFile(filepath.Join(root, "scripts", "testunit.cob"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fs := vfs.New()
	if err := fs.MountDirectory(root, 100); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	return fs
}

func TestBindStrictLinksPiecesAndRunsCreateOnce(t *testing.T) {
	fs := bindingFS(t, makeCOB([]uint32{0x10065000}, []string{"Create", "FirePrimary"}, []uint32{0, 0}, []string{"barrel", "base"}))
	binding, err := BindStrict(fs, BindingRequest{
		UnitName:        "TestUnit",
		ModelPieces:     []string{"base", "barrel"},
		RequiredScripts: []string{"Create", "FirePrimary"},
	})
	if err != nil {
		t.Fatalf("BindStrict: %v", err)
	}
	if binding.ScriptPath != "scripts/testunit.cob" {
		t.Fatalf("script path = %q", binding.ScriptPath)
	}
	if binding.Provider.ProviderID() == "" {
		t.Fatal("fallback VFS binding lost the winning provider")
	}
	if got, want := binding.PieceMap, []int{1, 0}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("piece map = %v want %v", got, want)
	}
	if !binding.CreateInvoked {
		t.Fatal("Create was not marked invoked")
	}
	if got := binding.VM.DrainCalls; got != 1 {
		t.Fatalf("Create D+wake barrier made %d drains, want exactly one", got)
	}
}

func TestBindStrictAllowsProgramWithoutCreate(t *testing.T) {
	fs := bindingFS(t, makeCOB([]uint32{0x10065000}, []string{"Killed"}, []uint32{0}, []string{"base"}))
	binding, err := BindStrict(fs, BindingRequest{UnitName: "TestUnit", ModelPieces: []string{"base"}})
	if err != nil {
		t.Fatalf("BindStrict without Create: %v", err)
	}
	if binding.CreateInvoked || binding.VM.ActiveThreadCount() != 0 {
		t.Fatalf("missing Create binding invoked=%v active=%d", binding.CreateInvoked, binding.VM.ActiveThreadCount())
	}
}

// Duplicate model names bind to the first unclaimed match, including case
// variants [02 R-MALF-01 §2][04 R-COB-01 §4]. Rejecting the hierarchy prevents
// stock ARMCH allocation.
func TestBindStrictDuplicateModelPiecesUseFirstMatch(t *testing.T) {
	fs := bindingFS(t, makeCOB([]uint32{0x10065000}, []string{"Create"}, []uint32{0}, []string{"beam", "base"}))
	binding, err := BindStrict(fs, BindingRequest{UnitName: "TestUnit", ModelPieces: []string{"base", "BEAM", "beam"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := binding.PieceMap; len(got) != 2 || got[0] != 1 || got[1] != 0 {
		t.Fatalf("piece map = %v, want [1 0]", got)
	}
}

func TestBindStrictInstantiatesCatalogProgramWithoutVFSReadAndKeepsVMsIndependent(t *testing.T) {
	data := makeCOB([]uint32{0x10065000}, []string{"Create"}, []uint32{0}, []string{"base"})
	base := bindingFS(t, data)
	fs := &countingBindingFS{FSOps: base}
	program, err := Load(data)
	if err != nil {
		t.Fatal(err)
	}
	info, err := base.Stat("scripts/testunit.cob")
	if err != nil {
		t.Fatal(err)
	}
	req := BindingRequest{UnitName: "TestUnit", ModelPieces: []string{"base"}, Program: program, ProgramProvenance: info.Source}
	first, err := BindStrict(fs, req)
	if err != nil {
		t.Fatalf("first catalog binding: %v", err)
	}
	second, err := BindStrict(fs, req)
	if err != nil {
		t.Fatalf("second catalog binding: %v", err)
	}
	if fs.reads != 0 {
		t.Fatalf("catalog program binding reads = %d, want 0", fs.reads)
	}
	if first.Program != program || second.Program != program || first.VM == second.VM {
		t.Fatalf("program/VM ownership first=%p second=%p program=%p", first.VM, second.VM, program)
	}
	first.VM.Pieces[0].Trans[0] = 77
	if got := second.VM.Pieces[0].Trans[0]; got != 0 {
		t.Fatalf("second VM observed first VM animation = %d", got)
	}
}

func TestBindStrictCatalogProgramsRetainSeparateProviders(t *testing.T) {
	dataA := makeCOB([]uint32{0x10065000}, []string{"Create"}, []uint32{0}, []string{"base"})
	dataB := makeCOB([]uint32{0x10065000, 0x10065000}, []string{"Create"}, []uint32{0}, []string{"base"})
	fsA, fsB := bindingFS(t, dataA), bindingFS(t, dataB)
	programA, err := Load(dataA)
	if err != nil {
		t.Fatal(err)
	}
	programB, err := Load(dataB)
	if err != nil {
		t.Fatal(err)
	}
	infoA, err := fsA.Stat("scripts/testunit.cob")
	if err != nil {
		t.Fatal(err)
	}
	infoB, err := fsB.Stat("scripts/testunit.cob")
	if err != nil {
		t.Fatal(err)
	}
	bindingA, err := BindStrict(nil, BindingRequest{UnitName: "TestUnit", ModelPieces: []string{"base"}, Program: programA, ProgramProvenance: infoA.Source})
	if err != nil {
		t.Fatal(err)
	}
	bindingB, err := BindStrict(nil, BindingRequest{UnitName: "TestUnit", ModelPieces: []string{"base"}, Program: programB, ProgramProvenance: infoB.Source})
	if err != nil {
		t.Fatal(err)
	}
	if bindingA.Program == bindingB.Program || bindingA.Provider.SourcePath == bindingB.Provider.SourcePath || len(bindingA.Program.Code) == len(bindingB.Program.Code) {
		t.Fatalf("catalog bindings crossed providers: A=%#v B=%#v", bindingA.Provider, bindingB.Provider)
	}
}

func BenchmarkBindStrictCatalogProgram(b *testing.B) {
	program, err := Load(makeCOB([]uint32{0x10065000}, []string{"Create"}, []uint32{0}, []string{"base"}))
	if err != nil {
		b.Fatal(err)
	}
	req := BindingRequest{UnitName: "TestUnit", ModelPieces: []string{"base"}, Program: program}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := BindStrict(nil, req); err != nil {
			b.Fatal(err)
		}
	}
}

func TestBindStrictReportsMissingAndMalformedCOB(t *testing.T) {
	missing := bindingFS(t, nil)
	_, err := BindStrict(missing, BindingRequest{UnitName: "TestUnit", ModelPieces: []string{"base"}})
	bindingErr, ok := err.(*BindingError)
	if !ok || !bindingErr.Has(BindingMissingCOB) {
		t.Fatalf("missing COB error = %#v, want BindingMissingCOB", err)
	}

	malformed := bindingFS(t, []byte("not a COB"))
	_, err = BindStrict(malformed, BindingRequest{UnitName: "TestUnit", ModelPieces: []string{"base"}})
	bindingErr, ok = err.(*BindingError)
	if !ok || !bindingErr.Has(BindingMalformedCOB) {
		t.Fatalf("malformed COB error = %#v, want BindingMalformedCOB", err)
	}
	if err.Error() == "" {
		t.Fatal("malformed diagnostic has empty error text")
	}
}

// Piece-name mismatches never refuse a bind: retail's link pass has no
// refusal, so only the entry-point failure is reported, and the same program
// without that requirement binds with the two pieces described as link notes
// [04 R-COB-01 §4].
func TestBindStrictReportsEntryFailureButLinksUnmatchedPieces(t *testing.T) {
	fs := bindingFS(t, makeCOB([]uint32{0x10065000}, []string{"Create"}, []uint32{0, 0}, []string{"barrel", "extra"}))
	_, err := BindStrict(fs, BindingRequest{
		UnitName:        "TestUnit",
		ModelPieces:     []string{"base"},
		RequiredScripts: []string{"Create", "FirePrimary"},
	})
	bindingErr, ok := err.(*BindingError)
	if !ok {
		t.Fatalf("error = %T %v, want BindingError", err, err)
	}
	if !bindingErr.Has(BindingMissingEntry) || bindingErr.Has(BindingPieceCount) || bindingErr.Has(BindingUnresolvedPiece) {
		t.Fatalf("diagnostics = %#v, want only missing-entry", bindingErr.Diagnostics)
	}

	binding, err := BindStrict(fs, BindingRequest{UnitName: "TestUnit", ModelPieces: []string{"base"}})
	if err != nil {
		t.Fatalf("unmatched pieces refused the bind: %v", err)
	}
	if got := binding.PieceMap; len(got) != 2 || got[0] != 0 || got[1] != -1 {
		t.Fatalf("piece map = %v, want [0 -1]: barrel takes slot 0's base, extra has no slot", got)
	}
	if len(binding.LinkNotes) != 2 || binding.LinkNotes[0].Code != BindingUnresolvedPiece || binding.LinkNotes[1].Code != BindingPieceCount {
		t.Fatalf("link notes = %#v, want unresolved then beyond-model", binding.LinkNotes)
	}
}

// The link pass permutes the model's piece slots in place, one script piece at
// a time, searching only from the script piece's own slot onward
// [04 R-COB-01 §4]. Each case locks one consequence of that order.
func TestLinkPiecesRetailSlotPass(t *testing.T) {
	cases := []struct {
		name          string
		script, model []string
		want          []int
	}{
		{"reorders by name", []string{"barrel", "base"}, []string{"base", "barrel"}, []int{1, 0}},
		{"ASCII case fold", []string{"TURRET"}, []string{"turret"}, []int{0}},
		{"no trimming", []string{"base "}, []string{"base", "x"}, []int{0}},
		// A missing name keeps the unclaimed piece already sitting in its
		// slot, including one an earlier swap moved there.
		{"missing name takes its slot", []string{"a", "missing", "b"}, []string{"b", "c", "a"}, []int{2, 1, 0}},
		// Trailing script pieces beyond the model have no record.
		{"beyond the model", []string{"base", "blastpt"}, []string{"base"}, []int{0, -1}},
		// A piece claimed by an earlier alias lies below the searching slot and
		// is not found again: y aliases slot 0's x, so x falls back to slot 1.
		{"claimed piece is not found again", []string{"y", "x"}, []string{"x", "z"}, []int{0, 1}},
		// Duplicate model names go to successive script entries of that name.
		{"duplicate model names", []string{"beam", "beam"}, []string{"base", "beam", "beam"}, []int{1, 2}},
		// A swap can carry the first duplicate past the second, so the second
		// is found first.
		{"swap reorders duplicates", []string{"a", "b"}, []string{"b", "b", "a"}, []int{2, 1}},
	}
	for _, tc := range cases {
		got := LinkPieces(tc.script, tc.model)
		if len(got) != len(tc.want) {
			t.Fatalf("%s: map = %v, want %v", tc.name, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("%s: map = %v, want %v", tc.name, got, tc.want)
			}
		}
	}
}

func TestBindStrictAlternativeEntryGroup(t *testing.T) {
	fs := bindingFS(t, makeCOB([]uint32{0x10065000}, []string{"Create"}, []uint32{0}, []string{"base"}))
	_, err := BindStrict(fs, BindingRequest{
		UnitName: "TestUnit", ModelPieces: []string{"base"},
		RequiredScriptGroups: [][]string{{"AimFromPrimary", "QueryPrimary"}},
	})
	bindingErr, ok := err.(*BindingError)
	if !ok || !bindingErr.Has(BindingMissingEntry) {
		t.Fatalf("missing alternatives error = %#v, want BindingMissingEntry", err)
	}
	if got := bindingErr.Diagnostics[0].Expected; got != "AimFromPrimary or QueryPrimary" {
		t.Fatalf("alternative expectation = %q", got)
	}

	fs = bindingFS(t, makeCOB([]uint32{0x10065000}, []string{"Create", "QueryPrimary"}, []uint32{0, 0}, []string{"base"}))
	binding, err := BindStrict(fs, BindingRequest{
		UnitName: "TestUnit", ModelPieces: []string{"base"},
		RequiredScriptGroups: [][]string{{"AimFromPrimary", "QueryPrimary"}},
	})
	if err != nil || binding == nil {
		t.Fatalf("Query fallback should satisfy alternative group: %v", err)
	}
}

func TestEmptyVMIsExplicit(t *testing.T) {
	vm := NewVM(&Program{})
	if vm == nil || vm.Program() == nil {
		t.Fatal("empty VM has no empty program")
	}
	if len(vm.Program().Pieces) != 0 || len(vm.Program().Scripts) != 0 {
		t.Fatalf("synthetic program = %#v, want no pieces/scripts", vm.Program())
	}
}
