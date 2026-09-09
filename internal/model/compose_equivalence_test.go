package model

import (
	"math"
	"testing"

	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// referenceChain is the composition of [03 §2.4] C21 written out literally:
// one node per piece of the leaf→root chain, every node rotating Z then X then
// Y with math.Round after each axis and adding its own translation afterwards.
// It is the contract the compressed chain and the trig memo must reproduce
// exactly.
type referenceNode struct {
	cx, sx, cy, sy, cz, sz float64
	t                      [3]numeric.Fixed
	ax, ay, az             uint16
}

func referenceCompose(m *Model, st []PieceState, piece int) []referenceNode {
	var chain []int
	for cur := piece; cur != -1; cur = m.Pieces[cur].Parent {
		chain = append(chain, cur)
	}
	nodes := make([]referenceNode, len(chain))
	for i, idx := range chain {
		t := m.Pieces[idx].Translate
		var ax, ay, az uint16
		if idx < len(st) {
			ax, ay, az = st[idx].RotX, st[idx].RotY, st[idx].RotZ
			t[0] = t[0].Add(st[idx].Trans[0])
			t[1] = t[1].Add(st[idx].Trans[1])
			t[2] = t[2].Add(st[idx].Trans[2])
		}
		n := referenceNode{t: t, ax: ax, ay: ay, az: az}
		if az != 0 {
			theta := float64(az) * 2 * math.Pi / 65536
			n.cz, n.sz = math.Cos(theta), math.Sin(theta)
		}
		if ax != 0 {
			theta := float64(ax) * 2 * math.Pi / 65536
			n.cx, n.sx = math.Cos(theta), math.Sin(theta)
		}
		if ay != 0 {
			theta := float64(ay) * 2 * math.Pi / 65536
			n.cy, n.sy = math.Cos(theta), math.Sin(theta)
		}
		nodes[i] = n
	}
	return nodes
}

func referenceApply(p [3]numeric.Fixed, nodes []referenceNode) [3]numeric.Fixed {
	x := float64(p[0].Raw())
	y := float64(p[1].Raw())
	z := float64(p[2].Raw())
	for i := range nodes {
		n := &nodes[i]
		if n.az != 0 {
			c, s := n.cz, n.sz
			nx := math.Round(c*x - s*y)
			ny := math.Round(s*x + c*y)
			x, y = nx, ny
		}
		if n.ax != 0 {
			c, s := n.cx, n.sx
			ny := math.Round(c*y - s*z)
			nz := math.Round(s*y + c*z)
			y, z = ny, nz
		}
		if n.ay != 0 {
			c, s := n.cy, n.sy
			nx := math.Round(c*x - s*z)
			nz := math.Round(s*x + c*z)
			x, z = nx, nz
		}
		x += float64(n.t[0].Raw())
		y += float64(n.t[1].Raw())
		z += float64(n.t[2].Raw())
	}
	return [3]numeric.Fixed{numeric.Fixed(int64(x)), numeric.Fixed(int64(y)), numeric.Fixed(int64(z))}
}

// fx authors a 16.16 word from quarters of a world unit, which is all the
// fixtures below need and keeps every operand exact.
func fx(v float64) numeric.Fixed { return numeric.FixedFromRaw(int64(v * 65536)) }

// equivalenceModel is an authored four-level hierarchy whose depths, authored
// translations and vertex spreads stand in for a hull with a turret, a barrel
// and a flare attachment.
func equivalenceModel() *Model {
	verts := func(seed int) [][3]numeric.Fixed {
		out := make([][3]numeric.Fixed, 0, 6)
		for i := 0; i < 6; i++ {
			out = append(out, [3]numeric.Fixed{
				fx(float64(seed*3+i) - 7.5),
				fx(float64(seed) + float64(i)*1.25),
				fx(float64(i*i) - float64(seed)*2.5),
			})
		}
		return out
	}
	return &Model{
		Name: "equivalence",
		Root: 0,
		Pieces: []Piece{
			{Name: "base", Parent: -1, Children: []int{1}, Translate: [3]numeric.Fixed{fx(0), fx(0), fx(0)}, Vertices: verts(0)},
			{Name: "hull", Parent: 0, Children: []int{2, 4}, Translate: [3]numeric.Fixed{fx(3.5), fx(-1.25), fx(9)}, Vertices: verts(1)},
			{Name: "turret", Parent: 1, Children: []int{3}, Translate: [3]numeric.Fixed{fx(-2), fx(6.5), fx(0.75)}, Vertices: verts(2)},
			{Name: "barrel", Parent: 2, Translate: [3]numeric.Fixed{fx(0), fx(1), fx(11.5)}, Vertices: verts(3)},
			{Name: "flare", Parent: 1, Translate: [3]numeric.Fixed{fx(12.25), fx(0), fx(-4)}, Vertices: verts(4)},
		},
	}
}

// authoredPoses covers the poses the recorder actually meets: everything at
// rest, a unit heading alone (the fold of [03 §2.4] C24), a rotating turret
// under a heading, a full three-axis root pose, and script translation lanes
// on an intermediate piece.
func authoredPoses() []struct {
	name   string
	states []PieceState
} {
	return []struct {
		name   string
		states []PieceState
	}{
		{"rest", make([]PieceState, 5)},
		{"heading only", []PieceState{{RotY: 0x4000}, {}, {}, {}, {}}},
		{"heading and turret", []PieceState{{RotY: 0x2ab1}, {}, {RotY: 0x9c40}, {RotX: 0x0400}, {}}},
		{"three axis root", []PieceState{{RotX: 0x1234, RotY: 0xabcd, RotZ: 0x5678}, {}, {}, {}, {}}},
		{"script translation", []PieceState{{RotY: 0x8000}, {Trans: [3]numeric.Fixed{fx(4), fx(-2.5), fx(7)}}, {}, {Trans: [3]numeric.Fixed{fx(-1), fx(0), fx(3)}}, {}}},
		{"leaf spin only", []PieceState{{}, {}, {}, {RotZ: 0xc000}, {RotY: 0x0001}}},
		{"every piece turns", []PieceState{{RotX: 11, RotY: 22, RotZ: 33}, {RotX: 4444, RotY: 5555, RotZ: 6666}, {RotY: 40000}, {RotZ: 12345}, {RotX: 65535}}},
	}
}

// TestComposeMatchesLiteralChain locks the compressed chain, the trig memo and
// the bulk vertex application to the literal per-piece chain of [03 §2.4] C21:
// every authored pose must transform every authored vertex to the identical
// 16.16 triple.
func TestComposeMatchesLiteralChain(t *testing.T) {
	m := equivalenceModel()
	offset := [3]numeric.Fixed{fx(1234.5), fx(-67.25), fx(890)}
	for _, pose := range authoredPoses() {
		var scratch ComposeScratch
		scratch.BeginModel(len(m.Pieces))
		for piece := range m.Pieces {
			want := referenceCompose(m, pose.states, piece)
			got := ComposeInto(m, pose.states, piece, Transform{}, &scratch)
			if origin := referenceApply([3]numeric.Fixed{}, want); origin != got.Origin {
				t.Fatalf("%s piece %d origin: got %v want %v", pose.name, piece, got.Origin, origin)
			}
			src := m.Pieces[piece].Vertices
			dst := make([][3]numeric.Fixed, len(src))
			got.ApplyOffsetInto(dst, src, offset)
			for vi, v := range src {
				ref := referenceApply(v, want)
				ref[0], ref[1], ref[2] = ref[0].Add(offset[0]), ref[1].Add(offset[1]), ref[2].Add(offset[2])
				if dst[vi] != ref {
					t.Fatalf("%s piece %d vertex %d: got %v want %v", pose.name, piece, vi, dst[vi], ref)
				}
				if single := got.Apply(v); single != referenceApply(v, want) {
					t.Fatalf("%s piece %d vertex %d Apply: got %v want %v", pose.name, piece, vi, single, referenceApply(v, want))
				}
			}
		}
	}
}

// TestComposeMatchesLiteralChainSweep runs the same equality over a
// deterministic sweep of angle triples so a pose the authored table does not
// name cannot regress silently.
func TestComposeMatchesLiteralChainSweep(t *testing.T) {
	m := equivalenceModel()
	states := make([]PieceState, len(m.Pieces))
	var scratch ComposeScratch
	seed := uint32(1)
	next := func() uint16 {
		seed = seed*1103515245 + 12345 // deterministic sweep, not a simulation stream (I4)
		return uint16(seed >> 13)
	}
	for round := 0; round < 64; round++ {
		for i := range states {
			states[i] = PieceState{}
			switch round % 4 {
			case 0:
				states[i].RotY = next()
			case 1:
				states[i].RotX, states[i].RotZ = next(), next()
			case 2:
				if i == m.Root {
					states[i].RotX, states[i].RotY, states[i].RotZ = next(), next(), next()
				}
			default:
				states[i].RotX, states[i].RotY, states[i].RotZ = next(), next(), next()
				states[i].Trans = [3]numeric.Fixed{fx(float64(next() % 32)), fx(-float64(next() % 16)), fx(float64(next()%64) / 2)}
			}
		}
		scratch.BeginModel(len(m.Pieces))
		for piece := range m.Pieces {
			want := referenceCompose(m, states, piece)
			got := ComposeInto(m, states, piece, Transform{}, &scratch)
			src := m.Pieces[piece].Vertices
			dst := make([][3]numeric.Fixed, len(src))
			got.ApplyOffsetInto(dst, src, [3]numeric.Fixed{})
			for vi, v := range src {
				if ref := referenceApply(v, want); dst[vi] != ref {
					t.Fatalf("round %d piece %d vertex %d: got %v want %v", round, piece, vi, dst[vi], ref)
				}
			}
		}
	}
}
