package main

import (
	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	contentprofiles "github.com/nanolathe-gg/nanolathe/internal/content/profiles"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// authorTestUnitScripts supplies the smallest behavior-free script needed by
// command fixtures. Every authored unit definition has a compiled script and
// every live unit receives its own VM; Create returns immediately here, so the
// fixture preserves strict allocation without adding command behavior
// [04 §4.1][04 §4.3][R-CB-01 §2][R-COB-04 §8].
func authorTestUnitScripts(defs ...*content.UnitDef) {
	program := &cob.Program{
		Code:        []uint32{0x10065000},
		Scripts:     map[string]int{"Create": 0},
		ScriptsByID: []int{0},
	}
	for _, def := range defs {
		if def != nil {
			def.Script = program
		}
	}
}

// testContentSet pairs one mounted fixture overlay with itself as the content
// view. An authored fixture ships retail-named directories, so the content
// profile that applies to it is `retail` and its table is empty — the view a
// loader reads is the overlay itself (docs/DESIGN_CONTENT_VFS.md §5).
func testContentSet(fs *vfs.FS) *contentSet {
	return &contentSet{fs: fs, unmappedMount: fs, profile: contentprofiles.RetailName}
}
