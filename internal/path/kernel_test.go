package path

import (
	"reflect"
	"testing"
)

// kernelScenario is a small blocked-corridor request: enough expansion that a
// kernel that answered differently would be visible in the route, and small
// enough to run inside an allocation probe.
func kernelScenario() SearchConfig {
	sc := pathStorageScenarios()[1]
	return storageConfig(sc, sc.pass)
}

// kernelSink keeps each opened search live so the dispatches below are not
// optimized away.
var kernelSink Search

// The retail kernel is an alias for the retail search and nothing else: a
// route opened through the seam is the route opened directly. This is what
// makes an identity fingerprint the correct gate for this unit — the seam
// moves no logic (docs/DESIGN_GAMEPLAY_RULES.md §3 rule 3).
func TestRetailKernelOpensTheRetailSearch(t *testing.T) {
	cfg := kernelScenario()
	direct := RunSearch(cfg)
	through := RetailKernel{}.NewSession(cfg)
	points, status, done := through.Resume(1 << 30)
	if !done {
		t.Fatal("the kernel's search did not finish under an unreachable budget")
	}
	if !reflect.DeepEqual(points, direct.Points) || status != direct.Status {
		t.Fatalf("kernel route/status = %v/%#x, direct = %v/%#x", points, status, direct.Points, direct.Status)
	}
	if through.Notified() != direct.Notified || through.SetupSteps() != direct.SetupSteps || through.Popped() != direct.Popped {
		t.Fatalf("kernel counters = (%#x,%d,%d), direct = (%#x,%d,%d)",
			through.Notified(), through.SetupSteps(), through.Popped(),
			direct.Notified, direct.SetupSteps, direct.Popped)
	}
	if through.Start() != cfg.Start {
		t.Fatalf("kernel search started at %v, want %v", through.Start(), cfg.Start)
	}
	if through.Config().Start != cfg.Start {
		t.Fatal("the kernel's search did not retain the request it was opened for")
	}
	through.Release()
}

// The seam itself must cost nothing. The retail kernel is zero size, so both
// holding it in a Kernel and asking it for a search must allocate exactly what
// opening the search directly allocates — no boxed receiver, no closure per
// request (docs/DESIGN_GAMEPLAY_RULES.md §3 rules 1 and 2).
func TestRetailKernelDispatchAddsNoAllocation(t *testing.T) {
	if size := reflect.TypeOf(RetailKernel{}).Size(); size != 0 {
		t.Fatalf("RetailKernel is %d bytes; a kernel must be zero size or a pointer", size)
	}
	var held Kernel
	if allocs := testing.AllocsPerRun(200, func() { held = RetailKernel{} }); allocs != 0 {
		t.Fatalf("holding the retail kernel in a Kernel allocated %v", allocs)
	}
	if held == nil {
		t.Fatal("the measured conversion was elided")
	}
	// A request is opened once per search, so what matters is that the
	// dispatch adds nothing to what the search already costs. The scenario's
	// bounds are not offered a workspace here, so both forms take the same
	// map-backed storage path.
	cfg := kernelScenario()
	direct := testing.AllocsPerRun(50, func() { kernelSink = NewSession(cfg) })
	seam := testing.AllocsPerRun(50, func() { kernelSink = held.NewSession(cfg) })
	if seam != direct {
		t.Fatalf("opening through the kernel allocated %v, opening directly allocated %v", seam, direct)
	}
	if kernelSink == nil {
		t.Fatal("the measured dispatch was elided")
	}
}

// countingKernel is the shape a replacement takes: it wraps or replaces the
// opening of a search and nothing about the scheduler around it. It is zero
// size on purpose, so the seam's allocation contract holds for it too.
type countingKernel struct{ opened *int }

func (k countingKernel) NewSession(cfg SearchConfig) Search {
	*k.opened++
	return RetailKernel{}.NewSession(cfg)
}

// One request, one kernel question, however many nodes and however many budget
// slices the search takes. This is the granularity rule the seam exists under
// (docs/DESIGN_GAMEPLAY_RULES.md §4) stated as a test.
func TestAKernelIsAskedOncePerRequest(t *testing.T) {
	opened := 0
	var kernel Kernel = countingKernel{opened: &opened}
	search := kernel.NewSession(kernelScenario())
	slices := 0
	for {
		_, _, done := search.Resume(7)
		slices++
		if done {
			break
		}
		if slices > 1<<16 {
			t.Fatal("the search did not finish in a bounded number of slices")
		}
	}
	if opened != 1 {
		t.Fatalf("the kernel was asked %d times for one request, want once", opened)
	}
	if slices < 2 || search.Popped() < 2 {
		t.Fatalf("the probe finished in %d slices and %d pops; it must span several slices to prove the count", slices, search.Popped())
	}
	search.Release()
}
