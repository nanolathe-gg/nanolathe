//go:build retail

package session

// The `aiprofile` name is a `[Schema N]` key read with the chosen schema
// current [02 R-MAP-01 §5] row 7 [08 R-CAMP-01 §2] [08 R-AI-01 §12]. Reading
// it from `[GlobalHeader]` returned the accessor default for the whole stock
// corpus — no reference .ota authors it there — so every battle silently ran
// `ai/default.txt`.
//
// These two cases are the ones a regression would be invisible in: the
// campaign profile changes what the computer player may build at all, and the
// sea maps' profile reweights whole unit families. Both assert the name the
// battle actually resolved, not the decode of a section.

import (
	"strings"
	"testing"
)

const (
	// AC01 is the first Arm campaign mission; `camps/Arm Campaign.tdf`
	// MISSION0 selects it. Its Easy and Medium schemas name MISSIONS and its
	// Hard schema names DEFAULT, so difficulty 0 (which tries Easy first,
	// [08 "Schema choice"]) must land on MISSIONS.
	aiProfileCampaignMission = "camps/Arm Campaign.tdf:MISSION0"
	aiProfileCampaignName    = "MISSIONS"
	// Coast To Coast authors exactly one schema, `Network 1`, naming
	// SeaBattle. It is a plain stock skirmish map, not a fixture.
	aiProfileSeaMap  = "Coast To Coast"
	aiProfileSeaName = "SeaBattle"
)

func TestRetailCampaignAIProfileComesFromTheSelectedSchema(t *testing.T) {
	f := loadRetailFixture(t)
	s, err := NewMissionWithEntryOptions(f.fs, f.cat, aiProfileCampaignMission, 0, 7, 7, MissionEntryOptions{}, nil)
	if err != nil {
		t.Skipf("stock campaign %q is unavailable: %v", aiProfileCampaignMission, err)
	}
	if got := battleAIProfileName(s.Mission); got != aiProfileCampaignName {
		t.Fatalf("campaign profile name %q, want %q from the selected schema %q [02 R-MAP-01 §5]", got, aiProfileCampaignName, s.Mission.Schema.Name)
	}
	// The manager is what consumes it; a name that resolves to no file would
	// still come back as the `default` record through the established
	// fallback, so this also proves ai\MISSIONS.txt was found under its
	// authored casing [08 R-AI-01 §12].
	mgr := s.AI[1]
	if mgr == nil || mgr.Profile == nil {
		t.Fatal("the campaign computer slot must own a manager with a profile [08 R-ENTRY-01 §3 step 24]")
	}
	if got := mgr.Profile.Name(); got != aiProfileCampaignName {
		t.Fatalf("computer slot runs profile %q, want %q [02 R-MAP-01 §5][08 R-AI-01 §12]", got, aiProfileCampaignName)
	}
}

func TestRetailSkirmishAIProfileComesFromTheSelectedSchema(t *testing.T) {
	f := loadRetailFixture(t)
	cfg := f.cfg
	cfg.MapName = aiProfileSeaMap
	s, err := NewSkirmishWithProgress(f.fs, f.cat, cfg, nil)
	if err != nil {
		t.Skipf("stock skirmish map %q is unavailable: %v", aiProfileSeaMap, err)
	}
	if got := battleAIProfileName(s.Mission); !strings.EqualFold(got, aiProfileSeaName) {
		t.Fatalf("skirmish profile name %q, want %q from the selected schema %q [02 R-MAP-01 §5]", got, aiProfileSeaName, s.Mission.Schema.Name)
	}
	mgr := s.AI[1]
	if mgr == nil || mgr.Profile == nil {
		t.Fatal("the skirmish computer slot must own a manager with a profile [08 R-ENTRY-01 §3 step 24]")
	}
	// The archive holds `ai/SeaBattle.TXT`; the name resolves through the
	// VFS's case-folded lookup, so a profile whose authored casing differs
	// from the file's must not silently degrade to `default`.
	if got := mgr.Profile.Name(); strings.EqualFold(got, "default") {
		t.Fatalf("skirmish computer slot degraded to the default profile; want %q [02 R-MAP-01 §5][08 R-AI-01 §12]", aiProfileSeaName)
	}
	if got := mgr.Profile.Name(); !strings.EqualFold(got, aiProfileSeaName) {
		t.Fatalf("skirmish computer slot runs profile %q, want %q [02 R-MAP-01 §5]", got, aiProfileSeaName)
	}
}
