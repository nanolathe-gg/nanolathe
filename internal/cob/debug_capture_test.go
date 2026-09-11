package cob

import (
	"reflect"
	"testing"
)

func TestDebugCallbackLivenessAfterDirectDrainAndSlotReuse(t *testing.T) {
	v := NewVM(buildTraceProg(t))
	b := NewCallbackBridge(v)
	started := b.Deferred("Sleeper0", nil, nil)
	if !started.Started {
		t.Fatal("callback did not start")
	}
	i := started.Thread
	v.Drain(1)
	live := b.DebugSnapshot().Pending[i]
	if !live.Recorded || !live.Active || v.Threads[i].Status != ThreadSleeping {
		t.Fatalf("sleeping callback was not reported live: %+v", live)
	}
	v.Drain(1) // Session uses a direct VM drain, without bridge collection.
	if !b.lifecyclePending[i].active || v.ThreadAliveAs(i, live.Identity) {
		t.Fatal("fixture did not retain a completed callback record")
	}
	before := v.DebugSnapshot()
	pending := b.lifecyclePending
	got := b.DebugSnapshot().Pending[i]
	if !got.Recorded || got.Active || got.Identity != live.Identity {
		t.Fatalf("completed callback reported pending: %+v", got)
	}
	if !reflect.DeepEqual(before, v.DebugSnapshot()) || pending != b.lifecyclePending {
		t.Fatal("capture changed VM or bridge bookkeeping")
	}
	if !v.StartByName("Sleeper0", nil) || v.LastStartedThread() != i {
		t.Fatal("fixture did not reuse the callback's slot")
	}
	if b.DebugSnapshot().Pending[i].Active {
		t.Fatal("replacement execution inherited old callback liveness")
	}
	if !live.Active {
		t.Fatal("detached earlier snapshot changed")
	}
}

func TestDebugCaptureDetachedSleepingScript(t *testing.T) {
	v := NewVM(&Program{Statics: 1, Pieces: []string{"base"}, Scripts: map[string]int{"Sleep": 0}, Code: []uint32{1}})
	v.statics[0] = 29
	v.Threads[0] = Thread{Status: ThreadSleeping, Sleep: 33, SP: 1}
	v.Threads[0].Stack[0] = 41
	v.anims[0].axes[0].moveTarget = 19
	d := v.DebugSnapshot()
	v.statics[0] = 99
	v.Threads[0].Stack[0] = 88
	v.anims[0].axes[0].moveTarget = 77
	v.prog.Pieces[0] = "changed"
	if d.Statics[0] != 29 || d.Threads[0].Stack[0] != 41 || d.Animations[0][0].MoveTarget != 19 || d.ProgramPieces[0] != "base" {
		t.Fatal("snapshot aliases live script")
	}
}
