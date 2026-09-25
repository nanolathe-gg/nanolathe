package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/modlibrary"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

// installHeadlessFixtureMod installs an authored one-file mod into the
// scratch library isolateHostFiles points at, without a content check.
func installHeadlessFixtureMod(t *testing.T, meta modlibrary.Metadata) modlibrary.Mod {
	t.Helper()
	dir := filepath.Join(t.TempDir(), meta.ID)
	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{modlibrary.MetadataFile: string(data), "units/fixture.fbi": "[UNITINFO] { }\n"} {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	root, err := modlibrary.DefaultRoot()
	if err != nil {
		t.Fatal(err)
	}
	lib, err := modlibrary.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	mod, err := lib.InstallDirectory(dir, modlibrary.InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return mod
}

// TestParseModMountsTheInstalledMod locks --mod for the displayless command
// (docs/DESIGN_MODS_MUTATORS.md §4.3): the installed mod is mounted as the
// last root with its own content profile, which beats the saved preference;
// the settings file's saved mod is never read; `none` mounts nothing; and a
// mod beside a manual root stack, an absent mod, a gameplay mode below the
// mod's minimum and the fixed benchmark scene are refused with the standard
// diagnostic.
func TestParseModMountsTheInstalledMod(t *testing.T) {
	isolateHostFiles(t)
	base := t.TempDir()
	mod := installHeadlessFixtureMod(t, modlibrary.Metadata{Schema: 1, ID: "fixture", Name: "Fixture", Version: "2", ContentProfile: "prota", MinimumGameplay: "community-3.9"})
	stored := settings.Defaults()
	stored.ContentProfile, stored.Mod = "zero", settings.ModSelection{ID: mod.ID}
	if err := stored.Save(); err != nil {
		t.Fatal(err)
	}

	req, _, _, _, err := parse([]string{"--root", base, "--mod", "fixture"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(req.Roots, []string{base, mod.Dir}) || req.Root != base || req.Mod != "fixture@2" || req.ContentProfile != "prota" {
		t.Fatalf("--mod request roots %v root %q mod %q profile %q", req.Roots, req.Root, req.Mod, req.ContentProfile)
	}
	req, _, _, _, err = parse([]string{"--root", base, "--mod", "fixture@2", "--content-profile", "retail", "--gameplay", "modern"}, io.Discard)
	if err != nil || req.ContentProfile != "retail" {
		t.Fatalf("an explicit profile beside --mod = %q, %v", req.ContentProfile, err)
	}
	for _, args := range [][]string{{"--root", base}, {"--root", base, "--mod", "none"}} {
		req, _, _, _, err := parse(args, io.Discard)
		if err != nil || !reflect.DeepEqual(req.Roots, []string{base}) || req.Mod != "" || req.ContentProfile != "zero" {
			t.Fatalf("parse(%v) mounted %v, mod %q, profile %q: %v", args, req.Roots, req.Mod, req.ContentProfile, err)
		}
	}

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--root", base, "--root", t.TempDir(), "--mod", "fixture"}, "conflicts with a manual root stack"},
		{[]string{"--root", base, "--mod", "absent"}, "mod is not installed"},
		{[]string{"--root", base, "--mod", "fixture", "--gameplay", "strict-3.1"}, "below the mod's minimum"},
		{[]string{"--root", base, "--mod", "fixture", "--sim-benchmark", "/tmp/unused-benchmark"}, "simulation-cost benchmark"},
	} {
		if _, _, _, _, err := parse(tc.args, io.Discard); err == nil || !strings.HasPrefix(err.Error(), "nanolathe: ") || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("parse(%v) = %v, want a nanolathe diagnostic containing %q", tc.args, err, tc.want)
		}
	}
	// Several roots with no mod stay a manual stack.
	if _, _, _, _, err := parse([]string{"--root", base, "--root", t.TempDir(), "--mod", "none"}, io.Discard); err != nil {
		t.Fatalf("--mod none beside a manual stack: %v", err)
	}
}
