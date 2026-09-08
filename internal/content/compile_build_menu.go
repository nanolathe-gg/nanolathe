// The build-menu catalog compiler.

package content

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/vfs"
)

// BuildMenuPage is one builder's ordered button list [02 "Build-menu catalog keys"].
//
// Per-unit numbered pages derive from `CANBUILD %s` sections holding numbered
// `canbuild%d` keys enumerated from 1 upward until the first absent key. Buttons are stored verbatim as
// authored; consumers compare case-insensitively via CanonicalKey.
type BuildMenuPage struct {
	DefinitionHeader
	Builder         string   // the CANBUILD child-section name, verbatim
	Buttons         []string // final authoritative products: base first, downloads appended [02 R-CAT-01 §8]
	BaseButtonCount int      // prefix authored by CANBUILD before download extension [02 R-CAT-01 §5,§8]
}

// BaseButtons returns a copy of the CANBUILD-authored prefix. Generated pages
// use explicit DownloadMenuPlacement slots instead of slicing appended products.
func (p *BuildMenuPage) BaseButtons() []string {
	if p == nil || p.BaseButtonCount <= 0 || len(p.Buttons) == 0 {
		return nil
	}
	count := p.BaseButtonCount
	if count > len(p.Buttons) {
		count = len(p.Buttons)
	}
	return append([]string(nil), p.Buttons[:count]...)
}

// ButtonsSorted returns the button names in canonical order for hashing and
// tests (I1). The stored order stays authored order; the build UI owns layout.
func (p *BuildMenuPage) ButtonsSorted() []string {
	if p == nil || len(p.Buttons) == 0 {
		return nil
	}
	out := append([]string(nil), p.Buttons...)
	sort.Slice(out, func(i, j int) bool {
		li, lj := CanonicalKey(out[i]), CanonicalKey(out[j])
		if li != lj {
			return li < lj
		}
		return out[i] < out[j]
	})
	return out
}

// CompileBuildMenus compiles the [CANBUILD] pages of gamedata/sidedata.tdf
// [02 "Build-menu catalog keys"].
//
// Measured shape of the reference install: sidedata.tdf has exactly three
// top-level sections — SIDE0, SIDE1, CANBUILD — where CANBUILD holds one child
// section per builder (ARMCOM..CORGANT, 45 of them) each authoring numbered
// `canbuild%d = UNITNAME` keys. The first missing key terminates the list
// [02 R-CAT-01 §5].
//
// Note on the rest of the vocabulary: doc-02 also names MENU, UNITMENU,
// DOWNLOADMENU and BUTTON sections consumed while assembling build and order
// menus. None of them appears in sidedata.tdf (measured above); their wiring
// beyond presence is supported inference and belongs to phase 12's GUI
// compilation, not here.
func CompileBuildMenus(fs vfs.FSOps) (map[string]*BuildMenuPage, error) {
	if fs == nil {
		return nil, fmt.Errorf("content: nil VFS")
	}
	data, err := fs.ReadFileLimit("gamedata/sidedata.tdf", 1<<20)
	if err != nil {
		return nil, fmt.Errorf("content: gamedata/sidedata.tdf: %w", err)
	}
	prov := Provenance{}
	if info, statErr := fs.Stat("gamedata/sidedata.tdf"); statErr == nil {
		prov = ProvenanceFrom(info)
	}
	doc, err := formats.ParseTDF(data)
	if err != nil {
		return nil, formats.WithTDFContext(fs, err, "gamedata/sidedata.tdf")
	}
	canbuild := doc.Root.Section("CANBUILD")
	pages := make(map[string]*BuildMenuPage)
	if canbuild == nil {
		return pages, nil
	}
	for _, builder := range canbuild.Sections() {
		name := trimTDFSemantic(builder.OriginalName)
		if name == "" {
			continue
		}
		page := &BuildMenuPage{
			DefinitionHeader: DefinitionHeader{
				CanonicalKey: CanonicalKey(name),
				Provenance:   prov,
			},
			Builder: name,
		}
		// Retail requests successive keys, ending at the first absent one.
		// It never parses a highest suffix from the authored key names
		// [02 R-CAT-01 §5].
		for i := 1; ; i++ {
			button, ok := builder.StringValue(fmt.Sprintf("canbuild%d", i), "")
			if !ok {
				break
			}
			if trimTDFSemantic(button) == "" {
				continue // An empty name resolves to no unit; a later key can exist.
			}
			page.Buttons = append(page.Buttons, button)
		}
		page.BaseButtonCount = len(page.Buttons)
		hashBuildMenu(page)
		pages[page.CanonicalKey] = page
	}
	return pages, nil
}

func hashBuildMenu(page *BuildMenuPage) {
	if page == nil {
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s|%s|base=%d|", page.CanonicalKey, page.Builder, page.BaseButtonCount)
	for _, btn := range page.ButtonsSorted() {
		fmt.Fprintf(&b, "%s|", btn)
	}
	page.Hash = HashDefinition([]byte(b.String()))
}

func sortedBuildMenuKeys(pages map[string]*BuildMenuPage) []string {
	keys := make([]string, 0, len(pages))
	for key := range pages {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// MenuButtonNames returns every distinct button name across all pages in
// canonical form, sorted (I1). This is the set the downloadable enforcement
// compares unit names against [02 "Unit record"].
func MenuButtonNames(pages map[string]*BuildMenuPage) []string {
	set := make(map[string]struct{})
	for _, p := range pages {
		for _, btn := range p.Buttons {
			set[CanonicalKey(btn)] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
