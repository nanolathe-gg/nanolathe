package client

import (
	"math"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// These are artistic Enhanced presentation choices (GPU design §26), not
// retail wake behavior. Retail's independent script sprinkles remain intact
// [03 R-WATER-01 §1]. No simulation state or RNG participates [I6].
const (
	wakeRingSize     = 8192
	wakeTrackerLimit = 4096
	wakeDustLife     = 45
	wakeDustSpacing  = 6
	wakeSnapDistance = 64
)

type surfaceWakeMark struct {
	x, y, z     numeric.Fixed
	dirX, dirZ  float32
	half, width float32
	born        uint32
	side        float32
}

type surfaceWakeTracker struct {
	x, z  numeric.Fixed
	tick  uint32
	carry float64
	puffs uint32
	defID uint16
	owner uint8
}

type surfaceWakeState struct {
	marks  []surfaceWakeMark
	next   int
	units  map[uint64]surfaceWakeTracker
	tick   uint32
	viewer uint8
	valid  bool
	arena  []drawlist.SurfaceWake
}

func (st *surfaceWakeState) push(m surfaceWakeMark) {
	if len(st.marks) < wakeRingSize {
		st.marks = append(st.marks, m)
		return
	}
	st.marks[st.next] = m
	st.next = (st.next + 1) % wakeRingSize
}

// dustSurface accepts valid dry ground only. Original script sprinkles keep
// their independent water survival rule [03 R-WATER-01 §1].
func (c *Client) dustSurface(x, z numeric.Fixed) (numeric.Fixed, bool) {
	y := c.terrain.HeightAt(x, z)
	return y, y >= 0 && y >= c.terrain.SeaLevelWorld()
}

// placeSurfaceWakes samples only consecutive committed, visible positions.
// Each puff keeps its birth direction, so turns retain the travelled path.
func (c *Client) placeSurfaceWakes(cur *frame.Frame) {
	if c == nil || cur == nil || c.terrain == nil || !c.enhanced {
		return
	}
	st := &c.wakes
	if st.valid && st.tick == cur.Tick && st.viewer == cur.ViewingPlayer {
		return
	}
	if st.valid && (cur.Tick != st.tick+1 || st.viewer != cur.ViewingPlayer) {
		// Restore, skipped observation, tick rewind and viewer changes cannot
		// connect two histories. Reset the age domain as well as the trackers.
		*st = surfaceWakeState{}
	}
	st.tick, st.viewer, st.valid = cur.Tick, cur.ViewingPlayer, true
	if st.units == nil {
		st.units = make(map[uint64]surfaceWakeTracker)
	}
	for i := range cur.Units {
		u := &cur.Units[i]
		// Instance identity, unlike a pool slot, changes on reuse. Legacy
		// snapshots without it cannot safely retain a movement history.
		if u.InstanceID == 0 || u.IsBuilding || !u.CanHover ||
			u.MoverMode != moverModeGrounded || isCarried(*u) || u.BuildRemaining > 0 ||
			!unitVisibleForFrame(cur, *u, cur.ViewingPlayer) ||
			(u.Owner != cur.ViewingPlayer && fogUnexploredUnit(cur.Fog, *u)) {
			continue
		}
		if _, ok := c.dustSurface(u.X, u.Z); !ok {
			continue
		}
		// Grounded hover Y is an average of four rotated selection-plate
		// samples [04 R-MOV-01 §5], not the centre terrain height. A small
		// centre-height tolerance rejects real hovercraft on uneven ground.
		// Trust the committed grounded mode; place each artistic puff on its
		// own dry terrain sample instead of inferring contact from model Y.
		previous, exists := st.units[u.InstanceID]
		if !exists && len(st.units) >= wakeTrackerLimit {
			continue
		}
		current := surfaceWakeTracker{x: u.X, z: u.Z, tick: cur.Tick, defID: u.DefID, owner: u.Owner}
		st.units[u.InstanceID] = current
		if !exists || previous.tick+1 != cur.Tick || previous.defID != u.DefID || previous.owner != u.Owner {
			continue
		}
		dx := float64(u.X-previous.x) / float64(numeric.FixedOne)
		dz := float64(u.Z-previous.z) / float64(numeric.FixedOne)
		distance := math.Hypot(dx, dz)
		if distance > wakeSnapDistance {
			continue
		}
		current.carry, current.puffs = previous.carry, previous.puffs
		if distance == 0 {
			st.units[u.InstanceID] = current
			continue
		}
		if _, ok := c.dustSurface(previous.x+(u.X-previous.x)/2, previous.z+(u.Z-previous.z)/2); !ok {
			continue
		}
		dirX, dirZ := dx/distance, dz/distance
		foot := float64(max(int32(u.FootX), int32(u.FootZ), 1) * cellPixels)
		// Artistic distance cadence, independent of speed and tick count.
		// Alternate discrete puffs around the rear skirt so they can emerge
		// from underneath the model instead of forming an occluded strip.
		for along := wakeDustSpacing - previous.carry; along <= distance; along += wakeDustSpacing {
			side := float64(1)
			if (current.puffs+uint32(u.InstanceID))&1 != 0 {
				side = -1
			}
			variation := float64(current.puffs%3) / 2
			current.puffs++
			rear, lateral := foot*(0.20+0.05*variation), side*foot*(0.40+0.06*variation)
			x := previous.x + numeric.Fixed((dirX*(along-rear)-dirZ*lateral)*float64(numeric.FixedOne))
			z := previous.z + numeric.Fixed((dirZ*(along-rear)+dirX*lateral)*float64(numeric.FixedOne))
			y, ok := c.dustSurface(x, z)
			if !ok || u.Owner != cur.ViewingPlayer && !SnapshotPointVisible(cur.Visibility, x, y, z, cur.ViewingPlayer) {
				continue
			}
			radius := float32(max(6, foot*(0.16+0.025*variation)))
			st.push(surfaceWakeMark{x: x, y: y, z: z, dirX: float32(dirX), dirZ: float32(dirZ),
				half: radius, width: radius * 0.8, born: cur.Tick, side: float32(side)})
		}
		current.carry = math.Mod(previous.carry+distance, wakeDustSpacing)
		st.units[u.InstanceID] = current
	}
	// Hidden, absent, carried and otherwise rejected units lose their last
	// position immediately. Reappearance always begins with an empty path.
	for id, tr := range st.units {
		if tr.tick != cur.Tick {
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
	// Visit oldest first even after the ring wraps, keeping soft overdraw
	// stable as older particles spread and fade.
	for n := range st.marks {
		m := st.marks[(st.next+n)%len(st.marks)]
		elapsed := st.tick - m.born
		if elapsed >= wakeDustLife {
			continue
		}
		age := float32(elapsed) / float32(wakeDustLife)
		width := (m.width + 7*age) * scale
		half := (m.half + 5*age) * scale
		sx, sy := c.cam.WorldToScreen(m.x, m.y, m.z)
		// Mild sideways drift and expansion expose the skirt puffs while the
		// shader clips them to dry ground. These are artistic recording pixels.
		drift := m.side * 6 * age * scale
		x := float32(sx-camera.OriginX) - m.dirZ*drift
		y := float32(sy-camera.OriginY) + m.dirX*drift
		margin := width + half
		if x+margin < 0 || y+margin < 0 || x-margin >= float32(w) || y-margin >= float32(h) {
			continue
		}
		st.arena = append(st.arena, drawlist.SurfaceWake{X: x, Y: y,
			AxisX: m.dirX * half, AxisY: m.dirZ * half, CrossX: -m.dirZ * width, CrossY: m.dirX * width,
			Age: age, Alpha: 0.45 * (1 - age) * (1 - age), Dust: true})
	}
	if len(st.arena) != 0 {
		c.list.RecordSurfaceWakes(drawlist.SurfaceWakes{Marks: st.arena})
	}
}
