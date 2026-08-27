package main

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

func TestBindBattleSessionCommandDispatchUsesCurrentSession(t *testing.T) {
	first := &session.Session{}
	second := &session.Session{}
	b := &battleSession{sess: first}
	bindBattleSessionCommandDispatch(b)

	if !b.requireCommandDispatch || b.commandDispatchFn == nil {
		t.Fatal("binder did not install strict command dispatch")
	}
	cmd := battleCommand{Kind: battleCommandOrder, Order: battleOrderCommand{
		Latch: input.LatchAttack, Handles: []pool.Handle{pool.Handle(4)},
		Target: pool.Handle(9), Position: orders.ResolvePos{
			X: numeric.Fixed(11 << 16), Y: numeric.Fixed(2 << 16), Z: numeric.Fixed(-7 << 16),
		}, Queued: true,
	}}
	if err := b.submitBattleCommand(cmd); err != nil {
		t.Fatalf("first dispatch: %v", err)
	}
	firstPending := first.PendingHumanCommands()
	if len(firstPending) != 1 || firstPending[0].Kind != session.HumanOrder || firstPending[0].Order.Code != 3 || len(firstPending[0].Order.Handles) != 1 || firstPending[0].Order.Handles[0] != pool.Handle(4) || firstPending[0].Order.Target != pool.Handle(9) || firstPending[0].Order.Position.X != numeric.Fixed(11<<16) || firstPending[0].Order.Position.Y != numeric.Fixed(2<<16) || firstPending[0].Order.Position.Z != numeric.Fixed(-7<<16) || !firstPending[0].Order.Queued {
		t.Fatalf("first session did not receive converted order: %+v", firstPending)
	}

	b.sess = second
	if err := b.submitBattleCommand(cmd); err != nil {
		t.Fatalf("replacement dispatch: %v", err)
	}
	if got := len(first.PendingHumanCommands()); got != 1 {
		t.Fatalf("replacement dispatch wrote to old session: %d pending commands", got)
	}
	secondPending := second.PendingHumanCommands()
	if len(secondPending) != 1 || secondPending[0].Kind != session.HumanOrder || secondPending[0].Order.Code != 3 || len(secondPending[0].Order.Handles) != 1 || secondPending[0].Order.Handles[0] != pool.Handle(4) || secondPending[0].Order.Target != pool.Handle(9) || secondPending[0].Order.Position.X != numeric.Fixed(11<<16) || secondPending[0].Order.Position.Y != numeric.Fixed(2<<16) || secondPending[0].Order.Position.Z != numeric.Fixed(-7<<16) || !secondPending[0].Order.Queued {
		t.Fatalf("replacement session did not receive converted order: %+v", secondPending)
	}
}
