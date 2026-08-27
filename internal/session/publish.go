package session

import (
	"fmt"

	"github.com/nanolathe/nanolathe/internal/combat"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/hud"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/visibility"
	"github.com/nanolathe/nanolathe/internal/world"
)

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
			fv := frame.FeatureView{
				CX:        int32(inst.CX),
				CZ:        int32(inst.CZ),
				X:         inst.X,
				Y:         inst.Y,
				Z:         inst.Z,
				DefName:   inst.Def.CanonicalKey,
				Model:     inst.Def.Object,
				Health:    inst.Health,
				MaxHealth: inst.MaxHealth,
				IsBurning: inst.IsBurning,
				IsSinking: inst.IsSinking,
				BurnTicks: inst.BurnTicks,
				FootX:     int8(inst.FootprintX),
				FootZ:     int8(inst.FootprintZ),

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
			pv := frame.ProjectileView{
				Handle:         h,
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
	current := vis.ByteGrid(visibility.PlayerID(local))
	if w <= 0 || h <= 0 || len(word) != int(w*h) || len(current) != int(w*h) {
		return
	}
	out.Visible = copyBytesInto(out.Visible, current)
	out.WordVisible = copyWordsInto(out.WordVisible, word)
	out.W, out.H = w, h
	out.CoverageBytes, out.Valid = true, true
}
