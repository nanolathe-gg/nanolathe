package client

import (
	"github.com/nanolathe/nanolathe/internal/camera"
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
			// Collect interpolated views with shell coords for Y-bucket sort [03 §1][rr-10].
			// Retail Y-bucket is ((zPix - camZ + bias)>>4)+16 with stable append;
			// sorting by screen Y (sy) with stable slot tie preserves that order.
			type drawEntry struct {
				view   snapshot.UnitView
				sx, sy int32
				slot   uint16
			}
			entries := make([]drawEntry, 0, len(cur.Units))
			for _, cv := range cur.Units {
				var pv snapshot.UnitView
				if idx, ok2 := prevBySlot[uint16(cv.Slot)]; ok2 {
					pv = prev.Units[idx]
				} else {
					pv = cv
				}
				lerped := LerpUnitView(pv, cv, alpha)
				sx0, sy0 := c.cam.WorldToScreen(lerped.X, lerped.Y, lerped.Z) // [03 §2.5] C1 beam
				sx := sx0 - 128
				sy := sy0 - 32 // shell [PLAN_04A C1]
				entries = append(entries, drawEntry{view: lerped, sx: sx, sy: sy, slot: uint16(lerped.Slot)})
			}
			// Y-bucket stable sort: sy ascending, slot ascending on tie [03 §1] I1.
			for i := 0; i < len(entries)-1; i++ {
				for j := i + 1; j < len(entries); j++ {
					if entries[j].sy < entries[i].sy || (entries[j].sy == entries[i].sy && entries[j].slot < entries[i].slot) {
						entries[i], entries[j] = entries[j], entries[i]
					}
				}
			}
			for _, e := range entries {
				lerped := e.view
				sx, sy := e.sx, e.sy
				// Real 3DO model first [fmt 3do][03 §2.5]; offscreen cache then blit in Y order [rr-10].
				// Buildings (IsBuilding) at 2x supersampled with per-piece DontShade shading [03 §2.4.1][04 §4.3];
				// mobiles at 1x without shading.
				if lerped.Model != "" && c.drawUnitModel(lerped, sx, sy) {
					c.drawUnitChrome(lerped, sx, sy)
					continue
				}
				if lerped.FootX > 0 && lerped.FootZ > 0 {
					c.drawUnitOriented(lerped, sx, sy)
					continue
				}
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
			// Projectiles: presentation-only interpolated models/beams/markers [06 §5][03 §5.4] (I6).
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
					interp := snapshot.ProjectileView{Handle: cvp.Handle, X: px, Y: py, Z: pz, WeaponID: cvp.WeaponID, Model: cvp.Model, Yaw: cvp.Yaw, Pitch: cvp.Pitch}
					// Try real 3DO projectile model first [fmt 3do][03 §5.4][03 §5.2] with yaw/pitch offsets.
					if interp.Model != "" && c.drawProjectileModel(interp, alpha) {
						continue
					}
					// Fallback: dispatch via render projectiles for beam/rendertype handling [03 §5.4].
					// For beams, draw line; for others, draw marker.
					sx0, sy0 := c.cam.WorldToScreen(px, py, pz)
					sx := sx0 - 128
					sy := sy0 - 32
					// Simple 3x3 projectile marker; presentation uses palette index 210 for visibility; never touches sim state.
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
			// Features: 3DO feature models + GAF sprites with shadows, Y-sorted [05 "Feature instance"] [fmt 3do][03 §5.1][research/features/feature_rendering.md §3].
			if len(cur.Features) > 0 {
				// Build interpolation map keyed by DefName+CX/CZ for stable matching when lengths differ.
				type featKey struct {
					name   string
					cx, cz int32
				}
				prevByKey := make(map[featKey]int, len(prev.Features))
				for i, pv := range prev.Features {
					prevByKey[featKey{pv.DefName, pv.CX, pv.CZ}] = i
				}
				// Collect drawable with interpolated positions and screen Y for painter order [03 §1] Y-bucket.
				type drawable struct {
					view   snapshot.FeatureView
					sx, sy int32
				}
				drawList := make([]drawable, 0, len(cur.Features))
				for _, cvf := range cur.Features {
					var pvf snapshot.FeatureView
					if idx, ok2 := prevByKey[featKey{cvf.DefName, cvf.CX, cvf.CZ}]; ok2 && idx < len(prev.Features) {
						pvf = prev.Features[idx]
					} else {
						pvf = cvf
					}
					fx := snapshot.Lerp(pvf.X, cvf.X, alpha)
					fy := snapshot.Lerp(pvf.Y, cvf.Y, alpha)
					fz := snapshot.Lerp(pvf.Z, cvf.Z, alpha)
					interp := cvf
					interp.X, interp.Y, interp.Z = fx, fy, fz
					sx, sy := c.featureScreenPos(interp)
					drawList = append(drawList, drawable{view: interp, sx: sx, sy: sy})
				}
				// Y-sort for deterministic painter order [03 §1] C3 per-row buckets.
				// Stable sort by screen Y then DefName for tie.
				for i := 0; i < len(drawList)-1; i++ {
					for j := i + 1; j < len(drawList); j++ {
						if drawList[j].sy < drawList[i].sy || (drawList[j].sy == drawList[i].sy && drawList[j].view.DefName < drawList[i].view.DefName) {
							drawList[i], drawList[j] = drawList[j], drawList[i]
						}
					}
				}
				for _, d := range drawList {
					cvf := d.view
					// 3DO path for object features (corpses, walls) [fmt 3do][02 "Feature record"] — try first.
					// Object present => Model is Object; filename case uses GAF path instead.
					is3DO := cvf.Model != "" && cvf.Filename == "" || (cvf.Filename == "" && cvf.SeqName == "")
					// Heuristic: if Filename empty and Model non-empty and SeqName empty => 3DO; otherwise sprite.
					// For Great Divide, sprite features have Filename set and SeqName populated.
					if is3DO && cvf.Model != "" {
						if c.drawFeatureModel(cvf) {
							continue
						}
					}
					// Sprite GAF path [03 §5.1] [research/features/feature_rendering.md §2].
					// Resolve normal and shadow frames; shadow draws first at same anchor [04 §6A610].
					normalFrame := c.featureFrameFor(cvf, false)
					shadowFrame := c.featureFrameFor(cvf, true)
					// Geothermal 1x1 invisible marker (geotherm.gaf 1x1) skip drawing to avoid single-pixel noise
					// unless its footprint suggests it should be visible as vent. Keep but skip if both frames 1x1.
					if cvf.Geothermal && normalFrame != nil && normalFrame.Width == 1 && normalFrame.Height == 1 && (shadowFrame == nil || (shadowFrame.Width == 1 && shadowFrame.Height == 1)) {
						// Draw as small metal-deposit tinted dot for visibility on Great Divide
						// rather than invisible; use reclaimable fallback color encoding [02 "Feature record"].
						sx, sy := d.sx, d.sy
						xp := int(sx)
						yp := int(sy)
						if xp >= 0 && xp < w && yp >= 0 && yp < h {
							// Vent marker: palette index for geothermal (yellow-ish)
							c.indexed[yp*w+xp] = 48
						}
						continue
					}
					drawn := false
					if shadowFrame != nil {
						// Shadow at same anchor, translucent via ShadTrans [05 "Feature catalog and placement"].
						dstX := int(d.sx) - int(shadowFrame.XOffset)
						dstY := int(d.sy) - int(shadowFrame.YOffset)
						c.blitGAFFrame(shadowFrame, dstX, dstY, true, cvf.ShadTrans)
						drawn = true
					}
					if normalFrame != nil {
						dstX := int(d.sx) - int(normalFrame.XOffset)
						dstY := int(d.sy) - int(normalFrame.YOffset)
						c.blitGAFFrame(normalFrame, dstX, dstY, false, cvf.AnimTrans)
						drawn = true
					}
					if drawn {
						continue
					}
					// Fallback rectangle: footprint-correct tinted body when GAF missing [04 §6.2][05 "Feature catalog and placement"].
					// Colors encode reclaimability/blocking for diagnostics on Great Divide.
					sx, sy := d.sx, d.sy
					paletteIdx := byte(96) // default tree green
					if cvf.Geothermal {
						paletteIdx = 48
					} else if !cvf.Reclaimable && cvf.Blocking {
						paletteIdx = 72 // blocking non-reclaimable (e.g. hurt rock)
					} else if !cvf.Reclaimable && !cvf.Blocking {
						paletteIdx = 40 // metal deposit non-blocking (traversable)
					} else if cvf.IsBurning {
						paletteIdx = 200 // burnt
					}
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
									c.indexed[yp*w+xp] = 40
								} else {
									c.indexed[yp*w+xp] = paletteIdx
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
								c.indexed[yp*w+xp] = paletteIdx
							}
						}
					}
				}
			}
		}
		// Fog presentation [03 §3.3] C13 — reads snapshot fog cache copied from visibility.Service.Fog() each tick (I6).
		// The cache is presentation-only and never writes sim state. Fog uses hard 32-pixel tiles [03 §3.3][rr-16].
		if ok && cur != nil && cur.Fog.Valid && c.cam != nil {
			fc := visibility.NewFogCacheFromChannels(cur.Fog.W, cur.Fog.H, cur.Fog.Ch0, cur.Fog.Ch1)
			c.ensureFogGAF()
			ops := render.BuildFogOps(fc, c.cam, c.cam.ViewW, c.cam.ViewH, cur.Fog.W, cur.Fog.H, c.pal, c.ditheredFog)
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
					// TODO(question): Historical analysis omitted; independently worded behavior is needed.
					for py := y0; py < y1; py++ {
						base := int(py)*w + int(x0)
						for px := x0; px < x1; px++ {
							c.indexed[base+int(px-x0)] = render.FogDarkPaletteIndex
						}
					}
				case render.FogKindGrayRemap:
					// hi==15 fogged-but-explored: remap existing pixels through the
					// TODO(question): Historical analysis omitted; independently worded behavior is needed.
					// — terrain texture is preserved and desaturated [rr-16 §8;
					// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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
					// TODO(question): Historical analysis omitted; independently worded behavior is needed.
					// Retail writes literal palette index 0 (black) at checker
					// positions (x+y+parity)&1==1 and leaves the rest untouched
					// TODO(question): Historical analysis omitted; independently worded behavior is needed.
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
					// TODO(question): Historical analysis omitted; independently worded behavior is needed.
					if c.fogGAF != nil && op.Variant >= 0 && op.Variant < 4 && op.Frame >= 0 {
						entry := c.fogGray[op.Variant]
						if entry != nil && op.Frame < len(entry.Frames) && entry.Frames[op.Frame].Frame != nil {
							frame := entry.Frames[op.Frame].Frame
							if op.Patterned {
								c.blitFogGAFPatterned(frame, int(x0), int(y0))
							} else {
								c.blitFogGAF(frame, int(x0), int(y0))
							}
							continue
						}
					}
					// Missing GAF entry/frame: retail skips the blit and the cell
					// keeps the underlying tile [rr-16 §9.3 SUPPORTED-INFERENCE].
					continue
				case render.FogKindGAFCh0:
					// TODO(question): Historical analysis omitted; independently worded behavior is needed.
					if c.fogGAF != nil && op.Variant >= 0 && op.Variant < 4 && op.Frame >= 0 {
						entry := c.fogBlack[op.Variant]
						if entry != nil && op.Frame < len(entry.Frames) && entry.Frames[op.Frame].Frame != nil {
							frame := entry.Frames[op.Frame].Frame
							c.blitFogGAF(frame, int(x0), int(y0))
							continue
						}
					}
					// Missing GAF: skip, cell keeps underlying tile [rr-16 §9.3].
					continue
				default:
					// Visible cells produce no ops; nothing to draw.
					continue
				}
			}
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
