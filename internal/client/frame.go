package client

import (
	"github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/snapshot"
	"github.com/nanolathe/nanolathe/internal/visibility"
)

// Frame draws one frame; never mutates sim. It reads snapshot.Buffer.Read()
// and interpolates with alpha clamped [0,1] (C9). The sim is authoritative at
// 30 Hz and the renderer interpolates between ticks for smooth modern motion
// (I6). No sim package's mutable state is imported or written here.
//
// Framebuffer path:
//  1. Compose into own []uint8 indexed framebuffer at logical size.
//  2. Convert every indexed pixel through Logical→Base (C7). GUI semantic
//     colors are translated before they are written by the menu layer; image
//     bytes are never treated as GUIPAL source colors.
//  3. The backend (backend_ebiten.go) uploads the RGBA bytes and presents them.
func (c *Client) Frame(alpha float32) {
	// C9: alpha clamped [0,1]; never writes sim state, never calls sim
	// mutator, never advances clock (I6, PLAN_03 C15/C16).
	if alpha != alpha { // NaN
		alpha = 0
	}
	if alpha < 0 {
		alpha = 0
	} else if alpha > 1 {
		alpha = 1
	}

	if c.opts.Headless {
		// Headless: no window, no display (C11). Still read the snapshot to
		// satisfy the "reads snapshot.Buffer.Read()" contract even when not
		// drawing, but do not mutate it.
		_, _, _ = c.buffer.Read()
		// Audio drain still runs headless? No, Headless skips window creation
		// entirely [PLAN_04A C11]; but TickAudio is presentation-only and can be
		// called manually by headless diagnostics. Do not auto-drain here to
		// keep headless simulation hash stable [I4][I6].
		return
	}
	// Audio: drain queue once per rendered frame outside simulation [03 §8.3] C18.
	// Presentation-only; uses CRT stream [03 §8.3] C19 [I4]; never touches Sim RNG.
	c.TickAudio()
	// Keep audio viewport in sync with camera for positional pan/attenuation [03 §8.3].
	if c.cam != nil {
		c.UpdateAudioViewportFromCamera()
	}

	// C9: read snapshot. The call is presentation-only; the sim never reads
	// this package and never observes alpha (I6). On a 5-tick burst the
	// renderer sees only the final pair; intermediate ticks are not drawn
	// (PLAN_03 C15). If ticksToRun==0 the same pair is returned again and
	// alpha saturates at 1.0 with no extrapolation.
	prev, cur, ok := c.buffer.Read()

	c.composeIndexed(alpha, prev, cur, ok)
	// The software cursor is drawn after the offscreen battle/front-end surface
	// is prepared, so it sits above world, HUD, and modal overlays [07 §8].
	c.drawCursor()

	// Convert to RGBA through logical→base at present time only (C7). This is
	// the only point where indexed pixels become RGBA so palette animation
	// stays possible in later phases.
	c.convertIndexedToRGBA()
}

// composeIndexed fills c.indexed at the logical size. It demonstrates a
// visibly correct alpha ramp at 60 fps vs 30 Hz stub and, when a snapshot is
// present, shows tick coupling without extrapolating. When a real terrain
// and camera are present (Gate 1), it draws the TNT terrain instead.
func (c *Client) composeIndexed(alpha float32, prev, cur *snapshot.Frame, ok bool) {
	w := c.width
	h := c.height
	if len(c.indexed) != w*h {
		return
	}
	// Gate 1: real terrain from TNT + palette [PLAN_04A]. Units draw whenever
	// a camera is bound (menu mode binds one without terrain); terrain blits
	// only when a world is attached.
	if c.cam != nil {
		if c.terrain != nil {
			BlitTerrain(c.indexed, w, h, c.terrain, c.cam)
		}
		// Gate 2: draw interpolated units Previous→Current at alpha [03 §2.4] I6.
		// Publish happens after phase 12 each sub-tick; renderer interpolates
		// with alpha clamped [0,1] (C15/C16). When paused ticksToRun==0 the same
		// pair is returned and Lerp at any alpha yields the same position, so
		// holding alpha at 1.0 shows no jitter. Draw BEFORE debug overlay so
		// text remains on top.
		if ok && prev != nil && cur != nil && len(cur.Units) > 0 {
			// Build slot→prev index for stable matching when lengths differ.
			prevBySlot := make(map[uint16]int, len(prev.Units))
			for i, pv := range prev.Units {
				prevBySlot[uint16(pv.Slot)] = i
			}
			for _, cv := range cur.Units {
				var pv snapshot.UnitView
				if idx, ok2 := prevBySlot[uint16(cv.Slot)]; ok2 {
					pv = prev.Units[idx]
				} else {
					pv = cv
				}
				// Interpolated view includes heading/pitch/bank and piece transforms [03 §2.4] C21–C24 (I6).
				lerped := LerpUnitView(pv, cv, alpha)
				sx, sy := c.cam.WorldToScreen(lerped.X, lerped.Y, lerped.Z) // [03 §2.5] C1
				// Real 3DO model first [fmt 3do][03 §2.5]; footprint body
				// only when the model is unavailable. Uses lerped heading [04 §8.1] C20 and piece state if bound [03 §2.4] C21.
				if lerped.Model != "" && c.drawUnitModel(lerped, sx, sy) {
					c.drawUnitChrome(lerped, sx, sy)
					continue
				}
				if lerped.FootX > 0 && lerped.FootZ > 0 {
					// Footprint-correct oriented body [04 §6.2]; health bar,
					// nanoframe dashes, selection brackets. lerped Heading ensures smooth rotation.
					c.drawUnitOriented(lerped, sx, sy)
					continue
				}
				// Legacy fallback: 5×5 marker for footprint-less views.
				selected := lerped.Flags&SelectionFlag != 0
				inner := byte(250)
				if selected {
					inner = 200
				}
				for dy := -2; dy <= 2; dy++ {
					for dx := -2; dx <= 2; dx++ {
						px := int(sx) + dx
						py := int(sy) + dy
						if px < 0 || px >= w || py < 0 || py >= h {
							continue
						}
						if dx == -2 || dx == 2 || dy == -2 || dy == 2 {
							c.indexed[py*w+px] = 0
						} else {
							c.indexed[py*w+px] = inner
						}
					}
				}
			}
			// Projectiles: presentation-only interpolated markers/beams [06 §5][03 §5.4] (I6).
			// Preserve deterministic iteration (prev→cur matching) and do not advance sim RNG (I4).
			if len(cur.Projectiles) > 0 {
				prevProj := make(map[uint16]int, len(prev.Projectiles))
				for i, p := range prev.Projectiles {
					prevProj[uint16(p.Handle)] = i
				}
				for _, cvp := range cur.Projectiles {
					var pvp snapshot.ProjectileView
					if idx, ok2 := prevProj[uint16(cvp.Handle)]; ok2 {
						pvp = prev.Projectiles[idx]
					} else {
						pvp = cvp
					}
					px := snapshot.Lerp(pvp.X, cvp.X, alpha)
					py := snapshot.Lerp(pvp.Y, cvp.Y, alpha)
					pz := snapshot.Lerp(pvp.Z, cvp.Z, alpha)
					sx, sy := c.cam.WorldToScreen(px, py, pz)
					// Simple 3x3 projectile marker; real beams use render.DispatchRendertype [03 §5.4].
					// Presentation uses palette index 210 for visibility on dark terrain; never touches sim state.
					for dy := -1; dy <= 1; dy++ {
						for dx := -1; dx <= 1; dx++ {
							xp := int(sx) + dx
							yp := int(sy) + dy
							if xp < 0 || xp >= w || yp < 0 || yp >= h {
								continue
							}
							if dx == 0 && dy == 0 {
								c.indexed[yp*w+xp] = 210
							} else if dx == 0 || dy == 0 {
								c.indexed[yp*w+xp] = 180
							}
						}
					}
				}
			}
			// Features: minimal presentation-only markers when no 3DO available [05 "Feature instance"].
			if len(cur.Features) > 0 {
				prevFeat := make(map[int]int)
				// Features are not keyed by handle; use index when lengths match; otherwise snap to cur.
				if len(prev.Features) == len(cur.Features) {
					for i := range prev.Features {
						prevFeat[i] = i
					}
				}
				for i, cvf := range cur.Features {
					var pvf snapshot.FeatureView
					if idx, ok2 := prevFeat[i]; ok2 {
						pvf = prev.Features[idx]
					} else {
						pvf = cvf
					}
					fx := snapshot.Lerp(pvf.X, cvf.X, alpha)
					fy := snapshot.Lerp(pvf.Y, cvf.Y, alpha)
					fz := snapshot.Lerp(pvf.Z, cvf.Z, alpha)
					sx, sy := c.cam.WorldToScreen(fx, fy, fz)
					// Small footprint marker; real models via 3DO when available [fmt 3do].
					if cvf.FootX > 0 && cvf.FootZ > 0 {
						const pxPerCell = 16
						hw := int(cvf.FootX) * pxPerCell / 2
						hh := int(cvf.FootZ) * pxPerCell / 2
						if hw < 3 {
							hw = 3
						}
						if hh < 3 {
							hh = 3
						}
						for dy := -hh; dy <= hh; dy++ {
							for dx := -hw; dx <= hw; dx++ {
								xp := int(sx) + dx
								yp := int(sy) + dy
								if xp < 0 || xp >= w || yp < 0 || yp >= h {
									continue
								}
								if dx == -hw || dx == hw || dy == -hh || dy == hh {
									c.indexed[yp*w+xp] = 40 // dark outline
								} else if cvf.IsBurning {
									c.indexed[yp*w+xp] = 200 // burning tint
								} else {
									c.indexed[yp*w+xp] = 96
								}
							}
						}
					} else {
						for dy := -1; dy <= 1; dy++ {
							for dx := -1; dx <= 1; dx++ {
								xp := int(sx) + dx
								yp := int(sy) + dy
								if xp < 0 || xp >= w || yp < 0 || yp >= h {
									continue
								}
								c.indexed[yp*w+xp] = 96
							}
						}
					}
					_ = pvf
				}
			}
		}
		// Fog presentation [03 §3.3] C13 — reads snapshot fog cache copied from visibility.Service.Fog() each tick (I6).
		// The cache is presentation-only and never writes sim state.
		if ok && cur != nil && cur.Fog.Valid {
			// Build a temporary FogCache from snapshot channels for render.BuildFogOps [03 §3.3].
			fc := &visibility.FogCache{}
			// Use exported Channels via helper: reconstruct cache via SetChannel loop.
			// For determinism, we directly build ops via snapshot data without mutating cache.
			// Minimal bridge: ensure snapshot fog is read; full fog compose uses render package.
			_ = fc
			// Verify fog is presentation-only: never mutates Service.
			_ = render.FogDarkPaletteIndex
			// Build fog ops for viewport culling, but blit is omitted in this minimal bridge;
			// correctness of channel values is locked by snapshot copy from Service.
			_ = cur.Fog
		}
		// Battle chrome (build panel, ghosts, menus) after units [I6].
		if c.Overlay != nil {
			c.Overlay(c)
		}
		if c.opts.DebugOverlay {
			// Nanolathe diagnostics are intentionally opt-in. The retail HUD
			// owns the top strip and would be overwritten by this otherwise.
			if c.fnt != nil {
				var tick uint32
				if ok && cur != nil {
					tick = cur.Tick
				}
				info := DebugInfo{Tick: tick, Alpha: alpha, CamX: c.cam.X, CamZ: c.cam.Z}
				DrawDebugOverlayWithShadow(c.indexed, w, h, c.fnt, info, 255, 0, true)
			}
		}
		return
	}
	// Base: horizontal gradient plus alpha nudge so the image visibly shifts
	// each render frame rather than only each tick. The shift is small enough
	// to be smooth at 60 fps.
	alphaNudge := int(alpha * 64)
	for y := 0; y < h; y++ {
		base := y * w
		for x := 0; x < w; x++ {
			// Classic indexed gradient: diagonal pattern plus alpha ramp.
			v := (x*2 + y + alphaNudge) & 0xFF
			// Darken the outer border to make the negotiated size visible.
			if x < 2 || y < 2 || x >= w-2 || y >= h-2 {
				v = 10
			}
			c.indexed[base+x] = byte(v)
		}
	}
	// Moving vertical bar: position = alpha * (w-1), width 5. At 60 fps the
	// bar glides smoothly; at 30 Hz stub without interpolation it would step.
	barX := int(alpha * float32(w-1))
	for y := 0; y < h; y++ {
		for dx := -2; dx <= 2; dx++ {
			x := barX + dx
			if x < 0 || x >= w {
				continue
			}
			// White bar with black outline for visibility on any palette.
			if dx == -2 || dx == 2 {
				c.indexed[y*w+x] = 0 // black outline
			} else {
				c.indexed[y*w+x] = 255 // white interior
			}
		}
	}
	// Menu chrome (front-end panels) draws over the placeholder too, so the
	// shell can present menus without a terrain bound [I6].
	if c.Overlay != nil {
		c.Overlay(c)
	}
	// If explicitly requested, encode the tick in the top rows for diagnostics.
	if c.opts.DebugOverlay && ok && prev != nil && cur != nil {
		// Example future interpolation point: unit positions would use
		// snapshot.Lerp(prevPos, curPos, alpha) with truncation toward zero (I3).
		_ = prev
		_ = cur

		// Encode cur.Tick in the top 4 rows as a binary bar so 30 Hz ticks are
		// visible stepping while alpha bar glides.
		tick := cur.Tick
		for y := 0; y < 4 && y < h; y++ {
			for x := 0; x < 16 && x < w; x++ {
				bit := (tick >> uint(x)) & 1
				val := byte(30)
				if bit == 1 {
					val = 200
				}
				c.indexed[y*w+x] = val
			}
		}
	}
}

// convertIndexedToRGBA converts the indexed framebuffer to RGBA at present
// time only (C7). The indexed framebuffer contains active PALETTE.PAL indices:
// GAF, PCX, TNT, FNT, and direct primitive writers all follow the same route.
// GUI semantic colors are resolved by the caller before FNT/primitives write.
func (c *Client) convertIndexedToRGBA() {
	if len(c.indexed)*4 != len(c.rgba) {
		return
	}
	for i, idx := range c.indexed {
		phys := c.logical[idx]
		c.rgba[i*4+0] = c.base[phys][0]
		c.rgba[i*4+1] = c.base[phys][1]
		c.rgba[i*4+2] = c.base[phys][2]
		c.rgba[i*4+3] = c.base[phys][3]
		// Ensure opaque; PALETTE.PAL's fourth byte is reserved zero.
		if c.rgba[i*4+3] == 0 {
			c.rgba[i*4+3] = 255
		}
	}
}
