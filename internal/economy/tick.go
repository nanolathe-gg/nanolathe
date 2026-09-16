package economy

import (
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// settleInterval is the per-player settlement deadline advance per [05 "Authoritative settlement order"] C2 and [GAP T1].
const settleInterval uint32 = 30

// isActiveState reports whether the controller/state byte is one of the three
// active states per [05 "Authoritative settlement order"] step 1. While not
// active, nothing advances including the deadline (C3).
//
// The three values are named: `1` is a locally controlled human, `2` a computer
// player, `3` a remote peer [05 R-SHARE-01 §1]. The marker that stood here said
// the names were unknown. internal/combat declares them as ControlByteHuman /
// ControlByteComputer / ControlByteRemote; this package cannot import combat, so
// the literals stay, with the identities stated.
func isActiveState(s uint8) bool {
	return s == 1 || s == 2 || s == 3
}

// isSettlingState reports whether the state is one of the two settling states
// per [05 "Authoritative settlement order"] step 4's narrowed predicate: the
// local human (`1`) and the computer player (`2`) settle, and the remote peer
// (`3`) traverses but never settles (C4) [05 R-SHARE-01 §1]. That is the same
// split the packet sender applies — it "refuses to emit unless the source's
// control byte is `1` or `2` and the destination's is `3`".
func isSettlingState(s uint8) bool {
	return s == 1 || s == 2
}

// Tick runs the per-player deadline block for all ten slots in ascending order per C3 and I1.
// It delegates to TickPlayer with a nil beforeDeadline hook; callers that need the AI dispatch
// hook (phase 5) should call TickPlayer directly with the desired callback per C3.
func (s *Service) Tick(tick uint32, w *units.World) {
	if s == nil {
		return
	}
	for p := 0; p < 10; p++ {
		s.TickPlayer(p, tick, w, nil)
	}
}

// TickPlayer owns the early eligibility gate, the session's beforeDeadline
// hook, the unsigned deadline advance, end conditions and settlement gates
// [05 "Authoritative settlement order"]. The hook owns manager/strategic/LOS
// work and runs on every eligible entry, even when settlement is not due.
// The returned verdict reports entry into the deadline block, including a due
// entry whose later settlement gates refuse. Session uses it for the viewing
// player's sensor tail [03 R-SENSOR-01].
func (s *Service) TickPlayer(player int, tick uint32, w *units.World, beforeDeadline func()) (deadlineRan bool) {
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

	// The session owns the real manager, strategic and LOS work, in that
	// order. Each service retains its own cadence [05 "Authoritative settlement order"].
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
	deadlineRan = true

	// The local slot's end-condition block runs here: inside the settlement
	// deadline block, after the advance and ahead of the gate chain
	// [08 R-TRIG-01 §6]. The block's due tick *is* this settlement deadline —
	// there is no separate trigger-poll or win/lose word, and an
	// implementation that kept a private deadline would diverge after a load,
	// because a retail save carries this one deadline and the poll resumes on
	// its phase. Economy cannot import the session, so the block reaches it
	// through the same callback seam as beforeDeadline; the *local* slot gate
	// belongs to the callback, which knows which slot is local.
	if s.EndCondition != nil {
		s.EndCondition(player, tick)
	}

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
	// The gate that stood here as an unresolved "status pair" is the
	// elimination test [05 R-ECO-01 §12]: the halfword is the player record's
	// live unit count and the word is its units-ever-created count, so
	// "halfword non-zero OR word zero" reads *the slot still has a live unit,
	// or never had one* — the exact negation of the elimination predicate.
	// There is no separate status to carry, and the counters are read from the
	// world rather than mirrored, because the phase-2 sweep decrements the live
	// count inside the same pass this gate runs in (see PlayerEliminated).
	//
	// The deadline advance above is deliberately ahead of this test: an
	// eliminated slot keeps advancing its settlement deadline and simply never
	// settles, which is the traced order [05 R-ECO-01 §12].
	//
	// This is the same skip the three other player walks make — both automatic
	// share walks below and the session's result sweep — so all four share one
	// helper.
	if PlayerEliminated(w, player) {
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
	return
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
	}
}

// ShareTick is the automatic sharing dispatcher per [05 "Allied resource and sensor sharing"] C12.
// It runs once per tick after the player phase, for the reference player only, and self-gates:
// metal/energy transfers when globalTick %60==0, sensor sharing at %450==0.
// Local transfers debit live stock and stage a request; received packets stage
// production without repeating the source debit [R-SHARE-01 §2, §4].
// The unit world is the candidate scan's elimination source: the slot's
// "not eliminated" clause is derived from the two unit counters, never from a
// flag [05 R-SHARE-01 §3]. See PlayerEliminated.
func (s *Service) ShareTick(tick uint32, w *units.World) {
	if s == nil {
		return
	}
	ref := s.ReferencePlayer
	if ref < 0 || ref >= 10 {
		ref = 0
	}
	if tick%60 == 0 {
		s.shareResource(ref, Metal, w)
		s.shareResource(ref, Energy, w)
	}
	if tick%450 == 0 {
		s.shareSensors(ref, w)
	}
}

func (s *Service) shareResource(ref int, res Res, w *units.World) {
	if s == nil {
		return
	}
	if ref < 0 || ref >= 10 {
		return
	}
	if !s.Networked {
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
		threshold = src.MetalShareThreshold // distinct sharing threshold [R-SHARE-01 §3]
		ratio = 0.33333334                  // [R-SHARE-01 §3]
	case Energy:
		enabled = src.AutoShareEnergy
		threshold = src.EnergyShareThreshold // distinct sharing threshold [R-SHARE-01 §3]
		ratio = 0.5                          // [R-SHARE-01 §3]
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
		if !dst.Exists || dst.IsObserver || !isActiveState(dst.ControllerState) || dst.ControllerState != 3 || dst.OptionKind != 1 || PlayerEliminated(w, i) {
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
	s.transfer(src, dst, res, transfer)
}

func (s *Service) shareSensors(ref int, w *units.World) {
	if s == nil {
		return
	}
	if !s.Networked {
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
	for i := 0; i < 10; i++ {
		if i == ref {
			continue
		}
		dst := &s.Players[i]
		if dst.Exists && !dst.IsObserver && !PlayerEliminated(w, i) && dst.ControllerState == 3 && dst.OptionKind == 1 && src.Allies[i] {
			// TODO(networking): emit the mapping-share packet here when the
			// deferred network transport is implemented [05 R-SHARE-01 §3].
			// This counter records eligibility only; it publishes no mapping.
			// The receive half is equally unbuilt: nothing decodes a resource
			// packet's no-debit credit [05 R-SHARE-01 §4] or merges a received
			// mapping grid [05 R-SHARE-01 §5-§6].
			s.SensorShareCalls++
		}
	}
}
