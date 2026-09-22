package combat

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

func transportedDeathUnit(handle pool.Handle, typeID uint32, health int32) *units.Unit {
	return &units.Unit{
		Handle: handle,
		Def: &content.UnitDef{
			UnitDefID: typeID,
		},
		Health: health,
		Alive:  true,
	}
}

func TestTransportPassengerCapturePersistsAndPrunesByIdentityOrSurvival(t *testing.T) {
	var state TransportDeathState
	u := transportedDeathUnit(7, 41, 100)
	state.CapturePassenger(u)
	state.Tick(10)
	state.Tick(11)
	if len(state.passengers) != 1 {
		t.Fatalf("untouched capture count = %d, want 1 across ticks", len(state.passengers))
	}

	// Health is solely the survived-damage pruning signal. Pending death keeps
	// a changed-health capture, and selection ignores the health snapshot.
	u.Health = 20
	u.Dying = true
	state.Tick(12)
	if !state.WasTransportedDeath(u) {
		t.Fatal("pending-death passenger with changed health lost its capture")
	}

	state.CapturePassenger(u)
	u.Dying = false
	u.Health = 19
	state.Tick(13)
	if len(state.passengers) != 0 {
		t.Fatalf("surviving changed-health capture count = %d, want 0", len(state.passengers))
	}

	state.CapturePassenger(u)
	u.Handle++
	state.Tick(14)
	if len(state.passengers) != 0 {
		t.Fatal("slot identity change did not prune capture")
	}

	u.Handle--
	state.CapturePassenger(u)
	u.Def = &content.UnitDef{UnitDefID: 42}
	state.Tick(15)
	if len(state.passengers) != 0 {
		t.Fatal("type identity change did not prune capture")
	}
}

func TestTransportPassengerIdentityIsPointerSlotAndType(t *testing.T) {
	var state TransportDeathState
	u := transportedDeathUnit(9, 55, 100)
	state.CapturePassenger(u)

	// Pool reuse with the same record pointer, slot and type reproduces the
	// source identity. Definition pointer replacement and unrelated instance
	// fields must not invent a generation rejection.
	u.Def = &content.UnitDef{UnitDefID: 55}
	u.Alive = false
	u.Health = -5
	if !state.WasTransportedDeath(u) {
		t.Fatal("same pointer/slot/type was rejected as a captured passenger")
	}

	state.CapturePassenger(u)
	u.Handle = 10
	if state.WasTransportedDeath(u) {
		t.Fatal("changed slot consumed as a matching passenger")
	}
	if len(state.passengers) != 0 {
		t.Fatal("same-pointer identity mismatch was not consumed")
	}
}

func TestTransportDeathTickRewindClearsBothCaptureForms(t *testing.T) {
	var state TransportDeathState
	passenger := transportedDeathUnit(3, 4, 100)
	pending := transportedDeathUnit(5, 6, 100)
	pending.Attachment.Carrier = 2
	state.Tick(20)
	state.CapturePassenger(passenger)
	state.CapturePreDeath(pending)
	state.Tick(19)
	if len(state.passengers) != 0 || state.pending != nil {
		t.Fatalf("rewind left passengers=%d pending=%p", len(state.passengers), state.pending)
	}
	passenger.Attachment.Carrier = 0
	pending.Attachment.Carrier = 0
	if state.WasTransportedDeath(passenger) || state.WasTransportedDeath(pending) {
		t.Fatal("rewound capture qualified a detached death")
	}
}

func TestCommunityDeathWeaponQualifiersAndIndependentOverrides(t *testing.T) {
	stockKill := &content.WeaponDef{ID: 1}
	stockSelf := &content.WeaponDef{ID: 2}
	transportKill := &content.WeaponDef{ID: 3}
	transportSelf := &content.WeaponDef{ID: 4}
	def := &content.UnitDef{
		UnitDefID:                    17,
		ExplodeAsDef:                 stockKill,
		SelfDestructAsDef:            stockSelf,
		TransportedExplodeAsDef:      transportKill,
		TransportedSelfDestructAsDef: transportSelf,
	}
	newUnit := func(handle pool.Handle) *units.Unit {
		return &units.Unit{Handle: handle, Def: def, Health: 100, Alive: true}
	}
	rules := CommunityRules{}

	t.Run("currently carried consumes passenger capture", func(t *testing.T) {
		var state TransportDeathState
		u := newUnit(1)
		u.Attachment.Carrier = 9
		state.CapturePassenger(u)
		if got := rules.DeathWeapon(DeathWeaponRequest{State: &state, Unit: u, Cause: CauseOrdinary, Enabled: true}); got != transportKill {
			t.Fatalf("carried kill weapon = %p, want %p", got, transportKill)
		}
		u.Attachment.Carrier = 0
		if got := rules.DeathWeapon(DeathWeaponRequest{State: &state, Unit: u, Cause: CauseOrdinary, Enabled: true}); got != stockKill {
			t.Fatalf("consumed passenger weapon = %p, want stock %p", got, stockKill)
		}
	})

	t.Run("pre-death capture survives detach", func(t *testing.T) {
		var state TransportDeathState
		u := newUnit(2)
		u.Attachment.Carrier = 9
		state.CapturePreDeath(u)
		u.Attachment.Carrier = 0
		if got := rules.DeathWeapon(DeathWeaponRequest{State: &state, Unit: u, Cause: CauseSelfDestruct, Enabled: true}); got != transportSelf {
			t.Fatalf("pre-death self-destruct weapon = %p, want %p", got, transportSelf)
		}
	})

	t.Run("passenger capture survives detach", func(t *testing.T) {
		var state TransportDeathState
		u := newUnit(3)
		state.CapturePassenger(u)
		if got := rules.DeathWeapon(DeathWeaponRequest{State: &state, Unit: u, Cause: CauseOrdinary, Enabled: true}); got != transportKill {
			t.Fatalf("captured passenger weapon = %p, want %p", got, transportKill)
		}
	})

	t.Run("override fallback is cause-specific", func(t *testing.T) {
		var state TransportDeathState
		u := newUnit(4)
		u.Def = &content.UnitDef{UnitDefID: 18, ExplodeAsDef: stockKill, SelfDestructAsDef: stockSelf, TransportedExplodeAsDef: transportKill}
		u.Attachment.Carrier = 9
		if got := rules.DeathWeapon(DeathWeaponRequest{State: &state, Unit: u, Cause: CauseSelfDestruct, Enabled: true}); got != stockSelf {
			t.Fatalf("missing transported self-destruct weapon = %p, want stock %p", got, stockSelf)
		}
		u.Attachment.Carrier = 9
		if got := rules.DeathWeapon(DeathWeaponRequest{State: &state, Unit: u, Cause: CauseOrdinary, Enabled: true}); got != transportKill {
			t.Fatalf("independent transported kill weapon = %p, want %p", got, transportKill)
		}
	})
}

func TestTransportDeathStrictAndDisabledBypass(t *testing.T) {
	stock := &content.WeaponDef{ID: 1}
	override := &content.WeaponDef{ID: 2}
	u := transportedDeathUnit(1, 2, 100)
	u.Def.ExplodeAsDef = stock
	u.Def.TransportedExplodeAsDef = override
	u.Attachment.Carrier = 0
	var state TransportDeathState
	state.CapturePassenger(u)

	q := DeathWeaponRequest{State: &state, Unit: u, Cause: CauseOrdinary, Enabled: true}
	if got := (StrictRules{}).DeathWeapon(q); got != stock {
		t.Fatalf("Strict weapon = %p, want stock %p", got, stock)
	}
	q.Enabled = false
	if got := (CommunityRules{}).DeathWeapon(q); got != stock {
		t.Fatalf("disabled Community weapon = %p, want stock %p", got, stock)
	}
	// Neither bypass enters the Community selector, so the captured identity is
	// still available after a command-boundary enable.
	q.Enabled = true
	if got := (CommunityRules{}).DeathWeapon(q); got != override {
		t.Fatalf("enabled Community weapon after bypass = %p, want %p", got, override)
	}

	if (StrictRules{}).TransportDeathEnabled(true) {
		t.Fatal("Strict admitted transported-death lifecycle")
	}
	if !(CommunityRules{}).TransportDeathEnabled(true) || (CommunityRules{}).TransportDeathEnabled(false) {
		t.Fatal("Community lifecycle admission did not preserve resolved feature answer")
	}
}

func TestTransportDeathPendingPointerIsSingleAndClearedByAnySelection(t *testing.T) {
	var state TransportDeathState
	outer := transportedDeathUnit(1, 1, 100)
	inner := transportedDeathUnit(2, 2, 100)
	outer.Attachment.Carrier = 8
	inner.Attachment.Carrier = 8

	state.CapturePreDeath(outer)
	state.CapturePreDeath(inner)
	outer.Attachment.Carrier = 0
	inner.Attachment.Carrier = 0
	if state.WasTransportedDeath(outer) {
		t.Fatal("nested capture did not replace the outer pending pointer")
	}
	if state.WasTransportedDeath(inner) {
		t.Fatal("a selection for another unit did not clear the global pending pointer")
	}

	inner.Attachment.Carrier = 8
	state.CapturePreDeath(inner)
	inner.Attachment.Carrier = 0
	state.ClearPostDecision()
	if state.WasTransportedDeath(inner) {
		t.Fatal("post-decision hook did not clear pending pointer")
	}
}
