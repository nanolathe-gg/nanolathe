package utility

// Tower timing (tower_time; the second round of front rules, README §12.4).
// With the defense plan and the layout switch on, the front rules held 4 /
// 10 / 22 towers at 10 / 15 / 20 minutes (layout-bench mirrors) where
// people hold 5 / 13 / 21 (original-TA winners) and 7 / 15 / 26 (ProTA,
// layout tool): the right number by minute 20, too few before it.
//
// The tower audit (defense_audit.go) says why: before minute 10 a ground
// tower is owed in only a third of the thinks and an owed spell lasts ~23
// s (a constructor is busy; the commander, idle, leaves missile towers to
// it); between 10 and 20 it is owed in two thirds and a spell lasts ~13 s.
// Builders almost always take a tower once they price one (the energy
// wait and the opening metal wait vetoed a few percent of pricings). The
// budget, not the conversion, is what holds the early towers back. Two
// changes:
//
//   - Pacing. The front rules' base share is multiplied by timeEarly until
//     minute 10, falling to 1× by minute 16: people build proportionally
//     more of their towers early (5 by 10 minutes and 13 by 15 of 21 by 20,
//     where the flat share of a growing income gave 4 and 10 of 22).
//   - Banking. The ambition caps' banking rule — a metal store at least
//     bankPercent full while income covers expense raises w_defense for
//     that think while the towers are below the tier's curve — applied
//     only below full ambition (the capped plan's metal); at full ambition
//     the store banked in 13–19% of thinks and nothing spent it. It now
//     applies at every ambition (style.go limit).
//   - More zones. With the faster pacing the plan found no site in a
//     quarter of the owed thinks between minutes 10 and 20 (7 bench games;
//     2% with six zones): the three zones it tried had no free slot. It
//     now tries timeZones.
//   - A ceiling. Paced faster, the plan's bank (up to four light towers
//     owed) and the banking rule carried on past minute 15: 5 / 14 / 28
//     towers stood at 10 / 15 / 20 minutes, and against the same brain
//     without tower timing the paced side took 42.7% of the points [38.0,
//     47.4] (96 games) — the towers came out of its army. No ground tower
//     is owed while the towers standing, framed or ordered reach timeCap
//     of the tier's defense curve (topDefenses at the persona's ambition,
//     the ambition caps' own curve: 7.1 / 13.5 / 19.8 at full ambition).
//
// The commander was measured too: letting it build its light tower when
// the plan was a whole such tower behind put it at forward sites, and the
// paced side's commander was killed twice as often (12 against 6 in those
// 96 games); it still leaves ground towers to its constructors.

// timeEarly is the base share's multiplier (permille) until timeFull,
// falling linearly to 1× at timeFade.
const (
	timeEarly = 1500
	timeFull  = 10 * 1800
	timeFade  = 16 * 1800
)

// timeBoost is the multiplier (permille) on the front rules' base share at
// tick t under tower timing.
func timeBoost(t int64) int64 {
	return one + (timeEarly-one)*lin(t, timeFade, timeFull)/one
}

// timeZones is how many zones pickZone tries under tower timing (3 before).
const timeZones = 6

// timeCap is the ceiling on towers under tower timing, permille of the
// tier's defense curve (topDefenses at the persona's ambition).
const timeCap = 1200

// timeCeiling reports whether the towers standing, framed or ordered have
// reached the ceiling (tower timing): timeCap of the tier's curve, and at
// least one tower. A missile tower counts once.
func (pl *defPlan) timeCeiling(s *shared) bool {
	n := int64(-pl.nDual)
	var framed int64
	for c := dcGround; c < dcCount; c++ {
		n += int64(pl.cnt[c])
		framed += int64(pl.cntF[c])
	}
	// Standing orders overlap the frames their builders are working on.
	n += max64(int64(len(pl.commits))-framed, 0)
	return n*one*one >= max64(topDefenses.at(ambition(&s.k.Persona), s.tick)*timeCap, one*one)
}
