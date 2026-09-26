package utility

import (
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
)

// Strategy sets the posture from the game phase, pressure on the base and
// the relative army estimate. It runs first, so it also refreshes the
// shared model the economy and production layers score against. It also
// owns the per-game style and the persona's ambition (style.go).
type Strategy struct {
	s         *shared
	pr        *Production // read for constructors queued (ambition caps)
	defending bool
	vr        variety
}

// Init applies ambition, the style and the opening jitter to the shared
// Params before the model derives its per-definition tables from them (for
// example unit cost-efficiency depends on v_ref), then sets the model up.
// It runs once, on the first think's tick, before any think.
func (st *Strategy) Init(b *core.Board) {
	if !st.s.init {
		st.vr.begin(b.K, &st.s.p)
	}
	st.s.setup(b.K)
	st.vr.index(st.s)
}

func (st *Strategy) Plan(b *core.Board) {
	s := st.s
	s.p = st.vr.planned
	p := &s.p
	s.observe(b)
	s.observeArmy(b) // army: production against income (army.go)
	s.refillAPM()
	st.vr.watch(b, s)
	st.vr.limit(b, s, st.pr)
	s.spendWeights(b) // expand: factories follow the income (econ_expand.go)

	// Phase: EcoShare slides from eco_early to eco_late over eco_ramp.
	t := int64(b.Tick)
	ramp := int64(p.EcoRamp) * 1800
	phase := int64(p.EcoEarly) - int64(p.EcoEarly-p.EcoLate)*min64(t, ramp)/ramp

	// Pressure: enemy strength near home against ours near home.
	var ownNear int64
	for _, i := range b.Combat {
		u := &b.O.Own[i]
		if aikit.Dist2(u.X, u.Z, b.HomeX, b.HomeZ) < core.BaseRadius*core.BaseRadius {
			ownNear += u.Info.Strength()
		}
	}
	for _, i := range b.Defenses {
		u := &b.O.Own[i]
		if aikit.Dist2(u.X, u.Z, b.HomeX, b.HomeZ) < core.BaseRadius*core.BaseRadius {
			ownNear += u.Info.Strength()
		}
	}
	s.pressure = b.NearHomeThreat * one / (b.NearHomeThreat + ownNear + 1)
	// Hysteresis: enter defense at 400, leave below 200.
	if st.defending {
		st.defending = s.pressure >= 200
	} else {
		st.defending = s.pressure > 400
	}
	adjT := s.pressure * 30 / one

	// Relative army: shift spending to the army when outnumbered.
	s.armyRatio = clamp(s.enEst*one/(int64(b.ArmyValue)+1), 0, 5000)
	adjA := lin(s.armyRatio, 1000, 3000) * 20 / one

	eco := clamp(phase-adjT-adjA, 10, 95)
	s.ecoPhase, s.ecoShare = int32(phase), int32(eco)

	// The attack value (the tactics army's launch threshold) follows the
	// persona's army tier curve; each wave's size is jittered within the
	// style (style.go). Production pushes toward the larger tuned build
	// value, jittered alike: pushing only to the lower launch value built a
	// quarter less army by minute 10 and cost the medium persona points
	// against retail (tactics README, "Attack value calibration").
	enemy := s.enEst * int64(p.AttackRatio) / 100
	attack := clamp(st.vr.wave(b.K, int64(b.ArmyValue), max64(st.vr.attackValue(s, p), enemy)), 0, 1<<30)
	s.armyTarget = attack
	if !st.vr.v.AttNoBuild {
		s.armyTarget = clamp(max64(attack, max64(buildValue(s, p), enemy)*st.vr.waveJit/100), 0, 1<<30)
	}
	aggr := clamp(int64(b.ArmyValue)*100/(int64(b.ArmyValue)+s.enEst+1), 0, 100)

	label := lblPressure
	switch {
	case st.defending:
		label = lblDefend
	case s.minutes < 3:
		label = lblOpening
	case s.armyRatio > 1500:
		label = lblBuildUp
	case eco >= 55:
		label = lblExpand
	}
	b.Posture = core.Posture{
		EcoShare:    int32(eco),
		Aggression:  int32(aggr),
		AttackValue: int32(attack),
		Tech:        s.mInc >= 15000 && s.minutes >= 6,
		Label:       st.vr.labels[label],
	}
}
