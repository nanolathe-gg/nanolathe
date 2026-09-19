package movement

import (
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/path"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// kernelFixture is the wiring fixture's movable ground unit on synthetic
// terrain, with the tick in scope the search binds its revision from.
func kernelFixture(t *testing.T) (*System, pool.Handle) {
	t.Helper()
	terrain := syntheticTerrainForIntegrate()
	sys := NewSystem(terrain, wiringProfile, NewOccupancyGrid())
	w := newMovementFixtureWorld(10)
	sys.BindWorld(w)
	h, err := w.Create(wiringDef(), 0, world.CellToWorld(2), terrain.HeightAt(world.CellToWorld(2), world.CellToWorld(2)), world.CellToWorld(2))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	sys.EnsureUnit(w.Unit(h))
	sys.BindWorld(w)
	sys.BeginTick(100)
	return sys, h
}

// runToCompletion drives one request through the production search boundary in
// the scheduler's own 100-pop slices and returns the published route.
func runToCompletion(t *testing.T, sys *System, req path.Request) []path.Point {
	t.Helper()
	for slice := 0; slice < 1<<16; slice++ {
		work := sys.searchFunc(req, 65536, 100)
		if work.Done {
			return work.Points
		}
	}
	t.Fatal("the request did not finish in a bounded number of scheduler slices")
	return nil
}

// countingKernel is the shape a replacement kernel takes at this seam: it
// decides what search opens and nothing about the scheduler around it.
type countingKernel struct {
	opened *int
	inner  path.Kernel
}

func (k countingKernel) NewSession(cfg path.SearchConfig) path.Search {
	*k.opened++
	return k.inner.NewSession(cfg)
}

// The bound kernel opens every production search, and it is asked once per
// request rather than once per budget slice or once per expanded node
// (docs/DESIGN_GAMEPLAY_RULES.md §4). Wrapping the retail kernel must also
// leave the route bit-identical, which is what an unchanged fingerprint means
// for this seam.
func TestSearchFuncOpensItsSearchThroughTheBoundKernel(t *testing.T) {
	sys, h := kernelFixture(t)
	req := path.Request{Unit: h, Start: path.Cell{X: 2, Z: 2}, Goal: path.PointGoal(path.Cell{X: 9, Z: 9}, 0)}
	retail := runToCompletion(t, sys, req)

	sys, h = kernelFixture(t)
	opened := 0
	sys.Kernel = countingKernel{opened: &opened, inner: path.RetailKernel{}}
	req.Unit = h
	through := runToCompletion(t, sys, req)

	if opened != 1 {
		t.Fatalf("the kernel was asked %d times for one request, want once", opened)
	}
	if !reflect.DeepEqual(through, retail) {
		t.Fatalf("route through a wrapped retail kernel = %v, unwrapped = %v", through, retail)
	}
	if len(retail) == 0 {
		t.Fatal("the fixture published no route; the comparison proves nothing")
	}
}

// pathKernelSink keeps the measured accessor result live.
var pathKernelSink path.Kernel

// An unbound Kernel is the retail one, and reading the fallback costs nothing:
// the zero-size literal converts to the interface without allocating, so the
// seam does not tax a system no rule set was bound to
// (docs/DESIGN_GAMEPLAY_RULES.md §3 rule 1).
func TestAnUnboundKernelIsRetailAndCostsNothing(t *testing.T) {
	sys, _ := kernelFixture(t)
	if sys.Kernel != nil {
		t.Fatalf("a freshly constructed system bound kernel %T; the fallback is what makes a fixture search as retail", sys.Kernel)
	}
	if _, retail := sys.pathKernel().(path.RetailKernel); !retail {
		t.Fatalf("an unbound kernel answered %T, want the retail kernel", sys.pathKernel())
	}
	if allocs := testing.AllocsPerRun(200, func() { pathKernelSink = sys.pathKernel() }); allocs != 0 {
		t.Fatalf("reading the kernel fallback allocated %v", allocs)
	}
	if pathKernelSink == nil {
		t.Fatal("the measured accessor call was elided")
	}
	var nilSystem *System
	if _, retail := nilSystem.pathKernel().(path.RetailKernel); !retail {
		t.Fatal("a nil system must still answer with the retail kernel")
	}
}
