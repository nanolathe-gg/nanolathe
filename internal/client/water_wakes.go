package client

import (
	"math"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/model"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// Enhanced land spray is an artistic counterpart to script water sprinkles
// (GPU design §26.3). It runs authored wake routines against committed poses, never simulation RNG [I6].
const (
	wakeRingSize     = 8192
	wakeTrackerLimit = 4096
)

type surfaceWakeMark struct {
	x, z   numeric.Fixed
	vx, vz float64
	born   uint32
	life   float32
}

type surfaceWakeState struct {
	marks    []surfaceWakeMark
	next     int
	tick     uint32
	viewer   uint8
	valid    bool
	units    map[uint64]*hoverWakeScript
	sequence uint64
	arena    []drawlist.SurfaceWake
}

func (st *surfaceWakeState) push(m surfaceWakeMark) {
	if len(st.marks) < wakeRingSize {
		st.marks = append(st.marks, m)
		return
	}
	st.marks[st.next] = m
	st.next = (st.next + 1) % wakeRingSize
}

func (c *Client) dustSurface(x, z numeric.Fixed) (numeric.Fixed, bool) {
	y := c.terrain.HeightAt(x, z)
	return y, y >= 0 && y >= c.terrain.SeaLevelWorld()
}

// SetHoverScripts retains immutable authored programs only. Visual VM instances
// never receive a live unit, simulation RNG, or gameplay port (GPU design §26.3).
func (c *Client) SetHoverScripts(cat *content.Catalog) {
	if c == nil {
		return
	}
	c.CancelPreRecord()
	c.pausedWorldRevision++
	c.hoverScripts = nil
	c.wakes = surfaceWakeState{}
	if cat == nil {
		return
	}
	c.hoverScripts = make(map[uint16]*cob.Program)
	for _, def := range cat.UnitRecords() {
		if def != nil && def.CanHover && def.Script != nil {
			c.hoverScripts[uint16(def.UnitDefID)] = def.Script
		}
	}
}

type hoverWakeEmission struct {
	piece int
	kind  int32
}
type hoverWakeScript struct {
	vm        *cob.VM
	pieceMap  []int
	emissions []hoverWakeEmission
	defID     uint16
	tick      uint32
	x, z      numeric.Fixed
}

func (s *hoverWakeScript) EmitSFX(piece int, kind int32, _ cob.SFXKind) {
	if kind >= 2 && kind <= 5 && len(s.emissions) < 256 {
		s.emissions = append(s.emissions, hoverWakeEmission{piece, kind})
	}
}

func newHoverWakeScript(prog *cob.Program, m *unitModel, defID uint16) *hoverWakeScript {
	if prog == nil || m == nil || m.compiled == nil {
		return nil
	}
	// The wake emitters are linked to the model the way the unit's own script
	// is, so an emission names the model piece the simulation would compose
	// for it; a piece beyond the model links to -1 and emits nothing
	// [04 R-COB-01 §4].
	modelPieces := make([]string, len(m.compiled.Pieces))
	for i := range m.compiled.Pieces {
		modelPieces[i] = m.compiled.Pieces[i].Name
	}
	s := &hoverWakeScript{vm: cob.NewPresentationVM(prog, 4096), defID: defID, pieceMap: cob.LinkPieces(prog.Pieces, modelPieces)}
	bridge := cob.NewCallbackBridge(s.vm)
	s.vm.SetSFXSink(s)
	s.vm.SetSFXVisible(func(_ int, kind int32) bool { return kind >= 2 && kind <= 5 })
	// This isolated visual routine sees the water-surface band so the authored
	// wake loop can run on dry ground. Create and every gameplay callback stay
	// unbound; the actual unit's COB instance is untouched.
	bridge.SetSFXoccupy(2)
	bridge.StartMoving()
	return s
}

// placeSurfaceWakes runs the authored wake loop once per committed tick. It
// admits emissions only after a change in consecutive committed X/Z positions.
// Hover bob and turning in place do not stir up land spray (GPU design §26.3).
func (c *Client) placeSurfaceWakes(cur *frame.Frame) {
	if c == nil || cur == nil || c.terrain == nil || !c.enhanced {
		return
	}
	st := &c.wakes
	if st.valid && st.tick == cur.Tick && st.viewer == cur.ViewingPlayer {
		return
	}
	// A pinned pass may read a tick older than the host has already observed
	// (§13.13); the wake history only moves forward.
	if c.observesInOrder() && st.valid && cur.Tick < st.tick && st.viewer == cur.ViewingPlayer {
		return
	}
	if st.valid && (cur.Tick != st.tick+1 || st.viewer != cur.ViewingPlayer) {
		*st = surfaceWakeState{}
	}
	st.tick, st.viewer, st.valid = cur.Tick, cur.ViewingPlayer, true
	if st.units == nil {
		st.units = make(map[uint64]*hoverWakeScript)
	}
	for i := range cur.Units {
		u := &cur.Units[i]
		if u.InstanceID == 0 || u.IsBuilding || !u.CanHover ||
			u.MoverMode != moverModeGrounded || isCarried(*u) || u.BuildRemaining > 0 ||
			!unitVisibleForFrame(cur, *u, cur.ViewingPlayer) {
			continue
		}
		if _, ok := c.dustSurface(u.X, u.Z); !ok {
			continue
		}
		m := c.modelForUnit(*u)
		if m == nil {
			continue
		}
		script := st.units[u.InstanceID]
		moving := false
		if script == nil || script.defID != u.DefID {
			if len(st.units) >= wakeTrackerLimit {
				continue
			}
			script = newHoverWakeScript(c.hoverScripts[u.DefID], m, u.DefID)
			if script == nil {
				continue
			}
			st.units[u.InstanceID] = script
		} else {
			moving = u.X != script.x || u.Z != script.z
			script.vm.Drain(1)
		}
		script.tick, script.x, script.z = cur.Tick, u.X, u.Z
		if !moving {
			// Stock StopMoving callbacks may leave wake loops running. Gate
			// their visual output here; already emitted specks keep fading.
			script.emissions = script.emissions[:0]
			continue
		}
		if len(script.emissions) == 0 {
			continue
		}
		states := c.modelStates(m, u.Pieces)
		model.FoldRootAngles(states, m.compiled.Root, u.Heading, u.Pitch, u.Bank)
		for _, ev := range script.emissions {
			if ev.piece < 0 || ev.piece >= len(script.pieceMap) {
				continue
			}
			piece := script.pieceMap[ev.piece]
			if piece < 0 || piece >= len(m.compiled.Pieces) {
				continue
			}
			vertices := m.compiled.Pieces[piece].Vertices
			if len(vertices) < 2 {
				continue
			}
			xf := model.Compose(m.compiled, states, piece)
			a, b := xf.Apply(vertices[0]), xf.Apply(vertices[1])
			if ev.kind >= 4 {
				a, b = b, a
			}
			// COB vector geometry adds X/Y and subtracts model Z [04 R-COB-03 §6].
			x, z := u.X+a[0], u.Z-a[2]
			dx, dy, dz := float64(b[0]-a[0]), float64(b[1]-a[1]), float64(a[2]-b[2])
			distance := math.Sqrt(dx*dx + dy*dy + dz*dz)
			if distance == 0 {
				continue
			}
			st.sequence++
			// White 2x2 specks follow the water spray's half-unit step. Density,
			// finite life, opacity and cue-local scatter are artistic choices; no RNG.
			for n := uint64(0); n < 2; n++ {
				phase := st.sequence*2 + n
				px := x + numeric.Fixed(int64(phase*5%17)-8)*numeric.FixedOne/2
				pz := z + numeric.Fixed(int64(phase*11%19)-9)*numeric.FixedOne/2
				y, dry := c.dustSurface(px, pz)
				if !dry || u.Owner != cur.ViewingPlayer && !SnapshotPointVisible(cur.Visibility, px, y, pz, cur.ViewingPlayer) {
					continue
				}
				st.push(surfaceWakeMark{x: px, z: pz, vx: dx / distance * 0.5, vz: dz / distance * 0.5, born: cur.Tick, life: 48 + float32(phase%17)})
			}
		}
		script.emissions = script.emissions[:0]
	}
	for id, script := range st.units {
		if script.tick != cur.Tick {
			delete(st.units, id)
		}
	}
}

func (c *Client) drawSurfaceWakes() {
	if c == nil || !c.enhanced || c.cam == nil || c.terrain == nil || c.strategicView() {
		return
	}
	st := &c.wakes
	st.arena = st.arena[:0]
	scale := float32(c.cam.EffectiveScale().Float())
	w, h := c.recordExtent()
	fraction := float32(0)
	if c.interpolation {
		fraction = c.TickFraction()
	}
	for n := range st.marks {
		m := st.marks[(st.next+n)%len(st.marks)]
		elapsed := float32(st.tick-m.born) + fraction
		if elapsed >= m.life {
			continue
		}
		age := elapsed / m.life
		x := m.x + numeric.Fixed(m.vx*float64(elapsed)*float64(numeric.FixedOne))
		z := m.z + numeric.Fixed(m.vz*float64(elapsed)*float64(numeric.FixedOne))
		y, dry := c.dustSurface(x, z)
		if !dry {
			continue
		}
		sx, sy := c.cam.WorldToScreen(x, y, z)
		px, py := float32(sx-camera.OriginX), float32(sy-camera.OriginY)
		if px+scale < 0 || py+scale < 0 || px-scale >= float32(w) || py-scale >= float32(h) {
			continue
		}
		st.arena = append(st.arena, drawlist.SurfaceWake{X: px, Y: py, AxisX: scale, CrossY: scale, Age: age, Alpha: 0.45 * (1 - age) * (1 - age), Dust: true})
	}
	if len(st.arena) > 0 {
		c.list.RecordSurfaceWakes(drawlist.SurfaceWakes{Marks: st.arena})
	}
}
