package cleanroom

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("could not locate repository root from %s", dir)
	return ""
}

// TestCleanRoom_Ratchet enforces AGENTS.md §"Clean-room discipline": no
// executable addresses, decompiler-generated symbol or local names, or
// structure offsets expressed as executable layout may enter a tracked file.
//
// Existing violations are recorded per file in Baseline. A file may not gain
// occurrences, and a file not listed may have none — so no new raw-forensics
// text can be committed. Falling below a baseline entry is progress: it is
// logged, not failed, so that cleaning a file never requires editing this
// register in the same commit. TestCleanRoom_Census reports the standing
// total so the trend still shows up in a CI log.
func TestCleanRoom_Ratchet(t *testing.T) {
	root := repoRoot(t)
	findings, err := Scan(root)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	counts := CountsByFile(findings)

	// Line detail for the files that moved, so a failure names the sites.
	linesFor := func(path string) []string {
		var out []string
		for _, f := range findings {
			if f.Path == path {
				out = append(out, f.Pattern)
			}
		}
		return out
	}

	for path, got := range counts {
		want, listed := Baseline[path]
		if !listed {
			t.Errorf("%s: %d raw-forensics occurrence(s) in a file with no baseline entry (%v). "+
				"AGENTS.md forbids committing executable addresses, decompiler-generated names, "+
				"or executable structure offsets; describe what the algorithm does instead and "+
				"keep the address trail in /tmp/ta-decompile/notes/.", path, got, linesFor(path))
			continue
		}
		if got > want {
			t.Errorf("%s: raw-forensics occurrences rose from %d to %d. The lint is a ratchet: "+
				"counts may only go down.", path, want, got)
		}
		if got < want {
			// Shrinking is the point of the register, so it is reported, not
			// failed. Making a cleanup commit also edit this table taxed every
			// removal and, in practice, is what kept the gate red.
			t.Logf("%s: raw-forensics occurrences fell from %d to %d; lower its Baseline "+
				"entry when convenient (regenerate with `go run ./tools/cleanroom-baseline .`).",
				path, want, got)
		}
	}
	for path, want := range Baseline {
		if _, ok := counts[path]; ok {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(path))); os.IsNotExist(err) {
			t.Errorf("Baseline lists %s (%d) but the file no longer exists; drop the entry.", path, want)
			continue
		}
		t.Logf("%s is now clean (baseline %d); its Baseline entry can be dropped.", path, want)
	}
}

// TestCleanRoom_Census reports the remaining debt so a CI log carries the
// number and its trend. It is a report, not an assertion.
func TestCleanRoom_Census(t *testing.T) {
	total := 0
	paths := make([]string, 0, len(Baseline))
	for p, n := range Baseline {
		total += n
		paths = append(paths, p)
	}
	sort.Strings(paths)
	t.Logf("clean-room debt: %d occurrence(s) across %d file(s)", total, len(paths))
	worst := paths
	sort.SliceStable(worst, func(i, j int) bool { return Baseline[worst[i]] > Baseline[worst[j]] })
	limit := 10
	if len(worst) < limit {
		limit = len(worst)
	}
	for _, p := range worst[:limit] {
		t.Logf("  %5d  %s", Baseline[p], p)
	}
}

// TestCleanRoom_PatternsMatchKnownForms locks the shapes the scanner must
// catch, so a later regex edit cannot quietly narrow it.
func TestCleanRoom_PatternsMatchKnownForms(t *testing.T) {
	cases := []struct {
		text    string
		rel     string
		pattern string
	}{
		{"the handler at 0043D0D0 clears the flag", "internal/x/a.go", "executable-address"},
		{"see 0x004866D0 for the walk", "internal/x/a.go", "executable-address"},
		{"FUN_00496E90 dispatches", "internal/x/a.go", "decompiler-symbol"},
		{"DAT_00511DE8 holds the count", "research/retail-executable-spec/01-x.md", "decompiler-symbol"},
		{"uVar3 becomes the mask", "internal/x/a.go", "decompiler-local"},
		{"local_28 sorted order", "internal/x/a.go", "decompiler-local"},
		{"makesmetal byte at def+0x22D", "internal/x/a.go", "structure-offset"},
	}
	for _, c := range cases {
		hit := false
		for _, p := range Patterns {
			if p.Scope != nil && !p.Scope(c.rel) {
				continue
			}
			if p.Name == c.pattern && p.Re.MatchString(c.text) {
				hit = true
			}
		}
		if !hit {
			t.Errorf("pattern %s did not match %q in %s", c.pattern, c.text, c.rel)
		}
	}

	// Byte offsets in the file-format owners are authored data layout, not
	// executable layout, and must not trip the structure-offset rule.
	for _, rel := range []string{"formats/three_do.go", "research/formats/tnt.md"} {
		for _, p := range Patterns {
			if p.Name != "structure-offset" {
				continue
			}
			if p.Scope == nil || p.Scope(rel) {
				t.Errorf("structure-offset rule must not apply to file-format owner %s", rel)
			}
		}
	}
}
