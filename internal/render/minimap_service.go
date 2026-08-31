package render

import "github.com/nanolathe/nanolathe/internal/camera"

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
	GUIRemap  []byte
}

// NewMinimapService creates a surface lifecycle. A nil picture remains
// representable for focused renderer fixtures and non-retail optional
// bindings; the retail battle HUD rejects that state at initialization so a
// missing source cannot become a silent blank rail [03 §3.7].
func NewMinimapService(cfg MinimapServiceConfig) *MinimapService {
	s := &MinimapService{mapW: cfg.MapW, mapH: cfg.MapH, local: cfg.LocalSlot, dcb: cfg.FogFill, dirty: MinimapDirtyMapped | MinimapDirtyFinal}
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

func (s *MinimapService) Picture() *RadarSurface { return cloneRadarSurface(s.picture) }
func (s *MinimapService) Final() *RadarSurface   { return cloneRadarSurface(s.final) }
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

// RebuildMapped consumes LOS stores only when mapped is dirty. It does not
// mutate the supplied stores and marks FINAL dirty after the copy. [03 §3.8]
func (s *MinimapService) RebuildMapped(word []uint16, current []uint8) bool {
	if s == nil || s.picture == nil || s.dirty&MinimapDirtyMapped == 0 {
		return false
	}
	s.mapped = BuildMapped(s.picture, word, current, s.mapW, s.mapH, s.local, s.dcb, s.remap)
	s.dirty &^= MinimapDirtyMapped
	s.dirty |= MinimapDirtyFinal
	return s.mapped != nil
}

// RebuildFinal resets FINAL from MAPPED and applies contacts. It is called on
// every presentation tick, including ticks without a mapped rebuild. [03 §3.6]
//
// radarColor and jammerColor are the two circle indices of [03 §3.10] — the
// outer radar/sonar circle and both jam circles respectively — resolved by the
// caller from the active palette.
//
// TODO(question): [03 §3.10] records the numeric identity of those two palette
// indices as still open ("What remains open is the numeric identity of the two
// palette indices"). They are parameters here precisely so no index is invented
// at this layer; the caller's current choice is a placeholder until a trace
// names them.
func (s *MinimapService) RebuildFinal(m camera.Minimap, playW, playH int32, contacts []MinimapContact, blit MinimapContactBlitter, radarColor, jammerColor, ringColor byte) bool {
	if s == nil || s.mapped == nil {
		return false
	}
	// The contacts pass is the sole circle producer: every circle on FINAL comes
	// from a contact record's own authored distances, drawn in the contact walk
	// [03 §3.10] correction of 2026-08-29 ("presentation drawn by the contacts
	// pass"). There is no second circle list to reconcile against.
	s.final = rebuildFinalExact(s.mapped, m, playW, playH, contacts, BlinkState{Phase: s.blinkPhase}, blit, radarColor, jammerColor, ringColor)
	s.dirty &^= MinimapDirtyFinal
	return s.final != nil
}
