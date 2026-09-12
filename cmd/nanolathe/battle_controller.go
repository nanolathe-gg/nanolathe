package main

import (
	"time"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/clock"
	committedframe "github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/session"
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
	shiftHeld bool

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
	c.battle.stepFollowCamera()
	state := input.StateFromSample(frame)
	if c.battle.sess != nil {
		c.battle.sess.SetPublicationObserver(func(cur *committedframe.Frame) {
			c.battle.applyPublishedCamera(cur)
			// Observe every committed position before a catch-up tick replaces it.
			// Recording alone skips history at high speed or after a slow frame,
			// suppressing hover dust (DESIGN_GPU_RENDERER §26). Repeated capture
			// and draw observations are idempotent; this consumes only frames [I6].
			cl.ObserveCommittedTick()
		})
		shift := state.Kbd.HasShift()
		if cl != nil && cl.Input() != nil {
			shift = cl.Input().Kbd.HasShift()
		}
		if shift != c.shiftHeld {
			if c.battle.sess.EnqueueHumanCommand(session.HumanCommand{Kind: session.HumanShiftState, ShiftHeld: shift}) == nil {
				c.shiftHeld = shift
			}
		}
	}
	// Retail handles follow hotkeys after the sub-tick batch. Keep these
	// presentation-only requests pending while automatic cycles consume it.
	c.battle.deferFollowInput = true
	c.battle.handleInput(state, cl)
	c.battle.deferFollowInput = false
	defer func() {
		if pending := c.battle.pendingFollowInput; pending != nil {
			c.battle.pendingFollowInput = nil
			pending()
		}
	}()
	if c.battle.ended || c.battle.sess == nil || c.battle.sess.Clock == nil {
		return
	}
	// Battle entry installs its loaded-model registry once. Client replacement
	// cannot change phase-7 ownership or pause model textures; the session
	// invokes that registry at every runnable sub-tick [01 §4.4][R-CRD-005 §1].
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
	c.battle.noteTickTiming()
}
