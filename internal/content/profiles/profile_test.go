package profiles_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content/profiles"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// markerFS answers Stat for a fixed marker set and nothing else. Detection
// reads no bytes, so this is the whole surface it needs.
type markerFS struct{ present map[string]bool }

func (m markerFS) Open(string) (vfs.File, error) { return nil, os.ErrNotExist }
func (m markerFS) ReadFileLimit(string, int64) ([]byte, error) {
	return nil, os.ErrNotExist
}
func (m markerFS) ReadDir(string) ([]vfs.EntryInfo, error) { return nil, os.ErrNotExist }
func (m markerFS) Stat(name string) (vfs.EntryInfo, error) {
	folded := strings.ToLower(name)
	if !m.present[folded] {
		return vfs.EntryInfo{}, os.ErrNotExist
	}
	return vfs.EntryInfo{Name: folded, Path: folded, IsDir: !strings.Contains(folded, ".")}, nil
}
func (m markerFS) CacheStamp(string) (string, error) { return "", os.ErrNotExist }

func markers(names ...string) markerFS {
	present := make(map[string]bool, len(names))
	for _, name := range names {
		present[strings.ToLower(name)] = true
	}
	return markerFS{present: present}
}

// TestShippedProfilesCarryTheInventoryTables locks the four shipped tables.
// The directory names are the ones the content sets' own archives and
// configuration files spell; the limits are the values their `.ini` files
// declare, except the two read caps, which are Nanolathe host caps.
func TestShippedProfilesCarryTheInventoryTables(t *testing.T) {
	want := map[string]struct {
		markers     []string
		directories map[string]string
		limits      profiles.Limits
	}{
		"retail": {
			limits: profiles.Limits{Units: 512, Weapons: 256, TNTBytes: 16 << 20, LOSBytes: 1 << 20, UnitLimit: 250, SearchEntries: 1333},
		},
		"escalation": {
			markers: []string{"aE", "downloadsE", "gamedatE", "guiE", "unitpicE", "unitsE", "weaponE"},
			directories: map[string]string{
				"units": "unitsE", "weapons": "weaponE", "gamedata": "gamedatE",
				"guis": "guiE", "unitpics": "unitpicE", "download": "downloadsE", "ai": "aE",
			},
			limits: profiles.Limits{Units: 16000, Weapons: 16000, TNTBytes: 64 << 20, LOSBytes: 8 << 20, UnitLimit: 1000, SearchEntries: 66650},
		},
		"prota": {
			markers: []string{"downloadP", "gamedatP", "guiP", "unitpicsP", "weaponP"},
			directories: map[string]string{
				"weapons": "weaponP", "gamedata": "gamedatP", "guis": "guiP",
				"unitpics": "unitpicsP", "download": "downloadP",
			},
			limits: profiles.Limits{Units: 16000, Weapons: 16000, TNTBytes: 64 << 20, LOSBytes: 8 << 20, UnitLimit: 1500, SearchEntries: 66650},
		},
		"zero": {
			markers: []string{"ZBuildMenu", "ZGameDat", "ZGui", "ZI", "ZUnitPic", "ZUnits", "ZWeapon"},
			directories: map[string]string{
				"units": "ZUnits", "weapons": "ZWeapon", "gamedata": "ZGameDat",
				"guis": "ZGui", "unitpics": "ZUnitPic", "download": "ZBuildMenu", "ai": "ZI", "music": "tamus",
			},
			limits: profiles.Limits{Units: 16000, Weapons: 16000, TNTBytes: 64 << 20, LOSBytes: 8 << 20, UnitLimit: 1500, SearchEntries: 66650},
		},
	}
	names := profiles.Names()
	if len(names) != len(want) {
		t.Fatalf("shipped profiles = %v, want one row per expected profile", names)
	}
	for _, name := range names {
		expected, ok := want[name]
		if !ok {
			t.Fatalf("unexpected shipped profile %q", name)
		}
		profile, err := profiles.Lookup(name)
		if err != nil {
			t.Fatalf("look up %q: %v", name, err)
		}
		if strings.Join(profile.Detect, ",") != strings.Join(expected.markers, ",") {
			t.Errorf("%s markers = %v, want %v", name, profile.Detect, expected.markers)
		}
		if len(profile.Directories) != len(expected.directories) {
			t.Errorf("%s directory table = %v, want %v", name, profile.Directories, expected.directories)
		}
		for retail, target := range expected.directories {
			if profile.Directories[retail] != target {
				t.Errorf("%s directory %s = %q, want %q", name, retail, profile.Directories[retail], target)
			}
		}
		if profile.Limits != expected.limits {
			t.Errorf("%s limits = %+v, want %+v", name, profile.Limits, expected.limits)
		}
	}
	// Keep the public preset list stable, with the fallback last.
	if names[len(names)-1] != profiles.RetailName {
		t.Fatalf("detection order = %v, want %s last", names, profiles.RetailName)
	}
}

// Detection follows complete logical trees, even after an archive is renamed.
// An unrelated or incomplete tree must not accidentally select a preset.
func TestDetectionRequiresOneCompleteLayout(t *testing.T) {
	for _, name := range []string{"retail", "escalation", "prota", "zero"} {
		t.Run(name, func(t *testing.T) {
			preset, err := profiles.Lookup(name)
			if err != nil {
				t.Fatal(err)
			}
			profile, err := profiles.Resolve(markers(preset.Detect...), "")
			if err != nil || profile.Name != name {
				t.Fatalf("Resolve = %q, %v; want %s", profile.Name, err, name)
			}
			for i := range preset.Detect {
				partial := append([]string(nil), preset.Detect[:i]...)
				partial = append(partial, preset.Detect[i+1:]...)
				profile, err := profiles.Detect(markers(partial...))
				if err != nil || profile.Name != "retail" {
					t.Fatalf("without %s: Detect = %q, %v; want retail", preset.Detect[i], profile.Name, err)
				}
			}
		})
	}
	profile, err := profiles.Detect(markers("TAESC.gp3", "unitsE"))
	if err != nil || profile.Name != "retail" {
		t.Fatalf("archive filename and one tree selected incomplete layout: %q, %v", profile.Name, err)
	}
}

func TestDetectionRejectsAmbiguousLayoutsUnlessExplicitlySelected(t *testing.T) {
	escalation, _ := profiles.Lookup("escalation")
	zero, _ := profiles.Lookup("zero")
	mounted := markers(append(escalation.Detect, zero.Detect...)...)
	if _, err := profiles.Resolve(mounted, ""); err == nil || !strings.Contains(err.Error(), "content layout is ambiguous") || !strings.Contains(err.Error(), "escalation, zero") {
		t.Fatalf("ambiguous resolution = %v", err)
	}
	profile, err := profiles.Resolve(mounted, "zero")
	if err != nil || profile.Name != "zero" {
		t.Fatalf("explicit resolution = %q, %v", profile.Name, err)
	}
}

// TestResolvePrefersTheExplicitSelector covers the override path, including a
// user-authored profile read from a file, and the rejection a typo produces.
func TestResolvePrefersTheExplicitSelector(t *testing.T) {
	preset, _ := profiles.Lookup("escalation")
	mounted := markers(preset.Detect...)
	profile, err := profiles.Resolve(mounted, "  Zero ")
	if err != nil {
		t.Fatalf("Resolve by name: %v", err)
	}
	if profile.Name != "zero" {
		t.Fatalf("explicit selector resolved to %q, want zero", profile.Name)
	}
	if profile, err = profiles.Resolve(mounted, ""); err != nil || profile.Name != "escalation" {
		t.Fatalf("empty selector resolved to %q (%v), want the detected escalation", profile.Name, err)
	}

	path := filepath.Join(t.TempDir(), "house.json")
	if err := os.WriteFile(path, []byte(`{"name":"House","detect":["HOUSE.gp3"],"layout":{"units":"houseUnits"},"limits":{"units":900}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	authored, err := profiles.Resolve(mounted, path)
	if err != nil {
		t.Fatalf("Resolve by path: %v", err)
	}
	if authored.Name != "house" || authored.Directories["units"] != "houseUnits" || authored.Limits.Units != 900 {
		t.Fatalf("authored profile = %+v", authored)
	}

	if _, err := profiles.Lookup("escalatoin"); err == nil {
		t.Fatal("a misspelled profile name was accepted")
	} else if !strings.Contains(err.Error(), "providers searched [escalation, prota, zero, retail]") {
		t.Fatalf("rejection %q does not name the shipped profiles", err)
	}
}

// TestAuthoredProfileRejectsShapesNoLoaderCouldUse keeps a hand-written
// profile from silently doing nothing.
func TestAuthoredProfileRejectsShapesNoLoaderCouldUse(t *testing.T) {
	dir := t.TempDir()
	for _, tt := range []struct{ name, body, want string }{
		{name: "nameless", body: `{"detect":[],"layout":{}}`, want: "has no name"},
		{name: "unknown field", body: `{"name":"x","limits":{},"directories":{}}`, want: "reading content profile failed"},
		{name: "nested directory", body: `{"name":"x","layout":{"units":"mod/units"}}`, want: "not a single directory"},
		{name: "empty row", body: `{"name":"x","layout":{"units":""}}`, want: "row is empty"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(dir, tt.name+".json")
			if err := os.WriteFile(path, []byte(tt.body), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := profiles.Lookup(path)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Lookup error = %v, want one containing %q", err, tt.want)
			}
		})
	}
}

// The optional recommendations are the fallback for a mod whose metadata
// names none (docs/DESIGN_MODS_MUTATORS.md §4.3); ProTA and Zero ship them, and
// an unknown value is refused like any other malformed profile.
func TestProfileRecommendations(t *testing.T) {
	for _, name := range profiles.Names() {
		profile, err := profiles.Lookup(name)
		if err != nil {
			t.Fatal(err)
		}
		wantControls, wantMinimum := "", ""
		if name == "prota" {
			wantControls, wantMinimum = "community", "community-3.9"
		} else if name == "zero" {
			wantControls, wantMinimum = "zero", "community-3.9"
		}
		if name == "escalation" {
			wantMinimum = "community-3.9"
		}
		if profile.Controls != wantControls || profile.MinimumGameplay != wantMinimum {
			t.Errorf("%s recommends controls %q, minimum %q", name, profile.Controls, profile.MinimumGameplay)
		}
	}
	dir := t.TempDir()
	for field, body := range map[string]string{
		"controls":        `{"name":"x","controls":"fancy"}`,
		"minimumGameplay": `{"name":"x","minimumGameplay":"strict"}`,
	} {
		path := filepath.Join(dir, field+".json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := profiles.Lookup(path); err == nil || !strings.Contains(err.Error(), field) {
			t.Errorf("an invalid %s = %v, want it refused and named", field, err)
		}
	}
}
