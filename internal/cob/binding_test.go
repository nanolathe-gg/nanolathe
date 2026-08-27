package cob

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe/nanolathe/vfs"
)

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
	if got, want := binding.PieceMap, []int{1, 0}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("piece map = %v want %v", got, want)
	}
	if !binding.CreateInvoked {
		t.Fatal("Create was not marked invoked")
	}
	if got := binding.VM.DrainCalls; got != 1 {
		t.Fatalf("Create mode-I barrier made %d drains, want exactly one", got)
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

func TestBindStrictReportsPieceAndEntryFailures(t *testing.T) {
	// COB declares more pieces than model provides — the strict count check is
	// that COB pieces must be a subset of model pieces, not strict equality;
	// model having extra unused pieces (e.g., ARMCOM 15 vs COB 14) is allowed
	// [d69ed97].
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
	if !bindingErr.Has(BindingPieceCount) || !bindingErr.Has(BindingUnresolvedPiece) || !bindingErr.Has(BindingMissingEntry) {
		t.Fatalf("diagnostics = %#v, want count/unresolved/missing-entry", bindingErr.Diagnostics)
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
