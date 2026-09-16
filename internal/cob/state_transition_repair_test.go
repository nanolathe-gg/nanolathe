package cob

import "testing"

// Invalid-opcode release does not wake a caller; only return and signal do.
// The wait records a slot, so a later tenant's explicit wake still reaches it
// [04 §4.2][04 §4.3].
func TestInvalidOpcodeCallerWaitSurvivesDrainsAndReuse(t *testing.T) {
	for _, wake := range []string{"return", "signal", "script signal"} {
		t.Run(wake, func(t *testing.T) {
			code := []uint32{
				0x10021001, 2, 0x10068000, // caller uses mask 2
				0x10062000, 1, 0, // call B
				0x10021001, 91, 0x10023004, 0, // record resumed caller
				0x10021001, 0, 0x10065000,
			}
			entries := []int{0, len(code)}
			code = append(code, 0x10099000) // B: invalid opcode
			entries = append(entries, len(code))
			code = append(code, 0x10021001, 7, 0x10065000) // C: return
			entries = append(entries, len(code))
			code = append(code, 0x10021001, 1000, 0x10013000, 0x10065000) // D: sleep
			entries = append(entries, len(code))
			code = append(code, 0x10021001, 4, 0x10068000, 0x10021001, 1, 0x10067000, 0x10065000) // E: signal mask 1, survive
			vm := newTestVM(synthProg(code, nil, 1, entries))
			if !vm.StartByName("A", nil) {
				t.Fatal("caller start failed")
			}
			assertBlocked := func() {
				t.Helper()
				if vm.Threads[0].Status != ThreadWaitCall || vm.Threads[0].WaitThread != 1 || vm.statics[0] != 0 || vm.ActiveThreadCount() != 1 {
					t.Fatalf("caller resumed without an explicit wake: thread=%+v static=%d active=%d", vm.Threads[0], vm.statics[0], vm.ActiveThreadCount())
				}
			}
			vm.Drain(1)
			assertBlocked()
			for _, delta := range []int{0, 1, 5} {
				vm.Drain(delta)
				assertBlocked()
			}
			if !vm.StartByName("B", nil) || vm.LastStartedThread() != 1 {
				t.Fatal("invalid callee slot was not reused")
			}
			vm.Drain(1)
			assertBlocked()
			vm.Drain(1)
			assertBlocked()
			if wake == "return" {
				vm.StartByName("C", nil)
				vm.Drain(1)
			} else {
				vm.StartByName("D", nil)
				vm.Drain(1)
				if vm.Threads[0].Status != ThreadWaitCall {
					t.Fatal("slot reuse woke caller before signal")
				}
				if wake == "signal" {
					vm.Signal(1)
				} else {
					vm.StartByName("E", nil)
					vm.Drain(0)
				}
			}
			if vm.Threads[0].Status != ThreadRunning || vm.Threads[0].WaitThread != -1 || vm.statics[0] != 0 {
				t.Fatalf("explicit wake did not make earlier caller runnable: %+v", vm.Threads[0])
			}
			vm.Drain(1)
			if vm.Threads[0].Status != ThreadIdle || vm.statics[0] != 91 || vm.ActiveThreadCount() != 0 {
				t.Fatalf("woken caller failed to finish: thread=%+v static=%d active=%d", vm.Threads[0], vm.statics[0], vm.ActiveThreadCount())
			}
		})
	}
}

func TestCallWaitDoesNotPollIdleSlot(t *testing.T) {
	// A restored wait must retain its state even if its callee is already idle
	// [04 §4.2]. This isolates the scheduler from the opcode release path.
	vm := newTestVM(synthProg([]uint32{0x10065000}, nil, 0, nil))
	vm.Start(0, nil)
	vm.Threads[0].Status = ThreadWaitCall
	vm.Threads[0].WaitThread = 1
	vm.Drain(1)
	if vm.Threads[0].Status != ThreadWaitCall || vm.Threads[0].WaitThread != 1 {
		t.Fatalf("scheduler polled idle callee: %+v", vm.Threads[0])
	}
}

// The argument-form starter writes four cells before setting its logical top;
// name-form zero-argument starts preserve all stale cells [R-COB-01 §1].
func TestDeferredArgsLogicalAndPhysicalWindow(t *testing.T) {
	for _, tc := range []struct {
		name  string
		arity int
		cells [4]int32
	}{
		{"cleanup", 0, [4]int32{}},
		{"transport", 1, [4]int32{17, 2301, 0, 0}},
		{"four arguments", 4, [4]int32{17, 23, 41, 59}},
	} {
		for _, wake := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/deferred", true: "/wake"}[wake], func(t *testing.T) {
				vm := newTestVM(synthProg([]uint32{0x10065000}, nil, 0, nil))
				vm.Threads[0].Stack = [32]int32{71, 72, 73, 74, 75}
				b := NewCallbackBridge(vm)
				var phases []string
				b.SetLifecycleSink(func(e LifecycleEvent) {
					phases = append(phases, e.Phase)
					if e.Phase == "start" {
						thread := vm.Threads[e.Thread]
						if thread.SP != tc.arity || [4]int32(thread.Stack[:4]) != tc.cells || thread.Stack[4] != 75 {
							t.Fatalf("wrong argument frame: SP=%d cells=%v fifth=%d", thread.SP, thread.Stack[:4], thread.Stack[4])
						}
					}
				})
				var returns []CallbackReturn
				receiver := func(r CallbackReturn) { returns = append(returns, r) }
				var result CallbackResult
				if wake {
					result = b.DeferredWakeArgs("A", tc.arity, tc.cells, receiver)
				} else {
					result = b.DeferredArgs("A", tc.arity, tc.cells, receiver)
				}
				if !result.Started || result.Wake != wake || result.Completed != wake {
					t.Fatalf("unexpected start result: %+v", result)
				}
				if !wake {
					if len(returns) != 0 || vm.DrainCalls != 0 {
						t.Fatal("deferred argument callback ran before drain")
					}
					b.Drain(1)
				}
				want := int32(0)
				if tc.arity > 0 {
					want = tc.cells[tc.arity-1]
				}
				if len(returns) != 1 || !returns[0].Explicit || returns[0].Value != want || returns[0].Mode != ModeDeferred {
					t.Fatalf("return did not use logical top: %+v, want %d", returns, want)
				}
				if len(phases) != 2 || phases[0] != "start" || phases[1] != "finish" {
					t.Fatalf("unexpected lifecycle: %v", phases)
				}
			})
		}
	}
}

func TestLegacyDeferredNameStartPreservesCells(t *testing.T) {
	for _, wake := range []bool{false, true} {
		vm := newTestVM(synthProg([]uint32{0x10065000}, nil, 0, nil))
		stale := [32]int32{71, 72, 73, 74, 75}
		vm.Threads[0].Stack = stale
		b := NewCallbackBridge(vm)
		if wake {
			b.DeferredWake("A", nil, nil)
		} else {
			b.Deferred("A", nil, nil)
		}
		if vm.Threads[0].SP != 0 || vm.Threads[0].Stack != stale {
			t.Fatalf("legacy name-form start rewrote stale window: %+v", vm.Threads[0])
		}
	}
}

func TestDeferredArgsFailureAndWakeBarrier(t *testing.T) {
	for _, failure := range []string{"missing name", "full pool", "nil VM", "nil bridge"} {
		for _, wake := range []bool{false, true} {
			vm := newTestVM(synthProg([]uint32{0x10065000}, nil, 0, nil))
			b := NewCallbackBridge(vm)
			name := "A"
			switch failure {
			case "missing name":
				name = "Missing"
			case "full pool":
				for range vm.Threads {
					vm.Start(0, nil)
				}
			case "nil VM":
				b = NewCallbackBridge(nil)
			case "nil bridge":
				b = nil
			}
			var returns []CallbackReturn
			receiver := func(r CallbackReturn) { returns = append(returns, r) }
			var result CallbackResult
			if wake {
				result = b.DeferredWakeArgs(name, 0, [4]int32{}, receiver)
			} else {
				result = b.DeferredArgs(name, 0, [4]int32{}, receiver)
			}
			if result.Started || result.Wake || result.Completed || result.Thread != -1 || vm.DrainCalls != 0 {
				t.Fatalf("%s/wake=%v: failed start drained VM: %+v", failure, wake, result)
			}
			if len(returns) != 1 || returns[0].Explicit || returns[0].Value != 0 || returns[0].Thread != -1 {
				t.Fatalf("%s/wake=%v: failure delivery = %+v", failure, wake, returns)
			}
		}
	}

	vm := newTestVM(synthProg([]uint32{
		0x10021001, 91, 0x10023004, 0, // earlier deferred callback publishes marker
		0x10021001, 1000, 0x10013000, 0x10065000,
		0x10065000, // argument callback returns
	}, nil, 1, []int{0, 8}))
	b := NewCallbackBridge(vm)
	b.Deferred("A", nil, nil)
	result := b.DeferredWakeArgs("B", 0, [4]int32{}, nil)
	if !result.Completed || vm.statics[0] != 91 || vm.Threads[0].Status != ThreadSleeping {
		t.Fatalf("argument wake did not drain earlier slots: result=%+v thread=%+v static=%d", result, vm.Threads[0], vm.statics[0])
	}
	sleep := vm.Threads[0].Sleep
	b.DeferredWakeArgs("B", 0, [4]int32{}, nil)
	if vm.Threads[0].Sleep != sleep {
		t.Fatal("wake barrier advanced sleeping thread's time")
	}
}
