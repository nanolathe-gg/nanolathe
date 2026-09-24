package session

import (
	"sort"

	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// Extra deposits near the start site (DESIGN_SURVIVAL §4.5). Stock metal
// patches are indestructible metal-bearing features whose metal the map-load
// deposit pass writes into the cells under them [05 R-FEAT-01 §7], so a
// Survival battle adds copies of the map's own most common deposit feature
// around the start site before that pass runs. They look and mine exactly like
// the map's authored patches.
const (
	depositNear      = 24 // cells: "near" a start position or the start site
	depositMinRadius = 6  // cells from the start site: leave the commander room
	depositMaxRadius = 28
	depositGap       = 2 // clear cells kept around each new deposit
	depositMax       = 12
)

// survivalSeedDeposits stamps the extra deposits. It runs inside the mission
// feature pass's window, after every authored feature and before the deposit
// pass, and draws nothing.
func (s *Session) survivalSeedDeposits(cfg SkirmishConfig, m *mission.Mission) {
	st := s.Survival
	if st == nil || s.World == nil || s.Features == nil || s.Catalog == nil {
		return
	}
	t := s.World
	isDeposit := func(cx, cz int32) *content.FeatureDef {
		def, ok := t.FeatureDefAt(t.Plot[int(cz*t.CellW+cx)].Feature())
		if !ok || def == nil || def.Metal == 0 || !def.Indestructible {
			return nil
		}
		return def
	}
	// The map's own most common deposit, ties to the lower key.
	counts := map[*content.FeatureDef]int{} // lookup and a sorted pass only
	var defs []*content.FeatureDef
	for cz := int32(0); cz < t.CellH; cz++ {
		for cx := int32(0); cx < t.CellW; cx++ {
			if def := isDeposit(cx, cz); def != nil {
				if counts[def] == 0 {
					defs = append(defs, def)
				}
				counts[def]++
			}
		}
	}
	if len(defs) == 0 {
		return // an all-metal or deposit-free map gets none
	}
	sort.SliceStable(defs, func(i, j int) bool {
		if counts[defs[i]] != counts[defs[j]] {
			return counts[defs[i]] > counts[defs[j]]
		}
		return defs[i].CanonicalKey < defs[j].CanonicalKey
	})
	deposit := defs[0]
	near := func(x, z int32) int {
		n := 0
		for cz := max(z-depositNear, 0); cz <= min(z+depositNear, t.CellH-1); cz++ {
			for cx := max(x-depositNear, 0); cx <= min(x+depositNear, t.CellW-1); cx++ {
				if isDeposit(cx, cz) != nil {
					n++
				}
			}
		}
		return n
	}
	// How many a start usually has: the median over the authored starts. Only
	// an anchor cell holds a feature index (fringe cells hold a sentinel), so
	// these are deposits, not cells.
	var perStart []int
	for _, sp := range m.Specials {
		if sp.Kind == 1 {
			perStart = append(perStart, near(int32(sp.X)/16, int32(sp.Z)/16))
		}
	}
	if len(perStart) == 0 {
		return
	}
	sort.Ints(perStart)
	want := perStart[len(perStart)/2] * len(st.team)
	have := near(st.centreX, st.centreZ)
	st.depositWant, st.depositHave = want, have
	add := min(want-have, depositMax)
	if add <= 0 {
		return
	}
	mex := s.survivalFirstExtractor(cfg)
	placed := 0
	s.World.RunMissionFeaturePass(func() {
		placed = s.survivalStampDeposits(deposit, mex, add)
	})
	st.deposits = placed
}

// survivalStampDeposits places up to add deposits on evenly spaced bearings
// from the start site. Each takes the first fit found by widening the radius
// and then swinging the bearing out to ±45° in 11.25° steps, so rough ground
// on one bearing does not lose its deposit.
func (s *Session) survivalStampDeposits(deposit *content.FeatureDef, mex *content.UnitDef, add int) int {
	st := s.Survival
	placed := 0
	for k := 0; k < add; k++ {
		base := uint32(k) * 65536 / uint32(add)
	search:
		for swing := 0; swing <= 8; swing++ {
			offset := int32((swing + 1) / 2 * 2048)
			if swing%2 == 1 {
				offset = -offset
			}
			angle := numeric.Angle(uint16(int32(base) + offset))
			for r := int32(depositMinRadius); r <= depositMaxRadius; r++ {
				cx := st.centreX + numeric.MulRound(numeric.Cos(angle), r)
				cz := st.centreZ + numeric.MulRound(numeric.Sin(angle), r)
				if s.survivalDepositFits(deposit, mex, cx, cz) {
					s.Features.PlaceAt(int(cx), int(cz), deposit)
					st.depositAt = append(st.depositAt, [2]int32{cx, cz})
					placed++
					break search
				}
			}
		}
	}
	return placed
}

// survivalDepositFits reports a clear, buildable spot: the deposit's footprint
// and a depositGap margin hold no feature and no yard, and the side's metal
// extractor could be placed there.
func (s *Session) survivalDepositFits(deposit *content.FeatureDef, mex *content.UnitDef, cx, cz int32) bool {
	st, t := s.Survival, s.World
	fx, fz := max(deposit.FootprintX, 1), max(deposit.FootprintZ, 1)
	for z := cz - depositGap; z < cz+fz+depositGap; z++ {
		for x := cx - depositGap; x < cx+fx+depositGap; x++ {
			if x < 0 || z < 0 || x >= t.CellW || z >= t.CellH {
				return false
			}
			if st.startClass.regions.At(x, z) != st.startRegion {
				return false
			}
			cell := t.PlotAt(x, z)
			if cell.Feature() != plotNoFeature || cell.StructureYard() {
				return false
			}
		}
	}
	if mex == nil {
		return true
	}
	wx := numeric.Fixed((cx*16 + fx*8) << 16)
	wz := numeric.Fixed((cz*16 + fz*8) << 16)
	_, _, _, reason := s.checkSpawnPlacement(mex, wx, 0, wz)
	return reason == ""
}

// plotNoFeature is the plot's "no feature" sentinel [02 "Terrain file"].
const plotNoFeature = 0xFFFF

// survivalFirstExtractor is the first metal extractor on the human's
// commander's build menu under the bound rules.
func (s *Session) survivalFirstExtractor(cfg SkirmishConfig) *content.UnitDef {
	commander, err := skirmishCommander(s.Catalog, cfg.Players[0].Side, 0)
	if err != nil {
		return nil
	}
	var rules construction.Rules
	if s.Build != nil {
		rules = s.Build.Rules
	}
	for _, key := range construction.BuildProducts(rules, s.Catalog.BuildMenus[content.CanonicalKey(commander.UnitName)]) {
		if def, ok := s.Catalog.Unit(key); ok && def != nil && def.ExtractsMetal > 0 && def.BMCode == 0 {
			return def
		}
	}
	return nil
}

// SurvivalDeposits lists the anchor cells of the extra deposits and the metal
// byte the deposit pass wrote there, for tests and reports.
func (s *Session) SurvivalDeposits() (cells [][2]int32, metal []uint8) {
	if s == nil || s.Survival == nil || s.World == nil {
		return nil, nil
	}
	for _, c := range s.Survival.depositAt {
		cells = append(cells, c)
		metal = append(metal, s.World.PlotAt(c[0], c[1]).Metal())
	}
	return cells, metal
}
