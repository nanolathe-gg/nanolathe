package cob

// Prevention tests for the C16 aim-ready handshake [GAP T15] [04 §5.3]
// [06 §3.3]. They lock the negative half of the contract — the half a future
// "helpful" fallback is most likely to invert: a missing Aim* script, an
// exhausted thread pool, an authored zero return, and signal/abnormal
// termination each leave the weapon unable to fire; only a nonzero delivered
// cell grants aim-ready, and only until the producer clears the aim state
// before the next start. See the C16 block contract in ports.go.
//
// Fixtures are authored byte-for-byte by makeCOB; no retail bytes are used.

import "testing"

// aimReceiver is the production completion-receiver wiring: the delivered
// cell is forwarded to the slot's CompleteAim [04 §5.3] [06 §3.3]. It records
// every delivery so tests can assert what the adapter actually delivered.
type aimReceiver struct {
	slot  *AimSlot
	calls []CallbackReturn
}

func (r *aimReceiver) receive(cr CallbackReturn) {
	r.calls = append(r.calls, cr)
	r.slot.CompleteAim(cr.Value)
}

func (r *aimReceiver) count() int { return len(r.calls) }

// loadAimProg authors a COB whose only script is AimPrimary returning ret
// (push constant ret; return) [04 §4.3].
func loadAimProg(t *testing.T, ret int32) *Program {
	t.Helper()
	code := []uint32{
		0x10021001, uint32(ret), // push constant [04 §4.3] F
		0x10065000, // return [04 §4.3]
	}
	prog, err := Load(makeCOB(code, []string{"AimPrimary"}, []uint32{0}, []string{"base"}))
	if err != nil {
		t.Fatalf("Load aim fixture: %v", err)
	}
	return prog
}

func TestAimReadyMissingScriptNeverGrants(t *testing.T) {
	// A unit whose COB has no Aim* entry: the deferred starter fails, and the
	// receiver-bearing adapter delivers 0 through the same receiver [04 §5.3]
	// [06 §3.3]. The weapon must stay unable to fire.
	data := makeCOB([]uint32{0x10065000}, []string{"Create"}, []uint32{0}, []string{"base"}) // Create: return [04 §4.3]
	prog, err := Load(data)
	if err != nil {
		t.Fatalf("Load fixture: %v", err)
	}
	if _, ok := prog.Scripts["AimPrimary"]; ok {
		t.Fatalf("fixture must not author AimPrimary")
	}
	slot := &AimSlot{}
	slot.StartAim() // the engine ORs the issue bit right after issuing [06 §3.3]
	recv := &aimReceiver{slot: slot}
	res := NewCallbackBridge(NewVM(prog)).Aim(WeaponPrimary, 0x1234, 0x0567, recv.receive)
	if res.Started {
		t.Fatalf("Aim start must fail for a missing script [04 §4.3]")
	}
	if recv.count() != 1 {
		t.Fatalf("missing script must deliver exactly once, got %d deliveries", recv.count())
	}
	if got := recv.calls[0]; got.Value != AimDeliveryZero || got.Explicit {
		t.Fatalf("missing script must deliver 0 non-explicit, got %+v [04 §5.3] [06 §3.3]", got)
	}
	if slot.CanFire() {
		t.Fatalf("missing Aim* script must NEVER set aim-ready [04 §5.3] [06 §3.3]")
	}
	if !slot.IssueBit {
		t.Fatalf("issue bit should still be armed [06 §3.3]")
	}

	// A nil VM must not set an Aim-ready state either [06 §3.3].
	slot2 := &AimSlot{}
	recv2 := &aimReceiver{slot: slot2}
	res2 := NewCallbackBridge(nil).Aim(WeaponPrimary, 1, 2, recv2.receive)
	if res2.Started {
		t.Fatalf("nil VM must not start a callback")
	}
	if recv2.count() != 1 || recv2.calls[0].Value != AimDeliveryZero {
		t.Fatalf("nil VM must deliver 0 through the receiver, got %+v [06 §3.3]", recv2.calls)
	}
	if slot2.CanFire() {
		t.Fatalf("nil VM must NEVER set aim-ready [06 §3.3]")
	}
}

func TestAimReadyPoolExhaustionNeverGrants(t *testing.T) {
	// AimPrimary exists and would grant if it ran, but all eight thread slots
	// are occupied: the start fails and delivers 0 [04 §5.3] [04 §4.3].
	const sleepMillis = 30000 // 900 ticks asleep [04 §4.6]
	code := []uint32{
		0x10021001, 1, // AimPrimary: push 1 [04 §4.3]
		0x10065000,              // return — would grant [04 §4.3]
		0x10021001, sleepMillis, // Sleeper: push duration [04 §4.3]
		0x10013000, // sleep [04 §4.6]
	}
	prog, err := Load(makeCOB(code, []string{"AimPrimary", "Sleeper"}, []uint32{0, 3}, []string{"base"}))
	if err != nil {
		t.Fatalf("Load fixture: %v", err)
	}
	bridge := NewCallbackBridge(NewVM(prog))
	for i := 0; i < 8; i++ {
		if r := bridge.Deferred("Sleeper", nil, nil); !r.Started {
			t.Fatalf("sleeper %d should occupy a thread [01 §6.1]", i)
		}
	}
	slot := &AimSlot{}
	slot.StartAim()
	recv := &aimReceiver{slot: slot}
	res := bridge.Aim(WeaponPrimary, 0, 0, recv.receive)
	if res.Started {
		t.Fatalf("ninth start must fail on a full pool [04 §4.3]")
	}
	if recv.count() != 1 || recv.calls[0].Value != AimDeliveryZero || recv.calls[0].Explicit {
		t.Fatalf("pool exhaustion must deliver 0 non-explicit, got %+v [04 §5.3] [06 §3.3]", recv.calls)
	}
	if slot.CanFire() {
		t.Fatalf("pool exhaustion must NEVER set aim-ready [04 §5.3] [06 §3.3]")
	}
}

func TestAimReadyAuthoredZeroReturnNeverGrants(t *testing.T) {
	// AimPrimary runs to completion and returns 0: the receiver is invoked
	// with the explicit zero and the weapon stays unable to fire [04 §5.3].
	slot := &AimSlot{}
	slot.StartAim()
	recv := &aimReceiver{slot: slot}
	bridge := NewCallbackBridge(NewVM(loadAimProg(t, 0)))
	if res := bridge.Aim(WeaponPrimary, 0x0111, 0x0222, recv.receive); !res.Started {
		t.Fatalf("AimPrimary should start")
	}
	bridge.Drain(1)
	if recv.count() != 1 {
		t.Fatalf("explicit return must deliver exactly once, got %d", recv.count())
	}
	if got := recv.calls[0]; !got.Explicit || got.Value != 0 {
		t.Fatalf("authored zero must deliver explicit 0, got %+v [04 §5.3]", got)
	}
	if slot.Ready || slot.CanFire() {
		t.Fatalf("authored zero return must NEVER set aim-ready [04 §5.3] [06 §3.3]")
	}
}

func TestAimReadyNonzeroGrantAndProducerReset(t *testing.T) {
	// A completed nonzero result grants aim-ready exactly once per handshake
	// cycle: the grant sticks through later zero deliveries, any further
	// nonzero stays granted, and only the producer's clear before the next
	// Aim* start unwinds it [04 §5.3] [06 §3.3].
	slot := &AimSlot{}
	slot.StartAim()
	recv := &aimReceiver{slot: slot}
	bridge := NewCallbackBridge(NewVM(loadAimProg(t, 1)))
	if res := bridge.Aim(WeaponPrimary, 0x0111, 0x0222, recv.receive); !res.Started {
		t.Fatalf("AimPrimary should start")
	}
	if slot.CanFire() {
		t.Fatalf("not ready before the completion [04 §5.3]")
	}
	bridge.Drain(1)
	if recv.count() != 1 || !recv.calls[0].Explicit || recv.calls[0].Value != 1 {
		t.Fatalf("nonzero return must deliver explicit 1, got %+v [04 §5.3]", recv.calls)
	}
	if !slot.Ready || !slot.CanFire() {
		t.Fatalf("nonzero return must grant aim-ready [04 §5.3] [06 §3.3]")
	}
	// A later zero delivery neither grants nor revokes [06 §3.3].
	if slot.CompleteAim(0) != true {
		t.Fatalf("zero delivery must not revoke the grant [06 §3.3]")
	}
	// Any nonzero delivery is a grant; repeats stay granted.
	if !slot.CompleteAim(-5) {
		t.Fatalf("negative nonzero must keep aim-ready granted [06 §3.3]")
	}
	// Producer reset: the next Aim* start is preceded by clearing the aim
	// state [04 §5.3]; a stale grant never survives a re-issue, and the new
	// cycle needs a fresh nonzero.
	slot.Ready = false
	slot.StartAim()
	if slot.CanFire() {
		t.Fatalf("re-issued start must begin not-ready [04 §5.3]")
	}
	if slot.CompleteAim(0) {
		t.Fatalf("new cycle delivered zero; it must not grant [04 §5.3]")
	}
}

func TestAimReadySignalTerminationNeverInvokesReceiver(t *testing.T) {
	// AimPrimary signals a mask intersecting its engine-seeded mask 1: the
	// thread dies by signal termination, which never invokes the receiver
	// [04 §5.3] [06 §3.3]. No delivery, no grant.
	code := []uint32{
		0x10021001, 1, // push 1 — signal mask [04 §4.3]
		0x10067000, // signal [04 §4.3]
	}
	prog, err := Load(makeCOB(code, []string{"AimPrimary"}, []uint32{0}, []string{"base"}))
	if err != nil {
		t.Fatalf("Load fixture: %v", err)
	}
	vm := NewVM(prog)
	slot := &AimSlot{}
	slot.StartAim()
	recv := &aimReceiver{slot: slot}
	bridge := NewCallbackBridge(vm)
	res := bridge.Aim(WeaponPrimary, 0, 0, recv.receive)
	if !res.Started {
		t.Fatalf("AimPrimary should start")
	}
	bridge.Drain(1)
	if recv.count() != 0 {
		t.Fatalf("signal termination must not invoke the receiver, got %+v [04 §5.3]", recv.calls)
	}
	if slot.CanFire() {
		t.Fatalf("signal termination must NEVER set aim-ready [04 §5.3] [06 §3.3]")
	}
	if vm.IsThreadAlive(res.Thread) {
		t.Fatalf("signalled thread should be dead [04 §4.3]")
	}
}

func TestAimReadyAbnormalTerminationNeverInvokesReceiver(t *testing.T) {
	// An undecodable opcode word kills the thread (abnormal termination):
	// no receiver invocation, no grant [04 §5.3] [04 §4.3] C11 kill path.
	code := []uint32{0x10000000} // masked key absent from the dispatched set [04 §4.3] C11
	prog, err := Load(makeCOB(code, []string{"AimPrimary"}, []uint32{0}, []string{"base"}))
	if err != nil {
		t.Fatalf("Load fixture: %v", err)
	}
	vm := NewVM(prog)
	slot := &AimSlot{}
	slot.StartAim()
	recv := &aimReceiver{slot: slot}
	bridge := NewCallbackBridge(vm)
	res := bridge.Aim(WeaponPrimary, 0, 0, recv.receive)
	if !res.Started {
		t.Fatalf("AimPrimary should start")
	}
	bridge.Drain(1)
	if recv.count() != 0 {
		t.Fatalf("abnormal termination must not invoke the receiver, got %+v [04 §5.3]", recv.calls)
	}
	if slot.CanFire() {
		t.Fatalf("abnormal termination must NEVER set aim-ready [04 §5.3] [06 §3.3]")
	}
	if vm.IsThreadAlive(res.Thread) {
		t.Fatalf("killed thread should be dead [04 §4.3]")
	}
}
