package main

import (
	"os"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
)

// fakeMillisSource is a deterministic stand-in for the host millisecond
// counter. Tests advance it explicitly so a "frame" costs a known amount of
// wall clock instead of whatever the test machine happened to take.
type fakeMillisSource struct{ ms uint32 }

func (s *fakeMillisSource) Millis32() uint32 { return s.ms }

// A held arrow key scrolls at scrollByte map pixels per thirtieth of a second,
// independent of the host frame rate, and never at the 128-pixel cap on an
// ordinary frame [07 §10 "Correction — the raw delta is thirtieths of a
// second"].
//
// Before the PT3-11 fix the battle screen fed the scroll pass a millisecond
// delta (`int32(delta*1000)`, floored at 16), so every frame produced
// 32*16 = 512, capped to 128 map pixels: eight times too fast at 60 Hz, and
// frame-rate dependent. This test fails against that code (it observes the
// camera pinned to the clamp maximum long before the second is up) and passes
// against the scaled 30-per-second delta.
func TestHeldArrowScrollsAtScaledRate(t *testing.T) {
	setting := int32((&battleSession{}).scrollSetting())
	if setting <= 0 || setting > 128 {
		t.Skipf("scroll setting %d outside the range this contract can assert", setting)
	}

	cat := testCatalogON05()
	// A map wide enough that one second of scrolling never reaches the clamp:
	// the clamp maximum is MapW-ViewW = 4800-640.
	terrain := testWorldON05(300, 300)
	b := newTestBattle(cat, terrain)
	millis := &fakeMillisSource{}
	b.millisSource = millis

	buf := &frame.Buffer{}
	cl, _ := client.New(client.Options{Buffer: buf, Width: 640, Height: 480, Step: func(delta float64) {}})
	cl.SetCamera(b.cam)
	cl.Input().Mouse.SetPosition(320, 240) // away from every edge band [07 §10]
	cl.Input().Kbd.SetKey(input.KeyRight, true)

	const frames = 50
	const msPerFrame = 20 // 50 frames == exactly 1000 ms == 30 scaled units
	// One priming frame seeds the scroll clock's anchor, exactly as retail's
	// budget step has already been running when the battle mode takes over.
	b.viewerStep(0, cl)
	b.cam.X, b.cam.Z = 1000, 1000

	prevX := b.cam.X
	maxStep := int32(0)
	moved := 0
	for i := 0; i < frames; i++ {
		millis.ms += msPerFrame
		b.viewerStep(float64(msPerFrame)/1000, cl)
		if step := b.cam.X - prevX; step != 0 {
			moved++
			if step > maxStep {
				maxStep = step
			}
		}
		prevX = b.cam.X
	}

	// One second of held arrow is scrollByte * 30 map pixels, whatever the
	// frame rate: the scaled clock advances exactly 30 units per second and
	// each unit is worth one setting byte of pixels.
	if got, want := b.cam.X-1000, setting*30; got != want {
		t.Fatalf("held arrow moved %d map pixels in one second, want %d (setting %d)", got, want, setting)
	}
	// At 50 fps only 30 of the 50 frames land in a fresh thirtieth; the rest
	// see a zero delta and scroll nothing.
	if moved != 30 {
		t.Fatalf("held arrow scrolled on %d of %d frames, want 30", moved, frames)
	}
	// No ordinary frame reaches the 128-pixel low-frame-rate cap.
	if maxStep != setting {
		t.Fatalf("largest single-frame step %d map pixels, want %d", maxStep, setting)
	}
}

// The scroll rate does not change with the host frame rate: a 30 Hz caller and
// a 60 Hz caller cover the same ground in the same wall-clock second [07 §10].
func TestScrollRateIsFrameRateIndependent(t *testing.T) {
	setting := int32((&battleSession{}).scrollSetting())
	if setting <= 0 || setting > 128 {
		t.Skipf("scroll setting %d outside the range this contract can assert", setting)
	}
	run := func(frames int, msPerFrame uint32) int32 {
		cat := testCatalogON05()
		terrain := testWorldON05(300, 300)
		b := newTestBattle(cat, terrain)
		millis := &fakeMillisSource{}
		b.millisSource = millis
		buf := &frame.Buffer{}
		cl, _ := client.New(client.Options{Buffer: buf, Width: 640, Height: 480, Step: func(delta float64) {}})
		cl.SetCamera(b.cam)
		cl.Input().Mouse.SetPosition(320, 240)
		cl.Input().Kbd.SetKey(input.KeyRight, true)
		b.viewerStep(0, cl) // seed the scroll clock's anchor at t=0
		b.cam.X, b.cam.Z = 1000, 1000
		for i := 0; i < frames; i++ {
			millis.ms += msPerFrame
			b.viewerStep(float64(msPerFrame)/1000, cl)
		}
		return b.cam.X - 1000
	}
	slow := run(20, 50)  // 20 frames x 50 ms == 1000 ms
	fast := run(100, 10) // 100 frames x 10 ms == 1000 ms
	if slow != fast {
		t.Fatalf("one second of scrolling moved %d at 20 fps and %d at 100 fps", slow, fast)
	}
	if slow != setting*30 {
		t.Fatalf("one second of scrolling moved %d map pixels, want %d", slow, setting*30)
	}
}

// A held arrow key across a pause reproduces retail's paused scroll pass
// [07 §10 "The scroll pass while paused"]. In single-player the pump gates the
// budget call behind the pause test, so neither the raw delta nor the anchor
// moves while paused; the scroll pass is gated only on the options window and
// keeps running, re-multiplying the frozen pre-pause delta every host frame.
// Three things are locked here because each of them reads as a defect:
//
//   - the paused camera still scrolls, at scrollByte per host frame;
//   - the frozen value is the delta of the frame that pressed pause, so a
//     pause landing on a zero-delta frame leaves the camera immobile;
//   - the first unpaused frame takes one 128-pixel capped step, the scroll
//     pass's view of the single-player unpause burst [01 §4.3].
func TestScrollAcrossPauseUsesFrozenDelta(t *testing.T) {
	setting := int32((&battleSession{}).scrollSetting())
	if setting <= 0 || setting > 128 {
		t.Skipf("scroll setting %d outside the range this contract can assert", setting)
	}

	newHeldArrowBattle := func() (*battleSession, *client.Client, *fakeMillisSource) {
		cat := testCatalogON05()
		terrain := testWorldON05(300, 300)
		b := newTestBattle(cat, terrain)
		millis := &fakeMillisSource{}
		b.millisSource = millis
		buf := &frame.Buffer{}
		cl, _ := client.New(client.Options{Buffer: buf, Width: 640, Height: 480, Step: func(delta float64) {}})
		cl.SetCamera(b.cam)
		cl.Input().Mouse.SetPosition(320, 240)
		cl.Input().Kbd.SetKey(input.KeyRight, true)
		b.viewerStep(0, cl) // seed the scroll clock's anchor at t=0
		return b, cl, millis
	}

	t.Run("paused frames scroll at the frozen delta", func(t *testing.T) {
		b, cl, millis := newHeldArrowBattle()
		// Press pause on a frame whose delta is 1 (34 ms is one full scaled
		// unit), through the real hotkey rather than by writing the bit: the
		// budget step runs before hotkey dispatch, so that frame is still
		// budgeted and its delta is the one the pause freezes [07 §1].
		millis.ms += 34
		cl.Input().Kbd.SetKey(input.KeyPause, true)
		b.cam.X, b.cam.Z = 1000, 1000
		b.viewerStep(0.034, cl)
		cl.Input().Kbd.SetKey(input.KeyPause, false)
		if !b.sess.Clock.Paused {
			t.Fatalf("the pause hotkey did not pause the session")
		}
		if got := b.cam.X - 1000; got != setting {
			t.Fatalf("the pause frame scrolled %d map pixels, want %d", got, setting)
		}
		if b.scrollDelta != 1 {
			t.Fatalf("the pause frame froze delta %d, want the frame's own delta 1", b.scrollDelta)
		}
		// Every subsequent paused frame re-multiplies the same frozen delta,
		// whatever the host frame costs.
		for i := 0; i < 5; i++ {
			before := b.cam.X
			millis.ms += 8 // a 125 fps frame: unpaused this would be worth nothing
			b.viewerStep(0.008, cl)
			if got := b.cam.X - before; got != setting {
				t.Fatalf("paused frame %d scrolled %d map pixels, want %d", i, got, setting)
			}
		}
	})

	t.Run("a pause on a zero-delta frame freezes the camera", func(t *testing.T) {
		b, cl, millis := newHeldArrowBattle()
		// Pause on a frame that lands inside the same thirtieth as the anchor,
		// so the frozen delta is 0 and the whole pass is skipped.
		millis.ms += 8
		cl.Input().Kbd.SetKey(input.KeyPause, true)
		b.viewerStep(0.008, cl)
		cl.Input().Kbd.SetKey(input.KeyPause, false)
		b.cam.X, b.cam.Z = 1000, 1000
		for i := 0; i < 6; i++ {
			millis.ms += 8
			b.viewerStep(0.008, cl)
		}
		if b.cam.X != 1000 {
			t.Fatalf("a pause frozen on a zero delta scrolled to %d, want 1000", b.cam.X)
		}
	})

	t.Run("the first unpaused frame takes one capped step", func(t *testing.T) {
		b, cl, millis := newHeldArrowBattle()
		millis.ms += 34
		cl.Input().Kbd.SetKey(input.KeyPause, true)
		b.viewerStep(0.034, cl)
		cl.Input().Kbd.SetKey(input.KeyPause, false)
		for i := 0; i < 10; i++ { // sit paused for a third of a second
			millis.ms += 33
			b.viewerStep(0.033, cl)
		}
		b.sess.Clock.Paused = false
		b.cam.X, b.cam.Z = 1000, 1000
		millis.ms += 34
		b.viewerStep(0.034, cl)
		// The anchor never moved during the pause, so this frame's delta spans
		// the whole pause and clamps to the 128-pixel cap.
		if got := b.cam.X - 1000; got != 128 {
			t.Fatalf("the first unpaused frame scrolled %d map pixels, want the 128 cap", got)
		}
		// And the frame after it is back to the ordinary rate.
		before := b.cam.X
		millis.ms += 34
		b.viewerStep(0.034, cl)
		if got := b.cam.X - before; got != setting {
			t.Fatalf("the frame after unpause scrolled %d map pixels, want %d", got, setting)
		}
	})
}

// TestScrollSettingReadsSettingsFileOnceNotPerFrame locks WU-19-114: the
// camera-pan block must not open and parse the settings file on every host
// frame. scrollSetting caches the byte on first read (primeScrollSetting does
// the same thing explicitly at battle entry [composeBattleEntryDetached]);
// this asserts the cache, not a call-count seam, because settings.Load has no
// injectable hook and adding one only for a test would be its own scope
// creep — the cache is the observable contract.
func TestScrollSettingReadsSettingsFileOnceNotPerFrame(t *testing.T) {
	path := t.TempDir() + "/settings.json"
	t.Setenv(settings.EnvPath, path)

	write := func(scrollSpeed int) {
		blob := settings.Defaults()
		blob.ScrollSpeed = scrollSpeed
		if err := blob.Save(); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	write(99)
	b := &battleSession{}
	if got := b.scrollSetting(); got != 99 {
		t.Fatalf("first read = %d, want 99", got)
	}

	// Change the file underneath the cached battle. A camera-pan frame that
	// still called settings.Load per frame would pick this up immediately;
	// the cached path must not.
	write(50)
	for i := 0; i < 5; i++ {
		if got := b.scrollSetting(); got != 99 {
			t.Fatalf("cached read after file change = %d, want the cached 99 (no per-frame file I/O)", got)
		}
	}

	// The refresh hook — primeScrollSetting, called again — is how a settings
	// change is meant to reach a live battle (at battle entry today; nothing
	// inside a running battle currently writes settings, since ARMOPT's modal
	// chain has no live settings editor [07 "Tab options menu and manual
	// exit"], so this exercises the mechanism the entry point already uses).
	b.primeScrollSetting()
	if got := b.scrollSetting(); got != 50 {
		t.Fatalf("after primeScrollSetting = %d, want the refreshed 50", got)
	}
}

// TestScrollSettingUsesAttachedShellWithoutDisk confirms a battle composed
// with a frontend shell attached reads the shell's already-loaded scroll
// speed [gameShell.scrollSpeed, populated once at process startup by
// attachSettings] rather than hitting the settings file a second time.
func TestScrollSettingUsesAttachedShellWithoutDisk(t *testing.T) {
	// Point NANOLATHE_SETTINGS at a path that does not exist. If
	// readScrollSetting ever fell through to settings.Load with a shell
	// attached, it would silently read defaults instead of the shell's value
	// and this test would fail on the mismatch below.
	t.Setenv(settings.EnvPath, os.DevNull+"-does-not-exist")

	shell := &gameShell{scrollSpeed: 77}
	b := &battleSession{shell: shell}
	if got := b.scrollSetting(); got != 77 {
		t.Fatalf("scrollSetting with an attached shell = %d, want the shell's 77", got)
	}
}
