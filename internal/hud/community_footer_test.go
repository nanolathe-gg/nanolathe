package hud

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func TestCommunityKillsLineUsesPublishedVeteranLevel(t *testing.T) {
	if got := CommunityKillsLine(units.ArmedStatus, 37, 9, true); got != "Vet9" {
		t.Fatalf("CommunityKillsLine = %q, want Vet9 [CP-UD-1]", got)
	}
	if got := CommunityKillsLine(units.ArmedStatus, 37, 9, false); got != "37 kills - Veteran" {
		t.Fatalf("disabled CommunityKillsLine = %q, want retail line", got)
	}
	if got := CommunityKillsLine(units.ArmedStatus, 3, 0, true); got != "3 kills" {
		t.Fatalf("level-zero CommunityKillsLine = %q, want untouched retail line", got)
	}
	if got := CommunityKillsLine(0, 37, 9, true); got != "" {
		t.Fatalf("unarmed CommunityKillsLine = %q, want existing empty gate", got)
	}
}
