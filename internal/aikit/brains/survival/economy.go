package survival

import (
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
)

// Economy wraps the utility economy: the survival layer first decides its
// own jobs and takes their builders, the utility economy then decides the
// builders it is shown, and the survival orders are issued last, from the
// action budget the economy left.
type Economy struct {
	st    *state
	inner core.Policy
}

// Init implements core.Policy.
func (e *Economy) Init(b *core.Board) {
	if e.inner != nil {
		e.inner.Init(b)
	}
}

// Plan implements core.Policy.
func (e *Economy) Plan(b *core.Board) {
	st := e.st
	if !st.ready {
		st.setup(b)
	}
	st.refreshJobs(b)
	st.plan(b)
	all := st.hideClaimed(b)
	if e.inner != nil {
		e.inner.Plan(b)
	}
	b.Builders = all
	st.emit(b)
}

// Explain implements core.Explaining.
func (e *Economy) Explain(b *core.Board, x *aikit.Explain) {
	if ex, ok := e.inner.(core.Explaining); ok {
		ex.Explain(b, x)
	}
}
