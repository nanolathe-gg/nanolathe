package film

import (
	"fmt"
	"image"
	"image/color"
	"maps"
	"math"
	"slices"
	"strings"
)

// A Cue is one animated block of overlay text. Its timing is in simulation
// ticks measured from the start of the shot that owns it, and is evaluated at
// the presented frame's fractional tick, so an animation is as smooth as the
// capture rate rather than stepping at 30 Hz.
//
// Sizes and anchors are fractions of the frame, never pixels: the same script
// composes the same picture at 720p and 4K.
type Cue struct {
	At    float64  `json:"at"`
	Ticks float64  `json:"ticks"`
	In    float64  `json:"in"`
	Out   float64  `json:"out"`
	Style string   `json:"style"`
	Anim  string   `json:"anim"`
	Lines []string `json:"lines"`
	Size  float64  `json:"size"`
	X     float64  `json:"x"`
	Y     float64  `json:"y"`
	Align string   `json:"align"`
	Color []uint8  `json:"color"`
	Rule  bool     `json:"rule"`
}

// cueStyle is a named preset: the anchor, alignment and size a cue takes when
// it does not name its own.
type cueStyle struct {
	size     float64 // cap height as a fraction of frame height
	x, y     float64 // anchor, fractions of the frame
	align    string
	tracking float64
	weight   float64
}

var cueStyles = map[string]cueStyle{
	"title":    {size: 0.082, x: 0.5, y: 0.46, align: "center", tracking: 0.13, weight: 0.085},
	"subtitle": {size: 0.030, x: 0.5, y: 0.60, align: "center", tracking: 0.22, weight: 0.10},
	"lower":    {size: 0.038, x: 0.085, y: 0.80, align: "left", tracking: 0.05, weight: 0.095},
	"caption":  {size: 0.026, x: 0.5, y: 0.90, align: "center", tracking: 0.10, weight: 0.10},
}

var cueAnims = map[string]bool{"fade": true, "rise": true, "wipe": true, "type": true}

// Validate reports a cue a capture cannot draw. An unknown style or animation
// is a script error rather than a silent default: a title that quietly does
// not appear costs a whole re-render to notice.
func (c Cue) Validate() error {
	if len(c.Lines) == 0 {
		return fmt.Errorf("text cue at %.1f has no lines", c.At)
	}
	if c.Ticks <= 0 {
		return fmt.Errorf("text cue %q has no duration", c.Lines[0])
	}
	if _, ok := cueStyles[c.style()]; !ok {
		return fmt.Errorf("text cue %q: unknown style %q, expected one of %s", c.Lines[0], c.Style, strings.Join(slices.Sorted(maps.Keys(cueStyles)), ", "))
	}
	if !cueAnims[c.anim()] {
		return fmt.Errorf("text cue %q: unknown animation %q, expected one of %s", c.Lines[0], c.Anim, strings.Join(slices.Sorted(maps.Keys(cueAnims)), ", "))
	}
	if len(c.Color) != 0 && len(c.Color) != 3 && len(c.Color) != 4 {
		return fmt.Errorf("text cue %q: colour wants three or four components, got %d", c.Lines[0], len(c.Color))
	}
	return nil
}

func (c Cue) style() string {
	if c.Style == "" {
		return "title"
	}
	return c.Style
}

func (c Cue) anim() string {
	if c.Anim == "" {
		return "fade"
	}
	return c.Anim
}

func (c Cue) colour() color.RGBA {
	out := color.RGBA{R: 0xf2, G: 0xf4, B: 0xf7, A: 0xff}
	if len(c.Color) >= 3 {
		out.R, out.G, out.B = c.Color[0], c.Color[1], c.Color[2]
	}
	return out
}

// envelope reports the cue's opacity and its animation progress at shot time
// t, or false when the cue is not on screen. Progress runs 0..1 across the
// in-animation and stays at 1 afterwards.
func (c Cue) envelope(t float64) (alpha, progress float64, on bool) {
	local := t - c.At
	if local < 0 || local >= c.Ticks {
		return 0, 0, false
	}
	in, out := c.In, c.Out
	if in <= 0 {
		in = 8
	}
	if out <= 0 {
		out = 10
	}
	alpha = 1
	if local < in {
		alpha = local / in
	}
	if remaining := c.Ticks - local; remaining < out {
		alpha = math.Min(alpha, remaining/out)
	}
	progress = 1.0
	if local < in {
		progress = local / in
	}
	return easeInOut(alpha), easeOut(progress), true
}

// Draw composes the cue onto a frame at shot time t in ticks.
func (c Cue) Draw(dst *image.RGBA, t float64) {
	alpha, progress, on := c.envelope(t)
	if !on || alpha <= 0 {
		return
	}
	preset := cueStyles[c.style()]
	bounds := dst.Bounds()
	frameH := float64(bounds.Dy())
	size := preset.size
	if c.Size > 0 {
		size = c.Size
	}
	style := TextStyle{
		Size:     size * frameH,
		Weight:   preset.weight,
		Tracking: preset.tracking,
		Color:    c.colour(),
		Shadow:   0.07,
		Halo:     0.055,
	}
	anchorX, anchorY := preset.x, preset.y
	if c.X > 0 {
		anchorX = c.X
	}
	if c.Y > 0 {
		anchorY = c.Y
	}
	align := preset.align
	if c.Align != "" {
		align = c.Align
	}

	leading := style.Size * 1.62
	originX := anchorX * float64(bounds.Dx())
	// The block is anchored on its own centre vertically, so a two-line title
	// sits where a one-line title sat.
	top := anchorY*frameH - leading*float64(len(c.Lines)-1)/2
	rise := 0.0
	if c.anim() == "rise" {
		rise = (1 - progress) * style.Size * 0.55
	}

	for i, line := range c.Lines {
		baseline := top + float64(i)*leading + rise
		text := line
		revealX := math.Inf(1)
		switch c.anim() {
		case "type":
			runes := []rune(line)
			shown := int(math.Round(progress * float64(len(runes))))
			text = string(runes[:min(shown, len(runes))])
		case "wipe":
			width := MeasureText(line, style)
			revealX = lineX(originX, width, align) + width*progress + style.Size*0.25
		}
		if text == "" {
			continue
		}
		x := lineX(originX, MeasureText(line, style), align)
		DrawText(dst, text, x, baseline, style, alpha, revealX)
	}

	if c.Rule {
		width := 0.0
		for _, line := range c.Lines {
			width = math.Max(width, MeasureText(line, style))
		}
		width *= progress
		x := lineX(originX, width, align)
		y := top - style.Size*1.25
		drawRule(dst, x, y, width, math.Max(2, style.Size*0.07), c.colour(), alpha)
	}
}

func lineX(origin, width float64, align string) float64 {
	switch align {
	case "center":
		return origin - width/2
	case "right":
		return origin - width
	default:
		return origin
	}
}

func drawRule(dst *image.RGBA, x, y, width, thickness float64, col color.RGBA, alpha float64) {
	if width <= 0 || alpha <= 0 {
		return
	}
	m := newMask(int(math.Floor(x-2)), int(math.Floor(y-thickness)), int(math.Ceil(width+4)), int(math.Ceil(thickness*2+4)))
	m.stroke(x, y, x+width, y, thickness/2)
	m.composite(dst, 1, 1, color.RGBA{A: 0xff}, alpha*0.5, math.Inf(1), 1)
	m.composite(dst, 0, 0, col, alpha, math.Inf(1), 1)
}

// easeInOut is the smoothstep the camera and the opacity ramps share.
func easeInOut(t float64) float64 {
	t = math.Max(0, math.Min(1, t))
	return t * t * (3 - 2*t)
}

func easeOut(t float64) float64 {
	t = math.Max(0, math.Min(1, t))
	return 1 - (1-t)*(1-t)
}

func easeIn(t float64) float64 {
	t = math.Max(0, math.Min(1, t))
	return t * t
}
