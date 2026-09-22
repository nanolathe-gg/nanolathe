package session

import (
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

type communityHUDPublication struct {
	catalog     *content.Catalog
	reloadNames map[string]bool
}

func (s *Session) communityReloadNames() map[string]bool {
	state := &s.ensurePublicationState().communityHUD
	if state.catalog != s.Catalog {
		state.catalog = s.Catalog
		state.reloadNames = make(map[string]bool)
		if s.Catalog != nil {
			for _, weapon := range s.Catalog.WeaponRecordsByID() {
				if weapon != nil && weapon.ReloadBar {
					state.reloadNames[weapon.Name] = true
				}
			}
		}
	}
	return state.reloadNames
}

// publishCommunityHUD copies the optional label operands at the existing
// tick-end publication boundary; presentation never follows a live slot/list.
func (s *Session) publishCommunityHUD(u *units.Unit) frame.UnitHUDView {
	var out frame.UnitHUDView
	if u == nil || u.Def == nil {
		return out
	}
	authored := [3]*content.WeaponDef{u.Def.Weapon1Def, u.Def.Weapon2Def, u.Def.Weapon3Def}
	names := s.communityReloadNames()
	hasStockpile := false
	for i, definition := range authored {
		slot := u.SlotAt(i)
		live := slot.Weapon
		chosen := definition
		if chosen == nil {
			chosen = live
		}
		if chosen != nil && chosen.Stockpile {
			hasStockpile = true
			out.StockpileCount += uint32(uint8(slot.Ammo))
		}
		view := &out.Weapons[i]
		view.Reload = uint16(slot.Reload)
		if definition != nil {
			view.Tagged = definition.ReloadBar || names[definition.Name]
			view.ReloadTime = uint16(definition.ReloadTime)
		}
		if live != nil {
			view.Tagged = view.Tagged || live.ReloadBar || names[live.Name]
			if view.ReloadTime == 0 {
				view.ReloadTime = uint16(live.ReloadTime)
			}
		}
		typed := definition
		if !communityHUDWeaponHasType(typed) {
			typed = live
		}
		view.Stockpile = typed != nil && typed.Stockpile
	}
	if hasStockpile {
		if q := orders.QueueOfUnit(u); q != nil {
			rear := q.Secondary()
			if len(rear) > 0 && rear[0] != nil && int32(rear[0].Param2) > 0 {
				out.StockpileQueued = rear[0].Param2
			}
		}
	}
	out.TransportCapacity = uint32(u.Def.TransportCapacity)
	out.SingleUnitFlyingTransport = u.Def.CanFly && out.TransportCapacity == 1
	if s.Units != nil {
		for _, handle := range u.Attachment.Cargo {
			if child := s.Units.Unit(handle); child != nil && child.Attachment.Carrier == u.Handle {
				out.TransportCount++
			}
		}
	}
	if s.Combat != nil {
		out.VeteranLevel = s.Combat.VeteranLevel(u.Def, u.Kills)
	}
	return out
}

// The reload reader prefers the authored type flags only when that set is
// nonempty, independently of its reload-word and tag fallback (CP-WPN-7).
func communityHUDWeaponHasType(w *content.WeaponDef) bool {
	return w != nil && (w.LineOfSight || w.Ballistic || w.ShellWeapon || w.BeamWeapon || w.VLaunch || w.Meteor || w.NoRadar || w.Paralyzer ||
		w.Dropped || w.StartSmoke || w.EndSmoke || w.SoundTrigger || w.Guidance || w.Tracks || w.UnitsOnly || w.GroundBounce ||
		w.WaterWeapon || w.ToAirWeapon || w.SmokeTrail || w.Turret || w.SelfProp || w.Propeller || w.NoExplode || w.BurnBlow ||
		w.TwoPhase || w.Cruise || w.CommandFire || w.NoAutoRange || w.Stockpile || w.Targetable || w.Interceptor || w.NotToAir)
}
