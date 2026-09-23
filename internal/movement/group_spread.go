package movement

import (
	"cmp"
	"slices"

	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

// Group-order admission spread: Nanolathe Modern policy
// (docs/DESIGN_MOVEMENT_PATH.md "Modern group-order spreading").
//
// Retail admits every follower whose throttle has elapsed as far as the
// scheduler's step allowance reaches [04 R-MOV-01 §7][04 R-PATH-01 §6]. A
// group order zeroes each member's admission stamp at goal installation
// [04 R-PATH-01 §8], so the whole group comes due on one tick. Under the
// raised allowance the whole group is then searched on that tick. The spread
// holds the farther members of a large group back by one or two ticks. It
// changes only which tick a request is admitted on: the request, its goal, the
// synthetic straight line installed with the goal and every search input are
// unchanged.

// firstRequest is one candidate of a group: a staged first request that comes
// due on the tick being assigned.
type firstRequest struct {
	slot   pool.Handle
	player uint8
	// cost is the request goal's own search heuristic from the unit's
	// committed start cell, plus one so a group of zero-distance requests
	// still splits by count.
	cost int64
}

// noteFirstRequest records a staged request as a group candidate when its
// follower has never been admitted since its goal was installed. It is called
// for every staging; the rule's threshold is the whole cost under Strict.
func (s *System) noteFirstRequest(h pool.Handle) {
	route := handleRow(s.Routes, h)
	if route == nil || route.LastRequestTick != 0 {
		return
	}
	// A restaged request is a new submission: any hold its predecessor was
	// given belongs to that request, and the new one is assigned afresh.
	route.firstHold = 0
	if route.firstPending {
		return
	}
	if minGroup, _ := s.rules().FirstRequestSpread(s); minGroup <= 0 {
		return
	}
	route.firstPending = true
	s.firstRequests = append(s.firstRequests, h)
}

// assignFirstRequestHolds runs at the start of each scheduler call, before its
// first poll. Candidates whose request comes due on tick form one group per
// player; a group of at least the rule's threshold is spread. Candidates not
// yet due stay pending. Nothing here draws RNG or reads a map in iteration.
func (s *System) assignFirstRequestHolds(tick uint32) {
	if len(s.firstRequests) == 0 {
		return
	}
	minGroup, ticks := s.rules().FirstRequestSpread(s)
	group := s.firstGroup[:0]
	keep := s.firstRequests[:0]
	for _, h := range s.firstRequests {
		route := handleRow(s.Routes, h)
		if route == nil || !route.firstPending {
			continue
		}
		r, player, staged := s.pathProvider.stagedRequest(h)
		if !staged || route.LastRequestTick != 0 || minGroup <= 0 {
			route.firstPending = false
			continue
		}
		if !s.repathDue(route, int(h), tick) {
			keep = append(keep, h)
			continue
		}
		route.firstPending = false
		cost := int64(1)
		if r.Goal != nil {
			cost += int64(max(r.Goal.H(s.pathStartCell(s.pathProvider.world.Unit(h))), 0))
		}
		group = append(group, firstRequest{slot: h, player: uint8(player), cost: cost})
	}
	clear(s.firstRequests[len(keep):])
	s.firstRequests = keep
	s.firstGroup = group[:0]
	if len(group) < minGroup {
		return
	}
	slices.SortFunc(group, func(a, b firstRequest) int {
		return cmp.Or(cmp.Compare(a.player, b.player), cmp.Compare(a.cost, b.cost), cmp.Compare(a.slot, b.slot))
	})
	for lo := 0; lo < len(group); {
		hi := lo + 1
		for hi < len(group) && group[hi].player == group[lo].player {
			hi++
		}
		if hi-lo >= minGroup {
			s.holdGroup(group[lo:hi], tick, ticks)
		}
		lo = hi
	}
}

// holdGroup assigns one player's group, sorted nearest first, to ticks
// consecutive ticks. Each request's share is decided by the summed cost of
// the requests before it: bucket k starts where that running sum reaches k of
// ticks equal parts of the group's total. The nearest request is therefore
// always admitted on the due tick, and every tick carries about the same
// expected search work.
func (s *System) holdGroup(group []firstRequest, tick uint32, ticks int) {
	var total int64
	for _, c := range group {
		total += c.cost
	}
	var before int64
	for _, c := range group {
		k := min(before*int64(ticks)/total, int64(ticks-1))
		before += c.cost
		if k == 0 {
			continue
		}
		if route := handleRow(s.Routes, c.slot); route != nil {
			route.firstHold = tick + uint32(k)
		}
	}
}
