package audio

import "github.com/nanolathe-gg/nanolathe/internal/content"

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
