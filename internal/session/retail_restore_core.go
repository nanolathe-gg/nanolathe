package session

import (
	"encoding/binary"
	"fmt"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/features"
	"github.com/nanolathe-gg/nanolathe/internal/movement"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/triggers"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// RestoreRetailBattleCore applies the D2 live-state passes to a successful D1
// stage. It never advances the clock or runs a simulation tick. The caller
// remains responsible for atomically swapping the resulting Session into the
// client [08 R-SAVE-02 §11].
//
// The order is deliberately explicit: player/account fields, alliances, base
// bodies, depth-first references, economy, queues, one front-head goal bind, then
// exact matching COB snapshots [08 R-SAVE-02 §6, §7, §8, §9, §11].
func RestoreRetailBattleCore(stage *RetailBattleStage) error {
	if stage == nil || stage.Session == nil || stage.Image == nil {
		return fmt.Errorf("session: retail restore: nil stage")
	}
	s := stage.Session
	image := stage.Image
	if s.Units == nil || s.Econ == nil {
		return fmt.Errorf("session: retail restore: incomplete session shell")
	}

	// Player fields are gated by the successful GameTime decode in D1. Apply
	// them in account order, preserving the typed economy reader's defaults.
	//
	// The per-player income and expense aggregates are deliberately NOT among
	// them, and their reading zero here is retail, not an omission. The
	// `Player%i` account is closed: the writer emits exactly the nineteen
	// scalars of the field table — the two stocks, the six cumulative doubles,
	// the two storage floats, `AddPlayerStorage`, `Kills`, `Losses`,
	// `UpdateTime`, `WinLoseTime`, `DisplayTimer`, `Controller`, `Logo`,
	// `Side` — followed by the 11-byte `Alliances` box and nothing else, and
	// the reader is symmetric (Established, bounded-negative;
	// [08 "Player records"], the WU-19-158 addendum). The four per-pass rate
	// floats the HUD resource bar samples, and the settled production and
	// consumption pair the planner scores with, are not in that list. Retail
	// loses them twice over: the world rebuild runs "for every kind and for
	// loads alike" and its per-player reset zeroes the whole economy and
	// statistics block — "stocks, incomes, expenditures" — BEFORE the
	// restoration dispatcher runs at all [08 R-ENTRY-01 §3 step 24]
	// [08 R-SAVE-02 §11].
	//
	// They come back where they are written in an ordinary session: at the
	// owning player's next settlement pass, which writes all four per-pass
	// counters and the two cumulative totals from the pass's own gather
	// [05 R-ECO-01 §6]. On the load path that is at most thirty ticks away and
	// is often immediate, because the restored `UpdateTime` is an absolute
	// deadline the account does carry, and the battle-entry tail primes the
	// per-player phase "on the restored world at the restored global tick"
	// — "the per-slot timers restored by `Players` decide whether the 30-tick
	// block fires" [08 R-ENTRY-01 §8]. Copying a saved aggregate here would be
	// inventing a save field.
	for i := range image.Players {
		p := image.Players[i]
		if p.Index < 0 || p.Index >= len(s.Econ.Players) {
			return fmt.Errorf("session: retail restore: player %d outside configured slots", p.Index)
		}
		p.ApplyToEconomy(&s.Econ.Players[p.Index])
		s.Econ.Players[p.Index].Exists = true
		// The `Player%i` account's `Side` item is that slot's player-table side
		// ordinal, restored into the low byte with 0 for a missing or mistyped
		// item [08 "Player records"]. It is the restore-side source of the
		// commander identity of [08 R-TRIG-01 §3], which is otherwise lost when
		// a battle is resumed instead of constructed. A byte outside the signed
		// slot names no row in the side-data table, so it stays unknown.
		if p.Index < len(s.campaignPlayerSide) && p.Side <= 0x7f {
			s.campaignPlayerSide[p.Index] = int8(p.Side)
			s.campaignPlayerSideKnown[p.Index] = true
		}
	}
	if image.HumanPlayer >= 0 && image.HumanPlayer < 10 {
		s.LocalOwner = uint8(image.HumanPlayer)
	}
	// Alliance rows are applied by the per-slot loop above, through
	// PlayerSlot.ApplyToEconomy. The row is the last item of each `Player%i`
	// account, so row *i* is slot *i*'s own first alliance row and the
	// ownership question this site used to carry is answered by the account
	// the box sits in [08 "Player records"] [05 R-SHARE-01 §1]. The self
	// column is forced to 1 on the way in, and a slot whose account carried no
	// box keeps the row battle entry built.

	// Every standard body has already been forced-allocated by D1. Restore
	// bodies in image order, never deriving live identity from enumeration.
	for _, rec := range image.Units.Records {
		if rec.Compat {
			continue // 0xB6 is a null compatibility record [08 R-SAVE-02 §6]
		}
		h, ok := stage.StableUnit[rec.StableID]
		if !ok || h == 0 {
			return fmt.Errorf("session: retail restore: stable unit %d was not reserved", rec.StableID)
		}
		if err := units.RetailUnitBase(s.Units.Unit(h), rec.Data); err != nil {
			return fmt.Errorf("session: retail restore: unit %d base: %w", rec.StableID, err)
		}
	}
	// Resolve carrier containment depth-first even though D1 has reserved all
	// bodies. Engagement links are ordinary references, not ownership edges:
	// they may point at this unit or form a cycle [08 R-SAVE-02 §6].
	processing := make(map[uint16]bool, len(image.Units.Records))
	carrierPath := make(map[uint16]bool, len(image.Units.Records))
	visited := make(map[uint16]bool, len(image.Units.Records))
	var restoreRefs func(uint16) error
	restoreRefs = func(id uint16) error {
		if visited[id] {
			return nil
		}
		// The retail reader reserves a stable slot before it follows either
		// link. A reference back to a record currently being read is therefore
		// already live and is skipped, rather than forming an ownership cycle.
		if processing[id] {
			return nil
		}
		processing[id] = true
		rec, ok := retailUnitRecord(image.Units.Records, id)
		if !ok {
			return fmt.Errorf("session: retail restore: unit reference %d missing", id)
		}
		carrier := readUnitRef(rec.Data, 0x89)
		engagement := readUnitRef(rec.Data, 0x8B)
		if carrier != 0 {
			if carrierPath[carrier] {
				return fmt.Errorf("session: retail restore: cyclic carrier reference %d", carrier)
			}
			carrierPath[id] = true
			if err := restoreRefs(carrier); err != nil {
				return err
			}
			carrierPath[id] = false
		}
		h := stage.StableUnit[id]
		u := s.Units.Unit(h)
		engagementHandle := stage.StableUnit[engagement]
		if engagement != 0 && engagementHandle == 0 {
			return fmt.Errorf("session: retail restore: engagement reference %d missing", engagement)
		}
		if err := units.RetailUnitReferences(u, 0, engagementHandle, rec.Data[0x8D]); err != nil {
			return err
		}
		if carrier != 0 {
			// The restore reader applies the same head-inserting attachment
			// operation as a live attach, with the saved mode and piece. The
			// unit-side mode mirror is the source here; the mover-side byte is
			// restored later and remains a separate saved word [08 R-SAVE-02 §6].
			if !movement.AttachCargoMode(s.Units, stage.StableUnit[carrier], h, int(rec.Data[0x8D]), int(u.Move.Mode)) {
				return fmt.Errorf("session: retail restore: attach unit %d to carrier %d", id, carrier)
			}
		}
		// The engagement reference follows the current record's local attach.
		// That order is observable because a referenced child can attach to the
		// same carrier and inserts at its cargo head [08 R-SAVE-02 §6].
		if engagement != 0 {
			if err := restoreRefs(engagement); err != nil {
				return err
			}
		}
		processing[id] = false
		visited[id] = true
		return nil
	}
	for _, rec := range image.Units.Records {
		if !rec.Compat {
			if err := restoreRefs(rec.StableID); err != nil {
				return err
			}
		}
	}

	if s.Clock == nil {
		return fmt.Errorf("session: retail restore: missing clock")
	}
	// Script enumeration is the writer's unit enumeration index, not a stable
	// unit slot. Validate and stage it before the later per-unit pass so the
	// pass can retain retail's account → mover → order → head-goal → script → weapon
	// order [08 R-SAVE-02 §6].
	scripts := make(map[int]save.ScriptRecord, len(image.Units.Scripts))
	for _, script := range image.Units.Scripts {
		if script.Index < 0 || script.Index >= len(image.Units.Records) {
			return fmt.Errorf("session: retail restore: script index %d outside unit records", script.Index)
		}
		if _, exists := scripts[script.Index]; exists {
			return fmt.Errorf("session: retail restore: duplicate script index %d", script.Index)
		}
		scripts[script.Index] = script
	}
	var binding *orders.QueueBinding
	if s.Build != nil {
		binding = s.Build.OrderBinding
	}
	if s.Movement != nil {
		s.Movement.BindWorld(s.Units)
	}
	// Index the two detached per-unit box families and the order records once,
	// rather than rescanning all of them for every unit record. Grouping by
	// the exact key and appending in scan order preserves both encounter
	// order and multiplicity, so the account/mover/order semantics below are
	// unchanged from the linear scan they replace [08 R-SAVE-02 §6, §8].
	otherByName := make(map[string][]int, len(image.Units.Other))
	for i, raw := range image.Units.Other {
		otherByName[raw.Name] = append(otherByName[raw.Name], i)
	}
	ordersByParent := make(map[uint16][]int, len(image.Units.Orders))
	for i, order := range image.Units.Orders {
		ordersByParent[order.ParentStableID] = append(ordersByParent[order.ParentStableID], i)
	}
	for recIndex, rec := range image.Units.Records {
		if rec.Compat {
			continue
		}
		h := stage.StableUnit[rec.StableID]
		owner := s.Units.Unit(h)
		if owner == nil {
			return fmt.Errorf("session: retail restore: unit %d vanished", rec.StableID)
		}
		// Unit economy accounts are detached in UnitImage.Other. This is the
		// first per-unit later pass [08 R-SAVE-02 §6].
		for _, idx := range otherByName[unitBoxName(rec.StableID, "acc")] {
			if err := economy.RetailUnitAccount(s.Econ, h, image.Units.Other[idx].Data); err != nil {
				return err
			}
		}
		// A mover box is read only inside the established HasMover branch;
		// detached boxes for a no-mover unit are ignored [08 R-SAVE-02 §6, §8].
		//
		// A unit still under construction is skipped entirely. In this build a
		// nanoframe holds its cells through the construction service's placement
		// record — re-registered by the session's COB binder when the forced slot
		// is allocated — and never receives a movement collision record until it
		// completes. Calling EnsureUnit here would hand a restored frame mover
		// state that the same frame does not have in a live session.
		if s.Movement != nil && owner.Remaining == 0 {
			s.Movement.EnsureUnit(owner)
			if owner.HasMover {
				moverIdx := otherByName[unitBoxName(rec.StableID, "mob")]
				if len(moverIdx) > 1 {
					return fmt.Errorf("session: retail restore: unit %d has duplicate mover boxes", rec.StableID)
				}
				if len(moverIdx) == 0 {
					return fmt.Errorf("session: retail restore: unit %d has mover flag but no mover box", rec.StableID)
				}
				if err := s.Movement.RestoreMover(h, image.Units.Other[moverIdx[0]].Data); err != nil {
					return fmt.Errorf("session: retail restore: unit %d mover: %w", rec.StableID, err)
				}
			}
		}
		// Rebuild queues after the account and mover state, then bind any saved
		// goal payload on the front head before the script snapshot. This does
		// not run an order handler or advance its queue [08 R-SAVE-02 §11].
		orderIdx := ordersByParent[rec.StableID]
		group := make([]save.OrderRecord, len(orderIdx))
		for i, idx := range orderIdx {
			group[i] = image.Units.Orders[idx]
		}
		if err := orders.RetailRestoreOrdersAtTick(owner, group, stage.StableUnit, binding, s.Clock.GlobalTick); err != nil {
			return fmt.Errorf("session: retail restore: unit %d orders: %w", rec.StableID, err)
		}
		if s.Movement != nil {
			if err := s.Movement.RestoreHeadGoal(owner); err != nil {
				return fmt.Errorf("session: retail restore: unit %d head goal: %w", rec.StableID, err)
			}
		}
		if script, ok := scripts[recIndex]; ok {
			vm := owner.GetScript()
			if vm == nil {
				return fmt.Errorf("session: retail restore: unit %d has no COB VM", rec.StableID)
			}
			if err := cob.RetailScriptRestore(vm, script.Data); err != nil {
				return fmt.Errorf("session: retail restore: unit %d script: %w", rec.StableID, err)
			}
		}
		if err := units.RetailUnitWeaponTargets(s.Units.Unit(h), stage.StableUnit); err != nil {
			return fmt.Errorf("session: retail restore: unit %d weapon targets: %w", rec.StableID, err)
		}
	}
	// Occupancy registration is a derived pass after all per-unit live words;
	// every cell mutation still goes through movement's ordinary stamp path
	// [08 R-SAVE-02 §11].
	//
	// Retail re-stamps every unit here because retail has one occupancy owner.
	// This build has two: a completed unit's committed anchor belongs to the
	// movement collision record, while a unit still under construction holds
	// its cells through the construction service's placement reservation and
	// has no collision record at all. Its saved cell pair is therefore not a
	// mover anchor and must not be pushed through the mover re-stamp. Its
	// footprint is already held: the forced-slot allocation runs the session's
	// COB binder, which registers a building placement for every non-`bmcode`
	// unit, and the restored rectangle matches the saved session's exactly.
	if s.Movement != nil {
		for _, rec := range image.Units.Records {
			if rec.Compat {
				continue
			}
			h := stage.StableUnit[rec.StableID]
			u := s.Units.Unit(h)
			if u.Remaining != 0 {
				continue
			}
			if err := s.Movement.RestoreOccupancy(h, u.CachedOccupancyX, u.CachedOccupancyZ); err != nil {
				return fmt.Errorf("session: retail restore: unit %d occupancy: %w", rec.StableID, err)
			}
		}
	}

	if s.Features != nil {
		s.Features.ResetForRestore()
		if err := restoreRetailFeatures(s.Features, s.Catalog, image.Features); err != nil {
			return err
		}
	}
	// Terrain follows the features it is stamped under, which is retail's own
	// account order — Features, then Metal, then PlayerFeatures, then Mapping
	// [08 R-SAVE-02 §11] [08 "Account inventory"]. Both boxes are exact-size
	// gated by the world restorers themselves: `Metal`/`Plotmap` is one byte
	// per plot cell and `PlayerFeatures`/`Plotmap` is half that, each byte
	// packing two consecutive cells' placer nibbles [08 R-SAVE-02 §12]. A save
	// whose map does not match the terrain this stage resolved therefore fails
	// the load rather than half-applying a grid.
	if s.World != nil {
		if err := s.World.RestoreRetailMetal(image.Metal); err != nil {
			return fmt.Errorf("session: retail restore: %w", err)
		}
		if err := s.World.RestoreRetailPlayerFeatures(image.PlayerFeatures); err != nil {
			return fmt.Errorf("session: retail restore: %w", err)
		}
	}
	// The AI group index is the one base-record word with a side effect beyond
	// a field copy: the reader moves the unit out of whatever group it holds
	// and into the saved one [08 R-SAVE-02 §6]. That is two writes — the unit's
	// own stored group value and the owner's group vector — and the base-record
	// pass only stages the saved index. Apply the stored value here, before the
	// managers rebuild their vectors from it, so a restored unit reports the
	// group it was saved in rather than the ungrouped record.
	for _, rec := range image.Units.Records {
		if rec.Compat {
			continue
		}
		u := s.Units.Unit(stage.StableUnit[rec.StableID])
		if u == nil {
			continue
		}
		u.Group = 0
		if u.RestoredAIGroup >= 1 && u.RestoredAIGroup <= 9 {
			u.Group = uint8(u.RestoredAIGroup)
		}
	}
	for _, mgr := range s.AI {
		if mgr != nil {
			mgr.RestoreGroupsFromUnits(s.Units)
		}
	}
	// The shower sits between the units and the trigger records in retail's
	// account order [08 R-SAVE-02 §11].
	if err := restoreRetailMeteor(s, image.Meteor); err != nil {
		return err
	}
	// Camera target/glide words and option-bit names are not part of the staged
	// session API; the presentation-side restore seam remains owned by D4
	// [08 R-SAVE-02 §12].
	if s.Mission != nil {
		if err := triggers.RestoreSaveAccounts(s.Mission.Victory, s.Mission.Defeat, image.Triggers); err != nil {
			return fmt.Errorf("session: retail restore: triggers: %w", err)
		}
	}
	// Restore rebuilds derived visibility after every authoritative fixup. The
	// service wipe is required because a fresh session may still carry masks
	// from map composition; publication then follows the ordinary observer
	// path without advancing the clock [03 §3.2, §3.3].
	if s.Vis != nil {
		s.Vis.RebuildAll(nil)
		// The `Mapping` box is the explored-memory word grid verbatim — the
		// same grid the share screen's merge walks, one sixteen-bit word per
		// four terrain cells with ten usable player bits
		// [08 R-SAVE-02 §12] [05 R-SHARE-01 §6]. It carries *history*, which
		// no observer can regenerate, so it is installed on top of the fills
		// the rebuild just wrote and underneath the observer publication that
		// follows. Retail restores it before the units exist and lets their
		// allocation-time sight registrations OR current coverage in
		// [08 R-ENTRY-01 §7] step 3; here the units are already restored, so
		// the same two writes happen in the same relative order with the
		// derived pass last. The byte grids — current coverage, which *is*
		// derived — are what publishVisibilityForAll rebuilds
		// [08 R-SAVE-02 §11].
		if err := restoreRetailMapping(s, image.Mapping); err != nil {
			return err
		}
		s.visStamps = make(map[int]visStamp)
		s.visStatus = make(map[int]uint32)
		publishVisibilityForAll(s)
	}
	return nil
}

// restoreRetailMeteor installs the shower's authored parameters and then
// applies the nine saved scalars over them.
//
// The `Meteor` account carries nine integer items and no weapon identity: "the
// weapon is resolved at load from the authored mission configuration"
// [08 "Meteor showers"], and the fix-up list names "the meteor shower's
// authored parameters (reinstalled from the map)" among the derived state a
// load rebuilds [08 R-SAVE-02 §11]. Retail's own order is the same: the world
// rebuild's meteor step resolves the weapon by name and installs the authored
// next-strike value before the restoration dispatcher runs at all
// [08 R-ENTRY-01 §3 step 22]. Without the first half a restored shower has no
// weapon, no radius and no density; without the second it restarts its
// schedule.
//
// A missing or wrong-typed item decodes as `0`, "so an absent `Meteor` account
// silently disables and de-activates the shower rather than failing the load"
// [08 "Account inventory"]. The scalar overlay itself is non-failing; this
// function returns the defensive idempotent authored-initialization failure
// when a caller did not stage the world-rebuild meteor step first.
func restoreRetailMeteor(s *Session, m save.MeteorScalars) error {
	if s == nil {
		return nil
	}
	if err := s.initMeteor(); err != nil {
		return err
	}
	s.Meteor.Enabled = m.Enabled != 0
	s.Meteor.Active = m.Active != 0
	s.Meteor.NextStrike = uint32(m.NextStrikeTime)
	s.Meteor.StrikeEnds = uint32(m.TimeStrikeEnds)
	s.Meteor.NextHit = uint32(m.NextHitTime)
	// The four coordinates are sixteen-bit globals the writer sign-extends and
	// the reader truncates back to sixteen bits [08 "Account inventory"].
	s.Meteor.OriginX = int32(int16(m.OriginX))
	s.Meteor.OriginZ = int32(int16(m.OriginZ))
	s.Meteor.TargetX = int32(int16(m.TargetX))
	s.Meteor.TargetZ = int32(int16(m.TargetZ))
	return nil
}

// restoreRetailMapping installs the saved explored-memory word grid, gated on
// the exact size the terrain this stage resolved implies. The box is
// `(cell width × cell height) >> 1` bytes, one little-endian word per four
// cells [08 R-SAVE-02 §12] [05 R-SHARE-01 §6].
func restoreRetailMapping(s *Session, data []byte) error {
	if s == nil || s.Vis == nil || s.World == nil {
		return fmt.Errorf("session: retail restore: Mapping target is unavailable: logical path session/restore/Mapping, providers searched [Session.Vis, Session.World], expected the explored-memory word grid")
	}
	words := s.Vis.WordMask()
	cells := int64(s.World.CellW) * int64(s.World.CellH)
	want := cells >> 2
	if want <= 0 || int64(len(words)) != want {
		return fmt.Errorf("session: retail restore: Mapping grid has %d words, expected %d", len(words), want)
	}
	return applyRetailMappingWords(words, data)
}

// applyRetailMappingWords copies the box into the word grid in its in-memory
// little-endian order. The size gate is exact, as it is for the two Plotmap
// boxes [08 R-SAVE-02 §12].
func applyRetailMappingWords(words []uint16, data []byte) error {
	if len(data) != len(words)*2 {
		return fmt.Errorf("session: retail restore: Mapping box has %d bytes, expected %d", len(data), len(words)*2)
	}
	for i := range words {
		words[i] = binary.LittleEndian.Uint16(data[i*2:])
	}
	return nil
}

func restoreRetailFeatures(svc *features.Service, cat *content.Catalog, img save.FeatureImage) error {
	families := []struct {
		rows []save.FeatureRecord
		kind int
	}{{img.Normal, 0}, {img.Animating, 1}, {img.ThreeD, 2}}
	type resolvedFeature struct {
		row  save.FeatureRecord
		kind int
		def  *content.FeatureDef
	}
	resolved := make([]resolvedFeature, 0, len(img.Normal)+len(img.Animating)+len(img.ThreeD))
	// Resolve every definition and wire length before the first placement. A
	// missing saved-name mapping therefore rejects the image transactionally,
	// instead of leaving an earlier family partially restored [08
	// R-SAVE-FEATURE-01].
	for _, family := range families {
		for _, row := range family.rows {
			var def *content.FeatureDef
			if img.HasTypeNames {
				if int(row.TypeID) >= len(img.TypeNames) || cat == nil {
					return fmt.Errorf("session: retail restore: feature type %d unresolved from saved name table", row.TypeID)
				}
				def = cat.Features[content.CanonicalKey(img.TypeNames[row.TypeID])]
				if def == nil {
					return fmt.Errorf("session: retail restore: saved feature name %q unresolved", img.TypeNames[row.TypeID])
				}
			}
			if def == nil && int(row.TypeID) < len(svc.Terrain.FeatureDefs) {
				def = svc.Terrain.FeatureDefs[row.TypeID]
			}
			if def == nil {
				return fmt.Errorf("session: retail restore: feature type %d unresolved", row.TypeID)
			}
			if want := features.RetailRestorePayloadSize(family.kind); want == 0 || len(row.Data) != want {
				return fmt.Errorf("session: retail restore: feature (%d,%d): family %d payload size %d", row.X, row.Z, family.kind, len(row.Data))
			}
			resolved = append(resolved, resolvedFeature{row: row, kind: family.kind, def: def})
		}
	}
	for _, item := range resolved {
		if _, err := svc.RestoreAt(int(item.row.X), int(item.row.Z), item.def, item.kind, item.row.Data); err != nil {
			return fmt.Errorf("session: retail restore: feature (%d,%d): %w", item.row.X, item.row.Z, err)
		}
	}
	return nil
}

// unitBoxName builds the exact detached box name a unit's account or mover
// record carries — "u" + the stable ID as four lowercase hex digits + the
// family suffix ("acc"/"mob") [08 R-SAVE-02 §6, §8]. Building it by hand
// keeps the per-unit restore loop off fmt.Sprintf's reflection path; the
// index that keys off it is built once from image.Units.Other, not per name
// comparison.
func unitBoxName(id uint16, suffix string) string {
	const hexDigits = "0123456789abcdef"
	var buf [5]byte
	buf[0] = 'u'
	buf[1] = hexDigits[(id>>12)&0xf]
	buf[2] = hexDigits[(id>>8)&0xf]
	buf[3] = hexDigits[(id>>4)&0xf]
	buf[4] = hexDigits[id&0xf]
	return string(buf[:]) + suffix
}

func readUnitRef(data []byte, off int) uint16 {
	if off < 0 || off+2 > len(data) {
		return 0
	}
	return uint16(data[off]) | uint16(data[off+1])<<8
}

func retailUnitRecord(records []save.UnitRecord, id uint16) (save.UnitRecord, bool) {
	for _, rec := range records {
		if !rec.Compat && rec.StableID == id {
			return rec, true
		}
	}
	return save.UnitRecord{}, false
}
