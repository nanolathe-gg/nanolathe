package session

import (
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/audio"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

func (s *Session) sharedAudioCRT() *rng.CRT {
	if s == nil {
		return nil
	}
	return s.CrtRNG()
}

// PresentationCRT returns the session-owned CRT stream shared by all
// presentation consumers. It never creates a second stream [01 §7.2][I4].
func (s *Session) PresentationCRT() *rng.CRT {
	return s.sharedAudioCRT()
}

// InitAudio creates the presentation audio queue/cache/music for the session
// [03 §8.3][03 §8.4] C16 C18 C20. It is presentation-only and uses the CRT
// stream for variant draws [03 §8.3] C19 [I4]; it never touches the simulation
// RNG. Missing optional sample aliases resolve to silence [03 §8.2], while
// the queue remains available for event production. Call after Catalog/Vis
// are bound. Repeating the call for the same service preserves cue state.
func (s *Session) InitAudio(fs vfs.FSOps) {
	if s == nil {
		return
	}
	if s.Audio == nil {
		s.Audio = audio.NewService(fs)
	} else {
		s.Audio.Init(fs)
	}
	if s.boundAudio != s.Audio {
		// The shell may reuse its service for a new or restored battle, whose
		// tick starts independently of the old deadlines. Detached candidates
		// bind the shell service only at successful presentation adoption.
		s.Audio.ResetBattleCues()
		s.Audio.BindCRT(s.sharedAudioCRT())
		s.boundAudio = s.Audio
	}
	s.Audio.BindCatalog(s.Catalog, s.audioResolver)
	// Music probing and briefing preload are presentation setup owned by the
	// audio service; Session only supplies mission metadata.
	hasBrief := false
	if s.Mission != nil && s.Mission.OTA != nil && s.Mission.OTA.Global != nil {
		g, _ := s.Mission.OTA.Global.StringValue("glamoursound", "")
		b, _ := s.Mission.OTA.Global.StringValue("brief", "")
		n, _ := s.Mission.OTA.Global.StringValue("narration", "")
		h, _ := s.Mission.OTA.Global.StringValue("missionhint", "")
		hasBrief = audio.BriefingAlias(g, b, n, h) != ""
		_ = s.Audio.PreloadBriefing(g, b, n, h)
	}
	s.Audio.ConfigureMusic(hasBrief)
}

// audioResolver maps a unit handle to its Category, definition display name
// and alive flag [03 §8.3] C17. It uses the catalog's Sounds map and the
// unit's SoundCategory field via content.CanonicalKey [02 §5]. Presentation-
// only, uses no Sim RNG.
func (s *Session) audioResolver(h pool.Handle) (*audio.Category, string, bool) {
	if s == nil || s.Catalog == nil || s.Units == nil {
		return nil, "", false
	}
	u := s.Units.Unit(h)
	if u == nil || u.Def == nil {
		return nil, "", false
	}
	ck := content.CanonicalKey(u.Def.SoundCategory)
	if ck == "" {
		return nil, u.Def.Name, u.Alive && !u.Dying
	}
	sc, ok := s.Catalog.Sounds[ck]
	if !ok || sc == nil {
		return nil, u.Def.Name, u.Alive && !u.Dying
	}
	return audio.CategoryFromContent(sc), u.Def.Name, u.Alive && !u.Dying
}

// SetAudioViewport sets the presentation viewport for positional pan and
// attenuation [03 §8.3] audience gating. It is presentation-only and never
// mutates authoritative state [I6].
func (s *Session) SetAudioViewport(v audio.Viewport) {
	if s == nil || s.Audio == nil {
		return
	}
	s.Audio.SetViewport(v)
}

// ViewportForAudio returns the current audio viewport for client presentation.
// It is a copy; mutations do not affect session state.
func (s *Session) ViewportForAudio() audio.Viewport {
	if s == nil || s.Audio == nil {
		return audio.Viewport{}
	}
	return s.Audio.Viewport()
}

// EmitSound inserts a category cue into the eight-slot queue [03 §8.3] C16.
// It respects per-slot cooldowns (nextAllowed) and duplicate-slot drops, sorts
// descending priority after equals for FIFO, and evicts the last entry silently
// when full [03 §8.3] C16. Tick is taken from s.Clock.GlobalTick when available;
// presentation RNG is via queue's CRT [I4] C19.
func (s *Session) EmitSound(slot audio.Slot, unit pool.Handle, text string) bool {
	if s == nil || s.Audio == nil || slot == 0 {
		return false
	}
	var frame uint32
	if s.Clock != nil {
		frame = s.Clock.GlobalTick
	} else {
		frame = s.Audio.Frame()
	}
	return s.Audio.Emit(frame, slot, unit, text)
}

// Convenience emitters for the 23 static slots [03 §8.3] C15.
// Each maps a UI/weapon/feature event to its slot priority/cooldown entry.

func (s *Session) EmitSelect(unit pool.Handle) bool { return s.EmitSound(audio.SlotSelect, unit, "") }
func (s *Session) EmitUnderAttack(unit pool.Handle) bool {
	return s.EmitSound(audio.SlotUnderAttack, unit, "")
}
func (s *Session) EmitActivate(unit pool.Handle) bool {
	return s.EmitSound(audio.SlotActivate, unit, "")
}
func (s *Session) EmitDeactivate(unit pool.Handle) bool {
	return s.EmitSound(audio.SlotDeactivate, unit, "")
}
func (s *Session) EmitOK(unit pool.Handle) bool      { return s.EmitSound(audio.SlotOK, unit, "") }
func (s *Session) EmitArrived(unit pool.Handle) bool { return s.EmitSound(audio.SlotArrived, unit, "") }
func (s *Session) EmitCant(unit pool.Handle) bool    { return s.EmitSound(audio.SlotCant, unit, "") }
func (s *Session) EmitUnitComplete(unit pool.Handle) bool {
	return s.EmitSound(audio.SlotUnitComplete, unit, "")
}
func (s *Session) EmitBuild(unit pool.Handle) bool   { return s.EmitSound(audio.SlotBuild, unit, "") }
func (s *Session) EmitRepair(unit pool.Handle) bool  { return s.EmitSound(audio.SlotRepair, unit, "") }
func (s *Session) EmitWorking(unit pool.Handle) bool { return s.EmitSound(audio.SlotWorking, unit, "") }
func (s *Session) EmitLoad(unit pool.Handle) bool    { return s.EmitSound(audio.SlotLoad, unit, "") }
func (s *Session) EmitUnload(unit pool.Handle) bool  { return s.EmitSound(audio.SlotUnload, unit, "") }
func (s *Session) EmitCloak(unit pool.Handle) bool   { return s.EmitSound(audio.SlotCloak, unit, "") }
func (s *Session) EmitUncloak(unit pool.Handle) bool { return s.EmitSound(audio.SlotUncloak, unit, "") }
func (s *Session) EmitCapture(unit pool.Handle) bool { return s.EmitSound(audio.SlotCapture, unit, "") }
func (s *Session) EmitCount5(unit pool.Handle) bool  { return s.EmitSound(audio.SlotCount5, unit, "") }
func (s *Session) EmitCount4(unit pool.Handle) bool  { return s.EmitSound(audio.SlotCount4, unit, "") }
func (s *Session) EmitCount3(unit pool.Handle) bool  { return s.EmitSound(audio.SlotCount3, unit, "") }
func (s *Session) EmitCount2(unit pool.Handle) bool  { return s.EmitSound(audio.SlotCount2, unit, "") }
func (s *Session) EmitCount1(unit pool.Handle) bool  { return s.EmitSound(audio.SlotCount1, unit, "") }
func (s *Session) EmitCount0(unit pool.Handle) bool  { return s.EmitSound(audio.SlotCount0, unit, "") }
func (s *Session) EmitCancelDestruct(unit pool.Handle) bool {
	return s.EmitSound(audio.SlotCancelDestruct, unit, "")
}

// IsAudibleAt reports the retail audience gate for a world position
// [03 §8.3] "audience gating". The gate is the visibility service's one-point
// predicate at the local viewing player's slot: the point is projected with
// the half-height shear and tested against the mode-selected grid — the
// explored byte grid when the mode word's bit 1 is set, otherwise the LOS word
// mask at the local player's bit alone, never an ally OR [03 §3.1]. Off-map is
// silent. Presentation-only, uses no Sim RNG [I4].
//
// Correction: this quantized X and Z alone, dropping the shear, so an elevated
// source was gated at the ground cell beneath it rather than the cell it
// occupies on screen — audible when the ground was seen and the aircraft was
// not, and silent in the converse case. It also copied the whole word mask and
// all ten player byte grids per query to read one cell; the service answers
// with a scalar instead [03 §3.2] step 4.
func (s *Session) IsAudibleAt(pos [3]numeric.Fixed) bool {
	if s == nil || s.Vis == nil {
		return false // positional audio requires the session visibility service [03 §8.3]
	}
	local := int(s.ViewingOwner)
	return s.Vis.AudiblePoint(visibility.PlayerID(local), pos[0], pos[1], pos[2])
}

// PositionalPan returns the retail viewport-relative pan for a world pos
// [03 §8.3] when stereo capable: dx = px - ((w/2)<<4) - left and dy = top +
// ((h/2)<<4) + (py>>1) - pz with half-height shear. Presentation-only [I4].
func (s *Session) PositionalPan(pos [3]numeric.Fixed) audio.Pan {
	if s == nil || s.Audio == nil {
		return audio.Pan{}
	}
	return audio.ComputePan(pos, s.Audio.Viewport())
}

// PositionalAttenuation returns the two-level mono-fallback volume
// [03 §8.3]: in-view -585 vs off-screen -1585 via inclusive bounds
// left<=x<=right and top<=z<=bottom where right=left+w*0x10. Retail never
// discards off-screen, just attenuates.
func (s *Session) PositionalAttenuation(pos [3]numeric.Fixed) int32 {
	if s == nil || s.Audio == nil {
		return audio.VolInView
	}
	return audio.Attenuate(pos, s.Audio.Viewport())
}

// EmitPositional tries to play a world-space alias with audience gating and
// viewport-relative placement [03 §8.3]. It first checks IsAudibleAt; off-map
// or failing the local gate is silent locally. Otherwise it computes pan or
// attenuation (caller can use the returned values for mixer), attempts to load
// the sample via cache (missing aliases resolve to silence [03 §8.2]), and returns pan/vol
// plus audible flag. Presentation-only, uses CRT for any variant draw inside
// the cache path only if the alias is a category variant; direct alias load
// does not draw [I4].
// The admitted value is played only after its containing frame is committed
// and drained by the presentation edge; authoritative callbacks never touch
// the backend [03 §8.3] [I6].
func (s *Session) EmitPositional(alias string, pos [3]numeric.Fixed) (audio.Pan, int32, bool) {
	return s.emitPositional(alias, pos, false)
}

func (s *Session) emitPositional(alias string, pos [3]numeric.Fixed, isWater bool) (audio.Pan, int32, bool) {
	if s == nil || s.Audio == nil || strings.TrimSpace(alias) == "" || !s.IsAudibleAt(pos) {
		return audio.Pan{}, 0, false
	}
	v := s.Audio.Viewport()
	var pan audio.Pan
	var volume int32
	if v.StereoCapable {
		pan = audio.ComputePan(pos, v)
		volume = audio.VolInView
	} else {
		volume = audio.Attenuate(pos, v)
	}
	if s.publication == nil || s.publication.events == nil {
		return pan, volume, false
	}
	tick := uint32(0)
	if s.Clock != nil {
		tick = s.Clock.GlobalTick
	}
	ok := s.publication.events.EmitAudio(frame.Event{Tick: tick, Sound: alias, AudioWater: isWater, AudioAudible: true, X: pos[0], Y: pos[1], Z: pos[2]})
	return pan, volume, ok
}

// EmitWeaponStart is the weapon fire path [06 §13.2][03 §8.3]: projectile
// creation queues hit/water sound synchronously with start sound via
// presentation sink. This helper provides the same gating for weapon aliases
// without requiring combat to import audio directly: session owns the port.
func (s *Session) EmitWeaponStart(alias string, pos [3]numeric.Fixed) (audio.Pan, int32, bool) {
	return s.emitPositional(alias, pos, false)
}

// EmitWeaponHit emits a hit or water sound for projectile impact ordering
// [06 §13.2][GAP T21] shake→hit/water→smoke→GAF→damage. Caller chooses alias
// based on terrain/water gate before calling; gating and pan are applied here.
func (s *Session) EmitWeaponHit(alias string, pos [3]numeric.Fixed, isWater bool) (audio.Pan, int32, bool) {
	if isWater && alias == "" {
		return audio.Pan{}, 0, false
	}
	return s.emitPositional(alias, pos, isWater)
}

// PlayBriefing plays the mission briefing sound if present; otherwise it
// falls back to CD/MCI playback [03 §8.4]. It uses the briefing alias
// resolution order GlamourSound → Brief → Narration → MissionHint
// [03 §8.4] and resolves an unavailable alias through the CD/MCI path.
func (s *Session) PlayBriefing() bool {
	if s == nil || s.Audio == nil {
		return false
	}
	var g, b, n, h string
	if s.Mission != nil && s.Mission.OTA != nil && s.Mission.OTA.Global != nil {
		g, _ = s.Mission.OTA.Global.StringValue("glamoursound", "")
		b, _ = s.Mission.OTA.Global.StringValue("brief", "")
		n, _ = s.Mission.OTA.Global.StringValue("narration", "")
		h, _ = s.Mission.OTA.Global.StringValue("missionhint", "")
	}
	return s.Audio.PlayBriefing(g, b, n, h)
}
