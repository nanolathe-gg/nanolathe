package modlibrary

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nanolathe-gg/nanolathe/vfs"
)

// zipItem is one authored fixture entry. Every fixture is built here from
// literal text; no retail bytes are involved.
type zipItem struct {
	name string
	body string
	mode fs.FileMode // zero for a regular file; ModeDir or ModeSymlink otherwise
}

func writeZip(t *testing.T, dir, name string, items ...zipItem) string {
	t.Helper()
	full := filepath.Join(dir, name)
	file, err := os.Create(full)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for _, item := range items {
		header := &zip.FileHeader{Name: item.name, Method: zip.Deflate}
		switch {
		case item.mode&fs.ModeSymlink != 0:
			header.SetMode(fs.ModeSymlink | 0o777)
		case item.mode&fs.ModeDir != 0:
			header.SetMode(fs.ModeDir | 0o755)
		default:
			header.SetMode(0o644)
		}
		w, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(item.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return full
}

func metadataJSON(t *testing.T, meta Metadata) string {
	t.Helper()
	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func sampleMetadata() Metadata {
	return Metadata{Schema: 1, ID: "sample", Name: "Sample Mod", Version: "1.0"}
}

// openTestLibrary opens a library in a temporary directory with a clock that
// advances one minute per install, so install order is observable.
func openTestLibrary(t *testing.T) *Library {
	t.Helper()
	lib, err := Open(filepath.Join(t.TempDir(), "mods"))
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	lib.now = func() time.Time {
		clock = clock.Add(time.Minute)
		return clock
	}
	return lib
}

// assertNothingInstalled checks the library shows no mod, holds no id
// directory, and left nothing in staging.
func assertNothingInstalled(t *testing.T, lib *Library) {
	t.Helper()
	mods, err := lib.Installed()
	if err != nil {
		t.Fatal(err)
	}
	if len(mods) != 0 {
		t.Fatalf("installed = %v, want none", mods)
	}
	entries, err := os.ReadDir(lib.Root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != stagingName {
			t.Fatalf("library holds %s after a refused install", entry.Name())
		}
	}
	staged, err := os.ReadDir(lib.StagingDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(staged) != 0 {
		t.Fatalf("staging holds %d entries after a refused install", len(staged))
	}
}

func TestParseMetadata(t *testing.T) {
	valid := `{"schema":1,"id":"prota","name":"ProTA","version":"4.8","contentProfile":"prota","minimumGameplay":"community-3.9","controls":"community","requires":["maps/foo.tnt"]}`
	meta, err := ParseMetadata([]byte(valid))
	if err != nil {
		t.Fatalf("valid metadata refused: %v", err)
	}
	want := Metadata{Schema: 1, ID: "prota", Name: "ProTA", Version: "4.8", ContentProfile: "prota", MinimumGameplay: "community-3.9", Controls: "community", Requires: []string{"maps/foo.tnt"}}
	if !reflect.DeepEqual(meta, want) {
		t.Fatalf("parsed %+v, want %+v", meta, want)
	}
	if _, err := ParseMetadata([]byte(`{"schema":1,"id":"z","name":"Z","version":"a5","contentProfile":"profiles/z.json"}`)); err != nil {
		t.Fatalf("relative profile path refused: %v", err)
	}
	for name, doc := range map[string]string{
		"schema 2":          `{"schema":2,"id":"a","name":"A","version":"1"}`,
		"uppercase id":      `{"schema":1,"id":"ProTA","name":"A","version":"1"}`,
		"empty name":        `{"schema":1,"id":"a","name":" ","version":"1"}`,
		"empty version":     `{"schema":1,"id":"a","name":"A","version":""}`,
		"escaping version":  `{"schema":1,"id":"a","name":"A","version":".."}`,
		"separator version": `{"schema":1,"id":"a","name":"A","version":"1/2"}`,
		"unknown gameplay":  `{"schema":1,"id":"a","name":"A","version":"1","minimumGameplay":"turbo"}`,
		"unknown controls":  `{"schema":1,"id":"a","name":"A","version":"1","controls":"fancy"}`,
		"unknown field":     `{"schema":1,"id":"a","name":"A","version":"1","colour":"red"}`,
		"escaping profile":  `{"schema":1,"id":"a","name":"A","version":"1","contentProfile":"../p.json"}`,
		"escaping require":  `{"schema":1,"id":"a","name":"A","version":"1","requires":["../x"]}`,
		"trailing document": `{"schema":1,"id":"a","name":"A","version":"1"}{}`,
	} {
		if _, err := ParseMetadata([]byte(doc)); err == nil {
			t.Errorf("%s: accepted", name)
		} else if !strings.HasPrefix(err.Error(), "nanolathe: ") || !strings.Contains(err.Error(), "providers searched [") {
			t.Errorf("%s: diagnostic %q is not in the standard shape", name, err)
		}
	}
}

func TestParseSelector(t *testing.T) {
	for _, tc := range []struct{ in, id, version string }{
		{"prota", "prota", ""},
		{"prota@4.8", "prota", "4.8"},
		{"ProTA@4.8", "prota", "4.8"},
		{"none", "", ""},
		{" NONE ", "", ""},
	} {
		id, version, err := ParseSelector(tc.in)
		if err != nil || id != tc.id || version != tc.version {
			t.Errorf("ParseSelector(%q) = %q, %q, %v; want %q, %q", tc.in, id, version, err, tc.id, tc.version)
		}
	}
	for _, bad := range []string{"", "prota@", "@4.8", "pro ta", "prota@../x"} {
		if _, _, err := ParseSelector(bad); err == nil {
			t.Errorf("ParseSelector(%q) accepted", bad)
		}
	}
}

func TestDefaultRootFollowsXDG(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/data")
	if got, err := DefaultRoot(); err != nil || got != filepath.Join("/data", "nanolathe", "mods") {
		t.Fatalf("DefaultRoot with XDG_DATA_HOME = %q, %v", got, err)
	}
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("HOME", "/home/player")
	home, _ := os.UserHomeDir()
	if got, err := DefaultRoot(); err != nil || got != filepath.Join(home, ".local", "share", "nanolathe", "mods") {
		t.Fatalf("DefaultRoot without XDG_DATA_HOME = %q, %v", got, err)
	}
}

func TestOpenClearsStaging(t *testing.T) {
	root := filepath.Join(t.TempDir(), "mods")
	if err := os.MkdirAll(filepath.Join(root, stagingName, "extract-old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, stagingName, "a.zip.part"), []byte("partial"), 0o644); err != nil {
		t.Fatal(err)
	}
	lib, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(lib.StagingDir())
	if err != nil || len(entries) != 0 {
		t.Fatalf("staging after Open = %v, %v; want empty", entries, err)
	}
}

// Staging is cleared at most once per library root per process, and never
// while an install is extracting into it (DESIGN_MODS_MUTATORS §4.1): the
// desktop command opens the library on every mount and every Mods screen, so
// a later Open must leave an install in progress alone.
func TestOpenClearsStagingOnlyOncePerRootAndNeverUnderAnInstall(t *testing.T) {
	root := filepath.Join(t.TempDir(), "mods")
	lib, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	busy := filepath.Join(lib.StagingDir(), "extract-busy")
	if err := os.MkdirAll(busy, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(root + string(filepath.Separator)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(busy); err != nil {
		t.Fatalf("a second Open of the same root cleared staging: %v", err)
	}

	// A root first opened while one of this process's installs is extracting
	// into it keeps its staging until no install is running.
	other := filepath.Join(t.TempDir(), "mods")
	stale := filepath.Join(other, stagingName, "extract-running")
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatal(err)
	}
	done := beginInstall(other)
	if _, err := Open(other); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); err != nil {
		t.Fatalf("Open cleared staging under an install in progress: %v", err)
	}
	done()
	if _, err := Open(other); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("staging left behind once no install was running: %v", err)
	}
}

// Reopening the library while an archive is extracting (the Mods screen opened
// again during a download's install) must not cut the install off.
func TestOpenDuringAnInstallLeavesItToFinish(t *testing.T) {
	lib := openTestLibrary(t)
	archive := writeZip(t, t.TempDir(), "sample.zip", zipItem{name: MetadataFile, body: metadataJSON(t, sampleMetadata())}, zipItem{name: "units/a.fbi", body: "a"})
	mod, err := lib.InstallArchive(archive, InstallOptions{Validate: func(stagedRoot string, meta Metadata) error {
		if _, err := Open(lib.Root); err != nil {
			return err
		}
		_, err := os.Stat(filepath.Join(stagedRoot, "units", "a.fbi"))
		return err
	}})
	if err != nil {
		t.Fatalf("install interrupted by a concurrent Open: %v", err)
	}
	if _, ok, _ := lib.Lookup(mod.ID, mod.Version); !ok {
		t.Fatal("the install did not commit")
	}
}

func TestInstallArchiveWithMetadata(t *testing.T) {
	lib := openTestLibrary(t)
	meta := Metadata{Schema: 1, ID: "prota", Name: "ProTA", Version: "4.8", ContentProfile: "prota", MinimumGameplay: "community-3.9", Controls: "community"}
	archive := writeZip(t, t.TempDir(), "prota-4.8.zip",
		zipItem{name: MetadataFile, body: metadataJSON(t, meta)},
		zipItem{name: "units/", mode: fs.ModeDir},
		zipItem{name: "units\\armcom.fbi", body: "[UNITINFO]{}"},
		zipItem{name: "prota.ufo", body: "authored archive stand-in"},
	)
	var lastDone, lastTotal int64
	mod, err := lib.InstallArchive(archive, InstallOptions{Progress: func(done, total int64) { lastDone, lastTotal = done, total }})
	if err != nil {
		t.Fatal(err)
	}
	if mod.Dir != filepath.Join(lib.Root, "prota", "4.8") || mod.Local || !reflect.DeepEqual(mod.Metadata, meta) {
		t.Fatalf("installed %+v", mod)
	}
	for _, name := range []string{MetadataFile, ReceiptFile, "prota.ufo", filepath.Join("units", "armcom.fbi")} {
		if _, err := os.Stat(filepath.Join(mod.Dir, name)); err != nil {
			t.Errorf("installed tree lacks %s: %v", name, err)
		}
	}
	data, _ := os.ReadFile(archive)
	sum := sha256.Sum256(data)
	if mod.Receipt.SHA256 != hex.EncodeToString(sum[:]) || mod.Receipt.Size != int64(len(data)) || mod.Receipt.Source != "local:prota-4.8.zip" {
		t.Fatalf("receipt %+v", mod.Receipt)
	}
	if lastTotal == 0 || lastDone != lastTotal {
		t.Fatalf("final progress %d/%d, want done == total > 0", lastDone, lastTotal)
	}
	mods, err := lib.Installed()
	if err != nil || len(mods) != 1 || !reflect.DeepEqual(mods[0], mod) {
		t.Fatalf("Installed = %+v, %v; want the one install", mods, err)
	}
}

func TestExtractionRefusesUnsafeEntries(t *testing.T) {
	metadata := zipItem{name: MetadataFile, body: metadataJSON(t, sampleMetadata())}
	for name, item := range map[string]zipItem{
		"absolute":          {name: "/etc/passwd", body: "x"},
		"backslash root":    {name: "\\\\server\\share\\x", body: "x"},
		"drive letter":      {name: "C:/x.txt", body: "x"},
		"drive backslash":   {name: "c:\\x.txt", body: "x"},
		"parent":            {name: "units/../../x", body: "x"},
		"parent backslash":  {name: "..\\x", body: "x"},
		"stream":            {name: "units/a.fbi:hidden", body: "x"},
		"symlink":           {name: "units/link", body: "/etc/passwd", mode: fs.ModeSymlink},
		"symlink in litter": {name: "__MACOSX/link", body: "/etc/passwd", mode: fs.ModeSymlink},
	} {
		t.Run(name, func(t *testing.T) {
			lib := openTestLibrary(t)
			archive := writeZip(t, t.TempDir(), "bad.zip", metadata, zipItem{name: "a.ufo", body: "a"}, item)
			if _, err := lib.InstallArchive(archive, InstallOptions{}); err == nil || !strings.Contains(err.Error(), "unsafe") {
				t.Fatalf("install = %v, want an unsafe-entry refusal", err)
			}
			assertNothingInstalled(t, lib)
		})
	}
}

func TestExtractionSkipsLitterAndExecutables(t *testing.T) {
	lib := openTestLibrary(t)
	archive := writeZip(t, t.TempDir(), "sample.zip",
		zipItem{name: MetadataFile, body: metadataJSON(t, sampleMetadata())},
		zipItem{name: "sample.ufo", body: "a"},
		zipItem{name: "__MACOSX/._sample.ufo", body: "fork"},
		zipItem{name: ".DS_Store", body: "finder"},
		zipItem{name: "units/.ds_store", body: "finder"},
		zipItem{name: "Setup.EXE", body: "x"},
		zipItem{name: "ddraw.dll", body: "x"},
		zipItem{name: "tools/run.sh", body: "x"},
		zipItem{name: "tools/lib.so", body: "x"},
		zipItem{name: "tools/readme.txt", body: "kept"},
		zipItem{name: ReceiptFile, body: `{"sha256":"forged"}`},
	)
	mod, err := lib.InstallArchive(archive, InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	_ = filepath.WalkDir(mod.Dir, func(name string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(mod.Dir, name)
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	want := []string{ReceiptFile, MetadataFile, "sample.ufo", "tools/readme.txt"}
	if !reflect.DeepEqual(files, want) {
		t.Fatalf("installed files %v, want %v", files, want)
	}
	if mod.Receipt.SHA256 == "forged" {
		t.Fatal("a packaged install.json replaced the library's receipt")
	}
}

func TestExtractionRefusesCaseFoldedDuplicates(t *testing.T) {
	metadata := zipItem{name: MetadataFile, body: metadataJSON(t, sampleMetadata())}
	for name, items := range map[string][]zipItem{
		"two files":         {{name: "units/ARMCOM.FBI", body: "a"}, {name: "Units/armcom.fbi", body: "b"}},
		"file and folder":   {{name: "units", body: "a"}, {name: "UNITS/armcom.fbi", body: "b"}},
		"two folders":       {{name: "units/", mode: fs.ModeDir}, {name: "UNITS/", mode: fs.ModeDir}, {name: "units/a.fbi", body: "a"}},
		"exact repeat":      {{name: "units/a.fbi", body: "a"}, {name: "units/a.fbi", body: "b"}},
		"slash styles fold": {{name: "units/a.fbi", body: "a"}, {name: "UNITS\\A.FBI", body: "b"}},
	} {
		t.Run(name, func(t *testing.T) {
			lib := openTestLibrary(t)
			archive := writeZip(t, t.TempDir(), "dup.zip", append([]zipItem{metadata}, items...)...)
			if _, err := lib.InstallArchive(archive, InstallOptions{}); err == nil || !strings.Contains(err.Error(), "differ only in case") {
				t.Fatalf("install = %v, want a case-folded duplicate refusal", err)
			}
			assertNothingInstalled(t, lib)
		})
	}
}

func TestExtractionCaps(t *testing.T) {
	items := []zipItem{
		{name: MetadataFile, body: metadataJSON(t, sampleMetadata())},
		{name: "a.ufo", body: "0123456789"},
		{name: "b.ufo", body: "0123456789"},
	}
	t.Run("entries", func(t *testing.T) {
		lib := openTestLibrary(t)
		lib.maxEntries = len(items) - 1
		archive := writeZip(t, t.TempDir(), "many.zip", items...)
		if _, err := lib.InstallArchive(archive, InstallOptions{}); err == nil || !strings.Contains(err.Error(), "entries") {
			t.Fatalf("install = %v, want the entry cap", err)
		}
		assertNothingInstalled(t, lib)
		lib.maxEntries = len(items)
		if _, err := lib.InstallArchive(archive, InstallOptions{}); err != nil {
			t.Fatalf("install at exactly the entry cap: %v", err)
		}
	})
	t.Run("bytes", func(t *testing.T) {
		lib := openTestLibrary(t)
		total := int64(0)
		for _, item := range items {
			total += int64(len(item.body))
		}
		lib.maxBytes = total - 1
		archive := writeZip(t, t.TempDir(), "big.zip", items...)
		if _, err := lib.InstallArchive(archive, InstallOptions{}); err == nil || !strings.Contains(err.Error(), "bytes") {
			t.Fatalf("install = %v, want the byte cap", err)
		}
		assertNothingInstalled(t, lib)
		lib.maxBytes = total
		if _, err := lib.InstallArchive(archive, InstallOptions{}); err != nil {
			t.Fatalf("install at exactly the byte cap: %v", err)
		}
	})
}

func TestWrapperFolderIsStripped(t *testing.T) {
	t.Run("metadata wrapper", func(t *testing.T) {
		lib := openTestLibrary(t)
		archive := writeZip(t, t.TempDir(), "wrapped.zip",
			zipItem{name: "Sample/", mode: fs.ModeDir},
			zipItem{name: "Sample/" + MetadataFile, body: metadataJSON(t, sampleMetadata())},
			zipItem{name: "Sample/units/a.fbi", body: "a"},
			zipItem{name: "__MACOSX/Sample/._units", body: "fork"},
		)
		mod, err := lib.InstallArchive(archive, InstallOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if mod.ID != "sample" || mod.Local {
			t.Fatalf("installed %+v, want the wrapped metadata", mod)
		}
		if _, err := os.Stat(filepath.Join(mod.Dir, "units", "a.fbi")); err != nil {
			t.Fatalf("wrapper not stripped: %v", err)
		}
	})
	t.Run("archive wrapper without metadata", func(t *testing.T) {
		lib := openTestLibrary(t)
		archive := writeZip(t, t.TempDir(), "Extras.zip", zipItem{name: "Extras\\extras.ccx", body: "a"})
		mod, err := lib.InstallArchive(archive, InstallOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(mod.Dir, "extras.ccx")); err != nil {
			t.Fatalf("wrapper not stripped: %v", err)
		}
	})
	t.Run("loose-only folder is kept", func(t *testing.T) {
		lib := openTestLibrary(t)
		archive := writeZip(t, t.TempDir(), "loose.zip", zipItem{name: "Loose/units/a.fbi", body: "a"})
		mod, err := lib.InstallArchive(archive, InstallOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(mod.Dir, "Loose", "units", "a.fbi")); err != nil {
			t.Fatalf("a folder without metadata or archives was stripped: %v", err)
		}
	})
	t.Run("two folders are kept", func(t *testing.T) {
		lib := openTestLibrary(t)
		archive := writeZip(t, t.TempDir(), "two.zip", zipItem{name: "A/a.ufo", body: "a"}, zipItem{name: "B/b.ufo", body: "b"})
		mod, err := lib.InstallArchive(archive, InstallOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(mod.Dir, "A", "a.ufo")); err != nil {
			t.Fatalf("one of two top-level folders was stripped: %v", err)
		}
	})
}

func TestMetadataLessPackageInstallsAsLocal(t *testing.T) {
	lib := openTestLibrary(t)
	archive := writeZip(t, t.TempDir(), "ProTA Extras!.zip", zipItem{name: "extras.ufo", body: "a"})
	mod, err := lib.InstallArchive(archive, InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := Metadata{Schema: 1, ID: "local-prota-extras", Name: "ProTA Extras!", Version: "local"}
	if !mod.Local || !reflect.DeepEqual(mod.Metadata, want) {
		t.Fatalf("installed %+v, want local %+v", mod, want)
	}
	data, err := os.ReadFile(filepath.Join(mod.Dir, MetadataFile))
	if err != nil {
		t.Fatal(err)
	}
	if generated, err := ParseMetadata(data); err != nil || !reflect.DeepEqual(generated, want) {
		t.Fatalf("generated metadata %+v, %v", generated, err)
	}
	mods, err := lib.Installed()
	if err != nil || len(mods) != 1 || !mods[0].Local {
		t.Fatalf("Installed = %+v, %v; want the local mod", mods, err)
	}

	claimed := sampleMetadata()
	claimed.ID = "local-sample"
	archive = writeZip(t, t.TempDir(), "claims.zip", zipItem{name: MetadataFile, body: metadataJSON(t, claimed)}, zipItem{name: "a.ufo", body: "a"})
	if _, err := lib.InstallArchive(archive, InstallOptions{}); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("packaged metadata claiming a local id installed: %v", err)
	}
	if got := sanitizeName("  ...  "); got != "mod" {
		t.Fatalf("sanitizeName of punctuation = %q, want mod", got)
	}
}

func TestExpectMismatchRefusesInstall(t *testing.T) {
	packaged := Metadata{Schema: 1, ID: "prota", Name: "ProTA", Version: "4.8", ContentProfile: "prota", MinimumGameplay: "community-3.9", Controls: "community"}
	archive := writeZip(t, t.TempDir(), "prota.zip", zipItem{name: MetadataFile, body: metadataJSON(t, packaged)}, zipItem{name: "a.ufo", body: "a"})
	for field, mutate := range map[string]func(*Metadata){
		"id":              func(m *Metadata) { m.ID = "escalation" },
		"version":         func(m *Metadata) { m.Version = "4.9" },
		"contentProfile":  func(m *Metadata) { m.ContentProfile = "" },
		"minimumGameplay": func(m *Metadata) { m.MinimumGameplay = "modern" },
		"controls":        func(m *Metadata) { m.Controls = "retail" },
	} {
		t.Run(field, func(t *testing.T) {
			lib := openTestLibrary(t)
			expect := packaged
			mutate(&expect)
			if _, err := lib.InstallArchive(archive, InstallOptions{Expect: &expect}); err == nil || !strings.Contains(err.Error(), "disagrees with the catalogue on "+field) {
				t.Fatalf("install = %v, want a %s disagreement", err, field)
			}
			assertNothingInstalled(t, lib)
		})
	}
	t.Run("summary may differ", func(t *testing.T) {
		lib := openTestLibrary(t)
		expect := packaged
		expect.Summary = "The catalogue's own words."
		if _, err := lib.InstallArchive(archive, InstallOptions{Expect: &expect}); err != nil {
			t.Fatalf("a display-only difference refused the install: %v", err)
		}
	})
	t.Run("no metadata", func(t *testing.T) {
		lib := openTestLibrary(t)
		bare := writeZip(t, t.TempDir(), "bare.zip", zipItem{name: "a.ufo", body: "a"})
		if _, err := lib.InstallArchive(bare, InstallOptions{Expect: &packaged}); err == nil {
			t.Fatal("a catalogue install without metadata was accepted")
		}
		assertNothingInstalled(t, lib)
	})
}

func TestArchiveIdentityIsVerifiedBeforeExtraction(t *testing.T) {
	archive := writeZip(t, t.TempDir(), "sample.zip", zipItem{name: MetadataFile, body: metadataJSON(t, sampleMetadata())}, zipItem{name: "a.ufo", body: "a"})
	data, _ := os.ReadFile(archive)
	sum := sha256.Sum256(data)
	good := hex.EncodeToString(sum[:])

	lib := openTestLibrary(t)
	if _, err := lib.InstallArchive(archive, InstallOptions{SHA256: strings.Repeat("0", 64)}); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("wrong digest = %v, want a SHA-256 refusal", err)
	}
	if _, err := lib.InstallArchive(archive, InstallOptions{Size: int64(len(data)) + 1}); err == nil || !strings.Contains(err.Error(), "size") {
		t.Fatalf("wrong size = %v, want a size refusal", err)
	}
	assertNothingInstalled(t, lib)
	mod, err := lib.InstallArchive(archive, InstallOptions{SHA256: strings.ToUpper(good), Size: int64(len(data)), Source: "https://nanolathe.gg/mods/sample/sample-1.0.zip"})
	if err != nil {
		t.Fatal(err)
	}
	if mod.Receipt.SHA256 != good || mod.Receipt.Source != "https://nanolathe.gg/mods/sample/sample-1.0.zip" {
		t.Fatalf("receipt %+v", mod.Receipt)
	}
}

func TestValidateFailureLeavesNothingInstalled(t *testing.T) {
	lib := openTestLibrary(t)
	archive := writeZip(t, t.TempDir(), "sample.zip", zipItem{name: MetadataFile, body: metadataJSON(t, sampleMetadata())}, zipItem{name: "units/a.fbi", body: "a"})
	refusal := errors.New("injected validation failure")
	var sawRoot string
	_, err := lib.InstallArchive(archive, InstallOptions{Validate: func(stagedRoot string, meta Metadata) error {
		sawRoot = stagedRoot
		if meta.ID != "sample" {
			t.Errorf("Validate saw %+v", meta)
		}
		if _, err := os.Stat(filepath.Join(stagedRoot, "units", "a.fbi")); err != nil {
			t.Errorf("Validate ran before extraction: %v", err)
		}
		if _, err := os.Stat(filepath.Join(stagedRoot, ReceiptFile)); err == nil {
			t.Error("the receipt was written before validation")
		}
		return refusal
	}})
	if !errors.Is(err, refusal) {
		t.Fatalf("install = %v, want the validation failure", err)
	}
	if !strings.HasPrefix(sawRoot, lib.StagingDir()) {
		t.Fatalf("Validate saw %s, want a directory under staging", sawRoot)
	}
	assertNothingInstalled(t, lib)
}

func TestInstalledLookupRemove(t *testing.T) {
	lib := openTestLibrary(t)
	install := func(meta Metadata) Mod {
		t.Helper()
		archive := writeZip(t, t.TempDir(), meta.ID+".zip", zipItem{name: MetadataFile, body: metadataJSON(t, meta)}, zipItem{name: "a.ufo", body: meta.Version})
		mod, err := lib.InstallArchive(archive, InstallOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return mod
	}
	beta := install(Metadata{Schema: 1, ID: "beta", Name: "Beta", Version: "1"})
	alpha2 := install(Metadata{Schema: 1, ID: "alpha", Name: "alpha", Version: "2"})
	alpha1 := install(Metadata{Schema: 1, ID: "alpha", Name: "alpha", Version: "1"})

	// Incomplete directories are not installs.
	if err := os.MkdirAll(filepath.Join(lib.Root, "gamma", "1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lib.Root, "gamma", "1", MetadataFile), []byte(metadataJSON(t, Metadata{Schema: 1, ID: "gamma", Name: "Gamma", Version: "1"})), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lib.Root, "manifest.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}

	mods, err := lib.Installed()
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, mod := range mods {
		order = append(order, mod.ID+"@"+mod.Version)
	}
	// Name folds case, so "alpha" sorts before "Beta"; the two alpha
	// versions follow install time, oldest first.
	if want := []string{"alpha@2", "alpha@1", "beta@1"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("Installed order %v, want %v", order, want)
	}

	if mod, ok, err := lib.Lookup("alpha", ""); err != nil || !ok || mod.Version != alpha1.Version {
		t.Fatalf("Lookup(alpha, newest) = %s, %v, %v; want the most recent install %s", mod.Version, ok, err, alpha1.Version)
	}
	if mod, ok, err := lib.Lookup("alpha", "2"); err != nil || !ok || mod.Dir != alpha2.Dir {
		t.Fatalf("Lookup(alpha, 2) = %+v, %v, %v", mod, ok, err)
	}
	if _, ok, err := lib.Lookup("alpha", "3"); err != nil || ok {
		t.Fatalf("Lookup(alpha, 3) found an uninstalled version: %v", err)
	}
	if _, ok, _ := lib.Lookup("gamma", ""); ok {
		t.Fatal("Lookup found an install without a receipt")
	}

	if _, err := lib.InstallArchive(writeZip(t, t.TempDir(), "again.zip", zipItem{name: MetadataFile, body: metadataJSON(t, beta.Metadata)}, zipItem{name: "b.ufo", body: "b"}), InstallOptions{}); !errors.Is(err, ErrAlreadyInstalled) {
		t.Fatalf("reinstall = %v, want ErrAlreadyInstalled", err)
	}

	if err := lib.Remove("alpha", "1"); err != nil {
		t.Fatal(err)
	}
	if mod, ok, _ := lib.Lookup("alpha", ""); !ok || mod.Version != "2" {
		t.Fatalf("after removing alpha@1, newest alpha = %+v, %v; want 2", mod, ok)
	}
	if err := lib.Remove("alpha", "2"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(lib.Root, "alpha")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the emptied id directory remains: %v", err)
	}
	if err := lib.Remove("alpha", "2"); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("second removal = %v, want ErrNotInstalled", err)
	}
	if err := lib.Remove("alpha", ""); err == nil {
		t.Fatal("a removal without a version was accepted")
	}
	if entries, _ := os.ReadDir(lib.StagingDir()); len(entries) != 0 {
		t.Fatalf("removal left %d staging entries", len(entries))
	}
}

func TestInstallDirectory(t *testing.T) {
	source := filepath.Join(t.TempDir(), "Sample Folder")
	for name, body := range map[string]string{
		MetadataFile:                         metadataJSON(t, sampleMetadata()),
		"sample.gp3":                         "a",
		filepath.Join("units", "a.fbi"):      "b",
		filepath.Join("__MACOSX", "._a.fbi"): "fork",
		"install.exe":                        "x",
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(source, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(source, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lib := openTestLibrary(t)
	if _, err := lib.InstallDirectory(source, InstallOptions{SHA256: strings.Repeat("0", 64)}); err == nil {
		t.Fatal("an archive digest was accepted for a folder")
	}
	mod, err := lib.InstallDirectory(source, InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if mod.ID != "sample" || mod.Receipt.Source != "local:Sample Folder" || mod.Receipt.SHA256 != "" {
		t.Fatalf("installed %+v", mod)
	}
	if _, err := os.Stat(filepath.Join(mod.Dir, "units", "a.fbi")); err != nil {
		t.Fatal(err)
	}
	for _, skipped := range []string{"__MACOSX", "install.exe"} {
		if _, err := os.Stat(filepath.Join(mod.Dir, skipped)); err == nil {
			t.Errorf("%s was copied", skipped)
		}
	}

	if err := os.Symlink("/etc", filepath.Join(source, "units", "escape")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	other := openTestLibrary(t)
	if _, err := other.InstallDirectory(source, InstallOptions{}); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("a folder with a link installed: %v", err)
	}
	assertNothingInstalled(t, other)
}

// writeTree writes authored text files under dir.
func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestContentValidator(t *testing.T) {
	base := t.TempDir()
	writeTree(t, base, map[string]string{
		"gamedata/moveinfo.tdf": "[CLASS0]{}",
		"gamedata/sidedata.tdf": "[SIDE0]{}",
		"maps/present.tnt":      "authored stand-in",
	})
	emptyBase := t.TempDir()

	install := func(t *testing.T, baseRoots []string, meta Metadata, files map[string]string) (*Library, error) {
		lib := openTestLibrary(t)
		items := []zipItem{{name: MetadataFile, body: metadataJSON(t, meta)}}
		for name, body := range files {
			items = append(items, zipItem{name: name, body: body})
		}
		archive := writeZip(t, t.TempDir(), meta.ID+".zip", items...)
		_, err := lib.InstallArchive(archive, InstallOptions{Validate: ContentValidator(baseRoots)})
		return lib, err
	}

	t.Run("detected retail layout over a base", func(t *testing.T) {
		if _, err := install(t, []string{base}, sampleMetadata(), map[string]string{"units/a.fbi": "a"}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("missing required product", func(t *testing.T) {
		lib, err := install(t, []string{emptyBase}, sampleMetadata(), map[string]string{"units/a.fbi": "a"})
		if err == nil || !strings.Contains(err.Error(), "gamedata/moveinfo.tdf") {
			t.Fatalf("install = %v, want the missing product named", err)
		}
		assertNothingInstalled(t, lib)
	})
	t.Run("shipped profile reads its renamed directory", func(t *testing.T) {
		meta := sampleMetadata()
		meta.ContentProfile = "prota"
		// The prota table sends gamedata to gamedatP, which the base lacks.
		if _, err := install(t, []string{base}, meta, map[string]string{"units/a.fbi": "a"}); err == nil {
			t.Fatal("a prota-profile mod without gamedatP validated")
		}
		if _, err := install(t, []string{base}, meta, map[string]string{
			"gamedatP/moveinfo.tdf": "[CLASS0]{}", "gamedatP/sidedata.tdf": "[SIDE0]{}",
		}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("profile path inside the mod", func(t *testing.T) {
		meta := sampleMetadata()
		meta.ContentProfile = "profiles/custom.json"
		lib, err := install(t, []string{emptyBase}, meta, map[string]string{
			"profiles/custom.json":  `{"name":"custom","detect":["gamedatX"],"layout":{"gamedata":"gamedatX"},"limits":{}}`,
			"gamedatX/moveinfo.tdf": "[CLASS0]{}", "gamedatX/sidedata.tdf": "[SIDE0]{}",
		})
		if err != nil {
			t.Fatal(err)
		}
		mod, ok, err := lib.Lookup("sample", "")
		if err != nil || !ok {
			t.Fatalf("Lookup = %v, %v", ok, err)
		}
		if got, want := mod.ContentProfileSelector(), filepath.Join(mod.Dir, "profiles", "custom.json"); got != want {
			t.Fatalf("ContentProfileSelector = %q, want %q", got, want)
		}
	})

	baseFS := vfs.New()
	defer baseFS.Close()
	if err := baseFS.MountGameDirectories([]string{base}); err != nil {
		t.Fatal(err)
	}
	meta := sampleMetadata()
	meta.Requires = []string{"maps/absent.tnt", "maps/present.tnt", "maps/also-absent.tnt"}
	if got, want := MissingRequirements(baseFS, meta), []string{"maps/absent.tnt", "maps/also-absent.tnt"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("MissingRequirements = %v, want %v", got, want)
	}
	for selector, want := range map[string]string{"": "", "PROTA": "PROTA"} {
		meta := sampleMetadata()
		meta.ContentProfile = selector
		if got := (Mod{Metadata: meta, Dir: "/mods/sample/1.0"}).ContentProfileSelector(); got != want {
			t.Errorf("ContentProfileSelector(%q) = %q, want %q", selector, got, want)
		}
	}
}
