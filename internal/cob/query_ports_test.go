package cob

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

// The nine query ports 7..15 were unbound until WU-19-234 and answered zero
// through readPortDefault. These tests lock the argument convention and the
// unit conversions of [04 §4.4] and [04 R-COB-03 §2] at the adapter boundary,
// which is where a wrong slot index or a missing raw/whole conversion would be
// silent: a script reads one integer and cannot tell a wrong one from a right
// one.

func fixedXYZ(x, y, z int64) [3]numeric.Fixed {
	return [3]numeric.Fixed{numeric.FixedFromInt(x), numeric.FixedFromInt(y), numeric.FixedFromInt(z)}
}

// TestPiecePositionPortsPackAndRaw locks port 7's packing (X high half, Z low
// half) and port 8's RAW 16.16 Y — port 8 is deliberately not shifted, which is
// the one asymmetry between the pair [04 §4.4].
func TestPiecePositionPortsPackAndRaw(t *testing.T) {
	var asked int32 = -99
	pieceWorld := PieceWorldPoint(func(piece int32) [3]numeric.Fixed {
		asked = piece
		return fixedXYZ(10, 7, -20)
	})
	xz := PiecePositionXZPortFunc(pieceWorld)([]int32{7, 3, 0, 0, 0})
	if asked != 3 {
		t.Fatalf("port 7 read piece index %d, want args[1] = 3", asked)
	}
	gotX, gotZ := unpackXZ(xz)
	if int64(gotX) != numeric.FixedFromInt(10).Raw() || int64(gotZ) != numeric.FixedFromInt(-20).Raw() {
		t.Fatalf("port 7 packed %#x unpacks to (%d,%d), want (10,-20) in 16.16", uint32(xz), gotX>>16, gotZ>>16)
	}
	y := PiecePositionYPortFunc(pieceWorld)([]int32{8, 3, 0, 0, 0})
	if int64(y) != numeric.FixedFromInt(7).Raw() {
		t.Fatalf("port 8 = %d, want raw 16.16 %d (not the whole part)", y, numeric.FixedFromInt(7).Raw())
	}
}

// TestPiecePositionPortsBareGetReadsSlotZero locks the zero-fill convention:
// the one-argument read opcode passes the identifier alone, and an absent slot
// reads as the compiler's zero fill [fmt cob][04 §4.4].
func TestPiecePositionPortsBareGetReadsSlotZero(t *testing.T) {
	var asked int32 = -1
	pieceWorld := PieceWorldPoint(func(piece int32) [3]numeric.Fixed {
		asked = piece
		return fixedXYZ(1, 2, 3)
	})
	PiecePositionXZPortFunc(pieceWorld)([]int32{7})
	if asked != 0 {
		t.Fatalf("bare get read piece index %d, want the zero fill 0", asked)
	}
}

// TestUnitPortsAliveGateAndZeroIdentifier locks the two gates ports 9, 10 and
// 11 share: a zero identifier reads zero without consulting the pool, and a
// lookup that declines (the alive bit clear) reads zero [04 §4.4].
func TestUnitPortsAliveGateAndZeroIdentifier(t *testing.T) {
	calls := 0
	lookup := UnitPortLookup(func(id int32) ([3]numeric.Fixed, int32, bool) {
		calls++
		if id == 5 {
			return fixedXYZ(4, 6, 8), 0x28000, true
		}
		return [3]numeric.Fixed{}, 0, false
	})
	if got := UnitPositionXZPortFunc(lookup)([]int32{9, 0, 0, 0, 0}); got != 0 {
		t.Fatalf("port 9 with identifier zero = %#x, want 0", uint32(got))
	}
	if got := UnitPositionYPortFunc(lookup)([]int32{10, 7, 0, 0, 0}); got != 0 {
		t.Fatalf("port 10 on a declined lookup = %d, want 0", got)
	}
	if got := UnitHeightPortFunc(lookup)([]int32{11, 5, 0, 0, 0}); got != 0x28000 {
		t.Fatalf("port 11 = %#x, want the definition's 16.16 model height 0x28000 [04 R-MOV-03 §5]", uint32(got))
	}
	if got := UnitPositionYPortFunc(lookup)([]int32{10, 5, 0, 0, 0}); int64(got) != numeric.FixedFromInt(6).Raw() {
		t.Fatalf("port 10 = %d, want raw 16.16 %d", got, numeric.FixedFromInt(6).Raw())
	}
	if calls == 0 {
		t.Fatal("lookup was never consulted")
	}
}

// TestTrigPortsArgumentSlots locks which argument slot each of ports 12..15
// reads, and that 13 unpacks while 15 does not — the whole difference between
// the two distance ports [04 §4.4][04 R-COB-03 §2].
func TestTrigPortsArgumentSlots(t *testing.T) {
	packed := PackXZ(numeric.FixedFromInt(3), numeric.FixedFromInt(4))
	// Port 13 unpacks: hypot(3.0, 4.0) in 16.16 is 5.0.
	if got := DistancePortFunc()([]int32{13, packed, 0, 0, 0}); int64(got) != numeric.FixedFromInt(5).Raw() {
		t.Fatalf("port 13 = %d, want 16.16 five (%d)", got, numeric.FixedFromInt(5).Raw())
	}
	// Port 15 does NOT unpack: it takes the two raw arguments as signed 32-bit.
	if got := HypotPortFunc()([]int32{15, 3, 4, 0, 0}); got != 5 {
		t.Fatalf("port 15 = %d, want 5 from the raw arguments (no unpacking)", got)
	}
	// Port 14 takes the two independent arguments with no heading subtraction.
	if got := AtanPortFunc()([]int32{14, 1, 0, 0, 0}); got != 16384 {
		t.Fatalf("port 14 atan2(1,0) = %d, want a quarter turn 16384", got)
	}
	// Port 12 unpacks args[1] and subtracts the heading read at call time.
	rel := RelativeBearingPortFunc(func() uint16 { return 16384 })
	if got := rel([]int32{12, PackXZ(numeric.FixedFromInt(1), 0), 0, 0, 0}); got != 0 {
		t.Fatalf("port 12 bearing to +X with heading +X = %d, want 0", got)
	}
	if got := rel([]int32{12, PackXZ(0, numeric.FixedFromInt(1)), 0, 0, 0}); got != 49152 {
		t.Fatalf("port 12 bearing to +Z with heading +X = %d, want 49152", got)
	}
}

// TestHypotPortTruncatesTowardZero locks I3 at port 15: the shared
// float-to-integer conversion truncates, it does not round [04 §4.4][01 §8].
func TestHypotPortTruncatesTowardZero(t *testing.T) {
	// hypot(2,2) is 2.828…; truncation gives 2, rounding would give 3.
	if got := HypotPort(2, 2); got != 2 {
		t.Fatalf("HypotPort(2,2) = %d, want 2 (truncated toward zero)", got)
	}
	if got := HypotPort(-3, -4); got != 5 {
		t.Fatalf("HypotPort(-3,-4) = %d, want 5 (arguments are signed)", got)
	}
}

// TestQueryPortsReachableFromBytecode proves the adapters are reachable
// through BindPort and the engine-read opcodes, not only as pure functions.
func TestQueryPortsReachableFromBytecode(t *testing.T) {
	vm := NewVM(&Program{})
	vm.BindPort(Port(15), HypotPortFunc())
	fn, ok := vm.portFuncs[Port(15)]
	if !ok || fn == nil {
		t.Fatal("port 15 did not bind")
	}
	if got := fn([]int32{15, 6, 8, 0, 0}); got != 10 {
		t.Fatalf("bound port 15 = %d, want 10", got)
	}
	// An identifier with no handler still falls to the documented zero.
	if got := vm.readPortDefault(21, []int32{21}); got != 0 {
		t.Fatalf("out-of-range identifier = %d, want 0 [04 §4.4]", got)
	}
}

// The raw-argument port retains the low word before the script sees it [01 R-DET-01 §1][04 R-COB-03 §2].
func TestHypotPortWrapsBeforeReturningToScript(t *testing.T) {
	if got := HypotPortFunc()([]int32{15, -2147483648, 0, 0, 0}); got != -2147483648 {
		t.Fatalf("port low word = %d", got)
	}
	if got := HypotPort(2147483647, 2147483647); got != -1257966798 {
		t.Fatalf("diagonal low word = %d", got)
	}
}
