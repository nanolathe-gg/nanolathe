package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func TestResultStatisticsAndScoreFreeze(t *testing.T) {
	ota, err := formats.LoadOTA([]byte(`[GlobalHeader]
{
	killmul=2.5;
	timemul=0.5;
}`))
	if err != nil {
		t.Fatal(err)
	}
	s := &Session{
		Clock:   &clock.State{GlobalTick: 600},
		Econ:    &economy.Service{},
		Mission: &mission.Mission{OTA: ota},
	}
	for i := 0; i < 2; i++ {
		s.Econ.Players[i] = economy.Player{Exists: true, ControllerState: 1, Side: uint8(i + 1)}
	}
	s.Econ.Players[0].Name = "victim"
	s.Econ.Players[0].TotalProduced[economy.Energy] = 100.75
	s.Econ.Players[0].Waste[economy.Metal] = 101.25
	s.RecordDeathStatistics(combat.DeathCreditInput{
		Cause: combat.CauseOrdinary, VictimOwner: 0, AttackerSide: 1,
		AttackerPresent: true, VictimCommander: true,
	})
	rows := s.collectScores(101, false)
	if len(rows) != 2 {
		t.Fatalf("rows=%d, want two active non-neutral players", len(rows))
	}
	if rows[0].Losses != 1 || rows[0].CommandersLost != 1 {
		t.Fatalf("victim row=%+v, want one unit and commander loss", rows[0])
	}
	if rows[1].Kills != 1 || rows[1].CommandersKilled != 1 || rows[1].Score != 7 {
		t.Fatalf("attacker row=%+v, want kill/commander kill and truncated score 7", rows[1])
	}
	if rows[0].EnergyProduced != 100 || rows[0].MetalWasted != 101 {
		t.Fatalf("result truncation row=%+v", rows[0])
	}
	max := resultColumnMaxima(rows)
	if max != [7]int{10, 10, 100, 100, 100, 101, 100} {
		t.Fatalf("column maxima=%v", max)
	}

	// The finalizer bridge consumes O2's stored packet provenance, so this
	// exercises the true death boundary rather than calling the accounting
	// helper directly. Cause 11 is explicitly no-credit; cause 5 keeps its
	// stored-side gates.
	def := &content.UnitDef{UnitName: "victim", MaxDamage: 100, Script: &cob.Program{Code: []uint32{0x10065000}}}
	for _, tc := range []struct {
		name      string
		cause     combat.Cause
		side      uint8
		wantLoss  int16
		wantKills int16
	}{
		{name: "ordinary", cause: combat.CauseOrdinary, side: 0, wantLoss: 1, wantKills: 1},
		{name: "water", cause: combat.CauseWaterDamage, side: 0},
		{name: "reclaim self", cause: combat.CauseReclaim, side: 1},
		{name: "reclaim enemy", cause: combat.CauseReclaim, side: 0, wantLoss: 1, wantKills: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			world := units.NewSliced(2, nil)
			h, err := world.Create(def, 1, 0, 0, 0)
			if err != nil {
				t.Fatal(err)
			}
			victim := world.Unit(h)
			victim.LastDamageCause = uint8(tc.cause)
			victim.LastDamageSide = tc.side
			econ := &economy.Service{}
			econ.Players[0].Exists, econ.Players[1].Exists = true, true
			session := &Session{Units: world, Econ: econ}
			session.RegisterAll()
			world.Destroy(h, units.DeathKilled)
			if got := world.FinalizeDeath(h, 1); !got.Freed {
				t.Fatalf("FinalizeDeath=%+v", got)
			}
			if econ.Players[1].Losses != tc.wantLoss || econ.Players[0].Kills != tc.wantKills {
				t.Fatalf("stats victim=%+v attacker=%+v, want loss=%d kills=%d", econ.Players[1], econ.Players[0], tc.wantLoss, tc.wantKills)
			}
		})
	}
}

func TestResultScoreUsesRetailFloat32Boundary(t *testing.T) {
	ota, err := formats.LoadOTA([]byte(`[GlobalHeader]
{
killmul=0;
timemul=1;
}`))
	if err != nil {
		t.Fatal(err)
	}
	// tick/60 is 16,777,217. Retail narrows that integer to float32 before
	// multiplying, which rounds it down to 16,777,216; a float64 result path
	// would retain the extra one [08 R-CAMP-01 §7].
	s := &Session{Clock: &clock.State{GlobalTick: 60 * 16_777_217}, Mission: &mission.Mission{OTA: ota}}
	if got := s.resultScore(0); got != 16_777_216 {
		t.Fatalf("production resultScore = %d, want float32-rounded 16777216", got)
	}
}
