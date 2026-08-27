package client

import (
	"strconv"

	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/presentation"
	"github.com/nanolathe/nanolathe/internal/render"
	"github.com/nanolathe/nanolathe/internal/snapshot"
	"github.com/nanolathe/nanolathe/internal/visibility"
	"github.com/nanolathe/nanolathe/internal/world"
)

// projectileVisible is the one-point projectile gate from the immutable
// local-player coverage grid [03 §3.2][03 §5.4]. An invalid/missing grid is
// treated as visible so fixtures without a visibility service still draw
// projectiles [03 §5.4][I9].
func projectileVisible(v snapshot.VisibilityView) func(snapshot.ProjectileView) bool {
	return func(p snapshot.ProjectileView) bool {
		if !v.Valid || v.W <= 0 || v.H <= 0 || len(v.Visible) != int(v.W*v.H) {
			return true
		}
		px := int32(int16(int64(p.X) >> 16))
		py := int32(int16(int64(p.Y) >> 16))
		pz := int32(int16(int64(p.Z) >> 16))
		u := px >> 5
		row := (pz - (py >> 1)) >> 5
		if u < 0 || row < 0 || u >= v.W || row >= v.H {
			return false
		}
		return v.Visible[int(row*v.W+u)] != 0
	}
}

// fogUnexploredUnit reports whether a unit's anchor visibility tile is never-explored [03 §3.3][03 §3.3].
// It checks Fog Ch0 ==15 (solid dark, all four neighbours fogged) via the immutable FogView, which is the presentation equivalent of the plot flag 0x04 [03 §3.3]. Invalid or missing fog is treated as explored so fixtures remain visible [I9].
func fogUnexploredUnit(fog snapshot.FogView, u snapshot.UnitView) bool {
	if !fog.Valid || fog.W <= 0 || fog.H <= 0 || len(fog.Ch0) != int(fog.W*fog.H) {
		return false
	}
	tx := world.WorldToTile(u.X)
	tz := world.WorldToTile(u.Z)
	if tx < 0 || tz < 0 || tx >= fog.W || tz >= fog.H {
		return false
	}
	idx := int(tz*fog.W + tx)
	return fog.Ch0[idx] == 15
}

// fogUnexploredFeature reports whether a feature's anchor cell maps to an unexplored fog tile [03 §3.3][03 §3.3].
// Feature CX/CZ are cell coordinates; fog is per visibility tile (2x2 cells) so tile = cell>>1 [03 §2.1][03 §3.1]. Invalid fog is treated as explored [I9].
func fogUnexploredFeature(fog snapshot.FogView, f snapshot.FeatureView) bool {
	if !fog.Valid || fog.W <= 0 || fog.H <= 0 || len(fog.Ch0) != int(fog.W*fog.H) {
		return false
	}
	tx := f.CX >> 1
	tz := f.CZ >> 1
	if tx < 0 || tz < 0 || tx >= fog.W || tz >= fog.H {
		return false
	}
	idx := int(tz*fog.W + tx)
	return fog.Ch0[idx] == 15
}

// unitVisibleForFrame is the enemy-visibility predicate for the painter [03 §3.2] C8.
// Own units always pass (owner bypass) [03 §3.2] step1; invalid visibility is treated as visible for fixtures [I9]; otherwise it delegates to SnapshotVisible which checks cloaked flag and the current-coverage byte grid.
func unitVisibleForFrame(frame *snapshot.Frame, u snapshot.UnitView, viewer uint8) bool {
	if frame == nil {
		return false
	}
	if u.Owner == viewer {
		return true
	}
	if !frame.Visibility.Valid || frame.Visibility.W <= 0 || frame.Visibility.H <= 0 || len(frame.Visibility.Visible) != int(frame.Visibility.W*frame.Visibility.H) {
		return true
	}
	return SnapshotVisible(frame, u, viewer)
}

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
	if c == nil {
		return
	}
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
		if c.buffer != nil {
			_, _, _ = c.buffer.Read()
		}
		// Audio drain still runs headless? No, Headless skips window creation
		// entirely [PLAN_04A C11]; but TickAudio is presentation-only and can be
		// called manually by headless diagnostics. Do not auto-drain here to
		// keep headless simulation hash stable [I4][I6].
		return
	}
	// Audio: drain queue once per rendered frame outside simulation [03 §8.3] C18.
	// Presentation-only; uses CRT stream [03 §8.3] C19 [I4]; never touches Sim RNG.
	prev, cur, ok := c.buffer.Read()
	if cur != nil {
		c.beginPresentationFrame(cur.Tick)
	}
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
	c.composeIndexed(alpha, prev, cur, ok)
	c.frameBegun = false
	// The software cursor is drawn after the offscreen battle/front-end surface
	// is prepared, so it sits above world, HUD, and modal overlays [07 §8].
	c.drawCursor()

	// Convert to RGBA through logical→base at present time only (C7). This is
	// the only point where indexed pixels become RGBA so palette animation
	// stays possible in later phases.
	c.convertIndexedToRGBA()
}

// composeIndexed delegates battle ordering to render.Composer. Each client
// adapter is stage-specific, so terrain, world objects, projectiles, effects,
// fog, selection, and interface work occur at the composer's barriers without
// a second hand-written ordering in the client [03 §1].
func (c *Client) composeIndexed(alpha float32, prev, cur *snapshot.Frame, ok bool) {
	if c == nil || len(c.indexed) != c.width*c.height {
		return
	}
	c.ensureComposer()
	if cur != nil && !c.frameBegun {
		c.beginPresentationFrame(cur.Tick)
	}
	c.composePrev, c.composeCur, c.composeAlpha, c.composeOK = prev, cur, alpha, ok
	c.selectionChrome = c.selectionChrome[:0]
	mode := 0
	if c.cam != nil {
		mode = 1
	}
	c.composer.Frame(cur, alpha, mode)
	// A direct composeIndexed call is itself one presentation frame. The
	// windowed Frame method pre-begins its shared clock before audio, so both
	// paths clear the one-frame guard here.
	c.frameBegun = false
}

func (c *Client) beginPresentationFrame(tick uint32) {
	if c == nil {
		return
	}
	if c.clock == nil {
		c.clock = &presentation.Clock{}
	}
	c.clock.BeginFrame(tick, 0)
	c.frameBegun = true
}

// ensureComposer installs the live client adapters once. The callbacks are
// presentation-only and capture no simulation state; all ordering remains in
// render.Composer.Frame [03 §1][I6].
func (c *Client) ensureComposer() {
	if c == nil || c.composer != nil {
		return
	}
	c.composer = &render.Composer{}
	c.composer.Hooks.PreWorld = func() { c.traceAdapter("preworld"); c.consumeShake() }
	c.composer.Hooks.Terrain = func() { c.traceAdapter("terrain"); c.drawBattleStage(stageTerrain) }
	c.composer.Hooks.Minimap = func() { c.traceAdapter("minimap") }
	c.composer.Hooks.Clip = func() { c.traceAdapter("clip") }
	c.composer.Hooks.DrawStrip = func(idx int) { c.traceAdapter("strip" + strconv.Itoa(idx)) }
	c.composer.Hooks.BucketBuild = func() { c.traceAdapter("bucket") }
	c.composer.Hooks.FeaturePass = func() { c.traceAdapter("features"); c.drawBattleStage(stageFeatures) }
	c.composer.Hooks.UnitTraversal = func(kind string) {
		if kind == "mid" {
			c.traceAdapter("units")
			c.drawBattleStage(stageUnits)
		}
	}
	c.composer.Hooks.Projectiles = func() { c.traceAdapter("projectiles"); c.drawBattleStage(stageProjectiles) }
	c.composer.Hooks.Effects = func() { c.traceAdapter("effects"); c.drawBattleStage(stageEffects) }
	c.composer.Hooks.Fog = func() { c.traceAdapter("fog"); c.drawBattleStage(stageFog) }
	c.composer.Hooks.Selection = func() { c.traceAdapter("selection"); c.drawSelectionStage() }
	c.composer.Hooks.Interface = func() { c.traceAdapter("interface"); c.drawBattleStage(stageInterface) }
}

func (c *Client) traceAdapter(name string) {
	if c != nil {
		c.operationTrace = append(c.operationTrace, name)
	}
}

const (
	stageTerrain = iota
	stageFeatures
	stageUnits
	stageProjectiles
	stageEffects
	stageFog
	stageInterface
)

type selectionChrome struct {
	view    snapshot.UnitView
	screenX int32
	screenY int32
}

// consumeShake admits each published shake event once and advances the
// presentation shake only at a new simulation tick. Repainting a snapshot
// therefore cannot consume CRT values or move the camera again [03 §5.6].
func (c *Client) consumeShake() {
	if c == nil || c.composeCur == nil || c.clock == nil || !c.clock.ConsumeNewSimTick() {
		return
	}
	for _, ev := range c.composeCur.Events {
		if ev.Kind != snapshot.EventKindShake {
			continue
		}
		key := shakeEventKey{sequence: ev.Sequence, tick: ev.Tick, id: ev.ID}
		if _, seen := c.shakeEvents[key]; seen {
			continue
		}
		c.shakeEvents[key] = struct{}{}
		// A zero authored duration remains zero. The retail contract does not
		// establish a fallback duration, so no compatibility constant is used
		// here [03 §5.6].
		c.shake.Request(ev.Magnitude, ev.Lifetime)
	}
	if c.crt != nil && c.cam != nil {
		c.shake.TickWithRandom(c.cam, c.crt)
	}
}

// drawBattleStage is a stage-local adapter for authored draw services. It has
// no ordering responsibilities; render.Composer invokes one stage at each
// position in the canonical pass trace [03 §1].
func (c *Client) drawBattleStage(stage int) {
	alpha, prev, cur, ok := c.composeAlpha, c.composePrev, c.composeCur, c.composeOK
	w := c.width
	h := c.height
	if len(c.indexed) != w*h {
		return
	}
	if stage == stageTerrain {
		if c.cam != nil && c.terrain != nil {
			BlitTerrain(c.indexed, w, h, c.terrain, c.cam)
			return
		}
		// Front-end frames have no world camera. Keep their existing software
		// surface local to this terrain-preparation stage.
		c.drawBaseSurface(alpha, prev, cur, ok)
		return
	}
	if stage == stageInterface {
		if c.Overlay != nil {
			c.Overlay(c)
		}
		if c.opts.DebugOverlay && c.fnt != nil {
			var tick uint32
			var camX, camZ int32
			if ok && cur != nil {
				tick = cur.Tick
			}
			if c.cam != nil {
				camX, camZ = c.cam.X, c.cam.Z
			}
			info := DebugInfo{Tick: tick, Alpha: alpha, CamX: camX, CamZ: camZ}
			DrawDebugOverlayWithShadow(c.indexed, w, h, c.fnt, info, 255, 0, true)
		}
		return
	}
	if c.cam == nil {
		return
	}
	// Gate 1: real terrain from TNT + palette [PLAN_04A]. Units draw whenever
	// a camera is bound (menu mode binds one without terrain); terrain blits
	// only when a world is attached.
	if c.cam != nil {
		// Gate 2: draw interpolated units Previous→Current at alpha [03 §2.4] I6.
		// Publish happens after phase 12 each sub-tick; renderer interpolates
		// with alpha clamped [0,1] (C15/C16). When paused ticksToRun==0 the same
		// pair is returned and Lerp at any alpha yields the same position, so
		// holding alpha at 1.0 shows no jitter. Draw BEFORE debug overlay so
		// text remains on top.
		if (stage == stageFeatures || stage == stageUnits) && ok && prev != nil && cur != nil {
			// Single painter pass: merge units+features Y-sorted [03 §1] fixing trees-over-tanks.
			// Retail Y-bucket is ((zPix - camZ + bias)>>4)+16 with stable append [rr-10];
			// we sort by screen-Y then stable handle tie [03 §1][I1] via sort.SliceStable (presentation-only).
			// Visibility admission: binary hard edge skip anchor-cell-unexplored via Fog Ch0==15 [03 §3.3][03 §3.3],
			// enemies in partial fog suppressed via SnapshotVisible predicate [03 §3.2]; own units always drawn [03 §3.2] C8 step1.
			// MarkUnexplored/ClearUnexplored helpers in visibility/fog.go own the 0x04 flag [03 §3.3] — presentation uses Ch0 equivalence, destroyed features absent from snapshot stop painting once Ch0 flips.
			prevBySlot := make(map[uint16]int, len(prev.Units))
			for i, pv := range prev.Units {
				prevBySlot[uint16(pv.Slot)] = i
			}
			type featKey struct {
				name   string
				cx, cz int32
			}
			prevByKey := make(map[featKey]int, len(prev.Features))
			for i, pv := range prev.Features {
				prevByKey[featKey{pv.DefName, pv.CX, pv.CZ}] = i
			}
			viewer := cur.Selection.LocalPlayer // 0..9, invalid falls through to fog-only gating [07 §9]
			isUnexploredUnit := func(u snapshot.UnitView) bool { return fogUnexploredUnit(cur.Fog, u) }
			isUnexploredFeat := func(f snapshot.FeatureView) bool { return fogUnexploredFeature(cur.Fog, f) }
			isEnemyVisible := func(u snapshot.UnitView) bool { return unitVisibleForFrame(cur, u, viewer) }
			type drawable struct {
				sy       int32
				isUnit   bool
				unit     snapshot.UnitView
				usx, usy int32
				feat     snapshot.FeatureView
				fsx, fsy int32
			}
			var drawables []drawable
			// Collect units with admission.
			if stage == stageUnits {
				for _, cv := range cur.Units {
					var pv snapshot.UnitView
					if idx, ok2 := prevBySlot[uint16(cv.Slot)]; ok2 {
						pv = prev.Units[idx]
					} else {
						pv = cv
					}
					lerped := LerpUnitView(pv, cv, alpha)
					if lerped.Owner != viewer {
						if isUnexploredUnit(lerped) {
							continue
						}
						if !isEnemyVisible(lerped) {
							continue
						}
					}
					sx0, sy0 := c.cam.WorldToScreen(lerped.X, lerped.Y, lerped.Z) // [03 §2.5] C1 beam
					sx := sx0 - 128
					sy := sy0 - 32 // shell [PLAN_04A C1]
					drawables = append(drawables, drawable{sy: sy, isUnit: true, unit: lerped, usx: sx, usy: sy})
				}
			}
			// Collect features with fog admission [03 §3.3] feature pass owns unexplored marker.
			if stage == stageFeatures {
				for _, cvf := range cur.Features {
					if isUnexploredFeat(cvf) {
						continue
					}
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
					drawables = append(drawables, drawable{sy: sy, isUnit: false, feat: interp, fsx: sx, fsy: sy})
				}
			}
			// Y-bucket insertion is the retail ordering primitive. In-row order
			// is the source enumeration order; no handle/name/coordinate tie
			// breaker is permitted [03 §1][I1].
			var drawBuckets render.YBuckets
			for i, d := range drawables {
				drawBuckets.Insert(int(d.sy), i)
			}
			for _, drawIndex := range drawBuckets.Ordered() {
				d := drawables[drawIndex]
				if d.isUnit {
					lerped := d.unit
					sx, sy := d.usx, d.usy
					// Real 3DO model first [fmt 3do][03 §2.5]; offscreen cache then blit in Y order [rr-10].
					// Buildings (IsBuilding) at 2x supersampled with per-piece DontShade shading [03 §2.4.1][04 §4.3];
					// mobiles at 1x without shading.
					if lerped.Model != "" && c.drawUnitModel(lerped, sx, sy) {
						c.selectionChrome = append(c.selectionChrome, selectionChrome{view: lerped, screenX: sx, screenY: sy})
					}
					// Missing authored model art is a compatibility no-op. Footprint
					// boxes are diagnostics/fallback art and are not part of the retail
					// compositor [I9].
					continue
				} else {
					cvf := d.feat
					sx, sy := d.fsx, d.fsy
					// 3DO path for object features (corpses, walls) [fmt 3do][02 "Feature record"] — try first.
					is3DO := cvf.Model != "" && cvf.Filename == "" || (cvf.Filename == "" && cvf.SeqName == "")
					if is3DO && cvf.Model != "" {
						_ = c.drawFeatureModel(cvf)
						continue
					}
					// Sprite GAF path [03 §5.1.1].
					normalFrame := c.featureFrameFor(cvf, false)
					shadowFrame := c.featureFrameFor(cvf, true)
					drawn := false
					if shadowFrame != nil {
						dstX := int(sx) - int(shadowFrame.XOffset)
						dstY := int(sy) - int(shadowFrame.YOffset)
						c.blitGAFFrame(shadowFrame, dstX, dstY, true, cvf.ShadTrans)
						drawn = true
					}
					if normalFrame != nil {
						dstX := int(sx) - int(normalFrame.XOffset)
						dstY := int(sy) - int(normalFrame.YOffset)
						c.blitGAFFrame(normalFrame, dstX, dstY, false, cvf.AnimTrans)
						drawn = true
					}
					if drawn {
						continue
					}
					// Missing authored sprite art is a compatibility no-op; no
					// synthetic footprint or 1×1 marker is emitted [I9].
					continue
				}
			}
		}
		if stage == stageFeatures || stage == stageUnits {
			return
		}
		// Projectiles and effects occupy the researched strip-6/7 window:
		// after unit/feature traversals and before fog/interface [03 §1][03 §5.4].
		// They are deliberately outside the unit-length guard so a frame with
		// only projectiles or effects still draws [I6]. Both adapters consume
		// immutable snapshots and never inspect live pools.
		if stage == stageProjectiles && ok && prev != nil && cur != nil {
			if len(cur.Projectiles) > 0 {
				c.DrawProjectileViews(prev.Projectiles, cur.Projectiles, alpha, cur.Tick, projectileVisible(cur.Visibility), func(snapshot.ProjectileView) bool { return false }, c.projectileDispatchOptions())
			}
			return
		}
		if stage == stageEffects && ok && prev != nil && cur != nil {
			if len(cur.Effects) > 0 {
				c.DrawEffectViews(cur.Effects, c.effectDrawOptions())
			}
			return
		}
		if stage != stageFog {
			return
		}
		// Fog presentation [03 §3.3] C13 — reads snapshot fog cache copied from visibility.Service.Fog() each tick (I6).
		// The cache is presentation-only and never writes sim state. Fog uses hard 32-pixel tiles [03 §3.3][03 §3.3].
		if ok && cur != nil && cur.Fog.Valid && c.cam != nil {
			fc := visibility.NewFogCacheFromChannelsAt(cur.Fog.W, cur.Fog.H, cur.Fog.OriginX, cur.Fog.OriginZ, cur.Fog.Ch0, cur.Fog.Ch1)
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
	}
	// No world stage has work outside the explicit adapters above.
}

func (c *Client) drawBaseSurface(alpha float32, prev, cur *snapshot.Frame, ok bool) {
	w, h := c.width, c.height
	// Base: horizontal gradient plus alpha nudge so the image visibly shifts
	// each render frame rather than only each tick. This is front-end surface
	// preparation, not battle-world ordering.
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
}

// drawSelectionStage emits unit selection and health chrome after fog. The
// body stage only records model positions, so these pixels remain visible
// above the fog overlay as required by the frame contract [03 §1][03 §3.3].
func (c *Client) drawSelectionStage() {
	if c == nil {
		return
	}
	for _, item := range c.selectionChrome {
		c.drawUnitChrome(item.view, item.screenX, item.screenY)
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
