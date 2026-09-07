package cob

import "testing"

// These are same-axis programs deliberately: different-axis motion cannot
// prove the independent animation-lane contract [04 §4.6].
func TestAnimationLanesShareAxisWithoutCancelling(t *testing.T) {
	prog := synthProg([]uint32{
		0x10021001, 30, // spin acceleration
		0x10021001, 30, // spin speed
		0x10003000, 0, 0,
		0x10021001, 30, // move speed
		0x10021001, 1, // move target
		0x10001000, 0, 0,
		0x10065000,
	}, []string{"base"}, 0, []int{0})
	vm := newTestVM(prog)
	if !vm.Start(0, nil) {
		t.Fatal("start")
	}
	vm.Drain(1)
	if got := vm.Pieces[0].GetTrans(0).Raw(); got != 1 {
		t.Fatalf("translation = %d, want 1", got)
	}
	if got := vm.Pieces[0].GetAngle(0); got != 1 {
		t.Fatalf("same-axis spin was cancelled by move: angle = %d, want 1 [04 §4.6]", got)
	}
}

func TestCompletedMoveStillProcessesRotation(t *testing.T) {
	prog := synthProg([]uint32{
		0x10021001, 30, // move speed
		0x10021001, 0, // already at target
		0x10001000, 0, 0,
		0x10021001, 30, // turn speed
		0x10021001, 100,
		0x10002000, 0, 0,
		0x10065000,
	}, []string{"base"}, 0, []int{0})
	vm := newTestVM(prog)
	if !vm.Start(0, nil) {
		t.Fatal("start")
	}
	vm.Drain(1)
	if got := vm.Pieces[0].GetAngle(0); got != 1 {
		t.Fatalf("already-complete move skipped rotation: angle = %d, want 1 [04 §4.6]", got)
	}
}

func TestMoveNowKeepsSameAxisSpin(t *testing.T) {
	prog := synthProg([]uint32{
		0x10021001, 0,
		0x10021001, 30,
		0x10003000, 0, 0,
		0x10021001, 9,
		0x1000b000, 0, 0,
		0x10065000,
	}, []string{"base"}, 0, []int{0})
	vm := newTestVM(prog)
	if !vm.Start(0, nil) {
		t.Fatal("start")
	}
	vm.Drain(1)
	if got := vm.Pieces[0].GetTrans(0).Raw(); got != 9 {
		t.Fatalf("move-now translation = %d, want 9", got)
	}
	if got := vm.Pieces[0].GetAngle(0); got != 1 {
		t.Fatalf("move-now cancelled same-axis spin: angle = %d, want 1 [04 §4.6]", got)
	}
}

func TestMoveNowKeepsSameAxisTurn(t *testing.T) {
	prog := synthProg([]uint32{
		0x10021001, 30,
		0x10021001, 100,
		0x10002000, 0, 0,
		0x10021001, 9,
		0x1000b000, 0, 0,
		0x10065000,
	}, []string{"base"}, 0, []int{0})
	vm := newTestVM(prog)
	if !vm.Start(0, nil) {
		t.Fatal("start")
	}
	vm.Drain(1)
	if got := vm.Pieces[0].GetAngle(0); got != 1 {
		t.Fatalf("move-now cancelled same-axis turn: angle = %d, want 1 [04 §4.6]", got)
	}
}

func TestMoveNowKeepsSameAxisAcceleratingSpin(t *testing.T) {
	prog := synthProg([]uint32{
		0x10021001, 30, // acceleration: +1 per pass
		0x10021001, 300, // target speed: 10 per tick
		0x10003000, 0, 0,
		0x10021001, 9,
		0x1000b000, 0, 0,
		0x10065000,
	}, []string{"base"}, 0, []int{0})
	vm := newTestVM(prog)
	if !vm.Start(0, nil) {
		t.Fatal("start")
	}
	vm.Drain(1)
	axis := vm.anims[0].axes[0]
	if axis.spinSpeed != 1 || axis.spinAccel != 1 || vm.Pieces[0].GetAngle(0) != 1 {
		t.Fatalf("move-now cancelled accelerating spin: %+v angle=%d [04 §4.6]", axis, vm.Pieces[0].GetAngle(0))
	}
}

func TestHalfCircleTurnRetainsIssuedDirection(t *testing.T) {
	for _, tc := range []struct {
		name  string
		speed uint32
		want  uint16
	}{
		{name: "positive", speed: 30, want: 1},
		{name: "negative", speed: ^uint32(29), want: 0xffff},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prog := synthProg([]uint32{
				0x10021001, tc.speed,
				0x10021001, 0x8000,
				0x10002000, 0, 0,
				0x10065000,
			}, []string{"base"}, 0, []int{0})
			vm := newTestVM(prog)
			if !vm.Start(0, nil) {
				t.Fatal("start")
			}
			vm.Drain(1)
			if got := vm.Pieces[0].GetAngle(0); got != tc.want {
				t.Fatalf("half-circle turn angle = %#x, want %#x [04 §4.6]", got, tc.want)
			}
		})
	}
}

func TestReverseHalfCircleTurnUsesSignedTargetDelta(t *testing.T) {
	for _, tc := range []struct {
		name  string
		speed uint32
		want  uint16
	}{
		{name: "positive", speed: 30, want: 0x7fff},
		{name: "negative", speed: ^uint32(29), want: 0x8001},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prog := synthProg([]uint32{
				0x10021001, tc.speed,
				0x10021001, 0,
				0x10002000, 0, 0,
				0x10065000,
			}, []string{"base"}, 0, []int{0})
			vm := newTestVM(prog)
			vm.Pieces[0].SetAngle(0, 0x8000)
			if !vm.Start(0, nil) {
				t.Fatal("start")
			}
			vm.Drain(1)
			if got := vm.Pieces[0].GetAngle(0); got != tc.want {
				t.Fatalf("reverse half-circle turn angle = %#x, want %#x [04 §4.6]", got, tc.want)
			}
		})
	}
}

func TestSpinRampUsesStoredSignedAcceleration(t *testing.T) {
	prog := synthProg([]uint32{
		0x10021001, 30, // acceleration: +1 per tick
		0x10021001, 0xfffffed4, // speed: -300, target -10 per tick
		0x10003000, 0, 0,
		0x10065000,
	}, []string{"base"}, 0, []int{0})
	vm := newTestVM(prog)
	if !vm.Start(0, nil) {
		t.Fatal("start")
	}
	vm.Drain(1)
	if got := vm.anims[0].axes[0].spinSpeed; got != -10 {
		t.Fatalf("signed ramp speed = %d, want -10 [04 §4.6]", got)
	}
	if got := vm.Pieces[0].GetAngle(0); got != 0xfff6 {
		t.Fatalf("signed ramp angle = %#x, want 0xfff6 [04 §4.6]", got)
	}
}

func TestSpinRampClampsAfterCrossingTarget(t *testing.T) {
	vm := newTestVM(synthProg(nil, []string{"base"}, 0, nil))
	axis := &vm.anims[0].axes[0]
	axis.spinActive = true
	axis.spinSpeed = 9
	axis.spinTarget = 10
	axis.spinAccel = 2
	vm.dirty = true
	vm.interpolate(1)
	if axis.spinSpeed != 10 || axis.spinAccel != 0 {
		t.Fatalf("post-add positive clamp = speed %d accel %d, want 10/0 [04 §4.6]", axis.spinSpeed, axis.spinAccel)
	}
}

func TestSpinRampUsesStoredWordAndRunsAtEqualTarget(t *testing.T) {
	vm := newTestVM(synthProg(nil, []string{"base"}, 0, nil))
	axis := &vm.anims[0].axes[0]
	axis.spinActive = true
	axis.spinSpeed = 10
	axis.spinTarget = 10
	axis.spinAccel = 1
	vm.dirty = true
	vm.interpolate(2)
	if axis.spinSpeed != 10 || axis.spinAccel != 0 {
		t.Fatalf("equal-target ramp = speed %d accel %d, want 10/0 [04 §4.6]", axis.spinSpeed, axis.spinAccel)
	}

	axis.spinSpeed = 0x7fffffff
	axis.spinTarget = 0x7fffffff
	axis.spinAccel = 1
	vm.dirty = true
	vm.interpolate(1)
	if axis.spinSpeed != -0x80000000 || axis.spinAccel != 1 {
		t.Fatalf("ramp stored-word wrap = speed %d accel %d, want -2147483648/1 [04 §4.6]", axis.spinSpeed, axis.spinAccel)
	}
}

func TestPhysicalExplodeHidesMappedSourceOnly(t *testing.T) {
	prog := synthProg([]uint32{
		0x10021001, 0, // physical flags
		0x10071000, 0, // COB piece 0
		0x10065000,
	}, []string{"turret", "base"}, 0, []int{0})
	vm := newTestVM(prog)
	modelFlags := []uint8{0x07, 0x07}
	// COB turret maps to model piece 1; the binding map must remain in effect.
	vm.BindRenderFlagHandlers(func() []uint8 { return modelFlags }, func(piece int, mask uint8, set bool) bool {
		mapped := []int{1, 0}
		if piece < 0 || piece >= len(mapped) {
			return false
		}
		if set {
			modelFlags[mapped[piece]] |= mask
		} else {
			modelFlags[mapped[piece]] &^= mask
		}
		return true
	})
	if !vm.Start(0, nil) {
		t.Fatal("start")
	}
	vm.Drain(1)
	if modelFlags[1]&0x01 != 0 || modelFlags[0]&0x01 == 0 {
		t.Fatalf("physical explode flags = %#v, want only mapped source hidden [04 R-COB-04 §1]", modelFlags)
	}
}

func TestBitmapOnlyExplodeLeavesSourceShown(t *testing.T) {
	prog := synthProg([]uint32{
		0x10021001, 0x20,
		0x10071000, 0,
		0x10065000,
	}, []string{"base"}, 0, []int{0})
	vm := newTestVM(prog)
	vm.pieceFlags[0] = 0x07
	if !vm.Start(0, nil) {
		t.Fatal("start")
	}
	vm.Drain(1)
	if vm.pieceFlags[0]&0x01 == 0 {
		t.Fatal("bitmap-only explode hid its source [04 R-COB-04 §1]")
	}
}
