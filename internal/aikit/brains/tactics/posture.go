package tactics

import "github.com/nanolathe-gg/nanolathe/internal/aikit/core"

// The strategy's posture (core.Posture) tells the army when to attack
// (AttackValue: the army value at which to launch an offensive) and how
// readily (Aggression 0..100). With Params.Posture on, the army honors it:
//
//   - The main squad launches no offensive — a march on a target zone, or
//     an exploration of an unseen start position — before the army it can
//     attack with reaches the attack value. Until then it takes only
//     clearly undefended targets (a predicted strength ratio of at least
//     the raid squad's margin) and still fights whatever comes within its
//     reach, answers incidents and defends. Once launched, an offensive
//     runs on (chains from cleared target to target) until the squad
//     regroups, retreats or turns to defend; the next one waits for the
//     attack value again.
//   - The square-law prediction gates every engagement as before: the
//     attack value only permits an offensive, it never sends the squad into
//     a fight predicted to lose.
//   - Aggression shifts the engage and retreat margins of every squad by
//     AggrSlope permille per point away from 50, at most aggrMaxShift
//     (the retreat margin by half as much): an army ahead overall accepts
//     narrower local fights, one behind demands wider ones. The engage
//     margin stays above an even fight (the smallest base margin, skill
//     100, is 1200 permille).
//
// The raid, home-guard, escort, air, naval and amphibious squads keep their
// own launch rules. posture=0 restores the army that launched on its own prediction
// alone: every decision below is then skipped and the games are identical.

// defaultAggrSlope is the margin shift per point of aggression away from 50
// (permille of square-law strength). A slope of 4 measured the same as none
// against the unshifted army (README, Results → Attack posture), so the
// shift is off unless pagg= asks for it.
const defaultAggrSlope = 0

// aggrMaxShift bounds the engage-margin shift (permille).
const aggrMaxShift = 150

// softMargin is the predicted ratio at which a target counts as clearly
// undefended: the raid squad's margin.
const softMargin = raidMargin

// Temper is a per-game character the strategy gives the army (utility's
// personality, README §13.15 there): Engage lowers the engage margin
// (permille; negative raises it) and the retreat margin by half as much, as
// the posture's aggression shift does; RaidPct and HarassPct are the percent
// of the value at which the raid squad splits off (RaidOn, raidOnValue by
// default; it folds back at the same proportion of it) and of the harass
// raid's least value (HarassValue). They scale the configured values, so
// raidv= and hv= are scaled too. The neutral temper is {0, 100, 100}.
type Temper struct {
	Engage, RaidPct, HarassPct int64
}

// TemperSource hands the army its temper at Init, after the strategy's
// Init has drawn it (core runs the layers' Init in order).
type TemperSource interface {
	ArmyTemper() Temper
}

// applyTemper folds Params.Temper into the margins and the raid sizes once
// and clears it, so a second Init cannot apply it twice.
func (a *Army) applyTemper() {
	src := a.P.Temper
	if src == nil {
		return
	}
	a.P.Temper = nil
	t := src.ArmyTemper()
	a.P.EngageAdj -= t.Engage
	a.P.RetreatAdj -= t.Engage / 2
	if t.RaidPct > 0 && t.RaidPct != 100 {
		on := a.P.RaidOn
		if on <= 0 {
			on = raidOnValue
		}
		a.P.RaidOn = max(on*t.RaidPct/100, 1)
	}
	if t.HarassPct > 0 && t.HarassPct != 100 {
		a.P.HarassValue = a.P.HarassValue * t.HarassPct / 100
	}
}

// softTargetMargin is the soft margin in play (sm= overrides it).
func (a *Army) softTargetMargin() int64 {
	if a.P.SoftMargin > 0 {
		return a.P.SoftMargin
	}
	return softMargin
}

// offStats counts main-squad offensives for Report and Explain; no decision
// reads it.
type offStats struct {
	n          int64 // offensives begun
	valueSum   int64 // squad value at each start
	first      uint32
	firstValue int64
	firstAV    int64 // the posture's attack value then
	soft       int64 // launches at clearly undefended targets while held
	firstSoft  uint32
	held       int64 // thinks the hold kept the squad from a target it would have attacked
	raids      int64 // raid-squad launches, chains included
	// The attack value, the available army and the main squad's value at
	// the first think past minutes 5, 10 and 15.
	atAV, atAvail, atMain [len(checkMinutes)]int64
	atDone                int
}

// checkMinutes are the instrumentation checkpoints.
var checkMinutes = [...]uint32{5, 10, 15}

// checkpoint records the posture against the army at the checkpoints.
func (a *Army) checkpoint(b *core.Board) {
	st := &a.off
	for st.atDone < len(checkMinutes) && b.Tick >= checkMinutes[st.atDone]*1800 {
		i := st.atDone
		st.atAV[i] = int64(b.Posture.AttackValue)
		st.atMain[i] = a.sq[sqMain].total.value
		st.atAvail[i] = a.squadsValue()
		st.atDone++
	}
}

// aggrShift is how much the posture's aggression lowers the engage margin
// (raises it when negative), permille.
func (a *Army) aggrShift(b *core.Board) int64 {
	if !a.P.Posture || a.P.AggrSlope == 0 {
		return 0
	}
	d := (int64(b.Posture.Aggression) - 50) * a.P.AggrSlope
	if d > aggrMaxShift {
		d = aggrMaxShift
	} else if d < -aggrMaxShift {
		d = -aggrMaxShift
	}
	return d
}

// attackValue is the posture's attack value when the army honors it, else 0.
func (a *Army) attackValue(b *core.Board) int64 {
	if !a.P.Posture {
		return 0
	}
	return int64(b.Posture.AttackValue)
}

// available is the army value measured against the attack value: every
// squad's members — the army the strategy sized the attack value against,
// less scouts and the units that cannot fight now (withdrawn, stuck, or
// called back from goals out of reach) — or with PostureMain the main
// squad's alone.
func (a *Army) available(b *core.Board, s *squad) int64 {
	if a.P.PostureMain {
		return s.total.value
	}
	return a.squadsValue()
}

// squadsValue is the value of every squad's members.
func (a *Army) squadsValue() int64 {
	var v int64
	for i := range a.sq {
		v += a.sq[i].total.value
	}
	return v
}

// postureHold reports whether the posture holds squad s back from an
// offensive: the main squad, not already on one, while the army it can
// attack with is below the attack value.
func (a *Army) postureHold(b *core.Board, s *squad) bool {
	if s.id != sqMain || s.offensive {
		return false
	}
	av := a.attackValue(b)
	return av > 0 && a.available(b, s) < av
}

// noteLaunch records a main-squad launch at the target chooseTarget just
// picked: a clearly undefended target while held, otherwise an offensive
// (which begins here unless one is already under way).
func (a *Army) noteLaunch(b *core.Board, s *squad) {
	st := &a.off
	if s.id == sqRaid {
		st.raids++
	}
	if s.id != sqMain {
		return
	}
	s.soft = a.P.Harass && s.held
	if s.held {
		st.soft++
		if st.firstSoft == 0 {
			st.firstSoft = b.Tick
		}
		return
	}
	if s.offensive {
		return
	}
	s.offensive = true
	st.n++
	st.valueSum += s.total.value
	if st.first == 0 {
		st.first = b.Tick
		st.firstValue = s.total.value
		st.firstAV = int64(b.Posture.AttackValue)
	}
}
