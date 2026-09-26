package tactics

import (
	"fmt"
	"strconv"

	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
)

// Explain implements core.Explaining. It runs between thinks on the
// simulation thread and may allocate.
func (a *Army) Explain(b *core.Board, x *aikit.Explain) {
	if !a.ready {
		return
	}
	x.Notes = append(x.Notes,
		fmt.Sprintf("tactics: actions avail %d spent %d (reserve %d), issued %d, withheld %d, host-dropped %d",
			a.avail, a.spent, a.prodReserve, a.issued, a.starved, a.droppedTotal),
		fmt.Sprintf("tactics: gather (%d,%d), enemy army %d value (seen at %d,%d), %d target zones, %d incidents, guard need %d",
			a.gx, a.gz, a.enemyMob.value, a.enemyMobX, a.enemyMobZ, len(a.candidates), len(a.incidents), a.threatMemory))
	for id := int32(1); id < numSq; id++ {
		s := &a.sq[id]
		if !s.active || len(s.members) == 0 {
			continue
		}
		task := roleNames[id] + ":" + stateNames[s.state]
		switch {
		case id == sqStrike && s.tgtH != 0:
			task += " target"
		case id == sqFighter && s.state == stDefend:
			task += " intercept"
		case s.state == stDefend:
			task += " incident"
		case s.target >= 0:
			z := &a.zones[s.target]
			task += fmt.Sprintf(" zone v=%d eco=%d", z.value, z.eco)
		case s.target == tgtExplore:
			task += " explore"
		case s.target == tgtSkirmish:
			task += " skirmish"
		}
		if s.routed {
			task += fmt.Sprintf(" via %d waypoints", s.nwp)
		}
		if s.state == stRetreat && s.why != whyNone {
			task += " (" + whyNames[s.why] + ")"
		}
		task += fmt.Sprintf(" ratio %d‰ present %d/%d", s.ratio, s.present.n, len(s.members))
		x.Squads = append(x.Squads, aikit.SquadView{
			ID: id, Task: task, X: s.cx, Z: s.cz, TargetX: s.tx, TargetZ: s.tz,
			Units: int32(len(s.members)), Strength: s.present.strength(), Enemy: s.enemy.strength(),
		})
	}
	if main := &a.sq[sqMain]; a.P.Posture {
		x.Notes = append(x.Notes, fmt.Sprintf("tactics: posture attack at %d (aggression %d, margins %d/%d‰), main %d, offensive %v, held %v; %d offensives, %d soft raids, held %d thinks",
			b.Posture.AttackValue, b.Posture.Aggression, a.engageMargin(b), a.retreatMargin(b), a.available(b, main), main.offensive, main.held, a.off.n, a.off.soft, a.off.held))
	}
	st := &a.stats
	x.Notes = append(x.Notes, fmt.Sprintf("tactics: launched %d, engaged %d (%d skirmishes), retreated %d in fight / %d before, cleared %d, defended %d, withdrew %d units, %d focus orders",
		st.launched, st.engaged, st.skirmish, st.retreatFight, st.retreatEarly, st.cleared, st.defended, st.withdrawn, st.focus))
	x.Notes = append(x.Notes, fmt.Sprintf("tactics: %d members stuck (standing still far from their orders); drops after army spend %d, while starved %d", a.stuckN, a.droppedAfterSpend, a.droppedStarved))
	if a.P.Air || a.P.Naval {
		x.Notes = append(x.Notes, fmt.Sprintf("tactics: reach tables %v (%d classes), %d members called back from goals out of reach", a.reachReady, len(a.rcls), a.stranded))
		x.Notes = append(x.Notes, fmt.Sprintf("tactics: air %d strikes (%d targets destroyed, %d aborted), %d intercepts, enemy air-to-air %d dps; land cut %v, %d naval target zones, enemy fleet %d value",
			st.strikes, st.strikeKills, st.strikeAborts, st.intercepts, a.enemyAirSup.dps, a.landCut(b), a.navalTargets, a.enemyFleet.value))
	}
	if a.P.Unseen && (a.P.Air || a.P.Naval) {
		x.Notes = append(x.Notes, fmt.Sprintf("tactics: enemy land army in sight %d value (%d dps, %d anti-air), remembered %d value (%d dps, %d anti-air) at (%d,%d)",
			a.enemyLand.value, a.enemyLand.dps, a.enemyLand.aa, a.landMem.value, a.landMem.dps, a.landMem.aa, a.landX, a.landZ))
		lc, gc := a.strikeCal()
		x.Notes = append(x.Notes, fmt.Sprintf("tactics: strikes flown expected to lose %d and destroy %d, lost %d and destroyed %d: loss ×%d‰, gain ×%d‰",
			a.calExpLoss, a.calExpGain, a.calLoss, a.calGain, lc, gc))
	}
	for _, g := range a.goals {
		score := g.score
		if score > 1<<30 {
			score = 1 << 30
		}
		x.Goals = append(x.Goals, aikit.Goal{
			Label:  fmt.Sprintf("%s → zone %d value %d ratio %d‰", roleNames[g.squad], g.zone, g.value, g.ratio),
			Score:  int32(score),
			Chosen: g.chosen,
			X:      g.x, Z: g.z,
		})
	}
	known := aikit.Grid{W: a.danger.W, H: a.danger.H, V: make([]int32, len(a.known))}
	for i, v := range a.known {
		known.V[i] = int32(v)
	}
	if a.P.Air {
		x.Grids = append(x.Grids, a.aaMem.Snapshot("tac_aamem"))
	}
	if a.P.Naval {
		x.Grids = append(x.Grids, a.seaHurt.Snapshot("tac_seahurt"))
		water := aikit.Grid{W: a.danger.W, H: a.danger.H, V: make([]int32, len(a.water))}
		for i, v := range a.water {
			water.V[i] = int32(v)
		}
		x.Grids = append(x.Grids, water.Snapshot("tac_water"))
	}
	if a.reachReady {
		// Passages for the main squad's land class at each sector centre
		// (1), with the main (2) and home guard (3) waiting points; tested
		// directly, so Explain leaves the cache as it was.
		m := b.K.Map
		pg := aikit.Grid{W: a.danger.W, H: a.danger.H, V: make([]int32, len(a.danger.V))}
		c, _ := a.walkerHome(b)
		if g := a.sq[sqMain].mainGroup(); g != nil {
			c = g.cls
		}
		if c >= 0 {
			mc := &a.rcls[c].mc
			for sec := range pg.V {
				px, pz := m.SectorCentre(int32(sec))
				if m.InPassage(mc, px/16-mc.FootX/2, pz/16-mc.FootZ/2, mc.FootX, mc.FootZ) {
					pg.V[sec] = 1
				}
			}
		}
		pg.V[m.Sector(a.gx, a.gz)] = 2
		dx, dz := a.gatherPoint(b, &a.sq[sqDefend])
		pg.V[m.Sector(dx, dz)] = 3
		x.Grids = append(x.Grids, pg.Snapshot("tac_passage"))
	}
	x.Grids = append(x.Grids,
		a.danger.Snapshot("tac_danger"),
		a.static.Snapshot("tac_static"),
		a.aa.Snapshot("tac_aa"),
		a.asset.Snapshot("tac_assets"),
		a.hurt.Snapshot("tac_hurt"),
		known.Snapshot("tac_known"))
}

// Report publishes the army's engagement counters (arena instrumentation;
// never read by play).
func (a *Army) Report(add func(name string, v int64)) {
	st := &a.stats
	add("tac_launched", st.launched)
	add("tac_engaged", st.engaged)
	add("tac_strikes", st.strikes)
	add("tac_strike_kills", st.strikeKills)
	add("tac_strike_aborts", st.strikeAborts)
	add("tac_intercepts", st.intercepts)
	add("tac_recalled", a.stranded)
	add("tac_passage_moved", st.passage)
	add("tac_wait_thinks", st.waits)
	add("tac_wait_passage", st.waitPassage)
	// Main-squad offensives (a launch the posture allowed; with posture=0
	// every launch from a regroup) and soft raids while held.
	off := &a.off
	add("tac_off_n", off.n)
	add("tac_off_first_tick", int64(off.first))
	add("tac_off_first_value", off.firstValue)
	add("tac_off_first_av", off.firstAV)
	if off.n > 0 {
		add("tac_off_value_mean", off.valueSum/off.n)
	}
	add("tac_soft_raids", off.soft)
	add("tac_raid_launches", off.raids)
	add("tac_soft_first_tick", int64(off.firstSoft))
	add("tac_held_thinks", off.held)
	for i, m := range checkMinutes {
		if i < off.atDone {
			add("tac_av_"+strconv.Itoa(int(m)), off.atAV[i])
			add("tac_avail_"+strconv.Itoa(int(m)), off.atAvail[i])
			add("tac_main_"+strconv.Itoa(int(m)), off.atMain[i])
		}
	}
	for k := range a.lost {
		add("tac_lost_"+bucketNames[k], a.lost[k])
	}
	a.reportEarly(add)
}
