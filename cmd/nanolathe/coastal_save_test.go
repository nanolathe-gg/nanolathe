package main

import (
	"fmt"
	"os"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport"
)

// Opt-in local diagnostic: read a supplied save without modifying it, then
// exercise the production shell and modern recorder with genuine simulation.
func TestCoastalSavedGameParticles(t *testing.T) {
	savePath := os.Getenv("NANOLATHE_COASTAL_SAVE")
	if savePath == "" {
		t.Skip("set NANOLATHE_COASTAL_SAVE to a local save")
	}
	for _, stride := range []uint32{1, 2} {
		t.Run(fmt.Sprintf("host_tick_stride_%d", stride), func(t *testing.T) { coastalSavedGameParticles(t, savePath, stride) })
	}
}

func coastalSavedGameParticles(t *testing.T, savePath string, stride uint32) {
	resetSaveLoadScreenState(t)
	opts := Options{Root: testsupport.RetailRoot(t), Map: "coast to coast", Seed: 7}
	cs, err := openContent(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	old := clPtr
	defer func() { clPtr = old }()
	shell, cl, err := newDirectBattleView(opts, cs)
	if err != nil {
		t.Fatal(err)
	}
	defer shell.teardownBattle(cl)
	cl.SetEnhanced(true)
	if err := shell.loadRetailSavePath(savePath); err != nil {
		t.Fatal(err)
	}
	b := shell.battle
	t.Logf("restored tick=%d clock=%+v enhanced=%v local=%d", b.sess.Clock.GlobalTick, b.sess.Clock, cl.Enhanced(), b.sess.LocalOwner)
	for _, u := range cl.Buffer().Current().Units {
		if u.CanHover {
			t.Logf("hover slot=%d instance=%d owner=%d def=%s mode=%d pos=%d,%d,%d ground=%d sea=%d build=%f", u.Slot, u.InstanceID, u.Owner, b.sess.Units.Unit(u.Slot).Def.UnitName, u.MoverMode, u.X.Int(), u.Y.Int(), u.Z.Int(), b.sess.World.HeightAt(u.X, u.Z).Int(), b.sess.World.SeaLevelWorld().Int(), u.BuildRemaining)
		}
	}
	ms := &shotMillisSource{step: uint32(b.sess.Clock.ScaledAnchor)}
	b.millisSource = ms
	// Resume the pause retained by saves through the battle options controls.
	cl.Input().Kbd.SetKey(input.KeyF2, true)
	ms.step++
	cl.Step(1.0 / 30)
	cl.Input().Kbd.SetKey(input.KeyF2, false)
	cl.Input().Kbd.ResetEdges()
	r := b.hud.optionsWin.PlacedRect(b.hud.optionsWin.GadgetIndex("OK"))
	cl.Input().Mouse.SetPosition(float32(r.X+r.W/2), float32(r.Y+r.H/2))
	cl.Input().Mouse.SetButton(input.MouseButtonLeft, true)
	ms.step++
	cl.Step(1.0 / 30)
	cl.Input().Mouse.SetButton(input.MouseButtonLeft, false)
	ms.step++
	cl.Step(1.0 / 30)
	cl.Input().Mouse.ResetEdges()
	var tracked []frame.UnitView
	for _, u := range cl.Buffer().Current().Units {
		if u.CanHover && u.Owner == b.sess.LocalOwner && u.BuildRemaining == 0 && b.sess.World.HeightAt(u.X, u.Z) >= b.sess.World.SeaLevelWorld() {
			tracked = append(tracked, u)
			x, z := u.X+150*numeric.FixedOne, u.Z
			if err := b.sess.EnqueueHumanCommand(session.HumanCommand{Kind: session.HumanOrder, Order: session.HumanOrderCommand{Handles: []pool.Handle{u.Slot}, Code: 2, Position: orders.ResolvePos{X: x, Y: b.sess.World.HeightAt(x, z), Z: z}}}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if b.sess.Clock.Paused {
		t.Fatal("options controls did not resume the saved battle")
	}
	if len(tracked) == 0 {
		t.Fatal("save needs a completed local hovercraft on dry ground")
	}
	// Keep the real moving craft inside this test's smaller viewport.
	b.cam.JumpToBattleViewCenter(int32(tracked[0].X.Int())+150, int32(tracked[0].Z.Int()))
	prev := map[uint64]frame.UnitView{}
	lastTick := cl.Buffer().Current().Tick
	moved, gaps, maxDust := 0, 0, 0
	for i := 0; i < 180; i++ {
		ms.step += stride
		cl.Step(1.0 / 30)
		cl.BeginPresentationFrame()
		f := cl.Buffer().Current()
		if f.Tick > lastTick+1 {
			gaps++
		}
		lastTick = f.Tick
		for _, u := range f.Units {
			if u.CanHover {
				if p, ok := prev[u.InstanceID]; ok && (p.X != u.X || p.Z != u.Z) {
					moved++
				}
				prev[u.InstanceID] = u
			}
		}
		sink := &coastalSaveSink{}
		cl.RecordModernFrame().Replay(sink)
		maxDust = max(maxDust, sink.dust)
		if i%30 == 0 {
			t.Logf("frame=%d tick=%d dust=%d foam=%d water=%+v", i, f.Tick, sink.dust, sink.foam, sink.water)
		}
	}
	t.Logf("end tick=%d moving hover samples=%d skipped draw observations=%d max dust=%d", lastTick, moved, gaps, maxDust)
	if moved == 0 {
		t.Fatal("move orders produced no actual hover movement")
	}
	if stride > 1 && gaps == 0 {
		t.Fatal("catch-up scenario did not advance multiple ticks between recordings")
	}
	if maxDust == 0 {
		t.Fatal("moving dry-ground hovercraft produced no visible dust records after load")
	}
}

type coastalSaveSink struct {
	dust, foam int
	water      drawlist.WaterSurface
}

func (s *coastalSaveSink) SurfaceWakes(v drawlist.SurfaceWakes) {
	for _, m := range v.Marks {
		if m.Dust {
			s.dust++
		}
		if m.Foam {
			s.foam++
		}
	}
}
func (s *coastalSaveSink) Clear()                     {}
func (s *coastalSaveSink) Terrain(v drawlist.Terrain) { s.water = v.Water }
func (s *coastalSaveSink) Sprite(drawlist.Sprite)     {}
func (s *coastalSaveSink) Glyphs(drawlist.Glyphs)     {}
func (s *coastalSaveSink) Fill(drawlist.Fill)         {}
func (s *coastalSaveSink) Line(drawlist.Line)         {}
func (s *coastalSaveSink) Points(drawlist.Points)     {}
func (s *coastalSaveSink) Flash(drawlist.Flash)       {}
func (s *coastalSaveSink) Halo(drawlist.Halo)         {}
func (s *coastalSaveSink) Model(drawlist.Model)       {}
func (s *coastalSaveSink) Fog(drawlist.Fog)           {}
func (s *coastalSaveSink) Surface(drawlist.Surface)   {}
func (s *coastalSaveSink) Cursor(drawlist.Cursor)     {}
func (s *coastalSaveSink) Expand()                    {}
