package movement

import (
	"math"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func TestAirReleaseLeadUsesDoubleExpressionAndTruncation(t *testing.T) {
	u := &units.Unit{Def: &content.UnitDef{CruiseAlt: 100}, Move: units.MoveState{Speed: numeric.FixedFromInt(2)}}
	want := int64(math.Sqrt((2.0*100.0)/10.0) * 30.0 * 2.0)
	if got := airReleaseLead(u, 10); got != want {
		t.Fatalf("release lead = %d, want trunc(double expression) %d [04 R-AIR-01 §8]", got, want)
	}
}
