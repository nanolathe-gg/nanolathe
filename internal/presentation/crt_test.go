package presentation

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/sim/rng"
)

func TestCRTRandomLedgerAndSequence(t *testing.T) {
	r := NewCRTRandom(1)
	if got := r.Draw("shake"); got != 41 {
		t.Fatalf("first CRT draw = %d, want 41", got)
	}
	if got := r.Draw("voice"); got != 18467 {
		t.Fatalf("second CRT draw = %d, want 18467", got)
	}
	ledger := r.Ledger()
	if len(ledger) != 2 || ledger[0].Consumer != "shake" || ledger[1].Consumer != "voice" || ledger[1].Draw != 2 {
		t.Fatalf("ledger = %#v", ledger)
	}
	ledger[0].Consumer = "mutated"
	if r.Ledger()[0].Consumer != "shake" {
		t.Fatal("ledger exposed internal storage")
	}
}

func TestCRTRandomSampleConsumesOneDrawForSmallBounds(t *testing.T) {
	r := NewCRTRandom(1)
	if got := r.Sample("silent voice", 1); got != 0 || r.Draws() != 1 {
		t.Fatalf("sample = %d with %d draws, want 0 with 1", got, r.Draws())
	}
	if got := r.Uint32n(0); got != 0 || r.Draws() != 2 {
		t.Fatalf("zero-bound sample = %d with %d draws, want 0 with 2", got, r.Draws())
	}
}

func TestCRTRandomWrapSharesUnderlyingStream(t *testing.T) {
	raw := NewRawCRTForTest(1)
	r := WrapCRT(raw)
	if got := r.Draw("wrapper"); got != 41 {
		t.Fatalf("wrapper first draw = %d, want 41", got)
	}
	if got := raw.Rand(); got != 18467 {
		t.Fatalf("underlying draw after wrapper = %d, want 18467", got)
	}
	if got := r.Draw("wrapper"); got != 6334 {
		t.Fatalf("wrapper draw after underlying = %d, want 6334", got)
	}
	if r.Draws() != 3 || raw.Draws() != 3 {
		t.Fatalf("shared draw count wrapper=%d underlying=%d, want 3", r.Draws(), raw.Draws())
	}
	if got := r.Ledger(); len(got) != 2 || got[1].Draw != 3 {
		t.Fatalf("shared ledger = %#v", got)
	}
}

// NewRawCRTForTest keeps the test's seed construction explicit while the
// production wrapper accepts the session-owned pointer.
func NewRawCRTForTest(seed uint32) *rng.CRT {
	stream := rng.NewCRT(seed)
	return &stream
}

func TestCRTRandomWideSampleLabelsEveryDraw(t *testing.T) {
	r := NewCRTRandom(1)
	r.Sample("music", ^uint32(0))
	if r.Draws() != 2 || len(r.Ledger()) != 2 {
		t.Fatalf("wide sample consumed %d draws, want 2", r.Draws())
	}
	for _, e := range r.Ledger() {
		if e.Consumer != "music" {
			t.Fatalf("wide sample ledger = %#v", r.Ledger())
		}
	}
}
