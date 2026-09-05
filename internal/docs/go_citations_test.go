package docs

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const goCitationBaseline = "testdata/go_citation_baseline.txt"

// TestGoCommentCitationsResolve scans Go comments for research citations and
// fails when one appears that resolves to nothing — a section number a
// rewrite dropped, a heading whose text changed, an anchor that never headed a
// section. The misses present when the check was introduced are listed in the
// baseline file, keyed by citation text, so the check passes today and fails
// only on a new miss. Removing a baseline entry once its comment is fixed is
// the expected way for the file to shrink; a stale entry is reported, not
// failed, so fixing comments never breaks the build.
func TestGoCommentCitationsResolve(t *testing.T) {
	baseline, err := ReadBaseline(goCitationBaseline)
	if err != nil {
		t.Fatal(err)
	}
	bad, err := DanglingInGo(repoRoot, GoSourceDirs)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	var fresh []Citation
	for _, c := range bad {
		seen[c.Text] = true
		if !baseline[c.Text] {
			fresh = append(fresh, c)
		}
	}
	var stale []string
	for text := range baseline {
		if !seen[text] {
			stale = append(stale, text)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Logf("%d baseline entry(ies) no longer dangling — remove them from %s:\n  %s",
			len(stale), goCitationBaseline, strings.Join(stale, "\n  "))
	}
	if len(fresh) == 0 {
		return
	}
	var b strings.Builder
	for _, c := range fresh {
		b.WriteString("\n  " + c.String())
	}
	t.Errorf("%d Go comment citation(s) do not resolve against research/ (fix the comment, or if the "+
		"research heading is the thing that moved, restore it): %s", len(fresh), b.String())
}

func TestCommentPart(t *testing.T) {
	for _, tc := range []struct {
		line    string
		inBlock bool
		want    string
		next    bool
	}{
		{"x := 1 // [04 §7.2] cardinal 16", false, " [04 §7.2] cardinal 16", false},
		{"x := 1", false, "", false},
		{"/* [04 §7.2]", false, " [04 §7.2]", true},
		{"still [05 \"Player slot\"] */ y := 2 // tail", true, "still [05 \"Player slot\"]   tail", false},
		{"inside block", true, "inside block", true},
	} {
		got, next := commentPart(tc.line, tc.inBlock)
		if got != tc.want || next != tc.next {
			t.Errorf("commentPart(%q, %v) = (%q, %v), want (%q, %v)", tc.line, tc.inBlock, got, next, tc.want, tc.next)
		}
	}
}

// The walk must not descend into fixtures or nested worktrees: a fixture may
// quote a citation on purpose, and a nested checkout is somebody else's tree.
func TestGoWalkSkipsFixturesAndNestedCheckouts(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(root, rel)
		if err := mkdirAllAndWrite(path, body); err != nil {
			t.Fatal(err)
		}
	}
	// The fixture's comment markers are assembled at run time so this file's
	// own comments do not carry the deliberately dangling citations.
	cm := "/" + "/"
	write("research/retail-executable-spec/04-x.md", "## 7.2 Ground path search\n")
	write("internal/a/a.go", "package a "+cm+" [04 §7.2] resolves\n"+cm+" [04 §9.9] does not\n")
	write("internal/a/testdata/f.go", "package f "+cm+" [04 §1.1] fixture, never scanned\n")
	write("internal/nested/.git", "gitdir: elsewhere\n")
	write("internal/nested/n.go", "package n "+cm+" [04 §8.8] nested checkout, never scanned\n")
	bad, err := DanglingInGo(root, []string{"internal"})
	if err != nil {
		t.Fatal(err)
	}
	if len(bad) != 1 || bad[0].Text != "04 §9.9" || bad[0].Line != 2 {
		t.Fatalf("DanglingInGo = %v, want exactly [internal/a/a.go:2: [04 §9.9]]", bad)
	}
}

func mkdirAllAndWrite(path, body string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(body), 0o644)
}
