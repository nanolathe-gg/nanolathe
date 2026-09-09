package render

import (
	"math"

	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// FrozenFragmentMaterial is the scalar material identity copied when a shatter
// fragment is admitted. Valid is false when artwork could not be resolved;
// physical admission and its eight simulation draws still occur [04 R-COB-04 §3].
type FrozenFragmentMaterial struct {
	UnitDefID      uint16
	PieceIndex     int
	PrimitiveIndex int
	FrameIndex     int32
	Valid          bool
}

// FragmentQuad is one already eligible quadrilateral. The session adapter
// supplies primitives in model order and excludes ground plates and flagged
// primitives [04 R-COB-04 §3].
type FragmentQuad struct {
	PrimitiveIndex int
	Vertices       [4][3]numeric.Fixed
}

// FragmentMaterialFreeze snapshots one material after physical admission won a
// paired effect/geometry slot. It is never a veto: an invalid result retains the
// geometry and its deterministic random draws [04 R-COB-04 §3].
type FragmentMaterialFreeze func(unitDefID uint16, pieceIndex int, quad FragmentQuad) FrozenFragmentMaterial

// FragmentRequest contains posed piece quads and their world-space origin.
// Position is not changed by vertex centering [04 R-COB-04 §3]. Session uses
// the approved current-simulation-pose source, independent of draw history
// [DESIGN_UNITS_ORDERS_COB §3.3 "Shatter core API"].
type FragmentRequest struct {
	UnitDefID     uint16
	PieceIndex    int
	Position      [3]numeric.Fixed
	MoverVelocity [3]numeric.Fixed
	Gravity       numeric.Fixed
	ExplodeOnHit  bool
	Quads         []FragmentQuad
	Freeze        FragmentMaterialFreeze
}

// FragmentStepContext provides the session values used by the fixed-effect
// phase. WaterEffectsWordZero retains the established zero polarity [04
// R-COB-04 §2][04 R-COB-04 §3].
type FragmentStepContext struct {
	TerrainHeight        func(x, z numeric.Fixed) numeric.Fixed
	SeaLevel             numeric.Fixed
	Gravity              numeric.Fixed
	WaterEffectsWordZero bool
	Lava                 bool
	Impact               FragmentImpactSink
}

// GroundFragmentImpact is the synchronous request emitted before its paired
// geometry and effect record are released [04 R-COB-04 §3][04 R-COB-04 §4].
type GroundFragmentImpact struct {
	Position             [3]numeric.Fixed
	Graphic              string
	CalculatedFrameTable uint8
	AboveSeaFlash        bool
}

// WaterFragmentImpact is the synchronous water request for a fragment. It has
// neither calculated frames nor an above-sea flash [04 R-COB-04 §3].
type WaterFragmentImpact struct {
	Position [3]numeric.Fixed
	Graphic  string
	Lava     bool
}

// FragmentImpactSink receives collision requests while the dying record still
// owns its geometry. It must admit synchronously into the same fixed pool [I5].
type FragmentImpactSink interface {
	GroundFragmentImpact(GroundFragmentImpact)
	WaterFragmentImpact(WaterFragmentImpact)
}

// FragmentMetadata is the detached geometry read for the frame adapter.
// Slot is zero-based; EffectView.FragmentSlot is its one-based owner identity.
type FragmentMetadata struct {
	Slot     int
	Position [3]numeric.Fixed
	Angles   [3]uint16
	Vertices [8][3]numeric.Fixed
	Material FrozenFragmentMaterial
}

type fragmentGeometry struct {
	live         bool
	baseVelocity [3]int32
	angles       [3]uint16
	angularRates [3]uint16
	vertices     [8][3]numeric.Fixed
	material     FrozenFragmentMaterial
}

// AdmitShatter claims a paired effect record and geometry slot per supplied
// eligible quad. It stops at the shared fixed-effect capacity before running
// the eight fragment draws for a refused quad [04 R-COB-04 §3]. The session
// supplies its simulation draw operation for this call; the pool retains none.
func (p *FixedEffectPool) AdmitShatter(req FragmentRequest, draw func(uint32) uint32) bool {
	if p == nil || draw == nil {
		return false
	}
	admitted := false
	for _, quad := range req.Quads {
		if len(p.records) >= FixedEffectCap {
			break
		}
		slot := p.firstFreeFragment()
		if slot < 0 {
			// Paired ownership makes this unreachable while a record can be
			// claimed. Do not create a stale record as a compatibility path.
			break
		}
		material := FrozenFragmentMaterial{
			UnitDefID: req.UnitDefID, PieceIndex: req.PieceIndex, PrimitiveIndex: quad.PrimitiveIndex,
		}
		if req.Freeze != nil {
			material = req.Freeze(req.UnitDefID, req.PieceIndex, quad)
		}
		geometry, velocity := buildFragmentGeometry(req, quad, material, draw)
		geometry.live = true
		p.fragments[slot] = geometry
		record := EffectRecord{
			X: fragmentWord(fragmentRaw(req.Position[0])), Y: fragmentWord(fragmentRaw(req.Position[1])), Z: fragmentWord(fragmentRaw(req.Position[2])),
			VX: numeric.Fixed(velocity[0]), VY: numeric.Fixed(velocity[1]), VZ: numeric.Fixed(velocity[2]),
			HasModel: true, Strip: -1, FragmentSlot: uint16(slot + 1), FragmentExplodeOnHit: req.ExplodeOnHit,
		}
		if !p.Append(record) {
			p.fragments[slot] = fragmentGeometry{}
			break
		}
		admitted = true
	}
	return admitted
}

// SetFragmentStepContext installs the current effect-phase collision context.
// Until the session adapter supplies terrain, fragment physics has no terrain
// result to substitute, so update leaves the paired record intact.
func (p *FixedEffectPool) SetFragmentStepContext(ctx FragmentStepContext) {
	if p == nil {
		return
	}
	p.fragmentContext = ctx
	p.fragmentStepping = ctx.TerrainHeight != nil
}

// FragmentMetadataInto appends live geometry in stable effect-record order. The
// presentation adapter follows its one-based FragmentSlot identity.
func (p *FixedEffectPool) FragmentMetadataInto(out []FragmentMetadata) []FragmentMetadata {
	if p == nil {
		return out[:0]
	}
	out = out[:0]
	for _, record := range p.records {
		if record.FragmentSlot == 0 {
			continue
		}
		slot := int(record.FragmentSlot) - 1
		if slot < 0 || slot >= len(p.fragments) || !p.fragments[slot].live {
			continue
		}
		geometry := &p.fragments[slot]
		out = append(out, FragmentMetadata{
			Slot: slot, Position: [3]numeric.Fixed{record.X, record.Y, record.Z},
			Angles: geometry.angles, Vertices: geometry.vertices, Material: geometry.material,
		})
	}
	return out
}

func (p *FixedEffectPool) firstFreeFragment() int {
	for i := range p.fragments {
		if !p.fragments[i].live {
			return i
		}
	}
	return -1
}

func (p *FixedEffectPool) releaseFragment(slot uint16) {
	if p == nil || slot == 0 {
		return
	}
	i := int(slot) - 1
	if i >= 0 && i < len(p.fragments) {
		p.fragments[i] = fragmentGeometry{}
	}
}

func (p *FixedEffectPool) stepFragment(index int) {
	if p == nil || index < 0 || index >= len(p.records) || !p.fragmentStepping {
		return
	}
	record := &p.records[index]
	if record.FragmentSlot == 0 {
		return
	}
	slot := int(record.FragmentSlot) - 1
	if slot < 0 || slot >= len(p.fragments) || !p.fragments[slot].live {
		record.FragmentSlot = 0
		record.HasModel = false
		return
	}
	geometry := &p.fragments[slot]
	ctx := p.fragmentContext
	previous := [3]numeric.Fixed{record.X, record.Y, record.Z}
	record.X = fragmentAdd(fragmentAdd(record.X, fragmentWord(geometry.baseVelocity[0])), record.VX)
	record.Y = fragmentAdd(fragmentAdd(record.Y, fragmentWord(geometry.baseVelocity[1])), record.VY)
	record.Z = fragmentAdd(fragmentAdd(record.Z, fragmentWord(geometry.baseVelocity[2])), record.VZ)
	record.VY = fragmentSub(record.VY, ctx.Gravity)
	geometry.angles[0] += geometry.angularRates[0]
	geometry.angles[1] += geometry.angularRates[1]
	geometry.angles[2] += geometry.angularRates[2]

	terrain := fragmentWhole(ctx.TerrainHeight(record.X, record.Z))
	sea := fragmentWhole(ctx.SeaLevel)
	groundDomain := fragmentRaw(record.Y) > fragmentRaw(ctx.SeaLevel) || terrain >= sea
	if !groundDomain {
		if record.FragmentExplodeOnHit && ctx.WaterEffectsWordZero && ctx.Impact != nil {
			graphic := "h2oboom2"
			if ctx.Lava {
				graphic = "lavasplash"
			}
			ctx.Impact.WaterFragmentImpact(WaterFragmentImpact{Position: [3]numeric.Fixed{record.X, record.Y, record.Z}, Graphic: graphic, Lava: ctx.Lava})
		}
		p.endFragment(index)
		return
	}
	if fragmentWhole(record.Y) > terrain {
		return
	}
	record.X, record.Y, record.Z = previous[0], previous[1], previous[2]
	record.VY = fragmentWord(-(int32(record.VY) / 2))
	if fragmentWhole(record.VY) >= 1 {
		return
	}
	if record.FragmentExplodeOnHit && ctx.Impact != nil {
		ctx.Impact.GroundFragmentImpact(GroundFragmentImpact{
			Position: [3]numeric.Fixed{record.X, record.Y, record.Z}, Graphic: "explosion", CalculatedFrameTable: 0, AboveSeaFlash: true,
		})
	}
	p.endFragment(index)
}

func (p *FixedEffectPool) endFragment(index int) {
	if p == nil || index < 0 || index >= len(p.records) {
		return
	}
	record := &p.records[index]
	p.releaseFragment(record.FragmentSlot)
	record.FragmentSlot = 0
	record.HasModel = false
}

func buildFragmentGeometry(req FragmentRequest, quad FragmentQuad, material FrozenFragmentMaterial, draw func(uint32) uint32) (fragmentGeometry, [3]int32) {
	geometry := fragmentGeometry{material: material}
	for i := 0; i < 4; i++ {
		geometry.vertices[i] = quad.Vertices[i]
		geometry.vertices[4+i] = quad.Vertices[3-i]
	}
	normal := fragmentNormal(quad)
	var velocity [3]int32
	velocity[0] = fragmentMul32(80-int32(draw(160)), 512)
	velocity[2] = fragmentMul32(80-int32(draw(160)), 512)
	velocity[1] = fragmentAdd32(fragmentMul32(80-int32(draw(160)), 512), fragmentMul32(30, fragmentRaw(req.Gravity)))
	geometry.baseVelocity = [3]int32{fragmentRaw(req.MoverVelocity[0]) >> 1, fragmentRaw(req.MoverVelocity[1]) >> 1, fragmentRaw(req.MoverVelocity[2]) >> 1}
	geometry.angularRates = [3]uint16{
		uint16(800 - int32(draw(1600))),
		uint16(800 - int32(draw(1600))),
		uint16(800 - int32(draw(1600))),
	}
	velocity[0] = fragmentAdd32(velocity[0], fragmentMul32(int32(draw(200)), int32(int16(numeric.TruncateFloat64ToLow32(float64(normal[0])*512)))))
	velocity[2] = fragmentSub32(velocity[2], fragmentMul32(int32(draw(200)), int32(int16(numeric.TruncateFloat64ToLow32(float64(normal[2])*512)))))
	for i := 4; i < 8; i++ {
		for axis := 0; axis < 3; axis++ {
			geometry.vertices[i][axis] = fragmentSub(geometry.vertices[i][axis], fragmentWord(numeric.TruncateFloat64ToLow32(float64(normal[axis])*65535)))
		}
	}
	for axis := 0; axis < 3; axis++ {
		var sum int32
		for i := range geometry.vertices {
			sum = fragmentAdd32(sum, fragmentRaw(geometry.vertices[i][axis]))
		}
		mean := sum / 8
		for i := range geometry.vertices {
			geometry.vertices[i][axis] = fragmentSub(geometry.vertices[i][axis], fragmentWord(mean))
		}
	}
	return geometry, velocity
}

// fragmentNormal preserves the helper boundaries of the retail calculation:
// differences, cross-product components, and normalized components are stored
// as binary32. Square, sum, square-root and division remain at working
// precision until the component stores [04 R-COB-04 §3][I2].
// TODO(question): establish whether valid retained point lists can distinguish
// retail's extended working precision from this portable binary64 expression.
func fragmentNormal(quad FragmentQuad) [3]float32 {
	p0, p1, p2 := quad.Vertices[0], quad.Vertices[1], quad.Vertices[2]
	// Each coordinate is converted and stored as binary32 before either vector
	// helper sees it. The conversion scale is the established 1/65535 literal,
	// not the usual 16.16 1/65536 scale [04 R-COB-04 §3].
	first := [3]float32{fragmentVertexFloat(p0[0]), fragmentVertexFloat(p0[1]), fragmentVertexFloat(p0[2])}
	second := [3]float32{fragmentVertexFloat(p1[0]), fragmentVertexFloat(p1[1]), fragmentVertexFloat(p1[2])}
	third := [3]float32{fragmentVertexFloat(p2[0]), fragmentVertexFloat(p2[1]), fragmentVertexFloat(p2[2])}
	// The two vector helpers store P0-P1, then P2-P1. The cross helper receives
	// them in that latter/former order [04 R-COB-04 §3].
	former := [3]float32{
		float32(float64(first[0]) - float64(second[0])),
		float32(float64(first[1]) - float64(second[1])),
		float32(float64(first[2]) - float64(second[2])),
	}
	latter := [3]float32{
		float32(float64(third[0]) - float64(second[0])),
		float32(float64(third[1]) - float64(second[1])),
		float32(float64(third[2]) - float64(second[2])),
	}
	cross := [3]float32{
		float32(float64(latter[1])*float64(former[2]) - float64(latter[2])*float64(former[1])),
		float32(float64(latter[2])*float64(former[0]) - float64(latter[0])*float64(former[2])),
		float32(float64(latter[0])*float64(former[1]) - float64(latter[1])*float64(former[0])),
	}
	length := math.Sqrt(float64(cross[0])*float64(cross[0]) + float64(cross[1])*float64(cross[1]) + float64(cross[2])*float64(cross[2]))
	return [3]float32{
		float32(float64(cross[0]) / length),
		float32(float64(cross[1]) / length),
		float32(float64(cross[2]) / length),
	}
}

const fragmentVertexScale = float32(1.5259021893143654e-05) // exact binary32 1/65535 [04 R-COB-04 §3]

func fragmentVertexFloat(value numeric.Fixed) float32 {
	return float32(float64(fragmentRaw(value)) * float64(fragmentVertexScale))
}

func fragmentRaw(value numeric.Fixed) int32  { return int32(value) }
func fragmentWord(value int32) numeric.Fixed { return numeric.Fixed(value) }
func fragmentAdd(a, b numeric.Fixed) numeric.Fixed {
	return fragmentWord(fragmentAdd32(fragmentRaw(a), fragmentRaw(b)))
}
func fragmentSub(a, b numeric.Fixed) numeric.Fixed {
	return fragmentWord(fragmentSub32(fragmentRaw(a), fragmentRaw(b)))
}
func fragmentAdd32(a, b int32) int32          { return int32(uint32(a) + uint32(b)) }
func fragmentSub32(a, b int32) int32          { return int32(uint32(a) - uint32(b)) }
func fragmentMul32(a, b int32) int32          { return int32(uint32(a) * uint32(b)) }
func fragmentWhole(value numeric.Fixed) int32 { return fragmentRaw(value) >> numeric.FractionBits }
