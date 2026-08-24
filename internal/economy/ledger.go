package economy

import (
	"fmt"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/internal/world"
)

// Res identifies the resource kind per [05 "Player slot"].
type Res int

const (
	Metal Res = iota
	Energy
)

// Bucket holds the four accumulators per resource per unit, mirrored at player level
// per [05 "Unit instance economy state"].
type Bucket struct {
	Production float32
	Requested  float32
	Accepted   float32
	Carry      float32
}

// Player is the per-player ledger state per [05 "Player slot"].
// Stocks and capacities are single precision per [05 "Player slot"] and I2.
// Cumulative totals and waste are double precision per [05 "Stocks, counters, and waste"] and I2.
type Player struct {
	Stock          [2]float32
	Capacity       [2]float32
	Mirror         [2]Bucket
	UpdateTime     uint32 // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	WinLoseTime    uint32 // sibling deadline #1 [05 "Saving economy, construction, and features"] TODO(question): consumer beyond save key unknown
	DisplayTimer   uint32 // sibling deadline #2 [05 "Saving economy, construction, and features"] TODO(question): consumer beyond save key unknown
	Waste          [2]float64
	TotalProduced  [2]float64
	TotalConsumed  [2]float64
	PassProduced   [2]float32
	PassConsumed   [2]float32
	ArchivedMirror [2]Bucket
	// Control fields for deadline block and gate chain per [05 "Authoritative settlement order"].
	// Retail offsets are identity per I13, not layout.
	Exists          bool  // whether slot exists and participates [05 "Player slot"]
	ControllerState uint8 // controller/state byte; three values allow traversal, two allow settlement [05 "Authoritative settlement order"] TODO(question): semantic names unknown
	IsObserver      bool  // observer byte excludes observers [05 "Authoritative settlement order"]
	// Status-pair identity per the decompile (notes/economy/07_settlement_cadence_deadline.md
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	// TODO(question): Historical analysis omitted; independently worded behavior is needed.
	StatusHalfwordAt144 int16 // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	StatusWordAt140     int32 // TODO(question): Historical analysis omitted; independently worded behavior is needed.
	GameEnded           bool  // game-ended flag bit clear required [05 "Authoritative settlement order"]
	// EndGameCountdown must be negative for settlement; initialized -1
	// [05 "Authoritative settlement order"].
	// TODO(question): the two arm/decrement sites that latch GameEnded and
	// drive this countdown were not dispatched to a phase yet — the victory/
	// defeat transition (phase 14 session) owns them. Until then the gate is
	// permanently satisfied, which matches an in-progress game.
	EndGameCountdown     int32
	Helper1Deadline      uint32   // private 30-tick counter for auxiliary player-level object update [05 "Authoritative settlement order"]
	Helper2Deadline      uint32   // second helper private 30-tick gate [05 "Authoritative settlement order"]
	Allies               [10]bool // alliance relations [05 "Player slot"]
	AutoShareMetal       bool     // automatic sharing option [05 "Allied resource and sensor sharing"]
	AutoShareEnergy      bool
	AutoShareSensor      bool
	MetalShareThreshold  float32 // separate sharing threshold [05 "Allied resource and sensor sharing"]
	EnergyShareThreshold float32
	Helper1Calls         int // diagnostic: helper1 invocations, never touches stock [05]
	Helper2Calls         int // diagnostic: helper2 invocations, never touches stock [05]
	WeaponRefreshCalls   int // diagnostic: weapon/position refresh sweep, never touches stock [05]
}

// Service is the economy service skeleton per plan public API.
// Players are visited 0..9 ascending per [05 "Authoritative settlement order"] and I1.
type Service struct {
	Players          [10]Player
	unitBuckets      []UnitEconomy
	OnSettle         func(p int, tick uint32) // notification-only diagnostic seam fired at settlement entry (WU-08-2/08-3 merge); production wiring leaves it nil [05 "Authoritative settlement order"] C7
	ReferencePlayer  int                      // reference/local player for ShareTick dispatcher [05 "Allied resource and sensor sharing"] C12
	SensorShareCalls int                      // diagnostic: sensor sharing invocations at tick%450==0 [05]
	EconomySelector  *int                     // global selector at 0x37EEE for negative energyUse refund discount [P1-06] 0=>-0.5 1=>-0.7

	// CloakCost reports a unit's per-pass cloak upkeep, or zero when the unit
	// is not cloaked [05 "Cloak debit"] C13. It is a seam rather than a field
	// read because the cloak state lives on the unit's runtime status and the
	// authored cost on its definition, and economy owns neither. A nil hook
	// skips the debit entirely, which is what a session with no cloaking units
	// would observe anyway.
	CloakCost func(*units.Unit) float32
}

// UnitEconomy holds per-unit live and archived buckets per [05 "Unit instance economy state"].
// Live buckets are the four accumulators; archived values are from the most recent settlement pass.
type UnitEconomy struct {
	Buckets  [2]Bucket
	Archived [2]Bucket
}

// ensureUnitBuckets grows the per-unit bucket slice to cover handle.
// Allocation is lowest-free ascending with slot 0 null per [01 §6.1].
func (s *Service) ensureUnitBuckets(handle pool.Handle) {
	if s == nil {
		return
	}
	need := int(handle) + 1
	if len(s.unitBuckets) >= need {
		return
	}
	nb := make([]UnitEconomy, need)
	copy(nb, s.unitBuckets)
	s.unitBuckets = nb
}

// UnitBuckets returns the live bucket pair for handle, growing the slice if needed.
// Caller must hold handle validity; slot 0 returns nil.
func (s *Service) UnitBuckets(handle pool.Handle) *[2]Bucket {
	if s == nil || handle == 0 {
		return nil
	}
	s.ensureUnitBuckets(handle)
	return &s.unitBuckets[handle].Buckets
}

// ForEachUnitOrdered visits units owned by player in stable slot order
// per [05 "Authoritative settlement order"] C6 and I1. Earlier units consume
// live stock before later units are tested, so slot order matters.
func ForEachUnitOrdered(w *units.World, player int, fn func(*units.Unit)) {
	if w == nil || fn == nil {
		return
	}
	if player < 0 || player >= 10 {
		return
	}
	// w.Iter returns units in pool slot ascending order per [01 §6.1] [I1].
	for _, u := range w.Iter() {
		if u == nil || !u.Alive {
			continue
		}
		if int(u.Owner) != player {
			continue
		}
		fn(u)
	}
}

// AddProduction verbatim accumulates authored per-pass production without time conversion
// per [05 "Authoritative settlement order"] C1. There is no division or multiplication
// by the tick rate in the ledger; the only floating constants in the whole ledger are
// -0.7 and -0.5 per [05 "Authoritative settlement order"] C1.
func AddProduction(b *Bucket, amount float32) {
	if b == nil {
		return
	}
	b.Production += amount
}

// AddRequested verbatim accumulates requested consumption per [05 "Unit instance economy state"].
func AddRequested(b *Bucket, amount float32) {
	if b == nil {
		return
	}
	b.Requested += amount
}

// RebuildCapacity rebuilds player storage capacities from scratch each pass
// by summing eligible completed units' authored storage per [05 "Storage capacity"] C14.
// Eligible means alive and remaining construction fraction zero per [05 "Completed-unit eligibility"].
func RebuildCapacity(s *Service, w *units.World) {
	if s == nil || w == nil {
		return
	}
	for i := range s.Players {
		s.Players[i].Capacity[Metal] = 0
		s.Players[i].Capacity[Energy] = 0
	}
	// Stable slot-order visitation per C6 [I1]; capacity sum is order independent but determinism requires stable iteration.
	for _, u := range w.Iter() {
		if u == nil || !u.Alive || u.Def == nil {
			continue
		}
		if u.Remaining != 0 {
			continue
		}
		owner := int(u.Owner)
		if owner < 0 || owner >= len(s.Players) {
			continue
		}
		// Storage fields are authored float64; narrow to float32 at the documented boundary per I2.
		s.Players[owner].Capacity[Energy] += float32(u.Def.EnergyStorage)
		s.Players[owner].Capacity[Metal] += float32(u.Def.MetalStorage)
	}
	// TODO(question): mission-provided storage bonuses per [05 "Storage capacity"] when enable flag is set.
}

// SampleExtractorYield wraps world.Terrain.SampleMetal for placement-time metal extraction
// per [05 "Terrain metal extraction"] C14. The value is Σ(cellMetal+1) × extractsMetal
// sampled at placement and stored on the unit; it is not resampled each pass.
// Adapt the signature without modifying internal/world.
func SampleExtractorYield(t *world.Terrain, cx, cz int32, footX, footZ int, extractsMetal float64) (float32, error) {
	if t == nil {
		return 0, fmt.Errorf("economy: nil terrain")
	}
	// SampleMetal signature is (cx, cz int32, footX, footZ int, extractsMetal float32) (float32, error) per [PLAN_04].
	return t.SampleMetal(cx, cz, footX, footZ, float32(extractsMetal))
}

// SampleExtractorYieldForDef is a convenience that samples using the unit definition's
// extractsMetal field per [02 "Unit record"] and the given footprint.
func SampleExtractorYieldForDef(t *world.Terrain, cx, cz int32, def *content.UnitDef) (float32, error) {
	if def == nil {
		return 0, fmt.Errorf("economy: nil def")
	}
	footX := int(def.FootprintX)
	footZ := int(def.FootprintZ)
	return SampleExtractorYield(t, cx, cz, footX, footZ, def.ExtractsMetal)
}

// DebitCloak applies a single cloak upkeep debit with truncation toward zero
// per [05 "Cloak debit"] C13. Cost is converted to integer via truncation (I3), compared
// with owner's live energy stock, and if affordable subtracted immediately with an energy
// request recorded. Units are visited in slot order by the caller.
func DebitCloak(p *Player, cost float32) bool {
	if p == nil {
		return false
	}
	trunc := int32(cost) // truncation toward zero per [01 §8] and I3
	if trunc <= 0 {
		return false
	}
	need := float32(trunc)
	if p.Stock[Energy] < need {
		return false
	}
	p.Stock[Energy] -= need
	p.Mirror[Energy].Requested += need
	return true
}

// ApplyCloakDebits sequentially debits cloak costs for all units of player in slot order
// per [05 "Cloak debit"] C13. Earlier slots consume live stock before later slots are tested.
//
// The outcome drives transitions through the shared transition helper
// [05 "Cloak debit"][05 "Activation and stall transitions"]: onSuccess /
// onFailure receive the unit after each debit decision so the caller can
// toggle the operational/building bit exactly where retail's transition call
// sits. economy cannot import cob, so the helper arrives as a seam.
func ApplyCloakDebits(s *Service, w *units.World, player int, getCost func(*units.Unit) float32, onSuccess, onFailure func(*units.Unit)) {
	if s == nil || w == nil || getCost == nil {
		return
	}
	if player < 0 || player >= len(s.Players) {
		return
	}
	p := &s.Players[player]
	ForEachUnitOrdered(w, player, func(u *units.Unit) {
		cost := getCost(u)
		if DebitCloak(p, cost) {
			if onSuccess != nil {
				onSuccess(u)
			}
		} else {
			if onFailure != nil {
				onFailure(u)
			}
		}
	})
}

// CommitPostSettlement implements the post-settlement commit order per [05 "Stocks, counters, and waste"] C10.
// Order: clamp stock to rebuilt capacity; overflow to cumulative waste with fractional preserved;
// stock stays single precision; archive and zero live buckets; per-pass counters and cumulative
// double totals are committed BEFORE opening stock folds into the pool so they report the pass
// not available funds. Capacity is assumed already rebuilt via RebuildCapacity.
func (p *Player) CommitPostSettlement() {
	if p == nil {
		return
	}
	// Per-pass counters and cumulative totals committed before stock fold per C10.
	for r := Metal; r <= Energy; r++ {
		p.PassProduced[r] = p.Mirror[r].Production
		p.PassConsumed[r] = p.Mirror[r].Requested
		p.TotalProduced[r] += float64(p.Mirror[r].Production)
		p.TotalConsumed[r] += float64(p.Mirror[r].Requested)
	}
	// Clamp stock to rebuilt capacity; overflow to waste with fractional preserved per C10.
	// Waste is float64 per I2; stock stays float32 per [05 "Player slot"].
	for r := Metal; r <= Energy; r++ {
		if p.Stock[r] > p.Capacity[r] {
			overflow := float64(p.Stock[r] - p.Capacity[r])
			p.Waste[r] += overflow
			p.Stock[r] = p.Capacity[r]
		}
		if p.Stock[r] < 0 {
			p.Stock[r] = 0
		}
	}
	// Archive and clear live bucket INPUTS per C10 and step 8 of
	// [05 "Authoritative settlement order"].
	p.ArchivedMirror = p.Mirror
	for r := Metal; r <= Energy; r++ {
		clearPassInputs(&p.Mirror[r])
	}
}

// clearPassInputs zeroes the three pass-local accumulators and preserves the
// carry [05 "Authoritative settlement order"] steps 1, 6 and 8.
//
// Carry is not a pass input. Step 1 clears "pass-local production, request,
// acceptance"; step 6 writes the remaining carry back to each unit and to the
// player bucket; step 8 clears the "live bucket inputs". Zeroing the whole
// four-value subrecord destroys the debt the next pass is supposed to service,
// which makes oldCarry permanently zero and the first stage of the two-stage
// algorithm dead [05 "Two-stage settlement algorithm"].
func clearPassInputs(b *Bucket) {
	b.Production = 0
	b.Requested = 0
	b.Accepted = 0
	// b.Carry survives.
}

// CommitUnitBuckets archives and zeroes per-unit live buckets for player per C10.
func (s *Service) CommitUnitBuckets(w *units.World, player int) {
	if s == nil || w == nil {
		return
	}
	ForEachUnitOrdered(w, player, func(u *units.Unit) {
		h := u.Handle
		if h == 0 {
			return
		}
		s.ensureUnitBuckets(h)
		ue := &s.unitBuckets[h]
		ue.Archived = ue.Buckets
		clearPassInputs(&ue.Buckets[Metal])
		clearPassInputs(&ue.Buckets[Energy])
	})
}

// Mirror bucket closed writer set per [05 "Stocks, counters, and waste"] C11.
// There is NO factory queue-draw writer — the player mirror bucket has no such writer
// per [05 "Stocks, counters, and waste"].

// InitPlayer initializes the player mirror bucket and ledger state per C11.
func InitPlayer(p *Player) {
	if p == nil {
		return
	}
	for r := Metal; r <= Energy; r++ {
		p.Mirror[r] = Bucket{}
		p.ArchivedMirror[r] = Bucket{}
		p.PassProduced[r] = 0
		p.PassConsumed[r] = 0
	}
	// Stock, Capacity, Waste, totals are zeroed by caller as needed; no float constants here.
	// Initialize control countdown to -1 so gate starts satisfied per [05 "Authoritative settlement order"].
	p.EndGameCountdown = -1
	p.GameEnded = false
}

// ClearMirrorPerPass clears the live mirror bucket's pass inputs for reuse per
// C11 and step 1 of [05 "Authoritative settlement order"]. Carry survives — see
// clearPassInputs.
func ClearMirrorPerPass(p *Player) {
	if p == nil {
		return
	}
	for r := Metal; r <= Energy; r++ {
		clearPassInputs(&p.Mirror[r])
	}
}

// SaveMirrorOverlay and LoadMirrorOverlay are stubs for save/load overlay per C11.
// The player-level mirror bucket is not persisted by the player save path and is
// reinitialized per [05 "Saving economy, construction, and features"].
func SaveMirrorOverlay(p *Player) [2]Bucket {
	if p == nil {
		return [2]Bucket{}
	}
	return p.Mirror
}

// LoadMirrorOverlay restores the mirror bucket from save data per C11.
func LoadMirrorOverlay(p *Player, m [2]Bucket) {
	if p == nil {
		return
	}
	p.Mirror = m
}

// AdmitTwoResource admits energy and metal demands as one transaction per [05 "Two-resource admission"].
// It always records both requested amounts; it records both as accepted only if both carries are non-positive.
func AdmitTwoResource(buckets *[2]Bucket, energy, metal float32) {
	if buckets == nil {
		return
	}
	buckets[Energy].Requested += energy
	buckets[Metal].Requested += metal
	if buckets[Energy].Carry <= 0 && buckets[Metal].Carry <= 0 {
		buckets[Energy].Accepted += energy
		buckets[Metal].Accepted += metal
	}
}

// AdmitOneResource admits an energy-only demand per [05 "One-resource admission"].
// It always adds to energy requested and adds to accepted only when energy carry is non-positive.
func AdmitOneResource(buckets *[2]Bucket, energy float32) {
	if buckets == nil {
		return
	}
	buckets[Energy].Requested += energy
	if buckets[Energy].Carry <= 0 {
		buckets[Energy].Accepted += energy
	}
}

// AdmitTwoResourceToMirror is the mirror-bucket two-resource admission helper per C11.
// Body arrives with WU-08-3 for settlement ratios; this records admission per C11.
func AdmitTwoResourceToMirror(p *Player, energy, metal float32) {
	if p == nil {
		return
	}
	AdmitTwoResource(&p.Mirror, energy, metal)
}

// AdmitOneResourceToMirror is the mirror-bucket one-resource admission helper per C11.
func AdmitOneResourceToMirror(p *Player, energy float32) {
	if p == nil {
		return
	}
	AdmitOneResource(&p.Mirror, energy)
}

// ImmediateDebit attempts a direct two-resource payment that debits both in full or neither
// per [05 "Direct two-resource payment"] C11.
func ImmediateDebit(p *Player, energy, metal float32) bool {
	if p == nil {
		return false
	}
	if p.Stock[Energy] < energy || p.Stock[Metal] < metal {
		return false
	}
	p.Stock[Energy] -= energy
	p.Stock[Metal] -= metal
	return true
}

// ShareTransfer mutates live stock between passes for allied sharing per [05 "Allied resource and sensor sharing"] C11.
// Transfers run outside settlement when global tick is multiple of 60/450 per C12; this helper just moves amount.
func ShareTransfer(src, dst *Player, res Res, amount float32) {
	if src == nil || dst == nil {
		return
	}
	if amount <= 0 {
		return
	}
	// No clamp to share buffer here; outer dispatcher gates it per [05 "Allied resource and sensor sharing"].
	src.Stock[res] -= amount
	dst.Stock[res] += amount
}

// CreditSpawn writes spawn credit directly to live stock outside the ledger
// per [05 "Authoritative settlement order"] C11.
func CreditSpawn(p *Player, res Res, amount float32) {
	if p == nil {
		return
	}
	p.Stock[res] += amount
}

// CreditConstructionTermination credits metal spent on an unfinished build per C11.
// Refund is trunc((1 - remaining) × metalBuildCost) per [05 "Cancel-current and stop interrupts"] C21.
// Normally adds to builder's metal bucket; special player modes subtract scaled amount.
// The only floating constants in the whole ledger are -0.7 and -0.5 per C1.
func CreditConstructionTermination(p *Player, remaining float32, metalBuildCost int32, specialMode int) {
	if p == nil {
		return
	}
	// Truncation toward zero per I3.
	refund := float32(int32((1 - remaining) * float32(metalBuildCost)))
	switch specialMode {
	case 0:
		// selector value 0 subtracts seven tenths per C21 and [05 "Cancel-current and stop interrupts"]
		p.Mirror[Metal].Production += refund * -0.7
	case 1:
		// selector value 1 subtracts one half
		p.Mirror[Metal].Production += refund * -0.5
	default:
		p.Mirror[Metal].Production += refund
	}
}
