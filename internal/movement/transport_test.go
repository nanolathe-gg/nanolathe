package movement

import "testing"

// helper to make a valid load state that passes all entry gates.
func validLoadState(phase uint8) *TransportState {
	return &TransportState{
		Phase:                phase,
		TargetPresent:        true,
		ExecutorFlags:        0,
		TargetY:              1 << 16, // 1 wu above sea level
		TargetModelTop:       0,
		SeaLevel:             0,
		CargoListEmpty:       true,
		IsAirCarrier:         true,
		CarrierTransportSize: 10,
		TargetFootprintX:     5,
		CarrierHasLiveMover:  true,
		CarrierCanFly:        true,
		InterruptFlags:       0,
		AttachPiece:          3,
		AttachPieceValid:     true,
		CarrierCruiseAlt:     100,
	}
}

// TestLoadPerPhaseRechecks verifies that every phase re-checks the four
// entry gates in order before doing work [04 §10.2] C31. Each gate failure
// must return code 8; gates 1–3 carry Transport mission failed verbatim,
// gate 4 returns code 8 with NO message. The check is re-applied at each
// phase 0..5.
func TestLoadPerPhaseRechecks(t *testing.T) {
	for phase := uint8(0); phase <= 5; phase++ {
		// Gate 1: target non-null [04 §10.2].
		s := validLoadState(phase)
		s.TargetPresent = false
		r, msg, ev := s.StepLoad()
		if r != TransportResultFailed || msg != TransportFailedMessage || ev != TransportEventNone {
			t.Fatalf("phase %d gate1 target null: got (%d,%q,%d) want (8,%q,0)", phase, r, msg, ev, TransportFailedMessage)
		}
		if s.Phase != phase {
			t.Fatalf("phase %d gate1 must not advance phase", phase)
		}

		// Gate 2: executor flags free of 0x10048 [04 §10.2] C31.
		s = validLoadState(phase)
		s.ExecutorFlags = 0x10048
		r, msg, ev = s.StepLoad()
		if r != TransportResultFailed || msg != TransportFailedMessage || ev != TransportEventNone {
			t.Fatalf("phase %d gate2 flags 0x10048: got (%d,%q,%d) want (8,%q,0)", phase, r, msg, ev, TransportFailedMessage)
		}
		// Also single bit of mask should fail: 0x00008 part.
		s = validLoadState(phase)
		s.ExecutorFlags = 0x40
		r, msg, _ = s.StepLoad()
		if r != TransportResultFailed || msg != TransportFailedMessage {
			t.Fatalf("phase %d gate2 flags 0x40: got (%d,%q) want (8,%q)", phase, r, msg, TransportFailedMessage)
		}
		s = validLoadState(phase)
		s.ExecutorFlags = 0x10000
		r, msg, _ = s.StepLoad()
		if r != TransportResultFailed || msg != TransportFailedMessage {
			t.Fatalf("phase %d gate2 flags 0x10000: got (%d,%q) want (8,%q)", phase, r, msg, TransportFailedMessage)
		}

		// Gate 3: target Y+modelTop signed greater than seaLevel<<16 [04 §10.2].
		s = validLoadState(phase)
		s.TargetY = 0
		s.TargetModelTop = 0
		s.SeaLevel = 0 // 0 <=0 fails (must be >)
		r, msg, ev = s.StepLoad()
		if r != TransportResultFailed || msg != TransportFailedMessage || ev != TransportEventNone {
			t.Fatalf("phase %d gate3 at sea level: got (%d,%q,%d) want (8,%q,0)", phase, r, msg, ev, TransportFailedMessage)
		}
		// Also below sea level fails.
		s = validLoadState(phase)
		s.TargetY = -10 << 16
		s.TargetModelTop = 0
		s.SeaLevel = 0
		r, msg, _ = s.StepLoad()
		if r != TransportResultFailed || msg != TransportFailedMessage {
			t.Fatalf("phase %d gate3 below sea level: got (%d,%q) want (8,%q)", phase, r, msg, TransportFailedMessage)
		}
		// Above passes (tested implicitly via validLoadState).

		// Gate 4: air carrier cargo list must be EMPTY; ground carriers don't apply [04 §10.2] C31.
		s = validLoadState(phase)
		s.IsAirCarrier = true
		s.CargoListEmpty = false
		r, msg, ev = s.StepLoad()
		if r != TransportResultFailed || msg != "" || ev != TransportEventNone {
			t.Fatalf("phase %d gate4 air cargo not empty: got (%d,%q,%d) want (8,\"\",0)", phase, r, msg, ev)
		}
		if s.Phase != phase {
			t.Fatalf("phase %d gate4 must not advance", phase)
		}
		// Ground carrier with cargo not empty must PASS gate 4.
		s = validLoadState(phase)
		s.IsAirCarrier = false
		s.CargoListEmpty = false
		// Need to bypass phase-0 heavy/live gates to see that entry gate itself passes.
		// For phases other than 0, no additional size gate interferes; for phase 0 we set size to pass.
		// The call should NOT return 8 due to gate4.
		r, msg, _ = s.StepLoad()
		if r == TransportResultFailed && msg == "" {
			t.Fatalf("phase %d ground carrier must not apply cargo-empty gate, but got gate4 failure", phase)
		}
		// For phase 0, even ground carrier with cargo not empty continues to heavy/mover checks;
		// ensure it doesn't fail with empty-diagnostic gate4 code.
		if phase == 0 && r == TransportResultFailed && msg == "" {
			t.Fatalf("phase 0 ground carrier incorrectly failed gate4")
		}
	}
}

// TestHeavyTransportMessageVerbatim locks the verbatim diagnostic string
// Unit is too heavy to transport at the phase-0 size gate [04 §10.2].
func TestHeavyTransportMessageVerbatim(t *testing.T) {
	s := validLoadState(0)
	s.TargetFootprintX = 5
	s.CarrierTransportSize = 2 // 5 >2 => too heavy
	r, msg, ev := s.StepLoad()
	if r != TransportResultFailed {
		t.Fatalf("heavy: result %d want 8", r)
	}
	if msg != HeavyTransportMessage {
		t.Fatalf("heavy: msg %q want %q", msg, HeavyTransportMessage)
	}
	if msg != "Unit is too heavy to transport" {
		t.Fatalf("heavy verbatim mismatch: %q", msg)
	}
	if ev != TransportEventNone {
		t.Fatalf("heavy: event %d want 0", ev)
	}
	// Exact case sensitive check: lower case variant must not equal.
	if msg == "unit is too heavy to transport" {
		t.Fatal("heavy message case mismatch must be verbatim")
	}
	// Size gate is signed compare: negative footprint must pass even if carrier small.
	s = validLoadState(0)
	s.TargetFootprintX = -5 // negative, signed comparison -5 <=2 passes
	s.CarrierTransportSize = 2
	r, msg, _ = s.StepLoad()
	if r == TransportResultFailed && msg == HeavyTransportMessage {
		t.Fatalf("heavy signed compare: negative footprint -5 should not be heavy vs 2, got heavy")
	}
	if r != TransportResultContinue {
		t.Fatalf("heavy signed negative should advance, got %d", r)
	}
	// Equal passes: 2 <=2 not heavy.
	s = validLoadState(0)
	s.TargetFootprintX = 2
	s.CarrierTransportSize = 2
	r, msg, _ = s.StepLoad()
	if r == TransportResultFailed && msg == HeavyTransportMessage {
		t.Fatalf("heavy equal should not be heavy")
	}
	// One over fails.
	s = validLoadState(0)
	s.TargetFootprintX = 3
	s.CarrierTransportSize = 2
	r, msg, _ = s.StepLoad()
	if msg != HeavyTransportMessage {
		t.Fatalf("heavy equal+1 should be heavy, got %q", msg)
	}
}

// TestPhase0LiveMoverGate checks the live mover / canfly gate at phase 0
// else 7 [04 §10.2].
func TestPhase0LiveMoverGate(t *testing.T) {
	s := validLoadState(0)
	s.CarrierHasLiveMover = false
	s.CarrierCanFly = true
	r, _, _ := s.StepLoad()
	if r != TransportResultBlocked {
		t.Fatalf("phase0 no live mover want 7 got %d", r)
	}
	s = validLoadState(0)
	s.CarrierHasLiveMover = true
	s.CarrierCanFly = false
	r, _, _ = s.StepLoad()
	if r != TransportResultBlocked {
		t.Fatalf("phase0 no canfly want 7 got %d", r)
	}
	// Both true passes to heavy check.
	s = validLoadState(0)
	r, _, _ = s.StepLoad()
	if r != TransportResultContinue {
		t.Fatalf("phase0 both live+canfly want 1 got %d", r)
	}
}

// TestEventCodesAndPhaseProgression verifies the full load phase machine
// and that event codes 12 and 13 are emitted at the established transitions
// [GAP T16][04 §10.2]. Load success row: phase 4 emits 12; unload phase 3 emits 13.
func TestEventCodesAndPhaseProgression(t *testing.T) {
	// Load phases 0..5 successful progression.
	s := validLoadState(0)
	// Phase 0 ->1
	r, msg, ev := s.StepLoad()
	if r != TransportResultContinue || ev != TransportEventNone || s.Phase != 1 {
		t.Fatalf("load p0: got (%d,%q,%d) phase %d want (1,Loading,0) 1", r, msg, ev, s.Phase)
	}
	if msg != "Loading" {
		t.Fatalf("load p0 diagnostic %q want Loading", msg)
	}
	// Phase1 ->2
	r, _, ev = s.StepLoad()
	if r != TransportResultContinue || ev != TransportEventNone || s.Phase != 2 {
		t.Fatalf("load p1: got (%d,_,%d) phase %d want 1,0 2", r, ev, s.Phase)
	}
	// Phase2 prepares attach piece, status Preparing for transport [04 §10.2].
	s.AttachPieceValid = false // force fallback path
	s.AttachPiece = 0
	r, msg, ev = s.StepLoad()
	if r != TransportResultContinue || ev != TransportEventNone || s.Phase != 3 {
		t.Fatalf("load p2: got (%d,%q,%d) phase %d want 1,Preparing...,0 3", r, msg, ev, s.Phase)
	}
	if msg != "Preparing for transport" {
		t.Fatalf("load p2 diagnostic %q want Preparing for transport", msg)
	}
	if s.AttachPiece != -1 || !s.AttachPieceValid {
		t.Fatalf("load p2 attach fallback want -1 got %d valid %v", s.AttachPiece, s.AttachPieceValid)
	}
	// Provide a real piece for next steps.
	s.AttachPiece = 7
	s.AttachPieceValid = true
	// Phase3 ->4
	r, _, ev = s.StepLoad()
	if r != TransportResultContinue || ev != TransportEventNone || s.Phase != 4 {
		t.Fatalf("load p3: got (%d,_,%d) phase %d want 1,0 4", r, ev, s.Phase)
	}
	// Phase4 success emits 12 and moves to 5.
	r, _, ev = s.StepLoad()
	if r != TransportResultContinue || ev != TransportEventAttach || s.Phase != 5 {
		t.Fatalf("load p4 success: got (%d,_,%d) phase %d want (1,_,12) 5", r, ev, s.Phase)
	}
	if ev != 12 {
		t.Fatalf("load p4 event %d want 12", ev)
	}
	// Phase5 done 5.
	r, _, ev = s.StepLoad()
	if r != TransportResultDone || ev != TransportEventNone {
		t.Fatalf("load p5: got (%d,_,%d) want (5,_,0)", r, ev)
	}
	// Other phase ->7.
	s.Phase = 9
	r, _, ev = s.StepLoad()
	if r != TransportResultBlocked || ev != TransportEventNone {
		t.Fatalf("load other: got (%d,_,%d) want (7,_,0)", r, ev)
	}

	// Unload events: phase 3 emits 13 [04 §10.2][GAP T16].
	us := &TransportUnloadState{
		Phase:                0,
		CargoListEmpty:       false,
		CarrierHasLiveMover:  true,
		CarrierCanFly:        true,
		PlacementValidFirst:  true,
		PlacementValidSecond: true,
		Interrupt:            false,
	}
	r, msg, ev = us.StepUnload()
	if r != TransportResultContinue || msg != "Unloading" || ev != TransportEventNone || us.Phase != 1 {
		t.Fatalf("unload p0: got (%d,%q,%d) phase %d want (1,Unloading,0) 1", r, msg, ev, us.Phase)
	}
	r, _, ev = us.StepUnload()
	if r != TransportResultContinue || ev != TransportEventNone || us.Phase != 2 {
		t.Fatalf("unload p1: got (%d,_,%d) phase %d want 1,0 2", r, ev, us.Phase)
	}
	r, _, ev = us.StepUnload()
	if r != TransportResultContinue || ev != TransportEventNone || us.Phase != 3 {
		t.Fatalf("unload p2: got (%d,_,%d) phase %d want 1,0 3", r, ev, us.Phase)
	}
	r, _, ev = us.StepUnload()
	if r != TransportResultDone || ev != TransportEventDetach || us.Phase != 4 {
		t.Fatalf("unload p3: got (%d,_,%d) phase %d want (5,_,13) 4", r, ev, us.Phase)
	}
	if ev != 13 {
		t.Fatalf("unload p3 event %d want 13", ev)
	}
	// Immediate empty cargo returns done 5 before any phase dispatch.
	us = &TransportUnloadState{CargoListEmpty: true, Phase: 0}
	r, _, ev = us.StepUnload()
	if r != TransportResultDone || ev != TransportEventNone {
		t.Fatalf("unload empty immediate: got (%d,_,%d) want (5,_,0)", r, ev)
	}
}

// TestPhase4Interruption ensures phase-4 interrupted edge ends the transport
// without attaching and returns code 8 [04 §10.2]. No event 12.
func TestPhase4Interruption(t *testing.T) {
	s := validLoadState(4)
	s.InterruptFlags = 0x42 // interrupt combination present [04 §10.2] mask 0x42
	r, msg, ev := s.StepLoad()
	if r != TransportResultFailed || msg != "" || ev != TransportEventNone {
		t.Fatalf("phase4 interrupted: got (%d,%q,%d) want (8,\"\",0)", r, msg, ev)
	}
	if ev == TransportEventAttach {
		t.Fatal("phase4 interrupted must NOT emit event 12")
	}
	// Phase must not have advanced to 5 on interruption (spec says return WITHOUT attaching => no climb-away, no event).
	// Our implementation leaves Phase at 4 on interrupted path (or could advance, but test checks that re-interrogating still fails with gate check).
	// Ensure that after interruption the order would be freed (code 8) and no later step emits 12.
	// Call again with same interrupt — should still return 8 and still no event.
	r, _, ev = s.StepLoad()
	if r != TransportResultFailed || ev != TransportEventNone {
		t.Fatalf("phase4 interrupted second call: got (%d,_,%d) want (8,_,0)", r, ev)
	}
	// Without interrupt flag, phase 4 success does emit 12.
	s = validLoadState(4)
	s.InterruptFlags = 0
	r, _, ev = s.StepLoad()
	if r != TransportResultContinue || ev != TransportEventAttach {
		t.Fatalf("phase4 success: got (%d,_,%d) want (1,_,12)", r, ev)
	}
	// Partial flags: 0x40 alone should trigger? Mask is 0x42, so 0x02 alone also triggers per & !=0?
	// Retail checks flags & 0x42 non-zero [04 §10.2] — any bit of 0x42 triggers.
	s = validLoadState(4)
	s.InterruptFlags = 0x40
	r, _, ev = s.StepLoad()
	if r != TransportResultFailed {
		t.Fatalf("phase4 interrupt 0x40 should trigger, got %d", r)
	}
	s = validLoadState(4)
	s.InterruptFlags = 0x02
	r, _, ev = s.StepLoad()
	if r != TransportResultFailed {
		t.Fatalf("phase4 interrupt 0x02 should trigger, got %d", r)
	}
	// No interrupt bits => success.
	s = validLoadState(4)
	s.InterruptFlags = 0x01
	r, _, ev = s.StepLoad()
	if r != TransportResultContinue || ev != TransportEventAttach {
		t.Fatalf("phase4 0x01 should not trigger interrupt, got (%d, %d)", r, ev)
	}
}

// TestAirCarrierCargoEmptyGate explicitly locks the air vs ground
// distinction [04 §10.2] C31. Air requires empty, ground does not.
func TestAirCarrierCargoEmptyGate(t *testing.T) {
	// Air carrier with cargo not empty fails gate 4 with no message and code 8, at any phase.
	for _, phase := range []uint8{0, 1, 2, 3, 4, 5} {
		s := validLoadState(phase)
		s.IsAirCarrier = true
		s.CargoListEmpty = false
		r, msg, _ := s.StepLoad()
		if r != TransportResultFailed || msg != "" {
			t.Fatalf("air phase %d cargo not empty want (8,\"\") got (%d,%q)", phase, r, msg)
		}
	}
	// Ground carrier with cargo not empty must NOT fail gate4; it should reach the phase-specific logic.
	for _, phase := range []uint8{1, 2, 3} { // avoid phase0 heavy/live gates obscuring
		s := validLoadState(phase)
		s.IsAirCarrier = false
		s.CargoListEmpty = false
		r, msg, _ := s.StepLoad()
		// Should not be gate4 failure (which is msg=="" + 8). For phase 1, expect continue.
		if r == TransportResultFailed && msg == "" {
			t.Fatalf("ground phase %d incorrectly failed gate4 air-empty check", phase)
		}
		if phase == 1 && r != TransportResultContinue {
			t.Fatalf("ground phase1 with cargo non-empty should pass gate4 and continue, got %d", r)
		}
	}
	// Air carrier empty passes.
	s := validLoadState(1)
	s.IsAirCarrier = true
	s.CargoListEmpty = true
	r, _, _ := s.StepLoad()
	if r != TransportResultContinue {
		t.Fatalf("air empty should pass, got %d", r)
	}
}

// TestUnloadValidation ensures unload double validation is injected and
// that Unable to unload unit is verbatim [04 §10.2].
func TestUnloadValidation(t *testing.T) {
	us := &TransportUnloadState{
		Phase:               1,
		CargoListEmpty:      false,
		PlacementValidFirst: false,
	}
	r, msg, _ := us.StepUnload()
	if r != TransportResultRetry || msg != UnableUnloadMessage {
		t.Fatalf("unload p1 invalid: got (%d,%q) want (9,%q)", r, msg, UnableUnloadMessage)
	}
	if msg != "Unable to unload unit" {
		t.Fatalf("unload p1 verbatim want %q got %q", "Unable to unload unit", msg)
	}
	us = &TransportUnloadState{
		Phase:                2,
		CargoListEmpty:       false,
		Interrupt:            true,
		PlacementValidSecond: true,
	}
	r, msg, _ = us.StepUnload()
	if r != TransportResultRetry || msg != "" {
		t.Fatalf("unload p2 interrupt before validation: got (%d,%q) want (9,\"\")", r, msg)
	}
	us = &TransportUnloadState{
		Phase:                2,
		CargoListEmpty:       false,
		Interrupt:            false,
		PlacementValidSecond: false,
	}
	r, msg, _ = us.StepUnload()
	if r != TransportResultRetry || msg != UnableUnloadMessage {
		t.Fatalf("unload p2 second invalid: got (%d,%q) want (9,%q)", r, msg, UnableUnloadMessage)
	}
	// Phase0 live mover gate also else 7.
	us = &TransportUnloadState{Phase: 0, CargoListEmpty: false, CarrierHasLiveMover: false, CarrierCanFly: true}
	r, _, _ = us.StepUnload()
	if r != TransportResultBlocked {
		t.Fatalf("unload p0 no mover want 7 got %d", r)
	}
}
