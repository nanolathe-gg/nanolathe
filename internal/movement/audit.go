package movement

import (
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

// AuditVerdict is the small set of observations needed by a movement audit.
// It is deliberately separate from path.Status: an audit describes evidence at
// a cell and does not choose a gameplay response [04 §6.1][04 §7.2].
type AuditVerdict uint8

const (
	AuditUnknown AuditVerdict = iota
	AuditPass
	AuditBlocked
)

// AuditFinding identifies a likely fault domain only when the supplied
// evidence supports it. It is diagnostic classification, not a movement
// policy. Unknown cases remain Planner rather than receiving an invented
// explanation [04 §7.2][04 §7.5].
type AuditFinding uint8

const (
	FindingUnknown AuditFinding = iota
	FindingAuthoredNonblocking
	FindingStampMismatch
	FindingStaleRoute
	FindingPlanner
)

// AuditBounds is an optional half-open cell-space rectangle for presentation
// geometry. The movement layer does not derive sprite bounds: callers provide
// them when the renderer has resolved them.
type AuditBounds struct {
	MinX, MinZ int32
	MaxX, MaxZ int32
	Valid      bool
}

// FeatureAudit records both the raw plot reference and the resolved feature
// index. DefinitionResolved says whether that index has a catalog definition;
// a stale/unbound index is therefore never reported as nonblocking. A fringe
// reference has no definition of its own; Anchor points to the real feature
// cell resolved through its signed offsets [fmt tnt][04 §6.2].
type FeatureAudit struct {
	RawRef             uint16
	ResolvedRef        uint16
	Resolved           bool
	DefinitionResolved bool
	Definition         string
	Blocking           bool
	Anchor             Cell
	HasAnchor          bool
	FootprintX         int32
	FootprintZ         int32
	Sprite             bool
	SpriteAsset        string
}

// ProfileAudit contains the exact profile and cell verdict observed by the
// audit call. ClassSteep remains passable, matching search consumption
// [04 §6.1].
type ProfileAudit struct {
	FootprintX int32
	FootprintZ int32
	Class      CellClass
	Verdict    AuditVerdict
}

// TerrainAudit retains the derived terrain values used by the profile gate.
// HasCell is false for an out-of-bounds coordinate or nil terrain.
type TerrainAudit struct {
	MinHeight uint8
	MaxHeight uint8
	SeaLevel  uint8
	HasCell   bool
}

// MovementAudit is one deterministic, opt-in observation of a movement cell.
// Optional fields are marked explicitly so a missing renderer, structure
// registry, or revision source cannot be mistaken for a clear result.
type MovementAudit struct {
	Cell    Cell
	Profile ProfileAudit
	Terrain TerrainAudit
	Feature FeatureAudit

	CompletedStructureBlocked bool
	HasStructureVerdict       bool
	MobileOccupantA           int16
	MobileOccupantB           int16
	HasMobileOccupant         bool

	StaticLayerValue    uint8
	HasStaticLayerValue bool
	RouteRevision       uint64
	HasRouteRevision    bool
	StaticRevision      uint64
	HasStaticRevision   bool

	RenderedAnchor    Cell
	HasRenderedAnchor bool
	RenderedBounds    AuditBounds
	HasRenderedBounds bool

	RayPassable   bool
	RayAccepted   bool
	HasRayVerdict bool
	Finding       AuditFinding
}

// AuditContext supplies evidence that belongs to systems outside the profile
// classifier. It is intentionally input-only: an audit cannot alter a route
// or cause a restamp. Route/static revision and ray values are optional because
// current callers do not all expose them [04 §7.3].
type AuditContext struct {
	CompletedStructureBlocked bool
	HasStructureVerdict       bool

	StaticLayerValue    uint8
	HasStaticLayerValue bool
	RouteRevision       uint64
	HasRouteRevision    bool
	StaticRevision      uint64
	HasStaticRevision   bool

	RenderedAnchor    Cell
	HasRenderedAnchor bool
	RenderedBounds    AuditBounds
	HasRenderedBounds bool

	RayPassable   bool
	RayAccepted   bool
	HasRayVerdict bool
	// Finding may be set by a caller with stronger evidence than this generic
	// classifier. Zero asks ClassifyAuditFinding to use the observable fields.
	Finding AuditFinding
}

// AuditSink receives observations. A nil sink disables the audit completely.
// Implementations must not feed observations back into simulation state.
type AuditSink interface {
	RecordMovementAudit(MovementAudit)
}

// AuditCollector is a bounded test/diagnostic sink. It is not attached to the
// simulation by default, so production movement does not retain an unbounded
// record history.
type AuditCollector struct {
	Records []MovementAudit
	Limit   int
}

// RecordMovementAudit appends until Limit is reached. A non-positive limit is
// disabled.
func (c *AuditCollector) RecordMovementAudit(a MovementAudit) {
	if c == nil || c.Limit <= 0 || len(c.Records) >= c.Limit {
		return
	}
	c.Records = append(c.Records, a)
}

// AuditCell captures the classifier and world evidence at one cell. It does
// not stamp, clear, resolve, or otherwise mutate the terrain [04 §6.1].
func AuditCell(t *world.Terrain, p Profile, cell Cell, ctx AuditContext) MovementAudit {
	a := MovementAudit{Cell: cell, Finding: ctx.Finding}
	a.Profile.FootprintX, a.Profile.FootprintZ = p.footprintSize()
	a.Terrain.SeaLevel = tSeaLevel(t)

	plot := tPlot(t, cell)
	if plot != nil {
		a.Terrain.HasCell = true
		a.Terrain.MinHeight = plot.MinHeight()
		a.Terrain.MaxHeight = plot.MaxHeight()
		a.Feature.RawRef = plot.Feature()
		a.MobileOccupantA = plot.OccupantA()
		a.MobileOccupantB = plot.OccupantB()
		a.HasMobileOccupant = a.MobileOccupantA != 0 || a.MobileOccupantB != 0
		if resolved, ok := world.ResolveFeature(t.Plot, int(t.CellW), int(t.CellH), int(cell.X), int(cell.Z)); ok {
			a.Feature.ResolvedRef = resolved
			a.Feature.Resolved = true
			if def, exists := t.FeatureDefAt(resolved); exists && def != nil {
				a.Feature.DefinitionResolved = true
				a.Feature.Definition = def.CanonicalKey
				a.Feature.Blocking = def.Blocking
				a.Feature.FootprintX = def.FootprintX
				a.Feature.FootprintZ = def.FootprintZ
				a.Feature.Sprite = def.Filename != ""
				a.Feature.SpriteAsset = def.Filename
				if def.SeqName != "" {
					a.Feature.SpriteAsset += ":" + def.SeqName
				}
			}
			a.Feature.Anchor = cell
			a.Feature.HasAnchor = true
			if plot.IsFringe() {
				a.Feature.Anchor.X += int32(plot.AnchorDXSigned())
				a.Feature.Anchor.Z += int32(plot.AnchorDZSigned())
			}
		}
	}
	class := p.Classify(t, cell.X, cell.Z)
	a.Profile.Class = class
	a.Profile.Verdict = AuditBlocked
	if class != ClassBlocked {
		a.Profile.Verdict = AuditPass
	}

	a.CompletedStructureBlocked = ctx.CompletedStructureBlocked
	a.HasStructureVerdict = ctx.HasStructureVerdict
	a.StaticLayerValue = ctx.StaticLayerValue
	a.HasStaticLayerValue = ctx.HasStaticLayerValue
	a.RouteRevision = ctx.RouteRevision
	a.HasRouteRevision = ctx.HasRouteRevision
	a.StaticRevision = ctx.StaticRevision
	a.HasStaticRevision = ctx.HasStaticRevision
	a.RenderedAnchor = ctx.RenderedAnchor
	a.HasRenderedAnchor = ctx.HasRenderedAnchor
	a.RenderedBounds = ctx.RenderedBounds
	a.HasRenderedBounds = ctx.HasRenderedBounds
	a.RayPassable = ctx.RayPassable
	a.RayAccepted = ctx.RayAccepted
	a.HasRayVerdict = ctx.HasRayVerdict
	if a.Finding == FindingUnknown {
		a.Finding = ClassifyAuditFinding(a)
	}
	return a
}

func tSeaLevel(t *world.Terrain) uint8 {
	if t == nil {
		return 0
	}
	return t.SeaLevel
}

func tPlot(t *world.Terrain, c Cell) *world.PlotCell {
	if t == nil {
		return nil
	}
	return t.PlotAt(c.X, c.Z)
}

// ClassifyAuditFinding selects a finding from evidence with no gameplay
// consequence. Explicit caller evidence should be supplied through
// AuditContext.Finding when more than one explanation is possible.
func ClassifyAuditFinding(a MovementAudit) AuditFinding {
	if a.Feature.DefinitionResolved && !a.Feature.Blocking {
		return FindingAuthoredNonblocking
	}
	if a.HasRouteRevision && a.HasStaticRevision && a.RouteRevision != a.StaticRevision {
		return FindingStaleRoute
	}
	if a.Feature.Resolved && a.Feature.Blocking && a.HasStaticLayerValue && a.StaticLayerValue != LayerBlocked {
		return FindingStampMismatch
	}
	return FindingPlanner
}

// AuditRouteFailure records a caller-selected set of cells involved in a
// failed route. The caller controls ordering, preserving the route's own
// deterministic order and avoiding map iteration [I1][04 §7.2].
func AuditRouteFailure(sink AuditSink, t *world.Terrain, p Profile, cells []Cell, ctx AuditContext) {
	if sink == nil {
		return
	}
	for _, cell := range cells {
		sink.RecordMovementAudit(AuditCell(t, p, cell, ctx))
	}
}
