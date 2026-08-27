// Package model implements the 3DO piece hierarchy and transform composition [03 §2.4] [PLAN_06 WU-06-8].
//
// Contracts C20–C24 plus the model portion of the Public API block are owned here.
// Load-time primitive reordering (selection swap + mean-Y bubble sort) is already
// applied by formats.ThreeDO per [GAP 02-A6] — see formats/three_do.go primitive
// reordering loop — and is NOT redone here. This package owns the recursive
// half-turn negation pass and the world transform composition.
package model

import (
	"fmt"
	"math"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/vfs"
)

// Primitive is a 3DO face after load-time reordering [03 §2.4] C20 [GAP 02-A6].
// Geometry is retained for presentation (WU-13-7); simulation uses piece origins.
type Primitive struct {
	ColorIndex    uint32
	VertexIndices []uint16
	TextureName   string
	IsColored     int32
}

// Piece is one object/piece of a 3DO model [03 §2.4] [fmt 3do].
// Translate is the authored parent translation (16.16) after the half-turn pass [03 §2.4] C20.
// Vertices are after the half-turn pass [03 §2.4] C20.
// Primitives are after the load-time reorder already applied in formats [GAP 02-A6].
// Leaf pieces with a vertex but no primitive are valid attachment/emit points [03 §2.4] C23.
type Piece struct {
	Name       string
	Parent     int   // -1 for root [03 §2.4]
	Children   []int // sibling/child links depth-first [fmt 3do]
	Translate  [3]numeric.Fixed
	Vertices   [][3]numeric.Fixed
	Primitives []Primitive
	// Selection marks the load-time selection primitive.  It remains in the
	// primitive list at index zero so consumers can exclude it without
	// reinterpreting the source geometry [03 §2.4.1].
	Selection bool
}

// Model is an immutable compiled 3DO model [03 §2.4].
type Model struct {
	Pieces []Piece
	Root   int
	Name   string
	Hash   [32]byte
}

// Axis identifies a rotation axis for the mutation surface [03 §2.4] C21–C22.
const (
	AxisX = 0 // pitch per C24 root X = pitch [03 §2.4] C24 [03 §5.2]
	AxisY = 1 // heading/yaw per C24 root Y = heading [03 §2.4] C24
	AxisZ = 2 // bank per C24 root Z = bank [03 §2.4] C24
)

// PieceState carries the three uint16 rotation accumulators and the script
// translation lanes [03 §2.4] C21. All three axes share one storage per C22:
// TURN, turn-now and SPIN converge on the same accumulators through one adapter,
// last writer wins [03 §2.4] C22 (I2 allowlist: model draw trig).
//
// Rotation is 65,536 per circle [03 §2.4] C21, applied Z then X then Y via
// floating-point trig with round-to-nearest NOT through the fixed-point tables
// [03 §2.4] C21 (I2 row: model draw trig). Translation lanes are 16.16 scalar.
type PieceState struct {
	// RotX, RotY, RotZ are the per-axis accumulators [03 §2.4] C21.
	RotX uint16 // X axis [03 §2.4] C21–C22
	RotY uint16 // Y axis [03 §2.4] C21–C22
	RotZ uint16 // Z axis [03 §2.4] C21–C22
	// Trans is the script-driven translation offset summed with the authored
	// parent translation per [03 §2.4] C21: t_i = authored + script.
	Trans      [3]numeric.Fixed
	DontShade  bool // presentation flag selecting identity SHD row [03 §2.4.1]
	Hidden     bool // presentation visibility state [03 §2.4.1]
	DontShadow bool // presentation shadow suppression state [03 §5.3]
}

// SetAngle sets the accumulator for axis, last writer wins per [03 §2.4] C22.
// This is the single adapter surface for TURN, turn-now and SPIN [03 §2.4] C22.
func (s *PieceState) SetAngle(axis int, angle uint16) {
	switch axis {
	case AxisX:
		s.RotX = angle
	case AxisY:
		s.RotY = angle
	case AxisZ:
		s.RotZ = angle
	}
}

// GetAngle returns the accumulator for axis [03 §2.4] C21.
func (s *PieceState) GetAngle(axis int) uint16 {
	switch axis {
	case AxisX:
		return s.RotX
	case AxisY:
		return s.RotY
	case AxisZ:
		return s.RotZ
	}
	return 0
}

// AddAngle adds delta modulo 65536 [04 §5.1]. Used by SPIN integration which
// shares storage with TURN per [03 §2.4] C22 (last writer wins).
func (s *PieceState) AddAngle(axis int, delta uint16) {
	switch axis {
	case AxisX:
		s.RotX += delta
	case AxisY:
		s.RotY += delta
	case AxisZ:
		s.RotZ += delta
	}
}

// SetTrans sets the script translation lane for axis [03 §2.4] C21.
func (s *PieceState) SetTrans(axis int, v numeric.Fixed) {
	if axis >= 0 && axis < 3 {
		s.Trans[axis] = v
	}
}

// GetTrans returns the script translation lane [03 §2.4] C21.
func (s *PieceState) GetTrans(axis int) numeric.Fixed {
	if axis >= 0 && axis < 3 {
		return s.Trans[axis]
	}
	return 0
}

// FoldRootAngles folds unit orientation into the ROOT piece's accumulators per
// [03 §2.4] C24 [03 §5.2]: bank→Z, heading→Y, pitch→X as the outermost factor.
// Unit position never enters piece math [03 §2.4] C24. The addition wraps modulo
// 65536 via uint16 overflow.
//
// Projectile reuse: projectile models reuse the identical rotation helper with
// yaw in the Y slot, pitch in the X slot, each carrying a constant negative
// half-circle (-32768) authored model-facing offset [03 §5.2]. No projectile code
// is present in this package; the note documents the caller-side offset.
// Example caller: state[root].RotY += yaw - 32768; state[root].RotX += pitch.
func FoldRootAngles(st []PieceState, root int, heading, pitch, bank uint16) {
	if root < 0 || root >= len(st) {
		return
	}
	st[root].RotZ += bank // Z = bank [03 §2.4] C24
	// Heading increases toward +X (east) from north (+Z) per [03 §2.4] and [fmt 3do] "Model facing is −Z" note:
	// Y rotation positive is CCW (east→north) per applyChain, but engine heading increases clockwise (north→east) [03 §2.4] C21.
	// Fold as -heading (65536-heading) so heading 90 east (+X) rotates north (+Z) → east (+X) clockwise.
	st[root].RotY -= heading // Y = -heading [03 §2.4] C24 (retail clockwise)
	st[root].RotX += pitch   // X = pitch [03 §2.4] C24
}

// xformNode is a leaf→root snapshot for Transform application [03 §2.4] C21.
type xformNode struct {
	t  [3]numeric.Fixed
	ax uint16
	ay uint16
	az uint16
}

// Transform is the composed world transform for one piece [03 §2.4] C21.
// world(v) = M_root·…·M_leaf·v with M_i = T(t_i)·R_i [03 §2.4] C21.
// Each piece rotates about its own origin FIRST then translates [03 §2.4] C21.
// Applying the transform replays the chain with float trig round-to-nearest
// per [03 §2.4] (I2 allowlist: model draw trig), not fixed-point tables [03 §2.4] C25.
type Transform struct {
	Origin [3]numeric.Fixed // world position of piece origin [03 §2.4] C21
	nodes  []xformNode      // leaf→root snapshot for Apply
}

// Apply transforms a point from piece-local space to world space [03 §2.4] C21.
func (t Transform) Apply(v [3]numeric.Fixed) [3]numeric.Fixed {
	return applyChain(v, t.nodes)
}

// ApplyVertex is an alias for Apply [03 §2.4] C21.
func (t Transform) ApplyVertex(v [3]numeric.Fixed) [3]numeric.Fixed {
	return t.Apply(v)
}

// Position returns the world position of the piece origin [03 §2.4] C21.
// Equivalent to Apply([3]Fixed{0,0,0}).
func (t Transform) Position() [3]numeric.Fixed { return t.Origin }

// Load loads a 3DO model via the VFS and completes the load-time work owned
// by this package [03 §2.4] C20 [PLAN_06 WU-06-8].
//
//   - Primitive reordering (selection swap + mean-Y bubble sort) is already
//     applied by formats.LoadThreeDO per [GAP 02-A6]; cited here and NOT redone
//     per the ownership note in docs/PLAN_06_UNITS_ORDERS_COB.md WU-06-8.
//   - The recursive half-turn pass negating first and third vertex coordinates
//     and first and third parent translations of every object is applied HERE
//     per [03 §2.4] C20.
//   - Draw order is fixed at load time; a per-frame sort does not reproduce
//     retail tie order [03 §2.4] C20.
//
// Authored translations and vertices are 16.16 signed [fmt 3do] stored as
// numeric.Fixed per docs/INVARIANTS.md I2. Leaf pieces with vertices but no
// primitive are preserved as valid attachment points [03 §2.4] C23.
func Load(fs vfs.FSOps, name string) (*Model, error) {
	if fs == nil {
		return nil, fmt.Errorf("model: nil filesystem")
	}
	three, err := formats.LoadThreeDOFile(fs, name)
	if err != nil {
		return nil, err
	}
	return buildModel(three, name)
}

func buildModel(three *formats.ThreeDO, name string) (*Model, error) {
	if three == nil {
		return nil, fmt.Errorf("model: nil threeDO")
	}
	if len(three.Objects) == 0 {
		return nil, fmt.Errorf("model: empty object table")
	}
	if three.Root < 0 || int(three.Root) >= len(three.Objects) {
		return nil, fmt.Errorf("model: bad root %d", three.Root)
	}
	m := &Model{
		Pieces: make([]Piece, len(three.Objects)),
		Root:   int(three.Root),
		Name:   name,
		Hash:   three.ContentHash,
	}
	for i, obj := range three.Objects {
		p := &m.Pieces[i]
		p.Name = obj.Name
		if obj.Parent >= 0 {
			p.Parent = int(obj.Parent)
		} else {
			p.Parent = -1
		}
		// Authored parent translation 16.16 [fmt 3do] -> Fixed, then half-turn
		// negation of first and third components per [03 §2.4] C20.
		// The same negation applies to every vertex X and Z per [03 §2.4] C20.
		tx := numeric.Fixed(obj.Translation[0])
		ty := numeric.Fixed(obj.Translation[1])
		tz := numeric.Fixed(obj.Translation[2])
		// Half-turn about vertical axis: negate X and Z [03 §2.4] C20.
		tx = -tx
		tz = -tz
		p.Translate = [3]numeric.Fixed{tx, ty, tz}
		// Vertices after half-turn [03 §2.4] C20 [fmt 3do].
		p.Vertices = make([][3]numeric.Fixed, len(obj.Vertices))
		for j, v := range obj.Vertices {
			x := numeric.Fixed(v.X)
			y := numeric.Fixed(v.Y)
			z := numeric.Fixed(v.Z)
			x = -x // half-turn [03 §2.4] C20
			z = -z
			p.Vertices[j] = [3]numeric.Fixed{x, y, z}
		}
		p.Primitives = make([]Primitive, len(obj.Primitives))
		p.Selection = obj.Selection == 0
		for j, pr := range obj.Primitives {
			cp := make([]uint16, len(pr.VertexIndices))
			copy(cp, pr.VertexIndices)
			p.Primitives[j] = Primitive{
				ColorIndex:    pr.ColorIndex,
				VertexIndices: cp,
				TextureName:   pr.TextureName,
				IsColored:     pr.IsColored,
			}
		}
	}
	// Build Children lists from Parent [fmt 3do] [03 §2.4].
	for i := range m.Pieces {
		m.Pieces[i].Children = nil
	}
	for i, p := range m.Pieces {
		if p.Parent >= 0 && p.Parent < len(m.Pieces) {
			parent := p.Parent
			m.Pieces[parent].Children = append(m.Pieces[parent].Children, i)
		}
	}
	return m, nil
}

// Compose returns the world transform for piece index [03 §2.4] C21.
// world(v) = M_root·…·M_leaf·v with M_i = T(t_i)·R_i [03 §2.4] C21.
// Per-node translation is authored + script lanes summed componentwise [03 §2.4] C21.
// Rotations are Z then X then Y via float trig round-to-nearest NOT fixed tables
// [03 §2.4] C21 (I2 allowlist row: model draw trig) [04 §5.1] for the simulation path.
// PieceState is defined here so cob can hold it later [PLAN_06 Public API].
// C24 root orientation folding is NOT included; call FoldRootAngles on a copy
// before Compose if unit angles must be incorporated [03 §2.4] C24 [03 §5.2].
func Compose(m *Model, st []PieceState, piece int) Transform {
	if m == nil || piece < 0 || piece >= len(m.Pieces) {
		return Transform{}
	}
	// Collect leaf→root chain [03 §2.4] C21.
	chain := make([]int, 0, 8)
	cur := piece
	seen := make(map[int]bool, len(m.Pieces))
	for cur != -1 {
		if seen[cur] {
			break // cycle guard — retail files are trees [fmt 3do]
		}
		seen[cur] = true
		chain = append(chain, cur)
		if cur < 0 || cur >= len(m.Pieces) {
			break
		}
		cur = m.Pieces[cur].Parent
		if len(chain) > len(m.Pieces) {
			break
		}
	}
	nodes := make([]xformNode, len(chain))
	for i, idx := range chain {
		var t [3]numeric.Fixed
		t = m.Pieces[idx].Translate
		var ax, ay, az uint16
		if idx >= 0 && idx < len(st) {
			ax = st[idx].RotX
			ay = st[idx].RotY
			az = st[idx].RotZ
			t[0] = t[0].Add(st[idx].Trans[0])
			t[1] = t[1].Add(st[idx].Trans[1])
			t[2] = t[2].Add(st[idx].Trans[2])
		}
		nodes[i] = xformNode{t: t, ax: ax, ay: ay, az: az}
	}
	origin := applyChain([3]numeric.Fixed{}, nodes)
	return Transform{Origin: origin, nodes: nodes}
}

func applyChain(p [3]numeric.Fixed, nodes []xformNode) [3]numeric.Fixed {
	// Work in float64 on raw Fixed values then round per [03 §2.4] C21 (I2).
	x := float64(p[0].Raw())
	y := float64(p[1].Raw())
	z := float64(p[2].Raw())
	for _, n := range nodes {
		if n.az != 0 {
			theta := float64(n.az) * 2 * math.Pi / 65536 // [03 §2.4] C21
			c := math.Cos(theta)
			s := math.Sin(theta)
			nx := math.Round(c*x - s*y) // Rz: x' = c*x - s*y ; y' = s*x + c*y [03 §2.4] C21
			ny := math.Round(s*x + c*y)
			x, y = nx, ny
		}
		if n.ax != 0 {
			theta := float64(n.ax) * 2 * math.Pi / 65536
			c := math.Cos(theta)
			s := math.Sin(theta)
			ny := math.Round(c*y - s*z) // Rx: y' = c*y - s*z ; z' = s*y + c*z [03 §2.4] C21
			nz := math.Round(s*y + c*z)
			y, z = ny, nz
		}
		if n.ay != 0 {
			theta := float64(n.ay) * 2 * math.Pi / 65536
			c := math.Cos(theta)
			s := math.Sin(theta)
			nx := math.Round(c*x - s*z) // Ry: x' = c*x - s*z ; z' = s*x + c*z [03 §2.4] C21
			nz := math.Round(s*x + c*z)
			x, z = nx, nz
		}
		x += float64(n.t[0].Raw())
		y += float64(n.t[1].Raw())
		z += float64(n.t[2].Raw())
	}
	return [3]numeric.Fixed{numeric.Fixed(int64(x)), numeric.Fixed(int64(y)), numeric.Fixed(int64(z))}
}
