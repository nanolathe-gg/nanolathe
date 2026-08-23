package formats

import (
	"fmt"
	"strings"
)

// ParseDiagnostic is one of retail's five TDF parse diagnostics [02 §4].
// The strings are reproduced verbatim; the title and the detail line shape
// belong to the same message-box family.
type ParseDiagnostic string

const (
	DiagEqualsNotFound   ParseDiagnostic = "Data field - '=' not found"
	DiagSemicolonMissing ParseDiagnostic = "Data field - ';' not found"
	DiagClosingBracket   ParseDiagnostic = "Sub-record - closing ']' not found"
	DiagOpeningBrace     ParseDiagnostic = "Sub-record - opening '{' not found"
	DiagNextBlock        ParseDiagnostic = "End of file - nextblock not zero"
)

// ParseErrorTitle is the exact title retail reports parse failures under.
const ParseErrorTitle = "Parse error in .TDF File!"

// ParseError carries a diagnostic plus the context retail prints with it.
// A failed load yields a valid-but-empty tree rather than terminating, so
// callers decide whether the failure is fatal for their resource family
// [02 §4].
type ParseError struct {
	Diagnostic ParseDiagnostic
	Name       string // the record or key being read when the parse failed
	Value      string // the partial value, when one had been scanned
	File       string // logical path, filled in by the loader
	Offset     int    // byte offset into the (comment-blanked) source
	Line       int
	Column     int
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("%s %s", ParseErrorTitle, e.Detail())
}

// Detail renders retail's detail line: ` - <name> = '<value>' from file <file>`.
func (e *ParseError) Detail() string {
	var b strings.Builder
	b.WriteString(" - ")
	b.WriteString(e.Name)
	b.WriteString(" = '")
	b.WriteString(e.Value)
	b.WriteString("' from file ")
	b.WriteString(e.File)
	return b.String()
}

// WithFile returns a copy naming the logical path. Loaders call this because
// the parser itself has no provenance.
func (e *ParseError) WithFile(file string) *ParseError {
	clone := *e
	clone.File = file
	return &clone
}

// blankComments overwrites comment spans with ASCII spaces, preserving every
// character offset so downstream offsets see text of unchanged length
// [02 §4]. Newlines inside block comments are preserved so reported line
// numbers stay meaningful; retail only guarantees offsets, and keeping the
// newline preserves both.
//
// Rules: `//` blanks to end of line; `/* */` blanks the span; an unterminated
// `/*` blanks everything through end of file. Comments cannot appear inside a
// value, and trailing comments after `;` are blanked too — both fall out of
// blanking before the grammar runs.
func blankComments(src []byte) []byte {
	out := make([]byte, len(src))
	copy(out, src)
	for i := 0; i < len(out); i++ {
		if out[i] != '/' || i+1 >= len(out) {
			continue
		}
		switch out[i+1] {
		case '/':
			for j := i; j < len(out) && out[j] != '\n'; j++ {
				out[j] = ' '
			}
		case '*':
			end := len(out)
			for j := i + 2; j+1 < len(out); j++ {
				if out[j] == '*' && out[j+1] == '/' {
					end = j + 2
					break
				}
			}
			for j := i; j < end; j++ {
				if out[j] != '\n' {
					out[j] = ' '
				}
			}
			i = end - 1
		}
	}
	return out
}
