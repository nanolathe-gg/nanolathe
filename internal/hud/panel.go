package hud

import (
	"fmt"
	"time"
)

// Panel slide contracts [07 §6] C13, C14, [GAP T22].
//
// I6 note: the 15 ms wall-clock throttle is PRESENTATION-side timing — retail
// does this too [07 §6]. It is kept out of sim packages; this is hud/, fine.
// time.Now is used here legitimately via StepNow, but it never feeds sim state
// per I6 (sim never reads wall-clock). The deterministic sim path uses only the
// explicit now uint32 argument to Step.

const (
	// PanelParked is the parked detent −31 [07 §6] C13.
	PanelParked int8 = -31
	// PanelVisible is the fully visible detent 0 [07 §6] C13.
	PanelVisible int8 = 0
	// PanelThrottleMs is the wall-clock throttle between accepted steps [07 §6] C13.
	// A step whose timestamp is early (<15 ms since LastThrottle) is SKIPPED [07 §6].
	PanelThrottleMs uint32 = 15
	// PanelBackupW/H are the cleared backup scratch strip dimensions [07 §6] C13.
	PanelBackupW = 300
	PanelBackupH = 480
	// ModeVisibleMask is the session mode byte bit that makes the panel start
	// visible [07 §6] C13.
	ModeVisibleMask byte = 0x04
)

// Cue names dispatched to the audio sink [07 §6] C13.
const (
	CuePanel   = "Panel"
	CueOptions = "Options"
)

// CueSink receives panel slide cues [07 §6] C13. The panel dispatches cue names
// to this sink; no audio implementation lives in hud [PLAN_12].
type CueSink interface {
	PlayCue(name string)
}

// Panel owns the signed pixel offset for the side-rail slide [07 §6] C13, C14.
// Offset is −31 (parked) .. 0 (visible). LastThrottle is the wall-clock ms of
// the last accepted step [07 §6]. Target is the current slide target derived
// from Space polarity [07 §6] C14.
// FlipW/H are the negotiated video-mode dimensions of the flip surface allocated
// on entering battle with the static PANEL backdrop blitted in [07 §6] C13.
// BackupW/H and Backup hold the cleared 300×480 scratch strip; BackupHeader
// saves its strip header words before clearing [07 §6] C13.
type Panel struct {
	Offset       int8   // −31..0 [07 §6] C13
	LastThrottle uint32 // GetTickCount-like wall-clock ms of last accepted step [07 §6] C13; PRESENTATION only per I6

	Target int8 // current slide target −31 or 0 [07 §6] C14

	FlipW        int    // negotiated video-mode width [07 §6] C13
	FlipH        int    // negotiated video-mode height [07 §6] C13
	FlipBackdrop string // static PANEL backdrop blitted into flip surface [07 §6] C13

	BackupW      int       // 300 [07 §6] C13
	BackupH      int       // 480 [07 §6] C13
	BackupHeader [2]uint16 // saved strip header words before clearing [07 §6] C13
	Backup       []byte    // cleared 300*480 strip pixels [07 §6] C13

	sink CueSink
}

// NewPanel allocates a panel on entering battle [07 §6] C13.
//
// It allocates the flip surface at the NEGOTIATED video-mode dimensions with the
// static PANEL backdrop blitted in, plus a cleared 300×480 backup scratch strip
// whose strip header words are saved. The command panel starts visible when the
// session mode byte has bit 0x04 set [07 §6] C13.
//
// negotiatedW/H are the negotiated display dimensions (logical vs negotiated is
// preserved because C13 depends on it [PLAN_12 Divergences]). Zero falls back to
// the logical design space 640×480 [07 §1][02 §1] so callers without a
// negotiation still get a valid surface. Backup is zero-filled and header words
// are saved as the strip dimensions. The initial Target equals Offset.
func NewPanel(modeByte byte, negotiatedW, negotiatedH int, sink CueSink) *Panel {
	if negotiatedW <= 0 {
		negotiatedW = 640 // logical design space [07 §1][02 §1] fallback
	}
	if negotiatedH <= 0 {
		negotiatedH = 480
	}
	p := &Panel{
		FlipW:        negotiatedW,
		FlipH:        negotiatedH,
		FlipBackdrop: "PANEL", // static PANEL backdrop blitted in [07 §6] C13
		BackupW:      PanelBackupW,
		BackupH:      PanelBackupH,
		BackupHeader: [2]uint16{uint16(PanelBackupW), uint16(PanelBackupH)}, // saved header words [07 §6] C13
		Backup:       make([]byte, PanelBackupW*PanelBackupH),               // cleared [07 §6] C13
		sink:         sink,
	}
	if modeByte&ModeVisibleMask != 0 {
		p.Offset = PanelVisible // start visible when bit 0x04 set [07 §6] C13
	} else {
		p.Offset = PanelParked // parked otherwise [07 §6] C13
	}
	p.Target = p.Offset
	// LastThrottle starts at 0 so the first accepted Step must be >=15 ms later.
	// Tests drive this explicitly via Step(now).
	return p
}

// SetSink replaces the cue sink (tests use a recording sink).
func (p *Panel) SetSink(sink CueSink) {
	if p == nil {
		return
	}
	p.sink = sink
}

// IsVisible reports whether the panel is fully visible (Offset==0).
func (p *Panel) IsVisible() bool {
	if p == nil {
		return false
	}
	return p.Offset == PanelVisible
}

// IsParked reports whether the panel is fully parked (Offset==-31).
func (p *Panel) IsParked() bool {
	if p == nil {
		return false
	}
	return p.Offset == PanelParked
}

// ShouldBlitStrip reports whether the moving strip must be blitted.
// Retail blits the strip whenever the offset is nonzero at y+offset [07 §6] C14.
func (p *Panel) ShouldBlitStrip() bool {
	if p == nil {
		return false
	}
	return p.Offset != 0 // nonzero offset blits moving strip [07 §6] C14
}

// StripY returns the y coordinate at which to blit the moving strip given a
// base y [07 §6] C14. When ShouldBlitStrip is false the strip is not blitted;
// the returned value is still baseY+offset for the client's convenience.
func (p *Panel) StripY(baseY int) int {
	if p == nil {
		return baseY
	}
	return baseY + int(p.Offset) // y+offset [07 §6] C14
}

// TargetFor computes the slide target from Space polarity [07 §6] C14.
//
// Space HELD slides toward −31 UNLESS a latched gadget of authored record type
// 3 (text-editor family [07 §4] runtime family 3) holds focus ⇒ toward 0.
// Space released always toward 0 [07 §6] C14.
func TargetFor(spaceHeld, editorFocused bool) int8 {
	if spaceHeld {
		if editorFocused {
			return PanelVisible // editor holds focus overrides peek toward −31 [07 §6] C14
		}
		return PanelParked // held, no editor ⇒ toward −31 [07 §6] C14
	}
	return PanelVisible // released always toward 0 [07 §6] C14
}

// IsTextEditorKind reports whether an authored gadget record type equals 3,
// the text-editor family [07 §4][07 §6] C14. Retail's Space polarity gate tests
// exactly this authored type byte, not the runtime family dispatch, but the
// value coincides with runtime family 3 [07 §4].
func IsTextEditorKind(kind byte) bool { return kind == 3 } // [07 §4] type 3

// SetTarget updates Target from Space polarity [07 §6] C14.
func (p *Panel) SetTarget(spaceHeld, editorFocused bool) {
	if p == nil {
		return
	}
	p.Target = TargetFor(spaceHeld, editorFocused)
}

// Step advances the signed pixel offset on the 15 ms wall-clock throttle [07 §6] C13.
// An early-timestamp step (now - LastThrottle < 15) is SKIPPED and LastThrottle
// is not updated [07 §6] C13. Each accepted step eases by remaining/3 with a
// MINIMUM of one pixel in both directions so it always converges [07 §6] C13.
// Detents are −31 and 0 [07 §6] C13. Cues: leaving −31 upward and leaving 0
// downward play Panel; reaching 0 and reaching −31 play Options [07 §6] C13.
// Cues are dispatched to the sink if non-nil.
//
// This is the deterministic core used by tests. For presentation wall-clock use
// StepNow (I6, time.Now, never feeds sim).
func (p *Panel) Step(now uint32) {
	if p == nil {
		return
	}
	// 15 ms wall-clock throttle: early timestamp steps are SKIPPED [07 §6] C13.
	// Use unsigned subtraction to handle GetTickCount wraparound [01 §4.1].
	if now-p.LastThrottle < PanelThrottleMs {
		return
	}
	p.LastThrottle = now

	if p.Offset == p.Target {
		return
	}
	// Cue on leaving a detent: leaving −31 upward and leaving 0 downward play Panel [07 §6] C13.
	if p.Offset == PanelParked && p.Target == PanelVisible {
		if p.sink != nil {
			p.sink.PlayCue(CuePanel)
		}
	} else if p.Offset == PanelVisible && p.Target == PanelParked {
		if p.sink != nil {
			p.sink.PlayCue(CuePanel)
		}
	}

	remaining := int(p.Target) - int(p.Offset) // signed remaining distance [07 §6] C13
	// Easing: remaining/3 with Truncate toward zero is Go's integer division [01 §8].
	step := remaining / 3 // [07 §6] C13
	if step == 0 {
		if remaining > 0 {
			step = 1 // minimum one pixel [07 §6] C13, always converges
		} else if remaining < 0 {
			step = -1 // minimum one pixel in opposite direction [07 §6] C13
		}
	}
	newOffset := int(p.Offset) + step
	// Clamp to target to avoid overshoot (easing guarantees no overshoot except
	// the minimum-one-pixel tail which lands exactly).
	if remaining > 0 && newOffset > int(p.Target) {
		newOffset = int(p.Target)
	} else if remaining < 0 && newOffset < int(p.Target) {
		newOffset = int(p.Target)
	}
	prev := p.Offset
	p.Offset = int8(newOffset)

	// Cue on reaching a detent: reaching 0 and reaching −31 play Options [07 §6] C13.
	if p.Offset != prev && (p.Offset == PanelVisible || p.Offset == PanelParked) {
		if p.sink != nil {
			p.sink.PlayCue(CueOptions)
		}
	}
}

// Advance sets the target from Space polarity and then steps with the given
// wall-clock timestamp [07 §6] C13, C14. It is the one-call presentation helper
// for the battle input tick.
func (p *Panel) Advance(now uint32, spaceHeld, editorFocused bool) {
	if p == nil {
		return
	}
	p.SetTarget(spaceHeld, editorFocused)
	p.Step(now)
}

// StepNow is the PRESENTATION-side wall-clock entry point [07 §6] I6.
// It uses time.Now (legitimate in hud/ per I6 note) and never feeds sim state.
// Retail does this too [07 §6]. It sets the target from Space polarity and then
// steps using the current wall-clock ms (GetTickCount-like). For deterministic
// tests use Step/Aggregate with explicit now.
func (p *Panel) StepNow(spaceHeld, editorFocused bool) {
	if p == nil {
		return
	}
	// PRESENTATION timing only per I6 — never write sim state.
	now := uint32(time.Now().UnixMilli() & 0xffffffff)
	p.Advance(now, spaceHeld, editorFocused)
}

// AdvanceNow is like Advance but using time.Now for the timestamp [07 §6] I6.
func (p *Panel) AdvanceNow(spaceHeld, editorFocused bool) {
	p.StepNow(spaceHeld, editorFocused)
}

// Overlay holds the data drawn onto the moving strip when the offset is nonzero
// [07 §6] C14. Drawing itself is the client's responsibility; the panel exposes
// the overlay data so the client can blit at y+offset [07 §6] C14.
type Overlay struct {
	// ShowStrip indicates the moving strip should be blitted (offset !=0) [07 §6] C14.
	ShowStrip bool
	// Y is baseY + offset when ShowStrip, otherwise baseY (client may ignore).
	Y int
	// Offset is the current signed pixel offset −31..0 [07 §6] C13.
	Offset int8
	// GameTime is "hh:mm:ss" from game ticks at 30 Hz [07 §6] C14.
	GameTime string
	// TotalUnits and MaxUnits are the unit counts for "Total Units: %d (Max %d)" [07 §6] C14.
	TotalUnits int
	MaxUnits   int
	// TotalUnitsText is the formatted "Total Units: %d (Max %d)" string.
	TotalUnitsText string
	// GameSpeedActive is the active speed 1..20 [01 §4.2][07 §6] C14.
	GameSpeedActive int
	// GameSpeedRequested is the requested speed 1..20 [01 §4.2].
	GameSpeedRequested int
	// GameSpeedLabel is the localized normal-speed word at value 10 or a generic
	// label for other speeds [07 §6] C14.
	GameSpeedLabel string
	// GameSpeedSuffix is " (+n)" or " (-n)" when requested != active, else "" [07 §6] C14.
	GameSpeedSuffix string
	// GameSpeedText is the formatted "Game Speed: %s%s" string [07 §6] C14.
	GameSpeedText string
}

// FormatGameTime formats game ticks (30 Hz [01 §4.1]) as "hh:mm:ss" for the
// "Game Time: hh:mm:ss" overlay [07 §6] C14.
func FormatGameTime(ticks int) string {
	if ticks < 0 {
		ticks = 0
	}
	secs := ticks / 30 // 30 ticks per second [01 §4.1]
	hh := secs / 3600
	mm := (secs % 3600) / 60
	ss := secs % 60
	return fmt.Sprintf("%02d:%02d:%02d", hh, mm, ss)
}

// GameSpeedLabel returns the display label for the active game speed [07 §6] C14.
// The localized normal-speed word is used at value 10 [07 §6] C14; retail's
// translation path is not reproduced here, so "Normal" is used as the fallback.
// Other values use a generic "Speed N" form.
func GameSpeedLabel(active int) string {
	if active == 10 {
		return "Normal" // localized normal-speed word at value 10 [07 §6] C14
	}
	return fmt.Sprintf("Speed %d", active)
}

// GameSpeedSuffix returns the "(+/-n)" suffix when requested differs from active [07 §6] C14.
func GameSpeedSuffix(requested, active int) string {
	if requested == active {
		return ""
	}
	diff := requested - active
	return fmt.Sprintf(" (%+d)", diff)
}

// Overlay returns the overlay data to be drawn onto the moving strip when the
// offset is nonzero [07 §6] C14. baseY is the panel's base y coordinate; the
// strip is blitted at y+offset [07 §6] C14. gameTicks is the global tick for
// Game Time hh:mm:ss (30 Hz [01 §4.1]). totalUnits/maxUnits feed the
// "Total Units: %d (Max %d)" string [07 §6] C14. requestedSpeed/activeSpeed
// feed the "Game Speed: %s%s" string with suffix when they differ and the
// normal-speed word at 10 [07 §6] C14.
//
// The client draws the strings; the panel only exposes them.
func (p *Panel) Overlay(baseY int, gameTicks int, totalUnits, maxUnits int, requestedSpeed, activeSpeed int) Overlay {
	if p == nil {
		return Overlay{}
	}
	show := p.Offset != 0 // nonzero offset blits moving strip [07 §6] C14
	y := baseY + int(p.Offset)
	gt := FormatGameTime(gameTicks)
	tut := fmt.Sprintf("Total Units: %d (Max %d)", totalUnits, maxUnits)
	label := GameSpeedLabel(activeSpeed)
	suffix := GameSpeedSuffix(requestedSpeed, activeSpeed)
	gst := fmt.Sprintf("Game Speed: %s%s", label, suffix)
	return Overlay{
		ShowStrip:          show,
		Y:                  y,
		Offset:             p.Offset,
		GameTime:           gt,
		TotalUnits:         totalUnits,
		MaxUnits:           maxUnits,
		TotalUnitsText:     tut,
		GameSpeedActive:    activeSpeed,
		GameSpeedRequested: requestedSpeed,
		GameSpeedLabel:     label,
		GameSpeedSuffix:    suffix,
		GameSpeedText:      gst,
	}
}
