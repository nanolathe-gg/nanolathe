package cob

import "testing"

func TestCallbackBridgeAimFallbackAndQuerySeeds(t *testing.T) {
	prog, err := Load(makeCOB([]uint32{0x10065000}, []string{"QueryPrimary", "QueryNanoPiece"}, []uint32{0, 0}, []string{"base"}))
	if err != nil {
		t.Fatal(err)
	}
	bridge := NewCallbackBridge(NewVM(prog))
	aim := bridge.AimPiece(WeaponPrimary)
	if !aim.Started || !aim.Completed || aim.QueryValue() != 0 {
		t.Fatalf("AimPiece = %#v, want QueryPrimary seed 0", aim)
	}
	nano := bridge.QueryNanoPiece()
	if !nano.Started || !nano.Completed || nano.QueryValue() != 0 {
		t.Fatalf("QueryNanoPiece = %#v, want seed 0", nano)
	}
}

func TestCallbackBridgeDeferredReceiverAndModeIOnce(t *testing.T) {
	prog, err := Load(makeCOB([]uint32{0x10065000}, []string{"Create", "AimPrimary"}, []uint32{0, 0}, []string{"base"}))
	if err != nil {
		t.Fatal(err)
	}
	bridge := NewCallbackBridge(NewVM(prog))
	create := bridge.Create()
	if !create.Started || !bridge.CreateInvoked() {
		t.Fatalf("Create = %#v", create)
	}
	drains := bridge.VM.DrainCalls
	if second := bridge.Create(); second.Started || bridge.VM.DrainCalls != drains {
		t.Fatalf("second Create = %#v drains=%d want no-op", second, bridge.VM.DrainCalls)
	}
	var got CallbackReturn
	started := bridge.Aim(WeaponPrimary, 100, 200, func(ret CallbackReturn) { got = ret })
	if !started.Started || got.Explicit {
		t.Fatalf("Aim start = %#v receiver=%#v", started, got)
	}
	bridge.Drain(1)
	if !got.Explicit || got.Value != 200 || got.Name != "AimPrimary" {
		t.Fatalf("receiver = %#v, want explicit return 200", got)
	}
}

func TestCallbackBridgeFullPoolDeliversAimZero(t *testing.T) {
	prog, err := Load(makeCOB([]uint32{0x10013000, 0x10065000}, []string{"AimPrimary"}, []uint32{0}, []string{"base"}))
	if err != nil {
		t.Fatal(err)
	}
	bridge := NewCallbackBridge(NewVM(prog))
	for i := 0; i < 8; i++ {
		if !bridge.Aim(WeaponPrimary, 1, 2, nil).Started {
			t.Fatalf("Aim %d failed before pool filled", i)
		}
	}
	var got CallbackReturn
	if result := bridge.Aim(WeaponPrimary, 1, 2, func(ret CallbackReturn) { got = ret }); result.Started {
		t.Fatalf("pool-full Aim started: %#v", result)
	}
	if got.Explicit || got.Value != 0 {
		t.Fatalf("pool-full receiver = %#v, want implicit zero", got)
	}
}

func TestCallbackBridgeQueryPreservesBlockedThreadAndPartialCells(t *testing.T) {
	prog, err := Load(makeCOB([]uint32{0x10013000, 0x10065000}, []string{"QueryNanoPiece"}, []uint32{0}, []string{"base"}))
	if err != nil {
		t.Fatal(err)
	}
	bridge := NewCallbackBridge(NewVM(prog))
	result := bridge.QueryNanoPiece()
	if !result.Started || result.Completed || result.QueryValue() != 0 {
		t.Fatalf("blocked query = %#v, want started partial seed 0", result)
	}
	active := 0
	for i := range bridge.VM.Threads {
		if bridge.VM.IsThreadAlive(i) {
			active++
		}
	}
	if active != 1 {
		t.Fatalf("blocked query active threads=%d want 1", active)
	}
	bridge.Drain(1)
	for i := range bridge.VM.Threads {
		if bridge.VM.IsThreadAlive(i) {
			t.Fatalf("query thread %d still active after wake", i)
		}
	}
}

func TestPresentationSinkAdapterForwardsTypedSFX(t *testing.T) {
	// The adapter's concrete forwarding is covered by ports' SFX tests; this
	// assertion locks that it remains an SFXSink without adding lifetimes.
	var _ SFXSink = PresentationSinkAdapter{}
}
