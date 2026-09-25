package main

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/frame"
)

// TestVictoryCueTriggerRule locks the shipped hook's rule
// (DESIGN_INTERFACE_HUD_INPUT §3.16): the local win plays once however many
// frames show it, a loss or a draw never plays, and a later win plays only
// when its tick is below the stored one or more than 300 ticks above it.
func TestVictoryCueTriggerRule(t *testing.T) {
	result := func(kind string, tick uint32) *frame.Frame {
		return &frame.Frame{Tick: tick, Result: frame.ResultView{Ended: true, Kind: kind, Tick: tick}}
	}
	var cue victoryCue
	if cue.step(true, result("victory", 300)) {
		t.Fatal("a first win at tick 300 played: the stored zero is within 300 ticks")
	}
	plays := 0
	for range 5 {
		if cue.step(true, result("victory", 9000)) {
			plays++
		}
	}
	if plays != 1 {
		t.Fatalf("a win shown for five frames played %d times, want once", plays)
	}
	if cue.step(true, result("defeat", 20000)) || cue.step(true, &frame.Frame{Tick: 20000, Result: frame.ResultView{Ended: true, Kind: "victory", Draw: true}}) {
		t.Fatal("a loss or a draw played the cue")
	}
	if cue.last != 9000 {
		t.Fatalf("a loss stored tick %d", cue.last)
	}
	if cue.step(true, result("victory", 9300)) {
		t.Fatal("a win 300 ticks after the stored one played")
	}
	if !cue.step(true, result("victory", 9601)) {
		t.Fatal("a win more than 300 ticks after the stored one did not play")
	}
	if !cue.step(true, result("victory", 450)) {
		t.Fatal("a later battle's win below the stored tick did not play")
	}
}

// TestVictoryCueOffIsRetail: with the preference at its default a win plays
// nothing and leaves the stored tick alone.
func TestVictoryCueOffIsRetail(t *testing.T) {
	var cue victoryCue
	win := &frame.Frame{Tick: 9000, Result: frame.ResultView{Ended: true, Kind: "victory"}}
	if cue.step(false, win) || cue.last != 0 {
		t.Fatal("the cue ran with the preference off")
	}
	b := &battleSession{}
	if b.hostPreferences().VictoryCue != 0 {
		t.Fatal("the victory cue is on by default")
	}
}

// TestVictoryCueWatcherGate locks the composer's gate before both end titles
// [07 §11]: when the local slot is a watcher the title branch is never reached,
// so the cue neither plays nor stores the tick. Another slot's watcher bit does
// not affect it.
func TestVictoryCueWatcherGate(t *testing.T) {
	win := func(local uint8, watcher int) *frame.Frame {
		f := &frame.Frame{Tick: 9000, Result: frame.ResultView{Ended: true, Kind: "victory", Tick: 9000}}
		f.Selection.LocalPlayer = local
		if watcher >= 0 {
			f.Players[watcher].Watcher = true
		}
		return f
	}
	var cue victoryCue
	if cue.step(true, win(2, 2)) || cue.last != 0 {
		t.Fatal("a watching local slot reached the victory title branch")
	}
	if !cue.step(true, win(2, 5)) {
		t.Fatal("another slot's watcher bit closed the local gate")
	}
}
