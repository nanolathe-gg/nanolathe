package main

// Selection and picking: which committed units the local player may select,
// the rectangle and category filters, and the world pick [07 §9].

import (
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func (b *battleSession) currentSnapshot() (*frame.Frame, bool) {
	if b == nil || b.sess == nil || b.sess.Snapshot == nil {
		return nil, false
	}
	cur := b.sess.Snapshot.Current()
	return cur, cur != nil
}

func (b *battleSession) hasSelection() bool {
	if f, ok := b.currentSnapshot(); ok {
		return len(f.Selection.Handles) != 0
	}
	return false
}

// selectedCommandUnits resolves the committed selection for typed commands.
// Live selection flags are presentation state; command application owns all
// authoritative selection and queue mutation [01 §4.4][07 §9].
func (b *battleSession) selectedCommandUnits() []*units.Unit {
	if b == nil || b.sess == nil {
		return nil
	}
	f, ok := b.currentSnapshot()
	if !ok {
		return nil
	}
	// The committed frame is the complete selection source. Do not dereference
	// the live pool here: command construction happens from immutable facts and
	// every mutation remains a typed Session command [I6].
	out := make([]*units.Unit, 0, len(f.Selection.Handles))
	for _, h := range f.Selection.Handles {
		if v, found := snapshotUnitByHandle(f, h); found && v.Owner == f.Selection.LocalPlayer {
			out = append(out, b.snapshotUnitCopy(v))
		}
	}
	return out
}

// pickTarget returns the unit handle under the cursor if any, else ground pos.
// It is the ONE picking routine that respects fog (local-player word), overlap
// (nearest squared distance wins with strict < tie-break so lower slot wins on
// equal), unit/feature overlap (units win when both within radius, else feature
// under cell wins), and command validity via orders.Resolve gate [04 §3.5][07 §9][03 §3.2] C8 [P0-I03][P0-I14].
// Feature picking sets ResolvePos.HasFeature when a feature footprint covers the
// clicked cell and is visible; unit picking is tried first [07 §8][07 §9][P0-I14].
//
// The short-lived copy carries the committed remaining-build fraction. It is a
// clause of the shared eligibility predicate `E(u)` and the word the shape
// chooser reads in the opposite, *unfinished* sense for its cursorrepair rows
// [07 R-WGT-01 §10]; leaving it zero made every nanoframe look finished, so the
// idle branch offered `cursorselect` over one.
func (b *battleSession) pickTarget(sx, sy int32) (pool.Handle, *units.Unit, *orders.ResolvePos) {
	wx, wy, wz := b.cursorWorld(sx, sy)
	pos := &orders.ResolvePos{X: wx, Y: wy, Z: wz}
	if b.interfaceTypeRightClick() {
		pos.InterfaceType = orders.InterfaceTypeRightClick
	}
	if b.sess == nil || b.cam == nil {
		return 0, nil, pos
	}
	// Unit and feature words coexist at a picked point. Resolve the feature
	// once before either unit path can return: code 12 tests it first
	// [04 R-ORD-02 §1][I6].
	if f, ok := b.currentSnapshot(); ok {
		cx := world.WorldToCell(wx)
		cz := world.WorldToCell(wz)
		for _, fv := range f.Features {
			footX, footZ := int32(fv.FootX), int32(fv.FootZ)
			if footX <= 0 {
				footX = 1
			}
			if footZ <= 0 {
				footZ = 1
			}
			if cx < fv.CX || cx >= fv.CX+footX || cz < fv.CZ || cz >= fv.CZ+footZ {
				continue
			}
			if !snapshotFeatureMappedAt(f, wx, wy, wz, f.ViewingPlayer) {
				continue
			}
			pos.HasFeature = fv.Reclaimable
			pos.IsWreck = b.isCorpseName(fv.DefName)
			pos.FeatureResurrectable = pos.IsWreck && fv.Reclaimable
			break
		}
	}
	// Presentation picking reads only the immutable committed frame. The
	// returned unit is a short-lived copy for cursor semantics, never a live
	// world pointer [07 §8][07 §9][I6].
	//
	// Over the minimap the pointer's unit word is not the view's hot-units
	// winner but the HOT RADAR unit within squared pixel distance < 4 of
	// the pointer, nearest first [03 §3.9][07 R-SEL-02B2]. cursorWorld above has
	// already taken the lens branch for the position, so only the unit word
	// differs — and both take it under the same condition, an armed drag
	// rectangle keeping the pointer in the view branch [07 R-CAM-01 §11].
	if f, ok := b.currentSnapshot(); ok {
		// The megamap's hovered unit is the unit word over it (§3.15).
		if handle, hit, owned := b.megamapPickTarget(f, sx, sy); owned {
			return handle, hit, pos
		}
	}
	if b.modernDrag == nil && b.isOverMinimap(sx, sy) && !b.battleState().Input.DragActive {
		f, ok := b.currentSnapshot()
		if !ok {
			return 0, nil, pos
		}
		handle := b.minimapHoverUnit(f, sx, sy)
		if handle == 0 {
			return 0, nil, pos
		}
		for i := range f.Units {
			view := f.Units[i]
			if view.Slot != handle {
				continue
			}
			hit := &units.Unit{Handle: handle, Owner: view.Owner, X: view.X, Y: view.Y, Z: view.Z, Flags: view.Flags, Health: view.Health, MaxHealth: view.MaxHealth, Remaining: view.BuildRemaining, Alive: true}
			if b.cat != nil && view.DefName != "" {
				hit.Def, _ = b.cat.Unit(view.DefName)
			}
			return handle, hit, pos
		}
		return 0, nil, pos
	}
	if f, ok := b.currentSnapshot(); ok {
		if bh, view, hit := b.pickPresentedUnit(f, sx, sy, f.ViewingPlayer); hit {
			copy := &units.Unit{Handle: bh, Owner: view.Owner, X: view.X, Y: view.Y, Z: view.Z, Flags: view.Flags, Health: view.Health, MaxHealth: view.MaxHealth, Remaining: view.BuildRemaining, Alive: true}
			if b.cat != nil && view.DefName != "" {
				copy.Def, _ = b.cat.Unit(view.DefName)
			}
			return bh, copy, pos
		}
	}

	return 0, nil, pos
}

// The order feature probe reads mapping memory at the picked position, not
// current feature visibility or footprint corners [04 R-ORD-02 §1].
func snapshotFeatureMappedAt(f *frame.Frame, x, y, z numeric.Fixed, viewer uint8) bool {
	if f == nil {
		return false
	}
	m := f.Visibility
	// The common projection narrows each 16.16 coordinate's signed whole
	// word before the half-height shear [03 §3.2]. This resolver always reads
	// mapping memory, independent of the display's current-coverage mode.
	m.CoverageBytes = false
	return client.PointVisible(m, x, y, z, 0, viewer)
}

func snapshotFeatureVisible(f *frame.Frame, v frame.FeatureView, viewer uint8) bool {
	if v.OwnerKnown && v.Owner == viewer {
		return true
	}
	if f == nil {
		return false
	}
	footX, footZ := int32(v.FootX), int32(v.FootZ)
	if footX <= 0 {
		footX = 1
	}
	if footZ <= 0 {
		footZ = 1
	}
	// Feature LOS uses the committed two-corner footprint form. CX/CZ are
	// 16-pixel attribute cells; visibility tiles are 32 pixels, and the shared
	// projected-point helper performs that conversion plus Y shear [03 §3.2].
	minX, minZ := world.CellToWorld(v.CX), world.CellToWorld(v.CZ)
	maxX, maxZ := world.CellToWorld(v.CX+footX), world.CellToWorld(v.CZ+footZ)
	return client.SnapshotPointVisible(f.Visibility, minX, v.Y, minZ, viewer) ||
		client.SnapshotPointVisible(f.Visibility, maxX, v.Y, maxZ, viewer)
}

// isCorpseName is a definition-only helper for authored feature checks.
// Picking itself never calls the live feature service [07 §8][I6].
func (b *battleSession) isCorpseName(name string) bool {
	if b == nil || b.cat == nil {
		return false
	}
	key := content.CanonicalKey(name)
	if i := strings.IndexByte(key, '_'); i >= 0 {
		key = key[:i]
	}
	if key == "" {
		return false
	}
	_, ok := b.cat.Unit(key)
	return ok
}

func (b *battleSession) snapshotUnitCopy(v frame.UnitView) *units.Unit {
	u := &units.Unit{Handle: v.Slot, Owner: v.Owner, X: v.X, Y: v.Y, Z: v.Z, Flags: v.Flags, Health: v.Health, MaxHealth: v.MaxHealth, Activated: v.Activated, Alive: true}
	if b != nil && b.cat != nil && v.DefName != "" {
		u.Def, _ = b.cat.Unit(v.DefName)
	}
	return u
}

// hostile applies the acting unit's committed directional alliance row, which
// is the exact hostility rule the order resolver uses [04 §3.4][05
// R-SHARE-01 §1][I6]. Snapshot units intentionally do not carry live order
// queues or economy bindings.
func (b *battleSession) hostile(actor, target *units.Unit) bool {
	if actor == nil || target == nil {
		return false
	}
	if actor.Owner == target.Owner {
		return false
	}
	f, ok := b.currentSnapshot()
	if !ok || int(actor.Owner) >= len(f.Players) || int(target.Owner) >= len(f.Players) {
		// This is the resolver's invalid-player fallback. A current committed
		// frame always has all ten rows, but preserving it avoids turning an
		// incomplete presentation fixture into a friendly relation.
		return true
	}
	return !f.Players[actor.Owner].Allies[target.Owner]
}

// ownSelectableUnit is the census's "own selectable unit" predicate
// [07 R-CAM-01 §2], byte-for-byte the predicate the rectangle selection of
// [07 §9] and the trigger system's eligible-unit test [08 R-TRIG-01 §3] share.
// Its four clauses, in that section's order: the status word carries
// selection bit 5 (`0x20`); construction remaining is `0.0`; the post-capture
// grace counter is `0`; and either the carrier reference is null or the
// carrier's own status word carries the cargo-selectable bit 30.
//
// The last two used to be missing, on the grounds that neither crossed the
// frame boundary. Neither needs to:
//
//   - The grace counter "is armed to 150 ticks only by a capture whose new
//     owner is a remote (multiplayer) controller … in single-player it is
//     always zero" [08 R-TRIG-01 §3]. Nanolathe is single-player, so the
//     clause is satisfied by construction and is recorded here rather than
//     published. It becomes a real field the day a remote controller exists.
//   - Bit 30 (`0x40000000`) is not a dynamic transport bit at all: it is a
//     static mirror of the carrier definition's `isairbase` flag, written once
//     by the unit initializer and never touched again [04 R-UNIT-06 §3]. The
//     committed carrier's definition answers it, the same way the production
//     dispatcher's builder check reads a compiled definition, so no live pool
//     read and no new frame field are involved [I6].
//
// The visible consequence: a landed aircraft parked on an airbase — or a
// factory product whose factory definition is an airbase — is selectable,
// while cargo aboard an ordinary transport still is not.
func (b *battleSession) ownSelectableUnit(f *frame.Frame, v frame.UnitView) bool {
	if b == nil || b.sess == nil || v.Slot == 0 {
		return false
	}
	if v.Owner != b.sess.LocalOwner {
		return false
	}
	if v.Flags&units.ClassifierEligibleStatus == 0 {
		return false
	}
	if v.BuildRemaining != 0 {
		return false
	}
	// The post-capture grace counter, always zero in single-player
	// [08 R-TRIG-01 §3].
	if v.Carrier == 0 {
		return true
	}
	carrier, ok := snapshotUnitByHandle(f, v.Carrier)
	return ok && b.snapshotAirBase(carrier)
}

// snapshotAirBase answers the carrier's cargo-selectable status bit from the
// compiled definition it mirrors [04 R-UNIT-06 §3][08 R-TRIG-01 §3].
func (b *battleSession) snapshotAirBase(v frame.UnitView) bool {
	if b == nil || b.cat == nil {
		return false
	}
	def, ok := b.cat.Unit(v.DefName)
	return ok && def != nil && def.IsAirBase
}

// dragEndpointWorld narrows a resolved cursor point to the whole
// three-component world endpoint a drag records [07 §9 "Drag-rectangle
// conversion is closed"]. The horizontal pair keeps the signed 16-bit narrowing
// the click classification of [07 R-CAM-01 §14] compares; the height is kept so
// the band's projection can shear the vertical coordinate by that endpoint's
// own height, the way the unit points it is tested against are sheared.
func dragEndpointWorld(wx, wy, wz numeric.Fixed) (x, y, z int32) {
	return int32(int16(wx.Floor())), int32(wy.Floor()), int32(int16(wz.Floor()))
}

// selectionBand projects the two recorded drag endpoints into the band used by
// both the membership test and the drawn box [07 §9 "Drag-rectangle conversion
// is closed"]. It is deliberately computed from the stored world pair at every
// use rather than kept in screen pixels: the camera can move during a gesture,
// and only a band re-projected with the current camera stays over the terrain
// the player dragged across.
func (b *battleSession) selectionBand(in ui.BattleInputState) client.SelectionBand {
	if b == nil {
		return client.SelectionBand{}
	}
	return client.WorldSelectionBand(b.cam,
		numeric.FixedFromInt(int64(in.DragStartWorldX)),
		numeric.FixedFromInt(int64(in.DragStartWorldY)),
		numeric.FixedFromInt(int64(in.DragStartWorldZ)),
		numeric.FixedFromInt(int64(in.DragEndWorldX)),
		numeric.FixedFromInt(int64(in.DragEndWorldY)),
		numeric.FixedFromInt(int64(in.DragEndWorldZ)))
}

// eligibleHandlesInBand is the drag-rectangle walk of [07 §9] as
// [07 R-WGT-01 §10] restates it: "walk the local player's unit slice in
// ascending record order; for each record with `E(u)` true, apply the inclusive
// rectangle test... A record failing `E(u)` is neither written, toggled, nor
// counted, regardless of position."
//
// The eligibility filter used to be missing here: the rect walk returned every
// VISIBLE unit, so a drag across a battle line selected the enemy's units and
// half-built nanoframes alongside one's own. `ownSelectableUnit` is this
// build's `E(u)` — the selectable status bit, the remaining-build fraction, and
// the carrier clause through the carrier definition's airbase mirror — so the
// two are composed rather than the predicate being written twice. Both inputs
// are frame order, which is pool-slot order [I1], so the result stays ascending.
func (b *battleSession) eligibleHandlesInBand(f *frame.Frame, band client.SelectionBand) []pool.Handle {
	if b == nil || f == nil {
		return nil
	}
	eligible := make(map[pool.Handle]struct{}, len(f.Units))
	for i := range f.Units {
		if v := f.Units[i]; b.ownSelectableUnit(f, v) {
			eligible[v.Slot] = struct{}{}
		}
	}
	inRect := client.SnapshotUnitHandlesInBand(f, b.cam, band, f.ViewingPlayer)
	out := make([]pool.Handle, 0, len(inRect))
	for _, h := range inRect {
		if _, ok := eligible[h]; ok {
			out = append(out, h)
		}
	}
	return out
}

// ownSelectableHandles walks the committed frame in slot order and returns the
// own selectable units, optionally filtered [07 R-CAM-01 §2]. Frame order is
// pool-slot order, which is the order retail's selection walks [I1].
func (b *battleSession) ownSelectableHandles(keep func(frame.UnitView) bool) []pool.Handle {
	f, ok := b.currentSnapshot()
	if !ok {
		return nil
	}
	out := make([]pool.Handle, 0, len(f.Units))
	for i := range f.Units {
		v := f.Units[i]
		if !b.ownSelectableUnit(f, v) {
			continue
		}
		if keep != nil && !keep(v) {
			continue
		}
		out = append(out, v.Slot)
	}
	return out
}

// selectedHandlesInSlotOrder returns the committed selection in slot order.
func (b *battleSession) selectedHandlesInSlotOrder() []pool.Handle {
	f, ok := b.currentSnapshot()
	if !ok {
		return nil
	}
	out := make([]pool.Handle, 0, len(f.Selection.Handles))
	for i := range f.Units {
		if containsHandle(f.Selection.Handles, f.Units[i].Slot) {
			out = append(out, f.Units[i].Slot)
		}
	}
	return out
}

func containsHandle(list []pool.Handle, h pool.Handle) bool {
	for _, v := range list {
		if v == h {
			return true
		}
	}
	return false
}

// commitSelection sends a selection to the session. Additive means "add to the
// current selection" — the census's word for Ctrl+A and for a Shift-held
// category select — which is a union followed by a replace, not the toggle a
// Shift-click performs [07 R-CAM-01 §2][07 §9].
func (b *battleSession) commitSelection(handles []pool.Handle, additive bool) {
	if additive {
		f, ok := b.currentSnapshot()
		if ok {
			merged := make([]pool.Handle, 0, len(handles)+len(f.Selection.Handles))
			merged = append(merged, f.Selection.Handles...)
			for _, h := range handles {
				if !containsHandle(merged, h) {
					merged = append(merged, h)
				}
			}
			handles = merged
		}
	}
	_ = b.enqueueSelectionCommand(session.HumanCommand{
		Kind:      session.HumanSelectionReplace,
		Selection: session.HumanSelectionCommand{Handles: handles},
	})
}

// categoryMask resolves an authored category token against the compiled
// registry. Membership is catalog data; no hand-written unit list exists here
// [02 §5][R-P0-03].
func (b *battleSession) categoryMask(name string) (content.CategoryMask, bool) {
	if b == nil || b.cat == nil {
		return content.CategoryMask{}, false
	}
	return b.cat.Category(name)
}

// inCategory reports whether a frame unit's definition is in a membership set.
func (b *battleSession) inCategory(v frame.UnitView, mask content.CategoryMask) bool {
	return v.DefID != 0 && mask.Contains(uint32(v.DefID))
}
