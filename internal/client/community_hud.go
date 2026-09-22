package client

import (
	"fmt"

	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
)

// CommunityHUDOptions controls optional host-side labels sourced from the
// Community patch. The zero value keeps the three added displays off and
// preserves Nanolathe's existing always-on nonzero group digit [CP-WPN-7]
// [CP-UD-1]. These switches are presentation preferences and do not select a
// gameplay rule set or enter a simulation fingerprint [I6].
type CommunityHUDOptions struct {
	Counters            bool
	ReloadBars          bool
	VeteranLabel        bool
	DisableGroupNumbers bool
}

// SetCommunityHUDOptions replaces the presentation-only Community HUD
// preferences.
func (c *Client) SetCommunityHUDOptions(options CommunityHUDOptions) {
	if c == nil {
		return
	}
	if c.communityHUD == options {
		return
	}
	c.JoinPreRecord()
	c.communityHUD = options
	c.pausedWorldRevision++
	c.BumpPresentationEpoch()
}

// CommunityHUDOptions returns the current presentation-only preferences.
func (c *Client) CommunityHUDOptions() CommunityHUDOptions {
	if c == nil {
		return CommunityHUDOptions{}
	}
	return c.communityHUD
}

type communityCounterLabels struct {
	stockpile string
	transport string
}

// communityCounters returns the two source labels. The stockpile label is the
// sum of completed rounds plus the positive queued amount. A transport label
// is absent for an empty carrier and for the one-seat flying transport class
// (docs/DESIGN_INTERFACE_HUD_INPUT.md §3.14).
func communityCounters(u *frame.UnitView) communityCounterLabels {
	if u == nil {
		return communityCounterLabels{}
	}
	var out communityCounterLabels
	if u.CommunityHUD.StockpileCount != 0 || u.CommunityHUD.StockpileQueued != 0 {
		out.stockpile = fmt.Sprintf("%d +%d", u.CommunityHUD.StockpileCount, u.CommunityHUD.StockpileQueued)
	}
	if u.CommunityHUD.TransportCapacity != 0 &&
		!u.CommunityHUD.SingleUnitFlyingTransport &&
		u.CommunityHUD.TransportCount != 0 {
		out.transport = fmt.Sprintf("%d/%d", u.CommunityHUD.TransportCount, u.CommunityHUD.TransportCapacity)
	}
	return out
}

// communityReload selects the first tagged slot with the largest nonzero
// authored reload time. Stockpile slots are excluded. The returned progress is
// elapsed reload, clamped to zero when the remaining count exceeds the authored
// time [CP-WPN-7].
func communityReload(u *frame.UnitView) (elapsed, total uint16, ok bool) {
	if u == nil || u.BuildRemaining != 0 {
		return 0, 0, false
	}
	var selected *frame.UnitWeaponHUDView
	for i := range u.CommunityHUD.Weapons {
		weapon := &u.CommunityHUD.Weapons[i]
		if !weapon.Tagged || weapon.Stockpile || weapon.ReloadTime == 0 {
			continue
		}
		if selected == nil || weapon.ReloadTime > selected.ReloadTime {
			selected = weapon
		}
	}
	if selected == nil {
		return 0, 0, false
	}
	if selected.Reload <= selected.ReloadTime {
		elapsed = selected.ReloadTime - selected.Reload
	}
	return elapsed, selected.ReloadTime, true
}

func (c *Client) drawCommunityUnitHUD(u *frame.UnitView, centerX, healthY int32) {
	if c == nil || u == nil || centerX == 0 || healthY == 0 || u.Health <= 0 {
		return
	}
	if c.communityHUD.Counters && c.fnt != nil {
		labels := communityCounters(u)
		fontHeight := int32(c.fnt.Height)
		if fontHeight <= 0 {
			fontHeight = 8
		}
		textY := healthY - fontHeight - 3
		if labels.stockpile != "" {
			c.drawCommunityCounter(labels.stockpile, centerX, textY)
		}
		if labels.transport != "" {
			if labels.stockpile != "" {
				textY -= fontHeight + 1
			}
			c.drawCommunityCounter(labels.transport, centerX, textY)
		}
	}
	if c.communityHUD.ReloadBars {
		if elapsed, total, ok := communityReload(u); ok {
			c.drawCommunityReloadBar(centerX, healthY+c.viewScale().Px(3), elapsed, total)
		}
	}
}

func (c *Client) drawCommunityCounter(text string, centerX, y int32) {
	if text == "" || c.fnt == nil {
		return
	}
	x := centerX - int32(MeasureText(c.fnt, text))/2
	// The source stamps a black one-pixel outline in all eight neighboring
	// positions, followed by the configured foreground. Nanolathe adopts its
	// default raw palette index 255 for this compact on/off host option.
	for dx := int32(-1); dx <= 1; dx++ {
		for dy := int32(-1); dy <= 1; dy++ {
			if dx == 0 && dy == 0 {
				continue
			}
			c.emitGlyphs(drawlist.Glyphs{Font: c.fnt, Text: text, X: x + dx, Y: y + dy, Color: 0})
		}
	}
	c.emitGlyphs(drawlist.Glyphs{Font: c.fnt, Text: text, X: x, Y: y, Color: 255})
}

func (c *Client) drawCommunityReloadBar(centerX, topY int32, elapsed, total uint16) {
	if total == 0 {
		return
	}
	if elapsed > total {
		elapsed = total
	}
	s := c.viewScale()
	left, right, bottom := centerX-s.Px(17), centerX+s.Px(17), topY+s.Px(4)
	background := c.paletteIndex(0)
	c.emitFillInclusive(left, topY, right, bottom, background)
	c.emitFillInclusive(left+s.Px(1), topY+s.Px(1), right-s.Px(1), bottom-s.Px(1), background)
	fillWidth := int32((uint32(s.Px(32)) * uint32(elapsed)) / uint32(total))
	if fillWidth == 0 {
		return
	}
	progress := (uint32(elapsed) * 100) / uint32(total)
	shadeStep := progress / 15
	if shadeStep > 6 {
		shadeStep = 6
	}
	fill := c.paletteIndex(uint8(144 - shadeStep))
	c.emitFillInclusive(left+s.Px(1), topY+s.Px(1), left+s.Px(1)+fillWidth, bottom-s.Px(1), fill)
}
