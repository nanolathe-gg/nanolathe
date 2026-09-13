package main

import (
	"image"
	"math"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/clock"
	"github.com/nanolathe-gg/nanolathe/internal/hud"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
)

type scriptedMillisSource struct {
	samples []uint32
	index   int
}

func (s *scriptedMillisSource) Millis32() uint32 {
	if s == nil || len(s.samples) == 0 {
		return 0
	}
	if s.index >= len(s.samples) {
		return s.samples[len(s.samples)-1]
	}
	v := s.samples[s.index]
	s.index++
	return v
}

func replayBattleFrame(c *BattleController, cl *client.Client, f BattleInputFrame) *image.RGBA {
	c.Step(f, cl)
	return cl.ComposeFrame()
}

// TestStrictSkirmish_ProductionInputReplayG10A proves that the first production
// replay uses the same logical input seam as Ebitengine: select the commander,
// then issue a contextual move from an empty world click. No order or session
// state is written by the test itself [07 §8][07 §9].
//
// The controller's host clock is pinned (newReplayController) so the replay is
// a function of its input frames alone. On the default monotonic source it
// would sample the whole test-process uptime on its first step and spend the
// clamped catch-up burst of [01 §4.2] before the click is ever enqueued, which
// makes the result depend on how long the tests that ran earlier took.
func TestStrictSkirmish_ProductionInputReplayG10A(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(40, 40))
	b.battleState().Input.Latch = input.LatchNormal
	commander := placeUnit(b, "armcons", numeric.Fixed(200*65536), numeric.Fixed(120*65536))
	applyPendingBattleCommands(b)
	cl, err := client.New(client.Options{
		Buffer: b.sess.Snapshot,
		Width:  640,
		Height: 480,
	})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	cl.SetTerrain(b.sess.World)
	cl.SetCamera(b.cam)
	c := newReplayController(b)

	sx, sy := screenPos(b.cam, commander)
	frame := BattleInputFrame{MouseX: sx, MouseY: sy, Buttons: BattleMouseButtons{Left: true}}
	frame.PressedButtons[input.MouseButtonLeft] = true
	if got := replayBattleFrame(c, cl, frame); got == nil || got.Bounds() != image.Rect(0, 0, 640, 480) {
		t.Fatalf("select press did not render logical 640x480 framebuffer")
	}
	replayBattleFrame(c, cl, frame)
	frame.PressedButtons[input.MouseButtonLeft] = false
	frame.Buttons.Left = false
	frame.ReleasedButtons[input.MouseButtonLeft] = true
	if got := replayBattleFrame(c, cl, frame); got == nil {
		t.Fatal("select release did not render a frame")
	}
	applyPendingBattleCommands(b)
	if commander.Flags&hud.SelectionFlag == 0 {
		t.Fatalf("production replay did not select commander at screen=%d,%d", sx, sy)
	}

	// Empty terrain click is the retail contextual move path. Keep it inside
	// the logical viewport and away from the commander.
	moveX, moveY := int32(300), int32(300)
	frame.MouseX, frame.MouseY = moveX, moveY
	frame.ReleasedButtons[input.MouseButtonLeft] = false
	frame.Buttons.Left = true
	frame.PressedButtons[input.MouseButtonLeft] = true
	replayBattleFrame(c, cl, frame)
	frame.PressedButtons[input.MouseButtonLeft] = false
	frame.Buttons.Left = false
	frame.ReleasedButtons[input.MouseButtonLeft] = true
	replayBattleFrame(c, cl, frame)
	pending := b.sess.PendingHumanCommands()
	if len(pending) == 0 || pending[len(pending)-1].Kind != session.HumanOrder || pending[len(pending)-1].Order.Code != 1 {
		t.Fatalf("production replay did not enqueue contextual move: pending=%+v", pending)
	}
}

func TestBattleControllerReleasesKeysAndHandlesZeroElapsed(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(20, 20))
	b.battleState().Input.Latch = input.LatchNormal
	cl, err := client.New(client.Options{Buffer: b.sess.Snapshot, Width: 640, Height: 480})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	// Pinned host clock: "zero elapsed" has to mean the frames carry no time,
	// not that the process happened to be young when the test ran.
	c := newReplayController(b)
	beforeTick := b.sess.Clock.GlobalTick
	c.Step(BattleInputFrame{
		HeldKeys:  []input.Key{input.KeyW},
		Modifiers: BattleModifiers{Shift: true},
	}, cl)
	c.Step(BattleInputFrame{}, cl)
	if b.sess.Clock.GlobalTick != beforeTick {
		t.Fatalf("zero elapsed replay advanced GlobalTick from %d to %d", beforeTick, b.sess.Clock.GlobalTick)
	}
	c.Step(BattleInputFrame{}, cl)
}

func TestBattleControllerInvalidElapsedDoesNotRewindClockAnchor(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(20, 20))
	b.battleState().Input.Latch = input.LatchNormal
	b.sess.State = session.StateBattle
	c := NewBattleController(b, &scriptedMillisSource{samples: []uint32{1000, 1000, 1000, 1000}})
	first := BattleInputFrame{Elapsed: 1}
	c.Step(first, nil)
	anchor := b.sess.Clock.ScaledAnchor
	if anchor <= 0 {
		t.Fatalf("valid elapsed frame did not advance scaled anchor: %d", anchor)
	}
	for _, elapsed := range []float64{math.NaN(), math.Inf(1), -1} {
		c.Step(BattleInputFrame{Elapsed: elapsed}, nil)
		if got := b.sess.Clock.ScaledAnchor; got != anchor {
			t.Fatalf("invalid elapsed %v rewound scaled anchor from %d to %d", elapsed, anchor, got)
		}
	}
}

func TestBattleControllerUsesMillisSourceNotElapsed(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(20, 20))
	b.sess.State = session.StateBattle
	source := &scriptedMillisSource{samples: []uint32{0, 1000}}
	c := NewBattleController(b, source)
	c.Step(BattleInputFrame{Elapsed: 1000}, nil)
	if got := b.sess.Clock.ScaledAnchor; got != clock.ScaledNow(0) {
		t.Fatalf("first source sample anchor = %d, want %d", got, clock.ScaledNow(0))
	}
	c.Step(BattleInputFrame{Elapsed: 0}, nil)
	if got := b.sess.Clock.ScaledAnchor; got != clock.ScaledNow(1000) {
		t.Fatalf("second source sample anchor = %d, want %d", got, clock.ScaledNow(1000))
	}
}
