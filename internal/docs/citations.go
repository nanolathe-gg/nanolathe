// Package docs checks that the design documents cite research that exists.
//
// A citation is the only link between a unit of work and the contract it must
// implement. A dangling one sends a sub-agent to a section that is not there,
// and the usual outcome is an invented constant — the one unrecoverable
// failure mode in AGENTS.md §"The four rules". This package resolves every
// citation mechanically so that never happens silently.
//
// The four citation forms are the ones docs/ARCHITECTURE.md §"Citation
// conventions" defines: `[04 §7.2]` numbered section, `[05 "Player slot"]`
// heading text, `[08 R-AI-01 §3]` inline addendum anchor, and `[fmt tnt]`
// format document.
package docs

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Citation is one reference found in a design document.
type Citation struct {
	File string
	Line int
	Text string // the bracket contents, e.g. `04 §7.2`
}

// String renders a citation as "file:line: [text]".
func (c Citation) String() string {
	return fmt.Sprintf("%s:%d: [%s]", c.File, c.Line, c.Text)
}

// research indexes one category document's addressable headings.
type research struct {
	sections map[string]bool // "7.2"
	headings map[string]bool // normalized heading text
	anchors  map[string]bool // "R-AI-01"
}

var (
	bracket     = regexp.MustCompile(`\[([^\[\]]{1,160}?)\]`)
	docCite     = regexp.MustCompile(`^(\d\d)\s+(.*)$`)
	fmtCite     = regexp.MustCompile(`^fmt\s+([a-z0-9]+)`)
	anchorCite  = regexp.MustCompile(`^(R-[A-Z0-9]+-\d+[A-Z]?)`)
	anchorAny   = regexp.MustCompile(`R-[A-Z0-9]+-\d+[A-Z]?`)
	sectionCite = regexp.MustCompile(`§\s*([\d.]+)`)
	numHeading  = regexp.MustCompile(`^(\d+(?:\.\d+)*)[.)]?\s+(.*)$`)
	trailingTag = regexp.MustCompile(`\s*(\[[^\[\]]*\]|\((?:19|20)\d\d-\d\d-\d\d\))$`)
	quoted      = regexp.MustCompile(`^["\x{201c}](.+)["\x{201d}]$`)
)

// normHeading reduces a heading, or a citation's quoted heading text, to the
// form both sides are compared in: trailing `[P0-05]`-style tags and closure
// dates removed, emphasis and quote characters dropped, lowercased.
func normHeading(s string) string {
	s = strings.TrimSpace(s)
	for {
		trimmed := strings.TrimSpace(trailingTag.ReplaceAllString(s, ""))
		if trimmed == s {
			break
		}
		s = trimmed
	}
	s = strings.NewReplacer("`", "", "*", "", "_", "", `"`, "", "“", "", "”", "").Replace(s)
	return strings.ToLower(strings.Trim(strings.TrimSpace(s), ",."))
}

// headingKeys returns every form a heading may be cited by: the whole text,
// and the part before an em-dash or colon qualifier ("Campaign discovery" for
// "Campaign discovery — Established [P0-05]").
func headingKeys(body string) []string {
	keys := []string{normHeading(body)}
	for _, sep := range []string{" — ", " – ", " -- ", ": "} {
		if i := strings.Index(body, sep); i > 0 {
			keys = append(keys, normHeading(body[:i]))
		}
	}
	return keys
}

func indexResearch(root string) (map[string]research, map[string]bool, error) {
	specs, err := filepath.Glob(filepath.Join(root, "research", "retail-executable-spec", "0*.md"))
	if err != nil {
		return nil, nil, err
	}
	docs := make(map[string]research, len(specs))
	for _, path := range specs {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, nil, err
		}
		r := research{sections: map[string]bool{}, headings: map[string]bool{}, anchors: map[string]bool{}}
		for _, line := range strings.Split(string(raw), "\n") {
			if !strings.HasPrefix(line, "#") {
				continue
			}
			body := strings.TrimSpace(strings.TrimLeft(line, "#"))
			if m := numHeading.FindStringSubmatch(body); m != nil {
				r.sections[m[1]] = true
				body = m[2]
			}
			for _, k := range headingKeys(body) {
				r.headings[k] = true
			}
		}
		for _, a := range anchorAny.FindAllString(string(raw), -1) {
			r.anchors[a] = true
		}
		docs[filepath.Base(path)[:2]] = r
	}
	formats := map[string]bool{}
	names, err := filepath.Glob(filepath.Join(root, "research", "formats", "*.md"))
	if err != nil {
		return nil, nil, err
	}
	for _, path := range names {
		formats[strings.TrimSuffix(filepath.Base(path), ".md")] = true
	}
	return docs, formats, nil
}

// CitingFiles lists the documents whose citations are checked: everything in
// docs/, which is the architecture document and the design documents.
func CitingFiles(root string) ([]string, error) {
	files, err := filepath.Glob(filepath.Join(root, "docs", "*.md"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

// Dangling returns every citation in the design documents that does not resolve
// to a heading, section number, anchor, or format document in research/. The
// returned paths are relative to root.
func Dangling(root string) ([]Citation, error) {
	docs, formats, err := indexResearch(root)
	if err != nil {
		return nil, err
	}
	if len(docs) == 0 {
		return nil, fmt.Errorf("nanolathe: no category documents: logical path research/retail-executable-spec/0*.md, root %s, expected eight category documents", root)
	}
	files, err := CitingFiles(root)
	if err != nil {
		return nil, err
	}
	var bad []Citation
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		rel, _ := filepath.Rel(root, path)
		for n, line := range strings.Split(string(raw), "\n") {
			for _, loc := range bracket.FindAllStringSubmatchIndex(line, -1) {
				// A `]` followed by `(` is a markdown link, not a citation.
				if loc[1] < len(line) && line[loc[1]] == '(' {
					continue
				}
				text := strings.TrimSpace(line[loc[2]:loc[3]])
				if why := resolve(text, docs, formats); why != "" {
					bad = append(bad, Citation{File: rel, Line: n + 1, Text: text + " — " + why})
				}
			}
		}
	}
	return bad, nil
}

// resolve returns "" when the bracket text is not a citation or is a citation
// that resolves, and otherwise says what is missing.
func resolve(text string, docs map[string]research, formats map[string]bool) string {
	if m := fmtCite.FindStringSubmatch(text); m != nil {
		if !formats[m[1]] {
			return "no research/formats/" + m[1] + ".md"
		}
		return ""
	}
	m := docCite.FindStringSubmatch(text)
	if m == nil {
		return ""
	}
	num, rest := m[1], strings.TrimSpace(m[2])
	doc, ok := docs[num]
	if !ok {
		return "no category document " + num
	}
	if a := anchorCite.FindStringSubmatch(rest); a != nil {
		if !doc.anchors[a[1]] {
			return a[1] + " is not in document " + num
		}
		return ""
	}
	if strings.HasPrefix(rest, "§") {
		for _, s := range sectionCite.FindAllStringSubmatch(rest, -1) {
			if n := strings.TrimSuffix(s[1], "."); !doc.sections[n] {
				return "§" + n + " is not in document " + num
			}
		}
		return ""
	}
	if q := quoted.FindStringSubmatch(rest); q != nil {
		if !doc.headings[normHeading(q[1])] {
			return fmt.Sprintf("no heading %q in document %s", q[1], num)
		}
	}
	return ""
}

// Finding is one anchored research finding — an `R-<id>` that heads a section
// of a category document, i.e. a contract somebody traced and wrote up.
type Finding struct {
	Doc    string // "04"
	Anchor string // "R-P0-09"
}

// String renders a finding as the citation form documents use, "[04 R-P0-09]".
func (f Finding) String() string { return "[" + f.Doc + " " + f.Anchor + "]" }

// UncitedFindings returns the anchored findings that no design document cites.
// An uncited finding is a traced contract with no implementation home: it is
// either work nobody designed, or a design document that has not caught up
// with a research drop. Either way somebody has to decide, which is why this
// is checked and not merely counted.
func UncitedFindings(root string) ([]Finding, error) {
	specs, err := filepath.Glob(filepath.Join(root, "research", "retail-executable-spec", "0*.md"))
	if err != nil {
		return nil, err
	}
	files, err := CitingFiles(root)
	if err != nil {
		return nil, err
	}
	cited := map[string]bool{}
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		for _, a := range anchorAny.FindAllString(string(raw), -1) {
			cited[a] = true
		}
	}
	var out []Finding
	for _, path := range specs {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		doc := filepath.Base(path)[:2]
		seen := map[string]bool{}
		for _, line := range strings.Split(string(raw), "\n") {
			if !strings.HasPrefix(line, "#") {
				continue
			}
			for _, a := range anchorAny.FindAllString(line, -1) {
				if cited[a] || seen[a] {
					continue
				}
				seen[a] = true
				out = append(out, Finding{Doc: doc, Anchor: a})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Doc != out[j].Doc {
			return out[i].Doc < out[j].Doc
		}
		return out[i].Anchor < out[j].Anchor
	})
	return out, nil
}
