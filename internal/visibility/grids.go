// Package visibility implements blocky 32-pixel-authoritative LOS [03 §3] [PLAN_05].
// This file implements C1, C4, C7: grid allocation, bit set, refcount inc/dec, rebuild fills.
package visibility

import (
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/world"
)

// Mode is the mode-word bits [03 §3.1] [PLAN_05 C2].
// Bit 2 selects sprite-mask (0) vs terrain-ray (1); other bits control history/current enable.
type Mode uint32

const (
	ModeHistoryEnabled Mode = 1 << 0 // when clear, word grid init fills all-bits-set [03 §3.2] C7
	ModeCurrentEnabled Mode = 1 << 1 // when clear, byte grids fill with 1 [03 §3.2] C7
	ModeTerrainRay     Mode = 1 << 2 // selects terrain-ray vs sprite-mask [03 §3.2] C2
	ModeFogCacheValid  Mode = 1 << 8 // presentation dirty bit; Service owns waking composer [03 §3.3]
)

// PlayerID is a player slot 0..9 [04 §2] [03 §3.1] ten usable bits.
type PlayerID uint8

// Service is the visibility service [PLAN_05 Public API].
// wordMask is []uint16 length W*H, ten usable player bits per cell, one per 32 pixels [03 §3.1] C1.
// byteGrids is per-player refcount [03 §3.1] C1.
type Service struct {
	terrain   *world.Terrain
	mode      Mode
	W, H      int32
	wordMask  []uint16
	byteGrids [10][]uint8
	fog       FogCache
	local     PlayerID

	// Authored raster inputs [03 §3.2]. Both nil means neither raster can
	// publish, which is the correct answer for a fixture with no content.
	shapes     *content.SightShapes
	rayTables  *content.LOSTables
	spokeCache [][][]step // per LOS table, built on first use

	// footprints holds each observer's last published raster so the refresh
	// throttle can compare against it [03 §3.2] C6.
	footprints map[ObserverID]footprint

	// surfaces receives the sensor phase's circles. They are separate from the
	// word mask and wiped each tick [03 §3.4] C11.
	surfaces SensorSurfaces
}

// ObserverID identifies one sight source across ticks — the owning unit's pool
// slot. The refresh throttle needs stable identity, not the footprint values,
// because it is precisely the values it compares [03 §3.2] C6.
type ObserverID uint32

// footprint is a stored raster: what was published, and where [03 §3.2] C6.
type footprint struct {
	owner      PlayerID
	cx, cz     int32
	heightByte uint8
	radius     int32
	quantized  int32 // shape index (sprite) or table index (ray)
	live       bool
}

// New creates a Service for terrain t and mode [03 §3.1] C1.
// Allocation is (cellW/2)*(cellH/2) bytes*2 = cellW*cellH/2 bytes as []uint16 [03 §3.1].
func New(t *world.Terrain, mode Mode) *Service {
	s := &Service{terrain: t, mode: mode, local: 0}
	if t != nil {
		s.W = t.CellW / 2
		s.H = t.CellH / 2
		if s.W < 0 {
			s.W = 0
		}
		if s.H < 0 {
			s.H = 0
		}
		n := int(s.W) * int(s.H)
		if n > 0 {
			s.wordMask = make([]uint16, n)
			for i := range s.byteGrids {
				s.byteGrids[i] = make([]uint8, n)
			}
			s.fog = FogCache{
				w:     s.W,
				h:     s.H,
				ch0:   make([]uint8, n),
				ch1:   make([]uint8, n),
				valid: false,
			}
		}
		// C7 initial fill via RebuildAll with no units, respecting mode bits.
		s.rebuildFills()
	}
	return s
}

// rebuildFills fills grids per C7 before publishing [03 §3.2].
// With history disabled word fills all-bits-set, otherwise zero.
// With current coverage disabled every eligible player's byte grid fills with 1, otherwise zero.
func (s *Service) rebuildFills() {
	if s.wordMask == nil {
		return
	}
	if s.mode&ModeHistoryEnabled == 0 {
		for i := range s.wordMask {
			s.wordMask[i] = 0x03FF // ten usable bits set [03 §3.1] C1
		}
	} else {
		for i := range s.wordMask {
			s.wordMask[i] = 0
		}
	}
	fillByte := uint8(0)
	if s.mode&ModeCurrentEnabled == 0 {
		fillByte = 1
	}
	for p := range s.byteGrids {
		if s.byteGrids[p] == nil {
			continue
		}
		for i := range s.byteGrids[p] {
			s.byteGrids[p][i] = fillByte
		}
	}
}

// WordMask returns the word grid for diagnostics; callers must not mutate.
func (s *Service) WordMask() []uint16 { return s.wordMask }

// ByteGrid returns the per-player byte grid for diagnostics.
func (s *Service) ByteGrid(p PlayerID) []uint8 {
	if int(p) >= len(s.byteGrids) {
		return nil
	}
	return s.byteGrids[p]
}

// SetLocal sets the local player for fog dirty semantics [03 §3.2] C15.
func (s *Service) SetLocal(p PlayerID) { s.local = p }

// Mode returns the current mode word.
func (s *Service) Mode() Mode { return s.mode }

// SetMode updates the mode word; callers use it for history/current toggles.
func (s *Service) SetMode(m Mode) { s.mode = m }

// cellBit returns the bit for a player [03 §3.1] C1 ten usable bits.
func cellBit(p PlayerID) uint16 { return 1 << (p % 10) }

// setWordBit sets the owner's bit if absent; idempotent, never decrements [03 §3.2] C4.
// Returns true if the cell changed.
func (s *Service) setWordBit(idx int, owner PlayerID) bool {
	if idx < 0 || idx >= len(s.wordMask) {
		return false
	}
	bit := cellBit(owner)
	if s.wordMask[idx]&bit != 0 {
		return false
	}
	s.wordMask[idx] |= bit
	return true
}

// incByteGrid increments the owner's byte refcount at idx [03 §3.1] C1.
func (s *Service) incByteGrid(idx int, owner PlayerID) bool {
	if int(owner) >= len(s.byteGrids) || s.byteGrids[owner] == nil {
		return false
	}
	if idx < 0 || idx >= len(s.byteGrids[owner]) {
		return false
	}
	// Refcount saturates at 255; increment regardless of prior nonzero so overlapping units preserve visibility [03 §3.1].
	if s.byteGrids[owner][idx] == 255 {
		return false
	}
	s.byteGrids[owner][idx]++
	return true
}

// decByteGrid decrements the owner's byte refcount at idx.
func (s *Service) decByteGrid(idx int, owner PlayerID) bool {
	if int(owner) >= len(s.byteGrids) || s.byteGrids[owner] == nil {
		return false
	}
	if idx < 0 || idx >= len(s.byteGrids[owner]) {
		return false
	}
	if s.byteGrids[owner][idx] == 0 {
		return false
	}
	s.byteGrids[owner][idx]--
	return true
}

// RebuildAll rebuilds both stores from scratch per C7 and republishes supplied observers [03 §3.2] [PLAN_05].
// With history disabled word fills all-bits-set else zero; with current disabled byte grids fill 1 else zero.
// Then all active footprints republish.
func (s *Service) RebuildAll(observers []Observer) {
	s.rebuildFills()
	for _, ob := range observers {
		s.Publish(ob.Owner, ob.CX, ob.CZ, ob.HeightByte, ob.Radius)
	}
	// Any rebuild dirty-invalidates the fog presentation cache; it rebuilds lazily when valid bit clears [03 §3.3] C13.
	s.fog.valid = false
}

// Observer is the minimal footprint source for RebuildAll [PLAN_05].
type Observer struct {
	Owner      PlayerID
	CX, CZ     int32 // coverage tile X/Y (already quantized to 32-pixel tiles)
	HeightByte uint8
	Radius     int32
}

// GridDimensions returns the visibility grid dimensions (W,H) as per C1.
func (s *Service) GridDimensions() (int32, int32) { return s.W, s.H }
