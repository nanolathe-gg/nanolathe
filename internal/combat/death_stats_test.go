package combat

import "testing"

func TestComputeDeathCreditCauseGates(t *testing.T) {
	cases := []struct {
		name          string
		in            DeathCreditInput
		loss          bool
		kill          bool
		commanderLoss bool
		commanderKill bool
	}{
		{"ordinary", DeathCreditInput{Cause: CauseOrdinary, VictimOwner: 0, AttackerSide: 1, AttackerPresent: true}, true, true, false, false},
		{"self destruct alliance blocked", DeathCreditInput{Cause: CauseSelfDestruct}, false, false, false, false},
		{"self destruct loss", DeathCreditInput{Cause: CauseSelfDestruct, Cause3LossEligible: true, VictimCommander: true}, true, false, true, false},
		{"reclaim self", DeathCreditInput{Cause: CauseReclaim, VictimOwner: 1, AttackerSide: 1, AttackerPresent: true}, false, false, false, false},
		{"reclaim enemy", DeathCreditInput{Cause: CauseReclaim, VictimOwner: 1, AttackerSide: 0, AttackerPresent: true}, true, true, false, false},
		{"neutral", DeathCreditInput{Cause: CauseOrdinary, VictimOwner: 0, AttackerSide: 10, AttackerPresent: true, VictimCommander: true}, true, false, true, false},
		{"nanoframe", DeathCreditInput{Cause: CauseOrdinary, VictimOwner: 0, AttackerSide: 1, AttackerPresent: true, RemainingFraction: 1, VictimCommander: true}, true, false, true, true},
		{"self commander", DeathCreditInput{Cause: CauseOrdinary, VictimOwner: 1, AttackerSide: 1, AttackerPresent: true, VictimCommander: true}, true, false, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ComputeDeathCredit(tc.in)
			if got.VictimLoss != tc.loss || got.AttackerKill != tc.kill || got.VictimCommanderLoss != tc.commanderLoss || got.AttackerCommanderKill != tc.commanderKill {
				t.Fatalf("credit=%+v, want loss=%v kill=%v commander-loss=%v commander-kill=%v", got, tc.loss, tc.kill, tc.commanderLoss, tc.commanderKill)
			}
		})
	}
}
