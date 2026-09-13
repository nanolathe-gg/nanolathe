package vfs_test

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/vfs"
)

// Root precedence is Nanolathe host policy, including archive-over-loose
// resolution across roots (docs/DESIGN_CONTENT_VFS.md §5).
func TestOrderedRootsOverrideWholeEarlierRoot(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	archive := func(root, name, data string) {
		t.Helper()
		var buf bytes.Buffer
		if err := vfs.WriteArchive(&buf, []vfs.ArchiveFile{{Path: "shared.txt", Data: []byte(data)}}, vfs.ArchiveWriteOptions{}); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), buf.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
	}
	archive(first, "totala1.hpi", "first base")
	archive(first, "patch.ccx", "first expansion")
	archive(first, "rev31.gp3", "first patch")
	archive(second, "totala1.hpi", "second base")
	if err := os.WriteFile(filepath.Join(first, "shared.txt"), []byte("first loose"), 0600); err != nil {
		t.Fatal(err)
	}
	fs := vfs.New()
	defer fs.Close()
	if err := fs.MountGameDirectories([]string{first, second}); err != nil {
		t.Fatal(err)
	}
	data, err := fs.ReadFile("shared.txt")
	if err != nil || string(data) != "second base" {
		t.Fatalf("winner = %q, %v", data, err)
	}
	sources := fs.Sources("shared.txt")
	want := []string{filepath.Join(second, "totala1.hpi"), filepath.Join(first, "shared.txt"), filepath.Join(first, "rev31.gp3"), filepath.Join(first, "patch.ccx"), filepath.Join(first, "totala1.hpi")}
	var got []string
	for _, source := range sources {
		got = append(got, source.Source.SourcePath)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sources = %v, want %v", got, want)
	}
	// The same ordering must reach enumeration and the manifest.
	entries, err := fs.ReadDir("")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name == "shared.txt" && entry.Source.SourcePath != want[0] {
			t.Fatal("enumeration lost root priority")
		}
	}
	records, err := fs.Manifest(vfs.ManifestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if record.LogicalPath == "shared.txt" && (record.ProviderID != "totala1.hpi" || len(record.Shadowed) != 4) {
			t.Fatalf("manifest = %+v", record)
		}
	}
	reversed := vfs.New()
	defer reversed.Close()
	if err := reversed.MountGameDirectories([]string{second, first}); err != nil {
		t.Fatal(err)
	}
	data, err = reversed.ReadFile("shared.txt")
	if err != nil || string(data) != "first loose" {
		t.Fatalf("reversed winner = %q, %v", data, err)
	}
}

func TestOneRootPreservesExistingManifest(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "x"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	original, layered := vfs.New(), vfs.New()
	defer original.Close()
	defer layered.Close()
	if err := original.MountGameDirectory(root); err != nil {
		t.Fatal(err)
	}
	if err := layered.MountGameDirectories([]string{root, root}); err != nil {
		t.Fatal(err)
	}
	a, err := original.ManifestHash()
	if err != nil {
		t.Fatal(err)
	}
	b, err := layered.ManifestHash()
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatal("single/repeated root changed manifest identity")
	}
}
