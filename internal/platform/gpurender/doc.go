// Package gpurender is the modern (GPU) executor for a recorded committed-frame
// draw list (docs/DESIGN_GPU_RENDERER.md §2.3). It replays the same
// internal/drawlist.List the classic software executor replays, but through
// Ebitengine, staying in palette-index space for the whole retail composite and
// expanding index→RGBA exactly once at the end (C-G1, C-G8).
//
// This package is one of the few permitted to import Ebitengine; it joins
// internal/platform/ebitenapp, internal/audiobackend and cmd/nanolathe in the
// architecture test's platform allowlist. internal/drawlist and internal/client
// stay device-free: the client records, this package executes.
//
// Index arithmetic is exact (C-G4). Palette indices ride the red channel of
// RGBA8 images; every table is sampled with nearest filtering at integer texel
// coordinates, never linearly filtered, and no blend arithmetic runs on indices.
// The final expansion maps index→colour through PALETTE.PAL alone and forces
// alpha opaque, matching the software convertIndexedToRGBA (C-G8).
//
// Scope of this unit (WU-2.1): the package skeleton, the palette-table textures,
// the index→RGBA expansion shader, and a Sink whose only live methods are Clear
// (fill the indexed offscreen with index 0) and Expand (run the expansion
// shader). Every other draw family is a stub; a replayed frame is therefore
// Clear then Expand, so the whole surface becomes PALETTE.PAL[0], the correct
// expansion of an empty frame. Later units fill in the drawing families.
package gpurender
