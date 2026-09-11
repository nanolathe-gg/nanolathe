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

// TacticalOverlayStage is an optional UI adapter boundary for live tactical
// guides. It runs outside the world transform, before strategic icons and HUD,
// with the current committed identity/visibility frame (GPU design §20, [I6]).
type TacticalOverlayStage interface {
	DrawTacticalOverlay(*Client, *frame.Frame)
}

// SetUIStage installs the one typed UI adapter used by the client. A nil stage
// leaves the committed-world surface without authored UI, which is useful for
// the loading hand-off and focused renderer tests.
func (c *Client) SetUIStage(stage UIStage) {
	if c != nil {
		c.uiStage = stage
	}
}

// DisplayedResources is the presentation-owned stock pair. It never replaces
// the authoritative EconomyView [05 R-ECO-01 §6][07 R-HUD-03 §4].
type DisplayedResources struct {
	Energy, Metal float32
}

// BeginPresentationFrame advances host-frame presentation state exactly once.
// Call after JoinPreRecord and before consuming or recording the frame. Pure
// composition and speculative recording must not call this boundary [I6].
func (c *Client) BeginPresentationFrame() {
	if c == nil {
		return
	}
	c.displayedResources = c.nextDisplayedResources()
	c.TickPresentationAudio()
}

func (c *Client) nextDisplayedResources() DisplayedResources {
	next := c.displayedResources
	if c.buffer == nil {
		return next
	}
	f := c.buffer.Current()
	if f == nil {
		return next
	}
	// A missing viewing row retains the pair; another owner is never a fallback.
	for _, live := range f.Economy {
		if live.Player != f.ViewingPlayer {
			continue
		}
		next.Energy = easeDisplayedStock(next.Energy, live.Energy, live.EnergyCapacity)
		next.Metal = easeDisplayedStock(next.Metal, live.Metal, live.MetalCapacity)
		break
	}
	return next
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
