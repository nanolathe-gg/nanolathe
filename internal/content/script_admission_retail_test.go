package content

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// A broken override for the other side must not prevent this side's opening
// bundle from loading. Required use still refuses [04 R-COB-04 §8], under
// DESIGN_CONTENT_VFS §3.4 C9's host admission policy.
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
	if manifest, err := PreflightSkirmish(fs, cat, "Ashap Plateau", 0); err != nil {
		t.Fatalf("unrelated Arm bundle refused: %v, %+v", err, manifest.Diagnostics)
	}
	manifest, err := PreflightSkirmish(fs, cat, "Ashap Plateau", 1)
	if err == nil || !manifest.Fatal() {
		t.Fatal("Core opening admitted its broken required script")
	}
	for _, d := range manifest.Diagnostics {
		if d.Fatal && d.Logical == "scripts/corcom.cob" {
			return
		}
	}
	t.Fatal("required refusal did not identify the broken COB")
}
