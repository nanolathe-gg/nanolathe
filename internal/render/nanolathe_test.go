package render

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

func wu(v int) numeric.Fixed { return numeric.Fixed(v) << 16 }

// The target box is narrowed to the span between its 4/11 and 7/11
// interpolants and stored as origin+extent; the source point stays a point.
func TestNanoFieldNarrowsTargetBoxToMiddleThreeElevenths(t *testing.T) {
	var f NanoField
	f.Add([3]numeric.Fixed{wu(10), wu(0), wu(10)},
		[3]numeric.Fixed{wu(0), wu(0), wu(0)},
		[3]numeric.Fixed{wu(110), wu(110), wu(110)}, 7)
	r := f.Records[0]
	for a := 0; a < 3; a++ {
		if r.SrcExtent[a] != 0 {
			t.Fatalf("axis %d: source extent = %v, want a degenerate point", a, r.SrcExtent[a])
		}
		if r.DstOrigin[a] != wu(40) || r.DstExtent[a] != wu(30) {
			t.Fatalf("axis %d: target box = %v+%v, want %v+%v", a, r.DstOrigin[a], r.DstExtent[a], wu(40), wu(30))
		}
	}
}

// Five particles per tick, six CRT draws each, over exactly two spawn ticks.
func TestNanoFieldSpawnCadenceAndDrawCount(t *testing.T) {
	var f NanoField
	f.Add([3]numeric.Fixed{0, 0, 0},
		[3]numeric.Fixed{wu(200), 0, 0}, [3]numeric.Fixed{wu(200), 0, 0}, 100)
	draws := 0
	rand := func() int32 { draws++; return 0x4000 }
	f.Tick(100, rand)
	if got := len(f.Records[0].Particles); got != NanoParticlesPerTick {
		t.Fatalf("first tick spawned %d particles, want %d", got, NanoParticlesPerTick)
	}
	if draws != NanoParticlesPerTick*6 {
		t.Fatalf("first tick took %d CRT draws, want %d", draws, NanoParticlesPerTick*6)
	}
	f.Tick(101, rand)
	if got := len(f.Records[0].Particles); got != 2*NanoParticlesPerTick {
		t.Fatalf("second tick left %d particles, want %d", got, 2*NanoParticlesPerTick)
	}
	draws = 0
	f.Tick(102, rand)
	if draws != 0 {
		t.Fatalf("third tick spawned again after %d draws; the spawn window is two ticks", draws)
	}
}

// A particle travels four world units per tick and expires on arrival, and its
// colour walks the ramp one step per tick.
func TestNanoParticleTravelAndColourCycle(t *testing.T) {
	var f NanoField
	f.Add([3]numeric.Fixed{0, 0, 0},
		[3]numeric.Fixed{wu(200), 0, 0}, [3]numeric.Fixed{wu(200), 0, 0}, 0)
	f.Tick(0, func() int32 { return 0 })
	p := f.Records[0].Particles[0]
	if p.VX != wu(4) {
		t.Fatalf("particle velocity = %v, want %v per tick", p.VX, wu(4))
	}
	if p.ExpiryTick != 50 {
		t.Fatalf("particle expiry = %d, want 50 ticks for 200 units at 4 per tick", p.ExpiryTick)
	}
	if p.Color != 0xa1 {
		t.Fatalf("first particle colour = %#x, want 0xa1", p.Color)
	}
	// The nibble climbs to seven and then wraps to one, never to zero.
	c := uint8(0xa6)
	for _, want := range []uint8{0xa7, 0xa1, 0xa2} {
		c = NanoColorBase | nextNanoNibble(c)
		if c != want {
			t.Fatalf("colour cycle = %#x, want %#x", c, want)
		}
	}
}

// A record retires once its last particle has arrived.
func TestNanoFieldRetiresEmptyRecords(t *testing.T) {
	var f NanoField
	f.Add([3]numeric.Fixed{0, 0, 0},
		[3]numeric.Fixed{wu(8), 0, 0}, [3]numeric.Fixed{wu(8), 0, 0}, 0)
	rand := func() int32 { return 0 }
	for tick := uint32(0); tick <= 5; tick++ {
		f.Tick(tick, rand)
	}
	if len(f.Records) != 0 {
		t.Fatalf("field still holds %d records after every particle arrived", len(f.Records))
	}
}
