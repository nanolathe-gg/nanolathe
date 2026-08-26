package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/ai"
	"github.com/nanolathe/nanolathe/internal/save"
)

func TestAICadenceCaptureRestoreContinuesAtSameClassificationTick(t *testing.T) {
	m := &ai.Manager{Player: 0}
	for k := ai.TaskKind(0); k < ai.TaskKindCount; k++ {
		m.Deadlines[k] = 1000
	}
	source := &Session{AI: [10]*ai.Manager{0: m}}
	for tick := uint32(1); tick <= 29; tick++ {
		m.Tick(tick, nil, nil)
	}
	if got := m.EntryCount(); got != 29 {
		t.Fatalf("source entry count = %d, want 29", got)
	}
	if got := m.ClassificationRuns(); got != 0 {
		t.Fatalf("source classified before boundary: %d", got)
	}

	state := source.CaptureStateV1()
	if state == nil || state.Version != save.StateV1VersionConst {
		t.Fatalf("CaptureStateV1 version = %#v, want %d", state, save.StateV1VersionConst)
	}
	decoded, err := save.UnmarshalStateV1(save.MarshalStateV1(state), "", "")
	if err != nil {
		t.Fatalf("UnmarshalStateV1: %v", err)
	}
	resumed := &Session{AI: [10]*ai.Manager{0: {Player: 0}}}
	if err := resumed.RestoreStateV1(decoded); err != nil {
		t.Fatalf("RestoreStateV1: %v", err)
	}
	if got := resumed.AI[0].EntryCount(); got != 29 {
		t.Fatalf("restored entry count = %d, want 29", got)
	}
	if got, want := HashState(resumed), HashState(source); got != want {
		t.Fatalf("state hash changed across cadence restore: got %s want %s", got, want)
	}

	m.Tick(30, nil, nil)
	resumed.AI[0].Tick(30, nil, nil)
	if got := m.ClassificationRuns(); got != 1 {
		t.Fatalf("source classification runs = %d, want 1", got)
	}
	if got := resumed.AI[0].ClassificationRuns(); got != 1 {
		t.Fatalf("restored classification runs = %d, want 1", got)
	}
	if got := resumed.AI[0].EntryCount(); got != m.EntryCount() {
		t.Fatalf("continuation entry counts differ: source=%d restored=%d", m.EntryCount(), resumed.AI[0].EntryCount())
	}
	if got, want := HashState(resumed), HashState(source); got != want {
		t.Fatalf("state hash diverged at shared classification tick: got %s want %s", got, want)
	}
}

func TestHashStateIncludesAICadenceCounter(t *testing.T) {
	m := &ai.Manager{Player: 0}
	s := &Session{AI: [10]*ai.Manager{0: m}}
	base := HashState(s)
	m.SetEntryCountForRestore(29)
	if got := HashState(s); got == base {
		t.Fatal("AI cadence counter change did not affect authoritative hash")
	}
}
