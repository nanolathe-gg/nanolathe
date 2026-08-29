package economy

import "testing"

func TestP28EconomyTraceNilWorldIsPure(t *testing.T) {
	s := &Service{}
	a := s.ParitySnapshot(nil)
	b := s.ParitySnapshot(nil)
	if len(a.Units) != 0 || len(b.Units) != 0 || a.Players != b.Players {
		t.Fatal("nil-world economy trace changed or fabricated state")
	}
}
