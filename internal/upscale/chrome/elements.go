package chrome

import "math"

// Element sizes and placements below are retail's own 1x layout, measured from
// the stock art; every one scales with Style.Scale.

// TopBar is the 513x32 resource strip: plate, centre divider, METAL and ENERGY
// labels, +/- signs and the two bar grooves. The game draws the bar fill and
// numbers over it.
func (s *Style) TopBar() *Layer {
	w, h := s.px(513), s.px(32)
	l := s.Face(w, h, 0.7287)
	e := s.atLeast1(1)
	litC, darkC := white.WithA(0.25), black.WithA(0.65)
	side := func(isLit bool) RGBA {
		if isLit {
			return litC
		}
		return darkC
	}
	bottomLit, leftLit := s.Light.Y > 0, s.Light.X < 0
	l.Rect(0, 0, w-1, e-1, side(!bottomLit))
	l.Rect(0, h-e, w-1, h-1, side(bottomLit))
	l.Rect(0, 0, e-1, h-1, side(leftLit))
	// Divider: a dark line with a light line on the side away from the light.
	dx := s.px(255.5)
	l.Rect(dx, e, dx+e-1, h-1-e, black.WithA(0.6))
	lx := dx + e
	if !leftLit {
		lx = dx - 1
	}
	l.Rect(lx, e, lx, h-1-e, white.WithA(0.18))
	// Wear in the open plate either side of each label; the labels, signs and
	// grooves are drawn over it.
	s.Wear(l, "topbar-metal", s.px(38))
	s.Wear(l, "topbar-energy", s.px(287))
	s.Wear(l, "topbar-mid", s.px(250))

	for _, lab := range []struct {
		text        string
		x, w        float64
		top, bottom string
	}{{"METAL", 40, 40, "#b4d0ff", "#2a44d8"}, {"ENERGY", 289, 46, "#ffe880", "#e84a10"}} {
		g := s.GradientLabel(lab.text, s.px(lab.w), s.px(11), hex(lab.top), hex(lab.bottom))
		s.Place(l, g, 0, s.px(lab.x), s.px(8), 0.9)
	}
	for _, off := range []float64{0, 252} {
		green, red := hex("#30e040"), hex("#ff3020")
		l.Rect(s.px(221+off), s.px(9.5), s.px(226.5+off)-1, s.px(11)-1, green)
		l.Rect(s.px(223+off), s.px(7.5), s.px(224.5+off)-1, s.px(13)-1, green)
		l.Rect(s.px(221+off), s.px(19.5), s.px(226.5+off)-1, s.px(21)-1, red)
	}
	// Bar anchors from the ARM side data, relative to the strip's left edge.
	g := s.atLeast1(1)
	for _, b := range [][4]float64{{89, 12, 216, 14}, {342, 12, 469, 14}} {
		x0, y0 := s.px(b[0]), s.px(b[1])
		x1, y1 := s.px(b[2]+1)-1, s.px(b[3]+1)-1
		s.Recess(l, x0-g, y0-g, x1+g, y1+g, 0.55, 0.85, 0.22)
	}
	return l
}

var captionNormal = CaptionStyle{Fill: hex("#dfdfb7"), NearLight: hex("#b9b996"), FarLight: hex("#f6f6e2"),
	Outline: hex("#3b3d36"), OutlineR: 1.1, SX: 0.85, SY: 1.0}

// captionGreyed follows retail's disabled art: the lettering loses its cream
// for neutral greys and its outline fades from near-black to dark grey.
var captionGreyed = CaptionStyle{Fill: hex("#6b6b6b"), NearLight: hex("#4b4b4b"), FarLight: hex("#7b7b7b"),
	Outline: hex("#2b2b2b"), OutlineR: 1.1, SX: 0.85, SY: 1.0}

// faceBright is the button face brightness; retail's faces are dark steel.
const faceBright = 1.25

// pressedLift is the pressed face's brightness over the normal one. Retail's
// art doubles it (98 against 50 at 1x); from this brighter base 1.75 reads
// the same.
const pressedLift = 1.75

// bevelLight and bevelDark are the rim strengths on the lit and shaded edges.
const bevelLight, bevelDark = 0.7, 0.8

// recessSoft is the Gaussian blur on the light recesses, in 1x pixels.
const recessSoft = 0.35

// greyedDim is the greyed face's brightness against the normal one.
const greyedDim = 0.6

// State is one of a button's three authored frames.
type State int

const (
	Normal State = iota
	Pressed
	Greyed
)

// faceFor returns the face brightness, lip direction, caption style and
// caption shift for a state. Retail's pressed cues: the face lights up to
// nearly twice its brightness, the lip inverts and the caption moves one
// pixel down-right.
func (s *Style) faceFor(st State) (mult float64, sunken bool, cs CaptionStyle, shift int) {
	switch st {
	case Pressed:
		return faceBright * pressedLift, true, captionNormal, s.px(1)
	case Greyed:
		return faceBright * greyedDim, false, captionGreyed, 0
	}
	return faceBright, false, captionNormal, 0
}

// Button is a plain w×h command button (54x30) or page tab (58x18) at 1x,
// its caption centred on an 8px box whose top is captionY.
func (s *Style) Button(w, h, captionY int, text string, st State, seed string) *Layer {
	mult, sunken, cs, shift := s.faceFor(st)
	l := s.Face(s.px(float64(w)), s.px(float64(h)), mult)
	s.Bevel(l, s.px(3), bevelLight, bevelDark, 0.12, sunken)
	s.Wear(l, seed, l.W)
	limit := float64(w - 8)
	cw := s.captionWidth(text, limit)
	s.placeCaption(l, text, math.Round((float64(w)-cw)/2), float64(captionY), cw, cs, shift, 0.8)
	return l
}

// LED states for a Toggle light.
type led int

const (
	ledOff led = iota
	ledOn
	ledMixed
)

// Toggle is a w×21 order selector: a caption and a row of pill lights at its
// right end. lights holds each light's state, left to right; an empty caption
// is retail's blank pressed and greyed frames. seed keeps the wear the same
// across one selector's frames.
func (s *Style) Toggle(w int, seed, text string, lights []led, st State) *Layer {
	mult, sunken, cs, shift := s.faceFor(st)
	l := s.Face(s.px(float64(w)), s.px(21), mult)
	s.Bevel(l, s.px(3), bevelLight, bevelDark, 0.12, sunken)
	// Retail keeps the lights in the three right-hand slots of a 113 px
	// selector; wider art moves them with its right edge.
	right := float64(w - 113)
	s.Wear(l, seed, s.px(80+right))
	if text != "" {
		s.placeCaption(l, text, 7, 6, s.captionWidth(text, 74), cs, shift, 0.8)
	}
	well, wm := s.Well(s.px(7), s.px(15), 1.5*s.Scale, recessSoft*s.Scale)
	on := s.LED(s.px(4), s.px(11), hex("#0c3800"), hex("#62e01c"), RGBA{200.0 / 255, 1, 140.0 / 255, 1}, 0.85)
	mixed := s.LED(s.px(4), s.px(11), hex("#061400"), hex("#245a0c"), RGBA{200.0 / 255, 1, 140.0 / 255, 1}, 0.2)
	off := s.LED(s.px(4), s.px(11), hex("#050600"), hex("#2c3320"), RGBA{235.0 / 255, 1, 210.0 / 255, 1}, 0.30)
	first := 3 - len(lights)
	for i, state := range lights {
		x := float64(9*(first+i)) + right
		l.Over(well, s.px(85+x)-wm, s.px(3)-wm)
		lamp := off
		switch {
		case st == Greyed:
		case state == ledOn:
			lamp = on
		case state == ledMixed:
			lamp = mixed
		}
		l.Over(lamp, s.px(86.5+x), s.px(5))
	}
	return l
}

// captionWidth is a caption's box width in 1x pixels, from the word's own
// proportions, calibrated so RECLAIM fills the 42 pixels retail gives it, and
// capped at limit.
func (s *Style) captionWidth(text string, limit float64) float64 {
	ratio := func(t string) float64 { m := s.textMask(s.CaptionFont, t, 0); return float64(m.W) / float64(m.H) }
	return math.Min(limit, math.Round(42*ratio(text)/ratio("RECLAIM")))
}

// PageArrow is the build menu's 45x17 previous or next page button: a plate
// with a 45-degree point at one end, in its normal, pressed or greyed state.
func (s *Style) PageArrow(next bool, st State) *Layer {
	w, h := s.px(45), s.px(17)
	mult, sunken, _, _ := s.faceFor(st)
	l := s.Face(w, h, mult)
	seed := "PREV"
	if next {
		seed = "NEXT"
	}
	s.Wear(l, seed, w-s.px(10))
	// The point: inside where the distance from the pointed end exceeds the
	// distance from the vertical centre line, so it closes at 45 degrees.
	mask, edge := ArrowMask(w, h, next), float64(s.px(2))
	s.BevelShape(l, mask, edge, bevelLight, bevelDark, 0.12, sunken)
	return l
}

// placeCaption centres a taller caption on its 8px authored box.
func (s *Style) placeCaption(l *Layer, text string, x, y, boxW float64, cs CaptionStyle, shift int, shadow float64) {
	c, pad := s.Caption(text, s.px(boxW), s.px(8), cs)
	ch := c.H - 2*pad
	top := s.px(y) - (ch-s.px(8))/2
	s.Place(l, c, pad, s.px(x)+shift, top+shift, shadow)
}
