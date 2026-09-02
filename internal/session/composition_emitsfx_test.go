package session

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/model"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
)

// emitSFXVectorFixture builds a one-piece unit whose piece carries two
// DIFFERENT vertices, so the emit-sfx vector geometry of [04 R-COB-03 §6] is
// observable: a fixture whose vertices coincided would hide the swap and the
// Z rule alike. The piece is the root with no translation and the unit has
// zero heading, pitch and bank, so the composed transform is the identity and
// each transformed vertex is the authored one.
func emitSFXVectorFixture(t *testing.T) (*Session, *cobPresentationSink, [3]numeric.Fixed, [3]numeric.Fixed) {
	t.Helper()
	s, _ := newStripTestSession(5, 5)
	s.Units = units.NewSliced(4, nil)
	def := &content.UnitDef{MaxDamage: 100, Limit: -1, Script: fixtureCOBProgram()}
	h, err := s.Units.Create(def, 0, numeric.FixedFromInt(100), numeric.FixedFromInt(200), numeric.FixedFromInt(300))
	if err != nil {
		t.Fatalf("create fixture unit: %v", err)
	}
	u := s.Units.Unit(h)
	if u == nil {
		t.Fatal("fixture unit missing from the pool")
	}
	// The allocator seeds a heading; zero it so the composed transform is the
	// identity and the authored vertices ARE the transformed ones. The
	// composition itself is [03 §2.4] C21's and is locked elsewhere; what this
	// test owns is which vertices the opcode reads and how they reach world
	// space [04 R-COB-03 §6].
	u.Move.Heading, u.Move.Pitch, u.Move.Bank = 0, 0, 0
	v0 := [3]numeric.Fixed{numeric.FixedFromInt(1), numeric.FixedFromInt(2), numeric.FixedFromInt(3)}
	v1 := [3]numeric.Fixed{numeric.FixedFromInt(10), numeric.FixedFromInt(20), numeric.FixedFromInt(30)}
	mdl := &model.Model{
		Root: 0,
		Pieces: []model.Piece{{
			Name:     "base",
			Parent:   -1,
			Vertices: [][3]numeric.Fixed{v0, v1},
		}},
	}
	vm := &cob.VM{Pieces: make([]model.PieceState, 1)}
	u.ScriptState = &units.ScriptState{VM: vm, Binding: &cob.Binding{Model: mdl, VM: vm, PieceMap: []int{0}}}

	// The world point rule: unit position plus the transformed vertex, third
	// component SUBTRACTED [04 R-COB-03 §6].
	point := func(v [3]numeric.Fixed) [3]numeric.Fixed {
		return [3]numeric.Fixed{u.X.Add(v[0]), u.Y.Add(v[1]), u.Z.Sub(v[2])}
	}
	return s, &cobPresentationSink{session: s, source: h}, point(v0), point(v1)
}

// TestEmitSFXVectorTypesTakeBothPieceVertices locks the emit-sfx vector
// geometry of [04 R-COB-03 §6]: types 0..5 read the piece's transformed
// vertices zero and one, each becoming a world point with the third component
// subtracted; types 4 and 5 pass the two points exchanged
// [03 R-FX-01 §3][03 R-FX-02 §1].
func TestEmitSFXVectorTypesTakeBothPieceVertices(t *testing.T) {
	t.Run("type 0 lays the strip-7 trail from vertex 0 toward vertex 1", func(t *testing.T) {
		s, sink, p0, p1 := emitSFXVectorFixture(t)
		sink.emitSFXStripProducers(cob.PresentationEvent{Piece: 0, SFXType: 0})
		if got := len(s.strips.strips[7]); got != 1 {
			t.Fatalf("strip 7 holds %d containers, want the one flame-stream trail", got)
		}
		o := s.strips.strips[7][0]
		if o.family != stripFamilyFlameTrail {
			t.Fatalf("family %v, want the flame-stream trail [03 R-FX-01 §3]", o.family)
		}
		if o.src != p0 || o.dst != p1 {
			t.Fatalf("trail points src=%v dst=%v, want vertex 0 %v -> vertex 1 %v", o.src, o.dst, p0, p1)
		}
		// The Z rule is what separates this from unit position PLUS the
		// vertex; assert it independently of the pair above so a sign flip
		// cannot pass by moving both points together.
		if o.dst[2] >= s.Units.Unit(sink.source).Z {
			t.Fatalf("trail target Z %v is not below the unit's %v; the third component is subtracted [04 R-COB-03 §6]",
				o.dst[2], s.Units.Unit(sink.source).Z)
		}
		// Lifetime 6 with hold 1 for type 0 [03 R-FX-02 §1].
		if o.particleLife != 6 || o.phaseModulus != 1 {
			t.Fatalf("trail lifetime/hold = %d/%d, want 6/1", o.particleLife, o.phaseModulus)
		}
	})

	t.Run("type 4 is type 2 with the two points exchanged", func(t *testing.T) {
		s, sink, p0, p1 := emitSFXVectorFixture(t)
		sink.emitSFXStripProducers(cob.PresentationEvent{Piece: 0, SFXType: 4})
		if got := len(s.strips.strips[2]); got != 1 {
			t.Fatalf("strip 2 holds %d containers, want the one sprinkle", got)
		}
		o := s.strips.strips[2][0]
		if o.family != stripFamilySprinkle {
			t.Fatalf("family %v, want the impact sprinkle [03 R-FX-01 §3]", o.family)
		}
		if o.src != p1 || o.dst != p0 {
			t.Fatalf("swapped sprinkle src=%v dst=%v, want vertex 1 %v -> vertex 0 %v", o.src, o.dst, p1, p0)
		}
		if o.phaseModulus != 16 || o.colorSel != 1 {
			t.Fatalf("spacing/colour = %d/%d, want 16/1 for type 4 [03 R-FX-01 §3]", o.phaseModulus, o.colorSel)
		}
	})

	t.Run("type 2 is the same pair unswapped", func(t *testing.T) {
		s, sink, p0, p1 := emitSFXVectorFixture(t)
		sink.emitSFXStripProducers(cob.PresentationEvent{Piece: 0, SFXType: 2})
		o := s.strips.strips[2][0]
		if o.src != p0 || o.dst != p1 {
			t.Fatalf("sprinkle src=%v dst=%v, want vertex 0 %v -> vertex 1 %v", o.src, o.dst, p0, p1)
		}
	})
}

// TestEmitSFXVectorTypesDropAPieceWithoutTwoVertices locks the bounds check
// that stands in for retail's read past its own vertex list: a piece with
// fewer than two vertices produces nothing rather than an invented target
// [I9][I11].
func TestEmitSFXVectorTypesDropAPieceWithoutTwoVertices(t *testing.T) {
	s, _ := newStripTestSession(5, 5)
	s.Units = units.NewSliced(4, nil)
	def := &content.UnitDef{MaxDamage: 100, Limit: -1, Script: fixtureCOBProgram()}
	h, err := s.Units.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("create fixture unit: %v", err)
	}
	u := s.Units.Unit(h)
	mdl := &model.Model{Root: 0, Pieces: []model.Piece{{Name: "base", Parent: -1,
		Vertices: [][3]numeric.Fixed{{}}}}}
	vm := &cob.VM{Pieces: make([]model.PieceState, 1)}
	u.ScriptState = &units.ScriptState{VM: vm, Binding: &cob.Binding{Model: mdl, VM: vm, PieceMap: []int{0}}}
	sink := &cobPresentationSink{session: s, source: h}
	for _, sfx := range []int32{0, 1, 2, 3, 4, 5} {
		sink.emitSFXStripProducers(cob.PresentationEvent{Piece: 0, SFXType: sfx})
	}
	if got := len(s.strips.strips[2]) + len(s.strips.strips[7]); got != 0 {
		t.Fatalf("%d containers appended for a one-vertex piece, want none", got)
	}
}
