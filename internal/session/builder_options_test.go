package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
)

func TestBuilderOptionsBelongToThePlayerAndChangeAtTheBoundary(t *testing.T) {
	s := &Session{LocalOwner: 2, Clock: &clock.State{GlobalTick: 10}, Econ: &economy.Service{}, Build: &construction.Service{OrderBinding: &orders.QueueBinding{}}}
	s.Econ.Players[2] = economy.Player{Exists: true, ControllerState: 1}
	s.Econ.Players[3] = economy.Player{Exists: true, ControllerState: 2}
	preferred := orders.DefaultBuilderOptions()
	preferred.Guard[0] = orders.GuardScatter
	if err := s.initializeBuilderOptions(&preferred); err != nil {
		t.Fatal(err)
	}
	b := s.Build.OrderBinding
	if b.BuilderOptions(2) != preferred || b.BuilderOptions(3) != orders.DefaultBuilderOptions() {
		t.Fatal("human preference crossed the player boundary")
	}
	next := preferred
	next.Patrol[0] = orders.PatrolAssistOnly
	command := HumanCommand{Kind: HumanBuilderOptions, BuilderOptions: HumanBuilderOptionsCommand{Owner: 2, Options: next}}
	if err := s.EnqueueHumanCommand(command); err != nil {
		t.Fatal(err)
	}
	s.applyHumanCommands(10)
	if b.BuilderOptions(2) != preferred {
		t.Fatal("preference changed before the input boundary")
	}
	s.applyHumanCommands(11)
	if b.BuilderOptions(2) != next || b.BuilderOptions(3) != orders.DefaultBuilderOptions() {
		t.Fatal("command did not update only its player through the existing binding")
	}
	for _, owner := range []uint8{3, 10} {
		command.BuilderOptions.Owner = owner
		if err := s.EnqueueHumanCommand(command); err == nil {
			t.Fatalf("accepted foreign or invalid owner %d", owner)
		}
	}
	command.BuilderOptions.Owner = 2
	command.BuilderOptions.Options.Guard[1] = 3
	if err := s.EnqueueHumanCommand(command); err == nil {
		t.Fatal("accepted invalid option")
	}
	if err := s.initializeBuilderOptions(nil); err != nil {
		t.Fatal(err)
	}
	if b.BuilderOptions(2) != orders.DefaultBuilderOptions() {
		t.Fatal("new battle retained old preferences")
	}
}
