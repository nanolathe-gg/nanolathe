package main

import (
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// battleDispatch is the narrow injection surface the HUD and battle input
// controller use to issue authoritative commands without importing session
// internals directly [R-P0-03][07 §9]. Cycle ON-09/session must bind these to
// the real construction/order pumps.
//
// ON-09 must bind:
//   - mobileBuild: construction.QueueMobileBuild(builder, product, wx, wz, 1, cat) via session
//   - factoryBuild: construction.QueueFactoryBuild(factory, product, 1, cat)
//   - orderLatch: orders.Resolve + Queue.Push via session
//
// The present battleSession implements this interface via its methods; headless
// tests can supply recording doubles without a live Session.
//
// Injection points session must bind (short):
//
//	mobileBuildFn(product string, wx, wz numeric.Fixed, queued bool) error
//	factoryBuildFn(product string, queued bool) error
//	orderDispatchFn(latch input.Latch, x, y int32, queued bool)
//
// All are func fields on battleSession; when nil the fallback construction/
// order paths run. Tests inject recording funcs to verify coordinates and
// product names data-driven from BuildMenus without needing a live Session.
type battleDispatch interface {
	DispatchMobileBuild(product string, wx, wz numeric.Fixed, queued bool) error
	DispatchFactoryBuild(product string, queued bool) error
	DispatchOrderLatch(latch input.Latch, x, y int32, queued bool)
}

var _ battleDispatch = (*battleSession)(nil)

// These stubs are overridden in battle.go with real logic; they exist here
// only to document the injected contract for ON-09.
func (b *battleSession) DispatchMobileBuild(product string, wx, wz numeric.Fixed, queued bool) error {
	if b.mobileBuildFn != nil {
		return b.mobileBuildFn(product, wx, wz, queued)
	}
	return b.dispatchMobileBuildFallback(product, wx, wz, queued)
}
func (b *battleSession) DispatchFactoryBuild(product string, queued bool) error {
	if b.factoryBuildFn != nil {
		return b.factoryBuildFn(product, queued)
	}
	return b.dispatchFactoryBuildFallback(product, queued)
}
func (b *battleSession) DispatchOrderLatch(latch input.Latch, x, y int32, queued bool) {
	if b.orderDispatchFn != nil {
		b.orderDispatchFn(latch, x, y, queued)
		return
	}
	// Fallback: map latch to code and dispatch via orderSelected.
	code := 0
	// Use hud helper; avoid import cycle by inline mapping similar to hud.LatchToCode.
	switch latch {
	case input.LatchNormal:
		code = 1
	case input.LatchMove:
		code = 2
	case input.LatchAttack:
		code = 3
	case input.LatchBlast:
		code = 4
	case input.LatchUnload:
		code = 5
	case input.LatchPickup:
		code = 6
	case input.LatchFollow:
		code = 7
	case input.LatchRepair:
		code = 8
	case input.LatchPatrol:
		code = 9
	case input.LatchTeleport:
		code = 11
	case input.LatchReclaim:
		code = 12
	case input.LatchCapture:
		code = 13
	case input.LatchMobileBuild:
		code = 14
	}
	if code != 0 {
		b.orderSelected(code, x, y, queued)
	}
}
func (b *battleSession) CancelPlacement() {
	b.buildDef = ""
	b.buildOK = false
}
func (b *battleSession) IsPlacementArmed() bool   { return b.buildDef != "" }
func (b *battleSession) PlacementProduct() string { return b.buildDef }
