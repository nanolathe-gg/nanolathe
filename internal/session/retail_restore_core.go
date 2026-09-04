package session

import (
	"fmt"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/features"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/save"
	"github.com/nanolathe/nanolathe/internal/triggers"
	"github.com/nanolathe/nanolathe/internal/units"
)

// RestoreRetailBattleCore applies the D2 live-state passes to a successful D1
// stage. It never advances the clock or runs a simulation tick. The caller
// remains responsible for atomically swapping the resulting Session into the
// client [08 R-SAVE-02 §11].
//
// The order is deliberately explicit: player/account fields, alliances, base
// bodies, depth-first references, economy, queues, one front-head pump, then
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
	// Resolve references depth-first even though D1 has reserved all bodies;
	// this preserves the retail fix-up order and makes the relationship edge
	// explicit rather than depending on numbered-box order.
	visiting := make(map[uint16]bool, len(image.Units.Records))
	visited := make(map[uint16]bool, len(image.Units.Records))
	var restoreRefs func(uint16) error
	restoreRefs = func(id uint16) error {
		if visited[id] {
			return nil
		}
		if visiting[id] {
			return fmt.Errorf("session: retail restore: cyclic unit reference %d", id)
		}
		visiting[id] = true
		rec, ok := retailUnitRecord(image.Units.Records, id)
		if !ok {
			return fmt.Errorf("session: retail restore: unit reference %d missing", id)
		}
		carrier := readUnitRef(rec.Data, 0x89)
		engagement := readUnitRef(rec.Data, 0x8B)
		if carrier != 0 {
			if err := restoreRefs(carrier); err != nil {
				return err
			}
		}
		if engagement != 0 {
			if err := restoreRefs(engagement); err != nil {
				return err
			}
		}
		h := stage.StableUnit[id]
		if err := units.RetailUnitReferences(s.Units.Unit(h), stage.StableUnit[carrier], stage.StableUnit[engagement], rec.Data[0x8D]); err != nil {
			return err
		}
		if carrier != 0 {
			carrierUnit := s.Units.Unit(stage.StableUnit[carrier])
			if carrierUnit != nil {
				carrierUnit.Attachment.Cargo = append(carrierUnit.Attachment.Cargo, h)
			}
		}
		visiting[id] = false
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
	// pass can retain retail's account → mover → order → pump → script → weapon
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
		for _, raw := range image.Units.Other {
			if raw.Name == fmt.Sprintf("u%04xacc", rec.StableID) {
				if err := economy.RetailUnitAccount(s.Econ, h, raw.Data); err != nil {
					return err
				}
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
				var mover *save.RawBox
				for i := range image.Units.Other {
					raw := &image.Units.Other[i]
					if raw.Name == fmt.Sprintf("u%04xmob", rec.StableID) {
						if mover != nil {
							return fmt.Errorf("session: retail restore: unit %d has duplicate mover boxes", rec.StableID)
						}
						mover = raw
					}
				}
				if mover == nil {
					return fmt.Errorf("session: retail restore: unit %d has mover flag but no mover box", rec.StableID)
				}
				if err := s.Movement.RestoreMover(h, mover.Data); err != nil {
					return fmt.Errorf("session: retail restore: unit %d mover: %w", rec.StableID, err)
				}
			}
		}
		// Rebuild queues after the account and mover state, then bind the front
		// head exactly once before the script snapshot [08 R-SAVE-02 §6].
		group := make([]save.OrderRecord, 0)
		for _, order := range image.Units.Orders {
			if order.ParentStableID == rec.StableID {
				group = append(group, order)
			}
		}
		if err := orders.RetailRestoreOrdersAtTick(owner, group, stage.StableUnit, binding, s.Clock.GlobalTick); err != nil {
			return fmt.Errorf("session: retail restore: unit %d orders: %w", rec.StableID, err)
		}
		if q := orders.QueueOfUnit(owner); q != nil && q.LenPrimary() > 0 {
			result := (&orders.Pump{World: s.Units}).PumpUnit(owner.Handle, s.Clock.GlobalTick)
			if result.Err != nil {
				return fmt.Errorf("session: retail restore: unit %d front pump: %w", rec.StableID, result.Err)
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
		s.visStamps = make(map[int]visStamp)
		s.visStatus = make(map[int]uint32)
		publishVisibilityForAll(s)
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
