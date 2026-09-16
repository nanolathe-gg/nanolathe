package client

import (
	"math"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// The Enhanced trail layer (docs/DESIGN_GPU_RENDERER.md §15): mobile ground
// units leave fading marks on the terrain — alternating footprints for legged
// units, a pair of track segments for tracked ones. Retail leaves no marks.
// This is a Nanolathe presentation feature, active only while the Enhanced
// executor presents, and it reads committed frame fields only: positions and
// ticks come from the committed frame, never the blended view, so the layer
// is a function of the committed history and the camera and nothing here
// reaches the simulation [I6].

// trailClass is what a unit leaves behind.
type trailClass uint8

const (
	trailNone trailClass = iota
	trailFeet
	trailTracks
)

const (
	// trailRingSize bounds the retained marks; the oldest is overwritten.
	trailRingSize = 4096
	// trailLifeTicks is a mark's life in committed ticks: ten seconds at the
	// 30 Hz tick.
	trailLifeTicks = 300
	// Fallback strides between marks in world pixels. A track segment is as long as
	// its stride, so consecutive segments join into a continuous line.
	trailFeetStride  = 10.0
	trailTrackStride = 8.0
	// A fresh mark's darkening at its centre: the terrain colour is
	// multiplied by 1 − strength there. Footprints are small, so they take
	// more; a track is a continuous line and needs less to read.
	trailFeetStrength  = 0.4
	trailTrackStrength = 0.3
	// trailSnap is the step, in strides, above which a unit is treated as
	// having been moved rather than having walked: a factory exit, a
	// transport drop, a save restore. No marks bridge such a step.
	trailSnap = 8
)

// trailMark is one retained quad in world space; the screen geometry is
// derived at record time from the camera of that frame.
type trailMark struct {
	x, z numeric.Fixed
	// dirX, dirZ is the unit direction of travel scaled by 256.
	dirX, dirZ int32
	born       uint32
	class      trailClass
	// Dimensions are captured at placement; later model loads and camera
	// changes cannot resize an existing mark.
	halfLength, halfWidth float64
	live                  bool
}

// trailTracker is one unit's placement state: where its last mark was laid.
type trailTracker struct {
	x, z numeric.Fixed
	side uint8
	seen bool
}

type trailState struct {
	marks []trailMark
	next  int
	units map[uint64]*trailTracker
	// classes caches the per-definition class; the definition never changes
	// during a battle.
	classes  map[uint16]trailClass
	geometry map[*unitModel]trailGeometry
	tick     uint32
	valid    bool
	// arena is the reusable slice the frame's batch record borrows.
	arena []drawlist.Trail
}

// trailDefInfo is what the model registry retains from a unit definition for
// the classifier: the map editor's class word, the movement class name and
// whether the unit flies.
type trailDefInfo struct {
	ted, move string
	aircraft  bool
}

// tedClassOf reads the FBI's TEDClass key, which the catalog keeps among its
// inert keys in authored case [fmt fbi].
func tedClassOf(unknown map[string]string) string {
	for k, v := range unknown {
		if strings.EqualFold(k, "TEDClass") {
			return v
		}
	}
	return ""
}

// trailLegPiece reports whether a model piece name says the unit walks.
func trailLegPiece(name string) bool {
	name = strings.ToLower(name)
	for _, w := range [...]string{"leg", "foot", "thigh", "knee", "shin", "toe"} {
		if strings.Contains(name, w) {
			return true
		}
	}
	return false
}

// classifyTrail selects the presentation mark (DESIGN_GPU_RENDERER §15.2).
// Editor TANK includes the six-legged spider, so leg pieces override that
// hint. Aircraft, boats, hovercraft and fixed editor classes remain excluded.
// Movement class names describe footprint and terrain rules [fmt fbi]; they
// exclude non-ground movers but never establish feet or tread spacing.
func classifyTrail(ted, move string, aircraft, legs bool) trailClass {
	if aircraft {
		return trailNone
	}
	move = strings.ToUpper(move)
	if strings.Contains(move, "HOVER") || strings.HasPrefix(move, "BOAT") {
		return trailNone
	}
	switch strings.ToUpper(strings.TrimSpace(ted)) {
	case "KBOT", "COMMANDER":
		return trailFeet
	case "TANK":
		// The spider is editor-class TANK, but its authored legs walk.
		// Terrain and non-ground exclusions above/below still take priority.
		if legs {
			return trailFeet
		}
		return trailTracks
	case "VTOL", "SHIP", "WATER", "PLANT", "FORT", "METAL", "ENERGY":
		return trailNone
	}
	if legs {
		return trailFeet
	}
	return trailTracks
}

func (c *Client) trailClassFor(u frame.UnitView) trailClass {
	if !u.BMCode || u.IsBuilding {
		return trailNone
	}
	st := &c.trails
	if u.DefID != 0 {
		if cls, ok := st.classes[u.DefID]; ok {
			return cls
		}
	}
	var info trailDefInfo
	if c.modelTextures != nil {
		info = c.modelTextures.trailInfo(u.DefName, u.DefID)
	}
	legs := false
	if m := c.modelForUnit(u); m != nil {
		for name := range m.pieceByName {
			if trailLegPiece(name) {
				legs = true
				break
			}
		}
	}
	cls := classifyTrail(info.ted, info.move, info.aircraft, legs)
	if u.DefID != 0 {
		if st.classes == nil {
			st.classes = map[uint16]trailClass{}
		}
		st.classes[u.DefID] = cls
	}
	return cls
}

func (st *trailState) push(m trailMark) {
	if len(st.marks) < trailRingSize {
		st.marks = append(st.marks, m)
		return
	}
	st.marks[st.next] = m
	st.next++
	if st.next >= len(st.marks) {
		st.next = 0
	}
}

// ObserveCommittedTick lets a route that advances ticks without presenting
// each one — the capture route — lay the trail marks a tick adds, so a
// capture shows the same marks the window would (§15). It is idempotent per
// committed tick and does nothing while Original presents.
func (c *Client) ObserveCommittedTick() {
	if c == nil {
		return
	}
	// Each observer is gated by its player switch (§30). A switch that is off
	// accumulates no history, so turning it back on starts from the current
	// tick instead of replaying marks that were never drawn.
	if c.effects.Marks {
		c.placeTrails(c.buffer.Current())
		c.observeScorchMarks(c.buffer.Current())
	}
	if c.effects.Water {
		c.placeSurfaceWakes(c.buffer.Current())
		c.observeWaterMotion(c.buffer.Current())
	}
}

func (c *Client) resetTrails() {
	if c != nil {
		c.trails = trailState{}
		c.wakes = surfaceWakeState{}
		c.waterMotion = waterMotionState{}
		c.scorch = scorchState{}
		c.debrisTrails.reset()
	}
}

// placeTrails lays the marks a committed tick adds. It runs once per
// committed tick, only while Enhanced presents, and only for units the
// classifier accepts that stand on dry ground: a unit that is airborne,
// carried, under construction, wading or standing at or below sea level lays
// nothing and restarts its stride where it next qualifies.
func (c *Client) placeTrails(cur *frame.Frame) {
	if c == nil || cur == nil || !c.enhanced || c.terrain == nil {
		return
	}
	st := &c.trails
	if st.valid && st.tick == cur.Tick {
		return
	}
	st.valid, st.tick = true, cur.Tick
	if st.units == nil {
		st.units = map[uint64]*trailTracker{}
	}
	for _, tr := range st.units {
		tr.seen = false
	}
	sea := int32(c.terrain.SeaLevel)
	viewer := cur.Selection.LocalPlayer
	for i := range cur.Units {
		u := &cur.Units[i]
		id := unitPresentationID(*u)
		if id == 0 {
			continue
		}
		class := c.trailClassFor(*u)
		if class == trailNone {
			continue
		}
		tr := st.units[id]
		if tr == nil {
			st.units[id] = &trailTracker{x: u.X, z: u.Z, seen: true}
			continue
		}
		tr.seen = true
		// A unit the local player cannot see lays nothing: a trail is the
		// memory of a walk that was watched, never a sensor. The painter's own
		// gate decides, so a cloaked or fogged enemy leaves no marks behind it.
		ground := c.terrain.HeightAt(u.X, u.Z)
		if !unitVisibleForFrame(cur, *u, viewer) || u.MoverMode != 1 || u.BuildRemaining > 0 || int32(ground>>16) < sea || u.Y-ground > 2*numeric.FixedOne {
			tr.x, tr.z = u.X, u.Z
			continue
		}
		style := c.trailStyleFor(*u, class)
		stride := style.stride
		dx := float64(u.X-tr.x) / float64(numeric.FixedOne)
		dz := float64(u.Z-tr.z) / float64(numeric.FixedOne)
		dist := math.Sqrt(dx*dx + dz*dz)
		if dist < stride {
			continue
		}
		if dist > stride*trailSnap {
			tr.x, tr.z = u.X, u.Z
			continue
		}
		ux, uz := dx/dist, dz/dist
		dirX, dirZ := int32(math.Round(ux*256)), int32(math.Round(uz*256))
		stepX := numeric.Fixed(math.Round(ux * stride * float64(numeric.FixedOne)))
		stepZ := numeric.Fixed(math.Round(uz * stride * float64(numeric.FixedOne)))
		for ; dist >= stride; dist -= stride {
			tr.x += stepX
			tr.z += stepZ
			// Store actual contact centres and one quad per ring slot. Track
			// pairs consume two slots, so the budget bounds rendered work.
			place := func(side float64) {
				offset := style.spread * side
				st.push(trailMark{
					x:    tr.x + numeric.Fixed(math.Round(-uz*offset*float64(numeric.FixedOne))),
					z:    tr.z + numeric.Fixed(math.Round(ux*offset*float64(numeric.FixedOne))),
					dirX: dirX, dirZ: dirZ, born: cur.Tick, class: class,
					halfLength: style.halfLength, halfWidth: style.halfWidth, live: true,
				})
			}
			if class == trailTracks {
				place(-1)
				place(1)
			} else if tr.side == 0 {
				place(-1)
			} else {
				place(1)
			}
			tr.side ^= 1
		}
	}
	for id, tr := range st.units {
		if !tr.seen {
			delete(st.units, id)
		}
	}
}

// drawTrails records the frame's visible marks as one batch. The screen
// geometry follows the view transform of §14.2: a mark's centre projects like
// any world point, at the terrain height under it, and its extent scales
// with the view. The direction of travel maps straight onto the screen
// plane; the half-height shear of the projection [03 §2.5] only moves the
// centre.
func (c *Client) drawTrails() {
	if c == nil || !c.enhanced || c.cam == nil || c.terrain == nil || len(c.trails.marks) == 0 {
		return
	}
	st := &c.trails
	s := c.cam.EffectiveScale()
	recW, recH := c.recordExtent()
	w, h := int32(recW), int32(recH)
	st.arena = st.arena[:0]
	for i := range st.marks {
		m := &st.marks[i]
		if !m.live {
			continue
		}
		age := st.tick - m.born
		if age >= trailLifeTicks {
			m.live = false
			continue
		}
		// WorldToScreen answers in the retail viewport frame; every world layer
		// rebases to the shell origin the way the model anchor does
		// [03 §2.5][03 §4.1].
		sx, sy := c.cam.WorldToScreen(m.x, c.terrain.HeightAt(m.x, m.z), m.z)
		sx -= camera.OriginX
		sy -= camera.OriginY
		margin := int32(math.Ceil((m.halfLength+m.halfWidth)*s.Float())) + 2
		if sx < -margin || sy < -margin || sx >= w+margin || sy >= h+margin {
			continue
		}
		fade := 1 - float64(age)/trailLifeTicks
		peak := trailFeetStrength
		if m.class == trailTracks {
			peak = trailTrackStrength
		}
		strength := uint8(math.Round(255 * peak * fade))
		if strength == 0 {
			continue
		}
		// Axes retain 1/256-pixel precision at the record scale. Each
		// centre already includes its lateral offset at the sampled terrain.
		shape := drawlist.TrailFootprint
		if m.class == trailTracks {
			shape = drawlist.TrailTrack
		}
		st.arena = append(st.arena, drawlist.Trail{
			X: sx, Y: sy,
			AxisX:  int32(math.Round(float64(m.dirX) * m.halfLength * s.Float())),
			AxisY:  int32(math.Round(float64(m.dirZ) * m.halfLength * s.Float())),
			CrossX: int32(math.Round(float64(-m.dirZ) * m.halfWidth * s.Float())),
			CrossY: int32(math.Round(float64(m.dirX) * m.halfWidth * s.Float())),
			Shape:  shape, Strength: strength,
		})
	}
	if len(st.arena) == 0 {
		return
	}
	c.list.RecordTrails(drawlist.Trails{Marks: st.arena})
}
