package formats

import (
	"errors"
	"fmt"

	"github.com/nanolathe-gg/nanolathe/vfs"
)

// ParseDiagnostic is one of retail's five TDF parse diagnostics [02 §4][P1-12].
// The strings are reproduced verbatim; the title and the detail line shape
// belong to the same message-box family [02 §4].
type ParseDiagnostic string

const (
	DiagEqualsNotFound   ParseDiagnostic = "Data field - '=' not found"
	DiagSemicolonMissing ParseDiagnostic = "Data field - ';' not found"
	DiagClosingBracket   ParseDiagnostic = "Sub-record - closing ']' not found"
	DiagOpeningBrace     ParseDiagnostic = "Sub-record - opening '{' not found"
	DiagNextBlock        ParseDiagnostic = "End of file - nextblock not zero"
)

// ParseErrorTitle is the exact title retail reports parse failures under [P1-12][02 §4].
const ParseErrorTitle = "Parse error in .TDF File!"

// ParseError carries a diagnostic plus the context retail prints with it.
type ParseError struct {
	Diagnostic ParseDiagnostic
	Name       string // retail's literal `name`
	Value      string // section active when the parse failed
	File       string // logical path, filled in by the loader
	Offset     int    // byte offset into the (comment-blanked) source
	Line       int
	Column     int
}

// Error renders the retail parse-failure line verbatim [02 §4].
func (e *ParseError) Error() string {
	return fmt.Sprintf("%s %s%s", ParseErrorTitle, e.Diagnostic, e.Detail())
}

// Detail renders retail's detail line: ` - name = '<section>' from file <file>`.
func (e *ParseError) Detail() string {
	return fmt.Sprintf(" - %s = '%s' from file %s", e.Name, e.Value, e.File)
}

// WithFile returns a copy naming the logical path. Loaders call this because
// the parser itself has no provenance.
func (e *ParseError) WithFile(file string) *ParseError {
	clone := *e
	clone.File = file
	return &clone
}

// WithTDFFile attaches a logical filename to a parser diagnostic while
// preserving non-parser errors unchanged. Content loaders use this one route
// before adding their provider-bearing outer context [02 §4].
func WithTDFFile(err error, file string) error {
	var parseErr *ParseError
	if errors.As(err, &parseErr) {
		return parseErr.WithFile(file)
	}
	return err
}

// WithTDFContext keeps the parser's verbatim diagnostic inside the loader's
// logical path and winning-provider context. Metadata lookup does not reopen
// or decode the authored bytes [02 §4][AGENTS.md diagnostics].
func WithTDFContext(fs vfs.FSOps, err error, logical string) error {
	if err == nil {
		return nil
	}
	provider := ""
	if fs != nil {
		if info, statErr := fs.Stat(logical); statErr == nil {
			provider = info.Source.ProviderID()
			if provider == "" {
				provider = "unknown"
			}
		}
	}
	return fmt.Errorf("nanolathe: TDF load failed: logical path %s, providers searched [%s], expected valid TDF document: %w", logical, provider, WithTDFFile(err, logical))
}

// blankComments overwrites comment spans with ASCII spaces, preserving every
// character offset so downstream offsets see text of unchanged length
// [02 §4][P1-12]. Newlines inside block comments are preserved so reported line
// numbers stay meaningful; retail only guarantees offsets, and keeping the
// newline preserves both. This is the verbatim comment-blanking contract
// that preserves offsets for duplicate-section handling [P1-12].
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
