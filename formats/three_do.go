package formats

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"

	"github.com/nanolathe/nanolathe/vfs"
)

// ThreeDOLimits bounds the object, vertex and primitive counts one model may
// declare.
type ThreeDOLimits struct {
	MaxDepth, MaxObjects, MaxVertices, MaxPrimitives, MaxPolygonVertices uint32
	MaxIndices                                                           uint64
	MaxStringBytes                                                       uint32
}

// DefaultThreeDOLimits returns the decode bounds used when a caller states none.
func DefaultThreeDOLimits() ThreeDOLimits {
	return ThreeDOLimits{MaxDepth: 64, MaxObjects: 1 << 16, MaxVertices: 1 << 22, MaxPrimitives: 1 << 22, MaxPolygonVertices: 1 << 16, MaxIndices: 1 << 22, MaxStringBytes: 1 << 20}
}

// ThreeDOVertex is one model-space vertex in the file's own integer units.
type ThreeDOVertex struct{ X, Y, Z int32 }

// ThreeDOPrimitive is one face: its vertex indices, its texture name and the
// per-face words the file carries [fmt 3do].
type ThreeDOPrimitive struct {
	ColorIndex         uint32
	VertexIndices      []uint16
	TextureName        string
	AlwaysZero         int32
	Unknown1, Unknown2 int32
	IsColored          int32
	SourceOffset       uint32
}

// ThreeDOObject is the lossless source-level piece record plus resolved
// topology indexes. Child and sibling indexes point into Objects; -1 is nil.
type ThreeDOObject struct {
	SourceOffset uint32
	Version      int32
	AlwaysZero   int32
	Selection    int32
	Translation  [3]int32
	Name         string
	Vertices     []ThreeDOVertex
	Primitives   []ThreeDOPrimitive
	Parent       int32
	FirstChild   int32
	NextSibling  int32
}

// ThreeDO is a decoded model: the piece hierarchy with each piece's
// vertices, primitives and pivot [fmt 3do].
type ThreeDO struct {
	ContentHash [32]byte
	// Raw owns the one immutable source backing store used during parsing.
	// Compilers derive simulation/presentation products from the parsed object
	// table and never need to reread this byte slice.
	Raw     []byte
	Root    int32
	Objects []ThreeDOObject
}

// ThreeDOModel is an alias kept for readers that name the whole model rather
// than the format.
type ThreeDOModel = ThreeDO

// LoadThreeDO decodes a model under the default limits [fmt 3do].
func LoadThreeDO(data []byte) (*ThreeDO, error) {
	return LoadThreeDOWithLimits(data, DefaultThreeDOLimits())
}

// LoadThreeDOWithLimits decodes a model under explicit bounds [fmt 3do].
func LoadThreeDOWithLimits(data []byte, limits ThreeDOLimits) (*ThreeDO, error) {
	if len(data) < 52 {
		return nil, fmt.Errorf("3do: root object is truncated")
	}
	if limits.MaxDepth == 0 || limits.MaxObjects == 0 || limits.MaxVertices == 0 || limits.MaxPrimitives == 0 || limits.MaxPolygonVertices == 0 || limits.MaxIndices == 0 || limits.MaxStringBytes == 0 {
		return nil, fmt.Errorf("3do: invalid decode limits")
	}
	data = append([]byte(nil), data...)
	result := &ThreeDO{Root: -1, Raw: data, ContentHash: sha256.Sum256(data)}
	active := make(map[uint32]bool)
	seen := make(map[uint32]bool)
	var totalVertices, totalPrimitives uint32
	var totalIndices uint64
	var parseObject func(uint32, int32, uint32) (int32, uint32, error)
	var parseSiblings func(uint32, int32, uint32) (int32, error)
	parseObject = func(off uint32, parent int32, depth uint32) (int32, uint32, error) {
		if off == 0 && len(result.Objects) > 0 {
			return -1, 0, fmt.Errorf("3do: object offset 0 is reused")
		}
		if depth > limits.MaxDepth {
			return -1, 0, fmt.Errorf("3do: hierarchy depth exceeds limit")
		}
		if uint64(off)+52 > uint64(len(data)) {
			return -1, 0, fmt.Errorf("3do: object at 0x%x is outside file", off)
		}
		if active[off] {
			return -1, 0, fmt.Errorf("3do: object cycle at 0x%x", off)
		}
		if seen[off] {
			return -1, 0, fmt.Errorf("3do: shared object at 0x%x", off)
		}
		if uint32(len(result.Objects)) >= limits.MaxObjects {
			return -1, 0, fmt.Errorf("3do: object count exceeds limit")
		}
		active[off], seen[off] = true, true
		defer delete(active, off)
		i := int32(len(result.Objects))
		result.Objects = append(result.Objects, ThreeDOObject{SourceOffset: off, Parent: parent, FirstChild: -1, NextSibling: -1})
		read := func(n int) int32 { return int32(binary.LittleEndian.Uint32(data[int(off)+n : int(off)+n+4])) }
		obj := &result.Objects[i]
		obj.Version = read(0)
		vertexCount, primitiveCount := read(4), read(8)
		obj.Selection = read(12)
		obj.Translation = [3]int32{read(16), read(20), read(24)}
		obj.AlwaysZero = read(32)
		nameOffset, vertexOffset, primitiveOffset := read(28), read(36), read(40)
		siblingOffset, childOffset := read(44), read(48)
		if obj.Version != 1 {
			return -1, 0, fmt.Errorf("3do: object at 0x%x has version %d", off, obj.Version)
		}
		if vertexCount < 0 || primitiveCount < 0 {
			return -1, 0, fmt.Errorf("3do: negative count at 0x%x", off)
		}
		if uint64(totalVertices)+uint64(vertexCount) > uint64(limits.MaxVertices) || uint64(totalPrimitives)+uint64(primitiveCount) > uint64(limits.MaxPrimitives) {
			return -1, 0, fmt.Errorf("3do: geometry count exceeds limits")
		}
		name, err := threeDOString(data, nameOffset, limits.MaxStringBytes)
		if err != nil {
			return -1, 0, fmt.Errorf("3do: object 0x%x name: %w", off, err)
		}
		obj.Name = name
		vertexBytes, err := threeDOSlice(data, vertexOffset, uint64(vertexCount), 12)
		if err != nil {
			return -1, 0, fmt.Errorf("3do: object 0x%x vertices: %w", off, err)
		}
		obj.Vertices = make([]ThreeDOVertex, int(vertexCount))
		for n := range obj.Vertices {
			b := vertexBytes[n*12:]
			obj.Vertices[n] = ThreeDOVertex{int32(binary.LittleEndian.Uint32(b)), int32(binary.LittleEndian.Uint32(b[4:])), int32(binary.LittleEndian.Uint32(b[8:]))}
		}
		totalVertices += uint32(vertexCount)
		primitiveBytes, err := threeDOSlice(data, primitiveOffset, uint64(primitiveCount), 32)
		if err != nil {
			return -1, 0, fmt.Errorf("3do: object 0x%x primitives: %w", off, err)
		}
		obj.Primitives = make([]ThreeDOPrimitive, int(primitiveCount))
		for n := range obj.Primitives {
			b := primitiveBytes[n*32:]
			count := int32(binary.LittleEndian.Uint32(b[4:]))
			if count < 0 || uint32(count) > limits.MaxPolygonVertices {
				return -1, 0, fmt.Errorf("3do: primitive %d at object 0x%x has invalid vertex count %d", n, off, count)
			}
			indices := []uint16(nil)
			if count > 0 {
				if uint64(count) > limits.MaxIndices-totalIndices {
					return -1, 0, fmt.Errorf("3do: aggregate polygon indexes exceed limit")
				}
				indexBytes, e := threeDOSlice(data, int32(binary.LittleEndian.Uint32(b[12:])), uint64(count), 2)
				if e != nil {
					return -1, 0, fmt.Errorf("3do: primitive %d indexes: %w", n, e)
				}
				indices = make([]uint16, int(count))
				for j := range indices {
					indices[j] = binary.LittleEndian.Uint16(indexBytes[j*2:])
					if uint32(indices[j]) >= uint32(vertexCount) {
						return -1, 0, fmt.Errorf("3do: primitive %d vertex index %d exceeds vertex count %d", n, indices[j], vertexCount)
					}
				}
				totalIndices += uint64(count)
			}
			textureOffset := int32(binary.LittleEndian.Uint32(b[16:]))
			texture := ""
			if textureOffset != 0 {
				texture, err = threeDOString(data, textureOffset, limits.MaxStringBytes)
				if err != nil {
					return -1, 0, fmt.Errorf("3do: primitive %d texture: %w", n, err)
				}
			}
			obj.Primitives[n] = ThreeDOPrimitive{ColorIndex: binary.LittleEndian.Uint32(b), VertexIndices: indices, TextureName: texture, AlwaysZero: int32(binary.LittleEndian.Uint32(b[8:])), Unknown1: int32(binary.LittleEndian.Uint32(b[20:])), Unknown2: int32(binary.LittleEndian.Uint32(b[24:])), IsColored: int32(binary.LittleEndian.Uint32(b[28:])), SourceOffset: uint32(primitiveOffset) + uint32(n*32)}
		}
		totalPrimitives += uint32(primitiveCount)
		if childOffset != 0 {
			child, e := parseSiblings(uint32(childOffset), i, depth+1)
			if e != nil {
				return -1, 0, e
			}
			// Recursive parsing may grow result.Objects and invalidate obj.
			result.Objects[i].FirstChild = child
		}
		return i, uint32(siblingOffset), nil
	}
	parseSiblings = func(off uint32, parent int32, depth uint32) (int32, error) {
		first, previous := int32(-1), int32(-1)
		for off != 0 {
			current, sibling, err := parseObject(off, parent, depth)
			if err != nil {
				return -1, err
			}
			if previous >= 0 {
				result.Objects[previous].NextSibling = current
			} else {
				first = current
			}
			previous = current
			off = sibling
		}
		return first, nil
	}
	root, siblingOffset, err := parseObject(0, -1, 0)
	if err != nil {
		return nil, err
	}
	if siblingOffset != 0 {
		sibling, err := parseSiblings(siblingOffset, -1, 0)
		if err != nil {
			return nil, err
		}
		result.Objects[root].NextSibling = sibling
	}
	result.Root = root
	// Retail load-time primitive reordering (02:3DO). After relocation, before
	// first draw: if object declares a selection primitive, swap it with
	// primitive zero and rewrite index to 0, then bubble-sort primitives
	// 1..n-1 ascending by mean of vertices' second coordinate (Y) via
	// integer division by vertex count. Draw order is fixed at load, not per
	// frame; an implementation that sorts at draw time misorders ties.
	for i := range result.Objects {
		obj := &result.Objects[i]
		if obj.Selection >= 0 && obj.Selection < int32(len(obj.Primitives)) {
			if obj.Selection != 0 {
				obj.Primitives[0], obj.Primitives[obj.Selection] = obj.Primitives[obj.Selection], obj.Primitives[0]
			}
			obj.Selection = 0
		}
		if len(obj.Primitives) > 1 {
			// Bubble from 1 upward; keep primitive 0 (selection) fixed.
			for end := len(obj.Primitives) - 1; end > 1; end-- {
				swapped := false
				for j := 1; j < end; j++ {
					meanJ := primitiveMeanY(obj, j)
					meanNext := primitiveMeanY(obj, j+1)
					if meanNext < meanJ {
						obj.Primitives[j], obj.Primitives[j+1] = obj.Primitives[j+1], obj.Primitives[j]
						swapped = true
					}
				}
				if !swapped {
					break
				}
			}
		}
	}
	return result, nil
}

func primitiveMeanY(obj *ThreeDOObject, index int) int32 {
	if index < 0 || index >= len(obj.Primitives) {
		return 0
	}
	prim := obj.Primitives[index]
	if len(prim.VertexIndices) == 0 {
		return 0
	}
	var sum int64
	for _, vi := range prim.VertexIndices {
		if int(vi) < len(obj.Vertices) {
			sum += int64(obj.Vertices[vi].Y)
		}
	}
	return int32(sum / int64(len(prim.VertexIndices)))
}

func threeDOSlice(data []byte, off int32, count, stride uint64) ([]byte, error) {
	if off < 0 || count != 0 && count > math.MaxUint64/stride {
		return nil, fmt.Errorf("invalid offset/count")
	}
	size := count * stride
	if uint64(off) > uint64(len(data)) || size > uint64(len(data))-uint64(off) {
		return nil, fmt.Errorf("range 0x%x+%d outside file", off, size)
	}
	return data[int(off):int(uint64(off)+size)], nil
}

func threeDOString(data []byte, off int32, max uint32) (string, error) {
	if off < 0 || uint64(off) >= uint64(len(data)) {
		return "", fmt.Errorf("string offset 0x%x outside file", off)
	}
	tail := data[off:]
	if uint64(len(tail)) > uint64(max) {
		tail = tail[:max]
	}
	for i, c := range tail {
		if c == 0 {
			return string(tail[:i]), nil
		}
	}
	return "", fmt.Errorf("unterminated string at 0x%x", off)
}

// LoadThreeDOFile reads and decodes a model from the VFS.
func LoadThreeDOFile(fs vfs.FSOps, name string) (*ThreeDO, error) {
	data, err := readVFS(fs, name)
	if err != nil {
		return nil, err
	}
	return LoadThreeDO(data)
}

// ModelTop returns the model's top extent in 16.16 world units, matching the
// retail loader helper whose result the unit definition stores for
// presentation and visibility [03 §3.2].
//
// Retail walks the piece and its siblings, taking the maximum of
// `vertex.Y + piece.Translation.Y` over every vertex, and for each child
// subtree the recursive result plus this piece's own Y translation. The
// accumulator starts at zero and only ever grows, so the value is floored at
// zero and a model entirely below its origin reports 0.
//
// The LOS writer reads only the HIGH word of that dword — the top in whole
// world units — as the observer's height addend, which is why the eye sits at
// the model's top rather than on the ground [03 §3.2][03 R-P0-18-A §1]. (That
// sentence previously named the definition word by its executable offset; the
// offset is a clean-room violation and carries no information the logical name
// does not.)
//
// There is deliberately NO min-Y counterpart to this walk. Established
// [02 R-CAT-01 §7][07 R-REV-01 §7]: retail's catalog loader zeroes the
// definition's minimum-Y word immediately before calling this helper, stores
// the helper's answer as the maximum-Y word, and rewrites the Y extent as
// `max - min` — which is why the Y extent simply is the model total height.
// The helper above is the only bound retail derives from model geometry; the X
// and Z bounds come from the authored footprint. Every consumer that looks like
// it wants a "model bottom" — the `setSFXoccupy` band-3 test
// [04 R-MOV-01 §8a], the transport lowering offset [04 R-AIR-01 §9] — reads
// this same maximum-Y word instead. Do not add an inverted walk here.
func (t *ThreeDO) ModelTop() int32 {
	if t == nil || len(t.Objects) == 0 {
		return 0
	}
	return t.modelTopFrom(t.Root)
}

// modelTopFrom walks one piece and every
// following sibling, each contributing its own vertices and its child subtree.
func (t *ThreeDO) modelTopFrom(index int32) int32 {
	var top int32 // the model-top accumulator starts at zero [03 §2.4]
	for index >= 0 && int(index) < len(t.Objects) {
		obj := &t.Objects[index]
		ty := obj.Translation[1]
		for _, v := range obj.Vertices {
			if y := v.Y + ty; y > top {
				top = y
			}
		}
		if obj.FirstChild >= 0 {
			if y := t.modelTopFrom(obj.FirstChild) + ty; y > top {
				top = y
			}
		}
		index = obj.NextSibling
	}
	return top
}
