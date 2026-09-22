package main

// Optional community-patch selection conveniences. They consume only the
// immutable committed frame and emit ordinary typed selection commands [I6].

import (
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
)

type communityCycleKind uint8

const (
	communityCycleBuilder communityCycleKind = iota
	communityCycleFactory
)

func (b *battleSession) communitySelectionEnabled() bool {
	return b != nil && b.shell != nil && b.shell.presentation.CommunitySelection&1 != 0
}

func (b *battleSession) doubleClickSelectionEnabled() bool {
	return b != nil && b.shell != nil && b.shell.presentation.DoubleClickSelection&1 != 0
}

// communityCycleMask uses an authored hotkey category whenever any definition
// belongs to it. Only a wholly empty category admits the source's definition
// fallback [community patch engine behavior §4.2].
func (b *battleSession) communityCycleMask(kind communityCycleKind) content.CategoryMask {
	name := "CTRL_B"
	if kind == communityCycleFactory {
		name = "CTRL_F"
	}
	if authored, ok := b.categoryMask(name); ok && !authored.IsZero() {
		return authored
	}
	var derived content.CategoryMask
	if b == nil || b.cat == nil {
		return derived
	}
	for _, def := range b.cat.UnitRecords() {
		// The patch's commander bitmap deliberately groups commanders with
		// decoys: both authored flags must be present. Use those compiled facts
		// rather than assigning meaning to a category name.
		commanderOrDecoy := def != nil && def.ShowPlayerName && def.HideDamage
		if def == nil || !def.Builder || def.IsAirBase || commanderOrDecoy {
			continue
		}
		mobile := def.BMCode != 0
		if (kind == communityCycleBuilder && mobile) || (kind == communityCycleFactory && !mobile) {
			derived = derived.Or(def.UnitMask)
		}
	}
	return derived
}

// communityPrimaryKind returns only the active primary order. The patch's two
// idle searches do not inspect queued secondary work.
func communityPrimaryKind(f *frame.Frame, h pool.Handle) (string, bool) {
	if f == nil {
		return "", false
	}
	for i := range f.OrderQueues {
		q := f.OrderQueues[i]
		if q.Unit == h {
			if len(q.Primary) == 0 {
				return "", false
			}
			return q.Primary[0].Kind, true
		}
	}
	return "", false
}

func communityIdle(f *frame.Frame, h pool.Handle, kind communityCycleKind) bool {
	primary, present := communityPrimaryKind(f, h)
	if !present {
		return true
	}
	if kind == communityCycleFactory {
		return primary != "BuildingBuild"
	}
	return primary == "Standby" || primary == "VTOL_Standby"
}

// nextCommunityCycleUnit walks live committed records in pool-slot order,
// starting strictly after the persistent cursor and wrapping once. Free and
// dead slots are absent from the committed frame; ownSelectableUnit rejects
// unfinished and otherwise ineligible records.
func (b *battleSession) nextCommunityCycleUnit(f *frame.Frame, cursor pool.Handle, mask content.CategoryMask, kind communityCycleKind) (frame.UnitView, bool) {
	if b == nil || f == nil || mask.IsZero() {
		return frame.UnitView{}, false
	}
	find := func(after bool) (frame.UnitView, bool) {
		for i := range f.Units {
			v := f.Units[i]
			if after != (v.Slot > cursor) || !b.ownSelectableUnit(f, v) || !b.inCategory(v, mask) || !communityIdle(f, v.Slot, kind) {
				continue
			}
			// Constructor cycling additionally uses the source's prior-window
			// health sample. Frame membership already establishes a live unit;
			// ownSelectableUnit above is Nanolathe's shared host admission.
			if kind == communityCycleBuilder && (v.PriorHealthSample == 0 || v.PriorHealthSample == 1) {
				continue
			}
			return v, true
		}
		return frame.UnitView{}, false
	}
	if v, ok := find(true); ok {
		return v, true
	}
	return find(false)
}

func (b *battleSession) cycleCommunityIdle(kind communityCycleKind) {
	f, ok := b.currentSnapshot()
	if !ok {
		return
	}
	cursor := &b.communityBuilderCursor
	if kind == communityCycleFactory {
		cursor = &b.communityFactoryCursor
	}
	v, found := b.nextCommunityCycleUnit(f, *cursor, b.communityCycleMask(kind), kind)
	if !found {
		*cursor = 0
		b.commitSelection(nil, false)
		b.disarmPlacement()
		return
	}
	*cursor = v.Slot
	b.commitSelection([]pool.Handle{v.Slot}, false)
	b.disarmPlacement()
	b.centerCommunityCycleUnit(v)
}

func (b *battleSession) centerCommunityCycleUnit(v frame.UnitView) {
	if b == nil || b.cam == nil {
		return
	}
	x := radarMapPixel(v.X)
	y := radarMapPixel(v.Y)
	z := radarMapPixel(v.Z)
	b.cam.JumpToBattleViewCenter(x, z-y/2)
}

// communityMobileWeaponMask is the pinned source's Ctrl+S classification:
// authored CTRL_W membership with the compiled canfly flag clear. Its release
// notes say NOTAIR/NAIR, but that revision has no lookup of either category;
// this consumer follows the executable source rather than assigning an
// unstated meaning to those tokens.
func (b *battleSession) communityMobileWeaponMask() content.CategoryMask {
	weapons, _ := b.categoryMask("CTRL_W")
	var combined content.CategoryMask
	if b == nil || b.cat == nil {
		return combined
	}
	for _, def := range b.cat.UnitRecords() {
		if def != nil && weapons.Contains(def.UnitDefID) && !def.CanFly {
			combined = combined.Or(def.UnitMask)
		}
	}
	return combined
}

func (b *battleSession) selectCommunityOnScreenWeapons() {
	mask := b.communityMobileWeaponMask()
	b.commitSelection(b.ownSelectableHandles(func(v frame.UnitView) bool {
		return b.inCategory(v, mask) && b.onScreenUnit(v)
	}), false)
	b.disarmPlacement()
}

func (b *battleSession) selectedDefinitionMask(f *frame.Frame) content.CategoryMask {
	var mask content.CategoryMask
	if b == nil || f == nil {
		return mask
	}
	for i := range f.Units {
		v := f.Units[i]
		if !containsHandle(f.Selection.Handles, v.Slot) {
			continue
		}
		if v.DefID != 0 {
			mask = mask.Or(content.MaskForID(uint32(v.DefID)))
		}
	}
	return mask
}

func (b *battleSession) selectSameDefinitionsOnScreen(f *frame.Frame) {
	mask := b.selectedDefinitionMask(f)
	if mask.IsZero() {
		return
	}
	b.commitSelection(b.ownSelectableHandles(func(v frame.UnitView) bool {
		return b.inCategory(v, mask) && b.onScreenUnit(v)
	}), false)
}

// handleCommunityDoubleClick consumes only the native classified event. Shift
// does not alter the patch action: the current same-definition set is replaced
// by its on-screen members. The hovered unit is only the ownership/viewport
// admission gate; definition identity comes from the committed selection.
func (b *battleSession) handleCommunityDoubleClick(in *input.State, mx, my int32) bool {
	if !b.doubleClickSelectionEnabled() || in == nil || b.classifyPointer(mx, my) != battlePointerViewport {
		return false
	}
	event, ok := in.CurrentPointer()
	if !ok || event.Kind != input.LeftDoubleClick {
		return false
	}
	f, ok := b.currentSnapshot()
	if !ok {
		return false
	}
	_, hovered, hit := b.pickPresentedUnit(f, mx, my, f.ViewingPlayer)
	if !hit || hovered.Owner != f.Selection.LocalPlayer {
		return false
	}
	b.selectSameDefinitionsOnScreen(f)
	return true
}
