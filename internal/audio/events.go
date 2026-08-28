package audio

import (
	"strings"

	"github.com/nanolathe/nanolathe/internal/content"
)

// Slot constants mirror the static table [03 §8.3] C15.
// Slot 0 is sentinel; 1..23 are real events named by Key.
const (
	SlotSelect         Slot = 1
	SlotUnderAttack    Slot = 2
	SlotActivate       Slot = 3
	SlotDeactivate     Slot = 4
	SlotOK             Slot = 5
	SlotArrived        Slot = 6
	SlotCant           Slot = 7
	SlotUnitComplete   Slot = 8
	SlotBuild          Slot = 9
	SlotRepair         Slot = 10
	SlotWorking        Slot = 11
	SlotLoad           Slot = 12
	SlotUnload         Slot = 13
	SlotCloak          Slot = 14
	SlotUncloak        Slot = 15
	SlotCapture        Slot = 16
	SlotCount5         Slot = 17
	SlotCount4         Slot = 18
	SlotCount3         Slot = 19
	SlotCount2         Slot = 20
	SlotCount1         Slot = 21
	SlotCount0         Slot = 22
	SlotCancelDestruct Slot = 23
)

// CategoryFromContent converts a compiled catalog category to the
// presentation queue category [02 "Sound category record"] [03 §8.3] C14.
// It copies variant aliases and captions verbatim; truncation to 64 bytes is
// already enforced at compile time [I13]. Presentation-only, uses no Sim RNG [I4].
func CategoryFromContent(sc *content.SoundCategory) *Category {
	if sc == nil {
		return nil
	}
	c := &Category{Name: sc.Name}
	for i := 0; i < 24 && i < len(sc.Slots); i++ {
		c.Rows[i].Variants = append([]string(nil), sc.Slots[i].Variants...)
		c.Rows[i].Captions = append([]string(nil), sc.Slots[i].Captions...)
	}
	return c
}

// SlotForKey returns the slot for an authored event key (case-insensitive)
// [03 §8.3] static table. It matches the eight-slot queue's key vocabulary.
func SlotForKey(key string) (Slot, bool) {
	k := strings.ToLower(strings.TrimSpace(key))
	for i, e := range slotTable {
		if strings.EqualFold(e.Key, k) && e.Key != "" {
			return Slot(i), true
		}
	}
	return 0, false
}

// AliasForSlot returns the resolved variant alias for a slot's row using the
// presentation CRT stream [03 §8.3] C17 C19. It draws one CRT value and scales
// by variant count with the fifteen-bit range. Caller must have already
// checked cooldown and crowding gates; this helper is presentation-only [I4].
func AliasForSlot(cat *Category, slot Slot, crt uint32) (string, string, bool) {
	if cat == nil || int(slot) >= len(cat.Rows) {
		return "", "", false
	}
	row := cat.Rows[slot]
	if len(row.Variants) == 0 {
		return "", "", false
	}
	draw := (crt >> 16) & 0x7FFF // 15-bit draw [03 §8.3] C17
	idx := int(draw) * len(row.Variants) / variantRange
	if idx >= len(row.Variants) {
		idx = len(row.Variants) - 1
	}
	alias := row.Variants[idx]
	cap := ""
	if idx < len(row.Captions) {
		cap = row.Captions[idx]
	}
	return alias, cap, true
}
