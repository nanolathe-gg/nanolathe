// Package movement — the flight command block, its per-tick producer, the air
// sector grid that producer reads, and the lean accumulator that writes bank
// and pitch [04 R-AIR-01 §1][04 R-AIR-01 §2][04 R-AIR-01 §5].
//
// The flight integrator of [04 §10.1] never reads an order record. It reads the
// command block alone, and the block is filled once per tick by the controller
// hook below from whatever goal payload the active air order installed. There is
// exactly one such supply, shared by every air order; the orders differ only in
// which payload they install [04 R-AIR-01 §1].
package movement

import (
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// Vec3 is a three-component 16.16 world position or velocity.
type Vec3 struct {
	X, Y, Z numeric.Fixed
}

// GoalPayload is the interface a motion controller calls on the goal payload an
// order installed [04 R-AIR-01 §1][04 R-AIR-01 §4]. The air half of the family
// is the path marker of [04 R-AIR-01 §4]; the ground half is the goal handle of
// section 8.3. The base declares six operations; the sixth, serialization, has
// no consumer here because in-battle save restoration is not supported.
type GoalPayload interface {
	// UpdateGoal overwrites dst with this tick's goal position. A payload that
	// declines — a follow marker whose target died or left the map — leaves dst
	// exactly as it found it [04 R-AIR-01 §4].
	UpdateGoal(u *units.Unit, dst *Vec3)
	// Arrived is the payload's own arrival test against the unit
	// [04 R-AIR-01 §4].
	Arrived(u *units.Unit) bool
	// SupplyHeading writes a suggested command heading into dst and reports
	// whether it supplied one. A false return means "no suggestion" and leaves
	// the caller to decide [04 R-AIR-01 §4].
	SupplyHeading(u *units.Unit, dst *uint16) bool
	// Persistent reports whether the payload survives its own arrival — true for
	// a follow marker with a live target, false for a point marker
	// [04 R-AIR-01 §4].
	Persistent() bool
	// Release drops the payload's own references. The producer calls it on a
	// non-persistent payload that has arrived [04 R-AIR-01 §1].
	Release()
}

// Flags byte of the command block [04 R-AIR-01 §1]: bit 0 is the "mover mode
// changed" dirty flag and bits 1..2 mirror the last observed committed mover
// mode.
const (
	flightCommandDirty     uint8 = 0x01
	flightCommandModeMask  uint8 = 0x06
	flightCommandModeShift uint8 = 1
)

// Producer distance thresholds, all raw 16.16 [04 R-AIR-01 §1].
const (
	// Beyond this the per-tick cruise-altitude rule of step 4 keeps rewriting
	// the command Y; inside it the rewrite stops so the final approach can
	// descend. 160 world units.
	cruiseRefreshRange = 0xA00000
	// Beyond this the command heading is the bearing to the goal and the
	// payload is not consulted at all. 320 world units.
	bearingOverrideRange = 0x1400000
	// Inside this a payload with no suggestion leaves the command heading
	// completely unchanged. 16 world units.
	headingHoldRange = 0x100000
)

// Satisfied-gate bits the producer raises on the owning order record
// [04 R-AIR-01 §1] step 6.
const (
	airGoalArrivedBit  uint32 = 0x20 // the payload reported arrival
	airGoalReleasedBit uint32 = 0x80 // the payload was released after arrival
)

// FlightCommand is the flight command block: the only input the flight
// integrator has [04 R-AIR-01 §1].
//
// A mover picks exactly one motion controller at construction — the ground
// route follower when the definition's `canfly` bit is clear, this block when
// it is set — and the choice is never revisited.
type FlightCommand struct {
	// Payload is the order's currently installed goal payload. A null payload
	// makes the producer do nothing at all, so the block keeps its last values
	// and the aircraft continues on its last command [04 R-AIR-01 §1].
	Payload GoalPayload
	// Unit is the block's reference to the unit it commands [04 R-AIR-01 §1].
	Unit *units.Unit
	// Pos is the command position, initialized to the unit's spawn X/Y/Z.
	Pos Vec3
	// Vel is the command velocity, initialized to zero. It is a pure
	// consequence of how far the goal itself moved this tick; nothing else
	// writes it [04 R-AIR-01 §1] step 2.
	Vel Vec3
	// Heading is the command heading, initialized to the unit's spawn heading.
	Heading uint16
	// Flags is the flags byte described above.
	//
	// TODO(question): [04 R-AIR-01 §1] establishes the WRITER — the controller's
	// per-tick hook sets bit 0x01 when the committed mover mode differs from the
	// mirror in bits 1..2, before the producer runs — and this build reproduces
	// that write exactly (StepFlightCommand below). What no section gives is a
	// READER: no consumer of bit 0x01, no site that clears it, and no initial
	// value for the byte. The field is consequently write-only here and
	// behavior-inert — nothing in the mover, the integrator or the order layer
	// branches on it — so no contract depends on the answer until a reader
	// exists. Decider: a reader census of the flags byte across the mover's
	// callers, which would also name the clear site.
	Flags uint8
}

// NewFlightCommand allocates a block with the initial values [04 R-AIR-01 §1]
// states: the command position is the unit's spawn X/Y/Z, the command velocity
// is zero, and the command heading is the unit's spawn heading.
func NewFlightCommand(u *units.Unit) *FlightCommand {
	c := &FlightCommand{Unit: u}
	if u != nil {
		c.Pos = Vec3{X: u.X, Y: u.Y, Z: u.Z}
		c.Heading = u.Move.Heading
	}
	return c
}

// FlightCommandFor returns the unit's flight command block, allocating it on
// first use. Retail allocates the controller once, when the mover is built; the
// mover surface here predates this unit, so the block is attached to it lazily
// and then lives as long as the mover does [04 R-AIR-01 §1].
func (s *System) FlightCommandFor(h pool.Handle, u *units.Unit) *FlightCommand {
	if s == nil {
		return nil
	}
	fl := s.Flights[h]
	if fl == nil {
		return nil
	}
	if fl.Command == nil {
		fl.Command = NewFlightCommand(u)
	}
	if fl.Command.Unit == nil {
		fl.Command.Unit = u
	}
	return fl.Command
}

// StepFlightCommand is the controller's per-tick hook [04 R-AIR-01 §1]: it sets
// the dirty flag when the committed mover mode differs from the mirrored copy,
// runs the six-step producer, and then performs the integrator's single input
// fetch — the copy of the command position, command velocity and command
// heading into the mover's own command words. Nothing else crosses that
// boundary.
//
// rec is the order record that installed the payload; step 6 raises its
// satisfied bits. sectors is the air sector grid of [04 R-AIR-01 §5], built once
// per map by the caller that owns the map, because this package's System has no
// field for it and this unit does not own the file that declares System.
func (s *System) StepFlightCommand(u *units.Unit, rec *orders.Node, sectors *AirSectorGrid) {
	if s == nil || u == nil {
		return
	}
	fl := s.Flights[u.Handle]
	if fl == nil {
		return
	}
	c := s.FlightCommandFor(u.Handle, u)
	if c == nil {
		return
	}

	// Hook prologue — the dirty flag, then the mirror. The bits hold the last
	// *observed* committed mode, so the observation that raises the flag is the
	// one that refreshes them [04 R-AIR-01 §1].
	mode := u.Move.Mode & 0x3
	if (c.Flags&flightCommandModeMask)>>flightCommandModeShift != mode {
		c.Flags |= flightCommandDirty
	}
	c.Flags = (c.Flags &^ flightCommandModeMask) | (mode << flightCommandModeShift)

	c.produce(u, rec, sectors)

	// The integrator's single input fetch [04 R-AIR-01 §1]. The mover's command
	// words are FlightState's Target* fields; §10.1 gives the vertical control
	// no command velocity, so only the horizontal pair is fetched.
	fl.TargetX = int32(c.Pos.X.Raw())
	fl.TargetY = int32(c.Pos.Y.Raw())
	fl.TargetZ = int32(c.Pos.Z.Raw())
	fl.TargetVX = int32(c.Vel.X.Raw())
	fl.TargetVZ = int32(c.Vel.Z.Raw())
	fl.TargetHeading = c.Heading

	// Bind the mover's lean inputs so the integrator's tail can write the unit's
	// bank and pitch words [04 R-AIR-01 §2].
	s.bindFlightLean(u, fl)
}

// produce is the per-tick command producer, in the six numbered steps and the
// order [04 R-AIR-01 §1] gives them. With a null goal payload it does nothing at
// all: the block keeps its last values, so an aircraft whose payload was
// released continues on its last command.
func (c *FlightCommand) produce(u *units.Unit, rec *orders.Node, sectors *AirSectorGrid) {
	if c == nil || u == nil || c.Payload == nil {
		return
	}

	// 1 — save the old command position, then let the payload overwrite it. A
	// payload that declines leaves it at last tick's value.
	old := c.Pos
	c.Payload.UpdateGoal(u, &c.Pos)

	// 2 — the command velocity is the componentwise difference new − old.
	c.Vel = Vec3{X: c.Pos.X - old.X, Y: c.Pos.Y - old.Y, Z: c.Pos.Z - old.Z}

	// 3 — d is the double-precision hypot of the two raw fixed-point horizontal
	// differences, truncated toward zero; it stays a raw 16.16 quantity. The
	// float boundary itself is in flight.go, which is the file the I2 allowlist
	// names for this package's flight temporaries.
	d := flightGoalDistance(int64(u.X)-int64(c.Pos.X), int64(u.Z)-int64(c.Pos.Z))

	// 4 — beyond 160 world units the command Y is overwritten from the air
	// sector grid, not from the four-corner terrain query: the height cleared is
	// the maximum terrain height of the 3×3 block of 128-world-unit sectors
	// around the unit, refreshed every tick, and it stops being refreshed inside
	// 160 world units so the final approach can descend. The 0x1FF0000 ceiling
	// the marker's own setter applies is NOT applied here.
	//
	// The definition flag that selects the sea-level variant of this expression
	// has no writer anywhere in the recovered function set, so the sector-height
	// variant is the operative one and the only one reproduced. Without a grid
	// the overwrite simply does not run: the four-corner terrain query is the
	// wrong source for it and is not substituted here.
	if d > cruiseRefreshRange && u.Def != nil {
		if sectorHeight, linked := sectors.SectorHeightAt(u.X, u.Z); linked {
			c.Pos.Y = numeric.Fixed((int64(u.Def.CruiseAlt) + int64(sectorHeight)) << 16)
		}
	}

	// 5 — the heading rule. Beyond 320 world units the payload is not consulted
	// at all; that ordering is the contract, not an optimization. Inside it, a
	// payload suggestion stands as written; with no suggestion the bearing wins
	// down to 16 world units, and inside 16 the command heading is left
	// completely unchanged.
	if d > bearingOverrideRange {
		c.Heading = bearing(u.X, u.Z, c.Pos.X, c.Pos.Z)
	} else if !c.Payload.SupplyHeading(u, &c.Heading) && d > headingHoldRange {
		c.Heading = bearing(u.X, u.Z, c.Pos.X, c.Pos.Z)
	}

	// 6 — arrival, persistence, release.
	if !c.Payload.Arrived(u) {
		return
	}
	if rec != nil {
		rec.Satisfied |= airGoalArrivedBit
	}
	if c.Payload.Persistent() {
		return
	}
	c.Payload.Release()
	c.Payload = nil
	if rec != nil {
		rec.Satisfied |= airGoalReleasedBit
	}
}

// --- the air sector grid [04 R-AIR-01 §5][03 R-TERR-01 §5] ---

// airSectorCells is the grid's cell side in attribute cells: 8 of them, which is
// 128 world units [04 R-AIR-01 §5].
const airSectorCells = 8

// airSectorShift divides a raw 16.16 world coordinate by those 128 world units.
const airSectorShift = 23

// airSector is one grid record. Retail's record also heads the singly-linked
// list of units inside the cell; Nanolathe's occupancy owns unit membership, so
// only the two height bytes and the edge word are modelled here.
type airSector struct {
	// Height is the per-cell maximum terrain height byte, floored at sea level.
	Height uint8
	// Smoothed is the maximum of Height over the 3×3 block of sectors centred on
	// this one, truncated at the map edges. This is the sectorHeight the
	// cruise-altitude rule of [04 R-AIR-01 §1] step 4 reads.
	Smoothed uint8
	// Edge carries bit 1 top row, 2 bottom row, 4 left column, 8 right column.
	Edge uint32
}

// AirSectorGrid is the coarse second grid the map loader builds after the
// terrain is decoded [04 R-AIR-01 §5]. Its cell is 8 attribute cells on a side.
type AirSectorGrid struct {
	Columns int32
	Rows    int32

	records []airSector
	// sentinel is the one extra record allocated alongside the grid, zeroed with
	// its edge word set to all four edge bits plus bit 4. A unit whose footprint
	// anchor lies outside the attribute grid links into this record instead of
	// an ordinary one; its height bytes stay zero forever because the build
	// passes never visit it [04 R-AIR-01 §5].
	sentinel airSector
}

// NewAirSectorGrid builds the grid from decoded terrain in the four passes
// [04 R-AIR-01 §5] gives, with the sweep reading the plot cell's derived maximum
// byte rather than its raw height sample [03 R-TERR-01 §5].
func NewAirSectorGrid(t *world.Terrain) *AirSectorGrid {
	if t == nil {
		return nil
	}
	// Columns and rows are the map's pixel extents rounded up to whole 128-pixel
	// cells; the record count is that product rounded up to a multiple of 8.
	cols := int32((int64(t.CellW)*16*65536 + 0x7FFFFF) >> airSectorShift)
	rows := int32((int64(t.CellH)*16*65536 + 0x7FFFFF) >> airSectorShift)
	if cols <= 0 || rows <= 0 {
		return nil
	}
	count := int(cols) * int(rows)
	if r := count % 8; r != 0 {
		count += 8 - r
	}
	g := &AirSectorGrid{Columns: cols, Rows: rows, records: make([]airSector, count)}
	g.sentinel = airSector{Edge: 0x1F}

	// Pass 1 — every record starts zeroed; the edge bits are OR'd in the order
	// top, bottom, left, right. The padding records that round the count up to a
	// multiple of 8 lie in no row and no column of the grid, are never swept and
	// are never queried, so they take no edge bits.
	for r := int32(0); r < rows; r++ {
		for c := int32(0); c < cols; c++ {
			rec := &g.records[r*cols+c]
			if r == 0 {
				rec.Edge |= 1
			}
			if r == rows-1 {
				rec.Edge |= 2
			}
			if c == 0 {
				rec.Edge |= 4
			}
			if c == cols-1 {
				rec.Edge |= 8
			}
		}
	}

	// Pass 2 — every record's height byte starts at the map's sea-level byte,
	// padding records included.
	for i := range g.records {
		g.records[i].Height = t.SeaLevel
	}

	// Pass 3 — sweep every attribute cell and raise the owning record's height
	// byte to the cell's derived maximum where that is higher.
	for z := int32(0); z < t.CellH; z++ {
		for x := int32(0); x < t.CellW; x++ {
			cell := t.PlotAt(x, z)
			if cell == nil {
				continue
			}
			rec := &g.records[(z/airSectorCells)*cols+(x/airSectorCells)]
			if h := cell.MaxHeight(); h > rec.Height {
				rec.Height = h
			}
		}
	}

	// Pass 4 — two separable maximum passes. The row pass writes the smoothed
	// byte as the maximum of the height byte over this cell and its two
	// horizontal neighbours; the column pass then rewrites the smoothed byte in
	// place as the maximum of the smoothed byte over this cell and its two
	// vertical neighbours, reading ahead so the in-place rewrite does not alias.
	// At the first and last cell of a row or column only two cells participate.
	for r := int32(0); r < rows; r++ {
		for c := int32(0); c < cols; c++ {
			m := g.records[r*cols+c].Height
			if c > 0 {
				if v := g.records[r*cols+c-1].Height; v > m {
					m = v
				}
			}
			if c < cols-1 {
				if v := g.records[r*cols+c+1].Height; v > m {
					m = v
				}
			}
			g.records[r*cols+c].Smoothed = m
		}
	}
	for c := int32(0); c < cols; c++ {
		prev := uint8(0)
		havePrev := false
		for r := int32(0); r < rows; r++ {
			cur := g.records[r*cols+c].Smoothed
			m := cur
			if havePrev && prev > m {
				m = prev
			}
			if r < rows-1 {
				if v := g.records[(r+1)*cols+c].Smoothed; v > m {
					m = v
				}
			}
			g.records[r*cols+c].Smoothed = m
			prev = cur
			havePrev = true
		}
	}
	return g
}

// SectorHeightAt returns the smoothed sector-height byte for the sector holding
// the given world position, and whether that position links into an ordinary
// record at all. A false second result is the out-of-bounds sector record, whose
// height bytes are zero forever [04 R-AIR-01 §5].
//
// Retail's link is written by the occupancy re-stamp, which indexes
// `grid[(Z >> 23) * columns + (X >> 23)]` and tests the unit's footprint anchor
// against the attribute grid. Nanolathe's occupancy does not yet carry that
// link; the index here is the same expression on the unit's own position, which
// agrees with the re-stamp for every unit whose footprint anchor is in bounds.
func (g *AirSectorGrid) SectorHeightAt(x, z numeric.Fixed) (uint8, bool) {
	if g == nil {
		return 0, false
	}
	cx := int64(x) >> airSectorShift // arithmetic shift: a negative coordinate floors [I3]
	cz := int64(z) >> airSectorShift
	if cx < 0 || cz < 0 || cx >= int64(g.Columns) || cz >= int64(g.Rows) {
		return g.sentinel.Smoothed, false
	}
	return g.records[cz*int64(g.Columns)+cx].Smoothed, true
}

// --- the lean accumulator [04 R-AIR-01 §2] ---

// Lean accumulator constants [04 R-AIR-01 §2].
const (
	// leanDecay is 0xF333 = 62259, a per-tick decay of 62259/65536, which is
	// 0.95 × 65536 = 62259.2 truncated.
	leanDecay = 0xF333
	// leanGravityDivisor is 0xCCD = 3277, the divisor under the gravity word.
	leanGravityDivisor = 0xCCD
)

// bindFlightLean copies the definition and map words the lean accumulator reads
// onto the mover, and gives the mover the back-reference it needs to write the
// unit's two visual angle words [04 R-AIR-01 §2]. `bankscale` and `pitchscale`
// are the 16.16 definition words, compiled with defaults 1.0 and 0.0; `gravity`
// is the runtime word the map loader fills from the OTA key [03 R-TERR-01 §6].
func (s *System) bindFlightLean(u *units.Unit, fl *FlightState) {
	if s == nil || u == nil || fl == nil {
		return
	}
	fl.Unit = u
	if u.Def != nil {
		fl.BankScale = u.Def.BankScale
		fl.PitchScale = u.Def.PitchScale
	}
	if s.Terrain != nil {
		fl.Gravity = int32(s.Terrain.Gravity)
	}
}

// levelFlightLean is the mover-mode setter's levelling call [04 R-AIR-01 §2]:
// the same routine the integrator's tail runs, with a zero delta, so bank and
// pitch decay by the 0xF333 factor exactly once and are then recomputed from the
// decayed accumulator. They are not snapped to zero.
func (s *System) levelFlightLean(u *units.Unit) {
	if s == nil || u == nil {
		return
	}
	fl := s.Flights[u.Handle]
	if fl == nil {
		return
	}
	s.bindFlightLean(u, fl)
	fl.ApplyLean(0, 0, 0)
}
