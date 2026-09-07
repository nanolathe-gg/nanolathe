package formats

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/vfs"
)

func TestTDFSemanticValuesTrimOnlyAuthoredWhitespace(t *testing.T) {
	doc, err := ParseTDF([]byte("[ A ] {\n" +
		"  spaced = \t\r\n value with interior  spaces \n\r\t ;\n" +
		"  empty = \t\r\n ;\n" +
		"  vertical = \v retained \f ;\n" +
		"}\n"))
	if err != nil {
		t.Fatal(err)
	}
	section := doc.Root.Section("a")
	if section == nil {
		t.Fatal("trimmed section name was not found")
	}
	for key, want := range map[string]string{
		"spaced":   "value with interior  spaces",
		"empty":    "",
		"vertical": "\v retained \f",
	} {
		got, ok := section.StringValue(key, "missing")
		if !ok || got != want {
			t.Fatalf("StringValue(%q) = (%q, %t), want (%q, true)", key, got, ok, want)
		}
	}
}

func TestTDFSemanticTrimRetainsPresentEmptyKey(t *testing.T) {
	doc, err := ParseTDF([]byte("[A]{ \t\r\n = value; }"))
	if err != nil {
		t.Fatal(err)
	}
	section := doc.Root.Section("A")
	if got, ok := section.FirstValue(""); !ok || got != "value" {
		t.Fatalf("empty authored key = (%q, %t), want (value, true)", got, ok)
	}
}

func TestTDFByteNamesPreserveHighBytesAndASCIICase(t *testing.T) {
	doc, err := ParseTDF([]byte("[\xc0]\n{\n\xc0=one;\n\xe0=two;\nAscii=first;\nascii=last;\n}\n"))
	if err != nil {
		t.Fatal(err)
	}
	section := doc.Root.Section("\xc0")
	if section == nil {
		t.Fatal("high-byte section was not found")
	}
	if got, _ := section.FirstValue("\xc0"); got != "one" {
		t.Fatalf("first high-byte key = %q, want one", got)
	}
	if got, _ := section.FirstValue("\xe0"); got != "two" {
		t.Fatalf("second high-byte key = %q, want two", got)
	}
	if got, _ := section.FirstValue("ASCII"); got != "last" {
		t.Fatalf("ASCII case variant = %q, want last parsed variant", got)
	}
}

func TestTDFGrammarScansForwardAndTopLevelTerminatorStops(t *testing.T) {
	doc, err := ParseTDF([]byte("[ multi\nline ] \n {\n key[ still } = value;\n}\n}\n[ignored]{key=no;}"))
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Root.Sections()) != 1 {
		t.Fatalf("top-level terminator retained %d sections, want 1", len(doc.Root.Sections()))
	}
	section := doc.Root.Section("multi\nline")
	if section == nil {
		t.Fatal("multiline header was not parsed")
	}
	if got, ok := section.FirstValue("key[ still }"); !ok || got != "value" {
		t.Fatalf("forward-scanned assignment = (%q, %t), want (value, true)", got, ok)
	}
}

func TestTDFDiagnosticsCarryExactReasonSectionAndLogicalFile(t *testing.T) {
	cases := []struct {
		name       string
		input      string
		diagnostic ParseDiagnostic
		section    string
	}{
		{"missing equals", "[A]{ field }", DiagEqualsNotFound, "A"},
		{"missing semicolon", "[A]{ field=value", DiagSemicolonMissing, "A"},
		{"missing closing bracket", "[A", DiagClosingBracket, "root"},
		{"missing opening brace", "[A] field=value;", DiagOpeningBrace, "root"},
		{"unterminated section", "[A]{ field=value;", DiagNextBlock, "A"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			const logical = "gamedata/authored.tdf"
			fs := tdfFS(t, map[string]string{logical: test.input})
			_, err := LoadTDF(fs, logical)
			var parseErr *ParseError
			if !errors.As(err, &parseErr) {
				t.Fatalf("LoadTDF error = %v, want ParseError", err)
			}
			if parseErr.Diagnostic != test.diagnostic {
				t.Fatalf("diagnostic = %q, want %q", parseErr.Diagnostic, test.diagnostic)
			}
			want := string(ParseErrorTitle) + " " + string(test.diagnostic) + " - name = '" + test.section + "' from file " + logical
			if got := parseErr.Error(); got != want {
				t.Fatalf("error = %q, want %q", got, want)
			}
			if !strings.HasPrefix(err.Error(), "nanolathe: TDF load failed:") || !strings.Contains(err.Error(), "providers searched [gamedata/authored.tdf]") {
				t.Fatalf("loader diagnostic lost winning provider: %v", err)
			}
		})
	}
}

func TestTDFCommentBlankingKeepsDiagnosticOffset(t *testing.T) {
	data := "// comment\n[A]{ value"
	_, err := ParseTDF([]byte(data))
	var parseErr *ParseError
	if !errors.As(err, &parseErr) {
		t.Fatalf("ParseTDF error = %v, want ParseError", err)
	}
	if parseErr.Diagnostic != DiagEqualsNotFound || parseErr.Offset != len(data) {
		t.Fatalf("commented diagnostic = (%q, offset %d), want (%q, offset %d)", parseErr.Diagnostic, parseErr.Offset, DiagEqualsNotFound, len(data))
	}
}

func TestTDFSemanticTrimDoesNotUseUnicodeWhitespace(t *testing.T) {
	doc, err := ParseTDF([]byte("[A]{\n\vkey=1;\n}\n"))
	if err != nil {
		t.Fatal(err)
	}
	section := doc.Root.Section("A")
	if section == nil {
		t.Fatal("section A missing")
	}
	if _, ok := section.FirstValue("key"); ok {
		t.Fatal("vertical-tab key was treated as semantic whitespace")
	}
	if got, ok := section.FirstValue("\vkey"); !ok || got != "1" {
		t.Fatalf("vertical-tab key = (%q, %t), want (1, true)", got, ok)
	}
}

func TestTDFDiagnosticDoesNotDropReason(t *testing.T) {
	_, err := ParseTDF([]byte("[A]{broken}"))
	if err == nil || !strings.Contains(err.Error(), string(DiagEqualsNotFound)) {
		t.Fatalf("parse error = %v, want exact equals diagnostic", err)
	}
}

func tdfFS(t *testing.T, files map[string]string) *vfs.FS {
	t.Helper()
	dir := t.TempDir()
	for logical, data := range files {
		full := filepath.Join(dir, filepath.FromSlash(logical))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fs := vfs.New()
	if err := fs.MountDirectory(dir, 1); err != nil {
		t.Fatal(err)
	}
	return fs
}
