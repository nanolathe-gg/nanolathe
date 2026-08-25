// Package combat — integrated weapon and projectile pipeline per [06] P0-I04.
package combat

import (
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

// TickWeapons runs the integrated per-unit weapon pipeline [06 §3][06 §4][04 §5.3][GAP T15].
func (s *Service) TickWeapons(tick uint32, w *units.World, vis *visibility.Service, terrain *world.Terrain, econ *economy.Service, catalog *content.Catalog, simRNG *rng.Simulation, crtRNG *rng.CRT) {
	if s == nil || w == nil {
		return
	}
	if simRNG == nil {
		simRNG = rng.Global.Sim
	}
	weaponsByID := map[int32]*content.WeaponDef{}
	if catalog != nil {
		for _, wd := range catalog.Weapons {
			if wd != nil {
				weaponsByID[wd.ID] = wd
			}
		}
	}
	var unitList []*units.Unit
	if w.IsSliced() {
		unitList = w.IterSliced()
	} else {
		unitList = w.Iter()
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
				for idx := 0; idx < NumSlots; idx++ {
					slotPtr := u.SlotAt(idx)
					if slotPtr == nil || !slotPtr.IsPopulated() {
						continue
					}
					weaponTickSlot(u, slotPtr, idx, tick, w, vis, terrain, econ, catalog, weaponsByID, simRNG, crtRNG, s)
				}
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
				for idx := 0; idx < NumSlots; idx++ {
					slotPtr := u.SlotAt(idx)
					if slotPtr == nil || !slotPtr.IsPopulated() {
						continue
					}
					weaponTickSlot(u, slotPtr, idx, tick, w, vis, terrain, econ, catalog, weaponsByID, simRNG, crtRNG, s)
				}
			}
		}
	}
}

func weaponTickSlot(u *units.Unit, slot *units.Slot, idx int, tick uint32, w *units.World, vis *visibility.Service, terrain *world.Terrain, econ *economy.Service, catalog *content.Catalog, weaponsByID map[int32]*content.WeaponDef, simRNG *rng.Simulation, crtRNG *rng.CRT, svc *Service) {
	if slot.Target.Kind == units.TargetUnit && slot.Target.Unit != 0 {
		targetUnit := w.Unit(slot.Target.Unit)
		if targetUnit == nil || !targetUnit.Alive || targetUnit.Dying {
			clearedHeading := slot.DesiredYaw != 0
			clearedPitch := slot.DesiredPitch != 0x8000
			slot.Target = units.Target{Kind: units.TargetNone}
			slot.Aim.IssueBit = false
			slot.Aim.Ready = false
			slot.Flags &^= 0x01
			if (clearedHeading || clearedPitch) && u.GetScript() != nil {
				_ = u.GetScript().StartByName("TargetCleared", nil)
			}
			slot.Flags &^= 0x02
			return
		}
	}
	if slot.Target.Kind == units.TargetNone || slot.Flags&0x02 == 0 {
		if acquired, ok := acquireTargetForSlot(u, slot, idx, w, vis, terrain, simRNG); ok {
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
			return
		}
	}
	if slot.Target.Kind == units.TargetNone {
		return
	}
	weapon := slot.Weapon
	if weapon == nil {
		return
	}
	needLatch, needResult := aimRequirement(weapon)
	var tgtPos Vec3
	var tgtHandle pool.Handle
	if slot.Target.Kind == units.TargetUnit {
		tgtHandle = slot.Target.Unit
		if tu := w.Unit(tgtHandle); tu != nil {
			tgtPos = Vec3{X: tu.X, Y: tu.Y, Z: tu.Z}
		} else {
			return
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
	var ballisticPitch uint16
	ballisticOk := false
	if weapon.Ballistic {
		var grav numeric.Fixed
		if terrain != nil {
			grav = terrain.Gravity
		}
		vel := numeric.Fixed(int64(weapon.WeaponVelocity))
		if vel.Raw() == 0 {
			goto admission
		}
		pitch, ok := BallisticSolve(dx, dy, dz, vel, grav, weapon.MinBarrelAngle)
		if !ok {
			goto admission
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
	if needResult && !slot.Aim.Ready {
		if !slot.Aim.IssueBit {
			dispatched := dispatchAim(u, idx, slot)
			if dispatched {
				slot.Aim.StartAim()
				slot.Flags |= 0x01
			} else {
				goto admission
			}
			return
		}
		slot.Aim.CompleteAim(1)
		if !slot.Aim.Ready {
			return
		}
	}
	if needLatch && !slot.Aim.IssueBit {
		return
	}
admission:
	if slot.Reload > 0 {
		return
	}
	if !checkAdmission(u, slot, weapon, tgtPos, tgtHandle, w, vis, terrain, ballisticOk, ballisticPitch) {
		return
	}
	if weapon.Stockpile {
		if slot.Ammo <= 0 {
			return
		}
	} else if econ != nil {
		eCost := float32(weapon.EnergyPerShot)
		mCost := float32(weapon.MetalPerShot)
		if eCost != 0 || mCost != 0 {
			p := &econ.Players[u.Owner]
			if p.Stock[economy.Energy] < eCost || p.Stock[economy.Metal] < mCost {
				return
			}
		}
	}
	if !tryFireForSlot(u, slot, idx, tick, terrain, simRNG, svc, w) {
		return
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
	_ = tgtHandle
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

func acquireTargetForSlot(u *units.Unit, slot *units.Slot, idx int, w *units.World, vis *visibility.Service, terrain *world.Terrain, simRNG *rng.Simulation) (pool.Handle, bool) {
	if u == nil || w == nil || slot == nil || slot.Weapon == nil {
		return 0, false
	}
	weapon := slot.Weapon
	var candidates []Candidate
	for _, cand := range w.Iter() {
		if cand == nil || cand.Handle == u.Handle || !cand.Alive || cand.Dying {
			continue
		}
		if cand.Owner == u.Owner {
			continue
		}
		c := Candidate{
			Handle:     cand.Handle,
			X:          cand.X,
			Z:          cand.Z,
			Y:          cand.Y,
			Category:   0,
			Hostile:    true,
			OwnSide:    false,
			Cloaked:    false,
			Underwater: false,
			AirTarget:  cand.Def != nil && cand.Def.CanFly,
		}
		candidates = append(candidates, c)
	}
	var seaLevel numeric.Fixed
	if terrain != nil {
		seaLevel = terrain.SeaLevelWorld()
	}
	acq := Acquisition{
		ShooterX:    u.X,
		ShooterZ:    u.Z,
		ShooterY:    u.Y,
		SeaLevel:    seaLevel,
		Range:       weapon.Range,
		BadMask:     0,
		WaterWeapon: weapon.WaterWeapon,
		ToAir:       weapon.ToAirWeapon,
		Ballistic:   weapon.Ballistic,
		RNG:         simRNG,
	}
	if vis != nil {
		acq.Visible = func(c Candidate) bool {
			t := visibility.Target{
				Owner:  visibility.PlayerID(0),
				X:      c.X,
				Y:      c.Y,
				Z:      c.Z,
				Hidden: false,
				Status: 0,
			}
			if candUnit := w.Unit(c.Handle); candUnit != nil {
				t.Owner = visibility.PlayerID(candUnit.Owner)
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
	weaponByID := map[int32]*content.WeaponDef{}
	if catalog != nil {
		for _, wd := range catalog.Weapons {
			if wd != nil {
				weaponByID[wd.ID] = wd
			}
		}
	}
	muzzleForBurst := func(piece int16) Vec3 {
		return Vec3{}
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
		weapon := weaponByID[p.WeaponID]
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
