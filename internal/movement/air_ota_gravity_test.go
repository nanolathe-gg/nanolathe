package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// The release lead reads the OTA integer independently of the terrain's
// converted acceleration and legacy fallback [04 R-AIR-01 §8].
func TestAirStrikePreservesOTAGravityOperand(t *testing.T) {
	for _, gravity := range []int32{112, 8, 0, -1} {
		s := &System{Terrain: &world.Terrain{OTAGravity: gravity, Gravity: 112 * 65536 / 900, AuthoredGravity: 112}}
		if got := s.airGravityWord(); got != int64(gravity) {
			t.Fatalf("OTA gravity %d became %d after terrain selection/conversion", gravity, got)
		}
	}
	u := &units.Unit{Def: &content.UnitDef{CruiseAlt: 150}, Move: units.MoveState{Speed: numeric.FixedFromInt(5)}}
	s := &System{Terrain: &world.Terrain{OTAGravity: 112, Gravity: 112 * 65536 / 900}}
	if got, want := airReleaseLead(u, s.airGravityWord()), airReleaseLead(u, 112); got != want {
		t.Fatalf("release lead = %d, want %d from authored gravity", got, want)
	}
	if got := airReleaseLead(u, -1); got != 0 {
		t.Fatalf("negative OTA gravity lead = %d, want invalid-root truncation to zero [01 R-DET-01 §1]", got)
	}
}
