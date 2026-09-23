//go:build retail

package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/content/profiles"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// loadProTAArchive mounts the original assets followed by the explicitly
// supplied ProTA roots. The environment variable has the same path-list shape
// as internal/content/profiles' corpus check; CI without the package skips.
func loadProTAArchive(t *testing.T) (vfs.FSOps, *content.Catalog, content.Limits, profiles.Profile) {
	t.Helper()
	value := strings.TrimSpace(os.Getenv("NANOLATHE_MOD_ROOTS_PROTA"))
	if value == "" {
		t.Skip("NANOLATHE_MOD_ROOTS_PROTA is unset: ProTA package acceptance needs the 4.8 archive")
	}
	roots := []string{testsupport.RetailRoot(t)}
	for _, root := range strings.Split(value, string(os.PathListSeparator)) {
		if root = strings.TrimSpace(root); root != "" {
			roots = append(roots, root)
		}
	}
	fs := vfs.New()
	t.Cleanup(func() { fs.Close() })
	if err := fs.MountGameDirectories(roots); err != nil {
		t.Fatalf("mount ProTA over retail: %v", err)
	}
	profile, err := profiles.Resolve(fs, "")
	if err != nil {
		t.Fatalf("resolve ProTA profile: %v", err)
	}
	if profile.Name != "prota" {
		t.Fatalf("resolved profile %q, want prota", profile.Name)
	}
	view := profile.Layout().Apply(fs)
	limits := content.LimitsFromProfile(profile.Limits)
	cat, err := content.CompileWithOptions(view, content.Options{Limits: limits})
	if err != nil {
		t.Fatalf("compile ProTA catalog: %v", err)
	}
	return view, cat, limits, profile
}

// TestProTA48PackageAcceptance is deliberately bounded. It exercises the
// historical 4.8 archive as authored content; it does not claim that the short,
// explicitly commanded battle reproduces the package DLL's patched AI or its
// complete graphical interface [research/extensions/prota-engine.md].
func TestProTA48PackageAcceptance(t *testing.T) {
	fs, cat, limits, profile := loadProTAArchive(t)
	communitySources := CommunitySources{Content: profile.GameplaySources()}

	t.Run("profile_gameplay_and_save_restore", func(t *testing.T) {
		assertProTACatalog(t, fs, cat)

		cfg := DirectSkirmishConfig("ashap plateau")
		cfg.ApplyDefaults()
		cfg.Gameplay = gameplay.Modern
		cfg.RNGSimSeed, cfg.RNGCrtSeed = 7, 7
		s, err := NewSkirmishWithEntryOptions(fs, cat, cfg, SkirmishEntryOptions{
			CommunitySources: communitySources,
			ContentLimits:    limits,
		})
		if err != nil {
			t.Fatalf("compose ProTA skirmish: %v", err)
		}
		assertProTASessionComposition(t, s)
		advanceProTATicks(t, s, 2)
		assertProTADirectionalShipyards(t, s)

		builder := retailUnit(s, 0, "ARMCOM")
		if builder == nil || retailUnit(s, 1, "CORCOM") == nil {
			t.Fatal("ProTA skirmish did not enter with both authored commanders")
		}
		x, z := retailBuildSite(t, s, cat, builder, "ARMLAB")
		if err := construction.QueueMobileBuild(builder, "ARMLAB", x, z, 1, cat); err != nil {
			t.Fatalf("queue ProTA-authored ARM factory: %v", err)
		}

		// This is an explicit human attack between two ProTA-only definitions,
		// not evidence about the patch DLL's autonomous target or AI policy.
		shooter := placeCompleteRetailUnit(t, s, "ARMLH", 0, numeric.FixedFromInt(600), numeric.FixedFromInt(600))
		target := placeCompleteRetailUnit(t, s, "CORFH", 1, numeric.FixedFromInt(600), numeric.FixedFromInt(700))
		beforeHealth := target.Health
		if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanOrder, Order: HumanOrderCommand{
			Handles: []pool.Handle{shooter.Handle}, Code: 3, Target: target.Handle,
			Position: orders.ResolvePos{X: target.X, Y: target.Y, Z: target.Z},
		}}); err != nil {
			t.Fatalf("issue Spark attack on Blaze: %v", err)
		}

		var frameHandle pool.Handle
		for dispatch := 0; dispatch < 1200 && (target.Health == beforeHealth || frameHandle == 0); dispatch++ {
			s.Econ.Players[0].Stock[economy.Metal] = 10000
			s.Econ.Players[0].Stock[economy.Energy] = 10000
			advanceProTATicks(t, s, 1)
			if target.Health < beforeHealth {
				_ = s.EnqueueHumanCommand(HumanCommand{Kind: HumanStop, Stop: HumanStopCommand{Handles: []pool.Handle{shooter.Handle}}})
			}
			if frame := retailUnit(s, 0, "ARMLAB"); frame != nil && frame.Remaining > 0 {
				frameHandle = frame.Handle
			}
		}
		if target.Health >= beforeHealth {
			t.Fatal("the explicitly ordered Spark never damaged Blaze through ProTA's resolved weapon")
		}
		frame := s.Units.Unit(frameHandle)
		if frame == nil || frame.Remaining <= 0 {
			t.Fatal("the authored ARM factory order did not reach a live nanoframe")
		}
		yardHandle, yardProduct := queueProTADirectionalShipyardProduct(t, s)

		summary := RetailBattleSummary(s, "ProTA acceptance", "0", s.Skirmish.UnitLimit)
		inputs, err := s.RetailBattleSaveInputs(summary, save.Camera{})
		if err != nil {
			t.Fatalf("project ProTA battle save: %v", err)
		}
		path := filepath.Join(t.TempDir(), "PROTA.SAV")
		if err := s.WriteRetailSave(path, inputs); err != nil {
			t.Fatalf("write ProTA battle save: %v", err)
		}
		loaded, err := LoadRetailSavePath(path, RetailLoadDeps{
			FS: fs, Catalog: cat, ContentLimits: limits, CommunitySources: communitySources,
			Gameplay: gameplay.Modern,
			SimSeed:  7, CRTSeed: 7, UnitLimit: s.Skirmish.UnitLimit,
		})
		if err != nil {
			t.Fatalf("restore ProTA battle save: %v", err)
		}
		if loaded.Route != RetailLoadRouteBattleRestoration || loaded.Battle == nil || loaded.Battle.Session == nil {
			t.Fatalf("ProTA save selected route %d with battle %#v", loaded.Route, loaded.Battle)
		}
		dst := loaded.Battle.Session
		assertProTARestoreBoundary(t, s, dst)
		restoredYard := dst.Units.Unit(loaded.Battle.StableUnit[uint16(yardHandle)])
		if restoredYard == nil {
			t.Fatal("restored ARMSYE is absent")
		}
		primary := orders.QueueForUnit(restoredYard).Primary()
		if len(primary) == 0 || primary[0] == nil || primary[0].BuildDefKey != yardProduct || primary[0].Param2 == 0 {
			t.Fatalf("restored ARMSYE product queue = %#v, want counted %s order", primary, yardProduct)
		}
		restoredFrame := dst.Units.Unit(loaded.Battle.StableUnit[uint16(frameHandle)])
		if restoredFrame == nil || restoredFrame.Remaining != frame.Remaining {
			t.Fatalf("restored factory frame = %#v, want remaining %.6f", restoredFrame, frame.Remaining)
		}
		remaining := restoredFrame.Remaining
		tick := dst.Clock.GlobalTick
		for i := 0; i < 90 && restoredFrame.Alive && restoredFrame.Remaining == remaining; i++ {
			dst.Econ.Players[0].Stock[economy.Metal] = 10000
			dst.Econ.Players[0].Stock[economy.Energy] = 10000
			advanceProTATicks(t, dst, 1)
		}
		if dst.Clock.GlobalTick <= tick {
			t.Fatal("restored ProTA session did not advance")
		}
		if !restoredFrame.Alive || restoredFrame.Remaining >= remaining {
			t.Fatalf("restored factory construction did not continue alive: alive=%v remaining %.6f, was %.6f", restoredFrame.Alive, restoredFrame.Remaining, remaining)
		}
	})

	t.Run("campaign_overlays_restrictions_and_progression", func(t *testing.T) {
		missionOptions := MissionEntryOptions{ContentLimits: limits, Gameplay: gameplay.Modern, CommunitySources: communitySources}
		core1, err := NewMissionWithEntryOptions(fs, cat, "camps/Core Campaign.tdf:MISSION0", 1, 7, 7, missionOptions, nil)
		if err != nil {
			t.Fatalf("enter original Core mission 1: %v", err)
		}
		if core1.Mission.TerrainKey != "CC01" || core1.Mission.UseOnlyPath == "" {
			t.Fatalf("Core mission 1 terrain/use-only = %q/%q", core1.Mission.TerrainKey, core1.Mission.UseOnlyPath)
		}
		if info, err := fs.Stat("maps/CC01.TNT"); err != nil {
			t.Fatalf("stat Core mission 1 terrain: %v", err)
		} else if got := info.Source.ProviderID(); !strings.EqualFold(got, "ProTA.gp3") {
			t.Fatalf("Core mission 1 terrain provider = %q, want ProTA.gp3", got)
		}
		if _, ok := cat.Unit("ARMAPEX"); !ok {
			t.Fatal("shared ProTA catalog lost Apex before campaign restriction")
		}
		if _, ok := core1.Catalog.Unit("ARMAPEX"); ok {
			t.Fatal("Core mission 1 admitted Apex despite its authored use-only file")
		}
		if _, ok := core1.Catalog.Unit("CORCOM"); !ok {
			t.Fatal("Core mission 1 use-only catalog removed its authored commander")
		}
		if !sessionHasAI(core1) {
			t.Fatal("Core mission 1 entered without its campaign AI manager")
		}
		advanceProTATicks(t, core1, 30)

		next, ok, err := mission.NextCampaignMission(fs, "camps/Core Campaign.tdf", 0)
		if err != nil || !ok || next != 1 {
			t.Fatalf("Core campaign successor = %d/%v, err=%v; want MISSION1", next, ok, err)
		}

		const ccCampaign = "camps/Core Campaign - Core Contingency .tdf"
		cc6, err := NewMissionWithEntryOptions(fs, cat, ccCampaign+":MISSION5", 1, 7, 7, missionOptions, nil)
		if err != nil {
			t.Fatalf("enter Core Contingency mission 6: %v", err)
		}
		if cc6.Mission.TerrainKey != "EXP1CC06" || cc6.Mission.WindBounds.Min != 90 || cc6.Mission.WindBounds.Max != 900 {
			t.Fatalf("Core Contingency mission 6 terrain/wind = %q/%+v, want EXP1CC06 and 90..900", cc6.Mission.TerrainKey, cc6.Mission.WindBounds)
		}
		if info, err := fs.Stat("maps/EXP1CC06.OTA"); err != nil {
			t.Fatalf("stat Core Contingency mission 6 overlay: %v", err)
		} else if got := info.Source.ProviderID(); !strings.EqualFold(got, "ProTA.gp3") {
			t.Fatalf("Core Contingency mission 6 OTA provider = %q, want ProTA.gp3", got)
		}
		if info, err := fs.Stat("maps/EXP1CC06.TNT"); err != nil {
			t.Fatalf("stat Core Contingency mission 6 base terrain: %v", err)
		} else if got := info.Source.ProviderID(); !strings.EqualFold(got, "ccmiss.ccx") {
			t.Fatalf("Core Contingency mission 6 terrain provider = %q, want retail ccmiss.ccx", got)
		}
		if cc6.Mission.UseOnlyPath == "" || !sessionHasAI(cc6) {
			t.Fatalf("Core Contingency mission 6 use-only/AI = %q/%v", cc6.Mission.UseOnlyPath, sessionHasAI(cc6))
		}
		advanceProTATicks(t, cc6, 30)
	})
}

func assertProTASessionComposition(t *testing.T, s *Session) {
	t.Helper()
	wantFeatures, err := community.Table("prota")
	if err != nil {
		t.Fatalf("resolve independent ProTA feature table: %v", err)
	}
	if s.Gameplay != gameplay.Modern || s.Rules.Name != ModernRuleSetName {
		t.Fatalf("ProTA gameplay/rules = %q/%q, want %q/%q", s.Gameplay, s.Rules.Name, gameplay.Modern, ModernRuleSetName)
	}
	if s.Community != wantFeatures || s.EntryCommunity != wantFeatures {
		t.Fatalf("ProTA feature table was not applied at entry: live=%s entry=%s want=%s", s.Community.Digest(), s.EntryCommunity.Digest(), wantFeatures.Digest())
	}
	def, ok := s.Catalog.Unit("ARMLH")
	if !ok || def == nil || !strings.EqualFold(def.Provenance.ProviderID, "ProTA.gp3") {
		t.Fatalf("live session catalog did not retain the ProTA profile: ARMLH=%#v", def)
	}
}

func assertProTADirectionalShipyards(t *testing.T, s *Session) {
	t.Helper()
	for _, key := range []string{
		"ARMSY", "ARMSYE", "ARMSYN", "ARMSYW",
		"ARMASY", "ARMASYE", "ARMASYN", "ARMASYW",
		"CORSY", "CORSYN",
		"CORASY", "CORASYE", "CORASYN", "CORASYW",
	} {
		products := s.buildProducts(key)
		if len(products) == 0 {
			t.Errorf("directional shipyard %s has no effective build products", key)
			continue
		}
		for _, product := range products {
			if def, ok := s.Catalog.Unit(product); !ok || def == nil {
				t.Errorf("directional shipyard %s admits unavailable product %q", key, product)
			}
		}
	}

	// TODO(question): ProTA 4.8 defines CORSYE/CORSYW but authors the two
	// populated CANBUILD pages as CORSYNE/CORSYNW. Primary package docs,
	// appropriately licensed source, or a bounded manual observation must
	// establish whether the patch aliases these names before the engine does.
	for _, key := range []string{"CORSYE", "CORSYW"} {
		if products := s.buildProducts(key); len(products) != 0 {
			t.Fatalf("unresolved Core regular shipyard %s unexpectedly admits %v; update the package acceptance finding", key, products)
		}
	}
	for _, orphanPage := range []string{"CORSYNE", "CORSYNW"} {
		if products := s.buildProducts(orphanPage); len(products) == 0 {
			t.Fatalf("authored ProTA page %s has no products", orphanPage)
		}
	}
}

func queueProTADirectionalShipyardProduct(t *testing.T, s *Session) (pool.Handle, string) {
	t.Helper()
	products := s.buildProducts("ARMSYE")
	if len(products) == 0 {
		t.Fatal("ARMSYE has no effective product to queue")
	}
	product := content.CanonicalKey(products[0])
	yard := placeCompleteRetailUnit(t, s, "ARMSYE", 0, numeric.FixedFromInt(800), numeric.FixedFromInt(600))
	if err := s.EnqueueHumanCommand(HumanCommand{Kind: HumanFactoryBuild, FactoryBuild: HumanFactoryBuildCommand{
		Builder: yard.Handle, Product: product, Count: 1,
	}}); err != nil {
		t.Fatalf("queue %s from ARMSYE: %v", product, err)
	}
	advanceProTATicks(t, s, 1)
	primary := orders.QueueForUnit(yard).Primary()
	if len(primary) == 0 || primary[0] == nil || primary[0].BuildDefKey != product || primary[0].Param2 == 0 {
		t.Fatalf("ARMSYE product queue = %#v, want one counted %s order", primary, product)
	}
	return yard.Handle, product
}

func assertProTACatalog(t *testing.T, fs vfs.FSOps, cat *content.Catalog) {
	t.Helper()
	if info, err := fs.Stat("gamedata/sidedata.tdf"); err != nil {
		t.Fatalf("profile-mapped SIDEDATA: %v", err)
	} else if got := info.Source.ProviderID(); !strings.EqualFold(got, "ProTA.gp3") {
		t.Fatalf("profile-mapped SIDEDATA provider = %q, want ProTA.gp3", got)
	}
	for _, want := range []struct{ key, name, side string }{
		{"ARMCOM", "", "ARM"}, {"CORCOM", "", "CORE"},
		{"ARMLH", "Spark", "ARM"}, {"CORFH", "Blaze", "CORE"}, {"ARMAPEX", "Apex", "ARM"},
	} {
		def, ok := cat.Unit(want.key)
		if !ok || def == nil {
			t.Fatalf("ProTA unit %s is absent", want.key)
		}
		if want.name != "" && def.Name != want.name {
			t.Fatalf("ProTA unit %s name = %q, want %q", want.key, def.Name, want.name)
		}
		if def.Side != want.side || !strings.EqualFold(def.Provenance.ProviderID, "ProTA.gp3") {
			t.Fatalf("ProTA unit %s side/provider = %q/%q", want.key, def.Side, def.Provenance.ProviderID)
		}
	}
	for _, prefix := range []string{"ARMSY", "ARMASY", "CORSY", "CORASY"} {
		for _, suffix := range []string{"", "E", "N", "W"} {
			key := prefix + suffix
			def, ok := cat.Unit(key)
			if !ok || def == nil || !strings.EqualFold(def.UnitName, key) || !strings.EqualFold(def.Provenance.ProviderID, "ProTA.gp3") {
				t.Fatalf("directional shipyard definition %s is absent or aliased", key)
			}
		}
	}
	// The package authors the Core east/west CANBUILD pages under CORSYNE and
	// CORSYNW rather than the corresponding unit-definition names. Preserve
	// that authored table instead of inferring a generic rotation/page rule.
	for _, key := range []string{"ARMSY", "ARMSYE", "ARMSYN", "ARMSYW", "CORSY", "CORSYNE", "CORSYN", "CORSYNW"} {
		menu := cat.BuildMenus[content.CanonicalKey(key)]
		if menu == nil || len(menu.AuthoredButtons) == 0 || !strings.EqualFold(menu.Provenance.ProviderID, "ProTA.gp3") {
			t.Fatalf("authored directional shipyard page %s = %#v", key, menu)
		}
	}
}

func assertProTARestoreBoundary(t *testing.T, src, dst *Session) {
	t.Helper()
	if dst.Gameplay != src.Gameplay || dst.Rules.Name != src.Rules.Name || dst.Community.Digest() != src.Community.Digest() || dst.EntryCommunity.Digest() != src.EntryCommunity.Digest() {
		t.Fatalf("restored ProTA mode/table = %q/%q %s/%s, want %q/%q %s/%s", dst.Gameplay, dst.Rules.Name, dst.Community.Digest(), dst.EntryCommunity.Digest(), src.Gameplay, src.Rules.Name, src.Community.Digest(), src.EntryCommunity.Digest())
	}
	def, ok := dst.Catalog.Unit("ARMLH")
	if !ok || def == nil || !strings.EqualFold(def.Provenance.ProviderID, "ProTA.gp3") {
		t.Fatalf("restored session catalog did not retain ProTA profile content: ARMLH=%#v", def)
	}
	if dst.Clock.GlobalTick != src.Clock.GlobalTick {
		t.Fatalf("restored ProTA tick = %d, want %d", dst.Clock.GlobalTick, src.Clock.GlobalTick)
	}
	wantUnits, gotUnits := retailUnitCensus(src), retailUnitCensus(dst)
	if len(gotUnits) != len(wantUnits) {
		t.Fatalf("restored ProTA units = %d, want %d", len(gotUnits), len(wantUnits))
	}
	for id, want := range wantUnits {
		if got := gotUnits[id]; got != want {
			t.Fatalf("restored ProTA unit %d = %q, want %q", id, got, want)
		}
	}
	wantEcon, gotEcon := retailEconomyCensus(src), retailEconomyCensus(dst)
	for slot, want := range wantEcon {
		if got := gotEcon[slot]; got != want {
			t.Fatalf("restored ProTA player %d = %q, want %q", slot, got, want)
		}
	}
}

func advanceProTATicks(t *testing.T, s *Session, ticks int) {
	t.Helper()
	advanced := 0
	for dispatch := 0; advanced < ticks && dispatch < ticks+8; dispatch++ {
		before := s.Clock.GlobalTick
		s.Step(s.Clock.ScaledAnchor + 1)
		if delta := int(s.Clock.GlobalTick - before); delta > 0 {
			advanced += delta
		}
	}
	if advanced != ticks {
		t.Fatalf("advanced %d ProTA ticks, want %d (state %v)", advanced, ticks, s.State)
	}
}

func sessionHasAI(s *Session) bool {
	if s == nil {
		return false
	}
	for _, manager := range s.AI {
		if manager != nil {
			return true
		}
	}
	return false
}
