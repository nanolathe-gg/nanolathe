package cob

// Contract tests for [R-COB-02 §1] (vertical-slice callback/port table: per-
// callback mode D/I/Q, wake semantics, argument cells, receiver, return
// consumption) and [R-COB-02 §2] (same-tick callback integration trace: the
// six-step visit order, defer-vs-immediate execution, the wake-flush barriers,
// slot-order execution, and the sleep-0 one-tick minimum).
//
// All fixtures are authored COB via the package's makeCOB helpers and run
// through the real CallbackBridge. Probe scripts report their argument cells
// by writing them to an engine-write port the test binds — the retail compiled
// form pushes the identifier first and the value second [R-P0-10].

import (
	"fmt"
	"testing"
)

// event ids; the probe port is 100+id so engine ports 1..20 stay untouched.
const (
	evCreate = iota + 1
	evSMR
	evSetDir
	evSetSpd
	evTC
	evAimH
	evAimP
	evFire
	evRockX
	evRockZ
	evHitX
	evHitZ
	evTake
	evStartMov
	evMR2
	evSFXOcc
	evStopMov
	evStartBuild
	evKilled
	evNano
	evSlept
)

const probePortBase = 100

// traceRecorder collects engine-write probes as "id=value" strings in
// execution order.
type traceRecorder struct{ marks []string }

func (r *traceRecorder) bind(vm *VM) {
	for id := 1; id <= 32; id++ {
		vm.BindPort(Port(probePortBase+id), func(args []int32) int32 {
			r.marks = append(r.marks, fmt.Sprintf("%d=%d", args[0], args[1]))
			return 0
		})
	}
}

func (r *traceRecorder) want(ev, val int32) string {
	return fmt.Sprintf("%d=%d", probePortBase+ev, val)
}

// pushConst / pushLocal / popLocal are the compiled operand forms [04 §4.3].
func pushConst(v int32) []uint32 { return []uint32{0x10021001, uint32(v)} }
func pushLocal(i int32) []uint32 { return []uint32{0x10021002, uint32(i)} }
func popLocal(i int32) []uint32  { return []uint32{0x10023002, uint32(i)} }

// engineWrite is the compiled engine-write form: identifier pushed first,
// value second, then the set opcode [R-P0-10].
var engineWrite = []uint32{0x10082000}

// markLocal appends a probe that reports window word i to event ev.
func markLocal(ev, i int32) []uint32 {
	return append(append(append([]uint32{}, pushConst(probePortBase+ev)...), pushLocal(i)...), engineWrite...)
}

// markConst appends a probe that reports the constant v to event ev.
func markConst(ev, v int32) []uint32 {
	return append(append(append([]uint32{}, pushConst(probePortBase+ev)...), pushConst(v)...), engineWrite...)
}

// buildTraceProg authors the full vertical-slice fixture COB. Every probe
// script reports its cells and returns; AimPrimary returns 1 (the grant
// value); Killed assigns its variant cell 3; Sleeper0 observes the sleep-0
// one-tick minimum.
func buildTraceProg(t *testing.T) *Program {
	t.Helper()
	var code []uint32
	idx := []uint32{}
	add := func(name string, words ...[]uint32) {
		idx = append(idx, uint32(len(code)))
		for _, w := range words {
			code = append(code, w...)
		}
	}
	add("Create", markConst(evCreate, 0), []uint32{0x10065000})
	add("SetMaxReloadTime", markLocal(evSMR, 0), []uint32{0x10065000})
	add("SetDirection", markLocal(evSetDir, 0), []uint32{0x10065000})
	add("SetSpeed", markLocal(evSetSpd, 0), []uint32{0x10065000})
	add("TargetCleared", markLocal(evTC, 0), []uint32{0x10065000})
	add("AimPrimary", markLocal(evAimH, 0), markLocal(evAimP, 1), pushConst(1), []uint32{0x10065000})
	add("FirePrimary", markConst(evFire, 0), []uint32{0x10065000})
	add("RockUnit", markLocal(evRockX, 0), markLocal(evRockZ, 1), []uint32{0x10065000})
	add("HitByWeapon", markLocal(evHitX, 0), markLocal(evHitZ, 1), []uint32{0x10065000})
	add("TakeDamage", markLocal(evTake, 0), []uint32{0x10065000})
	add("StartMoving", markConst(evStartMov, 0), []uint32{0x10065000})
	add("MoveRate1", markConst(evMR2, 1), []uint32{0x10065000})
	add("MoveRate2", markConst(evMR2, 0), []uint32{0x10065000})
	add("MoveRate3", markConst(evMR2, 3), []uint32{0x10065000})
	add("setSFXoccupy", markLocal(evSFXOcc, 0), []uint32{0x10065000})
	add("StopMoving", markConst(evStopMov, 0), []uint32{0x10065000})
	add("StartBuilding", markLocal(evStartBuild, 0), []uint32{0x10065000})
	add("Killed", markLocal(evKilled, 0), pushConst(3), popLocal(1), []uint32{0x10065000})
	add("Sleeper0", pushConst(0), []uint32{0x10013000}, markConst(evSlept, 0), []uint32{0x10065000})
	add("QueryNanoPiece", pushConst(42), popLocal(0), markLocal(evNano, 0), []uint32{0x10065000})
	prog, err := Load(makeCOBWithStatics(2, code, []string{
		"Create", "SetMaxReloadTime", "SetDirection", "SetSpeed", "TargetCleared",
		"AimPrimary", "FirePrimary", "RockUnit", "HitByWeapon", "TakeDamage",
		"StartMoving", "MoveRate1", "MoveRate2", "MoveRate3", "setSFXoccupy",
		"StopMoving", "StartBuilding", "Killed", "Sleeper0", "QueryNanoPiece",
	}, idx, []string{"base"}))
	if err != nil {
		t.Fatalf("Load trace fixture: %v", err)
	}
	return prog
}

// newTraceBridge returns a fresh bridge over the fixture with the recorder
// bound and a deterministic (unused) simulation stream.
func newTraceBridge(t *testing.T) (*CallbackBridge, *traceRecorder) {
	t.Helper()
	vm := NewVM(buildTraceProg(t))
	rec := &traceRecorder{}
	rec.bind(vm)
	return NewCallbackBridge(vm), rec
}

func TestVerticalSliceCallbackModes(t *testing.T) {
	// Per-callback mode and wake semantics [R-COB-02 §1]: D allocates only
	// (first runs in the visit's normal drain), D+wake is D plus the all-slot
	// delta-0 barrier inline, Q runs one slot inline with no drain and no
	// piece pass. Return consumption: only the Aim* receiver consumes the
	// delivered cell, and only a nonzero grant marks aim-ready.
	cases := []struct {
		name     string
		start    func(b *CallbackBridge) CallbackResult
		want     CallbackMode
		wantWake bool // D+wake: effect visible without any further drain
		checkVM  func(t *testing.T, b *CallbackBridge, rec *traceRecorder, res CallbackResult)
	}{
		{"Create", func(b *CallbackBridge) CallbackResult { return b.Create() }, ModeDeferred, true,
			func(t *testing.T, b *CallbackBridge, rec *traceRecorder, res CallbackResult) {
				if !b.CreateInvoked() {
					t.Fatal("Create must consume its one-shot slot [04 §5.1]")
				}
			}},
		{"SetMaxReloadTime", func(b *CallbackBridge) CallbackResult { return b.SetMaxReloadTime(45) }, ModeDeferred, false,
			func(t *testing.T, b *CallbackBridge, rec *traceRecorder, res CallbackResult) {
				// Issued after Create so it lands OUTSIDE Create's own drain
				// [R-COB-02 §1]: nothing ran yet.
				if len(rec.marks) != 0 {
					t.Fatalf("deferred SetMaxReloadTime ran at issue: %v", rec.marks)
				}
			}},
		{"Activate", func(b *CallbackBridge) CallbackResult { return b.Activate() }, ModeDeferred, false, nil},
		{"Deactivate", func(b *CallbackBridge) CallbackResult { return b.Deactivate() }, ModeDeferred, false, nil},
		{"StartBuilding edge", func(b *CallbackBridge) CallbackResult { return b.StartBuilding() }, ModeDeferred, false, nil},
		{"StartBuilding heading", func(b *CallbackBridge) CallbackResult { return b.StartBuildingHeading(0x1234) }, ModeDeferred, false,
			func(t *testing.T, b *CallbackBridge, rec *traceRecorder, res CallbackResult) {
				b.Drain(1)
				assertMarks(t, rec, []string{rec.want(evStartBuild, 0x1234)})
			}},
		{"StopBuilding", func(b *CallbackBridge) CallbackResult { return b.StopBuilding() }, ModeDeferred, false, nil},
		{"SetDirection", func(b *CallbackBridge) CallbackResult { return b.SetDirection(0x1234) }, ModeDeferred, false, nil},
		{"SetSpeed", func(b *CallbackBridge) CallbackResult { return b.SetSpeed(321) }, ModeDeferred, false, nil},
		{"StartMoving", func(b *CallbackBridge) CallbackResult { return b.StartMoving() }, ModeDeferred, true, nil},
		{"StopMoving", func(b *CallbackBridge) CallbackResult { return b.StopMoving() }, ModeDeferred, true, nil},
		{"MoveRate1", func(b *CallbackBridge) CallbackResult { return b.MoveRate1() }, ModeDeferred, true, nil},
		{"MoveRate2", func(b *CallbackBridge) CallbackResult { return b.MoveRate2() }, ModeDeferred, true, nil},
		{"MoveRate3", func(b *CallbackBridge) CallbackResult { return b.MoveRate3() }, ModeDeferred, true, nil},
		{"setSFXoccupy", func(b *CallbackBridge) CallbackResult { return b.SetSFXoccupy(3) }, ModeDeferred, true, nil},
		{"Aim", func(b *CallbackBridge) CallbackResult { return b.Aim(WeaponPrimary, 0x0AAA, 0x0BBB, nil) }, ModeDeferred, false, nil},
		{"Fire", func(b *CallbackBridge) CallbackResult { return b.Fire(WeaponPrimary) }, ModeDeferred, false, nil},
		{"RockUnit", func(b *CallbackBridge) CallbackResult { return b.RockUnit(0x0200) }, ModeDeferred, false, nil},
		{"HitByWeapon", func(b *CallbackBridge) CallbackResult { return b.HitByWeapon(0x40) }, ModeDeferred, false, nil},
		{"TakeDamage", func(b *CallbackBridge) CallbackResult { return b.TakeDamage(66) }, ModeDeferred, false, nil},
		{"TargetCleared", func(b *CallbackBridge) CallbackResult { return b.TargetCleared(2) }, ModeDeferred, false, nil},
		{"Killed network replay", func(b *CallbackBridge) CallbackResult { return b.Killed(5) }, ModeDeferred, true, nil},
		{"KilledLocal query", func(b *CallbackBridge) CallbackResult { return b.KilledLocal(37) }, ModeQuery, true, nil},
		{"QueryNanoPiece", func(b *CallbackBridge) CallbackResult { return b.QueryNanoPiece() }, ModeQuery, true, nil},
		{"QueryTransport", func(b *CallbackBridge) CallbackResult { return b.QueryTransport() }, ModeQuery, true, nil},
		{"QueryLandingPad", func(b *CallbackBridge) CallbackResult { return b.QueryLandingPad() }, ModeQuery, true, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, rec := newTraceBridge(t)
			before := b.VM.DrainCalls
			res := tc.start(b)
			if res.Mode != tc.want {
				t.Fatalf("mode = %d want %d [R-COB-02 §1]", res.Mode, tc.want)
			}
			if tc.wantWake {
				// D+wake semantics: all-slot delta-0 barrier inline
				// [04 §4.2][R-CB-01 §2]. Q adds no drain; plain D adds none.
				wantDrains := before
				if res.Wake {
					wantDrains = before + 1
				}
				if b.VM.DrainCalls != wantDrains {
					t.Fatalf("DrainCalls %d want %d [R-COB-02 §2]", b.VM.DrainCalls, wantDrains)
				}
			} else if b.VM.DrainCalls != before {
				t.Fatalf("deferred start must not drain: DrainCalls %d want %d [R-COB-02 §1]", b.VM.DrainCalls, before)
			}
			if tc.checkVM != nil {
				tc.checkVM(t, b, rec, res)
			}
		})
	}
}

func TestVerticalSliceArgumentCells(t *testing.T) {
	// Argument cells per callback [R-COB-02 §1]: the probe scripts report
	// their window words after the callback's first execution.
	t.Run("deferred cells", func(t *testing.T) {
		b, rec := newTraceBridge(t)
		b.SetMaxReloadTime(45)                    // 1 cell: trunc(45*1000/30) [04 §5.3]
		b.SetDirection(0x1234)                    // 1 cell: zero-extended direction [04 §5.3]
		b.SetSpeed(321)                           // 1 cell: signed speed <<4 [04 §5.3]
		b.TargetCleared(2)                        // 1 cell: weapon slot
		b.TakeDamage(66)                          // 1 cell: post-hit percentage
		b.Aim(WeaponPrimary, 0x0AAA, 0x0BBB, nil) // 2 cells: heading, pitch
		b.RockUnit(0x0200)                        // 2 cells: -cos(rel)*800, -sin(rel)*800
		b.HitByWeapon(0x40)                       // 2 cells: cos(dir)*400, sin(dir)*400
		b.Drain(1)
		rx, rz := RockUnitArgs(0x0200)
		hx, hz := HitByWeaponArgs(0x40)
		// Drain order is thread-slot order; the callbacks were queued in the
		// call order above, each taking the lowest free slot.
		want := []string{
			rec.want(evSMR, MaxReloadMillis(45)),
			rec.want(evSetDir, 0x1234),
			rec.want(evSetSpd, 321<<4),
			rec.want(evTC, 2),
			rec.want(evTake, 66),
			rec.want(evAimH, 0x0AAA),
			rec.want(evAimP, 0x0BBB),
			rec.want(evRockX, rx), rec.want(evRockZ, rz),
			rec.want(evHitX, hx), rec.want(evHitZ, hz),
		}
		assertMarks(t, rec, want)
	})
	t.Run("immediate cells", func(t *testing.T) {
		b, rec := newTraceBridge(t)
		b.SetSFXoccupy(3) // 1 cell: occupancy band 0..4 [R-COB-02 §1]
		assertMarks(t, rec, []string{rec.want(evSFXOcc, 3)})
	})
	t.Run("killed query cells", func(t *testing.T) {
		b, _ := newTraceBridge(t)
		res := b.KilledLocal(37) // cell 0 in/out severity; script assigns variant 3
		if !res.Started || !res.Completed {
			t.Fatalf("Killed query = %#v, want completed [R-COB-02 §1]", res)
		}
		if res.Values != ([4]int32{37, 3, 0, 0}) {
			t.Fatalf("Killed cells = %v want [37 3 0 0] [R-COB-02 §1]", res.Values)
		}
	})
	t.Run("query seeds", func(t *testing.T) {
		b, _ := newTraceBridge(t)
		if res := b.QueryNanoPiece(); !res.Started || res.QueryValue() != 42 {
			t.Fatalf("QueryNanoPiece = %#v, want cell 0 copied back [R-COB-02 §1]", res)
		}
		// Missing scripts leave the seeds untouched: -1 transport fallback,
		// all -1 landing pads [R-COB-02 §1].
		if res := b.QueryTransport(); res.Started || res.Values != (QueryTransportSeed()) {
			t.Fatalf("QueryTransport = %#v, want seed -1 preserved", res)
		}
		if res := b.QueryLandingPad(); res.Started || res.Values != (QueryLandingPadSeed()) {
			t.Fatalf("QueryLandingPad = %#v, want all -1 preserved", res)
		}
	})
}

func TestAimReturnConsumptionGrantsOnlyNonzero(t *testing.T) {
	// Return consumption [R-COB-02 §1]: the Aim* receiver is the only grant
	// path; the explicit return value is delivered through the slot receiver
	// after the deferred thread completes; a nonzero delivery marks aim-ready
	// (the negatives are locked by aimready_test.go).
	b, rec := newTraceBridge(t)
	slot := &AimSlot{}
	slot.StartAim()
	var got []CallbackReturn
	res := b.Aim(WeaponPrimary, 0x11, 0x22, func(cr CallbackReturn) {
		got = append(got, cr)
		slot.CompleteAim(cr.Value)
	})
	if !res.Started {
		t.Fatal("Aim must start")
	}
	if slot.CanFire() {
		t.Fatal("no grant before the deferred completion [R-COB-02 §1]")
	}
	if len(rec.marks) != 0 {
		t.Fatalf("Aim ran at issue: %v", rec.marks)
	}
	b.Drain(1)
	if len(got) != 1 || !got[0].Explicit || got[0].Value != 1 {
		t.Fatalf("receiver = %+v, want explicit 1 [R-COB-02 §1]", got)
	}
	if !slot.Ready {
		t.Fatal("nonzero completed result must grant aim-ready [R-COB-02 §1]")
	}
	assertMarks(t, rec, []string{rec.want(evAimH, 0x11), rec.want(evAimP, 0x22)})
}

func assertMarks(t *testing.T, rec *traceRecorder, want []string) {
	t.Helper()
	if len(rec.marks) != len(want) {
		t.Fatalf("marks = %v\nwant %v", rec.marks, want)
	}
	for i := range want {
		if rec.marks[i] != want[i] {
			t.Fatalf("marks[%d] = %q want %q\nall: %v\nwant: %v", i, rec.marks[i], want[i], rec.marks, want)
		}
	}
}

func TestSameTickTraceFixture(t *testing.T) {
	// The composed order of one unit visit [R-COB-02 §2]: unit update
	// (deferred SetDirection/SetSpeed) → weapon update (deferred
	// TargetCleared/Aim/Fire/RockUnit, inline Q queries) → normal COB drain
	// delta 1 → orders/build work → movement integration (D+wake
	// StartMoving/MoveRateN/setSFXoccupy) → slot-end death handling
	// (synchronous Killed query). Barriers: the normal drain executes
	// everything queued in steps 1–2 ordered by thread slot, not queue order;
	// every D+wake start flushes earlier deferrals at its delta-0
	// all-slot barrier; queries run one slot inline with no drain.
	b, rec := newTraceBridge(t)
	vm := b.VM
	slot := &AimSlot{}
	slot.StartAim()
	var delivered []CallbackReturn

	// Creation, before visit 1: Create is D+wake (one all-slot delta-0
	// drain plus one piece pass); SetMaxReloadTime is deferred and lands
	// OUTSIDE Create's own drain [R-COB-02 §1].
	b.Create()
	b.SetMaxReloadTime(45)
	assertMarks(t, rec, []string{rec.want(evCreate, 0)})
	if got := vm.DrainCalls; got != 1 {
		t.Fatalf("creation drains %d want 1 [R-COB-02 §2]", got)
	}

	// Step 1 — unit update queues deferred SetDirection then SetSpeed.
	b.SetDirection(0x1234)
	b.SetSpeed(321)
	// Step 2 — weapon update. The fixture stands in for the unlocated engine
	// interrupt producer (TODO(T25)) by killing the queued SetDirection
	// thread: its slot is freed and immediately reused, so the step-3 drain
	// order diverges from queue order.
	vm.killThread(1)
	b.TargetCleared(1) // reuses slot 1
	if b.VM.LastStartedThread() != 1 {
		t.Fatalf("TargetCleared thread %d want reused slot 1 [R-COB-02 §2]", b.VM.LastStartedThread())
	}
	if res := b.Aim(WeaponPrimary, 0x0AAA, 0x0BBB, func(cr CallbackReturn) {
		delivered = append(delivered, cr)
		slot.CompleteAim(cr.Value)
	}); !res.Started {
		t.Fatal("Aim must start")
	}
	if slot.CanFire() {
		t.Fatal("no grant at issue time — only a completed nonzero result grants [R-COB-02 §1]")
	}
	b.FireThenRock(WeaponPrimary, 0x0200)
	// A Q query in the weapon window runs its one slot inline: visible now,
	// with no drain and no piece pass [R-COB-02 §2].
	if res := b.QueryNanoPiece(); !res.Started || res.QueryValue() != 42 {
		t.Fatalf("inline Q = %#v", res)
	}
	assertMarks(t, rec, []string{rec.want(evCreate, 0), rec.want(evNano, 42)})

	// Step 3 — the normal COB drain (delta 1) executes every deferred
	// callback queued in steps 1–2, ordered by thread slot: the re-used slot 1
	// (TargetCleared, queued later) runs BEFORE slot 2 (SetSpeed, queued
	// earlier) [R-COB-02 §2].
	b.Drain(1)
	assertMarks(t, rec, []string{
		rec.want(evCreate, 0), rec.want(evNano, 42),
		rec.want(evSMR, MaxReloadMillis(45)),               // creation-time deferral, slot 0
		rec.want(evTC, 1),                                  // slot 1 — queued after SetSpeed
		rec.want(evSetSpd, 321<<4),                         // slot 2 — queued before TargetCleared
		rec.want(evAimH, 0x0AAA), rec.want(evAimP, 0x0BBB), // slot 3
		rec.want(evFire, 0),                                                                  // slot 4
		rec.want(evRockX, mustRock(t, 0x0200)[0]), rec.want(evRockZ, mustRock(t, 0x0200)[1]), // slot 5
	})
	if len(delivered) != 1 || !delivered[0].Explicit || delivered[0].Value != 1 {
		t.Fatalf("Aim receiver = %+v, want one explicit 1 [R-COB-02 §1]", delivered)
	}
	if !slot.Ready {
		t.Fatal("the completed nonzero Aim result must grant aim-ready [R-COB-02 §1]")
	}
	if got := vm.DrainCalls; got != 2 {
		t.Fatalf("drains after step 3 = %d want 2 [R-COB-02 §2]", got)
	}

	// Step 4 — orders/build work queues the damage pair (the post-unit
	// producer path); they must NOT run until a later barrier.
	b.HitByWeapon(0x40)
	b.TakeDamage(66)
	assertMarks(t, rec, []string{
		rec.want(evCreate, 0), rec.want(evNano, 42),
		rec.want(evSMR, MaxReloadMillis(45)),
		rec.want(evTC, 1), rec.want(evSetSpd, 321<<4),
		rec.want(evAimH, 0x0AAA), rec.want(evAimP, 0x0BBB),
		rec.want(evFire, 0),
		rec.want(evRockX, mustRock(t, 0x0200)[0]), rec.want(evRockZ, mustRock(t, 0x0200)[1]),
	})

	// Step 5 — movement integration. StartMoving is a D+wake start:
	// its all-slot delta-0 barrier flushes the step-4 deferrals (slots 0,1)
	// BEFORE its own marker (slot 2) — the wake-flush barrier pulls a step-4
	// deferral into the same visit [R-COB-02 §2].
	b.StartMoving()
	b.MoveRate2()
	b.SetSFXoccupy(2)
	hx, hz := HitByWeaponArgs(0x40)
	rx, rz := RockUnitArgs(0x0200)
	assertMarks(t, rec, []string{
		rec.want(evCreate, 0), rec.want(evNano, 42),
		rec.want(evSMR, MaxReloadMillis(45)),
		rec.want(evTC, 1), rec.want(evSetSpd, 321<<4),
		rec.want(evAimH, 0x0AAA), rec.want(evAimP, 0x0BBB),
		rec.want(evFire, 0), rec.want(evRockX, rx), rec.want(evRockZ, rz),
		rec.want(evHitX, hx), rec.want(evHitZ, hz), // step-4 deferrals, flushed
		rec.want(evTake, 66),    // slot 1
		rec.want(evStartMov, 0), // slot 2 — the flushing start itself
		rec.want(evMR2, 0),      // D+wake
		rec.want(evSFXOcc, 2),   // D+wake
	})

	// Step 6 — slot-end death handling runs the synchronous local Killed
	// query: severity in cell 0, the script's variant assignment copied back,
	// no drain and no piece pass [R-COB-02 §1][R-COB-02 §2].
	res := b.KilledLocal(37)
	if !res.Started || !res.Completed || res.Values != ([4]int32{37, 3, 0, 0}) {
		t.Fatalf("local Killed query = %#v [R-COB-02 §1]", res)
	}
	if got := vm.DrainCalls; got != 5 {
		t.Fatalf("drains after visit 1 = %d want 5 (1 creation + 1 normal + 3 immediates; queries add none)", got)
	}

	// Visit 2 — a sleep-0 thread observes the one-tick minimum: it yields in
	// the drain that issues the sleep and cannot complete in that same visit
	// [R-COB-02 §2] fixture note (d). The next drain of ANY delta wakes it —
	// including the delta-0 wake pass of the StopMoving barrier below, which
	// wakes any thread whose timer is already at or below zero [04 §4.6].
	b.Deferred("Sleeper0", nil, nil)
	b.Drain(1)
	for _, m := range rec.marks {
		if m == rec.want(evSlept, 0) {
			t.Fatalf("sleep 0 completed in its own visit: %v", rec.marks)
		}
	}
	// Step 5 of visit 2 — StopMoving is D+wake; its all-slot delta-0 wake
	// pass completes the sleeping timer-0 thread [04 §4.6].
	b.StopMoving()
	found := false
	for _, m := range rec.marks {
		if m == rec.want(evSlept, 0) {
			found = true
		}
	}
	if !found {
		t.Fatalf("the delta-0 wake pass must wake a timer-at-zero thread [04 §4.6]: %v", rec.marks)
	}

	// Visit 3 — the network-replay Killed is a deferred start with wake=1;
	// its barrier runs it inline [R-CB-01 §2].
	b.Drain(1)
	if got := vm.DrainCalls; got != 8 {
		t.Fatalf("drains after visit 3 normal pass = %d want 8", got)
	}
	netRes := b.Killed(5)
	if netRes.Mode != ModeDeferred || !netRes.Wake || !netRes.Started {
		t.Fatalf("network Killed = %#v, want started deferred wake [R-CB-01 §2]", netRes)
	}
	assertMarks(t, rec, []string{
		rec.want(evCreate, 0), rec.want(evNano, 42),
		rec.want(evSMR, MaxReloadMillis(45)),
		rec.want(evTC, 1), rec.want(evSetSpd, 321<<4),
		rec.want(evAimH, 0x0AAA), rec.want(evAimP, 0x0BBB),
		rec.want(evFire, 0), rec.want(evRockX, rx), rec.want(evRockZ, rz),
		rec.want(evHitX, hx), rec.want(evHitZ, hz), rec.want(evTake, 66),
		rec.want(evStartMov, 0), rec.want(evMR2, 0), rec.want(evSFXOcc, 2),
		rec.want(evKilled, 37),
		// StopMoving's delta-0 barrier drains slots ascending: the sleeping
		// timer-0 thread (slot 0) completes BEFORE StopMoving (slot 1) [04
		// §4.6][R-COB-02 §2].
		rec.want(evSlept, 0),
		rec.want(evStopMov, 0),
		// The network-replay Killed runs the same authored Killed script; its
		// probe reports the severity argument under the Killed event.
		rec.want(evKilled, 5),
	})
	if got := vm.DrainCalls; got != 9 {
		t.Fatalf("total drains %d want 9 [R-COB-02 §2]", got)
	}
}

func mustRock(t *testing.T, rel int16) [2]int32 {
	t.Helper()
	x, z := RockUnitArgs(rel)
	return [2]int32{x, z}
}
