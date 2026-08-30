package cob

import "testing"

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
