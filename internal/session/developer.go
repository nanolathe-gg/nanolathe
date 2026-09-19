package session

import (
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
)

// SetDeveloperDiagnostics opts in to the next ordinary publication. It does
// not publish while paused or change either authoritative random stream
// (DESIGN_DEVELOPER_TOOLS §3.1) [I4][I6].
func (s *Session) SetDeveloperDiagnostics(enabled bool) {
	if s != nil {
		s.developerDiagnostics = enabled
	}
}

func (s *Session) publishDeveloper(f *frame.Frame) {
	if !s.developerDiagnostics {
		return
	}
	d := f.BeginDeveloper()
	if terrain := s.World; terrain != nil {
		d.Width, d.Height, d.SeaLevel = terrain.CellW, terrain.CellH, terrain.SeaLevel
		for _, cell := range terrain.Plot {
			d.Cells = append(d.Cells, frame.DeveloperCell{
				Height: cell.Height(), Metal: cell.Metal(), Feature: cell.Feature(),
				Ground: uint16(cell.OccupantA()), Air: uint16(cell.OccupantB()),
				Building: cell.StructureYard(),
			})
		}
		if s.Path.DeveloperSearchAvailable() {
			d.SearchWidth, d.SearchHeight = d.Width, d.Height
			for z := int32(0); z < d.Height; z++ {
				for x := int32(0); x < d.Width; x++ {
					status, direction := s.Path.DeveloperSearchCell(x, z)
					d.Search = append(d.Search, frame.DeveloperSearchCell{Status: status, Direction: direction})
				}
			}
		}
	}
	if s.Vis != nil {
		d.CoverageWidth, d.CoverageHeight = s.Vis.GridDimensions()
		// The diagnostic reads true-local coverage, independent of View's
		// observer and independent of the ordinary fog policy [03 §3.12].
		d.Coverage = append(d.Coverage, s.Vis.ByteGrid(visibility.PlayerID(s.LocalOwner))...)
	}
	if len(f.Selection.Handles) > 0 {
		d.MovementSubject = f.Selection.Handles[0]
		d.MovementTiers = s.Movement.DeveloperTiers(d.MovementSubject, d.MovementTiers)
	}
	// Follow the already-published pool order and reuse its publication
	// identity. Reading diagnostics cannot allocate a new identity [I5][I6].
	for _, view := range f.Units {
		u := s.Units.Unit(view.Slot)
		if u == nil {
			continue
		}
		i := len(d.Units)
		if i < cap(d.Units) {
			d.Units = d.Units[:i+1]
		} else {
			d.Units = append(d.Units, frame.DeveloperUnit{})
		}
		out := &d.Units[i]
		builds, route := out.Builds[:0], out.Route[:0]
		*out = frame.DeveloperUnit{Slot: view.Slot, InstanceID: view.InstanceID, Dying: u.Dying,
			Builds: builds, Route: route, AutoTargetAvailable: true,
			MoveStance: uint8((u.Flags >> units.StandingMoveShift) & units.StandingFieldMask),
			FireStance: uint8((u.Flags >> units.StandingFireShift) & units.StandingFieldMask)}
		for slot := range out.AutoTarget {
			out.AutoTarget[slot] = u.Slots[slot].IsAutonomous()
		}
		if u.Def != nil {
			out.DisplayName = u.Def.Name
			if s.Catalog != nil {
				out.BuildsAvailable = true
				key := u.Def.CanonicalKey
				if key == "" {
					key = content.CanonicalKey(u.Def.UnitName)
				}
				if menu := s.Catalog.BuildMenus[key]; menu != nil {
					for _, name := range s.buildProducts(key) {
						// Candidate scores need the owning player's AI context,
						// which human owners do not have here. This first delivery
						// leaves scores explicitly unavailable (DESIGN_DEVELOPER_TOOLS §3.1).
						out.Builds = append(out.Builds, frame.DeveloperBuildOption{Name: name})
					}
				}
			}
		}
		follower := s.Movement.DeveloperFollowerState(view.Slot)
		out.MovementAvailable = follower.Available
		out.FootprintX, out.FootprintZ = follower.Anchor.X, follower.Anchor.Z
		out.HasWaypoint = follower.HasWaypoint
		for _, point := range follower.Points[:follower.Count] {
			out.Route = append(out.Route, frame.DeveloperRoutePoint{X: int32(point.X), Z: int32(point.Z)})
		}
	}
}
