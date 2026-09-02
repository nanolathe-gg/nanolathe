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
func ReverseRefund(builderBucket *float32, refund float32, isSpecial bool, modeSelector int) {
	if builderBucket == nil || refund <= 0 {
		return
	}
	if isSpecial {
		switch modeSelector {
		case 0:
			*builderBucket += refund * -0.5
		case 1:
			*builderBucket += refund * -0.7
		default:
			*builderBucket += refund
		}
	} else {
		*builderBucket += refund
	}
}
