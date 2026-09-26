package ai

import "sync"

// BattleShared is one battle's slot for what a Modern controller
// (internal/aikit) derives from the map alone and every computer player
// would otherwise derive for itself, such as the map analysis each
// controller's preparation runs. The session gives every manager of one
// battle the same slot at composition (Manager.Shared), so it is never
// process-global and a restored battle starts with an empty one. The retail
// step never reads it, and it is not saved: its value is recomputed from the
// map.
//
// The value is the controller package's own type; this package only holds
// it. What the controller keeps there must give every player the answer it
// would have computed alone, and must be safe for the players' preparation
// goroutines to read at once (docs/DESIGN_GAMEPLAY_RULES.md "The Modern AI
// controller").
type BattleShared struct {
	mu sync.Mutex
	v  any
}

// Value returns the slot's value, storing newValue() first when the slot is
// empty. A nil slot has no value to share and returns nil.
func (b *BattleShared) Value(newValue func() any) any {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.v == nil && newValue != nil {
		b.v = newValue()
	}
	return b.v
}
