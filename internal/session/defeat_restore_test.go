package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/save"
)

// Local defeat reads only the live count [08 R-SKIR-01 §3]. The economy's
// separate elimination predicate retains its created-count term, including
// after a load rebuilds both counters from surviving units [05 R-ECO-01 §12].
func TestLocalDefeatWithNoCreatedUnits(t *testing.T) {
	w, def := eliminationFixtureWorld(t)
	if _, err := w.Create(def, 1, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	cfg := SkirmishConfig{NumPlayers: 2}
	cfg.ApplyDefaults()
	s := &Session{Units: w, Skirmish: cfg, State: StateBattle, Latch: NewEndLatch()}
	if economy.PlayerEliminated(w, 0) {
		t.Fatal("a zero created count must keep the economy settlement gate open")
	}
	if !s.localDefeated() {
		t.Fatal("zero local live count must satisfy defeat even with no created units")
	}
	for tick := uint32(30); tick <= 180; tick += 30 {
		latched := s.EvaluateResult(tick)
		if latched != (tick == 180) {
			t.Fatalf("tick %d: latched=%v, want terminal only on sixth due", tick, latched)
		}
	}
	if !s.Latch.IsLose() || s.GetResult().Kind != "defeat" {
		t.Fatalf("empty local side did not lose: %+v", s.GetResult())
	}
}

// Saving during a pending defeat is legal [08 R-SAVE-02 §4]. Neither the
// created counter nor the end countdown is saved: loading an empty local
// side must start a fresh six-due countdown, then lose or respawn according
// to the commander-death rule [08 R-TRIG-01 §6] [08 R-SKIR-01 §3].
func TestRetailLoadRestartsLocalDefeat(t *testing.T) {
	f := loadRetailFixture(t)
	for _, mode := range []gameplay.Mode{gameplay.Modern, gameplay.Strict31, gameplay.Community39} {
		for _, rule := range []CommanderDeathRule{CommanderDeathContinues, CommanderDeathDeathmatch} {
			name := string(mode) + "/defeat"
			if rule == CommanderDeathDeathmatch {
				name = string(mode) + "/respawn"
			}
			t.Run(name, func(t *testing.T) {
				f.cfg.Gameplay, f.cfg.CommanderDeath = mode, int(rule)
				src := f.session(t)
				src.State = StateBattle
				local := int(src.LocalOwner)
				for _, u := range src.Units.IterSliced() {
					if u != nil && u.Alive && int(u.Owner) == local {
						u.Dying = true
						if !src.Units.FinalizeDeath(u.Handle, 30).Freed {
							t.Fatal("finalize local unit")
						}
					}
				}
				if !economy.PlayerEliminated(src.Units, local) || src.Units.LiveCountForPlayer(1-local) == 0 {
					t.Fatal("fixture requires an eliminated local player and a surviving opponent")
				}
				for tick := uint32(30); tick <= 90; tick += 30 {
					src.EvaluateResult(tick)
				}
				src.Clock.GlobalTick = 90
				src.Econ.Players[local].UpdateTime = 120
				if src.State != StateBattle || src.Latch.Countdown != 2 {
					t.Fatalf("source must remain saveable during countdown: state=%v latch=%+v", src.State, src.Latch)
				}
				inputs, err := src.RetailBattleSaveInputs(RetailBattleSummary(src, "pending defeat", "1", src.Skirmish.UnitLimit), save.Camera{})
				if err != nil {
					t.Fatal(err)
				}
				projection, err := src.RetailProjection(inputs)
				if err != nil {
					t.Fatal(err)
				}
				data, err := projection.Bytes()
				if err != nil {
					t.Fatal(err)
				}
				bank, err := save.OpenBytes(data)
				if err != nil {
					t.Fatal(err)
				}
				loaded, err := LoadRetailSaveWithDeps(bank, RetailLoadDeps{
					FS: f.fs, Catalog: f.cat, SimSeed: 1, CRTSeed: 2,
					UnitLimit: src.Skirmish.UnitLimit, Gameplay: mode,
				})
				if err != nil {
					t.Fatal(err)
				}
				dst := loaded.Battle.Session
				if dst.Units.LiveCountForPlayer(local) != 0 || dst.Units.CreatedCountForPlayer(local) != 0 || economy.PlayerEliminated(dst.Units, local) {
					t.Fatal("load must rebuild the empty local side with both counters zero")
				}
				if dst.Latch.Countdown != -1 || dst.resultPending || dst.Econ.Players[local].UpdateTime != 120 {
					t.Fatalf("load must retain settlement phase but reset countdown: latch=%+v due=%d", dst.Latch, dst.Econ.Players[local].UpdateTime)
				}
				simDraws, crtDraws := dst.SimRNG().Draws(), dst.CrtRNG().Draws()
				for tick := uint32(120); tick <= 270; tick += 30 {
					dst.Clock.GlobalTick = tick
					dst.endConditionBlock(local, tick)
					if tick < 270 {
						want := int16(4 - (tick-120)/30)
						if dst.Latch.Countdown != want || dst.GetResult().Ended || dst.Units.LiveCountForPlayer(local) != 0 {
							t.Fatalf("tick %d: expected pending countdown %d, got %+v", tick, want, dst.Latch)
						}
						if dst.SimRNG().Draws() != simDraws || dst.CrtRNG().Draws() != crtDraws {
							t.Fatal("countdown spent random draws before its terminal due")
						}
					}
				}
				if dst.Units.LiveCountForPlayer(1-local) == 0 {
					t.Fatal("opponent must survive the local defeat path")
				}
				if rule == CommanderDeathDeathmatch {
					if dst.Units.LiveCountForPlayer(local) == 0 || dst.resultPending || dst.GetResult().Ended || dst.Latch.Countdown != -1 || dst.State != StateBattle {
						t.Fatalf("sixth due must respawn and clear pending defeat: result=%+v latch=%+v", dst.GetResult(), dst.Latch)
					}
					if dst.SimRNG().Draws() <= simDraws {
						t.Fatal("respawn did not consume placement and allocation draws")
					}
				} else if !dst.GetResult().Ended || dst.GetResult().Kind != "defeat" || !dst.Latch.IsLose() || dst.State != StatePostBattle {
					t.Fatalf("sixth due must settle restored defeat: result=%+v latch=%+v state=%v", dst.GetResult(), dst.Latch, dst.State)
				}
			})
		}
	}
}
