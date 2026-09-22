package session

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

type observeDeathWeapon struct {
	combat.Rules
	unit     pool.Handle
	selected **content.WeaponDef
}

func (r observeDeathWeapon) DeathWeapon(q combat.DeathWeaponRequest) *content.WeaponDef {
	weapon := r.Rules.DeathWeapon(q)
	if q.Unit != nil && q.Unit.Handle == r.unit {
		*r.selected = weapon
	}
	return weapon
}

func TestCommunityTransportDeathSurvivesCargoDetach(t *testing.T) {
	for _, mode := range []gameplay.Mode{gameplay.Strict31, gameplay.Community39, gameplay.Modern} {
		for _, killCarrier := range []bool{false, true} {
			s := newLoopTestSession(t, 0)
			s.SetGameplay(mode)
			stock, carried := &content.WeaponDef{ID: 11}, &content.WeaponDef{ID: 12}
			def := *s.Catalog.Units["armcom"]
			def.ExplodeAsDef, def.TransportedExplodeAsDef = stock, carried
			def.IsFeature, def.Corpse = false, ""
			carrierHandle, err := s.Units.Create(&def, 0, 100<<16, 10<<16, 100<<16)
			if err != nil {
				t.Fatal(err)
			}
			passengerHandle, err := s.Units.Create(&def, 0, 100<<16, 10<<16, 100<<16)
			if err != nil {
				t.Fatal(err)
			}
			carrier, passenger := s.Units.Unit(carrierHandle), s.Units.Unit(passengerHandle)
			carrier.Attachment.Cargo = []pool.Handle{passengerHandle}
			passenger.Attachment.Carrier = carrierHandle
			passenger.SetScript(cob.NewVM(makeKilledProg(1)))
			var selected *content.WeaponDef
			s.Rules.Combat = observeDeathWeapon{Rules: s.Rules.Combat, unit: passengerHandle, selected: &selected}
			if killCarrier {
				carrier.Health = -1
				s.Units.Destroy(carrierHandle, units.DeathKilled)
				s.finalizePhase2Death(carrierHandle, 1)
			} else {
				passenger.Health = -1
				s.Units.Destroy(passengerHandle, units.DeathKilled)
			}
			s.finalizePhase2Death(passengerHandle, 2)
			want := carried
			if mode == gameplay.Strict31 {
				want = stock
			}
			if selected != want || passenger.Attachment.Carrier != 0 {
				t.Fatalf("mode=%s carrierDeath=%v selected=%p want=%p carrier=%d", mode, killCarrier, selected, want, passenger.Attachment.Carrier)
			}
		}
	}
}
