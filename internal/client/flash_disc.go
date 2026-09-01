package client

import (
	"math"

	"github.com/nanolathe/nanolathe/internal/render"
)

// The calculated (procedural) explosion frames of [06 R-WFX-01 §2].
//
// Every impact allocates one explosion-pool record with TWO cursors: a primary
// over the weapon's named art, and a secondary over one of three procedurally
// generated tables built once per battle. The named art always composes over
// the calculated disc, and a weapon with no art holder — a misspelled
// `explodeas`, or record 0 — still shows the disc, which is why an impact is
// never nothing.
//
// The three tables:
//
//	table 0 — 12 frames, sides 64, 60, … 20   (every ordinary impact)
//	table 1 — 15 frames, sides 128 down by 7  (built, never drawn by any caller)
//	table 2 — 15 frames, sides 200 down by 11 (the `explode` opcode's bitmap bits)
//
// Every frame's hold is 2 ticks and no table loops, so table 0 plays for 24
// ticks and the other two for 30. Table 1 is built here even though nothing
// draws it: the census is what makes "nothing passes table 1" a finding rather
// than an omission, and building it costs only memory.
const (
	flashTableCount   = render.FlashTableCount
	flashTransparent  = 0xFF
	flashRingIndex    = 0x6E
	flashRampTop      = 0x6F
	flashOpaqueCutoff = 0x22 // (0x20 − v) at or above this is transparent
	flashRingCutoff   = 0x20 // and at or above this is the ring index
)

// flashDisc is one generated frame: an N×N indexed square whose bytes are the
// disc's intensity ramp and 0xFF outside it. The offsets are both H, which is
// how the blit centres the disc on the impact point.
type flashDisc struct {
	Side   int
	Offset int
	Pixels []uint8
}

// buildFlashDisc generates one frame of side n [06 R-WFX-01 §2], spending one
// CRT draw per pixel.
//
// For row y and column x, both 0..n−1, with H = n/2 truncated:
//
//	dy = H − y ;  A  = dy·dy·1.33
//	dx = H − x
//	r  = crtRand()·10 / 0x8000            (0..9, the fuzzy edge)
//	v  = trunc(((r + sqrt(dx·dx + A)) / H) · 32)
//	c  = (0x20 − v) as an unsigned byte
//	    c ≥ 0x22 → transparent
//	    c ≥ 0x20 → the ring index 0x6E
//	    else     → c + 0x4F, i.e. the ramp 0x4F..0x6E
//
// The 1.33 multiplies the **row** term, so the disc is an ellipse compressed
// vertically by sqrt(1.33) — wider than it is tall, which is what an explosion
// on a sheared ground plane should be. There IS a division by H before the ×32.
// Both points contradict [03 §4.3.1], which put the factor on the x term and
// stated "the multiplier is ×32 with no division"; that section is corrected.
func buildFlashDisc(n int, crt *flashRand) flashDisc {
	h := n / 2
	d := flashDisc{Side: n, Offset: h, Pixels: make([]uint8, n*n)}
	if n <= 0 || h <= 0 || crt == nil {
		return d
	}
	hf := float64(h)
	for y := 0; y < n; y++ {
		dy := hf - float64(y)
		a := dy * dy * 1.33
		row := y * n
		for x := 0; x < n; x++ {
			dx := hf - float64(x)
			r := float64(crt.Rand() * 10 / 0x8000)
			// __ftol truncates toward zero [01 §8].
			v := int32(((r + math.Sqrt(dx*dx+a)) / hf) * 32.0)
			c := uint8(int32(0x20) - v)
			switch {
			case c >= flashOpaqueCutoff:
				d.Pixels[row+x] = flashTransparent
			case c >= flashRingCutoff:
				d.Pixels[row+x] = flashRingIndex
			default:
				d.Pixels[row+x] = c + 0x4F
			}
		}
	}
	return d
}

// buildFlashTable generates one whole table in frame order.
func buildFlashTable(table int, crt *flashRand) []flashDisc {
	sides := render.FlashTableSides(table)
	out := make([]flashDisc, 0, len(sides))
	for _, n := range sides {
		out = append(out, buildFlashDisc(n, crt))
	}
	return out
}

// flashTables holds the three generated tables for one client.
type flashTables struct {
	built  bool
	tables [flashTableCount][]flashDisc
}

// ensureFlashTables generates the tables once.
//
// Retail builds them during the world rebuild on the loading worker thread's
// OWN CRT state, so the 391,606 draws never touch the main thread's stream
// [06 R-WFX-01 §2][01 §6]. This build honours the same separation with a
// dedicated CRT rather than the session's, which is what keeps a
// presentation-side texture out of authoritative determinism.
//
// TODO(question): the worker's seed. Retail's disc noise comes from whatever
// state that thread's CRT held during the rebuild, which the trace does not
// name; the per-pixel draw only jitters the disc edge by up to nine
// thirty-seconds of a radius, so the shape is established and the exact noise
// is not. A trace of the loading worker's CRT seeding would settle it.
func (c *Client) ensureFlashTables() *flashTables {
	if c == nil {
		return nil
	}
	if c.flash.built {
		return &c.flash
	}
	c.flash.built = true
	crt := flashRand{state: flashTableSeed}
	for table := 0; table < flashTableCount; table++ {
		c.flash.tables[table] = buildFlashTable(table, &crt)
	}
	return &c.flash
}

// flashTableSeed is this build's dedicated seed for the disc noise; see
// ensureFlashTables' open question.
const flashTableSeed uint32 = 1

// flashRand is the disc builder's own generator. It is the CRT recurrence of
// [01 §7.2] — `state = state*214013 + 2531011`, value `(state >> 16) & 0x7FFF`
// — written here rather than taken from internal/sim/rng on purpose, and the
// reason is retail's, not a lint's: these draws happen on the loading worker
// thread's own state and never touch either simulation stream
// [06 R-WFX-01 §2][01 §6]. A private generator is what that separation looks
// like in a presentation package, and it is why this file needs no entry on
// the DET-01 (a) allowlist.
type flashRand struct {
	state uint32
	drawn int
}

func (r *flashRand) Rand() int32 {
	r.state = r.state*214013 + 2531011
	r.drawn++
	return int32((r.state >> 16) & 0x7FFF)
}

// Draws reports how many values have been taken, which is what lets the table
// geometry be checked against the published per-table draw census.
func (r *flashRand) Draws() int { return r.drawn }

// flashFrame returns one frame of one table, or nil when either index is out
// of range. A cursor past the end clamps, as the art resolver's does: it
// belongs to a sequence the pool is about to retire.
func (c *Client) flashFrame(table int, frameIndex int32) *flashDisc {
	if c == nil || table < 0 || table >= flashTableCount {
		return nil
	}
	tables := c.ensureFlashTables()
	if tables == nil {
		return nil
	}
	frames := tables.tables[table]
	if len(frames) == 0 {
		return nil
	}
	if frameIndex < 0 {
		frameIndex = 0
	}
	if int(frameIndex) >= len(frames) {
		frameIndex = int32(len(frames) - 1)
	}
	return &frames[frameIndex]
}

// drawCalculatedFlash composites one generated disc frame at a screen point
// [06 R-WFX-01 §2][03 §4.3.1].
//
// The disc is not blitted as colour. Its bytes encode intensity, and each
// opaque byte brightens the pixel already under it through the `LHT` table:
// `level = clamp(discByte − 0x50, 0, 31)`, then `dst = LHT[level][dst]`. That
// is the same lit-ground halo the section describes around explosions and
// muzzle flashes, driven by the traced texture rather than by a flat radius.
//
// The frame's own offsets are both H, so the disc centres on the impact point.
//
// The pass this runs in is the right one, and [03 §4.3.1] is what was wrong
// about it. That section said the halo is "composited after the flat tile pass
// and before shadows, units, and fog"; the composer's own call sequence puts
// the explosion pool's draw — both of its walks — at stage 7, after every unit
// traversal, exactly where [03 §1] lists "the fixed effect pool". The section
// is corrected; a previous version of this comment recorded our placement as a
// divergence, and it was not one.
//
// TODO(question): whether the disc composites as a BRIGHTENING or as opaque
// colour. [03 §4.3.1] states the first in detail — each opaque byte replaces
// the pixel under it with `LHT[level·256 + src]`, and gives the
// `discByte→level` mapping used here — but the explosion pool's first walk
// goes through a blitter distinct from the second's, and no read of the LHT
// table was found inside it. If that blitter simply writes the disc's bytes,
// the disc is an opaque 0x4F..0x6E ramp rather than a brightening, and every
// impact is far brighter than it is here. A trace of that blitter's span
// writer settles it; the LHT reading is kept meanwhile because it is the one
// the research states outright.
func (c *Client) drawCalculatedFlash(table int, frameIndex int32, cx, cy int, coverage func(x, y int) bool) bool {
	if c == nil || c.pal == nil || coverage == nil {
		return false
	}
	d := c.flashFrame(table, frameIndex)
	if d == nil || d.Side <= 0 {
		return false
	}
	drew := false
	for row := 0; row < d.Side; row++ {
		py := cy + row - d.Offset
		if py < 0 || py >= c.height {
			continue
		}
		base := row * d.Side
		for col := 0; col < d.Side; col++ {
			b := d.Pixels[base+col]
			if b == flashTransparent {
				continue
			}
			px := cx + col - d.Offset
			if px < 0 || px >= c.width {
				continue
			}
			if !coverage(px, py) {
				continue
			}
			level := int(b) - 0x50
			if level < 0 {
				level = 0
			} else if level > 31 {
				level = 31
			}
			idx := py*c.width + px
			c.indexed[idx] = c.pal.LightLookup(level, c.indexed[idx])
			drew = true
		}
	}
	return drew
}
