package content

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// A broken override for one side must not prevent the other side's opening
// bundle from compiling. The broken definition keeps its record and its
// winning provider but carries no program, which is what refuses it at unit
// creation [04 R-COB-04 §8], under DESIGN_CONTENT_VFS §3.4 C9's host
// admission policy.
func TestBrokenScriptOverrideDoesNotBlockUnrelatedBattle(t *testing.T) {
	fs := vfs.New()
	defer fs.Close()
	if err := fs.MountGameDirectory(testsupport.RetailRoot(t)); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := vfs.WriteArchive(&buf, []vfs.ArchiveFile{{Path: "scripts/corcom.cob", Data: []byte("authored invalid COB")}}, vfs.ArchiveWriteOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.MountArchiveReader("broken-script-mod.ufo", bytes.NewReader(buf.Bytes()), int64(buf.Len()), 1000, vfs.ArchiveOptions{}); err != nil {
		t.Fatal(err)
	}
	cat, err := Compile(fs)
	if err != nil {
		t.Fatal(err)
	}
	bad, ok := cat.Unit("corcom")
	if !ok || bad.Script != nil || bad.ScriptProvenance.ProviderID() != "broken-script-mod.ufo" {
		t.Fatal("unavailable program lost its definition or winning provider")
	}
	warned := false
	for _, warning := range cat.Warnings {
		warned = warned || strings.Contains(warning, "logical path scripts/corcom.cob, providers searched [broken-script-mod.ufo")
	}
	if !warned {
		t.Fatalf("missing script warning: %v", cat.Warnings)
	}
	// The unrelated side's own commander is unaffected: it keeps a program,
	// so a unit created from it binds normally.
	if len(cat.Sides) < 2 || cat.Sides[0] == nil || cat.Sides[1] == nil {
		t.Fatal("compiled corpus lost its side records")
	}
	other, ok := cat.Unit(cat.Sides[0].Commander)
	if !ok || other == nil || other.Script == nil {
		t.Fatalf("unrelated commander %q lost its program", cat.Sides[0].Commander)
	}
	// The overridden side's commander is the one the override reached, and it
	// is the definition with no program to bind.
	overridden, ok := cat.Unit(cat.Sides[1].Commander)
	if !ok || overridden == nil || overridden.Script != nil {
		t.Fatalf("overridden commander %q kept a program from a malformed file", cat.Sides[1].Commander)
	}
	if overridden.CanonicalKey != bad.CanonicalKey {
		t.Fatalf("the broken override reached %q, not the overridden side's commander %q", bad.CanonicalKey, overridden.CanonicalKey)
	}
}
