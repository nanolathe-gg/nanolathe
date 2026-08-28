// Package units — per-unit pre-update stage [04 §1.1][04 §5.1][01 §4.4].
//
// The authoritative phase-2 unit sweep is the explicit traversal API in
// sweep.go, driven by the session: VisitActiveSlots with StepPreUpdate at the
// front and FinalizeDeath at slot-end, with weapon/COB/orders/movement work
// between those boundaries [01 §4.4][04 "unit sweep"]. This file owns the
// pre-update stage that StepPreUpdate exposes; the remaining per-unit stage
// helpers (weapon-slot update, COB drain, slot-end death latch) are
// test-only fixtures in pipeline_test.go that drive the same traversal shape
// for package tests [04 §1.1][GAP T15] C17.
//
// Construction Remaining is owned exclusively by construction.Service [05
// "Construction target state"]; no stage here reads or writes it.
package units

import (
	"github.com/nanolathe/nanolathe/internal/cob"
)

// unitPreUpdate is the per-unit pre-update/status work [04 §2.4][04 §5.1].
// It maintains the 30-tick health sample used by the death severity input
// [04 §5.1] C22: every 30 ticks it recomputes clamp(health*100/maxHealth,0,100)
// and stores the previous window's percent. We keep a single PriorSample field
// as the previous-window percent; the current window's sample overwrites it
// directly for determinism without a second field — a one-window lag on
// exact-boundary deaths remains TODO(question) but preserves the contract that
// death severity uses the previous 30-tick window percent, not immediate
// pre-lethal health.
func (w *World) unitPreUpdate(u *Unit, tick uint32) {
	if u == nil || !u.Alive || u.Dying {
		return
	}
	if tick%30 == 0 && u.MaxHealth > 0 { // [04 §5.1] every 30 ticks clamp(health*100/maxHealth,0,100)
		pct := cob.HealthPercent(u.Health, u.MaxHealth) // [04 §4.4] clamped 0..100
		u.PriorSample = uint8(pct)                      // [04 §5.1] prior window percent for Killed severity
	}
	// No Remaining mutation. No order creation. No RNG draws.
}
