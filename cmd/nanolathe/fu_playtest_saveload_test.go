//go:build retail

package main

// Scenario 3 of the fu-playtest acceptance round: a save/load sequence driven
// through the in-battle save dialog and the load dialog in one process —
// save during construction, restore, issue new commands, complete a factory
// product, save again, save during combat, restore, refuse two corrupt banks
// with the active battle intact, then enter another battle
// [08 R-SAVE-02 §1] [08 R-SAVE-02 §11] [08 "Load process"].
//
// The second save of a restored battle is a recorded finding, reproduced
// minimally by TestFUPlaytestSaveAfterRestore; this probe records it and
// carries the rest of the sequence on a fresh battle.

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/save"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

type fuSaveNote struct {
	Label       string       `json:"label"`
	Path        string       `json:"path"`
	Bytes       int64        `json:"bytes"`
	Tick        uint32       `json:"tick"`
	Summary     save.Summary `json:"summary"`
	Census      fuCensus     `json:"census"`
	SavedHash   string       `json:"saved_hash,omitempty"`
	RestoredAt  uint32       `json:"restored_at,omitempty"`
	RestoreHash string       `json:"restore_hash,omitempty"`
	Error       string       `json:"error,omitempty"`
	Offender    *fuUnitRow   `json:"offending_unit,omitempty"`
}

type fuCorruptNote struct {
	Label        string `json:"label"`
	Path         string `json:"path"`
	Listed       bool   `json:"listed"`
	Route        string `json:"route"`
	Err          string `json:"error"`
	Modal        string `json:"modal"`
	BattleKept   bool   `json:"battle_kept"`
	TickBefore   uint32 `json:"tick_before"`
	StepsAfter   uint32 `json:"tick_after_stepping"`
	Replaced     bool   `json:"battle_replaced"`
	ReplacedTick uint32 `json:"replaced_tick,omitempty"`
}

func fuReadSummary(t *testing.T, path string) (save.Summary, int64) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	bank, err := save.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	summary, ok := save.ReadSummary(bank)
	if !ok {
		t.Fatalf("%s carries no Summary account", path)
	}
	return summary, info.Size()
}

// fuTrySave attempts the in-battle save dialog write and returns the refusal
// instead of failing, so a refused save can be recorded and the sequence can
// go on.
func fuTrySave(f *fuShell, name string) (string, error) {
	if err := f.shell.openSaveLoadScreen(saveScreenMode, saveLoadFromBattle); err != nil {
		return "", err
	}
	saveLoadUI.SetName(name)
	f.shell.activateSaveLoadGadget("LOAD")
	var refusal error
	if modal := f.shell.frontend.Panels.Modal(); modal != nil {
		refusal = fmt.Errorf("%s", modal.Message())
		f.shell.frontend.Panels.CloseModal()
	}
	f.shell.activateSaveLoadGadget("CANCEL")
	path := session.RetailSavePath(f.saveDir, name)
	if refusal != nil {
		return path, refusal
	}
	if _, err := os.Stat(path); err != nil {
		return path, fmt.Errorf("no bank written: %w", err)
	}
	return path, nil
}

// fuOrderMobileBuild sends the commander a mobile build for product at the
// first legal site among a ring of offsets, resolved through the same
// preview/commit predicate the build cursor uses [07 §9].
func fuOrderMobileBuild(t *testing.T, b *battleSession, builder *units.Unit, product string) {
	t.Helper()
	def, ok := b.cat.Unit(product)
	if !ok || def == nil {
		t.Fatalf("%s is not in the battle catalog", product)
	}
	footX, footZ := footprintCellsForCatalog(b.cat, def)
	offsets := [][2]int32{{128, 0}, {-128, 0}, {0, 128}, {0, -128}, {160, 160}, {-160, 160}, {160, -160}, {-160, -160}, {224, 0}, {0, 224}}
	var lastErr error
	for _, off := range offsets {
		wx := builder.X + numeric.Fixed(int64(off[0])<<16)
		wz := builder.Z + numeric.Fixed(int64(off[1])<<16)
		cellX, cellZ := world.PlacementAnchor(wx, wz, footX, footZ)
		result, err := b.sess.PreviewPlacementForCursor(cellX, cellZ, def, footX, footZ, builder.Handle)
		if err != nil {
			lastErr = err
			continue
		}
		cx, cz := world.PlacementCenter(cellX, cellZ, footX, footZ)
		wy := numeric.Fixed(int64(result.SiteHeight) << 16)
		if err := b.sess.EnqueueHumanCommand(session.HumanCommand{Kind: session.HumanMobileBuild, MobileBuild: session.HumanMobileBuildCommand{
			Builder: builder.Handle, Product: product, WX: cx, WY: wy, WZ: cz,
		}}); err != nil {
			t.Fatalf("enqueue mobile build: %v", err)
		}
		return
	}
	t.Fatalf("no legal site for %s around the commander: %v", product, lastErr)
}

func fuNanoframeOf(sess *session.Session, owner uint8, name string) *units.Unit {
	return fuFindUnit(sess, owner, func(u *units.Unit) bool { return strings.EqualFold(u.Def.UnitName, name) && u.Remaining > 0 })
}

func fuCompleteUnitOf(sess *session.Session, owner uint8, name string) *units.Unit {
	return fuFindUnit(sess, owner, func(u *units.Unit) bool { return strings.EqualFold(u.Def.UnitName, name) && u.Remaining == 0 })
}

func fuCombatNear(sess *session.Session) bool {
	if fuEvents(sess) > 0 {
		return true
	}
	local := sess.LocalOwner
	locals := fuUnitsOwnedBy(sess, local)
	for _, u := range sess.Units.Iter() {
		if u == nil || !u.Alive || u.Owner == local || u.Def == nil || !fuMobile(sess, u) {
			continue
		}
		for _, l := range locals {
			dx, dz := int64((u.X-l.X)>>16), int64((u.Z-l.Z)>>16)
			if dx*dx+dz*dz < 320*320 {
				return true
			}
		}
	}
	return false
}

func fuBattleIntact(f *fuShell, b *battleSession, tick uint32) (bool, uint32) {
	if f.shell.battle != b || b.sess.Clock.GlobalTick != tick {
		return false, b.sess.Clock.GlobalTick
	}
	f.step(30)
	return f.shell.battle == b && b.sess.Clock.GlobalTick == tick+30, b.sess.Clock.GlobalTick
}

// fuOffender parses the unit handle out of the economy account refusal and
// returns that unit's row.
func fuOffender(sess *session.Session, msg string) *fuUnitRow {
	const key = "unit handle "
	i := strings.LastIndex(msg, key)
	if i < 0 {
		return nil
	}
	rest := msg[i+len(key):]
	if j := strings.IndexByte(rest, ' '); j >= 0 {
		rest = rest[:j]
	}
	h, err := strconv.Atoi(rest)
	if err != nil {
		return nil
	}
	u := sess.Units.Unit(pool.Handle(h))
	if u == nil {
		return nil
	}
	row := fuUnitRowOf(u)
	return &row
}

func fuStartSkirmish(f *fuShell, seed int64) *battleSession {
	shell := f.shell
	shell.opts.Seed = seed
	shell.ensureRetailSkirmishControllers()
	shell.setup.NumPlayers = 2
	shell.retailControllers[0] = 1
	shell.retailControllers[1] = 2
	shell.setup.Players[0].AllyGroup = 0
	shell.setup.Players[1].AllyGroup = 1
	shell.startBattleLoad(fuSkirmishMap)
	return f.waitBattle(fmt.Sprintf("skirmish seed %d", seed))
}

func TestFUPlaytestSaveLoadSequence(t *testing.T) {
	f := fuNewShell(t, 7)
	shell := f.shell
	dir := fuOutDir(t)
	report := map[string]any{}
	var saves []fuSaveNote
	var corrupt []fuCorruptNote
	defer func() {
		report["saves"] = saves
		report["corrupt"] = corrupt
		fuWriteJSON(t, dir, "saveload-sequence.json", report)
	}()

	b1 := fuStartSkirmish(f, 7)
	sess := b1.sess
	local := sess.LocalOwner
	report["unit_limit"] = shell.setup.UnitLimit
	f.step(60)
	commander := fuFindUnit(sess, local, func(u *units.Unit) bool { return u.Def.Commander })
	if commander == nil {
		t.Fatal("the local player has no commander")
	}

	// Construction: the commander starts a kbot lab beside itself.
	const lab, product = "ARMLAB", "ARMPW"
	fuOrderMobileBuild(t, b1, commander, lab)
	if !f.stepUntil(900, func(b *battleSession) bool { return fuNanoframeOf(b.sess, local, lab) != nil }) {
		t.Fatalf("the commander never started %s: construction diagnostics %+v", lab, sess.Build.CommandDiagnostics())
	}
	nano := fuNanoframeOf(sess, local, lab)
	t.Logf("%s nanoframe %d at tick %d, remaining %.3f", lab, nano.Handle, sess.Clock.GlobalTick, nano.Remaining)
	hashA, _ := sess.PartialStateFingerprint()
	pathA, err := fuTrySave(f, "construction")
	if err != nil {
		t.Fatalf("save during construction refused: %v", err)
	}
	summaryA, sizeA := fuReadSummary(t, pathA)
	noteA := fuSaveNote{Label: "construction", Path: pathA, Bytes: sizeA, Tick: sess.Clock.GlobalTick, Summary: summaryA, Census: fuTakeCensus(sess), SavedHash: hashA}

	// Restore A through the load dialog: the battle object is replaced and the
	// clock resumes at the saved tick.
	if msg, err := f.loadViaDialog("construction"); err != nil || msg != "" {
		t.Fatalf("load construction: err=%v modal=%q", err, msg)
	}
	b2 := shell.battle
	if b2 == nil || b2 == b1 {
		t.Fatalf("load did not install a restored battle: %p -> %p", b1, b2)
	}
	sess = b2.sess
	noteA.RestoredAt = sess.Clock.GlobalTick
	noteA.RestoreHash, _ = sess.PartialStateFingerprint()
	saves = append(saves, noteA)
	if noteA.RestoredAt != noteA.Tick {
		t.Errorf("restored battle resumes at tick %d, saved at %d", noteA.RestoredAt, noteA.Tick)
	}
	t.Logf("restored construction save at tick %d (saved hash %s, restored hash %s)", noteA.RestoredAt, noteA.SavedHash, noteA.RestoreHash)
	if fuNanoframeOf(sess, local, lab) == nil && fuCompleteUnitOf(sess, local, lab) == nil {
		t.Errorf("the restored battle carries no %s", lab)
	}

	// New commands on the restored battle: the commander finishes the lab
	// (its build order came back with the queue family), the lab is told to
	// build one unit, and the commander is sent a move order.
	commander = fuFindUnit(sess, local, func(u *units.Unit) bool { return u.Def.Commander })
	if commander == nil {
		t.Fatal("the restored battle has no local commander")
	}
	if !f.stepUntil(6000, func(b *battleSession) bool { return fuCompleteUnitOf(b.sess, local, lab) != nil }) {
		t.Fatalf("%s did not complete after the restore: diagnostics %+v", lab, sess.Build.CommandDiagnostics())
	}
	labUnit := fuCompleteUnitOf(sess, local, lab)
	t.Logf("%s complete at tick %d", lab, sess.Clock.GlobalTick)
	if err := sess.EnqueueHumanCommand(session.HumanCommand{Kind: session.HumanFactoryBuild, FactoryBuild: session.HumanFactoryBuildCommand{Builder: labUnit.Handle, Product: product, Count: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := sess.EnqueueHumanCommand(session.HumanCommand{Kind: session.HumanOrder, Order: session.HumanOrderCommand{
		Handles: []pool.Handle{commander.Handle}, Code: 1,
		Position: orders.ResolvePos{X: commander.X - numeric.Fixed(96<<16), Y: commander.Y, Z: commander.Z},
	}}); err != nil {
		t.Fatal(err)
	}
	f.step(2)
	queued := false
	if q := orders.QueueOfUnit(labUnit); q != nil {
		for _, n := range q.Primary() {
			if n != nil && strings.EqualFold(n.BuildDefKey, product) {
				queued = true
			}
		}
	}
	if !queued {
		t.Fatalf("the factory build for %s is not on %s's queue: diagnostics %+v", product, lab, sess.Build.CommandDiagnostics())
	}
	if !f.stepUntil(6000, func(b *battleSession) bool { return fuCompleteUnitOf(b.sess, local, product) != nil }) {
		t.Fatalf("%s never completed a %s: diagnostics %+v", lab, product, sess.Build.CommandDiagnostics())
	}
	t.Logf("%s complete at tick %d; commander at %d,%d", product, sess.Clock.GlobalTick, int32(commander.X>>16), int32(commander.Z>>16))

	// Save again on the restored battle.
	hashB, _ := sess.PartialStateFingerprint()
	noteB := fuSaveNote{Label: "factory (restored battle)", Tick: sess.Clock.GlobalTick, Census: fuTakeCensus(sess), SavedHash: hashB}
	pathB, err := fuTrySave(f, "factory")
	noteB.Path = pathB
	if err != nil {
		noteB.Error = err.Error()
		noteB.Offender = fuOffender(sess, err.Error())
		t.Errorf("the restored battle refused its second save at tick %d: %v (offending unit %+v) — see TestFUPlaytestSaveAfterRestore", sess.Clock.GlobalTick, err, noteB.Offender)
	} else {
		noteB.Summary, noteB.Bytes = fuReadSummary(t, pathB)
	}
	saves = append(saves, noteB)

	// The rest of the sequence on a fresh battle: run to combat, save,
	// restore, refuse corrupt banks, enter another battle.
	b3 := fuStartSkirmish(f, 7)
	sess = b3.sess
	reached := f.stepUntil(30000, func(b *battleSession) bool { return fuCombatNear(b.sess) })
	report["combat_reached"] = reached
	report["combat_tick"] = sess.Clock.GlobalTick
	if !reached {
		t.Logf("no combat within 30000 viewer ticks; saving the battle as it stands")
	}
	hashC, _ := sess.PartialStateFingerprint()
	pathC, err := fuTrySave(f, "combat")
	if err != nil {
		t.Fatalf("save during combat refused: %v", err)
	}
	summaryC, sizeC := fuReadSummary(t, pathC)
	noteC := fuSaveNote{Label: "combat", Path: pathC, Bytes: sizeC, Tick: sess.Clock.GlobalTick, Summary: summaryC, Census: fuTakeCensus(sess), SavedHash: hashC}
	f.step(300)
	if msg, err := f.loadViaDialog("combat"); err != nil || msg != "" {
		t.Fatalf("load combat: err=%v modal=%q", err, msg)
	}
	b4 := shell.battle
	if b4 == nil || b4 == b3 {
		t.Fatal("the combat load did not install a restored battle")
	}
	sess = b4.sess
	noteC.RestoredAt = sess.Clock.GlobalTick
	noteC.RestoreHash, _ = sess.PartialStateFingerprint()
	saves = append(saves, noteC)
	if noteC.RestoredAt != noteC.Tick {
		t.Errorf("combat restore resumes at tick %d, saved at %d", noteC.RestoredAt, noteC.Tick)
	}
	f.step(60)
	t.Logf("combat save restored at tick %d and stepped to %d", noteC.RestoredAt, sess.Clock.GlobalTick)

	// Corrupt bank 1: a truncated file. It never lists (no readable Summary),
	// so it goes through the explicit path seam; the active battle must stay.
	data, err := os.ReadFile(pathC)
	if err != nil {
		t.Fatal(err)
	}
	truncPath := filepath.Join(f.saveDir, "TRUNC.SAV")
	if err := os.WriteFile(truncPath, data[:len(data)/3], 0o644); err != nil {
		t.Fatal(err)
	}
	tickBefore := sess.Clock.GlobalTick
	n1 := fuCorruptNote{Label: "truncated to one third", Path: truncPath, Route: "loadRetailSavePath", TickBefore: tickBefore}
	for _, e := range enumerateRetailSaves(f.saveDir) {
		if e.Path == truncPath {
			n1.Listed = true
		}
	}
	if err := shell.loadRetailSavePath(truncPath); err != nil {
		n1.Err = err.Error()
	}
	n1.Replaced = shell.battle != b4
	if !n1.Replaced {
		n1.BattleKept, n1.StepsAfter = fuBattleIntact(f, b4, tickBefore)
	}
	corrupt = append(corrupt, n1)
	if n1.Err == "" {
		t.Errorf("the truncated bank loaded without an error")
	}
	if !n1.BattleKept {
		t.Errorf("the truncated bank's failed load disturbed the active battle: %+v", n1)
	}

	// Corrupt bank 2: a bank with a valid header whose body is damaged. If it
	// lists, it goes through the dialog and the authored refusal box; either
	// way a returned failure must leave the active battle intact.
	damaged := append([]byte(nil), data...)
	start := len(damaged) * 3 / 5
	for i := start; i < start+256 && i < len(damaged); i++ {
		damaged[i] ^= 0xFF
	}
	damPath := filepath.Join(f.saveDir, "DAMAGED.SAV")
	if err := os.WriteFile(damPath, damaged, 0o644); err != nil {
		t.Fatal(err)
	}
	tickBefore = sess.Clock.GlobalTick
	n2 := fuCorruptNote{Label: "256 bytes inverted at 60%", Path: damPath, TickBefore: tickBefore}
	listedAs := ""
	for _, e := range enumerateRetailSaves(f.saveDir) {
		if e.Path == damPath {
			n2.Listed, listedAs = true, e.Description
		}
	}
	if n2.Listed {
		n2.Route = "load dialog"
		msg, err := f.loadViaDialog(listedAs)
		if err != nil {
			n2.Err = err.Error()
		}
		n2.Modal = msg
	} else {
		n2.Route = "loadRetailSavePath"
		if err := shell.loadRetailSavePath(damPath); err != nil {
			n2.Err = err.Error()
		}
	}
	n2.Replaced = shell.battle != b4
	if n2.Replaced {
		n2.ReplacedTick = shell.battle.sess.Clock.GlobalTick
	} else {
		n2.BattleKept, n2.StepsAfter = fuBattleIntact(f, b4, tickBefore)
	}
	corrupt = append(corrupt, n2)
	if (n2.Err != "" || n2.Modal != "") && !n2.BattleKept {
		t.Errorf("the damaged bank's failed load disturbed the active battle: %+v", n2)
	}
	if n2.Err == "" && n2.Modal == "" && !n2.Replaced {
		t.Errorf("the damaged bank neither failed nor replaced the battle: %+v", n2)
	}
	t.Logf("damaged bank: listed=%v route=%s err=%q modal=%q replaced=%v kept=%v", n2.Listed, n2.Route, n2.Err, n2.Modal, n2.Replaced, n2.BattleKept)

	// Another battle in the same process: a fresh skirmish replaces whatever
	// battle is active.
	active := shell.battle
	b5 := fuStartSkirmish(f, 11)
	if b5 == active {
		t.Fatal("the second skirmish did not replace the active battle")
	}
	if b5.sess.Clock.GlobalTick != 0 {
		t.Errorf("fresh skirmish starts at tick %d", b5.sess.Clock.GlobalTick)
	}
	f.step(120)
	report["another_battle_tick"] = b5.sess.Clock.GlobalTick
	report["another_battle_state"] = b5.sess.State.String()
	if b5.sess.Clock.GlobalTick < 120 {
		t.Errorf("the second skirmish did not advance: tick %d", b5.sess.Clock.GlobalTick)
	}
	t.Logf("another skirmish at tick %d state %s", b5.sess.Clock.GlobalTick, b5.sess.State.String())
}
