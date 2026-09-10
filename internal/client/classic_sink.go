package client

import (
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/render"
)

// classicSink replays recorded draw commands through the software byte-writing
// routines in internal/client (docs/DESIGN_GPU_RENDERER.md §2.2).
type classicSink struct {
	c *Client
}

// classicSink returns the software executor bound to this client. It is a
// zero-cost struct literal, so recording and replaying one frame allocates
// nothing (docs/DESIGN_GPU_RENDERER.md §2.2).
func (c *Client) classicSink() drawlist.Sink { return classicSink{c} }

// The emit* helpers record one command each into c.list and nothing more
// (WU-1.8). The recording pass writes nothing to c.indexed; the frame is
// executed by one c.list.Replay(c.classicSink()) after the whole frame — clear,
// world, interface, cursor, expand — has been recorded in order. Every record is
// self-contained: destination-reading families carry their palette on the record
// and Points batches own an immutable arena sub-slice, so a deferred replay
// observes no client scratch state (docs/DESIGN_GPU_RENDERER.md §2.2, C-G1).

// emitClear records the frame clear, which zeroes the indexed surface. It is the
// first command drawCommittedFrame records, so replaying the list clears before
// any draw (WU-1.8).
func (c *Client) emitClear() {
	c.list.RecordClear()
}

// emitTerrain records one terrain blit.
func (c *Client) emitTerrain(t drawlist.Terrain) {
	c.list.RecordTerrain(t)
}

// emitFill records one indexed rectangle.
func (c *Client) emitFill(f drawlist.Fill) {
	if c == nil {
		return
	}
	if !c.hasUIClip {
		c.list.RecordFill(f)
		return
	}
	if f.Style == drawlist.FillOutline {
		c.emitClippedUIOutline(f)
		return
	}
	if f.Style == drawlist.FillFrameInclusive {
		f.Clip = intersectUIRects(f.Clip, c.uiClip)
		if f.Clip.W <= 0 || f.Clip.H <= 0 {
			return
		}
		c.list.RecordFill(f)
		return
	}
	f.Rect = c.clipUIRect(f.Rect)
	if f.Rect.W <= 0 || f.Rect.H <= 0 {
		return
	}
	c.list.RecordFill(f)
}

// emitClippedUIOutline records the original frame edges as solid strokes before
// applying a private-surface clip. Intersecting the whole rectangle first would
// incorrectly manufacture a new outline along the child surface boundary.
func (c *Client) emitClippedUIOutline(f drawlist.Fill) {
	r := f.Rect
	if r.W <= 0 || r.H <= 0 {
		return
	}
	recordEdge := func(edge drawlist.Rect) {
		edge = c.clipUIRect(edge)
		if edge.W <= 0 || edge.H <= 0 {
			return
		}
		c.list.RecordFill(drawlist.Fill{Rect: edge, Index: f.Index, Style: drawlist.FillSolid})
	}
	recordEdge(drawlist.Rect{X: r.X, Y: r.Y, W: r.W, H: 1})
	if r.H > 1 {
		recordEdge(drawlist.Rect{X: r.X, Y: r.Y + r.H - 1, W: r.W, H: 1})
	}
	if r.H > 2 {
		recordEdge(drawlist.Rect{X: r.X, Y: r.Y + 1, W: 1, H: r.H - 2})
		if r.W > 1 {
			recordEdge(drawlist.Rect{X: r.X + r.W - 1, Y: r.Y + 1, W: 1, H: r.H - 2})
		}
	}
}

// emitFillInclusive records one inclusive-bounds solid rectangle and executes
// it inline (the health bar's two fills, WU-1.5). The caller passes the same
// inclusive left/top/right/bottom fillRectInclusive takes; they are carried in
// the Fill family's extent form (W = right-left+1, H = bottom-top+1) so the
// covered pixel set is preserved exactly and the sink reconstructs the inclusive
// bounds [03 R-FX-01 §6][R-P0-19-P].
func (c *Client) emitFillInclusive(left, top, right, bottom int32, idx uint8) {
	c.emitFill(drawlist.Fill{
		Rect:  drawlist.Rect{X: left, Y: top, W: right - left + 1, H: bottom - top + 1},
		Index: idx,
		Style: drawlist.FillSolidInclusive,
	})
}

// emitFog records one clipped fog op list.
func (c *Client) emitFog(fg drawlist.Fog) {
	c.list.RecordFog(fg)
}

// emitSprite records one GAF-frame blit. General keyed and tinted GAF calls
// decompose composites into their ordered leaves here, before either executor
// sees the list. That preserves each child's destination-dependent ALP read and
// leaves target clipping to the actual leaf blit [03 R-COMP-01 §2].
func (c *Client) emitSprite(sp drawlist.Sprite) {
	sp.HasClip, sp.Clip = c.clipUISprite(sp.HasClip, sp.Clip)
	if sp.Frame != nil && len(sp.Frame.Subframes) != 0 {
		switch sp.Kind {
		case drawlist.BlitKeyed, drawlist.BlitTinted:
			penX, penY := sp.X, sp.Y
			if sp.Kind == drawlist.BlitKeyed && !sp.Anchored {
				// A nonanchored caller supplied the parent's top-left. The
				// compositor receives its anchor pen, shared unchanged by every
				// child [fmt gaf][03 R-COMP-01 §2].
				penX += int32(sp.Frame.XOffset)
				penY += int32(sp.Frame.YOffset)
			}
			c.emitGeneralGAFLeaves(sp, sp.Frame, penX, penY, sp.Kind == drawlist.BlitTinted)
			return
		}
	}
	c.list.RecordSprite(sp)
}

func (c *Client) emitGeneralGAFLeaves(sp drawlist.Sprite, frame *formats.GAFFrame, penX, penY int32, tinted bool) {
	if frame == nil {
		return
	}
	if len(frame.Subframes) != 0 {
		for _, child := range frame.Subframes {
			if child == nil {
				continue
			}
			// A tinted parent calls its descendants through the same tinted
			// blitter; otherwise the child's authored high byte selects it.
			c.emitGeneralGAFLeaves(sp, child, penX, penY, tinted || child.AlternateBlitter != 0)
		}
		return
	}
	sp.Frame, sp.X, sp.Y = frame, penX, penY
	sp.Anchored = true
	if tinted {
		// The ALP capability is enabled at window startup independently of
		// the model Shading preference [03 R-REN-03D §4]. The executor checks
		// for its palette; an authored tinted leaf keeps its place in order.
		sp.Kind = drawlist.BlitTinted
	} else {
		sp.Kind = drawlist.BlitKeyed
	}
	c.list.RecordSprite(sp)
}

// emitLine records one indexed line (beam and segment strokes, WU-1.4).
func (c *Client) emitLine(l drawlist.Line) {
	c.list.RecordLine(l)
}

// emitPoints records the batch of single-pixel writes appended to the point
// arena since off (the LHT halo, the calculated flash disc, the minimap surface
// and its viewport rectangle). The record carries a three-index sub-slice
// arena[off:end:end]: the capped bound forces a later append to reallocate rather
// than overwrite this batch's region, so the batch is immutable for the life of
// the frame and safe under deferred replay (WU-1.8). An empty batch records
// nothing.
func (c *Client) emitPoints(off int, kind drawlist.PointKind) {
	if off < 0 || off >= len(c.pointArena) {
		return
	}
	rec := c.pointArena[off:len(c.pointArena):len(c.pointArena)]
	c.list.RecordPoints(drawlist.Points{Kind: kind, Points: rec})
}

// emitGlyphs records one FNT text run (the health-bar walk's control-group
// digit, WU-1.5).
func (c *Client) emitGlyphs(g drawlist.Glyphs) {
	g.HasClip, g.Clip = c.clipUISprite(g.HasClip, g.Clip)
	c.list.RecordGlyphs(g)
}

// emitCursor records the software-cursor blit (WU-1.5).
func (c *Client) emitCursor(cu drawlist.Cursor) {
	c.list.RecordCursor(cu)
}

// emitSurface records one indexed byte-surface blit (the SELMAP MAPPIC gadget,
// WU-1.7b).
func (c *Client) emitSurface(sf drawlist.Surface) {
	sf.HasClip, sf.Clip = c.clipUISprite(sf.HasClip, sf.Clip)
	c.list.RecordSurface(sf)
}

// emitModel records one composed model subject's durable classic commit. The
// packet owns its finished body or staging image and its already-punched shadow,
// so a retained list needs no client-side lookup. The pending record names which
// of the three commit steps the classic sink runs (the shadow, one body blit,
// then the observer), so a carrier's staged child can record its own framebuffer
// shadow before the carrier and its observer after, each in its own list
// position rather than as a direct write during recording
// (docs/DESIGN_GPU_RENDERER.md §2.1 C-G5).
func (c *Client) emitModel(pending pendingModelCommit) {
	cmd := drawlist.Model{Classic: c.classicModelForCommit(pending), Geometry: geometryForCommit(pending), ShadowOnly: pending.shadow && !pending.body}
	if pending.shadow && pending.m.draw != nil && pending.m.draw.CastsShadow && (cmd.Geometry == nil || !cmd.Geometry.Eligible) {
		cmd.ShadowOmissions = 1
	}
	c.list.RecordModel(cmd)
}

// classicModelImage copies the recorder's mutable model planes into the draw
// command. The command owns the values that replay needs, including a staging
// body and a child-only shadow [03 R-REN-03A §4].
func (c *Client) classicModelImage(t *modelTarget) *drawlist.ClassicModelImage {
	if t == nil {
		return nil
	}
	return c.list.CopyClassicImage(drawlist.ClassicModelImage{
		Color: t.color, Coverage: t.covered, Key: t.height,
		Width: int32(t.width), Height: int32(t.heightPx),
		OriginX: t.originX, OriginY: t.originY, AnchorX: t.anchorX, AnchorY: t.anchorY,
		Transparent: t.transparent, Blit: modelTargetBlit(t, c.modelBlitScale()),
	})
}

func modelTargetBlit(t *modelTarget, fallback camera.ViewScale) camera.ViewScale {
	if t != nil && t.blit != 0 {
		return t.blit
	}
	return fallback
}

// classicModelTarget adapts owned packet planes to the one model-image blitter
// and diagnostic reader. Blitting reads these planes without mutating them.
func classicModelTarget(i *drawlist.ClassicModelImage) *modelTarget {
	if i == nil {
		return nil
	}
	return &modelTarget{
		color: i.Color, covered: i.Coverage, height: i.Key,
		width: int(i.Width), heightPx: int(i.Height),
		originX: i.OriginX, originY: i.OriginY, anchorX: i.AnchorX, anchorY: i.AnchorY,
		transparent: i.Transparent, blit: i.Blit,
	}
}

// classicModelForCommit freezes every classic replay operand while the model
// composer still owns its scratch. Shadow construction, including the body
// punch, happens here because replay must not retain UnitDraw or model state.
// Trace publication is an observer-only sidecar: it cannot affect pixels and
// retains its own copied target planes rather than any resettable client table.
func (c *Client) classicModelForCommit(p pendingModelCommit) *drawlist.ClassicModel {
	if c == nil {
		return nil
	}
	classic := &drawlist.ClassicModel{}
	if p.shadow {
		classic.Shadow = c.classicModelImage(c.buildModelShadow(p.m.draw, p.m.image))
	}
	if p.body {
		classic.Body = c.classicModelImage(p.blit)
	}
	if p.trace && p.m.raster != nil && p.m.raster.trace != nil {
		trace := p.m.raster.trace
		sink, filter := c.rendererTraceSink, c.rendererTraceFilter
		classic.Trace = c.classicModelImage(p.m.raster)
		classic.Observer = func(target *drawlist.ClassicModelImage, indexed []byte, width, height int) {
			trace.resolve(classicModelTarget(target), indexed, width, height)
			trace.emit(sink, filter)
		}
	}
	if classic.Shadow == nil && classic.Body == nil && classic.Observer == nil {
		return nil
	}
	return classic
}

// Clear zeroes the indexed surface. It is the first command of every committed
// frame, replayed before any draw, and writes exactly the bytes the direct clear
// loop did (WU-1.8) [C-G1].
func (s classicSink) Clear() {
	// clear zeroes exactly the bytes the direct `for i := range c.indexed` loop
	// wrote — the whole indexed slice.
	clear(s.c.indexed)
}

// Terrain replays one terrain blit into the indexed surface. The record carries
// the immutable-after-load *world.Terrain and, for the classic executor, the
// live camera; a nil terrain clears the destination and a nil camera projects
// from 0,0, exactly as the direct call did (docs/DESIGN_GPU_RENDERER.md §2.2).
func (s classicSink) Terrain(t drawlist.Terrain) {
	// The record's detail tiles are used only at the detail scale, where the
	// blitter copies a 64x64 tile one-to-one; without them it doubles the 32x32
	// tile by nearest sampling (DESIGN_GPU_RENDERER §14.2, §14.3). The scale
	// itself rides the camera, which is the record's projection authority.
	BlitTerrainDetail(s.c.indexed, s.c.width, s.c.height, t.Terrain, t.Cam, t.Detail)
}

// Sprite replays one GAF-frame or PCX blit. It routes each recorded blit to its
// raw byte writer and this is that writer's only execution: the keyed
// effect/projectile/HUD blits (anchored or plain), the ALP-tinted strip blit,
// the LHT-lit glyph blit, the scaled surface-gadget blit, the 2D feature GAF
// sprite copy and its shadow stencil, and the opaque PCX background blit
// [03 R-COMP-01 §2][03 R-FX-02 §3][03 §4.4][07 §4].
func (s classicSink) Sprite(sp drawlist.Sprite) {
	c := s.c
	// A non-nil PCX carries an opaque frontend background; it cannot ride
	// Sprite.Frame, so it is routed to the PCX writer regardless of Kind, honoring
	// the recorded clip [fmt pcx][07 "Retail palette contract"].
	if sp.PCX != nil {
		clipX, clipY, clipW, clipH := s.clip(sp.HasClip, sp.Clip)
		c.uiBlitPCXClippedRaw(sp.PCX, int(sp.X), int(sp.Y), clipX, clipY, clipW, clipH)
		return
	}
	switch sp.Kind {
	case drawlist.BlitKeyed:
		if sp.Anchored {
			if sp.Frame == nil {
				return
			}
			// The frame-anchor blit: UIBlitAnchor subtracts the frame's authored
			// offsets before skipping the transparent key [03 R-RAST-01 §6][fmt gaf].
			if sp.HasClip {
				clipX, clipY, clipW, clipH := s.clip(true, sp.Clip)
				c.uiBlitClippedRaw(sp.Frame, int(sp.X)-int(sp.Frame.XOffset), int(sp.Y)-int(sp.Frame.YOffset), clipX, clipY, clipW, clipH)
			} else {
				c.uiBlitAnchorRaw(sp.Frame, int(sp.X), int(sp.Y))
			}
		} else {
			// The plain keyed blit: the rectangle is the contract, no offset is
			// subtracted (UIBlit and the software cursor) [07 §4].
			clipX, clipY, clipW, clipH := s.clip(sp.HasClip, sp.Clip)
			c.uiBlitClippedRaw(sp.Frame, int(sp.X), int(sp.Y), clipX, clipY, clipW, clipH)
		}
	case drawlist.BlitTinted:
		// The translucent strip blit: each non-key source byte resolves the
		// destination to ALP[src*256+dst]. tintedBlitAnchor is the raw byte
		// writer; the strip call site emits and this is its only execution
		// [03 R-COMP-01 §2][03 R-FX-02 §2].
		if sp.HasClip {
			clipX, clipY, clipW, clipH := s.clip(true, sp.Clip)
			c.tintedBlitAnchorClipped(sp.Frame, int(sp.X), int(sp.Y), clipX, clipY, clipW, clipH)
		} else {
			c.tintedBlitAnchor(sp.Frame, int(sp.X), int(sp.Y))
		}
	case drawlist.BlitLit:
		// The shaded glyph blit: every opaque pixel is remapped through one LHT
		// row selected by LightRow. The palette rides the record (sp.Pal), so the
		// deferred replay resolves against exactly the palette the caller installed
		// [03 §4.3.1].
		clipX, clipY, clipW, clipH := s.clip(sp.HasClip, sp.Clip)
		c.uiBlitLitClippedRaw(sp.Frame, int(sp.X), int(sp.Y), sp.Pal, int(sp.LightRow), clipX, clipY, clipW, clipH)
	case drawlist.BlitScaled:
		// The surface-gadget blit: sample the source sub-rect Src across the
		// destination Dst, clipped to Clip [07 R-HUD-03 §11].
		clipX, clipY, clipW, clipH := s.clip(sp.HasClip, sp.Clip)
		c.uiBlitFrameSourceRectScaledClippedRaw(sp.Frame,
			int(sp.Src.X), int(sp.Src.Y), int(sp.Src.W), int(sp.Src.H),
			int(sp.Dst.X), int(sp.Dst.Y), int(sp.Dst.W), int(sp.Dst.H),
			clipX, clipY, clipW, clipH)
	case drawlist.BlitFeatureNormal:
		// Static feature bodies select ALP[source*256+destination] when animtrans is
		// set and the opaque keyed primitive otherwise. Live event cursors are
		// always recorded with Trans clear [03 R-RAST-01 §6]. X/Y is already the
		// final top-left, while the tinted helper accepts an anchor.
		if sp.Trans {
			if sp.Frame == nil {
				return
			}
			c.tintedBlitAnchor(sp.Frame,
				int(sp.X)+int(sp.Frame.XOffset),
				int(sp.Y)+int(sp.Frame.YOffset))
			return
		}
		c.blitGAFFrame(sp.Frame, int(sp.X), int(sp.Y))
	case drawlist.BlitFeatureShadow:
		// Feature shadows are two ordinary frame primitives. Static shadtrans=1
		// uses ALP[source*256+destination]; shadtrans=0 and live event cursors copy
		// every non-key source index opaquely [03 R-RAST-01 §6][03 §5.3.1]
		// [R-REN-03D §4]. The record carries an already-offset top-left, while the
		// tinted helper accepts an anchor, so add the authored offsets before it
		// subtracts them again.
		if sp.Trans {
			if sp.Frame == nil {
				return
			}
			c.tintedBlitAnchor(sp.Frame,
				int(sp.X)+int(sp.Frame.XOffset),
				int(sp.Y)+int(sp.Frame.YOffset))
			return
		}
		c.blitGAFFrame(sp.Frame, int(sp.X), int(sp.Y))
	}
}

// tintedBlitAnchorClipped is the target-window form of tintedBlitAnchor. The
// ordinary direct site uses the full framebuffer; composed child leaves carry
// their caller's target clip and must not broaden it through anchor placement
// [03 R-COMP-01 §2].
func (c *Client) tintedBlitAnchorClipped(f *formats.GAFFrame, x, y, clipX, clipY, clipW, clipH int) bool {
	if c == nil || f == nil || c.pal == nil || len(c.indexed) == 0 {
		return false
	}
	x -= int(f.XOffset)
	y -= int(f.YOffset)
	minX, minY := max(clipX, 0), max(clipY, 0)
	maxX, maxY := min(clipX+clipW, c.width), min(clipY+clipH, c.height)
	for row := 0; row < int(f.Height); row++ {
		py := y + row
		if py < minY || py >= maxY {
			continue
		}
		for col := 0; col < int(f.Width); col++ {
			px := x + col
			if px < minX || px >= maxX {
				continue
			}
			src, ok := f.At(col, row)
			if !ok {
				continue
			}
			idx := py*c.width + px
			c.indexed[idx] = c.pal.Alpha[int(src)*256+int(c.indexed[idx])]
		}
	}
	return true
}

// clip resolves a recorded Sprite clip into the (x, y, w, h) the raw writers
// take: the recorded rectangle when HasClip is set, otherwise the full
// framebuffer, exactly as the unclipped UIBlit/UIBlitPCX/UIBlitFrameScaled
// wrappers passed it.
func (s classicSink) clip(has bool, r drawlist.Rect) (x, y, w, h int) {
	if has {
		return int(r.X), int(r.Y), int(r.W), int(r.H)
	}
	return 0, 0, s.c.width, s.c.height
}

// Glyphs replays one FNT text run through the raw drawText rasterizer. The
// converted call sites — the health-bar control-group digit, the HUD/menu
// UIText/UITextWidth paths and the message column — emit and this is their only
// execution. The run carries its own retail control width in g.MaxWidth: the
// group digit records the zero value, so drawText still receives max-width 0
// (no truncation) exactly as its direct call did, while the text paths pass the
// authored gadget width [07 §7][03 R-FX-01 §6A]. No call installs a foreground
// of its own and none passes a per-glyph callback [03 §7.1].
func (s classicSink) Glyphs(g drawlist.Glyphs) {
	clipX, clipY, clipW, clipH := s.clip(g.HasClip, g.Clip)
	drawTextClipped(s.c.indexed, s.c.width, s.c.height, g.Font, g.Text, int(g.X), int(g.Y), int(g.MaxWidth), g.Color, clipX, clipY, clipW, clipH, nil)
}

// Fill replays one indexed rectangle. Solid and Outline are the plain,
// destination-independent writers; LitRect and ShadeRect are the
// destination-reading UI light/shade rects, both resolved against the active
// palette exactly as their direct calls were [03 §4.3.1][03 R-COMP-02 §5].
func (s classicSink) Fill(f drawlist.Fill) {
	switch f.Style {
	case drawlist.FillSolid:
		s.c.fillIndexedRect(int(f.Rect.X), int(f.Rect.Y), int(f.Rect.W), int(f.Rect.H), f.Index)
	case drawlist.FillOutline:
		s.c.frameIndexedRect(int(f.Rect.X), int(f.Rect.Y), int(f.Rect.W), int(f.Rect.H), f.Index)
	case drawlist.FillSolidInclusive:
		// The health bar's inclusive-bounds solid fill. Rect's extent form
		// encodes the inclusive span, so right = X+W-1 and bottom = Y+H-1
		// reconstruct the exact arguments the byte writer received; this is a
		// different writer from FillSolid's fillIndexedRect and must not be
		// folded onto it [03 R-FX-01 §6][R-P0-19-P].
		s.c.fillRectInclusive(f.Rect.X, f.Rect.Y, f.Rect.X+f.Rect.W-1, f.Rect.Y+f.Rect.H-1, f.Index)
	case drawlist.FillFrameInclusive:
		// The drag-selection rectangle's clipped inclusive one-pixel frame. Both
		// Rect and Clip carry inclusive spans in extent form; the frame writer
		// clips each edge independently against Clip [R-SEL-02A].
		r := Rect{MinX: f.Rect.X, MinY: f.Rect.Y, MaxX: f.Rect.X + f.Rect.W - 1, MaxY: f.Rect.Y + f.Rect.H - 1}
		clip := Rect{MinX: f.Clip.X, MinY: f.Clip.Y, MaxX: f.Clip.X + f.Clip.W - 1, MaxY: f.Clip.Y + f.Clip.H - 1}
		drawIndexedFrameInclusive(s.c.indexed, s.c.width, s.c.height, r, f.Index, clip)
	case drawlist.FillLitRect:
		// The UI light rect brightens each destination pixel through one LHT row
		// selected by the level; uiLightRectRaw is the byte writer and the light
		// rect's call site emits, so this is its only execution. The palette the
		// caller passed rides the record (f.Pal), so the lookup is against exactly
		// the palette the direct call used, even under deferred replay [03 §4.3.1].
		s.c.uiLightRectRaw(f.Pal, int(f.Rect.X), int(f.Rect.Y), int(f.Rect.W), int(f.Rect.H), int(f.Level))
	case drawlist.FillShadeRect:
		// The UI shade rect folds each destination pixel through the signed fade
		// table — SHD for a negative level, LHT otherwise — via uiShadeRectRaw,
		// the byte writer the shade rect's call site emits into. The caller's
		// palette rides the record (f.Pal) as for FillLitRect [03 R-COMP-02 §5].
		s.c.uiShadeRectRaw(f.Pal, int(f.Rect.X), int(f.Rect.Y), int(f.Rect.W), int(f.Rect.H), int(f.Level))
	}
}

// Line replays one indexed line through the raw Bresenham primitive; the beam
// and segment call sites emit and this is their only execution [03 §5.4].
func (s classicSink) Line(l drawlist.Line) {
	s.c.drawIndexedLine(l.X0, l.Y0, l.X1, l.Y1, l.Index)
}

// Points replays one batch of single-pixel writes. PointPlain writes the byte
// straight in; PointLit brightens each destination pixel through the LHT row
// carried in Point.Index — the ground halo and the calculated flash disc, both
// destination-reading, resolved here against c.indexed exactly as the inline
// writers did [03 §4.3.1][03 R-FX-01 §4].
func (s classicSink) Points(p drawlist.Points) {
	c := s.c
	// The batches are clipped to the RECORD extent, which is wider than the
	// framebuffer whenever the modern executor is zoomed out
	// (docs/DESIGN_GPU_RENDERER.md §16.3). Classic never records at such a
	// factor itself, but a `--shot-renderer both` capture replays one list
	// through both executors, so the byte writer bounds its own store rather
	// than trusting the recorder's clip.
	w, h := int32(c.width), int32(c.height)
	switch p.Kind {
	case drawlist.PointLit:
		if c.pal == nil {
			return
		}
		for _, pt := range p.Points {
			if pt.X < 0 || pt.X >= w || pt.Y < 0 || pt.Y >= h {
				continue
			}
			idx := int(pt.Y)*c.width + int(pt.X)
			c.indexed[idx] = c.pal.LightLookup(int(pt.Index), c.indexed[idx])
		}
	default: // PointPlain
		for _, pt := range p.Points {
			if pt.X < 0 || pt.X >= w || pt.Y < 0 || pt.Y >= h {
				continue
			}
			c.indexed[int(pt.Y)*c.width+int(pt.X)] = pt.Index
		}
	}
}

// Model replays one composed model subject's commit: the two writes into
// c.indexed that finish a model, then the diagnostic trace that reads it. The
// composition (compose/rasterize/shadow-rasterize/waterline/digger/reveal/
// outline) is already complete before recording; this runs only what the old
// finishModel body ran, in the same order (docs/DESIGN_GPU_RENDERER.md §2.1
// C-G5). The record owns the two finished planes, so no client model lookup is
// involved.
//
// The shadow is composed and blitted before the body for the same subject
// [03 §5.3]. It reads the finished body image to punch the body's own silhouette
// out of itself [R-REN-03D §5][R-RAST-01 §4], which is why it runs after the
// raster and the anti-alias resolve rather than first. The punch reads the body,
// not the staging image: the hole is the carrier's own silhouette. The trace
// resolve/emit runs last because it reads c.indexed after the body commit
// [03 R-REN-03A].
func (s classicSink) Model(m drawlist.Model) {
	c := s.c
	if c == nil || m.Classic == nil {
		return
	}
	if m.Classic.Shadow != nil && c.pal != nil {
		classicModelTarget(m.Classic.Shadow).tintedCommit(c.indexed, c.width, c.height, &c.pal.Alpha)
	}
	if m.Classic.Body != nil {
		classicModelTarget(m.Classic.Body).commit(c.indexed, c.width, c.height)
	}
	if m.Classic.Observer != nil {
		// The observer receives a snapshot, never the writable framebuffer. This
		// makes trace publication incapable of changing the recorded pixel order.
		m.Classic.Observer(m.Classic.Trace, append([]byte(nil), c.indexed...), c.width, c.height)
	}
}

// Fog replays one clipped fog op list into the indexed surface. The op-list
// building (BuildFogOpsWindowInto and the fog cache/GAF setup) stays in
// drawFog; this method runs the per-op clip and the three fog fills and the fog
// GAF blit the direct loop ran [03 §3.3].
func (s classicSink) Fog(fg drawlist.Fog) {
	c := s.c
	w := c.width
	h := c.height
	for _, op := range fg.Ops {
		x0, y0, x1, y1 := op.ScreenX0, op.ScreenY0, op.ScreenX1, op.ScreenY1
		// Rebase from retail viewport origin (128,32) to Nanolathe full-window shell origin (0,0)
		// so fog aligns with terrain blitted via BlitTerrainOrigin 0,0 [03 §2.5][PLAN_04A C1].
		if c.cam != nil {
			x0 -= camera.OriginX
			y0 -= camera.OriginY
			x1 -= camera.OriginX
			y1 -= camera.OriginY
		}
		// DIVERGENCE from DESIGN_GPU_RENDERER §14.2, made on the evidence of a
		// 2x capture. The section has the GAF families tile the authored 32x32
		// frame s-by-s times across the scaled cell so the dithered checker
		// stays one pixel. In this build the checker is a test on the
		// DESTINATION pixel — blitFogGAF skips where (dstX+x + dstY + parity)
		// is even — so it is already one pixel at any scale, and tiling instead
		// stamps four copies of a cloud edge authored for one cell: the 2x
		// capture showed a black cross across every fogged boundary cell. The
		// cell therefore takes the frame's 2x VARIANT in one blit, which keeps
		// the cloud shape aligned to the scaled cell and keeps the checker one
		// pixel wide for the reason the section gives. The variant's authored
		// offsets are doubled with it, so the anchor arithmetic below is
		// unchanged.
		if x0 < 0 {
			x0 = 0
		}
		if y0 < 0 {
			y0 = 0
		}
		if x1 > int32(w) {
			x1 = int32(w)
		}
		if y1 > int32(h) {
			y1 = int32(h)
		}
		if x0 >= x1 || y0 >= y1 {
			continue
		}
		switch op.Kind {
		case render.FogKindSolidDark:
			// lo==15 short-circuit: fill the clipped cell with black [03 §3.3].
			c.fogFillSolid(x0, y0, x1, y1)
		case render.FogKindGrayRemap:
			// hi==15 fogged-but-explored: remap existing pixels through the
			// gray-table LUT; terrain texture is preserved and desaturated
			// [03 §3.3][03 §4.3.3]. Retail applies the LUT to physical
			// screen indices; c.indexed holds physical indices, with semantic
			// colours already resolved by their command producers.
			c.fogFillGray(x0, y0, x1, y1)
		case render.FogKindPatterned:
			// hi==15 dithered checker uses parity (camX+camZ)&1 [03 §3.3].
			// Retail writes literal palette index 0 (black) at checker
			// positions (x+y+parity)&1==1 and leaves the rest untouched
			// [R-RR16-A §2].
			parity := int32(0)
			if c.cam != nil {
				parity = (c.cam.X + c.cam.Z) & 1
			}
			c.fogFillChecker(x0, y0, x1, y1, parity)
		case render.FogKindGAFCh1:
			// hi 1..14: Gray family GAF, plain or patterned [03 §3.3].
			if c.fogGAF != nil && op.Variant >= 0 && op.Variant < 4 && op.Frame >= 0 {
				entry := c.fogGray[op.Variant]
				if entry != nil && op.Frame < len(entry.Frames) && entry.Frames[op.Frame].Frame != nil {
					frame := entry.Frames[op.Frame].Frame
					mode := fogBlitGray
					if op.Patterned {
						mode = fogBlitPatterned
					}
					// Plain Gray family is a masked GRAY TABLE remap of the
					// destination, not a copy of the source art [R-RR16-A §1].
					c.blitFogGAF(c.viewFrame(frame), int(x0), int(y0), mode)
					continue
				}
			}
			// Missing GAF entry/frame: retail skips the blit and the cell
			// keeps the underlying tile [03 §3.3].
			continue
		case render.FogKindGAFCh0:
			// lo 1..14: Black family is always plain [03 §3.3].
			if c.fogGAF != nil && op.Variant >= 0 && op.Variant < 4 && op.Frame >= 0 {
				entry := c.fogBlack[op.Variant]
				if entry != nil && op.Frame < len(entry.Frames) && entry.Frames[op.Frame].Frame != nil {
					frame := entry.Frames[op.Frame].Frame
					c.blitFogGAF(c.viewFrame(frame), int(x0), int(y0), fogBlitBlack)
					continue
				}
			}
			// Missing GAF: skip, cell keeps underlying tile [03 §3.3].
			continue
		default:
			// Visible cells produce no ops; nothing to draw.
			continue
		}
	}
}

// Surface replays one indexed byte surface blit (the SELMAP MAPPIC gadget,
// WU-1.7b). The UIBlitIndexed call site emits and the clipped raw writer runs
// here as its only execution; Dst remains the source-mapping rectangle.
func (s classicSink) Surface(sf drawlist.Surface) {
	clipX, clipY, clipW, clipH := s.clip(sf.HasClip, sf.Clip)
	s.c.uiBlitIndexedClippedRaw(sf.Pixels, int(sf.SrcW), int(sf.SrcH), int(sf.Dst.X), int(sf.Dst.Y), int(sf.Dst.W), int(sf.Dst.H), clipX, clipY, clipW, clipH)
}

// Cursor replays the software cursor blit [07 §8]. drawCursor's call site emits
// and this is its only execution. It calls uiBlitClippedRaw directly — not the
// now-emitting UIBlit — so replaying the Cursor record writes the cursor once and
// records nothing further; the arguments are the plain full-framebuffer keyed
// blit UIBlit(cu.Frame, HotX, HotY) resolved to (0,0,width,height), byte-identical
// to the former direct call. HotX/HotY are the blit origin drawCursor already
// resolved from the hotspot, so this passes them straight through.
func (s classicSink) Cursor(cu drawlist.Cursor) {
	s.c.uiBlitClippedRaw(cu.Frame, int(cu.HotX), int(cu.HotY), 0, 0, s.c.width, s.c.height)
}

// Expand performs the single index-to-RGBA expansion pass, the one command
// recorded and reached in this unit (C-G8, docs/DESIGN_GPU_RENDERER.md §2.2).
// It writes exactly the bytes the direct call to convertIndexedToRGBA did.
func (s classicSink) Expand() { s.c.convertIndexedToRGBA() }
