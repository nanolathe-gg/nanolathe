//go:build retail

package session

// The in-battle Save → Load loop on real content. This is the only test that
// exercises the whole chain the play-tester uses — projection, bank write,
// bank parse, staging, restore — against a composed skirmish with real COBs,
// real orders and a nanoframe in flight. Everything below it (the per-record
// writers and readers) is covered by synthetic fixtures; nothing else covers
// the seams between them.

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/internal/construction"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/save"
)

const (
	roundTripSeed       = 7
	roundTripSaveTick   = 600
	roundTripAfterTicks = 300
)

// retailRoundTripSource composes the seed-7 skirmish this test saves from and
// puts real work in flight first: a queued factory on each commander, so the
// bank has to carry live orders, a nanoframe under construction and a mover
// walking to its build site rather than two idle commanders.
func retailRoundTripSource(t *testing.T) (*retailFixture, *Session) {
	t.Helper()
	f := loadRetailFixture(t)
	f.cfg.RNGSimSeed, f.cfg.RNGCrtSeed = roundTripSeed, roundTripSeed
	s := f.session(t)
	arm, core := retailUnit(s, 0, retailARM), retailUnit(s, 1, retailCORE)
	if arm == nil || core == nil {
		t.Fatalf("authored commanders missing: arm=%v core=%v", arm, core)
	}
	armX, armZ := retailBuildSite(t, s, f.cat, arm, retailARMLab)
	if err := construction.QueueMobileBuild(arm, retailARMLab, armX, armZ, 1, f.cat); err != nil {
		t.Fatalf("queue ARM factory: %v", err)
	}
	coreX, coreZ := retailBuildSite(t, s, f.cat, core, retailCORELab)
	if err := construction.QueueMobileBuild(core, retailCORELab, coreX, coreZ, 1, f.cat); err != nil {
		t.Fatalf("queue CORE factory: %v", err)
	}
	stepRetail(s, roundTripSaveTick)
	return f, s
}

// retailUnitCensus is the comparison key: one line per live unit, keyed by the
// pool slot that is also its saved stable identifier [08 R-SAVE-02 §6].
func retailUnitCensus(s *Session) map[uint16]string {
	out := make(map[uint16]string)
	if s == nil || s.Units == nil {
		return out
	}
	for slot := 1; slot < s.Units.TotalRecords(); slot++ {
		u := s.Units.Unit(pool.Handle(slot))
		if u == nil || !u.Alive {
			continue
		}
		name := ""
		if u.Def != nil {
			name = u.Def.UnitName
		}
		out[uint16(slot)] = fmt.Sprintf("%s owner=%d x=%d y=%d z=%d h=%d heading=%d remaining=%.4f mover=%t",
			name, u.Owner, int64(u.X.Raw()), int64(u.Y.Raw()), int64(u.Z.Raw()),
			u.Health, u.Move.Heading, u.Remaining, u.HasMover)
	}
	return out
}

func retailEconomyCensus(s *Session) map[int]string {
	out := make(map[int]string)
	if s == nil || s.Econ == nil {
		return out
	}
	for i := range s.Econ.Players {
		p := &s.Econ.Players[i]
		if !p.Exists {
			continue
		}
		out[i] = fmt.Sprintf("m=%.4f e=%.4f mcap=%.4f ecap=%.4f",
			p.Stock[economy.Metal], p.Stock[economy.Energy], p.Capacity[economy.Metal], p.Capacity[economy.Energy])
	}
	return out
}

// TestRetailBattleSaveLoadRoundTripOnRealContent is WU-19-161's gate: a real
// skirmish must reach a written bank and come back as an equivalent session.
func TestRetailBattleSaveLoadRoundTripOnRealContent(t *testing.T) {
	f, src := retailRoundTripSource(t)

	summary := RetailBattleSummary(src, "roundtrip", "0")
	in, err := src.RetailBattleSaveInputs(summary, save.Camera{})
	if err != nil {
		t.Fatalf("battle save inputs: %v", err)
	}
	path := filepath.Join(t.TempDir(), "ROUNDTRIP.SAV")
	if err := src.WriteRetailSave(path, in); err != nil {
		t.Fatalf("write retail battle save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() == 0 {
		t.Fatalf("save file stat: %v (size %d)", err, info.Size())
	}
	t.Logf("bank written: %d bytes, %d live units at tick %d", info.Size(), len(in.StableIDs), src.Clock.GlobalTick)

	// The scenario has to be worth saving. A bank with no order records and no
	// unit under construction would pass every assertion below without having
	// exercised the queue or nanoframe families at all.
	projection, err := src.RetailProjection(in)
	if err != nil {
		t.Fatalf("projection for census: %v", err)
	}
	frames := 0
	for _, u := range retailUnitCensus(src) {
		if !strings.Contains(u, "remaining=0.0000") {
			frames++
		}
	}
	t.Logf("bank carries %d unit records, %d order records, %d units under construction",
		len(projection.Units.Records), len(projection.Units.Orders), frames)
	if len(projection.Units.Orders) == 0 {
		t.Fatal("scenario carried no order records; the queue family was not exercised")
	}
	if frames == 0 {
		t.Fatal("scenario carried no unit under construction; the nanoframe family was not exercised")
	}

	result, err := LoadRetailSavePath(path, RetailLoadDeps{
		FS: f.fs, Catalog: f.cat,
		SimSeed: roundTripSeed, CRTSeed: roundTripSeed,
		UnitLimit: src.Skirmish.UnitLimit,
	})
	if err != nil {
		t.Fatalf("load retail battle save: %v", err)
	}
	if result.Route != RetailLoadRouteBattleRestoration || result.Battle == nil || result.Battle.Session == nil {
		t.Fatalf("load route = %d, battle = %v", result.Route, result.Battle)
	}
	dst := result.Battle.Session

	// The restore boundary: nothing has been ticked on either side since the
	// projection, so every authoritative quantity the bank carries must match.
	if dst.Clock.GlobalTick != src.Clock.GlobalTick {
		t.Fatalf("restored tick %d, want %d", dst.Clock.GlobalTick, src.Clock.GlobalTick)
	}
	if dst.LocalOwner != src.LocalOwner {
		t.Fatalf("restored local owner %d, want %d", dst.LocalOwner, src.LocalOwner)
	}
	srcUnits, dstUnits := retailUnitCensus(src), retailUnitCensus(dst)
	if len(srcUnits) != len(dstUnits) {
		t.Fatalf("restored %d units, want %d", len(dstUnits), len(srcUnits))
	}
	for id, want := range srcUnits {
		got, ok := dstUnits[id]
		if !ok {
			t.Fatalf("unit %d missing after restore (was %s)", id, want)
		}
		if got != want {
			t.Fatalf("unit %d restored as\n  %s\nwant\n  %s", id, got, want)
		}
	}
	srcEcon, dstEcon := retailEconomyCensus(src), retailEconomyCensus(dst)
	if len(srcEcon) != len(dstEcon) {
		t.Fatalf("restored %d player accounts, want %d", len(dstEcon), len(srcEcon))
	}
	for slot, want := range srcEcon {
		if got := dstEcon[slot]; got != want {
			t.Fatalf("player %d restored as %s, want %s", slot, got, want)
		}
	}

	// A unit still under construction holds its cells through the construction
	// service's placement reservation, not the mover occupancy path, so its
	// footprint has to come back through that same registration. Comparing the
	// two services' records is what makes the restore's skip of the mover
	// re-stamp safe rather than merely quiet.
	for id := range dstUnits {
		h := result.Battle.StableUnit[id]
		u := dst.Units.Unit(h)
		if u == nil || u.Remaining == 0 {
			continue
		}
		want, hadSource := src.Build.PlacementForProduct(pool.Handle(id))
		got, hadRestored := dst.Build.PlacementForProduct(h)
		if !hadSource || !hadRestored || got != want {
			t.Fatalf("unit %d under construction lost its placement: source %v/%v restored %v/%v", id, want, hadSource, got, hadRestored)
		}
	}

	// Every restored unit must be drawable. A zero residue pair in the piece
	// records is a legal image that clears the draw bit of every piece, which
	// the presentation reads as hidden [08 R-SAVE-02 §9] [04 §"Piece flag
	// polarity"]; the shell's policy is what keeps a reloaded battle visible.
	for id := range dstUnits {
		u := dst.Units.Unit(result.Battle.StableUnit[id])
		if u == nil || len(u.RenderPieceFlags) == 0 {
			continue
		}
		drawn := false
		for _, flags := range u.RenderPieceFlags {
			if flags&0x01 != 0 {
				drawn = true
				break
			}
		}
		if !drawn {
			t.Fatalf("restored unit %d has no drawn piece: flags %v", id, u.RenderPieceFlags)
		}
	}
}

// TestRetailBattleSaveLoadContinuationReport steps both sides past the restore
// boundary and reports the first quantity that diverges. Retail saves omit
// both random streams [08 "Scheduler and random state in saves"], so this is a
// report rather than an assertion: it names what a resumed battle does not
// reproduce, so a later unit has a measurement to work from.
func TestRetailBattleSaveLoadContinuationReport(t *testing.T) {
	f, src := retailRoundTripSource(t)
	summary := RetailBattleSummary(src, "roundtrip", "0")
	in, err := src.RetailBattleSaveInputs(summary, save.Camera{})
	if err != nil {
		t.Fatalf("battle save inputs: %v", err)
	}
	path := filepath.Join(t.TempDir(), "ROUNDTRIP.SAV")
	if err := src.WriteRetailSave(path, in); err != nil {
		t.Fatalf("write retail battle save: %v", err)
	}
	result, err := LoadRetailSavePath(path, RetailLoadDeps{
		FS: f.fs, Catalog: f.cat,
		SimSeed: roundTripSeed, CRTSeed: roundTripSeed,
		UnitLimit: src.Skirmish.UnitLimit,
	})
	if err != nil {
		t.Fatalf("load retail battle save: %v", err)
	}
	dst := result.Battle.Session

	base := int32(src.Clock.GlobalTick)
	firstDiff, matched, mismatched := 0, 0, 0
	for i := 1; i <= roundTripAfterTicks; i++ {
		tick := base + int32(i)
		src.Step(tick)
		dst.Step(tick)
		if HashState(src) == HashState(dst) {
			matched++
			continue
		}
		mismatched++
		if firstDiff == 0 {
			firstDiff = i
			t.Logf("continuation diverges %d tick(s) after the restore (tick %d): %s", i, tick, firstRetailDivergence(src, dst))
		}
	}
	if firstDiff == 0 {
		t.Logf("continuation stayed hash-identical for %d ticks after the restore", roundTripAfterTicks)
		return
	}
	t.Logf("over %d ticks: %d hash-identical, %d divergent", roundTripAfterTicks, matched, mismatched)
	if HashState(src) == HashState(dst) {
		t.Logf("the two sessions are hash-identical again at tick %d", base+int32(roundTripAfterTicks))
		return
	}
	t.Logf("still divergent at tick %d: %s", base+int32(roundTripAfterTicks), firstRetailDivergence(src, dst))
}

// retailStateLines enumerates the same authoritative quantities HashState
// digests, one labelled line each, so a hash difference can be reported as a
// named field rather than as two opaque digests.
func retailStateLines(s *Session) []string {
	lines := make([]string, 0, 64)
	if s == nil {
		return lines
	}
	if s.Units != nil {
		for player := 0; player < 10; player++ {
			lines = append(lines, fmt.Sprintf("created[%d]=%d", player, s.Units.CreatedCountForPlayer(player)))
		}
		for _, u := range s.Units.Iter() {
			if u == nil || !u.Alive {
				continue
			}
			lines = append(lines, fmt.Sprintf("unit[%d] x=%d z=%d health=%d remaining=%.2f flags=%#x group=%d stance=%t busy=%t yard=%t bugger=%t armored=%t",
				u.Handle, int64(u.X.Raw()), int64(u.Z.Raw()), u.Health, u.Remaining, u.Flags, u.Group,
				u.InBuildStance, u.Busy, u.YardOpen, u.BuggerOff, u.Armored))
		}
	}
	if s.Combat != nil {
		lines = append(lines, fmt.Sprintf("projectiles=%d", s.Combat.Count()))
		for i := 0; i < s.Combat.Count() && i < len(s.Combat.Records); i++ {
			if s.Combat.IsDead(pool.Handle(i + 1)) {
				continue
			}
			p := s.Combat.Records[i]
			lines = append(lines, fmt.Sprintf("projectile[%d] x=%d y=%d z=%d", i, int64(p.Pos.X.Raw()), int64(p.Pos.Y.Raw()), int64(p.Pos.Z.Raw())))
		}
	}
	if s.Econ != nil {
		for i := 0; i < 10; i++ {
			p := &s.Econ.Players[i]
			if !p.Exists {
				continue
			}
			lines = append(lines, fmt.Sprintf("player[%d] metal=%08x energy=%08x aiProd=%08x/%08x aiCons=%08x/%08x", i,
				math.Float32bits(p.Stock[economy.Metal]), math.Float32bits(p.Stock[economy.Energy]),
				math.Float32bits(p.AIProduction[economy.Metal]), math.Float32bits(p.AIProduction[economy.Energy]),
				math.Float32bits(p.AIConsumption[economy.Metal]), math.Float32bits(p.AIConsumption[economy.Energy])))
		}
	}
	return lines
}

// firstRetailDivergence names the first differing quantity in the order
// HashState walks, so the report says what moved rather than that a hash
// changed.
func firstRetailDivergence(a, b *Session) string {
	la, lb := retailStateLines(a), retailStateLines(b)
	for i := 0; i < len(la) && i < len(lb); i++ {
		if la[i] != lb[i] {
			return fmt.Sprintf("\n  source   %s\n  restored %s", la[i], lb[i])
		}
	}
	if len(la) != len(lb) {
		return fmt.Sprintf("state line count %d vs %d", len(la), len(lb))
	}
	return "no enumerated unit, projectile or economy field differs; the difference is in state HashState covers and this report does not (AI group vectors)"
}
