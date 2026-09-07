package main

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/session"
)

func TestBattleMessageRetirementBindingUsesTheClientRing(t *testing.T) {
	s := &session.Session{
		State:    session.StateBattle,
		Clock:    &clock.State{Requested: 10, Active: 10, ScaledAnchor: 31, GlobalTick: 31},
		Snapshot: frame.NewBuffer(),
	}
	cl, err := client.New(client.Options{Buffer: s.Snapshot})
	if err != nil {
		t.Fatal(err)
	}
	cl.ConfigureMessageLines(4, 0)
	cl.MessageRing().Append("one", 1, 0, 10, 0)
	cl.MessageRing().Append("two", 1, 0, 10, 0)
	bindBattleMessageRetirement(s, cl)
	s.Step(31)
	if lines := cl.MessageLines(); len(lines) != 1 || lines[0].Text != "two" {
		t.Fatalf("first host pump lines = %#v, want second line only", lines)
	}
	s.Step(31)
	if lines := cl.MessageLines(); len(lines) != 0 {
		t.Fatalf("second host pump lines = %#v, want none", lines)
	}
}
