package client

// SetMoviePalette installs the movie's physical RGB entries without world
// gamma/lighting conversion [08 R-OOS-01 §4][fmt zrb]. A nil palette restores
// the regular display palette, including the user's gamma setting. The same
// display table feeds the classic expansion and the GPU executor.
func (c *Client) SetMoviePalette(p *[256][4]byte) {
	if c == nil {
		return
	}
	if p == nil {
		c.rebuildDisplayPalette()
		return
	}
	c.base = *p
}
