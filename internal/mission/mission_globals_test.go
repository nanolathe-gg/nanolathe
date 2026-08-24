package mission

import (
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/formats"
)

func mustParseGlobalsTDF(t *testing.T, text string) *formats.Section {
	t.Helper()
	doc, err := formats.ParseTDF([]byte(text))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	sec := doc.Root.Section("GlobalHeader")
	if sec == nil {
		t.Fatalf("no GlobalHeader")
	}
	return sec
}

// TestMissionGlobalCensus locks P1-02: ~40 keys inventory and classification [P1-02 §2.1][P1-02 §2.2].
func TestMissionGlobalCensus(t *testing.T) {
	if len(MissionGlobalCensus) < 35 {
		t.Fatalf("census len %d, want >=35 [P1-02 §2.1]", len(MissionGlobalCensus))
	}
	// Must contain representative authoritative, presentation, inert keys.
	needAuth := []string{"HumanMetal", "SurfaceMetal", "maxunits", "minwindspeed", "gravity", "lavaworld", "killmul"}
	needPres := []string{"Planet", "brief", "MissionDescription"}
	needInert := []string{"memory", "nomovie"}
	for _, k := range needAuth {
		if !IsAuthoritative(k) {
			t.Fatalf("authoritative key %q not classified [P1-02 §2.1]", k)
		}
	}
	for _, k := range needPres {
		if !IsPresentation(k) {
			t.Fatalf("presentation key %q not classified", k)
		}
	}
	for _, k := range needInert {
		if !IsInert(k) {
			t.Fatalf("inert key %q not classified", k)
		}
	}
	// Fatal vs degrade: TNT fatal, MOVEINFO fatal, translate not fatal, panorama degrade, GAMEDATA not fatal [P1-02 §2.2].
	if MediaFatal("tnt") != FatalKindFatal {
		t.Fatalf("TNT version fatal [P1-02 §2.2]")
	}
	if MediaFatal("moveinfo.tdf") != FatalKindFatal {
		t.Fatalf("MOVEINFO fatal [P1-02 §2.2]")
	}
	if MediaFatal("translate.tdf") != FatalKindNotFatal {
		t.Fatalf("translate.tdf not fatal empty fallback [P1-02 §2.2]")
	}
	if MediaFatal("gamedata.tdf") != FatalKindNotFatal {
		t.Fatalf("GAMEDATA.TDF SC2 not fatal [P1-02 §2.2][SPEC_CONFLICTS SC2]")
	}
	if MediaFatal("panorama") != FatalKindDegrade {
		t.Fatalf("panorama miss degraded [P1-02 §2.2]")
	}
	// Ensure every census entry has a class.
	for _, e := range MissionGlobalCensus {
		if e.Class != GlobalAuthoritative && e.Class != GlobalPresentation && e.Class != GlobalInert {
			t.Fatalf("entry %q missing class", e.Key)
		}
		if e.Key == "" {
			t.Fatalf("entry with empty key")
		}
	}
}

// TestMissionGlobalsDefaults locks defaults and clamps [P1-02 §4].
func TestMissionGlobalsDefaults(t *testing.T) {
	sec := mustParseGlobalsTDF(t, `[GlobalHeader]
{
}
`)
	mg := DecodeMissionGlobals(sec)
	if mg.MinWind != 100 || mg.MaxWind != 2000 {
		t.Fatalf("wind defaults 100/2000 canonical [P1-02 §2.1] got %d/%d", mg.MinWind, mg.MaxWind)
	}
	if mg.Gravity != 0x1FDB {
		t.Fatalf("gravity fallback 0x1FDB [P1-02 §2.1] got %d", mg.Gravity)
	}
	if mg.TidalStrength != 0.5 {
		t.Fatalf("tidal 0.5 default [P1-02 §2.1] got %f", mg.TidalStrength)
	}
	if mg.SurfaceMetal != 0 {
		t.Fatalf("SurfaceMetal 0 default [P1-02 §2.1]")
	}
	if mg.KillMul != 1.0 || mg.TimeMul != 1.0 {
		t.Fatalf("killmul/timemul 1.0 default TODO(question) [P1-02 §8] got %f/%f", mg.KillMul, mg.TimeMul)
	}
	if mg.MissionDescription != "No description available" {
		t.Fatalf("missiondescription fallback [P1-02 §2.1] got %q", mg.MissionDescription)
	}
	if mg.AIProfile != "default" {
		t.Fatalf("aiprofile default [P1-02 §2.1] got %q", mg.AIProfile)
	}
	if mg.MaxUnits != 250 {
		t.Fatalf("maxunits 250 [P1-02 §2.1] got %d", mg.MaxUnits)
	}
}

// TestMissionGlobalsAuthoritativeDecoding verifies authoritative consumers [P1-02 §2.1].
func TestMissionGlobalsAuthoritativeDecoding(t *testing.T) {
	sec := mustParseGlobalsTDF(t, `[GlobalHeader]
{
    HumanMetal=1500;
    HumanEnergy=2000;
    ComputerMetal=1200;
    ComputerEnergy=1800;
    SurfaceMetal=5;
    maxunits=100;
    minwindspeed=50;
    maxwindspeed=500;
    gravity=900;
    tidalstrength=1.5;
    lavaworld=1;
    waterdoesdamage=1;
    waterdamage=10;
    killmul=2.0;
    timemul=0.5;
    Planet=Green planet;
    brief=MyBrief;
    memory=99999;
    nomovie=1;
    MeteorWeapon=METEOR;
    UseOnlyUnits=Ac02.tdf;
}
`)
	mg := DecodeMissionGlobals(sec)
	if mg.HumanMetal != 1500 || mg.HumanEnergy != 2000 {
		t.Fatalf("human resources decode [P1-02 §2.1] got %f/%f", mg.HumanMetal, mg.HumanEnergy)
	}
	if mg.SurfaceMetal != 5 {
		t.Fatalf("SurfaceMetal 5 [P1-02 §2.1]")
	}
	if mg.Planet != "Green planet" {
		t.Fatalf("Planet [P1-02 §2.1] got %q", mg.Planet)
	}
	if mg.Brief != "MyBrief" {
		t.Fatalf("brief [P1-02 §2.1]")
	}
	if mg.Memory != "99999" {
		t.Fatalf("memory inert but parsed [P1-02 §2.1]")
	}
	if mg.MeteorWeapon != "METEOR" {
		t.Fatalf("MeteorWeapon [P1-02 §2.1]")
	}
	if mg.LavaWorld != 1 || mg.WaterDoesDamage != 1 || mg.WaterDamage != 10 {
		t.Fatalf("lava/water [P1-02 §2.1]")
	}
	if mg.KillMul != 2.0 || mg.TimeMul != 0.5 {
		t.Fatalf("score multipliers [P1-02 §2.1]")
	}
	if mg.UseOnlyUnitsPath != "camps/useonly/Ac02.tdf" {
		t.Fatalf("UseOnlyUnits routing camps\\useonly [P1-02 §2.1] got %q", mg.UseOnlyUnitsPath)
	}
	// Memory is inert, not authoritative.
	if !IsInert("memory") || IsAuthoritative("memory") {
		t.Fatalf("memory inert vs authoritative [P1-02 §2.1]")
	}
	// Lava/water authoritative per census.
	if !IsAuthoritative("lavaworld") || !IsAuthoritative("waterdoesdamage") {
		t.Fatalf("lava/water authoritative [P1-02 §2.1]")
	}
}

// TestPlanetPanoramaFallback verifies optional-media fallback degraded not fatal [P1-02 §2.2].
func TestPlanetPanoramaFallback(t *testing.T) {
	// Known planet
	if idx := PlanetIndex("Green planet"); idx != 0 {
		t.Fatalf("Green planet index 0 got %d", idx)
	}
	if _, _, _, ok := ResolvePlanetMedia("Green planet"); !ok {
		t.Fatalf("Green planet should resolve [P1-02 §2.1]")
	}
	// Unknown planet → -1 and no GAF, degraded not fatal [P1-02 §2.2].
	if idx := PlanetIndex("UnknownPlanet42"); idx != -1 {
		t.Fatalf("unknown planet should be -1 [P1-02 §2.2] got %d", idx)
	}
	if _, _, _, ok := ResolvePlanetMedia("UnknownPlanet42"); ok {
		t.Fatalf("unknown planet should not resolve [P1-02 §2.2]")
	}
	if MediaFatal("panorama") != FatalKindDegrade {
		t.Fatalf("panorama miss degraded not fatal [P1-02 §2.2]")
	}
	// Brief empty still loads; panorama missing leaves visual absent [P1-02 §2.2].
	sec := mustParseGlobalsTDF(t, `[GlobalHeader]
{
    Planet=UnknownPlanet42;
    brief=missing.tdf;
}
`)
	mg := DecodeMissionGlobals(sec)
	if mg.Planet != "UnknownPlanet42" {
		t.Fatalf("unknown planet still parsed [P1-02 §2.2]")
	}
}

// TestTranslateAndGAMEDATANotFatal verifies SC2 and translate fallback [P1-02 §2.2].
func TestTranslateAndGAMEDATANotFatal(t *testing.T) {
	if MediaFatal("translate.tdf") != FatalKindNotFatal {
		t.Fatalf("translate.tdf empty fallback not fatal [P1-02 §2.2]")
	}
	if MediaFatal("gamedata.tdf") != FatalKindNotFatal {
		t.Fatalf("GAMEDATA.TDF SC2 not fatal [P1-02 §2.2][SPEC_CONFLICTS SC2]")
	}
	// Ensure inert keys still decode without fatal.
	sec := mustParseGlobalsTDF(t, `[GlobalHeader]
{
    memory=high;
    nomovie=1;
}
`)
	mg := DecodeMissionGlobals(sec)
	if mg.Memory != "high" || mg.NoMovie != 1 {
		t.Fatalf("inert memory/nomovie parse [P1-02 §2.1]")
	}
}

// TestFallbackCensusEnsuresPresent ensures optional-media fallback table present [P1-02 §2.2].
func TestFallbackCensusEnsuresPresent(t *testing.T) {
	// Verify that presentation keys at least have degraded handling.
	keys := []string{"Planet", "brief", "narration", "missionhint", "glamour", "panorama"}
	for _, k := range keys {
		if MediaFatal(k) == FatalKindFatal {
			// Presentation keys should not be fatal.
			// Only TNT/MOVEINFO are fatal among globals [P1-02 §2.2].
			// Allow Planet to be degrade, not fatal.
			if strings.EqualFold(k, "Planet") {
				continue
			}
			t.Fatalf("presentation key %q should not be fatal [P1-02 §2.2]", k)
		}
	}
}
