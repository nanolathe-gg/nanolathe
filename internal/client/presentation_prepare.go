package client

import "github.com/nanolathe-gg/nanolathe/internal/drawlist"

// SetBattlePresentationPreparer installs the device owner's loading hook. It is
// called only by the window host and cleared when that host exits. No GPU work
// belongs to the client or to the asynchronous content loader (DESIGN_GPU_RENDERER
// §14.8).
func (c *Client) SetBattlePresentationPreparer(prepare func()) {
	if c != nil {
		c.prepareBattlePresentation = prepare
	}
}

// PrepareBattlePresentation finishes optional device preparation after successful
// battle binding, while the previous loading screen is still on screen. Hosts
// without a graphics device and the classic executor have no work here.
func (c *Client) PrepareBattlePresentation() {
	if c != nil && c.prepareBattlePresentation != nil {
		c.prepareBattlePresentation()
	}
}

// BattleTerrainSources returns immutable sources for both atlas scales without
// recording a frame, moving the camera, or consuming presentation random state.
// The detail admission matches detailTiles, independently of the live zoom.
func (c *Client) BattleTerrainSources() drawlist.Terrain {
	if c == nil {
		return drawlist.Terrain{}
	}
	sources := drawlist.Terrain{Terrain: c.terrain}
	if c.enhanced && c.detailArt != nil && c.terrain != nil && len(c.detailArt.Tiles) == len(c.terrain.TileSet) {
		sources.Detail = c.detailArt.Tiles
	}
	return sources
}
