package client

import "github.com/nanolathe/nanolathe/internal/frame"

// UIFrame is the immutable presentation value handed to the one UI stage
// after the committed-world passes. Committed is valid only for the duration
// of DrawUI; the frame buffer may reuse it after the draw returns [I6].
//
// Keeping the boundary typed prevents a UI stage from reaching through the
// client into a live session or installing another renderer. The client owns
// when this stage runs; the stage only supplies authored UI pixels.
type UIFrame struct {
	Committed *frame.Frame
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
