package economy

import (
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// MakerStall is the strict positive-carry gate for makers and extractors
// [R-PROD-01 §5].
func MakerStall(energyCarry float32) bool { return energyCarry > 0 }

// ExtractorProduction is an extractor's per-pass metal: its sampled spot
// yield, or nothing while the maker stall gate holds [R-PROD-01 §5].
func ExtractorProduction(spotMetal, energyCarry float32) float32 {
	if MakerStall(energyCarry) {
		return 0
	}
	return spotMetal
}

// MakerProduction uses the parsed unsigned maker byte as its amount
// [R-PROD-01 §5].
func MakerProduction(makesMetal int32, energyCarry float32) float32 {
	if makesMetal == 0 || MakerStall(energyCarry) {
		return 0
	}
	return float32(uint8(makesMetal))
}

// InitShareThresholds applies the battle initializer's zero writes. These
// fields remain distinct from capacity and are not refreshed by settlement
// [R-SHARE-01 §3].
func InitShareThresholds(p *Player) {
	if p == nil {
		return
	}
	p.MetalShareThreshold = 0
	p.EnergyShareThreshold = 0
}

// WindScalar is the published wind strength generators multiply by, zero
// when no wind field is bound.
func (s *Service) WindScalar() float32 {
	if s == nil || s.Wind == nil {
		return 0
	}
	return s.Wind.Scalar
}

// TidalScalar is the map's tidal strength as a scalar, zero when no terrain
// is bound.
func (s *Service) TidalScalar() float32 {
	if s == nil || s.Terrain == nil {
		return 0
	}
	return s.Terrain.Tidal
}

// addContribution applies the retail signed-contribution discount, or the
// ProTA 4.8 package's computer-player factors when the session's table enables
// AIDifficultyIncome (proTAIncomeCredit, below). The two
// difficulty factors are double constants; the contribution arrives at the
// working precision of [R-ECO-01 §1] and the accumulator store is the ONLY
// narrowing [R-ECO-01 §3][R-ECO-01 §11].
//
// Correction (AU-6). This helper used to narrow its argument to single
// precision on entry (`c := float32(contribution)`) and then run the ladder on
// the widened copy of that float32. For the two sites that hand it a value they
// have just *computed* — the wind product and the tidal product, which
// [R-ECO-01 §2] states are "formed as one multiply and one add with no
// intermediate narrowing" — that was a narrowing retail does not have, and it
// rounds differently in the last single-precision bit. §3 says so in as many
// words: "rounding the product to single first is not bit-identical".
//
// The distinction the callers now carry is which of §3's two contribution
// kinds they hold. An *authored* value is already a single float in the unit
// record [02 "Unit record"], so those call sites narrow once themselves, which
// is the record load, not an extra rounding. A *product just formed* stays at
// working precision all the way into the store here.
func addContribution(s *Service, p *Player, b *Bucket, contribution float64) {
	if b == nil {
		return
	}
	// A computer player marked FullIncome takes the human's plain credit
	// (DESIGN_ECONOMY_CONSTRUCTION "Modern AI full income"): no discount and
	// no package factor.
	if p == nil || !p.Exists || p.ControllerState != 2 || p.FullIncome {
		b.Production = float32(float64(b.Production) + contribution)
		return
	}
	selector := 2
	if s != nil && s.EconomySelector != nil {
		selector = *s.EconomySelector
	}
	if s != nil && s.Community.AIDifficultyIncome {
		b.Production = proTAIncomeCredit(b.Production, contribution, selector)
		return
	}
	// The explicit conversion of each discount product is retail's rounding of
	// that product before the subtraction, two roundings rather than the one a
	// fused multiply-add would perform [05 R-ECO-01 §3][05 R-ECO-01 §11]. The
	// seven-tenths constant is a full binary64 value, so its product is inexact
	// and the difference reaches the settled float32 production store.
	switch selector {
	case 0:
		b.Production = float32(float64(b.Production) - float64(contribution*-0.5))
	case 1:
		b.Production = float32(float64(b.Production) - float64(contribution*-0.7))
	default:
		b.Production = float32(float64(b.Production) + contribution)
	}
}

// proTAIncomeCredit is the ProTA 4.8 package's computer-player credit, used by
// the seven per-unit contribution routes and by both feature-reclaim credits
// when the session's table enables AIDifficultyIncome
// (research/extensions/prota-engine.md "AI and economy evidence audit",
// "shipped 4.8 production and feature-reclaim arithmetic"). The caller has
// already applied the owner's record and computer-control gate. Easy keeps
// retail's one-half, Medium becomes the plain add, and Hard — every selector
// other than zero and one — multiplies by four:
//
//	easy:   float32(p - c * double(-0.5))
//	medium: float32(p + c)
//	hard:   float32(p - c * double(-4))
//
// Working precision is kept until the single-precision store, as in the
// retail ladder above; the explicit conversion of each product is the same
// rounding barrier against a fused multiply-add. No sign or finiteness guard
// is added, and scaling stays per contribution rather than after the total.
func proTAIncomeCredit(production float32, contribution float64, selector int) float32 {
	switch selector {
	case 0:
		return float32(float64(production) - float64(contribution*-0.5))
	case 1:
		return float32(float64(production) + float64(contribution))
	default:
		return float32(float64(production) - float64(contribution*-4))
	}
}

// PerUnitProductionFills performs the per-unit gather in player-slice and
// unit-slot order [R-ECO-01 §2].
func (s *Service) PerUnitProductionFills(player int, w *units.World) {
	if s == nil || w == nil || player < 0 || player >= len(s.Players) {
		return
	}
	p := &s.Players[player]
	ForEachUnitOrdered(w, player, func(u *units.Unit) {
		if u == nil || u.Handle == 0 || u.Def == nil {
			return
		}
		s.ensureUnitBuckets(u.Handle)
		ue := &s.unitBuckets[u.Handle]
		energy := &ue.Buckets[Energy]
		metal := &ue.Buckets[Metal]
		def := u.Def

		// The definition byte selects the building (zero) or mobile (non-zero)
		// branch. Buildings require activation; mobile upkeep runs while
		// activated or moving, but mobile units never reach a generator arm
		// [R-ECO-01 §2].
		//
		// "Moving" is the cached movement-rate tier, non-zero only while the
		// unit is under way on its own mover [04 R-MOV-01 §6] — not the mode
		// mirror, which is non-zero for every parked ground unit and so would
		// bill every mobile definition with an authored `energyuse` for every
		// tick of its life.
		buildingActive := def.BMCode == 0 && u.Activated
		branchActive := buildingActive || (def.BMCode != 0 && (u.Activated || u.MoveTier != 0))
		upkeepAdmitted := false
		if branchActive {
			switch {
			// The refund branch includes unordered values [05 R-ECO-01 §1].
			case !(def.EnergyUse >= 0):
				// The refund is the negated authored value — a single float in
				// the record [02 "Unit record"] — entering the same ladder as
				// every other contribution, with the accumulator store as the
				// only narrowing [R-ECO-01 §2][R-ECO-01 §3].
				//
				// Correction (AU-6). The refund used to be scaled by the
				// difficulty factor into a float32 of its own and that float32
				// added plain, so the value was narrowed twice. That is the
				// factored form §3 warns rounds differently.
				addContribution(s, p, energy, -float64(float32(def.EnergyUse)))
				// A refund never admits the unit's production branch.
				upkeepAdmitted = false
			default:
				upkeepAdmitted = AdmitOneResource(&ue.Buckets, float32(def.EnergyUse))
			}
		}

		if buildingActive {
			switch {
			case def.ExtractsMetal > 0 && upkeepAdmitted:
				addContribution(s, p, metal, float64(ExtractorProduction(u.SpotMetal, energy.Carry)))
			case def.MakesMetal != 0 && upkeepAdmitted:
				addContribution(s, p, metal, float64(MakerProduction(def.MakesMetal, energy.Carry)))
			// Wind and tidal are the two "product just formed" contributions:
			// one multiply of the published scalar by the record's single-float
			// generator value, with no intermediate narrowing before the
			// discount and the store [R-ECO-01 §2][R-ECO-01 §3].
			case def.ExtractsMetal <= 0 && def.MakesMetal == 0 && def.WindGenerator > 0:
				addContribution(s, p, energy, float64(s.WindScalar())*float64(float32(def.WindGenerator)))
			case def.ExtractsMetal <= 0 && def.MakesMetal == 0 && def.TidalGenerator > 0:
				addContribution(s, p, energy, float64(s.TidalScalar())*float64(float32(def.TidalGenerator)))
			}
		}

		if u.Remaining == 0 {
			// Passive production uses completion only, independent of the
			// operational bit [05 "Completed-unit eligibility"]. Both are
			// authored values, single floats in the record [02 "Unit record"],
			// so the record load is their one narrowing.
			addContribution(s, p, energy, float64(float32(def.EnergyMake)))
			addContribution(s, p, metal, float64(float32(def.MetalMake)))
		}

	})
}

// SetEconomySelector installs the economy selector the settlement reads,
// allocating it on first use.
func (s *Service) SetEconomySelector(v int) {
	if s == nil {
		return
	}
	if s.EconomySelector == nil {
		s.EconomySelector = new(int)
	}
	*s.EconomySelector = v
}

// Removed (AU-6): economy.RepairResourceTerm / (*Service).AdmitRepair. They
// were an unreferenced second copy of the repair step's two terms, and the copy
// was wrong twice over against [R-WORK-01 §3]: it clamped a sub-unit term UP to
// one where the traced helper clamps a positive term DOWN to exactly one
// ("if term >= 1 then term = 1", so zero and negative terms survive), and it
// answered `1, 1` for a zero `buildtime` where the traced helper's low-word
// conversion yields `0, 0`. The live repair path is construction.repairTerms,
// which implements §3 correctly; nothing outside this file ever called the
// economy copy.

// creditReclaimedMaterial is the production-credit form both reclaim payouts
// use, cloned from the two sites rather than from the shared contribution
// helper [05 R-ECO-01 §3][05 R-ECO-01 §11]:
//
//	if (!discounted)      production := float32( production + contribution )
//	else if (sel == 0)    production := float32( production - contribution * -0.5 )
//	else if (sel == 1)    production := float32( production - contribution * -0.7 )
//	else                  production := float32( production + contribution )
//
// Three properties of that body matter and are the reason it is written out
// here instead of calling addContribution:
//
//   - the subtraction and the multiply happen at the x87 working precision of
//     [R-ECO-01 §1] and the store is the ONLY narrowing. Scaling into a float32
//     first and adding afterwards — which this file's unit-reclaim refund used
//     to do — narrows twice and rounds differently, which §3 warns about
//     explicitly ("the factored form rounds differently");
//   - neither traced site tests the sign of the contribution, just as the
//     shared contribution helper has no sign gate. No shipped feature authors
//     a negative pool [05 R-WORK-01 §5-A], but adding a guard here would still
//     introduce a predicate the sites do not have;
//   - a selector this build has not been given is the undiscounted path, which
//     is also what the traced ladder does for any selector above one (hard).
func creditReclaimedMaterial(s *Service, b *Bucket, contribution float64, discounted bool) {
	if b == nil {
		return
	}
	selector := 2
	if s != nil && s.EconomySelector != nil {
		selector = *s.EconomySelector
	}
	// Each discount product rounds before the subtraction, as in
	// addContribution above [05 R-ECO-01 §3][05 R-ECO-01 §11].
	if discounted {
		switch selector {
		case 0:
			b.Production = float32(float64(b.Production) - float64(contribution*-0.5))
			return
		case 1:
			b.Production = float32(float64(b.Production) - float64(contribution*-0.7))
			return
		}
	}
	// The conversion of an already-binary64 argument is a rounding barrier, not
	// a width change: callers that form `contribution` as a product would
	// otherwise have that product fused into this addition.
	b.Production = float32(float64(b.Production) + float64(contribution))
}

// specialPlayerSlot reports whether a player slot takes the difficulty-scaled
// production path: the slot's record must exist and its control byte must be 2,
// the computer-controlled value [05 R-ECO-01 §3][05 R-ECO-01 §11]. An owner
// outside the ten slots is not a record and takes the plain path, and so does
// a computer player marked FullIncome (DESIGN_ECONOMY_CONSTRUCTION "Modern AI
// full income").
func (s *Service) specialPlayerSlot(owner uint8) bool {
	if s == nil || int(owner) >= len(s.Players) {
		return false
	}
	p := &s.Players[owner]
	return p.Exists && p.ControllerState == 2 && !p.FullIncome
}

// CreditFeatureReclaim is the credit half of the feature-reclaim payout
// [05 R-WORK-01 §5]: the feature definition's WHOLE energy and metal values are
// added to the reclaiming builder's production accumulators, neither passing
// through an admission helper, as one completion event.
//
// Correction (PT3-05 follow-up). Both additions used to be made with a NIL
// player record, so the computer player's difficulty discount was never applied
// to reclaimed trees, rocks or wrecks, while the sibling unit-reclaim refund
// below did apply it — two credit paths for reclaimed material, one adjusted
// and one not. The trace settles it: the payout reads the builder's player
// record, tests that the record exists and that its control byte is 2, and
// routes EACH of the two additions through the same selector ladder as every
// other member of the family, with the same pairing (selector 0 halves,
// selector 1 takes seven tenths, anything else is the plain add). The omission
// was a defect, not a faithful reading [05 R-ECO-01 §11].
//
// Retail credits ENERGY first and metal second. The two buckets are
// independent, so the order is not observable; it is matched anyway because
// nothing is gained by differing.
//
// builderOwner is required rather than optional on purpose. An owner that a
// caller may omit is an owner a future call site omits by accident, and the
// omission is silent: the credit simply lands on the undiscounted arm, which is
// the exact defect this correction removed. A caller that genuinely has no
// player record — none exists in this build — would name a slot outside the
// ten, which specialPlayerSlot already reports as not special.
func (s *Service) CreditFeatureReclaim(builderHandle pool.Handle, builderOwner uint8, metal, energy float32) {
	if s == nil || builderHandle == 0 {
		return
	}
	s.ensureUnitBuckets(builderHandle)
	b := &s.unitBuckets[builderHandle].Buckets
	discounted := s.specialPlayerSlot(builderOwner)
	if discounted && s.Community.AIDifficultyIncome {
		// The ProTA 4.8 package replaces only this helper's two credits: each
		// takes the package's Easy/Medium/Hard 0.5/1/4 table at the same store
		// boundary, energy before metal. The unit-reclaim refund below and
		// every other credit keep retail's ladder
		// (research/extensions/prota-engine.md "AI and economy evidence audit").
		selector := 2
		if s.EconomySelector != nil {
			selector = *s.EconomySelector
		}
		b[Energy].Production = proTAIncomeCredit(b[Energy].Production, float64(energy), selector)
		b[Metal].Production = proTAIncomeCredit(b[Metal].Production, float64(metal), selector)
		return
	}
	creditReclaimedMaterial(s, &b[Energy], float64(energy), discounted)
	creditReclaimedMaterial(s, &b[Metal], float64(metal), discounted)
}

// CreditUnitReclaimRefund is the death-side metal refund of a lethal cause-5
// reclaim pulse [05 "Unit reclaim"]: `(1 - victim remaining) x victim metal
// build cost`, paid to the killing builder's metal production accumulator, with
// no energy counterpart. The discount applies when the killer's owner is a
// discounted computer player (DiscountsCredit), which the caller reads from
// the owner's record because the killer's slot may already be freed or reused.
//
// Remaining and cost are stored single floats, but subtraction, multiplication,
// discount and accumulation stay at working precision until the final bucket
// store [05 R-WORK-01 §4].
func (s *Service) CreditUnitReclaimRefund(killerHandle pool.Handle, victimRemaining float32, victimBuildCostMetal float32, discounted bool) {
	if s == nil || killerHandle == 0 {
		return
	}
	s.ensureUnitBuckets(killerHandle)
	refund := (1 - float64(victimRemaining)) * float64(victimBuildCostMetal)
	b := &s.unitBuckets[killerHandle].Buckets[Metal]
	creditReclaimedMaterial(s, b, refund, discounted)
}

// DiscountsCredit reports whether owner's credits take the difficulty
// discount: an existing computer player (control byte 2) that is not marked
// FullIncome [05 R-ECO-01 §3][05 R-WORK-01 §4].
func (s *Service) DiscountsCredit(owner uint8) bool { return s.specialPlayerSlot(owner) }

// AdmitStockpile charges one stockpile tick against the builder's buckets
// and reports whether the charge was admitted.
func (s *Service) AdmitStockpile(builderHandle pool.Handle, energyDelta, metalDelta float32) bool {
	if s == nil || builderHandle == 0 {
		return false
	}
	s.ensureUnitBuckets(builderHandle)
	b := &s.unitBuckets[builderHandle].Buckets
	beforeE, beforeM := b[Energy].Accepted, b[Metal].Accepted
	AdmitTwoResource(b, energyDelta, metalDelta)
	return b[Energy].Accepted != beforeE || b[Metal].Accepted != beforeM || (energyDelta == 0 && metalDelta == 0)
}
