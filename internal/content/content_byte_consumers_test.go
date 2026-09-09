package content

import (
	"errors"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/vfs"
)

func TestC09CategoryTokenizerPreservesHighBytesAndUsesOnlyCRTSpace(t *testing.T) {
	units := map[string]*UnitDef{
		"byteunit": categoryUnit("byteunit", "\xc0\v\xe0\fALPHA"),
	}
	r, err := CompileCategories(units)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"\xc0", "\xe0", "alpha"} {
		m, ok := r.Lookup(name)
		if !ok || !m.Contains(1) {
			t.Fatalf("category %q = %#v, want unit membership", name, m.Words)
		}
	}
}

func TestC09DownloadReferencesDoNotUnicodeTrimFormFeedOrVerticalTab(t *testing.T) {
	units := map[string]*UnitDef{"unit": {UnitName: "UNIT"}}
	fs := newFixtureFS(t, fixtureFile{path: "download/bytes.tdf", data: "[ENTRY] { UNITMENU=\vUNIT\f; UNITNAME=\vUNIT\f; MENU=1; BUTTON=0; }\n"})
	placements, err := CompileDownloadMenus(fs, units)
	if err != nil {
		t.Fatal(err)
	}
	if len(placements) != 1 {
		t.Fatalf("placements = %d, want 1", len(placements))
	}
	if placements[0].BuilderResolved || placements[0].ProductResolved {
		t.Fatalf("form-feed/vertical-tab reference was trimmed into UNIT: %#v", placements[0])
	}
}

func TestC09FeatureSectionNamesKeepHighBytesAndNonTDFWhitespace(t *testing.T) {
	fs := newFixtureFS(t, fixtureFile{path: "features/bytes.tdf", data: "[\xc0]{}\n[\xe0]{}\n[\vfeature\f]{}\n"})
	features, err := CompileFeatures(fs)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"\xc0", "\xe0", "\vfeature\f"} {
		if features[CanonicalKey(name)] == nil {
			t.Fatalf("feature section %q was not retained: %#v", name, features)
		}
	}
}

func TestC09ContentEntryReadErrorRetainsWinningProvider(t *testing.T) {
	wantCause := errors.New("fixture unreadable")
	fs := unreadableContentEntryFS{fixtureFS: newFixtureFS(t, fixtureFile{path: "weapons/read.tdf", data: "unused"}), err: wantCause}
	entry := archiveContentFile{info: vfs.EntryInfo{Path: "weapons/read.tdf", Source: vfs.Provenance{ProviderType: "hpi", SourcePath: "totala1.hpi"}}}
	_, err := readContentEntry(fs, entry)
	if !errors.Is(err, wantCause) {
		t.Fatalf("readContentEntry error = %v, want wrapped cause", err)
	}
	for _, want := range []string{"nanolathe: content entry read:", "logical path weapons/read.tdf", "providers searched [totala1.hpi]", "expected readable content definition"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("readContentEntry error %q does not contain %q", err, want)
		}
	}
}

type unreadableContentEntryFS struct {
	*fixtureFS
	err error
}

func (f unreadableContentEntryFS) ReadFileLimit(string, int64) ([]byte, error) { return nil, f.err }
