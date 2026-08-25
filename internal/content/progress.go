package content

// Progress observes a whole-install compile as it advances. It is presentation
// plumbing for the retail loading screen [07 §4] and never changes what is
// compiled: every compiler must produce the same catalog with a nil Progress.
//
// percent is 0..100 within one family and rises monotonically; a family always
// ends with a report at 100. Reports arrive on the goroutine running the
// compile, which is the loading thread, so an observer that touches renderer
// state must hand it across itself [I6].
type Progress func(family string, percent int)

// The families Compile visits, in visit order. They are the Nanolathe content
// catalogs, not the six retail loading-screen bars — the frontend owns that
// mapping.
const (
	FamilyWeapons      = "weapons"
	FamilyUnits        = "units"
	FamilyFeatures     = "features"
	FamilyMovement     = "movement"
	FamilySides        = "sides"
	FamilySounds       = "sounds"
	FamilyMaps         = "maps"
	FamilyAIProfiles   = "aiprofiles"
	FamilyBattleTables = "battletables"
	FamilyBuildMenus   = "buildmenus"
	FamilyModels       = "models"
)

// Report delivers one observation. It is safe on a nil Progress, so callers
// never branch on whether an observer exists.
func (p Progress) Report(family string, percent int) {
	if p == nil {
		return
	}
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	p(family, percent)
}
