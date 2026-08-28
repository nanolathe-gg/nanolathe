package session

import (
	"fmt"

	"github.com/nanolathe/nanolathe/internal/combat"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/features"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/hud"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/visibility"
	"github.com/nanolathe/nanolathe/internal/world"
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
func (s *Session) PreviewPlacement(cx, cz int32, def *content.UnitDef, footX, footZ int32, self pool.Handle) (world.PlacementResult, error) {
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
	if !def.BMCode {
		yard, err = world.ParseYardMap(def.YardMap, int(footX), int(footZ))
		if err != nil {
			return world.PlacementResult{}, err
		}
	}
	if s.Build != nil {
		if _, blocked := s.Build.StructureBlocks(self, rect); blocked {
			return world.PlacementResult{Rect: rect, SiteHeight: s.World.SiteHeight(cx, cz, yard, int(footX), int(footZ), def.Waterline)}, fmt.Errorf("session: footprint overlaps a completed structure")
		}
	}
	result, err := s.World.CheckPlacement(world.PlacementQuery{Rect: rect, Yard: yard, Rules: rules, Self: uint16(self), Mobile: def.BMCode})
	if err != nil {
		result = world.PlacementResult{Rect: rect, SiteHeight: s.World.SiteHeight(cx, cz, yard, int(footX), int(footZ), def.Waterline)}
	}
	return result, err
}

// publishSnapshot publishes one immutable frame after every completed sub-tick [PLAN_03 C15].
func (s *Session) publishSnapshot(tick uint32) {
	if s.Snapshot == nil {
		return
	}
	published := s.Snapshot.BeginWrite()
	if published == nil {
		return
	}
	published.Tick = tick
	published.Paused = s.Clock != nil && s.Clock.Paused
	if s.publication != nil && s.publication.effects != nil {
		published.Effects = s.publication.effects.SnapshotInto(published.Effects)
	} else {
		published.Effects = published.Effects[:0]
	}
	if s.Units != nil {
		views := published.Units[:0]
		orderQueues := published.OrderQueues[:0]
		for _, u := range s.Units.Iter() {
			if u == nil || !u.Alive {
				continue
			}
			v := frame.UnitView{
				Slot:           u.Handle,
				Owner:          u.Owner,
				X:              u.X,
				Y:              u.Y,
				Z:              u.Z,
				Health:         u.Health,
				MaxHealth:      u.MaxHealth,
				BuildRemaining: u.Remaining,
				Flags:          u.Flags,
				Heading:        u.Move.Heading,
				Pitch:          u.Move.Pitch,
				Bank:           u.Move.Bank,
				Activated:      u.Activated,
			}
			if s.Movement != nil {
				if st := s.Movement.Steers[u.Handle]; st != nil {
					v.Heading = st.Heading
				} else if fl := s.Movement.Flights[u.Handle]; fl != nil {
					v.Heading = fl.Heading
				} else if coll := s.Movement.Collisions[u.Handle]; coll != nil {
					v.Heading = coll.Heading
				}
			}
			if u.Def != nil {
				v.DefName = u.Def.CanonicalKey
				v.Model = u.Def.ObjectName
				v.FootX = int8(u.Def.FootprintX)
				v.FootZ = int8(u.Def.FootprintZ)
				if id := s.Units.DefIDForHandle(u.Handle); id != 0 {
					v.DefID = id
				}
				// Fixed vs mobile image-cache selector: retail uses runtime flag
				// (the unit's build-state bit and construction fraction) [rr-10],
				// but definition-level MaxVelocity==0 reliably identifies buildings
				// (all 21 factories and labs have 0, mobile have >0) and matches
				// the 2x building supersample expectation without needing runtime flags.
				if u.Def.MaxVelocity == 0 {
					v.IsBuilding = true
				}
			}
			views = appendUnitView(views, v)
			vp := &views[len(views)-1]
			if vm := u.GetScript(); vm != nil {
				if vmPieces := vm.Pieces; len(vmPieces) > 0 {
					flags := vm.SnapshotFlags()
					prog := vm.Program()
					var names []string
					if prog != nil {
						names = prog.Pieces
					}
					vp.Pieces = vp.Pieces[:0]
					for i, ps := range vmPieces {
						pv := frame.PieceView{
							Index: i,
							RotX:  ps.RotX,
							RotY:  ps.RotY,
							RotZ:  ps.RotZ,
							Tx:    ps.Trans[0],
							Ty:    ps.Trans[1],
							Tz:    ps.Trans[2],
						}
						if i < len(names) {
							pv.Name = names[i]
						}
						if i < len(flags) {
							f := flags[i]
							pv.Hidden = (f & 0x01) == 0     // show bit [04 §4.3]
							pv.DontShade = (f & 0x04) == 0  // shade bit [04 §4.3] 0x1000d/e000
							pv.DontShadow = (f & 0x08) == 0 // dont-shadow [04 §4.3] 0x1000a000
							// DontCache (0x02) not needed in snapshot; renderer decides via IsBuilding.
						}
						vp.Pieces = append(vp.Pieces, pv)
					}
				}
			}
			if q := orders.QueueForUnit(u); q != nil && (q.LenPrimary() > 0 || q.LenSecondary() > 0) {
				activeHead := q.Head()
				queue := orders.SnapshotQueueOf(q, u.Handle, func(n *orders.Node) []orders.SnapshotRoutePoint {
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
					points := make([]orders.SnapshotRoutePoint, int(r.Count))
					for i := range points {
						p := r.Points[i]
						points[i] = orders.SnapshotRoutePoint{X: world.CellToWorld(p.X), Z: world.CellToWorld(p.Z)}
					}
					return points
				})
				orderQueues = appendOrderQueueView(orderQueues, queue, s.Catalog)
			}
		}
		published.Units = views
		published.OrderQueues = orderQueues
		// Selection is authoritative unit state (bit 0x10), not a renderer-side
		// cache [07 §9]. Preserve pool order so a frame is deterministic [I1].
		for _, u := range views {
			if u.Owner != s.LocalOwner || u.Flags&0x10 == 0 {
				continue
			}
			published.Selection.Handles = append(published.Selection.Handles, u.Slot)
			if published.Selection.Primary == 0 {
				published.Selection.Primary = u.Slot
			}
		}
		published.Selection.LocalPlayer = s.LocalOwner
		published.Selection.Count = uint16(len(published.Selection.Handles))
		// Command-page state is authored by the selected builder's CANBUILD
		// page. Shift/input latches are presentation-owned and therefore remain
		// at their zero value until a typed input state is introduced [07 §9].
		if published.Selection.Count == 1 && published.Selection.Primary != 0 && s.Catalog != nil {
			// A command page is a single-selected-builder surface. Do not
			// promote one builder from a mixed or multi-builder selection to
			// the page owner; aggregate command state is distinct [07 §9].
			if u := s.Units.Unit(published.Selection.Primary); u != nil && u.Alive && u.Owner == s.LocalOwner && u.Flags&0x10 != 0 && u.Def != nil && u.Def.Builder {
				if page := s.Catalog.BuildMenus[content.CanonicalKey(u.Def.CanonicalKey)]; page != nil {
					published.CommandPage.Builder = u.Handle
					const buttonsPerPage = 6 // authored build rail page [07 §9]
					published.CommandPage.PageCount = uint16((len(page.Buttons) + buttonsPerPage - 1) / buttonsPerPage)
					if published.CommandPage.PageCount == 0 {
						published.CommandPage.PageCount = 1
					}
					pageNumber := 0
					if hud.IsPaged(u.Flags) {
						pageNumber = hud.DecodePage(u.Flags)
					}
					pageNumber = hud.ClampPage(pageNumber, int(published.CommandPage.PageCount))
					published.CommandPage.Page = uint16(pageNumber)
					start := pageNumber * buttonsPerPage
					end := start + buttonsPerPage
					if start < len(page.Buttons) {
						if end > len(page.Buttons) {
							end = len(page.Buttons)
						}
						published.CommandPage.ProductKeys = append(published.CommandPage.ProductKeys[:0], page.Buttons[start:end]...)
					}
				}
			}
		}
	}
	// Visibility masks are copied for the validated local player; their
	// mode-dependent/raw representation remains owned by visibility [03 §3.1–§3.2].
	// Radar is a separate presentation surface, not a mask
	// published by visibility.Service [03 §3.4], so it remains unset. The
	// service currently has no generation counter; Version consequently stays
	// zero rather than inventing one [I9].
	if s.Vis != nil {
		publishVisibilityView(s.Vis, s.LocalOwner, &published.Visibility)
		s.Vis.RebuildFog(0, 0)
		if fc := s.Vis.Fog(); fc != nil {
			w, h := fc.Dimensions()
			ch0, ch1 := fc.Channels()
			published.Fog.W = w
			published.Fog.H = h
			published.Fog.OriginX, published.Fog.OriginZ = fc.Origin()
			published.Fog.Ch0 = copyBytesInto(published.Fog.Ch0, ch0)
			published.Fog.Ch1 = copyBytesInto(published.Fog.Ch1, ch1)
			published.Fog.Valid = s.Vis.FogCacheValid()
		}
	}
	published.Features = published.Features[:0]
	if s.Features != nil {
		insts := s.Features.Instances()
		for _, inst := range insts {
			if inst == nil || inst.Def == nil {
				continue
			}
			featureOwner, featureOwnerKnown := featureOwnerSelector(inst)
			fv := frame.FeatureView{
				Owner:      featureOwner,
				OwnerKnown: featureOwnerKnown,
				CX:         int32(inst.CX),
				CZ:         int32(inst.CZ),
				X:          inst.X,
				Y:          inst.Y,
				Z:          inst.Z,
				DefName:    inst.Def.CanonicalKey,
				Model:      inst.Def.Object,
				Health:     inst.Health,
				MaxHealth:  inst.MaxHealth,
				Status:     uint32(inst.Status),
				IsBurning:  inst.IsBurning,
				IsSinking:  inst.IsSinking,
				BurnTicks:  inst.BurnTicks,
				FootX:      int8(inst.FootprintX),
				FootZ:      int8(inst.FootprintZ),

				Filename:    inst.Def.Filename,
				SeqName:     inst.Def.SeqName,
				SeqNameShad: inst.Def.SeqNameShad,
				Animating:   inst.Def.Animating != 0,
				AnimTrans:   inst.Def.AnimTrans != 0,
				ShadTrans:   inst.Def.ShadTrans != 0,
				Blocking:    inst.Def.Blocking,
				Reclaimable: inst.Def.Reclaimable,
				Height:      inst.Def.Height,
				Geothermal:  inst.Def.Geothermal,
			}
			if fv.Model == "" {
				fv.Model = inst.Def.Filename
			}
			published.Features = append(published.Features, fv)
		}
	}
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
			p := s.Combat.Records[i]
			owner, ownerKnown := projectileOwnerFromRecord(s, p.Shooter, p.ShooterSide)
			pv := frame.ProjectileView{
				Handle:         h,
				Owner:          owner,
				OwnerKnown:     ownerKnown,
				X:              p.Pos.X,
				Y:              p.Pos.Y,
				Z:              p.Pos.Z,
				WeaponID:       p.WeaponID,
				Shooter:        p.Shooter,
				Yaw:            uint16(p.Yaw),
				Pitch:          uint16(p.Pitch),
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
				Selector:       -1, // explicit suppression sentinel when no selector art is established [03 §5.4]
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
					// [03 §5.4] leaves the lifetime-scaled GAF input as a caller
					// parameter (commonly WeaponTimer or Duration); no projectile
					// record field identifies which authored value is selected. Keep
					// Lifetime explicitly unknown rather than guessing [I9].
				}
			}
			published.Projectiles = append(published.Projectiles, pv)
		}
	}
	// Radar contacts and callback circles are copied only after all
	// authoritative pools have been traversed. Presentation therefore receives
	// one coherent tick-end view and never needs to bind callbacks or inspect
	// mutable session services [03 §3.4][03 §3.9].
	published.Radar.Contacts = published.Radar.Contacts[:0]
	published.Radar.Circles = published.Radar.Circles[:0]
	// The minimap mode is session state supplied by the authoritative composer
	// input seam. Preserve its exact value; presentation must not manufacture a
	// viewport marker mode at the frame boundary [03 §3.12][I6].
	published.Radar.MarkerMode = s.RadarMarkerMode
	var sensorInputs []visibility.SensorInput
	var sensorCircles []visibility.SensorCircle
	if s.Vis != nil {
		sensorInputs = s.Vis.SensorInputs()
		sensorCircles = s.Vis.SensorCircles()
	}
	if s.Units != nil {
		sensorIndex := 0
		for _, u := range s.Units.Iter() {
			if u == nil || !u.Alive {
				continue
			}
			status := uint32(0)
			active := u.Activated
			onOffable := false
			if s.visStatus != nil {
				status = s.visStatus[int(u.Handle)]
			}
			hidden := u.IsCloaked
			stealth := false
			if u.Def != nil {
				stealth = u.Def.Stealth
				hidden = hidden || u.Def.Stealth || u.Def.InitCloaked
				onOffable = u.Def.OnOffable
			}
			if si := radarSensorInput(sensorInputs, uint16(u.Handle), sensorIndex); si != nil {
				// ID-bearing inputs are the authoritative Step seam. A zero-ID
				// positional fallback is retained for older producers, but its zero
				// fields are placeholders and must not erase state derived from the
				// live unit/catalog record.
				if si.ID != 0 {
					status = si.Status
					hidden = si.Hidden
					stealth = si.Stealth
					active = si.Active
					onOffable = si.OnOffable
				}
			}
			sensorIndex++
			selected := u.Owner == s.LocalOwner && u.Flags&0x10 != 0
			// The contact status word carries the selected/range-status bit used by
			// the later circle branch. Keep it distinct from visibility bits, which
			// are supplied by the sensor pass [03 §3.9].
			status &^= 0x10
			if selected {
				status |= 0x10
			}
			contact := frame.RadarContactView{
				Kind: frame.RadarContactUnit, Handle: u.Handle, Owner: u.Owner,
				X: u.X, Y: u.Y, Z: u.Z, Status: status,
				Hidden: hidden, Stealth: stealth, Active: active,
				OnOffable: onOffable,
				Selected:  selected,
				Seen:      status&visibility.SeenBit != 0,
				Friendly:  status&visibility.FriendlyMask != 0,
				Visible:   u.Owner == s.LocalOwner || status&visibility.SeenBit != 0,
			}
			if u.Def != nil {
				contact.Commander = u.Def.Commander
				contact.Graphic = u.Def.ObjectName
				// Contact-side range circles are the selected/range-status
				// branch. It runs before the activation/onoffable gate; inactive
				// on/off units therefore publish no range distances, while units
				// without that capability remain eligible [03 §3.9].
				contact.RangeStatus = status&0x10 != 0 && (active || !onOffable)
				if contact.RangeStatus {
					contact.RadarDistance = u.Def.RadarDistance
					contact.SonarDistance = u.Def.SonarDistance
					contact.RadarJam = u.Def.RadarDistanceJam
					contact.SonarJam = u.Def.SonarDistanceJam
				}
				// No compiled unit field or instance byte currently exposes the
				// authored no-radar/blink-suppress inputs. Keep their neutral
				// values until that source is traced; the frame still carries the
				// established status/hidden/friendly gates [03 §3.9].
				for slot := 0; slot < units.NumSlots; slot++ {
					ws := u.SlotAt(slot)
					if ws == nil || ws.Weapon == nil {
						continue
					}
					contact.Rings = append(contact.Rings, frame.RadarRingView{
						// The compiled unit flag sequence places CanGuard at bit
						// 29, the ring-loop enable [02 "Unit record"][03 §3.9].
						Enabled:   u.Def.CanGuard,
						Dashed:    ws.Weapon.Interceptor,
						Range:     ws.Weapon.Range,
						Intercept: ws.Weapon.Interceptor,
					})
				}
			}
			published.Radar.Contacts = append(published.Radar.Contacts, contact)
		}
	}
	// Cleanup follows the visibility pass in the committed-tick order. Keep a
	// callback only when its source unit survived that boundary; source identity
	// makes this deterministic and prevents stale circles from freed slots.
	for _, c := range sensorCircles {
		if c.SourceID == 0 || !radarHasLiveUnit(published.Radar.Contacts, c.SourceID) {
			continue
		}
		published.Radar.Circles = append(published.Radar.Circles, frame.RadarCircleView{SourceID: c.SourceID, U: c.U, V: c.V, Radius: c.Radius, Kind: c.Kind})
	}
	for _, p := range published.Projectiles {
		owner := p.Owner
		published.Radar.Contacts = append(published.Radar.Contacts, frame.RadarContactView{
			Kind: frame.RadarContactProjectile, Handle: p.Handle, Owner: owner, OwnerKnown: p.OwnerKnown, X: p.X, Y: p.Y, Z: p.Z,
			Graphic: p.Graphic, AssetID: p.AssetID, Status: p.Flags,
			Visible: radarPointVisible(s, owner, p.OwnerKnown, p.X, p.Y, p.Z),
		})
	}
	for _, f := range published.Features {
		published.Radar.Contacts = append(published.Radar.Contacts, frame.RadarContactView{
			Kind: frame.RadarContactFeature, Owner: f.Owner, OwnerKnown: f.OwnerKnown, X: f.X, Y: f.Y, Z: f.Z,
			Graphic: f.Model, AssetID: f.Filename, Status: f.Status,
			Visible: radarFeatureVisible(s, f),
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
			if builder.Def != nil {
				b.Factory = !builder.Def.CanMove && !builder.Def.CanFly
			}
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
			published.Economy = append(published.Economy, frame.EconomyView{
				Player:         uint8(p),
				Metal:          pl.Stock[economy.Metal],
				Energy:         pl.Stock[economy.Energy],
				MetalCapacity:  pl.Capacity[economy.Metal],
				EnergyCapacity: pl.Capacity[economy.Energy],
				MetalProduced:  pl.PassProduced[economy.Metal],
				MetalConsumed:  pl.PassConsumed[economy.Metal],
				EnergyProduced: pl.PassProduced[economy.Energy],
				EnergyConsumed: pl.PassConsumed[economy.Energy],
				Active:         pl.Exists && !pl.IsObserver,
			})
		}
	} else {
		published.Economy = published.Economy[:0]
	}
	// Shake offset produced at phase 10 [03 §5.6][01 §4.4] DET-04.
	published.ShakeOffsetX = s.shakeOffsetX
	published.ShakeOffsetY = s.shakeOffsetY
	published.ShakeActive = s.shakeActive
	published.ShakeDuration = s.shakeDuration
	published.ShakeRemaining = s.shakeRemaining
	published.ShakeAmpX = s.shakeAmpX
	published.ShakeAmpY = s.shakeAmpY

	// RS-05: publish the authoritative result once through the committed frame
	// [08 "Evaluation"][P1-01]. A pending result keeps its metadata while the
	// latch countdown is exposed for presentation; no second result side-channel
	// exists.
	{
		r := s.result
		published.Result = frame.ResultView{
			Ended:      r.Ended,
			Kind:       r.Kind,
			WinnerTeam: r.WinnerTeam,
			Reason:     r.Reason,
			Tick:       r.Tick,
			ArmedTick:  r.ArmedTick,
			Countdown:  r.Countdown,
			Draw:       r.Draw,
		}
		published.Result.Winners = copyIntsInto(published.Result.Winners, r.Winners)
		published.Result.Losers = copyIntsInto(published.Result.Losers, r.Losers)
		published.Result.Scores = copyScoresInto(published.Result.Scores, r.Scores)
		if !r.Ended {
			published.Result.Countdown = s.Latch.Countdown
		}
	}
	if s.publication != nil && s.publication.events != nil {
		published.Events = s.publication.events.SnapshotEventsInto(published.Events)
	} else {
		published.Events = published.Events[:0]
	}
	if err := s.Snapshot.Publish(tick); err != nil {
		panic(fmt.Sprintf("session: committed frame publication failed at tick %d: %v", tick, err))
	}
	if s.publication != nil && s.publication.events != nil {
		s.publication.events.Reset()
	}
}

func radarHasLiveUnit(contacts []frame.RadarContactView, id uint16) bool {
	for _, c := range contacts {
		if c.Kind == frame.RadarContactUnit && uint16(c.Handle) == id {
			return true
		}
	}
	return false
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
		if selector == 0 {
			// Map loading stamps selector 10, while the current runtime feature
			// placement seam leaves zero without an owning-player source. Keep
			// that ambiguity unknown so local player zero cannot receive an
			// accidental owner bypass [03 §3.3][03 §3.9].
			return combat.NeutralSide, false
		}
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

func radarPointVisible(s *Session, owner uint8, ownerKnown bool, x, y, z numeric.Fixed) bool {
	if s == nil {
		return false
	}
	if ownerKnown && owner < 10 && owner == s.LocalOwner {
		return true
	}
	return s.Vis != nil && s.Vis.VisiblePoint(visibility.PlayerID(s.LocalOwner), x, y, z)
}

func radarFeatureVisible(s *Session, f frame.FeatureView) bool {
	if s == nil {
		return false
	}
	if f.OwnerKnown && f.Owner < 10 && f.Owner == s.LocalOwner {
		return true
	}
	if s.Vis == nil {
		return false
	}
	owner := visibility.PlayerID(f.Owner)
	// VisibleExtents takes a player owner for its bypass check. Selector 10 is
	// explicitly non-player, so substitute a valid non-local identity solely to
	// run the two-corner LOS samples.
	if !f.OwnerKnown || f.Owner >= 10 {
		owner = visibility.PlayerID((s.LocalOwner + 1) % 10)
	}
	return s.Vis.VisibleExtents(visibility.PlayerID(s.LocalOwner), visibility.Box{
		Owner: owner,
		MinX:  world.CellToWorld(f.CX),
		MinZ:  world.CellToWorld(f.CZ),
		MaxX:  world.CellToWorld(f.CX + int32(f.FootX)),
		MaxZ:  world.CellToWorld(f.CZ + int32(f.FootZ)),
		Y:     f.Y,
	})
}

func radarSensorInput(inputs []visibility.SensorInput, id uint16, index int) *visibility.SensorInput {
	for i := range inputs {
		if id != 0 && inputs[i].ID == id {
			return &inputs[i]
		}
	}
	// Older producers do not provide the optional ID. SensorTick preserves
	// indexed live-unit order, so the ordinal is an immutable fallback.
	if index >= 0 && index < len(inputs) && inputs[index].ID == 0 {
		return &inputs[index]
	}
	return nil
}

// publishVisibilityView copies the local player's visibility masks into the
// immutable presentation frame. Radar has no authoritative mask source in the
// visibility service.
func snapshotOrderQueueView(src orders.SnapshotQueue, cat *content.Catalog) frame.OrderQueueView {
	return frame.OrderQueueView{
		Unit:               src.Unit,
		Primary:            snapshotOrderViews(src.Primary, cat),
		Secondary:          snapshotOrderViews(src.Secondary, cat),
		PrimaryTruncated:   src.PrimaryTruncated,
		SecondaryTruncated: src.SecondaryTruncated,
	}
}

func snapshotOrderViews(src []orders.SnapshotNode, cat *content.Catalog) []frame.OrderView {
	if len(src) == 0 {
		return nil
	}
	dst := make([]frame.OrderView, len(src))
	for i, n := range src {
		var footX, footZ int8
		if cat != nil && n.BuildProduct != "" {
			if def, ok := cat.Unit(n.BuildProduct); ok && def != nil {
				footX, footZ = int8(def.FootprintX), int8(def.FootprintZ)
			}
		}
		dst[i] = frame.OrderView{
			Unit: n.Owner, Target: n.Target,
			GoalX: n.GoalX, GoalY: n.GoalY, GoalZ: n.GoalZ,
			Kind: n.Kind, StateLabel: n.State, MoveState: n.MoveState,
			List: n.List, Index: n.Index, DescriptorID: n.DescriptorID,
			Phase: n.Phase, CreationTick: n.CreationTick, Flags: n.Flags,
			DynamicGate: n.DynamicGate, Deadline: n.Deadline,
			Satisfied: n.Satisfied, PathStatus: n.PathStatus,
			Param1: n.Param1, Param2: n.Param2, Param3: n.Param3,
			BuildProduct: n.BuildProduct, BuildCount: n.BuildCount,
			FootX: footX, FootZ: footZ,
			RouteTruncated: n.RouteTruncated,
		}
		if len(n.Route) > 0 {
			dst[i].Route = make([]frame.RoutePoint, len(n.Route))
			for j, p := range n.Route {
				dst[i].Route[j] = frame.RoutePoint{X: p.X, Y: p.Y, Z: p.Z, Flags: p.Flags}
			}
		}
	}
	return dst
}

func publishVisibilityView(vis *visibility.Service, local uint8, out *frame.VisibilityView) {
	if vis == nil || out == nil || local >= 10 {
		return
	}
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
}
