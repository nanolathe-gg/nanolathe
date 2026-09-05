package movement

import (
	"math"
	"testing"

	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// TestFlightVerticalClamp locks C29: the three dy branches plus the sentinel
// skip [04 §10.1]. Limit is 0x10000 while (speed & ~3) < 0x40000 else speed>>2.
func TestFlightVerticalClamp(t *testing.T) {
	// Helper that returns VY after integration with brake/decay/horiz neutralized
	// (Acceleration=0 => decay 0x10000 and a=0 => horiz 0, VX=VZ=0 => h=0 => brake skipped,
	// heading zero so C30 does not interfere with VY, TargetX/Z at X/Z so horiz 0).
	newBase := func(y, targetY, speed int32, sentinel bool) *FlightState {
		s := &FlightState{
			Mode:          2,
			Y:             y,
			TargetY:       targetY,
			Speed:         speed,
			VX:            0,
			VZ:            0,
			VY:            999999, // sentinel: should be overwritten unless sentinel skip
			Acceleration:  0,
			MaxVelocity:   65536, // non-zero to avoid panic, decay = 0x10000
			BrakeRate:     65536, // b=1, h=0 => brake skipped
			TurnRate:      0,
			Heading:       0,
			TargetHeading: 0,
			OffMap:        sentinel,
			X:             0,
			Z:             0,
			TargetX:       0,
			TargetZ:       0,
			TargetVX:      0,
			TargetVZ:      0,
		}
		return s
	}

	// Case A: dy <= -limit => vy = +limit. limit=0x10000.
	// Y=0 Target=70000 => dy=-70000 <= -65536 => vy=+65536
	s := newBase(0, 70000, 0x30000, false)
	IntegrateFlight(s)
	if s.VY != 0x10000 {
		t.Fatalf("dy<=-limit vy=%d want %d", s.VY, 0x10000)
	}
	// Case B: dy < limit (and > -limit) => vy = -dy
	// Y=10000 Target=0 => dy=10000 <65536 => vy=-10000
	s = newBase(10000, 0, 0x30000, false)
	IntegrateFlight(s)
	if s.VY != -10000 {
		t.Fatalf(" -dy branch vy=%d want -10000", s.VY)
	}
	// Also test negative small dy: Y=-10000 Target=0 => dy=-10000 => -dy=10000
	s = newBase(-10000, 0, 0x30000, false)
	IntegrateFlight(s)
	if s.VY != 10000 {
		t.Fatalf(" -dy negative dy vy=%d want 10000", s.VY)
	}
	// Case C: dy >= limit => vy = -limit
	// Y=70000 Target=0 => dy=70000 >=65536 => vy=-65536
	s = newBase(70000, 0, 0x30000, false)
	IntegrateFlight(s)
	if s.VY != -0x10000 {
		t.Fatalf("dy>=limit vy=%d want %d", s.VY, -0x10000)
	}
	// Edge: dy == -limit => first branch (<=) wins => vy=+limit
	s = newBase(0, 0x10000, 0x30000, false)
	IntegrateFlight(s)
	if s.VY != 0x10000 {
		t.Fatalf("dy==-limit vy=%d want %d", s.VY, 0x10000)
	}
	// Edge: dy == limit-1 => middle branch => vy=-(limit-1)
	s = newBase(0x10000-1, 0, 0x30000, false)
	IntegrateFlight(s)
	if s.VY != -(0x10000 - 1) {
		t.Fatalf("dy==limit-1 vy=%d want %d", s.VY, -(0x10000 - 1))
	}
	// Edge: dy == limit => else branch => vy=-limit (since middle is < limit)
	s = newBase(0x10000, 0, 0x30000, false)
	IntegrateFlight(s)
	if s.VY != -0x10000 {
		t.Fatalf("dy==limit vy=%d want %d", s.VY, -0x10000)
	}

	// Sentinel skip: when OffMap true, vy keeps damped value [04 §10.1] C29
	// Use Acceleration=0 so damped == initial, sentinel true should preserve.
	s = newBase(70000, 0, 0x30000, true)
	s.VY = 12345 // will be decayed to 12345 (decay 1.0) then kept
	IntegrateFlight(s)
	if s.VY != 12345 {
		t.Fatalf("sentinel skip vy=%d want 12345", s.VY)
	}
	// Also with different dy that would otherwise clamp, sentinel still preserves.
	s = newBase(0, 70000, 0x30000, true)
	s.VY = -9999
	IntegrateFlight(s)
	if s.VY != -9999 {
		t.Fatalf("sentinel skip 2 vy=%d want -9999", s.VY)
	}

	// Large-speed limit path: (speed & ~3) >= 0x40000 => limit = speed>>2
	// speed=0x50000 (327680) => limit=81920
	s = newBase(90000, 0, 0x50000, false) // dy=90000 >=81920 => vy=-81920
	IntegrateFlight(s)
	if s.VY != -81920 {
		t.Fatalf("large speed dy>=limit vy=%d want -81920", s.VY)
	}
	s = newBase(0, 90000, 0x50000, false) // dy=-90000 <=-81920 => vy=+81920
	IntegrateFlight(s)
	if s.VY != 81920 {
		t.Fatalf("large speed dy<=-limit vy=%d want 81920", s.VY)
	}
	s = newBase(80000, 0, 0x50000, false) // dy=80000 <81920 => vy=-80000
	IntegrateFlight(s)
	if s.VY != -80000 {
		t.Fatalf("large speed middle vy=%d want -80000", s.VY)
	}
}

func TestFlightDecayTruncation(t *testing.T) {
	// C27: decay = 0x10000 − trunc((Acceleration<<16)/MaxVelocity)  // idiv
	// v = (v·decay)>>16  arithmetic shift floors [04 §10.1] C27
	cases := []struct {
		acc, max   int32
		vxIn, want int32
		name       string
	}{
		{32768, 65536, 65536, 32768, "0.5*1.0=>0.5"},
		{32768, 65536, -1, -1, "shift floors: -1 *0.5 => -1 not 0"},
		{32768, 65536, -65537, -32769, "shift floors: -65537*0.5 => -32769"},
		{65536, 262144, 131072, 98304, "decay 49152: 2.0*0.75=1.5"},
		{65536, 262144, -131072, -98304, "negative  -2.0 => -1.5 shift same"},
		{0, 65536, 123456, 123456, "zero accel => decay 0x10000 no change"},
	}
	for _, tc := range cases {
		s := &FlightState{
			Mode: 2,
			VX:   tc.vxIn, VY: tc.vxIn, VZ: tc.vxIn,
			X: 0, Z: 0, TargetX: 0, TargetZ: 0,
			TargetVX: tc.want, TargetVZ: tc.want,
			MaxVelocity: tc.max, Acceleration: tc.acc,
			Speed:    0,
			OffMap:   true, // skip vertical overwrite
			TurnRate: 0, Heading: 0, TargetHeading: 0,
		}
		s.BrakeRate = 65536 * 10 // b=10 >h => brake skip
		IntegrateFlight(s)
		if s.VX != tc.want {
			t.Fatalf("%s: VX %d want %d (acc %d max %d)", tc.name, s.VX, tc.want, tc.acc, tc.max)
		}
		if s.VY != tc.want || s.VZ != tc.want {
			t.Fatalf("%s: VY/VZ %d/%d want %d", tc.name, s.VY, s.VZ, tc.want)
		}
	}

	// Zero MaxVelocity must panic deterministically (I11) [04 §10.1] C27
	didPanic := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				didPanic = true
			}
		}()
		s := &FlightState{Mode: 2, VX: 100, MaxVelocity: 0, Acceleration: 65536, OffMap: true}
		IntegrateFlight(s)
	}()
	if !didPanic {
		t.Fatal("zero MaxVelocity should panic (unguarded divide) [04 §10.1] C27 I11")
	}
}

func TestFlightBrakeStrictEquality(t *testing.T) {
	// C28 strict h > b: equality skips whole block [04 §10.1]
	// Use decay neutral (acc 0 => decay 65536), VX=65536 (1.0), VZ=0 => h=1.0
	// BrakeRate=65536 => b=1.0 => h==b => must NOT brake
	s := &FlightState{
		Mode: 2,
		VX:   65536, VZ: 0, VY: 0,
		Acceleration: 0, MaxVelocity: 65536,
		BrakeRate: 65536,
		Speed:     0, Y: 0, TargetY: 0, OffMap: true,
		Heading: 0, TargetHeading: 0, TurnRate: 0,
		X: 0, Z: 0, TargetX: 0, TargetZ: 0, TargetVX: 65536, TargetVZ: 0,
	}
	IntegrateFlight(s)
	if s.VX != 65536 || s.VZ != 0 {
		t.Fatalf("h==b must skip brake, got VX %d VZ %d want 65536,0", s.VX, s.VZ)
	}
	// h < b also skips
	s = &FlightState{
		Mode: 2,
		VX:   32768, VZ: 0,
		Acceleration: 0, MaxVelocity: 65536,
		BrakeRate: 65536, // b=1, h=0.5 => skip
		OffMap:    true,
		X:         0, Z: 0, TargetX: 0, TargetZ: 0, TargetVX: 32768, TargetVZ: 0,
	}
	IntegrateFlight(s)
	if s.VX != 32768 {
		t.Fatalf("h<b must skip, VX %d want 32768", s.VX)
	}
	// h > b must brake: VX=131072 (2.0), b=1.0 => scale to 1.0 then subtract q
	// Heading 0: sin0=0 cos8192 => after brake scale VX 65536, then q=65536 => VX 65536, VZ -65536
	// But horiz accel now runs after: dx=0 => ax=-dvx/65536 => dvx = VX(after brake)-TargetVX
	// Need to neutralize horiz accel by setting TargetVX to post-brake expected.
	// Post-brake VX 65536, so set TargetVX 65536 to make horiz 0.
	s = &FlightState{
		Mode: 2,
		VX:   131072, VZ: 0,
		Acceleration: 0, MaxVelocity: 65536,
		BrakeRate: 65536,
		Heading:   0,
		OffMap:    true,
		TurnRate:  0, TargetHeading: 0,
		X: 0, Z: 0, TargetX: 0, TargetZ: 0,
		TargetVX: 65536, TargetVZ: -65536,
	}
	IntegrateFlight(s)
	// Hand-computed: ratio=32768 => VX scaled to 65536, q=65536, sin0 cos8192 => VX 65536, VZ -65536
	// With rounding (q*sin+4096)>>13 same as before for these exact values.
	if s.VX != 65536 {
		t.Fatalf("h>b brake VX %d want 65536", s.VX)
	}
	if s.VZ != -65536 {
		t.Fatalf("h>b brake VZ %d want -65536", s.VZ)
	}
	// Same with heading 16384 (90deg) sin8192 cos0 => VX should be 0
	s = &FlightState{
		Mode: 2,
		VX:   131072, VZ: 0,
		Acceleration: 0, MaxVelocity: 65536,
		BrakeRate: 65536,
		Heading:   16384, // 90 deg
		OffMap:    true,
		TurnRate:  0, TargetHeading: 16384,
		X: 0, Z: 0, TargetX: 0, TargetZ: 0,
		TargetVX: 0, TargetVZ: 0,
	}
	IntegrateFlight(s)
	if s.VX != 0 {
		t.Fatalf("heading 90deg brake VX %d want 0", s.VX)
	}
	if s.VZ != 0 {
		t.Fatalf("heading 90deg brake VZ %d want 0", s.VZ)
	}
	// Brake trig rounding to nearest [04 §5.1] via [04 §10.1] C28: (q*sin+4096)>>13
	// Hand-computed small q case where rounding differs by 1 from trunc.
	// Choose q=1*65536? Actually need q where product not divisible by 8192.
	// Example: q=1 (1/65536 world units), sin=4096 (0.5) => product 4096 => 4096/8192 trunc 0, but (4096+4096)>>13 =1
	// That would be visible if such small q occurred, but h-b minimum is 1/65536 => q=1, so possible.
	// Use VX such that h-b gives q=1: need h-b =1/65536. With BrakeRate 65536 (1.0), need h=1+1/65536.
	// VX=65537 => h=1.000015258..., b=1 => q=1, ratio=65535
	// After scaling VX 65537*65535>>16 = 65536, q=1, sin=4096 => VX after brake =65536 - ((1*4096+4096)>>13)=65536-1=65535
	// With old trunc q*sin/8192 => 0 => VX would stay 65536. So new rounding gives 65535.
	// We test that new rounding is used.
	s = &FlightState{
		Mode: 2,
		VX:   65537, VZ: 0,
		Acceleration: 0, MaxVelocity: 65536,
		BrakeRate: 65536,
		Heading:   8192, // sin approx 0.707? Actually angle 8192 => 45deg sin~5793
		OffMap:    true,
		TurnRate:  0, TargetHeading: 8192,
		X: 0, Z: 0, TargetX: 0, TargetZ: 0,
		TargetVX: 0, TargetVZ: 0,
	}
	// For heading 8192, sin=5793 (round 8192*sin(45deg))=5793, cos same.
	// Compute expected with rounding: q=1, VX scaled 65535, then VX -= (1*5793+4096)>>13 = (9889)>>13=1 => VX 65534
	// If trunc, would be 0. So check rounding path is taken.
	// First compute ratio scaling: h = hypot(65537,0)/65536=1.000015..., ratio trunc((1/1.000015)*65536)=65535
	// scaled VX = 65537*65535>>16 = 65535 (since 65537*65535=4294967295 >>16 =65535)
	// Then q=1. With heading 8192 sin 5793 => (1*5793+4096)>>13=1 => VX 65534
	// So final VX should be 65534, not 65536.
	IntegrateFlight(s)
	// Horiz accel after will also add some delta: dx 0 dvx 65534 => a=0 => horiz capped 0 => no change (since a=0)
	if s.VX != 65534 {
		t.Fatalf("brake rounding (q*sin+4096)>>13 VX %d want 65534 (rounding check)", s.VX)
	}
}

func TestFlightModeNot2Zeroing(t *testing.T) {
	// C26 any mode !=2 zeroes all three velocities + speed + turn residual, no partial step [04 §10.1]
	// Active is low bits ==2, i.e. modes 2,6,10... Only those run; 6 is active so exclude it.
	for _, mode := range []uint8{0, 1, 3, 4, 5, 7, 8} {
		s := &FlightState{
			Mode: mode,
			VX:   10000, VY: 20000, VZ: 30000,
			Speed:        40000,
			TurnResidual: 1234,
			Heading:      1000, TargetHeading: 2000,
			Acceleration: 65536, MaxVelocity: 65536,
			BrakeRate: 65536,
			Y:         100, TargetY: 0, OffMap: false,
			X: 0, Z: 0, TargetX: 0, TargetZ: 0,
		}
		IntegrateFlight(s)
		if s.VX != 0 || s.VY != 0 || s.VZ != 0 {
			t.Fatalf("mode %d: velocities %d %d %d want 0,0,0", mode, s.VX, s.VY, s.VZ)
		}
		if s.Speed != 0 {
			t.Fatalf("mode %d: Speed %d want 0", mode, s.Speed)
		}
		if s.TurnResidual != 0 {
			t.Fatalf("mode %d: TurnResidual %d want 0", mode, s.TurnResidual)
		}
		// No partial step: heading must NOT have been integrated despite non-zero error
		if s.Heading != 1000 {
			t.Fatalf("mode %d: heading %d want 1000 (no partial)", mode, s.Heading)
		}
		if s.Dirty {
			t.Fatalf("mode %d: Dirty should remain false on zeroing path", mode)
		}
	}
	// Mode 2 must NOT zero; it must proceed to decay etc.
	s := &FlightState{
		Mode: 2,
		VX:   10000, VY: 20000, VZ: 30000,
		Speed:        40000,
		TurnResidual: 1234,
		MaxVelocity:  65536, Acceleration: 0,
		BrakeRate: 0,
		OffMap:    true,
		Heading:   0, TargetHeading: 0, TurnRate: 0,
		X: 0, Z: 0, TargetX: 0, TargetZ: 0, TargetVX: 10000, TargetVZ: 30000,
	}
	IntegrateFlight(s)
	if s.VX == 0 && s.VY == 0 && s.VZ == 0 {
		t.Fatal("mode 2 should not zero velocities")
	}
}

func TestFlightHeadingClamp(t *testing.T) {
	// C30 err = int16(target - heading); zero zeroes residual without dirty;
	// otherwise clamp to TurnRate [04 §10.1]
	// Zero error case
	s := &FlightState{Mode: 2, Heading: 1000, TargetHeading: 1000, TurnRate: 100, TurnResidual: 555, Acceleration: 0, MaxVelocity: 65536, OffMap: true, X: 0, Z: 0, TargetX: 0, TargetZ: 0}
	IntegrateFlight(s)
	if s.TurnResidual != 0 {
		t.Fatalf("zero err residual %d want 0", s.TurnResidual)
	}
	if s.Heading != 1000 {
		t.Fatalf("zero err heading %d want 1000", s.Heading)
	}
	if s.Dirty {
		t.Fatal("zero err should not set Dirty")
	}

	// Positive clamp: err 1000, TurnRate 100 => clamped to 100
	s = &FlightState{Mode: 2, Heading: 0, TargetHeading: 1000, TurnRate: 100, Acceleration: 0, MaxVelocity: 65536, OffMap: true, X: 0, Z: 0, TargetX: 0, TargetZ: 0}
	IntegrateFlight(s)
	if s.TurnResidual != 100 || s.Heading != 100 {
		t.Fatalf("clamp pos got resid %d heading %d want 100,100", s.TurnResidual, s.Heading)
	}
	if !s.Dirty {
		t.Fatal("non-zero err should set Dirty")
	}

	// Negative clamp
	s = &FlightState{Mode: 2, Heading: 1000, TargetHeading: 0, TurnRate: 50, Acceleration: 0, MaxVelocity: 65536, OffMap: true, X: 0, Z: 0, TargetX: 0, TargetZ: 0}
	IntegrateFlight(s)
	// err = 0-1000 = -1000 => clamp -50 => heading 950
	if s.TurnResidual != -50 || s.Heading != 950 {
		t.Fatalf("clamp neg got resid %d heading %d want -50,950", s.TurnResidual, s.Heading)
	}

	// No clamp when TurnRate larger than err
	s = &FlightState{Mode: 2, Heading: 0, TargetHeading: 500, TurnRate: 1000, Acceleration: 0, MaxVelocity: 65536, OffMap: true, X: 0, Z: 0, TargetX: 0, TargetZ: 0}
	IntegrateFlight(s)
	if s.TurnResidual != 500 || s.Heading != 500 {
		t.Fatalf("no clamp got %d %d want 500,500", s.TurnResidual, s.Heading)
	}

	// Wrap around: heading 65535 target 0 => uint16 diff 1 => err 1
	s = &FlightState{Mode: 2, Heading: 65535, TargetHeading: 0, TurnRate: 10, Acceleration: 0, MaxVelocity: 65536, OffMap: true, X: 0, Z: 0, TargetX: 0, TargetZ: 0}
	IntegrateFlight(s)
	if s.TurnResidual != 1 || s.Heading != 0 {
		t.Fatalf("wrap pos got %d %d want 1,0", s.TurnResidual, s.Heading)
	}
	// Wrap other way: heading 0 target 65535 => diff 65535 => int16 -1
	s = &FlightState{Mode: 2, Heading: 0, TargetHeading: 65535, TurnRate: 10, Acceleration: 0, MaxVelocity: 65536, OffMap: true, X: 0, Z: 0, TargetX: 0, TargetZ: 0}
	IntegrateFlight(s)
	if s.TurnResidual != -1 || s.Heading != 65535 {
		t.Fatalf("wrap neg got %d %d want -1,65535", s.TurnResidual, s.Heading)
	}

	// Heading integration independent of vertical sentinel: vertical skip must not suppress heading
	s = &FlightState{
		Mode: 2, Heading: 0, TargetHeading: 100, TurnRate: 10,
		Acceleration: 0, MaxVelocity: 65536,
		BrakeRate: 0, VX: 0, VZ: 0,
		Y: 70000, TargetY: 0, Speed: 0,
		OffMap: true, // would skip vertical
		X:      0, Z: 0, TargetX: 0, TargetZ: 0,
	}
	IntegrateFlight(s)
	if s.Heading != 10 || s.TurnResidual != 10 {
		t.Fatalf("heading independent of sentinel got %d %d want 10,10", s.Heading, s.TurnResidual)
	}
	// Ensure vertical still skipped: VY should remain decayed (0) not clamped to -limit
	if s.VY != 0 {
		t.Fatalf("vertical sentinel with heading: VY %d want 0", s.VY)
	}
}

func TestFlightHorizontalAccelDistanceFloor(t *testing.T) {
	// Distance floor 8.0 [04 §10.1]: d = max(hypot(dx,dz)/65536, 8.0)
	s := &FlightState{
		Mode: 2,
		X:    4 * 65536, Z: 0, TargetX: 0, TargetZ: 0,
		VX: 0, VZ: 0, TargetVX: 0, TargetVZ: 0,
		Acceleration: 65536, MaxVelocity: 655360,
		BrakeRate: 0, // ensure brake skipped (h=0)
		OffMap:    true, TurnRate: 0, Heading: 0, TargetHeading: 0,
		Y: 0, TargetY: 0, Speed: 0,
	}
	// With a=1, d floor 8, k=-0.5, dx 4 => ax -2 => cap to -1 => VX -65536
	IntegrateFlight(s)
	if s.VX != -65536 {
		t.Fatalf("distance floor 8.0 capped accel VX %d want -65536", s.VX)
	}
	// Floor vs no-floor visible when not capped. Use large a=10 to avoid cap,
	// dx=2*65536 => floor 8 => ax -3.162 => -207231, without floor d=2 => -414462
	s = &FlightState{
		Mode: 2,
		X:    2 * 65536, Z: 0, TargetX: 0, TargetZ: 0,
		VX: 0, VZ: 0, TargetVX: 0, TargetVZ: 0,
		Acceleration: 655360, MaxVelocity: 6553600, // a=10, decay ~0.9 but VX 0
		BrakeRate: 0,
		OffMap:    true, TurnRate: 0, Heading: 0, TargetHeading: 0,
		Y: 0, TargetY: 0, Speed: 0,
	}
	IntegrateFlight(s)
	// Hand-computed floor: k=-sqrt(20/8)=-1.58113883, ax=131072*-1.581/65536=-3.16227766 => *65536=-207231
	// Without floor d=2 => k=-3.162 => ax -6.324 => -414462, double.
	want := int32(-207231)
	if s.VX < want-2000 || s.VX > want+2000 {
		t.Fatalf("distance floor: VX %d want ~%d (floor 8.0)", s.VX, want)
	}
	// Ensure not the doubled no-floor value
	if s.VX < -410000 && s.VX > -420000 {
		t.Fatalf("distance floor: got no-floor value %d, floor not applied", s.VX)
	}
}

func TestFlightHorizontalAccelCap(t *testing.T) {
	// Cap at Acceleration magnitude [04 §10.1]: if hypot(ax,az)>a scale to a
	// Setup far target so uncapped ax would exceed a
	s := &FlightState{
		Mode: 2,
		X:    100 * 65536, Z: 0, TargetX: 0, TargetZ: 0,
		VX: 0, VZ: 0, TargetVX: 0, TargetVZ: 0,
		Acceleration: 65536,      // a=1.0
		MaxVelocity:  10 * 65536, // decay ~0.9 but VX 0 stays 0
		BrakeRate:    0,
		OffMap:       true, TurnRate: 0, Heading: 0, TargetHeading: 0,
		Y: 0, TargetY: 0, Speed: 0,
	}
	// Decay: Acc 65536 Max 655360 => decay 58982 => VX 0
	// Horiz: dx=6553600, d=100, k=-sqrt(2/100)=-0.1414, ax=6553600*-0.1414/65536=-14.14 => hypot 14.14 >1 => cap to -1 => VX -65536
	IntegrateFlight(s)
	if s.VX != -65536 {
		t.Fatalf("cap at accel: VX %d want -65536", s.VX)
	}
	// Ensure VZ unchanged
	if s.VZ != 0 {
		t.Fatalf("cap VZ %d want 0", s.VZ)
	}
	// Also test without cap (small distance) stays below a
	s = &FlightState{
		Mode: 2,
		X:    1 * 65536, Z: 0, TargetX: 0, TargetZ: 0,
		VX: 0, VZ: 0, TargetVX: 0, TargetVZ: 0,
		Acceleration: 65536,
		MaxVelocity:  10 * 65536,
		BrakeRate:    0,
		OffMap:       true, TurnRate: 0,
	}
	// dx=65536, d=8 floor => k -0.5 => ax=65536*-0.5/65536=-0.5 => VX -32768 (<a, no cap)
	IntegrateFlight(s)
	if s.VX != -32768 {
		t.Fatalf("no cap: VX %d want -32768", s.VX)
	}
}

func TestFlightScalarSpeed(t *testing.T) {
	// Speed recomputed as FULL 3-D magnitude trunc(sqrt(vx²+vy²+vz²)) [04 §10.1]
	s := &FlightState{
		Mode: 2,
		VX:   3 * 65536, VY: 4 * 65536, VZ: 0,
		X: 0, Z: 0, TargetX: 0, TargetZ: 0, TargetVX: 3 * 65536, TargetVZ: 0,
		Acceleration: 0, MaxVelocity: 65536,
		BrakeRate: 65536 * 10, // skip brake
		OffMap:    true,       // keep VY 4*65536
		TurnRate:  0, Heading: 0, TargetHeading: 0,
		Y: 0, TargetY: 0, Speed: 0,
	}
	// Acceleration 0 => decay 65536, horiz a=0 => no change, so velocities stay 3,4,0
	// Speed = sqrt(9+16)=5 => 5*65536=327680
	IntegrateFlight(s)
	want := int32(5 * 65536)
	if s.Speed != want {
		t.Fatalf("speed 3-4-0 want %d got %d", want, s.Speed)
	}
	// Check pythagoras with vy from vertical clamp: set vy via vertical
	s = &FlightState{
		Mode: 2,
		VX:   0, VZ: 0, VY: 99999,
		X: 0, Z: 0, TargetX: 0, TargetZ: 0, TargetVX: 0, TargetVZ: 0,
		Y: 0, TargetY: 70000, Speed: 0, // Speed 0 => limit 65536 => VY 65536
		Acceleration: 0, MaxVelocity: 65536,
		BrakeRate: 65536 * 10,
		OffMap:    false,
		TurnRate:  0,
	}
	IntegrateFlight(s)
	// VY should be 65536 after vertical, then horiz no change, speed =65536
	if s.VY != 65536 {
		t.Fatalf("vert VY %d want 65536", s.VY)
	}
	if s.Speed != 65536 {
		t.Fatalf("speed after vert %d want 65536", s.Speed)
	}
	// Test trunc: VX=65537 (1+1/65536) with others 0 => sqrt ~65537 => trunc 65537
	s = &FlightState{
		Mode: 2,
		VX:   65537, VY: 0, VZ: 0,
		X: 65537, Z: 0, TargetX: 65537, TargetZ: 0, TargetVX: 65537, TargetVZ: 0,
		Acceleration: 0, MaxVelocity: 65536,
		BrakeRate: 65536 * 10,
		OffMap:    true,
		TurnRate:  0,
	}
	IntegrateFlight(s)
	if s.Speed != 65537 {
		t.Fatalf("speed trunc %d want 65537", s.Speed)
	}
	// Exact sqrt(2) case: VX=65536 VZ=65536 => sqrt(2)*65536=92681.9 => trunc 92681
	// Use math to compute expected: sqrt(2)*65536 = 92681
	s = &FlightState{
		Mode: 2,
		VX:   65536, VZ: 65536, VY: 0,
		X: 65536, Z: 65536, TargetX: 65536, TargetZ: 65536, TargetVX: 65536, TargetVZ: 65536,
		Acceleration: 0, MaxVelocity: 65536,
		BrakeRate: 65536 * 10,
		OffMap:    true, TurnRate: 0,
	}
	IntegrateFlight(s)
	want2 := int32(math.Sqrt(float64(2) * float64(65536) * float64(65536))) // trunc
	if s.Speed != want2 {
		t.Fatalf("speed sqrt2 want %d got %d", want2, s.Speed)
	}
}

// TestLeanPitchReadsFirstRotatedComponent pins the lean accumulator's pitch
// operand and its sign [04 R-AIR-01 §2]. Retail feeds the NEGATED FIRST rotated
// component to both angle calls; the second rotated component is computed by
// the shared coordinate-pair rotation and never read. This routine derived the
// pitch term from the negated second component until AU-4, which was latent on
// stock content only because `pitchscale` defaults to 0.
//
// The case is chosen so the two candidate operands disagree in both magnitude
// and sign: heading 0 makes the rotation the identity, so the first rotated
// component is the decayed lean X and the second the decayed lean Z, and the
// two are given opposite signs.
func TestLeanPitchReadsFirstRotatedComponent(t *testing.T) {
	const gravity = 0x1FDB // OTA gravity default [04 R-AIR-01 §2]
	s := &FlightState{
		LeanX:      3 << 16,
		LeanZ:      -5 << 16,
		Heading:    0,
		Gravity:    gravity,
		BankScale:  0x8000,  // 0.5
		PitchScale: 0x10000, // 1.0 — a definition that authors pitchscale
	}
	s.ApplyLean(0, 0, 0)

	// Recompute the contract independently: decay, identity rotation, the
	// gravity denominator, then each scaled term through atan2.
	px := int32((int64(3<<16) * leanDecay) >> 16)
	pz := int32((int64(-5<<16) * leanDecay) >> 16)
	if px == pz || px == -pz {
		t.Fatalf("test setup: the two rotated components must disagree, got px=%d pz=%d", px, pz)
	}
	l := (int64(gravity) << 16) / leanGravityDivisor
	wantPitch := numeric.AngleFromAtan2((int64(0x10000)*int64(-px))>>16, l).Raw()
	wantBank := numeric.AngleFromAtan2((int64(0x8000)*int64(-px))>>16, l).Raw()
	wrongPitch := numeric.AngleFromAtan2((int64(0x10000)*int64(-pz))>>16, l).Raw()

	if s.Pitch != wantPitch {
		t.Fatalf("pitch = %d, want %d (negated FIRST rotated component) [04 R-AIR-01 §2]", s.Pitch, wantPitch)
	}
	if s.Pitch == wrongPitch {
		t.Fatalf("pitch = %d matches the SECOND rotated component; retail reads the first [04 R-AIR-01 §2]", s.Pitch)
	}
	if s.Bank != wantBank {
		t.Fatalf("bank = %d, want %d [04 R-AIR-01 §2]", s.Bank, wantBank)
	}
	// The first rotated component is positive here, so both negated terms are
	// negative and both angles sit in the fourth quadrant of the 16-bit circle.
	if int16(s.Pitch) >= 0 || int16(s.Bank) >= 0 {
		t.Fatalf("sign: pitch=%d bank=%d, both must be negative for a positive first rotated component", int16(s.Pitch), int16(s.Bank))
	}

	// With equal scales the two words are identical, because they consume the
	// same operand [04 R-AIR-01 §2].
	e := &FlightState{LeanX: 3 << 16, LeanZ: -5 << 16, Gravity: gravity, BankScale: 0x10000, PitchScale: 0x10000}
	e.ApplyLean(0, 0, 0)
	if e.Pitch != e.Bank {
		t.Fatalf("equal scales: pitch %d != bank %d, so the two angle calls do not share an operand", e.Pitch, e.Bank)
	}

	// A heading-rotated case where the second component is zero: any reader of
	// it would produce a zero pitch, and the correct reader does not.
	r := &FlightState{LeanX: 0, LeanZ: 4 << 16, Heading: 16384, Gravity: gravity, BankScale: 0x10000, PitchScale: 0x10000}
	r.ApplyLean(0, 0, 0)
	if r.Pitch == 0 {
		t.Fatalf("rotated case: pitch = 0, i.e. it read the (zero) second rotated component [04 R-AIR-01 §2]")
	}
	if r.Pitch != r.Bank {
		t.Fatalf("rotated case: pitch %d != bank %d", r.Pitch, r.Bank)
	}

	// The default `pitchscale` of 0 keeps pitch at zero whatever the lean is,
	// which is why the wrong operand was latent on stock content.
	d := &FlightState{LeanX: 3 << 16, LeanZ: -5 << 16, Gravity: gravity, BankScale: 0x10000, PitchScale: 0}
	d.ApplyLean(0, 0, 0)
	if d.Pitch != 0 {
		t.Fatalf("pitchscale 0: pitch = %d, want 0 [02 \"Unit record\"]", d.Pitch)
	}
}
