package gpurender

// quadVertices is the four-vertex quad every 2D family compiles into the
// scheduler's batches. The batches themselves are indexed with uint32 and bounded
// by schedRunVertexLimit, so no quad count can wrap an index
// [docs/DESIGN_GPU_RENDERER.md C-G3].
const quadVertices = 4
