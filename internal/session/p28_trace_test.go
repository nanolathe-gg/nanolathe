package session

import (
	"encoding/binary"
	"testing"

	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/pool"
)

func TestP28ParityHashIncludesCurrentHealthSample(t *testing.T) {
	w := newSessionFixtureWorld(1, nil)
	def := &content.UnitDef{UnitName: "parity-health", MaxDamage: 100}
	h, err := w.Create(def, 1, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	u := w.Unit(h)
	u.CurrentSample = 37
	a := &Session{Clock: &clock.State{}, Econ: &economy.Service{}, Units: w}
	first, err := a.ParityAuthoritativeHash()
	if err != nil {
		t.Fatal(err)
	}
	u.CurrentSample = 38
	second, err := a.ParityAuthoritativeHash()
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("current health sample did not affect parity hash")
	}
}

func TestP28ParityHashRepeatReadPure(t *testing.T) {
	s := &Session{Clock: &clock.State{Requested: 10, Active: 10}, Econ: &economy.Service{}}
	if got, err := s.ParityAuthoritativeHash(); err != nil || got == "" {
		t.Fatalf("baseline parity hash got %q err %v", got, err)
	}
	s.EnableParityTrace([]pool.Handle{3, 1, 3})
	a, err := s.ParityAuthoritativeHash()
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.ParityAuthoritativeHash()
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("repeat hash changed: %s vs %s", a, b)
	}
	if got := s.ParityHandles(); got != nil {
		t.Fatalf("selected empty world returned %v", got)
	}
}

func TestP28TracingDoesNotChangeAuthoritativeHashOrRNG(t *testing.T) {
	a := &Session{Clock: &clock.State{Requested: 10, Active: 10}, Econ: &economy.Service{}}
	b := &Session{Clock: &clock.State{Requested: 10, Active: 10}, Econ: &economy.Service{}}
	ha, err := a.ParityAuthoritativeHash()
	if err != nil {
		t.Fatal(err)
	}
	b.EnableParityTrace(nil)
	hb, err := b.ParityAuthoritativeHash()
	if err != nil {
		t.Fatal(err)
	}
	if ha != hb {
		t.Fatalf("trace enable changed hash: %s vs %s", ha, hb)
	}
	a.SeedSessionRNG(11, 17)
	b.SeedSessionRNG(11, 17)
	if a.SimRNG().Uint32n(100) != b.SimRNG().Uint32n(100) || a.CrtRNG().Rand() != b.CrtRNG().Rand() {
		t.Fatal("trace enable changed RNG behavior")
	}
	before, err := b.ParityAuthoritativeHash()
	if err != nil {
		t.Fatal(err)
	}
	b.Clock.Requested++
	after, err := b.ParityAuthoritativeHash()
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("clock requested field did not affect hash")
	}
	requestedHash := after
	b.Clock.Active++
	after, err = b.ParityAuthoritativeHash()
	if err != nil {
		t.Fatal(err)
	}
	if requestedHash == after {
		t.Fatal("clock active field did not affect hash")
	}
}

func TestP28SessionCallbackTraceLimitAndReset(t *testing.T) {
	s := &Session{}
	s.EnableParityTrace(nil)
	s.SetParityTraceLimit(1)
	s.appendParityCallback(cob.LifecycleEvent{Name: "first"})
	s.appendParityCallback(cob.LifecycleEvent{Name: "second"})
	if !s.ParityTraceDropped() {
		t.Fatal("callback cap did not report a dropped event")
	}
	if got := s.ParityCallbackEvents(); len(got) != 1 || got[0].Name != "second" {
		t.Fatalf("bounded callback capture = %v, want newest event only", got)
	}
	s.ResetParityTrace()
	if s.ParityTraceDropped() || s.ParityCallbackEvents() != nil {
		t.Fatalf("reset retained callback diagnostics: dropped=%v events=%v", s.ParityTraceDropped(), s.ParityCallbackEvents())
	}
}

func TestP28ParityHashDoesNotMutateClock(t *testing.T) {
	for _, paused := range []bool{false, true} {
		t.Run(map[bool]string{false: "running", true: "paused"}[paused], func(t *testing.T) {
			var box [28]byte
			binary.LittleEndian.PutUint16(box[20:22], 7)
			binary.LittleEndian.PutUint16(box[22:24], 4)
			binary.LittleEndian.PutUint16(box[26:28], 0xA006)
			clockState := &clock.State{Paused: paused}
			if err := clockState.LoadBoxChecked(box); err != nil {
				t.Fatal(err)
			}
			clockState.Paused = paused
			s := &Session{Clock: clockState, Econ: &economy.Service{}}
			before := *clockState
			if _, err := s.ParityAuthoritativeHash(); err != nil {
				t.Fatal(err)
			}
			if *clockState != before {
				t.Fatalf("hash mutated clock: before=%+v after=%+v", before, *clockState)
			}
		})
	}
}
