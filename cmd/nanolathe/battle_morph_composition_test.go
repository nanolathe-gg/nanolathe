//go:build retail

package main

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/testsupport"
)

// The menu morphs its own client into the battle instead of building a new
// one, so it has to repeat every session join the direct battle entry makes.
// Missing the presentation CRT is silent — nano particles all take the same
// trajectory, screen shake stops jittering, and no cue reaches the device —
// so the join is asserted rather than left to inspection [01 §7.2][I4].
func TestMenuBattleMorphJoinsThePresentationCRT(t *testing.T) {
	root := testsupport.RetailRoot(t)
	opts := Options{Root: root, Map: "ashap plateau", Seed: 1}
	cs, err := openContent(opts)
	if err != nil {
		t.Skipf("retail assets unavailable: %v", err)
	}
	defer cs.Close()
	rng.SeedGlobal(1, 1)
	sess, cat, err := newBattleSession(opts, cs)
	if err != nil {
		t.Fatal(err)
	}
	shell, err := newGameShell(opts, cs)
	if err != nil {
		t.Fatal(err)
	}
	cl, err := client.New(client.Options{Width: 640, Height: 480, Buffer: &frame.Buffer{}})
	if err != nil {
		t.Fatal(err)
	}
	cl.SetModelFS(cs.fs)
	if cl.HasPresentationCRT() {
		t.Fatal("a fresh client already has a CRT; the assertion below proves nothing")
	}
	clPtr = cl
	defer func() { clPtr = nil }()
	if err := shell.enterBattle(sess, cat); err != nil {
		t.Fatal(err)
	}
	if !cl.HasPresentationCRT() {
		t.Fatal("menu->battle morph left the client without the session's presentation CRT")
	}
	shell.returnFromBattle(cl)
}
