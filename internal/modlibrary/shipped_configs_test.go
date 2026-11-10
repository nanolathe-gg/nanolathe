package modlibrary

import (
	"reflect"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/community"
	contentprofiles "github.com/nanolathe-gg/nanolathe/internal/content/profiles"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
)

// The repository's authored configs for the four hosted mods
// (modconfigs/<release>/nanolathe-mod.json) replaced the engine's built-in
// content profiles and named Community tables. Everything below the
// "frozen" marker is a verbatim replica of that engine data as it stood
// before the move — internal/community/tables.go's table matrix and the
// five profile JSON files' gameplay blocks and limits — kept only so this
// test can prove each config reproduces it exactly. It is test data, not
// engine data, and it must not change.

type shippedConfig struct {
	dir, id, table string
	// legacy is the old profile's gameplay block beyond its table name.
	legacy func(*community.Features)
	// added is what the config gained after the migration, each switch with
	// its evidence: the config resolves to the removed table plus exactly
	// these, and nothing else may differ.
	added func(*community.Features)
	// unitLimit and searchEntries are the old profile's limits.unit_limit and
	// limits.search_entries, which it projected onto the table.
	unitLimit, searchEntries int
	// wantDigest is the resolved digest the old profile produced, recorded
	// from the pre-migration build with community.Resolve(false,
	// profile.GameplaySources()...).
	wantDigest string
	controls   string
	layout     map[string]string
	detect     []string
}

var shippedConfigs = []shippedConfig{
	{
		dir: "prota-4.8", id: "prota", table: "prota", controls: "community",
		legacy: func(f *community.Features) {
			f.AIDifficultyIncome, f.AIStockpileProducts, f.TargetLockRelease, f.AIApplianceEnergy, f.AIBuilderStopThreshold = true, true, true, true, true
			f.WorkingWeaponsAutonomous, f.AttackSingleSlotTake, f.MapFeatureOwnerEleven, f.ResurrectionTextFix = true, true, true, true
		},
		unitLimit: 1500, searchEntries: 66650,
		wantDigest: "66b6f1efeb8b8c5c7f9416c45fddf1d45ee7978d7d168f80850eef11c724189a",
		detect:     []string{"downloadP", "gamedatP", "guiP", "unitpicsP", "weaponP"},
		layout:     map[string]string{"weapons": "weaponP", "gamedata": "gamedatP", "guis": "guiP", "unitpics": "unitpicsP", "download": "downloadP"},
	},
	{
		dir: "escalation-10.2.0", id: "escalation", table: "escalation",
		legacy: func(f *community.Features) {
			f.HealTimeBitmask = true
			f.RepairRate.RepairMultiplier, f.RepairRate.SelfHealMultiplier = 1, 1
		},
		// The patched executable's swapped difficulty name slots
		// (research/extensions/taesc-engine.md "AI"), and Gold's inspected
		// square interception and biased minimap ring (taesc-engine.md
		// "Interceptor coverage and range rings").
		added: func(f *community.Features) {
			f.AIDifficultyReversed = true
			f.AntinukeCircularCoverage = false
		},
		unitLimit: 1000, searchEntries: 66650,
		wantDigest: "d398864376254306cf03d54645a25f08b5803c9bd0727da8b3c999d9690c3ff9",
		detect:     []string{"aE", "downloadsE", "gamedatE", "guiE", "unitpicE", "unitsE", "weaponE"},
		layout:     map[string]string{"units": "unitsE", "weapons": "weaponE", "gamedata": "gamedatE", "guis": "guiE", "unitpics": "unitpicE", "download": "downloadsE", "ai": "aE"},
	},
	{
		dir: "ta-zero-alpha5-20241224", id: "ta-zero", table: "tazero", controls: "zero",
		legacy: func(f *community.Features) {
			f.HealTimeBitmask = true
			f.AIBuilderPlacementLimit = 127
		},
		unitLimit: 1500, searchEntries: 66650,
		wantDigest: "cba3a909d8c0356cc795cced3f562164648b43a0e31d059c4d5c856592a661e0",
		detect:     []string{"ZBuildMenu", "ZGameDat", "ZGui", "ZI", "ZUnitPic", "ZUnits", "ZWeapon"},
		layout:     map[string]string{"units": "ZUnits", "weapons": "ZWeapon", "gamedata": "ZGameDat", "guis": "ZGui", "unitpics": "ZUnitPic", "download": "ZBuildMenu", "ai": "ZI", "music": "tamus"},
	},
	{
		dir: "mayhem-11.3.0", id: "mayhem", table: "mayhem",
		legacy:    func(*community.Features) {},
		unitLimit: 1500, searchEntries: 66650,
		wantDigest: "43e268190cff408e56fa700146d155d2a04272e1604d340d0e430ad9583a5dc3",
		detect:     []string{"downloadsM", "guiM", "unitpicM", "weaponM"},
		layout:     map[string]string{"download": "downloadsM", "guis": "guiM", "unitpics": "unitpicM", "weapons": "weaponM"},
	},
}

func readShippedConfig(t *testing.T, dir string) Metadata {
	t.Helper()
	meta, err := ReadConfigFile(testsupport.ModConfigPath(t, dir))
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	return meta
}

// TestShippedConfigsReproduceTheRemovedTables is the identity proof the
// migration owes: for each mod, the config's communityFeatures resolves to
// exactly the Features value its old named table plus its old profile
// produced, field for field and digest for digest, together with only the
// switches the config declares it gained since (shippedConfig.added), and
// Strict still ignores it.
func TestShippedConfigsReproduceTheRemovedTables(t *testing.T) {
	for _, tc := range shippedConfigs {
		t.Run(tc.id, func(t *testing.T) {
			meta := readShippedConfig(t, tc.dir)
			got, err := community.Resolve(false, meta.CommunitySources()...)
			if err != nil {
				t.Fatal(err)
			}
			want := frozenProfileResolution(t, tc)
			if want.Digest() != tc.wantDigest {
				t.Fatalf("the removed table and profile digest = %s, want the pre-migration %s", want.Digest(), tc.wantDigest)
			}
			if tc.added != nil {
				tc.added(&want)
				if got.Digest() == tc.wantDigest {
					t.Fatal("the declared addition did not change the resolved config identity")
				}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("config resolves to\n %+v\nthe removed table and profile, with the declared additions, resolved to\n %+v", got, want)
			}
			if strict, err := community.Resolve(true, meta.CommunitySources()...); err != nil || strict != (community.Features{}) {
				t.Fatalf("Strict resolved the config's table: %+v, %v", strict, err)
			}
		})
	}
}

// TestShippedConfigsCarryTheRemovedProfiles locks the content sections and
// recommendations against the removed profiles: the directory tables and
// markers the content sets' own archives and configuration files spell, the
// limits their `.ini` files declare (the two read caps are Nanolathe host
// caps), and the Community 3.9 minimum each profile or metadata named.
func TestShippedConfigsCarryTheRemovedProfiles(t *testing.T) {
	for _, tc := range shippedConfigs {
		t.Run(tc.id, func(t *testing.T) {
			meta := readShippedConfig(t, tc.dir)
			if meta.ID != tc.id || meta.Version != strings.TrimPrefix(tc.dir, tc.id+"-") {
				t.Fatalf("identity %s@%s does not match its release directory %s", meta.ID, meta.Version, tc.dir)
			}
			content := meta.Content()
			if content.Name != tc.id || !reflect.DeepEqual(content.Detect, tc.detect) || !reflect.DeepEqual(content.Directories, tc.layout) {
				t.Fatalf("content = %+v", content)
			}
			if want := (contentprofiles.Limits{Units: 16000, Weapons: 16000, TNTBytes: 64 << 20, LOSBytes: 8 << 20}); content.Limits != want {
				t.Fatalf("limits = %+v, want %+v", content.Limits, want)
			}
			rules := meta.Config.Rules
			if rules.MinimumGameplay != "community-3.9" || rules.Gameplay != "community-3.9" || meta.MinimumGameplay != "community-3.9" {
				t.Fatalf("rules = %+v", rules)
			}
			if meta.Controls != tc.controls {
				t.Fatalf("controls preset = %q, want %q", meta.Controls, tc.controls)
			}
			if len(meta.Config.Locks) != 0 {
				t.Fatalf("locks = %v; no shipped config pins a setting", meta.Config.Locks)
			}
			// Escalation and Mayhem name no preset and recommend no settings.
			if tc.controls == "" && (meta.Config.Settings != nil || meta.Config.Keys != nil) {
				t.Fatalf("%s recommends settings it never named", tc.id)
			}
		})
	}
	prota := readShippedConfig(t, "prota-4.8").Content()
	if prota.Presentation.MainMenuVersion != "4.8" {
		t.Fatalf("ProTA main-menu version = %q", prota.Presentation.MainMenuVersion)
	}
	zero := readShippedConfig(t, "ta-zero-alpha5-20241224").Content().Presentation
	if zero.MainMenuBackground != "bitmaps/FrontendZ.pcx" || zero.SinglePlayerBackground != "bitmaps/SingleZbg.pcx" || zero.LoadingBackground != "bitmaps/LoadGameZbg.pcx" || zero.TeamLogos != "textures/LogoZ.gaf" {
		t.Fatalf("TA Zero presentation = %+v", zero)
	}
}

// Gold's installed DLL predates the multiplier addition in the named source
// table [research/extensions/escalation-shields.md "Passive generator healing"].
func TestEscalationConfigHistoricalHealing(t *testing.T) {
	meta := readShippedConfig(t, "escalation-10.2.0")
	got, err := community.Resolve(false, meta.CommunitySources()...)
	if err != nil || !got.HealTimeBitmask || !got.RepairRate.Enabled || got.RepairRate.RepairMultiplier != 1 || got.RepairRate.SelfHealMultiplier != 1 {
		t.Fatalf("Gold historical healing: %+v, %v", got, err)
	}
}

// Gold's authored range rings are interceptor slots whose coverage is the
// firing range plus retail's 512 minimap bias. The identified binaries retain
// square targeting and that display bias (issue #199) [research/extensions/taesc-engine.md
// "Interceptor coverage and range rings"][03 §3.9].
func TestEscalationConfigKeepsRetailInterceptorCoverage(t *testing.T) {
	meta := readShippedConfig(t, "escalation-10.2.0")
	got, err := community.Resolve(false, meta.CommunitySources()...)
	if err != nil || got.AntinukeCircularCoverage {
		t.Fatalf("Gold interceptor coverage: circular=%v, %v", got.AntinukeCircularCoverage, err)
	}
}

// ---------------------------------------------------------------------------
// Frozen: the removed engine data, verbatim. Do not edit.

// frozenProfileResolution resolves an old profile the way the removed
// profiles.Profile.GameplaySources did: the named table, then the legacy
// limit parameters, then the explicit gameplay fields.
func frozenProfileResolution(t *testing.T, tc shippedConfig) community.Features {
	t.Helper()
	table := frozenTable(t, tc.table)
	explicit := table
	tc.legacy(&explicit)
	unitLimit, steps := tc.unitLimit, tc.searchEntries
	f, err := community.Resolve(false,
		community.Overrides{Base: &table},
		community.Overrides{UnitLimit: &unitLimit, PathStepAllowance: &steps},
		frozenExplicitFields(table, explicit),
	)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// frozenExplicitFields expresses the old gameplay block as field overrides:
// every field its legacy function changed.
func frozenExplicitFields(before, after community.Features) community.Overrides {
	var o community.Overrides
	flag := func(changed bool, value bool) *bool {
		if !changed {
			return nil
		}
		return &value
	}
	o.AIDifficultyIncome = flag(before.AIDifficultyIncome != after.AIDifficultyIncome, after.AIDifficultyIncome)
	o.AIStockpileProducts = flag(before.AIStockpileProducts != after.AIStockpileProducts, after.AIStockpileProducts)
	o.TargetLockRelease = flag(before.TargetLockRelease != after.TargetLockRelease, after.TargetLockRelease)
	o.AIApplianceEnergy = flag(before.AIApplianceEnergy != after.AIApplianceEnergy, after.AIApplianceEnergy)
	o.AIBuilderStopThreshold = flag(before.AIBuilderStopThreshold != after.AIBuilderStopThreshold, after.AIBuilderStopThreshold)
	o.WorkingWeaponsAutonomous = flag(before.WorkingWeaponsAutonomous != after.WorkingWeaponsAutonomous, after.WorkingWeaponsAutonomous)
	o.AttackSingleSlotTake = flag(before.AttackSingleSlotTake != after.AttackSingleSlotTake, after.AttackSingleSlotTake)
	o.MapFeatureOwnerEleven = flag(before.MapFeatureOwnerEleven != after.MapFeatureOwnerEleven, after.MapFeatureOwnerEleven)
	o.ResurrectionTextFix = flag(before.ResurrectionTextFix != after.ResurrectionTextFix, after.ResurrectionTextFix)
	o.HealTimeBitmask = flag(before.HealTimeBitmask != after.HealTimeBitmask, after.HealTimeBitmask)
	if before.AIBuilderPlacementLimit != after.AIBuilderPlacementLimit {
		limit := after.AIBuilderPlacementLimit
		o.AIBuilderPlacementLimit = &limit
	}
	if before.RepairRate != after.RepairRate {
		repair, selfHeal := after.RepairRate.RepairMultiplier, after.RepairRate.SelfHealMultiplier
		o.RepairRate = &community.RepairRateOverrides{RepairMultiplier: &repair, SelfHealMultiplier: &selfHeal}
	}
	return o
}

// frozenTable is the removed tables.go matrix for the four hosted mods'
// build profiles. Its raw digests are the ones that file's own test locked.
func frozenTable(t *testing.T, name string) community.Features {
	t.Helper()
	f := community.Features{
		ReclaimToggleKeepsBuild:  true,
		StructureRotation:        true,
		AreaDamageDedupCap:       true,
		TransportedExplosions:    true,
		AntinukeCircularCoverage: true,
		AlliedJammingIgnored:     true,
		ResurrectionFinalization: true,
		WeaponTargetKeys:         true,
		Veterancy:                true,
		SchemaUnits:              true,
		ScriptPorts:              true,
		RepairRate:               community.RepairRate{RepairMultiplier: 1, SelfHealMultiplier: 1},
		ProjectileCapacity:       3000,
		ExplosionCapacity:        3000,
		DebrisCapacity:           1000,
		SfxLimit:                 20480,
		PathStepAllowance:        66650,
		UnitLimit:                1500,
	}
	snap := func(mexDefault, mexMax, wreckDefault, wreckMax int) {
		f.MexSnap, f.WreckSnap = mexMax != 0, wreckMax != 0
		f.MexSnapRadius, f.WreckSnapRadius = mexDefault, wreckDefault
		f.MexSnapRadiusMax, f.WreckSnapRadiusMax = mexMax, wreckMax
	}
	var digest string
	switch name {
	case "prota":
		f.ConstructionKickout, f.GuardingBuildersHold, f.PatrollingBuilderFilters = true, true, true
		f.AreaDamageOverflow, f.GridClaimTieBreak = true, true
		f.OffMapAircraftMarginTiles = 1
		snap(3, 3, 1, 1)
		digest = "936c51eec9338d1ea3e8edcfd27682bf423644433a8224d384730e350de0ca81"
	case "escalation":
		f.ConstructionKickout, f.GuardingBuildersHold, f.PatrollingBuilderFilters = true, true, true
		f.AreaDamageOverflow, f.GridClaimTieBreak = true, true
		f.BuildWeaponSlotGuard, f.AirCorpseFall = true, true
		f.RepairRate = community.RepairRate{Enabled: true, RepairMultiplier: 3, SelfHealMultiplier: 3}
		f.OffMapAircraftMarginTiles = 32
		snap(0, 0, 1, 1)
		digest = "ca05f9962a1b9168f0075395ca47a4795beec10c9d441539aeaf24fd96ee5b3b"
	case "tazero":
		f.ConstructionKickout, f.GuardingBuildersHold, f.PatrollingBuilderFilters = true, true, true
		f.OffMapAircraftMarginTiles = 1
		snap(3, 3, 1, 1)
		digest = "d6d658142e99e20b0e695bfe77627adb56f39651ad1b5fbb6baad524cd797cee"
	case "mayhem":
		f.ConstructionKickout, f.GuardingBuildersHold, f.PatrollingBuilderFilters = true, true, true
		f.AreaDamageOverflow, f.GridClaimTieBreak = true, true
		f.OffMapAircraftMarginTiles = 32
		snap(3, 3, 1, 1)
		digest = "43e268190cff408e56fa700146d155d2a04272e1604d340d0e430ad9583a5dc3"
	default:
		t.Fatalf("no frozen table %q", name)
	}
	if f.Digest() != digest {
		t.Fatalf("frozen table %s digest = %s, want the removed test's %s", name, f.Digest(), digest)
	}
	return f
}
