package session

import (
	"strings"

	"github.com/nanolathe/nanolathe/internal/audio"
	"github.com/nanolathe/nanolathe/internal/combat"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/presentation"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/snapshot"
	"github.com/nanolathe/nanolathe/vfs"
)

func (s *Session) sharedAudioCRT() *presentation.CRTRandom {
	if s == nil {
		return nil
	}
	if s.audioCRT == nil {
		s.audioCRT = presentation.WrapCRT(s.CrtRNG())
	}
	return s.audioCRT
}

// SetPresentationClock binds the frame-domain clock used by audio. Session
// simulation ticks remain valid only for authoritative work; queue timing is
// presentation FrameSerial timing [03 §8.3][I6].
func (s *Session) SetPresentationClock(c *presentation.Clock) {
	if s == nil {
		return
	}
	s.audioClock = c
}

func (s *Session) presentationClock() *presentation.Clock {
	if s == nil {
		return nil
	}
	return s.audioClock
}

// PresentationClock returns the session-local presentation clock shared by
// the battle client and audio services. It is intentionally separate from the
// authoritative simulation clock [03 §1][I6].
func (s *Session) PresentationClock() *presentation.Clock {
	if s == nil {
		return nil
	}
	if s.audioClock == nil {
		s.audioClock = &presentation.Clock{}
	}
	return s.audioClock
}

// PresentationCRT returns the session-owned CRT wrapper shared by all
// presentation consumers. It never creates a second stream [01 §7.2][I4].
func (s *Session) PresentationCRT() *presentation.CRTRandom {
	return s.sharedAudioCRT()
}

// InitAudio creates the presentation audio queue/cache/music for the session
// [03 §8.3][03 §8.4] C16 C18 C20. It is presentation-only and uses the CRT
// stream for variant draws [03 §8.3] C19 [I4]; it never touches the simulation
// RNG. Missing optional sample aliases resolve to silence [03 §8.2], while
// the queue remains available for event production. Call once after
// Catalog/Vis are bound.
func (s *Session) InitAudio(fs vfs.FSOps) {
	if s == nil {
		return
	}
	if fs != nil {
		s.audioFS = fs
	}
	if s.AudioQueue == nil {
		s.AudioQueue = audio.NewQueue()
		// The queue starts with its presentation seed until the shared CRT is bound [I4].
		s.AudioQueue.Seed(1)
		s.AudioQueue.Configure(10, 10, true, true)
	}
	if s.AudioRegistry == nil {
		if s.audioFS != nil {
			s.AudioRegistry = audio.NewRegistry(s.audioFS)
		} else {
			s.AudioRegistry = audio.NewRegistry(fs)
		}
	}
	s.AudioCache = s.AudioRegistry.Cache()
	if s.Catalog != nil {
		for _, alias := range s.Catalog.AliasOrder {
			if alias != nil {
				s.AudioRegistry.RegisterPath(alias.Alias, alias.Sound)
			}
		}
	}
	if s.AudioCache == nil {
		if s.audioFS != nil {
			s.AudioCache = audio.NewCache(s.audioFS)
		} else if fs != nil {
			s.AudioCache = audio.NewCache(fs)
		} else {
			s.AudioCache = audio.NewCache(nil)
		}
	}
	if s.AudioMusic == nil {
		s.AudioMusic = audio.NewMusicController()
	}
	// Both services wrap the session-owned CRT stream.  They must not seed
	// private generators: silent voice resolves and CD picks share draw order
	// with every other presentation consumer [01 §7.2][03 §8.3–§8.4].
	sharedCRT := s.sharedAudioCRT()
	s.AudioQueue.SetCRTRandom(sharedCRT)
	s.AudioMusic.SetCRTRandom(sharedCRT)
	// Resolver maps unit handle to its sound category via catalog [02 "Sound category record"] [03 §8.3] C17.
	s.AudioQueue.SetResolver(s.audioResolver)
	// Playback sink loads the sample via cache; missing alias degrades silently [03 §8.2] C20.
	// When a backend is present it also plays via PCM [03 §8.3] [I6].
	s.AudioQueue.OnPlay(func(alias string, slot audio.Slot, unit pool.Handle) {
		if alias == "" || s.AudioRegistry == nil {
			return
		}
		id := s.AudioRegistry.Lookup(alias)
		if id == audio.MissingAlias {
			id = s.AudioRegistry.Register(alias)
		}
		sample, err := s.AudioRegistry.Load(id)
		if err == nil {
			if be := audio.GlobalBackend(); be != nil {
				_ = be.PlaySample(sample, 1.0, 0)
			}
		}
	})
	// Speech sink is presentation only; keep no-op for now but preserve call order [03 §8.3] C17.
	s.AudioQueue.OnSpeech(func(line string) {
		_ = line
	})
	// Probe the CD/MCI track path [03 §8.4].
	s.initMusicTracks()
	// Preload the selected briefing alias without playing it [03 §8.4].
	_ = s.preloadBriefing()
}

// audioResolver maps a unit handle to its Category, name and alive flag
// [03 §8.3] C17. It uses the catalog's Sounds map and the unit's SoundCategory
// field via content.CanonicalKey [02 §5]. Presentation-only, uses no Sim RNG.
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
		return nil, u.Def.UnitName, u.Alive && !u.Dying
	}
	sc, ok := s.Catalog.Sounds[ck]
	if !ok || sc == nil {
		return nil, u.Def.UnitName, u.Alive && !u.Dying
	}
	return audio.CategoryFromContent(sc), u.Def.UnitName, u.Alive && !u.Dying
}

func (s *Session) loadAudioAlias(alias string) (*audio.Sample, error) {
	if s == nil || strings.TrimSpace(alias) == "" {
		return nil, nil
	}
	if s.AudioRegistry != nil {
		id := s.AudioRegistry.Lookup(alias)
		if id == audio.MissingAlias {
			id = s.AudioRegistry.Register(alias)
		}
		return s.AudioRegistry.Load(id)
	}
	if s.AudioCache != nil {
		return s.AudioCache.Load(alias)
	}
	return nil, nil
}

// SetAudioViewport sets the presentation viewport for positional pan and
// attenuation [03 §8.3] audience gating. It is presentation-only and never
// mutates authoritative state [I6].
func (s *Session) SetAudioViewport(v audio.Viewport) {
	if s == nil {
		return
	}
	s.audioViewport = v
}

// ViewportForAudio returns the current audio viewport for client presentation.
// It is a copy; mutations do not affect session state.
func (s *Session) ViewportForAudio() audio.Viewport {
	if s == nil {
		return audio.Viewport{}
	}
	return s.audioViewport
}

// EmitSound inserts a category cue into the eight-slot queue [03 §8.3] C16.
// It respects per-slot cooldowns (nextAllowed) and duplicate-slot drops, sorts
// descending priority after equals for FIFO, and evicts the last entry silently
// when full [03 §8.3] C16. Tick is taken from s.Clock.GlobalTick when available;
// presentation RNG is via queue's CRT [I4] C19.
func (s *Session) EmitSound(slot audio.Slot, unit pool.Handle, text string) bool {
	if s == nil || s.AudioQueue == nil || slot == 0 {
		return false
	}
	var frame uint32
	if clock := s.presentationClock(); clock != nil {
		frame = clock.FrameSerial
	} else if s.Clock != nil {
		frame = s.Clock.GlobalTick
	} else {
		frame = s.audioFrame
	}
	s.AudioQueue.SetNow(frame)
	return s.AudioQueue.InsertAt(frame, slot, unit, text)
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
// [03 §8.3] presentation. It quantizes with sign-corrected floor division to visibility
// tiles (pos>>20) and tests the mode-selected grid: explored byte grid when
// mode &2 !=0 else LOS word mask at local player bit only (no ally OR)
// [03 §3.1]. Off-map is silent. Presentation-only, uses no Sim RNG [I4].
func (s *Session) IsAudibleAt(pos [3]numeric.Fixed) bool {
	if s == nil || s.Vis == nil {
		return false // positional audio requires the session visibility service [03 §8.3]
	}
	cx, cz := audio.CellFromWorld2D(pos)
	w, h := s.Vis.GridDimensions()
	if w <= 0 || h <= 0 {
		return false
	}
	wordMask, byteGrids := s.Vis.GridSnapshot()
	local := localPlayerForSession(s)
	mode := audio.VisibilityMode(s.Vis.Mode() & 0x02) // bit1 chooses byte vs word [03 §3.1][03 §8.3]
	gridLen := int(w) * int(h)
	if mode&audio.ModeExplored != 0 {
		if local < 0 || local >= len(byteGrids) || len(byteGrids[local]) != gridLen {
			return false
		}
	} else if len(wordMask) != gridLen {
		return false
	}
	return audio.IsAudible(cx, cz, local, mode, wordMask, byteGrids, int(w), int(h))
}

// PositionalPan returns the retail viewport-relative pan for a world pos
// [03 §8.3] when stereo capable: dx = px - ((w/2)<<4) - left and dy = top +
// ((h/2)<<4) + (py>>1) - pz with half-height shear. Presentation-only [I4].
func (s *Session) PositionalPan(pos [3]numeric.Fixed) audio.Pan {
	if s == nil {
		return audio.Pan{}
	}
	return audio.ComputePan(pos, s.audioViewport)
}

// PositionalAttenuation returns the two-level mono-fallback volume
// [03 §8.3]: in-view -585 vs off-screen -1585 via inclusive bounds
// left<=x<=right and top<=z<=bottom where right=left+w*0x10. Retail never
// discards off-screen, just attenuates.
func (s *Session) PositionalAttenuation(pos [3]numeric.Fixed) int32 {
	if s == nil {
		return audio.VolInView
	}
	return audio.Attenuate(pos, s.audioViewport)
}

// EmitPositional tries to play a world-space alias with audience gating and
// viewport-relative placement [03 §8.3]. It first checks IsAudibleAt; off-map
// or failing the local gate is silent locally. Otherwise it computes pan or
// attenuation (caller can use the returned values for mixer), attempts to load
// the sample via cache (missing aliases resolve to silence [03 §8.2]), and returns pan/vol
// plus audible flag. Presentation-only, uses CRT for any variant draw inside
// the cache path only if the alias is a category variant; direct alias load
// does not draw [I4].
// When a windowed backend is installed the alias is also played via PCM with
// volume/pan derived from the positional math [03 §8.3] [I6].
func (s *Session) EmitPositional(alias string, pos [3]numeric.Fixed) (audio.Pan, int32, bool) {
	alias = strings.TrimSpace(alias)
	if alias == "" || s == nil {
		return audio.Pan{}, 0, false
	}
	if !s.IsAudibleAt(pos) {
		return audio.Pan{}, 0, false
	}
	var pan audio.Pan
	var vol int32
	if s.audioViewport.StereoCapable {
		pan = s.PositionalPan(pos)
		vol = audio.VolInView // stereo path does not use mono attenuation, keep in-view
	} else {
		vol = s.PositionalAttenuation(pos)
		pan = audio.Pan{}
	}
	var sample *audio.Sample
	if s.AudioRegistry != nil {
		id := s.AudioRegistry.Lookup(alias)
		if id == audio.MissingAlias {
			id = s.AudioRegistry.Register(alias)
		}
		sample, _ = s.AudioRegistry.Load(id)
	} else if s.AudioCache != nil {
		sample, _ = s.AudioCache.Load(alias)
	}
	if be := audio.GlobalBackend(); be != nil {
		volF := audio.VolumeFromAttenuation(vol)
		panF := audio.PanFloat(pan, s.audioViewport)
		if sample != nil {
			_ = be.PlaySample(sample, volF, panF)
		}
	}
	return pan, vol, true
}

// EmitWeaponStart is the weapon fire path [06 §13.2][03 §8.3]: projectile
// creation queues hit/water sound synchronously with start sound via
// presentation sink. This helper provides the same gating for weapon aliases
// without requiring combat to import audio directly: session owns the port.
func (s *Session) EmitWeaponStart(alias string, pos [3]numeric.Fixed) (audio.Pan, int32, bool) {
	return s.EmitPositional(alias, pos)
}

// EmitWeaponHit emits a hit or water sound for projectile impact ordering
// [06 §13.2][GAP T21] shake→hit/water→smoke→GAF→damage. Caller chooses alias
// based on terrain/water gate before calling; gating and pan are applied here.
func (s *Session) EmitWeaponHit(alias string, pos [3]numeric.Fixed, isWater bool) (audio.Pan, int32, bool) {
	if isWater && alias == "" {
		return audio.Pan{}, 0, false
	}
	return s.EmitPositional(alias, pos)
}

// TickAudio drains the queue once per rendered frame outside simulation
// [03 §8.3] C18. Empty queue does nothing; within 30 frames of BaseTime
// resolve head silently; otherwise audible and reset BaseTime. Call from
// presentation (client.Frame) with the presentation frame counter [I6].
func (s *Session) TickAudio(presentationFrame uint32) {
	if s == nil || s.AudioQueue == nil {
		return
	}
	s.AudioQueue.Drain(presentationFrame)
	s.audioFrame = presentationFrame
	// Tick music controller with MCI poll [03 §8.4]. Poll isPlaying via
	// controller.IsPlaying(); retail would query mciSendStringA status.
	if s.AudioMusic != nil {
		s.AudioMusic.Tick(s.AudioMusic.IsPlaying())
	}
}

// TickAudioClock drains and polls music using the shared presentation clock.
// It is the preferred presentation entry point; TickAudio(uint32) remains for
// callers that explicitly provide a FrameSerial.
func (s *Session) TickAudioClock(clock *presentation.Clock) {
	if s == nil || s.AudioQueue == nil || clock == nil {
		return
	}
	s.SetPresentationClock(clock)
	s.AudioQueue.Drain(clock.FrameSerial)
	s.audioFrame = clock.FrameSerial
	if s.AudioMusic != nil {
		s.AudioMusic.TickFrame(clock, s.AudioMusic.IsPlaying())
	}
}

// CollectAudioForSnapshot copies pending audio cues into a snapshot frame's
// Sounds slice [03 §8.3][03 §2.4] I6. It is presentation-only and does not
// mutate simulation state; snapshot publish is the sole writer of frame.Sounds
// [I1][I6]. Call just before Snapshot.Publish so client can read Sounds without
// touching the live queue. Alias is resolved via variant draw at Drain time,
// so this copy stores Slot/Unit/Frame only; alias is filled at playback via
// OnPlay.
func (s *Session) CollectAudioForSnapshot(frame *snapshot.Frame) {
	if s == nil || frame == nil || s.AudioQueue == nil {
		return
	}
	if s.AudioQueue.Count == 0 {
		frame.Sounds = nil
		return
	}
	// Copy pending entries as SoundEvent in queue order (already sorted descending priority FIFO) [I1].
	out := make([]snapshot.SoundEvent, 0, s.AudioQueue.Count)
	for i := 0; i < s.AudioQueue.Count; i++ {
		e := s.AudioQueue.Entries[i]
		out = append(out, snapshot.SoundEvent{
			Alias: "", // filled at Drain via OnPlay variant draw [03 §8.3] C17
			Slot:  uint8(e.Slot),
			Unit:  e.Unit,
			Frame: e.Frame,
		})
	}
	frame.Sounds = out
}

// initMusicTracks probes VFS for CD/music track count and configures the
// controller's mode and track count [03 §8.4]. Zero tracks leaves the CD/MCI
// controller idle, as on a missing disc. Presentation-only.
func (s *Session) initMusicTracks() {
	if s == nil || s.AudioMusic == nil {
		return
	}
	var fs vfs.FSOps
	if s.audioFS != nil {
		fs = s.audioFS
	}
	num := audio.ProbeMusicTracks(fs)
	// A briefing alias selects the single-track CD/MCI mode [03 §8.4].
	hasBrief := false
	if s.Mission != nil && s.Mission.OTA != nil && s.Mission.OTA.Global != nil {
		// Decode quickly without full MissionGlobals to avoid import cycle; use raw string presence.
		if v, ok := s.Mission.OTA.Global.StringValue("glamoursound", ""); ok && strings.TrimSpace(v) != "" {
			hasBrief = true
		} else if v, ok := s.Mission.OTA.Global.StringValue("brief", ""); ok && strings.TrimSpace(v) != "" {
			hasBrief = true
		}
	}
	mode := audio.SelectMusicMode(num, hasBrief)
	s.AudioMusic.Configure(mode, 0)
	s.AudioMusic.SetNumTracks(num)
	if num > 0 {
		_ = s.AudioMusic.Open(num)
		// Do not auto-play here; Tick will advance on next presentation frame when not playing [03 §8.4].
	}
}

// preloadBriefing attempts to load the mission briefing sound alias selected
// by the retail field precedence [03 §8.4]. Missing aliases resolve to silence
// [03 §8.2]. Presentation-only.
func (s *Session) preloadBriefing() error {
	if s == nil || s.AudioCache == nil || s.Mission == nil || s.Mission.OTA == nil || s.Mission.OTA.Global == nil {
		return nil
	}
	g, _ := s.Mission.OTA.Global.StringValue("glamoursound", "")
	b, _ := s.Mission.OTA.Global.StringValue("brief", "")
	n, _ := s.Mission.OTA.Global.StringValue("narration", "")
	h, _ := s.Mission.OTA.Global.StringValue("missionhint", "")
	alias := audio.BriefingAlias(g, b, n, h)
	if alias == "" {
		return nil
	}
	_, err := s.loadAudioAlias(alias)
	if err != nil {
		// PlayBriefing may use the CD/MCI path when this alias is unavailable.
		return err
	}
	return nil
}

// InstallAudioBridge wires combat events to audio queue/positional playback
// [03 §8.3] [06 §13.2] [GAP T21] presentation-only [I6]. It wraps the existing
// combat event sink installed at composition.go:496 and additionally maps hit
// and water sounds via EmitWeaponHit and start sounds via EmitWeaponStart.
// Unit-complete etc. remain via the construction hooks. The orchestrator calls
// this from composition.go after the combat service is bound; do NOT edit
// composition.go directly. Device ownership remains at the client boundary.
func (s *Session) InstallAudioBridge() {
	if s == nil || s.Combat == nil {
		return
	}
	prev := s.Combat.Events
	s.Combat.Events = func(ev combat.Event) {
		if prev != nil {
			prev(ev)
		}
		switch ev.Kind {
		case combat.EventHitSound:
			if ev.Sound != "" {
				pos := [3]numeric.Fixed{ev.Position.X, ev.Position.Y, ev.Position.Z}
				_, _, _ = s.EmitWeaponHit(ev.Sound, pos, false)
			}
		case combat.EventWaterSound:
			if ev.Sound != "" {
				pos := [3]numeric.Fixed{ev.Position.X, ev.Position.Y, ev.Position.Z}
				_, _, _ = s.EmitWeaponHit(ev.Sound, pos, true)
			}
		case combat.EventShake, combat.EventEndSmoke, combat.EventExplosion, combat.EventWaterExplosion, combat.EventProjectileImpact, combat.EventUnitKilled, combat.EventCorpse:
			// No audio mapping; shake is presentation but not audio [03 §5.6]
		default:
			if ev.Sound != "" {
				pos := [3]numeric.Fixed{ev.Position.X, ev.Position.Y, ev.Position.Z}
				_, _, _ = s.EmitWeaponStart(ev.Sound, pos)
			}
		}
	}
}

// InstallAudioBridge is the package-level form for the orchestrator's call
// from composition.go [03 §8.3] [I6]. It forwards to the session method.
func InstallAudioBridge(s *Session) {
	if s != nil {
		s.InstallAudioBridge()
	}
}

// PlayBriefing plays the mission briefing sound if present; otherwise it
// falls back to CD/MCI playback [03 §8.4]. It uses the briefing alias
// resolution order GlamourSound → Brief → Narration → MissionHint
// [03 §8.4] and resolves an unavailable alias through the CD/MCI path.
func (s *Session) PlayBriefing() bool {
	if s == nil {
		return false
	}
	alias := ""
	if s.Mission != nil && s.Mission.OTA != nil && s.Mission.OTA.Global != nil {
		g, _ := s.Mission.OTA.Global.StringValue("glamoursound", "")
		b, _ := s.Mission.OTA.Global.StringValue("brief", "")
		n, _ := s.Mission.OTA.Global.StringValue("narration", "")
		h, _ := s.Mission.OTA.Global.StringValue("missionhint", "")
		alias = audio.BriefingAlias(g, b, n, h)
	}
	if alias != "" {
		if sample, err := s.loadAudioAlias(alias); err == nil {
			// Briefing audio has no world position: play at unity volume with no
			// pan, independently of the battle visibility service [03 §8.4].
			if sample != nil {
				if be := audio.GlobalBackend(); be != nil {
					_ = be.PlaySample(sample, 1.0, 0)
				}
			}
			return true
		}
	}
	// CD fallback via music controller [03 §8.4].
	if s.AudioMusic != nil && s.AudioMusic.NumTracks() > 0 {
		return s.AudioMusic.Play(1)
	}
	return false
}
