// Package combat — integrated weapon and projectile pipeline per [06] P0-I04.
package combat

import (
	"sort"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/features"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/visibility"
	"github.com/nanolathe/nanolathe/internal/world"
)

func (s *Service) emitEvent(ev Event) {
	if s != nil && s.Events != nil {
		s.Events(ev)
	}
}

// aimNameForSlot returns the Aim* function name for slotIdx [04 §5.3][06 §3.3].

func badMaskForSlot(def *content.UnitDef, slotIdx int) content.CategoryMask {
	if def == nil {
		return content.CategoryMask{}
	}
	switch slotIdx {
	case 0:
		return def.BadTargetCategoryWPRIMask
	case 1:
		return def.BadTargetCategoryWSECMask
	case 2:
		return def.BadTargetCategoryWSPEMask
	default:
		return content.CategoryMask{}
	}
}

func isAllied(owner, other uint8, econ *economy.Service) bool {
	if owner == other {
		return true
	}
	if econ == nil {
		return false
	}
	if int(owner) >= 10 || int(other) >= 10 {
		return false
	}
	if econ.Players[owner].Allies[other] {
		return true
	}
	if econ.Players[other].Allies[owner] {
		return true
	}
	return false
}

func isHostile(shooter *units.Unit, cand *units.Unit, econ *economy.Service) bool {
	if shooter == nil || cand == nil {
		return false
	}
	if shooter.Owner == cand.Owner {
		return false
	}
	if isAllied(shooter.Owner, cand.Owner, econ) {
		return false
	}
	return true
}

func isCloakedUnit(u *units.Unit) bool {
	if u == nil {
		return false
	}
	if u.IsCloaked {
		return true
	}
	if u.Def != nil && (u.Def.InitCloaked || u.Def.Stealth) {
		return true
	}
	return false
}

func isUnderwaterUnit(u *units.Unit, seaLevel numeric.Fixed) bool {
	if u == nil {
		return false
	}
	return u.Y.Raw() < seaLevel.Raw()
}

func aimNameForSlot(slotIdx int) string {
	switch slotIdx {
	case 1:
		return "AimSecondary"
	case 2:
		return "AimTertiary"
	default:
		return "AimPrimary"
	}
}

// callbackBridgeForUnit returns the callback bridge attached by strict unit
// composition. A playable unit always has this binding; an unattached unit
// cannot run weapon callbacks [04 §4.1][04 §5.3].
func (s *Service) callbackBridgeForUnit(u *units.Unit) *cob.CallbackBridge {
	if u == nil {
		return nil
	}
	if binding := u.COBBinding(); binding != nil && binding.Callbacks != nil {
		return binding.Callbacks
	}
	return nil
}

// UnitStepSummary reports the per-unit Aim handshake and firing result.
// Stable integer/fixed-point state only; it observes behavior, consumes no RNG,
// and alters no simulation decisions.
type UnitStepSummary struct {
	Dispatched       bool  // any Aim* dispatched this visit [04 §5.3]
	DispatchSlot     int   // weapon slot of the first dispatch (-1 if none)
	DispatchWeaponID int32 // weapon ID of the first dispatch
	ReturnSeen       bool  // an explicit COB return was consumed this visit [06 §3.3]
	ReturnValue      int32 // value of the last consumed return (0 or nonzero)
	Drained          bool  // combat performed the visit's synchronous vm.Drain(1) [04 §4.2][GAP T15 C17]
	Fired            int   // projectiles created via TryFire this visit [06 §4]
}

// StepWeaponsForUnit runs the per-unit weapon pipeline for one unit visit ON-04 [06 §3.3][06 §4][04 §5.3][GAP T15].
// It is the authoritative per-unit step; the session owns the deterministic
// pool-order loop and invokes this method directly [06 §1.2] C1 (I1).
// Dependencies are the session's world, visibility, terrain, economy, catalog,
// simulation RNG, and CRT RNG services.
// RS-08: exactly one VM drain per unit visit at the normal window [04 §4.2][04 §4.6][GAP T15 C17] (I7):
// queue all TargetCleared/Aim callbacks in slot order 0..2, drain once delta 1 (eight threads then one piece pass),
// collect actual explicit Aim returns, then apply post-drain fire permissions in slot order without second drain.
// Missing script or thread exhaustion never authorizes fire (explicit check) [GAP T15] [06 §3.3].
func (s *Service) StepWeaponsForUnit(u *units.Unit, tick uint32, w *units.World, vis *visibility.Service, terrain *world.Terrain, econ *economy.Service, catalog *content.Catalog, simRNG *rng.Simulation, crtRNG *rng.CRT) UnitStepSummary {
	var sum UnitStepSummary
	sum.DispatchSlot = -1
	if s == nil || u == nil {
		return sum
	}
	if !u.Alive || u.Dying {
		return sum
	}
	if u.Stunned && tick >= u.ParalyzeExpire {
		u.Stunned = false
		u.ParalyzeExpire = 0
	}
	if u.Stunned {
		return sum
	}
	bridge := s.callbackBridgeForUnit(u)
	// --- Phase: pre-drain callback scheduling (TargetCleared + Aim) in slot order 0..2 [GAP T15] ---
	type slotPrep struct {
		needLatch    bool
		needResult   bool
		weapon       *content.WeaponDef
		tgtPos       Vec3
		tgtHandle    pool.Handle
		desiredYaw   uint16
		desiredPitch uint16
		ballisticOk  bool
		pitch        uint16
		suppressAim  bool
	}
	var preps [NumSlots]*slotPrep
	var queuedTargetCleared bool
	for idx := 0; idx < NumSlots; idx++ {
		slot := u.SlotAt(idx)
		if slot == nil || !slot.IsPopulated() {
			continue
		}
		if slot.Target.Kind == units.TargetUnit && slot.Target.Unit != 0 {
			tu := w.Unit(slot.Target.Unit)
			if tu == nil || !tu.Alive || tu.Dying {
				clearedHeading := slot.DesiredYaw != 0
				clearedPitch := slot.DesiredPitch != 0x8000
				slot.Target = units.Target{Kind: units.TargetNone}
				slot.Aim.IssueBit = false
				slot.Aim.Ready = false
				slot.Flags &^= 0x01
				if s.pendingAims != nil {
					delete(s.pendingAims, pendingKey{Unit: u.Handle, Slot: idx})
				}
				if (clearedHeading || clearedPitch) && bridge != nil {
					bridge.TargetCleared(int32(idx))
					queuedTargetCleared = true
				}
				slot.Flags &^= 0x02
				continue
			}
		}
		if slot.Target.Kind == units.TargetNone || slot.Flags&0x02 == 0 {
			if acquired, ok := acquireTargetForSlot(u, slot, idx, w, vis, terrain, simRNG, econ, catalog); ok {
				savedYaw := slot.DesiredYaw
				savedPitch := slot.DesiredPitch
				savedIssue := slot.Aim.IssueBit
				savedReady := slot.Aim.Ready
				savedFlags := slot.Flags & 0x01
				slot.Target = units.Target{Kind: units.TargetUnit, Unit: acquired}
				slot.Flags |= 0x02
				slot.DesiredYaw = savedYaw
				slot.DesiredPitch = savedPitch
				slot.Aim.IssueBit = savedIssue
				slot.Aim.Ready = savedReady
				slot.Flags = (slot.Flags &^ 0x01) | savedFlags
			} else {
				continue
			}
		}
		if slot.Target.Kind == units.TargetNone {
			continue
		}
		weapon := slot.Weapon
		if weapon == nil {
			continue
		}
		var tgtPos Vec3
		var tgtHandle pool.Handle
		if slot.Target.Kind == units.TargetUnit {
			tgtHandle = slot.Target.Unit
			if tu := w.Unit(tgtHandle); tu != nil {
				tgtPos = Vec3{X: tu.X, Y: tu.Y, Z: tu.Z}
			} else {
				continue
			}
		} else {
			tgtPos = Vec3{X: slot.Target.X, Y: 0, Z: slot.Target.Z}
		}
		// Weapon piece selection is a synchronous Q path. AimFrom* uses -1 and
		// falls back to Query* with seed 0; SweetSpot is a separate Q query and
		// must not be conflated with the muzzle result [R-P0-07][04 §5.3].
		if bridge != nil {
			piece := bridge.AimPiece(cob.WeaponSlot(idx))
			if piece.Started && piece.QueryValue() >= 0 {
				slot.MuzzlePiece = piece.QueryValue()
			}
			_ = bridge.SweetSpot()
		}
		muzzlePos, muzzleOK := muzzleWorldPosResolved(u, slot.MuzzlePiece)
		if !muzzleOK {
			// A strict production binding resolves nonnegative queried pieces
			// through its model map; a negative query selects the unit-origin path.
			continue
		}
		dx := tgtPos.X.Sub(muzzlePos.X)
		dy := tgtPos.Y.Sub(muzzlePos.Y)
		dz := tgtPos.Z.Sub(muzzlePos.Z)
		var desiredYaw uint16
		var desiredPitch uint16
		var ballisticOk bool
		var ballisticPitch uint16
		if weapon.Ballistic {
			var grav numeric.Fixed
			if terrain != nil {
				grav = terrain.Gravity
			}
			vel := numeric.Fixed(int64(weapon.WeaponVelocity))
			if vel.Raw() == 0 {
				desiredYaw = uint16(YawFromDelta(dx, dz))
				desiredPitch = 0x8000
				slot.DesiredYaw = desiredYaw
				slot.DesiredPitch = desiredPitch
				preps[idx] = &slotPrep{weapon: weapon, tgtPos: tgtPos, tgtHandle: tgtHandle, desiredYaw: desiredYaw, desiredPitch: desiredPitch, suppressAim: true}
				continue
			}
			pitch, ok := BallisticSolve(dx, dy, dz, vel, grav, weapon.MinBarrelAngle)
			if !ok {
				desiredYaw = uint16(YawFromDelta(dx, dz))
				desiredPitch = 0x8000
				slot.DesiredYaw = desiredYaw
				slot.DesiredPitch = desiredPitch
				preps[idx] = &slotPrep{weapon: weapon, tgtPos: tgtPos, tgtHandle: tgtHandle, desiredYaw: desiredYaw, desiredPitch: desiredPitch, suppressAim: true}
				continue
			}
			ballisticPitch = pitch
			ballisticOk = true
			desiredPitch = pitch
			desiredYaw = uint16(YawFromDelta(dx, dz))
		} else {
			desiredYaw = uint16(YawFromDelta(dx, dz))
			desiredPitch = uint16(PitchFromDelta(dx, dy, dz))
		}
		slot.DesiredYaw = desiredYaw
		slot.DesiredPitch = desiredPitch
		needLatch, needResult := aimRequirement(weapon)
		suppress := weapon.Ballistic && desiredPitch == 0x8000
		preps[idx] = &slotPrep{weapon: weapon, tgtPos: tgtPos, tgtHandle: tgtHandle, desiredYaw: desiredYaw, desiredPitch: desiredPitch, ballisticOk: ballisticOk, pitch: ballisticPitch, needLatch: needLatch, needResult: needResult, suppressAim: suppress}
		if needResult && !slot.Aim.Ready && !suppress {
			if !slot.Aim.IssueBit {
				weaponID := weapon.ID
				if bridge == nil {
					slot.Aim.IssueBit = true
				} else {
					key := pendingKey{Unit: u.Handle, Slot: idx}
					if _, present := bridge.VM.ScriptPC(aimNameForSlot(idx)); !present {
					}
					result := bridge.Aim(cob.WeaponSlot(idx), desiredYaw, desiredPitch, func(ret cob.CallbackReturn) {
						if s.pendingAims != nil {
							delete(s.pendingAims, key)
						}
						sum.ReturnSeen = true
						sum.ReturnValue = ret.Value
						if ret.Explicit && ret.Value != 0 {
							slot.Aim.Ready = true
						}
					})
					slot.Aim.IssueBit = true
					slot.Flags |= 0x01
					if result.Started {
						if s.pendingAims == nil {
							s.pendingAims = make(map[pendingKey]pendingAim)
						}
						s.pendingAims[key] = pendingAim{ThreadIdx: result.Thread, DispatchedTick: tick}
						if !sum.Dispatched {
							sum.Dispatched = true
							sum.DispatchSlot = idx
							sum.DispatchWeaponID = weaponID
						}
					} else {
					}
				}
			}
		}
	}
	hasPending := false
	for idx := 0; idx < NumSlots; idx++ {
		if s.pendingAims != nil {
			if _, ok := s.pendingAims[pendingKey{Unit: u.Handle, Slot: idx}]; ok {
				hasPending = true
				break
			}
		}
	}
	shouldDrain := sum.Dispatched || hasPending || queuedTargetCleared
	if shouldDrain && bridge != nil {
		bridge.Drain(1)
		sum.Drained = true
	}
	for idx := 0; idx < NumSlots; idx++ {
		pre := preps[idx]
		slot := u.SlotAt(idx)
		if slot == nil || !slot.IsPopulated() || pre == nil {
			continue
		}
		weapon := pre.weapon
		needLatch, needResult := pre.needLatch, pre.needResult
		key := pendingKey{Unit: u.Handle, Slot: idx}
		if _, ok := s.pendingAims[key]; ok {
			continue
		} else {
			if needResult && slot.Aim.IssueBit && !slot.Aim.Ready {
				continue
			}
		}
		if needLatch && !slot.Aim.IssueBit {
			continue
		}
		if needResult && !slot.Aim.Ready {
			continue
		}
		if slot.Reload > 0 {
			continue
		}
		if !checkAdmission(u, slot, weapon, pre.tgtPos, pre.tgtHandle, w, vis, terrain, pre.ballisticOk, pre.pitch) {
			if weapon.Turret {
				slot.Aim.IssueBit = false
				slot.Aim.Ready = false
				slot.Flags &^= 0x01
				if s.pendingAims != nil {
					delete(s.pendingAims, pendingKey{Unit: u.Handle, Slot: idx})
				}
			}
			continue
		}
		if weapon.Stockpile {
			if slot.Ammo <= 0 {
				continue
			}
		} else if econ != nil {
			eCost := float32(weapon.EnergyPerShot)
			mCost := float32(weapon.MetalPerShot)
			if eCost != 0 || mCost != 0 {
				p := &econ.Players[u.Owner]
				if p.Stock[economy.Energy] < eCost || p.Stock[economy.Metal] < mCost {
					continue
				}
			}
		}
		if !tryFireForSlot(u, slot, idx, tick, terrain, simRNG, s, w) {
			continue
		}
		if needResult || needLatch {
			slot.Aim.IssueBit = false
			slot.Aim.Ready = false
			slot.Flags &^= 0x01
			if s.pendingAims != nil {
				delete(s.pendingAims, pendingKey{Unit: u.Handle, Slot: idx})
			}
		}
		if !weapon.Stockpile {
			stored := ComputeStoredReload(u.Health, u.MaxHealth, u.Kills, weapon.ReloadTime)
			slot.Reload = stored
		}
		if weapon.Stockpile && slot.Ammo > 0 {
			slot.Ammo--
			if slot.Ammo < 0 {
				slot.Ammo = 0
			}
		}
		if !weapon.Stockpile && econ != nil {
			eCost := float32(weapon.EnergyPerShot)
			mCost := float32(weapon.MetalPerShot)
			if eCost != 0 || mCost != 0 {
				economy.ImmediateDebit(&econ.Players[u.Owner], eCost, mCost)
			}
		}
		sum.Fired++
	}
	return sum
}

// TickWeapons runs the integrated per-unit weapon pipeline [06 §3][06 §4][04 §5.3][GAP T15].
// Compatibility wrapper: loops over units in deterministic order and delegates to StepWeaponsForUnit ON-04.
// Documented non-authoritative: authoritative behavior is per-unit StepWeaponsForUnit.
func (s *Service) TickWeapons(tick uint32, w *units.World, vis *visibility.Service, terrain *world.Terrain, econ *economy.Service, catalog *content.Catalog, simRNG *rng.Simulation, crtRNG *rng.CRT) {
	if s == nil || w == nil {
		return
	}
	// DET-01: no global fallback; session must inject simRNG.
	if simRNG == nil {
		// nil means no draw (return 0 without advancing) — production always injects.
	}
	var unitList []*units.Unit
	if w.IsSliced() {
		unitList = w.IterSliced()
		_ = unitList
	} else {
		unitList = w.Iter()
		_ = unitList
	}
	if w.IsSliced() {
		for player := 0; player < 10; player++ {
			start, end, ok := w.SliceForPlayer(player)
			if !ok {
				continue
			}
			for slot := start; slot <= end; slot++ {
				u := w.Unit(pool.Handle(slot))
				if u == nil || !u.Alive || u.Dying {
					continue
				}
				if u.Stunned && tick >= u.ParalyzeExpire {
					u.Stunned = false
					u.ParalyzeExpire = 0
				}
				if u.Stunned {
					continue
				}
				s.StepWeaponsForUnit(u, tick, w, vis, terrain, econ, catalog, simRNG, crtRNG)
			}
		}
	} else {
		buckets := make([][]*units.Unit, 10)
		for _, u := range unitList {
			if u == nil || u.Dying {
				continue
			}
			if u.Stunned && tick >= u.ParalyzeExpire {
				u.Stunned = false
				u.ParalyzeExpire = 0
			}
			if u.Stunned {
				continue
			}
			buckets[u.Owner] = append(buckets[u.Owner], u)
		}
		for player := 0; player < 10; player++ {
			for _, u := range buckets[player] {
				s.StepWeaponsForUnit(u, tick, w, vis, terrain, econ, catalog, simRNG, crtRNG)
			}
		}
	}
}

func acquireTargetForSlot(u *units.Unit, slot *units.Slot, idx int, w *units.World, vis *visibility.Service, terrain *world.Terrain, simRNG *rng.Simulation, econ *economy.Service, catalogs ...*content.Catalog) (pool.Handle, bool) {
	if u == nil || w == nil || slot == nil || slot.Weapon == nil {
		return 0, false
	}
	var catalog *content.Catalog
	if len(catalogs) != 0 {
		catalog = catalogs[0]
	}
	weapon := slot.Weapon
	var candidates []Candidate
	var seaLevel numeric.Fixed
	if terrain != nil {
		seaLevel = terrain.SeaLevelWorld()
	}
	for _, cand := range w.Iter() {
		if cand == nil || cand.Handle == u.Handle || !cand.Alive || cand.Dying {
			continue
		}
		hostile := isHostile(u, cand, econ)
		if !hostile {
			continue
		}
		ownSide := u.Owner == cand.Owner
		cloaked := isCloakedUnit(cand)
		underwater := isUnderwaterUnit(cand, seaLevel)
		underwaterSeen := isAllied(u.Owner, cand.Owner, econ)
		var catMask content.CategoryMask
		maskResolved := catalog != nil && cand.Def != nil
		if cand.Def != nil {
			catMask = cand.Def.DefinitionMask()
		}
		c := Candidate{
			Handle:               cand.Handle,
			X:                    cand.X,
			Z:                    cand.Z,
			Y:                    cand.Y,
			CategoryMask:         catMask,
			CategoryMaskResolved: maskResolved,
			Hostile:              true,
			OwnSide:              ownSide,
			Cloaked:              cloaked,
			Underwater:           underwater,
			UnderwaterSeen:       underwaterSeen,
			AirTarget:            cand.Def != nil && cand.Def.CanFly,
		}
		candidates = append(candidates, c)
	}
	acq := Acquisition{
		ShooterX:      u.X,
		ShooterZ:      u.Z,
		ShooterY:      u.Y,
		SeaLevel:      seaLevel,
		Range:         weapon.Range,
		BadTargetMask: badMaskForSlot(u.Def, idx),
		MaskResolved:  catalog != nil && u.Def != nil,
		WaterWeapon:   weapon.WaterWeapon,
		ToAir:         weapon.ToAirWeapon,
		Ballistic:     weapon.Ballistic,
		RNG:           simRNG,
	}
	if vis == nil {
		// Hostile acquisition is visibility-gated. Keep the predicate installed
		// even when the caller supplied no service so Acquisition fails closed.
		acq.Visible = func(Candidate) bool { return false }
	} else {
		acq.Visible = func(c Candidate) bool {
			candUnit := w.Unit(c.Handle)
			if candUnit == nil {
				return false
			}
			var status uint32
			if c.UnderwaterSeen {
				status |= 0x200
			}
			t := visibility.Target{
				Owner:  visibility.PlayerID(candUnit.Owner),
				X:      c.X,
				Y:      c.Y,
				Z:      c.Z,
				Hidden: c.Cloaked,
				Status: status,
			}
			return vis.IsVisible(visibility.PlayerID(u.Owner), t)
		}
	}
	if weapon.Ballistic {
		acq.BallisticFeasible = func(c Candidate) bool {
			muzzle, muzzleOK := muzzleWorldPosResolved(u, slot.MuzzlePiece)
			if !muzzleOK {
				return false
			}
			var tgt Vec3
			if tu := w.Unit(c.Handle); tu != nil {
				tgt = Vec3{X: tu.X, Y: tu.Y, Z: tu.Z}
			} else {
				return false
			}
			dx := tgt.X.Sub(muzzle.X)
			dy := tgt.Y.Sub(muzzle.Y)
			dz := tgt.Z.Sub(muzzle.Z)
			var grav numeric.Fixed
			if terrain != nil {
				grav = terrain.Gravity
			}
			vel := numeric.Fixed(int64(weapon.WeaponVelocity))
			_, ok := BallisticSolve(dx, dy, dz, vel, grav, weapon.MinBarrelAngle)
			return ok
		}
	}
	h, ok := AcquireTarget(candidates, acq)
	return h, ok
}

func checkAdmission(u *units.Unit, slot *units.Slot, weapon *content.WeaponDef, tgtPos Vec3, tgtHandle pool.Handle, w *units.World, vis *visibility.Service, terrain *world.Terrain, ballisticOk bool, ballisticPitch uint16) bool {
	if weapon == nil {
		return false
	}
	if !WithinRange(u.X, u.Z, tgtPos.X, tgtPos.Z, weapon.Range) {
		return false
	}
	var seaLevel numeric.Fixed
	if terrain != nil {
		seaLevel = terrain.SeaLevelWorld()
	}
	if weapon.WaterWeapon {
	} else {
		if u.Y <= seaLevel {
			return false
		}
		var tgtY numeric.Fixed
		if tgtHandle != 0 {
			if tu := w.Unit(tgtHandle); tu != nil {
				tgtY = tu.Y
			} else {
				tgtY = tgtPos.Y
			}
		} else {
			tgtY = tgtPos.Y
		}
		if tgtY <= seaLevel {
			return false
		}
		if weapon.ToAirWeapon {
			isAir := false
			if tgtHandle != 0 {
				if tu := w.Unit(tgtHandle); tu != nil && tu.Def != nil {
					isAir = tu.Def.CanFly
				}
			}
			if !isAir {
				return false
			}
		}
		if weapon.Ballistic {
			if !ballisticOk {
				return false
			}
		}
	}
	return true
}

func muzzleWorldPosResolved(u *units.Unit, piece int32) (Vec3, bool) {
	if u == nil {
		return Vec3{}, false
	}
	// A negative query result selects the ordinary unit-origin muzzle path;
	// only a nonnegative piece requires the strict COB/model composition [06 §4.1].
	if piece < 0 {
		return Vec3{X: u.X, Y: u.Y, Z: u.Z}, true
	}
	if binding := u.COBBinding(); binding != nil {
		origin, ok := binding.ComposePiece(int(piece), u.Move.Heading, u.Move.Pitch, u.Move.Bank)
		if !ok {
			return Vec3{}, false
		}
		return Vec3{X: u.X.Add(origin[0]), Y: u.Y.Add(origin[1]), Z: u.Z.Add(origin[2])}, true
	}
	return Vec3{}, false
}

func tryFireForSlot(u *units.Unit, slot *units.Slot, idx int, tick uint32, terrain *world.Terrain, simRNG *rng.Simulation, svc *Service, w *units.World) bool {
	if u == nil || slot == nil || slot.Weapon == nil || svc == nil {
		return false
	}
	weapon := slot.Weapon
	var tgt Target
	switch slot.Target.Kind {
	case units.TargetUnit:
		tgt = Target{Kind: TargetUnit, Unit: slot.Target.Unit}
	case units.TargetGround:
		tgt = Target{Kind: TargetPoint, X: slot.Target.X, Z: slot.Target.Z}
	default:
		tgt = Target{Kind: TargetNone}
	}
	var gravity numeric.Fixed
	if terrain != nil {
		gravity = terrain.Gravity
	}
	origin, ok := muzzleWorldPosResolved(u, slot.MuzzlePiece)
	if !ok {
		return false
	}
	targetWorld := func(h pool.Handle) (Vec3, bool) {
		if tu := w.Unit(h); tu != nil {
			return Vec3{X: tu.X, Y: tu.Y, Z: tu.Z}, true
		}
		return Vec3{}, false
	}
	muzzleWorld := func(piece int32) (Vec3, bool) {
		return muzzleWorldPosResolved(u, piece)
	}
	muzzlePieceFn := func(slotIdx int) int32 {
		if slot.MuzzlePiece >= 0 {
			return slot.MuzzlePiece
		}
		return -1
	}
	scriptAdapter := &fireScriptAdapter{bridge: svc.callbackBridgeForUnit(u), unit: u}
	// [06 §13.2] wire weapon-start events to same service sink installed at composition [06 §4.1] C2
	fireEvents := &combatFireEvents{svc: svc, tick: tick, shooter: u.Handle, pos: origin}
	ports := FirePorts{
		ShooterSide: uint8(u.Owner),
		Origin:      origin,
		MuzzlePiece: muzzlePieceFn,
		MuzzleWorld: muzzleWorld,
		TargetWorld: targetWorld,
		Gravity:     gravity,
		Script:      scriptAdapter,
		Events:      fireEvents,
		RNG:         simRNG,
		Spy:         nil,
	}
	cSlot := Slot{
		Weapon:       weapon,
		Reload:       slot.Reload,
		Flags:        slot.Flags,
		DesiredYaw:   slot.DesiredYaw,
		DesiredPitch: slot.DesiredPitch,
		Ammo:         slot.Ammo,
		MuzzlePiece:  slot.MuzzlePiece,
		Aim:          slot.Aim,
		Target:       tgt,
	}
	h, ok := TryFire(svc, &cSlot, idx, tgt, tick, ports)
	if ok {
		slot.MuzzlePiece = cSlot.MuzzlePiece
		slot.Aim = cSlot.Aim
		slot.Flags = cSlot.Flags
		if h != 0 {
			recIdx := int(h) - 1
			if recIdx >= 0 && recIdx < len(svc.Records) {
				svc.Records[recIdx].Shooter = u.Handle
				svc.Records[recIdx].ShooterSide = uint8(u.Owner)
			}
		}
		return true
	}
	slot.MuzzlePiece = cSlot.MuzzlePiece
	slot.Aim = cSlot.Aim
	return false
}

type fireScriptAdapter struct {
	bridge *cob.CallbackBridge
	unit   *units.Unit
}

func (a *fireScriptAdapter) FireWeapon(slotIdx int) {
	if a == nil || a.unit == nil {
		return
	}
	if a.bridge == nil {
		return
	}
	a.bridge.Fire(cob.WeaponSlot(slotIdx))
}

func (a *fireScriptAdapter) RockUnit(slotIdx int) {
	if a == nil || a.unit == nil {
		return
	}
	if a.bridge == nil {
		return
	}
	u := a.unit
	slot := u.SlotAt(slotIdx)
	if slot == nil {
		return
	}
	rel := int16(slot.DesiredYaw - u.Move.Heading)
	a.bridge.RockUnit(rel)
}

// combatFireEvents forwards weapon start sound/smoke to the service event sink
// [06 §4.1] C2 [06 §13.2] ordering: start sound before Fire/Rock, start smoke after.
type combatFireEvents struct {
	svc     *Service
	tick    uint32
	shooter pool.Handle
	pos     Vec3
}

func (c *combatFireEvents) StartSound(name string) {
	if c == nil || c.svc == nil || name == "" {
		return
	}
	c.svc.emitEvent(Event{Kind: EventStartSound, Tick: c.tick, Source: c.shooter, Position: c.pos, Sound: name})
}

func (c *combatFireEvents) StartSmoke(h pool.Handle) {
	if c == nil || c.svc == nil {
		return
	}
	c.svc.emitEvent(Event{Kind: EventStartSmoke, Tick: c.tick, Source: c.shooter, Target: h, Position: c.pos})
}

// EventStartSound and EventStartSmoke are weapon-start presentation events emitted
// before/after Fire callbacks [06 §4.1] C2 [06 §13.2]. Defined here to keep the
// service file as the sole owner of the start-event wiring; pool.go owns the
// impact ordering sink [06 §13.2] C27.
const (
	EventStartSound EventKind = 10 // [06 §4.1] C2 [06 §13.2]
	EventStartSmoke EventKind = 11 // [06 §4.1] C2 [06 §13.2]
)

func (s *Service) TickProjectiles(tick uint32, w *units.World, terrain *world.Terrain, windState *world.Wind, featSvc *features.Service, vis *visibility.Service, econ *economy.Service, catalog *content.Catalog, simRNG *rng.Simulation, crtRNG *rng.CRT) {
	if s == nil {
		return
	}
	// DET-01: no global fallback; session must inject simRNG.
	// ON-04 stable lookup: use once-compiled catalog index, not per-tick map rebuild
	muzzleForBurst := func(piece int16) Vec3 {
		return Vec3{}
	}
	// ON-04 stable lookup for burst params: use once-compiled index deterministically
	var weaponByID map[int32]*content.WeaponDef
	if catalog != nil {
		if idx := catalog.WeaponIndex(); idx != nil {
			weaponByID = idx
		} else if catalog.Weapons != nil {
			// Fallback for fixtures without compiled index. [02 §5 R-CONTENT-02]:
			// the same-ID merge is last-wins — the record parser unconditionally
			// stores every field it parses, so a later section with the same ID
			// overwrites the earlier one. Keys are sorted for a deterministic
			// order (I1); the last same-ID weapon in that order wins.
			weaponByID = make(map[int32]*content.WeaponDef, len(catalog.Weapons))
			keys := make([]string, 0, len(catalog.Weapons))
			for k := range catalog.Weapons {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				wd := catalog.Weapons[k]
				if wd == nil {
					continue
				}
				weaponByID[wd.ID] = wd
			}
		}
	}
	s.AdvanceBursts(tick, simRNG, weaponByID, muzzleForBurst)
	// [06 §6.4] ballistic/dropped drift: adds all three global wind values directly to position
	var windVec Vec3
	var gravity numeric.Fixed
	var seaLevel numeric.Fixed
	if terrain != nil {
		gravity = terrain.Gravity
		seaLevel = terrain.SeaLevelWorld()
	}
	if windState != nil {
		// [06 §6.4] wind vectors recomputed via MulRound; scalar capped at 1.0 [01 §7.3]
		// TODO(question): DirX/DirZ scaling as Fixed raw vs world units unresolved; treat Dir as Fixed raw
		windVec = Vec3{
			X: numeric.Fixed(int64(windState.DirX)),
			Y: numeric.Fixed(0),
			Z: numeric.Fixed(int64(windState.DirZ)),
		}
		_ = windState.Scalar // scalar published to wind generators [01 §7.3] I2 allowlist
	}
	entry := s.Count()
	for i := 0; i < entry; i++ {
		h := pool.Handle(i + 1)
		p := &s.Records[i]
		isDead := s.Slots.IsDead(h)
		if p.BurstRemaining > 0 {
			continue
		}
		var weapon *content.WeaponDef
		if catalog != nil {
			if w, ok := catalog.WeaponByID(p.WeaponID); ok {
				weapon = w
			}
		}
		if weapon == nil {
			if !isDead {
				s.MarkDead(h)
			}
			continue
		}
		if isDead {
			continue
		}
		var res AdvanceResult
		switch MotionFamilyForWeapon(weapon) {
		case MotionDirect:
			res = AdvanceDirect(p, weapon, tick)
		case MotionBallistic:
			res = AdvanceBallistic(p, weapon, tick, windVec, gravity)
		case MotionDropped:
			res = AdvanceDropped(p, weapon, tick, windVec, gravity)
		case MotionMeteor:
			res = AdvanceMeteor(p, weapon, tick)
		case MotionSelfProp:
			res = AdvanceSelfProp(p, weapon, tick, gravity, seaLevel)
		default:
			res = AdvanceRetire
		}
		if res == AdvanceRetire {
			s.MarkDead(h)
			continue
		}
		if res == AdvanceImpact {
			handleProjectileImpact(s, h, p, weapon, w, terrain, featSvc, econ, catalog, tick, windVec, simRNG, false)
			s.MarkDead(h)
			continue
		}
		if res == AdvancePhaseTransition {
			continue
		}
		hitUnit, hitFeature, isWaterTerrain, isOffMap, bounce := checkCollision(p, weapon, w, terrain, featSvc)
		if bounce {
			continue
		}
		if isOffMap {
			s.MarkDead(h)
			continue
		}
		needImpact := false
		var directTarget pool.Handle
		if hitUnit != 0 {
			needImpact = true
			directTarget = hitUnit
		} else if hitFeature != nil {
			needImpact = true
		} else if isWaterTerrain {
			needImpact = true
		} else {
			if terrain != nil {
				cellX := world.WorldToCell(p.Pos.X)
				cellZ := world.WorldToCell(p.Pos.Z)
				if cellX >= 0 && cellZ >= 0 && cellX < terrain.CellW && cellZ < terrain.CellH {
					th := terrain.HeightAt(p.Pos.X, p.Pos.Z)
					if th.Raw() != -1 && p.Pos.Y.Raw() < th.Raw() {
						needImpact = true
					}
				}
			}
		}
		if needImpact {
			isWater := isWaterTerrain && directTarget == 0
			handleProjectileImpact(s, h, p, weapon, w, terrain, featSvc, econ, catalog, tick, windVec, simRNG, isWater)
			if NoExplodeRetirement(weapon.NoExplode, true, isOffMap, false) {
				s.MarkDead(h)
			}
		}
		_ = directTarget
		_ = hitFeature
	}
	s.Compact(nil)
}

func checkCollision(p *Projectile, weapon *content.WeaponDef, w *units.World, terrain *world.Terrain, featSvc *features.Service) (hitUnit pool.Handle, hitFeature *features.Instance, isWaterTerrain bool, isOffMap bool, bounce bool) {
	if terrain != nil {
		cx := world.WorldToCell(p.Pos.X)
		cz := world.WorldToCell(p.Pos.Z)
		if cx < 0 || cz < 0 || cx >= terrain.CellW || cz >= terrain.CellH {
			isOffMap = true
			return
		}
		cache := [2]int32{p.CacheCellX, p.CacheCellZ}
		suppressed := FeatureCacheSuppressed(&cache, int32(cx), int32(cz))
		p.CacheCellX, p.CacheCellZ = cache[0], cache[1]
		if suppressed {
		} else {
			if featSvc != nil {
				if inst := featSvc.InstanceAt(int(cx), int(cz)); inst != nil && inst.Def != nil {
					top := inst.Y.Add(numeric.Fixed(int64(16) * 65536))
					if p.Pos.Y.Raw() < top.Raw() {
						hitFeature = inst
						return
					}
				}
			} else if terrain != nil {
				if featIdx := terrain.Plot[int(cz)*int(terrain.CellW)+int(cx)].Feature(); featIdx < 0xFFFF && featIdx != 0xFFFE {
					base := terrain.CoarseHeightAt(cx, cz)
					top := base.Add(numeric.Fixed(int64(16) * 65536))
					if p.Pos.Y.Raw() < top.Raw() {
						hitFeature = &features.Instance{CX: int(cx), CZ: int(cz)}
						return
					}
				}
			}
		}
		th := terrain.HeightAt(p.Pos.X, p.Pos.Z)
		if th.Raw() != -1 && p.Pos.Y.Raw() < th.Raw() {
			if weapon != nil && weapon.GroundBounce {
				p.Velocity.Y = numeric.Fixed(int64(-(p.Velocity.Y.Raw() >> 2)))
				bounce = true
				return
			}
			return
		}
		sea := terrain.SeaLevelWorld()
		if p.Pos.Y.Raw() < sea.Raw() {
			if weapon != nil && !weapon.WaterWeapon {
				isWaterTerrain = true
				return
			}
		}
	}
	if w != nil {
		// [06 §8.1] faithful two-slot Y-gate via CollisionSlotYGate [06 §8.1] C? ; planar r=24 retained for XY until grid slots wired
		bestDist2 := int64(1 << 62)
		var best pool.Handle
		for _, u := range w.Iter() {
			if u == nil || !u.Alive || u.Dying {
				continue
			}
			if u.Handle == p.Shooter {
				continue
			}
			lower := int32(u.Y.Raw() - 16*65536)
			upper := int32(u.Y.Raw() + 16*65536)
			py := int32(p.Pos.Y.Raw())
			// [06 §8.1] slot0: Y<upper (no lower), slot1: lower<=Y<=upper; either slot may authorize
			if !CollisionSlotYGate(py, lower, upper, 0) && !CollisionSlotYGate(py, lower, upper, 1) {
				continue
			}
			dx := p.Pos.X.Int() - u.X.Int()
			dz := p.Pos.Z.Int() - u.Z.Int()
			dist2 := int64(dx)*int64(dx) + int64(dz)*int64(dz)
			const hitRadius = 24 // TODO(question): planar radius approximation retained; grid-slot XY gate unresolved [06 §8.1]
			if dist2 <= hitRadius*hitRadius && dist2 < bestDist2 {
				bestDist2 = dist2
				best = u.Handle
			}
		}
		if best != 0 {
			hitUnit = best
			return
		}
	}
	return
}

func handleProjectileImpact(s *Service, h pool.Handle, p *Projectile, weapon *content.WeaponDef, w *units.World, terrain *world.Terrain, featSvc *features.Service, econ *economy.Service, catalog *content.Catalog, tick uint32, wind Vec3, simRNG *rng.Simulation, isWaterTerrain bool) {
	if weapon == nil {
		return
	}
	hasDirectTarget := p.TargetUnit != 0
	// Impact presentation is part of the concrete projectile path. Keep the
	// researched order visible here: shake, impact sound, end smoke or
	// explosion, then the impact event and authoritative damage [06 §13.2].
	if weapon.ShakeMagnitude != 0 || weapon.ShakeDuration != 0 {
		s.emitEvent(Event{Kind: EventShake, Tick: tick, Source: p.Shooter, Position: p.Pos, Magnitude: weapon.ShakeMagnitude, Duration: weapon.ShakeDuration})
	}
	if hasDirectTarget || !isWaterTerrain {
		if weapon.SoundHit != "" {
			s.emitEvent(Event{Kind: EventHitSound, Tick: tick, Source: p.Shooter, Position: p.Pos, Sound: weapon.SoundHit})
		}
	} else if weapon.SoundWater != "" {
		s.emitEvent(Event{Kind: EventWaterSound, Tick: tick, Source: p.Shooter, Position: p.Pos, Sound: weapon.SoundWater})
	}
	if weapon.EndSmoke && !isWaterTerrain {
		s.emitEvent(Event{Kind: EventEndSmoke, Tick: tick, Source: p.Shooter, Position: p.Pos})
	} else {
		graphic := weapon.ExplosionGaf
		isWaterExplosion := isWaterTerrain && !hasDirectTarget
		if isWaterExplosion {
			graphic = weapon.WaterExplosionGaf
			if graphic == "" {
				graphic = weapon.WaterExplosionArt
			}
		} else if graphic == "" {
			graphic = weapon.ExplosionArt
		}
		if graphic != "" {
			kind := EventExplosion
			if isWaterExplosion {
				kind = EventWaterExplosion
			}
			s.emitEvent(Event{Kind: kind, Tick: tick, Source: p.Shooter, Position: p.Pos, Graphic: graphic})
		}
	}
	s.emitEvent(Event{Kind: EventProjectileImpact, Tick: tick, Source: p.Shooter, Target: p.TargetUnit, Position: p.Pos})
	applyProjectileDamage(s, p, weapon, w, terrain, featSvc, econ, catalog, tick, simRNG, hasDirectTarget, isWaterTerrain, h)
	_ = wind
}

func applyProjectileDamage(service *Service, p *Projectile, weapon *content.WeaponDef, w *units.World, terrain *world.Terrain, featSvc *features.Service, econ *economy.Service, catalog *content.Catalog, tick uint32, simRNG *rng.Simulation, hasDirectTarget bool, isWaterTerrain bool, handle pool.Handle) {
	if w == nil || weapon == nil {
		return
	}
	if hasDirectTarget && weapon.AreaOfEffect <= 16 && p.TargetUnit != 0 {
		victim := w.Unit(p.TargetUnit)
		if victim != nil {
			applyDamageToUnit(service, victim, p, weapon, 1.0, 0, w, tick)
		}
		if p.Shooter == 0 {
			return
		}
		return
	}
	radius := BlastRadius(weapon.AreaOfEffect)
	if radius <= 0 {
		if hasDirectTarget && p.TargetUnit != 0 {
			if victim := w.Unit(p.TargetUnit); victim != nil {
				applyDamageToUnit(service, victim, p, weapon, 1.0, 0, w, tick)
			}
		}
		return
	}
	// Shared area splash: EnumerateArea→DistanceToBox→Falloff→ApplyDamage [06 §9.3]
	// Extracted to ExplodeWeaponAt for death DoExplosion reuse [06 §12.1] C22–C25 (I1, I2)
	service.ExplodeWeaponAt(w, terrain, weapon, p.Pos, p.Shooter, tick)
	_ = featSvc
	_ = econ
	_ = catalog
	_ = simRNG
	_ = isWaterTerrain
	_ = handle
	return
}

// ExplodeWeaponAt is the shared authoritative area-damage entry point for
// projectile splash and death explosions [06 §9.3][06 §12.1] C22–C25.
// It performs EnumerateArea → DistanceToBox (fixed-point box, Y+16) → Falloff (float32) →
// SelectBaseDamage → ComputeScaledAmount → ApplyDamage→Destroy exactly as TickProjectiles
// does, deterministically (pool asc via Iter, I1) and without new float64 sites
// (I2 allowlist: Falloff float32 only). Collect-then-apply avoids double-processing
// victims when nested deaths chain-explode [01 §4.4].
func (s *Service) ExplodeWeaponAt(w *units.World, terrain *world.Terrain, weapon *content.WeaponDef, impact Vec3, shooter pool.Handle, tick uint32) {
	if w == nil || weapon == nil {
		return
	}
	radius := BlastRadius(weapon.AreaOfEffect) // [06 §9.3] unsigned area>>1
	if radius <= 0 {
		return
	}
	var mapW, mapH int32
	if terrain != nil {
		mapW = terrain.CellW
		mapH = terrain.CellH
	}
	type victim struct {
		h       pool.Handle
		u       *units.Unit
		dist    int32
		falloff float32
	}
	var victims []victim
	EnumerateArea(impact, radius, mapW, mapH, func(cx, cz int32) {
		for _, u := range w.Iter() { // deterministic pool asc (I1)
			if u == nil || !u.Alive || u.Dying {
				continue
			}
			ucx := world.WorldToCell(u.X)
			ucz := world.WorldToCell(u.Z)
			if ucx != cx || ucz != cz {
				continue
			}
			footX := int32(1)
			footZ := int32(1)
			if u.Def != nil {
				if u.Def.FootprintX > 0 {
					footX = u.Def.FootprintX
				}
				if u.Def.FootprintZ > 0 {
					footZ = u.Def.FootprintZ
				}
			}
			halfX := numeric.Fixed(int64(footX) * 1048576 / 2)
			halfZ := numeric.Fixed(int64(footZ) * 1048576 / 2)
			min := Vec3{X: u.X.Sub(halfX), Y: u.Y, Z: u.Z.Sub(halfZ)}
			max := Vec3{X: u.X.Add(halfX), Y: u.Y.Add(numeric.Fixed(int64(16) * 65536)), Z: u.Z.Add(halfZ)}
			uv := UnitForArea{Handle: u.Handle, Pos: Vec3{X: u.X, Y: u.Y, Z: u.Z}, Min: min, Max: max}
			dist := DistanceToBox(impact, uv) // integer [06 §9.3]
			if dist >= radius {
				continue // strict < radius [06 §9.3]
			}
			falloff := float32(1)
			if dist != 0 {
				falloff = Falloff(float32(dist), float32(radius), float32(weapon.EdgeEffectiveness)) // [06 §9.3] float32
			}
			victims = append(victims, victim{h: u.Handle, u: u, dist: dist, falloff: falloff})
		}
	})
	if terrain == nil || mapW == 0 {
		// Fallback when no terrain map (mirrors TickProjectiles fallback) — planar dist2 check, no float64
		if len(victims) == 0 {
			for _, u := range w.Iter() { // deterministic (I1)
				if u == nil || !u.Alive || u.Dying {
					continue
				}
				dx := impact.X.Int() - u.X.Int()
				dz := impact.Z.Int() - u.Z.Int()
				dist2 := int64(dx)*int64(dx) + int64(dz)*int64(dz)
				if dist2 >= int64(radius)*int64(radius) {
					continue
				}
				victims = append(victims, victim{h: u.Handle, u: u, dist: 0, falloff: 1})
			}
		}
	}
	// Apply deterministically in collected order (EnumerateArea rows Z asc, cols X asc, Iter pool asc) [I1]
	for _, vi := range victims {
		cand := vi.u
		if cand == nil || !cand.Alive || cand.Dying {
			continue
		}
		p := &Projectile{Pos: impact, Shooter: shooter} // synthetic projectile for damage pipeline [06 §9.1]
		applyDamageToUnit(s, cand, p, weapon, vi.falloff, vi.dist, w, tick)
	}
}

func applyDamageToUnit(service *Service, victim *units.Unit, p *Projectile, weapon *content.WeaponDef, falloff float32, distance int32, w *units.World, tick uint32) {
	if victim == nil || weapon == nil || w == nil {
		return
	}
	if weapon.Paralyzer {
		if victim.Def != nil && victim.Def.ImmuneToParalyzer {
			return
		}
		base := SelectBaseDamage(weapon, victim.Def.UnitName)
		attackerKills := int32(0)
		if shooter := w.Unit(p.Shooter); shooter != nil {
			attackerKills = shooter.Kills
		}
		amount := ComputeScaledAmount(base, falloff, attackerKills, victim.Kills, false, 0, false, false, false)
		dur := uint32(amount)
		if dur == 0 {
			dur = 1
		}
		victim.ParalyzeExpire = tick + dur
		victim.Stunned = true
		return
	}
	base := SelectBaseDamage(weapon, victim.Def.UnitName)
	attackerKills := int32(0)
	if shooter := w.Unit(p.Shooter); shooter != nil {
		attackerKills = shooter.Kills
	}
	isArmored := false
	if victim.Def != nil {
		isArmored = victim.Def.ArmoredState
	}
	damageMod := int32(65536)
	if victim.Def != nil {
		damageMod = victim.Def.DamageModifier
	}
	amt := ComputeScaledAmount(base, falloff, attackerKills, victim.Kills, isArmored, damageMod, false, false, false)
	newHealth := ApplyDamage(victim.Health, amt)
	victim.Health = newHealth
	if newHealth <= 0 {
		w.Destroy(victim.Handle, units.DeathKilled)
		if service != nil {
			if service.deathNotified == nil {
				service.deathNotified = make(map[pool.Handle]*units.Unit)
			}
			if service.deathNotified[victim.Handle] != victim {
				service.deathNotified[victim.Handle] = victim
				service.emitEvent(Event{Kind: EventUnitKilled, Tick: tick, Source: p.Shooter, Target: victim.Handle, Position: Vec3{X: victim.X, Y: victim.Y, Z: victim.Z}})
			}
		}
	} else {
		dir := uint8(p.Yaw.Raw() >> 8)
		takeArg := cob.HealthPercent(victim.Health, victim.MaxHealth)
		if bridge := service.callbackBridgeForUnit(victim); bridge != nil {
			// The two damage callbacks are independent deferred starts and retain
			// their exact order after the HitByWeapon direction conversion
			// [04 §5.1]. The normal VM drain belongs to the session window.
			bridge.HitByWeapon(dir)
			bridge.TakeDamage(takeArg)
		}
	}
	_ = distance
}
