package utility

import (
	"fmt"

	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
)

// Explain runs between thinks on the simulation thread and may allocate.

func kindName(k commitKind) string {
	switch k {
	case pCons:
		return "constructor"
	case pCombat:
		return "combat"
	case pScout:
		return "scout"
	}
	if int(k) < len(commitNames) {
		return commitNames[k]
	}
	return "?"
}

func (st *Strategy) Explain(b *core.Board, x *aikit.Explain) {
	s := st.s
	enIncome := enemyIncome(b)
	x.Notes = append(x.Notes,
		fmt.Sprintf("supply M %d.%d/s E %d/s vs demand M %d.%d/s E %d/s (spend cap M %d.%d E %d, pending M %d.%d E %d)",
			s.supplyM/1000, s.supplyM%1000/100, s.supplyE/1000, s.demandM/1000, s.demandM%1000/100, s.demandE/1000,
			s.capM/1000, s.capM%1000/100, s.capE/1000, s.pendM/1000, s.pendM%1000/100, s.pendE/1000),
		fmt.Sprintf("coverage M %d‰ E %d‰ → need M %d E %d build-power %d", s.covM, s.covE, s.needM, s.needE, s.needBP),
		fmt.Sprintf("eco share %d (phase %d, base pressure %d‰, enemy/own army %d‰)", s.ecoShare, s.ecoPhase, s.pressure, s.armyRatio),
		fmt.Sprintf("enemy: army seen %d est %d, defense %d, air %d vs own AA %d (aa need %d‰), income seen %d.%d/s",
			s.enArmy, s.enEst, s.enDef, s.enAir, s.ownAA, s.aaNeed, enIncome/1000, enIncome%1000/100),
		fmt.Sprintf("builders %d (cons %d +%d pending), free safe spots %d, danger %d at (%d,%d), APM budget %d",
			s.builders, s.cons, s.consPending, s.freeSafe, s.danger, s.dangerX, s.dangerZ, s.budget),
		fmt.Sprintf("wind expected %d‰ now %d‰ (map %d..%d); steady energy %d/s vs extractor upkeep %d/s → firm need %d",
			s.windExp, s.curWind, s.k.Map.WindMin, s.k.Map.WindMax, s.firmE/1000, s.mexUpkeep/1000, s.needFirm),
		"style "+Styles[st.vr.style].Name+", "+st.vr.pers.String())
	st.explainN(b, x)
}

// explainN describes the switches' model: terrain reach, factory qualities
// and the tech state.
func (st *Strategy) explainN(b *core.Board, x *aikit.Explain) {
	s := st.s
	p := &s.p
	if p.Layout != 0 {
		stuck := int32(0)
		for _, n := range s.facTrapped {
			stuck += n
		}
		var metal int64
		var inLane int32
		for i := range b.O.Features {
			f := &b.O.Features[i]
			if f.Reclaimable {
				metal += int64(f.Metal)
				if f.Blocking && s.inLane(b, f) {
					inLane++
				}
			}
		}
		x.Notes = append(x.Notes, fmt.Sprintf("layout: zones fac %d energy %d makers %d; %d units stuck near factories, %d idle ships; features near home %d (%d metal, %d in factory lanes), clearing rests until t%d",
			s.zones.fac, s.zones.energy, s.zones.maker, stuck, s.navalIdle, len(b.O.Features), metal, inLane, s.clearRest/30))
	}
	if p.Naval == 0 && p.Air == 0 && p.Tech == 0 {
		return
	}
	t := &s.terr
	if t.ready {
		x.Notes = append(x.Notes, fmt.Sprintf("terrain: naval base (%d,%d) ok %v, home land %d cells, land reach %d‰, reach table %d (-1 = mean over starts), enemy naval share %d‰",
			t.navX, t.navZ, t.navOK, t.homeLand, s.landReach, s.reachSel, s.navalShare))
	}
	tab := s.k.Table
	for _, fi := range s.factories {
		f := tab.Units[fi]
		si := &s.info[fi]
		if f.Side != s.k.Side || f.Depth > 3 {
			continue
		}
		room, site := true, true
		if t.ready {
			room, site = t.landRoom[fi], t.siteOK[fi]
		}
		x.Notes = append(x.Notes, fmt.Sprintf("factory option %s: quality %d (reach-weighted %d) reach %d‰ water %v room %v site %v air %v t2 %v",
			f.Key, si.quality, si.qualityR, s.factoryReach(f), si.water, room, site, si.airFac, si.t2))
	}
	if p.Tech != 0 {
		x.Notes = append(x.Notes, fmt.Sprintf("tech: want %d‰, tech-2 factories %d, advanced constructors %d", s.tech.want, s.tech.t2Fac, s.tech.advCons))
	}
}

func explainRing(ring []decision, next int, x *aikit.Explain, tag string, now uint32) {
	n := len(ring)
	for j := 1; j <= n; j++ {
		d := &ring[(next-j+n)%n]
		if d.who == nil || now-d.tick > 900 {
			continue
		}
		for i := 0; i < d.n; i++ {
			c := &d.top[i]
			what := kindName(c.kind)
			if c.prod != nil {
				what += " " + c.prod.Key
			}
			if c.kind == cMex {
				what += fmt.Sprintf(" spot %d", c.spot)
			}
			verb := "→"
			switch d.note {
			case noteKeep:
				verb = "keeps; best"
			case noteNothing:
				verb = "idle; best"
			}
			label := fmt.Sprintf("%s t%d %s %s %s [%d %d %d %d]", tag, d.tick/30, d.who.Key, verb, what, c.f[0], c.f[1], c.f[2], c.f[3])
			x.Goals = append(x.Goals, aikit.Goal{Label: label, Score: int32(clamp(c.score, 0, 1<<30)), Chosen: int(d.chosen) == i, X: c.x, Z: c.z})
		}
	}
}

func (e *Economy) Explain(b *core.Board, x *aikit.Explain) {
	explainRing(e.ring[:], e.next, x, "eco", e.s.tick)
	e.explainDefense(b, x)
	x.Notes = append(x.Notes, fmt.Sprintf("economy: %d failed placements so far; considerations shown as [need return travel threat] (permille, 1000 nominal)", e.fails))
}

func (pr *Production) Explain(b *core.Board, x *aikit.Explain) {
	explainRing(pr.ring[:], pr.next, x, "prod", pr.s.tick)
	for _, fi := range b.Factories {
		f := &b.O.Own[fi]
		frame := ""
		for _, i := range b.Frames {
			u := &b.O.Own[i]
			if u.Info.Role.Has(aikit.RoleMobile) && aikit.Dist2(u.X, u.Z, f.X, f.Z) < 128*128 {
				frame = fmt.Sprintf(", building %s %d%%", u.Info.Key, u.Progress)
			}
		}
		fs := pr.s.fstateOf(f)
		if fs.dead {
			frame += ", DEAD (exit walled in)"
		} else if fs.blocked {
			frame += ", PAD BLOCKED"
		}
		if fs.unblocks > 0 {
			frame += fmt.Sprintf(", exit opening sent %d×", fs.unblocks)
		}
		x.Notes = append(x.Notes, fmt.Sprintf("factory %s at (%d,%d): queue %d%s", f.Info.Key, f.X, f.Z, f.QueueLen, frame))
	}
	x.Notes = append(x.Notes, fmt.Sprintf("production: %d blocked pads cleared, %d scouts made", pr.cleared, pr.scouts))
}
