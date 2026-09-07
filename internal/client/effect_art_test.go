package client

import (
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/palette"
	"github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/testsupport"
	"github.com/nanolathe/nanolathe/internal/world"
	"github.com/nanolathe/nanolathe/vfs"
)

// TestEffectArtResolvesAgainstStockBanks is the other half of the explosion
// fix: the producer publishes a bank and an entry, and this is what turns them
// into pixels [06 R-WFX-01 §1].
//
// Until now EffectDrawOptions.ResolveFrame was never supplied in production, so
// DrawEffectViews skipped every sprite-bearing effect in the game — explosions,
// impacts, muzzle flashes, corpses. The assertions below are deliberately about
// real stock entries rather than a fixture, because the defect was that nothing
// resolved at all.
func TestEffectArtResolvesAgainstStockBanks(t *testing.T) {
	root := testsupport.RetailRoot(t)
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	c := &Client{}
	c.SetModelFS(fs)

	// A weapon's own art: `explosionart` inside `explosiongaf`.
	view := frame.EffectView{Graphic: "Explosion", AssetID: "fx"}
	first, ok := c.resolveEffectFrame(view, 0)
	if !ok || first == nil {
		t.Fatalf("the stock fx/Explosion entry did not resolve; no impact in the game can draw")
	}
	if first.Width == 0 || first.Height == 0 {
		t.Fatalf("resolved a zero-sized frame: %dx%d", first.Width, first.Height)
	}

	// The bank name is authored, and retail's scan of the loaded banks is
	// case-insensitive.
	if _, ok := c.resolveEffectFrame(frame.EffectView{Graphic: "explosion", AssetID: "FX"}, 0); !ok {
		t.Fatal("bank and entry lookup must be case-insensitive")
	}

	// An entry published with no bank comes from the engine's own fixed
	// effect-slot table, which is bound from `fx`.
	if _, ok := c.resolveEffectFrame(frame.EffectView{Graphic: "smoke 1"}, 0); !ok {
		t.Fatal("an entry with no bank must fall to the fx bank the engine binds its own slots from")
	}

	// Nothing is fabricated for an identity that does not resolve.
	if _, ok := c.resolveEffectFrame(frame.EffectView{Graphic: "", AssetID: "fx"}, 0); ok {
		t.Fatal("an event with no entry name resolved a frame")
	}
	if _, ok := c.resolveEffectFrame(frame.EffectView{Graphic: "no-such-entry", AssetID: "fx"}, 0); ok {
		t.Fatal("an unknown entry name resolved a frame")
	}
	if _, ok := c.resolveEffectFrame(frame.EffectView{Graphic: "Explosion", AssetID: "no-such-bank"}, 0); ok {
		t.Fatal("an unknown bank resolved a frame")
	}

	// A cursor past the end belongs to a sequence the pool is about to retire;
	// it clamps rather than vanishing mid-animation.
	if _, ok := c.resolveEffectFrame(view, 1<<20); !ok {
		t.Fatal("a cursor past the last frame must clamp into the entry")
	}

	// Authored timing: the frame reference's second word is the per-frame hold
	// in whole ticks [fmt gaf], and explosion art does not loop, because the
	// weapon parser clears the entry's loop byte at bind time.
	timing, ok := c.EffectFrameTiming("fx", "Explosion")
	if !ok {
		t.Fatal("no authored timing for the stock Explosion entry")
	}
	if timing.Loop {
		t.Fatal("explosion art must not loop; its loop byte is cleared when the weapon binds it")
	}
	if len(timing.Durations) == 0 {
		t.Fatal("resolved an empty duration list")
	}
	for i, d := range timing.Durations {
		if d < 1 {
			t.Fatalf("frame %d hold %d; a frame with hold h is shown for max(h,1) advances", i, d)
		}
	}
	// The census in [06 R-WFX-01 §1] records the stock `Explosion` entry at a
	// hold of 2 ticks a frame.
	if timing.Durations[0] != 2 {
		t.Fatalf("stock Explosion frame 0 hold = %d, want 2", timing.Durations[0])
	}
}

// TestExplosionArtActuallyReachesTheBlitter closes the loop the resolver test
// above only half proves: that the production draw options carry the resolver,
// so an explosion view becomes a sprite rather than a skip.
//
// This is the regression that hid every effect in the game. ResolveFrame was
// nil in production, and DrawEffectViews' own contract is to skip an effect it
// cannot resolve — so the composer reported no error, drew nothing, and looked
// exactly like a game with no explosions in it.
func TestExplosionArtActuallyReachesTheBlitter(t *testing.T) {
	root := testsupport.RetailRoot(t)
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	c := &Client{
		width:   256,
		height:  256,
		indexed: make([]uint8, 256*256),
		cam:     &camera.Camera{},
		pal:     &palette.Tables{},
	}
	c.SetModelFS(fs)

	view := frame.EffectView{
		ID: 1, Kind: frame.KindExplosion.String(), Strip: -1,
		Graphic: "Explosion", AssetID: "fx", ActiveA: true,
		X: numeric.Fixed(160 << 16), Y: 0, Z: numeric.Fixed(160 << 16),
	}
	stats := c.DrawEffectViews([]frame.EffectView{view}, c.effectDrawOptions())
	if stats.Sprites != 1 || stats.Skipped != 0 {
		t.Fatalf("a weapon's own explosion art drew %+v; the production draw options must resolve it", stats)
	}

	// An identity that cannot resolve is still skipped, not substituted.
	miss := view
	miss.Graphic = "no-such-entry"
	stats = c.DrawEffectViews([]frame.EffectView{miss}, c.effectDrawOptions())
	if stats.Sprites != 0 || stats.Skipped != 1 {
		t.Fatalf("an unresolvable identity drew %+v, want one skip and no sprite", stats)
	}
}

// TestCalculatedFlashDrawsUnderTheArt locks the explosion pool's SECONDARY
// cursor [06 R-WFX-01 §2]: every impact brightens the ground with a generated
// disc, whether or not the weapon has any art, and the named art composes over
// it rather than under it.
func TestCalculatedFlashDrawsUnderTheArt(t *testing.T) {
	root := testsupport.RetailRoot(t)
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	newClient := func() *Client {
		c := &Client{
			width: 256, height: 256,
			indexed: make([]uint8, 256*256),
			cam:     &camera.Camera{},
			pal:     brighteningTables(),
		}
		c.SetModelFS(fs)
		// The halo is bounded to the map rectangle by the same coverage
		// predicate the LHT pass uses, so the fixture needs a terrain to be
		// inside.
		c.terrain = &world.Terrain{CellW: 64, CellH: 64}
		return c
	}
	view := frame.EffectView{
		ID: 1, Kind: frame.KindExplosion.String(), Strip: -1,
		HasCalculatedFlash: true, CalculatedTable: 0, SeqB: 0, ActiveB: true,
		X: numeric.Fixed(160 << 16), Y: 0, Z: numeric.Fixed(160 << 16),
	}

	// With no art at all the disc is still drawn: a weapon whose art holder is
	// null shows a calculated flash and nothing else, which is a presentation
	// event, not nothing.
	c := newClient()
	stats := c.DrawEffectViews([]frame.EffectView{view}, c.effectDrawOptions())
	if stats.Halos != 1 {
		t.Fatalf("an impact with no art drew %+v; the calculated disc does not depend on the art holder", stats)
	}
	if stats.Sprites != 0 {
		t.Fatalf("an impact with no art drew a sprite: %+v", stats)
	}
	touched := 0
	for _, p := range c.indexed {
		if p != 0 {
			touched++
		}
	}
	if touched == 0 {
		t.Fatal("the calculated disc brightened no pixels")
	}

	// The disc is an ellipse compressed vertically by sqrt(1.33), so its
	// footprint is wider than it is tall. [03 §4.3.1] had this backwards.
	minX, maxX, minY, maxY := 1<<30, -1, 1<<30, -1
	for i, p := range c.indexed {
		if p == 0 {
			continue
		}
		x, y := i%c.width, i/c.width
		if x < minX {
			minX = x
		}
		if x > maxX {
			maxX = x
		}
		if y < minY {
			minY = y
		}
		if y > maxY {
			maxY = y
		}
	}
	w, h := maxX-minX+1, maxY-minY+1
	if w <= h {
		t.Fatalf("the disc covers %dx%d; the 1.33 is on the ROW term, so it must be wider than tall", w, h)
	}

	// An event that carries no flash draws none: the flag is what distinguishes
	// "table 0" from "no secondary cursor".
	plain := view
	plain.HasCalculatedFlash = false
	c2 := newClient()
	if stats := c2.DrawEffectViews([]frame.EffectView{plain}, c2.effectDrawOptions()); stats.Halos != 0 {
		t.Fatalf("an event with no calculated flash drew %+v", stats)
	}
}

// brighteningTables builds a minimal LHT whose rows visibly differ from the
// source index, so a test can see which pixels the halo touched.
func brighteningTables() *palette.Tables {
	t := &palette.Tables{}
	for level := 0; level < 32; level++ {
		for src := 0; src < 256; src++ {
			v := src + level
			if v > 255 {
				v = 255
			}
			t.Light[level*256+src] = uint8(v)
		}
	}
	return t
}

// TestStripFillParticleDraws locks the second of the two per-sub-record draw
// forms of [03 R-STRIP-01 §2]: a family that fills rather than blits paints a
// two-by-two rectangle in its own palette colour.
func TestStripFillParticleDraws(t *testing.T) {
	c := &Client{
		width: 64, height: 64,
		indexed: make([]uint8, 64*64),
		cam:     &camera.Camera{},
		pal:     &palette.Tables{},
	}
	view := frame.EffectView{
		ID: 1, Kind: frame.KindSmokeStart.String(), Strip: 2,
		StripFill: 0x67,
		X:         numeric.Fixed(20 << 16), Y: 0, Z: numeric.Fixed(20 << 16),
	}
	stats := c.DrawEffectViews([]frame.EffectView{view}, c.effectDrawOptions())
	if stats.Sprites != 1 || stats.Skipped != 0 {
		t.Fatalf("a filling strip particle drew %+v, want one sprite", stats)
	}
	painted := 0
	for _, p := range c.indexed {
		if p == 0x67 {
			painted++
		}
	}
	if painted != 4 {
		t.Fatalf("the fill painted %d pixels, want the two-by-two rectangle", painted)
	}

	// A view with neither an entry nor a fill still draws nothing.
	empty := view
	empty.StripFill = 0
	c2 := &Client{width: 64, height: 64, indexed: make([]uint8, 64*64), cam: &camera.Camera{}, pal: &palette.Tables{}}
	if stats := c2.DrawEffectViews([]frame.EffectView{empty}, c2.effectDrawOptions()); stats.Sprites != 0 {
		t.Fatalf("a view with no draw identity drew %+v", stats)
	}
}

// TestFinishedEffectPlayerStopsDrawing is the draw half of the fixed pool's
// per-player liveness [03 §1].
//
// The defect: an effect record survives until BOTH embedded animation players
// are inactive, and a non-looping player that terminates first resets its
// cursor to 0 [03 §4.4]. The two draw passes fired on the art name and on the
// calculated-flash flag alone, so the finished layer was painted again at frame
// 0 on every later tick — the explosion sprite restarting under its own fading
// disc, or the widest disc re-lighting the ground under art that was still
// playing. Both passes now require their own player to be live.
//
// The record is driven through the real pool so the test locks the published
// contract, not a hand-written view.
func TestFinishedEffectPlayerStopsDrawing(t *testing.T) {
	// A 2x2 opaque frame stands in for the weapon's art: this test is about
	// which pass runs, not about asset resolution.
	art := &formats.GAFFrame{Width: 2, Height: 2, Pixels: []byte{7, 7, 7, 7}, Transparent: make([]bool, 4)}
	options := EffectDrawOptions{
		ResolveFrame:    func(frame.EffectView, int32) (*formats.GAFFrame, bool) { return art, true },
		TerrainCoverage: func(x, y int) bool { return true },
	}
	newClient := func() *Client {
		return &Client{
			width: 256, height: 256,
			indexed: make([]uint8, 256*256),
			cam:     &camera.Camera{},
			pal:     brighteningTables(),
		}
	}
	// Unequal timing: two ticks of art (two frames held one tick each) against
	// six ticks of flash (three frames held two ticks each).
	admit := func(durationsA, durationsB []int32) *render.FixedEffectPool {
		p := &render.FixedEffectPool{}
		if !p.AppendView(frame.EffectView{
			ID: 1, Kind: frame.KindExplosion.String(), Strip: -1,
			Graphic: "Explosion", AssetID: "fx",
			HasCalculatedFlash: true, CalculatedTable: 0,
			DurationsA: durationsA, DurationsB: durationsB,
			X: numeric.Fixed(160 << 16), Y: 0, Z: numeric.Fixed(160 << 16),
		}) {
			t.Fatal("two-player admission failed")
		}
		return p
	}

	// Both players live: the disc and the art both draw, disc first.
	p := admit([]int32{1, 1}, []int32{2, 2, 2})
	var views []frame.EffectView
	views = p.SnapshotViewsInto(views)
	c := newClient()
	if stats := c.DrawEffectViews(views, options); stats.Sprites != 1 || stats.Halos != 1 || stats.Skipped != 0 {
		t.Fatalf("a record with both players live drew %+v, want one sprite and one halo", stats)
	}

	// (a) The art finishes first. The record is still published because the
	// flash keeps it alive, and its primary cursor is back at 0.
	for tick := uint32(1); tick <= 2; tick++ {
		p.Update(tick)
	}
	views = p.SnapshotViewsInto(views)
	if len(views) != 1 {
		t.Fatalf("the record retired while its flash was still running: %d", len(views))
	}
	c = newClient()
	stats := c.DrawEffectViews(views, options)
	if stats.Sprites != 0 {
		t.Fatalf("the finished art drew again at frame 0: %+v", stats)
	}
	if stats.Halos != 1 {
		t.Fatalf("the still-running flash stopped drawing: %+v", stats)
	}
	if stats.Skipped != 0 {
		t.Fatalf("a finished layer is not an unresolved one: %+v", stats)
	}

	// (b) The inverse: the flash finishes first and the art plays on.
	q := admit([]int32{2, 2, 2}, []int32{1, 1})
	for tick := uint32(1); tick <= 2; tick++ {
		q.Update(tick)
	}
	views = q.SnapshotViewsInto(views)
	if len(views) != 1 {
		t.Fatalf("the record retired while its art was still running: %d", len(views))
	}
	c = newClient()
	stats = c.DrawEffectViews(views, options)
	if stats.Halos != 0 {
		t.Fatalf("the finished flash re-lit the ground: %+v", stats)
	}
	if stats.Sprites != 1 {
		t.Fatalf("the still-running art stopped drawing: %+v", stats)
	}

	// (d) A layer with no player of its own draws nothing: named art whose
	// authored timing never resolved activates no player, and neither the art
	// name nor the record's survival stands in for one [I9]. The production
	// impact is NOT this case — its art is resolved as the primary player
	// beside the flash's secondary timing [06 R-WFX-01 §2], which
	// render.TestEffectServiceResolvesPrimaryTimingBesideACalculatedFlash
	// covers.
	r := admit(nil, []int32{2, 2, 2})
	r.Update(1)
	views = r.SnapshotViewsInto(views)
	c = newClient()
	if stats := c.DrawEffectViews(views, options); stats.Sprites != 0 || stats.Halos != 1 {
		t.Fatalf("a record whose art has no player drew %+v, want only its disc", stats)
	}
}
