package frame

import "github.com/nanolathe-gg/nanolathe/internal/pool"

// DeveloperView is an opt-in detached observation of one committed tick.
// Missing slices and false availability flags mean unavailable, never zero
// measurements. See DESIGN_DEVELOPER_TOOLS §3 and [03 §3.12].
type DeveloperView struct {
	Tick                          uint32
	Width, Height                 int32
	SeaLevel                      uint8
	Cells                         []DeveloperCell
	MovementSubject               pool.Handle
	MovementTiers                 []uint8
	SearchWidth, SearchHeight     int32
	Search                        []DeveloperSearchCell
	CoverageWidth, CoverageHeight int32
	Coverage                      []uint8
	Units                         []DeveloperUnit
}

type DeveloperCell struct {
	Height, Metal uint8
	Ground, Air   uint16
	Feature       uint16
	Building      bool
}

// Search status and parent direction are the named grid fields in [03 §3.12].
type DeveloperSearchCell struct{ Status, Direction uint8 }

// DeveloperUnit supplements UnitView; identity is the existing publication
// identity, not an authoritative generation counter [I5][I6].
type DeveloperUnit struct {
	Dying                  bool
	MoveStance, FireStance uint8
	Slot                   pool.Handle
	InstanceID             uint64
	DisplayName            string
	AutoTarget             [3]bool
	AutoTargetAvailable    bool
	Builds                 []DeveloperBuildOption
	BuildsAvailable        bool
	MovementAvailable      bool
	// FootprintX/Z are the committed footprint origin in terrain cells.
	// Extents are UnitView.FootX/Z [03 R-COMP-01 §5].
	FootprintX, FootprintZ int32
	HasWaypoint            bool
	Route                  []DeveloperRoutePoint
}

type DeveloperBuildOption struct {
	Name           string
	Score          int32
	ScoreAvailable bool
}

// Route coordinates are whole world units, as stored by the route follower.
type DeveloperRoutePoint struct{ X, Z int32 }

// BeginDeveloper reuses only this frame slot's private diagnostic storage.
// Reset hides it from ordinary publications, including after an opt-out.
func (f *Frame) BeginDeveloper() *DeveloperView {
	if f.retainedDeveloper == nil {
		f.retainedDeveloper = &DeveloperView{}
	}
	d := f.retainedDeveloper
	*d = DeveloperView{
		Tick: f.Tick, Cells: d.Cells[:0], MovementTiers: d.MovementTiers[:0],
		Search: d.Search[:0], Coverage: d.Coverage[:0], Units: d.Units[:0],
	}
	f.Developer = d
	return d
}
