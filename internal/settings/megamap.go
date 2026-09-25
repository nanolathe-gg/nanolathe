package settings

// The optional megamap overview's host preferences
// (DESIGN_INTERFACE_HUD_INPUT §3.15). They mirror the ProTA 4.8 draw engine's
// preference keys ([draw-engine-interface](../../research/extensions/draw-engine-interface.md#prota-48-shipped-megamap))
// and never select gameplay [I6].

// Overview values: the smooth-zoom overview (today's behaviour) or the
// megamap.
const (
	OverviewZoom    = 0
	OverviewMegamap = 1
)

// DefaultPlayerDotColors are the draw engine's own `Player1..10DotColors`
// defaults, indexed by a player's logo colour. ProTA 4.8's INI replaces them
// with 227, 249, 18, 250, 67, 149, 208, 117, 210, 34, which the ProTA controls
// preset can apply.
var DefaultPlayerDotColors = [10]int{227, 212, 80, 235, 108, 219, 208, 93, 130, 67}

// normalizeMegamap repairs hand-edited megamap values: booleans keep their low
// bit, a negative ring minimum becomes zero, and a dot colour outside the
// palette falls back to its default slot.
func (p *Presentation) normalizeMegamap() {
	if p.Overview != OverviewMegamap {
		p.Overview = OverviewZoom
	}
	for _, value := range []*int{&p.MegamapWheel, &p.MegamapWheelMove, &p.MegamapDoubleClickMove, &p.MegamapFlash} {
		if *value < 0 {
			*value = 0
		} else {
			*value &= 1
		}
	}
	for _, value := range []*int{&p.MegamapRadarMinimum, &p.MegamapSonarMinimum, &p.MegamapSonarJamMinimum, &p.MegamapAntiNukeMinimum} {
		if *value < 0 {
			*value = 0
		}
	}
	for i, value := range p.PlayerDotColors {
		if value < 0 || value > 255 {
			p.PlayerDotColors[i] = DefaultPlayerDotColors[i]
		}
	}
}
