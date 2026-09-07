package session

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"sort"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/features"
	"github.com/nanolathe/nanolathe/internal/movement"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/path"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/units"
)

// ParityUnit is the session-owned, value-only unit diagnostic. It is not a
// presentation snapshot and never aliases a live unit.
//
// A `Guards GuardTrace` member stood here, mirroring four per-unit dedup
// arrays, and writeParityUnit hashed all 56 of their handles as `guard-build`,
// `guard-repair`, `guard-help` and `guard-fire` rows. [04 R-UNIT-06 §1]
// establishes that retail keeps "no dedup array and no latch in either guard
// handler", so WU-19-35 retired the arrays' readers and writers and this
// follow-up deleted the state itself. Both the member and its four hash rows
// are gone with it: the digest covered 56 permanently-zero handles per unit.
// Removing them changes the authoritative parity DIGEST — no golden value pins
// it, and no simulation quantity moves — while leaving every other row's
// format untouched.
type ParityUnit struct {
	Slot                                              pool.Handle
	DefinitionKey                                     string
	Owner                                             uint8
	Alive, Dying                                      bool
	DeathCause                                        units.DeathCause
	X, Y, Z                                           int64
	Health, MaxHealth                                 int32
	Remaining                                         float32
	Flags, Pending                                    uint32
	InBuildStance, Busy, YardOpen, BuggerOff, Armored bool
	Group                                             uint8
	Move                                              MoveTrace
	Attachment                                        AttachmentTrace
	SpotMetal                                         float32
	Activated, IsCloaked                              bool
	Kills                                             int32
	ParalyzeExpire                                    uint32
	Stunned                                           bool
	CurrentSample, PriorSample                        uint8
	Slots                                             [3]SlotTrace
	Orders                                            []OrderTrace
	Threads                                           [8]ThreadTrace
	Callbacks                                         []cob.LifecycleEvent
	Pieces                                            []PieceTrace
	RenderPieceFlags                                  []uint8
}

type MoveTrace struct {
	Mode                 uint8
	Heading, Pitch, Bank uint16
	Speed                int64
	PendingHeading       uint16
	PendingSpeed         int32
}

type AttachmentTrace struct {
	Carrier     pool.Handle
	AttachPiece int
	Cargo       []pool.Handle
}

type SlotTrace struct {
	WeaponKey                string
	Reload                   int32
	Flags                    uint8
	DesiredYaw, DesiredPitch uint16
	Ammo, MuzzlePiece        int32
	AimIssue, AimReady       bool
	TargetKind               uint8
	TargetUnit               pool.Handle
	TargetX, TargetZ         int64
}

type OrderTrace struct {
	ID, Target, Owner                                                  uint32
	Phase                                                              uint8
	DynamicGate                                                        uint32
	Deadline                                                           int32
	GoalX, GoalY, GoalZ                                                int64
	GuardX, GuardY, CachedX, CachedY                                   int16
	Param1, Param2, Param3, StaticGate, CreationTick, Satisfied, Flags uint32
	MoveState                                                          uint8
	PathStatus                                                         uint32
	BuildDefKey                                                        string
}

type PieceTrace struct {
	RotX, RotY, RotZ              uint16
	TransX, TransY, TransZ        int64
	DontShade, Hidden, DontShadow bool
}

type ThreadTrace struct {
	Status, PC, SP                                     int
	Stack                                              [32]int32
	Sleep, WaitPiece, WaitAxis, WaitThread, SignalMask int
}

type FeatureTrace struct {
	Key                                    string
	CX, CZ                                 int
	DamageAccumulator                      uint16
	ReclaimProgress                        int32
	IsBurning                              bool
	BurnCountdown, BurnTicks, BurnDuration int32
	RemoteSuppressed                       bool
	Y, Vy, X, Z                            int64
	IsSinking, Settled                     bool
	Status                                 uint8
	FootprintX, FootprintZ                 int32
}

// ParityCallbackEvents returns actual queue/VM lifecycle events in arrival
// order. It never derives events from pending state or thread snapshots.
func (s *Session) ParityCallbackEvents() []cob.LifecycleEvent {
	if s == nil || !s.parityTraceEnabled {
		return nil
	}
	return append([]cob.LifecycleEvent(nil), s.parityCallbacks...)
}

// ParityHandles returns selected live-unit identities in authoritative slot
// order. An explicitly empty selection selects no units; this prevents a
// capture accidentally expanding to the entire battle.
func (s *Session) ParityHandles() []ParityUnit {
	if s == nil || !s.parityTraceEnabled || s.Units == nil {
		return nil
	}
	out := make([]ParityUnit, 0)
	for _, u := range s.Units.IterSliced() {
		if u == nil || !s.paritySelected(u.Handle) {
			continue
		}
		out = append(out, s.parityUnit(u))
	}
	return out
}

func (s *Session) allParityUnits() []ParityUnit {
	if s == nil || s.Units == nil {
		return nil
	}
	out := make([]ParityUnit, 0)
	for _, u := range s.Units.IterSliced() {
		if u != nil {
			out = append(out, s.parityUnit(u))
		}
	}
	return out
}

func (s *Session) paritySelected(h pool.Handle) bool {
	if s == nil || !s.parityTraceEnabled {
		return false
	}
	for _, selected := range s.paritySelection {
		if selected == h {
			return true
		}
	}
	return false
}

// EnableParityTrace installs opt-in sinks and movement capture. No sink or
// diagnostic storage is installed on the default session path [I6].
func (s *Session) EnableParityTrace(handles []pool.Handle) {
	if s == nil {
		return
	}
	s.parityTraceEnabled = true
	if s.parityTraceLimit <= 0 {
		s.parityTraceLimit = 4096
	}
	s.paritySelection = append(s.paritySelection[:0], handles...)
	sort.Slice(s.paritySelection, func(i, j int) bool { return s.paritySelection[i] < s.paritySelection[j] })
	s.parityCallbacks = s.parityCallbacks[:0]
	s.parityTraceDropped = false
	if s.Movement != nil {
		s.Movement.EnableParityTrace()
	}
	s.wireParityTrace()
}

// SetParityTraceLimit bounds retained lifecycle events. A zero limit records
// no events but still leaves authoritative simulation untouched.
func (s *Session) SetParityTraceLimit(limit int) {
	if s == nil {
		return
	}
	if limit < 0 {
		limit = 0
	}
	s.parityTraceLimit = limit
	for len(s.parityCallbacks) > limit {
		s.parityCallbacks = s.parityCallbacks[1:]
	}
	if s.Movement != nil {
		s.Movement.SetParityTraceLimit(limit)
	}
}

// ResetParityTrace clears callback and movement diagnostic history only.
func (s *Session) ResetParityTrace() {
	if s == nil {
		return
	}
	s.parityCallbacks = s.parityCallbacks[:0]
	s.parityTraceDropped = false
	if s.Movement != nil {
		s.Movement.ResetParityTrace()
	}
}

func (s *Session) ParityTraceDropped() bool {
	return s != nil && (s.parityTraceDropped || (s.Movement != nil && s.Movement.ParityTraceDropped()))
}

func (s *Session) appendParityCallback(event cob.LifecycleEvent) {
	if s == nil || !s.parityTraceEnabled {
		return
	}
	if s.parityTraceLimit <= 0 || len(s.parityCallbacks) >= s.parityTraceLimit {
		s.parityTraceDropped = true
		if s.parityTraceLimit <= 0 {
			return
		}
		s.parityCallbacks = s.parityCallbacks[1:]
	}
	s.parityCallbacks = append(s.parityCallbacks, event)
}

func (s *Session) wireParityTrace() {
	if s == nil || !s.parityTraceEnabled || s.Units == nil {
		return
	}
	// Rewire from a clean state so changing the selected set cannot leave an
	// old closure recording an unselected handle.
	for _, u := range s.Units.IterSliced() {
		if u == nil {
			continue
		}
		if binding := u.COBBinding(); binding != nil && binding.Callbacks != nil {
			binding.Callbacks.SetLifecycleSink(nil)
		}
	}
	for _, u := range s.Units.IterSliced() {
		if u == nil || !s.paritySelected(u.Handle) {
			continue
		}
		source := uint16(u.Handle)
		if binding := u.COBBinding(); binding != nil && binding.Callbacks != nil {
			binding.Callbacks.SetLifecycleContext(s.parityTraceTick, source)
			binding.Callbacks.SetLifecycleSink(func(event cob.LifecycleEvent) {
				s.appendParityCallback(event)
			})
		}
	}
}

func (s *Session) setParityTraceTick(tick uint32) {
	if s == nil || !s.parityTraceEnabled {
		return
	}
	s.parityTraceTick = tick
	s.wireParityTrace()
}

func (s *Session) parityUnit(u *units.Unit) ParityUnit {
	pu := ParityUnit{Slot: u.Handle, Owner: u.Owner, Alive: u.Alive, Dying: u.Dying, DeathCause: u.DeathCause,
		X: u.X.Raw(), Y: u.Y.Raw(), Z: u.Z.Raw(), Health: u.Health, MaxHealth: u.MaxHealth,
		Remaining: u.Remaining, Flags: u.Flags, Pending: u.Pending, InBuildStance: u.InBuildStance,
		Busy: u.Busy, YardOpen: u.YardOpen, BuggerOff: u.BuggerOff, Armored: u.Armored,
		Group: u.Group, SpotMetal: u.SpotMetal, Activated: u.Activated,
		IsCloaked: u.IsCloaked, Kills: u.Kills, ParalyzeExpire: u.ParalyzeExpire, Stunned: u.Stunned,
		CurrentSample: u.CurrentSample, PriorSample: u.PriorSample}
	if u.Def != nil {
		pu.DefinitionKey = u.Def.CanonicalKey
		if pu.DefinitionKey == "" {
			pu.DefinitionKey = u.Def.UnitName
		}
	}
	pu.Move = MoveTrace{Mode: u.Move.Mode, Heading: u.Move.Heading, Pitch: u.Move.Pitch, Bank: u.Move.Bank,
		Speed: u.Move.Speed.Raw(), PendingHeading: u.Move.PendingHeading, PendingSpeed: u.Move.PendingSpeed}
	pu.Attachment = AttachmentTrace{Carrier: u.Attachment.Carrier, AttachPiece: u.Attachment.AttachPiece,
		Cargo: append([]pool.Handle(nil), u.Attachment.Cargo...)}
	for i := range u.Slots {
		slot := &u.Slots[i]
		st := SlotTrace{Reload: slot.Reload, Flags: slot.Flags, DesiredYaw: slot.DesiredYaw,
			DesiredPitch: slot.DesiredPitch, Ammo: slot.Ammo, MuzzlePiece: slot.MuzzlePiece,
			AimIssue: slot.Aim.IssueBit, AimReady: slot.Aim.Ready, TargetKind: uint8(slot.Target.Kind),
			TargetUnit: slot.Target.Unit, TargetX: slot.Target.X.Raw(), TargetZ: slot.Target.Z.Raw()}
		if slot.Weapon != nil {
			st.WeaponKey = slot.Weapon.CanonicalKey
		}
		pu.Slots[i] = st
	}
	if q := orders.QueueOfUnit(u); q != nil {
		for _, node := range append(append([]*orders.Node(nil), q.Primary()...), q.Secondary()...) {
			if node == nil {
				continue
			}
			pu.Orders = append(pu.Orders, OrderTrace{ID: uint32(node.ID), Target: uint32(node.Target), Owner: uint32(node.Owner),
				Phase: node.Phase, DynamicGate: node.DynamicGate, Deadline: node.Deadline, GoalX: node.GoalX.Raw(), GoalY: node.GoalY.Raw(), GoalZ: node.GoalZ.Raw(),
				GuardX: node.GuardX, GuardY: node.GuardY, CachedX: node.CachedX, CachedY: node.CachedY, Param1: node.Param1, Param2: node.Param2, Param3: node.Param3,
				StaticGate: node.StaticGate, CreationTick: node.CreationTick, Satisfied: node.Satisfied, Flags: node.Flags, MoveState: node.MoveState, PathStatus: node.PathStatus, BuildDefKey: node.BuildDefKey})
		}
	}
	if u.GetScript() != nil {
		vm := u.GetScript()
		for i, thread := range vm.Threads {
			pu.Threads[i] = ThreadTrace{Status: thread.Status, PC: thread.PC, SP: thread.SP, Stack: thread.Stack, Sleep: int(thread.Sleep), WaitPiece: thread.WaitPiece, WaitAxis: thread.WaitAxis, WaitThread: thread.WaitThread, SignalMask: int(thread.SignalMask)}
		}
		for _, p := range vm.Pieces {
			pu.Pieces = append(pu.Pieces, PieceTrace{RotX: p.RotX, RotY: p.RotY, RotZ: p.RotZ, TransX: p.Trans[0].Raw(), TransY: p.Trans[1].Raw(), TransZ: p.Trans[2].Raw(), DontShade: p.DontShade, Hidden: p.Hidden, DontShadow: p.DontShadow})
		}
	}
	pu.RenderPieceFlags = append([]uint8(nil), u.RenderPieceFlags...)
	for _, event := range s.parityCallbacks {
		if event.Source == uint16(u.Handle) {
			pu.Callbacks = append(pu.Callbacks, event)
		}
	}
	return pu
}

// ParityEconomy returns selected per-unit state and all ten player buckets in
// fixed order. Economy itself remains an independent package-local snapshot.
func (s *Session) ParityEconomy() economy.TraceSnapshot {
	if s == nil || !s.parityTraceEnabled || s.Econ == nil {
		return economy.TraceSnapshot{}
	}
	full := s.Econ.ParitySnapshot(s.Units)
	out := full
	if s.Units != nil {
		out.Units = out.Units[:0]
		for _, u := range full.Units {
			if s.paritySelected(u.Slot) {
				out.Units = append(out.Units, u)
			}
		}
	}
	return out
}

// ParityMovement returns selected movement state, including the scheduler's
// pending/result boundaries and collision history captured at EndTick.
func (s *Session) ParityMovement(tick uint32) []movement.MovementTrace {
	if s == nil || !s.parityTraceEnabled || s.Movement == nil || s.Units == nil {
		return nil
	}
	all := s.Movement.ParitySnapshot(s.Units, tick)
	out := make([]movement.MovementTrace, 0, len(all))
	for _, trace := range all {
		if s.paritySelected(trace.Slot) {
			out = append(out, trace)
		}
	}
	return out
}

// ParityAuthoritativeHash hashes complete selected unit state plus the full
// authoritative economy/player, movement, feature, callback, clock, and RNG
// surfaces. It uses ordered slices and raw float bits, and does not mutate any
// service while observing it [01 §4.4][05 "Player slot"][08 "Scheduler and random state in saves"].
func (s *Session) ParityAuthoritativeHash() (string, error) {
	if s == nil {
		return "", fmt.Errorf("nanolathe: parity hash: nil session")
	}
	h := sha256.New()
	w := func(format string, args ...interface{}) { _, _ = fmt.Fprintf(h, format, args...) }
	w("clock-present:%t|", s.Clock != nil)
	if s.Clock != nil {
		w("clock:%d:%d:%d:%08x:%t|", s.Clock.GlobalTick, s.Clock.ScaledAnchor, s.Clock.Delta, math.Float32bits(s.Clock.Carry), s.Clock.Paused)
		// SaveBox normalizes the pause and requested/active mismatch flags. Call
		// it on a value copy: observing the hash must not write those private
		// scheduler bits back into the live clock [01 §4.3][08 "Scheduler and random state in saves"].
		clockSnapshot := *s.Clock
		box := clockSnapshot.SaveBox()
		for i, b := range box {
			w("clock-box:%d:%02x|", i, b)
		}
	}
	for _, u := range s.allParityUnits() {
		writeParityUnit(w, u)
	}
	if s.Units != nil {
		for player := 0; player < 10; player++ {
			w("units-created:%d:%d|", player, s.Units.CreatedCountForPlayer(player))
		}
	}
	if s.Econ != nil {
		e := economy.TraceSnapshot{}
		if s.Econ != nil {
			e = s.Econ.ParitySnapshot(s.Units)
		}
		for _, u := range e.Units {
			w("economy-unit:%d:%d:%s:%08x:%08x|", u.Slot, u.Owner, u.DefinitionKey, math.Float32bits(u.SpotMetal), math.Float32bits(u.Remaining))
			for resource := 0; resource < 2; resource++ {
				writeBucket(w, "unit-live", int(u.Slot), resource, u.Buckets[resource])
				writeArchivedBucket(w, "unit-archive", int(u.Slot), resource, u.Archived[resource])
			}
		}
		for i := range e.Players {
			p := e.Players[i]
			w("player:%d:%t:%d:%d:%d:%d:%08x:%08x|", i, p.Exists, p.ControllerState, p.LiveUnitCount, p.UnitsEverCreated, p.EndGameCountdown, math.Float32bits(p.Stock[0]), math.Float32bits(p.Stock[1]))
			w("cap:%08x:%08x:pass:%08x:%08x:%08x:%08x:ai:%08x:%08x:%08x:%08x|", math.Float32bits(p.Capacity[0]), math.Float32bits(p.Capacity[1]), math.Float32bits(p.PassProduced[0]), math.Float32bits(p.PassProduced[1]), math.Float32bits(p.PassConsumed[0]), math.Float32bits(p.PassConsumed[1]), math.Float32bits(p.AIProduction[0]), math.Float32bits(p.AIProduction[1]), math.Float32bits(p.AIConsumption[0]), math.Float32bits(p.AIConsumption[1]))
			for r := 0; r < 2; r++ {
				writeBucket(w, "mirror", i, r, p.Mirror[r])
				writeArchivedBucket(w, "archive", i, r, p.ArchivedMirror[r])
				w("totals:%d:%d:%016x:%016x:%016x|", i, r, math.Float64bits(p.Waste[r]), math.Float64bits(p.TotalProduced[r]), math.Float64bits(p.TotalConsumed[r]))
			}
			w("deadlines:%d:%d:%d:%d:%d:%d:%t:%t:%t:%t:%t:%08x:%08x|", i, p.UpdateTime, p.WinLoseTime, p.DisplayTimer, p.Helper1Deadline, p.Helper2Deadline, p.IsObserver, p.GameEnded, p.AutoShareMetal, p.AutoShareEnergy, p.AutoShareSensor, math.Float32bits(p.MetalShareThreshold), math.Float32bits(p.EnergyShareThreshold))
			w("storage:%d:%t:%08x:%08x|", i, p.StorageBonusEnabled, math.Float32bits(p.StorageBonus[0]), math.Float32bits(p.StorageBonus[1]))
			for a := 0; a < 10; a++ {
				w("ally:%d:%d:%t|", i, a, p.Allies[a])
			}
		}
	}
	if s.Movement != nil {
		if s.Movement.Scheduler != nil {
			st := s.Movement.Scheduler.TraceState()
			w("scheduler:%d:%t:%d:%t|", st.Base, st.BaseSet, st.LastReplenish, st.HaveLast)
			for player := 0; player < 10; player++ {
				w("scheduler-player:%d:%d:%d|", player, st.Scales[player], st.Pending[player])
			}
			for _, request := range st.Requests {
				w("scheduler-request:%d:%d:%d:%d:%d:%d|", request.Unit, request.Player, request.Start.X, request.Start.Z, request.Activation, request.Goal.Kind)
				writeGoal(w, request.Goal)
			}
		}
		if s.Units != nil {
			for _, m := range s.Movement.ParitySnapshot(s.Units, s.parityTraceTick) {
				w("movement:%d:%d:%d:%d:%d:%d:%d|", m.Slot, m.X, m.Z, m.Heading, m.Speed, m.VelocityX, m.VelocityZ)
				w("route-meta:%d:%t:%t:%d:%d|", m.Slot, m.CurrentActive, m.CurrentDirty, m.CurrentStatus, m.CurrentStaticRevision)
				if m.PathFailure != nil {
					w("path-failure:%d:%d:%d|", m.Slot, m.PathFailure.Status, m.PathFailure.Tick)
				}
				w("route-storage:%d:%d|", m.Slot, m.CurrentRouteCount)
				for i, p := range m.CurrentRouteStorage {
					w("route-stale:%d:%d:%d:%d|", m.Slot, i, p.X, p.Z)
				}
				if m.Collision != nil {
					c := m.Collision
					w("collision-current:%d:%d:%d:%d:%d:%d:%d:%d:%d:%d:%d:%d:%d:%d:%d:%d:%d:%d:%t:%d:%t|", m.Slot, c.ID, c.X, c.Z, c.Y, c.VX, c.VZ, c.Speed, c.Heading, c.MaxVelocity, c.FootPrintX, c.FootPrintZ, c.Mode, c.CachedMode, c.CachedAnchor.X, c.CachedAnchor.Z, c.OldAnchor.X, c.OldAnchor.Z, c.Blocked, c.BlockerID, c.Dirty)
				}
				if m.Pending != nil {
					w("movement-pending:%d:%d:%d:%d:%d:%d:%d|", m.Slot, m.Pending.Unit, m.Pending.Player, m.Pending.Activation, m.Pending.Start.X, m.Pending.Start.Z, m.PendingGoal.Kind)
					writeGoal(w, m.PendingGoal)
				}
				if m.Result != nil {
					w("movement-result:%d:%d:%t:%d|", m.Slot, m.Result.Status, m.Result.Done, m.Result.Tick)
					writeGoal(w, m.Result.Goal)
					for _, p := range m.Result.Points {
						w("movement-result-point:%d:%d:%d|", m.Slot, p.X, p.Z)
					}
				}
				for _, p := range m.CurrentRoute {
					w("route-current:%d:%d|", p.X, p.Z)
				}
				for _, p := range m.NextRoute {
					w("route-next:%d:%d|", p.X, p.Z)
				}
				for _, c := range m.CollisionHistory {
					w("collision:%d:%d:%d:%d:%d:%d:%d:%d:%d:%d:%d:%d:%d:%d:%d:%d:%d:%d:%d:%d:%t:%d:%t|", c.Tick, c.Slot, c.State.ID, c.State.X, c.State.Z, c.State.Y, c.State.VX, c.State.VZ, c.State.Speed, c.State.Heading, c.State.MaxVelocity, c.State.FootPrintX, c.State.FootPrintZ, c.State.Mode, c.State.CachedMode, c.State.CachedAnchor.X, c.State.CachedAnchor.Z, c.State.OldAnchor.X, c.State.OldAnchor.Z, c.State.Blocked, c.State.BlockerID, c.State.Dirty)
				}
			}
		}
	}
	if s.Features != nil {
		for _, f := range s.Features.Instances() {
			writeFeature(w, f)
		}
	}
	w("rng-init:%t:sim:%d:crt:%d:draws:%d:%d|", s.rngInitialized, s.rngSim.State, s.rngCrt.State, s.rngSim.Draws(), s.rngCrt.Draws())
	return hex.EncodeToString(h.Sum(nil)[:8]), nil
}

func writeBucket(w func(string, ...interface{}), kind string, player, resource int, b economy.Bucket) {
	w("bucket:%s:%d:%d:%08x:%08x:%08x:%08x|", kind, player, resource, math.Float32bits(b.Production), math.Float32bits(b.Requested), math.Float32bits(b.Accepted), math.Float32bits(b.Carry))
}

func writeArchivedBucket(w func(string, ...interface{}), kind string, player, resource int, b economy.ArchivedBucket) {
	w("bucket:%s:%d:%d:%08x:%08x|", kind, player, resource, math.Float32bits(b.Production), math.Float32bits(b.Requested))
}

func writeGoal(w func(string, ...interface{}), g path.GoalTrace) {
	w("goal:%d:%t:%d:%d:%d:%d:%d:%d:%d:%d:%d|", g.Kind, g.Unknown, g.Center.X, g.Center.Z, g.A, g.B, g.Rect.Min.X, g.Rect.Min.Z, g.Rect.Max.X, g.Rect.Max.Z, len(g.Cells))
	for _, c := range g.Cells {
		w("goal-cell:%d:%d|", c.X, c.Z)
	}
}

func writeParityUnit(w func(string, ...interface{}), u ParityUnit) {
	w("unit:%d:%s:%d:%t:%t:%d:%d:%d:%d:%d:%d:%08x:%08x:%08x:%t:%t:%t:%t:%t:%d:%08x:%t:%t:%d:%d:%t:%d:%d|", u.Slot, u.DefinitionKey, u.Owner, u.Alive, u.Dying, u.DeathCause, u.X, u.Y, u.Z, u.Health, u.MaxHealth, math.Float32bits(u.Remaining), u.Flags, u.Pending, u.InBuildStance, u.Busy, u.YardOpen, u.BuggerOff, u.Armored, u.Group, math.Float32bits(u.SpotMetal), u.Activated, u.IsCloaked, u.Kills, u.ParalyzeExpire, u.Stunned, u.CurrentSample, u.PriorSample)
	w("move:%d:%d:%d:%d:%d:%d:%d:%d:%d|", u.Move.Mode, u.Move.Heading, u.Move.Pitch, u.Move.Bank, u.Move.Speed, u.Move.PendingHeading, u.Move.PendingSpeed, u.Attachment.Carrier, u.Attachment.AttachPiece)
	for _, c := range u.Attachment.Cargo {
		w("cargo:%d:%d|", u.Slot, c)
	}
	for i, thread := range u.Threads {
		w("thread:%d:%d:%d:%d:%d:%d:%d:%d:%d:%d|", u.Slot, i, thread.Status, thread.PC, thread.SP, thread.Sleep, thread.WaitPiece, thread.WaitAxis, thread.WaitThread, thread.SignalMask)
		for _, stack := range thread.Stack {
			w("stack:%d:%d:%d|", u.Slot, i, stack)
		}
	}
	for i, flag := range u.RenderPieceFlags {
		w("render-flag:%d:%d:%d|", u.Slot, i, flag)
	}
	for i, slot := range u.Slots {
		w("slot:%d:%d:%s:%d:%d:%d:%d:%d:%t:%t:%d:%d:%d:%d:%d|", u.Slot, i, slot.WeaponKey, slot.Reload, slot.Flags, slot.DesiredYaw, slot.DesiredPitch, slot.Ammo, slot.AimIssue, slot.AimReady, slot.TargetKind, slot.TargetUnit, slot.TargetX, slot.TargetZ, slot.MuzzlePiece)
	}
	for _, o := range u.Orders {
		w("order:%d:%d:%d:%d:%d:%d:%d:%d:%d:%d:%d:%d:%d:%d:%d:%d:%d:%d:%d:%d:%d:%d:%s|", o.ID, o.Owner, o.Target, o.Phase, o.DynamicGate, o.Deadline, o.GoalX, o.GoalY, o.GoalZ, o.Param1, o.Param2, o.Param3, o.StaticGate, o.CreationTick, o.Satisfied, o.Flags, o.MoveState, o.PathStatus, o.GuardX, o.GuardY, o.CachedX, o.CachedY, o.BuildDefKey)
	}
	for _, p := range u.Pieces {
		w("piece:%d:%d:%d:%d:%d:%d:%d:%t:%t:%t|", p.RotX, p.RotY, p.RotZ, p.TransX, p.TransY, p.TransZ, u.Slot, p.DontShade, p.Hidden, p.DontShadow)
	}
}

func writeFeature(w func(string, ...interface{}), f *features.Instance) {
	if f == nil {
		return
	}
	key := ""
	if f.Def != nil {
		key = f.Def.CanonicalKey
	}
	w("feature:%s:%d:%d:%d:%d:%d:%t:%d:%d:%d:%t:%d:%d:%d:%d:%t:%t:%d:%d|", key, f.CX, f.CZ, f.DamageAccumulator, f.ReclaimProgress, f.Status, f.IsBurning, f.BurnCountdown, f.BurnTicks, f.BurnDuration, f.RemoteSuppressed, f.Y.Raw(), f.Vy.Raw(), f.X.Raw(), f.Z.Raw(), f.IsSinking, f.Settled, f.FootprintX, f.FootprintZ)
}
