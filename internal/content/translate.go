package content

import (
	"errors"
	"sort"

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

// LoadTranslationTable parses gamedata/translate.tdf for the exact language
// string supplied by its caller. Ordinary startup supplies the literal
// lowercase `english`; an empty string remains a valid explicit generic input,
// but is not retail's startup default [02 §3 "Translation table"].
// translate.tdf is optional content [02 §1]. Only a missing file yields no
// table; a present file that cannot be read or parsed is authored corruption
// and its diagnostic reaches the loader. A language with no matching key in
// any section yields a nil table. Every lookup treats a nil table as "no table
// loaded."
func LoadTranslationTable(fs vfs.FSOps, language string) (*TranslationTable, error) {
	if fs == nil {
		return nil, nil
	}
	data, err := fs.ReadFileLimit("gamedata/translate.tdf", 1<<20)
	if err != nil {
		if errors.Is(err, vfs.ErrNotFound) {
			return nil, nil // optional file [02 §1][02 "Translation table"]
		}
		return nil, err
	}
	doc, err := formats.ParseTDF(data)
	if err != nil {
		return nil, formats.WithTDFContext(fs, err, "gamedata/translate.tdf")
	}
	if doc.Root == nil {
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
	// Construction sorts the immutable source keys bytewise; lower-bound lookup
	// preserves exact equality and the identity fallback [02 "Translation table"].
	i := sort.Search(len(t.entries), func(i int) bool { return t.entries[i].source >= source })
	if i < len(t.entries) && t.entries[i].source == source {
		return t.entries[i].translation
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
		if asciiEqualFold(e.translation, translated) {
			return e.source, true
		}
	}
	return "", false
}

// asciiEqualFold preserves high bytes until retail's code-page comparison is
// traced. Translation forward lookup stays byte-exact; this is only its
// documented reverse case-insensitive comparison.
// TODO(question): trace retail's active code-page comparison for bytes >= 0x80.
func asciiEqualFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ac, bc := a[i], b[i]
		if ac >= 'A' && ac <= 'Z' {
			ac += 'a' - 'A'
		}
		if bc >= 'A' && bc <= 'Z' {
			bc += 'a' - 'A'
		}
		if ac != bc {
			return false
		}
	}
	return true
}
