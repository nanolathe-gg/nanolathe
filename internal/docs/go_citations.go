package docs

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// GoSourceDirs are the directories whose Go comments cite research. Anything
// outside them (tools' generated content, fixtures) is not a citing surface.
var GoSourceDirs = []string{"internal", "cmd", "vfs", "formats", "probes", "tools"}

// goWalkSkip are directory names never descended into: authored fixtures,
// version-control metadata, and nested agent worktrees whose files are not
// part of this module.
var goWalkSkip = map[string]bool{
	".git":       true,
	".worktrees": true,
	"worktrees":  true,
	"testdata":   true,
}

// DanglingInGo returns every research citation in a Go comment under dirs
// (relative to root) that does not resolve, in the same four forms Dangling
// checks in the documents. Citations in string literals are not scanned: a
// comment is where a contract is cited, and a diagnostic message that quotes
// one is prose about a citation, not a citation.
//
// The scan is one directory walk and one regular expression per comment line;
// it does not parse Go. A line's comment part is everything after the first
// `//`, or the whole line while inside a `/* ... */` block.
func DanglingInGo(root string, dirs []string) ([]Citation, error) {
	docs, formats, err := indexResearch(root)
	if err != nil {
		return nil, err
	}
	var bad []Citation
	for _, dir := range dirs {
		base := filepath.Join(root, dir)
		if _, err := os.Stat(base); err != nil {
			continue
		}
		err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if path != base && (goWalkSkip[d.Name()] || hasGitEntry(path)) {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(d.Name(), ".go") {
				return nil
			}
			found, err := scanGoFile(path, docs, formats)
			if err != nil {
				return err
			}
			for _, c := range found {
				c.File, _ = filepath.Rel(root, path)
				bad = append(bad, c)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(bad, func(i, j int) bool {
		if bad[i].File != bad[j].File {
			return bad[i].File < bad[j].File
		}
		return bad[i].Line < bad[j].Line
	})
	return bad, nil
}

// hasGitEntry reports whether dir is a nested repository root — a linked
// worktree leaves a .git file, a clone a .git directory.
func hasGitEntry(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, ".git"))
	return err == nil
}

// scanGoFile returns the dangling citations in one file's comments. The Text
// of each result is the bracket contents alone (no reason appended), so a
// baseline can be keyed by the citation rather than by its location.
func scanGoFile(path string, docs map[string]research, formats map[string]bool) ([]Citation, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Citation
	inBlock := false
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		comment, next := commentPart(line, inBlock)
		inBlock = next
		if comment == "" || !strings.Contains(comment, "[") {
			continue
		}
		for _, loc := range bracket.FindAllStringSubmatchIndex(comment, -1) {
			text := strings.TrimSpace(comment[loc[2]:loc[3]])
			if resolve(text, docs, formats) != "" {
				out = append(out, Citation{Line: n, Text: text})
			}
		}
	}
	return out, sc.Err()
}

// commentPart returns the portion of line that is comment text, and whether
// the next line starts inside a block comment. A `//` inside a string literal
// is misread as a comment start; the cost is scanning a few extra brackets,
// never missing a comment.
func commentPart(line string, inBlock bool) (string, bool) {
	if inBlock {
		if end := strings.Index(line, "*/"); end >= 0 {
			rest, next := commentPart(line[end+2:], false)
			return line[:end] + " " + rest, next
		}
		return line, true
	}
	slash := strings.Index(line, "//")
	block := strings.Index(line, "/*")
	switch {
	case block >= 0 && (slash < 0 || block < slash):
		rest, next := commentPart(line[block+2:], true)
		return rest, next
	case slash >= 0:
		return line[slash+2:], false
	}
	return "", false
}

// ReadBaseline parses a baseline file: one citation text per line, `#` lines
// and blank lines ignored. The returned set is keyed by the bracket contents.
func ReadBaseline(path string) (map[string]bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		set[line] = true
	}
	return set, nil
}
