package movement

import (
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Nanolathe Modern policy, DESIGN_MOVEMENT_PATH "Modern repair-pad queue".
// These are holding-pattern tuning, not retail constants. The normal approach,
// descent, attachment and resource-admitted SelfRepair remain the work path.
const (
	repairQueueRetry = 30
	repairHoldRadius = 192
	repairHoldSpace  = 64
)

type repairLanding struct {
	unit     *units.Unit
	node     *orders.Node
	pad      *units.Unit
	piece    uint16
	reserved bool
	holding  bool
	anchor   Vec3
	post     Vec3
}

func (s *System) discardRepairLanding(n *orders.Node) {
	for i, entry := range s.repairLandings {
		if entry.node == n {
			copy(s.repairLandings[i:], s.repairLandings[i+1:])
			s.repairLandings[len(s.repairLandings)-1] = nil
			s.repairLandings = s.repairLandings[:len(s.repairLandings)-1]
			return
		}
	}
}

func (s *System) pruneRepairLandings() {
	if len(s.repairLandings) == 0 {
		return
	}
	if !s.rules().RepairPadQueue(s) {
		clear(s.repairLandings)
		s.repairLandings = s.repairLandings[:0]
		return
	}
	for i := len(s.repairLandings) - 1; i >= 0; i-- {
		e := s.repairLandings[i]
		retained := false
		if q := orders.QueueOfUnit(e.unit); q != nil {
			for _, n := range q.Primary() {
				retained = retained || n == e.node
			}
		}
		if s.unitFor(e.unit.Handle) != e.unit || !e.unit.Alive || e.unit.Dying ||
			!retained || len(e.unit.Attachment.Cargo) != 0 {
			s.discardRepairLanding(e.node)
		}
	}
}

func (s *System) usableRepairPad(u, pad *units.Unit) bool {
	if pad == nil || !pad.Alive || pad.Dying || pad.Def == nil ||
		!pad.Def.IsAirBase || !pad.Def.Builder || pad.Remaining != 0 ||
		!pad.Activated || pad.Attachment.Carrier != 0 {
		return false
	}
	if u.Owner == pad.Owner {
		return true
	}
	b := airBinding(u)
	return b != nil && b.World != nil && b.World.DeclaresAlliance != nil &&
		b.World.DeclaresAlliance(u.Owner, pad.Owner) && b.World.DeclaresAlliance(pad.Owner, u.Owner)
}

// Only a lost/unavailable base triggers a new selection. A full base retains
// its queue, so patients do not chase the same newly freed piece elsewhere.
func (s *System) replacementRepairPad(u *units.Unit) *units.Unit {
	var best *units.Unit
	var distance int64
	for _, pad := range s.world.IterSliced() {
		if pad == u || !s.usableRepairPad(u, pad) {
			continue
		}
		d := airPlanarDistance(u.X, u.Z, pad.X, pad.Z)
		if best == nil || d < distance {
			best, distance = pad, d
		}
	}
	return best
}

// grantRepairPieces preserves existing reservations, then fills free authored
// pieces in FIFO order. Duplicate script outputs cannot grant a piece twice.
func (s *System) grantRepairPieces(pad *units.Unit) {
	if pad == nil || pad.ScriptBridge() == nil {
		return
	}
	result := pad.ScriptBridge().QueryLandingPad()
	var pieces [airPadCandidates]uint16
	count := 0
	for _, value := range result.Values {
		if value < 0 {
			continue
		}
		piece := uint16(value)
		duplicate := false
		for _, prior := range pieces[:count] {
			duplicate = duplicate || prior == piece
		}
		if !duplicate && s.padPieceFree(pad, piece) {
			pieces[count] = piece
			count++
		}
	}
	var taken [airPadCandidates]bool
	for _, e := range s.repairLandings {
		if e.pad != pad || !e.reserved {
			continue
		}
		e.reserved = false
		if !s.usableRepairPad(e.unit, pad) || e.node.Target != pad.Handle {
			continue
		}
		for i, piece := range pieces[:count] {
			if e.piece == piece && !taken[i] {
				taken[i], e.reserved = true, true
				break
			}
		}
		if !e.reserved {
			e.node.Phase = 1
		}
	}
	for _, e := range s.repairLandings {
		if e.pad != pad || e.reserved || e.node.Target != pad.Handle || !s.usableRepairPad(e.unit, pad) {
			continue
		}
		for i, piece := range pieces[:count] {
			if !taken[i] {
				e.piece, e.reserved, taken[i] = piece, true, true
				break
			}
		}
	}
}

func (s *System) landingPiece(pad *units.Unit, e *repairLanding) (uint16, bool) {
	if e == nil {
		return s.queryLandingPad(pad)
	}
	return e.piece, e.reserved && e.pad == pad && s.padPieceFree(pad, e.piece)
}

func (s *System) legQueuedRepairLanding(u *units.Unit, n *orders.Node, satisfied uint32, tick uint32) orders.Code {
	if !s.airMoverReady(u) {
		return 7
	}
	pad := s.unitFor(n.Target)
	s.pruneRepairLandings()
	var entry *repairLanding
	for _, e := range s.repairLandings {
		if e.node == n {
			entry = e
			break
		}
	}
	// Non-repair commands retain the ordinary landing behavior. An existing
	// patient whose target was captured/replaced instead seeks another base.
	if entry == nil && pad != nil && pad.Def != nil && (!pad.Def.IsAirBase || !pad.Def.Builder) {
		return s.legPadLanding(u, n, satisfied, tick, nil)
	}
	if entry == nil {
		entry = &repairLanding{unit: u, node: n, pad: pad, anchor: Vec3{X: u.X, Y: u.Y, Z: u.Z}}
		s.repairLandings = append(s.repairLandings, entry)
		// Restored and interrupted approaches acquire a fresh reservation
		// before descending. COB is queried here, never during save restore.
		if n.Phase > 1 {
			n.Phase = 1
		}
	}
	if entry.pad != pad || !s.usableRepairPad(u, pad) {
		pad = s.replacementRepairPad(u)
		entry.pad, entry.reserved = pad, false
		n.Target = 0
		if pad != nil {
			n.Target = pad.Handle
		}
		if n.Phase != 0 {
			n.Phase = 1
		}
	}
	if pad != nil {
		entry.anchor = Vec3{X: pad.X, Y: pad.Y, Z: pad.Z}
	}
	if n.Phase == 0 && pad != nil {
		return s.legPadLanding(u, n, satisfied, tick, entry)
	}
	if n.Phase == 0 {
		s.takeoffPreamble(u, n)
		n.Phase = 1
	}
	s.grantRepairPieces(pad)
	if !entry.reserved {
		// A deadline, independent of arrival, retries an occupied or lost pad.
		// The landing remains the head, keeping suspended battle orders inert.
		entry.post = s.repairHoldingPoint(entry)
		entry.holding = true
		m := s.newPointMarker(u, entry.post)
		m.setAltitudeOffset(int16(u.Def.CruiseAlt))
		m.setArrivalRadius(32)
		s.installAirGoal(u, n, m)
		n.Phase, n.DynamicGate = 1, 0
		airDeadline(n, tick, repairQueueRetry)
		return 2
	}
	if entry.holding || n.Phase == 1 || satisfied&0x40 != 0 {
		entry.holding = false
		n.Phase = 2
	}
	if n.Phase >= 3 {
		n.Param1 = uint32(entry.piece)
	}
	return s.legPadLanding(u, n, satisfied, tick, entry)
}

// repairHoldingPoint searches separated stations around the base. It ranks
// visible armed contacts conservatively by distance beyond their longest
// weapon range. Hidden positions never participate. No claim of guaranteed
// safety is possible when the base itself is under attack.
func (s *System) repairHoldingPoint(e *repairLanding) Vec3 {
	centre := e.anchor
	if e.pad != nil {
		centre = Vec3{X: e.pad.X, Y: e.pad.Y, Z: e.pad.Z}
	}
	ordinal := 0
	for _, other := range s.repairLandings {
		if other == e {
			break
		}
		if other.pad == e.pad {
			ordinal++
		}
	}
	directions := [...]struct{ x, z int64 }{{-1, 0}, {0, -1}, {1, 0}, {0, 1}, {-1, -1}, {1, -1}, {1, 1}, {-1, 1}}
	var points [16]Vec3
	var clearance [16]int64
	var crowded [16]bool
	var feasible [16]bool
	for i := range points {
		radius := int64(repairHoldRadius + repairHoldSpace*(ordinal/8+i/8))
		d := directions[(i+ordinal)%len(directions)]
		p := Vec3{X: centre.X + numeric.Fixed(d.x*radius<<16), Y: centre.Y, Z: centre.Z + numeric.Fixed(d.z*radius<<16)}
		if s.Terrain != nil {
			margin := numeric.Fixed(repairHoldSpace << 16)
			p.X = max(margin, min(p.X, numeric.Fixed(int64(s.Terrain.CellW*16)<<16)-margin))
			p.Z = max(margin, min(p.Z, numeric.Fixed(int64(s.Terrain.CellH*16)<<16)-margin))
		}
		points[i], clearance[i] = p, 1<<60
		feasible[i] = s.repairStationFree(e.unit, p)
		for _, other := range s.repairLandings {
			if other != e && other.holding && airPlanarDistance(p.X, p.Z, other.post.X, other.post.Z) < repairHoldSpace<<16 {
				crowded[i] = true
			}
		}
	}
	if b := airBinding(e.unit); b != nil && b.DangerVisible != nil {
		for _, threat := range s.world.IterSliced() {
			if !b.DangerVisible(e.unit, threat) {
				continue
			}
			var weaponRange int32
			for slot := 0; slot < units.NumSlots; slot++ {
				if w := threat.SlotAt(slot); w != nil && w.Weapon != nil {
					weaponRange = max(weaponRange, w.Weapon.Range)
				}
			}
			if weaponRange == 0 {
				continue
			}
			for i, p := range points {
				clearance[i] = min(clearance[i], (airPlanarDistance(p.X, p.Z, threat.X, threat.Z)>>16)-int64(weaponRange))
			}
		}
	}
	best := -1
	for i := range points {
		if !feasible[i] {
			continue
		}
		if best == -1 || clearance[i] > 0 && clearance[best] <= 0 ||
			(clearance[i] > 0) == (clearance[best] > 0) &&
				(crowded[best] && !crowded[i] || crowded[best] == crowded[i] && clearance[i] > clearance[best]) {
			best = i
		}
	}
	if best == -1 {
		return Vec3{X: e.unit.X, Y: e.unit.Y, Z: e.unit.Z}
	}
	return points[best]
}

// Check the destination footprint, not a ground path: aircraft use their
// ordinary flight integrator to reach it, potentially from across the map.
func (s *System) repairStationFree(u *units.Unit, p Vec3) bool {
	if s.Terrain == nil || s.Grid == nil {
		return true
	}
	c := handleRow(s.Collisions, u.Handle)
	if c == nil {
		return false
	}
	bx, bz := c.HalfBias()
	at := QuantizedAnchor(int32(p.X), int32(p.Z), bx, bz)
	if !commitRectInBounds(s.Terrain, at, c.FootPrintX, c.FootPrintZ) {
		return false
	}
	for z := int32(0); z < int32(c.FootPrintZ); z++ {
		for x := int32(0); x < int32(c.FootPrintX); x++ {
			if id, occupied := s.Grid.OccupantAtPlane(PlaneAir, Cell{X: at.X + x, Z: at.Z + z}); occupied && id != int(u.Handle) {
				return false
			}
		}
	}
	return true
}
