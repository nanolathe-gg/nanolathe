package gameplay

import (
	"strings"
	"testing"
)

// stubNames stands in for the session's registry. This package is a leaf: it
// cannot import the owner of the names, so the installed view is the whole
// coupling and this is what a build gives it.
type stubNames struct{ registered []string }

func (s stubNames) Known(name string) bool {
	if name == string(Modern) || name == string(Community39) || name == string(Strict31) {
		return true
	}
	for _, have := range s.registered {
		if have == name {
			return true
		}
	}
	return false
}

func (s stubNames) Names() []string {
	return append([]string{string(Modern), string(Community39), string(Strict31)}, s.registered...)
}

// useNames installs a registry for one test and restores the previous one.
func useNames(t *testing.T, r NameRegistry) {
	t.Helper()
	previous := names
	UseNameRegistry(r)
	t.Cleanup(func() { UseNameRegistry(previous) })
}

// With no registry installed — a build below the session, such as this test
// binary by default — only the three reserved words are selectable, and the
// vocabulary behaves exactly as it did before names existed.
func TestWithoutARegistryOnlyTheReservedWordsAreSelectable(t *testing.T) {
	useNames(t, nil)
	for _, word := range []string{string(Modern), string(Community39), string(Strict31)} {
		if mode, err := Parse(word); err != nil || string(mode) != word {
			t.Fatalf("Parse(%q) = %q, %v", word, mode, err)
		}
	}
	if _, err := Parse("example"); err == nil {
		t.Fatal("a name no build registered was accepted")
	}
	if got := Mode("example").Normalize(); got != Modern {
		t.Fatalf("Normalize(%q) = %q, want %q", "example", got, Modern)
	}
	if got := Mode("").Normalize(); got != Modern {
		t.Fatalf("the zero value normalized to %q, want the default %q", got, Modern)
	}
	if got := Strict31.Normalize(); got != Strict31 {
		t.Fatalf("Normalize(%q) = %q", Strict31, got)
	}
	if got := Community39.Normalize(); got != Community39 {
		t.Fatalf("Normalize(%q) = %q", Community39, got)
	}
}

// A registered name is the vocabulary's third form: it parses, and it survives
// normalization, which is what carries it from a settings file or a flag
// through the host's options to the session that resolves it.
func TestARegisteredNameParsesAndSurvivesNormalization(t *testing.T) {
	useNames(t, stubNames{registered: []string{"example"}})
	mode, err := Parse("example")
	if err != nil {
		t.Fatalf("Parse(%q): %v", "example", err)
	}
	if mode != Mode("example") {
		t.Fatalf("Parse(%q) = %q", "example", mode)
	}
	if got := mode.Normalize(); got != mode {
		t.Fatalf("Normalize(%q) = %q; a selectable name must survive a round trip", mode, got)
	}
}

// The diagnostic names what the word could have been, in the repository's
// shape, because the caller — a command line or a settings file — is the only
// place the mistake can be corrected.
func TestAnUnselectableWordIsRejectedWithTheSelectableNames(t *testing.T) {
	useNames(t, stubNames{registered: []string{"example"}})
	mode, err := Parse("strict")
	if err == nil {
		t.Fatal("a misspelled reserved word was accepted")
	}
	if mode != Modern {
		t.Fatalf("a rejected word answered %q, want the default %q", mode, Modern)
	}
	for _, want := range []string{"nanolathe: invalid gameplay rule set", "providers searched", string(Modern), string(Community39), string(Strict31), "example"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("diagnostic %q does not carry %q", err, want)
		}
	}
}
