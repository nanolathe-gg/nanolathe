package render

import (
	"github.com/nanolathe/nanolathe/internal/model"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// WholeDebrisSlots is the fixed whole-piece debris table size [04 R-COB-04 §2].
const WholeDebrisSlots = 100

// WholeDebrisStorageCharge is the arena's retained storage charge [04 R-COB-04 §2].
const WholeDebrisStorageCharge = 100000

const (
	debrisFixedCharge   = 110 // [04 R-COB-04 §2]
	debrisMinimumCharge = debrisFixedCharge
	debrisSplitMinimum  = 9
	debrisStopVelocity  = numeric.Fixed(2 << 16)
	debrisPointCapacity = WholeDebrisStorageCharge/12 + 1
	debrisBlockCapacity = WholeDebrisStorageCharge/debrisSplitMinimum + 1
)

// DebrisRequest is one already-seeded, whole-piece physical explosion. Model
// identity and its primitives stay immutable; Points and all kinematic state
// are copied at admission [04 R-COB-04 §1][04 R-COB-04 §2].
//
// Position is the already absolute piece world offset. The later session
// adapter resolves a COB source, model piece and unit transform before calling
// Admit; this package does not import COB.
type DebrisRequest struct {
	Model        *model.Model
	PieceIndex   int
	GeometryName string
	Points       [][3]numeric.Fixed
	Position     [3]numeric.Fixed
	Angles       [3]uint16
	RenderFlags  uint8

	Velocity     [3]numeric.Fixed
	AngularRates [3]uint16
	Lifetime     uint16
	Fall         bool
	ExplodeOnHit bool
	Smoke        bool
	Fire         bool
}

// DebrisStepContext contains the authoritative terrain and session values the
// fixed effect phase supplies. WaterEffectsWordZero preserves the retail word
// polarity: only zero permits an on-hit water bitmap [04 R-COB-04 §2].
type DebrisStepContext struct {
	TerrainHeight        func(x, z numeric.Fixed) numeric.Fixed
	SeaLevel             numeric.Fixed
	Gravity              numeric.Fixed
	WaterEffectsWordZero bool
	Lava                 bool
}

// GroundDebrisImpact is the synchronous on-hit request for a whole piece that
// stops after a terrain bounce. The consumer owns effect-pool admission [04
// R-COB-04 §2][04 R-COB-04 §4].
type GroundDebrisImpact struct {
	Position             [3]numeric.Fixed
	Graphic              string
	CalculatedFrameTable uint8
	AboveSeaFlash        bool
}

// WaterDebrisImpact is the synchronous on-hit request for a piece at or below
// sea level. It deliberately has no calculated frames or flash [04 R-COB-04 §2].
type WaterDebrisImpact struct {
	Position [3]numeric.Fixed
	Graphic  string
	Lava     bool
}

// DebrisImpactSink receives collision requests during Step, before the debris
// slot is released. It is synchronous and must not retain an unbounded queue
// [04 R-COB-04 §2] [I5].
type DebrisImpactSink interface {
	GroundDebrisImpact(GroundDebrisImpact)
	WaterDebrisImpact(WaterDebrisImpact)
}

// DebrisSnapshot is a detached read of one live piece. Its point slice is
// copied by SnapshotInto, so publication cannot mutate arena-owned state [I6].
type DebrisSnapshot struct {
	Slot         int
	Model        *model.Model
	PieceIndex   int
	GeometryName string
	Points       [][3]numeric.Fixed
	Position     [3]numeric.Fixed
	Angles       [3]uint16
	RenderFlags  uint8
	Velocity     [3]numeric.Fixed
	AngularRates [3]uint16
	Lifetime     uint16
	Fall         bool
	ExplodeOnHit bool
	Smoke        bool
	Fire         bool
}

type debrisSlot struct {
	live       bool
	generation uint64
	pointStart int
	pointCount int

	model        *model.Model
	pieceIndex   int
	geometryName string
	position     [3]numeric.Fixed
	angles       [3]uint16
	renderFlags  uint8
	velocity     [3]numeric.Fixed
	angularRates [3]uint16
	lifetime     uint16
	fall         bool
	explodeOnHit bool
	smoke        bool
	fire         bool
}

// debrisBlock is one preserved partition boundary in the storage arena. It
// uses named algorithm fields, not an executable memory layout [I13]. A
// released debris slot leaves its occupied block resident until the cursor
// later clears that exact partition [04 R-COB-04 §2].
type debrisBlock struct {
	occupied   bool
	start      int
	charge     int
	slot       int
	generation uint64
}

// DebrisPool owns both bounded admission state and effect-phase physics for
// whole-piece debris. It consumes no random stream [04 R-COB-04 §1][I4].
type DebrisPool struct {
	slots  [WholeDebrisSlots]debrisSlot
	blocks [debrisBlockCapacity]debrisBlock
	points [debrisPointCapacity][3]numeric.Fixed
	count  int
	cursor int
	serial uint64
}

// NewDebrisPool creates an empty fixed whole-piece debris arena.
func NewDebrisPool() *DebrisPool { return &DebrisPool{} }

// Admit copies req into the first empty debris slot, then allocates its
// bounded storage block. An oversized request has no representable arena block
// and is refused [04 R-COB-04 §2].
func (p *DebrisPool) Admit(req DebrisRequest) bool {
	if p == nil {
		return false
	}
	slotIndex := p.firstEmptySlot()
	if slotIndex < 0 {
		return false
	}
	if len(req.Points) > (WholeDebrisStorageCharge-debrisMinimumCharge)/12 {
		return false
	}
	charge := len(req.Points)*12 + debrisFixedCharge
	blockIndex, start, ok := p.allocateBlock(charge, slotIndex)
	if !ok {
		return false
	}
	pointStart := start / 12
	if pointStart+len(req.Points) > len(p.points) {
		// The charge bound above makes this unreachable for a contiguous block.
		// Keep the pool bounded if a future storage representation changes.
		p.blocks[blockIndex] = debrisBlock{}
		return false
	}
	p.serial++
	if p.serial == 0 {
		p.serial++
	}
	p.blocks[blockIndex].generation = p.serial
	copy(p.points[pointStart:pointStart+len(req.Points)], req.Points)
	p.slots[slotIndex] = debrisSlot{
		live: true, generation: p.serial,
		pointStart: pointStart, pointCount: len(req.Points),
		model: req.Model, pieceIndex: req.PieceIndex, geometryName: req.GeometryName,
		position: debrisWord3(req.Position), angles: req.Angles, renderFlags: req.RenderFlags,
		velocity: debrisWord3(req.Velocity), angularRates: req.AngularRates, lifetime: req.Lifetime,
		fall: req.Fall, explodeOnHit: req.ExplodeOnHit, smoke: req.Smoke, fire: req.Fire,
	}
	return true
}

// Step applies the established whole-piece lifecycle in ascending slot order.
// Contact requests are emitted synchronously before their slot is freed [04
// R-COB-04 §2].
func (p *DebrisPool) Step(ctx DebrisStepContext, sink DebrisImpactSink) {
	if p == nil {
		return
	}
	for i := range p.slots {
		s := &p.slots[i]
		if !s.live {
			continue
		}
		lifetime := s.lifetime
		s.lifetime--
		if lifetime == 0 {
			p.releaseSlot(i)
			continue
		}

		if int32(s.position[1]) > int32(ctx.SeaLevel) { // strict above-sea predicate [04 R-COB-04 §2]
			height := numeric.Fixed(0)
			if ctx.TerrainHeight != nil {
				height = debrisWord(ctx.TerrainHeight(s.position[0], s.position[2]))
			}
			if debrisAdd(s.velocity[1], s.position[1]) <= height {
				s.velocity[1] = numeric.Fixed(-(int32(s.velocity[1]) >> 1))
				s.velocity[0] = numeric.Fixed(int32(s.velocity[0]) >> 1)
				s.velocity[2] = numeric.Fixed(int32(s.velocity[2]) >> 1)
				if s.velocity[1] < debrisStopVelocity {
					if s.explodeOnHit && sink != nil {
						sink.GroundDebrisImpact(GroundDebrisImpact{
							Position: s.position, Graphic: "explosion", CalculatedFrameTable: 1, AboveSeaFlash: true,
						})
					}
					p.releaseSlot(i)
					continue
				}
			}

			s.position[0] = debrisAdd(s.position[0], s.velocity[0])
			s.position[1] = debrisAdd(s.position[1], s.velocity[1])
			s.position[2] = debrisAdd(s.position[2], s.velocity[2])
			// The stored rate order is permuted at the XYZ angle words [04 R-COB-04 §2].
			s.angles[0] += s.angularRates[1]
			s.angles[1] += s.angularRates[2]
			s.angles[2] += s.angularRates[0]
			if s.fall {
				s.velocity[1] = debrisSub(s.velocity[1], ctx.Gravity)
			}
			continue
		}

		if s.explodeOnHit && ctx.WaterEffectsWordZero && sink != nil {
			graphic := "h2oboom2"
			if ctx.Lava {
				graphic = "lavasplash"
			}
			sink.WaterDebrisImpact(WaterDebrisImpact{Position: s.position, Graphic: graphic, Lava: ctx.Lava})
		}
		p.releaseSlot(i)
	}
}

// SnapshotInto appends live pieces in slot order. Every returned point list is
// detached from the fixed arena so callers may retain it through publication.
func (p *DebrisPool) SnapshotInto(out []DebrisSnapshot) []DebrisSnapshot {
	if p == nil {
		return out[:0]
	}
	out = out[:0]
	for i := range p.slots {
		s := &p.slots[i]
		if !s.live {
			continue
		}
		points := make([][3]numeric.Fixed, s.pointCount)
		copy(points, p.points[s.pointStart:s.pointStart+s.pointCount])
		out = append(out, DebrisSnapshot{
			Slot: i, Model: s.model, PieceIndex: s.pieceIndex, GeometryName: s.geometryName,
			Points: points, Position: s.position, Angles: s.angles, RenderFlags: s.renderFlags,
			Velocity: s.velocity, AngularRates: s.angularRates, Lifetime: s.lifetime,
			Fall: s.fall, ExplodeOnHit: s.explodeOnHit, Smoke: s.smoke, Fire: s.fire,
		})
	}
	return out
}

// SlotCount reports live whole-piece slots.
func (p *DebrisPool) SlotCount() int {
	if p == nil {
		return 0
	}
	count := 0
	for i := range p.slots {
		if p.slots[i].live {
			count++
		}
	}
	return count
}

// StorageUsed reports resident ring charge, including blocks whose debris slot
// has expired but whose storage has not reached the eviction cursor yet.
func (p *DebrisPool) StorageUsed() int {
	if p == nil {
		return 0
	}
	used := 0
	for i := 0; i < p.count; i++ {
		if p.blocks[i].occupied {
			used += p.blocks[i].charge
		}
	}
	return used
}

func (p *DebrisPool) firstEmptySlot() int {
	for i := range p.slots {
		if !p.slots[i].live {
			return i
		}
	}
	return -1
}

func (p *DebrisPool) allocateBlock(charge, slot int) (blockIndex, start int, ok bool) {
	if charge < debrisMinimumCharge || charge > WholeDebrisStorageCharge {
		return 0, 0, false
	}
	p.ensurePartition()
	if p.blocks[p.cursor].start+charge > WholeDebrisStorageCharge {
		// Clearing the tail preserves every free/occupied boundary. A later
		// allocation may absorb a sub-nine-charge free partition [04 R-COB-04 §2].
		for i := p.cursor; i < p.count; i++ {
			p.clearBlock(i)
		}
		p.cursor = 0
	}

	first := p.cursor
	covered := 0
	last := first
	for covered < charge {
		if last >= p.count {
			return 0, 0, false
		}
		p.clearBlock(last)
		covered += p.blocks[last].charge
		last++
	}
	retained := charge
	if covered-charge < debrisSplitMinimum {
		retained = covered
	}
	blockIndex, ok = p.replaceRun(first, last, retained, slot)
	if !ok {
		return 0, 0, false
	}
	start = p.blocks[blockIndex].start
	return blockIndex, start, true
}

func (p *DebrisPool) releaseSlot(index int) {
	if index >= 0 && index < len(p.slots) {
		p.slots[index].live = false
	}
}

func (p *DebrisPool) evictBlock(index int) {
	if index < 0 || index >= p.count {
		return
	}
	p.clearBlock(index)
}

func (p *DebrisPool) clearBlock(index int) {
	if index < 0 || index >= p.count || !p.blocks[index].occupied {
		return
	}
	b := p.blocks[index]
	if b.slot >= 0 && b.slot < len(p.slots) {
		s := &p.slots[b.slot]
		if s.live && s.generation == b.generation {
			p.releaseSlot(b.slot)
		}
	}
	p.blocks[index].occupied = false
	p.blocks[index].slot = 0
	p.blocks[index].generation = 0
}

func (p *DebrisPool) ensurePartition() {
	if p.count != 0 {
		return
	}
	p.blocks[0] = debrisBlock{start: 0, charge: WholeDebrisStorageCharge}
	p.count = 1
	p.cursor = 0
}

func (p *DebrisPool) replaceRun(first, last, retained, slot int) (int, bool) {
	if first < 0 || last <= first || last > p.count || retained <= 0 {
		return 0, false
	}
	covered := 0
	for i := first; i < last; i++ {
		covered += p.blocks[i].charge
	}
	freeCharge := covered - retained
	replacementCount := 1
	if freeCharge >= debrisSplitMinimum {
		replacementCount++
	}
	delta := replacementCount - (last - first)
	if p.count+delta > len(p.blocks) {
		return 0, false
	}
	if delta != 0 {
		copy(p.blocks[last+delta:p.count+delta], p.blocks[last:p.count])
		p.count += delta
	}
	start := p.blocks[first].start
	p.blocks[first] = debrisBlock{occupied: true, start: start, charge: retained, slot: slot}
	if replacementCount == 2 {
		p.blocks[first+1] = debrisBlock{start: start + retained, charge: freeCharge}
	}
	p.cursor = first + 1
	if p.cursor == p.count {
		p.cursor = 0
	}
	return first, true
}

func debrisWord(v numeric.Fixed) numeric.Fixed { return numeric.Fixed(int32(v)) }

func debrisWord3(v [3]numeric.Fixed) [3]numeric.Fixed {
	return [3]numeric.Fixed{debrisWord(v[0]), debrisWord(v[1]), debrisWord(v[2])}
}

func debrisAdd(a, b numeric.Fixed) numeric.Fixed {
	return numeric.Fixed(int32(uint32(int32(a)) + uint32(int32(b))))
}

func debrisSub(a, b numeric.Fixed) numeric.Fixed {
	return numeric.Fixed(int32(uint32(int32(a)) - uint32(int32(b))))
}
