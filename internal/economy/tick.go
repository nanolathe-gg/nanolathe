package economy

import (
	"github.com/nanolathe/nanolathe/internal/units"
)

// settleInterval is the per-player settlement deadline advance per [05 "Authoritative settlement order"] C2 and [GAP T1].
const settleInterval uint32 = 30

// isActiveState reports whether controller/state byte is one of the three active states
// per [05 "Authoritative settlement order"] step 1. While not active, nothing advances including deadline (C3).
// TODO(question): semantic names of the three values unknown; literal set 1,2,3 preserved.
func isActiveState(s uint8) bool {
	return s == 1 || s == 2 || s == 3
}

// isSettlingState reports whether state is one of the two settling states
// per [05 "Authoritative settlement order"] step 4 narrowed predicate. The third active state traverses but never settles (C4).
// TODO(question): semantic names unknown; literal set 1,2 preserved.
func isSettlingState(s uint8) bool {
	return s == 1 || s == 2
}

// statusPairPredicate is the literal status-pair predicate per [05 "Authoritative settlement order"] C4.
// It is a nonzero halfword at one field OR a zero word at its neighbor — kept literal with TODO(question), no located writer.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
func statusPairPredicate(half uint16, word uint16) bool {
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	return half != 0 || word == 0
}

// Tick runs the per-player deadline block for all ten slots in ascending order per C3 and I1.
// It delegates to TickPlayer with a nil beforeDeadline hook; callers that need the AI dispatch
// hook (phase 14) should call TickPlayer directly with the desired callback per C3.
func (s *Service) Tick(tick uint32, w *units.World) {
	if s == nil {
		return
	}
	for p := 0; p < 10; p++ {
		s.TickPlayer(p, tick, w, nil)
	}
}

// TickPlayer owns eligibility, per-tick helpers, deadline comparison and settlement per C2–C4.
// Order per [05 "Authoritative settlement order"]:
//
//  1. Early skip unless record exists, controller/state byte ∈ three active states, observer byte excludes observers —
//     while skipped NOTHING advances INCLUDING the deadline (C3).
//  2. Per-tick work independent of settlement still runs: two internally 30-paced helpers + a weapon/position refresh sweep,
//     never touching stock (C3).
//  3. After helpers and before the settlement deadline compare, invoke optional beforeDeadline callback (C3).
//     Phase 14 supplies AI dispatch there; economy never imports internal/ai.
//  4. Settlement deadline block: deadline is absolute tick compared UNSIGNED against global tick;
//     while greater, skip slot's remaining processing; when due, advance by EXACTLY 30 BEFORE anything else runs (C2).
//     Because the advance is a single conditional add, a slot >30 behind settles once per tick until caught up — reproduce that, do NOT loop (C2).
//  5. Still inside deadline block: settlement gate chain, all required before Settle is called (C4).
func (s *Service) TickPlayer(player int, tick uint32, w *units.World, beforeDeadline func()) {
	if s == nil {
		return
	}
	if player < 0 || player >= 10 {
		return
	}
	p := &s.Players[player]

	// C3 early slot structure: skip unless record exists, controller/state ∈ three active states, observer excludes.
	if !p.Exists {
		return
	}
	if !isActiveState(p.ControllerState) {
		return
	}
	if p.IsObserver {
		return
	}

	// C3 per-tick non-settlement work still runs regardless of deadline; never touches stock.
	// Two internally 30-paced helpers.
	// Helper1: private 30-tick counter per [05 "Authoritative settlement order"] step 2.
	if p.Helper1Deadline <= tick {
		// Unsigned due check: deadline <= tick means due. Advance by exactly 30 before helper work.
		// Single add, not loop, mirrors settlement catch-up shape.
		p.Helper1Deadline += settleInterval
		p.Helper1Calls++
	}
	// Helper2: second helper with its own internal 30-tick gate.
	if p.Helper2Deadline <= tick {
		p.Helper2Deadline += settleInterval
		p.Helper2Calls++
	}
	// Weapon/position refresh sweep for player's unit range, never touching stock.
	// Retail sweeps units of one type family; we count invocations and iterate for determinism without stock effects.
	p.WeaponRefreshCalls++
	if w != nil {
		// Stable slot order visitation per I1, but never touch stock per C3.
		for _, u := range w.Iter() {
			if u == nil || !u.Alive {
				continue
			}
			if int(u.Owner) != player {
				continue
			}
			_ = u // refresh marker, no stock mutation
		}
	}

	// C3: After helpers, before deadline compare, invoke optional beforeDeadline callback.
	if beforeDeadline != nil {
		beforeDeadline()
	}

	// C2 settlement deadline block: unsigned compare against global tick.
	// While deadline greater (unsigned), skip slot's remaining processing entirely.
	if p.UpdateTime > tick {
		return
	}
	// When due, advance by EXACTLY 30 BEFORE anything else in the block runs (C2).
	// Single conditional add — do NOT loop. A slot >30 behind settles once per tick until caught up.
	p.UpdateTime += settleInterval

	// C4 settlement gate chain, all required before Settle. The early check already covered
	// exists, active (three states), not observer, but settlement narrows state to two settling states
	// and adds status-pair, game-ended, and countdown predicates.
	if !p.Exists {
		return
	}
	if !isActiveState(p.ControllerState) {
		return
	}
	if p.IsObserver {
		return
	}
	if !statusPairPredicate(p.StatusHalfwordAt144, p.StatusWordAt140) {
		return
	}
	if !isSettlingState(p.ControllerState) {
		return
	}
	if p.GameEnded {
		return
	}
	if p.EndGameCountdown >= 0 {
		return
	}

	// C7 pass ordering delegated to admission via Settle, WITH the world.
	// Dropping w here is what made the per-unit half of the two-stage
	// algorithm dead code: the sums degenerate to the player mirror, both
	// ratios come out wrong, and C6's slot-order consumption never happens.
	s.Settle(player, tick, w)
}

// PlayerSave persists the three absolute tick deadlines verbatim per [05 "Authoritative settlement order"] C5 and [05 "Saving economy, construction, and features"].
type PlayerSave struct {
	UpdateTime   uint32
	WinLoseTime  uint32
	DisplayTimer uint32
}

// SaveState persists UpdateTime/WinLoseTime/DisplayTimer verbatim per C5.
func (s *Service) SaveState() [10]PlayerSave {
	var out [10]PlayerSave
	if s == nil {
		return out
	}
	for i := 0; i < 10; i++ {
		p := s.Players[i]
		out[i] = PlayerSave{
			UpdateTime:   p.UpdateTime,
			WinLoseTime:  p.WinLoseTime,
			DisplayTimer: p.DisplayTimer,
		}
	}
	return out
}

// LoadState restores UpdateTime/WinLoseTime/DisplayTimer verbatim, never re-seeding on load per C5.
func (s *Service) LoadState(st [10]PlayerSave) {
	if s == nil {
		return
	}
	for i := 0; i < 10; i++ {
		s.Players[i].UpdateTime = st[i].UpdateTime
		s.Players[i].WinLoseTime = st[i].WinLoseTime
		s.Players[i].DisplayTimer = st[i].DisplayTimer
	}
}

// SeedDeadlines seeds every active player's deadline (+two siblings) to current global tick per C5.
// Battle initialization seeds every active player's settlement deadline (and two siblings) to the current global tick,
// so all players share the same initial phase [05 "Authoritative settlement order"] C5.
// Active means record exists, controller/state ∈ three active states, not observer — the same early gate per C3.
// Also seeds the two private helper deadlines to keep helpers phase-aligned; helpers never touch stock.
func (s *Service) SeedDeadlines(tick uint32) {
	if s == nil {
		return
	}
	for i := 0; i < 10; i++ {
		p := &s.Players[i]
		if !p.Exists {
			continue
		}
		if !isActiveState(p.ControllerState) {
			continue
		}
		if p.IsObserver {
			continue
		}
		p.UpdateTime = tick
		p.WinLoseTime = tick
		p.DisplayTimer = tick
		p.Helper1Deadline = tick
		p.Helper2Deadline = tick
	}
}

// ShareTick is the automatic sharing dispatcher per [05 "Allied resource and sensor sharing"] C12.
// It runs once per tick after the player phase, for the reference player only, and self-gates:
// metal/energy transfers when globalTick %60==0, sensor sharing at %450==0.
// Transfers mutate live stock between passes via ShareTransfer.
func (s *Service) ShareTick(tick uint32) {
	if s == nil {
		return
	}
	ref := s.ReferencePlayer
	if ref < 0 || ref >= 10 {
		ref = 0
	}
	if tick%60 == 0 {
		s.shareResource(ref, Metal)
		s.shareResource(ref, Energy)
	}
	if tick%450 == 0 {
		s.shareSensors(ref)
	}
}

func (s *Service) shareResource(ref int, res Res) {
	if s == nil {
		return
	}
	if ref < 0 || ref >= 10 {
		return
	}
	src := &s.Players[ref]
	if !src.Exists || src.IsObserver || !isActiveState(src.ControllerState) {
		return
	}
	var enabled bool
	var threshold float32
	var ratio float32
	switch res {
	case Metal:
		enabled = src.AutoShareMetal
		threshold = src.MetalShareThreshold
		ratio = 0.333333343 // [05 "Allied resource and sensor sharing"] metal ratio
	case Energy:
		enabled = src.AutoShareEnergy
		threshold = src.EnergyShareThreshold
		ratio = 0.5 // [05 "Allied resource and sensor sharing"] energy ratio
	default:
		return
	}
	if !enabled {
		return
	}
	if src.Stock[res] <= threshold {
		return
	}
	// Scan player slots 0..9 ascending; every eligible allied candidate with lower current stock replaces previous, so last qualifying wins.
	// Eligibility: exists, not observer, allied, lower stock per [05].
	dstIdx := -1
	for i := 0; i < 10; i++ {
		if i == ref {
			continue
		}
		dst := &s.Players[i]
		if !dst.Exists || dst.IsObserver || !isActiveState(dst.ControllerState) {
			continue
		}
		if !src.Allies[i] {
			continue
		}
		if dst.Stock[res] >= src.Stock[res] {
			continue
		}
		dstIdx = i
	}
	if dstIdx < 0 {
		return
	}
	dst := &s.Players[dstIdx]
	// Transfer = min(destination capacity - destination current, (source current - source threshold) × ratio)
	gap := dst.Capacity[res] - dst.Stock[res]
	if gap <= 0 {
		return
	}
	excess := src.Stock[res] - threshold
	if excess <= 0 {
		return
	}
	transfer := excess * ratio
	if transfer > gap {
		transfer = gap
	}
	if transfer <= 0 {
		return
	}
	ShareTransfer(src, dst, res, transfer)
}

func (s *Service) shareSensors(ref int) {
	if s == nil {
		return
	}
	if ref < 0 || ref >= 10 {
		return
	}
	src := &s.Players[ref]
	if !src.Exists || src.IsObserver || !isActiveState(src.ControllerState) {
		return
	}
	if !src.AutoShareSensor {
		return
	}
	s.SensorShareCalls++
}
