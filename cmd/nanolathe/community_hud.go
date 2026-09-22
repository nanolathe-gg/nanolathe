package main

import (
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
)

// communityFooterOptions is the composition boundary between the client's
// host preferences and the pure committed-frame footer builder. It remains
// independent of the gameplay profile and simulation identity
// (docs/DESIGN_COMMUNITY_PATCH.md §7) [I6].
func communityFooterOptions(c *client.Client) hud.CommunityFooterOptions {
	if c == nil {
		return hud.CommunityFooterOptions{}
	}
	options := c.CommunityHUDOptions()
	return hud.CommunityFooterOptions{VeteranLabel: options.VeteranLabel}
}
