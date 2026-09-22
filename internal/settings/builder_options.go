package settings

// BuilderOptions persists the local player's preferences. Array indices are
// Hold Position, Maneuver and Roam; values follow the labels in
// DESIGN_COMMUNITY_PATCH §4.3. This leaf package keeps only configuration data.
type BuilderOptions struct {
	Guard  [3]int `json:"guard"`
	Patrol [3]int `json:"patrol"`
}

func DefaultBuilderOptions() BuilderOptions {
	return BuilderOptions{Guard: [3]int{1, 1, 1}, Patrol: [3]int{0, 1, 1}}
}

func (b *BuilderOptions) Normalize() {
	for i := range b.Guard {
		if b.Guard[i] < 0 || b.Guard[i] > 2 {
			b.Guard[i] = 1 // Cavedog
		}
		if b.Patrol[i] < 0 || b.Patrol[i] > 2 {
			b.Patrol[i] = 1 // Both
		}
	}
}
