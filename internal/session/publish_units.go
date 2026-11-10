package session

import (
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
)

// The per-unit half of the publication boundary: one live unit's committed
// view, its order-queue snapshot and its radar contact. The unit channel
// (publish_parallel.go) runs these for every live unit, on the calling
// goroutine or on joined workers; both run exactly this code.
//
// Every read here is of committed state the tick has finished writing, and
// every write lands in the destination element, the element's own nested
// storage, or the caller's staging scratch. Nothing here may write shared
// session or service state: the workers depend on it [I1][I6].

// unitPublishScratch is one executor's reusable staging storage for order
// snapshots. Every value in it is overwritten or truncated before it is read,
// and the snapshot is copied into the committed frame's own storage in the
// same iteration, so no tick can observe the tick before it. It exists because
// the queue snapshot ran for every unit every tick and was a third of
// everything the simulation allocated.
type unitPublishScratch struct {
	orderSnapshot orders.SnapshotQueue
	routePoints   []orders.SnapshotRoutePoint
}

// fillUnitView writes one live unit's committed view through vp and, when the
// unit's queue is to be published (queued, decided by the channel before any
// view is built), returns its order-queue snapshot. id is the unit's
// presentation identity, allocated on the calling goroutine in slot order.
// status is the unit's sensor status word for the viewer
// (publishedSensorStatus), which feeds four of the view's fields.
//
// The view is built THROUGH the destination element rather than as a value
// that is then copied: UnitView is 256 bytes and this runs for every live unit
// every tick, which made the copy alone a measurable share of the publication.
func (s *Session) fillUnitView(vp *frame.UnitView, u *units.Unit, id uint64, queued bool, viewer uint8, reveal bool, status uint32, scratch *unitPublishScratch) (orders.SnapshotQueue, bool) {
	pieces, cargo := vp.Pieces, vp.Cargo
	*vp = frame.UnitView{
		InstanceID:        id,
		Slot:              u.Handle,
		AllocationSerial:  u.AllocationSerial,
		Owner:             u.Owner,
		X:                 u.X,
		Y:                 u.Y,
		Z:                 u.Z,
		Health:            u.Health,
		PriorHealthSample: u.PriorSample,
		MaxHealth:         u.MaxHealth,
		BuildRemaining:    u.Remaining,
		Flags:             status,
		Heading:           u.Move.Heading,
		Pitch:             u.Move.Pitch,
		Bank:              u.Move.Bank,
		Activated:         u.Activated,
		CloakRequested:    u.IsCloaked,
		Kills:             u.Kills,
		// The unit painter's pass selector is the committed low two
		// bits of the mover mode word, never a screen coordinate
		// [03 R-RAST-01 §7][04 R-MOV-01 §8].
		MoverMode: publishedMoverMode(u),
		// Step 2 of the visibility gate [03 §3.2], published as its two
		// inputs so presentation never has to guess at cloak state.
		// The published bit is the INSTANCE cloaked bit, not the
		// request: a unit whose owner could not pay this pass is drawn
		// [05 R-ECO-01 §9] (WU-19-92).
		Cloaked:    u.Hidden,
		Decloaking: status&visibility.DecloakBit != 0,
		// The carrier link the unit painter's per-unit present needs:
		// a carried child is drawn with its carrier, not only as its
		// own bucket entry [03 R-RAST-01 §7][04 R-UNIT-06 §3].
		Carrier:      u.Attachment.Carrier,
		CarriedPiece: publishedCarriedPiece(u.Attachment.AttachPiece),
		// The health-bar pass draws '0'+Group beside the bar of a unit
		// whose group number is nonzero [03 R-FX-01 §6][07 §9].
		Group: u.Group,
	}
	vp.CommunityHUD = s.publishCommunityHUD(u)
	vp.PatrolWork = s.publishedPatrolWork(u)
	vp.Pieces = pieces[:0]
	vp.Cargo = cargo[:0]
	// The footer's four rate fields read the archived production and
	// requested totals of the most recent settlement pass, not the
	// definition constants [07 R-HUD-03 §2][05 R-ECO-01 §5].
	if s.Econ != nil {
		archived := s.Econ.UnitArchived(u.Handle)
		vp.ArchivedMetalMake = archived[economy.Metal].Production
		vp.ArchivedEnergyMake = archived[economy.Energy].Production
		vp.ArchivedMetalUse = archived[economy.Metal].Requested
		vp.ArchivedEnergyUse = archived[economy.Energy].Requested
	}
	// The owner logo the footer blits at LOGO2 is the logos frame at the
	// owner's lobby colour index [07 R-HUD-03 §2]; it is the same
	// selector the minimap contacts already carry.
	vp.OwnerColor, vp.OwnerColorKnown = radarOwnerPalette(s, u.Owner, true)
	// The heading published is the unit record's own word and nothing
	// else. There is exactly one heading word — "the unit's current
	// heading word", the one the steering step just turned
	// [04 §8.1][R-MOV-01 §4] — and every mover surface beside it is a
	// copy that surface's own step maintains (I13).
	//
	// This used to prefer the ground steer record's copy, falling back
	// to the flight record and then to the collision record. Every unit
	// gets a steer record at EnsureUnit, including an aircraft, and the
	// air mover writes the unit record and the collision record but
	// never the steer — so an aircraft published the heading its steer
	// was seeded with at creation and never turned into the direction it
	// was flying. Reading the record itself is both correct and
	// unambiguous: the ground commit writes the record and both mirrors
	// together, so no reachable state has them disagreeing.
	if u.Def != nil {
		vp.DefName = u.Def.CanonicalKey
		vp.Model = u.Def.ObjectName
		vp.FootX = int8(u.Def.FootprintX)
		vp.FootZ = int8(u.Def.FootprintZ)
		if u.StructureFacing&1 != 0 {
			vp.FootX, vp.FootZ = vp.FootZ, vp.FootX
		}
		// Structure-builder classification follows the factory arm, never
		// authored CanMove (stock factories author CanMove) [04 R-FAC-02 §1].
		vp.IsFactory = u.Def.Builder && u.Def.BMCode == 0
		vp.BMCode = u.Def.BMCode != 0 // model-shading class gate [R-RND-02A]
		vp.ZBuffer = u.Def.ZBuffer    // composition height plane [R-REN-03A §2]
		// Model shadow gate and digger clip [R-REN-03D §1][R-REN-03A §8].
		vp.NoShadow = u.Def.NoShadow
		vp.CanHover = u.Def.CanHover
		vp.Floater = u.Def.Floater
		vp.Waterline = u.Def.Waterline
		vp.Digger = u.Def.Digger
		if id := s.Units.DefIDForHandle(u.Handle); id != 0 {
			vp.DefID = id
		}
		// Fixed vs mobile image-cache selector: retail uses runtime flag
		// (the unit's build-state bit and construction fraction) [rr-10],
		// but definition-level MaxVelocity==0 reliably identifies buildings
		// (all 21 factories and labs have 0, mobile have >0) and matches
		// the 2x building supersample expectation without needing runtime flags.
		if u.Def.MaxVelocity == 0 {
			vp.IsBuilding = true
		}
	}
	// The queued-order range overlay reads the live enabled bit for each
	// weapon slot, including retail's slot-three/slot-one gate asymmetry.
	// Publish the three value bits rather than exposing a slot or definition
	// pointer across the presentation boundary [06 R-WPN-05 §3]
	// [07 R-P0-11 §3][I6].
	for slot := range vp.EnabledWeaponSlots {
		vp.EnabledWeaponSlots[slot] = u.SlotAt(slot).IsEnabled()
	}
	// The held-round byte a MAKENUKE/MAKEANTI toy prints: the order
	// alias's build type is always zero and every shipped stockpile
	// weapon sits in slot 0, so slot 0's completed-round remainder is
	// the byte [07 R-P0-11 §2][06 §11.1][06 R-WPN-05 §2]. The host's
	// command page reads it for its page unit (CommandPageView).
	if slot := u.SlotAt(0); slot != nil {
		vp.StockpileRounds = slot.Ammo
	}
	// Copy the linkage owner's traversal order; it can change without
	// allocation when a child is detached and reattached [04 R-UNIT-06 §3].
	vp.Cargo = append(vp.Cargo, u.Attachment.Cargo...)
	// The hull extents and the underwater-exemption bit of the
	// four-point visibility gate [03 §3.2] steps 3 and 5.
	publishHullGateInputs(vp, u)
	vp.UnderwaterExempt = status&visibility.SonarBit != 0
	if s.Vis != nil {
		vp.DirectVisibilityKnown = true
		vp.DirectlyVisible = reveal || s.Vis.IsVisible(visibility.PlayerID(viewer), unitVisibilityTarget(u, status))
	}
	if vm := u.GetScript(); vm != nil {
		// CacheRevision is copied at the publication boundary; consuming or
		// clearing it here would make presentation cadence authoritative.
		vp.CacheRevision = vm.CacheRevision()
		vp.CacheValidityRevision = vm.CacheValidityRevision()
		var link []int
		if b := u.COBBinding(); b != nil && b.VM == vm {
			link = b.PieceMap
		}
		vp.Pieces = appendPieceViews(vp.Pieces[:0], vm, link)
	}
	if !queued {
		return orders.SnapshotQueue{}, false
	}
	q := orders.QueueOfUnit(u)
	activeHead := q.Head()
	// One staging value serves every unit this executor publishes: the
	// snapshot is copied straight into the committed frame's own reused
	// storage by the caller, so nothing outlives the next call.
	queue := orders.SnapshotQueueInto(scratch.orderSnapshot, q, u.Handle, func(n *orders.Node) []orders.SnapshotRoutePoint {
		// A route is authoritative only for the node that activated it;
		// movement.Route is keyed by unit for that active binding. Do not
		// attach a stale route to a queued node [04 §7.3].
		if n == nil || n != activeHead || s.Movement == nil {
			return nil
		}
		r := s.Movement.Routes[u.Handle]
		if r == nil || !r.Active || r.Count == 0 {
			return nil
		}
		points := scratch.routePoints
		if cap(points) < int(r.Count) {
			points = make([]orders.SnapshotRoutePoint, int(r.Count))
		}
		points = points[:int(r.Count)]
		for i := range points {
			p := r.Points[i]
			// Route points are already integer world coordinates; only
			// add the fixed fraction here [04 R-MOV-01 §3]. Treating
			// them as cells stretches the Shift trail sixteenfold.
			points[i] = orders.SnapshotRoutePoint{X: numeric.Fixed(p.X) << 16, Z: numeric.Fixed(p.Z) << 16}
		}
		scratch.routePoints = points
		return points
	})
	// Carry the grown snapshot storage forward: the next unit's snapshot
	// reuses it, and the frame keeps its own copy of what was written here.
	scratch.orderSnapshot = queue
	return queue, true
}

// unitQueuePublished is the one test of whether a unit's order queue enters
// the committed frame: it has a queue with a record in either segment.
func unitQueuePublished(u *units.Unit) bool {
	q := orders.QueueOfUnit(u)
	return q != nil && (q.LenPrimary() > 0 || q.LenSecondary() > 0)
}

// fillUnitContact writes one live unit's radar contact through cp. The
// contact keeps the ring storage the element already held: Reset truncated
// it to length zero and left its capacity, and reusing it spares a fresh
// slice for every armed unit every tick [03 §3.9] (R05). status is the unit's
// sensor status word for the viewer (publishedSensorStatus).
func (s *Session) fillUnitContact(cp *frame.RadarContactView, u *units.Unit, viewer uint8, reveal bool, status uint32) {
	active := u.Activated
	onOffable := false
	// The contact's cloak input is the INSTANCE cloaked bit and
	// nothing else. `init_cloaked` is consumed once, by the
	// constructor, and no longer feeds this predicate
	// [03 R-VIS-01 §6][05 R-ECO-01 §9] (RWU-19-26); the definition's
	// `stealth` flag no longer feeds it either, because stealth
	// suppresses radar and sonar detection and never line of sight
	// [03 R-VIS-01 §5]. Stealth still travels beside it, as its own
	// field, for the minimap blink gate of [03 §3.9] — the two are
	// distinct inputs and must not be folded.
	// The INSTANCE cloaked bit, never the request (WU-19-92).
	hidden := u.Hidden
	stealth := false
	if u.Def != nil {
		stealth = u.Def.Stealth
		onOffable = u.Def.OnOffable
	}
	// Sensor status remains latched between due passes, but activation,
	// cloak and definition inputs are current unit state at publication
	// [03 R-VIS-01 §4][03 §3.9]. A detached sensor snapshot may still
	// describe a prior occupant of this pool slot.
	ownerKnown := u.Owner < 10
	palette, paletteKnown := radarOwnerPalette(s, u.Owner, ownerKnown)
	// The contact status word's selected/range-status bit, which the
	// later circle branch reads, is the local selection: the host
	// composes it (frame.RadarContactView). The status word the
	// simulation holds carries no selected bit at a tick boundary.
	status &^= units.SelectedStatus
	rings := cp.Rings[:0]
	*cp = frame.RadarContactView{
		Kind: frame.RadarContactUnit, Handle: u.Handle, Owner: u.Owner, OwnerKnown: ownerKnown,
		X: u.X, Y: u.Y, Z: u.Z, Status: status,
		Hidden: hidden, Stealth: stealth, Active: active,
		OnOffable: onOffable,
		// The damage-flash byte of [06 R-WPN-04 §2], the blink gate's
		// per-unit term [03 §3.9]. The sim record holds it signed (the
		// sweep decrements -16 up to zero); the frame carries retail's
		// unsigned byte, so the wrap back to 240 is the conversion, not
		// a reinterpretation — presentation tests the byte against zero
		// and never for a magnitude.
		BlinkSuppress: uint8(u.BlinkSuppress),
		Seen:          status&visibility.SeenBit != 0,
		Friendly:      status&visibility.FriendlyMask != 0,
		Visible:       reveal || u.Owner == viewer || status&visibility.SeenBit != 0,
		Palette:       palette, PaletteKnown: paletteKnown,
		Rings: rings,
	}
	if u.Def == nil {
		return
	}
	cp.Graphic = u.Def.ObjectName
	// The selected-unit circle gate of [03 §3.9] "Selected-unit
	// circle gate correction" (Established): the selected/range-status
	// bit must be set, and the circles are then drawn when the
	// instance is active OR the definition's on/off bit is clear. An
	// inactive on/off-capable unit therefore publishes no range
	// distances, while a unit without that capability stays eligible.
	// The selected half is the host's local selection, so this
	// publishes the activation half and the distances it admits, and
	// the host composes RangeStatus from the two.
	// This is the ONLY producer of minimap circles — the sensor phase
	// rasterizes nothing [03 §3.10] correction of 2026-08-29.
	cp.RangeEligible = active || !onOffable
	if cp.RangeEligible {
		cp.RadarDistance = u.Def.RadarDistance
		cp.SonarDistance = u.Def.SonarDistance
		cp.RadarJam = u.Def.RadarDistanceJam
		cp.SonarJam = u.Def.SonarDistanceJam
	}
	// The blink-suppress input is published above, from the unit's
	// damage-flash byte [06 R-WPN-04 §2]. There is no authored `noradar`
	// key to go with it: the unit parser loads none, and the selected-unit
	// circle gate reads `onoffable` instead ([03 §3.9] "Selected-unit circle
	// gate correction").
	for slot := 0; slot < units.NumSlots; slot++ {
		ws := u.SlotAt(slot)
		if ws == nil || ws.Weapon == nil {
			continue
		}
		cp.Rings = append(cp.Rings, frame.RadarRingView{
			// antiweapons enables the unit loop; interceptor admits
			// each weapon ring [02 R-KEYS-01 §1][03 §3.9]. The ring is
			// dashed while the slot holds stockpiled rounds and solid
			// when it holds none [03 §3.9], so an antinuke shows whether
			// it is loaded and a slot without a stockpile draws solid.
			Enabled:   u.Def.AntiWeapons && ws.Weapon.Interceptor,
			Dashed:    ws.Ammo != 0,
			Range:     s.Combat.InterceptorRingRadius(ws.Weapon),
			Intercept: ws.Weapon.Interceptor,
		})
	}
}
