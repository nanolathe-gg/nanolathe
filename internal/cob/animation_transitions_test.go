package cob

import "testing"

// Established: an immediate rotation commit does not own translation or its
// wait; interpolation arrival wakes the waiter on the next drain [04 §4.6].
func TestTurnNowPreservesMoveAndItsWait(t *testing.T) {
	code := []uint32{
		0x10021001, 30, 0x10021001, 3, 0x10001000, 0, 0,
		0x10021001, 9, 0x1000c000, 0, 0, 0x10065000,
	}
	waiter := len(code)
	code = append(code, 0x10012000, 0, 0, 0x10021001, 1, 0x10065000)
	vm := newTestVM(synthProg(code, []string{"base"}, 0, []int{0, waiter}))
	if !vm.Start(0, nil) || !vm.Start(waiter, nil) {
		t.Fatal("start")
	}
	for tick := int64(1); tick <= 3; tick++ {
		vm.Drain(1)
		if got := vm.Pieces[0].GetTrans(0).Raw(); got != tick {
			t.Fatalf("tick %d translation = %d, want %d [04 §4.6]", tick, got, tick)
		}
		if vm.Threads[1].Status != ThreadWaitMove {
			t.Fatalf("tick %d: translation waiter woke before its next-drain arrival guard", tick)
		}
	}
	vm.Drain(1)
	if vm.Threads[1].Status != ThreadIdle || vm.Pieces[0].GetAngle(0) != 9 {
		t.Fatal("move waiter did not finish after arrival, or immediate angle changed [04 §4.6]")
	}
}

// Established: positional turns and spins share their current rotation speed.
// Changing the marker must not select an older speed from another mode; saving
// between the instructions cannot change that transition [04 §4.6][08 R-SAVE-02 §9].
func TestAcceleratedSpinContinuesCurrentRotationSpeed(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup []uint32
		want  uint16
	}{
		{"turn-to-spin", []uint32{0x10021001, 60, 0x10021001, 100, 0x10002000, 0, 0}, 3},
		{"spin-turn-now-spin", []uint32{
			0x10021001, 0, 0x10021001, 150, 0x10003000, 0, 0,
			0x10021001, 0, 0x1000c000, 0, 0,
		}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code := append(append([]uint32{}, tc.setup...), 0x10065000)
			spin := len(code)
			code = append(code, 0x10021001, 30, 0x10021001, 300, 0x10003000, 0, 0, 0x10065000)
			prog := synthProg(code, []string{"base"}, 0, []int{0, spin})
			vm := newTestVM(prog)
			if !vm.Start(0, nil) {
				t.Fatal("start setup")
			}
			vm.Drain(0)
			image, err := RetailScriptImage(vm, RetailScriptWriterScratch{})
			if err != nil {
				t.Fatal(err)
			}
			restored := newTestVM(prog)
			if err := RetailScriptRestore(restored, image); err != nil {
				t.Fatal(err)
			}
			for _, candidate := range []*VM{vm, restored} {
				if !candidate.Start(spin, nil) {
					t.Fatal("start spin")
				}
				candidate.Drain(1)
				if got := candidate.Pieces[0].GetAngle(0); got != tc.want {
					t.Fatalf("spin angle = %d, want %d [04 §4.6]", got, tc.want)
				}
			}
		})
	}
}

// Established: stop-spin changes the shared speed/acceleration without replacing
// the rotation target or marker. Its ramp precedes rotation [04 §4.6].
func TestStopSpinUsesCurrentRotationMode(t *testing.T) {
	for _, decel := range []uint32{0, 30} {
		code := []uint32{
			0x10021001, 60, 0x10021001, 100, 0x10002000, 0, 0,
			0x10021001, decel, 0x10004000, 0, 0, 0x10065000,
		}
		vm := newTestVM(synthProg(code, []string{"base"}, 0, []int{0}))
		if !vm.Start(0, nil) {
			t.Fatal("start")
		}
		vm.Drain(1)
		want := uint16(decel / 30)
		if got := vm.Pieces[0].GetAngle(0); got != want {
			t.Fatalf("stop-spin deceleration %d during turn: angle=%d, want %d [04 §4.6]", decel, got, want)
		}
		vm.Drain(1)
		if vm.isTurnBusy(0, 0) || vm.ScriptDirty() {
			t.Fatal("stopped rotation retained its wait or dirty state [04 §4.6]")
		}
	}
}

func TestStoppedSpinRetainsMarkerAcrossRestore(t *testing.T) {
	code := []uint32{
		0x10021001, 0, 0x10021001, 150, 0x10003000, 0, 0,
		0x10021001, 0, 0x10004000, 0, 0, 0x10065000,
	}
	prog := synthProg(code, []string{"base"}, 0, []int{0})
	vm := newTestVM(prog)
	if !vm.Start(0, nil) {
		t.Fatal("start")
	}
	vm.Drain(1)
	image, err := RetailScriptImage(vm, RetailScriptWriterScratch{})
	if err != nil {
		t.Fatal(err)
	}
	restored := newTestVM(prog)
	if err := RetailScriptRestore(restored, image); err != nil {
		t.Fatal(err)
	}
	if !restored.anims[0].axes[0].spinActive || restored.isTurnBusy(0, 0) {
		t.Fatal("stopped spin lost its continuous-rotation marker or retained speed [04 §4.6][08 R-SAVE-02 §9]")
	}
	restored.Drain(1)
	if restored.ScriptDirty() {
		t.Fatal("restored stopped spin did not clear its forced dirty pass")
	}
}
