package content

import (
	"sort"
	"strings"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/vfs"
)

// TranslationTable is the loaded gamedata/translate.tdf mapping for one
// configured language. The generic TDF parser enumerates every top-level
// section: the section name is the source string, and the value of the key
// named by the current language string is its translation; a section whose
// language key is absent or empty contributes nothing, and a repeated
// section name keeps the last translation [02 "Translation table"].
//
// Entries are kept in stored (byte-sorted source) order because the mission
// loader's reverse lookup walks that order and returns the first
// case-insensitive hit on the translated text [08 R-CAMP-01 §11].
type TranslationTable struct {
	entries []translationEntry
}

type translationEntry struct {
	source      string
	translation string
}

// LoadTranslationTable parses gamedata/translate.tdf for the given language
// string (an empty string selects English) [02 "Translation table"].
// translate.tdf is optional content [02 §1]; a missing or unparsable file,
// or a language with no matching key in any section (English: the internal
// language string is empty, and no authored key is ever named "", so the
// table comes back empty), yields a nil table. Every lookup treats a nil
// table as "no table loaded."
func LoadTranslationTable(fs vfs.FSOps, language string) (*TranslationTable, error) {
	if fs == nil {
		return nil, nil
	}
	data, err := fs.ReadFileLimit("gamedata/translate.tdf", 1<<20)
	if err != nil {
		return nil, nil // optional file [02 §1][02 "Translation table"]
	}
	doc, err := formats.ParseTDF(data)
	if err != nil || doc.Root == nil {
		return nil, nil
	}
	index := make(map[string]int)
	t := &TranslationTable{}
	for _, sec := range doc.Root.Sections() {
		value, ok := sec.StringValue(language, "")
		if !ok || value == "" {
			continue // absent or empty language key contributes nothing [02 "Translation table"]
		}
		source := sec.OriginalName
		if i, dup := index[source]; dup {
			t.entries[i].translation = value // repeated section name keeps the last translation [02 "Translation table"]
			continue
		}
		index[source] = len(t.entries)
		t.entries = append(t.entries, translationEntry{source: source, translation: value})
	}
	if len(t.entries) == 0 {
		return nil, nil
	}
	sort.Slice(t.entries, func(i, j int) bool { return t.entries[i].source < t.entries[j].source })
	return t, nil
}

// Translate performs the forward lookup: byte-exact equality on the source
// string, unlike every other name comparison in the content layer, which is
// case-insensitive [02 "Translation table"]. It returns the input unchanged
// when no table is loaded or no entry matches — the identity fallback.
func (t *TranslationTable) Translate(source string) string {
	if t == nil {
		return source
	}
	for _, e := range t.entries {
		if e.source == source {
			return e.translation
		}
	}
	return source
}

// Source performs the reverse lookup the mission loader's mission-file
// recovery uses when a directly supplied map name cannot be opened: a linear
// walk of the table in stored (source-sorted) order, case-insensitive
// equality on the translated text, returning the first hit
// [08 R-CAMP-01 §11]. With no table loaded, an empty name, or no matching
// entry it reports false; the caller's load then fails silently.
func (t *TranslationTable) Source(translated string) (string, bool) {
	if t == nil || translated == "" {
		return "", false
	}
	for _, e := range t.entries {
		if strings.EqualFold(e.translation, translated) {
			return e.source, true
		}
	}
	return "", false
}
