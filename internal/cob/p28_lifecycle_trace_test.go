package cob

import "testing"

func TestP28QueueLifecycleHasRealBoundaries(t *testing.T) {
	var got []string
	q := &DeferredQueue{}
	q.SetLifecycleContext(41, 7)
	q.SetLifecycleSink(func(e LifecycleEvent) {
		if e.Tick != 41 || e.Source != 7 {
			t.Fatalf("bad lifecycle context: %+v", e)
		}
		got = append(got, e.Name+":"+e.Phase)
	})
	q.EnqueueDeferred(QueuedCallback{Script: "AimPrimary"})
	q.DrainNormal(func(QueuedCallback) bool { return true })
	want := []string{"AimPrimary:enqueue", "AimPrimary:dequeue"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

func TestP28QueueNilDispatcherStillRecordsDequeue(t *testing.T) {
	var got []string
	q := &DeferredQueue{}
	q.SetLifecycleSink(func(e LifecycleEvent) { got = append(got, e.Phase) })
	q.EnqueueDeferred(QueuedCallback{Script: "FirePrimary"})
	q.DrainNormal(nil)
	if len(got) != 2 || got[0] != "enqueue" || got[1] != "dequeue" {
		t.Fatalf("nil-dispatch lifecycle %v, want enqueue/dequeue", got)
	}
}

func TestP28BridgeLifecycleStartThenFinish(t *testing.T) {
	var got []string
	vm := NewVM(buildTraceProg(t))
	b := NewCallbackBridge(vm)
	b.SetLifecycleContext(9, 3)
	b.SetLifecycleSink(func(e LifecycleEvent) { got = append(got, e.Name+":"+e.Phase) })
	if result := b.Deferred("AimPrimary", nil, nil); !result.Started {
		t.Fatal("callback did not start")
	}
	b.Drain(1)
	start, finish := -1, -1
	for i, event := range got {
		if event == "AimPrimary:start" {
			start = i
		}
		if event == "AimPrimary:finish" {
			finish = i
		}
	}
	if start < 0 || finish < 0 || start >= finish {
		t.Fatalf("lifecycle %v lacks ordered start/finish", got)
	}
}

func TestP28QueueSinkDoesNotChangeDispatchOrder(t *testing.T) {
	run := func(sink LifecycleSink) []string {
		q := &DeferredQueue{}
		q.SetLifecycleSink(sink)
		q.EnqueueDeferred(QueuedCallback{Script: "SetDirection"})
		q.EnqueueDeferred(QueuedCallback{Script: "SetSpeed"})
		var got []string
		q.DrainNormal(func(cb QueuedCallback) bool { got = append(got, cb.Script); return true })
		return got
	}
	a := run(nil)
	b := run(func(LifecycleEvent) {})
	if len(a) != len(b) || a[0] != b[0] || a[1] != b[1] {
		t.Fatalf("sink changed dispatch order: %v vs %v", a, b)
	}
}

func TestP28BridgeFailedStartIsExplicit(t *testing.T) {
	var got []string
	b := NewCallbackBridge(NewVM(nil))
	b.SetLifecycleSink(func(e LifecycleEvent) { got = append(got, e.Phase) })
	if result := b.Deferred("Missing", nil, nil); result.Started {
		t.Fatal("missing callback unexpectedly started")
	}
	if len(got) != 1 || got[0] != "start-failed" {
		t.Fatalf("failed-start lifecycle %v", got)
	}
}

func TestP28BridgeCreateMissingStartIsExplicit(t *testing.T) {
	var got []string
	b := NewCallbackBridge(NewVM(nil))
	b.SetLifecycleSink(func(e LifecycleEvent) { got = append(got, e.Name+":"+e.Phase) })
	if result := b.Create(); result.Started {
		t.Fatal("missing Create unexpectedly started")
	}
	if len(got) != 1 || got[0] != "Create:start-failed" {
		t.Fatalf("failed Create lifecycle %v", got)
	}
}

func TestP28BridgeAbnormalFinishIsExplicit(t *testing.T) {
	var got []string
	vm := NewVM(buildTraceProg(t))
	b := NewCallbackBridge(vm)
	b.SetLifecycleSink(func(e LifecycleEvent) { got = append(got, e.Phase) })
	if result := b.Deferred("AimPrimary", nil, nil); !result.Started {
		t.Fatal("callback did not start")
	}
	vm.Signal(1)
	b.Drain(0)
	for _, phase := range got {
		if phase == "finish-abnormal" {
			return
		}
	}
	t.Fatalf("abnormal lifecycle %v", got)
}
