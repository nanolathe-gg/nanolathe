package ebitenapp

import (
	"fmt"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
)

const (
	fpsOverlayWidth  = 350
	fpsOverlayHeight = 284
	fpsPlotLeft      = 94
	fpsPlotRight     = fpsOverlayWidth - 7
	fpsLaneTop       = 62
	fpsLaneHeight    = 34
)

type fpsLane struct {
	name   string
	median time.Duration
	peak   time.Duration
	color  [3]byte
	value  func(fpsGraphColumn) time.Duration
}

// drawFPSOverlay is host-only. It reads the preceding completed Draws, so its
// own callback time appears in the next sample (DESIGN_GPU_RENDERER §13.5).
func (a *app) drawFPSOverlay(screen *ebiten.Image, screenWidth int) {
	if a.fpsGraph == nil {
		a.fpsGraph = ebiten.NewImage(fpsOverlayWidth, fpsOverlayHeight)
		a.fpsGraphPixels = make([]byte, 4*fpsOverlayWidth*fpsOverlayHeight)
	}
	var columns [fpsPlotRight - fpsPlotLeft]fpsGraphColumn
	target := a.presentInterval
	now := time.Now()
	summary := a.fpsCounter.graph(now, target, columns[:])
	live := a.fpsCounter.live(now)
	lanes := [...]fpsLane{
		{"Frame", live.interval, summary.peakInterval, [3]byte{72, 170, 245}, func(c fpsGraphColumn) time.Duration { return c.interval }},
		{"Draw", live.draw, summary.peakDraw, [3]byte{55, 185, 105}, func(c fpsGraphColumn) time.Duration { return c.draw }},
		{"Sim", live.sim, summary.peakSim, [3]byte{235, 192, 69}, func(c fpsGraphColumn) time.Duration { return c.sim }},
		{"Blend", live.blend, summary.peakBlend, [3]byte{242, 134, 177}, func(c fpsGraphColumn) time.Duration { return c.blend }},
		{"Record", live.record, summary.peakRecord, [3]byte{185, 130, 230}, func(c fpsGraphColumn) time.Duration { return c.record }},
		{"Submit", live.submit, summary.peakSubmit, [3]byte{70, 207, 205}, func(c fpsGraphColumn) time.Duration { return c.submit }},
	}
	paintFPSGraph(a.fpsGraphPixels, columns[:], lanes[:], target)
	a.fpsGraph.WritePixels(a.fpsGraphPixels)
	x := max(0, screenWidth-fpsOverlayWidth-6)
	op := &ebiten.DrawImageOptions{}
	op.GeoM.Translate(float64(x), 6)
	screen.DrawImage(a.fpsGraph, op)

	fps := "FPS --"
	if live.interval > 0 {
		fps = fmt.Sprintf("FPS %.0f", float64(time.Second)/float64(live.interval))
	}
	capLabel := "display (uncapped)"
	if target > 0 {
		capLabel = fmt.Sprintf("cap %.1f ms", ms(target))
	}
	ebitenutil.DebugPrintAt(screen, fps+" (500ms med)   "+capLabel, x+7, 8)
	if live.frames == 0 {
		ebitenutil.DebugPrintAt(screen, "Frame --   peak --", x+7, 21)
		ebitenutil.DebugPrintAt(screen, "Draw --", x+7, 34)
	} else {
		frameLabel := fmt.Sprintf("Frame %.1f ms   peak %.1f", ms(live.interval), ms(summary.peakInterval))
		if target > 0 {
			frameLabel += fmt.Sprintf(" (+%.1f)", ms(max(0, summary.peakInterval-target)))
		}
		ebitenutil.DebugPrintAt(screen, frameLabel, x+7, 21)
		drawLabel := fmt.Sprintf("Draw %.1f ms", ms(live.draw))
		if target > 0 {
			drawLabel += fmt.Sprintf("   room %.1f ms", ms(target-live.draw))
		}
		ebitenutil.DebugPrintAt(screen, drawLabel, x+7, 34)
	}
	lateLabel := fmt.Sprintf("30s: %d frames", summary.frames)
	if target > 0 {
		lateLabel = fmt.Sprintf("30s: %d/%d late (>cap+1ms)", summary.late, summary.frames)
	}
	// The frame's render passes: past about 80 in flight the Metal driver
	// stops scheduling until the GPU drains, and the present waits
	// (DESIGN_GPU_RENDERER §22).
	lateLabel += fmt.Sprintf("   passes %d", a.fpsPasses)
	ebitenutil.DebugPrintAt(screen, lateLabel, x+7, 47)
	for index, lane := range lanes {
		y := 6 + fpsLaneTop + index*fpsLaneHeight
		ebitenutil.DebugPrintAt(screen, fmt.Sprintf("%s %.1f", lane.name, ms(lane.median)), x+7, y)
		ebitenutil.DebugPrintAt(screen, fmt.Sprintf("peak %.1f", ms(lane.peak)), x+7, y+12)
	}
	ebitenutil.DebugPrintAt(screen, "-30s", x+fpsPlotLeft, 272)
	ebitenutil.DebugPrintAt(screen, "-15s", x+(fpsPlotLeft+fpsPlotRight)/2-12, 272)
	ebitenutil.DebugPrintAt(screen, "now", x+fpsPlotRight-18, 272)
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

// paintFPSGraph draws six separate lanes on one fixed bitmap, avoiding one
// GPU call per sample. The lanes are not stacked: async recording and host
// simulation may overlap Draw work. Each pixel column keeps a peak in its
// 30-second time slice; a cap line is shown at half height when one is set.
func paintFPSGraph(pixels []byte, columns []fpsGraphColumn, lanes []fpsLane, target time.Duration) {
	for i := 0; i < len(pixels); i += 4 {
		pixels[i], pixels[i+1], pixels[i+2], pixels[i+3] = 13, 20, 28, 255
	}
	scale := 2 * target
	if scale <= 0 {
		scale = time.Second / 30
	}
	for index, lane := range lanes {
		top := fpsLaneTop + index*fpsLaneHeight
		bottom := top + fpsLaneHeight - 5
		if target > 0 {
			y := fpsGraphY(target, scale, top, bottom)
			for x := fpsPlotLeft; x < fpsPlotRight; x++ {
				fpsPixel(pixels, x, y, 100, 92, 55)
			}
		}
		for i, column := range columns {
			value := lane.value(column)
			if value <= 0 {
				continue
			}
			x := fpsPlotLeft + i
			y := fpsGraphY(value, scale, top, bottom)
			color := lane.color
			if index == 0 && target > 0 && value > target+time.Millisecond {
				color = [3]byte{245, 133, 58}
			}
			for row := y; row <= bottom; row++ {
				fpsPixel(pixels, x, row, color[0], color[1], color[2])
			}
		}
	}
}

func fpsGraphY(duration, scale time.Duration, top, bottom int) int {
	if duration >= scale {
		return top
	}
	return bottom - int(int64(duration)*int64(bottom-top)/int64(scale))
}

func fpsPixel(pixels []byte, x, y int, r, g, b byte) {
	i := 4 * (y*fpsOverlayWidth + x)
	pixels[i], pixels[i+1], pixels[i+2], pixels[i+3] = r, g, b, 255
}
