package cob

import "testing"

func TestFiveLocalsAndPortReadUseElevenWindowWords(t *testing.T) {
	// Five allocated locals, one existing expression value, then the five
	// words consumed by the port read occupy words 0 through 10. This is the
	// shape used by stock scripts with a five-argument engine query.
	prog := synthProg([]uint32{
		0x10022000, 0x10022000, 0x10022000, 0x10022000, 0x10022000,
		0x10021001, 0x5151, // existing expression value
		0x10021001, 9, // port id
		0x10021001, 10, 0x10021001, 20, 0x10021001, 30, 0x10021001, 40,
		0x10043000, // five-argument engine read
		0x10065000,
	}, nil, 1, []int{0})
	vm := NewVM(prog)
	var got [4]int32
	vm.BindPortBinding(9, PortBinding{Read: func(args [4]int32) int32 {
		got = args
		return 0x6161
	}})
	if !vm.Start(0, nil) {
		t.Fatal("start fixture")
	}
	vm.Drain(0)
	if got := vm.Threads[0].Status; got != ThreadIdle {
		t.Fatalf("thread status after eleven-word port frame = %d, want idle", got)
	}
	if got[0] != 10 || got[1] != 20 || got[2] != 30 || got[3] != 40 {
		t.Fatalf("port arguments = %v, want [10 20 30 40]", got)
	}
	if value, ok := vm.ConsumeReturn(0); !ok || value != 0x6161 {
		t.Fatalf("return = (%#x,%v), want (%#x,true)", value, ok, int32(0x6161))
	}
	if got := vm.Threads[0].Stack[5]; got != 0x5151 {
		t.Fatalf("existing expression word = %#x, want %#x", got, int32(0x5151))
	}
}

func TestWindowWordThirtyOneIsAnAddressableLocal(t *testing.T) {
	prog := synthProg([]uint32{
		0x10024000, // make room below the full argument frame
		0x10021001, 0x1234,
		0x10023002, 31, // pop local 31
		0x10021002, 31, // push local 31
		0x10065000,
	}, nil, 1, []int{0})
	args := make([]int32, threadWindowWords)
	args[threadWindowWords-1] = -1
	vm := NewVM(prog)
	if !vm.Start(0, args) {
		t.Fatal("32-word start failed")
	}
	if got := vm.Threads[0].SP; got != threadWindowWords {
		t.Fatalf("start depth = %d, want %d", got, threadWindowWords)
	}
	vm.Drain(0)
	if value, ok := vm.ConsumeReturn(0); !ok || value != 0x1234 {
		t.Fatalf("local 31 return = (%#x,%v), want (%#x,true)", value, ok, int32(0x1234))
	}
}

func TestPhysicalWindowBoundaryStopsMalformedPushAndLocalAllocation(t *testing.T) {
	for _, opcode := range []uint32{0x10021001, 0x10022000} {
		t.Run("opcode", func(t *testing.T) {
			code := []uint32{opcode}
			if opcode == 0x10021001 {
				code = append(code, 1)
			}
			code = append(code, 0x10065000)
			vm := NewVM(synthProg(code, nil, 1, []int{0}))
			if !vm.Start(0, make([]int32, threadWindowWords)) {
				t.Fatal("32-word start failed")
			}
			vm.Drain(0)
			if got := vm.Threads[0].Status; got != ThreadIdle {
				t.Fatalf("status after out-of-window opcode = %d, want idle", got)
			}
			if _, ok := vm.ConsumeReturn(0); ok {
				t.Fatal("out-of-window opcode reached return")
			}
		})
	}

	vm := NewVM(synthProg([]uint32{0x10065000}, nil, 1, []int{0}))
	if vm.Start(0, make([]int32, threadWindowWords+1)) {
		t.Fatal("33-word start succeeded past physical window")
	}
}
