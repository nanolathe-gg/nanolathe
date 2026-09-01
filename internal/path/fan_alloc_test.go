package path

import "testing"

// BenchmarkNeighborsForDir locks the expansion fan at zero allocations. It is
// called once per expanded node, so two slices per call dominated a search
// [04 §7.1].
func BenchmarkNeighborsForDir(b *testing.B) {
	cur := Cell{X: 64, Z: 64}
	b.ReportAllocs()
	b.ResetTimer()
	var sink int32
	for i := 0; i < b.N; i++ {
		fan := NeighborsForDir(cur, uint8(i&7), i%16 == 0)
		for j := 0; j < fan.Len; j++ {
			sink += fan.Cells[j].X
		}
	}
	_ = sink
}
