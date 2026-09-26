package utility

import (
	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/aikit/core"
)

// Metal use at every ambition (metal parameter, README §13.14).
//
// A play test on great divide (medium) found the brain with 5 extractors,
// its metal store empty and its energy store full at minute 9, while 22
// free safe spots stood open, two of them 425 and 1,291 wu from home; it
// had built 8 solar collectors, 6 wind generators and 3 metal makers
// against 7 extractors by minute 10. Three causes:
//
//   - The ambition extractor cap zeroed the extractor (and maker) weight
//     whenever the count reached the persona's share of the top tier's
//     curve, so energy, radar, storage and towers were all an idle builder
//     had left to build, whatever the metal need, and spots next to home
//     waited with the rest.
//   - An energy building's need never falls to zero: need is the
//     reciprocal of coverage, and a full store only cuts it by three
//     quarters. The energy expense the observation reports is what builds
//     request, so while metal is short (builds stall and draw less) the
//     energy coverage reads below one with the store full.
//   - A maker beat an extractor candidate whenever the spot was a little
//     far: the maker's return per cost is higher and it stands at home.
//
// metal is a sum of parts:
//
//   - 1 makers last: no metal maker is started while a free, safe
//     extractor spot on our side stands open that a builder of ours can
//     reach (shared.freeSafe).
//   - 2 energy follows metal: no energy building and no energy storage is
//     started while the energy store is at least nine tenths full and not
//     draining: income covers expense, or metal is short (the expense read
//     is then only requested). The opening lead, energy before the first
//     extractor, is exempt. A builder with nothing worth building assists,
//     guards, reclaims or waits.
//   - 4 home spots: when the ambition extractor cap binds, spots within
//     the commander's leash of home (com_radius, scaled by ambition) stay
//     in the plan; only spots beyond it wait for the curve (and makers,
//     as before).
//
// The extractor cap stays the lower personas' handicap: without it (and
// with the handicap moved to constructors or to a rest between builder
// jobs) easy and medium played nearly as strongly as hard (README §13.14).

// Parts of metal (the parameter is their sum).
const (
	mMakers = 1 << iota // makers last
	mEnergy             // energy follows metal
	mHome               // home spots stay in the plan under the cap
)

// mpart reports whether one part of metal acts.
func (s *shared) mpart(bit int32) bool { return s.p.Metal&bit != 0 }

// metalState is the metal rules' per-think state and the audit's per-game
// state. The audit reads the model and the builders' assignments and never
// feeds a decision.
type metalState struct {
	// Per think (metalThink).
	far  bool // the ambition extractor cap binds: spots beyond home wait
	lead bool // the opening lead holds energy first (limit)

	// Audit.
	makers, makersFree int32 // maker starts; those while a free safe spot stood open
	energy, energyFull int32 // energy starts; those while the store was nine tenths full and metal short
	energyHeldN        int32 // energy starts while part 2's condition held (the opening lead)
	fullEmpty          int64 // ticks with the energy store full and the metal store empty
	mexAt, freeAt      [3]int32
	at                 [3]bool
}

// auditMinutes are the minutes the audit samples extractors held against
// free safe spots.
var auditMinutes = [3]uint32{5, 10, 15}

// energyStored reports an energy store at least nine tenths full.
func (s *shared) energyStored() bool { return s.eCap > 0 && s.eStock*10 >= s.eCap*9 }

// energyFull reports an energy store at least nine tenths full that is not
// draining: income covers expense, or metal is short. The expense the
// observation reports is what builds request; while metal is short they
// stall and draw less, and the store stays full.
func (s *shared) energyFull() bool {
	return s.energyStored() && (s.eInc >= s.eExp || s.metalShort())
}

// metalShort reports metal supply below demand (short-run coverage under
// nominal): metal is what binds.
func (s *shared) metalShort() bool { return s.covM < one }

// makerHeld is part 1: a free safe spot stands open, so no maker.
func (s *shared) makerHeld() bool { return s.mpart(mMakers) && s.freeSafe > 0 }

// energyHeld is part 2: the energy store is full and not draining, so no
// energy building or energy storage (outside the opening lead). It
// follows the reservations made earlier in the think.
func (s *shared) energyHeld() bool {
	return s.mpart(mEnergy) && !s.mt.lead && s.energyFull()
}

// capExtractors is the ambition extractor cap binding (limit): without the
// home part the extractor and maker weights are zero for the think; with
// it makers and spots beyond home wait (outOfPlan).
func (s *shared) capExtractors() {
	s.p.WMaker = 0
	if s.mpart(mHome) {
		s.mt.far = true
		return
	}
	s.p.WMetal = 0
}

// outOfPlan reports a spot beyond the commander's leash of home while the
// ambition extractor cap binds (part 4).
func (s *shared) outOfPlan(b *core.Board, x, z int32) bool {
	if !s.mt.far {
		return false
	}
	r := int64(s.p.ComRadius)
	return aikit.Dist2(x, z, b.HomeX, b.HomeZ) > r*r
}

// metalThink starts a think's metal state (limit, after observe) and
// samples the audit: ticks with the energy store full and the metal store
// empty, and extractors held against free safe spots at the audit minutes.
func (s *shared) metalThink(b *core.Board) {
	mt := &s.mt
	mt.far, mt.lead = false, false
	if s.energyStored() && s.mStock*20 <= s.mCap && s.tick > s.lastThink {
		mt.fullEmpty += int64(s.tick - s.lastThink)
	}
	for i, m := range auditMinutes {
		if !mt.at[i] && s.tick >= m*1800 {
			mt.at[i] = true
			mt.mexAt[i], mt.freeAt[i] = s.mexBuilt, s.freeSafe
		}
	}
}

// metalStarted records an assignment the audit counts.
func (s *shared) metalStarted(kind commitKind) {
	mt := &s.mt
	switch kind {
	case cMaker:
		mt.makers++
		if s.freeSafe > 0 {
			mt.makersFree++
		}
	case cEnergy:
		mt.energy++
		if s.energyStored() && s.metalShort() {
			mt.energyFull++
		}
		if s.energyFull() {
			mt.energyHeldN++
		}
	}
}

// report publishes the audit (Strategy.Report).
func (mt *metalState) report(add func(name string, value int64)) {
	add("metal_makers", int64(mt.makers))
	add("metal_makers_free", int64(mt.makersFree))
	add("metal_energy", int64(mt.energy))
	add("metal_energy_full", int64(mt.energyFull))
	add("metal_energy_held", int64(mt.energyHeldN))
	add("metal_full_empty_ticks", mt.fullEmpty)
	for i, m := range auditMinutes {
		if mt.at[i] {
			add("metal_mex_"+itoa2(m), int64(mt.mexAt[i]))
			add("metal_free_"+itoa2(m), int64(mt.freeAt[i]))
		}
	}
}

// itoa2 spells a minute below 100 with two digits.
func itoa2(m uint32) string {
	return string([]byte{byte('0' + m/10%10), byte('0' + m%10)})
}
