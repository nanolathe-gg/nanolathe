package movement

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/path"
)

// TestPathRequestSetupReadsTheAdmissionTimeCommittedCell locks [04 R-PATH-01
// §4] step 1: request setup copies the requesting unit's cached committed cell
// as the start cell when the scheduler ADMITS the request, not the cell the
// follower held when it submitted one.
//
// The two differ in ordinary play. There is one global working set
// [04 R-PATH-01 §6], so a submitted request waits in the provider while the
// mover keeps walking. Consuming the submitted cell made setup judge a stale
// position, and the worst case is the one reproduced here: a request submitted
// while the mover stood on the very cell it was later ordered to leave takes
// step 6's already-satisfied exit and publishes an empty route, whereupon
// [04 R-PATH-01 §7]'s count-zero branch asks the LIVE position, disagrees, and
// raises `0x40`. That bit is the "cannot get there" notification
// [04 R-COLL-01 §6]; the work and mobile-build rows abandon their record and
// notify caption slot 7 on it [04 R-ORD-01 §5], so a plainly reachable goal was
// refused with a message while an identical second order reached it.
func TestPathRequestSetupReadsTheAdmissionTimeCommittedCell(t *testing.T) {
	goal := path.Cell{X: 8, Z: 8}
	sys, _, _, head, req := newGroundPathStatusFixture(t, path.Cell{X: 2, Z: 2}, goal)

	// The follower submitted while the mover's committed cell was still the
	// goal cell; by admission it has walked to the fixture's live cell (2,2).
	req.Start = goal

	work := sys.searchFunc(req, 65536, 1<<16)
	if work.Status == path.StatusAlreadySatisfied {
		t.Fatalf("setup consumed the submitted cell: already-satisfied for a mover six cells away, work = %+v", work)
	}
	if !work.Done || work.Status != 0 || len(work.Points) == 0 {
		t.Fatalf("reachable goal did not produce a route: %+v", work)
	}

	sys.publishFunc(req, work.Points, work.Status)
	if head.Satisfied&0x40 != 0 {
		t.Fatalf("reachable goal raised the cannot-get-there bit: satisfied = %#x [04 R-COLL-01 §6]", head.Satisfied)
	}
	if route := sys.Routes[req.Unit]; route == nil || route.Count == 0 {
		t.Fatalf("no route published: %+v", sys.Routes[req.Unit])
	}
}

// TestPathWorkingSetSurvivesAMovingMover locks the other half of the same
// contract [04 R-PATH-01 §4 step 1][04 R-PATH-01 §6]: the start is copied ONCE,
// at admission. A budget slice taken while the mover has moved on must resume
// the same working set, never restart it — a restart per slice is a search that
// never finishes.
func TestPathWorkingSetSurvivesAMovingMover(t *testing.T) {
	sys, u, _, _, req := newGroundPathStatusFixture(t, path.Cell{X: 2, Z: 2}, path.Cell{X: 18, Z: 18})

	if work := sys.searchFunc(req, 65536, 0); work.Done {
		t.Fatalf("admission with no pop budget should leave the search resumable: %+v", work)
	}
	admitted := sys.sessions[int(req.Unit)]
	if admitted == nil || admitted.session == nil {
		t.Fatal("admission stored no working set")
	}
	start := admitted.session.Start()

	// The mover walks a cell while the search is mid-flight.
	if coll := sys.Collisions[u.Handle]; coll != nil {
		coll.CachedAnchor = Cell{X: 3, Z: 3}
	}
	if work := sys.searchFunc(req, 65536, 1); work.Done && work.Status == path.StatusRejected {
		t.Fatalf("continuation restarted the working set: %+v", work)
	}
	resumed := sys.sessions[int(req.Unit)]
	if resumed != nil && resumed.session != nil && resumed.session.Start() != start {
		t.Fatalf("continuation re-read the live cell: start %v, want the admitted %v", resumed.session.Start(), start)
	}
	if resumed != nil && resumed.session != admitted.session {
		t.Fatal("continuation allocated a second working set for one request")
	}
}
