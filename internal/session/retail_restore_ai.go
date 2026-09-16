package session

import (
	"fmt"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// initializeRestoredBattleAI is the load path's copy of the per-player reset
// that constructs a computer player's planner, and it is deliberately not a
// restore: retail's world rebuild "runs next, once per battle, for every kind
// and for loads alike", and its per-player reset step builds the AI record —
// the ten task records with their initial deadlines, the strategic state with
// its eight simulation draws, the classifier table entry — then loads the AI
// profile and applies the difficulty tables to every computer-controlled slot
// [08 R-ENTRY-01 §3 step 24]. That step runs BEFORE the restoration
// dispatcher, so a loaded battle's planner is a battle-entry planner that then
// meets a restored world, never a planner reconstructed from the bank: the
// strategic state, the class vectors and the manager tasks are absent from
// every account [08 R-SAVE-02 §11], and the one AI word the bank does carry is
// each unit's group index, whose reader enrols the unit in the owner's group
// vector as it is restored [08 R-SAVE-02 §6]. The load-path answer is stated
// whole in [08 R-SAVE-02 §11-A].
//
// The slot rule is the reset's own: every slot whose controller byte is
// non-zero gets a record unless the controller is 3 (a remote peer), humans
// included — a human's record is built and simply never dispatched, because
// the manager's outer gate admits only controller 2 [08 R-AI-01 §1]. The
// controller bytes come from the `Player%i` accounts the stage has already
// applied to the economy service, which is also where retail reads them
// [08 "Player records"].
//
// Difficulty is not in the player record and is not re-derived from the units:
// it is the Summary account's `Difficulty` word, restored with the rest of the
// game metadata before battle entry [08 "Summary"] [08 "Load process" step 3],
// which the stage has already copied onto the session. initializeBattleAI
// applies it to the shared profile through the ordinary plan gate
// [08 R-SKIR-01 §9] [08 R-AI-01 §12].
func initializeRestoredBattleAI(s *Session, fs vfs.FSOps, m *mission.Mission, sessionKind int) error {
	if s == nil || s.Econ == nil {
		return fmt.Errorf("session: retail restore: no economy service for AI construction")
	}
	slots := make([]uint8, 0, len(s.Econ.Players))
	for i := range s.Econ.Players {
		if i >= len(s.AI) {
			break
		}
		p := &s.Econ.Players[i]
		// Controller 0 is an empty slot and controller 3 is a remote peer; the
		// reset skips both and builds a record for everything else
		// [08 R-ENTRY-01 §3 step 24].
		if !p.Exists || p.ControllerState == 0 || p.ControllerState == 3 {
			continue
		}
		slots = append(slots, uint8(i))
	}
	for i := range s.AI {
		s.AI[i] = nil
	}
	if len(slots) == 0 {
		return nil
	}
	// One profile record is shared by every slot, resolved through the mission's
	// authored name with the established `ai/default.txt` fallback
	// [08 R-P0-05 §7] [08 R-AI-01 §12]. A restored battle resolves it exactly as
	// a fresh one does: the profile is not in the bank either.
	profileName := "default"
	if m != nil && m.OTA != nil && m.OTA.Global != nil {
		if mg := mission.DecodeMissionGlobals(m.OTA.Global); mg != nil && strings.TrimSpace(mg.AIProfile) != "" {
			profileName = mg.AIProfile
		}
	}
	prof, err := loadSkirmishAIProfile(fs, profileName)
	if err != nil {
		return err
	}
	// Ascending slot order: each constructor consumes its exact eight strategic
	// draws from the shared stream in that order [08 R-ENTRY-01 §3 step 24].
	for _, slot := range slots {
		if err := initializeBattleAI(s, slot, prof, sessionKind); err != nil {
			return err
		}
	}
	return nil
}

// finishRestoredBattleEntry is the load path's battle-entry tail. Retail runs
// the same tail for a restored battle as for a fresh one with two differences,
// both named by the load-path summary of [08 R-ENTRY-01 §8]: the per-player
// phase is primed "on the restored world at the restored global tick" rather
// than at tick 0, and the second starting-resource grant is skipped, because
// the restored stocks are the authoritative ones. The metal-spot list is then
// rebuilt for every slot that owns an AI record, exactly as on the fresh path.
//
// The prime is what puts a restored computer player back to work in the same
// pass retail does: every task record was constructed with deadline 0 and the
// dispatcher's compare is `deadline <= tick` unsigned, so all ten task slots
// are due at the restored tick and run once, in ascending slot order, before
// the first restored tick is ever stepped [08 R-AI-01 §1] [08 R-ENTRY-01 §8
// step 4]. Their vectors are already populated, because the unit restore
// enrolled every saved unit in its saved group [08 R-SAVE-02 §6].
//
// The prime is also where the restored player's income and expense aggregates
// come back, and it is the only place they can: the `Player%i` account carries
// no per-pass rate item (the restore site in retail_restore_core.go states that
// census), and the planner keeps no copy of its own — the resource score reads
// the settled production and consumption pair straight off the economy player
// record, the same record the HUD resource bar samples its four per-pass floats
// from [08 "Established AI-facing data and rooted planner"] [05 R-ECO-01 §6].
// One event refills both consumers, the owning slot's next settlement:
// immediately in this prime when the restored `UpdateTime` is already due at
// the restored tick, and otherwise within thirty ticks [08 R-ENTRY-01 §8]
// [05 "Authoritative settlement order"].
func finishRestoredBattleEntry(s *Session) error {
	if s == nil || s.Econ == nil {
		return fmt.Errorf("session: retail restore: missing economy for battle-entry tail")
	}
	if s.battleEntryTailDone {
		return nil
	}
	if s.Clock == nil {
		return fmt.Errorf("session: retail restore: missing clock for battle-entry tail")
	}
	s.RegisterAll()
	s.clearWatcherVisibilityMasks()
	s.stepPlayerPhase(s.Clock.GlobalTick)
	for _, mgr := range s.AI {
		if mgr != nil {
			mgr.Strategic.InitializeMetalSpots(s.World)
		}
	}
	s.battleEntryTailDone = true
	return nil
}
