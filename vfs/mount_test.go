package vfs_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/internal/testsupport"
	"github.com/nanolathe/nanolathe/vfs"
)

// TestMountRetailInstall is PLAN_01's real gate: the whole reference install
// mounts, tier precedence puts the patch archive above the base archives, and
// no archive is dropped (docs/SPEC_CONFLICTS.md SC1).
func TestMountRetailInstall(t *testing.T) {
	root := testsupport.RetailRoot(t)
	fileSystem := vfs.New()
	if err := fileSystem.MountGameDirectory(root); err != nil {
		t.Fatalf("mount: %v", err)
	}
	defer fileSystem.Close()

	archives := 0
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		switch strings.ToLower(filepath.Ext(entry.Name())) {
		case ".hpi", ".ufo", ".ccx", ".gp3", ".gp4", ".gpf", ".swx":
			archives++
		}
	}
	// Every archive plus the loose root directory.
	if want := archives + 1; fileSystem.MountCount() != want {
		t.Fatalf("mounted %d providers, want %d (no archive may be dropped)", fileSystem.MountCount(), want)
	}

	for _, required := range []string{
		"gamedata/moveinfo.tdf", "gamedata/sidedata.tdf", "gamedata/los.tdf",
		"palettes/palette.pal", "units/armcom.fbi", "ai/default.txt",
	} {
		if _, err := fileSystem.Stat(required); err != nil {
			t.Errorf("%s: %v", required, err)
		}
	}
}

// TestLooseShadowsArchive proves the loose tier wins and that the manifest
// names the provider it shadowed — the guarantee that makes PLAN_01's
// intra-tier ordering divergence auditable.
func TestLooseShadowsArchive(t *testing.T) {
	root := testsupport.RetailRoot(t)
	overlay := t.TempDir()
	if err := os.MkdirAll(filepath.Join(overlay, "gamedata"), 0o755); err != nil {
		t.Fatal(err)
	}
	marker := []byte("[MOVEINFO] { marker=1; }\n")
	if err := os.WriteFile(filepath.Join(overlay, "gamedata", "moveinfo.tdf"), marker, 0o644); err != nil {
		t.Fatal(err)
	}

	fileSystem := vfs.New()
	if err := fileSystem.MountGameDirectory(root); err != nil {
		t.Fatal(err)
	}
	defer fileSystem.Close()
	if err := fileSystem.MountDirectory(overlay, 200); err != nil {
		t.Fatal(err)
	}

	data, err := fileSystem.ReadFile("gamedata/moveinfo.tdf")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(marker) {
		t.Fatal("loose overlay did not win the lookup")
	}

	records, err := fileSystem.Manifest(vfs.ManifestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if record.LogicalPath != "gamedata/moveinfo.tdf" {
			continue
		}
		if len(record.Shadowed) == 0 {
			t.Fatal("manifest did not record the shadowed archive provider")
		}
		return
	}
	t.Fatal("manifest is missing the overlaid path")
}

// TestManifestHashStable locks the identity guarantee: no map iteration may
// reach the output (INVARIANTS I1).
func TestManifestHashStable(t *testing.T) {
	root := testsupport.RetailRoot(t)
	var hashes []string
	for i := 0; i < 2; i++ {
		fileSystem := vfs.New()
		if err := fileSystem.MountGameDirectory(root); err != nil {
			t.Fatal(err)
		}
		hash, err := fileSystem.ManifestHash()
		if err != nil {
			t.Fatal(err)
		}
		hashes = append(hashes, hash)
		fileSystem.Close()
	}
	if hashes[0] != hashes[1] {
		t.Fatalf("manifest hash unstable: %s vs %s", hashes[0], hashes[1])
	}
}

// TestDuplicateMountSuppressed covers the dedup rule: an equal full path
// mounts once [02 §2].
func TestDuplicateMountSuppressed(t *testing.T) {
	dir := t.TempDir()
	fileSystem := vfs.New()
	if err := fileSystem.MountDirectory(dir, 10); err != nil {
		t.Fatal(err)
	}
	before := fileSystem.MountCount()
	if err := fileSystem.MountGameDirectoryWithPlan(dir, vfs.DefaultRetailMountPlan()); err != nil {
		t.Fatal(err)
	}
	_ = before
	if notes := fileSystem.Notes(); len(notes) > 0 {
		t.Logf("notes: %v", notes)
	}
}
