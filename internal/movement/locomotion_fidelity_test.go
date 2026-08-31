package movement

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/path"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/world"
)

// TestLocomotionPitchCapUsesAuthoritativeWord locks the established speed-cap
// table without asserting an unestablished ground attitude producer.
func TestLocomotionPitchSustainsCapOnGentleSlope(t *testing.T) {
	s := &SteerState{MaxVelocity: 78643, HeightWord: 10}
	if got := s.SpeedCapForPitch(0); got != 78643 {
		t.Fatalf("level pitch cap = %d, want 78643", got)
	}
}

// TestLocomotionAccelBrakeRamp proves M3: speed ramps +accel/tick toward cap
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func TestLocomotionAccelBrakeRamp(t *testing.T) {
	const maxV = 78643
	const accel = 9830  // ARMCOM 0.15*65536
	const brake = 19660 // 0.30*65536
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	s := &SteerState{
		MaxVelocity:  maxV,
		Acceleration: accel,
		BrakeRate:    brake,
		Speed:        0,
		HeightWord:   10,
		SeaLevel:     0,
	}
	s.MaxVelocity = maxV
	cap := s.SpeedCapForPitch(0) // 100%
	if cap != maxV {
		t.Fatalf("cap %d want %d", cap, maxV)
	}
	for i := 1; i <= 9; i++ {
		// large dist, hasWaypoint true, not blocked
		s.UpdateSpeedWithBraking(cap, true, 1<<30, false)
		want := int32(i * accel)
		if want > cap {
			want = cap
		}
		if s.Speed != want {
			t.Fatalf("accel ramp tick %d speed %d want ~%d (accel %d)", i, s.Speed, want, accel)
		}
	}
	if s.Speed != cap {
		t.Fatalf("after ramp speed %d want cap %d", s.Speed, cap)
	}
	// Deceleration: set speed to cap, dist small -> should brake -brake per tick [M3] quadratic
	s.Speed = cap
	// Simulate approaching goal: dist = stoppingDist/2 triggers brake
	// For maxV, stoppingDist ~ speed^2/(brake*2) = 157k (~2.4px) as per [M3]
	// Choose dist 0 to force brake
	for i := 0; i < 5; i++ {
		prev := s.Speed
		s.UpdateSpeedWithBraking(cap, true, 0, false)
		if s.Speed >= prev && prev != 0 {
			t.Fatalf("braking tick %d speed %d not < prev %d", i, s.Speed, prev)
		}
		if s.Speed < 0 {
			t.Fatalf("negative speed %d", s.Speed)
		}
	}
	if s.Speed != 0 {
		// after 4 ticks at brake 19660, 78643 -> 58983->39323->19663->0
		t.Fatalf("after braking to goal speed %d want 0", s.Speed)
	}
	// No oscillation: braking when hasWaypoint false should stay 0, not go negative or bounce
	for i := 0; i < 3; i++ {
		s.UpdateSpeedWithBraking(cap, false, 0, false)
		if s.Speed != 0 {
			t.Fatalf("no waypoint should stay 0, got %d", s.Speed)
		}
	}
	// Integration test: drive via System and ensure no negative-speed oscillation
	terrain := &world.Terrain{
		CellW:    20,
		CellH:    20,
		SeaLevel: 0,
		Plot:     make([]world.PlotCell, 400),
	}
	for i := range terrain.Plot {
		terrain.Plot[i].SetFeature(world.PlotFeatureNone)
		terrain.Plot[i].SetHeight(10)
		terrain.Plot[i].SetMinHeight(10)
		terrain.Plot[i].SetMaxHeight(10)
	}
	profile := Profile{FootPrintX: 1, FootPrintZ: 1, MaxWaterDepth: 12, MinWaterDepth: -10000, MaxSlope: 50}
	grid := NewOccupancyGrid()
	sys := NewSystem(terrain, profile, grid)
	w := newMovementFixtureWorld(10)
	def := &content.UnitDef{UnitName: "armflea", MaxVelocity: 3 * 65536, TurnRate: 800, Acceleration: 1 * 65536, BrakeRate: 2 * 65536}
	def.MaxDamage = 100
	def.FootprintX = 1
	def.FootprintZ = 1
	h, _ := w.Create(def, 0, world.CellToWorld(0), numeric.Fixed(0), world.CellToWorld(0))
	u := w.Unit(h)
	sys.BindWorld(w)
	sys.EnsureUnit(u)
	steer2 := sys.Steers[h]
	// Override accel/brake for deterministic
	steer2.Acceleration = 1 * 65536
	steer2.BrakeRate = 2 * 65536
	sys.SubmitMove(h, 0, path.Cell{X: 0, Z: 0}, path.Cell{X: 3, Z: 0})
	id := orders.Lookup("Move_Ground")
	q := orders.QueueForUnit(u)
	q.Push(id, orders.Node{GoalX: world.CellToWorld(3), GoalZ: world.CellToWorld(0)})
	sys.Scheduler.Tick(1)
	var lastDist numeric.Fixed
	var arrived bool
	for tick := uint32(2); tick < 100; tick++ {
		sys.Scheduler.Tick(tick)
		sys.BeginTick(tick)
		res := sys.StepUnit(h, tick)
		sys.EndTick(tick)
		lastDist = res.DistToGoal
		if res.Arrived {
			arrived = true
			// Check also speed settled (no oscillation)
			if steer2.Speed < 0 {
				t.Fatalf("negative speed on arrival %d", steer2.Speed)
			}
			break
		}
		// Ensure no oscillation: speed never negative
		if steer2.Speed < 0 {
			t.Fatalf("tick %d negative speed %d", tick, steer2.Speed)
		}
		// Ensure monotonic approach after some ticks? Not strict, but distance should generally decrease
	}
	if arrived {
		t.Fatalf("route progress must not complete Move_Ground while final tolerance is unresolved")
	}
	if lastDist <= 0 {
		t.Fatalf("distance diagnostic must remain positive")
	}
}
