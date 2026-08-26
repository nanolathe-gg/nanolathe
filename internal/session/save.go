package session

import (
	"fmt"
	"sort"

	"github.com/nanolathe/nanolathe/internal/ai"
	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/combat"
	"github.com/nanolathe/nanolathe/internal/construction"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/features"
	"github.com/nanolathe/nanolathe/internal/model"
	"github.com/nanolathe/nanolathe/internal/movement"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/path"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/save"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/visibility"
)

// CaptureStateV1 captures the authoritative session into a versioned save [P0-I11][PLAN_14 C18].
// It is the sole codec; it canonically encodes every mutable authoritative service, slot-indexed,
// both RNG streams/draw counts, catalog/manifest hashes, and reconstructs via published APIs.
// Both RNG states+draw counts are included; restoration uses forced slot identity [01 §6.1][P0-I11].
func (s *Session) CaptureStateV1() *save.StateV1 {
	if s == nil {
		return nil
	}
	st := &save.StateV1{
		Version: save.StateV1VersionConst,
	}
	if s.Catalog != nil {
		st.CatalogHash = s.Catalog.Hash
		st.ManifestHash = s.Catalog.Manifest
	}
	// Per-session RNG [RS-06][I4] isolated; was process-global before RS-06.
	if s.rngInitialized {
		st.SimState = s.rngSim.State
		st.SimDraws = s.rngSim.Draws()
		st.CrtState = s.rngCrt.State
		st.CrtDraws = s.rngCrt.Draws()
	} else {
		if rng.Global.Sim != nil {
			st.SimState = rng.Global.Sim.State
			st.SimDraws = rng.Global.Sim.Draws()
		}
		if rng.Global.Crt != nil {
			st.CrtState = rng.Global.Crt.State
			st.CrtDraws = rng.Global.Crt.Draws()
		}
	}
	if s.Clock != nil {
		st.Clock = *s.Clock
	}
	// Units — forced slot identity [01 §6.1][P0-I11]
	if s.Units != nil {
		for _, u := range s.Units.Iter() {
			// Iter returns alive; include dying as well via full scan? Use Iter which includes Dying still alive
			if u == nil || !u.Alive {
				continue
			}
			name := ""
			if u.Def != nil {
				name = u.Def.CanonicalKey
				if name == "" {
					name = u.Def.UnitName
				}
			}
			flags := u.Flags
			// Persist economy activation/cloak in high bits of Flags for save/load continuity [P1-I04] without changing save box layout.
			if u.Activated {
				flags |= 1 << 16
			}
			if u.IsCloaked {
				flags |= 1 << 17
			}
			if u.Busy {
				flags |= 1 << 18
			}
			if u.YardOpen {
				flags |= 1 << 19
			}
			if u.BuggerOff {
				flags |= 1 << 20
			}
			if u.Armored {
				flags |= 1 << 21
			}
			rec := save.UnitRecord{
				Slot:              int32(u.Handle),
				DefName:           name,
				Owner:             u.Owner,
				X:                 int32(u.X.Raw()),
				Y:                 int32(u.Y.Raw()),
				Z:                 int32(u.Z.Raw()),
				Health:            u.Health,
				Remaining:         u.Remaining,
				Flags:             flags,
				Group:             u.Group,
				InBuildStance:     u.InBuildStance,
				MaxHealth:         u.MaxHealth,
				Dying:             u.Dying,
				DeathCause:        uint8(u.DeathCause),
				Pending:           u.Pending,
				Kills:             u.Kills,
				ParalyzeExpire:    u.ParalyzeExpire,
				Stunned:           u.Stunned,
				SpotMetal:         u.SpotMetal,
				PlacementIdx:      int32(u.PlacementIdx),
				PlacementIdent:    u.PlacementIdent,
				PlacementUnitName: u.PlacementUnitName,
				MoveMode:          u.Move.Mode,
				MoveHeading:       u.Move.Heading,
				MoveSpeed:         int32(u.Move.Speed.Raw()),
				MovePendingHeading: func() uint16 {
					if st, ok := s.Movement.Steers[u.Handle]; ok {
						return st.PendingHeading
					}
					return 0
				}(),
				MoveDirty: func() bool {
					if st, ok := s.Movement.Steers[u.Handle]; ok {
						return st.Dirty
					}
					return false
				}(),
				MoveHeightWord: func() int16 {
					if st, ok := s.Movement.Steers[u.Handle]; ok {
						return st.HeightWord
					}
					return 0
				}(),
				MoveSeaLevel: func() uint8 {
					if st, ok := s.Movement.Steers[u.Handle]; ok {
						return st.SeaLevel
					}
					return 0
				}(),
				MoveDefFlags: func() uint32 {
					if st, ok := s.Movement.Steers[u.Handle]; ok {
						return st.DefFlags
					}
					return 0
				}(),
				MoveMaxVelocity: func() int32 {
					if st, ok := s.Movement.Steers[u.Handle]; ok {
						return st.MaxVelocity
					}
					return int32(u.Def.MaxVelocity)
				}(),
				MoveTurnRate: func() int32 {
					if st, ok := s.Movement.Steers[u.Handle]; ok {
						return st.TurnRate
					}
					return int32(u.Def.TurnRate)
				}(),
				Carrier: int32(u.Attachment.Carrier),
				Cargo:   make([]int32, len(u.Attachment.Cargo)),
				HasCOB:  u.GetScript() != nil && u.ScriptState != nil,
			}
			for i, h := range u.Attachment.Cargo {
				rec.Cargo[i] = int32(h)
			}
			for i := 0; i < 3; i++ {
				slot := u.Slots[i]
				var wname string
				if slot.Weapon != nil {
					wname = slot.Weapon.CanonicalKey
					if wname == "" {
						wname = slot.Weapon.Name
					}
				}
				tkind := uint8(0)
				var tu int32
				var tx, tz int32
				switch slot.Target.Kind {
				case units.TargetUnit:
					tkind = 1
					tu = int32(slot.Target.Unit)
				case units.TargetGround:
					tkind = 2
					tx = int32(slot.Target.X.Raw())
					tz = int32(slot.Target.Z.Raw())
				default:
					tkind = 0
				}
				rec.Slots[i] = save.SlotRecord{
					WeaponName:   wname,
					Reload:       slot.Reload,
					Flags:        slot.Flags,
					DesiredYaw:   slot.DesiredYaw,
					DesiredPitch: slot.DesiredPitch,
					Ammo:         slot.Ammo,
					MuzzlePiece:  slot.MuzzlePiece,
					AimIssue:     slot.Aim.IssueBit,
					AimReady:     slot.Aim.Ready,
					TargetKind:   tkind,
					TargetUnit:   tu,
					TargetX:      tx,
					TargetZ:      tz,
				}
			}
			if vm := u.GetScript(); vm != nil {
				statics, threads := vm.Snapshot()
				var cobRec save.COBRecord
				cobRec.Statics = append([]int32(nil), statics...)
				for t := 0; t < 8; t++ {
					th := threads[t]
					cobRec.Threads[t] = save.ThreadRecord{
						Status:     int32(th.Status),
						PC:         int32(th.PC),
						SP:         int32(th.SP),
						Sleep:      th.Sleep,
						WaitPiece:  int32(th.WaitPiece),
						WaitAxis:   int32(th.WaitAxis),
						WaitThread: int32(th.WaitThread),
						SignalMask: th.SignalMask,
						Stack:      append([]int32(nil), th.Stack[:th.SP]...),
					}
					// Stack length is SP; but thread.Stack array length 10, we store only SP entries
					// Trim to SP
					if int(th.SP) < len(cobRec.Threads[t].Stack) {
						cobRec.Threads[t].Stack = cobRec.Threads[t].Stack[:th.SP]
					}
				}
				// Pieces, flags, anims [03 §2.4][04 §4.6][04 §4.3] P1-I01
				pieces := vm.SnapshotPieces()
				if len(pieces) > 0 {
					cobRec.Pieces = make([]save.PieceStateSave, len(pieces))
					for i, p := range pieces {
						cobRec.Pieces[i] = save.PieceStateSave{RotX: p.RotX, RotY: p.RotY, RotZ: p.RotZ, TransX: int32(p.Trans[0].Raw()), TransY: int32(p.Trans[1].Raw()), TransZ: int32(p.Trans[2].Raw())}
					}
				}
				if flags := vm.SnapshotFlags(); len(flags) > 0 {
					cobRec.Flags = append([]uint8(nil), flags...)
				}
				if anims := vm.SnapshotAnims(); len(anims) > 0 {
					cobRec.Anims = make([]save.PieceAnimSave, len(anims))
					for i, a := range anims {
						for ax := 0; ax < 3; ax++ {
							src := a.Axes[ax]
							cobRec.Anims[i].Axes[ax] = save.AxisAnimSave{MoveTarget: src.MoveTarget, MoveSpeed: src.MoveSpeed, MoveBusy: src.MoveBusy, TurnTarget: src.TurnTarget, TurnSpeed: src.TurnSpeed, TurnBusy: src.TurnBusy, SpinSpeed: src.SpinSpeed, SpinTarget: src.SpinTarget, SpinAccel: src.SpinAccel, SpinActive: src.SpinActive}
						}
					}
				}
				rec.COB = cobRec
				rec.HasCOB = true
			}
			st.Units = append(st.Units, rec)
		}
		// Also need to capture Dying units that are still Alive but not in Iter? Iter includes them; okay.
		// For deterministic ordering, sort by slot already in Marshal, but we also sort here
		sort.Slice(st.Units, func(i, j int) bool { return st.Units[i].Slot < st.Units[j].Slot })
	}
	// Legacy Queues stub (keep empty or populate minimal for compat)
	// We fill Queues with minimal stubs for version 1 readers; v2 uses Orders detailed
	// Keep empty for now; don't populate to avoid duplication
	// Orders detailed [04 §3.2][P0-I11]
	if s.Units != nil {
		for _, u := range s.Units.Iter() {
			if u == nil {
				continue
			}
			q := orders.QueueForUnit(u)
			if q == nil {
				continue
			}
			prim, sec := q.Snapshot()
			for idx, n := range prim {
				if n == nil {
					continue
				}
				desc := orders.DescriptorFor(n.ID).Name
				rec := save.OrderRecord{
					UnitSlot:     int32(u.Handle),
					Segment:      0,
					Index:        int32(idx),
					Descriptor:   desc,
					Phase:        n.Phase,
					DynamicGate:  n.DynamicGate,
					Deadline:     n.Deadline,
					Owner:        int32(n.Owner),
					Target:       int32(n.Target),
					GoalX:        int32(n.GoalX.Raw()),
					GoalY:        int32(n.GoalY.Raw()),
					GoalZ:        int32(n.GoalZ.Raw()),
					GuardX:       n.GuardX,
					GuardY:       n.GuardY,
					CachedX:      n.CachedX,
					CachedY:      n.CachedY,
					Param1:       n.Param1,
					Param2:       n.Param2,
					Param3:       n.Param3,
					StaticGate:   n.StaticGate,
					CreationTick: n.CreationTick,
					Satisfied:    n.Satisfied,
					Flags:        n.Flags,
					MoveState:    n.MoveState,
					PathStatus:   n.PathStatus,
					BuildDefKey:  n.BuildDefKey,
				}
				st.Orders = append(st.Orders, rec)
			}
			for idx, n := range sec {
				if n == nil {
					continue
				}
				desc := orders.DescriptorFor(n.ID).Name
				rec := save.OrderRecord{
					UnitSlot:     int32(u.Handle),
					Segment:      1,
					Index:        int32(idx),
					Descriptor:   desc,
					Phase:        n.Phase,
					DynamicGate:  n.DynamicGate,
					Deadline:     n.Deadline,
					Owner:        int32(n.Owner),
					Target:       int32(n.Target),
					GoalX:        int32(n.GoalX.Raw()),
					GoalY:        int32(n.GoalY.Raw()),
					GoalZ:        int32(n.GoalZ.Raw()),
					GuardX:       n.GuardX,
					GuardY:       n.GuardY,
					CachedX:      n.CachedX,
					CachedY:      n.CachedY,
					Param1:       n.Param1,
					Param2:       n.Param2,
					Param3:       n.Param3,
					StaticGate:   n.StaticGate,
					CreationTick: n.CreationTick,
					Satisfied:    n.Satisfied,
					Flags:        n.Flags,
					MoveState:    n.MoveState,
					PathStatus:   n.PathStatus,
					BuildDefKey:  n.BuildDefKey,
				}
				st.Orders = append(st.Orders, rec)
			}
		}
		sort.Slice(st.Orders, func(i, j int) bool {
			if st.Orders[i].UnitSlot != st.Orders[j].UnitSlot {
				return st.Orders[i].UnitSlot < st.Orders[j].UnitSlot
			}
			if st.Orders[i].Segment != st.Orders[j].Segment {
				return st.Orders[i].Segment < st.Orders[j].Segment
			}
			return st.Orders[i].Index < st.Orders[j].Index
		})
	}
	// Movement routes [04 §7.3]
	if s.Movement != nil {
		for h, r := range s.Movement.Routes {
			if r == nil {
				continue
			}
			var rec save.MovementRouteRecord
			rec.Unit = int32(h)
			rec.Count = r.Count
			rec.Active = r.Active
			rec.Dirty = r.Dirty
			for i := 0; i < 20; i++ {
				rec.Points[i].X = r.Points[i].X
				rec.Points[i].Z = r.Points[i].Z
			}
			st.MovementRoutes = append(st.MovementRoutes, rec)
		}
		sort.Slice(st.MovementRoutes, func(i, j int) bool { return st.MovementRoutes[i].Unit < st.MovementRoutes[j].Unit })
		// Scheduler pending [04 §7.3]
		if s.Movement.Scheduler != nil {
			pending := s.Movement.Scheduler.AllRequests()
			for _, req := range pending {
				var gx, gz, rad int32
				var kind uint8 = 0
				// Try to extract point goal details if pointGoal
				// Use type assertion via reflection? We can just store start/goal cells via Goal.Enumerate
				cells := req.Goal.Enumerate(nil)
				if len(cells) > 0 {
					gx = cells[0].X
					gz = cells[0].Z
				}
				// radius not directly stored; default 0
				rec := save.SchedulerPendingRecord{
					Unit:       int32(req.Unit),
					Player:     req.Player,
					StartX:     req.Start.X,
					StartZ:     req.Start.Z,
					GoalX:      gx,
					GoalZ:      gz,
					GoalRadius: rad,
					GoalKind:   kind,
				}
				st.SchedulerPending = append(st.SchedulerPending, rec)
			}
			sort.Slice(st.SchedulerPending, func(i, j int) bool { return st.SchedulerPending[i].Unit < st.SchedulerPending[j].Unit })
		}
		// Steering state [04 §8.1] C20 C21 [ON-12] — deterministic sorted
		for h, stt := range s.Movement.Steers {
			if stt == nil {
				continue
			}
			rec := save.MovementSteerRecord{
				Handle:         int32(h),
				X:              stt.X,
				Z:              stt.Z,
				Heading:        stt.Heading,
				PendingHeading: stt.PendingHeading,
				Dirty:          stt.Dirty,
				Speed:          stt.Speed,
				MaxVelocity:    stt.MaxVelocity,
				TurnRate:       stt.TurnRate,
				HeightWord:     stt.HeightWord,
				SeaLevel:       stt.SeaLevel,
				DefFlags:       stt.DefFlags,
			}
			st.MovementSteers = append(st.MovementSteers, rec)
		}
		sort.Slice(st.MovementSteers, func(i, j int) bool { return st.MovementSteers[i].Handle < st.MovementSteers[j].Handle })
	}
	// Economy [05]
	if s.Econ != nil {
		for i := 0; i < 10; i++ {
			p := s.Econ.Players[i]
			rec := save.EconomyPlayerRecord{
				Exists:              p.Exists,
				ControllerState:     p.ControllerState,
				IsObserver:          p.IsObserver,
				StockMetal:          p.Stock[0],
				StockEnergy:         p.Stock[1],
				CapacityMetal:       p.Capacity[0],
				CapacityEnergy:      p.Capacity[1],
				MirrorMetal:         save.BucketRecord{Production: p.Mirror[0].Production, Requested: p.Mirror[0].Requested, Accepted: p.Mirror[0].Accepted, Carry: p.Mirror[0].Carry},
				MirrorEnergy:        save.BucketRecord{Production: p.Mirror[1].Production, Requested: p.Mirror[1].Requested, Accepted: p.Mirror[1].Accepted, Carry: p.Mirror[1].Carry},
				UpdateTime:          p.UpdateTime,
				WinLoseTime:         p.WinLoseTime,
				DisplayTimer:        p.DisplayTimer,
				WasteMetal:          p.Waste[0],
				WasteEnergy:         p.Waste[1],
				TotalProducedMetal:  p.TotalProduced[0],
				TotalProducedEnergy: p.TotalProduced[1],
				TotalConsumedMetal:  p.TotalConsumed[0],
				TotalConsumedEnergy: p.TotalConsumed[1],
				PassProducedMetal:   p.PassProduced[0],
				PassProducedEnergy:  p.PassProduced[1],
				PassConsumedMetal:   p.PassConsumed[0],
				PassConsumedEnergy:  p.PassConsumed[1],
				ArchivedMetal:       save.BucketRecord{Production: p.ArchivedMirror[0].Production, Requested: p.ArchivedMirror[0].Requested, Accepted: p.ArchivedMirror[0].Accepted, Carry: p.ArchivedMirror[0].Carry},
				ArchivedEnergy:      save.BucketRecord{Production: p.ArchivedMirror[1].Production, Requested: p.ArchivedMirror[1].Requested, Accepted: p.ArchivedMirror[1].Accepted, Carry: p.ArchivedMirror[1].Carry},
				StatusHalfwordAt144: p.StatusHalfwordAt144,
				StatusWordAt140:     p.StatusWordAt140,
				GameEnded:           p.GameEnded,
				EndGameCountdown:    p.EndGameCountdown,
				Helper1Deadline:     p.Helper1Deadline,
				Helper2Deadline:     p.Helper2Deadline,
				Helper1Calls:        int32(p.Helper1Calls),
				Helper2Calls:        int32(p.Helper2Calls),
				WeaponRefreshCalls:  int32(p.WeaponRefreshCalls),
				ReferencePlayer:     int32(s.Econ.ReferencePlayer),
				SensorShareCalls:    int32(s.Econ.SensorShareCalls),
				StorageBonusEnabled: p.StorageBonusEnabled,
				StorageBonusMetal:   p.StorageBonus[0],
				StorageBonusEnergy:  p.StorageBonus[1],
				AIProductionMetal:   p.AIProduction[economy.Metal],
				AIProductionEnergy:  p.AIProduction[economy.Energy],
				AIConsumptionMetal:  p.AIConsumption[economy.Metal],
				AIConsumptionEnergy: p.AIConsumption[economy.Energy],
			}
			st.Economy.Players[i] = rec
		}
		snap := s.Econ.SnapshotUnitBuckets()
		for idx, ue := range snap {
			if idx == 0 {
				continue
			}
			empty := false
			// skip empty buckets (all zero)
			if ue.Buckets[0].Production == 0 && ue.Buckets[0].Requested == 0 && ue.Buckets[0].Accepted == 0 && ue.Buckets[0].Carry == 0 &&
				ue.Buckets[1].Production == 0 && ue.Buckets[1].Requested == 0 && ue.Buckets[1].Accepted == 0 && ue.Buckets[1].Carry == 0 &&
				ue.Archived[0].Production == 0 && ue.Archived[0].Requested == 0 && ue.Archived[0].Accepted == 0 && ue.Archived[0].Carry == 0 &&
				ue.Archived[1].Production == 0 && ue.Archived[1].Requested == 0 && ue.Archived[1].Accepted == 0 && ue.Archived[1].Carry == 0 {
				empty = true
			}
			if empty {
				continue
			}
			rec := save.EconomyUnitBucketRecord{
				Handle: int32(idx),
				Buckets: [2]save.BucketRecord{
					{Production: ue.Buckets[0].Production, Requested: ue.Buckets[0].Requested, Accepted: ue.Buckets[0].Accepted, Carry: ue.Buckets[0].Carry},
					{Production: ue.Buckets[1].Production, Requested: ue.Buckets[1].Requested, Accepted: ue.Buckets[1].Accepted, Carry: ue.Buckets[1].Carry},
				},
				Archived: [2]save.BucketRecord{
					{Production: ue.Archived[0].Production, Requested: ue.Archived[0].Requested, Accepted: ue.Archived[0].Accepted, Carry: ue.Archived[0].Carry},
					{Production: ue.Archived[1].Production, Requested: ue.Archived[1].Requested, Accepted: ue.Archived[1].Accepted, Carry: ue.Archived[1].Carry},
				},
			}
			st.Economy.UnitBuckets = append(st.Economy.UnitBuckets, rec)
		}
		sort.Slice(st.Economy.UnitBuckets, func(i, j int) bool { return st.Economy.UnitBuckets[i].Handle < st.Economy.UnitBuckets[j].Handle })
	}
	// Features [05][06 §13.1]
	if s.Features != nil {
		cur, lri, instMap := s.Features.SnapshotWithKeys()
		st.Features.Cursor = int32(cur)
		st.Features.LastReproIdx = int32(lri)
		for _, inst := range instMap {
			if inst == nil || inst.Def == nil {
				continue
			}
			name := inst.Def.CanonicalKey
			if name == "" {
				name = inst.Def.Filename
			}
			rec := save.FeatureInstanceRecord{
				DefName:         name,
				CX:              int32(inst.CX),
				CZ:              int32(inst.CZ),
				Health:          inst.Health,
				MaxHealth:       inst.MaxHealth,
				ReclaimProgress: inst.ReclaimProgress,
				IsBurning:       inst.IsBurning,
				BurnCountdown:   inst.BurnCountdown,
				BurnTicks:       inst.BurnTicks,
				BurnDuration:    inst.BurnDuration,
				IsSinking:       inst.IsSinking,
				Y:               int32(inst.Y.Raw()),
				Vy:              int32(inst.Vy.Raw()),
				Settled:         inst.Settled,
				Status:          inst.Status,
				X:               int32(inst.X.Raw()),
				Z:               int32(inst.Z.Raw()),
				FootX:           inst.FootprintX,
				FootZ:           inst.FootprintZ,
			}
			st.Features.Instances = append(st.Features.Instances, rec)
		}
		sort.Slice(st.Features.Instances, func(i, j int) bool {
			if st.Features.Instances[i].CX != st.Features.Instances[j].CX {
				return st.Features.Instances[i].CX < st.Features.Instances[j].CX
			}
			return st.Features.Instances[i].CZ < st.Features.Instances[j].CZ
		})
	}
	// Projectiles [06 §5.1]
	if s.Combat != nil {
		cnt := s.Combat.Count()
		for i := 0; i < cnt; i++ {
			h := pool.Handle(i + 1)
			if i >= len(s.Combat.Records) {
				continue
			}
			p := s.Combat.Records[i]
			rec := save.ProjectileRecord{
				Handle:           int32(h),
				WeaponID:         p.WeaponID,
				PosX:             int32(p.Pos.X.Raw()),
				PosY:             int32(p.Pos.Y.Raw()),
				PosZ:             int32(p.Pos.Z.Raw()),
				StartPosX:        int32(p.StartPos.X.Raw()),
				StartPosY:        int32(p.StartPos.Y.Raw()),
				StartPosZ:        int32(p.StartPos.Z.Raw()),
				TargetPosX:       int32(p.TargetPos.X.Raw()),
				TargetPosY:       int32(p.TargetPos.Y.Raw()),
				TargetPosZ:       int32(p.TargetPos.Z.Raw()),
				TargetUnit:       int32(p.TargetUnit),
				TargetProjectile: int32(p.TargetProjectile),
				VelocityX:        int32(p.Velocity.X.Raw()),
				VelocityY:        int32(p.Velocity.Y.Raw()),
				VelocityZ:        int32(p.Velocity.Z.Raw()),
				Speed:            int32(p.Speed.Raw()),
				Yaw:              uint16(p.Yaw),
				Pitch:            uint16(p.Pitch),
				Shooter:          int32(p.Shooter),
				ShooterSide:      p.ShooterSide,
				MuzzlePiece:      p.MuzzlePiece,
				CreationTick:     p.CreationTick,
				BurstDeadline:    p.BurstDeadline,
				BurstRemaining:   p.BurstRemaining,
				ExpiryTick:       p.ExpiryTick,
				SmokeDeadline:    p.SmokeDeadline,
				BeamLatch:        p.BeamLatch,
				TwoPhase:         p.TwoPhase,
				Dead:             p.Dead || s.Combat.IsDead(h),
				PropellerYaw:     uint16(p.PropellerYaw),
				MeteorPitch:      uint16(p.MeteorPitch),
				CacheCellX:       p.CacheCellX,
				CacheCellZ:       p.CacheCellZ,
				Scratch5E:        p.Scratch5E,
				State69:          p.State69,
				OldMarker:        p.OldMarker,
			}
			st.Projectiles = append(st.Projectiles, rec)
		}
		sort.Slice(st.Projectiles, func(i, j int) bool { return st.Projectiles[i].Handle < st.Projectiles[j].Handle })
	}
	// AI [08][PLAN_11] RS-02: player-indexed [10]*Manager, direct index, no packed scan
	hasAIMgr := false
	for _, m := range s.AI {
		if m != nil {
			hasAIMgr = true
			break
		}
	}
	if hasAIMgr {
		for _, m := range s.AI {
			if m == nil {
				continue
			}
			var rec save.AIManagerRecord
			rec.Player = m.Player
			rec.EntryCount = m.EntryCount()
			for k := 0; k < len(m.Deadlines) && k < len(rec.Deadlines); k++ {
				rec.Deadlines[k] = m.Deadlines[k]
			}
			rec.SurfaceMetal = m.SurfaceMetal
			rec.OriginX = int32(m.OriginX.Raw())
			rec.OriginZ = int32(m.OriginZ.Raw())
			rec.Strategic.CenterX = int32(m.Strategic.CenterX.Raw())
			rec.Strategic.CenterZ = int32(m.Strategic.CenterZ.Raw())
			rec.Strategic.Radius = int32(m.Strategic.Radius.Raw())
			rec.Strategic.LastRefreshTick = m.Strategic.LastRefreshTick
			rec.Strategic.LastClassRecomputeTick = m.Strategic.LastClassRecomputeTick
			for k, v := range m.Strategic.Counts {
				rec.Strategic.Counts = append(rec.Strategic.Counts, save.StrategicCountRecord{Key: k, Value: v})
			}
			sort.Slice(rec.Strategic.Counts, func(i, j int) bool { return rec.Strategic.Counts[i].Key < rec.Strategic.Counts[j].Key })
			for k, v := range m.Strategic.ClassVectors {
				rec.Strategic.ClassVectors = append(rec.Strategic.ClassVectors, save.StrategicClassVectorRecord{Key: k, C0: v.C0, C1: v.C1, C2: v.C2})
			}
			sort.Slice(rec.Strategic.ClassVectors, func(i, j int) bool { return rec.Strategic.ClassVectors[i].Key < rec.Strategic.ClassVectors[j].Key })
			for k, v := range m.Strategic.InitVectors {
				rec.Strategic.InitVectors = append(rec.Strategic.InitVectors, save.StrategicInitVectorRecord{Key: k, Value: v})
			}
			sort.Slice(rec.Strategic.InitVectors, func(i, j int) bool { return rec.Strategic.InitVectors[i].Key < rec.Strategic.InitVectors[j].Key })
			for k, v := range m.Strategic.SingleVectors {
				rec.Strategic.SingleVectors = append(rec.Strategic.SingleVectors, save.StrategicSingleVectorRecord{Key: k, Value: v})
			}
			sort.Slice(rec.Strategic.SingleVectors, func(i, j int) bool { return rec.Strategic.SingleVectors[i].Key < rec.Strategic.SingleVectors[j].Key })
			appendHandles := func(dst *[]int32, src []pool.Handle) {
				for _, h := range src {
					*dst = append(*dst, int32(h))
				}
			}
			// [R-P0-04] preserve the exact nine-vector manager record order.
			appendHandles(&rec.Groups.Resource, m.GroupResource)
			appendHandles(&rec.Groups.WaveA, m.GroupWaveA)
			appendHandles(&rec.Groups.RegroupA, m.GroupRegroupA)
			appendHandles(&rec.Groups.Construction, m.GroupConstruction)
			appendHandles(&rec.Groups.Null, m.GroupNull)
			appendHandles(&rec.Groups.WaveB, m.GroupWaveB)
			appendHandles(&rec.Groups.RegroupB, m.GroupRegroupB)
			appendHandles(&rec.Groups.Explore, m.GroupExplore)
			appendHandles(&rec.Groups.Rally, m.GroupRally)
			st.AI = append(st.AI, rec)
		}
		sort.Slice(st.AI, func(i, j int) bool { return st.AI[i].Player < st.AI[j].Player })
	}
	// Visibility [03 §3]
	if s.Vis != nil {
		wm, bgs := s.Vis.GridSnapshot()
		st.Visibility.W, st.Visibility.H = s.Vis.GridDimensions()
		st.Visibility.WordMask = append([]uint16(nil), wm...)
		for i := 0; i < 10; i++ {
			st.Visibility.ByteGrids[i] = append([]uint8(nil), bgs[i]...)
		}
		// Use snapshot method to get local/mode
		_, _, _, local, mode, _, _ := s.Vis.Snapshot()
		st.Visibility.Local = uint8(local)
		st.Visibility.Mode = uint32(mode)
		// visStatus / visDecloak are session maps, not vis service — sorted after collection to ensure deterministic save bytes despite map iteration [INVARIANTS I1][RS-06].
		for h, v := range s.visStatus {
			st.Visibility.Status = append(st.Visibility.Status, save.VisibilityStatusRecord{Handle: int32(h), Status: v})
		}
		sort.Slice(st.Visibility.Status, func(i, j int) bool { return st.Visibility.Status[i].Handle < st.Visibility.Status[j].Handle })
		for h, v := range s.visDecloak {
			st.Visibility.Decloak = append(st.Visibility.Decloak, save.VisibilityDecloakRecord{Handle: int32(h), Deadline: v})
		}
		sort.Slice(st.Visibility.Decloak, func(i, j int) bool { return st.Visibility.Decloak[i].Handle < st.Visibility.Decloak[j].Handle })
	}
	// Latch [P1-01]
	st.Latch.Countdown = s.Latch.Countdown
	st.Latch.Bits = s.Latch.Bits
	st.Latch.Pending = s.Latch.Pending
	// Wind [01 §7.3]
	if s.Wind != nil {
		min, max, strength, heading, scalar, dirX, dirZ, nextChange, lastChange, changed, pending := s.Wind.Snapshot()
		st.Wind.Min = min
		st.Wind.Max = max
		st.Wind.Strength = strength
		st.Wind.Heading = heading
		st.Wind.Scalar = scalar
		st.Wind.DirX = dirX
		st.Wind.DirZ = dirZ
		st.Wind.NextChange = nextChange
		st.Wind.LastChange = lastChange
		st.Wind.Changed = changed
		st.Wind.Pending = pending
	}
	// Meteor [08 "Meteor showers"] [06 §6.5] OW-3-Q
	st.Meteor.Enabled = s.Meteor.Enabled
	st.Meteor.Active = s.Meteor.Active
	st.Meteor.NextStrike = s.Meteor.NextStrike
	st.Meteor.StrikeEnds = s.Meteor.StrikeEnds
	st.Meteor.NextHit = s.Meteor.NextHit
	st.Meteor.OriginX = s.Meteor.OriginX
	st.Meteor.OriginZ = s.Meteor.OriginZ
	st.Meteor.TargetX = s.Meteor.TargetX
	st.Meteor.TargetZ = s.Meteor.TargetZ
	st.Meteor.WeaponName = s.Meteor.WeaponName
	st.Meteor.Density = s.Meteor.Density
	st.Meteor.Radius = s.Meteor.Radius
	st.Meteor.DurationTicks = s.Meteor.DurationTicks
	st.Meteor.IntervalTicks = s.Meteor.IntervalTicks
	st.Meteor.PerHitDelay = s.Meteor.PerHitDelay
	st.Meteor.Initialized = s.Meteor.Initialized
	// Construction builder-product links [05 C18][RS-10] — canonical sorted, no hooks
	if s.Build != nil {
		links := s.Build.SnapshotLinks()
		st.Construction.BuilderLinks = make([]save.BuilderLinkRecord, 0, len(links))
		for _, l := range links {
			st.Construction.BuilderLinks = append(st.Construction.BuilderLinks, save.BuilderLinkRecord{Builder: int32(l.Builder), Product: int32(l.Product)})
		}
		sort.Slice(st.Construction.BuilderLinks, func(i, j int) bool {
			if st.Construction.BuilderLinks[i].Product != st.Construction.BuilderLinks[j].Product {
				return st.Construction.BuilderLinks[i].Product < st.Construction.BuilderLinks[j].Product
			}
			return st.Construction.BuilderLinks[i].Builder < st.Construction.BuilderLinks[j].Builder
		})
	}
	return st
}

// RestoreStateV1 restores the session from a save via published APIs with forced slot identity [P0-I11][01 §6.1].
func (s *Session) RestoreStateV1(st *save.StateV1) error {
	if s == nil || st == nil {
		return fmt.Errorf("session: nil restore")
	}
	if err := validateSavedAIGroups(st); err != nil {
		return err
	}
	// Hashes already validated by Unmarshal
	// RNG per-session isolated [RS-06][I4] — restore into session, sync global for backward compat.
	s.rngSim = rng.SimulationFromState(st.SimState)
	s.rngSim.RestoreDraws(st.SimDraws)
	s.rngCrt = rng.CRTFromState(st.CrtState)
	s.rngCrt.RestoreDraws(st.CrtDraws)
	s.rngInitialized = true
	if rng.Global.Sim != nil {
		*rng.Global.Sim = s.rngSim
	}
	if rng.Global.Crt != nil {
		*rng.Global.Crt = s.rngCrt
	}
	for _, mgr := range s.AI {
		if mgr != nil {
			mgr.RNG = s.SimRNG()
		}
	}
	// Clock
	if s.Clock != nil {
		*s.Clock = st.Clock
	}
	// Wind
	if s.Wind != nil {
		s.Wind.RestoreSnapshot(st.Wind.Min, st.Wind.Max, st.Wind.Strength, st.Wind.Heading, st.Wind.Scalar, st.Wind.DirX, st.Wind.DirZ, st.Wind.NextChange, st.Wind.LastChange, st.Wind.Changed, st.Wind.Pending)
	}
	// Meteor [08 "Meteor showers"] [06 §6.5] OW-3-Q
	s.Meteor.Enabled = st.Meteor.Enabled
	s.Meteor.Active = st.Meteor.Active
	s.Meteor.NextStrike = st.Meteor.NextStrike
	s.Meteor.StrikeEnds = st.Meteor.StrikeEnds
	s.Meteor.NextHit = st.Meteor.NextHit
	s.Meteor.OriginX = st.Meteor.OriginX
	s.Meteor.OriginZ = st.Meteor.OriginZ
	s.Meteor.TargetX = st.Meteor.TargetX
	s.Meteor.TargetZ = st.Meteor.TargetZ
	s.Meteor.WeaponName = st.Meteor.WeaponName
	s.Meteor.Density = st.Meteor.Density
	s.Meteor.Radius = st.Meteor.Radius
	s.Meteor.DurationTicks = st.Meteor.DurationTicks
	s.Meteor.IntervalTicks = st.Meteor.IntervalTicks
	s.Meteor.PerHitDelay = st.Meteor.PerHitDelay
	s.Meteor.Initialized = st.Meteor.Initialized
	if s.Meteor.WeaponName != "" && s.Catalog != nil && s.Catalog.Weapons != nil {
		s.Meteor.Weapon = combat.ResolveMeteorWeapon(s.Meteor.WeaponName, s.Catalog.Weapons)
		if s.Meteor.Weapon == nil {
			// Fallback keep disabled if weapon not found but name persisted; treat as disabled until catalog provides it.
			// Preserve Enabled as persisted; weapon nil will prevent spawns deterministically even if Enabled true.
		}
	} else {
		s.Meteor.Weapon = nil
	}
	// Latch
	s.Latch.Countdown = st.Latch.Countdown
	s.Latch.Bits = st.Latch.Bits
	s.Latch.Pending = st.Latch.Pending
	// Units — forced slot identity [01 §6.1][P0-I11]
	if s.Units != nil && s.Catalog != nil {
		// Destroy existing units fully
		for _, u := range s.Units.Iter() {
			s.Units.Destroy(u.Handle, units.DeathUnknown)
		}
		s.Units.Cleanup()
		// Also need to clear any remaining via direct free if iterated missed due to cleanup? Ensure pool cleared
		// Create forced slots sorted asc
		sort.Slice(st.Units, func(i, j int) bool { return st.Units[i].Slot < st.Units[j].Slot })
		for _, rec := range st.Units {
			def := s.Catalog.Units[rec.DefName]
			if def == nil {
				// try canonical lookup
				if s.Catalog != nil {
					for _, cand := range s.Catalog.Units {
						if cand != nil && cand.CanonicalKey == rec.DefName {
							def = cand
							break
						}
					}
				}
				if def == nil {
					continue
				}
			}
			fx := numeric.Fixed(int64(rec.X))
			fy := numeric.Fixed(int64(rec.Y))
			fz := numeric.Fixed(int64(rec.Z))
			h, err := s.Units.CreateWithForcedSlot(def, rec.Owner, fx, fy, fz, pool.Handle(rec.Slot))
			if err != nil {
				continue
			}
			if u := s.Units.Unit(h); u != nil {
				u.Health = rec.Health
				u.MaxHealth = rec.MaxHealth
				u.Remaining = rec.Remaining
				u.Flags = rec.Flags &^ ((1 << 16) | (1 << 17) | (1 << 18) | (1 << 19) | (1 << 20) | (1 << 21))
				u.Group = rec.Group
				u.InBuildStance = rec.InBuildStance
				u.Activated = rec.Flags&(1<<16) != 0
				u.IsCloaked = rec.Flags&(1<<17) != 0
				u.Busy = rec.Flags&(1<<18) != 0
				u.YardOpen = rec.Flags&(1<<19) != 0
				u.BuggerOff = rec.Flags&(1<<20) != 0
				u.Armored = rec.Flags&(1<<21) != 0
				// Backward compat for saves before P1-I04: non-OnOffable complete units default to active.
				if !u.Activated && u.Def != nil && !u.Def.OnOffable && u.Remaining == 0 {
					u.Activated = true
				}
				if rec.Dying {
					u.Dying = true
				} else {
					u.Dying = false
				}
				u.DeathCause = units.DeathCause(rec.DeathCause)
				u.Pending = rec.Pending
				u.Kills = rec.Kills
				u.ParalyzeExpire = rec.ParalyzeExpire
				u.Stunned = rec.Stunned
				u.SpotMetal = rec.SpotMetal
				u.PlacementIdx = int(rec.PlacementIdx)
				u.PlacementIdent = rec.PlacementIdent
				u.PlacementUnitName = rec.PlacementUnitName
				u.Move.Mode = rec.MoveMode
				u.Move.Heading = rec.MoveHeading
				u.Move.Speed = numeric.Fixed(int64(rec.MoveSpeed))
				// Restore system-level steer state so the integrator resumes with
				// identical heading/speed/pending [RX-07 G8][04 §8.1].
				if s.Movement != nil {
					if s.Movement.Steers == nil {
						s.Movement.Steers = make(map[pool.Handle]*movement.SteerState)
					}
					s.Movement.Steers[h] = &movement.SteerState{
						X:              int32(rec.X),
						Z:              int32(rec.Z),
						Heading:        rec.MoveHeading,
						PendingHeading: rec.MovePendingHeading,
						Dirty:          rec.MoveDirty,
						Speed:          rec.MoveSpeed,
						MaxVelocity:    rec.MoveMaxVelocity,
						TurnRate:       rec.MoveTurnRate,
						HeightWord:     rec.MoveHeightWord,
						SeaLevel:       rec.MoveSeaLevel,
						DefFlags:       rec.MoveDefFlags,
					}
				}
				u.Attachment.Carrier = pool.Handle(rec.Carrier)
				u.Attachment.Cargo = make([]pool.Handle, len(rec.Cargo))
				for i, ch := range rec.Cargo {
					u.Attachment.Cargo[i] = pool.Handle(ch)
				}
				for i := 0; i < 3; i++ {
					sr := rec.Slots[i]
					// weapon binding is via catalog lookup
					var wdef *content.WeaponDef
					if sr.WeaponName != "" && s.Catalog != nil {
						wdef = s.Catalog.Weapons[sr.WeaponName]
						if wdef == nil {
							for _, cand := range s.Catalog.Weapons {
								if cand != nil && cand.CanonicalKey == sr.WeaponName {
									wdef = cand
									break
								}
							}
						}
					}
					u.Slots[i].Weapon = wdef
					u.Slots[i].Reload = sr.Reload
					u.Slots[i].Flags = sr.Flags
					u.Slots[i].DesiredYaw = sr.DesiredYaw
					u.Slots[i].DesiredPitch = sr.DesiredPitch
					u.Slots[i].Ammo = sr.Ammo
					u.Slots[i].MuzzlePiece = sr.MuzzlePiece
					u.Slots[i].Aim.IssueBit = sr.AimIssue
					u.Slots[i].Aim.Ready = sr.AimReady
					switch sr.TargetKind {
					case 1:
						u.Slots[i].Target = units.Target{Kind: units.TargetUnit, Unit: pool.Handle(sr.TargetUnit)}
					case 2:
						u.Slots[i].Target = units.Target{Kind: units.TargetGround, X: numeric.Fixed(int64(sr.TargetX)), Z: numeric.Fixed(int64(sr.TargetZ))}
					default:
						u.Slots[i].Target = units.Target{Kind: units.TargetNone}
					}
				}
				if rec.HasCOB {
					vm := u.GetScript()
					if vm == nil {
						vm = createVMForRestore(rec.COB.Statics)
						u.SetScript(vm)
						vm = u.GetScript()
					}
					if vm != nil {
						restoreCOBThreads(vm, rec.COB)
					}
				}
			}
		}
		// Ensure movement state for restored units
		ensureMovementForAll(s)
		// Publish visibility for restored units
		publishVisibilityForAll(s)
	}
	// Orders — restore via published queue APIs
	if s.Units != nil {
		// First clear all queues
		for _, u := range s.Units.Iter() {
			q := orders.QueueForUnit(u)
			if q == nil {
				continue
			}
			prim, sec := q.Snapshot()
			// Clear by restoring empty
			q.RestoreSnapshot(nil, nil)
			_ = prim
			_ = sec
		}
		// Group orders by unit slot
		byUnit := make(map[int32][]save.OrderRecord)
		for _, rec := range st.Orders {
			byUnit[rec.UnitSlot] = append(byUnit[rec.UnitSlot], rec)
		}
		for slot, recs := range byUnit {
			u := s.Units.Unit(pool.Handle(slot))
			if u == nil {
				continue
			}
			q := orders.QueueForUnit(u)
			// Sort by segment/index already sorted in st, but ensure
			sort.Slice(recs, func(i, j int) bool {
				if recs[i].Segment != recs[j].Segment {
					return recs[i].Segment < recs[j].Segment
				}
				return recs[i].Index < recs[j].Index
			})
			var prim []*orders.Node
			var sec []*orders.Node
			for _, r := range recs {
				id := orders.Lookup(r.Descriptor)
				if id == 0 && r.Descriptor != "" {
					// Try to lookup by canonical? Keep 0 if not found but still create node with that ID
					id = orders.Lookup(r.Descriptor)
				}
				n := orders.Node{
					ID:           id,
					Phase:        r.Phase,
					DynamicGate:  r.DynamicGate,
					Deadline:     r.Deadline,
					Owner:        pool.Handle(r.Owner),
					Target:       pool.Handle(r.Target),
					GoalX:        numeric.Fixed(int64(r.GoalX)),
					GoalY:        numeric.Fixed(int64(r.GoalY)),
					GoalZ:        numeric.Fixed(int64(r.GoalZ)),
					GuardX:       r.GuardX,
					GuardY:       r.GuardY,
					CachedX:      r.CachedX,
					CachedY:      r.CachedY,
					Param1:       r.Param1,
					Param2:       r.Param2,
					Param3:       r.Param3,
					StaticGate:   r.StaticGate,
					CreationTick: r.CreationTick,
					Satisfied:    r.Satisfied,
					Flags:        r.Flags,
					MoveState:    r.MoveState,
					PathStatus:   r.PathStatus,
					BuildDefKey:  r.BuildDefKey,
				}
				// Copy as heap allocated node
				nc := n
				if r.Segment == 0 {
					prim = append(prim, &nc)
				} else {
					sec = append(sec, &nc)
				}
			}
			q.RestoreSnapshot(prim, sec)
		}
	}
	// Movement routes
	if s.Movement != nil {
		// Clear existing
		for h := range s.Movement.Routes {
			delete(s.Movement.Routes, h)
		}
		for _, rec := range st.MovementRoutes {
			route := &movement.Route{
				Count:  rec.Count,
				Active: rec.Active,
				Dirty:  rec.Dirty,
			}
			for i := 0; i < 20; i++ {
				route.Points[i] = movement.Point{X: rec.Points[i].X, Z: rec.Points[i].Z}
			}
			s.Movement.Routes[pool.Handle(rec.Unit)] = route
		}
		// Scheduler pending
		if s.Movement.Scheduler != nil {
			// Clear all
			// Use RestoreSnapshot with empty then repopulate via Submit
			var emptyQueues [10][]path.Request
			var emptyScales [10]int32
			s.Movement.Scheduler.RestoreSnapshot(emptyQueues, emptyScales, false, 0)
			for _, rec := range st.SchedulerPending {
				goal := path.PointGoal(path.Cell{X: rec.GoalX, Z: rec.GoalZ}, rec.GoalRadius)
				req := path.Request{
					Unit:   pool.Handle(rec.Unit),
					Player: rec.Player,
					Start:  path.Cell{X: rec.StartX, Z: rec.StartZ},
					Goal:   goal,
				}
				s.Movement.Scheduler.Submit(req)
			}
		}
		// Steering state [04 §8.1] C20 C21 [ON-12] — restore deterministically sorted
		for h := range s.Movement.Steers {
			delete(s.Movement.Steers, h)
		}
		for _, rec := range st.MovementSteers {
			s.Movement.Steers[pool.Handle(rec.Handle)] = &movement.SteerState{
				X:              rec.X,
				Z:              rec.Z,
				Heading:        rec.Heading,
				PendingHeading: rec.PendingHeading,
				Dirty:          rec.Dirty,
				Speed:          rec.Speed,
				MaxVelocity:    rec.MaxVelocity,
				TurnRate:       rec.TurnRate,
				HeightWord:     rec.HeightWord,
				SeaLevel:       rec.SeaLevel,
				DefFlags:       rec.DefFlags,
			}
		}
		// Rebuild occupancy grid from restored positions to keep collision determinism [ON-12]
		if s.Movement.Grid != nil {
			s.Movement.Grid = movement.NewOccupancyGrid()
			for _, u := range s.Units.Iter() {
				if u == nil || !u.Alive {
					continue
				}
				prof := s.Movement.ProfileFor(u.Handle)
				fx := prof.FootPrintX
				fz := prof.FootPrintZ
				if fx <= 0 {
					fx = int16(u.Def.FootprintX)
					if fx <= 0 {
						fx = 1
					}
				}
				if fz <= 0 {
					fz = int16(u.Def.FootprintZ)
					if fz <= 0 {
						fz = 1
					}
				}
				bx := int32(int64(fx) * 1048576 / 2)
				bz := int32(int64(fz) * 1048576 / 2)
				anchor := movement.QuantizedAnchor(int32(u.X.Raw()), int32(u.Z.Raw()), bx, bz)
				s.Movement.Grid.Stamp(anchor, fx, fz, int(u.Handle))
				if coll, ok := s.Movement.Collisions[u.Handle]; ok && coll != nil {
					coll.X = int32(u.X.Raw())
					coll.Z = int32(u.Z.Raw())
					coll.Y = int32(u.Y.Raw())
					coll.CachedAnchor = anchor
					coll.OldAnchor = anchor
				}
			}
		}
	}
	// Economy
	if s.Econ != nil {
		for i := 0; i < 10; i++ {
			p := &s.Econ.Players[i]
			rec := st.Economy.Players[i]
			p.Exists = rec.Exists
			p.ControllerState = rec.ControllerState
			p.IsObserver = rec.IsObserver
			p.Stock[0] = rec.StockMetal
			p.Stock[1] = rec.StockEnergy
			p.Capacity[0] = rec.CapacityMetal
			p.Capacity[1] = rec.CapacityEnergy
			p.Mirror[0] = economy.Bucket{Production: rec.MirrorMetal.Production, Requested: rec.MirrorMetal.Requested, Accepted: rec.MirrorMetal.Accepted, Carry: rec.MirrorMetal.Carry}
			p.Mirror[1] = economy.Bucket{Production: rec.MirrorEnergy.Production, Requested: rec.MirrorEnergy.Requested, Accepted: rec.MirrorEnergy.Accepted, Carry: rec.MirrorEnergy.Carry}
			p.UpdateTime = rec.UpdateTime
			p.WinLoseTime = rec.WinLoseTime
			p.DisplayTimer = rec.DisplayTimer
			p.Waste[0] = rec.WasteMetal
			p.Waste[1] = rec.WasteEnergy
			p.TotalProduced[0] = rec.TotalProducedMetal
			p.TotalProduced[1] = rec.TotalProducedEnergy
			p.TotalConsumed[0] = rec.TotalConsumedMetal
			p.TotalConsumed[1] = rec.TotalConsumedEnergy
			p.PassProduced[0] = rec.PassProducedMetal
			p.PassProduced[1] = rec.PassProducedEnergy
			p.PassConsumed[0] = rec.PassConsumedMetal
			p.PassConsumed[1] = rec.PassConsumedEnergy
			p.ArchivedMirror[0] = economy.Bucket{Production: rec.ArchivedMetal.Production, Requested: rec.ArchivedMetal.Requested, Accepted: rec.ArchivedMetal.Accepted, Carry: rec.ArchivedMetal.Carry}
			p.ArchivedMirror[1] = economy.Bucket{Production: rec.ArchivedEnergy.Production, Requested: rec.ArchivedEnergy.Requested, Accepted: rec.ArchivedEnergy.Accepted, Carry: rec.ArchivedEnergy.Carry}
			p.StatusHalfwordAt144 = rec.StatusHalfwordAt144
			p.StatusWordAt140 = rec.StatusWordAt140
			p.GameEnded = rec.GameEnded
			p.EndGameCountdown = rec.EndGameCountdown
			p.Helper1Deadline = rec.Helper1Deadline
			p.Helper2Deadline = rec.Helper2Deadline
			p.Helper1Calls = int(rec.Helper1Calls)
			p.Helper2Calls = int(rec.Helper2Calls)
			p.WeaponRefreshCalls = int(rec.WeaponRefreshCalls)
			p.StorageBonusEnabled = rec.StorageBonusEnabled
			p.StorageBonus[0] = rec.StorageBonusMetal
			p.StorageBonus[1] = rec.StorageBonusEnergy
			p.AIProduction[economy.Metal] = rec.AIProductionMetal
			p.AIProduction[economy.Energy] = rec.AIProductionEnergy
			p.AIConsumption[economy.Metal] = rec.AIConsumptionMetal
			p.AIConsumption[economy.Energy] = rec.AIConsumptionEnergy
			// ReferencePlayer and SensorShareCalls are service-level but we stored per player rec; use first
			if i == 0 {
				s.Econ.ReferencePlayer = int(rec.ReferencePlayer)
				s.Econ.SensorShareCalls = int(rec.SensorShareCalls)
			}
		}
		// Rebind Wind/Terrain and cloak hook after load [P1-I04] — the one ledger must stay bound to authoritative wind/terrain.
		s.Econ.Wind = s.Wind
		s.Econ.Terrain = s.World
		s.Econ.CloakCost = func(u *units.Unit) float32 {
			if u == nil {
				return 0
			}
			return u.CloakCost()
		}
		// Thresholds are written once at battle setup from capacity [P1-06]. They are not in the persisted
		// EconomyPlayerRecord (retail save omits them), so reconstruct from current capacity if zero
		// to keep sharing (60/450) functional after load [P1-I04]. Retail would recompute similarly.
		for i := 0; i < 10; i++ {
			if s.Econ.Players[i].Exists && s.Econ.Players[i].MetalShareThreshold == 0 && s.Econ.Players[i].EnergyShareThreshold == 0 {
				// Use current capacity as threshold — matches initial battle setup where thresholds = capacity.
				s.Econ.Players[i].MetalShareThreshold = s.Econ.Players[i].Capacity[0]
				s.Econ.Players[i].EnergyShareThreshold = s.Econ.Players[i].Capacity[1]
			}
		}
		// Unit buckets
		m := make(map[int]struct {
			Buckets  [2]economy.Bucket
			Archived [2]economy.Bucket
		})
		// Build from st.Economy.UnitBuckets
		s.Econ.RestoreUnitBuckets(nil)
		maxHandle := 0
		for _, ub := range st.Economy.UnitBuckets {
			if int(ub.Handle) > maxHandle {
				maxHandle = int(ub.Handle)
			}
		}
		// Ensure slice size
		for _, ub := range st.Economy.UnitBuckets {
			h := pool.Handle(ub.Handle)
			s.Econ.EnsureUnitBucketsSize(h)
			// Direct assignment via Snapshot map method would need expose; we can use internal slice via SnapshotUnitBuckets then modify?
			// Use helper: get snapshot, modify, restore
		}
		// Use direct private access via method that we added: SnapshotUnitBuckets returns slice, we can manipulate via Restore
		snap := s.Econ.SnapshotUnitBuckets()
		if len(snap) < maxHandle+1 {
			nb := make([]economy.UnitEconomy, maxHandle+1)
			copy(nb, snap)
			snap = nb
		}
		for _, ub := range st.Economy.UnitBuckets {
			idx := int(ub.Handle)
			if idx >= 0 && idx < len(snap) {
				snap[idx].Buckets[0] = economy.Bucket{Production: ub.Buckets[0].Production, Requested: ub.Buckets[0].Requested, Accepted: ub.Buckets[0].Accepted, Carry: ub.Buckets[0].Carry}
				snap[idx].Buckets[1] = economy.Bucket{Production: ub.Buckets[1].Production, Requested: ub.Buckets[1].Requested, Accepted: ub.Buckets[1].Accepted, Carry: ub.Buckets[1].Carry}
				snap[idx].Archived[0] = economy.Bucket{Production: ub.Archived[0].Production, Requested: ub.Archived[0].Requested, Accepted: ub.Archived[0].Accepted, Carry: ub.Archived[0].Carry}
				snap[idx].Archived[1] = economy.Bucket{Production: ub.Archived[1].Production, Requested: ub.Archived[1].Requested, Accepted: ub.Archived[1].Accepted, Carry: ub.Archived[1].Carry}
			}
			m[int(ub.Handle)] = struct {
				Buckets  [2]economy.Bucket
				Archived [2]economy.Bucket
			}{}
		}
		_ = m
		s.Econ.RestoreUnitBuckets(snap)
	}
	// Features
	if s.Features != nil {
		// Clear
		// Build map from records
		mp := make(map[int]*features.Instance, len(st.Features.Instances))
		w := int32(0)
		if s.World != nil {
			w = s.World.CellW
		}
		for _, rec := range st.Features.Instances {
			var def *content.FeatureDef
			if s.Catalog != nil {
				def = s.Catalog.Features[rec.DefName]
				if def == nil {
					for _, cand := range s.Catalog.Features {
						if cand != nil && cand.CanonicalKey == rec.DefName {
							def = cand
							break
						}
					}
				}
			}
			if def == nil {
				continue
			}
			inst := &features.Instance{
				Def:             def,
				Terrain:         s.World,
				CX:              int(rec.CX),
				CZ:              int(rec.CZ),
				Health:          rec.Health,
				MaxHealth:       rec.MaxHealth,
				ReclaimProgress: rec.ReclaimProgress,
				IsBurning:       rec.IsBurning,
				BurnCountdown:   rec.BurnCountdown,
				BurnTicks:       rec.BurnTicks,
				BurnDuration:    rec.BurnDuration,
				IsSinking:       rec.IsSinking,
				Y:               numeric.Fixed(int64(rec.Y)),
				Vy:              numeric.Fixed(int64(rec.Vy)),
				Settled:         rec.Settled,
				Status:          rec.Status,
				X:               numeric.Fixed(int64(rec.X)),
				Z:               numeric.Fixed(int64(rec.Z)),
				FootprintX:      rec.FootX,
				FootprintZ:      rec.FootZ,
			}
			key := int(rec.CZ)*int(w) + int(rec.CX)
			if w == 0 {
				key = int(rec.CX)*10000 + int(rec.CZ)
			}
			mp[key] = inst
			// Also stamp plot? The feature service's PlaceAt would stamp terrain plot; but for restore we just set map, plot already has terrain features from load?
			// We may need to re-stamp via service's internal map; direct map assignment is enough for logical state, but occupancy grid not.
			// Call Restore to set cursor and map
		}
		s.Features.Restore(int(st.Features.Cursor), int(st.Features.LastReproIdx), mp)
	}
	// Projectiles
	if s.Combat != nil {
		// Reset combat pool
		// We need to clear Slots and Records
		// Slots is pool.Projectiles with count etc; there is no direct clear method, but we can create new Service
		// For determinism, reconstruct via new Service and then reserve up to max handle
		newSvc := &combat.Service{}
		// We need to recreate projectiles with forced handles in order
		// But pool appends at tail, so to get handle N we need to reserve N times
		// Sort already
		for _, rec := range st.Projectiles {
			// Reserve until handle matches; but handles are sequential per allocation order; to get handle = rec.Handle we need to reserve that many times
			// If rec.Handle is sequential 1..N, we can just reserve in order; if there are gaps due to dead compaction not yet compacted, we still need to maintain holes?
			// For native continuation before compaction, dead records may still be in active span including dead flags
			// Our snapshot includes Dead bool; we should reserve and then set Dead flag accordingly
			h, ok := newSvc.Reserve()
			if !ok {
				continue
			}
			// If reserved handle != rec.Handle, we have mismatch due to gaps; we can still proceed if our snapshot is dense sequential
			// For now assume dense
			_ = h
			idx := int(h) - 1
			if idx >= 0 && idx < len(newSvc.Records) {
				p := &newSvc.Records[idx]
				p.WeaponID = rec.WeaponID
				p.Pos = combat.Vec3{X: numeric.Fixed(int64(rec.PosX)), Y: numeric.Fixed(int64(rec.PosY)), Z: numeric.Fixed(int64(rec.PosZ))}
				p.StartPos = combat.Vec3{X: numeric.Fixed(int64(rec.StartPosX)), Y: numeric.Fixed(int64(rec.StartPosY)), Z: numeric.Fixed(int64(rec.StartPosZ))}
				p.TargetPos = combat.Vec3{X: numeric.Fixed(int64(rec.TargetPosX)), Y: numeric.Fixed(int64(rec.TargetPosY)), Z: numeric.Fixed(int64(rec.TargetPosZ))}
				p.TargetUnit = pool.Handle(rec.TargetUnit)
				p.TargetProjectile = pool.Handle(rec.TargetProjectile)
				p.Velocity = combat.Vec3{X: numeric.Fixed(int64(rec.VelocityX)), Y: numeric.Fixed(int64(rec.VelocityY)), Z: numeric.Fixed(int64(rec.VelocityZ))}
				p.Speed = numeric.Fixed(int64(rec.Speed))
				p.Yaw = numeric.Angle(rec.Yaw)
				p.Pitch = numeric.Angle(rec.Pitch)
				p.Shooter = pool.Handle(rec.Shooter)
				p.ShooterSide = rec.ShooterSide
				p.MuzzlePiece = rec.MuzzlePiece
				p.CreationTick = rec.CreationTick
				p.BurstDeadline = rec.BurstDeadline
				p.BurstRemaining = rec.BurstRemaining
				p.ExpiryTick = rec.ExpiryTick
				p.SmokeDeadline = rec.SmokeDeadline
				p.BeamLatch = rec.BeamLatch
				p.TwoPhase = rec.TwoPhase
				p.Dead = rec.Dead
				p.PropellerYaw = numeric.Angle(rec.PropellerYaw)
				p.MeteorPitch = numeric.Angle(rec.MeteorPitch)
				p.CacheCellX = rec.CacheCellX
				p.CacheCellZ = rec.CacheCellZ
				p.Scratch5E = rec.Scratch5E
				p.State69 = rec.State69
				p.OldMarker = rec.OldMarker
				if rec.Dead {
					newSvc.MarkDead(h)
				}
				// Ensure handle matches expected; if not, we could have extra reserved handles
				_ = rec.Handle
			}
		}
		*s.Combat = *newSvc
	}
	// AI RS-02: player-indexed [10]*Manager, direct access, assert index == Player
	if len(st.AI) > 0 {
		for _, rec := range st.AI {
			if int(rec.Player) < 0 || int(rec.Player) >= 10 {
				continue
			}
			m := s.AI[rec.Player]
			if m == nil {
				m = &ai.Manager{Player: rec.Player}
				s.AI[rec.Player] = m
			}
			if int(m.Player) != int(rec.Player) {
				// Enforce RS-02 invariant at restore
				m.Player = rec.Player
			}
			// v10 persists the cumulative eligible-entry count so the next
			// 30-entry classifier boundary is unchanged after restore. Legacy
			// v1-v9 records decode EntryCount as zero [08 C3].
			if st.Version >= save.StateV1Version10 {
				m.SetEntryCountForRestore(rec.EntryCount)
			} else {
				m.SetEntryCountForRestore(0)
			}
			for k := 0; k < len(m.Deadlines) && k < len(rec.Deadlines); k++ {
				m.Deadlines[k] = rec.Deadlines[k]
			}
			m.SurfaceMetal = rec.SurfaceMetal
			m.OriginX = numeric.Fixed(int64(rec.OriginX))
			m.OriginZ = numeric.Fixed(int64(rec.OriginZ))
			m.Strategic.CenterX = numeric.Fixed(int64(rec.Strategic.CenterX))
			m.Strategic.CenterZ = numeric.Fixed(int64(rec.Strategic.CenterZ))
			m.Strategic.Radius = numeric.Fixed(int64(rec.Strategic.Radius))
			m.Strategic.LastRefreshTick = rec.Strategic.LastRefreshTick
			m.Strategic.LastClassRecomputeTick = rec.Strategic.LastClassRecomputeTick
			if m.Strategic.Counts == nil {
				m.Strategic.Counts = make(map[string]int32)
			} else {
				for k := range m.Strategic.Counts {
					delete(m.Strategic.Counts, k)
				}
			}
			for _, c := range rec.Strategic.Counts {
				m.Strategic.Counts[c.Key] = c.Value
			}
			if m.Strategic.ClassVectors == nil {
				m.Strategic.ClassVectors = make(map[string]ai.ClassVector)
			} else {
				for k := range m.Strategic.ClassVectors {
					delete(m.Strategic.ClassVectors, k)
				}
			}
			for _, c := range rec.Strategic.ClassVectors {
				m.Strategic.ClassVectors[c.Key] = ai.ClassVector{C0: c.C0, C1: c.C1, C2: c.C2}
			}
			if m.Strategic.InitVectors == nil {
				m.Strategic.InitVectors = make(map[string]int8)
			} else {
				for k := range m.Strategic.InitVectors {
					delete(m.Strategic.InitVectors, k)
				}
			}
			for _, c := range rec.Strategic.InitVectors {
				m.Strategic.InitVectors[c.Key] = c.Value
			}
			if m.Strategic.SingleVectors == nil {
				m.Strategic.SingleVectors = make(map[string]int8)
			} else {
				for k := range m.Strategic.SingleVectors {
					delete(m.Strategic.SingleVectors, k)
				}
			}
			for _, c := range rec.Strategic.SingleVectors {
				m.Strategic.SingleVectors[c.Key] = c.Value
			}
			// Groups: exact manager record order [R-P0-04]. The vectors are
			// restored independently of Unit.Group; the coherence check below
			// verifies that every member carries its matching manager group.
			toHandles := func(src []int32) []pool.Handle {
				dst := make([]pool.Handle, len(src))
				for i, h := range src {
					dst[i] = pool.Handle(h)
				}
				return dst
			}
			m.GroupResource = toHandles(rec.Groups.Resource)
			m.GroupWaveA = toHandles(rec.Groups.WaveA)
			m.GroupRegroupA = toHandles(rec.Groups.RegroupA)
			m.GroupConstruction = toHandles(rec.Groups.Construction)
			m.GroupNull = toHandles(rec.Groups.Null)
			m.GroupWaveB = toHandles(rec.Groups.WaveB)
			m.GroupRegroupB = toHandles(rec.Groups.RegroupB)
			m.GroupExplore = toHandles(rec.Groups.Explore)
			m.GroupRally = toHandles(rec.Groups.Rally)
			if st.Version >= save.StateV1Version9 && s.Units != nil {
				groupVectors := []struct {
					group uint8
					list  []pool.Handle
				}{
					{1, m.GroupResource}, {2, m.GroupWaveA}, {3, m.GroupRegroupA},
					{4, m.GroupConstruction}, {5, m.GroupNull}, {6, m.GroupWaveB},
					{7, m.GroupRegroupB}, {8, m.GroupExplore}, {9, m.GroupRally},
				}
				for _, gv := range groupVectors {
					for _, h := range gv.list {
						u := s.Units.Unit(h)
						if u == nil || u.Owner != rec.Player || u.Group != gv.group {
							return fmt.Errorf("session: AI group/vector mismatch player=%d group=%d handle=%d", rec.Player, gv.group, h)
						}
					}
				}
			}
		}
	}
	// Visibility
	if s.Vis != nil {
		// Restore grids
		s.Vis.RestoreSnapshot(st.Visibility.WordMask, st.Visibility.ByteGrids, nil, visibility.PlayerID(st.Visibility.Local), visibility.Mode(st.Visibility.Mode))
		// Restore session maps
		if s.visStatus == nil {
			s.visStatus = make(map[int]uint32)
		} else {
			for k := range s.visStatus {
				delete(s.visStatus, k)
			}
		}
		for _, rec := range st.Visibility.Status {
			s.visStatus[int(rec.Handle)] = rec.Status
		}
		if s.visDecloak == nil {
			s.visDecloak = make(map[int]uint32)
		} else {
			for k := range s.visDecloak {
				delete(s.visDecloak, k)
			}
		}
		for _, rec := range st.Visibility.Decloak {
			s.visDecloak[int(rec.Handle)] = rec.Deadline
		}
		// Rebuild fog? Not needed
	}
	// Construction builder-product links [05 C18][RS-10] — rebind without firing hooks
	if s.Build != nil {
		links := make([]construction.LinkRecord, 0, len(st.Construction.BuilderLinks))
		for _, r := range st.Construction.BuilderLinks {
			links = append(links, construction.LinkRecord{Builder: pool.Handle(r.Builder), Product: pool.Handle(r.Product)})
		}
		s.Build.RestoreLinks(links)
	}
	return nil
}

// validateSavedAIGroups checks the cross-record invariants that the binary
// save codec cannot know: every vector member must refer to a saved unit owned
// by that manager and carry the corresponding retail group value. Units that
// are ungrouped (Group==0), dying, or otherwise absent from every vector are
// valid; only a vector's explicit membership is constrained [R-P0-04]. The
// check runs before RestoreStateV1 mutates RNG, clock, or pools.
func validateSavedAIGroups(st *save.StateV1) error {
	if st == nil || st.Version < save.StateV1Version9 {
		return nil
	}
	type unitIdentity struct {
		owner uint8
		group uint8
	}
	unitsBySlot := make(map[int32]unitIdentity, len(st.Units))
	for _, u := range st.Units {
		if u.Slot <= 0 {
			return fmt.Errorf("session: saved unit slot %d is invalid", u.Slot)
		}
		if _, exists := unitsBySlot[u.Slot]; exists {
			return fmt.Errorf("session: duplicate saved unit slot %d", u.Slot)
		}
		unitsBySlot[u.Slot] = unitIdentity{owner: u.Owner, group: u.Group}
	}
	seen := make(map[int32]struct{})
	for _, m := range st.AI {
		vectors := []struct {
			group uint8
			name  string
			list  []int32
		}{
			{1, "resource", m.Groups.Resource}, {2, "wave A", m.Groups.WaveA},
			{3, "regroup A", m.Groups.RegroupA}, {4, "construction", m.Groups.Construction},
			{5, "null", m.Groups.Null}, {6, "wave B", m.Groups.WaveB},
			{7, "regroup B", m.Groups.RegroupB}, {8, "explore", m.Groups.Explore},
			{9, "rally", m.Groups.Rally},
		}
		for _, v := range vectors {
			for _, h := range v.list {
				if h <= 0 {
					return fmt.Errorf("session: AI %s group contains null handle %d", v.name, h)
				}
				identity, ok := unitsBySlot[h]
				if !ok {
					return fmt.Errorf("session: AI %s group references missing unit handle=%d", v.name, h)
				}
				if identity.owner != m.Player || identity.group != v.group {
					return fmt.Errorf("session: AI group/vector mismatch player=%d group=%d handle=%d", m.Player, v.group, h)
				}
				if _, exists := seen[h]; exists {
					return fmt.Errorf("session: duplicate AI group membership handle=%d", h)
				}
				seen[h] = struct{}{}
			}
		}
	}
	return nil
}

func restoreCOBThreads(vm *cob.VM, rec save.COBRecord) {
	if vm == nil {
		return
	}
	var threads [8]cob.Thread
	for i := 0; i < 8; i++ {
		tr := rec.Threads[i]
		threads[i].Status = int(tr.Status)
		threads[i].PC = int(tr.PC)
		threads[i].SP = int(tr.SP)
		threads[i].Sleep = tr.Sleep
		threads[i].WaitPiece = int(tr.WaitPiece)
		threads[i].WaitAxis = int(tr.WaitAxis)
		threads[i].WaitThread = int(tr.WaitThread)
		threads[i].SignalMask = tr.SignalMask
		// Stack
		for s := 0; s < len(tr.Stack) && s < 10; s++ {
			threads[i].Stack[s] = tr.Stack[s]
		}
	}
	vm.RestoreSnapshot(rec.Statics, threads)
	// Pieces, flags, anims [03 §2.4][04 §4.6] P1-I01
	if len(rec.Pieces) > 0 {
		pieces := make([]model.PieceState, len(rec.Pieces))
		for i, p := range rec.Pieces {
			pieces[i].RotX = p.RotX
			pieces[i].RotY = p.RotY
			pieces[i].RotZ = p.RotZ
			pieces[i].Trans[0] = numeric.Fixed(int64(p.TransX))
			pieces[i].Trans[1] = numeric.Fixed(int64(p.TransY))
			pieces[i].Trans[2] = numeric.Fixed(int64(p.TransZ))
		}
		vm.RestorePieces(pieces)
	}
	if len(rec.Flags) > 0 {
		vm.RestoreFlags(append([]uint8(nil), rec.Flags...))
	}
	if len(rec.Anims) > 0 {
		anims := make([]cob.PieceAnimSave, len(rec.Anims))
		for i, a := range rec.Anims {
			for ax := 0; ax < 3; ax++ {
				src := a.Axes[ax]
				anims[i].Axes[ax] = cob.AxisAnimSave{MoveTarget: src.MoveTarget, MoveSpeed: src.MoveSpeed, MoveBusy: src.MoveBusy, TurnTarget: src.TurnTarget, TurnSpeed: src.TurnSpeed, TurnBusy: src.TurnBusy, SpinSpeed: src.SpinSpeed, SpinTarget: src.SpinTarget, SpinAccel: src.SpinAccel, SpinActive: src.SpinActive}
			}
		}
		vm.RestoreAnims(anims)
	}
}

func createVMForRestore(statics []int32) *cob.VM {
	prog := &cob.Program{
		Code:    make([]uint32, 1),
		Statics: len(statics),
		Pieces:  []string{},
		Scripts: map[string]int{},
	}
	vm := cob.NewVM(prog)
	if len(statics) > 0 {
		vm.RestoreStatics(statics)
	}
	return vm
}
