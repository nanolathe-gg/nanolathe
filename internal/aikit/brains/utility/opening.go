package utility

import (
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
)

// Openings and the first ten minutes (unit G4; README §13.13). Nanolathe
// Modern AI policy: the switches are Variety switches (style.go) and each
// is measured against the brain without it.
//
// openState is the opening's per-game state. It rides in the tech state
// (s.open) because the shared model's struct lives in another unit's
// file; the request is a field of its own there.
type openState struct {
	rec  openReclaim // early reclaim (open_reclaim.go)
	army int32       // early army parts (open_army)
	// The factory after the first (open_follow; econ_family.go): the
	// family preferred until followN factories of it are owned (a second
	// of the first family) or one is (another family), and its factories.
	followN, followFam int32
	followFacs         []int32

	// Instrumentation for the arena's Report (never read by a decision):
	// the first factory's frame and completion, the first constructor, the
	// first combat unit that is not a scout and the first scout (ticks, 0 =
	// not yet), the first factory's family, and the reclaimable metal the
	// first feature listing held near the start.
	facFrame, facDone, consDone, combatDone, scoutDone uint32
	facFam                                             int32
	featSeen                                           bool
	featN                                              int32
	featMetal, featNear                                int64
	// Metal produced (milli), integrated from the observed income between
	// thinks, and its value at minutes 5 and 10.
	metal, metal5, metal10 int64
	lastTick               uint32
}

// Early army (open_army, a sum of parts):
//
//   - 1 scouts wait: no scout is made before the first combat unit that is
//     not a scout is built or queued, until minute openScoutWait. The
//     brain's first factory product was a scout in 41% of games (two
//     scouts in a row in 40%), a human player's in 8% of the stock-unit
//     1v1 recordings (constructor 53%, combat unit 33%), and scouts do
//     not join the army.
//   - 2 combat after the first constructor: once a constructor is built
//     or queued, constructors rate armyFirstCut times lower until
//     openFirstCombat combat units have been queued, until minute
//     openArmyEnd. The brain's first combat unit that is not a scout came
//     at a median minute 4.3–4.5 (first constructor 3.7), a human
//     player's at 3.7 (constructor 2.5; stock-unit 1v1 recordings, first
//     four products of the first factory CCCC 12%, AAAA 12%, CAAA 9%).
const (
	armyScouts      = 1
	armyAfterCons   = 2
	openScoutWait   = 7200  // minute 4
	openFirstCombat = 2     // combat units queued before constructors rate as before
	openArmyEnd     = 10800 // minute 6
	armyFirstCut    = 8
)

// armyFirst reports whether combat units go before constructors (open_army
// part 2): a constructor of ours is built, framed or queued (pending
// counts those queued or framed), fewer than openFirstCombat combat units
// have been queued, and it is before openArmyEnd.
func (s *shared) armyFirst(pr *Production, pending int32) bool {
	return s.open.army&armyAfterCons != 0 && s.tick < openArmyEnd &&
		s.cons+pending > 0 && pr.combatQueued < openFirstCombat
}

// scoutsWait reports whether scouts wait for the first combat unit this
// think (open_army part 1): no combat unit of ours that is not a scout is
// built, and none is queued at a factory.
func (s *shared) scoutsWait(b *core.Board, pr *Production) bool {
	if s.open.army&armyScouts == 0 || s.tick >= openScoutWait || s.armyCount > 0 {
		return false
	}
	for _, fi := range b.Factories {
		f := &b.O.Own[fi]
		if f.QueueLen == 0 || int(f.H) >= len(pr.reg) {
			continue
		}
		r := &pr.reg[f.H]
		if r.def == f.Info && r.gen == f.Gen && r.prod != nil && s.tick-r.tick < 1800 &&
			r.prod.Role.Has(aikit.RoleCombat) && !r.prod.Role.Has(aikit.RoleScout) {
			return false
		}
	}
	return true
}

// openWatchEnd bounds the milestone watch (a milestone not reached by
// then reports 0).
const openWatchEnd = 36000

// openNear is the radius (world units) of the "near the start" feature
// count in the report.
const openNear = 800

// watchOpen records the opening milestones for Report. It reads only the
// board and the static tables and never draws.
func (s *shared) watchOpen(b *core.Board) {
	o := &s.open
	obs := b.O
	if b.Tick > o.lastTick {
		o.metal += int64(obs.Metal.Income) * int64(b.Tick-o.lastTick) * 1000 / 30
		o.lastTick = b.Tick
	}
	if b.Tick <= 9000 {
		o.metal5 = o.metal
	}
	if b.Tick <= 18000 {
		o.metal10 = o.metal
	}
	if !o.featSeen && obs.FeaturesTick != 0 {
		o.featSeen = true
		for i := range obs.Features {
			f := &obs.Features[i]
			if !f.Reclaimable || f.Metal <= 0 {
				continue
			}
			o.featN++
			o.featMetal += int64(f.Metal)
			if aikit.Dist2(f.X, f.Z, b.HomeX, b.HomeZ) <= openNear*openNear {
				o.featNear += int64(f.Metal)
			}
		}
	}
	if b.Tick > openWatchEnd || (o.facDone != 0 && o.consDone != 0 && o.combatDone != 0 && o.scoutDone != 0) {
		return // milestones are looked for in the first twenty minutes
	}
	for i := range obs.Own {
		u := &obs.Own[i]
		r := u.Info.Role
		switch {
		case r.Has(aikit.RoleFactory):
			if o.facFrame == 0 {
				o.facFrame = b.Tick
				if int(u.Info.Index) < len(s.facFam) {
					o.facFam = int32(s.facFam[u.Info.Index])
				} else {
					o.facFam = factoryFamily(s, u.Info)
				}
			}
			if u.Built && o.facDone == 0 {
				o.facDone = b.Tick
			}
		case !u.Built || !r.Has(aikit.RoleMobile):
		case s.info[u.Info.Index].canEco && !r.Has(aikit.RoleCommander):
			if o.consDone == 0 {
				o.consDone = b.Tick
			}
		case r.Has(aikit.RoleCombat) && r.Has(aikit.RoleScout):
			if o.scoutDone == 0 {
				o.scoutDone = b.Tick
			}
		case r.Has(aikit.RoleCombat):
			if o.combatDone == 0 {
				o.combatDone = b.Tick
			}
		}
	}
}

// factoryFamily labels one factory as setupFamily does (the majority
// family of its mobile combat products), for a game whose family table was
// not built.
func factoryFamily(s *shared, f *aikit.UnitInfo) int32 {
	var n [famShip + 1]int32
	for _, q := range f.Builds {
		if q.Role.Has(aikit.RoleCombat) && q.Role.Has(aikit.RoleMobile) {
			n[productFamily(q, s.info[q.Index].water)]++
		}
	}
	best := famNone
	for fam := famKbot; fam <= famShip; fam++ {
		if n[fam] > n[best] {
			best = fam
		}
	}
	return best
}

// reportOpen publishes the opening milestones.
func (s *shared) reportOpen(add func(name string, value int64)) {
	o := &s.open
	add("open_fac_frame_tick", int64(o.facFrame))
	add("open_fac_done_tick", int64(o.facDone))
	add("open_fac_family", int64(o.facFam))
	add("open_family_drawn", int64(s.firstFam))
	add("open_cons_tick", int64(o.consDone))
	add("open_combat_tick", int64(o.combatDone))
	add("open_scout_tick", int64(o.scoutDone))
	add("open_feat_n", int64(o.featN))
	add("open_feat_metal", o.featMetal)
	add("open_feat_metal_near", o.featNear)
	add("open_metal_5", o.metal5/1000)
	add("open_metal_10", o.metal10/1000)
	add("open_reclaim_orders", int64(o.rec.orders))
	add("open_reclaim_metal", o.rec.metal)
}
