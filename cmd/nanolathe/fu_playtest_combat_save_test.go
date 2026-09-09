//go:build retail

package main

// Minimal reproduction for the fu-playtest scenario 3 finding: a fresh
// skirmish refuses to be saved during combat because a live unit's plain
// engagement link names a unit slot the stable-identifier map no longer
// holds. Combat writes the shooter into the victim's engagement link, and the
// save projection resolves that link through the stable-ID map built from
// the live pool [08 R-SAVE-02 §6] [04 R-UNIT-06 §5].
//
// The probe runs the seed-7 skirmish to its first kill or loss and then
// attempts the save projection every 30 ticks for the next 900, recording the
// first refusal and the state of the slot the refusal names.

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
)

func fuHandleAfter(msg, key string) (pool.Handle, bool) {
	i := strings.LastIndex(msg, key)
	if i < 0 {
		return 0, false
	}
	rest := strings.TrimSpace(msg[i+len(key):])
	if j := strings.IndexAny(rest, " :"); j >= 0 {
		rest = rest[:j]
	}
	h, err := strconv.Atoi(rest)
	if err != nil {
		return 0, false
	}
	return pool.Handle(h), true
}

func TestFUPlaytestSaveDuringCombat(t *testing.T) {
	root := probeRetail(t)
	opts := Options{Root: root, Map: fuSkirmishMap, Seed: 7}
	cs, err := openContent(opts)
	if err != nil {
		t.Skipf("retail assets unavailable: %v", err)
	}
	defer cs.Close()
	rng.SeedGlobal(7, 7)
	sess, _, err := newBattleSession(opts, cs)
	if err != nil {
		t.Fatal(err)
	}
	report := map[string]any{}
	defer func() { fuWriteJSON(t, fuOutDir(t), "combat-save-repro.json", report) }()

	scaledNow := sess.Clock.ScaledAnchor
	for sess.State != session.StatePostBattle && sess.Clock.GlobalTick < 54000 && fuEvents(sess) == 0 {
		scaledNow += 5
		sess.Step(scaledNow)
	}
	if fuEvents(sess) == 0 {
		t.Skipf("no kill or loss by tick %d", sess.Clock.GlobalTick)
	}
	report["first_event_tick"] = sess.Clock.GlobalTick
	t.Logf("first kill/loss at tick %d", sess.Clock.GlobalTick)

	var attempts []map[string]any
	var firstErr error
	var firstTick uint32
	for i := 0; i < 30 && sess.State != session.StatePostBattle; i++ {
		err := fuSaveInputsErr(sess, "combat")
		attempt := map[string]any{"tick": sess.Clock.GlobalTick, "error": fmt.Sprint(err)}
		if err != nil {
			if h, ok := fuHandleAfter(err.Error(), "target handle "); ok {
				attempt["target_handle"] = uint32(h)
				if u := sess.Units.Unit(h); u != nil {
					attempt["target_unit"] = fuUnitRowOf(u)
				} else {
					attempt["target_unit"] = "slot holds no unit"
				}
				var referrers []fuUnitRow
				for _, u := range sess.Units.Iter() {
					if u.EngagementTarget == h {
						referrers = append(referrers, fuUnitRowOf(u))
					}
				}
				attempt["units_linking_to_target"] = referrers
			}
			if firstErr == nil {
				firstErr, firstTick = err, sess.Clock.GlobalTick
			}
		}
		attempts = append(attempts, attempt)
		for j := 0; j < 6; j++ {
			scaledNow += 5
			sess.Step(scaledNow)
		}
	}
	report["attempts"] = attempts
	if firstErr != nil {
		t.Fatalf("save projection refused at tick %d during combat: %v [08 R-SAVE-02 §6]", firstTick, firstErr)
	}
	t.Logf("all %d save projections during combat succeeded", len(attempts))
}
