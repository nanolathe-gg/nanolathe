package movement

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

var captureBootstrapProfile = Profile{
	FootPrintX: 1, FootPrintZ: 1,
	MinWaterDepth: -10000, MaxWaterDepth: 12,
	MaxSlope: 50, BadSlope: 25, MaxWaterSlope: 255, BadWaterSlope: 127,
}

// TestEnsureUnitAdmitsCreatorAndRestoreModes keeps the initial collision,
// flight, and occupancy records on the mode supplied by the creator or restore
// input [05 R-WORK-01 §15][04 R-MOV-01 §8][04 R-FAC-02 §5][08 R-SAVE-02 §6].
func TestEnsureUnitAdmitsCreatorAndRestoreModes(t *testing.T) {
	tests := []struct {
		name     string
		create   bool
		restored bool
		wantMode uint8
	}{
		{name: "ordinary create", wantMode: units.CreatedMoverMode},
		{name: "airborne capture", create: true, wantMode: 2},
		{name: "restore", restored: true, wantMode: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			terrain := syntheticFlat(32, 32)
			sys := NewSystem(terrain, captureBootstrapProfile, NewOccupancyGrid())
			w := newMovementFixtureWorld(4)
			sys.BindWorld(w)
			def := setScratchMovement(&content.UnitDef{
				UnitName: "capture-bootstrap", CanFly: true, CanMove: true,
				MaxDamage: 100, MaxVelocity: 4 << 16, Acceleration: 1 << 14,
				BrakeRate: 1 << 13, TurnRate: 500,
			}, captureBootstrapProfile)
			x, z := world.CellToWorld(8), world.CellToWorld(8)
			var (
				h   pool.Handle
				err error
			)
			if tt.create {
				h, err = w.CreateWithMoverMode(def, 0, x, terrain.HeightAt(x, z), z, tt.wantMode)
			} else {
				h, err = w.Create(def, 0, x, terrain.HeightAt(x, z), z)
			}
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			u := w.Unit(h)
			if tt.restored {
				u.Move.Mode = tt.wantMode
				u.Move.ModeMirror = tt.wantMode
				u.RestoredMoveMode = true
			}
			sys.EnsureUnit(u)

			coll := handleRow(sys.Collisions, h)
			if coll == nil || coll.Mode != tt.wantMode || coll.CachedMode != tt.wantMode {
				t.Fatalf("collision modes = %#v, want %d", coll, tt.wantMode)
			}
			flight := handleRow(sys.Flights, h)
			if flight == nil || flight.Mode != tt.wantMode || flight.ModeMirror != tt.wantMode {
				t.Fatalf("flight modes = %#v, want %d", flight, tt.wantMode)
			}
			if u.Move.Mode != tt.wantMode {
				t.Fatalf("unit mode = %d, want %d", u.Move.Mode, tt.wantMode)
			}
			if _, present := sys.Grid.OccupantAtPlane(PlaneGround, coll.CachedAnchor); present != (tt.wantMode == 1) {
				t.Fatalf("ground stamp present=%v for mode %d", present, tt.wantMode)
			}
			if _, present := sys.Grid.OccupantAtPlane(PlaneAir, coll.CachedAnchor); present != (tt.wantMode == 2) {
				t.Fatalf("air stamp present=%v for mode %d", present, tt.wantMode)
			}
		})
	}
}
