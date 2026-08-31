package client

import (
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/visibility"
	"github.com/nanolathe/nanolathe/internal/world"
)

// visibilityGridSize validates a published mask before a projected cell is
// used. A malformed publication cannot be interpreted as visible data [03
// §3.1][03 §5.4].
func visibilityGridSize(w, h int32, length int) (int, bool) {
	if w <= 0 || h <= 0 {
		return 0, false
	}
	n := int64(w) * int64(h)
	maxInt := int64(^uint(0) >> 1)
	if n <= 0 || n > maxInt || int(n) != length {
		return 0, false
	}
	return int(n), true
}

// projectileVisible is the one-point projectile gate from the immutable
// local-player coverage grid [03 §3.2][03 §5.4]. The published mode selects
// current byte coverage or the history word mask; an invalid/missing source
// rejects the projectile rather than exposing it.
// The frame path supplies the committed local-player slot explicitly.
func projectileVisible(v frame.VisibilityView, localPlayer uint8) func(frame.ProjectileView) bool {
	mode := uint8(0)
	if v.CoverageBytes {
		mode = ProjectileVisibilityModeBytes
	}
	return func(p frame.ProjectileView) bool {
		// The local player table has exactly ten usable slots [03 §3.1].
		// Keep the wrapper's contract narrower than the generic uint16 mask
		// helper, which also serves contexts that address reserved bits.
		if localPlayer >= 10 {
			return false
		}
		return ProjectileVisible(v, p, mode, localPlayer)
	}
}

// fogUnexploredUnit reports whether a unit's anchor visibility tile is never-explored [03 §3.3][03 §3.3].
// It checks Fog Ch0 ==15 (solid dark, all four neighbours fogged) via the
// immutable FogView, which is the presentation equivalent of the plot flag
// 0x04 [03 §3.3]. Invalid or missing fog is treated as unexplored so foreign
// content cannot be exposed by an incomplete publication.
func fogUnexploredUnit(fog frame.FogView, u frame.UnitView) bool {
	if !fog.Valid {
		// TODO(question): whether retail emits a pre-first-frame fog view is not established; fail closed until a committed cache exists [03 §3.3].
		return true
	}
	if _, ok := visibilityGridSize(fog.W, fog.H, len(fog.Ch0)); !ok {
		return true
	}
	tx := world.WorldToTile(u.X) - fog.OriginX
	tz := world.WorldToTile(u.Z) - fog.OriginZ
	if tx < 0 || tz < 0 || tx >= fog.W || tz >= fog.H {
		return true
	}
	idx := int(tz*fog.W + tx)
	return fog.Ch0[idx] == 15
}

// fogUnexploredFeature reports whether a feature's anchor cell maps to an unexplored fog tile [03 §3.3][03 §3.3].
// Feature CX/CZ are cell coordinates; fog is per visibility tile (2x2 cells) so tile = cell>>1 [03 §2.1][03 §3.1]. Invalid fog is treated as unexplored so an incomplete publication cannot expose content.
func fogUnexploredFeature(fog frame.FogView, f frame.FeatureView) bool {
	if !fog.Valid {
		// TODO(question): whether retail emits a pre-first-frame fog view is not established; fail closed until a committed cache exists [03 §3.3].
		return true
	}
	if _, ok := visibilityGridSize(fog.W, fog.H, len(fog.Ch0)); !ok {
		return true
	}
	tx := (f.CX >> 1) - fog.OriginX
	tz := (f.CZ >> 1) - fog.OriginZ
	if tx < 0 || tz < 0 || tx >= fog.W || tz >= fog.H {
		return true
	}
	idx := int(tz*fog.W + tx)
	return fog.Ch0[idx] == 15
}

// unitVisibleForFrame is the enemy-visibility predicate for the painter [03 §3.2] C8.
// Own units always pass (owner bypass) [03 §3.2] step1; invalid visibility
// rejects foreign units; otherwise it delegates to SnapshotVisible, preserving
// the hidden/cloaked early-out before the selected visibility source.
func unitVisibleForFrame(frame *frame.Frame, u frame.UnitView, viewer uint8) bool {
	if frame == nil {
		return false
	}
	if viewer >= 10 {
		return false
	}
	if u.Owner == viewer {
		return true
	}
	if !frame.Visibility.Valid {
		return false
	}
	return SnapshotVisible(frame, u, viewer)
}

// Frame draws one frame from the currently committed tick; it never mutates
// simulation state and never interpolates between publications (I6).
//
// Framebuffer path:
//  1. Compose into own []uint8 indexed framebuffer at logical size.
//  2. Convert every indexed pixel through Logical→Base (C7). GUI semantic
//     colors are translated before they are written by the menu layer; image
//     bytes are never treated as GUIPAL source colors.
//  3. The backend (backend_ebiten.go) uploads the RGBA bytes and presents them.
func (c *Client) Frame() {
	if c == nil {
		return
	}
	// Audio: drain queue once per rendered frame outside simulation [03 §8.3] C18.
	// Presentation-only; uses CRT stream [03 §8.3] C19 [I4]; never touches Sim RNG.
	cur := c.buffer.Current()
	ok := cur != nil
	// Keep audio viewport in sync with camera for positional pan/attenuation [03 §8.3].
	if c.cam != nil {
		c.UpdateAudioViewportFromCamera()
	}
	c.TickAudio()

	// C9: read the committed frame only; intermediate ticks are not drawn
	// (PLAN_03 C15). A paused simulation simply presents the same frame.
	c.composeIndexed(cur, ok)
	// The software cursor is drawn after the offscreen battle/front-end surface
	// is prepared, so it sits above world, HUD, and modal overlays [07 §8].
	c.drawCursor()

	// Convert to RGBA through logical→base at present time only (C7). This is
	// the only point where indexed pixels become RGBA so palette animation
	// stays possible in later phases.
	c.convertIndexedToRGBA()
}

// composeIndexed runs the one concrete committed-frame ordering and leaves the
// indexed surface ready for cursor and palette presentation [03 §1][I6].
func (c *Client) composeIndexed(cur *frame.Frame, ok bool) {
	if c == nil || len(c.indexed) != c.width*c.height {
		return
	}
	c.selectionChrome = c.selectionChrome[:0]
	c.drawCommittedFrame(cur, ok)
}

// selectionChrome is the frame's record of which units the two unit passes
// actually presented, in paint order. It no longer carries a draw of its own:
// the health bar it used to feed is now the label walk of [03 R-FX-01 §6],
// which runs before the fog composite and derives its own geometry.
type selectionChrome struct {
	view    frame.UnitView
	screenX int32
	screenY int32
}

func (c *Client) drawTerrainPrep() {
	if c == nil || len(c.indexed) != c.width*c.height {
		return
	}
	if c.cam != nil && c.terrain != nil {
		BlitTerrain(c.indexed, c.width, c.height, c.terrain, c.cam)
	}
	// A frontend without terrain remains the cleared indexed surface. Retail
	// does not define a synthetic gradient fallback [I9].
}

func (c *Client) drawInterface(cur *frame.Frame) {
	if c == nil || len(c.indexed) != c.width*c.height {
		return
	}
	if c.uiStage != nil {
		c.uiStage.DrawUI(c, UIFrame{Committed: cur})
	}
}

func (c *Client) drawProjectiles(cur *frame.Frame) {
	if c == nil || cur == nil || c.cam == nil || len(cur.Projectiles) == 0 {
		return
	}
	// Missing projectile GAF metadata suppresses that instruction through the
	// resolver; admission stays open so it cannot abort unrelated projectiles.
	c.DrawProjectileViews(cur.Projectiles, cur.Tick, projectileVisible(cur.Visibility, cur.Selection.LocalPlayer), func(frame.ProjectileView) bool { return true }, c.projectileDispatchOptions())
}

func (c *Client) drawEffects(cur *frame.Frame) {
	if c == nil || cur == nil || c.cam == nil {
		return
	}
	// Nano segments are emitters, not sprites: they advance once per committed
	// tick and then paint their live particles [03 §5.5].
	c.tickNanolathe(cur)
	c.drawNanolathe(cur)
	if len(cur.Effects) == 0 {
		return
	}
	c.DrawEffectViews(cur.Effects, c.effectDrawOptions())
}

func (c *Client) drawFog(cur *frame.Frame) {
	ok := cur != nil
	w := c.width
	h := c.height
	if len(c.indexed) != w*h {
		return
	}
	// Fog presentation [03 §3.3] C13 — reads snapshot fog cache copied from visibility.Service.Fog() each tick (I6).
	// The cache is presentation-only and never writes sim state. Fog uses hard 32-pixel tiles [03 §3.3][03 §3.3].
	if ok && cur != nil && cur.Fog.Valid && c.cam != nil {
		if _, valid0 := visibilityGridSize(cur.Fog.W, cur.Fog.H, len(cur.Fog.Ch0)); !valid0 {
			return
		}
		if _, valid1 := visibilityGridSize(cur.Fog.W, cur.Fog.H, len(cur.Fog.Ch1)); !valid1 {
			return
		}
		if c.fogCache == nil {
			c.fogCache = visibility.NewFogCacheFromChannelsAt(cur.Fog.W, cur.Fog.H, cur.Fog.OriginX, cur.Fog.OriginZ, cur.Fog.Ch0, cur.Fog.Ch1)
		} else if !c.fogCache.ReplaceChannelsAt(cur.Fog.W, cur.Fog.H, cur.Fog.OriginX, cur.Fog.OriginZ, cur.Fog.Ch0, cur.Fog.Ch1) {
			return
		}
		c.ensureFogGAF()
		c.fogOps = render.BuildFogOpsInto(c.fogOps, c.fogCache, c.cam, c.cam.ViewW, c.cam.ViewH, cur.Fog.W, cur.Fog.H, c.pal, c.ditheredFog)
		ops := c.fogOps
		for _, op := range ops {
			x0, y0, x1, y1 := op.ScreenX0, op.ScreenY0, op.ScreenX1, op.ScreenY1
			// Rebase from retail viewport origin (128,32) to Nanolathe full-window shell origin (0,0)
			// so fog aligns with terrain blitted via BlitTerrainOrigin 0,0 [03 §2.5][PLAN_04A C1].
			if c.cam != nil {
				x0 -= camera.OriginX
				y0 -= camera.OriginY
				x1 -= camera.OriginX
				y1 -= camera.OriginY
			}
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
				for py := y0; py < y1; py++ {
					base := int(py)*w + int(x0)
					for px := x0; px < x1; px++ {
						c.indexed[base+int(px-x0)] = render.FogDarkPaletteIndex
					}
				}
			case render.FogKindGrayRemap:
				// hi==15 fogged-but-explored: remap existing pixels through the
				// gray-table LUT; terrain texture is preserved and desaturated
				// [03 §3.3][03 §4.3.3]. Retail applies the LUT to physical
				// screen indices; c.indexed holds logical indices and Logical is
				// identity until animated, so the direct application matches.
				if c.pal != nil {
					for py := y0; py < y1; py++ {
						base := int(py)*w + int(x0)
						for px := x0; px < x1; px++ {
							i := base + int(px-x0)
							c.indexed[i] = c.pal.Gray[c.indexed[i]]
						}
					}
				}
			case render.FogKindPatterned:
				// hi==15 dithered checker uses parity (camX+camZ)&1 [03 §3.3].
				// Retail writes literal palette index 0 (black) at checker
				// positions (x+y+parity)&1==1 and leaves the rest untouched
				// [R-RR16-A §2].
				parity := int32(0)
				if c.cam != nil {
					parity = (c.cam.X + c.cam.Z) & 1
				}
				for py := y0; py < y1; py++ {
					base := int(py)*w + int(x0)
					for px := x0; px < x1; px++ {
						if (px+py+parity)&1 != 1 {
							continue
						}
						c.indexed[base+int(px-x0)] = render.FogDarkPaletteIndex
					}
				}
			case render.FogKindGAFCh1:
				// hi 1..14: Gray family GAF, plain or patterned [03 §3.3].
				if c.fogGAF != nil && op.Variant >= 0 && op.Variant < 4 && op.Frame >= 0 {
					entry := c.fogGray[op.Variant]
					if entry != nil && op.Frame < len(entry.Frames) && entry.Frames[op.Frame].Frame != nil {
						frame := entry.Frames[op.Frame].Frame
						if op.Patterned {
							c.blitFogGAF(frame, int(x0), int(y0), fogBlitPatterned)
						} else {
							// Plain Gray family is a masked GRAY TABLE remap of the
							// destination, not a copy of the source art [R-RR16-A §1].
							c.blitFogGAF(frame, int(x0), int(y0), fogBlitGray)
						}
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
						c.blitFogGAF(frame, int(x0), int(y0), fogBlitBlack)
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
	return
	// No world stage has work outside the explicit adapters above.
}

// drawSelectionStage emits the drag-selection rectangle, which the composer
// draws after the fog presentation together with the rest of the interface
// work [03 §1].
//
// Correction: this comment previously said the stage "emits unit selection and
// health chrome after fog" and that health pixels had to stay "visible above
// the fog overlay as required by the frame contract". No frame contract
// requires that. [03 §1] item 9 puts the unit labels — the health bar and the
// group digit — between the strip-8 and strip-9 walks, and item 10 puts fog
// after all ten strips, so retail's bars are drawn *under* the fog composite,
// not over it. The bar this stage used to draw was invented besides: it took
// its geometry from the footprint box, drew for any owner, and gated on
// "selected or damaged". It is deleted; [03 R-FX-01 §6]'s raster replaces it
// in drawUnitLabels, at the barrier the composer actually uses.
//
// The per-unit footprint quad is not part of this stage either: it belongs to
// the unit's own depth slot in the world pass, before the fog composite
// [03 R-WATER-01 §1].
func (c *Client) drawSelectionStage() {
	if c == nil {
		return
	}
	c.drawSelectionDrag()
}

// convertIndexedToRGBA converts the indexed framebuffer to RGBA at present
// time only. The indexed framebuffer contains active PALETTE.PAL indices:
// GAF, PCX, TNT, FNT, and direct primitive writers all follow the same route.
// GUI semantic colors are resolved through the logical→physical map by the
// caller before FNT/primitives write, and "no GUI lookup is performed again
// during indexed-to-RGB presentation" [03 §4.3][07 "Retail palette contract"].
func (c *Client) convertIndexedToRGBA() {
	if len(c.indexed)*4 != len(c.rgba) {
		return
	}
	for i, idx := range c.indexed {
		c.rgba[i*4+0] = c.base[idx][0]
		c.rgba[i*4+1] = c.base[idx][1]
		c.rgba[i*4+2] = c.base[idx][2]
		c.rgba[i*4+3] = c.base[idx][3]
		// Ensure opaque; PALETTE.PAL's fourth byte is reserved zero.
		if c.rgba[i*4+3] == 0 {
			c.rgba[i*4+3] = 255
		}
	}
}
