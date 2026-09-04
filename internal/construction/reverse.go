package construction

// The shared construction step's reverse arm — the arm a negative worker
// quantum selects — lives in sharedStep, because retail has one helper and the
// sign of the quantum picks the arm [05 R-WORK-01 §1]. This file holds only the
// refund's selector ladder, which sharedStep calls.
//
// Retired here (WU-19-101): ReverseStep, ApplyReverse and ReverseCause9. They
// were a second, parallel expression of that arm written while its dispatch
// origin was unknown, and they had no production caller once sharedStep took a
// float32 quantum. The `TODO(question)` they carried — "which order passes a
// negative worker factor is unknown" — is closed: the caller is `GetBuilt`'s
// phase-2 decay wrapper, whose quantum is `−((float)(buildtime × 11) /
// buildcostenergy)` [04 R-ORD-01 §11][05 R-WORK-01 §11]. Their arithmetic also
// disagreed with the traced arm on two points now settled in sharedStep: the
// clamp that kills the frame is the clamp to 1.0, not to zero, and the refund
// is credited to the TARGET's bucket, not the builder's.

// ReverseRefund credits the reverse arm's metal-only refund. It is direct — no
// two-resource admission, no energy credit — and the special-second-state
// discount ties to the TARGET's owner rather than the builder's, with the same
// 0.5/0.7 selector pairing cancel-current uses: selector 0 credits half,
// selector 1 seven tenths, any other value the whole amount
// [05 R-WORK-01 §1][05 "Cancel-current and stop interrupts"].
//
// CORRECTION (WU-19-166): the scaled arms were DEBITS. They read
// `+= refund * -0.5` and `+= refund * -0.7` under the doc comment above, which
// already said "credits half" — the comment and the arithmetic disagreed, and
// the arithmetic was wrong. [05 R-ECO-01 §11] enumerates the discount's
// fourteen sites, names "the reverse-construction refund, which negates its
// value before the gate and credits the same accumulator" as one of them, and
// establishes that every one of the fourteen forms
// `accumulator − contribution × (−0.5)`: the constants are negative and the
// operation is a subtraction, so the scaled arm **adds** half (easy) or seven
// tenths (medium) of the contribution. Reading the constant's sign alone and
// writing `+= contribution × −0.5` "inverts the whole effect: it charges the
// player where retail pays". A computer player on easy or medium whose
// nanoframe decayed was therefore charged half or seven tenths of the metal it
// had already sunk, on every decay visit, instead of being paid it back.
//
// This is the same defect PT3-05 fixed at the sibling cancel-current site in
// factory.go, which cites the same section; that fix did not reach this
// function, and the retired-marker note above missed it while listing two other
// disagreements. The two construction refunds are two distinct sites of the
// family, not one ([05 R-ECO-01 §11] "§3 undercounts by at least four").
func ReverseRefund(builderBucket *float32, refund float32, isSpecial bool, modeSelector int) {
	if builderBucket == nil || refund <= 0 {
		return
	}
	if isSpecial {
		switch modeSelector {
		case 0:
			*builderBucket += refund * 0.5 // credit one half [05 R-ECO-01 §11]
		case 1:
			*builderBucket += refund * 0.7 // credit seven tenths [05 R-ECO-01 §11]
		default:
			*builderBucket += refund
		}
	} else {
		*builderBucket += refund
	}
}
