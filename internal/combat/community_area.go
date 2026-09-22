package combat

import (
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

const communityAreaOverflowSlots = 6

type communityAreaCell struct {
	stamp      uint32
	head, tail int32 // node index plus one; zero is empty
	count      int32
}

type communityAreaNode struct {
	unit pool.Handle
	next int32 // node index plus one; zero is end
}

// ResetCommunityAreaState drops the transient CP-DMG-1 index and explosion
// generations. Session composition calls it when it binds a fresh movement
// filing owner; Rules.AreaIndexTick performs the first eager rebuild.
func (s *Service) ResetCommunityAreaState() {
	if s == nil {
		return
	}
	s.InvalidateCommunityAreaIndex()
	s.communityAreaHitGen = nil
	s.communityAreaStamp = 0
	s.communityAreaGenCounter = 0
	s.communityAreaCurrentGen = 0
	s.communityAreaSaturations = 0
}

// InvalidateCommunityAreaIndex drops only the CP-DMG-1 footprint snapshot.
// A rule switch uses it when the overflow feature changes so re-enabling cannot
// expose handles filed before Strict stopped the tail refresh. The approved
// end-of-tick callback builds the next snapshot; explosion generations,
// saturation diagnostics and unrelated combat bindings keep their lifetime.
func (s *Service) InvalidateCommunityAreaIndex() {
	if s == nil {
		return
	}
	s.communityAreaCells = nil
	s.communityAreaNodes = nil
	s.communityAreaUnits = nil
	s.communityAreaWidth = 0
	s.communityAreaHeight = 0
	s.communityAreaLimit = 0
	s.communityAreaBuiltTick = 0
	s.communityAreaBuilt = false
}

// CommunityAreaSaturations reports the cumulative number of failed
// (cell,unit) insertions. One failure is counted for each full cell reached by
// one eligible unit on one per-tick rebuild [CP-DMG-1].
func (s *Service) CommunityAreaSaturations() uint64 {
	if s == nil {
		return 0
	}
	return s.communityAreaSaturations
}

func (s *Service) beginCommunityArea() uint32 {
	if s == nil || !s.Community.AreaDamageOverflow {
		return 0
	}
	saved := s.communityAreaCurrentGen
	s.communityAreaGenCounter++
	if s.communityAreaGenCounter == 0 {
		s.communityAreaGenCounter++
	}
	s.communityAreaCurrentGen = s.communityAreaGenCounter
	return saved
}

func (s *Service) endCommunityArea(saved uint32) {
	if s != nil && s.Community.AreaDamageOverflow {
		s.communityAreaCurrentGen = saved
	}
}

func (s *Service) ensureCommunityAreaHitGen(h pool.Handle) {
	need := int(h) + 1
	if need <= len(s.communityAreaHitGen) {
		return
	}
	capacity := len(s.communityAreaHitGen) * 2
	if capacity < need {
		capacity = need
	}
	grown := make([]uint32, capacity)
	copy(grown, s.communityAreaHitGen)
	s.communityAreaHitGen = grown
}

func (s *Service) communityAreaAdmit(h pool.Handle, overflow bool) bool {
	if h == 0 {
		return false
	}
	// Generation zero is the source-defined fail-open state when a provider is
	// reached without its explosion bracket.
	if s == nil || s.communityAreaCurrentGen == 0 {
		return true
	}
	s.ensureCommunityAreaHitGen(h)
	if s.communityAreaHitGen[h] >= s.communityAreaCurrentGen {
		return !overflow && !s.Community.AreaDamageDedupCap
	}
	s.communityAreaHitGen[h] = s.communityAreaCurrentGen
	return true
}

func communityAreaVictims(q AreaVictimQuery, visit func(pool.Handle)) {
	if visit == nil {
		return
	}
	s := q.Service
	if s == nil || !s.Community.AreaDamageOverflow {
		StrictRules{}.AreaVictims(q, visit)
		return
	}
	if q.Terrain == nil {
		return
	}
	// The index is prepared eagerly by Rules.AreaIndexTick at the session-owned
	// authoritative boundary. A cell visit never snapshots mutable unit state.
	cell := q.Terrain.PlotAt(q.CellX, q.CellZ)
	if cell == nil {
		return
	}
	// Each visit completes synchronously before the next selector is read.
	if word := cell.OccupantA(); word > 0 {
		h := pool.Handle(word)
		if s.communityAreaAdmit(h, false) {
			visit(h)
		}
	}
	if word := cell.OccupantB(); word > 0 {
		h := pool.Handle(word)
		if s.communityAreaAdmit(h, false) {
			visit(h)
		}
	}
	idx := q.CellZ*s.communityAreaWidth + q.CellX
	if idx < 0 || int(idx) >= len(s.communityAreaCells) {
		return
	}
	c := &s.communityAreaCells[idx]
	if c.stamp != s.communityAreaStamp {
		return
	}
	for link := c.head; link != 0; {
		n := &s.communityAreaNodes[link-1]
		next := n.next
		if s.communityAreaAdmit(n.unit, true) {
			visit(n.unit)
		}
		link = next
	}
}

func (s *Service) ensureCommunityAreaIndex(w *units.World, terrain *world.Terrain, tick uint32, overflowLimit int32) {
	if s == nil || w == nil || terrain == nil || terrain.CellW <= 0 || terrain.CellH <= 0 {
		return
	}
	if s.communityAreaBuilt && s.communityAreaBuiltTick == tick && s.communityAreaWidth == terrain.CellW && s.communityAreaHeight == terrain.CellH && s.communityAreaLimit == overflowLimit {
		return
	}
	cellCount := int(terrain.CellW * terrain.CellH)
	if len(s.communityAreaCells) != cellCount {
		s.communityAreaCells = make([]communityAreaCell, cellCount)
	}
	if slots := w.Capacity() + 1; len(s.communityAreaHitGen) < slots {
		grown := make([]uint32, slots)
		copy(grown, s.communityAreaHitGen)
		s.communityAreaHitGen = grown
	}
	s.communityAreaWidth = terrain.CellW
	s.communityAreaHeight = terrain.CellH
	s.communityAreaLimit = overflowLimit
	s.communityAreaNodes = s.communityAreaNodes[:0]
	s.communityAreaStamp++
	if s.communityAreaStamp == 0 {
		s.communityAreaStamp++
	}

	s.communityAreaUnits = w.AppendLive(s.communityAreaUnits[:0])
	for _, u := range s.communityAreaUnits {
		if u == nil || u.Def == nil || !u.Alive || u.Dying || u.Move.ModeMirror != 2 || u.Attachment.Carrier != 0 {
			continue
		}
		if s.IsOffMapFiled != nil && s.IsOffMapFiled(u.Handle) {
			continue
		}
		footW, footH := int32(u.FootprintSizeX), int32(u.FootprintSizeZ)
		if footW <= 0 || footH <= 0 || u.Handle == 0 {
			continue
		}
		x0, z0 := int32(u.CachedOccupancyX), int32(u.CachedOccupancyZ)
		x1, z1 := x0+footW, z0+footH
		if x0 < 0 {
			x0 = 0
		}
		if z0 < 0 {
			z0 = 0
		}
		if x1 > terrain.CellW {
			x1 = terrain.CellW
		}
		if z1 > terrain.CellH {
			z1 = terrain.CellH
		}
		for z := z0; z < z1; z++ {
			for x := x0; x < x1; x++ {
				s.communityAreaInsert(int(z*terrain.CellW+x), u.Handle, overflowLimit)
			}
		}
	}
	s.communityAreaBuiltTick = tick
	s.communityAreaBuilt = true
}

func (s *Service) communityAreaInsert(cellIndex int, h pool.Handle, limit int32) {
	c := &s.communityAreaCells[cellIndex]
	if c.stamp != s.communityAreaStamp {
		c.stamp = s.communityAreaStamp
		c.head, c.tail, c.count = 0, 0, 0
	}
	for link := c.head; link != 0; link = s.communityAreaNodes[link-1].next {
		if s.communityAreaNodes[link-1].unit == h {
			return
		}
	}
	if limit > 0 && c.count >= limit {
		s.communityAreaSaturations++
		return
	}
	s.communityAreaNodes = append(s.communityAreaNodes, communityAreaNode{unit: h})
	link := int32(len(s.communityAreaNodes))
	if c.tail == 0 {
		c.head = link
	} else {
		s.communityAreaNodes[c.tail-1].next = link
	}
	c.tail = link
	c.count++
}
