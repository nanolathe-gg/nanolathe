package ebitenapp

import "testing"

func TestMonitorIdentitySurvivesOtherDisplayRemoval(t *testing.T) {
	before := monitorID([]string{"DP-3", "eDP-1"}, 1)
	after := monitorID([]string{"eDP-1"}, 0)
	if before != after {
		t.Fatalf("removing external display changed laptop identity: %q != %q", before, after)
	}
}

func TestMonitorIdentityDistinguishesSameNames(t *testing.T) {
	names := []string{"Generic PnP Monitor", "Other", "Generic PnP Monitor"}
	first, second := monitorID(names, 0), monitorID(names, 2)
	if first == second {
		t.Fatalf("same-name displays collide: %q", first)
	}
	if second != monitorID([]string{names[0], names[2]}, 1) {
		t.Fatal("removing a differently named display changed duplicate identity")
	}
}
