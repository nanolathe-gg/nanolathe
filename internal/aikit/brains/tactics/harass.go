package tactics

import (
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
)

// Early harassment (harass, probe and tour switches, default on).
//
// The posture holds the main squad from offensives until the army reaches
// the strategy's attack value; while held it took only clearly undefended
// targets, and only once it was three units worth 300. The hard persona's
// first combat unit comes at minute three to four and the second soon
// after, so the army waited at its gather point with two units and
// undefended enemy extractors in view until the third arrived, and without
// any target known it never looked: the scouts walked the start positions
// nearest to home, zig-zagging across a ten-start map. First kills came at
// minute five to seven in mirrors (human winners: 4.7).
//
//   - harass: while held, the main squad raids with two units worth 200
//     (HarassUnits, HarassValue) and weighs targets as the raid squad
//     does, by economy (extractors, constructors, energy, factories; a
//     zone with none is no raid target), still only those clearly
//     undefended (the soft margin, 3.0). Out on such a raid it turns back
//     like a raider: it fights in the field only at the engage margin, and
//     leaves the target or the fight below the engage margin rather than
//     the retreat margin, so a small raid that meets defenses — a tower
//     come into sight, a defending army — goes home instead of trading.
//   - probe: while held with nothing known, a main squad of the harass
//     size explores the nearest unseen start position (the offensive's
//     exploration needs 600 in value and no hold) as such a raid.
//   - tour: each scout goes to the unseen start position nearest to
//     itself, not to home, so it tours the starts instead of zig-zagging.
//
// The offensive, the raid squad and every other squad are unchanged; with
// all three off the army plays exactly as before.

// Harass raid size: members and value (hn=, hv=).
const (
	harassUnits = 2
	harassValue = 200
)

// mayExplore reports whether squad s may explore when nothing is known:
// unheld and worth exploreValue, or held and allowed to probe (its size is
// then the harass launch rule's).
func (a *Army) mayExplore(s *squad) bool {
	if !s.held {
		return s.total.value >= exploreValue
	}
	return a.P.Probe && a.P.Harass && s.id == sqMain
}

// pullMargin is the ratio below which a launched squad leaves its target or
// its fight: the retreat margin, or the engage margin for the main squad
// out on a raid the hold allowed.
func (a *Army) pullMargin(b *core.Board, s *squad) int64 {
	if s.soft {
		return a.engageMargin(b)
	}
	return a.retreatMargin(b)
}

// earlyStats times the army's first contact for Report: when it first had
// a member, first knew of a target, first launched and first fought, and how
// long its small early army waited with a target it was too small to
// launch at. No decision reads it.
type earlyStats struct {
	member uint32 // first think a ground squad had a member
	zone   uint32 // first think a target zone was known
	launch uint32 // first launch of a ground squad
	fight  uint32 // first engagement of a ground squad (target or field)
	// Thinks the main squad had chosen a target but was too small to
	// launch (fewer units or less value than the launch rule asks), and
	// thinks it waited gathered with members and no target zone known.
	small, blind int64
}

// groundSquad reports whether squad id fights on the ground (main, raid,
// home guard, amphibious assault).
func groundSquad(id int32) bool {
	return id == sqMain || id == sqRaid || id == sqDefend || id == sqAmph
}

// noteEarly records this think's first-contact milestones.
func (a *Army) noteEarly(b *core.Board) {
	e := &a.early
	if e.member == 0 {
		for id := int32(1); id < numSq; id++ {
			if groundSquad(id) && len(a.sq[id].members) > 0 {
				e.member = b.Tick
				break
			}
		}
	}
	if e.zone == 0 && len(a.candidates) > 0 {
		e.zone = b.Tick
	}
	if m := &a.sq[sqMain]; len(m.members) > 0 && m.state == stGather && len(a.candidates) == 0 {
		e.blind++
	}
}

// noteState records a ground squad's first launch and first fight.
func (a *Army) noteState(b *core.Board, s *squad, st squadState) {
	if !groundSquad(s.id) {
		return
	}
	e := &a.early
	switch {
	case st == stApproach && e.launch == 0:
		e.launch = b.Tick
	case st == stEngage && e.fight == 0:
		e.fight = b.Tick
	}
}

// reportEarly publishes the first-contact counters.
func (a *Army) reportEarly(add func(name string, v int64)) {
	e := &a.early
	add("tac_first_member_tick", int64(e.member))
	add("tac_first_zone_tick", int64(e.zone))
	add("tac_first_launch_tick", int64(e.launch))
	add("tac_first_fight_tick", int64(e.fight))
	add("tac_small_thinks", e.small)
	add("tac_blind_thinks", e.blind)
}
