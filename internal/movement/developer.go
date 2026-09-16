package movement

import "github.com/nanolathe-gg/nanolathe/internal/pool"

// DeveloperTiers copies an existing class layer without allocating a class or
// running its revision pass. The first selected local subject remains the
// subject even when it has no layer [03 §3.12][I6].
func (s *System) DeveloperTiers(h pool.Handle, dst []uint8) []uint8 {
	if s == nil || s.layerRegistry == nil {
		return dst[:0]
	}
	layer := s.layerRegistry.Existing(s.classKeyFor(h))
	if layer == nil {
		return dst[:0]
	}
	dst = dst[:0]
	for z := int32(0); z < layer.H; z++ {
		for x := int32(0); x < layer.W; x++ {
			dst = append(dst, layer.Value(x, z))
		}
	}
	return dst
}

// DeveloperFollower is a value copy of the ground follower's drawing inputs.
// Its fixed route storage cannot alias the live route [03 R-COMP-01 §5].
type DeveloperFollower struct {
	Available   bool
	Anchor      Cell
	HasWaypoint bool
	Count       uint8
	Points      [20]Point
}

// DeveloperFollowerState reads existing surfaces only. Aircraft and buildings
// have no ground-follower drawing method [03 R-COMP-01 §5].
func (s *System) DeveloperFollowerState(h pool.Handle) DeveloperFollower {
	if s == nil || int(h) >= len(s.Collisions) || int(h) >= len(s.Steers) {
		return DeveloperFollower{}
	}
	coll, steer := s.Collisions[h], s.Steers[h]
	if coll == nil || steer == nil || coll.Building || (int(h) < len(s.Flights) && s.Flights[h] != nil) {
		return DeveloperFollower{}
	}
	out := DeveloperFollower{Available: true, Anchor: coll.CachedAnchor}
	if int(h) < len(s.Routes) && s.Routes[h] != nil {
		r := s.Routes[h]
		out.Count = min(r.Count, uint8(len(out.Points)))
		out.Points = r.Points
		out.HasWaypoint = r.Active && r.Count > 1
	}
	return out
}
