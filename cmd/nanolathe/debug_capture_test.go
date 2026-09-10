package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/session"
)

func TestDebugCapturePausesBeforeTickAndConsumesInput(t *testing.T) {
	for _, paused := range []bool{false, true} {
		s := &session.Session{Clock: &clock.State{GlobalTick: 7, ScaledAnchor: 5, Carry: .5, Requested: 10, Active: 10, Paused: paused}, Snapshot: frame.NewBuffer()}
		s.SeedSessionRNG(41, 23)
		c, err := client.New(client.Options{Buffer: s.Snapshot, Width: 16, Height: 16})
		if err != nil {
			t.Fatal(err)
		}
		b := &battleSession{sess: s}
		b.battleState().OpenOptions()
		// Fail before runtime/OS collection; the pause-and-consume boundary must
		// survive even when no capture directory can be created.
		base := filepath.Join(t.TempDir(), "file")
		if err = os.WriteFile(base, []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
		b.debugCaptureBase = base
		beforeSim, beforeCRT := *s.SimRNG(), *s.CrtRNG()
		before := *s.Clock
		in := c.Input()
		in.Kbd.SetKey(input.KeyCtrl, true)
		in.Kbd.SetKey(input.KeyShift, true)
		in.Kbd.SetKey(input.KeyF11, true)
		in.Mouse.SetButton(input.MouseButtonLeft, true)
		b.viewerStep(1, c)
		if !s.Clock.Paused || s.Clock.GlobalTick != before.GlobalTick || s.Clock.Carry != before.Carry || s.Clock.ScaledAnchor != before.ScaledAnchor {
			t.Fatalf("capture advanced scheduler: %+v", s.Clock)
		}
		if *s.SimRNG() != beforeSim || *s.CrtRNG() != beforeCRT {
			t.Fatal("capture drew RNG")
		}
		if b.debugCaptureError == nil {
			t.Fatal("disk failure lost")
		}
		if in.Kbd.KeyDown(input.KeyF11) || in.Mouse.Pressed(input.MouseButtonLeft) {
			t.Fatal("capture sample leaked edges")
		}
		if b.handleDebugCapture(c) {
			t.Fatal("held shortcut captured twice")
		}
		in.Kbd.SetKey(input.KeyF11, false)
		in.Kbd.SetKey(input.KeyF11, true)
		if !b.handleDebugCapture(c) {
			t.Fatal("independent edge ignored")
		}
	}
}

func TestDebugCaptureWritesBundleWithFailedDevice(t *testing.T) {
	s := &session.Session{Clock: &clock.State{GlobalTick: 11, Paused: true}, Snapshot: frame.NewBuffer()}
	c, err := client.New(client.Options{Buffer: s.Snapshot, Width: 16, Height: 16})
	if err != nil {
		t.Fatal(err)
	}
	b := &battleSession{sess: s, debugCaptureBase: t.TempDir()}
	directory, err := b.writeDebugCapture(c, true)
	if err == nil {
		t.Fatal("missing device and services must mark partial bundle")
	}
	for _, name := range []string{"manifest.json", "session.json", "units.jsonl", "client.json", "runtime.json", "process.json", "heap.pprof", "allocs.pprof", "goroutine.pprof", "goroutines.txt", "threadcreate.pprof"} {
		if _, e := os.Stat(filepath.Join(directory, name)); e != nil {
			t.Fatalf("missing %s: %v", name, e)
		}
	}
	if !s.Clock.Paused || s.Clock.GlobalTick != 11 {
		t.Fatal("capture changed stopped boundary")
	}
}
