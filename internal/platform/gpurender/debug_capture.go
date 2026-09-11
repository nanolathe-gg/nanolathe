package gpurender

import (
	"unsafe"

	"github.com/hajimehoshi/ebiten/v2"
)

type debugStorage struct {
	Name                         string
	Length, Capacity, IdleOffset int
	RetainedBytes                uintptr
}

func debugSlice[T any](name string, v []T) debugStorage {
	var x T
	return debugStorage{Name: name, Length: len(v), Capacity: cap(v), RetainedBytes: uintptr(cap(v)) * unsafe.Sizeof(x)}
}
func debugArena[T any](name string, a *frameArena[T]) debugStorage {
	d := debugSlice(name, a.buf)
	d.IdleOffset = a.off
	return d
}

// recordSubmission observes the actual slices handed to Ebitengine. Keeping
// one high-water Execute distinguishes a large frame from dependency queues
// that accumulate ordinary frames while presentation is suppressed.
func (r *Renderer) recordSubmission(vertices, indices int) {
	r.modelStats.SubmittedVertices += vertices
	r.modelStats.SubmittedIndices += indices
	r.modelStats.MaxSubmissionVertices = max(r.modelStats.MaxSubmissionVertices, vertices)
}

type debugImage struct {
	Name          string
	Width, Height int
	RGBABytes     int64
}

// DebugSnapshot reads existing renderer storage on its device owner.
func (r *Renderer) DebugSnapshot() map[string]any {
	if r == nil {
		return nil
	}
	storage := []debugStorage{debugArena("strips", &r.modelPrep.strips), debugArena("vertices", &r.modelPrep.vertices), debugArena("prepared", &r.modelPrep.prepared), debugArena("crosses", &r.modelPrep.crosses), debugArena("ears", &r.modelPrep.ears), debugArena("indices", &r.modelPrep.indices), debugSlice("scheduler_phases", r.sched.phases), debugSlice("scheduler_owners", r.sched.owners), debugSlice("scheduler_cells", r.sched.cells), debugSlice("scheduler_point_phase", r.sched.pointPhase), debugSlice("model_vertices", r.modelAtlas.verts), debugSlice("model_resident", r.modelAtlas.resident), debugSlice("model_resolves", r.modelAtlas.resolves), debugSlice("scene_upload", r.scene.uploadBuf), debugSlice("scene_padding", r.scene.padBuf)}
	for _, bucket := range r.sched.vertPool {
		for _, v := range bucket {
			storage = append(storage, debugSlice("scheduler_pooled_vertices", v))
		}
	}
	for _, bucket := range r.sched.idxPool {
		for _, v := range bucket {
			storage = append(storage, debugSlice("scheduler_pooled_indices", v))
		}
	}
	for _, phase := range r.sched.phases {
		for _, batch := range phase.batch {
			storage = append(storage, debugSlice("scheduler_batch_runs", batch.runs), debugSlice("scheduler_batch_vertices", batch.verts), debugSlice("scheduler_batch_indices", batch.idx))
		}
	}
	images := []debugImage{}
	add := func(name string, w, h int) { images = append(images, debugImage{name, w, h, int64(w) * int64(h) * 4}) }
	for _, p := range r.scene.pages {
		if p != nil {
			add("scene_page", p.w, p.h)
		}
	}
	for _, p := range []*modelPage{&r.modelAtlas.page, &r.modelAtlas.overflow[0], &r.modelAtlas.overflow[1]} {
		if p.img != nil {
			add("model_color_page", p.w, p.h)
		}
		if p.key != nil {
			add("model_key_page", p.w, p.h)
		}
		if p.post != nil {
			add("model_post_page", p.w, p.h)
		}
	}
	for _, img := range r.surfaces {
		if img != nil {
			b := img.Bounds()
			add("surface", b.Dx(), b.Dy())
		}
	}
	return map[string]any{"width": r.w,
		"height":                r.h,
		"model_frame_counter":   r.modelAtlas.frame,
		"frame_device_draws":    r.frameDraws,
		"model_stats":           r.ModelStats(),
		"submission_frame":      r.submissionFrame,
		"peak_submission_frame": r.peakSubmissionFrame,
		"peak_submission_stats": r.peakSubmissionStats,
		"storage":               storage,
		"images":                images,
		"gaf_image_count":       len(r.gafImages),
		"terrain_atlas_count":   len(r.tileAtlases),
		"scene_frame_count":     len(r.scene.frames),
		"scene_page_count":      len(r.scene.pages),
		"model_slots":           len(r.modelAtlas.slots),
		"note":                  "Arena idle offsets are reset after Execute and can rewind on growth; they are not last-frame peaks. RGBA estimates cover listed logical images only, exclude Ebitengine backing textures/driver overhead and unlisted texture caches. Outline raw-versus-clipped spans and obsolete reference retention are unavailable without new instrumentation."}
}

// DebugLastFrame returns the existing offscreen composition without executing.
// Read it synchronously on the owner; it is not a detached image.
func (r *Renderer) DebugLastFrame() *ebiten.Image {
	if r == nil || r.surfaces[0] == nil {
		return nil
	}
	return r.surfaces[0]
}
