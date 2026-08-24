package snapshot

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// TestPublishedFramesAreStable locks the immutability that lets Read hand out
// stored pointers: a later Publish must replace the pointers, never rewrite a
// frame the renderer is already holding.
func TestPublishedFramesAreStable(t *testing.T) {
	var b Buffer
	b.Publish(&Frame{Tick: 1, Units: []UnitView{{DefID: 10}}})
	prev1, cur1, ok := b.Read()
	if !ok {
		t.Fatal("first publish produced no frame")
	}
	if prev1.Tick != 1 || cur1.Tick != 1 {
		t.Fatalf("first publish = prev %d cur %d, want 1/1", prev1.Tick, cur1.Tick)
	}

	b.Publish(&Frame{Tick: 2, Units: []UnitView{{DefID: 20}}})
	if cur1.Tick != 1 || cur1.Units[0].DefID != 10 {
		t.Fatal("a held frame changed under the reader")
	}
	prev2, cur2, _ := b.Read()
	if prev2.Tick != 1 || cur2.Tick != 2 {
		t.Fatalf("second publish = prev %d cur %d, want 1/2", prev2.Tick, cur2.Tick)
	}
}

// TestPublishCopiesInput: the caller may reuse the slices it passed.
func TestPublishCopiesInput(t *testing.T) {
	var b Buffer
	units := []UnitView{{DefID: 7}}
	b.Publish(&Frame{Tick: 1, Units: units})
	units[0].DefID = 99
	_, cur, _ := b.Read()
	if cur.Units[0].DefID != 7 {
		t.Fatal("Publish did not copy its input")
	}
}

// TestReadDoesNotAllocate is the R17 regression. Read used to deep-copy both
// frames on every call, so a 60 fps render loop allocated two full frames per
// rendered frame purely to avoid a race that immutability already prevents.
func TestReadDoesNotAllocate(t *testing.T) {
	var b Buffer
	b.Publish(&Frame{Tick: 1, Units: make([]UnitView, 256)})
	b.Publish(&Frame{Tick: 2, Units: make([]UnitView, 256)})
	if got := testing.AllocsPerRun(100, func() { b.Read() }); got != 0 {
		t.Fatalf("Read allocates %v times per call, want 0", got)
	}
}

// TestReadBeforePublish reports not-ok rather than a zero frame.
func TestReadBeforePublish(t *testing.T) {
	var b Buffer
	if prev, cur, ok := b.Read(); ok || prev != nil || cur != nil {
		t.Fatal("an unpublished buffer returned a frame")
	}
}

// TestLerpNeverExtrapolates locks C15/C16: alpha is clamped at both ends and
// NaN degrades to the previous frame.
func TestLerpNeverExtrapolates(t *testing.T) {
	prev, cur := numeric.Fixed(0), numeric.Fixed(100)
	if got := Lerp(prev, cur, -1); got != prev {
		t.Fatalf("alpha -1 = %d, want %d", got, prev)
	}
	if got := Lerp(prev, cur, 2); got != cur {
		t.Fatalf("alpha 2 = %d, want %d", got, cur)
	}
	if got := Lerp(prev, cur, 0.5); got != 50 {
		t.Fatalf("alpha 0.5 = %d, want 50", got)
	}
	nan := float32(0)
	nan = nan / nan
	if got := Lerp(prev, cur, nan); got != prev {
		t.Fatalf("NaN alpha = %d, want %d", got, prev)
	}
}
