// Package gpurender is the modern (GPU) executor for a recorded committed-frame
// draw list (docs/DESIGN_GPU_RENDERER.md §2.3). It replays the same
// internal/drawlist.List the classic software executor replays, but through
// Ebitengine, composing the retail frame in true colour (§13).
//
// This package is one of the few permitted to import Ebitengine; it joins
// internal/platform/ebitenapp, internal/audiobackend and cmd/nanolathe in the
// architecture test's platform allowlist. internal/drawlist and internal/client
// stay device-free: the client records, this package executes.
//
// Sources stay indexed and their arithmetic stays exact (C-G4): palette indices
// ride the red channel of RGBA8 images, and every source-side table is sampled
// with nearest filtering at integer texel coordinates, never linearly filtered.
// What each fragment finally writes is that index resolved through PALETTE.PAL,
// the same lookup the software expansion made, applied per fragment rather than
// once per frame (C-G8 as amended). The destination-side tables — ALP, the LHT
// and SHD rows, GRAY — become device blends and a desaturation carrying the
// arithmetic those tables were generated from [03 §4.3.4], which reproduces the
// retail composite up to palette rounding (§13.2).
//
// Terrain, sprites, UI, fog, models and their palette composites execute on the
// device. Model stages still awaiting GPU support carry explicit omissions;
// there is no CPU model-image fallback (DESIGN_GPU_RENDERER §9–§10).
//
// The executor replays recorded screen coordinates, so the detail view scale is
// almost entirely the recorder's business (§14.2). Two things here read it: the
// terrain atlas, which holds one 32·s square per tile so the tile blit stays a
// 1:1 copy (§14.5), and the fog pass, whose cell lattice is 32·s and whose GAF
// cell is drawn from the frame's 2× variant. Sprite variants arrive as ordinary
// frames and model geometry arrives already projected and scaled.
package gpurender
