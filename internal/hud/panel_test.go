package hud

import (
	"testing"
)

// mockSink records cue names for assertions [07 §6] C13.
type mockSink struct {
	cues []string
}

func (m *mockSink) PlayCue(name string) { m.cues = append(m.cues, name) }

func TestPanelInitialVisibilityModeByte(t *testing.T) {
	// Mode byte bit 0x04 set => visible [07 §6] C13.
	pVisible := NewPanel(0x04, 640, 480, nil)
	if pVisible.Offset != PanelVisible {
		t.Fatalf("mode 0x04: Offset=%d want %d visible", pVisible.Offset, PanelVisible)
	}
	if pVisible.Target != PanelVisible {
		t.Fatalf("mode 0x04: Target=%d want visible", pVisible.Target)
	}
	// Bit clear => parked.
	pParked := NewPanel(0x00, 640, 480, nil)
	if pParked.Offset != PanelParked {
		t.Fatalf("mode 0x00: Offset=%d want %d parked", pParked.Offset, PanelParked)
	}
	if pParked.Target != PanelParked {
		t.Fatalf("mode 0x00: Target=%d want parked", pParked.Target)
	}
	// Bit set among other bits still visible.
	pMixed := NewPanel(0xFF, 800, 600, nil)
	if pMixed.Offset != PanelVisible {
		t.Fatalf("mode 0xFF: Offset=%d want visible", pMixed.Offset)
	}
	pMixed2 := NewPanel(0x07, 640, 480, nil) // 0x04 set
	if pMixed2.Offset != PanelVisible {
		t.Fatalf("mode 0x07: Offset=%d want visible", pMixed2.Offset)
	}
	pMixed3 := NewPanel(0xFB, 640, 480, nil) // 0x04 clear?
	// 0xFB = 11111011, bit 0x04 is 0? Actually 0x04 = 00000100, 0xFB has it clear? 0xFB = 11111011 includes bit2=0? Let's check: 0xFB &0x04 = 0, so parked.
	if pMixed3.Offset != PanelParked {
		t.Fatalf("mode 0xFB: Offset=%d want parked (bit clear)", pMixed3.Offset)
	}
}

func TestPanelFlipAndBackupAllocation(t *testing.T) {
	// Entering battle allocates flip surface at NEGOTIATED dimensions with PANEL backdrop [07 §6] C13.
	p := NewPanel(0x04, 1024, 768, nil)
	if p.FlipW != 1024 || p.FlipH != 768 {
		t.Fatalf("flip surface: got %dx%d want 1024x768 negotiated [07 §6] C13", p.FlipW, p.FlipH)
	}
	if p.FlipBackdrop != "PANEL" {
		t.Fatalf("flip backdrop = %q want PANEL [07 §6] C13", p.FlipBackdrop)
	}
	if p.BackupW != PanelBackupW || p.BackupH != PanelBackupH {
		t.Fatalf("backup strip: got %dx%d want %dx%d [07 §6] C13", p.BackupW, p.BackupH, PanelBackupW, PanelBackupH)
	}
	if len(p.Backup) != PanelBackupW*PanelBackupH {
		t.Fatalf("backup pixels len %d want %d cleared 300*480 [07 §6] C13", len(p.Backup), PanelBackupW*PanelBackupH)
	}
	for i, b := range p.Backup {
		if b != 0 {
			t.Fatalf("backup strip not cleared at %d: %d [07 §6] C13", i, b)
		}
	}
	if p.BackupHeader[0] != uint16(PanelBackupW) || p.BackupHeader[1] != uint16(PanelBackupH) {
		t.Fatalf("backup header words = %v want [300 480] saved [07 §6] C13", p.BackupHeader)
	}
	// Negotiated fallback to 640x480 when zero [07 §1].
	p2 := NewPanel(0x00, 0, 0, nil)
	if p2.FlipW != 640 || p2.FlipH != 480 {
		t.Fatalf("fallback flip surface: got %dx%d want 640x480", p2.FlipW, p2.FlipH)
	}
}

func TestPanelThrottleEarlySkip(t *testing.T) {
	sink := &mockSink{}
	p := NewPanel(0x00, 640, 480, sink) // parked -31
	p.Target = PanelVisible             // want to slide to 0
	p.LastThrottle = 1000

	// Early timestamp: now - LastThrottle = 10 <15 => SKIPPED [07 §6] C13.
	p.Step(1010)
	if p.Offset != PanelParked {
		t.Fatalf("early timestamp step should be skipped: Offset=%d want %d", p.Offset, PanelParked)
	}
	if p.LastThrottle != 1000 {
		t.Fatalf("early step should not update LastThrottle: got %d want 1000", p.LastThrottle)
	}
	if len(sink.cues) != 0 {
		t.Fatalf("early step should emit no cues: got %v", sink.cues)
	}

	// Accepted after 15 ms: diff 15 -> accepted [07 §6] C13.
	p.Step(1015)
	if p.Offset == PanelParked {
		t.Fatalf("accepted step should have moved from parked; still %d", p.Offset)
	}
	if p.LastThrottle != 1015 {
		t.Fatalf("accepted step LastThrottle=%d want 1015", p.LastThrottle)
	}
	if len(sink.cues) == 0 {
		t.Fatalf("accepted leaving step should emit Panel cue")
	}
	if sink.cues[0] != CuePanel {
		t.Fatalf("first cue = %q want %q (leaving -31 upward)", sink.cues[0], CuePanel)
	}

	// Next early: 5 ms later skipped.
	sink2 := &mockSink{}
	p2 := NewPanel(0x04, 640, 480, sink2) // visible 0, target parked
	p2.Target = PanelParked
	p2.LastThrottle = 2000
	p2.Step(2005) // early 5
	if p2.Offset != PanelVisible {
		t.Fatalf("early skip opposite direction: Offset=%d want 0", p2.Offset)
	}
	if p2.LastThrottle != 2000 {
		t.Fatalf("early LastThrottle mutated")
	}
	p2.Step(2015) // accepted
	if p2.Offset == PanelVisible {
		t.Fatalf("accepted step opposite direction should move")
	}
	if sink2.cues[0] != CuePanel {
		t.Fatalf("leaving 0 downward should play Panel: got %q", sink2.cues[0])
	}
}

func TestPanelEasingConvergence(t *testing.T) {
	// Verify remaining/3 with minimum one pixel in both directions always converges [07 §6] C13.
	testEasing := func(start, target int8, name string) {
		sink := &mockSink{}
		p := &Panel{Offset: start, Target: target, LastThrottle: 0, sink: sink}
		// Use increasing timestamps >=15 apart to avoid throttle.
		var now uint32 = 15
		seen := []int8{start}
		for i := 0; i < 100; i++ {
			prev := p.Offset
			p.Step(now)
			now += 15
			if p.Offset == prev && p.Offset != target {
				t.Fatalf("%s: stalled at %d target %d after %d steps", name, p.Offset, target, i)
			}
			// Verify easing step = remaining/3 trunc toward zero with min 1.
			remainingBefore := int(target) - int(prev)
			if prev != target {
				expectedStep := remainingBefore / 3
				if expectedStep == 0 {
					if remainingBefore > 0 {
						expectedStep = 1
					} else {
						expectedStep = -1
					}
				}
				expectedOffset := int(prev) + expectedStep
				if remainingBefore > 0 && expectedOffset > int(target) {
					expectedOffset = int(target)
				}
				if remainingBefore < 0 && expectedOffset < int(target) {
					expectedOffset = int(target)
				}
				if int(p.Offset) != expectedOffset {
					t.Fatalf("%s step %d: remaining %d, expected step %d -> offset %d, got %d", name, i, remainingBefore, expectedStep, expectedOffset, p.Offset)
				}
			}
			seen = append(seen, p.Offset)
			if p.Offset == target {
				break
			}
		}
		if p.Offset != target {
			t.Fatalf("%s: did not converge from %d to %d, last %d seen %v", name, start, target, p.Offset, seen)
		}
		// Ensure monotonic.
		for i := 1; i < len(seen); i++ {
			if target > start && seen[i] < seen[i-1] {
				t.Fatalf("%s: non-monotonic up: %v", name, seen)
			}
			if target < start && seen[i] > seen[i-1] {
				t.Fatalf("%s: non-monotonic down: %v", name, seen)
			}
		}
		// Verify minimum-one-pixel tail: last step must be 1 pixel.
		if len(seen) >= 2 {
			lastStep := int(seen[len(seen)-1]) - int(seen[len(seen)-2])
			if lastStep != 1 && lastStep != -1 {
				t.Fatalf("%s: last step not min 1px: %d", name, lastStep)
			}
		}
	}

	testEasing(PanelParked, PanelVisible, "parked->visible")
	testEasing(PanelVisible, PanelParked, "visible->parked")

	// Also test intermediate convergence and that both directions always converge regardless of start.
	for _, s := range []int8{-31, -20, -15, -10, -5, -1} {
		for _, tg := range []int8{-31, 0} {
			if s == tg {
				continue
			}
			name := "mid"
			sink := &mockSink{}
			p := &Panel{Offset: s, Target: tg, LastThrottle: 0, sink: sink}
			var now uint32 = 15
			for i := 0; i < 100 && p.Offset != tg; i++ {
				p.Step(now)
				now += 15
			}
			if p.Offset != tg {
				t.Fatalf("%s %d->%d not converged", name, s, tg)
			}
		}
	}
}

func TestPanelEasingMinimumOnePixelTail(t *testing.T) {
	// Explicit tail: remaining 1 or -1 should still move 1 pixel [07 §6] C13.
	p := &Panel{Offset: -1, Target: 0, LastThrottle: 0}
	p.Step(15)
	if p.Offset != 0 {
		t.Fatalf("remaining 1 should step 1 to 0, got %d", p.Offset)
	}
	p2 := &Panel{Offset: -30, Target: -31, LastThrottle: 0}
	p2.Step(15)
	if p2.Offset != -31 {
		t.Fatalf("remaining -1 should step -1 to -31, got %d", p2.Offset)
	}
	p3 := &Panel{Offset: -2, Target: 0, LastThrottle: 0}
	p3.Step(15) // remaining 2 => 2/3=0 => min 1 => -1
	if p3.Offset != -1 {
		t.Fatalf("remaining 2 min-1: got %d want -1", p3.Offset)
	}
	p3.Step(30)
	if p3.Offset != 0 {
		t.Fatalf("remaining 1 min-1 to 0: got %d", p3.Offset)
	}
}

func TestPanelCueEmissionSequence(t *testing.T) {
	// Cues: leaving −31 upward and leaving 0 downward play Panel; reaching 0 and reaching −31 play Options [07 §6] C13.
	// Verify sequence for both directions: Panel on leave, Options on arrive.

	// Parked -> Visible
	sink := &mockSink{}
	p := NewPanel(0x00, 640, 480, sink) // parked
	p.Target = PanelVisible
	var now uint32 = 15
	// First accepted step should emit Panel (leaving -31 upward).
	p.Step(now)
	if len(sink.cues) < 1 || sink.cues[0] != CuePanel {
		t.Fatalf("parked->visible first cue = %v want [%q] leaving -31 upward", sink.cues, CuePanel)
	}
	// Continue to convergence; last cue should be Options (reaching 0).
	for i := 0; i < 50 && p.Offset != PanelVisible; i++ {
		now += 15
		p.Step(now)
	}
	if p.Offset != PanelVisible {
		t.Fatalf("did not reach visible")
	}
	if len(sink.cues) == 0 || sink.cues[len(sink.cues)-1] != CueOptions {
		t.Fatalf("parked->visible last cue = %v want ending %q (reaching 0)", sink.cues, CueOptions)
	}
	if len(sink.cues) != 2 {
		t.Fatalf("parked->visible cues = %v want exactly 2 [Panel, Options], got %d", sink.cues, len(sink.cues))
	}
	if sink.cues[0] != CuePanel || sink.cues[1] != CueOptions {
		t.Fatalf("parked->visible cue order = %v want [Panel Options]", sink.cues)
	}

	// Visible -> Parked
	sink2 := &mockSink{}
	p2 := NewPanel(0x04, 640, 480, sink2) // visible
	p2.Target = PanelParked
	now = 15
	p2.Step(now)
	if len(sink2.cues) < 1 || sink2.cues[0] != CuePanel {
		t.Fatalf("visible->parked first cue = %v want [%q] leaving 0 downward", sink2.cues, CuePanel)
	}
	for i := 0; i < 50 && p2.Offset != PanelParked; i++ {
		now += 15
		p2.Step(now)
	}
	if p2.Offset != PanelParked {
		t.Fatalf("did not reach parked")
	}
	if len(sink2.cues) != 2 {
		t.Fatalf("visible->parked cues = %v want exactly 2, got %d", sink2.cues, len(sink2.cues))
	}
	if sink2.cues[0] != CuePanel || sink2.cues[1] != CueOptions {
		t.Fatalf("visible->parked cue order = %v want [Panel Options]", sink2.cues)
	}

	// Parked -> Visible but interrupted midway should not double-emit Panel.
	sink3 := &mockSink{}
	p3 := NewPanel(0x00, 640, 480, sink3)
	p3.Target = PanelVisible
	now = 15
	p3.Step(now) // leaves -31 => Panel
	if len(sink3.cues) != 1 {
		t.Fatalf("interrupted: first cue count %d", len(sink3.cues))
	}
	// Move a couple more steps toward visible, no new Panel.
	now += 15
	p3.Step(now)
	now += 15
	p3.Step(now)
	if len(sink3.cues) != 1 {
		t.Fatalf("mid-slide should not re-emit Panel: cues %v", sink3.cues)
	}
	// Reverse target back to parked mid-slide: should not emit new Panel until reaching? Actually leaving detent only at detent.
	p3.Target = PanelParked
	// Current offset is not at detent (e.g., -11), so next step should not emit Panel (only when leaving 0).
	now += 15
	p3.Step(now)
	if len(sink3.cues) != 1 {
		t.Fatalf("reversal mid-slide should not emit Panel (not at detent): cues %v", sink3.cues)
	}
	// Continue to parked, should emit Options on arrival.
	for i := 0; i < 50 && p3.Offset != PanelParked; i++ {
		now += 15
		p3.Step(now)
	}
	if sink3.cues[len(sink3.cues)-1] != CueOptions {
		t.Fatalf("arrival at parked should emit Options: cues %v", sink3.cues)
	}
}

func TestPanelNoCueWhenNotAtDetent(t *testing.T) {
	// No Panel cue when starting mid-slide; only Options on arrival if detent reached.
	sink := &mockSink{}
	p := &Panel{Offset: -15, Target: PanelVisible, LastThrottle: 0, sink: sink}
	p.Step(15)
	if len(sink.cues) != 0 {
		t.Fatalf("mid-start should not emit Panel: %v", sink.cues)
	}
	// Advance to visible; should emit Options.
	var now uint32 = 30
	for i := 0; i < 50 && p.Offset != PanelVisible; i++ {
		p.Step(now)
		now += 15
	}
	if len(sink.cues) != 1 || sink.cues[0] != CueOptions {
		t.Fatalf("mid->visible should emit single Options on arrival: %v", sink.cues)
	}
}

func TestPanelSpacePolarityMatrix(t *testing.T) {
	// Space polarity [07 §6] C14:
	// HELD slides toward −31 unless editor holds focus ⇒ toward 0; RELEASED always toward 0.
	tests := []struct {
		spaceHeld     bool
		editorFocused bool
		wantTarget    int8
		desc          string
	}{
		{false, false, PanelVisible, "released, no editor => 0"},
		{false, true, PanelVisible, "released, editor => 0 (always 0)"},
		{true, false, PanelParked, "held, no editor => -31"},
		{true, true, PanelVisible, "held, editor holds focus => 0 override"},
	}
	for _, tc := range tests {
		got := TargetFor(tc.spaceHeld, tc.editorFocused)
		if got != tc.wantTarget {
			t.Errorf("TargetFor(held=%v editor=%v) = %d want %d (%s)", tc.spaceHeld, tc.editorFocused, got, tc.wantTarget, tc.desc)
		}
		// Also via Panel.SetTarget
		p := &Panel{Offset: 0}
		p.SetTarget(tc.spaceHeld, tc.editorFocused)
		if p.Target != tc.wantTarget {
			t.Errorf("SetTarget(held=%v editor=%v) = %d want %d (%s)", tc.spaceHeld, tc.editorFocused, p.Target, tc.wantTarget, tc.desc)
		}
	}
	// Verify IsTextEditorKind gate uses authored type 3 [07 §4][07 §6] C14.
	if !IsTextEditorKind(3) {
		t.Fatalf("IsTextEditorKind(3) should be true [07 §4] C14")
	}
	if IsTextEditorKind(1) || IsTextEditorKind(2) || IsTextEditorKind(4) || IsTextEditorKind(0) {
		t.Fatalf("IsTextEditorKind non-3 should be false")
	}
	// Full Advance matrix: held+editorFocused should move toward visible even from parked.
	sink := &mockSink{}
	p := NewPanel(0x00, 640, 480, sink) // parked -31
	p.Advance(15, true, true)           // held, editor focused => target 0
	if p.Target != PanelVisible {
		t.Fatalf("Advance held+editor: Target=%d want 0", p.Target)
	}
	if p.Offset == PanelParked {
		t.Fatalf("Advance held+editor should have moved toward 0, still parked")
	}
	// Held without editor should move toward -31 from visible.
	sink2 := &mockSink{}
	p2 := NewPanel(0x04, 640, 480, sink2) // visible 0
	p2.Advance(15, true, false)           // held, no editor => -31
	if p2.Target != PanelParked {
		t.Fatalf("Advance held no editor: Target=%d want -31", p2.Target)
	}
	if p2.Offset == PanelVisible {
		t.Fatalf("Advance held no editor should have moved toward -31, still visible")
	}
}

func TestPanelOverlayData(t *testing.T) {
	// Nonzero offset blits moving strip at y+offset with Game Time hh:mm:ss, total units, game speed [07 §6] C14.
	p := NewPanel(0x00, 640, 480, nil) // parked -31 => should blit
	if !p.ShouldBlitStrip() {
		t.Fatalf("parked offset -31 should blit strip [07 §6] C14")
	}
	if p.StripY(100) != 69 { // 100 + (-31)
		t.Fatalf("StripY(100) at -31 = %d want 69", p.StripY(100))
	}
	ov := p.Overlay(100, 0, 5, 10, 10, 10)
	if !ov.ShowStrip {
		t.Fatalf("Overlay ShowStrip should be true when offset !=0 [07 §6] C14")
	}
	if ov.Y != 69 {
		t.Fatalf("Overlay Y = %d want 69 y+offset", ov.Y)
	}
	if ov.GameTime != "00:00:00" {
		t.Fatalf("GameTime at 0 ticks = %q want 00:00:00", ov.GameTime)
	}
	if ov.TotalUnitsText != "Total Units: 5 (Max 10)" {
		t.Fatalf("TotalUnitsText = %q want Total Units: 5 (Max 10)", ov.TotalUnitsText)
	}
	if ov.GameSpeedText != "Game Speed: Normal" {
		t.Fatalf("GameSpeed at 10 = %q want 'Game Speed: Normal'", ov.GameSpeedText)
	}
	if ov.GameSpeedSuffix != "" {
		t.Fatalf("suffix should be empty when requested==active")
	}

	// Visible offset 0 => no strip blit [07 §6] C14.
	pVis := NewPanel(0x04, 640, 480, nil) // visible 0
	if pVis.ShouldBlitStrip() {
		t.Fatalf("visible 0 should not blit strip [07 §6] C14")
	}
	ov2 := pVis.Overlay(100, 90, 3, 7, 10, 10) // 90 ticks = 3 sec => 00:00:03
	if ov2.ShowStrip {
		t.Fatalf("visible overlay ShowStrip should be false")
	}
	if ov2.GameTime != "00:00:03" {
		t.Fatalf("GameTime 90 ticks = %q want 00:00:03", ov2.GameTime)
	}

	// GameTime hh:mm:ss edge: 30*3600 = 108000 ticks => 01:00:00
	pMid := &Panel{Offset: -10, LastThrottle: 0}
	ov3 := pMid.Overlay(0, 108000, 0, 0, 12, 10)
	if ov3.GameTime != "01:00:00" {
		t.Fatalf("108000 ticks = %q want 01:00:00", ov3.GameTime)
	}
	if ov3.GameSpeedSuffix != " (+2)" {
		t.Fatalf("GameSpeed suffix 12 vs 10 = %q want ' (+2)'", ov3.GameSpeedSuffix)
	}
	if ov3.GameSpeedLabel != "Normal" {
		t.Fatalf("GameSpeedLabel active 10 = %q want Normal", ov3.GameSpeedLabel)
	}
	if ov3.GameSpeedText != "Game Speed: Normal (+2)" {
		t.Fatalf("GameSpeedText = %q want 'Game Speed: Normal (+2)'", ov3.GameSpeedText)
	}

	// Negative suffix.
	ov4 := pMid.Overlay(0, 0, 1, 2, 8, 10)
	if ov4.GameSpeedSuffix != " (-2)" {
		t.Fatalf("suffix 8 vs 10 = %q want ' (-2)'", ov4.GameSpeedSuffix)
	}

	// FormatGameTime helper directly.
	if FormatGameTime(30) != "00:00:01" {
		t.Fatalf("FormatGameTime 30 = %q want 00:00:01", FormatGameTime(30))
	}
	if FormatGameTime(30*60) != "00:01:00" {
		t.Fatalf("FormatGameTime 1800 = %q", FormatGameTime(1800))
	}
	if FormatGameTime(30*3661) != "01:01:01" { // 3661 sec
		t.Fatalf("FormatGameTime 109830 = %q want 01:01:01", FormatGameTime(30*3661))
	}
}

func TestPanelThrottleUsesWallClockAndNeverFeedsSim(t *testing.T) {
	// I6 note: wall-clock throttle is PRESENTATION-side, never feeds sim state.
	// Verify that Step uses the explicit now uint32 and that time.Now helper exists but deterministic path does not depend on it.
	// This is a static check that internal sim packages do not import time, but hud may.
	// Here we just ensure Panel.Step is deterministic for same now and target.
	p1 := NewPanel(0x00, 640, 480, nil)
	p1.Target = PanelVisible
	p1.Step(15)
	o1 := p1.Offset
	p2 := NewPanel(0x00, 640, 480, nil)
	p2.Target = PanelVisible
	p2.Step(15)
	if o1 != p2.Offset {
		t.Fatalf("deterministic Step should give same offset for same now")
	}
	// StepNow uses time.Now but still respects throttle and polarity [07 §6] I6.
	// We cannot test time.Now deterministically, but ensure it doesn't panic.
	p3 := NewPanel(0x00, 640, 480, nil)
	p3.StepNow(true, false) // presentation helper, should not panic
	p3.AdvanceNow(false, false)
}
