package client

import (
	"image"
	"image/color"
	"math"
	"sort"

	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
)

// All dimensions below are independently authored presentation geometry
// (DESIGN_GPU_RENDERER §18), in a 32px source tile with transparent gutters.
// Supersampling produces disjoint coverage weights, never RGBA color pixels.
const strategicIconSourceSize = 32

func strategicArtKey(d StrategicIconDescriptor) string {
	return d.Family + "/" + d.Role + "/" + d.Subtype
}
func makeStrategicIconAtlas(descriptors []StrategicIconDescriptor) (*drawlist.MarkerAtlas, map[string]drawlist.Rect) {
	unique := make(map[string]StrategicIconDescriptor)
	for _, d := range descriptors {
		unique[strategicArtKey(d)] = d
	}
	keys := make([]string, 0, len(unique))
	for k := range unique {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	const columns = 8
	rows := (len(keys) + columns - 1) / columns
	atlas := &drawlist.MarkerAtlas{Width: columns * strategicIconSourceSize, Height: rows * strategicIconSourceSize}
	atlas.Pixels = make([]byte, atlas.Width*atlas.Height*4)
	rects := make(map[string]drawlist.Rect, len(keys))
	for i, key := range keys {
		ox, oy := (i%columns)*strategicIconSourceSize, (i/columns)*strategicIconSourceSize
		rects[key] = drawlist.Rect{X: int32(ox), Y: int32(oy), W: strategicIconSourceSize, H: strategicIconSourceSize}
		for y := 0; y < strategicIconSourceSize; y++ {
			for x := 0; x < strategicIconSourceSize; x++ {
				var counts [4]int
				const samples = 4
				for sy := 0; sy < samples; sy++ {
					for sx := 0; sx < samples; sx++ {
						px := float64(x) - 16 + (float64(sx)+.5)/samples
						py := float64(y) - 16 + (float64(sy)+.5)/samples
						channel := strategicCoverage(unique[key], px, py)
						if channel >= 0 {
							counts[channel]++
						}
					}
				}
				offset := ((oy+y)*atlas.Width + ox + x) * 4
				for channel, n := range counts {
					atlas.Pixels[offset+channel] = byte(n * 255 / (samples * samples))
				}
			}
		}
	}
	return atlas, rects
}
func strategicCoverage(d StrategicIconDescriptor, x, y float64) int {
	if !strategicContour(d.Family, x/1.20, y/1.20) {
		return -1
	}
	if !strategicContour(d.Family, x/1.11, y/1.11) {
		return 2
	}
	if !strategicContour(d.Family, x, y) {
		return 3
	}
	if !strategicContour(d.Family, x/.84, y/.84) {
		return 0
	}
	if d.Family == "aircraft" {
		y -= 2.8
		x /= 0.86
		y /= 0.86
	}
	if d.Family == "vehicle" {
		x /= .85
		y /= .85
	}
	if strategicGlyph(d, x, y) {
		return 1
	}
	return 3
}

type strategicPoint struct{ x, y float64 }

func strategicPolygon(x, y float64, points ...strategicPoint) bool {
	in := false
	j := len(points) - 1
	for i, p := range points {
		q := points[j]
		if (p.y > y) != (q.y > y) && x < (q.x-p.x)*(y-p.y)/(q.y-p.y)+p.x {
			in = !in
		}
		j = i
	}
	return in
}
func strategicContour(family string, x, y float64) bool {
	switch family {
	case "structure":
		return math.Abs(x) <= 11.5 && math.Abs(y) <= 11.5
	case "kbot":
		return x*x+y*y <= 12*12
	case "vehicle":
		return math.Abs(x)+math.Abs(y) <= 12.5
	case "aircraft":
		return strategicPolygon(x, y, strategicPoint{0, -12}, strategicPoint{12, 10}, strategicPoint{-12, 10})
	case "hovercraft":
		return strategicPolygon(x, y, strategicPoint{-8, -10}, strategicPoint{8, -10}, strategicPoint{12, 10}, strategicPoint{-12, 10})
	case "ship":
		return strategicPolygon(x, y, strategicPoint{-11, -10}, strategicPoint{11, -10}, strategicPoint{11, 5}, strategicPoint{0, 12}, strategicPoint{-11, 5})
	case "submarine":
		dx := math.Max(math.Abs(x)-4, 0)
		return dx*dx+y*y <= 8*8
	case "commander":
		return strategicPolygon(x, y, strategicPoint{-8, -11}, strategicPoint{8, -11}, strategicPoint{12, -5}, strategicPoint{12, 5}, strategicPoint{0, 12}, strategicPoint{-12, 5}, strategicPoint{-12, -5})
	default:
		return strategicPolygon(x, y, strategicPoint{-6, -11}, strategicPoint{6, -11}, strategicPoint{12, 0}, strategicPoint{6, 11}, strategicPoint{-6, 11}, strategicPoint{-12, 0})
	}
}
func strategicLine(x, y, ax, ay, bx, by, width float64) bool {
	dx, dy := bx-ax, by-ay
	t := ((x-ax)*dx + (y-ay)*dy) / (dx*dx + dy*dy)
	t = math.Max(0, math.Min(1, t))
	dx = x - (ax + t*dx)
	dy = y - (ay + t*dy)
	return dx*dx+dy*dy <= width*width/4
}
func strategicRing(x, y, r, width float64) bool {
	a := math.Hypot(x, y)
	return math.Abs(a-r) <= width/2
}
func strategicBox(x, y, l, t, r, b float64) bool { return x >= l && x <= r && y >= t && y <= b }
func strategicBolt(x, y float64) bool {
	return strategicPolygon(x, y, strategicPoint{1, -6.5}, strategicPoint{-5, 1}, strategicPoint{-1, 1}, strategicPoint{-2, 6.5}, strategicPoint{5, -1.5}, strategicPoint{1, -1.5})
}
func strategicGlyph(d StrategicIconDescriptor, x, y float64) bool {
	line := func(ax, ay, bx, by float64) bool { return strategicLine(x, y, ax, ay, bx, by, 1.8) }
	switch d.Role {
	case "commander":
		return strategicPolygon(x, y, strategicPoint{-6, -4}, strategicPoint{-3, 0}, strategicPoint{0, -5.5}, strategicPoint{3, 0}, strategicPoint{6, -4}, strategicPoint{4.5, 4}, strategicPoint{-4.5, 4}) || line(-4.5, 6, 4.5, 6)
	case "construction", "assist":
		tool := line(-4.5, 5, 3, -2.5) || (strategicRing(x-3, y+3, 3, 2) && !(x > 3 && y < -3))
		if d.Role == "construction" && d.Subtype == "advanced" {
			// A second crossed tool makes the reviewed constructor distinction
			// visible without shrinking the family contour (design §18.7).
			tool = tool || line(4.5, 5, -3, -2.5) || (strategicRing(x+3, y+3, 3, 2) && !(x < -3 && y < -3))
		}
		return tool
	case "resurrection":
		return line(-5, 0, 5, 0) || line(0, -5, 0, 5) || (strategicRing(x, y, 7.1, 1.2) && x < -2)
	case "factory":
		if strategicBox(x, y, -6, -1, 6, 1) || strategicBox(x, y, -6, -6, -4, 1) || strategicPolygon(x, y, strategicPoint{-4, -1}, strategicPoint{0, -5}, strategicPoint{0, -1}, strategicPoint{5, -5}, strategicPoint{5, -1}) {
			return true
		}
		if d.Subtype != "" {
			return strategicContour(d.Subtype, x/.31, (y-5)/.31)
		}
		return strategicBox(x, y, -5, 4, -2, 6) || strategicBox(x, y, 2, 4, 5, 6)
	case "airbase":
		return line(-5, -5, -5, 5) || line(5, -5, 5, 5) || line(-5, 0, 5, 0)
	case "transport":
		return line(-5, -4, -5, 4) || line(5, -4, 5, 4) || line(-5, 4, 5, 4) || line(0, -6, 0, 0) || line(-2, -2, 0, 0) || line(2, -2, 0, 0)
	case "energy":
		return strategicBolt(x, y)
	case "storage":
		frame := line(-5, -6, 5, -6) || line(-5, 6, 5, 6) || line(-5, -6, -5, 6) || line(5, -6, 5, 6)
		if d.Subtype == "energy" {
			return frame || strategicBolt(x/.55, y/.65)
		}
		return frame || line(-2, 0, 2, 0) || line(-2, 3, 2, 3)
	case "extractor":
		return line(-5, -4, 5, -4) || line(0, -4, 0, 5) || line(-4, 1, 0, 5) || line(4, 1, 0, 5) || line(-5, 7, 5, 7)
	case "converter":
		return line(-5, -4, 4, -4) || line(1, -7, 4, -4) || line(-5, 4, 4, 4) || line(-5, 4, -2, 7) || strategicBox(x, y, -1, -1, 1, 1)
	case "radar", "sonar", "jammer":
		beam := line(0, 5, 0, -2) || strategicRing(x, y-5, 1.1, 1.5)
		arcs := y < 0 && (strategicRing(x, y-1, 4, 1.7) || strategicRing(x, y-1, 7, 1.7))
		if d.Role == "jammer" {
			return beam || arcs || line(-5, 6, 6, -5)
		}
		if d.Role == "sonar" {
			return line(-6, -3, 6, -3) || (y > -1 && (strategicRing(x, y+1, 3, 1.7) || strategicRing(x, y+1, 6, 1.7)))
		}
		return beam || arcs
	case "teleporter":
		return (strategicRing(x, y, 6, 1.8) && math.Abs(y) > 2) || line(-6, 0, 6, 0) || line(3, -3, 6, 0) || line(3, 3, 6, 0)
	case "targeting":
		return strategicRing(x, y, 4.2, 1.6) || line(-7, 0, -2, 0) || line(2, 0, 7, 0) || line(0, -7, 0, -2) || line(0, 2, 0, 7)
	case "spy":
		return strategicPolygon(x, y, strategicPoint{-7, 0}, strategicPoint{0, -4}, strategicPoint{7, 0}, strategicPoint{0, 4}) && !strategicRing(x, y, 2, 1.5)
	case "mine", "kamikaze":
		return strategicRing(x, y, 3, 2.5) || line(-5, -5, -3, -3) || line(3, 3, 5, 5) || line(-5, 5, -3, 3) || line(3, -3, 5, -5)
	case "fortification":
		return strategicBox(x, y, -6, -1, 6, 5) || strategicBox(x, y, -6, -5, -3, -1) || strategicBox(x, y, -1.5, -5, 1.5, -1) || strategicBox(x, y, 3, -5, 6, -1)
	case "combat":
		switch d.Subtype {
		case "interceptor":
			return strategicPolygon(x, y, strategicPoint{-5, -5}, strategicPoint{5, -5}, strategicPoint{5, 1}, strategicPoint{0, 6}, strategicPoint{-5, 1}) && !(math.Abs(x) < 2 && y < 1 && y > -3)
		case "paralyzer":
			return strategicBolt(x, y) || line(-6, -5, -4, -5) || line(4, 5, 6, 5)
		case "dropped":
			return strategicRing(x, y-2, 3.2, 2.8) || line(0, -5, 0, 0) || line(-3, -5, 3, -5)
		case "water":
			return line(-6, -3, 6, -3) || line(-4, 2, 4, 2) || line(4, 2, 1, -1) || line(4, 2, 1, 5)
		case "beam":
			return line(-5, 5, 5, -5) || line(-5, 0, 0, -5) || line(0, 5, 5, 0)
		case "ballistic":
			return strategicRing(x-3, y+3, 2, 2.2) || line(-5, 4, -3, 0) || line(-3, 0, 0, -3)
		case "propelled":
			return strategicPolygon(x, y, strategicPoint{0, -6}, strategicPoint{3, -1}, strategicPoint{3, 4}, strategicPoint{0, 2}, strategicPoint{-3, 4}, strategicPoint{-3, -1}) || line(0, 4, 0, 6)
		case "mixed":
			return line(-5, -4, 5, 4) || line(-5, 4, 5, -4)
		default:
			return line(-5, 4, 4, -5) || line(-4, -1, 1, 4) || line(-6, 6, -3, 3)
		}
	default:
		return strategicRing(x, y, 3, 1.8) || strategicBox(x, y, -1, -1, 1, 1)
	}
}

// StrategicIconPreview samples exactly the atlas weights used by the GPU,
// bilinearly at the destination pixel center, then resolves team/white/halo/
// black coverage. Background is opaque; atlas alpha is a mask, not image alpha.
func StrategicIconPreview(d StrategicIconDescriptor, size int, team, halo, background color.RGBA, selected bool) *image.RGBA {
	out := image.NewRGBA(image.Rect(0, 0, size, size))
	if size <= 0 || d.Atlas == nil {
		return out
	}
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			sx := float64(d.Rect.X) + (float64(x)+.5)*float64(d.Rect.W)/float64(size) - .5
			sy := float64(d.Rect.Y) + (float64(y)+.5)*float64(d.Rect.H)/float64(size) - .5
			ix, iy := int(math.Floor(sx)), int(math.Floor(sy))
			fx, fy := sx-float64(ix), sy-float64(iy)
			var masks [4]float64
			for dy := 0; dy < 2; dy++ {
				for dx := 0; dx < 2; dx++ {
					px := max(int(d.Rect.X), min(int(d.Rect.X+d.Rect.W)-1, ix+dx))
					py := max(int(d.Rect.Y), min(int(d.Rect.Y+d.Rect.H)-1, iy+dy))
					wx, wy := 1-fx, 1-fy
					if dx == 1 {
						wx = fx
					}
					if dy == 1 {
						wy = fy
					}
					offset := (py*d.Atlas.Width + px) * 4
					for c := range masks {
						masks[c] += float64(d.Atlas.Pixels[offset+c]) * wx * wy / 255
					}
				}
			}
			if !selected {
				masks[2] = 0
			}
			a := masks[0] + masks[1] + masks[2] + masks[3]
			mix := func(t, h, b uint8) uint8 {
				return uint8(math.Round(float64(t)*masks[0] + 255*masks[1] + float64(h)*masks[2] + float64(b)*(1-a)))
			}
			out.SetRGBA(x, y, color.RGBA{mix(team.R, halo.R, background.R), mix(team.G, halo.G, background.G), mix(team.B, halo.B, background.B), 255})
		}
	}
	return out
}
