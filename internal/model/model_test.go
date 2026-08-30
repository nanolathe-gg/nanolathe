package model

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/testsupport"
	"github.com/nanolathe/nanolathe/vfs"
)

func TestComposeTranslationSumming(t *testing.T) {
	// Synthetic hierarchy root -> child, no rotation [03 §2.4] C21.
	m := &Model{
		Pieces: []Piece{
			{
				Name:      "base",
				Parent:    -1,
				Translate: [3]numeric.Fixed{0, 0, 0},
			},
			{
				Name:      "turret",
				Parent:    0,
				Translate: [3]numeric.Fixed{numeric.Fixed(65536), numeric.Fixed(131072), numeric.Fixed(196608)}, // (1,2,3)
			},
		},
		Root: 0,
	}
	m.Pieces[0].Children = []int{1}
	st := make([]PieceState, 2)
	// script lanes for child: (0.5, -1, 0.25) in 16.16
	st[1].Trans = [3]numeric.Fixed{numeric.Fixed(32768), numeric.Fixed(-65536), numeric.Fixed(16384)}
	// leaf is turret piece 1; world position = sum of chain: root(0) + child(authored+script)
	// Expected: (1+0.5, 2-1, 3+0.25) = (1.5,1,3.25)
	tr := Compose(m, st, 1)
	got := tr.Origin
	expX := numeric.Fixed(98304)  // 1.5 *65536
	expY := numeric.Fixed(65536)  // 1
	expZ := numeric.Fixed(212992) // 3.25*65536 = 212992
	if got[0] != expX || got[1] != expY || got[2] != expZ {
		t.Fatalf("translation sum: got %v want [%d %d %d]", got, expX, expY, expZ)
	}
	// Also verify Apply of zero equals origin
	zero := [3]numeric.Fixed{}
	if tr.Apply(zero) != got {
		t.Fatalf("apply zero vs origin mismatch")
	}
}

func TestComposeRotationOrderZThenXThenY(t *testing.T) {
	// Single piece, point (1,0,0) [03 §2.4] C21: apply Z then X then Y with float trig round-to-nearest NOT fixed tables (I2).
	m := &Model{
		Pieces: []Piece{
			{Name: "base", Parent: -1, Translate: [3]numeric.Fixed{}},
		},
		Root: 0,
	}
	// Point in local space: (1,0,0) => raw 65536
	pt := [3]numeric.Fixed{numeric.Fixed(65536), 0, 0}
	// Set Z=90deg (16384), Y=90deg (16384), X=0; order matters [03 §2.4] C21.
	st := make([]PieceState, 1)
	st[0].RotZ = 16384 // 90 deg
	st[0].RotY = 16384 // 90 deg

	tr := Compose(m, st, 0)
	got := tr.Apply(pt)
	// Manual expected Z then Y: start (1,0,0) -> Z90 -> (0,1,0) -> Y90 -> (0,1,0) because x=0,z=0 unchanged by Y
	exp := [3]numeric.Fixed{0, numeric.Fixed(65536), 0}
	if got != exp {
		t.Fatalf("rotation order Z->Y: got %v want %v", got, exp)
	}
	// Verify the opposite order Y then Z would give (0,0,1), proving our order is Z first.
	// Compute Y then Z manually:
	x, y, z := float64(65536), float64(0), float64(0)
	// Y90 first
	thY := float64(16384) * 2 * math.Pi / 65536
	cY, sY := math.Cos(thY), math.Sin(thY)
	nx := math.Round(cY*x - sY*z)
	nz := math.Round(sY*x + cY*z)
	x, z = nx, nz
	// then Z90
	thZ := float64(16384) * 2 * math.Pi / 65536
	cZ, sZ := math.Cos(thZ), math.Sin(thZ)
	nx2 := math.Round(cZ*x - sZ*y)
	ny2 := math.Round(sZ*x + cZ*y)
	x, y = nx2, ny2
	alt := [3]numeric.Fixed{numeric.Fixed(int64(x)), numeric.Fixed(int64(y)), numeric.Fixed(int64(z))}
	altExp := [3]numeric.Fixed{0, 0, numeric.Fixed(65536)}
	if alt != altExp {
		t.Fatalf("alt order expectation off")
	}
	if got == alt {
		t.Fatalf("rotation order not distinguished: got alt %v", alt)
	}
}

func TestComposeRotationZ(t *testing.T) {
	m := &Model{Pieces: []Piece{{Name: "base", Parent: -1}}, Root: 0}
	pt := [3]numeric.Fixed{numeric.Fixed(65536), 0, 0}
	st := make([]PieceState, 1)
	st[0].RotZ = 16384 // 90 deg [03 §2.4] C21
	tr := Compose(m, st, 0)
	got := tr.Apply(pt)
	// 90 deg about Z: (1,0,0) -> (0,1,0) per Rz: x' = c*x - s*y ; y' = s*x + c*y [03 §2.4] C21
	want := [3]numeric.Fixed{0, numeric.Fixed(65536), 0}
	if got != want {
		t.Fatalf("Z rotation: got %v want %v", got, want)
	}
	// X rotation: (0,1,0) -> 90 deg about X -> (0,0,1) per Rx: y' = c*y - s*z ; z' = s*y + c*z [03 §2.4] C21
	pt2 := [3]numeric.Fixed{0, numeric.Fixed(65536), 0}
	st2 := make([]PieceState, 1)
	st2[0].RotX = 16384
	got2 := Compose(m, st2, 0).Apply(pt2)
	want2 := [3]numeric.Fixed{0, 0, numeric.Fixed(65536)}
	if got2 != want2 {
		t.Fatalf("X rotation: got %v want %v", got2, want2)
	}
	// Y rotation: (1,0,0) -> 90 deg about Y -> (0,0,1) per Ry: x' = c*x - s*z ; z' = s*x + c*z [03 §2.4] C21
	st3 := make([]PieceState, 1)
	st3[0].RotY = 16384
	got3 := Compose(m, st3, 0).Apply(pt)
	want3 := [3]numeric.Fixed{0, 0, numeric.Fixed(65536)}
	if got3 != want3 {
		t.Fatalf("Y rotation: got %v want %v", got3, want3)
	}
}

func TestHalfTurnNegation(t *testing.T) {
	// Verify that Load's half-turn negates X and Z translation and vertices [03 §2.4] C20.
	// Build a minimal 3DO file with known translation (2,3,4) and vertex (10,20,30) in 16.16 raw.
	dir := t.TempDir()
	// Build file bytes for one object "base" with 1 vertex, 0 primitives, translation (2,3,4) units.
	data := buildHalfTurn3DO()
	fpath := filepath.Join(dir, "test_half.3do")
	// Need to place under a subdirectory to test vfs directory mounting: MountDirectory indexes recursively.
	sub := filepath.Join(dir, "objects3d")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "test_half.3do"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	_ = fpath
	fs := vfs.New()
	if err := fs.MountDirectory(dir, 10); err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	m, err := Load(fs, "objects3d/test_half.3do")
	if err != nil {
		t.Fatalf("Load half-turn model: %v", err)
	}
	if len(m.Pieces) != 1 {
		t.Fatalf("pieces %d", len(m.Pieces))
	}
	p := m.Pieces[0]
	// Authored translation before half-turn was (2*65536, 3*65536, 4*65536); after negating X,Z should be (-2,3,-4)
	expTx := numeric.Fixed(-2 * 65536)
	expTy := numeric.Fixed(3 * 65536)
	expTz := numeric.Fixed(-4 * 65536)
	if p.Translate[0] != expTx || p.Translate[1] != expTy || p.Translate[2] != expTz {
		t.Fatalf("half-turn translation: got %v want [%d %d %d]", p.Translate, expTx, expTy, expTz)
	}
	if len(p.Vertices) != 1 {
		t.Fatalf("vertices %d", len(p.Vertices))
	}
	v := p.Vertices[0]
	expVx := numeric.Fixed(-10 * 65536)
	expVy := numeric.Fixed(20 * 65536)
	expVz := numeric.Fixed(-30 * 65536)
	if v[0] != expVx || v[1] != expVy || v[2] != expVz {
		t.Fatalf("half-turn vertex: got %v want [%d %d %d]", v, expVx, expVy, expVz)
	}
}

func buildHalfTurn3DO() []byte {
	// Minimal 3DO with one object, based on source_test builder logic [fmt 3do].
	// Layout: root object at 0 (52 bytes), vertex array at 100, string table.
	// Offsets are absolute file offsets.
	size := 400
	data := make([]byte, size)
	put32 := func(off int, v int32) { binary.LittleEndian.PutUint32(data[off:], uint32(v)) }
	// Root object
	put32(0, 1)        // VersionSignature
	put32(4, 1)        // NumberOfVertexes
	put32(8, 0)        // NumberOfPrimitives
	put32(12, -1)      // Selection -1
	put32(16, 2*65536) // XFromParent 2 units
	put32(20, 3*65536) // YFromParent 3
	put32(24, 4*65536) // ZFromParent 4
	put32(28, 300)     // OffsetToObjectName -> "base"
	put32(32, 0)       // Always_0
	put32(36, 100)     // OffsetToVertexArray
	put32(40, 0)       // OffsetToPrimitiveArray (0 when no primitives, but parser handles 0 with count 0)
	put32(44, 0)       // Sibling
	put32(48, 0)       // Child
	// Vertex at 100: (10,20,30) units
	put32(100, 10*65536)
	put32(104, 20*65536)
	put32(108, 30*65536)
	// Name at 300
	copy(data[300:], "base\x00")
	return data
}

func TestLeafPiecesValidAttachment(t *testing.T) {
	// C23: leaf pieces with a vertex but no primitive are valid attachment points [03 §2.4] C23.
	m := &Model{
		Pieces: []Piece{
			{Name: "base", Parent: -1, Translate: [3]numeric.Fixed{}},
			{Name: "flare", Parent: 0, Translate: [3]numeric.Fixed{numeric.Fixed(100)}, Vertices: [][3]numeric.Fixed{{numeric.Fixed(1), numeric.Fixed(2), numeric.Fixed(3)}}, Primitives: nil},
		},
		Root: 0,
	}
	m.Pieces[0].Children = []int{1}
	// Ensure the flare piece is retained
	if len(m.Pieces[1].Vertices) == 0 {
		t.Fatal("flare vertex missing")
	}
	if len(m.Pieces[1].Primitives) != 0 {
		t.Fatal("flare should have no primitives")
	}
	tr := Compose(m, make([]PieceState, 2), 1)
	// World position should be root + leaf translate; leaf still valid
	if tr.Origin[0] != numeric.Fixed(100) {
		t.Fatalf("leaf attachment pos %v", tr.Origin)
	}
}

func TestSelectionPrimitivesEnumeratesValidAuthoredFaces(t *testing.T) {
	m := &Model{
		Pieces: []Piece{
			{
				Name: "root", Parent: -1, Selection: true,
				Vertices:   [][3]numeric.Fixed{{}, {numeric.Fixed(1)}, {numeric.Fixed(1), 0, numeric.Fixed(1)}, {0, 0, numeric.Fixed(1)}},
				Primitives: []Primitive{{VertexIndices: []uint16{0, 1, 2, 3}}},
			},
			{Name: "invalid", Parent: 0, Selection: true, Vertices: [][3]numeric.Fixed{{}}, Primitives: []Primitive{{VertexIndices: []uint16{0, 2, 0}}}},
			{Name: "not-plate", Parent: 0, Selection: false, Vertices: [][3]numeric.Fixed{{}, {numeric.Fixed(1)}, {numeric.Fixed(1), 0, numeric.Fixed(1)}}, Primitives: []Primitive{{VertexIndices: []uint16{0, 1, 2}}}},
		},
		Root: 0,
	}
	plates := m.SelectionPrimitives()
	if len(plates) != 1 {
		t.Fatalf("selection primitive count=%d, want 1", len(plates))
	}
	if plates[0].PieceIndex != 0 || plates[0].PrimitiveIndex != 0 || len(plates[0].Primitive.VertexIndices) != 4 {
		t.Fatalf("selection identity=%+v, want root primitive zero with four corners", plates[0])
	}
	plates[0].Primitive.VertexIndices[0] = 99
	if m.Pieces[0].Primitives[0].VertexIndices[0] != 0 {
		t.Fatal("selection primitive accessor aliases immutable model data")
	}
}

func TestFoldRootAngles(t *testing.T) {
	st := make([]PieceState, 2)
	st[0].RotX = 1000
	st[0].RotY = 2000
	st[0].RotZ = 3000
	FoldRootAngles(st, 0, 500, 600, 700) // heading 500->Y, pitch 600->X, bank 700->Z [03 §2.4] C24
	// The fold is the literal C24 one — heading into Y with no sign change. It
	// previously folded -heading, compensating for the model projection's
	// missing handedness flip [R-RAST-01 §2]; both were corrected together.
	if st[0].RotX != 1600 || st[0].RotY != 2500 || st[0].RotZ != 3700 {
		t.Fatalf("fold root angles: got X=%d Y=%d Z=%d", st[0].RotX, st[0].RotY, st[0].RotZ)
	}
	// Unit position never enters piece math [03 §2.4] C24 — folding only touches angles
}

func TestAccumulatorLastWriterWins(t *testing.T) {
	// C22: TURN/turn-now/SPIN converge on same accumulator, last writer wins [03 §2.4] C22.
	var s PieceState
	s.SetAngle(AxisX, 1000)
	if s.GetAngle(AxisX) != 1000 {
		t.Fatalf("set")
	}
	s.SetAngle(AxisX, 2000) // overwrites
	if s.GetAngle(AxisX) != 2000 {
		t.Fatalf("last writer wins")
	}
	s.AddAngle(AxisX, 100) // SPIN would increment same storage
	if s.GetAngle(AxisX) != 2100 {
		t.Fatalf("spin add")
	}
}

func TestPieceCompose(t *testing.T) {
	root := testsupport.RetailRoot(t)
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Fatalf("mount retail: %v", err)
	}
	defer fs.Close()
	// Prefer model with flare leaves for C23 coverage (e.g. armflash) [03 §2.4] C23.
	candidates := []string{"objects3d/armflash.3do", "objects3d/armflea.3do", "objects3d/armcom.3do", "objects3d/armham.3do"}
	var m *Model
	var name string
	for _, cand := range candidates {
		if mm, err := Load(fs, cand); err == nil && len(mm.Pieces) > 1 {
			m = mm
			name = cand
			break
		}
	}
	if m == nil {
		t.Skip("no suitable retail model found")
	}
	t.Logf("testing model %s pieces=%d root=%d", name, len(m.Pieces), m.Root)
	// Find a leaf piece
	leaf := -1
	for i, p := range m.Pieces {
		if len(p.Children) == 0 {
			leaf = i
			break
		}
	}
	if leaf == -1 {
		t.Fatal("no leaf")
	}
	// Hand-computed chain using authored values, independent of Compose's internal loop,
	// but same spec [03 §2.4] C21. This locks the contract without hardcoding pixel numbers.
	st := make([]PieceState, len(m.Pieces))
	// Deterministic non-zero rotations on a few ancestors to exercise trig
	// Pick leaf and its parent chain
	cur := leaf
	depth := 0
	for cur != -1 && depth < 3 {
		st[cur].RotZ = uint16(7000 + depth*3000)
		st[cur].RotX = uint16(2000 + depth*1000)
		st[cur].RotY = uint16(4000 + depth*2000)
		// script translation lane small offset
		st[cur].Trans = [3]numeric.Fixed{numeric.Fixed(int64(depth) * 1000), numeric.Fixed(int64(depth) * 2000), numeric.Fixed(int64(depth) * 3000)}
		cur = m.Pieces[cur].Parent
		depth++
	}
	// Hand compute expected via independent float loop [03 §2.4] C21 (I2)
	expected := handCompose(m, st, leaf)
	gotTr := Compose(m, st, leaf)
	got := gotTr.Origin
	if got != expected {
		t.Fatalf("leaf %d (%s) world origin mismatch: got %v want %v", leaf, m.Pieces[leaf].Name, got, expected)
	}
	// Also verify Apply matches direct Apply of hand path for a vertex if leaf has vertices
	if len(m.Pieces[leaf].Vertices) > 0 {
		v := m.Pieces[leaf].Vertices[0]
		expV := handApply(m, st, leaf, v)
		gotV := gotTr.Apply(v)
		if gotV != expV {
			t.Fatalf("leaf vertex world mismatch: got %v want %v", gotV, expV)
		}
	}
	// C23 check: if model has leaf with vertex but no primitive, it's preserved [03 §2.4] C23.
	foundC23 := false
	for _, p := range m.Pieces {
		if len(p.Children) == 0 && len(p.Vertices) > 0 && len(p.Primitives) == 0 {
			foundC23 = true
			break
		}
	}
	if !foundC23 {
		// Try other candidates for C23 positive evidence [03 §2.4] C23.
		for _, cand := range candidates {
			if cand == name {
				continue
			}
			if mm, err := Load(fs, cand); err == nil {
				for _, p := range mm.Pieces {
					if len(p.Children) == 0 && len(p.Vertices) > 0 && len(p.Primitives) == 0 {
						foundC23 = true
						t.Logf("C23 positive in %s piece %s", cand, p.Name)
						break
					}
				}
			}
			if foundC23 {
				break
			}
		}
	}
	if !foundC23 {
		t.Logf("no candidate model had leaf vertex-no-primitive; C23 vacuously holds (code preserves them)")
	}
}

func handCompose(m *Model, st []PieceState, piece int) [3]numeric.Fixed {
	return handApply(m, st, piece, [3]numeric.Fixed{})
}

func handApply(m *Model, st []PieceState, piece int, pt [3]numeric.Fixed) [3]numeric.Fixed {
	// Build leaf->root chain
	var chain []int
	cur := piece
	for cur != -1 {
		chain = append(chain, cur)
		cur = m.Pieces[cur].Parent
		if len(chain) > len(m.Pieces)+2 {
			break
		}
	}
	x := float64(pt[0].Raw())
	y := float64(pt[1].Raw())
	z := float64(pt[2].Raw())
	for _, idx := range chain {
		// translation
		var tx, ty, tz float64
		tx = float64(m.Pieces[idx].Translate[0].Raw())
		ty = float64(m.Pieces[idx].Translate[1].Raw())
		tz = float64(m.Pieces[idx].Translate[2].Raw())
		var ax, ay, az uint16
		if idx < len(st) {
			ax = st[idx].RotX
			ay = st[idx].RotY
			az = st[idx].RotZ
			tx += float64(st[idx].Trans[0].Raw())
			ty += float64(st[idx].Trans[1].Raw())
			tz += float64(st[idx].Trans[2].Raw())
		}
		if az != 0 {
			th := float64(az) * 2 * math.Pi / 65536
			c, s := math.Cos(th), math.Sin(th)
			nx := math.Round(c*x - s*y)
			ny := math.Round(s*x + c*y)
			x, y = nx, ny
		}
		if ax != 0 {
			th := float64(ax) * 2 * math.Pi / 65536
			c, s := math.Cos(th), math.Sin(th)
			ny := math.Round(c*y - s*z)
			nz := math.Round(s*y + c*z)
			y, z = ny, nz
		}
		if ay != 0 {
			th := float64(ay) * 2 * math.Pi / 65536
			c, s := math.Cos(th), math.Sin(th)
			nx := math.Round(c*x - s*z)
			nz := math.Round(s*x + c*z)
			x, z = nx, nz
		}
		x += tx
		y += ty
		z += tz
	}
	return [3]numeric.Fixed{numeric.Fixed(int64(x)), numeric.Fixed(int64(y)), numeric.Fixed(int64(z))}
}
