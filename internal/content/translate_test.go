package content

import (
	"bytes"
	"errors"

	"github.com/nanolathe-gg/nanolathe/formats"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/vfs"
)

func translateFS(t *testing.T, files map[string]string) *vfs.FS {
	t.Helper()
	dir := t.TempDir()
	for logical, data := range files {
		full := filepath.Join(dir, filepath.FromSlash(logical))
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatalf("mkdir %q: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(data), 0644); err != nil {
			t.Fatalf("write %q: %v", full, err)
		}
	}
	fs := vfs.New()
	if err := fs.MountDirectory(dir, 10); err != nil {
		t.Fatalf("MountDirectory: %v", err)
	}
	return fs
}

// TestLoadTranslationTableForwardAndReverse locks the two lookup directions
// [02 "Translation table"] [08 R-CAMP-01 §11]: Translate is byte-exact on the
// source string; Source is case-insensitive on the translated text and
// returns the first hit in stored (source-sorted) order.
func TestLoadTranslationTableForwardAndReverse(t *testing.T) {
	fs := translateFS(t, map[string]string{
		"gamedata/translate.tdf": "" +
			"[Beta]\n{\n    German=Zweite;\n}\n" +
			"[Alpha]\n{\n    German=Anfang;\n}\n",
	})
	table, err := LoadTranslationTable(fs, "German")
	if err != nil {
		t.Fatalf("LoadTranslationTable: %v", err)
	}
	if table == nil {
		t.Fatalf("want a loaded table")
	}
	if got := table.Translate("Alpha"); got != "Anfang" {
		t.Fatalf("forward lookup: got %q want %q", got, "Anfang")
	}
	if got := table.Translate("alpha"); got != "alpha" {
		t.Fatalf("forward lookup must be byte-exact: got %q want identity %q", got, "alpha")
	}
	if got := table.Translate("Missing"); got != "Missing" {
		t.Fatalf("forward lookup identity fallback: got %q want %q", got, "Missing")
	}
	source, ok := table.Source("anfang")
	if !ok || source != "Alpha" {
		t.Fatalf("reverse lookup case-insensitive: got (%q, %v) want (%q, true)", source, ok, "Alpha")
	}
	if _, ok := table.Source("Unknown"); ok {
		t.Fatalf("reverse lookup should miss for an untranslated name")
	}
}

// TestLoadTranslationTableDefaultEnglishIsEmpty locks ordinary startup's
// literal lowercase `english` selection [02 §3]. With no authored `english`
// key, no entry is collected and the table comes back nil ("no table loaded").
func TestLoadTranslationTableDefaultEnglishIsEmpty(t *testing.T) {
	fs := translateFS(t, map[string]string{
		"gamedata/translate.tdf": "[ashap plateau]\n{\n    German=Ashap-Ebene;\n}\n",
	})
	table, err := LoadTranslationTable(fs, "english")
	if err != nil {
		t.Fatalf("LoadTranslationTable: %v", err)
	}
	if table != nil {
		t.Fatalf("want a nil (empty) table for the English/default language, got %+v", table)
	}
}

// TestLoadTranslationTableMissingFileIsOptional locks translate.tdf as
// optional content [02 §1]: a missing file is not an error.
func TestLoadTranslationTableMissingFileIsOptional(t *testing.T) {
	fs := translateFS(t, map[string]string{})
	table, err := LoadTranslationTable(fs, "German")
	if err != nil {
		t.Fatalf("LoadTranslationTable: %v", err)
	}
	if table != nil {
		t.Fatalf("want a nil table when translate.tdf is absent, got %+v", table)
	}
}

func TestLoadTranslationTableReturnsPresentParseFailure(t *testing.T) {
	fs := translateFS(t, map[string]string{
		"gamedata/translate.tdf": "[Alpha]{ German=Anfang",
	})
	_, err := LoadTranslationTable(fs, "German")
	if err == nil {
		t.Fatal("malformed present translation table was accepted")
	}
	for _, want := range []string{"Parse error in .TDF File!", "Data field - ';' not found", "from file gamedata/translate.tdf"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("translation error %q does not contain %q", err, want)
		}
	}
}

func TestTranslationReverseFoldPreservesHighBytes(t *testing.T) {
	table := &TranslationTable{entries: []translationEntry{{source: "A", translation: "\xc0"}, {source: "B", translation: "\xe0"}}}
	if source, ok := table.Source("\xc0"); !ok || source != "A" {
		t.Fatalf("first high-byte translation = (%q, %t), want (A, true)", source, ok)
	}
	if source, ok := table.Source("\xe0"); !ok || source != "B" {
		t.Fatalf("second high-byte translation = (%q, %t), want (B, true)", source, ok)
	}
}

// TestTranslationTableNilReceiverIsSafe locks the "no table loaded" case a
// caller reaches without ever checking for nil first [08 R-CAMP-01 §11].
func TestTranslationTableNilReceiverIsSafe(t *testing.T) {
	var table *TranslationTable
	if got := table.Translate("Alpha"); got != "Alpha" {
		t.Fatalf("nil table forward lookup: got %q want identity %q", got, "Alpha")
	}
	if _, ok := table.Source("Anfang"); ok {
		t.Fatalf("nil table reverse lookup must report no hit")
	}
}

func TestTranslationParseFailureNamesWinningArchive(t *testing.T) {
	fs := vfs.New()
	defer fs.Close()
	const logical = "gamedata/translate.tdf"
	for priority, provider := range []string{"original.hpi", "patch.hpi"} {
		body := "[valid]{German=translated;}"
		if priority == 1 {
			body = "[broken"
		}
		var archive bytes.Buffer
		if err := vfs.WriteArchive(&archive, []vfs.ArchiveFile{{Path: logical, Data: []byte(body)}}, vfs.ArchiveWriteOptions{}); err != nil {
			t.Fatal(err)
		}
		if _, err := fs.MountArchiveReader(provider, bytes.NewReader(archive.Bytes()), int64(archive.Len()), priority, vfs.ArchiveOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	_, err := LoadTranslationTable(fs, "German")
	var parseErr *formats.ParseError
	if !errors.As(err, &parseErr) || parseErr.Diagnostic != formats.DiagClosingBracket || parseErr.File != logical {
		t.Fatalf("winning translation error = %v, want structured closing-bracket diagnostic [02 §4]", err)
	}
	if !strings.Contains(err.Error(), "providers searched [patch.hpi]") || strings.Contains(err.Error(), "original.hpi") {
		t.Fatalf("winning provider lost or shadowed provider substituted: %v", err)
	}
}
