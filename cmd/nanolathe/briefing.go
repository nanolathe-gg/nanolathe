package main

import (
	"fmt"
	"strings"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/render"
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
	// rotate is the PLANET sequence cursor. The rotator steps it once per
	// 25 ms of wall clock and the cursor holds each frame for the frame's own
	// authored duration, so a 36-frame planet at duration 3 turns once every
	// 2.7 s [08 R-CAMP-01 §2][03 §4.4].
	rotate       render.Cursor
	rotateEntry  *formats.GAFEntry
	rotateNextMS int64
	rotateSteps  int

	narrationPath string
	narrationOn   bool

	// text is the authored slot-2 file; wrapped is that text after the
	// front-end wrapper and the run pre-split; lines are the laid labels of
	// the current page [07 R-FE-02 §6][07 R-HUD-03 §10][07 R-FE-02 §7].
	text      string
	wrapped   string
	page      int
	pageLines int
	pageCount int
	lines     []briefingTextLine
	measure   func(string) int
	// tick is the last scaled-timer sample the screen saw. A blink entry is
	// registered with a deadline one second ahead of it [07 R-FE-02 §7].
	tick int64

	request func() (freshBattleRequest, error)
}

// NewCampaignBriefingController opens one briefing and consumes the two
// entry CRT draws in their authored order [08 R-CAMP-01 §2][01 §7.3].
func NewCampaignBriefingController(m *mission.Mission, localSide int, crt briefingRandom, request func() (freshBattleRequest, error)) *campaignBriefingController {
	b := &campaignBriefingController{mission: m, localSide: localSide, crt: crt, request: request, state: BriefingOpen, rotateNextMS: -1}
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
	// MSNBRIEF opens with SHUTUP at stage 1, even if optional narration
	// media is absent. This is the toggle state, not playback status [07 R-FE-01 §4].
	b.narrationOn = true
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

func (b *campaignBriefingController) entryWind() {
	if b == nil || b.crt == nil {
		return
	}
	// The entry draw is a *signed* remainder over the authored span
	// [01 §7.3] — a truncating divide, so the remainder carries the sign of
	// the CRT draw and is therefore never negative. Go's `%` on int32 is the
	// same operation, which is why the span is not made unsigned here: an
	// authored `maxwindspeed` below `minwindspeed` gives a negative span, and
	// retail then displays a speed at or above `minWind`, not below it. The
	// per-update clamp of changeWind is what pulls the display down to
	// `maxWind` on the first countdown expiry, because its high clamp runs
	// after its low clamp.
	span := b.maxWind - b.minWind + 1
	if span == 0 {
		// `maxwindspeed == minwindspeed - 1` divides by zero and faults the
		// retail process. Nanolathe substitutes the low bound rather than
		// reproducing the fault; nothing in stock content reaches it.
		b.windSpeed = b.minWind
		b.countdown = int32(b.crt.Rand() & 0x3f)
		return
	}
	b.windSpeed = b.minWind + b.crt.Rand()%span
	b.countdown = int32(b.crt.Rand() & 0x3f)
}

// Update advances the presentation draw. nowMS is the presentation wall-time
// sample used by the 25 ms planet rotator gate; tick is the presentation tick
// used for the one-frame-per-tick rotation rule [08 R-CAMP-01 §2].
func (b *campaignBriefingController) Update(nowMS, tick int64) {
	if b == nil || b.state != BriefingOpen {
		return
	}
	b.tick = tick
	b.countdown--
	if b.countdown < 1 {
		b.changeWind()
	}
	// The panorama gadget's frame word is sampled on every draw from the
	// scaled presentation clock, independently of the slower planet-rotation
	// wall gate. It is *not* the strip's tiling origin — the scroller tiles
	// from frame 0 and fetches this frame only to null-test it — so it moves
	// nothing on screen; keeping it is what makes that guard reachable
	// [08 R-CAMP-01 §2].
	if b.panoramaCount > 0 {
		b.panoramaFrame = int((tick / 3) % int64(b.panoramaCount))
	}
	// The planet rotator's whole body sits behind one wall-clock gate: retail
	// draws far more often than 40 Hz, so its single "has the deadline
	// passed" check is, in effect, a catch-up loop already — no draw is ever
	// more than a slice of a millisecond late. Nanolathe's own presentation
	// host is deliberately fixed at 30 Hz [ARCHITECTURE.md "clock, rng,
	// pool"], slower than the 40 Hz this gate must clear, so a single check
	// per call would cap the rotator at the host's own rate instead of the
	// authored one. The loop below is the wall-clock-exact replacement: it
	// consumes every 25 ms boundary nowMS has crossed since the previous
	// sample, however many that is, so the rotation rate is governed by
	// elapsed wall time alone, never by how often this method happens to be
	// called [08 R-CAMP-01 §2][03 §4.4]. The first sample after a bind takes
	// exactly one step regardless of nowMS's value, establishing that
	// instant as the phase origin the subsequent boundaries count from.
	if b.rotateNextMS < 0 {
		b.rotate.Step()
		b.rotateSteps++
		b.rotateNextMS = nowMS + briefingRotationGateMS
	} else {
		for nowMS >= b.rotateNextMS {
			b.rotate.Step()
			b.rotateSteps++
			b.rotateNextMS += briefingRotationGateMS
		}
	}
	b.stepBlinkWords(tick)
	// One pixel of strip scroll per three presentation ticks: the pass steps
	// when the clock is strictly past the deadline and then re-sets the
	// deadline to `now + 2`, so the next step needs `tick >= deadline + 1`.
	// Retail's scroll and deadline are statics nothing resets at screen entry,
	// which leaves the deadline of the *first* draw of a briefing always in
	// the past — so that draw always takes one step [08 R-CAMP-01 §2].
	if !b.scrollStarted {
		b.scroll++
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
		// The pager advances its counter and re-lays the region; a page start
		// the text does not reach wraps back to page 0 [07 R-HUD-03 §10].
		b.page++
		if b.page >= b.pageCount {
			b.page = 0
		}
		b.layPage()
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

// SetRotationSequence binds the PLANET gadget's rotation sequence. The cursor
// starts at frame 0 with that frame's duration loaded, and the entry's own loop
// word decides whether the sequence wraps [08 R-CAMP-01 §2][03 §4.4].
func (b *campaignBriefingController) SetRotationSequence(entry *formats.GAFEntry) {
	if b == nil || b.rotateEntry == entry {
		return
	}
	b.rotateEntry = entry
	b.rotate.Bind(entry, 0, entry != nil && entry.Unknown1 != 0)
	// -1 is never a valid deadline, so it marks the phase as unestablished:
	// Update's next sample, whatever its nowMS, takes the bootstrap step and
	// becomes the new phase origin [08 R-CAMP-01 §2].
	b.rotateNextMS = -1
}

// SetTextRegion installs the authored slot-2 text into the pager. `width` and
// `height` are the TextRegion gadget's authored size, `fontHeight` the height
// of the font its font index selects, and `measure` that font's width metric —
// the wrapper, the lines-per-page divide and the run pen all read the same font
// [07 R-FE-02 §6][07 R-HUD-03 §10].
func (b *campaignBriefingController) SetTextRegion(text string, width, height, fontHeight int, measure func(string) int) {
	if b == nil {
		return
	}
	b.text = text
	b.measure = measure
	b.wrapped = briefingSplitBlinkRuns(retailWordWrap(text, width, measure))
	step := fontHeight + 2
	lines := 1
	if step > 0 {
		lines = height / step
	}
	if lines < 1 {
		lines = 1
	}
	b.pageLines = lines
	// The pager finds page n by scanning for the `n × linesPerPage`-th newline
	// and wraps to page 0 when the text has no such newline, so the page count
	// is one more than the number of whole pages of line ends [07 R-HUD-03 §10].
	b.pageCount = strings.Count(b.wrapped, "\n")/lines + 1
	b.page = 0
	b.layPage()
}

// briefingBlinkWordCap is the fifteen-entry blink table the briefing, help and
// in-battle briefing windows allocate on open [07 R-FE-02 §7].
const briefingBlinkWordCap = 15

// briefingRotationGateMS is the rotator's wall-clock gate: one sequence step
// per 25 ms [08 R-CAMP-01 §2].
const briefingRotationGateMS = 25

// briefingBlinkPhaseB is the second blink colour, palette index 94, shared by
// every run whatever letter opened it [07 R-FE-02 §7].
const briefingBlinkPhaseB = 94

// briefingTextRun is one `&X…&` run on a laid line. The pager copies the run's
// bytes into the line's own label as well, so the run is drawn over the label
// text it duplicates, alternating between the letter's colour and palette
// index 94 [07 R-FE-02 §7].
type briefingTextRun struct {
	Text string
	// X is the pen offset from the line's left edge: the measured width of the
	// label text laid before the run opened.
	X     int
	Entry int
	phase int
	// deadline is the scaled-timer stamp the phase flips at. The registration
	// arithmetic is single precision against an integer timer [07 R-FE-02 §7].
	deadline float32
}

// briefingTextLine is one emitted label of the current page.
type briefingTextLine struct {
	Text string
	Runs []briefingTextRun
}

// Color is the run's colour for the phase it is in: the side text-colour entry
// the opening letter selected, or palette index 94 [07 R-FE-02 §7].
func (r briefingTextRun) Color(side int) byte {
	if r.phase != 0 {
		return briefingBlinkPhaseB
	}
	return briefingSideTextColor(side, r.Entry)
}

// layPage emits the current page's labels. It clears the blink table first:
// turning a page frees every entry the previous page registered
// [07 R-HUD-03 §10][07 R-FE-02 §7].
func (b *campaignBriefingController) layPage() {
	if b == nil {
		return
	}
	b.lines = nil
	if b.wrapped == "" || b.pageLines < 1 {
		return
	}
	lines := strings.Split(b.wrapped, "\n")
	start := b.page * b.pageLines
	if start >= len(lines) {
		b.page, start = 0, 0
	}
	end := start + b.pageLines
	if end > len(lines) {
		end = len(lines)
	}
	measure := b.measure
	if measure == nil {
		measure = func(string) int { return 0 }
	}
	// The open/closed marker state is the pager's, not the line's: it is set
	// once before the page's first label and carried across the page's lines
	// [07 R-HUD-03 §10]. The pre-split has already closed and reopened every
	// run that crossed a line end.
	open := true
	runs := 0
	// An entry is registered in phase A with a deadline one second ahead of the
	// clock the lay ran on [07 R-FE-02 §7].
	deadline := float32(b.tick) + float32(briefingPresentationRate)
	for _, raw := range lines[start:end] {
		line := briefingLayLine(raw, measure, &open, briefingBlinkWordCap-runs)
		for i := range line.Runs {
			line.Runs[i].deadline = deadline
		}
		runs += len(line.Runs)
		b.lines = append(b.lines, line)
	}
}

// stepBlinkWords advances every live run's two-phase blink: phase A for one
// second in the letter's colour, phase B for a quarter second in palette index
// 94 [07 R-FE-02 §7].
func (b *campaignBriefingController) stepBlinkWords(tick int64) {
	if b == nil {
		return
	}
	for i := range b.lines {
		for j := range b.lines[i].Runs {
			run := &b.lines[i].Runs[j]
			if float32(tick) <= run.deadline {
				continue
			}
			run.phase ^= 1
			period := float32(1.0)
			if run.phase != 0 {
				period = 0.25
			}
			run.deadline = float32(tick) + float32(briefingPresentationRate)*period
		}
	}
}

// briefingPresentationRate is the configured presentation frame rate the
// scaled clock is built from [07 R-CAM-01 §1].
const briefingPresentationRate = 30

// briefingRunColorEntry maps the letter after `&` onto the side text-colour
// table: `G` is entry 1, `Y` entry 2, `R` entry 3, and any other letter also
// reads as entry 3 [07 R-HUD-03 §10].
func briefingRunColorEntry(letter byte) int {
	switch letter {
	case 'R':
		return 3
	case 'Y':
		return 2
	case 'G':
		return 1
	default:
		return 3
	}
}

// briefingSideTextColors is the four-entry-per-side text-colour table the
// briefing, help and end-of-mission pagers draw through. The entries are
// physical palette indices, not GUI semantic colours, because a kind-5 label
// installs its colour word raw [03 R-FONT-01 §6][07 R-HUD-03 §10]. Rows past
// Core repeat the Core row in the executable's own table.
var briefingSideTextColors = [2][4]byte{
	{53, 51, 64, 208},
	{117, 86, 82, 212},
}

func briefingSideTextColor(side, entry int) byte {
	if side < 0 || side >= len(briefingSideTextColors) {
		side = 1
	}
	if entry < 0 || entry >= len(briefingSideTextColors[side]) {
		entry = 0
	}
	return briefingSideTextColors[side][entry]
}

// PlainColor is the colour a laid label draws in: text-colour entry 0 of the
// local player's side [07 R-HUD-03 §10].
func (b *campaignBriefingController) PlainColor() byte {
	if b == nil {
		return briefingSideTextColor(0, 0)
	}
	return briefingSideTextColor(b.localSide, 0)
}

// CaptionColor is the `MOREBAR` caption colour: text-colour entry 1
// [07 R-HUD-03 §10].
func (b *campaignBriefingController) CaptionColor() byte {
	if b == nil {
		return briefingSideTextColor(0, 1)
	}
	return briefingSideTextColor(b.localSide, 1)
}

// Lines are the labels of the current page.
func (b *campaignBriefingController) Lines() []briefingTextLine {
	if b == nil {
		return nil
	}
	return b.lines
}

// MoreCaption is the `MOREBAR` caption for the current page: `MORE...` while a
// further page start exists, `BACK TO START` when it does not and the pager is
// past page 0, and empty on a single-page text [07 R-HUD-03 §10].
func (b *campaignBriefingController) MoreCaption() string {
	if b == nil || b.pageLines < 1 {
		return ""
	}
	if strings.Count(b.wrapped, "\n") >= (b.page+1)*b.pageLines {
		return "MORE..."
	}
	if b.page > 0 {
		return "BACK TO START"
	}
	return ""
}

// briefingLayLine emits one label from one wrapped line, stripping the `&X` /
// `&` markers the pager consumes and recording each bracketed run with the pen
// offset the label text laid before it [07 R-HUD-03 §10][07 R-FE-02 §7].
// `open` carries the marker state across the page's lines; `budget` is the
// blink table's remaining capacity.
func briefingLayLine(raw string, measure func(string) int, open *bool, budget int) briefingTextLine {
	var out []byte
	var runs []briefingTextRun
	for i := 0; i < len(raw); {
		if raw[i] == '&' {
			if *open {
				letter := byte(0)
				if i+1 < len(raw) {
					letter = raw[i+1]
				}
				if len(runs) < budget {
					// The run's text is the bytes up to the closing `&`, at
					// most 127; its pen is the label's own x plus the width of
					// the label text laid so far [07 R-FE-02 §7].
					//
					// The opening marker is two bytes — `&` and the colour
					// letter — so a line whose final byte is an unmatched `&`
					// carries neither a letter nor any run text. The start is
					// clamped to the end of the line for that case: the run is
					// registered empty, and the marker state still advances
					// past both bytes, because the pager consumes the marker
					// whether or not a closing one follows [07 R-FE-02 §7].
					// Stock briefs are balanced; third-party or badly wrapped
					// text is the only source of an unterminated run.
					start := min(i+2, len(raw))
					end := start
					for end < len(raw) && raw[end] != '&' && end-start < 127 {
						end++
					}
					runs = append(runs, briefingTextRun{
						Text:  raw[start:end],
						X:     measure(string(out)),
						Entry: briefingRunColorEntry(letter),
					})
				}
				*open = false
				i += 2
			} else {
				*open = true
				i++
			}
			if i >= len(raw) {
				break
			}
		}
		out = append(out, raw[i])
		i++
	}
	return briefingTextLine{Text: string(out), Runs: runs}
}

// retailWordWrap is the front-end wrapper `MSGBOX`, `RESTART`'s mission name
// and the briefing text share [07 R-FE-02 §6]. It copies the input byte by
// byte, stopping at NUL or `0xFF`; after copying a byte whose *successor* is a
// space, a newline or `-` it measures the current line and, when the measured
// width has reached `width`, walks back over the copied output and the input
// together to the nearest earlier space or hyphen, replaces that separator
// with `CR LF` and restarts the line after it. A literal newline in the input
// also restarts the line. The test is `>=`, breaks happen only at a space or a
// hyphen — a word longer than the width is never split — and the separator is
// consumed.
func retailWordWrap(text string, width int, measure func(string) int) string {
	if measure == nil || width <= 0 {
		return text
	}
	src := []byte(text)
	out := make([]byte, 0, len(src)+16)
	lineStart := 0
	for i := 0; i < len(src) && src[i] != 0 && src[i] != 0xFF; {
		out = append(out, src[i])
		j := len(out) - 1
		next := byte(0)
		if i+1 < len(src) {
			next = src[i+1]
		}
		nextSrc, nextOut := i+1, j+1
		if next == ' ' || next == '\n' || next == '-' {
			if lineStart <= j && measure(string(out[lineStart:])) >= width {
				// The walk-back rewinds the output and the input together to
				// the separator the line breaks at. Retail's has no line-start
				// guard: on a word wider than the region it rewinds past an
				// earlier break to that line's separator, then re-copies the
				// same word and rewinds to the same place forever. Stopping at
				// the line start is the termination guard for that hang; its
				// visible consequence is the documented one either way — a word
				// longer than the width is never split.
				back, bi := j, i
				for back > lineStart && src[bi] != ' ' && src[bi] != '-' {
					back--
					bi--
				}
				if src[bi] == ' ' || src[bi] == '-' {
					i, j = bi, back
					out = append(out[:j], '\r', '\n')
					nextSrc, nextOut = i+1, j+2
					lineStart = nextOut
				}
			}
		}
		i = nextSrc
		if i < len(src) && src[i] == '\n' {
			lineStart = nextOut + 1
		}
	}
	return string(out)
}

// briefingSplitBlinkRuns is the pre-pass the pager runs over the wrapped text:
// a `&X…&` run that spans a line end is closed before the newline and reopened
// after it, so each line blinks on its own [07 R-FE-02 §7].
func briefingSplitBlinkRuns(text string) string {
	src := []byte(text)
	out := make([]byte, 0, len(src)+16)
	open := false
	letter := byte(0)
	for i := 0; i < len(src) && src[i] != 0xFF; i++ {
		out = append(out, src[i])
		if src[i] == '&' {
			if i+1 < len(src) {
				letter = src[i+1]
			}
			open = !open
		}
		if src[i] == '\n' && open && len(out) >= 2 {
			// The newline's own slot becomes `CR`, the byte before it the
			// closing `&`, and the reopening `&` plus its letter follow.
			out[len(out)-2] = '&'
			out[len(out)-1] = '\r'
			out = append(out, '\n', '&', letter)
		}
	}
	return string(out)
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
func (b *campaignBriefingController) PanoramaFrame() int   { return b.panoramaFrame }
func (b *campaignBriefingController) Page() int            { return b.page }
func (b *campaignBriefingController) NarrationOn() bool    { return b.narrationOn }

// RotationFrame is the PLANET sequence's current frame index.
func (b *campaignBriefingController) RotationFrame() int {
	if b == nil {
		return 0
	}
	return b.rotate.Idx
}

// RotationSteps counts the rotator passes taken, which is what the 25 ms gate
// bounds; the frame index moves more slowly than this by each frame's own
// duration [08 R-CAMP-01 §2].
func (b *campaignBriefingController) RotationSteps() int {
	if b == nil {
		return 0
	}
	return b.rotateSteps
}
