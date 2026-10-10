package replay

import (
	"errors"
	"testing"
)

// The player matches recorded and observed checksums by tick, whichever
// came first: a single-player recording writes a checksum before the pump
// that computes it, an online seat after its tick. An observation the
// recording lacks is dropped; a recorded checksum for a tick already played
// that playback never observed is a mismatch, as is a different sum.
func TestPlayerMatchesChecksumsByTick(t *testing.T) {
	sum := func(b byte) [32]byte { return [32]byte{b} }
	for _, c := range []struct {
		name               string
		tick               uint32
		expected, observed []checksumAt
		verified           int
		mismatch           uint32
		pending            int
	}{
		{"recorded before its pump, not yet run", 29, []checksumAt{{30, sum(1)}}, nil, 0, 0, 1},
		{"recorded before its pump, run", 32, []checksumAt{{30, sum(1)}}, []checksumAt{{30, sum(1)}}, 1, 0, 0},
		{"acknowledged after its tick", 60, []checksumAt{{30, sum(1)}, {60, sum(2)}}, []checksumAt{{30, sum(1)}, {60, sum(2)}}, 2, 0, 0},
		{"observation not recorded", 60, []checksumAt{{60, sum(2)}}, []checksumAt{{30, sum(9)}, {60, sum(2)}}, 1, 0, 0},
		{"different sum", 60, []checksumAt{{30, sum(1)}, {60, sum(2)}}, []checksumAt{{30, sum(1)}, {60, sum(3)}}, 1, 60, 1},
		{"never observed", 40, []checksumAt{{30, sum(1)}}, nil, 0, 30, 1},
		{"observed later than recorded", 60, []checksumAt{{30, sum(1)}}, []checksumAt{{60, sum(2)}}, 0, 30, 1},
	} {
		p := &Player{tick: c.tick, expected: c.expected, observed: c.observed}
		err := p.compare()
		if c.mismatch == 0 {
			if err != nil {
				t.Errorf("%s: %v", c.name, err)
			}
		} else if m := p.Mismatch(); !errors.Is(err, ErrMismatch) || m == nil || m.Tick != c.mismatch {
			t.Errorf("%s: %v, want a mismatch at %d", c.name, err, c.mismatch)
		}
		if p.Verified() != c.verified || len(p.expected) != c.pending || len(p.observed) != 0 && err == nil {
			t.Errorf("%s: verified %d, %d recorded and %d observed pending", c.name, p.Verified(), len(p.expected), len(p.observed))
		}
	}
}
