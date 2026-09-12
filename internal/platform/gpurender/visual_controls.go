package gpurender

// SetMaterials and SetScorch are local comparison controls for Enhanced
// presentation (GPU design §29). They never change simulation or saved state.
func (r *Renderer) SetMaterials(enabled bool) { r.materialsEnabled = enabled }
func (r *Renderer) SetScorch(enabled bool)    { r.scorchEnabled = enabled }
