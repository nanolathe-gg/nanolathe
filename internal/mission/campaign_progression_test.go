package mission

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/vfs"
)

// helper same as catalog_test.go fsFromMap but local to this package test.

func progressionFS(t *testing.T, files map[string]string) *vfs.FS {
	t.Helper()
	dir := t.TempDir()
	for logical, data := range files {
		full := filepath.Join(dir, filepath.FromSlash(logical))
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatalf("mkdir %q: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(data), 0644); err != nil {
			t.Fatalf("write %q: %v", full, err)
		}
	}
	fs := vfs.New()
	if err := fs.MountDirectory(dir, 10); err != nil {
		t.Fatalf("MountDirectory: %v", err)
	}
	return fs
}

func TestNextCampaignMissionLinearAdvance(t *testing.T) {
	// Authored testdata: contiguous 0..2 [08 "Campaign discovery"] C1; next after 0 is 1, after 2 is complete.
	src := `
[HEADER]
{
	campaignside=ARM;
}
[MISSION0]
{
	missionfile=AC01.ota;
	missionname=First;
}
[MISSION1]
{
	missionfile=AC02.ota;
	missionname=Second;
}
[MISSION2]
{
	missionfile=AC03.ota;
	missionname=Third;
}
`
	minOTA := "[GlobalHeader]\n{\n[SCHEMA 0]\n{\nType=Easy;\n}\n}\n"
	fs := progressionFS(t, map[string]string{
		"camps/prog.tdf": src,
		"maps/AC01.ota":  minOTA,
		"maps/AC02.ota":  minOTA,
		"maps/AC03.ota":  minOTA,
	})
	// Mission provenance setup via LoadCampaignWithSink to get CampaignPath/Index wired.
	m0, err := LoadCampaignWithSink(fs, "camps/prog.tdf", 0, 0, 0, nil)
	if err != nil {
		t.Fatalf("LoadCampaignWithSink 0: %v", err)
	}
	if m0.CampaignPath != "camps/prog.tdf" || m0.CampaignIndex != 0 || m0.CampaignMissionName != "First" {
		t.Fatalf("provenance m0: path=%q idx=%d name=%q", m0.CampaignPath, m0.CampaignIndex, m0.CampaignMissionName)
	}
	m2, err := LoadCampaignWithSink(fs, "camps/prog.tdf", 2, 0, 0, nil)
	if err != nil {
		t.Fatalf("LoadCampaignWithSink 2: %v", err)
	}
	if m2.CampaignIndex != 2 || m2.CampaignMissionName != "Third" {
		t.Fatalf("m2 identity: idx=%d name=%q", m2.CampaignIndex, m2.CampaignMissionName)
	}
	// Next after 0 exists, is 1 [08 "Campaign discovery"] linear.
	next, ok, err := NextCampaignMission(fs, "camps/prog.tdf", 0)
	if err != nil {
		t.Fatalf("Next after 0: %v", err)
	}
	if !ok || next != 1 {
		t.Fatalf("Next after 0: ok=%v next=%d want 1", ok, next)
	}
	next, ok, err = NextCampaignMission(fs, "camps/prog.tdf", 1)
	if err != nil || !ok || next != 2 {
		t.Fatalf("Next after 1: ok=%v next=%d err=%v", ok, next, err)
	}
	_, ok, err = NextCampaignMission(fs, "camps/prog.tdf", 2)
	if err != nil {
		t.Fatalf("Next after 2 err: %v", err)
	}
	if ok {
		t.Fatalf("Next after 2 should be complete (no next) [08 \"Campaign discovery\"] gap termination")
	}
	// Gap handling: gap at 2 => only 0,1 exist.
	gapSrc := `
[HEADER]{campaignside=CORE;}
[MISSION0]{missionfile=CC01.ota; missionname=A;}
[MISSION1]{missionfile=CC02.ota; missionname=B;}
[MISSION3]{missionfile=CC04.ota; missionname=Ignored;}
`
	fs2 := progressionFS(t, map[string]string{"camps/gap2.tdf": gapSrc})
	_, ok, _ = NextCampaignMission(fs2, "camps/gap2.tdf", 1)
	if ok {
		t.Fatalf("gap campaign next after 1 should be complete (first gap) [08 \"Campaign discovery\"]")
	}
	_, ok, _ = NextCampaignMission(fs2, "camps/gap2.tdf", 0)
	if !ok {
		t.Fatalf("gap campaign next after 0 should be 1")
	}
	// Deterministic: same discovery twice yields same.
	for i := 0; i < 2; i++ {
		n, ok2, _ := NextCampaignMission(fs, "camps/prog.tdf", 0)
		if !ok2 || n != 1 {
			t.Fatalf("deterministic iteration %d: %v %d", i, ok2, n)
		}
	}
}

func TestCampaignMissionCountDeterministic(t *testing.T) {
	src := `
[HEADER]{campaignside=ARM;}
[MISSION0]{missionfile=A.ota;}
[MISSION1]{missionfile=B.ota;}
`
	minOTA := "[GlobalHeader]\n{\n[SCHEMA 0]\n{\nType=Easy;\n}\n}\n"
	fs := progressionFS(t, map[string]string{
		"camps/c.tdf": src,
		"maps/A.ota":  minOTA,
		"maps/B.ota":  minOTA,
	})
	first, err := DiscoverCampaign(fs, "camps/c.tdf")
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(first.Missions) != 2 {
		t.Fatalf("count %d want 2", len(first.Missions))
	}
	second, err := DiscoverCampaign(fs, "camps/c.tdf")
	if err != nil {
		t.Fatalf("discover again: %v", err)
	}
	if len(second.Missions) != len(first.Missions) {
		t.Fatalf("count not deterministic")
	}
	// Provenance retained in Mission.
	m, err := LoadCampaignWithSink(fs, "camps/c.tdf", 1, 1, 0, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if m.Difficulty != 1 {
		t.Fatalf("difficulty provenance %d", m.Difficulty)
	}
	if m.CampaignMissionName != "Error -- Unnamed Mission" {
		t.Fatalf("mission-name fallback provenance %q", m.CampaignMissionName)
	}
}
