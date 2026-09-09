//go:build retail

package main

// Minimal reproduction for the fu-playtest scenario 3 finding: a battle
// restored from a retail bank refuses to be saved again. The save projection
// asks the economy for every live unit's account image, and the restored
// session's economy holds no bucket for one handle [08 "Save-file
// organization"] [08 R-SAVE-02 §11] [05 "Saving economy, construction, and
// features"].
//
// The probe writes a bank from a fresh skirmish at several ticks — including
// the shell scenario's tick 133, when the computer commander's first
// extractor is still a nanoframe — restores each through the production
// path, audits which live units the restored economy can image, runs the
// restored session on, audits again, and then attempts the save. The fresh
// session, stepped to the same tick, is the control.

import (
	"fmt"
	"path/filepath"
	"sort"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

type fuUnitRow struct {
	Handle     uint32  `json:"handle"`
	Owner      uint8   `json:"owner"`
	Def        string  `json:"def"`
	Alive      bool    `json:"alive"`
	Dying      bool    `json:"dying"`
	Remaining  float32 `json:"remaining"`
	HasMover   bool    `json:"has_mover_mirror"`
	Activated  bool    `json:"activated"`
	Account    bool    `json:"economy_account"`
	AccountErr string  `json:"economy_account_error,omitempty"`
	Sliced     bool    `json:"in_sliced_walk"`
}

func fuUnitRowOf(u *units.Unit) fuUnitRow {
	r := fuUnitRow{Handle: uint32(u.Handle), Owner: u.Owner, Alive: u.Alive, Dying: u.Dying, Remaining: u.Remaining, HasMover: u.HasMover, Activated: u.Activated}
	if u.Def != nil {
		r.Def = u.Def.UnitName
	}
	return r
}

// fuAuditAccounts lists every live unit with whether the economy can image
// its account and whether the sliced walk the economy passes use visits it.
func fuAuditAccounts(s *session.Session) (rows []fuUnitRow, missing []uint32) {
	sliced := map[pool.Handle]bool{}
	for _, u := range s.Units.IterSliced() {
		if u != nil {
			sliced[u.Handle] = true
		}
	}
	for _, u := range s.Units.Iter() {
		r := fuUnitRowOf(u)
		_, err := s.Econ.RetailUnitAccountImage(u.Handle)
		r.Account = err == nil
		if err != nil {
			r.AccountErr = err.Error()
		}
		r.Sliced = sliced[u.Handle]
		if !r.Account {
			missing = append(missing, r.Handle)
		}
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Handle < rows[j].Handle })
	return rows, missing
}

// fuSaveInputsErr runs the whole save projection — inputs and the bank
// image — without writing a file; the economy account images are produced
// by the projection, not by the input gather [08 "Save-file organization"].
func fuSaveInputsErr(s *session.Session, label string) error {
	summary := session.RetailBattleSummary(s, label, "0", s.Skirmish.UnitLimit)
	in, err := s.RetailBattleSaveInputs(summary, save.Camera{})
	if err != nil {
		return err
	}
	_, err = session.ProjectRetailSession(s, in)
	return err
}

// fuStocks lists each live player's metal and energy stock, so a restored
// session's economy can be compared with the never-restored control.
func fuStocks(s *session.Session) [][2]float32 {
	var out [][2]float32
	for i := range s.Econ.Players {
		if p := &s.Econ.Players[i]; p.Exists {
			out = append(out, [2]float32{p.Stock[economy.Metal], p.Stock[economy.Energy]})
		}
	}
	return out
}

// fuGateRows lists, per live player, the words the settlement gate reads
// before running a slot's per-unit passes [05 "Authoritative settlement
// order"] [08 R-TRIG-01 §6].
func fuGateRows(s *session.Session) []map[string]any {
	var out []map[string]any
	for i := range s.Econ.Players {
		if p := &s.Econ.Players[i]; p.Exists {
			out = append(out, map[string]any{"player": i, "controller_state": p.ControllerState, "end_game_countdown": p.EndGameCountdown, "game_ended": p.GameEnded, "observer": p.IsObserver, "update_time": p.UpdateTime})
		}
	}
	return out
}

func fuDeadlines(s *session.Session) []uint32 {
	var out []uint32
	for i := range s.Econ.Players {
		if s.Econ.Players[i].Exists {
			out = append(out, s.Econ.Players[i].UpdateTime)
		}
	}
	return out
}

func fuStepTicks(s *session.Session, n uint32) {
	scaledNow := s.Clock.ScaledAnchor
	end := s.Clock.GlobalTick + n
	for s.State != session.StatePostBattle && s.Clock.GlobalTick < end {
		scaledNow += 5
		s.Step(scaledNow)
	}
}

func TestFUPlaytestSaveAfterRestore(t *testing.T) {
	root := probeRetail(t)
	opts := Options{Root: root, Map: fuSkirmishMap, Seed: 7}
	cs, err := openContent(opts)
	if err != nil {
		t.Skipf("retail assets unavailable: %v", err)
	}
	defer cs.Close()
	report := map[string]any{}
	defer func() { fuWriteJSON(t, fuOutDir(t), "restore-save-repro.json", report) }()

	for _, saveTick := range []uint32{133, 300, 600} {
		name := fmt.Sprintf("save_at_%d", saveTick)
		row := map[string]any{}
		report[name] = row
		rng.SeedGlobal(7, 7)
		src, _, err := newBattleSession(opts, cs)
		if err != nil {
			t.Fatal(err)
		}
		fuAdvance(src, saveTick, nil)
		path := filepath.Join(t.TempDir(), fmt.Sprintf("RESTORE%d.SAV", saveTick))
		summary := session.RetailBattleSummary(src, "restore", "0", src.Skirmish.UnitLimit)
		in, err := src.RetailBattleSaveInputs(summary, save.Camera{})
		if err != nil {
			t.Fatalf("%s: fresh session save inputs: %v", name, err)
		}
		if err := src.WriteRetailSave(path, in); err != nil {
			t.Fatal(err)
		}
		srcRows, _ := fuAuditAccounts(src)
		row["saved_units"] = srcRows
		row["saved_deadlines"] = fuDeadlines(src)

		loaded, err := session.LoadRetailSavePath(path, session.RetailLoadDeps{FS: cs.fs, Catalog: src.Catalog, SimSeed: 7, CRTSeed: 7, UnitLimit: src.Skirmish.UnitLimit})
		if err != nil {
			t.Fatalf("%s: restore: %v", name, err)
		}
		if loaded.Route != session.RetailLoadRouteBattleRestoration || loaded.Battle == nil || loaded.Battle.Session == nil {
			t.Fatalf("%s: restore route %d", name, loaded.Route)
		}
		dst := loaded.Battle.Session
		rows, missing := fuAuditAccounts(dst)
		row["restored_tick"] = dst.Clock.GlobalTick
		row["restored_units"] = rows
		row["restored_missing_accounts"] = missing
		row["restored_deadlines"] = fuDeadlines(dst)
		row["restored_gate"] = fuGateRows(dst)
		row["saved_gate"] = fuGateRows(src)
		t.Logf("%s: settlement gate words saved %v restored %v", name, fuGateRows(src), fuGateRows(dst))
		// The per-player settlement block runs only while the slot's signed
		// end-game countdown is negative; a fresh battle initializes it to -1
		// and the first true due sets it to 4 [08 R-TRIG-01 §6]
		// [05 "Authoritative settlement order"]. A restore that leaves it at
		// zero silences every settlement — and with it the production fills,
		// the admission passes and the end-condition hook seam of C5.
		for i := range dst.Econ.Players {
			restored, saved := &dst.Econ.Players[i], &src.Econ.Players[i]
			if restored.Exists && saved.EndGameCountdown < 0 && restored.EndGameCountdown >= 0 {
				t.Errorf("%s: player %d end-game countdown reads %d after the restore (saved session: %d); the settlement gate needs a negative countdown [08 R-TRIG-01 §6] [05 \"Authoritative settlement order\"]", name, i, restored.EndGameCountdown, saved.EndGameCountdown)
			}
		}
		immediate := fuSaveInputsErr(dst, "immediate")
		row["immediate_error"] = fmt.Sprint(immediate)
		t.Logf("%s: restored %d units at tick %d, %d without an economy account %v, immediate save error: %v", name, len(rows), dst.Clock.GlobalTick, len(missing), missing, immediate)

		// Run the restored session on past two settlement dues and a further
		// stretch, auditing at each stop.
		var firstFailure string
		for _, more := range []uint32{60, 300, 900} {
			fuStepTicks(dst, more)
			rows, missing := fuAuditAccounts(dst)
			stop := map[string]any{"tick": dst.Clock.GlobalTick, "missing_accounts": missing, "units": rows, "deadlines": fuDeadlines(dst), "stocks": fuStocks(dst)}
			errAfter := fuSaveInputsErr(dst, "after")
			stop["save_error"] = fmt.Sprint(errAfter)
			row[fmt.Sprintf("after_%d", dst.Clock.GlobalTick)] = stop
			t.Logf("%s: restored session at tick %d: %d units, %d without an account %v, save error: %v", name, dst.Clock.GlobalTick, len(rows), len(missing), missing, errAfter)
			if errAfter != nil && firstFailure == "" {
				firstFailure = errAfter.Error()
			}
		}
		// Control: the never-restored session stepped to the same tick saves.
		fuAdvance(src, dst.Clock.GlobalTick, nil)
		if err := fuSaveInputsErr(src, "control"); err != nil {
			t.Fatalf("%s: control (never restored) session refuses to save at tick %d: %v", name, src.Clock.GlobalTick, err)
		}
		row["control_error"] = ""
		row["control_stocks"] = fuStocks(src)
		row["control_units"] = len(src.Units.Iter())
		t.Logf("%s: at tick %d restored stocks %v (%d units) vs control stocks %v (%d units)", name, src.Clock.GlobalTick, fuStocks(dst), len(dst.Units.Iter()), fuStocks(src), len(src.Units.Iter()))
		if immediate != nil {
			t.Errorf("%s: a restored battle refuses to save before it has stepped: %v [08 R-SAVE-02 §11]", name, immediate)
		}
		if firstFailure != "" {
			t.Errorf("%s: a restored battle refuses to save after running on while the never-restored control saves: %s [08 R-SAVE-02 §11] [05 \"Saving economy, construction, and features\"]", name, firstFailure)
		}
	}
}
