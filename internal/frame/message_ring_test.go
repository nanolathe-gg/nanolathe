package frame

import "testing"

func TestMessageRingCaptionBudgetAndExpiry(t *testing.T) {
	r := NewMessageRing()
	r.Configure(3, 0)
	r.Append("a", 1, 4, 10, 0)
	r.Append("b", 1, 5, 10, 0)
	r.Append("c", 1, 6, 10, 0) // third append drops the oldest of two visible lines
	if r.Producer != 3 || r.Display != 1 {
		t.Fatalf("indices after fullness = producer %d display %d, want 3,1", r.Producer, r.Display)
	}
	lines := r.Visible()
	if len(lines) != 2 || lines[0].Text != "b" || lines[1].Text != "c" {
		t.Fatalf("visible lines = %#v, want b,c", lines)
	}
	r.Expire(31) // strict stored+(scroll+1)*30 < currentTick
	if got := r.Visible(); len(got) != 0 {
		t.Fatalf("expired lines = %#v, want empty", got)
	}
}

func TestMessageRingStorageIndicesWrapAtThirty(t *testing.T) {
	r := NewMessageRing()
	r.Configure(30, 0)
	for i := 0; i < 31; i++ {
		if !r.Append("x", 1, 0, 10, uint32(i)) {
			t.Fatalf("append %d rejected", i)
		}
	}
	if r.Producer != 1 || r.Display != 2 {
		t.Fatalf("indices after 30-slot wrap = producer %d display %d, want 1,2", r.Producer, r.Display)
	}
	if got := len(r.Visible()); got != 29 {
		t.Fatalf("visible count after wrap = %d, want textlines-1 = 29", got)
	}
}
