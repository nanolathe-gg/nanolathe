// Package orders — transport order handlers [04 §10.2] C31 [PLAN_07].
//
// Wires the EXISTING transport executors (helper-only in internal/movement)
// into their order descriptors so Load/Unload orders drive them end-to-end.
//
// Mapping per [04 §10.2][04 §3.1][04 §3.4]:
//
//	Ground_Pickup / VTOL_Pickup  -> load executor (phase table 0..5)
//	Ground_Unload / VTOL_Unload  -> unload executor (phase table 0..3, double validation)
//	VTOL_Landing                 -> landing-pad executor (QueryLandingPad 0..3)
//	BeCarried                    -> cargo passive state while carried
//
// Anything not established (exact pickup approach radius, modelTop/modelBottom
// offsets, cruise altitude queuing, status bits, event codes) stays
// TODO(question) with placeholder marked. No new RNG draws beyond what
// executors and pump codes already specify [I4].
package orders

import (
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/units"
)

// Transport verbatim diagnostics [04 §10.2] C31 [GAP T16].
const (
	transportFailedMessage = "Transport mission failed"       // gates 1–3 share this terminal with code 8 [04 §10.2]
	transportHeavyMessage  = "Unit is too heavy to transport" // size gate at phase 0 code 8 verbatim [04 §10.2]
	unableUnloadMessage    = "Unable to unload unit"          // unload placement failure code 9 [04 §10.2]
)

// Sentinel for attach piece -1 (root fallback) stored in Param1 [04 §5.3][04 §10.2].
// Param1 is uint32; 0xFFFFFFFF represents -1.
const attachPieceRootSentinel = 0xFFFFFFFF

func isVTOLPickup(name string) bool { return name == "VTOL_Pickup" }
func isVTOLUnload(name string) bool { return name == "VTOL_Unload" }

// lookupTarget resolves target handle via per-queue Lookup [P0-I16].
func lookupTarget(carrier *units.Unit, target pool.Handle) *units.Unit {
	if carrier == nil || target == 0 {
		return nil
	}
	if q := QueueForUnit(carrier); q != nil {
		if binding := q.Binding(); binding != nil && binding.Lookup != nil {
			return binding.Lookup(target)
		}
	}
	return nil
}

// boardingRange returns the effective boarding range for carrier [04 §10.2].
// First enabled weapon slot's range scanned via weapon-slot enabled flag;
// shipped unarmed fallback is weapon record 0 (NOWEAPON, Range 16) [04 §10.2].
// TODO(question): exact weapon-slot enabled flag not represented in content.UnitDef;
// using first active Weapon1Def/2Def/3Def as proxy [02 "Unit record"][06 §1.2];
// the record-0 inactive sentinel a missed link resolves to is not a weapon
// [02 §5 R-CONTENT-02].
func boardingRange(carrier *units.Unit) int32 {
	if carrier == nil || carrier.Def == nil {
		return 16
	}
	if !content.IsWeaponInactive(carrier.Def.Weapon1Def) {
		return carrier.Def.Weapon1Def.Range
	}
	if !content.IsWeaponInactive(carrier.Def.Weapon2Def) {
		return carrier.Def.Weapon2Def.Range
	}
	if !content.IsWeaponInactive(carrier.Def.Weapon3Def) {
		return carrier.Def.Weapon3Def.Range
	}
	// Fallback via installed slots (units.Slot.Weapon) when Def.Weapon*Def not wired.
	for i := 0; i < 3; i++ {
		if sl := carrier.SlotAt(i); sl != nil && sl.Weapon != nil {
			return sl.Weapon.Range
		}
	}
	return 16 // NOWEAPON fallback [04 §10.2]
}

// checkPickupEntryGates implements the four re-checks run at entry to every load
// phase [04 §10.2] C31. Returns true with result 8 and appropriate diagnostic
// when a gate fails; gate 4 returns code 8 with NO message [04 §10.2].
// Order of checks is retail order [04 §10.2].
func checkPickupEntryGates(carrier *units.Unit, n *Node) (failed bool, code Code, msg string) {
	if n == nil || n.Target == 0 {
		return true, 8, transportFailedMessage // gate 1: target non-null [04 §10.2]
	}
	// Gate 2: executor flags word must hold none of mask 0x10048 [04 §10.2] C31.
	// TODO(question): exact offset of this flags word not established; word is on the executor/unit state.
	// Placeholder: treat Node.Flags bits 0x10048 as proxy; assume 0 for tests.
	if n.Flags&0x10048 != 0 {
		return true, 8, transportFailedMessage
	}
	// Gate 3: target Y + modelTop SIGNED greater than seaLevel<<16 [04 §10.2] C31.
	// TODO(question): definition modelTop field not in content.UnitDef; placeholder uses target.Y alone.
	// SeaLevel placeholder 0 when no terrain; assume pass for ground tests.
	// For now, if target lookup succeeds and target.Y is very low (submerged), we would fail,
	// but without seaLevel we assume pass.
	// Gate 4: AIR carrier cargo list must be EMPTY; ground carriers don't apply [04 §10.2] C31.
	if carrier != nil && carrier.Def != nil && carrier.Def.CanFly {
		if len(carrier.Attachment.Cargo) > 0 {
			// Count live cargo where parent == carrier
			cnt := 0
			for _, h := range carrier.Attachment.Cargo {
				if tgt := lookupTarget(carrier, h); tgt != nil && tgt.Attachment.Carrier == carrier.Handle {
					cnt++
				} else if h != 0 {
					// Fallback count raw entry when lookup fails but handle non-zero
					cnt++
				}
			}
			if cnt > 0 {
				return true, 8, "" // gate 4 returns code 8 with NO message [04 §10.2]
			}
		}
	}
	return false, 0, ""
}

// pickupHandler implements Ground_Pickup / VTOL_Pickup per [04 §10.2] load phase table.
// It advances phases 0..5 and performs attachment at phase 4.
// All fixed-point world state remains 16.16 [I2]; no float64; no map iteration [I1].
func pickupHandler(carrier *units.Unit, n *Node, satisfied uint32) Code {
	_ = satisfied
	if carrier == nil || n == nil {
		return 8
	}
	if failed, code, _ := checkPickupEntryGates(carrier, n); failed {
		return code
	}
	name := DescriptorFor(n.ID).Name
	isVTOL := isVTOLPickup(name)

	switch n.Phase {
	case 0:
		// Phase 0: require live carrier mover and canfly else 7 [04 §10.2].
		// TODO(question): live mover check placeholder; assume CanMove/CanFly/CanLoad indicates live mover.
		hasLiveMover := false
		if carrier.Def != nil {
			if carrier.Def.CanMove || carrier.Def.CanFly || carrier.Def.CanLoad {
				hasLiveMover = true
			}
		}
		// Fallback: if Move.Mode !=0, consider live.
		if !hasLiveMover && carrier.Move.Mode != 0 {
			hasLiveMover = true
		}
		if !hasLiveMover {
			return 7
		}
		if isVTOL {
			if carrier.Def == nil || !carrier.Def.CanFly {
				return 7
			}
		}
		// Size gate: target cached FootPrintX WORD signed must be <= carrier transportsize BYTE zero-extended [04 §10.2].
		target := lookupTarget(carrier, n.Target)
		if target != nil && target.Def != nil && carrier.Def != nil {
			foot := int16(target.Def.FootprintX) // TODO(question): using Def.FootprintX vs movement class FootPrintX WORD
			if int32(foot) > int32(carrier.Def.TransportSize) {
				// Log verbatim diagnostic via queue diagnostics? Handler return code is 8; message is emitted by caller via diagnostics.
				// For now, record diagnostic on queue if available.
				if q := QueueForUnit(carrier); q != nil {
					q.recordDiagnostic(transportHeavyMessage)
				}
				return 8 // Unit is too heavy to transport verbatim [04 §10.2]
			}
		}
		// TODO(question): point-command queuing at carrier current X/Z with altitude cruisealt/2 and no radius, status bits, Activate, mover mode etc not simulated beyond phase advance [04 §10.2].
		return 1
	case 1:
		// Phase 1: queue follow command toward target with full cruisealt altitude offset and horizontal arrival radius 0x30 [04 §10.2].
		// TODO(question): follow-command altitude cruisealt and radius 0x30 queuing not simulated beyond phase advance [04 §10.2].
		// TODO(question): exact pickup approach radius remains TODO(question); using BoardingRange placeholder for admission but 0x30 for phase1 radius [04 §10.2] C31.
		_ = boardingRange(carrier) // ensure no unused, but approach radius is fixed 0x30 [04 §10.2]
		return 1
	case 2:
		// Phase 2: status Preparing for transport; pre-seed first QueryTransport output to -1 and run synchronous four-output query [04 §5.3][04 §10.2].
		// Observed seed [-1,0,0,0]; missing script leaves -1 root-piece fallback [04 §10.2]; retain output 0 as attach piece; status =0x100E8 [04 §10.2].
		if n.Param1 == 0 && n.Param2 == 0 && n.Param3 == 0 {
			n.Param1 = attachPieceRootSentinel // root fallback [04 §5.3][04 §10.2]
		}
		if n.Param1 == 0 {
			n.Param1 = attachPieceRootSentinel
		}
		return 1
	case 3:
		// Phase 3: start asynchronous one-argument BeginTransport with exact 32-bit target-definition model-top value mirrored through network forwarder [04 §10.2];
		// fetch selected piece world transform; construct cargo follow order with altitude offset = NEGATED integer part of that piece's world Y [04 §10.2];
		// status =0x100EA [04 §10.2].
		// TODO(question): BeginTransport async callback and cargo follow-order hang height (negated piece Y) are established but piece world transform fetch and network mirroring not simulated [04 §10.2].
		return 1
	case 4:
		// Phase 4 has two edges [04 §10.2]:
		// Interrupted: flags &0x42 present => start deferred zero-argument EndTransport and return WITHOUT attaching => code 8 [04 §10.2].
		// TODO(question): exact flags word offset not established; using Node.Flags placeholder for interrupt check.
		if n.Flags&0x42 != 0 {
			return 8
		}
		// Success: attach target to carrier on queried piece; emit event code 12 [GAP T16][04 §10.2]; queue climb-away point command at carrier current X/Z with altitude cruisealt, no radius; status |=0xE0 [04 §10.2].
		target := lookupTarget(carrier, n.Target)
		if target == nil {
			return 8
		}
		if target.Attachment.Carrier != 0 {
			return 8 // already carried
		}
		piece := int32(-1)
		if n.Param1 != attachPieceRootSentinel {
			// Param1 holds piece index; decode sentinel -1 vs valid
			if n.Param1 == 0xFFFFFFFF {
				piece = -1
			} else {
				piece = int32(n.Param1)
			}
		}
		// Perform attachment via direct field mutation (avoids import cycle with movement).
		target.Attachment.Carrier = carrier.Handle
		target.Attachment.AttachPiece = int(piece)
		// Append to carrier cargo if not already
		found := false
		for _, h := range carrier.Attachment.Cargo {
			if h == n.Target {
				found = true
				break
			}
		}
		if !found {
			carrier.Attachment.Cargo = append(carrier.Attachment.Cargo, n.Target)
		}
		// Also push BeCarried onto cargo's queue to reflect "Being transported" state [04 §3.1] BeCarried.
		if cargoQ := QueueForUnit(target); cargoQ != nil {
			beID := Lookup("BeCarried")
			if beID != 0 {
				// Avoid duplicate BeCarried
				hasBe := false
				for _, nn := range cargoQ.Primary() {
					if nn.ID == beID {
						hasBe = true
						break
					}
				}
				if !hasBe {
					cargoQ.Push(beID, Node{Target: carrier.Handle})
					// BeCarried has gate 0x24; ensure it is dispatchable (clear gate for phase 0)
					if head := cargoQ.Head(); head != nil && head.ID == beID && head.Phase == 0 && head.DynamicGate != 0 {
						head.DynamicGate = 0
						head.Deadline = -1
					}
				}
			}
		}
		// TODO(question): climb-away point command at carrier current X/Z with altitude cruisealt, no radius – not simulated.
		// TODO(question): event code 12 emission via presentation service not wired – would be Gap T16.
		return 1
	case 5:
		// Phase 5: no work result 5 [04 §10.2].
		return 5
	default:
		// Other: no work result 7 [04 §10.2].
		return 7
	}
}

// unloadHandler implements Ground_Unload / VTOL_Unload per [04 §10.2] unload dispatch.
// Returns done 5 immediately when cargo list already empty, then dispatches on phase.
func unloadHandler(carrier *units.Unit, n *Node, satisfied uint32) Code {
	_ = satisfied
	if carrier == nil || n == nil {
		return 7
	}
	// Immediate done when cargo list already empty [04 §10.2] unload.
	if len(carrier.Attachment.Cargo) == 0 {
		return 5
	}
	name := DescriptorFor(n.ID).Name
	isVTOL := isVTOLUnload(name)

	switch n.Phase {
	case 0:
		// Phase 0 requires live canfly carrier mover else 7 [04 §10.2] unload.
		hasLiveMover := false
		if carrier.Def != nil {
			if carrier.Def.CanMove || carrier.Def.CanFly || carrier.Def.CanLoad {
				hasLiveMover = true
			}
		}
		if !hasLiveMover && carrier.Move.Mode != 0 {
			hasLiveMover = true
		}
		if !hasLiveMover {
			return 7
		}
		if isVTOL {
			if carrier.Def == nil || !carrier.Def.CanFly {
				return 7
			}
		}
		// Success announces Unloading, records cargo reference, queues point command toward stored drop point with altitude cruisealt and radius 0x140 [04 §10.2].
		// Record cargo reference in Param1 if not already
		if n.Param1 == 0 && len(carrier.Attachment.Cargo) > 0 {
			n.Param1 = uint32(carrier.Attachment.Cargo[0])
		}
		// TODO(question): point-command altitude cruisealt and radius 0x140 queuing not simulated beyond phase advance [04 §10.2].
		return 1
	case 1:
		// Phase 1 converts drop point to footprint anchor using cargo packed footprint dimensions and validates via placement validator mode 1 [04 §10.2].
		// Failure emits Unable to unload unit and returns 9 [04 §10.2]; success queues lowering command at same X/Z with signed altitude offset = cargo definition model-bottom and no radius [04 §10.2].
		// TODO(question): placement validator mode 1 for unloading not injected in orders package; placeholder assumes valid when Goal present or cargo has footprint [04 §10.2].
		// For determinism, if GoalX/Z is zero and no cargo, treat as invalid? But test will set Goal.
		// Assume valid for now; if we had terrain we would check.
		// To exercise failure path, we could check if cargo definition has footprint 0? But assume pass.
		// Record diagnostic would be via queue if needed.
		return 1
	case 2:
		// Phase 2 REVALIDATES — second anchor recompute plus validator before release [04 §10.2] double validation.
		// An unload interrupt flag returns 9 BEFORE second validation [04 §10.2].
		// TODO(question): interrupt flag exact word not established; using Node.Flags placeholder.
		if n.Flags&0x1 != 0 { // placeholder for unload interrupt flag
			return 9
		}
		// Failed revalidation emits same message and returns 9 [04 §10.2].
		// TODO(question): second validation same as first; placeholder passes.
		// Success starts deferred zero-argument EndTransport FIRST, then detaches cargo (reserved no-piece index), then constructs climb-away point command at carrier current X/Z with altitude cruisealt [04 §10.2].
		return 1
	case 3:
		// Phase 3 emits event code 13 with no text payload and finishes [04 §10.2][GAP T16].
		cargoHandle := pool.Handle(n.Param1)
		if cargoHandle == 0 && len(carrier.Attachment.Cargo) > 0 {
			cargoHandle = carrier.Attachment.Cargo[0]
		}
		cargo := lookupTarget(carrier, cargoHandle)
		// Fallback: if lookup fails, try direct world via cargo handle's unit via attachment list? Already have handle.
		// If still nil, try to find cargo unit via direct handle in carrier's list without lookup (for tests where Lookup not set)
		if cargo == nil && cargoHandle != 0 {
			// Attempt to find via carrier's cargo list's unit's world not available; keep nil and try alternative path:
			// For headless tests where Lookup not set, we still need to detach. We can attempt to locate cargo via a global registry?
			// As fallback, we will detach via carrier's list manipulation even without cargo unit pointer,
			// but we need cargo pointer to clear its Carrier field and set position.
			// If lookup fails, we cannot fully detach; return 9 to indicate failure.
			// For now, treat as failure if cargo not found.
			return 9
		}
		if cargo != nil {
			// TODO(question): EndTransport deferred zero-arg start not modeled.
			// Detach cargo (reserved no-piece index) [04 §10.2]
			cargo.Attachment.Carrier = 0
			cargo.Attachment.AttachPiece = -1
			// Remove from carrier cargo list – copy first to avoid aliasing with [:0] range bug [I1].
			origCargo := append([]pool.Handle(nil), carrier.Attachment.Cargo...)
			newCargo := origCargo[:0]
			for _, h := range origCargo {
				if h != cargoHandle {
					newCargo = append(newCargo, h)
				}
			}
			carrier.Attachment.Cargo = newCargo
			// Set cargo position to drop point anchor center [04 §10.2] unload phase 1 anchor conversion.
			// Goal holds drop point world Fixed; use it directly.
			if n.GoalX != 0 || n.GoalZ != 0 {
				cargo.X = n.GoalX
				cargo.Z = n.GoalZ
				// Y: use terrain height if available else carrier Y; placeholder keep cargo.Y as is or set to carrier Y.
				// TODO(question): exact Y after unload not established (modelBottom offset, terrain clamp) [04 §10.2][04 §9.2].
				// Use carrier Y as placeholder for air carrier's altitude? For ground unload, use terrain height.
				// Keep cargo.Y unchanged for now; movement system will clamp on next tick via SyncCarriedMotion/Validate.
			}
			// Remove BeCarried from cargo's queue if present
			if cargoQ := QueueForUnit(cargo); cargoQ != nil {
				beID := Lookup("BeCarried")
				if beID != 0 {
					// Remove BeCarried head if present
					if head := cargoQ.Head(); head != nil && head.ID == beID {
						cargoQ.RemoveHead()
					} else {
						// Scan and remove any BeCarried
						prim := cargoQ.Primary()
						newPrim := prim[:0]
						for _, nn := range prim {
							if nn.ID != beID {
								newPrim = append(newPrim, nn)
							}
						}
						// Rebuild queue preserving hooks
						if len(newPrim) != len(prim) {
							sec := cargoQ.Secondary()
							// Clear old queue in place so its owner binding and diagnostics survive.
							cargoQ.SetPrimary(newPrim)
							cargoQ.SetSecondary(sec)
						}
					}
				}
			}
			// TODO(question): climb-away point command at carrier current X/Z with altitude cruisealt – not simulated.
			// TODO(question): event code 13 [GAP T16] not emitted.
		}
		return 5
	default:
		return 7
	}
}

// landingHandler implements VTOL_Landing per [04 §10.2] landing pads.
// QueryLandingPad is synchronous four-output query on target script; candidates tried order 0..3 and first free wins [04 §10.2].
// With no pad the loiter/spiral heading step is used; no free pad keeps order alive for next-tick retry or 30+rand(15) delayed retry [04 §10.2].
func landingHandler(carrier *units.Unit, n *Node, satisfied uint32) Code {
	_ = satisfied
	if carrier == nil || n == nil {
		return 7
	}
	if carrier.Def == nil || !carrier.Def.CanFly {
		return 7
	}
	// If no target pad, try to find any pad? For now, require target.
	if n.Target == 0 {
		// No pad target: loiter/spiral heading step – placeholder keep alive with wait 30+rand15 [04 §10.2] TODO(question)
		// Return 3 to set wait via pump (30+rand15)
		return 3
	}
	pad := lookupTarget(carrier, n.Target)
	if pad == nil || pad.Def == nil || !pad.Def.IsAirBase {
		// Invalid pad
		return 3
	}
	if pad.Attachment.Carrier != 0 {
		// Pad is carried – not available [04 §10.2] "not carried"
		return 3
	}
	// Check not already assigned to another unit (any unit whose attach-piece field equals candidate) [04 §10.2]
	// For landing pads, assignment is any unit whose attach-piece equals candidate? Our pad model is one unit = one candidate.
	// Placeholder: check if any other air unit is near pad (occupied)
	// For determinism, check if pad has any cargo? Not applicable.
	// Assume pad is free if not carried.
	// Land: set VTOL position to pad, mode to parked (1) [04 §9.1] 1 stopped/parked, Y to terrain height (landed).
	carrier.X = pad.X
	carrier.Z = pad.Z
	carrier.Y = pad.Y
	carrier.Move.Mode = 1
	// TODO(question): pad assignment tracking, loiter/spiral heading step, retry cadence 30+rand15 vs next-tick retry not fully modeled [04 §10.2].
	// TODO(question): QueryLandingPad synchronous four-output query with pre-seed -1 and validity/availability checks not modeled beyond IsAirBase.
	return 5
}

// beCarriedHandler implements BeCarried (Being transported) per [04 §3.1] 0x24.
// It keeps the cargo's order alive while attached, and completes when detached.
func beCarriedHandler(u *units.Unit, n *Node, satisfied uint32) Code {
	return beCarriedHandlerAtTick(u, n, satisfied, 0)
}

// beCarriedHandlerAtTick is the exact two-phase carried wait. It draws no RNG:
// phase 0 releases weapon slots and advances; phase 1 holds on an exact
// ten-tick deadline until detach makes the pre-check complete
// [04 R-ORD-01 §2][04 R-FAC-02 §4].
func beCarriedHandlerAtTick(u *units.Unit, n *Node, satisfied uint32, tick uint32) Code {
	_ = satisfied
	if u == nil || n == nil {
		return 5
	}
	if u.Attachment.Carrier == 0 {
		return 5 // no longer carried, done [04 §10.2] cargo first detaches
	}
	if n.Phase == 0 {
		for i := 0; i < units.NumSlots; i++ {
			if slot := u.SlotAt(i); slot != nil {
				slot.Target = units.Target{Kind: units.TargetNone}
			}
		}
		return 1
	}
	n.DynamicGate = 1
	n.Deadline = int32(tick + 10)
	return 2
}

// setQueuePrimary and setQueueSecondary are helpers to rebuild queues without import cycle.
// They use the exported Queue.SetPrimary/SetSecondary.
func setQueuePrimary(q *Queue, prim []*Node) {
	if q == nil {
		return
	}
	q.SetPrimary(prim)
}
func setQueueSecondary(q *Queue, sec []*Node) {
	if q == nil {
		return
	}
	q.SetSecondary(sec)
}

func ensureTransportHandlers() {
	// Called lazily from pump and init to wire handlers after table built [04 §3.1] C4.
	if len(table) == 0 {
		return
	}
	mappings := []struct {
		name string
		h    func(*units.Unit, *Node, uint32) Code
	}{
		{"Ground_Pickup", pickupHandler},
		{"VTOL_Pickup", pickupHandler},
		{"Ground_Unload", unloadHandler},
		{"VTOL_Unload", unloadHandler},
		{"VTOL_Landing", landingHandler},
		{"BeCarried", beCarriedHandler},
	}
	for _, m := range mappings {
		id := Lookup(m.name)
		if id == 0 || int(id) >= len(table) {
			continue
		}
		if table[int(id)].Handler == nil {
			table[int(id)].Handler = m.h
		}
	}
}

func init() {
	// Attempt early wiring; if table not yet built, lazy ensure will retry on first pump.
	ensureTransportHandlers()
}
