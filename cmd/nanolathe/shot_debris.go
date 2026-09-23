package main

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"sort"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/features"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/platform/gpurender"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// A debris-shower capture route: `--shot-debris <dir>` kills a tight cluster of
// units in front of the viewing player's own and writes one modern PNG per tick
// from the kill onward, so the ground pools the burning pieces throw
// (DESIGN_GPU_RENDERER §31.3) can be reviewed as pictures across their whole
// lifetime rather than asserted about at one arbitrary tick.
//
// `--shot` cannot reach this view: a capture advances a fixed tick count and
// kills nothing, and the event is over in under a second. The sequence is
// composed inside ONE Ebitengine loop, because a process may enter RunGame only
// once, and from the process main thread, because AppKit requires it.

// runDebrisShot builds the scene, advances it to the kill, and hands the loop
// the tick-by-tick capture.
func runDebrisShot(opts Options, cs *contentSet) error {
	if err := os.MkdirAll(opts.ShotDebris, 0o755); err != nil {
		return err
	}
	shotW, shotH := retailScreenW, retailScreenH
	if opts.ShotSize != "" {
		if _, err := fmt.Sscanf(opts.ShotSize, "%dx%d", &shotW, &shotH); err != nil || shotW <= 0 || shotH <= 0 {
			return fmt.Errorf("nanolathe: shot debris: --shot-size wants \"WxH\", got %q", opts.ShotSize)
		}
	}
	// The same fresh-battle composition the ordinary capture route builds.
	request, _, err := headlessFreshBattleRequest(opts, cs, newBattleSeedSource(opts))
	if err != nil {
		return err
	}
	authoritative, err := composeAuthoritativeBattle(request)
	if err != nil {
		return err
	}
	sess := authoritative.Session
	cat := sess.Catalog

	// The viewing player's start is the one place on an arbitrary map that is
	// certainly land, certainly in line of sight, and certainly clear.
	var ox, oy, oz numeric.Fixed
	found := false
	for _, u := range sess.Units.Iter() {
		if u == nil || !u.Alive || u.Owner != sess.LocalOwner {
			continue
		}
		ox, oy, oz, found = u.X, u.Y, u.Z, true
		break
	}
	if !found {
		return fmt.Errorf("nanolathe: shot debris: no local unit to place the cluster beside")
	}
	def, ok := cat.Unit(opts.ShotDebrisUnit)
	if !ok {
		return fmt.Errorf("nanolathe: shot debris: unit %q is not in the catalog", opts.ShotDebrisUnit)
	}
	// A tight cluster one screen-quarter away: close enough that one frame
	// holds every pool, far enough that the commander survives to keep the
	// site in line of sight, so the effects are admitted (§22.4).
	// The two modes are exclusive: --shot-debris-burn stages standing fires and
	// kills nothing, so it spawns no victims either.
	kills := opts.ShotDebrisCount
	if opts.ShotDebrisBurn > 0 {
		kills = 0
	}
	victims := make([]pool.Handle, 0, kills)
	for i := 0; i < kills; i++ {
		x := ox + numeric.Fixed(int64(96+(i%3)*48)<<16)
		z := oz + numeric.Fixed(int64(96+(i/3)*48)<<16)
		y := sess.World.HeightAt(x, z)
		if y == -1 {
			y = oy
		}
		h, err := sess.Units.Create(def, uint8(sess.LocalOwner), x, y, z)
		if err != nil {
			return fmt.Errorf("nanolathe: shot debris: create %s: %w", opts.ShotDebrisUnit, err)
		}
		victims = append(victims, h)
	}

	var (
		b  *battleSession
		cl *client.Client
	)
	cl, err = client.New(client.Options{
		Buffer: sess.Snapshot,
		Width:  shotW,
		Height: shotH,
		Step:   func(delta float64) { b.viewerStep(delta, cl) },
	})
	if err != nil {
		return fmt.Errorf("nanolathe: client: %w", err)
	}
	cl.SetModelFS(cs.unmappedMount)
	applyVisualOptions(cl, loadedSettings().Display)
	applyCommunityHUDOptions(cl, loadedSettings().Presentation)
	// Every Enhanced switch on: the point of the capture is the lighting pass.
	cl.SetEffects(drawlist.Effects{Water: true, Lighting: opts.ShotDebrisLighting, Finish: true, Distortion: true, Marks: true})
	cl.SetGlow(opts.ShotDebrisGlow)
	cl.SetEnhanced(true)
	b, err = composeBattleEntryWithDetail(sess, cat, cs, cl, nil, captureDetailArt(opts, cs, sess.World))
	if err != nil {
		return err
	}
	defer b.teardown(cl)
	b.cam.ViewW, b.cam.ViewH = int32(shotW), int32(shotH)
	b.cam.JumpToBattleViewCenter(int32(ox.Int())+144, int32(oz.Int())+144)
	b.cam.Clamp()
	if opts.Zoom != 0 {
		jumpBattleZoom(b, int32(shotW)/2, int32(shotH)/2, opts.Zoom, true)
	}
	millis := &shotMillisSource{}
	b.millisSource = millis

	const tickSeconds = 1.0 / 30.0
	step := uint32(0)
	advance := func() {
		step++
		millis.step = step
		b.viewerStep(tickSeconds, cl)
		cl.ObserveCommittedTick()
	}
	// Let the spawned units settle and the site reveal before anything dies.
	for i := 0; i < 40; i++ {
		advance()
	}
	if opts.ShotDebrisBurn > 0 {
		// A standing-fire capture: the pools a burning treeline lays on open
		// ground, without a fireball over them.
		// Nearest the capture centre first, so the fires land in shot.
		cxCell, czCell := (int32(ox.Int())+144)/16, (int32(oz.Int())+144)/16
		insts := sess.Features.Instances()
		sort.SliceStable(insts, func(i, j int) bool {
			return cellDist2(insts[i], cxCell, czCell) < cellDist2(insts[j], cxCell, czCell)
		})
		lit := 0
		for _, inst := range insts {
			if lit >= opts.ShotDebrisBurn || inst == nil {
				continue
			}
			if sess.Features.Ignite(inst.CX, inst.CZ, 1, 1000) {
				lit++
			}
		}
		fmt.Fprintf(os.Stderr, "nanolathe: shot debris: ignited %d features\n", lit)
	} else {
		for _, h := range victims {
			u := sess.Units.Unit(h)
			if u == nil {
				continue
			}
			u.Health = -u.MaxHealth
			sess.Units.Destroy(h, units.DeathKilled)
		}
	}

	game := &debrisShotGame{cl: cl, buffer: sess.Snapshot, dir: opts.ShotDebris, w: shotW, h: shotH, frames: opts.ShotDebrisFrames, advance: advance}
	ebiten.SetWindowVisible(false)
	ebiten.SetWindowSize(shotW, shotH)
	if err := ebiten.RunGame(game); err != nil {
		return fmt.Errorf("nanolathe: shot debris: capture loop: %w", err)
	}
	if game.err != nil {
		return game.err
	}
	fmt.Fprintf(os.Stderr, "nanolathe: shot debris: wrote %d frames to %s (%d x %s on %q)\n",
		game.drawn, opts.ShotDebris, opts.ShotDebrisCount, opts.ShotDebrisUnit, opts.Map)
	return nil
}

// debrisShotGame advances the battle one tick per Draw and writes that tick's
// modern frame, so one process produces a whole sequence through one device.
type debrisShotGame struct {
	cl      *client.Client
	buffer  *frame.Buffer
	dir     string
	w, h    int
	frames  int
	advance func()

	gpu      *gpurender.Renderer
	drawn    int
	err      error
	done     bool
	readback []byte
}

func (g *debrisShotGame) Update() error {
	if g.done {
		return ebiten.Termination
	}
	return nil
}

func (g *debrisShotGame) Draw(screen *ebiten.Image) {
	if g.done {
		return
	}
	if g.gpu == nil {
		gpu, err := gpurender.NewChecked(g.cl.PaletteTables(), g.w, g.h)
		if err != nil {
			g.err = fmt.Errorf("nanolathe: shot debris: modern renderer setup: %w", err)
			g.done = true
			return
		}
		g.gpu = gpu
		g.readback = make([]byte, 4*g.w*g.h)
	}
	g.cl.BeginPresentationFrame()
	list := g.cl.RecordModernFrame()
	if list == nil {
		g.err = fmt.Errorf("nanolathe: shot debris: frame %d recorded nothing", g.drawn)
		g.done = true
		return
	}
	g.gpu.SetDisplayPalette(g.cl.DisplayPalette())
	g.gpu.SetGlow(g.cl.Glow())
	g.gpu.SetGlowStrength(g.cl.GlowStrength())
	g.gpu.SetGlowFamilies(g.cl.GlowFamilies())
	g.gpu.SetEffects(g.cl.Effects())
	img := g.gpu.Execute(list, g.w, g.h)
	if img == nil {
		g.err = fmt.Errorf("nanolathe: shot debris: frame %d composed no surface", g.drawn)
		g.done = true
		return
	}
	screen.DrawImage(img, &ebiten.DrawImageOptions{})
	img.ReadPixels(g.readback)
	if err := g.writeFrame(); err != nil {
		g.err = err
		g.done = true
		return
	}
	var debris, burning, smoking int
	if cur := g.buffer.Current(); cur != nil {
		for _, d := range cur.Debris {
			debris++
			if d.Fire {
				burning++
			}
			if d.Smoke {
				smoking++
			}
		}
	}
	ms := g.gpu.ModelStats()
	fmt.Fprintf(os.Stderr, "nanolathe: shot debris: frame %02d sources=%d explosion=%d nano=%d fire=%d projectile=%d wreck=%d spark=%d ground=%d debris=%d burning=%d smoking=%d\n",
		g.drawn, ms.BattleLights, ms.BattleLightKinds[0], ms.BattleLightKinds[1],
		ms.BattleLightKinds[2], ms.BattleLightKinds[3], ms.BattleLightKinds[4], ms.BattleLightKinds[5], ms.GroundLights, debris, burning, smoking)
	g.drawn++
	if g.drawn >= g.frames {
		g.done = true
		return
	}
	g.advance()
}

func (g *debrisShotGame) writeFrame() error {
	out := image.NewRGBA(image.Rect(0, 0, g.w, g.h))
	copy(out.Pix, g.readback)
	path := filepath.Join(g.dir, fmt.Sprintf("frame%02d.png", g.drawn))
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, out)
}

func (g *debrisShotGame) Layout(int, int) (int, int) { return g.w, g.h }

func cellDist2(inst *features.Instance, cx, cz int32) int64 {
	if inst == nil {
		return 1 << 60
	}
	dx, dz := int64(int32(inst.CX)-cx), int64(int32(inst.CZ)-cz)
	return dx*dx + dz*dz
}
