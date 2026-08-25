// Package combat — integrated weapon and projectile pipeline per [06] P0-I04.
package combat

import (
	"sort"
	"strings"

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

// emitTrace emits a debug trace event if sink is non-nil ON-04 [06 §3.3].
// Disabled by default, nil-safe, consumes no RNG and alters no state.
func (s *Service) emitTrace(ev TraceEvent) {
	if s == nil || s.Trace == nil {
		return
	}
	s.Trace(ev)
}

// aimNameForSlot returns the Aim* function name for slotIdx [04 §5.3][06 §3.3].

// categoryBits converts a retail Category token list into a 32-bit mask [02 "Unit record"] [fmt fbi] [06 §3.1].
// Tokens are split on whitespace, lowercased, mapped via fnv32 %32 to a bit. "none" yields 0.
func categoryBits(cat string) uint32 {
	if cat == "" {
		return 0
	}
	lower := strings.ToLower(strings.TrimSpace(cat))
	if lower == "" || lower == "none" {
		return 0
	}
	tokens := strings.Fields(lower)
	var bits uint32
	for _, tok := range tokens {
		if tok == "" || tok == "none" {
			continue
		}
		h := uint32(2166136261)
		for i := 0; i < len(tok); i++ {
			h ^= uint32(tok[i])
			h *= 16777619
		}
		bit := h % 32
		bits |= 1 << bit
	}
	return bits
}

func badMaskForSlot(def *content.UnitDef, slotIdx int) uint32 {
	if def == nil {
		return 0
	}
	var s string
	switch slotIdx {
	case 0:
		s = def.BadTargetCategoryWPRI
	case 1:
		s = def.BadTargetCategoryWSEC
	case 2:
		s = def.BadTargetCategoryWSPE
	default:
		s = "none"
	}
	return categoryBits(s)
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
	if u.Flags&0x04 != 0 {
		return true
	}
	if u.Def != nil && u.Def.InitCloaked {
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

// UnitStepSummary reports what one StepWeaponsForUnit visit did [ON-09 trace contract].
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
// It is the authoritative per-unit step; TickWeapons is a compatibility wrapper that loops over units in
// deterministic order (players 0..9 asc, pool slot asc) and calls this per unit [06 §1.2] C1 (I1).
// Dependencies match TickWeapons: world, vis, terrain, econ, catalog, simRNG, crtRNG.
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
	vm := u.GetScript()
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
				if (clearedHeading || clearedPitch) && vm != nil {
					_ = vm.StartByName("TargetCleared", nil)
					queuedTargetCleared = true
				}
				slot.Flags &^= 0x02
				continue
			}
		}
		if slot.Target.Kind == units.TargetNone || slot.Flags&0x02 == 0 {
			if acquired, ok := acquireTargetForSlot(u, slot, idx, w, vis, terrain, simRNG, econ); ok {
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
		muzzlePos := muzzleWorldPos(u, slot.MuzzlePiece)
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
				if vm == nil {
					s.emitTrace(TraceEvent{Tick: tick, Unit: u.Handle, Slot: idx, WeaponID: weaponID, Event: "aim_no_script"})
					slot.Aim.IssueBit = true
					slot.Aim.Ready = true
					slot.Flags |= 0x01
				} else {
					aimName := aimNameForSlot(idx)
					if _, ok := vm.ScriptPC(aimName); !ok {
						s.emitTrace(TraceEvent{Tick: tick, Unit: u.Handle, Slot: idx, WeaponID: weaponID, Event: "aim_function_absent"})
						slot.Aim.IssueBit = true
						slot.Flags |= 0x01
					} else {
						args := []int32{int32(desiredYaw), int32(desiredPitch)}
						if !vm.StartByName(aimName, args) {
							s.emitTrace(TraceEvent{Tick: tick, Unit: u.Handle, Slot: idx, WeaponID: weaponID, Event: "aim_pool_exhausted"})
							slot.Aim.IssueBit = true
							slot.Flags |= 0x01
						} else {
							threadIdx := vm.LastStartedThread()
							if s.pendingAims == nil {
								s.pendingAims = make(map[pendingKey]pendingAim)
							}
							key := pendingKey{Unit: u.Handle, Slot: idx}
							s.pendingAims[key] = pendingAim{ThreadIdx: threadIdx, DispatchedTick: tick}
							slot.Aim.StartAim()
							slot.Flags |= 0x01
							s.emitTrace(TraceEvent{Tick: tick, Unit: u.Handle, Slot: idx, WeaponID: weaponID, Event: "aim_dispatch"})
							if !sum.Dispatched {
								sum.Dispatched = true
								sum.DispatchSlot = idx
								sum.DispatchWeaponID = weaponID
							}
						}
					}
				}
			}
		}
	}
	hasPending := false
	if s.pendingAims != nil {
		for k := range s.pendingAims {
			if k.Unit == u.Handle {
				hasPending = true
				break
			}
		}
	}
	shouldDrain := sum.Dispatched || hasPending || queuedTargetCleared
	if shouldDrain && vm != nil {
		vm.Drain(1)
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
		if pending, ok := s.pendingAims[key]; ok {
			if vm == nil {
				delete(s.pendingAims, key)
			} else if val, okRet := vm.ConsumeReturn(pending.ThreadIdx); okRet {
				if val != 0 {
					slot.Aim.Ready = true
					s.emitTrace(TraceEvent{Tick: tick, Unit: u.Handle, Slot: idx, WeaponID: weapon.ID, Event: "aim_return_nonzero", ReturnValue: &val})
				} else {
					s.emitTrace(TraceEvent{Tick: tick, Unit: u.Handle, Slot: idx, WeaponID: weapon.ID, Event: "aim_return_zero", ReturnValue: &val})
				}
				sum.ReturnSeen = true
				sum.ReturnValue = val
				delete(s.pendingAims, key)
				if !slot.Aim.Ready {
					continue
				}
			} else {
				if vm.IsThreadAlive(pending.ThreadIdx) {
					s.emitTrace(TraceEvent{Tick: tick, Unit: u.Handle, Slot: idx, WeaponID: weapon.ID, Event: "aim_sleeping"})
					continue
				}
				s.emitTrace(TraceEvent{Tick: tick, Unit: u.Handle, Slot: idx, WeaponID: weapon.ID, Event: "aim_abnormal_termination"})
				delete(s.pendingAims, key)
				continue
			}
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
				s.emitTrace(TraceEvent{Tick: tick, Unit: u.Handle, Slot: idx, WeaponID: weapon.ID, Event: "aim_cleared_infeasible"})
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
			s.emitTrace(TraceEvent{Tick: tick, Unit: u.Handle, Slot: idx, WeaponID: weapon.ID, Event: "aim_cleared_after_fire"})
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
		s.emitTrace(TraceEvent{Tick: tick, Unit: u.Handle, Slot: idx, WeaponID: weapon.ID, Event: "fire"})
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
	if simRNG == nil {
		simRNG = rng.Global.Sim
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
				// Stunned handling duplicated in StepWeaponsForUnit; wrapper pre-filters as well for determinism.
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
			b := buckets[player]
			for _, u := range b {
				s.StepWeaponsForUnit(u, tick, w, vis, terrain, econ, catalog, simRNG, crtRNG)
			}
		}
	}
}

func dispatchAim(u *units.Unit, slotIdx int, slot *units.Slot) bool {
	if u == nil || slot == nil {
		return false
	}
	vm := u.GetScript()
	if vm == nil {
		return true
	}
	var name string
	switch slotIdx {
	case 1:
		name = "AimSecondary"
	case 2:
		name = "AimTertiary"
	default:
		name = "AimPrimary"
	}
	args := []int32{int32(slot.DesiredYaw), int32(slot.DesiredPitch)}
	if vm.StartByName(name, args) {
		return true
	}
	return true
}

func acquireTargetForSlot(u *units.Unit, slot *units.Slot, idx int, w *units.World, vis *visibility.Service, terrain *world.Terrain, simRNG *rng.Simulation, econ *economy.Service) (pool.Handle, bool) {
	if u == nil || w == nil || slot == nil || slot.Weapon == nil {
		return 0, false
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
		catBits := uint32(0)
		if cand.Def != nil {
			catBits = categoryBits(cand.Def.Category)
		}
		c := Candidate{
			Handle:         cand.Handle,
			X:              cand.X,
			Z:              cand.Z,
			Y:              cand.Y,
			Category:       catBits,
			Hostile:        true,
			OwnSide:        ownSide,
			Cloaked:        cloaked,
			Underwater:     underwater,
			UnderwaterSeen: underwaterSeen,
			AirTarget:      cand.Def != nil && cand.Def.CanFly,
		}
		candidates = append(candidates, c)
	}
	acq := Acquisition{
		ShooterX:    u.X,
		ShooterZ:    u.Z,
		ShooterY:    u.Y,
		SeaLevel:    seaLevel,
		Range:       weapon.Range,
		BadMask:     badMaskForSlot(u.Def, idx),
		WaterWeapon: weapon.WaterWeapon,
		ToAir:       weapon.ToAirWeapon,
		Ballistic:   weapon.Ballistic,
		RNG:         simRNG,
	}
	if vis != nil {
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
			muzzle := muzzleWorldPos(u, slot.MuzzlePiece)
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

func muzzleWorldPos(u *units.Unit, piece int32) Vec3 {
	if u == nil {
		return Vec3{}
	}
	vm := u.GetScript()
	if vm != nil && piece >= 0 && int(piece) < len(vm.Pieces) {
		tr := vm.Pieces[piece].Trans
		return Vec3{X: u.X.Add(tr[0]), Y: u.Y.Add(tr[1]), Z: u.Z.Add(tr[2])}
	}
	return Vec3{X: u.X, Y: u.Y, Z: u.Z}
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
	origin := muzzleWorldPos(u, slot.MuzzlePiece)
	targetWorld := func(h pool.Handle) (Vec3, bool) {
		if tu := w.Unit(h); tu != nil {
			return Vec3{X: tu.X, Y: tu.Y, Z: tu.Z}, true
		}
		return Vec3{}, false
	}
	muzzleWorld := func(piece int32) (Vec3, bool) {
		pos := muzzleWorldPos(u, piece)
		return pos, true
	}
	muzzlePieceFn := func(slotIdx int) int32 {
		if slot.MuzzlePiece >= 0 {
			return slot.MuzzlePiece
		}
		return -1
	}
	scriptAdapter := &fireScriptAdapter{unit: u, slotIdx: idx}
	ports := FirePorts{
		ShooterSide: uint8(u.Owner),
		Origin:      origin,
		MuzzlePiece: muzzlePieceFn,
		MuzzleWorld: muzzleWorld,
		TargetWorld: targetWorld,
		Gravity:     gravity,
		Script:      scriptAdapter,
		Events:      nil,
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
	unit    *units.Unit
	slotIdx int
}

func (a *fireScriptAdapter) FireWeapon(slotIdx int) {
	if a == nil || a.unit == nil {
		return
	}
	vm := a.unit.GetScript()
	if vm == nil {
		return
	}
	var name string
	switch slotIdx {
	case 1:
		name = "FireSecondary"
	case 2:
		name = "FireTertiary"
	default:
		name = "FirePrimary"
	}
	_ = vm.StartByName(name, nil)
}

func (a *fireScriptAdapter) RockUnit(slotIdx int) {
	if a == nil || a.unit == nil {
		return
	}
	vm := a.unit.GetScript()
	if vm == nil {
		return
	}
	u := a.unit
	slot := u.SlotAt(slotIdx)
	if slot == nil {
		return
	}
	rel := int16(slot.DesiredYaw - u.Move.Heading)
	x, y := cob.RockUnitArgs(rel)
	_ = vm.StartByName("RockUnit", []int32{x, y})
}

func (s *Service) TickProjectiles(tick uint32, w *units.World, terrain *world.Terrain, featSvc *features.Service, vis *visibility.Service, econ *economy.Service, catalog *content.Catalog, simRNG *rng.Simulation, crtRNG *rng.CRT) {
	if s == nil {
		return
	}
	if simRNG == nil {
		simRNG = rng.Global.Sim
	}
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
			// Fallback for fixtures without compiled index: build deterministically smallest wins (I1)
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
				if _, exists := weaponByID[wd.ID]; !exists {
					weaponByID[wd.ID] = wd
				}
			}
		}
	}
	s.AdvanceBursts(tick, simRNG, weaponByID, muzzleForBurst)
	var wind Vec3
	var gravity numeric.Fixed
	var seaLevel numeric.Fixed
	if terrain != nil {
		gravity = terrain.Gravity
		seaLevel = terrain.SeaLevelWorld()
		wind = Vec3{}
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
			res = AdvanceBallistic(p, weapon, tick, wind, gravity)
		case MotionDropped:
			res = AdvanceDropped(p, weapon, tick, wind, gravity)
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
			handleProjectileImpact(s, h, p, weapon, w, terrain, featSvc, econ, catalog, tick, wind, simRNG, false)
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
			handleProjectileImpact(s, h, p, weapon, w, terrain, featSvc, econ, catalog, tick, wind, simRNG, isWater)
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
		if FeatureCacheSuppressed((*[2]int32)(&[2]int32{p.CacheCellX, p.CacheCellZ}), int32(cx), int32(cz)) {
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
		bestDist2 := int64(1 << 62)
		var best pool.Handle
		for _, u := range w.Iter() {
			if u == nil || !u.Alive || u.Dying {
				continue
			}
			if u.Handle == p.Shooter {
				continue
			}
			lower := u.Y.Raw() - 16*65536
			upper := u.Y.Raw() + 16*65536
			py := p.Pos.Y.Raw()
			if py >= upper {
				continue
			}
			_ = lower
			dx := p.Pos.X.Int() - u.X.Int()
			dz := p.Pos.Z.Int() - u.Z.Int()
			dist2 := int64(dx)*int64(dx) + int64(dz)*int64(dz)
			const hitRadius = 24
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
	var sink *noopSink
	DispatchPresentation(weapon, hasDirectTarget, isWaterTerrain, p.Pos, sink)
	applyProjectileDamage(p, weapon, w, terrain, featSvc, econ, catalog, tick, simRNG, hasDirectTarget, isWaterTerrain, h)
	_ = wind
}

type noopSink struct{}

func (n *noopSink) Shake(magnitude, duration int32)             {}
func (n *noopSink) PlayHitSound(sound string)                   {}
func (n *noopSink) PlayWaterSound(sound string)                 {}
func (n *noopSink) EmitEndSmoke(pos Vec3)                       {}
func (n *noopSink) EmitExplosion(gaf, art string, isWater bool) {}

func applyProjectileDamage(p *Projectile, weapon *content.WeaponDef, w *units.World, terrain *world.Terrain, featSvc *features.Service, econ *economy.Service, catalog *content.Catalog, tick uint32, simRNG *rng.Simulation, hasDirectTarget bool, isWaterTerrain bool, handle pool.Handle) {
	if w == nil || weapon == nil {
		return
	}
	if hasDirectTarget && weapon.AreaOfEffect <= 16 && p.TargetUnit != 0 {
		victim := w.Unit(p.TargetUnit)
		if victim != nil {
			applyDamageToUnit(victim, p, weapon, 1.0, 0, w, tick)
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
				applyDamageToUnit(victim, p, weapon, 1.0, 0, w, tick)
			}
		}
		return
	}
	var mapW, mapH int32
	if terrain != nil {
		mapW = terrain.CellW
		mapH = terrain.CellH
	}
	impact := p.Pos
	EnumerateArea(impact, radius, mapW, mapH, func(cx, cz int32) {
		if w != nil {
			for _, u := range w.Iter() {
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
				dist := DistanceToBox(impact, uv)
				if dist >= radius {
					continue
				}
				var falloff float32 = 1
				if dist != 0 {
					falloff = Falloff(float32(dist), float32(radius), float32(weapon.EdgeEffectiveness))
				}
				applyDamageToUnit(u, p, weapon, falloff, dist, w, tick)
			}
		}
		_ = featSvc
	})
	if terrain == nil || mapW == 0 {
		for _, u := range w.Iter() {
			if u == nil || !u.Alive || u.Dying {
				continue
			}
			dx := impact.X.Int() - u.X.Int()
			dz := impact.Z.Int() - u.Z.Int()
			dist2 := int64(dx)*int64(dx) + int64(dz)*int64(dz)
			if dist2 >= int64(radius)*int64(radius) {
				continue
			}
			applyDamageToUnit(u, p, weapon, 1.0, 0, w, tick)
		}
	}
}

func applyDamageToUnit(victim *units.Unit, p *Projectile, weapon *content.WeaponDef, falloff float32, distance int32, w *units.World, tick uint32) {
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
	} else {
		dir := uint8(p.Yaw.Raw() >> 8)
		hitX, hitY := HitByWeaponArgs(dir)
		takeArg := cob.HealthPercent(victim.Health, victim.MaxHealth)
		if vm := victim.GetScript(); vm != nil {
			_ = vm.StartByName("HitByWeapon", []int32{hitX, hitY})
			_ = vm.StartByName("TakeDamage", []int32{takeArg})
		}
	}
	_ = distance
}
