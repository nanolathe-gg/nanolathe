package session

import (
	"slices"

	"github.com/nanolathe-gg/nanolathe/internal/movement"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// groupDestinations holds Modern destination slots for one ordinary group
// command, in handle order (DESIGN_INTERFACE_HUD_INPUT "Modern group
// destination slots"). The zero value holds none.
type groupDestinations []movement.GroupDestination

func (g groupDestinations) lookup(h pool.Handle) (movement.GroupDestination, bool) {
	i, ok := slices.BinarySearchFunc(g, h, func(d movement.GroupDestination, h pool.Handle) int {
		return int(d.H) - int(h)
	})
	if !ok {
		return movement.GroupDestination{}, false
	}
	return g[i], true
}

// groupDestinationSlots computes the Modern destinations of an ordinary,
// unassigned, untargeted group move whose actors take a formation goal, or
// none when the bound movement rules keep retail's broadcast. It reads state
// only; the command loop still resolves and issues every order. Aircraft keep
// the retail goal. handles are sorted, so the result is in handle order.
func (s *Session) groupDestinationSlots(o HumanOrderCommand, handles []pool.Handle, excluded pool.Handle, target *units.Unit, center orders.ResolvePos, count int32) groupDestinations {
	if s.Movement == nil || s.Movement.Rules == nil || o.AssignedPosition || target != nil || count < 2 || !s.Movement.Rules.GroupDestinationSlots(s.Movement) {
		return nil
	}
	var out groupDestinations
	for _, h := range handles {
		u := s.humanUnit(h)
		if u == nil || h == excluded || u.Def == nil || u.Def.CanFly {
			continue
		}
		pos := o.Position
		id := orders.Resolve(o.Code, u, nil, &pos)
		if id == 0 || orders.DescriptorFor(id).StaticGate&2 == 0 {
			continue
		}
		g := clampedFormationGoal(o.Position, u, center, count)
		out = append(out, movement.GroupDestination{H: h, X: g.X, Z: g.Z})
	}
	s.Movement.AssignGroupDestinations(s.Units, out, o.Position.X, o.Position.Z)
	return out
}

// clampedFormationGoal is the Modern form of humanFormationGoal: an actor
// beyond retail's cutoff keeps its bearing from the centroid, clamped to the
// cutoff radius, instead of taking the clicked point, so outliers of a long
// line do not share one destination [04 R-STANCE-01 §5]. Within the cutoff it
// is exactly the retail offset.
func clampedFormationGoal(goal orders.ResolvePos, u *units.Unit, center orders.ResolvePos, count int32) orders.ResolvePos {
	if retail := humanFormationGoal(goal, u, center, count); retail != goal || u.X == center.X && u.Z == center.Z {
		return retail
	}
	dx, dz := int64(u.X-center.X), int64(u.Z-center.Z)
	wx, wz := dx>>16, dz>>16
	d := isqrtInt64(wx*wx + wz*wz)
	r := isqrtInt64(3000 * int64(count))
	if d <= 0 {
		return goal
	}
	goal.X += numeric.Fixed(dx * r / d)
	goal.Z += numeric.Fixed(dz * r / d)
	return goal
}

// isqrtInt64 is the floor square root of a non-negative value.
func isqrtInt64(v int64) int64 {
	if v <= 0 {
		return 0
	}
	x := v
	y := (x + 1) / 2
	for y < x {
		x = y
		y = (x + v/x) / 2
	}
	return x
}
