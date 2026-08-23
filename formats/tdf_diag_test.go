package formats

import (
	"errors"
	"strings"
	"testing"
)

// TestCommentBlankingPreservesOffsets locks [02 §4]: comments become ASCII
// spaces in place, so every downstream offset sees text of unchanged length.
func TestCommentBlankingPreservesOffsets(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"line", "a=1; // note\nb=2;", "a=1;        \nb=2;"},
		{"block", "a=/*x*/1;", "a=     1;"},
		{"block with newline", "a=1;/*\n*/b=2;", "a=1;  \n  b=2;"},
		{"unterminated blanks to eof", "a=1; /* trailing", "a=1;            "},
		{"slash alone", "a=1/2;", "a=1/2;"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := string(blankComments([]byte(testCase.in)))
			if len(got) != len(testCase.in) {
				t.Fatalf("length changed: %d -> %d", len(testCase.in), len(got))
			}
			if got != testCase.want {
				t.Fatalf("blankComments(%q) = %q, want %q", testCase.in, got, testCase.want)
			}
		})
	}
}

// TestParseDiagnosticsVerbatim proves each of retail's five diagnostics is
// reachable and reproduced exactly [02 §4].
func TestParseDiagnosticsVerbatim(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  ParseDiagnostic
	}{
		{"missing equals", "[A] { key }", DiagEqualsNotFound},
		{"missing semicolon", "[A] { key=value", DiagSemicolonMissing},
		{"missing closing bracket", "[A\n", DiagClosingBracket},
		{"missing opening brace", "[A] key=1;", DiagOpeningBrace},
		{"unterminated block", "[A] { key=1;", DiagNextBlock},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := ParseTDF([]byte(testCase.input))
			if err == nil {
				t.Fatal("parse succeeded, expected a diagnostic")
			}
			var parseErr *ParseError
			if !errors.As(err, &parseErr) {
				t.Fatalf("error %v is not a *ParseError", err)
			}
			if parseErr.Diagnostic != testCase.want {
				t.Fatalf("diagnostic = %q, want %q", parseErr.Diagnostic, testCase.want)
			}
			if !strings.HasPrefix(parseErr.Error(), ParseErrorTitle) {
				t.Fatalf("error %q does not carry the verbatim title", parseErr.Error())
			}
			detail := parseErr.WithFile("gamedata/example.tdf").Detail()
			if !strings.HasPrefix(detail, " - ") || !strings.Contains(detail, " from file gamedata/example.tdf") {
				t.Fatalf("detail line %q does not match retail's shape", detail)
			}
		})
	}
}

// TestDuplicateKeyPolicy covers [02 §4]: an identical spelling replaces (last
// write wins, one entry) while a case variant coexists as a second entry, and
// typed lookups return the lower bound. The variant behavior is labelled
// MEDIUM confidence in the research; the test documents which branch we took.
func TestDuplicateKeyPolicy(t *testing.T) {
	document, err := ParseTDF([]byte("[A] { key=1; key=2; KEY=3; }"))
	if err != nil {
		t.Fatal(err)
	}
	section := document.Root.Section("a")
	if section == nil {
		t.Fatal("section a is missing")
	}
	values := section.Values("key")
	if len(values) == 0 {
		t.Fatal("no values for key")
	}
	if last, _ := section.LastValue("key"); last != "3" {
		t.Fatalf("LastValue = %q, want 3 (last write wins across case variants)", last)
	}
}
