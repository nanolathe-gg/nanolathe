// This file implements C1, C4, C7: grid allocation, bit set, refcount inc/dec, rebuild fills.

package visibility

import (
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/world"
)

// Mode is the four low mode-word bits [03 §3.1] [PLAN_05 C2].
// Bit 3 is solely the presentation fog-cache-valid bit; it is not a terrain
// height-word dirty flag.
type Mode uint32

const (
	ModeHistoryEnabled Mode = 1 << 0 // 0x1 history — when clear, word grid init fills all-bits-set [03 §3.2] C7 [P0-18]
	ModeCurrentEnabled Mode = 1 << 1 // 0x2 byte-vs-word — selects predicate source; when clear, byte grids fill with 1 [03 §3.2] C7 [P0-18]
	ModeTerrainRay     Mode = 1 << 2 // 0x4 ray-vs-sprite — selects raster shape [03 §3.2] C2 [P0-18]
	ModeFogCacheValid  Mode = 1 << 3 // 0x8 fog/minimap cache valid [03 §3.1]
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

	// sensorInputs is the sensor phase's per-unit status snapshot. The phase has
	// no presentation surface of its own: the minimap's circles are drawn by
	// the contacts pass [03 §3.10] correction of 2026-08-29.
	sensorInputs []SensorInput

	// viewerDefeated mirrors the viewing player's defeated/observer rule flag.
	// It is the friendly pass's third disjunct: a defeated viewer marks every
	// live unit friendly [R-VIS-01 §4] pass 1.
	viewerDefeated bool
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
	s := &Service{terrain: t, mode: mode & 0x0f, local: 0}
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
				w:   s.W,
				h:   s.H,
				ch0: make([]uint8, n),
				ch1: make([]uint8, n),
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
	if s == nil || !validPlayer(p) {
		return nil
	}
	return s.byteGrids[p]
}

// SetLocal sets the local player for fog dirty semantics [03 §3.2] C15.
// Invalid slots are ignored; they must not alias slot zero.
func (s *Service) SetLocal(p PlayerID) {
	if s != nil && validPlayer(p) {
		s.local = p
	}
}

// Mode returns the current mode word.
func (s *Service) Mode() Mode {
	if s == nil {
		return 0
	}
	return s.mode
}

// SetMode updates the mode word; callers use it for history/current toggles.
func (s *Service) SetMode(m Mode) {
	if s == nil {
		return
	}
	s.mode = m & 0x0f
	s.mode &^= ModeFogCacheValid
}

// The mode predicates keep bit tests explicit at call sites and prevent
// higher bits from becoming accidental second meanings [03 §3.1].
func (m Mode) HistoryEnabled() bool { return m&ModeHistoryEnabled != 0 }
func (m Mode) CurrentEnabled() bool { return m&ModeCurrentEnabled != 0 }
func (m Mode) TerrainRay() bool     { return m&ModeTerrainRay != 0 }
func (m Mode) FogCacheValid() bool  { return m&ModeFogCacheValid != 0 }

// The service predicates report the same bits of the service's own mode
// word, nil-safe for callers holding no service.
func (s *Service) HistoryEnabled() bool { return s != nil && s.mode.HistoryEnabled() }
func (s *Service) CurrentEnabled() bool { return s != nil && s.mode.CurrentEnabled() }
func (s *Service) TerrainRay() bool     { return s != nil && s.mode.TerrainRay() }
func (s *Service) FogCacheValid() bool  { return s != nil && s.mode.FogCacheValid() }

// cellBit returns the bit for a player [03 §3.1] C1 ten usable bits.
func cellBit(p PlayerID) uint16 {
	if !validPlayer(p) {
		return 0
	}
	return 1 << p
}

func validPlayer(p PlayerID) bool { return p < 10 }

// setWordBit sets the owner's bit if absent; idempotent, never decrements [03 §3.2] C4.
// Returns true if the cell changed.
func (s *Service) setWordBit(idx int, owner PlayerID) bool {
	if idx < 0 || idx >= len(s.wordMask) || !validPlayer(owner) {
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
	// Raw u8 wrap, no saturation — the increment has no clamp in retail
	// [03 §3.1]; the promoted raster notes confirm the wrap.
	s.byteGrids[owner][idx]++
	return true
}

// decByteGrid decrements the owner's byte refcount at idx P0-11.
// Retail does plain DEC with u8 wrap 256 (0→255) [03 §3.1] P0-11.
func (s *Service) decByteGrid(idx int, owner PlayerID) bool {
	if int(owner) >= len(s.byteGrids) || s.byteGrids[owner] == nil {
		return false
	}
	if idx < 0 || idx >= len(s.byteGrids[owner]) {
		return false
	}
	// Plain u8 wrap, no saturation [03 §3.1] P0-11; DEC at 0 wraps to 255.
	s.byteGrids[owner][idx]--
	return true
}

// RebuildAll rebuilds both stores from scratch per C7 and republishes supplied observers [03 §3.2] [PLAN_05].
// With history disabled word fills all-bits-set else zero; with current disabled byte grids fill 1 else zero.
// Then all active footprints republish.
func (s *Service) RebuildAll(observers []Observer) {
	if s == nil {
		return
	}
	// The session load path invokes this before reading serialized mapping and
	// publishes reconstructed units synchronously, so no empty-coverage frame
	// can escape to presentation [03 §3.3] C16.
	s.rebuildFills()
	// A rebuild replaces the stores, so no old record may throttle a
	// republication or later retirement.
	s.footprints = make(map[ObserverID]footprint, len(observers))
	for _, ob := range observers {
		s.Publish(ob.Owner, ob.CX, ob.CZ, ob.HeightByte, ob.Radius)
	}
	// Any rebuild dirty-invalidates the fog presentation cache; it rebuilds lazily when valid bit clears [03 §3.3] C13.
	s.mode &^= ModeFogCacheValid
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
