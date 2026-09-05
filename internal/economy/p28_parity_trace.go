package economy

import (
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/units"
)

// UnitTrace is a copy of the economy state relevant to settlement and HUD
// diagnosis. It is deliberately package-local; the parity adapter can map it
// to an evidence format without making economy depend on that package.
type UnitTrace struct {
	Slot          pool.Handle
	DefinitionKey string
	Owner         uint8
	SpotMetal     float32
	Remaining     float32
	Activated     bool
	Buckets       [2]Bucket
	Archived      [2]ArchivedBucket
}

// PlayerTrace copies both live and archived player buckets, stocks, counters,
// and settlement gates. Float payloads are retained exactly by the caller's
// hash or encoder [05 "Player slot"] [05 "Authoritative settlement order"].
type PlayerTrace struct {
	Slot            int
	Stock           [2]float32
	Capacity        [2]float32
	Mirror          [2]Bucket
	ArchivedMirror  [2]ArchivedBucket
	AIProduction    [2]float32
	AIConsumption   [2]float32
	PassProduced    [2]float32
	PassConsumed    [2]float32
	Waste           [2]float64
	TotalProduced   [2]float64
	TotalConsumed   [2]float64
	UpdateTime      uint32
	WinLoseTime     uint32
	DisplayTimer    uint32
	Exists          bool
	ControllerState uint8
	IsObserver      bool
	// The settlement gate's two inputs are the player record's live unit count
	// and units-ever-created count, not a separate status pair
	// [05 R-ECO-01 §12]. They are traced under their own names, read from the
	// world the snapshot is given; a nil world traces the zero pair.
	LiveUnitCount        int16
	UnitsEverCreated     uint32
	GameEnded            bool
	EndGameCountdown     int32
	Helper1Deadline      uint32
	Helper2Deadline      uint32
	Allies               [10]bool
	AutoShareMetal       bool
	AutoShareEnergy      bool
	AutoShareSensor      bool
	MetalShareThreshold  float32
	EnergyShareThreshold float32
	StorageBonusEnabled  bool
	StorageBonus         [2]float32
}

// TraceSnapshot is a copy of every unit and player economy record, taken in
// slot order for parity capture.
type TraceSnapshot struct {
	Units   []UnitTrace
	Players [10]PlayerTrace
}

// ParitySnapshot copies economy state in player/slot order. It is pure: in
// particular, it does not call UnitBuckets (which grows storage) and does not
// settle or otherwise advance the ledger [05 "Authoritative settlement order"].
func (s *Service) ParitySnapshot(w *units.World) TraceSnapshot {
	var out TraceSnapshot
	if s == nil {
		return out
	}
	for i := range s.Players {
		p := s.Players[i]
		var live int16
		var created uint32
		if w != nil {
			live = int16(w.LiveCountForPlayer(i))
			created = w.CreatedCountForPlayer(i)
		}
		out.Players[i] = PlayerTrace{
			Slot: i, Stock: p.Stock, Capacity: p.Capacity, Mirror: p.Mirror,
			ArchivedMirror: p.ArchivedMirror, AIProduction: p.AIProduction,
			AIConsumption: p.AIConsumption, PassProduced: p.PassProduced,
			PassConsumed: p.PassConsumed, Waste: p.Waste,
			TotalProduced: p.TotalProduced, TotalConsumed: p.TotalConsumed,
			UpdateTime: p.UpdateTime, WinLoseTime: p.WinLoseTime,
			DisplayTimer: p.DisplayTimer, Exists: p.Exists,
			ControllerState: p.ControllerState, IsObserver: p.IsObserver,
			LiveUnitCount: live, UnitsEverCreated: created,
			GameEnded: p.GameEnded, EndGameCountdown: p.EndGameCountdown,
			Helper1Deadline: p.Helper1Deadline, Helper2Deadline: p.Helper2Deadline,
			Allies: p.Allies, AutoShareMetal: p.AutoShareMetal,
			AutoShareEnergy: p.AutoShareEnergy, AutoShareSensor: p.AutoShareSensor,
			MetalShareThreshold: p.MetalShareThreshold, EnergyShareThreshold: p.EnergyShareThreshold,
			StorageBonusEnabled: p.StorageBonusEnabled, StorageBonus: p.StorageBonus,
		}
	}
	if w == nil {
		return out
	}
	for _, u := range w.IterSliced() {
		if u == nil || !u.Alive {
			continue
		}
		ut := UnitTrace{Slot: u.Handle, Owner: u.Owner, SpotMetal: u.SpotMetal, Remaining: u.Remaining, Activated: u.Activated}
		if u.Def != nil {
			ut.DefinitionKey = u.Def.CanonicalKey
			if ut.DefinitionKey == "" {
				ut.DefinitionKey = u.Def.UnitName
			}
		}
		if int(u.Handle) < len(s.unitBuckets) {
			ut.Buckets = s.unitBuckets[u.Handle].Buckets
			ut.Archived = s.unitBuckets[u.Handle].Archived
		}
		out.Units = append(out.Units, ut)
	}
	return out
}
