package main

import (
	"fmt"
	"strings"

	"github.com/nanolathe/nanolathe/internal/mission"
)

// BriefingAction is the semantic command surface of MSNBRIEF.GUI. The
// controller deliberately does not accept GUI strings from callers; the
// authored controls are translated at the shell boundary [07 R-FE-02 §7].
type BriefingAction uint8

const (
	BriefingActionNone BriefingAction = iota
	BriefingActionStart
	BriefingActionPrev
	BriefingActionShutup
	BriefingActionMore
)

// BriefingState identifies the lifetime of one campaign briefing.
type BriefingState uint8

const (
	BriefingClosed BriefingState = iota
	BriefingOpen
)

// BriefingPlanet is one row of the authored 15-entry parallel planet table
// [08 R-CAMP-01 §2]. The spellings are intentionally retained, including the
// asymmetrical Wet Desert and Crystal names.
type BriefingPlanet struct {
	Name     string
	Brief    string
	Panorama string
	Rotate   string
}

var briefingPlanets = [...]BriefingPlanet{
	{Name: "Green planet", Brief: "Greenbrief", Panorama: "GreenPan", Rotate: "GreenRotate"},
	{Name: "Archipelago", Brief: "Archibrief", Panorama: "ArchiPan", Rotate: "ArchiRotate"},
	{Name: "Wet Desert", Brief: "WDesertbrief", Panorama: "WDesPan", Rotate: "WDesertRotate"},
	{Name: "Desert", Brief: "Desertbrief", Panorama: "DDesPan", Rotate: "DDesRotate"},
	{Name: "Lava", Brief: "Lavabrief", Panorama: "LavaPan", Rotate: "LavaRotate"},
	{Name: "Red Planet", Brief: "Marsbrief", Panorama: "MarsPan", Rotate: "MarsRotate"},
	{Name: "Lunar", Brief: "Lunarbrief", Panorama: "LunarPan", Rotate: "LunarRotate"},
	{Name: "Metal", Brief: "Metalbrief", Panorama: "MetalPan", Rotate: "MetalRotate"},
	{Name: "Lunar2", Brief: "Lunar2brief", Panorama: "Lunar2Pan", Rotate: "Lunar2Rotate"},
	{Name: "Ice", Brief: "Icebrief", Panorama: "IcePan", Rotate: "IceRotate"},
	{Name: "Lush", Brief: "Lushbrief", Panorama: "LushPan", Rotate: "LushRotate"},
	{Name: "Slate", Brief: "Slatebrief", Panorama: "SlatePan", Rotate: "SlateRotate"},
	{Name: "Water World", Brief: "Waterbrief", Panorama: "WaterPan", Rotate: "WaterRotate"},
	{Name: "Acid", Brief: "Acidbrief", Panorama: "AcidPan", Rotate: "AcidRotate"},
	{Name: "Crystal", Brief: "Crystalbrief", Panorama: "CrystPan", Rotate: "CrystalRotate"},
}

// ResolveBriefingPlanet performs the retail walk. Unknown values select row 0
// rather than suppressing the art [08 R-CAMP-01 §2]. Core's Lunar briefing is
// rewritten to Lunar2 before lookup.
func ResolveBriefingPlanet(planet string, localSide int) (BriefingPlanet, int) {
	lookup := planet
	if strings.EqualFold(lookup, "Lunar") && localSide != 0 {
		lookup += "2"
	}
	for i := range briefingPlanets {
		if strings.EqualFold(briefingPlanets[i].Name, lookup) {
			return briefingPlanets[i], i
		}
	}
	return briefingPlanets[0], 0
}

// briefingPresentationTick converts the shell's monotonic milliseconds to
// the established 30 Hz presentation clock [07 R-CAM-01 §1].
func briefingPresentationTick(nowMS int64) int64 {
	return (nowMS * 30) / 1000
}

type briefingRandom interface {
	Rand() int32
}

// BriefingBattleEvent is emitted by Start after the shared request builder
// has succeeded. Campaign briefing code never constructs a session itself.
type BriefingBattleEvent struct {
	Request freshBattleRequest
	Valid   bool
	Audio   []BriefingAudioEffect
}

type BriefingAudioKind uint8

const (
	BriefingAudioStop BriefingAudioKind = iota
	BriefingAudioStart
)

// BriefingAudioEffect is the typed hand-off to the audio owner. Delay 60 and
// volume 0 are the established narration stream arguments [03 R-AUD-02 §1].
type BriefingAudioEffect struct {
	Kind   BriefingAudioKind
	Path   string
	Delay  int
	Volume int
}

// campaignBriefingController is presentation state only. The mission remains
// immutable and authoritative battle construction happens after Start [I6].
type campaignBriefingController struct {
	mission   *mission.Mission
	localSide int
	planet    BriefingPlanet
	planetIdx int
	state     BriefingState

	crt briefingRandom

	minWind   int32
	maxWind   int32
	windSpeed int32
	countdown int32

	panoramaFrame  int
	panoramaCount  int
	scroll         int
	scrollDeadline int64
	scrollStarted  bool
	rotateFrame    int
	lastTick       int64
	lastWallMS     int64
	text           string
	page           int
	pageLines      int
	pageCount      int
	narrationPath  string
	narrationOn    bool

	request func() (freshBattleRequest, error)
}

// NewCampaignBriefingController opens one briefing and consumes the two
// entry CRT draws in their authored order [08 R-CAMP-01 §2][01 §7.3].
func NewCampaignBriefingController(m *mission.Mission, localSide int, crt briefingRandom, request func() (freshBattleRequest, error)) *campaignBriefingController {
	b := &campaignBriefingController{mission: m, localSide: localSide, crt: crt, request: request, state: BriefingOpen, lastTick: -1, lastWallMS: -1}
	if m != nil {
		b.minWind, b.maxWind = m.WindBounds.Min, m.WindBounds.Max
		if m.OTA != nil && m.OTA.Global != nil {
			globals := mission.DecodeMissionGlobals(m.OTA.Global)
			b.planet, b.planetIdx = ResolveBriefingPlanet(globals.Planet, localSide)
			b.narrationPath = briefingMediaPath(globals.Narration, "wav")
		} else {
			b.planet, b.planetIdx = ResolveBriefingPlanet("", localSide)
		}
	} else {
		b.planet, b.planetIdx = ResolveBriefingPlanet("", localSide)
	}
	b.narrationOn = b.narrationPath != ""
	if b.maxWind < b.minWind {
		// TODO(question): retail's malformed maxwindspeed < minwindspeed modulo
		// behavior is not settled. The placeholder clamps max to min, preserving
		// a one-value display range until a probe closes it.
		b.maxWind = b.minWind
	}
	b.entryWind()
	return b
}

// OpeningAudio is the delayed narration request emitted when MSNBRIEF opens.
// The shell/audio owner may bind this effect without coupling the controller to
// a sound backend [03 R-AUD-02 §1].
func (b *campaignBriefingController) OpeningAudio() []BriefingAudioEffect {
	return b.startAudio()
}

func briefingMediaPath(name, extension string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if dot := strings.LastIndex(name, "."); dot >= 0 {
		name = name[:dot]
	}
	return "camps/briefs/" + name + "." + extension
}

// NewBriefingController is the exported spelling for focused controller tests.
func NewBriefingController(m *mission.Mission, localSide int, crt briefingRandom, request func() (freshBattleRequest, error)) *campaignBriefingController {
	return NewCampaignBriefingController(m, localSide, crt, request)
}

func (b *campaignBriefingController) entryWind() {
	if b == nil || b.crt == nil {
		return
	}
	span := uint32(b.maxWind-b.minWind) + 1
	// span is at least one after the malformed-range guard above, so this is
	// the raw CRT modulo used by the briefing screen [01 §7.3].
	b.windSpeed = b.minWind + int32(uint32(b.crt.Rand())%span)
	b.countdown = int32(b.crt.Rand() & 0x3f)
}

// Update advances the presentation draw. nowMS is the presentation wall-time
// sample used by the 25 ms planet rotator gate; tick is the presentation tick
// used for the one-frame-per-tick rotation rule [08 R-CAMP-01 §2].
func (b *campaignBriefingController) Update(nowMS, tick int64) {
	if b == nil || b.state != BriefingOpen {
		return
	}
	b.countdown--
	if b.countdown < 1 {
		b.changeWind()
	}
	// Panorama selection is sampled on every draw from the scaled presentation
	// clock. It is independent of the slower planet-rotation wall gate [08
	// R-CAMP-01 §2].
	if b.panoramaCount > 0 {
		b.panoramaFrame = int((tick / 3) % int64(b.panoramaCount))
	}
	if nowMS-b.lastWallMS >= 25 && tick != b.lastTick {
		b.rotateFrame++
		b.lastWallMS, b.lastTick = nowMS, tick
	}
	if !b.scrollStarted {
		b.scrollDeadline = tick + 2
		b.scrollStarted = true
	} else if tick > b.scrollDeadline {
		b.scroll++
		b.scrollDeadline = tick + 2
	}
}

func (b *campaignBriefingController) changeWind() {
	if b == nil || b.crt == nil {
		return
	}
	// One draw changes speed, then one draw seeds the next countdown. The
	// speed remains clamped to the authored bounds [08 R-CAMP-01 §2].
	b.windSpeed += int32(b.crt.Rand()%5) - 2
	if b.windSpeed < b.minWind {
		b.windSpeed = b.minWind
	}
	if b.windSpeed > b.maxWind {
		b.windSpeed = b.maxWind
	}
	b.countdown = int32(b.crt.Rand() % 63)
}

// Dispatch handles a typed semantic control. Start emits a shared battle
// request; Prev and SHUTUP stop/close presentation; MORE pages authored text.
func (b *campaignBriefingController) Dispatch(action BriefingAction) (BriefingBattleEvent, error) {
	if b == nil || b.state != BriefingOpen {
		return BriefingBattleEvent{}, nil
	}
	switch action {
	case BriefingActionStart:
		if b.request == nil {
			return BriefingBattleEvent{}, fmt.Errorf("nanolathe: briefing start: missing shared battle request")
		}
		req, err := b.request()
		if err != nil {
			return BriefingBattleEvent{}, err
		}
		b.state = BriefingClosed
		b.narrationOn = false
		return BriefingBattleEvent{Request: req, Valid: true, Audio: b.stopAudio()}, nil
	case BriefingActionPrev:
		b.state, b.narrationOn = BriefingClosed, false
		return BriefingBattleEvent{Audio: b.stopAudio()}, nil
	case BriefingActionShutup:
		if b.narrationOn {
			b.narrationOn = false
			return BriefingBattleEvent{Audio: b.stopAudio()}, nil
		}
		b.narrationOn = true
		return BriefingBattleEvent{Audio: b.startAudio()}, nil
	case BriefingActionMore:
		if b.pageCount > 1 {
			b.page++
			if b.page >= b.pageCount {
				b.page = 0
			}
		}
	}
	return BriefingBattleEvent{}, nil
}

func (b *campaignBriefingController) startAudio() []BriefingAudioEffect {
	if b == nil || b.narrationPath == "" {
		return nil
	}
	return []BriefingAudioEffect{{Kind: BriefingAudioStart, Path: b.narrationPath, Delay: 60, Volume: 0}}
}

func (b *campaignBriefingController) stopAudio() []BriefingAudioEffect {
	if b == nil || b.narrationPath == "" {
		return nil
	}
	return []BriefingAudioEffect{{Kind: BriefingAudioStop, Path: b.narrationPath}}
}

func (b *campaignBriefingController) SetPanoramaFrameCount(count int) {
	if b == nil {
		return
	}
	if count < 0 {
		count = 0
	}
	b.panoramaCount = count
}

// SetText installs the authored slot-2 text. The pager keeps the authored bytes
// and computes line pages when the renderer supplies the active region height
// [07 R-HUD-03 §10].
func (b *campaignBriefingController) SetText(text string) {
	if b == nil {
		return
	}
	b.text = text
	b.page = 0
	b.pageCount = 1
}

func (b *campaignBriefingController) SetPageLines(lines int) {
	if b == nil {
		return
	}
	if lines < 1 {
		lines = 1
	}
	b.pageLines = lines
	lineCount := 1 + strings.Count(b.text, "\n")
	b.pageCount = (lineCount + lines - 1) / lines
	if b.page >= b.pageCount {
		b.page = 0
	}
}

func (b *campaignBriefingController) pageText() string {
	if b == nil || b.text == "" {
		return ""
	}
	if b.pageLines < 1 {
		return b.text
	}
	lines := strings.Split(b.text, "\n")
	start := b.page * b.pageLines
	if start >= len(lines) {
		return lines[len(lines)-1]
	}
	end := start + b.pageLines
	if end > len(lines) {
		end = len(lines)
	}
	return strings.Join(lines[start:end], "\n")
}

func (b *campaignBriefingController) State() BriefingState {
	if b == nil {
		return BriefingClosed
	}
	return b.state
}

func (b *campaignBriefingController) Planet() BriefingPlanet {
	if b == nil {
		return BriefingPlanet{}
	}
	return b.planet
}

func (b *campaignBriefingController) PlanetIndex() int {
	if b == nil {
		return 0
	}
	return b.planetIdx
}

func (b *campaignBriefingController) WindSpeed() int32     { return b.windSpeed }
func (b *campaignBriefingController) WindCountdown() int32 { return b.countdown }
func (b *campaignBriefingController) RotationFrame() int   { return b.rotateFrame }
func (b *campaignBriefingController) PanoramaFrame() int   { return b.panoramaFrame }
func (b *campaignBriefingController) Page() int            { return b.page }
func (b *campaignBriefingController) NarrationOn() bool    { return b.narrationOn }
