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
	// Census narration must agree with the researched accessor defaults
	// [02 map-global keys][02 §5 R-CONTENT-03][08 mission globals].
	censusDefault := func(key string) *CensusEntry {
		for i := range MissionGlobalCensus {
			if strings.EqualFold(MissionGlobalCensus[i].Key, key) {
				return &MissionGlobalCensus[i]
			}
		}
		return nil
	}
	if e := censusDefault("maxunits"); e == nil || e.Default != "200" {
		t.Fatalf("maxunits census default 200 [02 map-global keys] got %+v", e)
	}
	if e := censusDefault("HumanMetal"); e == nil || e.Type != "int" || e.Default != "0" {
		t.Fatalf("HumanMetal census int/0 [02 map-global keys] got %+v", e)
	}
	if e := censusDefault("killmul"); e == nil || e.Default != "0.0" {
		t.Fatalf("killmul census default 0.0 [02 map-global keys][08 mission globals] got %+v", e)
	}
	if e := censusDefault("numplayers"); e == nil || e.Type != "string" || e.Default != "empty" {
		t.Fatalf("numplayers census string/empty [02 map-global keys][08 mission globals] got %+v", e)
	}
	if e := censusDefault("gravity"); e == nil || e.Default != "0" {
		t.Fatalf("gravity census default 0 [02 map-global keys] got %+v", e)
	}
}

// TestMissionGlobalsDefaults locks the accessor defaults of the map-global key
// table [02 map-global keys]. The canonical-TNT hard-codes (wind 100/2000,
// gravity 0) and the world-init fallbacks (gravity 0x1FDB, tidal 0.5) are
// terrain-consumption concerns [03 §2.2], and the totala.ini UnitLimit 250 is
// a different mechanism from OTA maxunits [02 §5 R-CONTENT-03] — none of them
// belong in this decode.
func TestMissionGlobalsDefaults(t *testing.T) {
	sec := mustParseGlobalsTDF(t, `[GlobalHeader]
{
}
`)
	mg := DecodeMissionGlobals(sec)
	if mg.MinWind != 0 || mg.MaxWind != 0 {
		t.Fatalf("wind accessor defaults 0/0 [02 map-global keys]; canonical TNT hard-codes 100/2000 at world consumption [03 §2.2] got %d/%d", mg.MinWind, mg.MaxWind)
	}
	if mg.Gravity != 0 {
		t.Fatalf("gravity accessor default 0 [02 map-global keys]; 0x1FDB is the world-init fallback when neither source supplies [03 §2.2] got %d", mg.Gravity)
	}
	if mg.TidalStrength != 0 {
		t.Fatalf("tidalstrength accessor default 0.0 [02 map-global keys]; 0.5 is the world-init fallback [03 §2.2] got %f", mg.TidalStrength)
	}
	if mg.HumanMetal != 0 || mg.HumanEnergy != 0 || mg.ComputerMetal != 0 || mg.ComputerEnergy != 0 {
		t.Fatalf("starting resources integer accessor default 0 [02 map-global keys] got %d/%d/%d/%d", mg.HumanMetal, mg.HumanEnergy, mg.ComputerMetal, mg.ComputerEnergy)
	}
	if mg.KillMul != 0.0 || mg.TimeMul != 0.0 {
		t.Fatalf("killmul/timemul 0.0 default [02 map-global keys][08 mission globals] got %f/%f", mg.KillMul, mg.TimeMul)
	}
	if mg.MaxUnits != 200 {
		t.Fatalf("maxunits 200 [02 map-global keys]; 250 is the totala.ini [Preferences] UnitLimit profile default, a different mechanism [02 §5 R-CONTENT-03] got %d", mg.MaxUnits)
	}
	if mg.NumPlayers != "" {
		t.Fatalf("numplayers is a string slot, empty default [02 map-global keys][08 mission globals] got %q", mg.NumPlayers)
	}
	if mg.AIProfile != "" {
		t.Fatalf("aiprofile empty default [02 map-global keys]; ai\\default.txt fallback happens at profile load [08 planner] got %q", mg.AIProfile)
	}
	if mg.Mapping != 0 || mg.LineOfSight != 0 {
		t.Fatalf("mapping/lineofsight accessor defaults 0 [02 map-global keys] got %d/%d", mg.Mapping, mg.LineOfSight)
	}
	if mg.MissionDescription != "No description available" {
		t.Fatalf("missiondescription fallback [02 map-global keys] got %q", mg.MissionDescription)
	}
	if mg.MeteorWeapon != "" || mg.MeteorRadius != 0 || mg.MeteorDensity != 0 || mg.MeteorDuration != 0 || mg.MeteorInterval != 0 {
		t.Fatalf("meteor decode defaults empty/0/0.0 [02 map-global keys]; METEOR.TDF substitution happens at storm resolution [02 meteor merge] got %q/%d/%f/%f/%f", mg.MeteorWeapon, mg.MeteorRadius, mg.MeteorDensity, mg.MeteorDuration, mg.MeteorInterval)
	}
	if mg.UpdateTime != 0 || mg.WinLoseTime != 0 || mg.DisplayTimer != 0 {
		t.Fatalf("sibling timers i32 default 0 [08 player records] got %d/%d/%d", mg.UpdateTime, mg.WinLoseTime, mg.DisplayTimer)
	}
}

// TestMissionGlobalsNilSection: a missing GlobalHeader is fatal upstream [02
// mission-file diagnostics]; the nil path mirrors decoding an empty section.
func TestMissionGlobalsNilSection(t *testing.T) {
	mg := DecodeMissionGlobals(nil)
	if mg == nil {
		t.Fatal("nil section should yield accessor defaults, not nil")
	}
	if mg.MaxUnits != 200 || mg.KillMul != 0 || mg.HumanMetal != 0 {
		t.Fatal("nil section defaults must equal the empty-section decode [02 map-global keys]")
	}
	if mg.MissionDescription != "No description available" {
		t.Fatalf("missiondescription fallback [02 map-global keys] got %q", mg.MissionDescription)
	}
}

// TestMissionGlobalsAuthoritativeDecoding verifies authoritative consumers [P1-02 §2.1].
func TestMissionGlobalsAuthoritativeDecoding(t *testing.T) {
	sec := mustParseGlobalsTDF(t, `[GlobalHeader]
{
    numplayers=2, 4;
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
	if mg.NumPlayers != "2, 4" {
		t.Fatalf("numplayers is the authored string [02 map-global keys][fmt ota] got %q", mg.NumPlayers)
	}
	if mg.HumanMetal != 1500 || mg.HumanEnergy != 2000 {
		t.Fatalf("human resources decode [P1-02 §2.1] got %d/%d", mg.HumanMetal, mg.HumanEnergy)
	}
	if mg.ComputerMetal != 1200 || mg.ComputerEnergy != 1800 {
		t.Fatalf("computer resources decode [02 map-global keys] got %d/%d", mg.ComputerMetal, mg.ComputerEnergy)
	}
	if mg.MaxUnits != 100 {
		t.Fatalf("maxunits authored override [02 map-global keys] got %d", mg.MaxUnits)
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
	if mg.MinWind != 50 || mg.MaxWind != 500 || mg.Gravity != 900 {
		t.Fatalf("authored wind/gravity override the decode defaults [02 map-global keys] got %d/%d/%d", mg.MinWind, mg.MaxWind, mg.Gravity)
	}
	if mg.TidalStrength != 1.5 {
		t.Fatalf("authored tidal override [02 map-global keys] got %f", mg.TidalStrength)
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
