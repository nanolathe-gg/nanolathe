// Command loc-audit reports exact physical line counts for tracked Go files.
// It deliberately measures bytes from either the checkout or a Git tree; it
// does not estimate source size from blob bytes or depend on an external CSV.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type source string

const (
	worktree source = "worktree"
	blob     source = "blob"
)

type inputFile struct {
	path string
	data []byte
}

type groupTotals struct {
	productionFiles int
	productionLines int
	testFiles       int
	testLines       int
}

type report struct {
	source          source
	ref             string
	files           int
	productionFiles int
	productionLines int
	testFiles       int
	testLines       int
	byPackagePath   map[string]groupTotals
}

func main() {
	root := flag.String("root", ".", "repository root (also accepted as the first positional argument)")
	mode := flag.String("source", string(worktree), "input source: worktree or blob")
	ref := flag.String("ref", "HEAD", "Git tree/ref when -source=blob")
	flag.Parse()
	if flag.NArg() > 1 {
		fmt.Fprintln(os.Stderr, "loc-audit: expected at most one repository-root argument")
		os.Exit(2)
	}
	if flag.NArg() == 1 {
		*root = flag.Arg(0)
	}

	r, err := audit(*root, source(*mode), *ref)
	if err != nil {
		fmt.Fprintf(os.Stderr, "loc-audit: %v\n", err)
		os.Exit(1)
	}
	printReport(r)
}

func audit(root string, mode source, ref string) (report, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return report{}, fmt.Errorf("resolve root: %w", err)
	}
	if mode != worktree && mode != blob {
		return report{}, fmt.Errorf("unknown source %q (want worktree or blob)", mode)
	}
	paths, err := trackedGoPaths(root, mode, ref)
	if err != nil {
		return report{}, err
	}
	files := make([]inputFile, 0, len(paths))
	for _, path := range paths {
		var data []byte
		if mode == worktree {
			data, err = os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		} else {
			data, err = gitOutput(root, "cat-file", "blob", ref+":"+path)
		}
		if err != nil {
			return report{}, fmt.Errorf("read tracked Go file %s: %w", path, err)
		}
		files = append(files, inputFile{path: path, data: data})
	}
	return summarize(mode, ref, files), nil
}

func trackedGoPaths(root string, mode source, ref string) ([]string, error) {
	var args []string
	if mode == worktree {
		args = []string{"ls-files", "-z", "--", "*.go"}
	} else {
		// ls-tree's pathspec matching is not consistent across the Git
		// versions used by the supported development environments. List the
		// tree and apply the same suffix filter below as worktree mode.
		args = []string{"ls-tree", "-r", "-z", "--name-only", ref}
	}
	out, err := gitOutput(root, args...)
	if err != nil {
		return nil, fmt.Errorf("list tracked Go files: %w", err)
	}
	parts := bytes.Split(out, []byte{0})
	paths := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		path := filepath.ToSlash(string(part))
		if path == "" {
			continue
		}
		if !strings.HasSuffix(path, ".go") {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths, nil
}

func gitOutput(root string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			stderr := strings.TrimSpace(string(exit.Stderr))
			if stderr != "" {
				return nil, fmt.Errorf("git %s: %s", strings.Join(args, " "), stderr)
			}
		}
		return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

// physicalLines counts the lines represented by a file, including a final
// unterminated line. Empty files contain zero lines. This is independent of
// the host's newline convention because each LF terminates one line.
func physicalLines(data []byte) int {
	if len(data) == 0 {
		return 0
	}
	n := bytes.Count(data, []byte{'\n'})
	if data[len(data)-1] != '\n' {
		n++
	}
	return n
}

func packagePath(path string) string {
	dir := filepath.ToSlash(filepath.Dir(filepath.FromSlash(path)))
	if dir == "." {
		return "."
	}
	return dir
}

func summarize(mode source, ref string, files []inputFile) report {
	r := report{source: mode, ref: ref, files: len(files), byPackagePath: make(map[string]groupTotals)}
	for _, file := range files {
		lines := physicalLines(file.data)
		pkg := packagePath(file.path)
		g := r.byPackagePath[pkg]
		if strings.HasSuffix(file.path, "_test.go") {
			r.testFiles++
			r.testLines += lines
			g.testFiles++
			g.testLines += lines
		} else {
			r.productionFiles++
			r.productionLines += lines
			g.productionFiles++
			g.productionLines += lines
		}
		r.byPackagePath[pkg] = g
	}
	return r
}

func printReport(r report) {
	fmt.Printf("source: %s", r.source)
	if r.source == blob {
		fmt.Printf(" (%s)", r.ref)
	}
	fmt.Println()
	fmt.Printf("tracked Go files: %d\n", r.files)
	fmt.Printf("production: %d files, %d lines\n", r.productionFiles, r.productionLines)
	fmt.Printf("tests: %d files, %d lines\n", r.testFiles, r.testLines)
	fmt.Printf("total: %d files, %d lines\n", r.productionFiles+r.testFiles, r.productionLines+r.testLines)
	fmt.Println()
	fmt.Println("package path                         production files  production lines  test files  test lines")
	paths := make([]string, 0, len(r.byPackagePath))
	for path := range r.byPackagePath {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		g := r.byPackagePath[path]
		fmt.Printf("%-36s %17d %17d %11d %10d\n", path, g.productionFiles, g.productionLines, g.testFiles, g.testLines)
	}
}
