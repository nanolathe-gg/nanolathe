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
// Terrain, sprites, UI, fog, models and their palette composites execute on the
// device. Model stages still awaiting GPU support carry explicit omissions;
// there is no CPU model-image fallback (DESIGN_GPU_RENDERER §9–§10).
package gpurender
