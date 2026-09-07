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

	"github.com/nanolathe/nanolathe/internal/ai"
	"github.com/nanolathe/nanolathe/internal/construction"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/save"
)

const (
	roundTripSeed       = 7
	roundTripSaveTick   = 600
	roundTripAfterTicks = 300
	// The computer player's slowest reachable effect is a placed building: the
	// construction task runs every 90 ticks, its builder has to walk to the site
	// it chose, and the nanoframe only then exists. This window is the one
	// WU-19-161 measured, tripled, so the gate fails on a planner that never
	// resumed rather than on one that resumed slowly [08 R-AI-01 §3].
	roundTripAIResumeTicks = 900
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
		// The persisted economy words are the two stocks, the two storage-bonus
		// operands and the bonus flag; capacity is not in the account — retail's
		// world-rebuild reset zeroes it before the reader runs and the first
		// settlement pass rebuilds it from the units plus the restored bonus
		// [08 "Player records"] [05 R-ECO-01 §4] [08 R-ENTRY-01 §3 step 24].
		// This census used to compare capacity, which locked a restore of a
		// word retail never restores.
		out[i] = fmt.Sprintf("m=%.4f e=%.4f mbonus=%.4f ebonus=%.4f bonus=%t",
			p.Stock[economy.Metal], p.Stock[economy.Energy], p.StorageBonus[economy.Metal], p.StorageBonus[economy.Energy], p.StorageBonusEnabled)
	}
	return out
}

// TestRetailBattleSaveLoadRoundTripOnRealContent is WU-19-161's gate: a real
// skirmish must reach a written bank and come back as an equivalent session.
func TestRetailBattleSaveLoadRoundTripOnRealContent(t *testing.T) {
	f, src := retailRoundTripSource(t)

	summary := RetailBattleSummary(src, "roundtrip", "0", SkirmishDefaultUnitLimit)
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
	summary := RetailBattleSummary(src, "roundtrip", "0", SkirmishDefaultUnitLimit)
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

// retailAIActivity is the play-tester's question in one line per computer-owned
// unit: where it is, how long its order queue is, and what it is building. The
// planner's effect on the world is a change in this census; a planner that was
// never reconstructed leaves it frozen except for whatever the save's own
// orders finish on their own.
func retailAIActivity(s *Session, owner uint8) map[uint16]string {
	out := make(map[uint16]string)
	if s == nil || s.Units == nil {
		return out
	}
	for slot := 1; slot < s.Units.TotalRecords(); slot++ {
		u := s.Units.Unit(pool.Handle(slot))
		if u == nil || !u.Alive || u.Owner != owner {
			continue
		}
		primary, secondary := 0, 0
		if q := orders.QueueForUnit(u); q != nil {
			primary, secondary = q.LenPrimary(), q.LenSecondary()
		}
		name := ""
		if u.Def != nil {
			name = u.Def.UnitName
		}
		out[uint16(slot)] = fmt.Sprintf("%s x=%d z=%d orders=%d/%d remaining=%.4f group=%d",
			name, int64(u.X.Raw()), int64(u.Z.Raw()), primary, secondary, u.Remaining, u.Group)
	}
	return out
}

// TestRetailBattleSaveLoadComputerPlayerResumes is WU-19-172's gate. Retail
// rebuilds a computer player's planner on the load path exactly as at a fresh
// battle entry — the per-player reset that constructs the AI record runs "for
// every kind and for loads alike", before the restoration dispatcher, and the
// battle-entry tail then primes the per-player phase once on the restored world
// at the restored global tick [08 R-ENTRY-01 §3 step 24][08 R-ENTRY-01 §8].
// Nothing of the planner is in the bank except each unit's group index
// [08 R-SAVE-02 §6, §11], so this asserts the reconstruction and the
// resumption, never hash equality with the source: retail reseeds both random
// streams before any restoration, so no bit-identical continuation exists
// [08 "Scheduler and random state in saves"].
func TestRetailBattleSaveLoadComputerPlayerResumes(t *testing.T) {
	f, src := retailRoundTripSource(t)
	summary := RetailBattleSummary(src, "roundtrip", "0", SkirmishDefaultUnitLimit)
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
	restoredTick := dst.Clock.GlobalTick

	// The reset's slot rule is the controller byte, which the `Player%i`
	// accounts carry: every non-remote slot owns a record, and the computer
	// slots are the ones the manager's outer gate dispatches
	// [08 R-ENTRY-01 §3 step 24][08 R-AI-01 §1].
	srcSlots, dstSlots := restoredComputerSlots(src), restoredComputerSlots(dst)
	if len(dstSlots) == 0 {
		t.Fatal("restored battle has no computer player: the per-player reset built no AI record")
	}
	if len(srcSlots) != len(dstSlots) {
		t.Fatalf("restored computer slots %v, source %v", dstSlots, srcSlots)
	}
	for i := range srcSlots {
		if srcSlots[i] != dstSlots[i] {
			t.Fatalf("restored computer slots %v, source %v", dstSlots, srcSlots)
		}
	}
	// A human slot owns a record too; only remote peers do not
	// [08 R-ENTRY-01 §3 step 24].
	if dst.AI[src.LocalOwner] == nil {
		t.Fatalf("local slot %d lost its AI record across the restore", src.LocalOwner)
	}
	for _, slot := range dstSlots {
		if dst.AI[slot] == nil || dst.AI[slot].Player != slot {
			t.Fatalf("slot %d has no manager after restore", slot)
		}
	}

	// The saved group words are the whole of the planner's persisted state, and
	// their reader's side effect is the group-vector append [08 R-SAVE-02 §6].
	for _, slot := range dstSlots {
		for group := uint8(1); group <= 9; group++ {
			want := 0
			for _, u := range src.Units.IterSliced() {
				if u != nil && u.Alive && u.Owner == slot && u.Group == group {
					want++
				}
			}
			if got := len(dst.AI[slot].GroupMembers(group)); got != want {
				t.Fatalf("restored slot %d group %d holds %d members, saved %d", slot, group, got, want)
			}
		}
	}

	// Every task record is constructed with deadline 0 and the dispatcher's
	// compare is unsigned `deadline <= tick`, so the battle-entry prime at the
	// restored tick runs all ten slots once and each rescheduling task writes
	// its next deadline past that tick [08 R-AI-01 §1][08 R-ENTRY-01 §8 step 4].
	// A planner that was never dispatched leaves them all at zero.
	dispatched := []ai.TaskKind{ai.TaskResource, ai.TaskConstruction, ai.TaskWaveA, ai.TaskWaveB, ai.TaskRegroupA, ai.TaskRegroupB, ai.TaskExplore, ai.TaskRally}
	for _, slot := range dstSlots {
		mgr := dst.AI[slot]
		for _, k := range dispatched {
			if mgr.Deadlines[k] <= restoredTick {
				t.Fatalf("slot %d task %d deadline %d did not advance past the restored tick %d: the battle-entry prime never dispatched it",
					slot, k, mgr.Deadlines[k], restoredTick)
			}
		}
	}

	// The behavioural half. The signal has to be one only the planner can
	// produce, because the bank's own orders keep running either way: a nanoframe
	// already under construction finishes, and a mover already under way keeps
	// walking, in a session whose managers were never rebuilt. Nothing else
	// submits an order on a computer slot — there is no input on it — so a unit
	// the slot did not own at the restore boundary is a building the planner
	// placed, and an order queue that grows is an order the planner submitted
	// [08 R-AI-01 §2, §3].
	computer := dstSlots[0]
	mgr := dst.AI[computer]
	before := retailAIActivity(dst, computer)
	beforeOrders := retailAIOrderCounts(dst, computer)
	created := dst.Units.CreatedCountForPlayer(int(computer))
	resourceDeadline, dispatches := mgr.Deadlines[ai.TaskResource], 0
	acted, actedAt := "", 0
	for i := 1; i <= roundTripAIResumeTicks; i++ {
		dst.Step(int32(restoredTick) + int32(i))
		if mgr.Deadlines[ai.TaskResource] != resourceDeadline {
			resourceDeadline = mgr.Deadlines[ai.TaskResource]
			dispatches++
		}
		if acted != "" {
			continue
		}
		if now := dst.Units.CreatedCountForPlayer(int(computer)); now != created {
			acted, actedAt = fmt.Sprintf("units created %d -> %d", created, now), i
			continue
		}
		for id, count := range retailAIOrderCounts(dst, computer) {
			if was, ok := beforeOrders[id]; !ok || count > was {
				acted, actedAt = fmt.Sprintf("unit %d order queue %d -> %d", id, was, count), i
				break
			}
		}
	}
	if acted == "" {
		t.Fatalf("computer player %d ordered nothing for %d ticks after the restore; its census is still\n  %v",
			computer, roundTripAIResumeTicks, before)
	}
	t.Logf("computer player %d resumed %d tick(s) after the restore: %s", computer, actedAt, acted)
	for id, line := range retailAIActivity(dst, computer) {
		t.Logf("  computer unit %d: %s", id, line)
	}

	// Resumption is not a one-shot: the resource task's own 30-tick cadence has
	// to keep rescheduling for the whole window, not fire once at the prime
	// [08 R-AI-01 §1, §2].
	if want := roundTripAIResumeTicks/30 - 1; dispatches < want {
		t.Fatalf("slot %d resource task was dispatched %d times in %d ticks, want at least %d",
			computer, dispatches, roundTripAIResumeTicks, want)
	}
}

// retailAIOrderCounts is the per-unit order-queue depth of one player's units,
// the quantity that can only grow when someone submits an order.
func retailAIOrderCounts(s *Session, owner uint8) map[uint16]int {
	out := make(map[uint16]int)
	if s == nil || s.Units == nil {
		return out
	}
	for slot := 1; slot < s.Units.TotalRecords(); slot++ {
		u := s.Units.Unit(pool.Handle(slot))
		if u == nil || !u.Alive || u.Owner != owner {
			continue
		}
		count := 0
		if q := orders.QueueForUnit(u); q != nil {
			count = q.LenPrimary() + q.LenSecondary()
		}
		out[uint16(slot)] = count
	}
	return out
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

// retailAllianceCensus is one line per active slot: the eleven-byte first
// alliance row as the save carries it [05 R-SHARE-01 §1] [08 "Player records"].
func retailAllianceCensus(s *Session) map[int]string {
	out := make(map[int]string)
	if s == nil || s.Econ == nil {
		return out
	}
	for i := range s.Econ.Players {
		if !s.Econ.Players[i].Exists {
			continue
		}
		out[i] = fmt.Sprintf("%v", s.Econ.AllianceRow(i))
	}
	return out
}

// TestRetailBattleSaveLoadCarriesAllianceRows is WU-19-182's gate. The
// `Alliances` box lives inside each `Player%i` account and row *i* is slot
// *i*'s own first alliance row [08 "Player records"], so a three-seat skirmish
// with one ally pair must come back with the pair still allied, the third seat
// still hostile to both, and every self column set.
//
// A two-seat fixture cannot fail this: with no ally pair every row is
// self-only, and the self column is forced to 1 on load whether or not
// anything was persisted. The ally pair is what makes the assertion able to
// fail.
func TestRetailBattleSaveLoadCarriesAllianceRows(t *testing.T) {
	f := loadRetailFixture(t)
	cfg := f.cfg
	cfg.NumPlayers = 3
	cfg.RNGSimSeed, cfg.RNGCrtSeed = roundTripSeed, roundTripSeed
	// Slots 0 and 2 share ally group 1; slot 1 keeps a group of its own, so it
	// is allied with nobody but itself [08 R-SKIR-01 §2].
	cfg.Players[0].Side, cfg.Players[0].Controller, cfg.Players[0].AllyGroup = 0, SkirmishControllerHuman, 1
	cfg.Players[1].Side, cfg.Players[1].Controller, cfg.Players[1].AllyGroup = 1, SkirmishControllerComputer, 2
	cfg.Players[2].Side, cfg.Players[2].Controller, cfg.Players[2].AllyGroup = 0, SkirmishControllerComputer, 1
	src, err := NewSkirmishWithFS(f.fs, f.cat, cfg)
	if err != nil {
		// The kind-2 start-slot walk is fatal on a missing start position
		// [08 R-ENTRY-01 §5], so a map authored for two seats cannot host this
		// scenario at all.
		t.Skipf("three-seat skirmish on %q is unavailable: %v", retailMap, err)
	}
	if err := src.ValidateComposition(); err != nil {
		t.Fatalf("validate three-seat composition: %v", err)
	}
	stepRetail(src, 60)

	want := retailAllianceCensus(src)
	if len(want) != 3 {
		t.Fatalf("three-seat skirmish produced %d active slots: %v", len(want), want)
	}
	if !src.Econ.DeclaresAlliance(0, 2) || !src.Econ.DeclaresAlliance(2, 0) {
		t.Fatalf("the ally pair is not allied before the save: %v", want)
	}
	if src.Econ.DeclaresAlliance(0, 1) || src.Econ.DeclaresAlliance(1, 0) || src.Econ.DeclaresAlliance(1, 2) {
		t.Fatalf("slot 1 is not hostile before the save: %v", want)
	}

	summary := RetailBattleSummary(src, "alliances", "0", SkirmishDefaultUnitLimit)
	in, err := src.RetailBattleSaveInputs(summary, save.Camera{})
	if err != nil {
		t.Fatalf("battle save inputs: %v", err)
	}
	path := filepath.Join(t.TempDir(), "ALLIES.SAV")
	if err := src.WriteRetailSave(path, in); err != nil {
		t.Fatalf("write retail battle save: %v", err)
	}

	// The wire shape, before the loader gets a chance to be symmetric with a
	// writer that is wrong in the same way: one 11-byte box per `Player%i`
	// account and none under `Players` [08 "Player records"].
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read bank: %v", err)
	}
	bank, err := save.OpenBytes(raw)
	if err != nil {
		t.Fatalf("open bank: %v", err)
	}
	if ac, ok := bank.Account("Players"); ok {
		if _, present := ac.BoxData("Alliances", 0); present {
			t.Fatal("the Players account carries an Alliances box; the box is per Player%i")
		}
	}
	for slot := 0; slot < 3; slot++ {
		row, ok := save.ReadAlliances(bank, slot)
		if !ok {
			t.Fatalf("Player%d carries no 11-byte Alliances box", slot)
		}
		if row[slot] != 1 {
			t.Fatalf("Player%d self column = %d, want 1", slot, row[slot])
		}
	}
	if row, _ := save.ReadAlliances(bank, 0); row[2] != 1 || row[1] != 0 {
		t.Fatalf("slot 0's saved row does not carry the ally pair: %v", row)
	}
	if row, _ := save.ReadAlliances(bank, 1); row[0] != 0 || row[2] != 0 {
		t.Fatalf("slot 1's saved row is not hostile to both: %v", row)
	}

	result, err := LoadRetailSavePath(path, RetailLoadDeps{
		FS: f.fs, Catalog: f.cat,
		SimSeed: roundTripSeed, CRTSeed: roundTripSeed,
		UnitLimit: src.Skirmish.UnitLimit,
	})
	if err != nil {
		t.Fatalf("load retail battle save: %v", err)
	}
	if result.Battle == nil || result.Battle.Session == nil {
		t.Fatalf("load route = %d produced no battle", result.Route)
	}
	dst := result.Battle.Session
	got := retailAllianceCensus(dst)
	if len(got) != len(want) {
		t.Fatalf("restored %d active slots, want %d", len(got), len(want))
	}
	for slot, row := range want {
		if got[slot] != row {
			t.Fatalf("slot %d alliance row restored as %s, want %s", slot, got[slot], row)
		}
	}
	if !dst.Econ.DeclaresAlliance(0, 2) || !dst.Econ.DeclaresAlliance(2, 0) {
		t.Fatalf("the ally pair did not survive the round trip: %v", got)
	}
	if dst.Econ.DeclaresAlliance(0, 1) || dst.Econ.DeclaresAlliance(1, 0) || dst.Econ.DeclaresAlliance(1, 2) {
		t.Fatalf("hostility did not survive the round trip: %v", got)
	}
}
