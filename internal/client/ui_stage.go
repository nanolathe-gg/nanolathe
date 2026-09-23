package client

import (
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// UIFrame is the immutable presentation value handed to the one UI stage
// after the committed-world passes. Committed is valid only for the duration
// of DrawUI; the frame buffer may reuse it after the draw returns [I6].
//
// Keeping the boundary typed prevents a UI stage from reaching through the
// client into a live session or installing another renderer. The client owns
// when this stage runs; the stage only supplies authored UI pixels.
type UIFrame struct {
	Committed *frame.Frame
	Resources DisplayedResources
}

// UIStage is the single concrete client's UI boundary. Implementations are
// adapters for an authored frontend or battle surface; they do not own world
// ordering or frame publication [03 §1][07 §3].
type UIStage interface {
	DrawUI(*Client, UIFrame)
}

// SetUIStage installs the one typed UI adapter used by the client. A nil stage
// leaves the committed-world surface without authored UI, which is useful for
// the loading hand-off and focused renderer tests.
func (c *Client) SetUIStage(stage UIStage) {
	if c != nil {
		c.uiStage = stage
	}
}

// DisplayedResources is the presentation-owned stock pair and rate latch. It never replaces
// the authoritative EconomyView [05 R-ECO-01 §6][07 R-HUD-03 §4].
type DisplayedResources struct {
	Energy, Metal                  float32
	EnergyProduced, EnergyConsumed float32
	MetalProduced, MetalConsumed   float32
}

type resourceDisplayTimer struct {
	deadline uint32
	bound    bool
}

// ResourceDisplayTimers returns a detached save-projection overlay for players
// whose display deadlines presentation has owned. Unviewed players retain the
// session's saved values; no presentation value is written into simulation [I6].
func (c *Client) ResourceDisplayTimers() map[uint8]uint32 {
	if c == nil {
		return nil
	}
	out := make(map[uint8]uint32)
	for player, timer := range c.resourceTimers {
		if timer.bound {
			out[uint8(player)] = timer.deadline
		}
	}
	return out
}

// BeginPresentationFrame advances host-frame presentation state exactly once.
// Call after JoinPreRecord and before consuming or recording the frame. Pure
// composition and speculative recording must not call this boundary [I6].
func (c *Client) BeginPresentationFrame() {
	if c == nil {
		return
	}
	c.displayedResources, c.resourceTimers = c.nextResourceDisplayState()
	c.TickPresentationAudio()
}

func (c *Client) nextDisplayedResources() DisplayedResources {
	next, _ := c.nextResourceDisplayState()
	return next
}

// Prediction copies both latch and deadlines. Only BeginPresentationFrame
// commits this state; composition and pre-record retries cannot advance it.
func (c *Client) nextResourceDisplayState() (DisplayedResources, [10]resourceDisplayTimer) {
	next := c.displayedResources
	timers := c.resourceTimers
	if c.buffer == nil {
		return next, timers
	}
	f := c.buffer.Current()
	if f == nil {
		return next, timers
	}
	// A missing viewing row retains the pair; another owner is never a fallback.
	for _, live := range f.Economy {
		if live.Player != f.ViewingPlayer {
			continue
		}
		next.Energy = easeDisplayedStock(next.Energy, live.Energy, live.EnergyCapacity)
		next.Metal = easeDisplayedStock(next.Metal, live.Metal, live.MetalCapacity)
		if int(live.Player) < len(timers) {
			timer := &timers[live.Player]
			if !timer.bound {
				timer.deadline, timer.bound = live.DisplayTimer, true
			}
			// Unsigned, strict, and one prior-deadline advance per presented
			// frame even when several intervals are overdue [05 R-ECO-01 §1, §6].
			if timer.deadline < f.Tick {
				timer.deadline += 30
				next.EnergyProduced, next.EnergyConsumed = live.EnergyProduced, live.EnergyConsumed
				next.MetalProduced, next.MetalConsumed = live.MetalProduced, live.MetalConsumed
			}
		}
		break
	}
	return next, timers
}

// Low-word integer truncation precedes the wrapped gap and signed divide; the
// single store precedes the strict capacity cap [I3][05 R-ECO-01 §6][07 R-HUD-03 §4].
func easeDisplayedStock(displayed, live, capacity float32) float32 {
	i, j := numeric.TruncateFloat32ToLow32(displayed), numeric.TruncateFloat32ToLow32(live)
	gap := j - i
	step := gap / 8
	if step == 0 {
		if gap > 0 {
			step = 1
		} else if gap < 0 {
			step = -1
		}
	}
	next := float32(i + step)
	if next > capacity {
		next = capacity
	}
	return next
}
