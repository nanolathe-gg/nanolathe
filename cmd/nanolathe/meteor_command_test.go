package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/session"
)

func TestMeteorCommandFormsAndSessionMask(t *testing.T) {
	campaign := &battleSession{sess: &session.Session{Mission: &mission.Mission{Type: mission.TypeCampaign}}}
	campaign.dispatchLocalCommand("+Meteor")
	if got := campaign.sess.PendingHumanCommands(); len(got) != 0 {
		t.Fatalf("campaign Meteor crossed mask-2 gate: %+v", got)
	}

	skirmish := &battleSession{sess: &session.Session{}}
	skirmish.dispatchLocalCommand("+Meteor")
	skirmish.dispatchLocalCommand("+Meteor junk")
	skirmish.dispatchLocalCommand("+Meteor -2tail ignored")
	got := skirmish.sess.PendingHumanCommands()
	if len(got) != 3 {
		t.Fatalf("skirmish Meteor commands = %+v, want three", got)
	}
	if got[0].Kind != session.HumanMeteor || got[0].Meteor.ArgumentPresent {
		t.Fatalf("argument-free Meteor = %+v, want forced form", got[0])
	}
	if !got[1].Meteor.ArgumentPresent || got[1].Meteor.Enabled {
		t.Fatalf("nondigit Meteor argument = %+v, want explicit disable", got[1].Meteor)
	}
	if !got[2].Meteor.ArgumentPresent || !got[2].Meteor.Enabled {
		t.Fatalf("negative Meteor argument = %+v, want explicit enable", got[2].Meteor)
	}
}
