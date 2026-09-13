package client

import (
	"sort"
	"unsafe"

	"github.com/nanolathe-gg/nanolathe/internal/camera"
	"github.com/nanolathe-gg/nanolathe/internal/input"
)

type DebugStorage struct {
	Name             string
	Length, Capacity int
	RetainedBytes    uintptr
}

func debugStorage[T any](name string, v []T) DebugStorage {
	var x T
	return DebugStorage{name, len(v), cap(v), uintptr(cap(v)) * unsafe.Sizeof(x)}
}
func debugModelScratch(s *modelScratch) []DebugStorage {
	out := []DebugStorage{debugStorage("states", s.states), debugStorage("draws", s.draws), debugStorage("packets", s.packets), debugStorage("polys", s.polys), debugStorage("images", s.images), debugStorage("outlines", s.outlines), debugStorage("projectiles", s.projectiles)}
	for _, v := range s.states {
		out = append(out, debugStorage("piece_states", v))
	}
	for _, p := range s.packets {
		if p != nil {
			out = append(out, debugStorage("packet_vertices", p.vertices), debugStorage("packet_cached_faces", p.cachedFaces), debugStorage("packet_cached_vertices", p.cachedVertices), debugStorage("packet_supersample_faces", p.supersampleFaces), debugStorage("packet_supersample_vertices", p.supersampleVerts))
		}
	}
	for _, p := range s.outlines {
		if p != nil {
			out = append(out, debugStorage("outline_faces", p.faces), debugStorage("outline_vertices", p.verts), debugStorage("outline_spans", p.spans))
		}
	}
	for _, p := range s.polys {
		if p != nil {
			out = append(out, debugStorage("polygons", p.polys), debugStorage("polygon_lanes", p.lanes), debugStorage("polygon_odd", p.odd))
		}
	}
	for _, p := range s.images {
		if p != nil {
			out = append(out, debugStorage("image_keys", p.keys))
		}
	}
	return out
}
func debugGeometryStore(s *cachedGeometryStore) []DebugStorage {
	return []DebugStorage{debugStorage("faces", s.faces), debugStorage("vertices", s.vertices), debugStorage("supersample_faces", s.supersampleFaces), debugStorage("supersample_vertices", s.supersampleVerts)}
}

// DebugSnapshot joins all record workers and copies diagnostics without a draw,
// interpolation refresh, palette advance, or borrowed frame/object serialization.
func (c *Client) DebugSnapshot() map[string]any {
	if c == nil {
		return nil
	}
	c.JoinPreRecord()
	hits, misses, launches := c.PreRecordCounts()
	d := map[string]any{"presentation_paused": c.PresentationPaused(), "width": c.width,
		"height":                    c.height,
		"record_width":              c.recordW,
		"record_height":             c.recordH,
		"focused":                   c.focused,
		"pointer_captured":          c.pointerCaptured,
		"tick_fraction_16":          c.tickFraction16,
		"camera_fraction_16":        c.cameraFraction16,
		"interpolation":             c.interpolation,
		"last_recorded_tick":        c.frameTick,
		"presentation_epoch":        c.presentationEpoch,
		"pre_record_hits":           hits,
		"pre_record_misses":         misses,
		"pre_record_launches":       launches,
		"pre_record_nanos":          c.PreRecordNanos(),
		"model_count":               len(c.models),
		"texture_count":             len(c.texIndex),
		"orientation_cache_count":   len(c.modelOrientation),
		"cached_body_count":         len(c.cachedModelBodies),
		"feature_banks":             len(c.featureGAFs),
		"feature_frames":            len(c.featureFrames),
		"detail_frames":             len(c.detailFrames),
		"effect_banks":              len(c.effectBanks),
		"art_diagnostics":           append([]ArtDiagnostic(nil), c.artDiagnostics...),
		"art_diagnostics_truncated": c.artDiagnosticsTruncated,
		"recorded_effect_stats":     c.effectStats,
		"recorded_strip_stats":      c.stripStats,
		"art_stats_note":            "Counters describe the latest recording pass, including a joined speculative pass; they reset on every recording and do not accumulate replays.",
		"storage":                   []DebugStorage{debugStorage("indexed", c.indexed), debugStorage("rgba", c.rgba), debugStorage("surface_arena", c.surfaceArena), debugStorage("point_arena", c.pointArena), debugStorage("unit_jobs", c.unitJobs), debugStorage("effect_draws", c.effectDraws), debugStorage("projectile_draws", c.projectileDraws)},
		"main_model_scratch":        debugModelScratch(&c.modelScratch),
		"storage_note":              "Backing-array estimates exclude referenced objects, map buckets, allocator overhead, image internals and any obsolete retained tails. Capacities describe retention; lengths are current storage, not a last-frame high-water measurement."}
	if c.projectileGAFErr != nil {
		d["projectile_gaf_error"] = c.projectileGAFErr.Error()
	}
	if c.cam != nil {
		cam := camera.Camera(*c.cam)
		d["camera"] = cam
	}
	if c.buffer != nil {
		if f := c.buffer.Current(); f != nil {
			d["committed_tick"] = f.Tick
			d["committed_paused"] = f.Paused
		}
	}
	d["input"] = input.SampleFromState(c.Input(), 0, int32(c.width), int32(c.height))
	d["selection_drag"] = c.selectionDrag
	workers := [][]DebugStorage{}
	if c.recordPool != nil {
		for _, w := range c.recordPool.workers {
			workers = append(workers, debugModelScratch(&w.clone.modelScratch))
		}
	}
	d["worker_model_scratch"] = workers
	keys := make([]uint64, 0, len(c.cachedModelBodies))
	for k := range c.cachedModelBodies {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	type bodyStorage struct {
		Identity     uint64
		Body, Shadow []DebugStorage
	}
	bodies := make([]bodyStorage, 0, len(keys))
	for _, k := range keys {
		b := c.cachedModelBodies[k]
		if b != nil {
			bodies = append(bodies, bodyStorage{k, debugGeometryStore(&b.store), debugGeometryStore(&b.shadowStore)})
		}
	}
	d["cached_geometry_storage"] = bodies
	return d
}
