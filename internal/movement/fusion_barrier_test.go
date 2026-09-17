package movement

import "testing"

// IntegrateFlight's horizontal acceleration truncates straight into the 16.16
// velocity words, every tick, for every aircraft. `[04 §10.1]` C29 fixes the
// mixed fixed/float instruction order, and retail rounds `dx·k` before
// subtracting the velocity error. A compiler that fuses the two rounds once and
// stores a different velocity word; this case is one where it does.
//
// The state is arranged so nothing before the acceleration block moves the
// velocities: both horizontal components start at zero, so the decay leaves
// them zero and the brake test `h > b` is false; the off-map bypass holds the
// vertical component; and the heading already matches its command.
func TestIntegrateFlightRoundsTheAccelerationProductBeforeSubtracting(t *testing.T) {
	s := &FlightState{
		Mode:         2,
		X:            0,
		TargetX:      1840591431, // dx = -1840591431
		Z:            90477107,   // dz = +90477107
		TargetZ:      0,
		VX:           0,
		VZ:           0,
		TargetVX:     -106792096, // dvx = +106792096
		TargetVZ:     5249531,    // dvz = -5249531
		MaxVelocity:  1 << 30,
		Acceleration: 3101808,
		BrakeRate:    1 << 20,
		OffMap:       true,
	}
	IntegrateFlight(s)
	// The separately rounded product gives +1; fusing the multiply into the
	// subtraction gives 0.
	if s.VX != 1 {
		t.Errorf("VX = %d, want the separately rounded 1 (a fused multiply-add stores 0)", s.VX)
	}
	if s.VZ != 0 {
		t.Errorf("VZ = %d, want 0", s.VZ)
	}
}
