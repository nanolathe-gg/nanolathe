package session

import (
	"fmt"
	"math"

	"github.com/nanolathe/nanolathe/internal/combat"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/features"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/hud"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/render"
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
// It passes retail's NULL player, under which the blocker's two occupancy
// rejections — the structure-yard mark of yard bit 0, and the ground occupant
// of bits 1–2 — apply unconditionally [04 R-P0-08-B §1]. That is the right
// answer for every caller except the human build cursor, which passes the
// local player's record and runs the known-site gate; PreviewPlacementForCursor
// below is that form.
func (s *Session) PreviewPlacement(cx, cz int32, def *content.UnitDef, footX, footZ int32, self pool.Handle) (world.PlacementResult, error) {
	return s.previewPlacement(cx, cz, def, footX, footZ, self, nil)
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
func (s *Session) PreviewPlacementForCursor(cx, cz int32, def *content.UnitDef, footX, footZ int32, self pool.Handle) (world.PlacementResult, error) {
	if s == nil || s.Vis == nil {
		return world.PlacementResult{}, fmt.Errorf("session: placement visibility unavailable")
	}
	local := uint8(localPlayerForSession(s))
	return s.previewPlacement(cx, cz, def, footX, footZ, self, &sessionPlacementViewer{vis: s.Vis, local: local, player: local})
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

func (s *Session) previewPlacement(cx, cz int32, def *content.UnitDef, footX, footZ int32, self pool.Handle, viewer world.PlacementViewer) (world.PlacementResult, error) {
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
		yard, err = world.ParseYardMap(def.YardMap, int(footX), int(footZ))
		if err != nil {
			return world.PlacementResult{}, err
		}
	}
	result, err := s.World.CheckPlacement(world.PlacementQuery{Rect: rect, Yard: yard, Rules: rules, Self: uint16(self), Mobile: def.BMCode != 0, Viewer: viewer})
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
	// Every live strip sub-record, in the composer's walk order [03 §1]. This
	// is the one writer of the committed strip channel, and it runs once per
	// tick inside the publication boundary [I6].
	published.Strips = s.appendStripViews(published.Strips[:0])
	if s.Units != nil {
		views := published.Units[:0]
		orderQueues := published.OrderQueues[:0]
		for _, u := range s.Units.Iter() {
			if u == nil || !u.Alive {
				continue
			}
			v := frame.UnitView{
				InstanceID:     publication.unitIdentity(u),
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
				Kills:          u.Kills,
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
				Decloaking: s.visStatus != nil && s.visStatus[int(u.Handle)]&visibility.DecloakBit != 0,
				// The carrier link the unit painter's per-unit present needs:
				// a carried child is drawn with its carrier, not only as its
				// own bucket entry [03 R-RAST-01 §7][04 R-UNIT-06 §3].
				Carrier:      u.Attachment.Carrier,
				CarriedPiece: publishedCarriedPiece(u.Attachment.AttachPiece),
				// The health-bar pass draws '0'+Group beside the bar of a unit
				// whose group number is nonzero [03 R-FX-01 §6][07 §9].
				Group: u.Group,
			}
			// The footer's four rate fields read the archived production and
			// requested totals of the most recent settlement pass, not the
			// definition constants [07 R-HUD-03 §2][05 R-ECO-01 §5].
			if s.Econ != nil {
				archived := s.Econ.UnitArchived(u.Handle)
				v.ArchivedMetalMake = archived[economy.Metal].Production
				v.ArchivedEnergyMake = archived[economy.Energy].Production
				v.ArchivedMetalUse = archived[economy.Metal].Requested
				v.ArchivedEnergyUse = archived[economy.Energy].Requested
			}
			// The owner logo the footer blits at LOGO2 is the logos frame at the
			// owner's lobby colour index [07 R-HUD-03 §2]; it is the same
			// selector the minimap contacts already carry.
			v.OwnerColor, v.OwnerColorKnown = radarOwnerPalette(s, u.Owner, true)
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
				v.DefName = u.Def.CanonicalKey
				v.Model = u.Def.ObjectName
				v.FootX = int8(u.Def.FootprintX)
				v.FootZ = int8(u.Def.FootprintZ)
				v.BMCode = u.Def.BMCode != 0 // model-shading class gate [R-RND-02A]
				v.ZBuffer = u.Def.ZBuffer    // composition height plane [R-REN-03A §2]
				// Model shadow gate and digger clip [R-REN-03D §1][R-REN-03A §8].
				v.NoShadow = u.Def.NoShadow
				v.CanHover = u.Def.CanHover
				v.Floater = u.Def.Floater
				v.Digger = u.Def.Digger
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
			// The hull extents and the underwater-exemption bit of the
			// four-point visibility gate [03 §3.2] steps 3 and 5.
			publishHullGateInputs(vp, u, s)
			if vm := u.GetScript(); vm != nil {
				// CacheRevision is copied at the publication boundary; consuming or
				// clearing it here would make presentation cadence authoritative.
				vp.CacheRevision = vm.CacheRevision()
				vp.CacheValidityRevision = vm.CacheValidityRevision()
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
							pv.DontCache = (f & 0x02) == 0  // cache bit [04 §4.3]
							pv.DontShade = (f & 0x04) == 0  // shade bit [04 §4.3] 0x1000d/e000
							pv.DontShadow = (f & 0x08) == 0 // dont-shadow [04 §4.3] 0x1000a000
						}
						vp.Pieces = append(vp.Pieces, pv)
					}
				}
			}
			if q := orders.QueueOfUnit(u); q != nil && (q.LenPrimary() > 0 || q.LenSecondary() > 0) {
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
		publishSelectionAggregate(s, published.Selection.Handles, &published.CommandPage)
		// Command-page state belongs to the single selected unit whose
		// definition authors page windows. Shift/input latches are
		// presentation-owned and therefore remain at their zero value until a
		// typed input state is introduced [07 §9].
		if published.Selection.Count == 1 && published.Selection.Primary != 0 && s.Catalog != nil {
			// A command page is a single-selection surface. Do not promote one
			// unit from a mixed or multi-unit selection to the page owner;
			// aggregate command state is distinct [07 §9].
			if u := s.Units.Unit(published.Selection.Primary); u != nil && u.Alive && u.Owner == s.LocalOwner && u.Flags&0x10 != 0 && u.Def != nil {
				// The single selected unit owns the command page whatever it is.
				// The window the switch then opens is chosen by that unit's own
				// page-shown bit and page field, and which windows exist is the
				// definition's page-count byte — the catalog compiler's probe of
				// guis/<internal name>N.GUI [07 R-HUD-03 §6]
				// [02 R-CAT-01 §5 step 5]. A count of 0 is a valid state, not an
				// absent page: the switch opens the side's "%sGEN.GUI" and the
				// stage/grey table greys BUILD and ORDERS on its own count-0 arm.
				//
				// Neither the FBI `Builder` word nor CANBUILD membership is part
				// of that test, and gating on both here was what hid the
				// stockpile launchers' pages. Exactly eight reference-install
				// definitions author a page window without the builder word —
				// ARMSILO/CORSILO, ARMAMD/CORFMD, ARMSCAB/CORMABM and
				// ARMEMP/CORTRON, the eight that carry a `stockpile` weapon —
				// and each one's page holds a single MAKENUKE/MAKEANTI toy
				// [06 §11.1]. With the page suppressed a Retaliator could never
				// be told to build a round.
				pageCount := hud.BuilderPageCount(u.Def)
				published.CommandPage.Builder = u.Handle
				const buttonsPerPage = hud.RetailBuildButtonsPerPage // authored build rail page [07 §9]
				// Page 0 is the orders state, not a build page: the count is
				// the definition's page-count byte, compiled from the
				// authored page windows [02 R-CAT-01 §5 step 5], page N
				// carries the authored entries (N-1)*6..N*6-1, and page 0
				// carries none [07 R-HUD-03 §6]. The page number itself is
				// the unit's own state — the page-shown bit and the page
				// field of [07 §9] — copied out here, never derived from the
				// renderer.
				published.CommandPage.PageCount = uint16(pageCount)
				pageNumber := hud.ClampPage(hud.DecodePage(u.Flags), pageCount)
				published.CommandPage.Page = uint16(pageNumber)
				// The held-round byte the MAKENUKE/MAKEANTI toy prints
				// [07 R-P0-11 §2]. The order alias's build type is always zero
				// and every shipped stockpile weapon sits in slot 0, so slot 0's
				// completed-round remainder is the byte [06 §11.1]
				// [06 R-WPN-05 §2]. The pending half of the label comes from the
				// committed order queues, which already carry the secondary
				// BUILDWEAPON nodes.
				if slot := u.SlotAt(0); slot != nil {
					published.CommandPage.Stockpile = slot.Ammo
				}
				published.CommandPage.ProductKeys = published.CommandPage.ProductKeys[:0]
				// A page window with no CANBUILD list behind it publishes no
				// products; its authored toys are its own. Only a unit that
				// authors a build menu has product membership to place.
				if page := s.Catalog.BuildMenus[content.CanonicalKey(u.Def.CanonicalKey)]; page != nil {
					// Base CANBUILD membership keeps its canonical page order. The
					// download-menu tail on BuildMenuPage.Buttons is an extension of
					// authoritative membership, not a flat continuation whose slice
					// position determines a page: download records carry PAGE and
					// BUTTON explicitly [02 R-CAT-01 §8][07 §9].
					baseButtons := page.BaseButtons()
					// Hand-built catalogs predating BaseButtonCount have no
					// download records and therefore consist wholly of CANBUILD
					// membership. Compiled catalogs always set the count, including
					// the legitimate zero-base/download-only case.
					if page.BaseButtonCount == 0 && len(s.Catalog.DownloadPlacements) == 0 {
						baseButtons = page.Buttons
					}
					baseProducts := hud.ProductsForPage(baseButtons, pageNumber, buttonsPerPage)
					published.CommandPage.ProductKeys = append(published.CommandPage.ProductKeys, baseProducts...)
					for _, placement := range s.Catalog.DownloadPlacementsForPage(u.Def.CanonicalKey, pageNumber) {
						published.CommandPage.GeneratedProducts = append(published.CommandPage.GeneratedProducts, frame.GeneratedProductPlacement{
							ProductKey: placement.Product,
							Button:     placement.Button,
						})
						if containsCanonicalProduct(published.CommandPage.ProductKeys, placement.Product) {
							continue
						}
						published.CommandPage.ProductKeys = append(published.CommandPage.ProductKeys, placement.Product)
					}
				}
			}
		}
	}
	// Visibility masks are copied for the validated local player; their
	// mode-dependent/raw representation remains owned by visibility [03 §3.1–§3.2].
	// Radar is a separate presentation surface, not a mask
	// published by visibility.Service [03 §3.4], so it remains unset. The
	// Visibility publishes immutable presentation revisions; source identity
	// prevents a fresh service from restoring another service's retained bytes.
	if s.Vis != nil {
		publishVisibilityView(s.Vis, s.LocalOwner, published)
		// Step 3 of the gate compares against the scaled sea-level byte, never
		// against zero [03 §3.2][03 §2.2].
		published.Visibility.SeaLevel = publishedSeaLevel(s.World)
		s.Vis.RebuildFog(0, 0)
		if fc := s.Vis.Fog(); fc != nil {
			version := s.Vis.FogVersion()
			source := s.Vis.PresentationIdentity()
			if !published.RestoreFog(source, version) {
				w, h := fc.Dimensions()
				ch0, ch1 := fc.Channels()
				published.Fog.W = w
				published.Fog.H = h
				published.Fog.OriginX, published.Fog.OriginZ = fc.Origin()
				published.Fog.Ch0 = copyBytesInto(published.Fog.Ch0, ch0)
				published.Fog.Ch1 = copyBytesInto(published.Fog.Ch1, ch1)
				published.Fog.Version = version
				published.Fog.Source = source
			}
			published.Fog.Valid = s.Vis.FogCacheValid()
		}
	} else {
		published.Visibility = frame.VisibilityView{}
		published.Fog = frame.FogView{}
	}
	published.Features = published.Features[:0]
	if s.Features != nil {
		s.featurePublicationScratch = s.Features.AppendInstances(s.featurePublicationScratch[:0])
		for _, inst := range s.featurePublicationScratch {
			if inst == nil || inst.Def == nil {
				continue
			}
			featureOwner, featureOwnerKnown := featureOwnerSelector(inst)
			runtime := inst.RuntimeView()
			fv := frame.FeatureView{
				Owner:      featureOwner,
				OwnerKnown: featureOwnerKnown,
				CX:         int32(inst.CX),
				CZ:         int32(inst.CZ),
				X:          inst.X,
				Y:          inst.Y,
				Z:          inst.Z,
				// The live record's orientation triple, published because the
				// 3DO feature pass fills a pseudo-unit with "model pointer,
				// position and the slot's orientation words"
				// [03 R-RAST-01 §6][05 "Feature instance and terrain cell"].
				// Zero for everything but a wreck.
				Bank:      inst.Bank,
				Heading:   inst.Heading,
				Pitch:     inst.Pitch,
				DefName:   inst.Def.CanonicalKey,
				Model:     inst.Def.Object,
				Status:    uint32(inst.Status),
				IsBurning: inst.IsBurning,
				IsSinking: inst.IsSinking,
				FootX:     int8(inst.FootprintX),
				FootZ:     int8(inst.FootprintZ),

				Filename:        inst.Def.Filename,
				SeqName:         inst.Def.SeqName,
				SeqNameShad:     inst.Def.SeqNameShad,
				Animating:       inst.Def.Animating != 0,
				AnimTrans:       inst.Def.AnimTrans != 0,
				ShadTrans:       inst.Def.ShadTrans != 0,
				Blocking:        inst.Def.Blocking,
				Reclaimable:     inst.Def.Reclaimable,
				NoDrawUnderGray: inst.Def.NoDrawUnderGray,
				Height:          inst.Def.Height,
				Geothermal:      inst.Def.Geothermal,
				RuntimeLive:     runtime.Live,
				ShadowEnabled:   runtime.ShadowEnabled,
			}
			if fv.Model == "" {
				fv.Model = inst.Def.Filename
			}
			// A cell carrying a live EVENT record draws that record's own
			// cursor, not the definition's rest cursor [03 R-RAST-01 §6]
			// [05 R-FEAT-01 §10] pass 3. Publishing only the rest sequence left
			// a reclaimed tree standing on its idle frame for the whole
			// animation and then popping straight to its successor: the
			// reclaim sequence the definition names was resolved by the
			// simulation, which timed the record from it, and then never drawn.
			if name, shadow, visit, ok := inst.EventSequence(); ok {
				fv.EventSeqName = name
				fv.EventSeqNameShad = shadow
				fv.EventSeqVisit = visit
			}
			published.Features = append(published.Features, fv)
		}
		// The committed frame owns values; release the borrowed live pointers
		// while retaining only the scratch capacity for the next publication [I6].
		clear(s.featurePublicationScratch)
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
				Handle:     h,
				Owner:      owner,
				OwnerKnown: ownerKnown,
				X:          p.Pos.X,
				Y:          p.Pos.Y,
				Z:          p.Pos.Z,
				WeaponID:   p.WeaponID,
				Shooter:    p.Shooter,
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
				}
			}
			// The cached average floor height the ground shadow is anchored
			// against [06 §8.1][03 §5.4].
			publishProjectileFloorHeight(&pv, &s.Combat.Records[i])
			published.Projectiles = append(published.Projectiles, pv)
		}
	}
	// Radar contacts and callback circles are copied only after all
	// authoritative pools have been traversed. Presentation therefore receives
	// one coherent tick-end view and never needs to bind callbacks or inspect
	// mutable session services [03 §3.4][03 §3.9].
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
	var sensorInputs []visibility.SensorInput
	if s.Vis != nil {
		sensorInputs = s.Vis.SensorInputs()
	}
	if s.Units != nil {
		s.buildRadarSensorIndex(sensorInputs)
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
			// The production sensor pass is the only producer of sensor inputs
			// [visibility.Service.SensorInputs] and always stamps a nonzero ID
			// with the pool handle, so the ID-bearing lookup is the sole path;
			// the ordinal fallback for hypothetical zero-ID producers has been
			// retired (R05).
			if si := s.radarSensorInputFor(sensorInputs, uint16(u.Handle)); si != nil {
				status = si.Status
				hidden = si.Hidden
				stealth = si.Stealth
				active = si.Active
				onOffable = si.OnOffable
			}
			selected := u.Owner == s.LocalOwner && u.Flags&0x10 != 0
			ownerKnown := u.Owner < 10
			palette, paletteKnown := radarOwnerPalette(s, u.Owner, ownerKnown)
			// The contact status word carries the selected/range-status bit used by
			// the later circle branch. Keep it distinct from visibility bits, which
			// are supplied by the sensor pass [03 §3.9].
			status &^= 0x10
			if selected {
				status |= 0x10
			}
			contactIdx := len(published.Radar.Contacts)
			contact := frame.RadarContactView{
				Kind: frame.RadarContactUnit, Handle: u.Handle, Owner: u.Owner, OwnerKnown: ownerKnown,
				X: u.X, Y: u.Y, Z: u.Z, Status: status,
				Hidden: hidden, Stealth: stealth, Active: active,
				OnOffable: onOffable,
				Selected:  selected,
				// The damage-flash byte of [06 R-WPN-04 §2], the blink gate's
				// per-unit term [03 §3.9]. The sim record holds it signed (the
				// sweep decrements -16 up to zero); the frame carries retail's
				// unsigned byte, so the wrap back to 240 is the conversion, not
				// a reinterpretation — presentation tests the byte against zero
				// and never for a magnitude.
				BlinkSuppress: uint8(u.BlinkSuppress),
				Seen:          status&visibility.SeenBit != 0,
				Friendly:      status&visibility.FriendlyMask != 0,
				Visible:       u.Owner == s.LocalOwner || status&visibility.SeenBit != 0,
				Palette:       palette, PaletteKnown: paletteKnown,
			}
			if contactIdx < len(existingContacts) {
				// Reuse the slot's previous ring backing array (already
				// truncated to zero length by Reset, capacity intact) instead
				// of appending into a fresh nil slice [03 §3.9] (R05).
				contact.Rings = existingContacts[contactIdx].Rings[:0]
			}
			if u.Def != nil {
				contact.Commander = u.Def.Commander
				contact.Graphic = u.Def.ObjectName
				// The selected-unit circle gate of [03 §3.9] "Selected-unit
				// circle gate correction" (Established): the selected/range-status
				// bit must be set, and the circles are then drawn when the
				// instance is active OR the definition's on/off bit is clear. An
				// inactive on/off-capable unit therefore publishes no range
				// distances, while a unit without that capability stays eligible.
				// This is the ONLY producer of minimap circles — the sensor phase
				// rasterizes nothing [03 §3.10] correction of 2026-08-29.
				contact.RangeStatus = status&0x10 != 0 && (active || !onOffable)
				if contact.RangeStatus {
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
	for _, p := range published.Projectiles {
		owner := p.Owner
		palette, paletteKnown := radarOwnerPalette(s, owner, p.OwnerKnown)
		published.Radar.Contacts = append(published.Radar.Contacts, frame.RadarContactView{
			Kind: frame.RadarContactProjectile, Handle: p.Handle, Owner: owner, OwnerKnown: p.OwnerKnown, Palette: palette, PaletteKnown: paletteKnown, X: p.X, Y: p.Y, Z: p.Z,
			Graphic: p.Graphic, AssetID: p.AssetID, Status: p.Flags,
			Visible: radarPointVisible(s, owner, p.OwnerKnown, p.X, p.Y, p.Z),
		})
	}
	for _, f := range published.Features {
		palette, paletteKnown := radarOwnerPalette(s, f.Owner, f.OwnerKnown)
		published.Radar.Contacts = append(published.Radar.Contacts, frame.RadarContactView{
			Kind: frame.RadarContactFeature, Owner: f.Owner, OwnerKnown: f.OwnerKnown, Palette: palette, PaletteKnown: paletteKnown, X: f.X, Y: f.Y, Z: f.Z,
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
	publishPlayerRows(s, published)
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

func containsCanonicalProduct(products []string, candidate string) bool {
	want := content.CanonicalKey(candidate)
	for _, product := range products {
		if content.CanonicalKey(product) == want {
			return true
		}
	}
	return false
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
		}
		if s.Units != nil {
			row.LiveUnits = s.Units.LiveCountForPlayer(i)
		}
		// The name and the logo byte are the lobby record's, and this build
		// writes both at registration [08 R-SKIR-01 §2]. The setup-row
		// fallback that used to stand here answered nothing after a load,
		// where only the five rule words and the map name come back into
		// the setup record [08 R-SKIR-01 §2] "Save persistence".
		published.Players[i] = row
	}
}

// publishSelectionAggregate folds the local player's selection into the
// selection-aggregate command state the side panel stages and greys its
// command buttons from [07 §9][07 R-HUD-03 §6].  handles carries the selection
// in ascending pool order, the order every selection walk uses [07 §9].
//
// The fold is a copy out of authoritative state at the publication boundary and
// mutates nothing [I6].  Values and folds are documented on
// frame.CommandPageView; the two open spec conflicts are marked there.
func publishSelectionAggregate(s *Session, handles []pool.Handle, page *frame.CommandPageView) {
	// Each field starts at its not-applicable sentinel: 4 for the three-bit
	// stance fields [04 R-STANCE-01 §1], 3 for the two-bit pairs, which is
	// also the value that greys CLOAK and ONOFF [07 R-HUD-03 §6].
	page.MoveStance = 4
	page.FireStance = 4
	page.CloakState = 3
	page.OnOffState = 3
	if s == nil || s.Units == nil {
		return
	}
	for _, h := range handles {
		u := s.Units.Unit(h)
		if u == nil || !u.Alive || u.Def == nil {
			continue
		}
		// A unit joins a stance fold only when its definition authors the
		// matching accept key [04 R-STANCE-01 §5]; the unit's own two-bit
		// fields are bits 18-19 (move) and 20-21 (fire) of its status word
		// [04 R-STANCE-01 §2].
		if u.Def.MobileStandOrders {
			v := uint8((u.Flags >> 18) & 3)
			if page.MoveStance == 4 {
				page.MoveStance = v
			} else if page.MoveStance != v {
				page.MoveStance = 3
			}
		}
		if u.Def.FireStandOrders {
			v := uint8((u.Flags >> 20) & 3)
			if page.FireStance == 4 {
				page.FireStance = v
			} else if page.FireStance != v {
				page.FireStance = 3
			}
		}
		// onoffable gates the on/off pair; the folded state is the unit's
		// committed activation [04 R-SPEC-01 §11][02 "Unit record"].
		if u.Def.OnOffable {
			v := uint8(0)
			if u.Activated {
				v = 1
			}
			if page.OnOffState == 3 {
				page.OnOffState = v
			} else if page.OnOffState != v {
				page.OnOffState = 2
			}
		}
		// The can-cloak capability is derived at definition load as
		// cloakcost > 0 [05 "which units can request cloak at all"]; the folded
		// state is the unit's cloak-requested bit.  Unlike the on/off fold this
		// one takes the disagreement value for any second cloak-capable unit,
		// agreeing or not.
		//
		// This is the one cloak reader that stays on the REQUEST after the two
		// bits were split (WU-19-92): the gadget shows what the player asked
		// for and is what `Cloak_On` / `Cloak_Off` toggle, not whether the unit
		// happens to be paid up and hidden this pass [04 R-ORD-01 §2].
		if u.Def.CloakCost > 0 {
			if page.CloakState == 3 {
				v := uint8(0)
				if u.IsCloaked {
					v = 1
				}
				page.CloakState = v
			} else {
				page.CloakState = 2
			}
		}
		// The capability aggregates of the stage/grey table [07 R-HUD-03 §6],
		// each from its authored key [02 "Unit record"].  REPAIR reads the
		// parser's derived copy of canreclamate [02 R-KEYS-01 §1].
		page.CanMove = page.CanMove || u.Def.CanMove
		page.CanStop = page.CanStop || u.Def.CanStop
		page.CanAttack = page.CanAttack || u.Def.CanAttack
		page.CanDefend = page.CanDefend || u.Def.CanGuard
		page.CanPatrol = page.CanPatrol || u.Def.CanPatrol
		page.CanReclaim = page.CanReclaim || u.Def.CanReclamate
		page.CanCapture = page.CanCapture || u.Def.CanCapture
		page.CanRepair = page.CanRepair || u.Def.CanReclamate
		page.IsTransport = page.IsTransport || u.Def.CanLoad
		page.CanBlast = page.CanBlast || u.Def.CanDGun
	}
}

// radarOwnerPalette resolves the player-record color used as the authored
// radar/feature frame selector. Neutral selectors and unresolved owners have
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

// radarPointVisible is the minimap contacts pass's second-pass admission test
// for a projectile candidate: the mode-selected local player visibility
// source sampled at the candidate's own position, with owner-local identity
// as the only bypass [03 §3.9].
func radarPointVisible(s *Session, owner uint8, ownerKnown bool, x, y, z numeric.Fixed) bool {
	if s == nil {
		return false
	}
	if ownerKnown && owner < 10 && owner == s.LocalOwner {
		return true
	}
	return s.Vis != nil && s.Vis.VisiblePoint(visibility.PlayerID(s.LocalOwner), x, y, z)
}

// radarFeatureVisible is the minimap contacts pass's second-pass admission
// test for a feature candidate. Retail draws projectiles and features from
// one shared, kind-agnostic list and admits a candidate through the
// mode-selected local player visibility source sampled at its own projected
// point, with owner-local identity as the only bypass [03 §3.9] — the same
// one-point test as radarPointVisible above, not the world composer's
// two-corner footprint test of [03 §5.1.5], which is a distinct gate keyed to
// `nodrawundergray` and the plot placer nibble (see
// `featureVisibleForFrame` in internal/client/world_draw.go). The minimap
// contacts pass carries neither of those terms.
func radarFeatureVisible(s *Session, f frame.FeatureView) bool {
	if s == nil {
		return false
	}
	if f.OwnerKnown && f.Owner < 10 && f.Owner == s.LocalOwner {
		return true
	}
	return s.Vis != nil && s.Vis.VisiblePoint(visibility.PlayerID(s.LocalOwner), f.X, f.Y, f.Z)
}

// buildRadarSensorIndex (re)builds the session-retained index that resolves a
// live unit's sensor input by pool handle in O(1), replacing the whole-slice
// scan this used to do once per live unit per tick [03 §3.9] (review finding
// R05). The single production sensor pass (visibility.Service.SensorInputs,
// fed by the sweep at internal/visibility sensors.go) always stamps a
// nonzero ID with the live unit's pool handle, so a handle-keyed index is a
// complete replacement.
//
// The index itself (radarSensorIndex/radarSensorIndexGen) is never cleared;
// each call bumps the generation stamp radarSensorIndexAt and only the
// entries this call actually writes compare equal to it, so a stale handle
// from a unit that died since the index last held its slot reads back as
// "not found" without a per-tick clear pass. Use radarSensorInputFor to read
// it back; this is a plain method (not a closure-returning one) so building
// the index allocates nothing beyond the one-time slice growth.
func (s *Session) buildRadarSensorIndex(inputs []visibility.SensorInput) {
	if s == nil || s.Units == nil {
		return
	}
	// Handle 0 is the pool's null sentinel [01 §6.1]; live handles run
	// 1..Capacity(), so the index needs Capacity()+1 slots.
	need := s.Units.Capacity() + 1
	if cap(s.radarSensorIndex) < need {
		s.radarSensorIndex = make([]int32, need)
		s.radarSensorIndexGen = make([]uint32, need)
	} else {
		s.radarSensorIndex = s.radarSensorIndex[:need]
		s.radarSensorIndexGen = s.radarSensorIndexGen[:need]
	}
	s.radarSensorIndexAt++
	gen := s.radarSensorIndexAt
	idx := s.radarSensorIndex
	stamps := s.radarSensorIndexGen
	for i := range inputs {
		id := inputs[i].ID
		if int(id) < len(idx) {
			idx[id] = int32(i)
			stamps[id] = gen
		}
	}
}

// radarSensorInputFor reads back the index buildRadarSensorIndex built for
// this same publication, keyed by pool handle [03 §3.9] (R05).
func (s *Session) radarSensorInputFor(inputs []visibility.SensorInput, id uint16) *visibility.SensorInput {
	if int(id) >= len(s.radarSensorIndex) || s.radarSensorIndexGen[id] != s.radarSensorIndexAt {
		return nil
	}
	return &inputs[s.radarSensorIndex[id]]
}

// publishVisibilityView copies the local player's visibility masks into the
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
// the low two bits of the unit record's own flags word — the mover-mode
// *mirror* of [04 R-MOV-01 §8], not the mover object's copy. Pass A takes the
// units whose mirror is `1` and pass B, which runs after the nanolathe strip,
// the projectile pool and the fixed effect pool, takes the rest
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

func publishedMoverMode(u *units.Unit) uint8 {
	if u == nil {
		return 0
	}
	return u.Move.Mode & 3
}

// SetEffectTimingResolver installs the authored-timing resolver the
// presentation effect pool asks for when an admitted event names art but
// carries no per-frame durations of its own [03 §1][06 R-WFX-01 §1].
//
// The durations are a GAF entry's own per-frame holds, which live in the
// asset the shell loads, not in the simulation — so the session exposes the
// seam and the composer that owns the VFS fills it. A session with no
// resolver (every headless run) admits the same events and gives their
// animation players the pool's own default step; nothing authoritative reads
// either, because the effect pool is presentation state on the far side of
// the publication boundary [I6].
func (s *Session) SetEffectTimingResolver(resolver render.TimingResolver) {
	if s == nil {
		return
	}
	pub := s.ensurePublicationState()
	if pub == nil || pub.effects == nil {
		return
	}
	pub.effects.SetTimingResolver(resolver)
}

// publishHullGateInputs copies the three inputs the committed four-point
// visibility gate needs that nothing else on the unit view carries: the
// definition's hull extent triple and the runtime underwater-exemption bit
// [03 §3.2] steps 3 and 5.
//
// The extents are the compiled definition's own extent words, whose writers
// are traced in [07 R-REV-01 §7]: the unit-record compiler writes
// `xExtent = footprintX << 20` and `zExtent = footprintZ << 20` — a footprint
// cell is sixteen world units and `<< 20` is that sixteen expressed in 16.16 —
// and the catalog loader then rewrites the vertical word as the model's total
// height, the same zero-seeded model-top walk the compiled catalog already
// resolves once per definition at load.
//
// Presentation cannot derive the vertical word: it comes from the 3DO, not
// from the FBI record, and reading either from the far side of the frame
// boundary is what [I6] forbids.
func publishHullGateInputs(vp *frame.UnitView, u *units.Unit, s *Session) {
	if vp == nil || u == nil {
		return
	}
	if u.Def != nil {
		vp.HullXExtent = numeric.Fixed(int64(u.Def.FootprintX) << 20)
		vp.HullZExtent = numeric.Fixed(int64(u.Def.FootprintZ) << 20)
		vp.HullYExtent = numeric.Fixed(u.Def.ModelTopFixed)
	}
	if s != nil && s.visStatus != nil {
		vp.UnderwaterExempt = s.visStatus[int(u.Handle)]&visibility.SonarBit != 0
	}
}

// publishedSeaLevel is the map header's sea-level byte in 16.16 world units,
// the value the visibility gate's step 3 compares a base height against
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
