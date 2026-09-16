package orders

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// captureAdmissionFixture is workFixture with the status sink attached, so a
// phase-0 refusal can be read back by its code AND its retail text.
func captureAdmissionFixture(t *testing.T) (*units.Unit, *units.Unit, *[]string) {
	t.Helper()
	q, builder, target := workFixture()
	var texts []string
	q.Binding().Presentation = &PresentationAdapter{
		Status: func(_ *units.Unit, _ uint8, text string) bool {
			texts = append(texts, text)
			return true
		},
	}
	return builder, target, &texts
}

// TestCaptureHandlerPhaseZeroAdmissionLadder locks the capture executor's
// phase-0 admission ladder of [05 R-WORK-01 §6] as [05 R-WORK-01 §10] closed
// it. The ladder has exactly five predicates, tested in this order and stopping
// at the first failure:
//
//  1. the order's target handle is non-null — `Capture failed`;
//  2. the builder is still linked — a silent terminal;
//  3. the BUILDER's definition carries `cancapture` — a silent terminal;
//  4. the TARGET's definition does NOT carry `cancapture` —
//     `That unit cannot be captured`. The same bit gates both ends, so
//     anything that can capture cannot be captured;
//  5. the target's remaining construction fraction compares equal to zero —
//     `That unit is a cloud of vapor and cannot be captured`.
//
// Three things it pins that an earlier build got wrong:
//
//   - `Remaining == 0` IS the retail predicate, not a proxy for an idleness
//     sentinel: it is a float32 compare with a literal zero whose only accepted
//     outcome is equal [05 R-WORK-01 §10]. A finished unit carries zero; a
//     nanoframe carries 1.0 and is the "cloud of vapor". There is no idleness,
//     health or order-state test here, so a moving or firing finished unit is
//     captured normally.
//   - the "victim immunity" the build could not locate is predicate 4, the
//     TARGET's own `cancapture` bit — the same bit predicate 3 requires of the
//     builder.
//   - the same-owner and dying-victim rejects the build added are NOT in the
//     ladder. [05 R-WORK-01 §15] says where each one lives instead: the
//     same-owner exclusion is the command resolver's code-13 arm, and the death
//     latch is the ownership transfer's own entry gate. A victim killed this
//     tick — latch set, alive bit still set until the next sweep — passes this
//     ladder and is refused silently at the transfer.
//
// This test moved here from internal/construction, which carried the ladder a
// second time as a caller-less `CaptureEligible`; the executor's own phase 0 is
// the live implementation and is what the vectors now drive.
func TestCaptureHandlerPhaseZeroAdmissionLadder(t *testing.T) {
	const (
		codeAdvance = Code(1)
		codeCancel  = Code(7)
		codeAbandon = Code(8)
	)
	cases := []struct {
		name  string
		setup func(builder, target *units.Unit, n *Node)
		want  Code
		text  string
	}{
		{name: "a finished enemy unit is admitted", want: codeAdvance},
		{
			name:  "1: a null target handle rejects",
			setup: func(_, _ *units.Unit, n *Node) { n.Target = 0 },
			want:  codeAbandon, text: "Capture failed",
		},
		{
			name:  "2: an unlinked builder rejects silently",
			setup: func(b, _ *units.Unit, _ *Node) { b.Def = nil },
			want:  codeCancel,
		},
		{
			name:  "3: a builder without cancapture rejects silently",
			setup: func(b, _ *units.Unit, _ *Node) { b.Def.CanCapture = false },
			want:  codeCancel,
		},
		{
			name:  "4: a target that can itself capture rejects",
			setup: func(_, tg *units.Unit, _ *Node) { tg.Def.CanCapture = true },
			want:  codeAbandon, text: "That unit cannot be captured",
		},
		{
			name:  "5: a nanoframe is a cloud of vapor",
			setup: func(_, tg *units.Unit, _ *Node) { tg.Remaining = 1 },
			want:  codeAbandon, text: "That unit is a cloud of vapor and cannot be captured",
		},
		{
			name:  "5: a partly built target is a cloud of vapor",
			setup: func(_, tg *units.Unit, _ *Node) { tg.Remaining = 0.5 },
			want:  codeAbandon, text: "That unit is a cloud of vapor and cannot be captured",
		},
		// Not in the ladder [05 R-WORK-01 §10]: retail's phase 0 has exactly
		// the five predicates above, so none of these is a reject here.
		{
			name:  "same owner is not a phase-0 reject",
			setup: func(b, tg *units.Unit, _ *Node) { b.Owner, tg.Owner = 3, 3 },
			want:  codeAdvance,
		},
		{
			name:  "a death-latched victim is not a phase-0 reject",
			setup: func(_, tg *units.Unit, _ *Node) { tg.Dying = true },
			want:  codeAdvance,
		},
		{
			name:  "a damaged, moving victim is not a phase-0 reject",
			setup: func(_, tg *units.Unit, _ *Node) { tg.Health = 1; tg.Move.Speed = 1 << 16 },
			want:  codeAdvance,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			builder, target, texts := captureAdmissionFixture(t)
			n := &Node{ID: Lookup("Capture"), Owner: builder.Handle, Target: target.Handle}
			if tc.setup != nil {
				tc.setup(builder, target, n)
			}
			got := captureHandler(builder, n, 0, 0)
			if got != tc.want {
				t.Fatalf("phase-0 code = %d, want %d [05 R-WORK-01 §6][05 R-WORK-01 §10]", got, tc.want)
			}
			if tc.text == "" {
				if len(*texts) != 0 {
					t.Fatalf("a silent terminal published %q [05 R-WORK-01 §10]", *texts)
				}
			} else if len(*texts) != 1 || (*texts)[0] != tc.text {
				t.Fatalf("status text = %q, want [%q] [05 R-WORK-01 §6]", *texts, tc.text)
			}
			if tc.want == codeAdvance && n.Param2 == 0 {
				t.Fatal("an admitted phase 0 must leave the capture budget in p2 [05 R-WORK-01 §6]")
			}
		})
	}

	// Negative zero compares equal to the literal zero and is accepted
	// [05 R-WORK-01 §10]. The negation is a runtime one so the compiler cannot
	// fold it back to +0 the way it folds the constant -0.0.
	builder, target, _ := captureAdmissionFixture(t)
	var zero float32
	target.Remaining = -zero
	n := &Node{ID: Lookup("Capture"), Owner: builder.Handle, Target: target.Handle}
	if got := captureHandler(builder, n, 0, 0); got != 1 {
		t.Fatalf("negative zero compares equal to literal zero and is accepted: code = %d [05 R-WORK-01 §10]", got)
	}
}
