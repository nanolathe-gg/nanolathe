package cob

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

func p28RetailARMLABVM(t *testing.T) *VM {
	t.Helper()
	root := testsupport.RetailRoot(t)
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Fatalf("mount retail assets: %v", err)
	}
	data, err := fs.ReadFileLimit("scripts/armlab.cob", 4<<20)
	if err != nil {
		t.Fatalf("read ARMLAB COB: %v", err)
	}
	prog, err := Load(data)
	if err != nil {
		t.Fatalf("load ARMLAB COB: %v", err)
	}
	return NewVM(prog)
}

func p28BindFactoryPorts(vm *VM, denyCloseWrites int, events *[]string) {
	stance, busy, yard, bugger := true, false, true, false
	bind := func(port Port, state *bool, deny *int, label string) {
		vm.BindPortBinding(port, PortBinding{
			Read: func([4]int32) int32 {
				if *state {
					return 1
				}
				return 0
			},
			Write: func(value int32) {
				want := value&1 != 0
				if !want && deny != nil && *deny > 0 {
					*deny--
					*events = append(*events, label+":denied")
					return
				}
				*state = want
				*events = append(*events, label+":"+map[bool]string{false: "0", true: "1"}[want])
			},
		})
	}
	bind(Port(5), &stance, nil, "stance")
	bind(Port(6), &busy, nil, "busy")
	bind(Port(18), &yard, &denyCloseWrites, "yard")
	bind(Port(19), &bugger, nil, "bugger")
	vm.BindPortBinding(Port(20), PortBinding{Read: func([4]int32) int32 { return 0 }})
	vm.BindPortBinding(Port(1), PortBinding{Read: func([4]int32) int32 { return 0 }})
}

func TestP28ARMLABDeactivateOwnsAuthoredCloseDelay(t *testing.T) {
	vm := p28RetailARMLABVM(t)
	var events []string
	tick := 0
	p28BindFactoryPorts(vm, 0, &events)
	if !vm.StartByName("Deactivate", nil) {
		t.Fatal("start ARMLAB Deactivate")
	}
	vm.Drain(1)
	if vm.Threads[vm.LastStartedThread()].Sleep != 150 {
		t.Fatalf("Deactivate sleep = %d ticks, want authored 150", vm.Threads[vm.LastStartedThread()].Sleep)
	}
	if len(events) != 0 {
		t.Fatalf("door work began before authored idle sleep: %v", events)
	}
	// The VM stores the authored 150-tick timer; its normal sleep guard wakes
	// on the following drain boundary [04 §4.6].
	for tick = 1; tick <= 151; tick++ {
		vm.Drain(1)
	}
	if len(events) == 0 || events[0] != "stance:0" {
		t.Fatalf("first post-delay factory write = %v, want stance:0", events)
	}
	foundYardClose := false
	for _, event := range events {
		if event == "yard:0" {
			foundYardClose = true
			break
		}
	}
	if !foundYardClose {
		t.Fatalf("ARMLAB Stop did not reach CloseYard after delay: %v", events)
	}
}

func TestP28ARMLABDeniedCloseUsesAuthoredRetrySleep(t *testing.T) {
	vm := p28RetailARMLABVM(t)
	var events []string
	tick := 0
	p28BindFactoryPorts(vm, 1, &events)
	if !vm.StartByName("Deactivate", nil) {
		t.Fatal("start ARMLAB Deactivate")
	}
	for tick = 0; tick <= 151; tick++ {
		vm.Drain(1)
	}
	if len(events) < 3 || events[0] != "stance:0" || events[1] != "yard:denied" || events[2] != "bugger:1" {
		t.Fatalf("denied close prefix = %v", events)
	}
	found45 := false
	for i := range vm.Threads {
		if vm.Threads[i].Status == ThreadSleeping && vm.Threads[i].Sleep == 45 {
			found45 = true
			break
		}
	}
	if !found45 {
		t.Fatalf("denied CloseYard did not enter authored 45-tick sleep: events=%v", events)
	}
	for ; tick <= 210; tick++ {
		vm.Drain(1)
	}
	wantSuffix := []string{"yard:0", "bugger:0"}
	if len(events) < 5 || events[len(events)-2] != wantSuffix[0] || events[len(events)-1] != wantSuffix[1] {
		t.Fatalf("CloseYard retry did not accept then clear BUGGER_OFF: %v", events)
	}
}
