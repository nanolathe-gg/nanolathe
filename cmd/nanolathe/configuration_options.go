package main

import (
	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

// configurationUnavailable describes an existing consumer's boundary, without
// changing the preference or selecting any gameplay rule. Both configuration
// surfaces use the same answers (DESIGN_INTERFACE_HUD_INPUT §§3.3, 3.15, 3.17
// and "Modern radar dots"). A registered set keeps its base's camera policy.
func configurationUnavailable(key string, mode gameplay.Mode, p settings.Presentation) string {
	base := session.BaseModeOf(mode)
	enhanced := p.Renderer != "classic"
	switch key {
	case "zoomstyle", "zoomlock", "iconstyle":
		if base == gameplay.Strict31 {
			return "Strict 3.1 keeps legacy camera controls."
		}
		if base == gameplay.Community39 && p.Overview == settings.OverviewMegamap {
			return "Select Tab: Options first."
		}
		// Classic still honors No zoom; Smooth and Steps both retain its
		// native F9 scale cycle. Only the other two rows require free zoom.
		if key != "zoomstyle" {
			if !enhanced {
				return "Requires the Enhanced renderer."
			}
			if p.ZoomStyle == settings.ZoomNone {
				return "Enable camera zoom first."
			}
		}
	case "tab":
		if base == gameplay.Modern {
			if !enhanced {
				return "Tab is fixed to Options."
			}
			if p.ZoomStyle == settings.ZoomNone {
				return "Enable camera zoom first."
			}
		}
	case "radardots":
		if !session.RuleSetForMode(mode).Visibility.MainViewRadarDots() {
			return "Requires rules that enable Modern radar dots."
		}
		if !enhanced {
			return "Requires the Enhanced renderer."
		}
	case "sidebar", "uiscale", "builddrag", "fps", "glow", "water", "lights", "finish", "heat", "marks":
		if !enhanced {
			return "Requires the Enhanced renderer."
		}
	case "snapkey":
		// The modifier also cancels queued-order dragging, including under
		// Strict (DESIGN_INTERFACE_HUD_INPUT "Community order-position gestures").
		if base == gameplay.Strict31 && p.QueuedOrderDrag == 0 {
			return "Enable Order drag first."
		}
	}
	return ""
}

// configurationValueUnavailable handles choices which share an otherwise
// usable row. Classic's Steps value has no consumer distinct from its ordinary
// 1x/2x scale cycle; No zoom remains usable (interface design §3.8).
func configurationValueUnavailable(key string, value int, mode gameplay.Mode, p settings.Presentation) string {
	if reason := configurationUnavailable(key, mode, p); reason != "" {
		return reason
	}
	if key == "zoomstyle" && value == settings.ZoomStepped && p.Renderer == "classic" {
		return "Stepped free zoom requires the Enhanced renderer."
	}
	return ""
}

// A battle may be running a restored or registered rule set different from
// the front-end selection. Availability follows that bound configuration.
func (g *gameShell) configurationMode() gameplay.Mode {
	if b := g.battleOptionsSession(); b != nil && b.sess != nil {
		if b.sess.Rules.Name != "" {
			return gameplay.Mode(b.sess.Rules.Name)
		}
		return b.sess.Gameplay
	}
	return g.gameplay
}

// Resolve front-end availability from the same content, player and command-line
// sources as battle entry. A battle already owns the resolved table; reading it
// also retains registered overrides and Strict's empty-table bypass.
func (g *gameShell) configurationFeatures() community.Features {
	if b := g.battleOptionsSession(); b != nil && b.sess != nil {
		return b.sess.Community
	}
	sources := session.CommunitySources{Player: g.gameplayFeatures, CommandLine: g.opts.GameplayOverrides}
	if g.cs != nil {
		sources.Content = g.cs.gameplayFeatures
	}
	features, err := session.ResolveCommunity(g.gameplay, sources)
	if err != nil {
		// The same invalid declaration prevents battle entry. Do not offer an
		// active control by guessing a substitute feature table.
		return community.Features{}
	}
	return features
}

// Keep saved stages intact while applying the widget's ordinary disabled hit
// gate. Help states why it is unavailable and returns to its ordinary text when
// the consumer becomes usable again.
func syncConfigurationOption(panel *ui.Panel, name, reason, help string) {
	if panel == nil {
		return
	}
	retailGreyGadget(panel.Window, name, reason != "")
	if reason != "" {
		help = reason
	}
	panel.SetHelp(name, help)
}

// Recheck the current page before widget input. Rule changes are committed at
// the command boundary after their callback, so the next input frame must not
// retain the prior mode's disabled controls. This updates no staged values.
func (g *gameShell) syncConfigurationAvailability() {
	g.syncNanolatheAvailability()
	g.syncBuilderAvailability()
	g.syncCommunityPlacementAvailability()
	g.syncCommunityHUDAvailability()
}
