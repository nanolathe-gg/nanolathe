package orders

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/units"
)

func TestWorkMoverGateRequiresExactlyOne(t *testing.T) {
	for _, value := range []uint8{0, 1, 2, 255} {
		u := &units.Unit{Def: &content.UnitDef{BMCode: value, CanMove: true, CanFly: true}}
		if hasMover(u) != (value == 1) {
			t.Fatalf("BMCode %d passed wrong mover gate", value)
		}
	}
}
