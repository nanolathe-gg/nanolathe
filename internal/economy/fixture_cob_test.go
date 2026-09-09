package economy

import (
	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
)

// economyFixtureDef supplies the smallest authored Create program to economy
// tests that create units. Production catalogs receive this program from the
// content compiler; these tests isolate economy behavior from COB loading.
func economyFixtureDef(def *content.UnitDef) *content.UnitDef {
	if def != nil && (def.Script == nil || len(def.Script.Code) == 0) {
		def.Script = &cob.Program{
			Code:        []uint32{0x10065000},
			Scripts:     map[string]int{"Create": 0},
			ScriptsByID: []int{0},
		}
	}
	return def
}
