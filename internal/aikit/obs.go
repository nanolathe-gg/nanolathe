package aikit

import (
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// OrderClass is the coarse meaning of a unit's current order.
type OrderClass uint8

const (
	OrderIdle OrderClass = iota
	OrderMove
	OrderAttack
	OrderPatrol
	OrderGuard
	OrderBuild   // building, factory production or assisting a build
	OrderRepair  // repairing
	OrderReclaim // reclaim, resurrect, capture
	OrderTransport
	OrderGetBuilt // still a nanoframe
	OrderOther
)

// orderClassByID classifies the order table once, at package initialization
// (the orders package builds its table in its own init, which runs first),
// so hosts on different goroutines only ever read it.
var orderClassByID = func() []OrderClass {
	table := orders.Table()
	out := make([]OrderClass, len(table))
	for i, d := range table {
		out[i] = classifyOrderName(d.Name)
	}
	return out
}()

func orderClassTable() []OrderClass { return orderClassByID }

func classifyOrderName(name string) OrderClass {
	switch name {
	case "", "Stop", "Standby", "VTOL_Standby", "Standby_Mine", "Park", "Wait", "WaitForAttack",
		"VTOL_LandIfCan", "Standing_MoveOrder", "Standing_FireOrder", "Activate", "Deactivate",
		"Cloak_On", "Cloak_Off", "SelfRepair", "MakeSelectable", "VTOL_GetRepaired":
		return OrderIdle
	case "Move_Ground", "VTOL_Move", "QMove", "Teleport":
		return OrderMove
	case "Patrol", "QPatrol", "VTOL_Patrol", "RepairPatrol", "VTOL_RepairPatrol":
		return OrderPatrol
	case "Follow_Ground", "VTOL_Follow", "Guard_NoMove", "VTOL_SeekGuard":
		return OrderGuard
	case "MobileBuild", "VTOL_MobileBuild", "BuildingBuild", "HelpBuild", "VTOL_HelpBuild", "BuildWeapon":
		return OrderBuild
	case "RepairUnit", "VTOL_RepairUnit", "RepairUnitNoMove":
		return OrderRepair
	case "Reclaim", "ReclaimUnit", "VTOL_Reclaim", "VTOL_ReclaimUnit", "Resurrect", "Capture":
		return OrderReclaim
	case "Ground_Pickup", "Ground_Unload", "VTOL_Pickup", "VTOL_Unload", "VTOL_Landing", "BeCarried":
		return OrderTransport
	case "GetBuilt":
		return OrderGetBuilt
	}
	if strings.HasPrefix(name, "Attack") || strings.HasPrefix(name, "AirTo") || name == "AirStrike" ||
		name == "Suppress" || name == "VTOL_SeekAttack" {
		return OrderAttack
	}
	return OrderOther
}

// Res is one resource's state, whole units; Income and Expense per second.
type Res struct {
	Stock, Cap, Income, Expense int32
}

// OwnUnit is one of the observer's own units.
type OwnUnit struct {
	H pool.Handle
	// Gen changes whenever the slot behind H holds a different unit than
	// at an earlier observation (the pool recycles slots without a
	// generation), so per-handle brain state keyed by H must be reset when
	// (H, Gen) changes.
	Gen       uint32
	Info      *UnitInfo
	X, Y, Z   int32
	HP, MaxHP int32
	Built     bool
	Progress  int32 // construction progress percent
	Order     OrderClass
	QueueLen  int32 // primary orders (factories: queued products)
	// Target is the product of the first build order in the queue (a
	// builder's current construction, a factory's current item), nil when
	// the unit is not building.
	Target *UnitInfo
	Tag    int32 // brain-owned label (squad id), preserved across observations
}

// Contact is an enemy the observer currently detects: in sight, or a radar
// or sonar blip (sensors.go).
type Contact struct {
	// H and Gen identify a contact in sight (Gen as OwnUnit.Gen). A blip
	// carries neither: which unit it is is not known, so it cannot be
	// followed across observations or matched to an earlier sighting.
	H       pool.Handle
	Gen     uint32
	Info    *UnitInfo // nil for a blip: its type is not known
	Owner   uint8
	X, Z    int32
	HPPct   int32 // 0..100 when seen, 100 for a blip
	Visible bool
	Built   bool
}

// AllyUnit is a unit of an allied player in the owner's sight: its own line
// of sight in a skirmish, the team's shared sight in Survival
// (docs/DESIGN_SURVIVAL.md §4.3). It carries its type and state as an own
// unit does, but no order or tag: the owner cannot command it. The ordinary
// commands that accept a friendly target take one — a repair or an assist
// (code 8), a guard (code 7).
type AllyUnit struct {
	H         pool.Handle
	Gen       uint32 // as OwnUnit.Gen: the instance this record describes
	Info      *UnitInfo
	Owner     uint8
	X, Z      int32
	HP, MaxHP int32
	Built     bool
	Progress  int32 // construction progress percent
}

// Remembered is a sighting kept after the contact left view. Positions are
// the last observed ones; a record is dropped only when the owner looks at
// the spot again and finds nothing (or sees the unit elsewhere).
type Remembered struct {
	H        pool.Handle
	Gen      uint32 // as OwnUnit.Gen: the instance this record describes
	Info     *UnitInfo
	Owner    uint8
	X, Z     int32
	LastSeen uint32
	Building bool
}

// Feature is a map feature near the observer's start — a tree, rock or
// wreck — as the executor last saw it (features.go). Only features that
// block movement or hold metal are listed.
type Feature struct {
	X, Z          int32 // footprint centre, world units
	FootX, FootZ  int32 // cells
	Metal, Energy int32 // what reclaiming it yields
	Blocking      bool
	Reclaimable   bool
}

// Obs is one fair observation. The host rebuilds it into the same buffers
// every think, so a brain must copy anything it wants to keep.
type Obs struct {
	Tick   uint32
	Me     uint8
	Metal  Res
	Energy Res
	Own    []OwnUnit
	Enemy  []Contact
	Memory []Remembered // includes currently visible contacts' last positions
	// Allies are the allied players' units in the owner's sight (AllyUnit),
	// in the unit walk's order.
	Allies []AllyUnit
	// Allied marks the other players this owner is allied with (true at
	// index).
	Allied [10]bool
	// UnitCount is the owner's live unit count; UnitLimit the session limit.
	UnitCount, UnitLimit int32
	// WindPermille is the current wind strength a wind generator multiplies
	// by, in thousandths (public: the interface shows it).
	WindPermille int32
	// Features near the start (within FeatureRadius), refreshed by the
	// command executor every FeatureEvery ticks when a batch is applied;
	// FeaturesTick is when. Map features there are in the owner's view.
	Features     []Feature
	FeaturesTick uint32
}

// FeatureRadius and FeatureEvery bound Obs.Features.
const (
	FeatureRadius = 1600
	FeatureEvery  = 300
	maxFeatures   = 512
)

// OwnByHandle finds an own unit by handle (linear; brains index their own).
func (o *Obs) OwnByHandle(h pool.Handle) *OwnUnit {
	for i := range o.Own {
		if o.Own[i].H == h {
			return &o.Own[i]
		}
	}
	return nil
}

// observer builds observations for one owner.
type observer struct {
	walk     []*units.Unit
	memIndex []int32 // handle → index in Memory, -1 when absent
	sensors  []sensorSource
	jammers  []jammerSource
	tags     []int32 // handle → brain tag
	// inst is the unit each slot held at the last observation and gen the
	// slot's instance count: a unit is a fresh object per creation, so a
	// different pointer is a different unit.
	inst   []*units.Unit
	gen    []uint32
	tagGen []uint32 // the instance each slot's tag belongs to
}

func (b *observer) ensure(n int) {
	for len(b.memIndex) < n {
		b.memIndex = append(b.memIndex, -1)
	}
	for len(b.tags) < n {
		b.tags = append(b.tags, 0)
	}
	for len(b.inst) < n {
		b.inst = append(b.inst, nil)
		b.gen = append(b.gen, 0)
		b.tagGen = append(b.tagGen, 0)
	}
}

// identify records the unit now in its slot and returns the slot's
// instance count.
func (b *observer) identify(u *units.Unit) uint32 {
	h := u.Handle
	if b.inst[h] != u {
		b.inst[h] = u
		b.gen[h]++
	}
	return b.gen[h]
}

func queueState(u *units.Unit, classes []OrderClass) (OrderClass, int32, string) {
	q := orders.QueueOfUnit(u)
	if q == nil {
		return OrderIdle, 0, ""
	}
	target := ""
	class := OrderIdle
	var n int32
	for i, count := 0, q.PrimaryLen(); i < count; i++ {
		node := q.PrimaryAt(i)
		if node == nil {
			continue
		}
		c := OrderIdle
		if int(node.ID) < len(classes) {
			c = classes[node.ID]
		}
		if c == OrderIdle {
			continue
		}
		// A factory queue coalesces repeated products into one record whose
		// count is the queued number.
		if c == OrderBuild && node.BuildDefKey != "" && node.Param2 > 1 {
			n += int32(node.Param2)
		} else {
			n++
		}
		if target == "" && c == OrderBuild && node.BuildDefKey != "" {
			target = node.BuildDefKey
		}
		if class == OrderIdle {
			class = c
		}
	}
	return class, n, target
}

// buildProgress reports whether u is finished and its construction
// progress percent (100 once finished).
func buildProgress(u *units.Unit) (bool, int32) {
	if u.Remaining == 0 {
		return true, 100
	}
	return false, int32(100 - float32(u.Remaining*100))
}

func fixedToWorld(v int64) int32 { return int32(v >> 16) }
