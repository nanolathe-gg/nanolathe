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
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/triggers"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
)

// A rejected detached candidate may be retried after its bad payload is
// corrected. Retain completed unit phases so retry neither draws a new
// constructor phase nor repeats an attachment after restoring pending death.
type retailUnitRestoreProgress uint8

const (
	retailUnitCarrierRestored retailUnitRestoreProgress = iota + 1
	retailUnitBodyRestored
	retailUnitOrdersRestored
	retailUnitRestored
)

// RestoreRetailBattleCore applies the D2 live-state passes to a successful D1
// stage. It never advances the clock or runs a simulation tick. The caller
// remains responsible for atomically swapping the resulting Session into the
// client [08 R-SAVE-02 §11].
//
// Each unit is constructed on its first recursive visit, publishes its saved
// pose, loads its carrier and engagement references, then finishes its scalar
// body, accounts, mover, orders, script and weapons before returning
// [08 R-SAVE-02 §6, §7, §8, §9, §11].
func RestoreRetailBattleCore(stage *RetailBattleStage) error {
	if stage == nil || stage.Session == nil || stage.Image == nil {
		return fmt.Errorf("session: retail restore: nil stage")
	}
	if stage.coreRestored {
		return nil
	}
	s := stage.Session
	image := stage.Image
	if s.Units == nil || s.Econ == nil {
		return fmt.Errorf("session: retail restore: incomplete session shell")
	}
	// Campaign marks belong to both load routes. They must survive the later
	// single-mission teardown write and the next save [08 R-SAVE-02 §2]
	// [08 R-CAMP-01 §8].
	s.Progress.Thumbs = retailProgressThumbs(image.Summary.Thumbs)

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
		s.ViewingOwner = uint8(image.HumanPlayer)
		if s.Vis != nil {
			s.Vis.SetViewingPlayer(visibility.PlayerID(s.ViewingOwner))
		}
	}
	// Alliance rows are applied by the per-slot loop above, through
	// PlayerSlot.ApplyToEconomy. The row is the last item of each `Player%i`
	// account, so row *i* is slot *i*'s own first alliance row and the
	// ownership question this site used to carry is answered by the account
	// the box sits in [08 "Player records"] [05 R-SHARE-01 §1]. The self
	// column is forced to 1 on the way in, and a slot whose account carried no
	// box keeps the row battle entry built.

	// Production staging restores these accounts before forced allocation.
	// A caller-supplied detached shell takes the same pass here; completed
	// stages do not ignite features or consume their random draw twice.
	if err := stage.restoreFeatures(); err != nil {
		return err
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
	// Identity reservation and payload indexing consume no constructor RNG.
	// Detached test shells may supply already allocated bodies; production
	// leaves every reserved slot empty until this traversal first reaches it.
	recordIndex := make(map[uint16]int, len(image.Units.Records))
	for i, rec := range image.Units.Records {
		if rec.Compat {
			continue
		}
		if stage.StableUnit[rec.StableID] == 0 {
			return fmt.Errorf("session: retail restore: stable unit %d was not reserved", rec.StableID)
		}
		if len(rec.Data) != save.UnitBoxSize {
			return fmt.Errorf("session: retail restore: unit %d base: invalid image size %d", rec.StableID, len(rec.Data))
		}
		recordIndex[rec.StableID] = i
	}
	// Carrier containment is acyclic even when engagement references form a
	// cycle. Validate those edges separately: a mixed reference path is not
	// itself a containment cycle [08 R-SAVE-02 §6].
	carrierState := make(map[uint16]uint8, len(recordIndex))
	var validateCarrier func(uint16) error
	validateCarrier = func(id uint16) error {
		if carrierState[id] == 1 {
			return fmt.Errorf("session: retail restore: cyclic carrier reference %d", id)
		}
		if carrierState[id] == 2 {
			return nil
		}
		i, ok := recordIndex[id]
		if !ok {
			return fmt.Errorf("session: retail restore: unit reference %d missing", id)
		}
		carrierState[id] = 1
		if carrier := readUnitRef(image.Units.Records[i].Data, 0x89); carrier != 0 {
			if err := validateCarrier(carrier); err != nil {
				return err
			}
		}
		carrierState[id] = 2
		return nil
	}
	for _, rec := range image.Units.Records {
		if !rec.Compat {
			if err := validateCarrier(rec.StableID); err != nil {
				return err
			}
			if target := readUnitRef(rec.Data, 0x8b); target != 0 {
				if _, ok := recordIndex[target]; !ok {
					return fmt.Errorf("session: retail restore: engagement reference %d missing", target)
				}
			}
		}
	}
	if stage.unitProgress == nil {
		stage.unitProgress = make(map[uint16]retailUnitRestoreProgress, len(recordIndex))
	}
	processing := make(map[uint16]bool, len(recordIndex))
	var restoreUnit func(uint16) error
	restoreUnit = func(id uint16) error {
		// The allocator makes the slot live before either reference is read.
		// A recursive back-reference skips that record; its original visit
		// still finishes its later fields [08 R-SAVE-02 §6].
		if processing[id] || stage.unitProgress[id] == retailUnitRestored {
			return nil
		}
		processing[id] = true
		recIndex := recordIndex[id]
		rec := image.Units.Records[recIndex]
		h := stage.StableUnit[id]
		owner := s.Units.Unit(h)
		if owner == nil {
			if s.Catalog == nil {
				return fmt.Errorf("session: retail restore: unit %d has no catalog", id)
			}
			if _, err := allocateRetailUnit(s.Units, s.Catalog, rec, s.Build); err != nil {
				return err
			}
			owner = s.Units.Unit(h)
		}
		if stage.unitProgress[id] < retailUnitCarrierRestored {
			if err := units.RetailUnitPose(owner, rec.Data); err != nil {
				return fmt.Errorf("session: retail restore: unit %d pose: %w", id, err)
			}
			carrier := readUnitRef(rec.Data, 0x89)
			if carrier != 0 {
				if err := restoreUnit(carrier); err != nil {
					return err
				}
				// Attach before the engagement recursion and saved death latch.
				// Retain this phase before recursing into engagement, so a
				// retry never repeats this head insertion [08 R-SAVE-02 §6].
				mode := int((binary.LittleEndian.Uint32(rec.Data[0xb4:]) >> 4) & 3)
				if !movement.AttachCargoMode(s.Units, stage.StableUnit[carrier], h, int(rec.Data[0x8d]), mode) {
					return fmt.Errorf("session: retail restore: attach unit %d to carrier %d", id, carrier)
				}
			} else {
				owner.Attachment.AttachPiece = -1
			}
			stage.unitProgress[id] = retailUnitCarrierRestored
		}
		if stage.unitProgress[id] < retailUnitBodyRestored {
			engagement := readUnitRef(rec.Data, 0x8b)
			if engagement != 0 {
				if err := restoreUnit(engagement); err != nil {
					return err
				}
			}
			owner.EngagementTarget = stage.StableUnit[engagement]
			if err := units.RetailUnitState(owner, rec.Data); err != nil {
				return fmt.Errorf("session: retail restore: unit %d base: %w", id, err)
			}
			// The group append follows the recursive references, so retain this
			// sequence for the derived manager vectors [08 R-SAVE-02 §6].
			stage.restoredUnits = append(stage.restoredUnits, owner)
			stage.unitProgress[id] = retailUnitBodyRestored
		}
		if stage.unitProgress[id] < retailUnitOrdersRestored {
			// Unit economy accounts are detached in UnitImage.Other. This is the
			// first per-unit later pass [08 R-SAVE-02 §6].
			for _, idx := range otherByName[unitBoxName(rec.StableID, "acc")] {
				if err := economy.RetailUnitAccount(s.Econ, h, image.Units.Other[idx].Data); err != nil {
					return err
				}
			}
			// A mover box is read only inside the established HasMover branch;
			// detached boxes for a no-mover unit are ignored [08 R-SAVE-02 §6, §8].
			// Construction progress does not suppress the saved mover branch: a
			// factory product can be unfinished and still own a mover.
			if s.Movement != nil && owner.HasMover {
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
			} else if s.Movement != nil && owner.Remaining == 0 {
				// A completed no-mover structure still has the collision surface that
				// owns its saved yard/footprint stamp. This is structure support, not a
				// fabricated mover, and it remains separate from HasMover.
				s.Movement.EnsureUnit(owner)
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
			stage.unitProgress[id] = retailUnitOrdersRestored
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
		if err := units.RetailUnitWeapons(owner, rec.Data); err != nil {
			return fmt.Errorf("session: retail restore: unit %d weapon state: %w", id, err)
		}
		if err := units.RetailUnitWeaponDefinitions(owner, rec.Data); err != nil {
			return fmt.Errorf("session: retail restore: unit %d weapons: %w", rec.StableID, err)
		}
		if err := units.RetailUnitWeaponTargets(s.Units.Unit(h), func(id uint16) (pool.Handle, bool) {
			return pool.Handle(id), id != 0 && int(id) < s.Units.TotalRecords()
		}); err != nil {
			return fmt.Errorf("session: retail restore: unit %d weapon targets: %w", rec.StableID, err)
		}
		stage.unitProgress[id] = retailUnitRestored
		processing[id] = false
		return nil
	}
	for _, rec := range image.Units.Records {
		if !rec.Compat {
			if err := restoreUnit(rec.StableID); err != nil {
				return err
			}
		}
	}
	// All constructor yards must be released together before the derived
	// occupancy pass, so a later constructor stamp cannot affect an earlier
	// unit's restored overlap decision [08 R-SAVE-02 §11][04 R-COLL-01 §4].
	if s.Build != nil {
		for _, rec := range image.Units.Records {
			if !rec.Compat {
				s.Build.ReleasePlacement(stage.StableUnit[rec.StableID])
			}
		}
	}
	if s.Build != nil {
		s.Build.RestoreBuilderLinks()
	}
	// Occupancy registration is a derived pass after all per-unit live words;
	// every cell mutation still goes through movement's ordinary stamp path
	// [08 R-SAVE-02 §11].
	//
	// Saved movers and completed structures both restore their committed anchor.
	// The latter use their building collision surface for yard/footprint support;
	// an unfinished no-mover frame remains owned by construction placement.
	if s.Movement != nil {
		for _, rec := range image.Units.Records {
			if rec.Compat {
				continue
			}
			h := stage.StableUnit[rec.StableID]
			u := s.Units.Unit(h)
			if !u.HasMover && u.Remaining != 0 {
				continue
			}
			if err := s.Movement.RestoreOccupancy(h, u.CachedOccupancyX, u.CachedOccupancyZ); err != nil {
				return fmt.Errorf("session: retail restore: unit %d occupancy: %w", rec.StableID, err)
			}
		}
	}

	// Construction retains the same saved anchor for port-18 transactions and
	// teardown. Unfinished structures have no mover but still own a yard, so
	// they participate in this reconstruction too [08 R-SAVE-02 §6, §11].
	if s.Build != nil {
		for _, rec := range image.Units.Records {
			if !rec.Compat {
				if err := s.Build.RestoreBuildingPlacement(s.Units.Unit(stage.StableUnit[rec.StableID])); err != nil {
					return fmt.Errorf("session: retail restore: unit %d building placement: %w", rec.StableID, err)
				}
			}
		}
	}

	// Rebuild the manager vectors from the retained recursive group order.
	for _, mgr := range s.AI {
		if mgr != nil {
			mgr.RestoreGroupsFromUnits(stage.restoredUnits)
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
		publishVisibilityForAll(s)
	}
	if s.Features != nil && s.Features.BurnSound != nil {
		for _, pos := range stage.burnSounds {
			s.Features.BurnSound(pos)
		}
	}
	stage.burnSounds = nil
	stage.coreRestored = true
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
