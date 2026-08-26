package cleanroom

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Patterns is the set of raw-forensics forms AGENTS.md forbids in tracked
// files. Each is deliberately narrow so ordinary hex constants, palette
// values and file-format offsets do not trip it.
//
// A pattern with a non-nil Scope applies only where Scope reports true. That
// keeps the structure-offset rule off research/formats and the formats
// package, which legitimately own byte offsets into authored files — that is
// data layout, not executable layout.
var Patterns = []struct {
	Name  string
	Re    *regexp.Regexp
	Scope func(rel string) bool
}{
	// Addresses into the retail executable's code and data segments. Bare or
	// 0x-prefixed, eight hex digits beginning 004 or 005.
	{"executable-address", regexp.MustCompile(`\b(?:0x)?00[45][0-9A-Fa-f]{5}\b`), nil},
	// Decompiler-generated symbol names.
	{"decompiler-symbol", regexp.MustCompile(`\b(?:FUN|DAT|LAB|UNK|PTR|SUB)_[0-9A-Fa-f]{6,8}\b`), nil},
	// Decompiler-generated local and parameter names.
	{"decompiler-local", regexp.MustCompile(`\b(?:[a-z]{0,3}Var[0-9]+|local_[0-9a-f]+|param_[0-9]+|in_(?:EAX|EBX|ECX|EDX|ESI|EDI))\b`), nil},
	// Structure field offsets expressed as executable layout, e.g. "def+0x22D"
	// or a bare "+0xD4". Scoped away from the file-format owners.
	{"structure-offset", regexp.MustCompile(`\+0x[0-9A-Fa-f]{2,4}\b`), simulationSource},
}

// simulationSource reports whether rel is engine source governed by the
// executable-layout rule, as opposed to a file-format owner.
func simulationSource(rel string) bool {
	if !strings.HasSuffix(rel, ".go") {
		return false
	}
	if strings.HasPrefix(rel, "formats/") || strings.HasPrefix(rel, "research/") {
		return false
	}
	return strings.HasPrefix(rel, "internal/") || strings.HasPrefix(rel, "cmd/")
}

// scanExtensions limits the walk to the text the rule governs: our source,
// our prose, and our build scripts.
var scanExtensions = map[string]bool{
	".go": true, ".md": true, ".yml": true, ".yaml": true, ".txt": true, ".sh": true,
}

// exemptPaths are files whose whole purpose is to name the forbidden forms.
var exemptPaths = map[string]bool{
	"internal/cleanroom/scan.go":      true,
	"internal/cleanroom/baseline.go":  true,
	"internal/cleanroom/scan_test.go": true,
	"internal/cleanroom/doc.go":       true,
}

// Finding is one violating line.
type Finding struct {
	Path    string // repository-relative, slash-separated
	Line    int    // 1-indexed
	Pattern string // which Patterns entry matched
}

// Scan walks root and reports every raw-forensics occurrence in tracked-file
// shapes. Dot directories are skipped: .git and the per-agent worktrees under
// .worktrees and .claude hold other branches' copies of these files.
func Scan(root string) ([]Finding, error) {
	var out []Finding
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !scanExtensions[strings.ToLower(filepath.Ext(d.Name()))] {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if exemptPaths[rel] {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for i, line := range strings.Split(string(data), "\n") {
			for _, p := range Patterns {
				if p.Scope != nil && !p.Scope(rel) {
					continue
				}
				for range p.Re.FindAllString(line, -1) {
					out = append(out, Finding{Path: rel, Line: i + 1, Pattern: p.Name})
				}
			}
		}
		return nil
	})
	return out, err
}

// CountsByFile reduces findings to the per-file census the ratchet compares.
func CountsByFile(findings []Finding) map[string]int {
	counts := make(map[string]int, len(findings))
	for _, f := range findings {
		counts[f.Path]++
	}
	return counts
}
