package mission

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// helper to build a VFS from a map of logical paths to TDF content.
// It creates a temp directory, writes files, and mounts it.
func fsFromMap(t *testing.T, files map[string]string) *vfs.FS {
	t.Helper()
	dir := t.TempDir()
	for logical, content := range files {
		// logical uses slash, convert to host path
		full := filepath.Join(dir, filepath.FromSlash(logical))
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatalf("mkdir %q: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatalf("write %q: %v", full, err)
		}
	}
	fs := vfs.New()
	if err := fs.MountDirectory(dir, 10); err != nil {
		t.Fatalf("MountDirectory: %v", err)
	}
	return fs
}

func TestDiscoverContiguousRun(t *testing.T) {
	// Fixture: contiguous MISSION0..2 [08 "Campaign discovery"] C1.
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
	fs := fsFromMap(t, map[string]string{
		"camps/contiguous.tdf": src,
	})
	campaigns, err := Discover(fs)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(campaigns) != 1 {
		t.Fatalf("want 1 campaign got %d", len(campaigns))
	}
	c := campaigns[0]
	if len(c.Missions) != 3 {
		t.Fatalf("contiguous: want 3 missions got %d", len(c.Missions))
	}
	// Deterministic enumeration: indices 0..2, names in order.
	for i, m := range c.Missions {
		if m.Index != i {
			t.Fatalf("mission %d index %d", i, m.Index)
		}
	}
	if c.Missions[0].Name != "First" || c.Missions[1].Name != "Second" || c.Missions[2].Name != "Third" {
		t.Fatalf("names mismatch: %+v", c.Missions)
	}
	// MissionList not required — this campaign has no MissionList and still works.
	// Also check missionfile routed correctly.
	if c.Missions[0].MissionFile != "AC01.ota" {
		t.Fatalf("missionfile 0: %q", c.Missions[0].MissionFile)
	}
}

func TestDiscoverGapStops(t *testing.T) {
	// Fixture: gap at MISSION2 must stop discovery; MISSION3+ ignored. C1.
	src := `
[HEADER]
{
	campaignside=CORE;
}
[MISSION0]
{
	missionfile=CC01.ota;
	missionname=Opening;
}
[MISSION1]
{
	missionfile=CC02.ota;
	missionname=Middle;
}
// MISSION2 missing
[MISSION3]
{
	missionfile=CC04.ota;
	missionname=Should Be Ignored;
}
[MISSION4]
{
	missionfile=CC05.ota;
	missionname=Also Ignored;
}
`
	fs := fsFromMap(t, map[string]string{
		"camps/gap.tdf": src,
	})
	campaigns, err := Discover(fs)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(campaigns) != 1 {
		t.Fatalf("want 1 campaign got %d", len(campaigns))
	}
	c := campaigns[0]
	if len(c.Missions) != 2 {
		t.Fatalf("gap: want 2 missions (0,1) got %d: %+v", len(c.Missions), c.Missions)
	}
	if c.Missions[0].Index != 0 || c.Missions[1].Index != 1 {
		t.Fatalf("gap indices: %+v", c.Missions)
	}
	for _, m := range c.Missions {
		if m.Name == "Should Be Ignored" || m.Name == "Also Ignored" {
			t.Fatalf("gap stopping failed, found ignored mission %q", m.Name)
		}
	}
	// Also verify via DiscoverCampaign single path.
	c2, err := DiscoverCampaign(fs, "camps/gap.tdf")
	if err != nil {
		t.Fatalf("DiscoverCampaign: %v", err)
	}
	if len(c2.Missions) != 2 {
		t.Fatalf("DiscoverCampaign gap: want 2 got %d", len(c2.Missions))
	}
}

func TestDiscoverMissionListTolerated(t *testing.T) {
	// Fixture: MissionList is allocation tag only, not authored wrapper. Must
	// NOT be required, and must be ignored when present. [08 "Campaign discovery"] C1.
	withList := `
[HEADER]
{
	campaignside=ARM;
}
[MissionList]
{
	MissionCount=2;
}
[MISSION0]
{
	missionfile=BT01.ota;
	missionname=Alpha;
}
[MISSION1]
{
	missionfile=BT02.ota;
	missionname=Beta;
}
`
	withoutList := `
[HEADER]
{
	campaignside=ARM;
}
[MISSION0]
{
	missionfile=BT01.ota;
	missionname=Alpha;
}
[MISSION1]
{
	missionfile=BT02.ota;
	missionname=Beta;
}
`
	// Both should discover identically; MissionList must not be counted as mission.
	for _, tc := range []struct {
		name string
		src  string
	}{
		{"with MissionList", withList},
		{"without MissionList", withoutList},
	} {
		fs := fsFromMap(t, map[string]string{
			"camps/test.tdf": tc.src,
		})
		campaigns, err := Discover(fs)
		if err != nil {
			t.Fatalf("%s: Discover: %v", tc.name, err)
		}
		if len(campaigns) != 1 {
			t.Fatalf("%s: want 1 campaign got %d", tc.name, len(campaigns))
		}
		c := campaigns[0]
		if len(c.Missions) != 2 {
			t.Fatalf("%s: want 2 missions got %d", tc.name, len(c.Missions))
		}
		if c.Missions[0].Name != "Alpha" || c.Missions[1].Name != "Beta" {
			t.Fatalf("%s: names %+v", tc.name, c.Missions)
		}
		// Explicitly ensure MissionList not discovered as MISSION
		for _, m := range c.Missions {
			if strings.EqualFold(m.Name, "MissionList") {
				t.Fatalf("%s: MissionList incorrectly counted as mission", tc.name)
			}
		}
		// Verify via Document that MissionList section exists but is ignored.
		if strings.Contains(strings.ToLower(tc.src), "[missionlist]") {
			sec := c.Document.Root.Section("MissionList")
			if sec == nil {
				t.Fatalf("%s: expected MissionList section in raw doc", tc.name)
			}
			// Ensure missions walk still ignored it — already verified len==2.
		}
	}
}

func TestDiscoverUnnamedFallback(t *testing.T) {
	// Fixture: missing missionname must fallback to "unnamed mission". [08 "Campaign discovery"]
	src := `
[HEADER]
{
	campaignside=ARM;
}
[MISSION0]
{
	missionfile=AC01.ota;
}
[MISSION1]
{
	missionfile=AC02.ota;
	missionname=Has Name;
}
`
	fs := fsFromMap(t, map[string]string{
		"camps/missing-name.tdf": src,
	})
	campaigns, err := Discover(fs)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	c := campaigns[0]
	if len(c.Missions) != 2 {
		t.Fatalf("want 2 missions got %d", len(c.Missions))
	}
	if c.Missions[0].Name != "Error -- Unnamed Mission" {
		t.Fatalf("fallback: want \"Error -- Unnamed Mission\" got %q", c.Missions[0].Name)
	}
	if c.Missions[1].Name != "Has Name" {
		t.Fatalf("second name: got %q", c.Missions[1].Name)
	}
}

func TestDiscoverDeterministicEnumeration(t *testing.T) {
	// Two campaigns, names out of order on disk, Discover must sort by logical path.
	// [I1] deterministic iteration.
	srcA := `
[HEADER]{campaignside=ARM;}
[MISSION0]{missionname=A0;}
`
	srcB := `
[HEADER]{campaignside=CORE;}
[MISSION0]{missionname=B0;}
[MISSION1]{missionname=B1;}
`
	// Write them with names that lexically sort differently than creation order.
	// Use non-alphabetical insertion order via map (random) but Discover must sort.
	files := map[string]string{
		"camps/zebra.tdf": srcA,
		"camps/alpha.tdf": srcB,
	}
	fs := fsFromMap(t, files)
	campaigns, err := Discover(fs)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(campaigns) != 2 {
		t.Fatalf("want 2 campaigns got %d", len(campaigns))
	}
	// Must be sorted by Path (logical lowercased).
	if campaigns[0].Path != "camps/alpha.tdf" || campaigns[1].Path != "camps/zebra.tdf" {
		t.Fatalf("deterministic sort failed: got %q, %q", campaigns[0].Path, campaigns[1].Path)
	}
	// Also missions within each campaign must be index-ordered.
	if campaigns[0].Missions[0].Index != 0 || campaigns[0].Missions[1].Index != 1 {
		t.Fatalf("missions not index-ordered")
	}
	// Verify Name derived from base without extension, preserving original case via EntryInfo.Name
	// Both files have lower-case names already; check not empty.
	for _, c := range campaigns {
		if c.Name == "" {
			t.Fatalf("campaign Name empty for %q", c.Path)
		}
		if c.Document == nil || len(c.Document.Root.Sections()) == 0 {
			t.Fatalf("campaign Document missing for %q", c.Path)
		}
	}
	// Ensure sorting is stable across multiple Discover calls.
	campaigns2, err := Discover(fs)
	if err != nil {
		t.Fatalf("second Discover: %v", err)
	}
	if len(campaigns2) != len(campaigns) {
		t.Fatalf("second Discover count mismatch")
	}
	for i := range campaigns {
		if campaigns[i].Path != campaigns2[i].Path || campaigns[i].Name != campaigns2[i].Name {
			t.Fatalf("second Discover not stable at %d", i)
		}
		// Missions must also be identical.
		if len(campaigns[i].Missions) != len(campaigns2[i].Missions) {
			t.Fatalf("mission count unstable")
		}
	}
}

func TestDiscoverFromTestdata(t *testing.T) {
	// Walk the real testdata/camps/*.tdf via VFS mount to ensure the fixture
	// files on disk are correctly discovered. This satisfies the requirement that
	// "fixture TDF bytes you author under internal/mission/testdata/..."
	_, filename, _, _ := runtime.Caller(0)
	testdataDir := filepath.Join(filepath.Dir(filename), "testdata")
	if _, err := os.Stat(testdataDir); err != nil {
		t.Skipf("testdata not found at %q: %v", testdataDir, err)
	}
	fs := vfs.New()
	if err := fs.MountDirectory(testdataDir, 10); err != nil {
		t.Fatalf("MountDirectory testdata: %v", err)
	}
	campaigns, err := Discover(fs)
	if err != nil {
		t.Fatalf("Discover testdata: %v", err)
	}
	if len(campaigns) == 0 {
		t.Fatalf("testdata Discover: want at least 1 campaign, got 0 (files: %v)", listTestdataFiles(t, testdataDir))
	}
	// Expect our four fixture files to be present (contiguous, gap, with-missionlist, missing-name)
	// Use relationships not census: check each expected logical name exists and its mission count meets spec.
	byPath := make(map[string]Campaign)
	for _, c := range campaigns {
		byPath[c.Path] = c
	}
	// contiguous.tdf: 3 missions
	if c, ok := byPath["camps/contiguous.tdf"]; ok {
		if len(c.Missions) != 3 {
			t.Fatalf("contiguous.tdf: want 3 missions got %d", len(c.Missions))
		}
	} else {
		t.Fatalf("contiguous.tdf not discovered; have %v", keys(byPath))
	}
	// gap.tdf: gap at 2 => 2 missions
	if c, ok := byPath["camps/gap.tdf"]; ok {
		if len(c.Missions) != 2 {
			t.Fatalf("gap.tdf: want 2 missions (stop at first gap) got %d", len(c.Missions))
		}
	} else {
		t.Fatalf("gap.tdf not discovered")
	}
	// with-missionlist.tdf: MissionList ignored, 2 missions
	if c, ok := byPath["camps/with-missionlist.tdf"]; ok {
		if len(c.Missions) != 2 {
			t.Fatalf("with-missionlist.tdf: want 2 missions got %d", len(c.Missions))
		}
		if sec := c.Document.Root.Section("MissionList"); sec == nil {
			t.Fatalf("with-missionlist.tdf: expected MissionList section in raw doc")
		}
	} else {
		t.Fatalf("with-missionlist.tdf not discovered")
	}
	// missing-name.tdf: first mission fallback
	if c, ok := byPath["camps/missing-name.tdf"]; ok {
		if len(c.Missions) != 2 {
			t.Fatalf("missing-name.tdf: want 2 missions got %d", len(c.Missions))
		}
		if c.Missions[0].Name != "Error -- Unnamed Mission" {
			t.Fatalf("missing-name fallback: got %q", c.Missions[0].Name)
		}
	} else {
		t.Fatalf("missing-name.tdf not discovered")
	}
	// Deterministic: sorted order
	sorted := make([]string, 0, len(campaigns))
	for _, c := range campaigns {
		sorted = append(sorted, c.Path)
	}
	sortedCopy := append([]string(nil), sorted...)
	sort.Strings(sortedCopy)
	for i := range sorted {
		if sorted[i] != sortedCopy[i] {
			t.Fatalf("testdata campaigns not sorted: %v vs %v", sorted, sortedCopy)
		}
	}
}

func TestDiscoverAssetGuarded(t *testing.T) {
	// Asset-guarded optional walk of camps/Arm Campaign.tdf if cheap.
	// Uses relationships not census: checks header + contiguous run, not exact counts.
	root := retailRoot(t)
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Fatalf("MountGameDirectory %q: %v", root, err)
	}
	campaigns, err := Discover(fs)
	if err != nil {
		t.Fatalf("Discover retail: %v", err)
	}
	if len(campaigns) == 0 {
		t.Skipf("no campaigns discovered in retail (camps missing?)")
	}
	// Find Arm Campaign (case-insensitive logical path)
	var arm *Campaign
	for i := range campaigns {
		if campaigns[i].Path == "camps/arm campaign.tdf" {
			arm = &campaigns[i]
			break
		}
	}
	if arm == nil {
		// Try case-insensitive search for any arm campaign variant
		for i := range campaigns {
			if strings.Contains(strings.ToLower(campaigns[i].Path), "arm campaign") {
				arm = &campaigns[i]
				break
			}
		}
		if arm == nil {
			t.Skipf("Arm Campaign.tdf not in discovered list; have %v", campaignPaths(campaigns))
		}
	}
	// Retail Arm Campaign.tdf has HEADER + MISSION0..MISSION24 contiguous (25 missions)
	// Check relationships: at least one mission, first mission exists, contiguous indices,
	// and stopping at first gap (next index missing).
	if len(arm.Missions) == 0 {
		t.Fatalf("Arm Campaign: want at least 1 mission got 0")
	}
	for i, m := range arm.Missions {
		if m.Index != i {
			t.Fatalf("Arm Campaign: mission index %d != position %d", m.Index, i)
		}
		if m.Name == "" {
			t.Fatalf("Arm Campaign: mission %d has empty name", i)
		}
		if m.MissionFile == "" {
			t.Fatalf("Arm Campaign: mission %d has empty missionfile", i)
		}
	}
	// Verify gap behavior: after last discovered mission, next MISSION%d must be missing.
	nextSec := arm.Document.Root.Section(strings.ToUpper(missionKey(len(arm.Missions))))
	if nextSec != nil {
		t.Fatalf("Arm Campaign: expected gap after %d missions but found %q", len(arm.Missions), missionKey(len(arm.Missions)))
	}
	// Verify HEADER exists
	if sec := arm.Document.Root.Section("HEADER"); sec == nil {
		t.Fatalf("Arm Campaign: missing HEADER section")
	}
	// Verify MissionList not required: retail file has no MissionList section (allocation tag only)
	// So ensure we didn't mistakenly require it.
	// It's okay if it doesn't exist; we already discovered without it.
	if sec := arm.Document.Root.Section("MissionList"); sec != nil {
		t.Logf("Arm Campaign: unexpected MissionList section found (should be allocation tag only) - but tolerated")
	}
	// Check campaign Name and Path preservation
	if arm.Name == "" || arm.Path == "" || arm.OriginalPath == "" {
		t.Fatalf("Arm Campaign provenance missing: Name=%q Path=%q Original=%q", arm.Name, arm.Path, arm.OriginalPath)
	}
	// Relationships not census: verify that every mission's section is the same object as
	// Document's MISSION%d lookup (identity).
	for _, m := range arm.Missions {
		lookup := arm.Document.Root.Section(missionKey(m.Index))
		if lookup != m.Section {
			t.Fatalf("Arm Campaign: mission %d section mismatch", m.Index)
		}
	}
}

func retailRoot(t *testing.T) string {
	t.Helper()
	return testsupport.RetailRoot(t)
}

func campaignPaths(cs []Campaign) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Path
	}
	sort.Strings(out)
	return out
}

func missionKey(i int) string {
	// MISSION%d case as authored in retail; lookup is case-insensitive.
	return "MISSION" + strconv.Itoa(i)
}

func keys(m map[string]Campaign) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func listTestdataFiles(t *testing.T, dir string) []string {
	t.Helper()
	camps := filepath.Join(dir, "camps")
	ents, err := os.ReadDir(camps)
	if err != nil {
		return []string{err.Error()}
	}
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	return out
}
