package render

import "github.com/nanolathe/nanolathe/internal/camera"

// Minimap dirty bits match the radar dirty word: bit zero is the blink phase,
// bit one invalidates FINAL, and bit two invalidates MAPPED. [03 §3.6]
const (
	MinimapDirtyBlink  uint8 = 1 << 0
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
	blink                  BlinkState
	mapW, mapH             int
	local                  uint8
	dcb                    byte
	remap                  []byte
	sensorCircles          []MinimapCircle
}

// MinimapCircle is a presentation-only sensor callback result. Coordinates
// are the 128-world-unit surface cells emitted by visibility.SensorTick.
type MinimapCircle struct {
	U, V   int32
	Radius int32
	Kind   uint8 // 0 outer radar/sonar, 1 radar jam, 2 sonar jam
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

// NewMinimapService creates a surface lifecycle. A nil picture is allowed;
// all rebuild methods then become no-ops, matching absent optional art.
func NewMinimapService(cfg MinimapServiceConfig) *MinimapService {
	s := &MinimapService{mapW: cfg.MapW, mapH: cfg.MapH, local: cfg.LocalSlot, dcb: cfg.FogFill, dirty: MinimapDirtyMapped | MinimapDirtyFinal}
	if cfg.Picture != nil {
		s.picture = cloneRadarSurface(cfg.Picture)
	}
	if len(cfg.GUIRemap) == 256 {
		s.remap = append([]byte(nil), cfg.GUIRemap...)
	}
	s.blink.Countdown = 7
	return s
}

func cloneRadarSurface(src *RadarSurface) *RadarSurface {
	if src == nil {
		return nil
	}
	return &RadarSurface{W: src.W, H: src.H, Pitch: src.Pitch, Bits: append([]byte(nil), src.Bits...)}
}

func (s *MinimapService) Picture() *RadarSurface { return cloneRadarSurface(s.picture) }
func (s *MinimapService) Mapped() *RadarSurface  { return cloneRadarSurface(s.mapped) }
func (s *MinimapService) Final() *RadarSurface   { return cloneRadarSurface(s.final) }
func (s *MinimapService) Dirty() uint8 {
	if s == nil {
		return 0
	}
	return s.dirty
}
func (s *MinimapService) Blink() BlinkState {
	if s == nil {
		return BlinkState{}
	}
	return s.blink
}

// MarkPlacement invalidates mapped and final surfaces after a placement or
// other world-picture change. Picture itself remains cached. [03 §3.6]
func (s *MinimapService) MarkPlacement() {
	if s != nil {
		s.dirty |= MinimapDirtyMapped | MinimapDirtyFinal
	}
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
func (s *MinimapService) RebuildFinal(m camera.Minimap, playW, playH int32, contacts []MinimapContact, blit MinimapContactBlitter, radarColor, jammerColor, ringColor byte) bool {
	if s == nil || s.mapped == nil {
		return false
	}
	// Sensor callback tables are the canonical circle source. Once the sensor
	// phase has published callbacks, contact records still provide blips and
	// commander art but must not draw the same circles a second time [03 §3.4].
	if len(s.sensorCircles) != 0 && len(contacts) != 0 {
		contacts = append([]MinimapContact(nil), contacts...)
		for i := range contacts {
			contacts[i].RawDistRadar = 0
			contacts[i].RawDistSonar = 0
			contacts[i].RawDistJamR = 0
			contacts[i].RawDistJamS = 0
		}
	}
	s.final = rebuildFinalExact(s.mapped, m, playW, playH, contacts, s.sensorCircles, s.blink, blit, radarColor, jammerColor, ringColor)
	s.dirty &^= MinimapDirtyFinal
	return s.final != nil
}

// Wipe implements the sensor backing-surface contract. It clears only the
// presentation circle list; LOS grids remain owned by visibility.Service.
func (s *MinimapService) Wipe() {
	if s != nil {
		s.sensorCircles = s.sensorCircles[:0]
		s.dirty |= MinimapDirtyFinal
	}
}

func (s *MinimapService) Sensor(u, v, radius int32) {
	if s != nil {
		s.sensorCircles = append(s.sensorCircles, MinimapCircle{U: u, V: v, Radius: radius})
		s.dirty |= MinimapDirtyFinal
	}
}

func (s *MinimapService) RadarJam(u, v, radius int32) {
	if s != nil {
		s.sensorCircles = append(s.sensorCircles, MinimapCircle{U: u, V: v, Radius: radius, Kind: 1})
		s.dirty |= MinimapDirtyFinal
	}
}

func (s *MinimapService) SonarJam(u, v, radius int32) {
	if s != nil {
		s.sensorCircles = append(s.sensorCircles, MinimapCircle{U: u, V: v, Radius: radius, Kind: 2})
		s.dirty |= MinimapDirtyFinal
	}
}

// Tick advances the host-frame blink cadence and schedules FINAL. Callers
// should then invoke RebuildMapped (if dirty) and RebuildFinal. [03 §3.6]
func (s *MinimapService) Tick() {
	if s == nil {
		return
	}
	old := s.blink.Phase
	s.blink.Tick()
	s.dirty |= MinimapDirtyFinal
	if old != s.blink.Phase {
		s.dirty |= MinimapDirtyBlink
	}
}

// Restore marks all derived surfaces for synchronous regeneration. Picture
// remains cached; MAPPED and FINAL are rebuilt before the restored frame is
// consumed. [03 §3.6]
func (s *MinimapService) Restore() {
	if s != nil {
		s.dirty |= MinimapDirtyMapped | MinimapDirtyFinal
	}
}

// ClearBlinkDirty acknowledges the blink schedule after FINAL has been drawn.
func (s *MinimapService) ClearBlinkDirty() {
	if s != nil {
		s.dirty &^= MinimapDirtyBlink
	}
}
