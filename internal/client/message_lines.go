package client

// drawMessageLines is the master-composer message column. Unit captions use
// the no-speaker sentinel, so they draw directly at x=138 with dcb[15]; the
// same consumer also handles future chat and announcement lines [07 R-HUD-03
// §14.4].
func (c *Client) drawMessageLines() {
	if c == nil || c.fnt == nil {
		return
	}
	for i, line := range c.MessageLines() {
		if line.SpeakerSlot != 10 {
			// Real-speaker logo composition is not yet connected to the
			// committed player roster. Captions and chat carry sentinel 10;
			// leave unresolved logo art absent rather than inventing a colour.
			continue
		}
		y := 52 + i*int(c.fnt.Height)
		DrawText(c.indexed, c.width, c.height, c.fnt, line.Text, 138, y, 0, 15)
	}
}
