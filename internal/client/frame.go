package client

import (
	"kaijuengine.com/matrix"
	"kaijuengine.com/rendering"

	"github.com/nanolathe/nanolathe/internal/snapshot"
)

// Frame draws one frame; never mutates sim. It reads snapshot.Buffer.Read()
// and interpolates with alpha clamped [0,1] (C9). The sim is authoritative at
// 30 Hz and the renderer interpolates between ticks for smooth modern motion
// (I6). No sim package's mutable state is imported or written here.
//
// Framebuffer path (verified against ../kaiju/src):
//  1. Compose into own []uint8 indexed framebuffer at logical size.
//  2. Convert to RGBA through palette.Tables.Logical→Base at present time only (C7).
//  3. Upload through a cached nearest-filtered texture on Kaiju's render
//     boundary, then present it with one persistent fixed-position UI image.
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

	if c.host == nil || c.host.Window == nil {
		// Headless or not yet launched: no window, no display (C11). Still
		// read the snapshot to satisfy the "reads snapshot.Buffer.Read()"
		// contract even when not drawing, but do not mutate it.
		_, _, _ = c.buffer.Read()
		return
	}

	// Handle negotiated size changes (the single Kaiju path preserves the
	// concept). If the window was resized, reallocate buffers and discard the
	// texture so it is recreated at the new size and rebound to the image.
	w := c.host.Window.Width()
	h := c.host.Window.Height()
	if w <= 0 || h <= 0 {
		w = c.width
		h = c.height
	}
	if w != c.width || h != c.height {
		c.width = w
		c.height = h
		c.indexed = make([]uint8, w*h)
		c.rgba = make([]byte, w*h*4)
		c.texture = nil
	}

	// C9: read snapshot. The call is presentation-only; the sim never reads
	// this package and never observes alpha (I6). On a 5-tick burst the
	// renderer sees only the final pair; intermediate ticks are not drawn
	// (PLAN_03 C15). If ticksToRun==0 the same pair is returned again and
	// alpha saturates at 1.0 with no extrapolation.
	prev, cur, ok := c.buffer.Read()

	// Compose. For WU-04A-1 the terrain and unit buckets are not yet present,
	// so we produce a visibly correct alpha ramp that proves the 60 fps loop
	// is interpolating against the injected 30 Hz stub. The ramp moves a
	// vertical bar smoothly with alpha and shows tick feedback when a buffer
	// exists.
	c.composeIndexed(alpha, prev, cur, ok)

	// Convert to RGBA through logical→base at present time only (C7). This is
	// the only point where indexed pixels become RGBA so palette animation
	// stays possible in later phases.
	c.convertIndexedToRGBA()

	// Upload. Create once with NewTextureFromMemory at TextureFilterNearest;
	// per frame TextureWritePixels with Region 0,0,w,h. Use nearest filtering
	// — bilinear on indexed-derived pixels destroys the look.
	c.ensureResources()
	if c.texture == nil {
		return
	}
	texture := c.texture
	req := rendering.GPUImageWriteRequest{
		Region: matrix.Vec4i{0, 0, int32(c.width), int32(c.height)},
		Pixels: c.rgba,
	}
	// Kaiju's updater workers are concurrent. Queue Vulkan work for the
	// locked render boundary after updates join, keeping it off a worker OS
	// thread on macOS/MoltenVK.
	c.host.RunBeforeRender(func() {
		if texture == nil || !texture.RenderId.IsValid() || c.host.Window == nil ||
			c.host.Window.GpuInstance == nil || !c.host.Window.GpuInstance.IsValid() {
			return
		}
		device := c.host.Window.GpuInstance.PrimaryDevice()
		if device != nil {
			texture.WritePixels(device, []rendering.GPUImageWriteRequest{req})
		}
	})
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
	// Gate 1: real terrain from TNT + palette [PLAN_04A]. When terrain and
	// camera are present, blit the tile map instead of the placeholder gradient.
	if c.terrain != nil && c.cam != nil {
		BlitTerrain(c.indexed, w, h, c.terrain, c.cam)
		// Overlay: debug info via FNT when available [03 §7.1] C8.
		if c.fnt != nil {
			// Use snapshot tick if available, else 0.
			var tick uint32
			if ok && cur != nil {
				tick = cur.Tick
			}
			info := DebugInfo{
				Tick:  tick,
				Alpha: alpha,
				CamX:  c.cam.X,
				CamZ:  c.cam.Z,
			}
			// Draw with shadow for visibility on any terrain palette.
			DrawDebugOverlayWithShadow(c.indexed, w, h, c.fnt, info, 255, 0, true)
		} else {
			// Fallback: small alpha bar when font not loaded, to keep visibly correct ramp.
			barX := int(alpha * float32(w-1))
			for y := 0; y < 4 && y < h; y++ {
				for dx := -1; dx <= 1; dx++ {
					x := barX + dx
					if x < 0 || x >= w {
						continue
					}
					c.indexed[y*w+x] = 255
				}
			}
		}
		// Still encode tick bar when snapshot present for verification.
		if ok && prev != nil && cur != nil {
			_ = prev
			tick := cur.Tick
			for y := 0; y < 4 && y < h; y++ {
				for x := 0; x < 16 && x < w; x++ {
					bit := (tick >> uint(x)) & 1
					val := byte(30)
					if bit == 1 {
						val = 200
					}
					// Only overwrite where we haven't drawn terrain? For debug, just overwrite top rows.
					c.indexed[y*w+x] = val
				}
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
	// If we have a real snapshot, encode the tick in the top rows so the
	// viewer can verify that the stub is ticking at 30 Hz while the bar
	// interpolates at 60 fps. This is presentation-only and does not mutate.
	if ok && prev != nil && cur != nil {
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

// convertIndexedToRGBA converts the indexed framebuffer to RGBA through
// c.logical → c.base at present time only (C7). Later phases replace the
// fallback grayscale tables with real PALETTE.PAL / GUIPAL.PAL / ALP / LHT /
// SHD and the 256-byte logical→physical lookup.
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
