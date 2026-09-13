package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/vfs"
)

func TestRootFlagsKeepLoadOrder(t *testing.T) {
	t.Setenv("NANOLATHE_TA_ROOT", "/unused/environment/root")
	opts, err := parseFlags([]string{"--root", "base", "--root=mod"}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Root != "base" || !reflect.DeepEqual(opts.Roots, []string{"base", "mod"}) {
		t.Fatalf("roots = %q / %v", opts.Root, opts.Roots)
	}
	if _, err := parseFlags([]string{"--root="}, &bytes.Buffer{}); err == nil {
		t.Fatal("empty root accepted")
	}
}

func TestRestartKeepsAllContentRoots(t *testing.T) {
	base, mod := t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(base, "gamedata"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"moveinfo.tdf", "sidedata.tdf"} {
		if err := os.WriteFile(filepath.Join(base, "gamedata", name), []byte("[TEST] { value=base; }"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	var archive bytes.Buffer
	if err := vfs.WriteArchive(&archive, []vfs.ArchiveFile{{Path: "gamedata/sidedata.tdf", Data: []byte("[TEST] { value=mod; }")}}, vfs.ArchiveWriteOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "totala1.hpi"), archive.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	cs, err := openContent(Options{Roots: []string{base, mod}})
	if err != nil {
		t.Fatal(err)
	}
	shell := &gameShell{cs: cs, opts: Options{Root: t.TempDir()}}
	defer func() { shell.cs.Close() }()
	if !shell.prepareBattleRestartContent() {
		t.Fatal("restart remount failed")
	}
	data, err := shell.cs.fs.ReadFile("gamedata/sidedata.tdf")
	if err != nil || string(data) != "[TEST] { value=mod; }" {
		t.Fatalf("restart lost mod: %q, %v", data, err)
	}
	if shell.cs.root != base || !reflect.DeepEqual(shell.cs.roots, []string{base, mod}) {
		t.Fatalf("restart roots = %v", shell.cs.roots)
	}
}

func TestRemasterWinsAboveManyRoots(t *testing.T) {
	var roots []string
	for i := 0; i < 12; i++ {
		roots = append(roots, t.TempDir())
	}
	if err := os.WriteFile(filepath.Join(roots[len(roots)-1], "art"), []byte("root"), 0600); err != nil {
		t.Fatal(err)
	}
	remaster := t.TempDir()
	if err := os.WriteFile(filepath.Join(remaster, "art"), []byte("remaster"), 0600); err != nil {
		t.Fatal(err)
	}
	fs := vfs.New()
	defer fs.Close()
	if err := fs.MountGameDirectories(roots); err != nil {
		t.Fatal(err)
	}
	if err := mountRemaster(fs, remaster); err != nil {
		t.Fatal(err)
	}
	got, err := fs.ReadFile("art")
	if err != nil || string(got) != "remaster" {
		t.Fatalf("art = %q, %v", got, err)
	}
}
