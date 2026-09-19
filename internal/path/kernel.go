package path

// The search kernel seam. Everything in this file is composition: it names
// who opens a search, and nothing about how one runs.
// docs/DESIGN_GAMEPLAY_RULES.md owns the seam contract, including the
// granularity rule this seam is built to respect.

// Search is one route request in progress: the resumable object the scheduler
// drives in bounded increments until it reports done [04 §7.3].
//
// The scheduler asks for one of these per admitted request and then only
// resumes it, so every method here is reached at most once per scheduler call
// on the single active request — never per expanded node.
type Search interface {
	// Resume expands at most budget nodes and reports the route, the status
	// and whether the search finished. A budget boundary publishes nothing:
	// a search that returns false keeps its state for the next call
	// [04 §7.3].
	Resume(budget int) ([]Point, Status, bool)

	// Notified returns the status the request reported while setting up,
	// which is not necessarily the status it finishes with
	// [04 R-PATH-01 §4][04 R-PATH-01 §9].
	Notified() Status

	// SetupSteps returns the setup work performed before the first
	// expansion, which the scheduler charges on the admission slice only
	// [04 R-PATH-01 §5].
	SetupSteps() int

	// Popped returns how many nodes the search has taken off its frontier so
	// far — the work charged against the player's share [04 §7.3].
	Popped() int

	// Start returns the cell the search began from, which is the cell
	// request setup consumed rather than the one the caller submitted
	// [04 R-PATH-01 §4] step 1.
	Start() Cell

	// Config returns the request this search was opened for.
	Config() SearchConfig

	// Release hands back any storage the search borrowed for the request.
	// Every caller that drops a search owes this call.
	Release()
}

// Kernel creates resumable searches. It is asked **once per search request**
// and never per node: the scheduler's admission order, its per-player step
// allowance [04 R-PATH-01 §10] and its full-or-empty publication boundary
// [04 §7.3] are shared by every kernel, so a replacement may change how a
// route is found and never when one publishes.
//
// A kernel is selected by the session's gameplay rule set and bound outside
// any tick, so a phase holds a concrete kernel and selects nothing
// [docs/INVARIANTS.md I1].
type Kernel interface {
	NewSession(cfg SearchConfig) Search
}

// RetailKernel opens the retail search: the ray-and-A* kernel of [04 §7.2]
// and [04 R-PATH-01], which is what Strict 3.1 means for pathfinding and what
// Modern also binds today.
//
// It is zero size, so holding it in a Kernel never allocates and a rule set
// can carry it by value.
type RetailKernel struct{}

// NewSession opens and seeds a retail search for cfg.
func (RetailKernel) NewSession(cfg SearchConfig) Search { return NewSession(cfg) }
