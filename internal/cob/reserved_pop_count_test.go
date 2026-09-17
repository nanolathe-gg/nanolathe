package cob

import "testing"

// TestReservedPopCountEdges locks the reserved pop-count opcode's count
// handling [R-COB-04 §6]. Retail decrements the operand and takes its sign, so
// a count of zero — and every negative count — pops nothing and advances past
// the three words. Only a count above the four-word temporary is malformed, and
// there Nanolathe keeps its sanctioned divergence: it stops the thread instead
// of reproducing retail's write past the temporary.
//
// The zero-count rows matter because a guard written as "count exceeds the
// window" reads a zero count on an empty window as an overflow, which would
// kill a thread retail lets run on.
func TestReservedPopCountEdges(t *testing.T) {
	for _, tc := range []struct {
		name string
		// pushes are the values on the window before the opcode.
		pushes []int32
		count  uint32
		// killed is whether the thread stops; when it runs on, want is the
		// value the following return delivers — the window's remaining top, or
		// zero once the window is empty.
		killed bool
		want   int32
	}{
		{name: "zero count on an empty window advances", count: 0, want: 0},
		{name: "zero count on a non-empty window pops nothing", pushes: []int32{0x22}, count: 0, want: 0x22},
		{name: "negative count pops nothing", pushes: []int32{0x33}, count: 0xffffffff, want: 0x33},
		{name: "one below the bound pops", pushes: []int32{0x44, 0x55, 0x56, 0x57}, count: 3, want: 0x44},
		{name: "the bound pops the whole window", pushes: []int32{0x66, 0x67, 0x68, 0x69}, count: 4, want: 0},
		{name: "above the four-word bound stops the thread", pushes: []int32{1, 2, 3, 4, 5}, count: 5, killed: true},
		{name: "beyond the logical window stops the thread", count: 1, killed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code := []uint32{}
			for _, v := range tc.pushes {
				code = append(code, 0x10021001, uint32(v))
			}
			code = append(code,
				0x10063000, 0, tc.count, // reserved pop-N: script id ignored, then the count
				0x10065000, // return — delivers the window's remaining top
			)
			vm := NewVM(synthProg(code, nil, 1, []int{0}))
			if !vm.Start(0, nil) {
				t.Fatal("start fixture")
			}
			vm.Drain(0)

			value, ok := vm.ConsumeReturn(0)
			if tc.killed {
				if ok {
					t.Fatalf("malformed count %d returned %#x; the thread should have stopped [R-COB-04 §6]", int32(tc.count), value)
				}
				return
			}
			if !ok {
				t.Fatalf("count %d stopped the thread; retail advances [R-COB-04 §6]", int32(tc.count))
			}
			if value != tc.want {
				t.Fatalf("count %d left %#x on top, want %#x [R-COB-04 §6]", int32(tc.count), value, tc.want)
			}
		})
	}
}
