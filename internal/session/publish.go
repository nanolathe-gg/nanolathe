package session

import (
	"fmt"
	"math"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/features"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// CursorToWorld resolves the presentation cursor through the authoritative
// terrain resolver without exposing the mutable terrain service to UI code.
// The query is read-only and does not cross the human-command mutation path
// [07 §8][I6].
func (s *Session) CursorToWorld(x, z int32) (numeric.Fixed, numeric.Fixed, numeric.Fixed, bool) {
	if s == nil || s.World == nil {
		return 0, 0, 0, false
	}
	wx, wy, wz := s.World.CursorToWorld(x, z)
	return wx, wy, wz, true
}

// CursorAttackAdmits is the ATTACK cursor row's range test for an actor with
// no mover [07 §8]. Over a unit it asks runtime weapon slot 0's unit-to-unit
// admission gate — the one routine [06 R-WPN-05 §9] lists the cursor-shape
// chooser among the callers of — and over open ground the point form of the
// shot-time gate, after refusing a `toairweapon` slot outright: that
// target-class restriction answers the too-far shape even for a point in
// range [07 §8]. The bound combat rule set answers both, as it does for the
// order the click would issue.
//
// actor and target are presentation copies of committed unit views, which
// carry no weapon slots. Slot 0 is rebuilt from the definition's first weapon
// link because nothing but the creation initializer and save restore writes a
// slot's link, and both write exactly that [06 R-WPN-05 §3]. The query is
// read-only and draws no RNG [I6].
func (s *Session) CursorAttackAdmits(actor, target *units.Unit, x, y, z numeric.Fixed) bool {
	if s == nil || s.Combat == nil || actor == nil || actor.Def == nil {
		return false
	}
	w := actor.Def.Weapon1Def
	if content.IsWeaponInactive(w) {
		w = nil
	}
	probe := *actor
	probe.InstallWeapon(0, w)
	if target != nil {
		return s.Combat.CanEngageSlotTarget(&probe, target, 0, s.World)
	}
	if w == nil || w.ToAirWeapon {
		return false
	}
	return s.Combat.ShotTimeAdmitsPoint(&probe, 0, x, y, z, s.World)
}

// PlayArea returns the authored playable extents through the session query
// boundary. Presentation callers use it for minimap geometry without reaching
// into the mutable terrain object [03 §3.4][07 §10][I6].
func (s *Session) PlayArea() (int32, int32, bool) {
	if s == nil || s.World == nil || s.World.PlayRight <= 0 || s.World.PlayBottom <= 0 {
		return 0, 0, false
	}
	return s.World.PlayRight, s.World.PlayBottom, true
}

// PreviewPlacement is the authoritative read-only placement boundary used by
// the battle ghost. It reuses the same typed footprint, yard, occupancy, and
// terrain validator as construction. On rejection it still returns the site
// height derived by the same footprint scan so the ghost has no second rule
// set for its drawn height [04 §6.2][07 §9][I6].
// It passes retail's NULL player, under which the blocker's two occupancy
// rejections — the structure-yard mark of yard bit 0, and the ground occupant
// of bits 1–2 — apply unconditionally [04 R-P0-08-B §1]. That is the right
// answer for every caller except the human build cursor, which passes the
// local player's record and runs the known-site gate; PreviewPlacementForCursor
// below is that form.
func (s *Session) PreviewPlacement(cx, cz int32, def *content.UnitDef, footX, footZ int32, self pool.Handle) (world.PlacementResult, error) {
	return s.previewPlacement(cx, cz, def, footX, footZ, self, nil, units.FacingSouth)
}

// PreviewPlacementForCursor is PreviewPlacement with the blocker's fourth
// argument bound to the LOCAL player's record — the one caller in the whole
// executable that passes a player rather than null [04 R-P0-08-B §1]. It runs
// the known-site gate: the footprint centre is projected onto the
// 32-world-unit LOS grid with the height shear, a site off that grid or one
// the local viewing slot cannot currently see is rejected outright, and the
// mapping option then decides whether the occupancy rejections apply.
//
// The battle adapter's build ghost is the caller.
func (s *Session) PreviewPlacementForCursor(cx, cz int32, def *content.UnitDef, footX, footZ int32, self pool.Handle, facing ...units.StructureFacing) (world.PlacementResult, error) {
	if s == nil || s.Vis == nil {
		return world.PlacementResult{}, fmt.Errorf("session: placement visibility unavailable")
	}
	selected := units.FacingSouth
	if len(facing) != 0 {
		selected = facing[0]
	}
	return s.previewPlacement(cx, cz, def, footX, footZ, self, &sessionPlacementViewer{vis: s.Vis, local: s.ViewingOwner, player: s.LocalOwner}, selected)
}

// sessionPlacementViewer is the world package's PlacementViewer over the
// battle's visibility grids. The alias it tests is always the LOCAL viewing
// slot's bit; `player` is the record whose explored-grid byte the mapping
// option's gate reads [04 R-P0-08-B §1].
type sessionPlacementViewer struct {
	vis    *visibility.Service
	local  uint8
	player uint8
}

func (v *sessionPlacementViewer) ExploredExtent() (int32, int32) { return v.vis.W, v.vis.H }

func (v *sessionPlacementViewer) LocallyVisible(vx, vz int32) bool {
	mask := v.vis.WordMask()
	idx := int(vz*v.vis.W + vx)
	if idx < 0 || idx >= len(mask) {
		return false
	}
	return mask[idx]&(1<<v.local) != 0
}

func (v *sessionPlacementViewer) Explored(vx, vz int32) bool {
	grid := v.vis.ByteGrid(visibility.PlayerID(v.player))
	idx := int(vz*v.vis.W + vx)
	if grid == nil || idx < 0 || idx >= len(grid) {
		return false
	}
	return grid[idx] != 0
}

// MappingOption is the LOS-mode word's bit 1 [03 §3.1].
func (v *sessionPlacementViewer) MappingOption() bool { return v.vis.CurrentEnabled() }

func (s *Session) previewPlacement(cx, cz int32, def *content.UnitDef, footX, footZ int32, self pool.Handle, viewer world.PlacementViewer, facing units.StructureFacing) (world.PlacementResult, error) {
	if s == nil || s.World == nil {
		return world.PlacementResult{}, fmt.Errorf("session: placement world unavailable")
	}
	if def == nil {
		return world.PlacementResult{}, fmt.Errorf("session: placement definition unavailable")
	}
	extent, err := world.NewFootprintExtent(footX, footZ)
	if err != nil {
		return world.PlacementResult{}, err
	}
	rect, err := world.NewFootprintRect(world.NewFootprintAnchor(cx, cz), extent)
	if err != nil {
		return world.PlacementResult{}, err
	}
	rules, err := world.PlacementRulesForUnit(s.Catalog, def)
	if err != nil {
		return world.PlacementResult{}, err
	}
	var yard []world.YardCell
	if def.BMCode == 0 {
		if s.Build != nil {
			var geometry construction.StructureGeometry
			geometry, err = s.Build.StructureGeometry(def, facing)
			yard = geometry.Yard
		} else {
			yard, err = world.ParseYardMap(def.YardMap, int(footX), int(footZ))
		}
		if err != nil {
			return world.PlacementResult{}, err
		}
	}
	var admit func(uint16) bool
	if viewer != nil && s.Build != nil && s.Units != nil {
		builder := s.humanUnit(self)
		if builder != nil {
			admit = func(occupant uint16) bool {
				return s.Build.AdmitSiteOccupant(builder, s.Units.Unit(pool.Handle(occupant)))
			}
		}
	}
	result, err := s.World.CheckPlacement(world.PlacementQuery{Rect: rect, Yard: yard, Rules: rules, Self: uint16(self), Mobile: def.BMCode != 0, Viewer: viewer, AdmitOccupant: admit})
	if err != nil {
		result = world.PlacementResult{Rect: rect, SiteHeight: s.World.SiteHeight(cx, cz, yard, int(footX), int(footZ), def.Waterline)}
	}
	return result, err
}

// publishSnapshot publishes one immutable frame after every completed sub-tick [PLAN_03 C15].
func (s *Session) publishSnapshot(tick uint32) { s.publishFrame(tick, false) }

// publishPausedSnapshot replaces the committed view of the tick that already
// ran, after the paused-input boundary applied commands
// (docs/DESIGN_INTERFACE_HUD_INPUT.md §3.12). It writes the same fields from
// the same authoritative state; nothing here is a second publication path.
//
// Every per-tick one-shot the frame carries is safe under repetition because
// its producer is reset at its own commit: the staged presentation events are
// reset by the previous publication, the big-brother notices by the boundary
// itself, and the remaining channels — effects, debris, fragments, strips,
// radar, result, economy, shake — are snapshots of live state that no phase
// has advanced [03 §1][I6].
func (s *Session) publishPausedSnapshot(tick uint32) { s.publishFrame(tick, true) }

func (s *Session) publishFrame(tick uint32, paused bool) {
	if s.publication != nil {
		s.publication.wrecks.prune(s.Features, tick)
	}
	if s.Snapshot == nil {
		return
	}
	published := s.Snapshot.BeginWrite()
	if published == nil {
		return
	}
	published.Tick = tick
	if s.Wind != nil {
		published.Wind = frame.WindView{Heading: s.Wind.Heading, Strength: s.Wind.Strength}
	}
	// The playback perspective is presentation-only (replay_view.go).
	viewer, reveal := s.publicationPerspective()
	published.ViewingPlayer = viewer
	if s.Rules.Visibility != nil {
		published.MainViewRadarDots = s.Rules.Visibility.MainViewRadarDots()
	}
	publication := s.ensurePublicationState()
	publication.beginUnitIdentities()
	defer publication.finishUnitIdentities()
	published.Paused = s.Clock != nil && s.Clock.Paused
	// The §6 slide strip's two non-tick readouts. Both are scheduling/lobby
	// scalars the composer may not read live [07 §6][07 R-HUD-04 §4][I6].
	published.Strip = frame.StripReadout{UnitLimit: sessionUnitLimit(s)}
	if s.Clock != nil {
		published.Strip.ActiveSpeed = s.Clock.Active
		published.Strip.RequestedSpeed = s.Clock.Requested
	}
	if s.publication != nil && s.publication.effects != nil {
		published.Effects = s.publication.effects.SnapshotInto(published.Effects)
	} else {
		published.Effects = published.Effects[:0]
	}
	published.Debris = s.publishDebris(published.Debris[:0])
	s.filterOnlineVisuals(published)
	published.Fragments = s.publishFragments(published.Fragments[:0])
	// Every live strip sub-record, in the composer's walk order [03 §1]. This
	// is the one writer of the committed strip channel, and it runs once per
	// tick inside the publication boundary [I6].
	published.Strips = s.appendStripViews(tick, published.Strips[:0])
	if s.Units != nil {
		views := published.Units[:0]
		orderQueues := published.OrderQueues[:0]
		s.publishUnitScratch = s.Units.AppendLive(s.publishUnitScratch[:0]) // pool slot ascending (I1)
		for _, u := range s.publishUnitScratch {
			if u == nil || !u.Alive {
				continue
			}
			// The view is built THROUGH the destination element rather than
			// as a value the append then copies: UnitView is 256 bytes and
			// this loop runs for every live unit every tick, which made the
			// copy alone a measurable share of the publication [I6].
			views = reserveUnitView(views)
			vp := &views[len(views)-1]
			pieces, cargo := vp.Pieces, vp.Cargo
			*vp = frame.UnitView{
				InstanceID:        publication.unitIdentity(u),
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
				Flags:             s.sensorStatus(viewer, u),
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
				Decloaking: s.sensorStatus(viewer, u)&visibility.DecloakBit != 0,
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
			vp.UnderwaterExempt = s.sensorStatus(viewer, u)&visibility.SonarBit != 0
			if s.Vis != nil {
				vp.DirectVisibilityKnown = true
				vp.DirectlyVisible = reveal || s.Vis.IsVisible(visibility.PlayerID(viewer), unitVisibilityTarget(u, s.sensorStatus(viewer, u)))
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
			if q := orders.QueueOfUnit(u); q != nil && (q.LenPrimary() > 0 || q.LenSecondary() > 0) {
				activeHead := q.Head()
				// One staging value serves every unit and every tick: the
				// snapshot is copied straight into the committed frame's own
				// reused storage below, so nothing outlives the next call.
				queue := orders.SnapshotQueueInto(s.orderSnapshotScratch, q, u.Handle, func(n *orders.Node) []orders.SnapshotRoutePoint {
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
					points := s.routePointScratch
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
					s.routePointScratch = points
					return points
				})
				orderQueues = appendOrderQueueView(orderQueues, queue, s.Catalog)
				s.orderSnapshotScratch = queue
			}
		}
		published.Units = views
		published.OrderQueues = orderQueues
		// The local seat. The selection, the build page and the command page are
		// the client's local interface state (DESIGN_MULTIPLAYER §7.3): the host
		// composes them onto the frames it presents (frame.SelectionView,
		// hud.ComposeCommandPage), so this publication leaves them at their zero
		// value and copies each status word as the simulation holds it. Only a
		// retail save's restore writes a selected bit there; the host seeds its
		// selection from those words once (hud.LocalInterface.AdoptStatusWords)
		// and composes its own bit over them.
		published.Selection.LocalPlayer = s.LocalOwner
	}
	// Visibility masks are copied for the viewing player; their
	// mode-dependent/raw representation remains owned by visibility [03 §3.1–§3.2].
	// Radar is a separate presentation surface, not a mask
	// published by visibility.Service [03 §3.4], so it remains unset. The
	// Visibility publishes immutable presentation revisions; source identity
	// prevents a fresh service from restoring another service's retained bytes.
	if s.Vis != nil {
		publishVisibilityView(s.Vis, viewer, published)
		// Step 3 of the gate compares against the scaled sea-level byte, never
		// against zero [03 §3.2][03 §2.2].
		published.Visibility.SeaLevel = publishedSeaLevel(s.World)
		s.Vis.RebuildFog(0, 0)
		if fc := s.Vis.Fog(); fc != nil {
			version := s.Vis.FogVersion()
			source := s.Vis.PresentationIdentity()
			if !published.RestoreFog(source, version) {
				w, h := fc.Dimensions()
				published.Fog.W = w
				published.Fog.H = h
				published.Fog.OriginX, published.Fog.OriginZ = fc.Origin()
				// One copy, from the cache straight into this frame slot's
				// own storage; the published bytes never alias the cache the
				// next rebuild writes [I6].
				published.Fog.Ch0, published.Fog.Ch1 = fc.CopyChannelsInto(published.Fog.Ch0, published.Fog.Ch1)
				published.Fog.Version = version
				published.Fog.Source = source
			}
			published.Fog.Valid = s.Vis.FogCacheValid()
		}
		s.publishPerspectiveView(published, viewer, reveal)
	} else {
		published.Visibility = frame.VisibilityView{}
		published.Fog = frame.FogView{}
	}
	s.publishFeatures(published, publication)
	published.Projectiles = published.Projectiles[:0]
	if s.Combat != nil {
		for i := 0; i < s.Combat.Count(); i++ {
			h := pool.Handle(i + 1)
			if !s.Combat.Alive(h) {
				continue
			}
			if i < 0 || i >= len(s.Combat.Records) {
				continue
			}
			// Read the record in place. A copy was moved to the heap by the
			// marker query below taking its address, one allocation per live
			// projectile per tick; the query only reads it.
			p := &s.Combat.Records[i]
			owner, ownerKnown := projectileOwnerFromRecord(s, p.Shooter, p.ShooterSide)
			pv := frame.ProjectileView{
				PresentationID: s.Combat.PresentationID(h),
				Handle:         h,
				Owner:          owner,
				OwnerKnown:     ownerKnown,
				X:              p.Pos.X,
				Y:              p.Pos.Y,
				Z:              p.Pos.Z,
				WeaponID:       p.WeaponID,
				Shooter:        p.Shooter,
				// The frame carries retail's yaw word: combat's own stored
				// yaw is half a turn from it (a documented transform,
				// [06 R-WPN-05 §11]) and the renderer's projectile angle
				// block is written over retail's [03 §5.2]. Pitch shares
				// retail's numbering already.
				Yaw:            combat.RetailYaw(p.Yaw),
				Pitch:          uint16(p.Pitch),
				Roll:           uint16(p.Roll),
				PropellerRoll:  uint16(p.PropellerYaw),
				MeteorPitch:    uint16(p.MeteorPitch),
				StartX:         p.StartPos.X,
				StartY:         p.StartPos.Y,
				StartZ:         p.StartPos.Z,
				TailX:          p.StartPos.X,
				TailY:          p.StartPos.Y,
				TailZ:          p.StartPos.Z,
				VX:             p.Velocity.X,
				VY:             p.Velocity.Y,
				VZ:             p.Velocity.Z,
				CreationTick:   p.CreationTick,
				ExpiryTick:     p.ExpiryTick,
				BurstRemaining: p.BurstRemaining,
				MuzzlePiece:    int32(p.MuzzlePiece),
				Target:         p.TargetUnit,
				TargetX:        p.TargetPos.X,
				TargetY:        p.TargetPos.Y,
				TargetZ:        p.TargetPos.Z,
				TrailFrame:     0,  // no authored/runtime trail-frame field is established [I9]
				Selector:       -1, // suppression sentinel until the weapon record's `color` byte replaces it below [06 R-WFX-01 §1]
			}
			if s.Catalog != nil {
				if w, ok := s.Catalog.WeaponByID(p.WeaponID); ok && w != nil {
					pv.Model = w.Model
					pv.Graphic = w.Model
					pv.RenderType = w.RenderType
					// Creation-family is established via Weapon record flags
					// (Ballistic/VLaunch/etc.) and is presentation-relevant per [03 §5.4] C6 [06 §6.2].
					pv.Family = int32(combat.CreationFamilyForWeapon(w))
					pv.SmokeTrail = w.SmokeTrail
					pv.Propeller = w.Propeller
					pv.Meteor = w.Meteor
					// The lifetime-scaled render type's divisor is named: render
					// type 5 draws frame
					// `N - ((expiry - currentTick) * N) / weapontimer`, and a zero
					// `weapontimer` is retail's own divide by zero there
					// [06 R-WFX-01 §4]. [03 §5.4] left the input as "commonly
					// WeaponTimer or Duration"; it is WeaponTimer, so the view
					// carries the compiled field (already `weapontimer × 30`
					// truncated at catalog compile time, per I8).
					pv.Lifetime = w.WeaponTimer
					// The two authored presentation bytes. The weapon record
					// stores `color` and `color2` as BYTES [06 R-WFX-01 §1],
					// and `color` has two readers: it is the palette index
					// the beam (type 0) and lightning (type 7) strokes are
					// drawn in, and it is the sequence selector of render
					// type 4 — 0 `cannonshell`, 1 `plasmasm`, 2 `plasmamd`,
					// 3 `ultrashell`, 4 `plasmasm`, with 5..254 drawing
					// nothing and 255 read as −1, which suppresses the whole
					// case [06 R-WFX-01 §4].
					//
					// Publishing the pair is what makes stock guns, shells
					// and lasers visible at all. The draw dispatch suppresses
					// a record whose colour is absent and a type-4 record
					// whose selector is the −1 sentinel, so the unconditional
					// sentinel this replaces left every render-type-4 weapon
					// (82 of the 198 stock weapons, the Peewee's `emg` among
					// them) and every type-0 beam drawing nothing at all.
					pv.PrimaryColor = uint8(w.Color)
					pv.HasPrimaryColor = true
					pv.SecondaryColor = uint8(w.Color2)
					pv.HasSecondaryColor = true
					// The selector is that same byte read as a signed value,
					// which is what turns the 255 the stock `earthquake`
					// authors into the −1 sentinel [06 R-WFX-01 §1]. Any
					// other out-of-range byte resolves no entry and the
					// resolver suppresses it there instead.
					pv.Selector = int32(int8(uint8(w.Color)))
					// The minimap projectile pass's art selector. Its three
					// terms are weapon-definition flags, tested in this order:
					// a `targetable` or `interceptor` weapon draws the
					// `nuclogo` marker, a `noradar` weapon draws nothing at
					// all, and every other weapon draws the 1×1 dot
					// [03 §3.9] layer 6 [06 §11.3]. Resolving it once here is
					// what keeps presentation off the compiled catalog [I6].
					switch {
					case !s.Combat.MapWeaponMarker(p, w):
						pv.RadarArt = frame.RadarProjectileHidden
					case w.Targetable || w.Interceptor:
						pv.RadarArt = frame.RadarProjectileMarker
					case w.NoRadar:
						pv.RadarArt = frame.RadarProjectileHidden
					default:
						pv.RadarArt = frame.RadarProjectileDot
					}
				}
			}
			// The cached average floor height the ground shadow is anchored
			// against [06 §8.1][03 §5.4].
			publishProjectileFloorHeight(&pv, p)
			published.Projectiles = append(published.Projectiles, pv)
		}
	}
	// Radar contacts and callback circles are copied only after all
	// authoritative pools have been traversed. Presentation therefore receives
	// one coherent tick-end view and never needs to bind callbacks or inspect
	// mutable session services [03 §3.4][03 §3.9].
	published.Radar.BlinkPhase = s.RadarBlinkPhase() // completed phase 12 [01 R-CORE-03]
	published.Radar.Contacts = published.Radar.Contacts[:0]
	// The destination slot's previous contents are still live in the backing
	// array beneath the truncated length above (Reset kept every capacity,
	// including each contact's nested Rings backing array, truncated to
	// length zero). Re-slicing to the full capacity recovers them so the loop
	// below can hand a unit's contact its old ring storage back instead of
	// growing a fresh slice every armed unit, every tick [03 §3.9] (R05).
	existingContacts := published.Radar.Contacts[:cap(published.Radar.Contacts)]
	// The debug display mode is session state written only by the film-mode key
	// set [03 §3.12][07 R-CAM-01 §9]. Preserve its exact value; presentation
	// must not manufacture a mode at the frame boundary [I6]. The frame field's
	// "MarkerMode" name predates the trace that identified the byte.
	published.Radar.MarkerMode = s.DebugDisplayMode
	published.Radar.MappingLOS = uint8(s.Vis.Mode()) & 3
	if s.Units != nil {
		s.radarUnitScratch = s.Units.AppendLive(s.radarUnitScratch[:0]) // pool slot ascending (I1)
		for _, u := range s.radarUnitScratch {
			if u == nil || !u.Alive {
				continue
			}
			status := s.sensorStatus(viewer, u)
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
			contactIdx := len(published.Radar.Contacts)
			contact := frame.RadarContactView{
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
			}
			if contactIdx < len(existingContacts) {
				// Reuse the slot's previous ring backing array (already
				// truncated to zero length by Reset, capacity intact) instead
				// of appending into a fresh nil slice [03 §3.9] (R05).
				contact.Rings = existingContacts[contactIdx].Rings[:0]
			}
			if u.Def != nil {
				contact.Graphic = u.Def.ObjectName
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
				contact.RangeEligible = active || !onOffable
				if contact.RangeEligible {
					contact.RadarDistance = u.Def.RadarDistance
					contact.SonarDistance = u.Def.SonarDistance
					contact.RadarJam = u.Def.RadarDistanceJam
					contact.SonarJam = u.Def.SonarDistanceJam
				}
				// The blink-suppress input is now published above, from the
				// unit's damage-flash byte [06 R-WPN-04 §2]. There is no authored
				// `noradar` key to go with it: the unit parser loads none, and the
				// selected-unit circle gate reads `onoffable` instead
				// ([03 §3.9] "Selected-unit circle gate correction").
				for slot := 0; slot < units.NumSlots; slot++ {
					ws := u.SlotAt(slot)
					if ws == nil || ws.Weapon == nil {
						continue
					}
					contact.Rings = append(contact.Rings, frame.RadarRingView{
						// antiweapons enables the unit loop; interceptor admits
						// each weapon ring [02 R-KEYS-01 §1][03 §3.9].
						Enabled:   u.Def.AntiWeapons && ws.Weapon.Interceptor,
						Dashed:    ws.Weapon.Interceptor,
						Range:     s.Combat.InterceptorRingRadius(ws.Weapon),
						Intercept: ws.Weapon.Interceptor,
					})
				}
			}
			published.Radar.Contacts = append(published.Radar.Contacts, contact)
		}
	}
	for _, p := range published.Projectiles {
		owner := p.Owner
		palette, paletteKnown := radarOwnerPalette(s, owner, p.OwnerKnown)
		published.Radar.Contacts = append(published.Radar.Contacts, frame.RadarContactView{
			Kind: frame.RadarContactProjectile, Handle: p.Handle, Owner: owner, OwnerKnown: p.OwnerKnown, Palette: palette, PaletteKnown: paletteKnown, X: p.X, Y: p.Y, Z: p.Z,
			Graphic: p.Graphic, AssetID: p.AssetID, Status: p.Flags, RadarArt: p.RadarArt,
			Visible: reveal || radarPointVisible(s, owner, p.OwnerKnown, p.X, p.Y, p.Z),
		})
	}
	if s.Build != nil && s.Units != nil {
		// BuilderLinks is the construction service's authoritative product→builder
		// relation [05 C18]. SnapshotLinks provides deterministic product order;
		// queue index, accepted work, and stall state have no published source yet.
		for _, link := range s.Build.SnapshotLinks() {
			builder := s.Units.Unit(link.Builder)
			product := s.Units.Unit(link.Product)
			if builder == nil || product == nil || !builder.Alive || !product.Alive {
				continue
			}
			b := frame.BuildProgressView{
				Builder:    link.Builder,
				Product:    link.Product,
				Remaining:  product.Remaining,
				Health:     product.Health,
				MaxHealth:  product.MaxHealth,
				QueueIndex: -1, // queue position is O5 and is not exposed here
			}
			if product.Def != nil {
				b.ProductKey = product.Def.CanonicalKey
				b.FootX = int8(product.Def.FootprintX)
				b.FootZ = int8(product.Def.FootprintZ)
			}
			// The runtime building-class status bit, not authored mobility:
			// stock factories (kbot lab included) author CanMove=1, so a
			// CanMove/CanFly heuristic misclassifies every stock factory as
			// not-a-factory. The bit is derived once, at allocation, from the
			// definition's authored bmcode [05 "Factory production
			// lifecycle"] — the same source construction's isMobileBuilder
			// reads (internal/construction/factory.go) (review finding R08).
			b.Factory = builder.Flags&units.BuildingClassStatus != 0
			published.Builds = append(published.Builds, b)
		}
	}
	if s.Econ != nil {
		published.Economy = published.Economy[:0]
		for p := 0; p < 10; p++ {
			pl := s.Econ.Players[p]
			if !pl.Exists {
				continue
			}
			view := frame.EconomyView{
				Player:         uint8(p),
				Metal:          pl.Stock[economy.Metal],
				Energy:         pl.Stock[economy.Energy],
				MetalCapacity:  pl.Capacity[economy.Metal],
				EnergyCapacity: pl.Capacity[economy.Energy],
				MetalProduced:  pl.PassProduced[economy.Metal],
				MetalConsumed:  pl.PassConsumed[economy.Metal],
				EnergyProduced: pl.PassProduced[economy.Energy],
				EnergyConsumed: pl.PassConsumed[economy.Energy],
				DisplayTimer:   pl.DisplayTimer,
				// The top strip marks each automatic-sharing threshold on its
				// bar [07 R-HUD-03 §4][05 R-SHARE-01 §3].
				MetalShareThreshold:  pl.MetalShareThreshold,
				EnergyShareThreshold: pl.EnergyShareThreshold,
				Active:               pl.Exists && !pl.IsObserver,
			}
			s.survivalTeamEconomy(&view)
			published.Economy = append(published.Economy, view)
		}
	} else {
		published.Economy = published.Economy[:0]
	}
	publishPlayerRows(s, published)
	// The tick's local-interface facts; none at the paused-input boundary,
	// which runs no sweep (interface_facts.go).
	s.publishInterfaceFacts(published)
	// Shake offset produced at phase 10 [03 §5.6][01 §4.4] DET-04.
	published.ShakeOffsetX = s.shakeOffsetX
	published.ShakeOffsetY = s.shakeOffsetY
	published.ShakeActive = s.shakeActive
	published.ShakeDuration = s.shakeDuration
	published.ShakeRemaining = s.shakeRemaining
	published.ShakeAmpX = s.shakeAmpX
	published.Survival = s.survivalFrameStatus(tick)
	published.ShakeAmpY = s.shakeAmpY

	// RS-05: publish the authoritative result once through the committed frame
	// [08 "Evaluation"][P1-01]. A pending result keeps its metadata while the
	// latch countdown is exposed for presentation; no second result side-channel
	// exists.
	{
		r, countdown := s.result, s.Latch.Countdown
		if s.onlineResults != nil {
			// Each admitted seat owns its pending and final result; publication
			// selects this client's row (DESIGN_MULTIPLAYER §16.4.1).
			row := &s.onlineResults.seats[s.LocalOwner]
			r, countdown = row.result, row.latch.Countdown
		}
		columnMaxima := r.ColumnMaxima
		if columnMaxima == [7]int{} {
			columnMaxima = resultColumnMaxima(r.Scores)
		}
		published.Result = frame.ResultView{
			Ended:        r.Ended,
			Kind:         r.Kind,
			WinnerTeam:   r.WinnerTeam,
			Reason:       r.Reason,
			Tick:         r.Tick,
			ArmedTick:    r.ArmedTick,
			Countdown:    r.Countdown,
			Draw:         r.Draw,
			ColumnMaxima: columnMaxima,
		}
		published.Result.Winners = copyIntsInto(published.Result.Winners, r.Winners)
		published.Result.Losers = copyIntsInto(published.Result.Losers, r.Losers)
		published.Result.Scores = copyScoresInto(published.Result.Scores, r.Scores)
		if !r.Ended {
			published.Result.Countdown = countdown
		}
	}
	if s.publication != nil && s.publication.events != nil {
		published.Events = s.publication.events.SnapshotEventsInto(published.Events)
	} else {
		published.Events = published.Events[:0]
	}
	s.publishDeveloper(published)
	if err := s.commitFrame(tick, paused); err != nil {
		panic(fmt.Sprintf("session: committed frame publication failed at tick %d: %v", tick, err))
	}
	if s.publication != nil && s.publication.events != nil {
		s.publication.events.Reset()
	}
}

// appendPieceViews appends one committed lane per script piece of vm, in
// script piece order [03 §2.4]. link is the unit binding's script-to-model
// piece link [04 R-COB-01 §4]: each lane carries the model piece it poses, so
// presentation draws a script piece exactly where the simulation composes it —
// including a name the model lacks, which animates the model piece its slot
// took, and a piece beyond the model (-1), which poses nothing. A script
// attached without a model binding has no link; its lanes carry the script
// piece names for presentation to resolve instead.
//
// The VM's live piece array is read, not copied: the loop consumes it before
// anything else runs, and a per-unit allocation here was a measurable share
// of the publication [I6].
func appendPieceViews(dst []frame.PieceView, vm *cob.VM, link []int) []frame.PieceView {
	vmPieces := vm.Pieces
	if len(vmPieces) == 0 {
		return dst
	}
	flags := vm.RenderPieceFlags()
	var names []string
	if link == nil {
		if prog := vm.Program(); prog != nil {
			names = prog.Pieces
		}
	}
	for i := range vmPieces {
		ps := &vmPieces[i]
		if len(dst) == cap(dst) {
			dst = append(dst, frame.PieceView{})
		} else {
			dst = dst[:len(dst)+1]
		}
		pv := &dst[len(dst)-1]
		*pv = frame.PieceView{
			Index: i,
			RotX:  ps.RotX,
			RotY:  ps.RotY,
			RotZ:  ps.RotZ,
			Tx:    ps.Trans[0],
			Ty:    ps.Trans[1],
			Tz:    ps.Trans[2],
		}
		switch {
		case link != nil && i < len(link):
			pv.Index = link[i]
		case link != nil:
			pv.Index = -1
		case i < len(names):
			pv.Name = names[i]
		}
		if i < len(flags) {
			f := flags[i]
			pv.Hidden = (f & 0x01) == 0     // show bit [04 §4.3]
			pv.DontCache = (f & 0x02) == 0  // cache bit [04 §4.3]
			pv.DontShade = (f & 0x04) == 0  // shade bit [04 §4.3] 0x1000d/e000
			pv.DontShadow = (f & 0x08) == 0 // dont-shadow [04 §4.3] 0x1000a000
		}
	}
	return dst
}

// commitFrame closes the pending write. An ordinary publication advances the
// committed tick; a paused one replaces the committed view of the tick that
// already ran and therefore must not [01 §4.4][I6]. A paused boundary reached
// before anything has been published — a session that never ticked and has no
// opening frame — takes the ordinary path, because there is no committed view
// of that tick to replace.
func (s *Session) commitFrame(tick uint32, paused bool) error {
	if paused {
		if last, published := s.Snapshot.PublishedTick(); published && last == tick {
			return s.Snapshot.Republish(tick)
		}
	}
	return s.Snapshot.Publish(tick)
}

// publishPlayerRows publishes the ten player slots' live rows once per tick,
// inside the same publication boundary every other committed field is written
// in. It is the only writer of frame.Frame.Players.
//
// The Space-held score panel's row filter reads six terms per slot — the
// record is present; the controller byte is 1, 2 or 3; the side byte is not
// the neutral 10; the live-unit count is nonzero or the slot's auxiliary word
// is zero; and the lobby record's watcher bit is clear — emits the rows in
// rank order, and prints either the kill/loss pair or the commander pair
// [07 R-HUD-04 §1]. Every one of those is copied out of authoritative state
// here: nothing is mutated, no RNG is drawn, and no live pointer crosses the
// boundary [I6].
//
// Slots are visited 0..9 ascending, never through a map [I1].
func publishPlayerRows(s *Session, published *frame.Frame) {
	published.Players = [frame.PlayerRowSlots]frame.PlayerRow{}
	if s == nil || s.Econ == nil {
		return
	}
	for i := 0; i < frame.PlayerRowSlots; i++ {
		p := s.Econ.Players[i]
		row := frame.PlayerRow{
			Present: p.Exists,
			Name:    p.Name,
			Logo:    p.Logo,
			// The four counters are the per-slot words the death-credit switch
			// writes at the death site, not a rescan of the live pool
			// [06 §12.1][08 R-CAMP-01 §7].
			Kills:            int(p.Kills),
			Losses:           int(p.Losses),
			CommandersKilled: int(p.CommanderKills),
			CommandersLost:   int(p.CommanderLosses),
			Controller:       p.ControllerState,
			Side:             p.Side,
			// Established: the lobby record's watcher bit (0x40) has exactly two
			// retail writers and both are multiplayer-only — the battleroom's
			// SIDE control cycled past the last side, and the kind-3 branch of
			// the elimination handler ("Continue Watching?"). Skirmish
			// elimination, slot registration and battle entry never set it, so in
			// every single-player session it is constantly clear and
			// economy.Player.Watcher correctly has no session-side writer. The
			// panel reads it purely as a row exclusion, and it is NOT retail's
			// observer byte, which is a different field [07 R-HUD-04 §1 "the
			// watcher bit's writers"]. IsObserver is this build's own observer
			// controller kind, not a retail skirmish slot; it is ORed in so that
			// such a slot is excluded the way a retail watcher would be, matching
			// the result-row gate and the local-watcher test that already spend
			// it as the same exclusion.
			Watcher: p.Watcher || p.IsObserver,
			// Established: the auxiliary word has no writer. The static trace this
			// site named as its decider has been run — the word is read at ten
			// sites (this panel, the score helper's row gate, the elimination and
			// participant filters, the per-player economy gate, the multiplayer
			// alliance vote), every one a bare zero test, and it is STORED nowhere
			// outside the player record's bulk initialisation, so it is zero from
			// the session block's allocation onward and a reimplementation
			// publishes zero [08 R-CAMP-01 §7][07 R-HUD-04 §1]. With it zero the
			// panel's second term is always satisfied, so a slot keeps its row
			// after losing its last unit. economy.Player.ResultAuxiliary is
			// therefore a published constant zero; it is retained rather than
			// deleted because it is the field both readers name — see its
			// declaration.
			Auxiliary: p.ResultAuxiliary,
			// The authoritative rank byte, copied out like every other term:
			// registration seeds it to the slot index and the kill-lead shift is
			// its only other writer [08 R-SKIR-01 §2][08 R-CAMP-01 §9]
			// [07 R-HUD-04 §1]. The panel's vacated-rank compaction runs on the
			// published copy, never back onto the record.
			Rank: p.Rank,
			// Preserve the acting player's directional row exactly. The cursor
			// predictor uses this committed copy for the same hostility answer as
			// the order resolver; it must not combine the reverse declaration
			// [04 §3.4][05 R-SHARE-01 §1][I6].
			Allies: p.Allies,
		}
		if s.Units != nil {
			row.LiveUnits = s.Units.LiveCountForPlayer(i)
			if p.Exists {
				if first, _, ok := s.Units.SliceForPlayer(i); ok {
					row.UnitSlotStart = pool.Handle(first)
				}
			}
		}
		// The name and the logo byte are the lobby record's, and this build
		// writes both at registration [08 R-SKIR-01 §2]. The setup-row
		// fallback that used to stand here answered nothing after a load,
		// where only the five rule words and the map name come back into
		// the setup record [08 R-SKIR-01 §2] "Save persistence".
		published.Players[i] = row
	}
}

// CommandPageProducts is the bound construction rule's complete product
// membership for a builder definition: the CommandPageView.AllowedProducts
// the host composes for its page unit, independent of the visible page. It is
// a read-only query of the immutable catalog and the bound rule, on the
// host's goroutine; presentation never reselects gameplay rules [I6]. A
// nonnil empty slice is an authoritative empty list.
func (s *Session) CommandPageProducts(builder string) []string {
	out := make([]string, 0)
	if s == nil {
		return out
	}
	return append(out, s.buildProducts(builder)...)
}

// radarOwnerPalette resolves the player-record color used as the authored
// radar frame selector. Neutral selectors and unresolved owners have
// no player color and therefore publish no owner art [03 §3.9].
func radarOwnerPalette(s *Session, owner uint8, ownerKnown bool) (uint8, bool) {
	if s == nil || !ownerKnown || owner >= 10 {
		return 0, false
	}
	// The colour is the player record's logo byte, which battle entry writes
	// and the `Player%i` account persists; the setup row this used to read is
	// empty after a load [08 R-SKIR-01 §2]. See player_record.go.
	return s.colourForOwner(int(owner))
}

// featureOwnerSelector reads the plot placer nibble. Selector 10 identifies a
// map-authored feature; corpse/runtime placement stamps the owner slot
// [03 §3.3][03 §5.1.5].
func featureOwnerSelector(inst *features.Instance) (uint8, bool) {
	if inst == nil || inst.Terrain == nil {
		return combat.NeutralSide, false
	}
	if cell := inst.Terrain.PlotAt(int32(inst.CX), int32(inst.CZ)); cell != nil {
		selector := cell.PlacerNibble()
		return selector, true
	}
	return combat.NeutralSide, false
}

func projectileOwnerFromRecord(s *Session, shooter pool.Handle, side uint8) (uint8, bool) {
	if s != nil && s.Units != nil && shooter != 0 {
		if u := s.Units.Unit(shooter); u != nil {
			return u.Owner, true
		}
	}
	// Shooterless records carry NeutralSide. A zero side on an unresolved
	// fixture must not accidentally bypass local-player-zero LOS [06 §6.1][06 §6.5].
	if side != 0 {
		return side, true
	}
	return combat.NeutralSide, false
}

// radarPointVisible is the minimap contacts pass's second-pass admission test
// for a projectile candidate: the mode-selected local player visibility
// source sampled at the candidate's own position, with owner-local identity
// as the only bypass [03 §3.9]. The local player is the publication's
// viewer, which a playback perspective may override (replay_view.go).
func radarPointVisible(s *Session, owner uint8, ownerKnown bool, x, y, z numeric.Fixed) bool {
	if s == nil {
		return false
	}
	viewer, _ := s.publicationPerspective()
	if ownerKnown && owner < 10 && owner == viewer {
		return true
	}
	return s.Vis != nil && s.Vis.VisiblePoint(visibility.PlayerID(viewer), x, y, z)
}

// publishVisibilityView copies the viewing player's visibility masks into the
// immutable presentation frame. Radar has no authoritative mask source in the
// visibility service.
func publishVisibilityView(vis *visibility.Service, local uint8, dst *frame.Frame) {
	if vis == nil || dst == nil || local >= 10 {
		return
	}
	version := vis.MappingVersion()
	source := vis.PresentationIdentity()
	if dst.RestoreVisibility(source, version) {
		return
	}
	out := &dst.Visibility
	w, h := vis.GridDimensions()
	word := vis.WordMask()
	if w <= 0 || h <= 0 || len(word) != int(w*h) {
		return
	}
	// CoverageBytes records the actual mode-selected source. When current
	// coverage is disabled, the byte grids are initialization fill only and
	// must not be published as if they admitted foreign objects [03 §3.1–§3.2].
	byteCoverage := vis.Mode()&visibility.ModeCurrentEnabled != 0
	out.Visible = out.Visible[:0]
	if byteCoverage {
		current := vis.ByteGrid(visibility.PlayerID(local))
		if len(current) != int(w*h) {
			return
		}
		out.Visible = copyBytesInto(out.Visible, current)
	}
	out.WordVisible = copyWordsInto(out.WordVisible, word)
	out.W, out.H = w, h
	out.CoverageBytes, out.Valid = byteCoverage, true
	out.MappingVersion = version
	out.MappingSource = source
}

// publishedMoverMode is the selector the composer's two unit passes split on:
// the low two bits of the unit record's own flags word — the committed
// mover-mode *mirror* of [04 R-MOV-01 §8], not the mover's pending request.
// Pass A takes the units whose mirror is `1`. Pass B runs after the nanolathe
// strip, projectile pool and fixed effect pool, and takes the rest
// [03 R-RAST-01 §7].
//
// Every unit is created with the mirror at `1` (units.CreatedMoverMode), a
// building included, and only the air setter moves it off `1`. So pass A is
// ground and sea movers, landed aircraft and every structure — nanoframe or
// finished — and pass B is airborne aircraft (`2`) plus the attached/parked
// mode (`0`). A building does not change pass when it completes.
//
// This function previously narrowed the published word to `0` for every
// building-class definition, on the reading that a structure owns no mover and
// so must read `0`. That was wrong twice over: the composer never dereferences
// a mover, and a unit with no mover still has a mirror, which stays at the `1`
// its spawn wrote. The narrowing put every building and every nanoframe in
// pass B, where it painted over the construction spray aimed at it — playtest
// defect PT3-01. See the 2026-08-30 correction under [03 R-RAST-01 §7], which
// retracts that section's "structures (no mover, mode `0`) … This is the
// retail order; it is not a bug to fix".
func publishedMoverMode(u *units.Unit) uint8 {
	if u == nil {
		return 0
	}
	return u.Move.ModeMirror & 3
}

// publishedCarriedPiece narrows the carrier hang piece to the committed copy.
// Retail stores it as one byte and reads it back signed, so the reserved
// no-piece index is a negative value on the read side [04 R-FAC-02 §1]; the
// composer only needs "is there a real piece", which is the sign
// [03 R-RAST-01 §7]. Anything outside the signed 16-bit range is published as
// the no-piece sentinel rather than wrapping into a valid-looking index.
func publishedCarriedPiece(piece int) int16 {
	if piece < 0 || piece > math.MaxInt16 {
		return -1
	}
	return int16(piece)
}

// publishHullGateInputs retains the exact definition bounds independently of
// the draw position. Session, combat and committed presentation form the same
// minimum-X/maximum-Y/minimum-Z probe and full spans [06 §3.1][03 §3.2][I6].
func publishHullGateInputs(vp *frame.UnitView, u *units.Unit) {
	if vp == nil || u == nil {
		return
	}
	min, max := u.Def.BoundingExtents()
	hull := visibility.TargetFromBounds(visibility.Target{}, min, max)
	vp.HullOffsetX, vp.HullOffsetY, vp.HullOffsetZ = hull.X, hull.Y, hull.Z
	vp.HullXExtent, vp.HullYExtent, vp.HullZExtent = hull.XExtent, hull.YExtent, hull.ZExtent
	vp.UnderwaterExempt = u.Flags&visibility.SonarBit != 0
}

// publishedSeaLevel is the map header's sea-level byte in 16.16 world units,
// the value the visibility gate's step 3 compares the first probe height against
// [03 §3.2][03 §2.2]. A session with no terrain publishes zero, which is what
// the gate's own fixture path uses.
func publishedSeaLevel(ter *world.Terrain) numeric.Fixed {
	if ter == nil {
		return 0
	}
	return ter.SeaLevelWorld()
}

// publishProjectileFloorHeight copies the record's cached average floor height
// into the committed view. The collision gate writes the scratch on every
// in-map tick — after the in-map test, before the unit-slot tests — as
// `(cell.maxHeight + cell.minHeight) / 2` over the plot cell of the post-motion
// point [06 §8.1] step 2, [R-DMG-01 §14]; an off-map record retires without
// sampling and is compacted away before publication. The projectile draw pass
// is its only reader, anchoring the shared ground `shadow` sprite at half this
// height instead of the projectile's own Y [03 §5.4]. A record the gate has not
// visited yet — a burst clone appended after the phase captured its count
// [06 §5.1] — publishes whatever its slot last held, which is what retail's
// draw pass reads for it too.
func publishProjectileFloorHeight(pv *frame.ProjectileView, p *combat.Projectile) {
	if pv == nil || p == nil {
		return
	}
	pv.FloorHeight = p.CachedFloorHeight
	pv.FloorHeightValid = true
}
