package main

import (
	"time"

	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/input"
)

// BattleMouseButtons is the logical mouse state consumed by the battle
// controller. Coordinates and button state are expressed in the negotiated
// 640x480 battle surface, not in device pixels [07 §1][07 §8].
type BattleMouseButtons = input.MouseButtons

// BattleModifiers is the platform-neutral modifier state. Keeping modifiers
// in the logical frame makes a replay independent of Ebitengine's key names.
type BattleModifiers = input.Modifiers

// BattleInputFrame is one presentation input sample. It is deliberately a
// value type: the Ebitengine adapter and deterministic replays both submit the
// same frame to BattleController.Step, while the controller alone decides
// which canonical battle command (if any) is issued [07 §8][07 §9].
//
// PressedKeys and HeldKeys carry the complete keyboard state so production
// hotkeys continue to use the existing input decision path. The slices are
// copied by the controller before use; callers may reuse their frame storage.
type BattleInputFrame = input.Sample

// monotonicMillisSource adapts Go's monotonic process clock to the wrapping
// 32-bit millisecond contract. time.Since uses the monotonic component of the
// captured start value when one is available, and the uint32 conversion is
// intentionally allowed to wrap [01 §4.1][01 §4.2].
type monotonicMillisSource struct {
	start time.Time
}

var monotonicHostStart = time.Now()

func newMonotonicMillisSource() *monotonicMillisSource {
	return &monotonicMillisSource{start: monotonicHostStart}
}

func (s *monotonicMillisSource) Millis32() uint32 {
	if s == nil {
		return 0
	}
	return uint32(time.Since(s.start) / time.Millisecond)
}

// BattleController is the single production/replay input seam. It owns only
// presentation input state and timing; authoritative mutation remains in the
// existing battleSession.handleInput and Session.Step calls.
type BattleController struct {
	battle            *battleSession
	millis            clock.MillisSource
	cursorScaled      int32
	cursorScaledValid bool
}

// NewBattleController constructs the production controller. An optional
// source is provided for deterministic replays and tests; omitted sources use
// the monotonic host clock. The variadic form preserves the existing call site
// while keeping source injection at the composition boundary.
func NewBattleController(b *battleSession, sources ...clock.MillisSource) *BattleController {
	var source clock.MillisSource
	if len(sources) > 0 {
		source = sources[0]
	}
	if source == nil && b != nil {
		source = b.millisSource
	}
	if source == nil {
		source = newMonotonicMillisSource()
	}
	return &BattleController{battle: b, millis: source}
}

// Step feeds one logical input frame through the production battle decision
// path and advances the existing presentation-to-simulation budget. Elapsed
// remains part of the input value for presentation callers, but is not a
// timing authority; every budget sample comes from Millis32 [01 §4.1].
func (c *BattleController) Step(frame BattleInputFrame, cl *client.Client) {
	if c == nil || c.battle == nil {
		return
	}
	c.battle.handleInput(input.StateFromSample(frame), cl)
	if c.battle.ended || c.battle.sess == nil || c.battle.sess.Clock == nil {
		return
	}
	// The composition root binds, or clears, the presentation-owned phase-7
	// seam before the next runnable sub-tick. The session invokes it at the
	// exact boundary, including every sub-tick in a catch-up batch
	// [01 §4.4][R-CRD-005 §1].
	c.battle.sess.SetPhase7Service(cl)
	scaled := int32(0)
	if c.millis != nil {
		scaled = clock.ScaledNow(c.millis.Millis32())
	}
	if cl != nil {
		if c.cursorScaledValid {
			delta := scaled - c.cursorScaled
			if delta > 0 {
				cl.StepCursorScaledDelta(delta)
			}
		} else {
			c.cursorScaledValid = true
		}
		c.cursorScaled = scaled
	} else {
		c.cursorScaledValid = false
	}
	c.battle.sess.Step(scaled)
}
