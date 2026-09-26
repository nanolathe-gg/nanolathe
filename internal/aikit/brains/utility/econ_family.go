package utility

import (
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/aikit"
)

// The first factory's family (fac_first, w_fac_first; README §13.5).
//
// util+tac's first factory was the vehicle plant in every game: the family
// is decided by factory quality, which no weight moved. Humans pick kbot
// 53% / vehicle 36% / air 9% on ProTA land maps and, in the stock unit set
// (original-TA 1v1 recordings), vehicle 63% / air 20% / kbot 10% / ship
// 7%. fac_first names a family (1 kbot lab, 2 vehicle plant, 3 air plant,
// 4 shipyard; 0 = none) whose factories rate at least as the best factory
// while every other factory's suitability is divided by w_fac_first
// percent, until the brain owns one of that family (built, framed or
// committed); 5 draws the
// family once per game from the stock shares (the shipyard only where
// ships reach the plausible enemy starts).
//
// A factory's family is the majority family of its combat products: air
// (RoleAir), ship (a water unit or RoleNaval), kbot (the product's
// movement class names a KBOT class, or its editor class TEDClass is KBOT:
// stock kbots mostly author tank movement classes), otherwise vehicle
// (hovercraft included).

const (
	famNone int32 = iota
	famKbot
	famVehicle
	famAir
	famShip
	famDraw // fac_first=5
)

// famShares are the stock-unit human first-factory shares (percent) by
// family: original-TA 1v1 recordings (REPORT.md section 8).
var famShares = [famShip + 1]int32{famKbot: 10, famVehicle: 63, famAir: 20, famShip: 7}

var famNames = [famShip + 1]string{"none", "kbot", "vehicle", "air", "ship"}

// Per-style tilts of famShares (open_fam; Style.famTilt), percent: each
// archetype's first-factory family share (ProTA 1v1 openings, REPORT.md
// section 3) over the whole corpus's (first factories: kbot labs 51%,
// vehicle plants 27%, air plants 11%, shipyards the remaining 11%), for
// the families the archetype's row lists; the others keep 100. The ProTA
// shares themselves are not used: that mod rebalances the units, and in
// the stock unit set human players open with a kbot lab a tenth of the
// time (famShares).
var (
	famTiltExpand = [famShip + 1]int32{famKbot: 116, famVehicle: 93, famAir: 100, famShip: 100} // O1: kbot 59, vehicle 25, ship 11
	famTiltEco    = [famShip + 1]int32{famKbot: 104, famVehicle: 111, famAir: 73, famShip: 91}  // O2: kbot 53, vehicle 30, ship 10, air 8
	famTiltUnits  = [famShip + 1]int32{famKbot: 55, famVehicle: 119, famAir: 173, famShip: 182} // O3: vehicle 32, kbot 28, ship 20, air 19
	famTiltTower  = [famShip + 1]int32{famKbot: 137, famVehicle: 78, famAir: 64, famShip: 100}  // O4: kbot 70, vehicle 21, air 7
	famTiltTwofac = [famShip + 1]int32{famKbot: 63, famVehicle: 26, famAir: 382, famShip: 173}  // O5: air 42, kbot 32, ship 19, vehicle 7
)

// styleShares is the stock family shares tilted by a style (percent
// products, before normalization; Rand.Pick normalizes).
func styleShares(st *Style) [famShip + 1]int32 {
	w := famShares
	for f := famKbot; f <= famShip; f++ {
		if t := st.famTilt[f]; t > 0 {
			w[f] = w[f] * t
		} else {
			w[f] = w[f] * 100
		}
	}
	return w
}

// productFamily is the family of one combat product.
func productFamily(q *aikit.UnitInfo, water bool) int32 {
	switch {
	case q.Role.Has(aikit.RoleAir):
		return famAir
	case water || q.Role.Has(aikit.RoleNaval):
		return famShip
	case q.Def != nil && (strings.Contains(strings.ToUpper(q.Def.MovementClass), "KBOT") || isKbotClass(q.Def.Unknown)):
		return famKbot
	}
	return famVehicle
}

// isKbotClass reads the editor class the unit authors (TEDClass, kept
// among the catalog's inert keys): most stock kbots author a tank movement
// class (armpw: TANKSH2) but TEDClass=KBOT. Looked up by its authored
// spellings, never by ranging the map.
func isKbotClass(unknown map[string]string) bool {
	for _, k := range [...]string{"TEDClass", "tedclass", "TEDCLASS", "TedClass"} {
		if v, ok := unknown[k]; ok {
			return strings.EqualFold(strings.TrimSpace(v), "KBOT")
		}
	}
	return false
}

// setupFamily labels every factory with its family and picks this game's
// family (setup; draws from Kit.Rand only for fac_first=5).
func (s *shared) setupFamily(k *aikit.Kit) {
	s.labelFamilies(k)
	s.firstFam = s.p.FacFirst
	if s.firstFam == famDraw {
		s.firstFam = s.drawFamily(k, famShares)
	}
	s.keepFamily(k)
}

// labelFamilies labels every factory with its family, once.
func (s *shared) labelFamilies(k *aikit.Kit) {
	if s.facFam != nil {
		return
	}
	t := k.Table
	s.facFam = make([]int8, len(t.Units))
	for i, f := range t.Units {
		if f.Role.Has(aikit.RoleFactory) {
			s.facFam[i] = int8(factoryFamily(s, f))
		}
	}
}

// drawFamily draws a family from the shares w (percent by family), never
// the shipyard where ships do not reach the plausible enemy starts and
// never the air plant where vehicles or kbots do: opening with an air
// plant scored 32% of points against the brain without fac_first on the
// land pool (22 games), vehicle and kbot openings 52% and 55%. famNone
// without a generator (a unit test's bare kit) or with nothing left.
func (s *shared) drawFamily(k *aikit.Kit, w [famShip + 1]int32) int32 {
	w = s.gateFamilies(k, w)
	if k.Rand == nil {
		return famNone
	}
	if j := k.Rand.Pick(w[:]); j > 0 {
		return int32(j)
	}
	return famNone
}

// gateFamilies zeroes the shipyard where ships do not reach the plausible
// enemy starts and the air plant where vehicles or kbots do.
func (s *shared) gateFamilies(k *aikit.Kit, w [famShip + 1]int32) [famShip + 1]int32 {
	if !s.familyReaches(k, famShip) {
		w[famShip] = 0
	}
	if s.familyReaches(k, famVehicle) || s.familyReaches(k, famKbot) {
		w[famAir] = 0
	}
	return w
}

// modalFamily is the most likely family of the shares w after the gates
// (open_fam without jitter: no draw), famNone with nothing left.
func (s *shared) modalFamily(k *aikit.Kit, w [famShip + 1]int32) int32 {
	w = s.gateFamilies(k, w)
	best := famNone
	for f := famKbot; f <= famShip; f++ {
		if w[f] > w[best] {
			best = f
		}
	}
	return best
}

// keepFamily drops a shipyard preference whose ships cannot get to the
// enemy (reach_mix: holding the other factories back for it would only
// delay the first factory) and lists the chosen family's factories.
func (s *shared) keepFamily(k *aikit.Kit) {
	if s.firstFam == famShip && !s.familyReaches(k, famShip) {
		s.firstFam = famNone
	}
	s.famFacs = s.famFacs[:0]
	for i := range s.facFam {
		if s.firstFam != famNone && int32(s.facFam[i]) == s.firstFam {
			s.famFacs = append(s.famFacs, int32(i))
		}
	}
}

// familyReaches reports whether a combat product of a family our tree can
// make reaches the plausible enemy starts (the reach_mix prior before any
// scouting: the mean over the starts farther than startNearHome from
// home). Without the terrain model ships reach nothing and land units
// everything.
func (s *shared) familyReaches(k *aikit.Kit, fam int32) bool {
	t := &s.terr
	if !t.ready {
		return fam != famShip
	}
	m := k.Map
	for i, u := range k.Table.Units {
		if !t.inTree[i] || !u.Role.Has(aikit.RoleCombat) || !u.Role.Has(aikit.RoleMobile) || productFamily(u, s.info[i].water) != fam {
			continue
		}
		var sum, cnt int64
		for j, st := range m.Starts {
			if aikit.Dist2(st[0], st[1], m.HomeX, m.HomeZ) < startNearHome*startNearHome || !m.MaybeEnemyStart(j) {
				continue
			}
			sum += int64(t.reachAt[j][i])
			cnt++
		}
		if cnt > 0 && sum/cnt >= strandReach {
			return true
		}
	}
	return false
}

// familySuit applies the family preference to a factory's suitability
// while none of the chosen family is owned, framed or committed: its
// factories rate at least as the best factory on offer (1000), and every
// other factory's suitability is divided by w_fac_first percent. Raising
// the family to the best rather than multiplying it keeps the best
// factory's score, so the first factory comes when it did and only its
// family changes (a multiplier alone also brought it two builds
// earlier); and the floor is what lets a family win that factory quality
// rates far below the best (an air plant rates about a twentieth of a
// vehicle plant on the ground efficiency scale, so no multiplier in range
// could lift it).
func (s *shared) familySuit(p *aikit.UnitInfo, suit int64) int64 {
	if s.firstFam == famNone || int(p.Index) >= len(s.facFam) {
		return suit
	}
	fam := s.firstFam
	var own int32
	for _, i := range s.famFacs {
		own += s.count[i]
	}
	if own > 0 {
		// The follow-up (open_follow): the same family once more, or
		// another family next.
		f := &s.open
		if f.followN == 0 || (own >= f.followN && f.followFam == s.firstFam) {
			return suit
		}
		if f.followFam != s.firstFam {
			for _, i := range f.followFacs {
				if s.count[i] > 0 {
					return suit
				}
			}
			fam = f.followFam
		}
	}
	if int32(s.facFam[p.Index]) != fam {
		return suit * 100 / int64(s.p.WFacFirst)
	}
	return max64(suit, one)
}

// setFollow sets the factory that follows the first (open_follow): 1 a
// second of the first family, 2 a vehicle plant.
func (s *shared) setFollow(mode int32) {
	f := &s.open
	f.followN, f.followFam, f.followFacs = 0, famNone, f.followFacs[:0]
	switch {
	case s.firstFam == famNone || mode == 0:
		return
	case mode == 1:
		f.followN, f.followFam = 2, s.firstFam
	case mode == 2 && s.firstFam != famVehicle:
		f.followN, f.followFam = 1, famVehicle
	default:
		return
	}
	for i := range s.facFam {
		if int32(s.facFam[i]) == f.followFam {
			f.followFacs = append(f.followFacs, int32(i))
		}
	}
}
