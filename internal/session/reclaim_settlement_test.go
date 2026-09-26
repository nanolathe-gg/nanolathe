package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// Real intake only latches the victim; the normal finalizer owns the refund.
// Owner slices put the attacker on either side of the victim's slot, and raw
// slot cases lock the retained/reused identity contract [05 R-WORK-01 §4].
func TestFatalReclaimSettlementUsesRawAttackerAtVictimFinalization(t *testing.T) {
	for _, state := range []string{"live earlier", "live later", "freed", "reused", "null", "absent owner", "remote owner"} {
		t.Run(state, func(t *testing.T) {
			root := t.TempDir()
			writeCompositionModel(t, root, "fixture", 1)
			writeCompositionCOB(t, root, "testunit", []string{"modelroot", "modelchild"})
			fs := vfs.New()
			if err := fs.MountDirectory(root, 10); err != nil {
				t.Fatal(err)
			}
			defer fs.Close()
			def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "testunit"}, UnitName: "testunit", ObjectName: "fixture", MaxDamage: 100, Limit: -1, BMCode: 1}
			cat := &content.Catalog{Units: map[string]*content.UnitDef{def.CanonicalKey: def}}
			w, err := newSlicedWorldWithCOB(cat, fs)
			if err != nil {
				t.Fatal(err)
			}
			s := &Session{Catalog: cat, World: minimalTerrain(), Mission: syntheticMission(), Units: w, Econ: &economy.Service{}}
			s.InitBattleWindForSession()
			if err := createAndBindServicesForTest(t, s); err != nil {
				t.Fatal(err)
			}
			s.RegisterAll()
			def.BuildCostMetal = 100
			attackerOwner, victimOwner := uint8(0), uint8(1)
			if state == "live later" {
				attackerOwner, victimOwner = 1, 0
			}
			create := func(owner uint8) pool.Handle {
				t.Helper()
				h, err := s.Units.Create(def, owner, 0, 0, 0)
				if err != nil {
					t.Fatal(err)
				}
				return h
			}
			attacker := create(attackerOwner)
			victimHandle := create(victimOwner)
			victim := s.Units.Unit(victimHandle)
			victim.Health = 1
			victim.Remaining = 0.25
			// Medium discount, through the battle's difficulty word: the
			// session projects the ledger's selector from it on every bind,
			// so a selector written past the session would not survive the
			// intake's queue binding [05 R-ECO-01 §3].
			s.Skirmish.Difficulty, s.Mission.Difficulty = 1, 1
			s.RebindRules()
			s.Econ.Players[attackerOwner].Exists = true
			s.Econ.Players[attackerOwner].ControllerState = 2
			s.Econ.Players[victimOwner].Exists = true
			s.Econ.Players[victimOwner].ControllerState = 1
			if state == "live earlier" && attacker >= victimHandle {
				t.Fatal("fixture attacker must precede victim")
			}
			if state == "live later" && attacker <= victimHandle {
				t.Fatal("fixture attacker must follow victim")
			}
			rawAttacker := attacker
			want := float32(52.5)
			switch state {
			case "freed", "reused":
				s.Units.FreeImmediate(attacker)
				if state == "reused" {
					if got := create(attackerOwner); got != attacker {
						t.Fatalf("reuse got %d, want %d", got, attacker)
					}
				}
			case "null":
				rawAttacker = 0
				want = 0
			case "absent owner":
				s.Econ.Players[attackerOwner].Exists = false
				want = 75
			case "remote owner":
				s.Econ.Players[attackerOwner].ControllerState = 3
				want = 75
			}
			result := s.Combat.AcceptDamage(s.Units, 3, combat.DamageInput{Victim: victimHandle, Attacker: rawAttacker, Nominal: 2, Kind: 5})
			if !result.DeathLatched || !victim.Dying {
				t.Fatalf("intake did not latch: %+v", result)
			}
			if got := s.Econ.UnitBuckets(attacker)[economy.Metal].Production; got != 0 {
				t.Fatalf("intake paid early: %v", got)
			}
			var observed bool
			s.Units.OnDeathExtra = func(h pool.Handle, _ units.DeathCause, _ *units.Unit) {
				if h != victimHandle {
					return
				}
				observed = true
				if s.Units.Unit(victimHandle) == nil {
					t.Error("refund observation ran after slot release")
				}
				if got := s.Econ.UnitBuckets(attacker)[economy.Metal].Production; got != want {
					t.Errorf("refund before release=%v, want %v", got, want)
				}
			}
			if result := s.Units.FinalizeDeath(victimHandle, 3); !result.Freed || !observed {
				t.Fatal("normal finalizer did not run")
			}
			s.Units.FinalizeDeath(victimHandle, 3)
			if got := s.Econ.UnitBuckets(attacker)[economy.Metal].Production; got != want {
				t.Fatalf("refund repeated or missing: %v, want %v", got, want)
			}
			if got := s.Econ.UnitBuckets(attacker)[economy.Energy].Production; got != 0 {
				t.Fatalf("reclaim paid energy: %v", got)
			}
			if state == "freed" {
				if got := create(attackerOwner); got != attacker {
					t.Fatalf("post-refund reuse=%d, want %d", got, attacker)
				}
				if got := s.Econ.UnitBuckets(attacker)[economy.Metal].Production; got != 0 {
					t.Fatalf("reuse retained the old credit: %v", got)
				}
			}
		})
	}
}

// The live construction entry delivers the pulse through the combat service
// installed by composition; payment waits for the victim visit [05 R-WORK-01 §4].
func TestComposedReclaimProducerDefersRefundUntilVictimVisit(t *testing.T) {
	root := t.TempDir()
	writeCompositionModel(t, root, "fixture", 1)
	writeCompositionCOB(t, root, "testunit", []string{"modelroot", "modelchild"})
	fs := vfs.New()
	if err := fs.MountDirectory(root, 10); err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	def := &content.UnitDef{DefinitionHeader: content.DefinitionHeader{CanonicalKey: "testunit"}, UnitName: "testunit", ObjectName: "fixture", MaxDamage: 100, Limit: -1, BMCode: 1, CanReclamate: true, WorkerTime: 300, BuildDistance: 10, DamageModifier: 65536}
	cat := &content.Catalog{Units: map[string]*content.UnitDef{def.CanonicalKey: def}}
	w, err := newSlicedWorldWithCOB(cat, fs)
	if err != nil {
		t.Fatal(err)
	}
	s := &Session{Catalog: cat, World: minimalTerrain(), Mission: syntheticMission(), Units: w, Econ: &economy.Service{}}
	s.InitBattleWindForSession()
	if err := createAndBindServicesForTest(t, s); err != nil {
		t.Fatal(err)
	}
	s.RegisterAll()
	def.BuildCostMetal = 100
	builderHandle, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	victimHandle, err := w.Create(def, 1, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	builder, victim := w.Unit(builderHandle), w.Unit(victimHandle)
	victim.Health, victim.Remaining = 1, 0.25
	s.Econ.Players[0] = economy.Player{Exists: true, ControllerState: combat.ControlByteHuman}
	s.Econ.Players[1] = economy.Player{Exists: true, ControllerState: combat.ControlByteHuman}
	q := orders.QueueForUnit(builder)
	q.PurgeUnprotected()
	q.Push(orders.Lookup("ReclaimUnit"), orders.Node{Owner: builderHandle, Target: victimHandle, Phase: 5, Param1: 15, Param2: 16, Deadline: -1})
	node := q.Head()
	result := s.Build.StepUnit(construction.TickContext{Tick: 3}, builderHandle)
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if !victim.Dying || victim.Health != -14 || victim.LastDamageCause != uint8(combat.CauseReclaim) || victim.EngagementTarget != builderHandle || uint8(victim.BlinkSuppress) != 240 {
		t.Fatalf("composed pulse failed: health %d dying %v cause %d attacker %d flash %d", victim.Health, victim.Dying, victim.LastDamageCause, victim.EngagementTarget, uint8(victim.BlinkSuppress))
	}
	if node.Param2 != 2 || node.Deadline != 5 {
		t.Fatal("fatal producer omitted its cadence epilogue")
	}
	if got := s.Econ.UnitBuckets(builderHandle)[economy.Metal].Production; got != 0 {
		t.Fatalf("producer paid before victim visit: %v", got)
	}
	s.stepUnitPhase(4)
	if w.Unit(victimHandle) != nil {
		t.Fatal("victim visit did not finalize")
	}
	if got := s.Econ.UnitBuckets(builderHandle)[economy.Metal].Production; got != 75 {
		t.Fatalf("victim visit refund=%v, want 75", got)
	}
}
