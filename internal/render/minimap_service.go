package render

import (
	"sync/atomic"

	"github.com/nanolathe/nanolathe/internal/camera"
)

var minimapSurfaceIdentity atomic.Uint64

// Minimap dirty bits track presentation surface invalidation. The committed
// blink phase is a separate scalar and is never folded into this word [03
// §3.6][R-CORE-03].
const (
	MinimapDirtyFinal  uint8 = 1 << 1
	MinimapDirtyMapped uint8 = 1 << 2
)

// MinimapService owns the persistent battle radar surfaces. Picture is
// immutable after map load, mapped is rebuilt only when dirty, and final is
// rebuilt each tick. Radar temp is local scratch in picture construction, not
// a service-owned surface [03 §3.6].
type MinimapService struct {
	picture, mapped, final *RadarSurface
	dirty                  uint8
	blinkPhase             uint8
	mapW, mapH             int
	local                  uint8
	dcb                    byte
	remap                  []byte
	mappedSource           uint64
	mappedVersion          uint64
	finalInputVersion      uint64
	finalRevision          uint64
	finalIdentity          uint64
}

// MinimapServiceConfig supplies the map-sized picture and MAPPED inputs.
// The byte slices are copied at construction so the service remains a
// presentation-only consumer of immutable snapshot data.
type MinimapServiceConfig struct {
	Picture   *RadarSurface
	MapW      int
	MapH      int
	LocalSlot uint8
	FogFill   byte
	// GUIRemap is the 256-entry index translation MAPPED applies to explored
	// but currently unseen cells. Despite the field name, the table retail
	// reads here is the palette-install gray table — the same grayscale-nearest
	// LUT the main-view fog overlay uses, which desaturates fogged terrain
	// while preserving its texture — and not the GUI colour-field lookup
	// [03 §3.8][03 §3.3].
	GUIRemap []byte
}

// NewMinimapService creates a surface lifecycle. A nil picture remains
// representable for focused renderer fixtures and non-retail optional
// bindings; the retail battle HUD rejects that state at initialization so a
// missing source cannot become a silent blank rail [03 §3.7].
func NewMinimapService(cfg MinimapServiceConfig) *MinimapService {
	s := &MinimapService{mapW: cfg.MapW, mapH: cfg.MapH, local: cfg.LocalSlot, dcb: cfg.FogFill, dirty: MinimapDirtyMapped | MinimapDirtyFinal, finalIdentity: minimapSurfaceIdentity.Add(1)}
	if cfg.Picture != nil {
		s.picture = cloneRadarSurface(cfg.Picture)
	}
	if len(cfg.GUIRemap) == 256 {
		s.remap = append([]byte(nil), cfg.GUIRemap...)
	}
	return s
}

func cloneRadarSurface(src *RadarSurface) *RadarSurface {
	if src == nil {
		return nil
	}
	return &RadarSurface{W: src.W, H: src.H, Pitch: src.Pitch, Bits: append([]byte(nil), src.Bits...)}
}

// Picture returns a copy of the source radar picture [03 §3.7].
func (s *MinimapService) Picture() *RadarSurface { return cloneRadarSurface(s.picture) }

// Final returns a copy of the composed radar surface the HUD blits [03 §3.8].
// The copy is durable: a caller may retain it across rebuilds.
func (s *MinimapService) Final() *RadarSurface { return cloneRadarSurface(s.final) }

// FinalInto copies FINAL into a caller-owned surface and returns it. The HUD
// draws the projectile and feature pass over its own copy every frame, so it
// keeps one surface for the life of the battle instead of cloning FINAL per
// frame; the bytes are the same ones Final returns, and the service's own FINAL
// is not exposed. A nil FINAL leaves dst untouched and returns nil, matching
// Final [03 §3.8].
func (s *MinimapService) FinalInto(dst *RadarSurface) *RadarSurface {
	if s == nil || s.final == nil {
		return nil
	}
	if dst == nil {
		return cloneRadarSurface(s.final)
	}
	dst.W, dst.H, dst.Pitch = s.final.W, s.final.H, s.final.Pitch
	dst.Bits = resizeRadarBits(dst.Bits, len(s.final.Bits))
	copy(dst.Bits, s.final.Bits)
	return dst
}

// FinalIdentity and FinalRevision identify the current FINAL bytes for a
// renderer cache. They do not expose mutable surface storage.
func (s *MinimapService) FinalIdentity() uint64 {
	if s == nil {
		return 0
	}
	return s.finalIdentity
}

func (s *MinimapService) FinalRevision() uint64 {
	if s == nil {
		return 0
	}
	return s.finalRevision
}

// Blink is the current radar blink phase [01 R-CORE-03].
func (s *MinimapService) Blink() BlinkState {
	if s == nil {
		return BlinkState{}
	}
	return BlinkState{Phase: s.blinkPhase}
}

// SetBlinkPhase consumes the committed phase bit. Repeated presentation of
// one committed frame is therefore idempotent; only a phase change requires a
// FINAL rebuild [R-CORE-03][03 §3.6].
func (s *MinimapService) SetBlinkPhase(phase uint8) {
	if s == nil {
		return
	}
	phase &= 1
	if s.blinkPhase == phase {
		return
	}
	s.blinkPhase = phase
	s.dirty |= MinimapDirtyFinal
}

// RebuildMapped composites the picture against the supplied LOS stores. It
// does not mutate them and marks FINAL dirty after the composite. [03 §3.8]
//
// Retail gates the composite on the MAPPED dirty bit, raised by a local
// observer's changed LOS raster [03 §3.6 "Mapped-surface invalidation"]
// [R-VIS-01 §2]. Publication carries that event as MappingVersion. The
// versioned API therefore reuses MAPPED for a retained immutable input while
// the zero-version compatibility API remains dynamic.
func (s *MinimapService) RebuildMapped(word []uint16, current []uint8) bool {
	return s.RebuildMappedVersion(word, current, 0, 0)
}

// RebuildMappedVersion consumes a source identity and its committed revision.
// A zero source retains the dynamic API; a known source can retain revision
// zero until its first visibility mutation [03 §3.6].
func (s *MinimapService) RebuildMappedVersion(word []uint16, current []uint8, source, version uint64) bool {
	if s == nil || s.picture == nil {
		return false
	}
	if source != 0 && s.mapped != nil && s.mappedSource == source && s.mappedVersion == version {
		return true
	}
	// MAPPED is recomposed over its own retained storage. Nothing outside the
	// service holds it — FINAL copies it and the accessors clone — so reusing
	// the buffer cannot be observed.
	s.mapped = buildMappedInto(s.mapped, s.picture, word, current, s.mapW, s.mapH, s.local, s.dcb, s.remap)
	s.dirty &^= MinimapDirtyMapped
	s.dirty |= MinimapDirtyFinal
	s.mappedSource, s.mappedVersion = source, version
	return s.mapped != nil
}

// RebuildFinal resets FINAL from MAPPED and applies contacts. It is called on
// every presentation tick, including ticks without a mapped rebuild. [03 §3.6]
//
// radarColor and jammerColor are the two circle indices of [03 §3.10] — the
// outer radar/sonar circle and both jam circles respectively — resolved by the
// caller from the active palette, and ringColor is the weapon/interceptor ring.
//
// Their numeric identity was an open question in [03 §3.10] and is now closed
// by [03 R-MM-01 §2]: logical entry 10 for the radar and sonar outer circles,
// entry 12 for both jam circles, entry 15 for the rings. They stay parameters
// so the palette lookup remains the caller's, which is where the
// logical→physical map lives.
func (s *MinimapService) RebuildFinal(m camera.Minimap, playW, playH int32, contacts []MinimapContact, blit MinimapContactBlitter, radarColor, jammerColor, ringColor byte) bool {
	return s.RebuildFinalVersion(m, playW, playH, contacts, blit, radarColor, jammerColor, ringColor, 0)
}

// RebuildFinalVersion avoids rebuilding the same committed FINAL input during
// repeated presentation of one frame. A nonzero version must include contact
// and blink inputs; zero preserves the legacy every-call behaviour [03 §3.6].
func (s *MinimapService) RebuildFinalVersion(m camera.Minimap, playW, playH int32, contacts []MinimapContact, blit MinimapContactBlitter, radarColor, jammerColor, ringColor byte, version uint64) bool {
	if s == nil || s.mapped == nil {
		return false
	}
	if version != 0 && s.final != nil && s.finalInputVersion == version && s.dirty&MinimapDirtyFinal == 0 {
		return true
	}
	// The contacts pass is the sole circle producer: every circle on FINAL comes
	// from a contact record's own authored distances, drawn in the contact walk
	// [03 §3.10] correction of 2026-08-29 ("presentation drawn by the contacts
	// pass"). There is no second circle list to reconcile against.
	// FINAL is recomposed over its own retained storage: the wipe from MAPPED
	// that opens the rebuild overwrites every byte, and callers only ever see a
	// copy (Final, FinalInto), so no stale pixel and no aliased surface escapes.
	s.final = rebuildFinalExactInto(s.final, s.mapped, m, playW, playH, contacts, BlinkState{Phase: s.blinkPhase}, blit, radarColor, jammerColor, ringColor)
	s.dirty &^= MinimapDirtyFinal
	s.finalInputVersion = version
	s.finalRevision++
	return s.final != nil
}
