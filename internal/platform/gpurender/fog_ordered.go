package gpurender

import (
	"fmt"
	"math"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/drawlist"
	"github.com/nanolathe-gg/nanolathe/internal/render"
)

// fogNeedsOrdered keeps the stock atlas path until a selected frame needs
// repeated operations or reaches outside its atlas tile. The entire op list
// then takes the ordered path: later cells must see earlier child writes,
// including writes outside the parent's dimensions [03 R-COMP-01 §2].
func fogNeedsOrdered(fg drawlist.Fog, scale camera.ViewScale) bool {
	for i := range fg.Ops {
		op := &fg.Ops[i]
		fr := fogSelectedFrame(fg, op)
		if fr == nil || op.Kind == render.FogKindGAFCh1 && fr.Compressed != 0 {
			continue
		}
		if len(fr.Subframes) != 0 || fr.SubframeCount != 0 || op.Frame >= fogAtlasCols {
			return true
		}
		// Admission only needs geometry. Match Resampled's ceil extents and
		// signed half-away-from-zero anchors, including their stored narrowing,
		// without allocating any pixel planes (DESIGN_GPU_RENDERER §14.3).
		geometry := formats.GAFFrame{
			Width: uint16(scale.Project(int32(fr.Width))), Height: uint16(scale.Project(int32(fr.Height))),
			XOffset: int16(scale.Px(int32(fr.XOffset))), YOffset: int16(scale.Px(int32(fr.YOffset))),
		}
		if _, _, ok := fogFrameTilePlacement(&geometry, fogAtlasTile(scale)); !ok && geometry.Width > 0 && geometry.Height > 0 {
			return true
		}
	}
	return false
}

func fogSelectedFrame(fg drawlist.Fog, op *render.FogOp) *formats.GAFFrame {
	if op.Variant < 0 || op.Variant >= fogVariants || op.Frame < 0 {
		return nil
	}
	var entry *formats.GAFEntry
	switch op.Kind {
	case render.FogKindGAFCh1:
		entry = fg.Gray[op.Variant]
	case render.FogKindGAFCh0:
		entry = fg.Black[op.Variant]
	}
	if entry == nil || op.Frame >= len(entry.Frames) {
		return nil
	}
	return entry.Frames[op.Frame].Frame
}

type fogLeafMode uint8

const (
	fogLeafGray fogLeafMode = iota
	fogLeafChecker
	fogLeafBlack
	fogLeafTint
)

// walkFogLeaves preserves the consumer through recursion. Gray/checker test
// compression before looking at children and ignore alternate selectors. Black
// dispatches an alternate child through tint, which propagates to all of that
// child's descendants. Parents never establish a clip [03 R-COMP-01 §2].
func walkFogLeaves(fr *formats.GAFFrame, mode fogLeafMode, emit func(*formats.GAFFrame, fogLeafMode)) {
	if fr == nil || (mode == fogLeafGray || mode == fogLeafChecker) && fr.Compressed != 0 {
		return
	}
	if len(fr.Subframes) != 0 || fr.SubframeCount != 0 {
		for _, child := range fr.Subframes {
			childMode := mode
			if mode == fogLeafBlack && child != nil && child.AlternateBlitter != 0 {
				childMode = fogLeafTint
			}
			walkFogLeaves(child, childMode, emit)
		}
		return
	}
	emit(fr, mode)
}

// fogOrdered emits each leaf as a separate scheduler command. Overlapping gray
// commands therefore read separate snapshots; black copies and ALP children use
// the ordinary keyed and tint streams and their established modern colour
// arithmetic (DESIGN_GPU_RENDERER §13.3). It never paints a parent-sized raster.
func (r *Renderer) fogOrdered(fg drawlist.Fog, scale camera.ViewScale) {
	if r.fog.orderedShader == nil {
		var err error
		r.fog.orderedShader, err = ebiten.NewShader([]byte(fogOrderedShaderSource))
		if err != nil {
			r.fog.contentErr = fmt.Errorf("nanolathe: fog shader compilation failed: logical path anims/fog.gaf, providers searched [], expected ordered fog compositor: %w", err)
		}
	}
	if r.fog.orderedShader == nil {
		return
	}
	w, h := int32(r.clipW()), int32(r.clipH())
	parity := fogParityOps(fg.Ops, scale)
	for i := range fg.Ops {
		op := &fg.Ops[i]
		rawX, rawY, x0, y0, x1, y1, ok := fogOpRect(op, w, h)
		if !ok {
			continue
		}
		switch op.Kind {
		case render.FogKindSolidDark:
			r.fillSolidInclusive(x0, y0, x1-1, y1-1, render.FogDarkPaletteIndex)
		case render.FogKindGrayRemap:
			r.drawFogRemap(nil, int(x0), int(y0), int(x1), int(y1), fogLeafGray, parity)
		case render.FogKindPatterned:
			r.drawFogRemap(nil, int(x0), int(y0), int(x1), int(y1), fogLeafChecker, parity)
		case render.FogKindGAFCh0, render.FogKindGAFCh1:
			mode := fogLeafGray
			if op.Kind == render.FogKindGAFCh0 {
				mode = fogLeafBlack
			} else if op.Patterned {
				mode = fogLeafChecker
			}
			fr := r.fog.viewFrame(fogSelectedFrame(fg, op), scale)
			walkFogLeaves(fr, mode, func(leaf *formats.GAFFrame, mode fogLeafMode) {
				x, y := int(rawX)-int(leaf.XOffset), int(rawY)-int(leaf.YOffset)
				switch mode {
				case fogLeafBlack:
					r.drawKeyed(leaf, x, y, 0, 0, int(w), int(h))
				case fogLeafTint:
					r.drawTint(leaf, int(rawX), int(rawY), 0, 0, int(w), int(h))
				default:
					r.drawFogRemap(leaf, x, y, x+int(leaf.Width), y+int(leaf.Height), mode, parity)
				}
			})
		}
	}
}

// drawFogRemap samples the destination in screen coordinates, as the stock fog
// shader does. The record pixel and leaf atlas offset travel in vertex lanes;
// clipping never rebases the original leaf anchor [03 R-COMP-01 §2]. A nil leaf
// denotes a fill. The checker remains one screen pixel at every zoom.
func (r *Renderer) drawFogRemap(fr *formats.GAFFrame, x0, y0, x1, y1 int, mode fogLeafMode, parity int32) {
	imgs := [4]*ebiten.Image{2: r.tables.atlas}
	var ax, ay, masked float32
	if fr != nil {
		e := r.sceneFrameFor(fr)
		if !e.ok {
			return
		}
		imgs[1] = r.sceneImages(e)[0]
		ax, ay, masked = float32(int(e.x)-x0), float32(int(e.y)-y0), 1
	}
	x0, y0 = max(x0, 0), max(y0, 0)
	x1, y1 = min(x1, r.clipW()), min(y1, r.clipH())
	if x0 >= x1 || y0 >= y1 {
		return
	}
	k := float32(1)
	if r.sched.worldOn {
		k = r.sched.worldScale
	}
	sx0, sy0, sx1, sy1 := fogRemapScreenRect(x0, y0, x1, y1, k)
	sx0, sy0, sx1, sy1 = max(sx0, 0), max(sy0, 0), min(sx1, r.w), min(sy1, r.h)
	worldOn := r.sched.worldOn
	r.sched.worldOn = false
	defer func() { r.sched.worldOn = worldOn }()
	if !r.sched.beginBlended(schedDest, sx0, sy0, sx1, sy1, imgs, r.fog.orderedShader, blendComposite, 0) {
		return
	}
	xa, ya, xb, yb := float32(sx0), float32(sy0), float32(sx1), float32(sy1)
	r.sched.quad(schedDest, xa, ya, xb, yb, xa, ya, xb, yb,
		[4]float32{k, masked, 0, 0}, [4]float32{ax, ay, float32(parity), float32(mode)})
	r.fog.draws++
}

// fogRemapScreenRect admits exactly the screen pixels whose floor(screen/k)
// record sample lies in the clipped leaf. A conservative floor at the left
// edge could sample outside the leaf and into another scene atlas entry when
// zooming out; the stock fog shader rejects those samples in its cell test.
func fogRemapScreenRect(x0, y0, x1, y1 int, k float32) (int, int, int, int) {
	if k == 1 {
		return x0, y0, x1, y1
	}
	scale := float64(k)
	return int(math.Ceil(float64(x0) * scale)), int(math.Ceil(float64(y0) * scale)),
		int(math.Ceil(float64(x1) * scale)), int(math.Ceil(float64(y1) * scale))
}

// The desaturation and checker arithmetic is identical to the stock fog pass
// (DESIGN_GPU_RENDERER §13.3). Only coverage and command ordering differ.
var fogOrderedShaderSource = fmt.Sprintf(`//kage:unit pixels
package main

func Fragment(dstPos vec4, srcPos vec2, color vec4, custom vec4) vec4 {
	sp := floor(dstPos.xy - imageDstOrigin())
	if color.g > 0.5 {
		p := floor(sp / color.r)
		mask := imageSrc1AtFromSrc0Pos(imageSrc0Origin() + p + custom.xy + vec2(0.5))
		if mask.g < 0.5 {
			discard()
		}
	}
	if custom.w > 0.5 {
		if mod(sp.x+sp.y+custom.z, 2.0) < 0.5 {
			discard()
		}
		return vec4(imageSrc2AtFromSrc0Pos(imageSrc0Origin() + vec2(%d.5, %d.5)).rgb, 1.0)
	}
	c := imageSrc0At(srcPos).rgb
	avg := floor((floor(c.r*255.0+0.5) + floor(c.g*255.0+0.5) + floor(c.b*255.0+0.5)) / 3.0)
	return vec4(vec3(avg/255.0), 1.0)
}
`, render.FogDarkPaletteIndex, tableRowPAL)
