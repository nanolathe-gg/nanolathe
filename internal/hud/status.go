package hud

// Snapshot-only status readouts for the battle HUD.
//
// These helpers deliberately consume only the immutable presentation frame.
// They do not retain pointers into a session, economy service, unit pool, or
// order queue.  A missing publication remains absent/unknown; in particular,
// a HUD readout must not guess a queue length or a stalled state.

import (
	"fmt"
	"math"
	"strings"

	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/pool"
)

// HUD palette roles are logical GUI entries, not physical palette indices.
// [07 §6] says normal text uses 15, production uses 10, and consumption uses
// 12; the active palette maps those entries at presentation time.
const (
	PaletteNormal      uint8 = 15
	PaletteProduction  uint8 = 10
	PaletteConsumption uint8 = 12
)

// PaletteRole is a formatted text value and its logical HUD color role.
type PaletteRole struct {
	Text    string
	Palette uint8
}

// StatusBar is one current-over-capacity resource bar. Fraction is clamped to
// [0,1] for drawing; Current and Capacity remain the authored float32 values.
type StatusBar struct {
	Current  float32
	Capacity float32
	Fraction float32
}

// ResourceStatus contains the stock, capacity, and latched rates for one
// local player. Rate text is already formatted for the side HUD.
type ResourceStatus struct {
	Present bool
	Player  uint8

	Energy StatusBar
	Metal  StatusBar

	EnergyCurrent  string
	EnergyCapacity string
	MetalCurrent   string
	MetalCapacity  string

	EnergyProduced PaletteRole
	EnergyConsumed PaletteRole
	MetalProduced  PaletteRole
	MetalConsumed  PaletteRole
	EnergyZero     PaletteRole
	MetalZero      PaletteRole
}

// ConstructionStatus describes a selected builder's published target. A
// zero Present value means no selected construction record was published.
// Percent is completion (retail's remaining fraction is inverted for display).
type ConstructionStatus struct {
	Present    bool
	Builder    pool.Handle
	Product    pool.Handle
	ProductKey string

	Remaining     float32
	Percent       int
	Health        int32
	MaxHealth     int32
	HealthPercent int

	Factory         bool
	QueueIndex      int32
	QueueIndexKnown bool
	Stalled         bool
	StalledKnown    bool
}

// FactoryStatus contains the published active product and queue records. A
// queue count is known only when a matching OrderQueueView was published.
type FactoryStatus struct {
	Present         bool
	Builder         pool.Handle
	Product         pool.Handle
	ProductKey      string
	QueueIndex      int32
	QueueIndexKnown bool

	QueueCount          int
	QueueCountKnown     bool
	PrimaryQueueCount   int
	SecondaryQueueCount int
	QueueProductKeys    []string

	Stalled      bool
	StalledKnown bool
}

// OrderStatus is the canonical descriptor status shown for a selected unit.
// Name is the immutable descriptor name from the frame. Label is populated
// only for established descriptor labels; unknown kinds keep Label empty.
type OrderStatus struct {
	Present      bool
	Unit         pool.Handle
	Target       pool.Handle
	Name         string
	Label        string
	List         uint8
	Index        uint16
	State        uint8
	MoveState    uint8
	BuildProduct string
}

// SelectedStatus is the selected-unit portion of a HUD readout.
type SelectedStatus struct {
	Present       bool
	Unit          pool.Handle
	Health        int32
	MaxHealth     int32
	HealthPercent int
	Construction  ConstructionStatus
	Factory       FactoryStatus
	Order         OrderStatus
}

// Status is the complete snapshot-only status readout consumed by a HUD.
type Status struct {
	Selected  SelectedStatus
	Resources ResourceStatus
}

// SnapshotStatus builds a local HUD status from f. If selected is nil, the
// frame's immutable Selection.Handles are used only when Selection.LocalPlayer
// matches localPlayer. A non-nil selected slice is used verbatim in caller
// order; it is never sorted or mutated.
func SnapshotStatus(f *frame.Frame, localPlayer uint8, selected []pool.Handle) Status {
	if f == nil {
		return Status{}
	}
	if selected == nil {
		if f.Selection.LocalPlayer == localPlayer {
			selected = f.Selection.Handles
		}
	}
	return Status{
		Selected:  selectedStatus(f, localPlayer, selected),
		Resources: ResourceStatusFor(f, localPlayer),
	}
}

// HUDStatus is an explicit-name alias useful at integration call sites.
func HUDStatus(f *frame.Frame, localPlayer uint8, selected []pool.Handle) Status {
	return SnapshotStatus(f, localPlayer, selected)
}

// StatusForFrame is a descriptive alias for SnapshotStatus.
func StatusForFrame(f *frame.Frame, localPlayer uint8, selected []pool.Handle) Status {
	return SnapshotStatus(f, localPlayer, selected)
}

// SelectedStatusFor returns the first selected local unit's status. Build and
// factory fields come only from BuildProgress and OrderQueue publications;
// health falls back to the selected UnitView when no construction target is
// published.
func SelectedStatusFor(f *frame.Frame, localPlayer uint8, selected []pool.Handle) SelectedStatus {
	if f == nil {
		return SelectedStatus{}
	}
	if selected == nil && f.Selection.LocalPlayer == localPlayer {
		selected = f.Selection.Handles
	}
	return selectedStatus(f, localPlayer, selected)
}

func selectedStatus(f *frame.Frame, localPlayer uint8, selected []pool.Handle) SelectedStatus {
	for _, handle := range selected {
		if handle == 0 {
			continue
		}
		if u := unitForLocal(f, handle, localPlayer); u != nil {
			out := SelectedStatus{Present: true, Unit: handle, Health: u.Health, MaxHealth: u.MaxHealth}
			out.HealthPercent = healthPercent(u.Health, u.MaxHealth)
			if b, ok := buildForSelection(f, handle); ok {
				out.Construction = constructionStatus(b)
				out.Factory = factoryStatus(f, b)
				if out.Construction.Product != 0 {
					out.Health = out.Construction.Health
					out.MaxHealth = out.Construction.MaxHealth
					out.HealthPercent = out.Construction.HealthPercent
				}
			}
			out.Order = orderFor(f, handle)
			return out
		}
		// A frame may intentionally publish selection handles before a UnitView
		// exists. BuildProgress is still authoritative for a selected builder.
		if b, ok := buildForSelection(f, handle); ok {
			return SelectedStatus{
				Present: true, Unit: handle,
				Construction: constructionStatus(b),
				Factory:      factoryStatus(f, b),
				Order:        orderFor(f, handle),
			}
		}
	}
	return SelectedStatus{}
}

func unitForLocal(f *frame.Frame, handle pool.Handle, player uint8) *frame.UnitView {
	for i := range f.Units {
		if f.Units[i].Slot == handle && f.Units[i].Owner == player {
			return &f.Units[i]
		}
	}
	return nil
}

func buildFor(f *frame.Frame, builder pool.Handle) (frame.BuildProgressView, bool) {
	for _, b := range f.Builds {
		if b.Builder == builder {
			return b, true
		}
	}
	return frame.BuildProgressView{}, false
}

func buildForSelection(f *frame.Frame, selected pool.Handle) (frame.BuildProgressView, bool) {
	if b, ok := buildFor(f, selected); ok {
		return b, true
	}
	// A nanoframe is selected by its product handle while BuildProgress is
	// published by builder handle. Preserve that explicit product link.
	for _, b := range f.Builds {
		if b.Product == selected {
			return b, true
		}
	}
	return frame.BuildProgressView{}, false
}

func constructionStatus(b frame.BuildProgressView) ConstructionStatus {
	remaining := b.Remaining
	if remaining < 0 {
		remaining = 0
	}
	if remaining > 1 {
		remaining = 1
	}
	percent := int((1 - remaining) * 100) // __ftol truncates toward zero [01 §8]
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	return ConstructionStatus{
		Present: true, Builder: b.Builder, Product: b.Product, ProductKey: b.ProductKey,
		Remaining: b.Remaining, Percent: percent, Health: b.Health, MaxHealth: b.MaxHealth,
		HealthPercent: healthPercent(b.Health, b.MaxHealth), Factory: b.Factory,
		QueueIndex: b.QueueIndex, QueueIndexKnown: b.QueueIndex >= 0,
		// False is not treated as an asserted non-stalled state: the producer
		// has no separate publication bit, so only true is an explicit stall.
		Stalled: b.Stalled, StalledKnown: b.Stalled,
	}
}

func healthPercent(current, max int32) int {
	if max <= 0 || current <= 0 {
		return 0
	}
	if current >= max {
		return 100
	}
	percent := int((float32(current) / float32(max)) * 100) // [01 §8]
	if percent < 0 {
		return 0
	}
	if percent > 100 {
		return 100
	}
	return percent
}

func factoryStatus(f *frame.Frame, b frame.BuildProgressView) FactoryStatus {
	out := FactoryStatus{
		Present: b.Factory, Builder: b.Builder, Product: b.Product, ProductKey: b.ProductKey,
		QueueIndex: b.QueueIndex, QueueIndexKnown: b.QueueIndex >= 0,
		Stalled: b.Stalled, StalledKnown: b.Stalled,
	}
	for _, q := range f.OrderQueues {
		if q.Unit != b.Builder {
			continue
		}
		out.QueueCountKnown = true
		out.PrimaryQueueCount = len(q.Primary)
		out.SecondaryQueueCount = len(q.Secondary)
		out.QueueCount = out.PrimaryQueueCount
		for _, o := range q.Primary {
			if o.BuildProduct != "" {
				out.QueueProductKeys = append(out.QueueProductKeys, o.BuildProduct)
			}
		}
		break
	}
	return out
}

func orderFor(f *frame.Frame, handle pool.Handle) OrderStatus {
	for _, q := range f.OrderQueues {
		if q.Unit != handle {
			continue
		}
		if len(q.Primary) != 0 {
			return orderStatus(q.Primary[0])
		}
		if len(q.Secondary) != 0 {
			return orderStatus(q.Secondary[0])
		}
	}
	return OrderStatus{}
}

func orderStatus(o frame.OrderView) OrderStatus {
	name, label := canonicalOrderName(o.Kind)
	return OrderStatus{Present: true, Unit: o.Unit, Target: o.Target, Name: name, Label: label,
		List: o.List, Index: o.Index, State: o.State, MoveState: o.MoveState, BuildProduct: o.BuildProduct}
}

// CanonicalOrderStatus returns the descriptor name as published by the
// snapshot, trimmed but otherwise unchanged. Descriptor names are already
// canonical content keys; unknown names remain unknown rather than being
// mapped to a guessed display label.
func CanonicalOrderStatus(o frame.OrderView) OrderStatus { return orderStatus(o) }

func canonicalOrderName(name string) (string, string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", ""
	}
	// This is the one label established by the order table and is useful to a
	// HUD without making any claim about untraced descriptor labels [04 §3].
	if strings.EqualFold(name, "Move_Ground") {
		return "Move_Ground", "Moving"
	}
	return name, ""
}

// ResourceStatusFor returns stocks/capacities/rates for player from the
// committed economy view.
func ResourceStatusFor(f *frame.Frame, player uint8) ResourceStatus {
	if f == nil {
		return ResourceStatus{}
	}
	var (
		metal, energy, metalCap, energyCap                           float32
		metalProduced, metalConsumed, energyProduced, energyConsumed float32
		found                                                        bool
	)
	for _, e := range f.Economy {
		if e.Player != player {
			continue
		}
		metal, energy = e.Metal, e.Energy
		metalCap, energyCap = e.MetalCapacity, e.EnergyCapacity
		metalProduced, metalConsumed = e.MetalProduced, e.MetalConsumed
		energyProduced, energyConsumed = e.EnergyProduced, e.EnergyConsumed
		found = true
		break
	}
	if !found {
		return ResourceStatus{}
	}
	return ResourceStatus{
		Present: true, Player: player,
		Energy:        StatusBar{Current: energy, Capacity: energyCap, Fraction: ResourceFraction(energy, energyCap)},
		Metal:         StatusBar{Current: metal, Capacity: metalCap, Fraction: ResourceFraction(metal, metalCap)},
		EnergyCurrent: FormatCurrent(energy), EnergyCapacity: FormatCurrent(energyCap),
		MetalCurrent: FormatCurrent(metal), MetalCapacity: FormatCurrent(metalCap),
		EnergyProduced: PaletteRole{Text: FormatEnergyRate(energyProduced), Palette: PaletteProduction},
		EnergyConsumed: PaletteRole{Text: FormatEnergyRate(-abs32(energyConsumed)), Palette: PaletteConsumption},
		MetalProduced:  PaletteRole{Text: FormatMetalRate(metalProduced), Palette: PaletteProduction},
		MetalConsumed:  PaletteRole{Text: FormatMetalRate(-abs32(metalConsumed)), Palette: PaletteConsumption},
		EnergyZero:     PaletteRole{Text: "0", Palette: PaletteNormal},
		MetalZero:      PaletteRole{Text: "0", Palette: PaletteNormal},
	}
}

// FormatCurrent applies retail's integer conversion to a stock/capacity value.
// Go's int(float32) truncates toward zero, matching __ftol [01 §8].
func FormatCurrent(value float32) string { return fmt.Sprintf("%d", int(value)) }

// FormatStock is an alias for the integer stock/capacity formatter.
func FormatStock(value float32) string { return FormatCurrent(value) }

// FormatEnergyRate formats energy production/consumption as an integer, with
// a truncated integer K suffix outside the inclusive -99999..99999 range
// [07 §6]. The sign supplied by the caller is preserved.
func FormatEnergyRate(value float32) string {
	if value > 99999 || value < -99999 {
		return fmt.Sprintf("%dK", int(value/1000))
	}
	return fmt.Sprintf("%d", int(value))
}

// FormatMetalRate formats metal production/consumption with one fractional
// digit [07 §6].
func FormatMetalRate(value float32) string {
	return fmt.Sprintf("%.1f", float64(value))
}

func abs32(v float32) float32 {
	if math.IsNaN(float64(v)) || v == 0 {
		return 0
	}
	if v < 0 {
		return -v
	}
	return v
}
