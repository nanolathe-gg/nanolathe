package main

import (
	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
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
