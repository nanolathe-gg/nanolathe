package combat

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/features"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/checkpoint"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

type checkpointBindingSlot struct {
	name      string
	install   func(*Service, *checkpoint.BindingAuthority)
	reinstall func(*Service)
	clear     func(*Service)
}

var checkpointBindingSlots = []checkpointBindingSlot{
	{"ControlByte",
		func(s *Service, a *checkpoint.BindingAuthority) {
			s.SetControlByteWithCheckpointBinding(func(uint8) uint8 { panic("ControlByte called") }, a)
		},
		func(s *Service) { s.SetControlByte(s.ControlByteHook()) },
		func(s *Service) { s.SetControlByte(nil) },
	},
	{"DamageActivity",
		func(s *Service, a *checkpoint.BindingAuthority) {
			s.SetDamageActivityWithCheckpointBinding(func(*units.Unit, *units.Unit, uint32) { panic("DamageActivity called") }, a)
		},
		func(s *Service) { s.SetDamageActivity(s.DamageActivityHook()) },
		func(s *Service) { s.SetDamageActivity(nil) },
	},
	{"DangerNotice",
		func(s *Service, a *checkpoint.BindingAuthority) {
			s.SetDangerNoticeWithCheckpointBinding(func(*units.Unit, *units.Unit, uint32) { panic("DangerNotice called") }, a)
		},
		func(s *Service) { s.SetDangerNotice(s.DangerNoticeHook()) },
		func(s *Service) { s.SetDangerNotice(nil) },
	},
	{"Events",
		func(s *Service, a *checkpoint.BindingAuthority) {
			s.SetEventsWithCheckpointBinding(func(Event) { panic("Events called") }, a)
		},
		func(s *Service) { s.SetEvents(s.EventsHook()) },
		func(s *Service) { s.SetEvents(nil) },
	},
	{"HealthLost",
		func(s *Service, a *checkpoint.BindingAuthority) {
			s.SetHealthLostWithCheckpointBinding(func(*units.Unit, *units.Unit, int32) { panic("HealthLost called") }, a)
		},
		func(s *Service) { s.SetHealthLost(s.HealthLostHook()) },
		func(s *Service) { s.SetHealthLost(nil) },
	},
	{"ImpactNotice",
		func(s *Service, a *checkpoint.BindingAuthority) {
			s.SetImpactNoticeWithCheckpointBinding(func(*units.Unit, *units.Unit, numeric.Angle, uint32) { panic("ImpactNotice called") }, a)
		},
		func(s *Service) { s.SetImpactNotice(s.ImpactNoticeHook()) },
		func(s *Service) { s.SetImpactNotice(nil) },
	},
	{"InfectionThreat",
		func(s *Service, a *checkpoint.BindingAuthority) {
			s.SetInfectionThreatWithCheckpointBinding(func(*units.Unit) bool { panic("InfectionThreat called") }, a)
		},
		func(s *Service) { s.SetInfectionThreat(s.InfectionThreatHook()) },
		func(s *Service) { s.SetInfectionThreat(nil) },
	},
	{"IsOffMapFiled",
		func(s *Service, a *checkpoint.BindingAuthority) {
			s.SetIsOffMapFiledWithCheckpointBinding(func(pool.Handle) bool { panic("IsOffMapFiled called") }, a)
		},
		func(s *Service) { s.SetIsOffMapFiled(s.IsOffMapFiledHook()) },
		func(s *Service) { s.SetIsOffMapFiled(nil) },
	},
	{"Visibility",
		func(s *Service, a *checkpoint.BindingAuthority) {
			s.SetVisibilityWithCheckpointBinding(func(visibility.PlayerID, visibility.Target) bool { panic("Visibility called") }, a)
		},
		func(s *Service) { s.SetVisibility(s.VisibilityHook()) },
		func(s *Service) { s.SetVisibility(nil) },
	},
	{"VisitOffMapFiled",
		func(s *Service, a *checkpoint.BindingAuthority) {
			s.SetVisitOffMapFiledWithCheckpointBinding(func(func(pool.Handle, uint64) bool) { panic("VisitOffMapFiled called") }, a)
		},
		func(s *Service) { s.SetVisitOffMapFiled(s.VisitOffMapFiledHook()) },
		func(s *Service) { s.SetVisitOffMapFiled(nil) },
	},
	{"Reaction.Allied",
		func(s *Service, a *checkpoint.BindingAuthority) {
			s.Reaction.SetAlliedWithCheckpointBinding(func(uint8, uint8) bool { panic("Allied called") }, a)
		},
		func(s *Service) { s.Reaction.SetAllied(s.Reaction.AlliedHook()) },
		func(s *Service) { s.Reaction.SetAllied(nil) },
	},
	{"Reaction.ArmConstructionThrottle",
		func(s *Service, a *checkpoint.BindingAuthority) {
			s.Reaction.SetArmConstructionThrottleWithCheckpointBinding(func(uint8, uint32) { panic("ArmConstructionThrottle called") }, a)
		},
		func(s *Service) { s.Reaction.SetArmConstructionThrottle(s.Reaction.ArmConstructionThrottleHook()) },
		func(s *Service) { s.Reaction.SetArmConstructionThrottle(nil) },
	},
	{"Reaction.ObserverNotice",
		func(s *Service, a *checkpoint.BindingAuthority) {
			s.Reaction.SetObserverNoticeWithCheckpointBinding(func(*units.Unit) { panic("ObserverNotice called") }, a)
		},
		func(s *Service) { s.Reaction.SetObserverNotice(s.Reaction.ObserverNoticeHook()) },
		func(s *Service) { s.Reaction.SetObserverNotice(nil) },
	},
	{"Reaction.PurgeOrdersOnDamage",
		func(s *Service, a *checkpoint.BindingAuthority) {
			s.Reaction.SetPurgeOrdersOnDamageWithCheckpointBinding(func(*units.Unit) { panic("PurgeOrdersOnDamage called") }, a)
		},
		func(s *Service) { s.Reaction.SetPurgeOrdersOnDamage(s.Reaction.PurgeOrdersOnDamageHook()) },
		func(s *Service) { s.Reaction.SetPurgeOrdersOnDamage(nil) },
	},
	{"Reaction.RetaliationOrder",
		func(s *Service, a *checkpoint.BindingAuthority) {
			s.Reaction.SetRetaliationOrderWithCheckpointBinding(func(*units.Unit, *units.Unit) bool { panic("RetaliationOrder called") }, a)
		},
		func(s *Service) { s.Reaction.SetRetaliationOrder(s.Reaction.RetaliationOrderHook()) },
		func(s *Service) { s.Reaction.SetRetaliationOrder(nil) },
	},
	{"Reaction.SlotAcquisitionAdmits",
		func(s *Service, a *checkpoint.BindingAuthority) {
			s.Reaction.SetSlotAcquisitionAdmitsWithCheckpointBinding(func(*units.Unit, int, *units.Unit) bool { panic("SlotAcquisitionAdmits called") }, a)
		},
		func(s *Service) { s.Reaction.SetSlotAcquisitionAdmits(s.Reaction.SlotAcquisitionAdmitsHook()) },
		func(s *Service) { s.Reaction.SetSlotAcquisitionAdmits(nil) },
	},
	{"Reaction.UnderAttackNotice",
		func(s *Service, a *checkpoint.BindingAuthority) {
			s.Reaction.SetUnderAttackNoticeWithCheckpointBinding(func(*units.Unit) { panic("UnderAttackNotice called") }, a)
		},
		func(s *Service) { s.Reaction.SetUnderAttackNotice(s.Reaction.UnderAttackNoticeHook()) },
		func(s *Service) { s.Reaction.SetUnderAttackNotice(nil) },
	},
	{"Reaction.UnderAttackSilenced",
		func(s *Service, a *checkpoint.BindingAuthority) {
			s.Reaction.SetUnderAttackSilencedWithCheckpointBinding(func(*units.Unit) bool { panic("UnderAttackSilenced called") }, a)
		},
		func(s *Service) { s.Reaction.SetUnderAttackSilenced(s.Reaction.UnderAttackSilencedHook()) },
		func(s *Service) { s.Reaction.SetUnderAttackSilenced(nil) },
	},
}

type checkpointBoundCombat struct {
	s         *Service
	c         *CheckpointContext
	inputs    *content.SimulationInputs
	authority *checkpoint.BindingAuthority
}

func newCheckpointBoundCombat(t *testing.T, c *CheckpointContext) checkpointBoundCombat {
	t.Helper()
	f := checkpointBoundCombat{
		s: NewServiceWithProjectileCapacity(1),
		c: NewCheckpointContext(c.Units, c.World),
		// Contents are deliberately not admitted here: validating frozen
		// semantics belongs to root, once per capture (§16.3.70).
		inputs: &content.SimulationInputs{}, authority: checkpoint.NewBindingAuthority(),
	}
	f.s.Features, f.s.ProjectileWind, f.s.Reaction = &features.Service{}, &world.Wind{}, &ReactionSeams{}
	for _, slot := range checkpointBindingSlots {
		slot.install(f.s, f.authority)
	}
	if err := f.register(); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f checkpointBoundCombat) register() error {
	return f.c.SetBindings(f.s, f.inputs, f.s.Features, f.s.ProjectileWind, f.s.Reaction, f.authority)
}

func checkpointCombatRefused(t *testing.T, s *Service, c *CheckpointContext, path string) {
	t.Helper()
	if _, err := s.CollectCheckpointReferences(c); err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("collect error = %v, want path %s", err, path)
	}
	var out bytes.Buffer
	err := s.WriteCheckpoint(checkpoint.NewEncoder(&out), c)
	if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "nanolathe: combat checkpoint failed: logical path ") || out.Len() != 0 {
		t.Fatalf("write error = %v, bytes = %d, want path %s", err, out.Len(), path)
	}
}

func TestCheckpointCombatBindingSlots(t *testing.T) {
	c := combatCheckpointContext(t)
	for index, slot := range checkpointBindingSlots {
		t.Run(slot.name, func(t *testing.T) {
			f := newCheckpointBoundCombat(t, c)
			before := combatCheckpointBytes(t, f.s, f.c)
			serviceProofs, reactionProofs := f.s.checkpointCallbacks, f.s.Reaction.checkpointCallbacks
			// Extracting the same function and installing it ordinarily loses
			// only that slot's proof, even though its behavior is identical.
			slot.reinstall(f.s)
			if index < 10 {
				serviceProofs[index] = checkpointServiceCallbackProof{}
			} else {
				reactionProofs[index-10] = checkpointReactionCallbackProof{}
			}
			if f.s.checkpointCallbacks != serviceProofs || f.s.Reaction.checkpointCallbacks != reactionProofs {
				t.Fatal("replacement invalidated another slot")
			}
			saved := *f.c
			if err := f.register(); err == nil || *f.c != saved {
				t.Fatalf("repeat admitted ordinary replacement or changed context: %v", err)
			}
			checkpointCombatRefused(t, f.s, f.c, slot.name)
			for _, authority := range []*checkpoint.BindingAuthority{nil, checkpoint.NewBindingAuthority()} {
				slot.install(f.s, authority)
				if err := f.register(); err == nil || *f.c != saved {
					t.Fatal("unadmitted authority registered")
				}
				checkpointCombatRefused(t, f.s, f.c, slot.name)
			}
			slot.install(f.s, f.authority)
			if err := f.register(); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, combatCheckpointBytes(t, f.s, f.c)) {
				t.Fatal("proof reinstatement changed bytes")
			}
			slot.clear(f.s)
			if f.s.checkpointCallbacks != serviceProofs || f.s.Reaction.checkpointCallbacks != reactionProofs {
				t.Fatal("nil callback retained proof or cleared another slot")
			}
			if err := f.register(); err != nil {
				t.Fatal(err)
			}
			if bytes.Equal(before, combatCheckpointBytes(t, f.s, f.c)) {
				t.Fatal("nil callback lost from presence payload")
			}
		})
	}
}

// This full vector is independently authored from §16.3.16/.70. One zero
// projectile occupies 180 bytes; Community is 32 bools plus 14 i64s. The
// optional reaction adds exactly eight lexical callback bytes, never a table.
func TestCheckpointCombatBindingPresenceVector(t *testing.T) {
	f := newCheckpointBoundCombat(t, combatCheckpointContext(t))
	want := checkpointHex(t,
		strings.Repeat("00", 144)+
			"010101010101010101000101"+"0101010101010101"+
			"01000000"+strings.Repeat("00", 180)+"00"+
			"010000000000000000000000000000000100000000"+
			"000000000000000000"+"0101"+
			strings.Repeat("00", 41)+"0000"+ // area state/death count and two option gates
			strings.Repeat("00", 12)+ // incoming count and both Modern ticks
			strings.Repeat("00", 80+1+10+40+80)) // cursors, ShootAll gate, registry gates, rebuild ticks and empty target rows
	before := combatCheckpointBytes(t, f.s, f.c)
	if !bytes.Equal(before, want) {
		t.Fatalf("got (%d) %x; want (%d) %x", len(before), before, len(want), want)
	}
	// Independent byte positions also distinguish UnderAttackNotice from
	// UnderAttackSilenced and each service slot from its adjacent owner tag.
	offsets := []int{144, 145, 146, 147, 149, 150, 151, 152, 379, 380, 156, 157, 158, 159, 160, 161, 162, 163}
	for i, slot := range checkpointBindingSlots {
		slot.clear(f.s)
		got := combatCheckpointBytes(t, f.s, f.c)
		expected := bytes.Clone(want)
		expected[offsets[i]] = 0
		if !bytes.Equal(got, expected) {
			t.Fatalf("%s wire position changed", slot.name)
		}
		slot.install(f.s, f.authority)
	}
	// A present empty reaction differs from absence by its presence and
	// eight zero booleans; partial records retain all eight positions.
	for _, slot := range checkpointBindingSlots[10:] {
		slot.clear(f.s)
	}
	empty := combatCheckpointBytes(t, f.s, f.c)
	wantEmpty := bytes.Clone(want)
	clear(wantEmpty[156:164])
	if !bytes.Equal(empty, wantEmpty) {
		t.Fatal("empty reaction framing")
	}
	f.s.Reaction = nil
	fresh := NewCheckpointContext(f.c.Units, f.c.World)
	if err := fresh.SetBindings(f.s, f.inputs, f.s.Features, f.s.ProjectileWind, nil, f.authority); err != nil {
		t.Fatal(err)
	}
	absent := combatCheckpointBytes(t, f.s, fresh)
	wantAbsent := append(bytes.Clone(wantEmpty[:156]), wantEmpty[164:]...)
	wantAbsent[155] = 0
	if !bytes.Equal(absent, wantAbsent) {
		t.Fatal("absent reaction framing")
	}
}

func TestCheckpointCombatBindingAliasesAndAtomicity(t *testing.T) {
	c := combatCheckpointContext(t)
	for _, tc := range []struct {
		name   string
		mutate func(*Service)
	}{
		{"Features", func(s *Service) { s.Features = &features.Service{} }},
		{"Features nil", func(s *Service) { s.Features = nil }},
		{"ProjectileWind", func(s *Service) { s.ProjectileWind = &world.Wind{} }},
		{"ProjectileWind nil", func(s *Service) { s.ProjectileWind = nil }},
		{"Reaction", func(s *Service) { s.Reaction = &ReactionSeams{} }},
		{"Reaction nil", func(s *Service) { s.Reaction = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCheckpointBoundCombat(t, c)
			b := f.c.binding
			tc.mutate(f.s)
			before := *f.c
			if err := f.c.SetBindings(f.s, f.inputs, b.features, b.wind, b.reaction, f.authority); err == nil || *f.c != before {
				t.Fatal("repeat accepted replaced alias")
			}
			checkpointCombatRefused(t, f.s, f.c, strings.Fields(tc.name)[0])
			fresh := NewCheckpointContext(c.Units, c.World)
			if err := fresh.SetBindings(f.s, f.inputs, b.features, b.wind, b.reaction, f.authority); err == nil || fresh.binding.service != nil {
				t.Fatal("first registration accepted foreign alias")
			}
		})
	}
	f := newCheckpointBoundCombat(t, c)
	for _, alter := range []func(*checkpointBindings){
		func(b *checkpointBindings) { b.service = NewServiceWithProjectileCapacity(1) },
		func(b *checkpointBindings) { b.inputs = &content.SimulationInputs{} },
		func(b *checkpointBindings) { b.features = &features.Service{} },
		func(b *checkpointBindings) { b.wind = &world.Wind{} },
		func(b *checkpointBindings) { b.reaction = &ReactionSeams{} },
		func(b *checkpointBindings) { b.authority = checkpoint.NewBindingAuthority() },
		func(b *checkpointBindings) { b.service = nil },
		func(b *checkpointBindings) { b.inputs = nil },
		func(b *checkpointBindings) { b.authority = nil },
	} {
		b := f.c.binding
		alter(&b)
		before := *f.c
		if err := f.c.SetBindings(b.service, b.inputs, b.features, b.wind, b.reaction, b.authority); err == nil || *f.c != before {
			t.Fatal("conflict accepted or changed context")
		}
	}
	if err := (*CheckpointContext)(nil).SetBindings(f.s, f.inputs, f.s.Features, f.s.ProjectileWind, f.s.Reaction, f.authority); err == nil {
		t.Fatal("nil context accepted")
	}
	// Every present slot refuses without registration, including admitted
	// callbacks whose proof must not stand in for a capture-local tuple.
	checkpointCombatRefused(t, f.s, NewCheckpointContext(c.Units, c.World), "ControlByte")
	// Optional nil owners are valid when explicitly expected.
	empty := NewServiceWithProjectileCapacity(1)
	fresh := NewCheckpointContext(c.Units, c.World)
	before := combatCheckpointBytes(t, empty, fresh)
	if err := fresh.SetBindings(empty, f.inputs, nil, nil, nil, f.authority); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, combatCheckpointBytes(t, empty, fresh)) {
		t.Fatal("absent bytes changed")
	}
}

func TestCheckpointCombatBindingCopies(t *testing.T) {
	f := newCheckpointBoundCombat(t, combatCheckpointContext(t))
	copyService := *f.s
	checkpointCombatRefused(t, &copyService, f.c, "combat.bindings")
	fresh := NewCheckpointContext(f.c.Units, f.c.World)
	if err := fresh.SetBindings(&copyService, f.inputs, copyService.Features, copyService.ProjectileWind, copyService.Reaction, f.authority); err == nil || fresh.binding.service != nil {
		t.Fatal("copied service inherited slot proof")
	}
	copyReaction := *f.s.Reaction
	f.s.Reaction = &copyReaction
	checkpointCombatRefused(t, f.s, f.c, "Reaction")
	if err := fresh.SetBindings(f.s, f.inputs, f.s.Features, f.s.ProjectileWind, &copyReaction, f.authority); err == nil || fresh.binding.service != nil {
		t.Fatal("copied reaction inherited slot proof")
	}
}

func TestCheckpointCombatBindingCapturePurity(t *testing.T) {
	f := newCheckpointBoundCombat(t, combatCheckpointContext(t))
	// A private derived lookup is never invoked or refreshed during capture.
	f.s.weaponByID = func(int32) (*content.WeaponDef, bool) { panic("lookup called") }
	f.s.weaponByIDCatalog = &content.Catalog{}
	beforeService, beforeReaction, beforeContext := *f.s, *f.s.Reaction, *f.c
	before := combatCheckpointBytes(t, f.s, f.c)
	for i := 0; i < 3; i++ {
		if err := f.register(); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before, combatCheckpointBytes(t, f.s, f.c)) {
			t.Fatal("unstable capture")
		}
	}
	if *f.c != beforeContext || f.s.checkpointCallbacks != beforeService.checkpointCallbacks || f.s.Reaction.checkpointCallbacks != beforeReaction.checkpointCallbacks || f.s.weaponByIDCatalog != beforeService.weaponByIDCatalog || f.s.weaponByID == nil {
		t.Fatal("capture changed bindings or cache")
	}
	if n := testing.AllocsPerRun(20, func() {
		if err := f.register(); err != nil {
			panic(err)
		}
		if _, err := f.s.CollectCheckpointReferences(f.c); err != nil {
			panic(err)
		}
	}); n != 0 {
		t.Fatalf("registration/collection allocations = %v", n)
	}
	var beforeSummary, afterSummary checkpoint.Summary
	if err := f.s.AppendCheckpointSummary(&beforeSummary); err != nil {
		t.Fatal(err)
	}
	for _, slot := range checkpointBindingSlots {
		slot.reinstall(f.s)
	}
	if err := f.s.AppendCheckpointSummary(&afterSummary); err != nil {
		t.Fatal(err)
	}
	if beforeSummary != afterSummary {
		t.Fatal("cheap summary inspected binding proof")
	}
	// Registered callbacks cannot bypass any of the existing active contexts.
	for _, edit := range []func(*Service){
		func(s *Service) { s.impactStack = []pool.Handle{1} },
		func(s *Service) { s.TransportDeaths.pending = &units.Unit{} },
		func(s *Service) { s.communityAreaCurrentGen = 1 },
	} {
		g := newCheckpointBoundCombat(t, f.c)
		edit(g.s)
		checkpointCombatRefused(t, g.s, g.c, "combat.Service.")
	}
}

func TestCheckpointCombatBindingNilFunctionsAndConstructors(t *testing.T) {
	c := combatCheckpointContext(t)
	clearWith := []func(*Service, *checkpoint.BindingAuthority){
		func(s *Service, a *checkpoint.BindingAuthority) { s.SetControlByteWithCheckpointBinding(nil, a) },
		func(s *Service, a *checkpoint.BindingAuthority) { s.SetDamageActivityWithCheckpointBinding(nil, a) },
		func(s *Service, a *checkpoint.BindingAuthority) { s.SetDangerNoticeWithCheckpointBinding(nil, a) },
		func(s *Service, a *checkpoint.BindingAuthority) { s.SetEventsWithCheckpointBinding(nil, a) },
		func(s *Service, a *checkpoint.BindingAuthority) { s.SetHealthLostWithCheckpointBinding(nil, a) },
		func(s *Service, a *checkpoint.BindingAuthority) { s.SetImpactNoticeWithCheckpointBinding(nil, a) },
		func(s *Service, a *checkpoint.BindingAuthority) { s.SetInfectionThreatWithCheckpointBinding(nil, a) },
		func(s *Service, a *checkpoint.BindingAuthority) { s.SetIsOffMapFiledWithCheckpointBinding(nil, a) },
		func(s *Service, a *checkpoint.BindingAuthority) { s.SetVisibilityWithCheckpointBinding(nil, a) },
		func(s *Service, a *checkpoint.BindingAuthority) { s.SetVisitOffMapFiledWithCheckpointBinding(nil, a) },
		func(s *Service, a *checkpoint.BindingAuthority) { s.Reaction.SetAlliedWithCheckpointBinding(nil, a) },
		func(s *Service, a *checkpoint.BindingAuthority) {
			s.Reaction.SetArmConstructionThrottleWithCheckpointBinding(nil, a)
		},
		func(s *Service, a *checkpoint.BindingAuthority) {
			s.Reaction.SetObserverNoticeWithCheckpointBinding(nil, a)
		},
		func(s *Service, a *checkpoint.BindingAuthority) {
			s.Reaction.SetPurgeOrdersOnDamageWithCheckpointBinding(nil, a)
		},
		func(s *Service, a *checkpoint.BindingAuthority) {
			s.Reaction.SetRetaliationOrderWithCheckpointBinding(nil, a)
		},
		func(s *Service, a *checkpoint.BindingAuthority) {
			s.Reaction.SetSlotAcquisitionAdmitsWithCheckpointBinding(nil, a)
		},
		func(s *Service, a *checkpoint.BindingAuthority) {
			s.Reaction.SetUnderAttackNoticeWithCheckpointBinding(nil, a)
		},
		func(s *Service, a *checkpoint.BindingAuthority) {
			s.Reaction.SetUnderAttackSilencedWithCheckpointBinding(nil, a)
		},
	}
	for index, clearSlot := range clearWith {
		f := newCheckpointBoundCombat(t, c)
		clearSlot(f.s, f.authority)
		if index < 10 {
			if f.s.checkpointCallbacks[index] != (checkpointServiceCallbackProof{}) {
				t.Fatal("nil Service slot has proof")
			}
		} else if f.s.Reaction.checkpointCallbacks[index-10] != (checkpointReactionCallbackProof{}) {
			t.Fatal("nil Reaction slot has proof")
		}
		if err := f.register(); err != nil {
			t.Fatal(err)
		}
	}
	f := newCheckpointBoundCombat(t, c)
	r := f.s.Reaction
	config := ReactionSeamsConfig{
		Allied: r.AlliedHook(), ArmConstructionThrottle: r.ArmConstructionThrottleHook(), ObserverNotice: r.ObserverNoticeHook(),
		PurgeOrdersOnDamage: r.PurgeOrdersOnDamageHook(), RetaliationOrder: r.RetaliationOrderHook(), SlotAcquisitionAdmits: r.SlotAcquisitionAdmitsHook(),
		UnderAttackNotice: r.UnderAttackNoticeHook(), UnderAttackSilenced: r.UnderAttackSilencedHook(),
	}
	for _, ordinary := range []*ReactionSeams{NewReactionSeams(config), NewReactionSeamsWithCheckpointBinding(config, nil)} {
		if ordinary.checkpointCallbacks != ([8]checkpointReactionCallbackProof{}) {
			t.Fatal("ordinary constructor invented proof")
		}
		if err := ordinary.validateCheckpointBindings(f.authority); err == nil {
			t.Fatal("ordinary constructor admitted")
		}
	}
	admitted := NewReactionSeamsWithCheckpointBinding(config, f.authority)
	if err := admitted.validateCheckpointBindings(f.authority); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	admitted.writeCheckpoint(checkpoint.NewEncoder(&out))
	if !bytes.Equal(out.Bytes(), []byte{1, 1, 1, 1, 1, 1, 1, 1, 1}) {
		t.Fatal("admitted constructor lost a callback")
	}
	if got := NewReactionSeamsWithCheckpointBinding(ReactionSeamsConfig{}, f.authority); got.checkpointCallbacks != ([8]checkpointReactionCallbackProof{}) {
		t.Fatal("empty constructor invented proof")
	}
	// Ordinary Service configuration still copies callbacks and storage without
	// initializing its projectile arena or borrowing any extracted proof.
	s := NewService(ServiceConfig{
		ControlByte: f.s.ControlByteHook(), DamageActivity: f.s.DamageActivityHook(), DangerNotice: f.s.DangerNoticeHook(), Events: f.s.EventsHook(),
		HealthLost: f.s.HealthLostHook(), ImpactNotice: f.s.ImpactNoticeHook(), InfectionThreat: f.s.InfectionThreatHook(), IsOffMapFiled: f.s.IsOffMapFiledHook(),
		Visibility: f.s.VisibilityHook(), VisitOffMapFiled: f.s.VisitOffMapFiledHook(), Reaction: admitted,
	})
	if s.Records != nil || s.incoming != nil || s.Slots.Count() != 0 || s.checkpointCallbacks != ([10]checkpointServiceCallbackProof{}) {
		t.Fatal("ordinary Service constructor changed storage or acquired proof")
	}
	fresh := NewCheckpointContext(c.Units, c.World)
	if err := fresh.SetBindings(s, f.inputs, nil, nil, admitted, f.authority); err == nil || fresh.binding.service != nil {
		t.Fatal("ordinary constructor callbacks admitted")
	}
}

func TestCheckpointCombatBindingPreservesReactionOrderAndDraws(t *testing.T) {
	c := combatCheckpointContext(t)
	type outcome struct {
		calls    []string
		state    uint32
		draws    uint64
		deadline uint32
	}
	run := func(mode int) outcome {
		var result outcome
		stream := rng.SimulationFromState(7)
		victim := &units.Unit{Owner: 1, Def: &content.UnitDef{CanCapture: true}, Flags: units.ArmedStatus, LastDamageSide: 10}
		attacker := &units.Unit{Owner: 2, Def: &content.UnitDef{}}
		push := func(name string) { result.calls = append(result.calls, name) }
		config := ReactionSeamsConfig{
			ObserverNotice: func(v *units.Unit) {
				if v != victim {
					t.Fatal("observer operand")
				}
				push("observer")
			},
			ArmConstructionThrottle: func(owner uint8, tick uint32) {
				if owner != 1 || tick != 17 {
					t.Fatal("throttle operands")
				}
				push("throttle")
				result.deadline = tick + 30 + stream.Uint32n(300)
			},
			PurgeOrdersOnDamage: func(v *units.Unit) {
				if v != victim {
					t.Fatal("purge operand")
				}
				push("purge")
			},
			Allied: func(a, b uint8) bool {
				if a != 1 || b != 2 {
					t.Fatal("alliance operands")
				}
				push("allied")
				return false
			},
			SlotAcquisitionAdmits: func(v *units.Unit, slot int, a *units.Unit) bool {
				if v != victim || slot != 0 || a != attacker {
					t.Fatal("acquisition operands")
				}
				push("acquire")
				return true
			},
			RetaliationOrder: func(v, a *units.Unit) bool {
				if v != victim || a != attacker {
					t.Fatal("retaliation operands")
				}
				push("retaliate")
				return true
			},
			UnderAttackSilenced: func(v *units.Unit) bool {
				if v != victim {
					t.Fatal("silenced operand")
				}
				push("silenced")
				return false
			},
			UnderAttackNotice: func(v *units.Unit) {
				if v != victim {
					t.Fatal("notice operand")
				}
				push("notice")
			},
		}
		control := func(owner uint8) uint8 {
			if owner != 1 {
				t.Fatal("control operand")
			}
			push("control")
			return ControlByteComputer
		}
		s := NewServiceWithProjectileCapacity(1)
		authority := checkpoint.NewBindingAuthority()
		if mode == 0 {
			s.Reaction = NewReactionSeams(config)
			s.SetControlByte(control)
		} else {
			s.Reaction = NewReactionSeamsWithCheckpointBinding(config, authority)
			s.SetControlByteWithCheckpointBinding(control, authority)
			if mode == 2 {
				authority = checkpoint.NewBindingAuthority()
			}
			fresh := NewCheckpointContext(c.Units, c.World)
			err := fresh.SetBindings(s, &content.SimulationInputs{}, nil, nil, s.Reaction, authority)
			if (err != nil) != (mode == 2) {
				t.Fatalf("mode %d binding = %v", mode, err)
			}
		}
		if len(result.calls) != 0 || stream.Draws() != 0 {
			t.Fatal("installation or capture invoked a callback")
		}
		s.ReactToDamage(nil, victim, attacker, 17)
		result.state, result.draws = stream.State, stream.Draws()
		return result
	}
	ordinary := run(0)
	// Existing reaction order and the throttle's sole draw [06 R-WPN-04 §2].
	want := []string{"observer", "control", "throttle", "purge", "control", "allied", "acquire", "retaliate", "silenced", "notice"}
	if !reflect.DeepEqual(ordinary.calls, want) || ordinary.draws != 1 {
		t.Fatalf("reaction = %+v", ordinary)
	}
	for _, mode := range []int{1, 2} {
		if got := run(mode); !reflect.DeepEqual(got, ordinary) {
			t.Fatalf("binding mode %d changed reaction: %+v", mode, got)
		}
	}
}
