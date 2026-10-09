package main

import (
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

// Modern UI scale (DESIGN_INTERFACE_HUD_INPUT "Modern UI scale").
// The rail is laid out on a virtual surface 1/k of the framebuffer's height
// and magnified k times; the minimap is drawn outside that region at its
// magnified size from a picture built at that size, so it stays sharp. The
// top and bottom strips, and the overlays anchored to the viewport's left
// edge, take the bar scale and start at the magnified rail's edge.

// resolveChromeScale fixes the sidebar and bar magnification at the host presentation
// boundary; input until the next frame maps the pointer through the same value,
// which is what the player sees. It is 1 wherever the classic executor may
// replay the recording, which ignores the region markers, and for captures
// that crop the chrome at retail's fixed insets (films, `--shot-renderer
// both`, Nanolathe screen previews).
func (b *battleSession) resolveChromeScale() {
	if b == nil {
		return
	}
	b.chromeK, b.barOffsetY = 1, 0
	if b.cl == nil || !b.cl.Enhanced() || b.chromeFixed || b.preview {
		return
	}
	w, h := b.cl.Size()
	p := b.hostPreferences()
	b.chromeK = hud.ChromeScale(p.UIScale, int32(h), settings.MaxChromeScale)
	// Auto also leaves the bars the width their art and readouts are authored
	// for; a chosen size applies regardless.
	for p.UIScale == settings.ChromeScaleAuto && b.chromeK > 1 && (int32(w)-railInset(b.chromeK))/b.chromeK < hud.MinBarScaleWidth {
		b.chromeK--
	}
	b.barOffsetY = int32(h) % b.chromeK
}

// chromeScale is the sidebar magnification resolveChromeScale last fixed.
func (b *battleSession) chromeScale() int32 {
	if b == nil {
		return 1
	}
	return max(b.chromeK, 1)
}

// railRegion magnifies the rail about the framebuffer origin.
func (b *battleSession) railRegion() client.ChromeRegion {
	return client.ChromeRegion{Scale: b.chromeScale()}
}

// stripRegion is railRegion moved down by the rows a whole number of virtual
// rows leaves over, so the bottom strip ends on the framebuffer's last row. The
// bottom-anchored overlays draw in it too; the top strip uses railRegion, so
// PANELTOP's column 129 meets the magnified rail.
func (b *battleSession) stripRegion() client.ChromeRegion {
	r := b.railRegion()
	if b != nil {
		r.OffsetY = b.barOffsetY
	}
	return r
}

// railSize is the virtual surface the rail's windows are laid out on.
func (b *battleSession) railSize() (int, int) {
	if b == nil || b.cl == nil {
		return 0, 0
	}
	w, h := b.cl.Size()
	return b.railRegion().VirtualSize(w, h)
}

// railPointer maps a framebuffer pointer onto the rail's virtual surface.
func (b *battleSession) railPointer(x, y int32) (int32, int32) {
	return b.railRegion().ToVirtual(x, y)
}

// railPointerFrame maps a widget pass's pointer and events onto the rail. The
// mapped events reuse the session's buffer; the frame is consumed within the
// pass.
func (b *battleSession) railPointerFrame(frame ui.WidgetFrame) ui.WidgetFrame {
	if b == nil || b.railRegion().Identity() {
		return frame
	}
	frame.PointerX, frame.PointerY = b.railPointer(frame.PointerX, frame.PointerY)
	events := b.railEvents[:0]
	for _, event := range frame.PointerEvents {
		event.X, event.Y = b.railPointer(event.X, event.Y)
		events = append(events, event)
	}
	b.railEvents = events
	frame.PointerEvents = events
	return frame
}

// railInset is the camera's left inset beside a rail magnified k times: the
// magnified PANELSIDE covers 129k columns, and retail's 128 is that less one
// [03 §4.1][07 §6].
func railInset(k int32) int32 { return hud.ChromeRailX*k - 1 }

// syncChromeInsets widens the camera's left inset to the magnified rail, so
// the clamp keeps every playable column reachable beside it [03 §4.1]. The
// point at the viewport's centre stays there, which also re-centres the
// battle-start placement made before the first draw knew the scale.
func (b *battleSession) syncChromeInsets() {
	if b == nil || b.cam == nil {
		return
	}
	want := camera.ChromeInsets{}
	if k := b.chromeScale(); k > 1 {
		want = camera.ChromeInsets{Left: railInset(k), Top: camera.OriginY * k, Bottom: camera.OriginY * k}
	}
	if b.cam.Chrome == want {
		return
	}
	ox, oz := b.cam.BattleViewOrigin()
	w, h := b.cam.BattleView()
	b.cam.Chrome = want
	b.cam.JumpToBattleViewCenter(ox+w/2, oz+h/2)
	// The new viewport is an immediate layout change, so the camera samples
	// must frame its retained centre rather than blend from the old layout
	// (DESIGN_GPU_RENDERER §13.5; DESIGN_INTERFACE_HUD_INPUT
	// "Modern UI scale").
	if b.cl != nil {
		b.cl.SnapCameraBlend()
	}
}
