package main

// The MAINMENU background shimmer: one hundred single-pixel sparks that crawl
// across the menu art in orthogonal three-pixel steps [07 §5 "Main-menu
// background shimmer (SPARKS) is closed"]. Presentation only [I6]: the field
// draws from its own CRT-recurrence generator and never touches a simulation
// stream.

import (
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
)

const (
	menuSparkCount = 100
	menuSparkW     = 640
	menuSparkH     = 480
	// menuSparkSpawnRows is the spawn band: sparks are born only in the top
	// 220 rows, though they may wander below it [07 §5].
	menuSparkSpawnRows = 220
	// menuSparkIndex is the PALETTE.PAL index every spark writes. It is a dark
	// green, which is why the effect reads as faint [07 §5].
	menuSparkIndex = 0xAA
	// menuSparkRate is the Nanolathe presentation cadence of the tick. Retail
	// runs it once per front-end frame with no throttle of its own [07 §5].
	// TODO(question): the retail front-end frame rate is not traced; a retail
	// capture under a 60 Hz host redraws the sparks at 60 Hz, so this cadence
	// follows it. Tracing the pacing of the host-mode-2 pump would settle it.
	menuSparkRate = 60
)

// menuSpark is one record of the retail field [07 §5].
type menuSpark struct {
	x, y   int16
	active bool
	dx, dy int8
	life   uint8
	timer  uint8
	off    int32
	// drawn marks a record whose pixel is on the plane: retail draws nothing
	// in the spawn frame.
	drawn bool
}

// menuSparkRand is the field's private copy of the CRT recurrence
// `state*214013+2531011`, value `(state>>16)&0x7FFF` [01 §7.2]. Retail draws
// from the process CRT stream; the field keeps its own so the menu cannot
// shift any other consumer [I4].
type menuSparkRand struct{ state uint32 }

func (r *menuSparkRand) rand() int32 {
	r.state = r.state*214013 + 2531011
	return int32((r.state >> 16) & 0x7FFF)
}

// menuSparks is the field for one MAINMENU window. Retail allocates it zeroed
// when the window opens and frees it when the window closes, so a new window
// starts an empty field [07 §5].
type menuSparks struct {
	owner *ui.Panel
	bg    *formats.PCX
	// backup is the pristine menu art and plane the window surface the sparks
	// draw into. Both tests read the plane, so a pixel another record drew
	// this pass (index 0xAA, low nibble 10) turns a spark away [07 §5].
	// Nanolathe composes gadgets through the draw list rather than into a
	// retained window surface, so the plane holds the background alone and
	// the tests never see gadget pixels (DESIGN_INTERFACE_HUD_INPUT C16).
	backup []byte
	plane  []byte
	sparks [menuSparkCount]menuSpark
	rng    menuSparkRand
	accum  float64
}

// newMenuSparks seeds the generator with the MSVCRT default of 1; nothing
// about the effect depends on the seed.
func newMenuSparks() *menuSparks { return &menuSparks{rng: menuSparkRand{state: 1}} }

// bind attaches the field to the MAINMENU window being shown, clearing it
// when the window or its art changed. It reports whether the field can run.
func (s *menuSparks) bind(owner *ui.Panel, bg *formats.PCX) bool {
	if owner == nil || bg == nil || int(bg.Width) != menuSparkW || int(bg.Height) != menuSparkH || len(bg.Pixels) < menuSparkW*menuSparkH {
		s.owner, s.bg = nil, nil
		return false
	}
	if s.owner == owner && s.bg == bg {
		return true
	}
	s.owner, s.bg = owner, bg
	s.backup = bg.Pixels[:menuSparkW*menuSparkH]
	if len(s.plane) != len(s.backup) {
		s.plane = make([]byte, len(s.backup))
	}
	copy(s.plane, s.backup)
	s.sparks = [menuSparkCount]menuSpark{}
	s.accum = 0
	return true
}

// advance runs the tick at menuSparkRate for delta seconds of presentation.
func (s *menuSparks) advance(delta float64) {
	s.accum += delta * menuSparkRate
	for ; s.accum >= 1; s.accum-- {
		s.tick()
	}
}

// candidate is the retail pixel test: the low nibble of the palette index is
// 13 or more. In PALETTE.PAL that selects the darkest shades of each ramp.
func (s *menuSparks) candidate(off int32) bool { return s.plane[off]&0xF >= 0xD }

// tick is one front-end frame over records 0..99 in order [07 §5].
func (s *menuSparks) tick() {
	for i := range s.sparks {
		sp := &s.sparks[i]
		if !sp.active {
			s.spawn(sp)
			continue
		}
		// Erase the previous frame's pixel before any test.
		s.plane[sp.off] = s.backup[sp.off]
		sp.drawn = false
		sp.x += int16(sp.dx)
		sp.y += int16(sp.dy)
		if sp.x < 0 || sp.x >= menuSparkW || sp.y < 0 || sp.y >= menuSparkH || sp.life == 0 {
			sp.active = false
			continue
		}
		sp.life--
		sp.off += int32(sp.dx)
		if sp.dy != 0 {
			sp.off += int32(sp.dy) * menuSparkW
		}
		if !s.candidate(sp.off) {
			sp.active = false
			continue
		}
		s.plane[sp.off] = menuSparkIndex
		sp.drawn = true
		if sp.timer != 0 {
			sp.timer--
			continue
		}
		// Turn onto the other axis. The sign comes from the parity of the
		// coordinate on the new axis, with opposite conventions for the two
		// arms and for the spawn choice below [07 §5].
		if sp.dy != 0 {
			sp.dy = 0
			sp.dx = 3
			if sp.x&1 != 0 {
				sp.dx = -3
			}
		} else {
			sp.dx = 0
			sp.dy = -3
			if sp.y&1 != 0 {
				sp.dy = 3
			}
		}
		sp.timer = uint8(s.rng.rand()&0xF) + 1
	}
}

// spawn is the inactive record's attempt: x, y, life and timer are drawn in
// that order, and a rejected position consumes only its two draws [07 §5].
func (s *menuSparks) spawn(sp *menuSpark) {
	sp.x = int16(s.rng.rand() % menuSparkW)
	sp.y = int16(s.rng.rand() % menuSparkSpawnRows)
	sp.off = int32(sp.y)*menuSparkW + int32(sp.x)
	if !s.candidate(sp.off) {
		return
	}
	sp.active = true
	// The low byte plus one wraps, so a life of 0 is possible and the spark
	// dies on its first move.
	sp.life = uint8(s.rng.rand()) + 1
	sp.timer = uint8(s.rng.rand()&0x1F) + 1
	// An odd life starts vertically and an even one horizontally; the sign
	// comes from the parity of that axis's coordinate.
	if sp.life&1 != 0 {
		sp.dx, sp.dy = 0, 3
		if sp.y&1 != 0 {
			sp.dy = -3
		}
	} else {
		sp.dx, sp.dy = 3, 0
		if sp.x&1 != 0 {
			sp.dx = -3
		}
	}
}

// draw emits every visible spark over the MAINMENU window at (ox, oy).
// Retail writes the sparks into the window's own surface, so they sit above
// its gadgets and below any window stacked on it [07 §5]; drawRetailWindow
// calls this before the Nanolathe-owned gadgets so the MODS button stays clean.
func (s *menuSparks) draw(c *client.Client, owner *ui.Panel, ox, oy int) {
	if s == nil || s.owner != owner || s.plane == nil {
		return
	}
	for i := range s.sparks {
		sp := &s.sparks[i]
		// Another record's erase can clear a pixel this one drew.
		if sp.active && sp.drawn && s.plane[sp.off] == menuSparkIndex {
			c.UIFillRect(ox+int(sp.x), oy+int(sp.y), 1, 1, menuSparkIndex)
		}
	}
}

// mainMenuPanel is the MAINMENU window in the chain, or nil.
func (g *gameShell) mainMenuPanel() *ui.Panel {
	if g == nil || g.frontend == nil {
		return nil
	}
	for _, entry := range g.frontend.Panels.Entries() {
		if !entry.Modal && entry.Panel != nil && entry.Panel.Window != nil && g.panelMode(entry.Panel) == modeMenuMain {
			return entry.Panel
		}
	}
	return nil
}

// stepMenuSparks advances the shimmer while a MAINMENU window is open.
func (g *gameShell) stepMenuSparks(delta float64) {
	if g == nil || g.assets == nil {
		return
	}
	var bg *formats.PCX
	if asset := g.assets.panel[modeMenuMain]; asset != nil {
		bg = asset.background
	}
	panel := g.mainMenuPanel()
	if panel == nil {
		return
	}
	if g.menuSparks == nil {
		g.menuSparks = newMenuSparks()
	}
	if g.menuSparks.bind(panel, bg) {
		g.menuSparks.advance(delta)
	}
}
