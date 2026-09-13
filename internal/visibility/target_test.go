package visibility

import (
	"reflect"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// Authored asymmetric and odd raw bounds prevent recovering the origin from
// half of a span; the callback locks the cumulative order [06 §3.1][03 §3.2].
func TestHullBoundsAndCumulativeProbes(t *testing.T) {
	base := Target{Owner: 1, X: 100, Y: 200, Z: 300, Status: SonarBit}
	target := TargetFromBounds(base, [3]int32{-7, -3, -11}, [3]int32{12, 14, 8})
	want := [][3]numeric.Fixed{{93, 214, 289}, {112, 214, 289}, {112, 197, 308}, {93, 197, 308}}
	for accepted := 0; accepted <= len(want); accepted++ {
		var visited [][3]numeric.Fixed
		got := target.IsVisible(0, 0, func(x, y, z numeric.Fixed) bool {
			visited = append(visited, [3]numeric.Fixed{x, y, z})
			return len(visited)-1 == accepted
		})
		n := accepted + 1
		if n > len(want) {
			n = len(want)
		}
		if got != (accepted < len(want)) || !reflect.DeepEqual(visited, want[:n]) {
			t.Fatalf("accepted probe %d: visible=%v walk=%v, want %v", accepted, got, visited, want[:n])
		}
	}
}

// Definition span subtraction happens at signed 32-bit width [06 §3.1].
func TestHullSpanRetainsSignedNarrowing(t *testing.T) {
	target := TargetFromBounds(Target{}, [3]int32{-2147483648, 0, 0}, [3]int32{2147483647, 0, 0})
	if target.X != -2147483648 || target.XExtent != -1 {
		t.Fatalf("origin/span=%d/%d, want signed bounds and wrapped span", target.X, target.XExtent)
	}
}
