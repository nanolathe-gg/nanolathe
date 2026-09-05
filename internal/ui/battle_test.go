package ui

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/input"
)

func TestBattleStateModalChainAndReleaseCapture(t *testing.T) {
	s := NewBattleState(0x04)
	if got := s.Modal(); got != BattleModalClosed {
		t.Fatalf("initial modal=%d, want closed", got)
	}
	if intent := s.OpenOptions(); !intent.PauseSet || !intent.Pause || s.Modal() != BattleModalOptions {
		t.Fatalf("open options modal=%d intent=%+v", s.Modal(), intent)
	}
	s.PressModal(4)
	if got, ok := s.ReleaseModal(3); ok || got != -1 {
		t.Fatalf("release outside captured gadget got=%d ok=%t", got, ok)
	}
	s.PressModal(4)
	if got, ok := s.ReleaseModal(4); !ok || got != 4 {
		t.Fatalf("release inside captured gadget got=%d ok=%t", got, ok)
	}
	s.ShowExit()
	s.ShowConfirmation(true)
	if got := s.Activate("CHOICE2"); got != BattleModalActionNone || s.Modal() != BattleModalExit {
		t.Fatalf("choice2 modal=%d action=%d", s.Modal(), got)
	}
	s.ShowConfirmation(false)
	if got := s.Activate("CHOICE1"); got != BattleModalActionExitGame || s.Modal() != BattleModalConfirmExit {
		t.Fatalf("choice1 action=%d modal=%d", got, s.Modal())
	}
	if intent := s.Back(); intent.PauseSet || s.Modal() != BattleModalExit {
		t.Fatalf("back from confirm intent=%+v modal=%d", intent, s.Modal())
	}
	if intent := s.Back(); intent.PauseSet || s.Modal() != BattleModalOptions {
		t.Fatalf("back from exit intent=%+v modal=%d", intent, s.Modal())
	}
	if intent := s.Back(); !intent.PauseSet || intent.Pause || s.Modal() != BattleModalClosed {
		t.Fatalf("back from options intent=%+v modal=%d", intent, s.Modal())
	}
}

func TestBattleStateScheduleIntentValues(t *testing.T) {
	if got := PauseIntent(true); !got.PauseSet || !got.Pause || got.SpeedDelta != 0 {
		t.Fatalf("pause intent=%+v", got)
	}
	if got := SpeedIntent(-1); got.PauseSet || got.SpeedDelta != -1 {
		t.Fatalf("speed intent=%+v", got)
	}
}

func TestBattleStatePauseTruthIsSynchronousAndIgnoresStaleFrames(t *testing.T) {
	s := NewProductionBattleState()
	if s.Paused() {
		t.Fatal("production battle state starts paused")
	}
	s.SyncCommittedPause(true)
	if !s.Paused() {
		t.Fatal("first committed paused frame was not adopted")
	}
	s.SetPauseTruth(false)
	if s.Paused() {
		t.Fatal("scheduling boundary did not record unpause truth")
	}
	// A stale paused tick must not invert the synchronous UI state after a
	// schedule transition, including while zero simulation ticks publish.
	s.SyncCommittedPause(true)
	if s.Paused() {
		t.Fatal("stale committed pause overwrote synchronous unpause truth")
	}
}

func TestBattleStatePanelStartsFromEnteringModeByte(t *testing.T) {
	if production := NewProductionBattleState(); production.PanelOffset != PanelVisible || production.PanelTarget != PanelVisible {
		t.Fatalf("production entry mode starts offset=%d target=%d, want visible", production.PanelOffset, production.PanelTarget)
	}
	tests := []struct {
		mode byte
		want int8
	}{
		{mode: 0x00, want: PanelParked},
		{mode: 0x04, want: PanelVisible},
		{mode: 0xff, want: PanelVisible},
		{mode: 0xfb, want: PanelParked},
	}
	for _, tc := range tests {
		s := NewBattleState(tc.mode)
		if s.PanelOffset != tc.want || s.PanelTarget != tc.want {
			t.Errorf("mode %#02x starts offset=%d target=%d, want %d", tc.mode, s.PanelOffset, s.PanelTarget, tc.want)
		}
	}
}

func TestBattleStateOwnsPanelSlideAndUsesOneOffset(t *testing.T) {
	s := NewBattleState(0x04)
	if s.PanelOffset != PanelVisible || s.PanelTarget != PanelVisible {
		t.Fatalf("initial panel state offset=%d target=%d", s.PanelOffset, s.PanelTarget)
	}
	var cues []string
	s.SetPanelCue(func(name string) { cues = append(cues, name) })
	s.PanelOffset = PanelParked
	s.AdvancePanel(100, false, false)
	if s.PanelTarget != PanelVisible || s.PanelOffset != -21 {
		t.Fatalf("panel advance offset=%d target=%d, want -21/0", s.PanelOffset, s.PanelTarget)
	}
	if len(cues) != 1 || cues[0] != "Panel" {
		t.Fatalf("panel leaving cue=%v, want [Panel]", cues)
	}
	// An early host timestamp is ignored, including the offset itself.
	before := s.PanelOffset
	s.AdvancePanel(105, true, false)
	if s.PanelOffset != before || s.PanelTarget != PanelParked {
		t.Fatalf("early panel step offset=%d target=%d, want unchanged/%d", s.PanelOffset, s.PanelTarget, PanelParked)
	}
}

func TestBattleStatePanelConvergesInBothDirections(t *testing.T) {
	tests := []struct {
		name       string
		mode       byte
		spaceHeld  bool
		wantTarget int8
	}{
		{name: "parked to visible", mode: 0x00, wantTarget: PanelVisible},
		{name: "visible to parked", mode: 0x04, spaceHeld: true, wantTarget: PanelParked},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := NewBattleState(tc.mode)
			start := s.PanelOffset
			previous := s.PanelOffset
			now := uint32(15)
			for i := 0; i < 100 && s.PanelOffset != tc.wantTarget; i++ {
				s.AdvancePanel(now, tc.spaceHeld, false)
				if s.PanelOffset == previous && s.PanelOffset != tc.wantTarget {
					t.Fatalf("stalled at %d before target %d", s.PanelOffset, tc.wantTarget)
				}
				if tc.wantTarget > start && s.PanelOffset < previous {
					t.Fatalf("moved away from visible target: %d -> %d", previous, s.PanelOffset)
				}
				if tc.wantTarget < start && s.PanelOffset > previous {
					t.Fatalf("moved away from parked target: %d -> %d", previous, s.PanelOffset)
				}
				previous = s.PanelOffset
				now += PanelThrottleMs
			}
			if s.PanelOffset != tc.wantTarget {
				t.Fatalf("did not converge: offset=%d target=%d", s.PanelOffset, tc.wantTarget)
			}
		})
	}
}

func TestBattleStatePanelMinimumTailAndThrottle(t *testing.T) {
	s := NewBattleState(0x04)
	s.PanelOffset = -1
	s.PanelTarget = PanelVisible
	s.PanelLastThrottle = 1000
	s.AdvancePanel(1010, false, false)
	if s.PanelOffset != -1 || s.PanelLastThrottle != 1000 {
		t.Fatalf("early panel step changed offset=%d throttle=%d", s.PanelOffset, s.PanelLastThrottle)
	}
	s.AdvancePanel(1015, false, false)
	if s.PanelOffset != PanelVisible || s.PanelLastThrottle != 1015 {
		t.Fatalf("minimum positive tail offset=%d throttle=%d, want 0/1015", s.PanelOffset, s.PanelLastThrottle)
	}

	s.PanelOffset = -30
	s.PanelTarget = PanelParked
	s.PanelLastThrottle = 2000
	s.AdvancePanel(2015, true, false)
	if s.PanelOffset != PanelParked {
		t.Fatalf("minimum negative tail offset=%d, want %d", s.PanelOffset, PanelParked)
	}
}

func TestBattleStatePanelCueSequence(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mode      byte
		spaceHeld bool
	}{
		{name: "parked to visible", mode: 0x00},
		{name: "visible to parked", mode: 0x04, spaceHeld: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var cues []string
			s := NewBattleState(tc.mode)
			s.SetPanelCue(func(cue string) { cues = append(cues, cue) })
			now := uint32(15)
			for i := 0; i < 100 && len(cues) < 2; i++ {
				s.AdvancePanel(now, tc.spaceHeld, false)
				now += PanelThrottleMs
			}
			if len(cues) != 2 || cues[0] != "Panel" || cues[1] != "Options" {
				t.Fatalf("cue sequence=%v, want [Panel Options]", cues)
			}
		})
	}
}

func TestBattleStatePanelSpaceEditorPolarity(t *testing.T) {
	for _, tc := range []struct {
		spaceHeld, editorFocused bool
		want                     int8
	}{
		{want: PanelVisible},
		{editorFocused: true, want: PanelVisible},
		{spaceHeld: true, want: PanelParked},
		{spaceHeld: true, editorFocused: true, want: PanelVisible},
	} {
		s := NewBattleState(0x04)
		s.SetPanelTarget(tc.spaceHeld, tc.editorFocused)
		if s.PanelTarget != tc.want {
			t.Errorf("held=%t editor=%t target=%d, want %d", tc.spaceHeld, tc.editorFocused, s.PanelTarget, tc.want)
		}
	}
}

func TestBattleStateOwnsInputAndPlacementState(t *testing.T) {
	s := NewBattleState(0x04)
	if s.Input.Latch != input.LatchNormal || s.Input.BuildDef != "" {
		t.Fatalf("initial input state=%+v", s.Input)
	}
	s.ArmPlacement("armmex", 2, 3)
	if s.Input.Latch != input.LatchMobileBuild || s.Input.BuildDef != "armmex" || s.Input.BuildFootX != 2 || s.Input.BuildFootZ != 3 {
		t.Fatalf("armed placement=%+v", s.Input)
	}
	s.Input.BuildOK = true
	s.Input.BuildCellX, s.Input.BuildCellZ, s.Input.BuildSiteH = 4, 5, 6
	s.ClearPlacement()
	if s.Input.Latch != input.LatchNormal || s.Input.BuildDef != "" || s.Input.BuildOK || s.Input.BuildCellX != 0 || s.Input.BuildSiteH != 0 {
		t.Fatalf("cleared placement=%+v", s.Input)
	}
	s.Input.DragActive = true
	s.Input.HUDCaptured = true
	s.Input.PlaceCaptured = true
	s.Input.ShiftLatchSticky = true
	s.Input.PointerX, s.Input.PointerY = 10, 20
	s.Input.ShiftHeld = true
	s.Input.StatusMessage, s.Input.StatusUntil = "paused", 90
	s.Input.ResultDismissed = true
	s.ResetInteraction()
	if s.Input.DragActive || s.Input.HUDCaptured || s.Input.PlaceCaptured || s.Input.ShiftLatchSticky || s.Input.PointerX != 0 || s.Input.PointerY != 0 || s.Input.ShiftHeld || s.Input.StatusMessage != "" || s.Input.StatusUntil != 0 || s.Input.ResultDismissed || s.Input.Latch != input.LatchNormal {
		t.Fatalf("reset interaction=%+v", s.Input)
	}
}

// TestBattleStatePanelEaseSequenceTruncatesTowardZero locks the exact step
// sequence in both directions: "each accepted step eases by remaining-
// distance/3 with a minimum step of one pixel in both directions so it always
// converges; detents are -31 ... and 0" [07 §6 "Panel slide"]. The division is
// an integer divide, so it truncates toward zero, not floors [I3]: from -31 the
// remaining 31 gives 10, and from -1 the remaining 1 gives 0 and the one-pixel
// minimum finishes the run. A floor would step -11 out of the -31 detent and
// change every value below.
func TestBattleStatePanelEaseSequenceTruncatesTowardZero(t *testing.T) {
	for _, tc := range []struct {
		name      string
		start     int8
		spaceHeld bool
		want      []int8
	}{
		{
			name:  "toward the zero detent",
			start: PanelParked,
			want:  []int8{-21, -14, -10, -7, -5, -4, -3, -2, -1, 0},
		},
		{
			name:      "toward the -31 detent",
			start:     PanelVisible,
			spaceHeld: true,
			want:      []int8{-10, -17, -21, -24, -26, -27, -28, -29, -30, -31},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewBattleState(0x04)
			s.PanelOffset = tc.start
			now := uint32(1000)
			for i, want := range tc.want {
				now += PanelThrottleMs
				s.AdvancePanel(now, tc.spaceHeld, false)
				if s.PanelOffset != want {
					t.Fatalf("step %d offset=%d, want %d (sequence so far %v)", i+1, s.PanelOffset, want, tc.want[:i+1])
				}
			}
			// The detent is terminal: further accepted steps do not overshoot.
			now += PanelThrottleMs
			s.AdvancePanel(now, tc.spaceHeld, false)
			if s.PanelOffset != tc.want[len(tc.want)-1] {
				t.Fatalf("offset left its detent: %d", s.PanelOffset)
			}
		})
	}
}

// TestAdvancePanelNowStepsOnceFromTheHostClock covers the wall-clock entry
// point itself. It is presentation, so it may read the host clock [I6]; one
// call from a cold throttle is always accepted and takes exactly the
// remaining/3 step, and it applies the Space/editor polarity of [07 §6]
// through the same SetPanelTarget the explicit-timestamp form uses.
func TestAdvancePanelNowStepsOnceFromTheHostClock(t *testing.T) {
	s := NewBattleState(0x04)
	s.PanelOffset = PanelParked
	s.AdvancePanelNow(false, false)
	if s.PanelTarget != PanelVisible {
		t.Fatalf("released Space target=%d, want %d", s.PanelTarget, PanelVisible)
	}
	if s.PanelOffset != -21 {
		t.Fatalf("first host-clock step offset=%d, want -21", s.PanelOffset)
	}
	if s.PanelLastThrottle == 0 {
		t.Fatal("host-clock step did not stamp the throttle")
	}

	// Space held with no text editor focused reverses the target; a text editor
	// with the focus takes Space for itself and the slide returns to 0.
	s.AdvancePanelNow(true, false)
	if s.PanelTarget != PanelParked {
		t.Fatalf("held Space target=%d, want %d", s.PanelTarget, PanelParked)
	}
	s.AdvancePanelNow(true, true)
	if s.PanelTarget != PanelVisible {
		t.Fatalf("held Space with an editor focused target=%d, want %d", s.PanelTarget, PanelVisible)
	}
}
