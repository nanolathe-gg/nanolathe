package movement

import (
	"testing"
)

func TestRoutePublishClampsAndClears(t *testing.T) {
	r := &Route{}
	points := make([]Point, 21)
	for i := range points {
		points[i] = Point{X: int32(i), Z: -int32(i)}
	}
	r.Publish(points)
	if !r.Active || r.Count != 20 || r.Points[19] != points[19] {
		t.Fatalf("publication = active %v count %d tail %+v", r.Active, r.Count, r.Points[19])
	}
	old := r.Points
	r.Publish(nil)
	if r.Active || !r.Dirty || r.Count != 20 || r.Points != old {
		t.Fatalf("zero publication changed stale storage: active=%v dirty=%v count=%d", r.Active, r.Dirty, r.Count)
	}
}

func TestRoutePruneUsesSecondWaypoint(t *testing.T) {
	r := &Route{Active: true, Count: 3, Points: [20]Point{{X: 1}, {X: 5, Z: -2}, {X: 9, Z: 4}}}
	r.Prune(Point{X: 5, Z: 1})
	if r.Count != 2 || r.Points[0] != (Point{X: 5, Z: -2}) || r.Points[1] != (Point{X: 9, Z: 4}) || !r.Active {
		t.Fatalf("prune = active %v count %d points %#v", r.Active, r.Count, r.Points[:2])
	}
}
