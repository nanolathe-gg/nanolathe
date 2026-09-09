// What a finished product inherits and what interrupts a build: the standing-
// flag copy of [04 R-FAC-02 §4] §3.8, the rally-record walk, and the cancel-
// current and stop interrupts of [05 "Cancel-current and stop interrupts"].
//
// Moved out of factory.go by CL-5, which split that file by concern; the code
// is unchanged.

package construction

import (
	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/movement"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// standingMergeAdmits is the double guard both standing-field merges share:
// the copy is allowed only when BOTH units carry the state word's alive bit
// (bit 28) and NEITHER carries bit 14, the death latch the kill service sets
// beside the cause byte and the completion transition sets for an `isfeature`
// product [04 §3.8][04 R-SPEC-01 §12].
//
// Retail keeps both bits in one status word; Nanolathe keeps each as its own
// named field, which is what I13 requires — the alive bit is units.Unit.Alive
// ("slot valid; cleared by the phase-2 finalizer" [04 §2.4]) and the death
// latch is units.Unit.Dying, the mark World.Destroy sets. Reading them off the
// instance flag word instead, as this guard used to, tested two literals no
// live code path ever sets: the allocator's initial status word carries
// neither, so the state-2 merge below was a silent no-op in every battle.
func standingMergeAdmits(builder, product *units.Unit) bool {
	if builder == nil || product == nil {
		return false
	}
	return builder.Alive && product.Alive && !builder.Dying && !product.Dying
}

// copyStandingFlags is the recovered initial standing-field merge guard. The
// class and auto exclusions are distinct from the later rally traversal
// [R-P0-09]. This is the state-2 epilogue's copy — the initial product-state
// merge — and is a distinct stage from the post-build gate in
// inheritStandingFields; the product's initial flags are not proof that
// `GetBuilt` has run [04 §3.8].
func copyStandingFlags(builder, product *units.Unit) {
	if !standingMergeAdmits(builder, product) {
		return
	}
	product.Flags = (product.Flags &^ (StandingMoveMask | StandingFireMask)) |
		(builder.Flags & (StandingMoveMask | StandingFireMask))
}

func (s *Service) copyStandingFlags(builder, product *units.Unit) {
	copyStandingFlags(builder, product)
}

// controlByteComputer is the player slot's control byte for a computer player:
// `1` is a locally controlled human, `2` a computer player, `3` a remote peer
// [05 R-SHARE-01 §1].
//
// [04 §3.8] parenthesises the experience-word gate as "owner player state byte
// value 1". That parenthetical is the same mislabel [04 §3.6]'s 2026-08-31
// correction retired for the idle-queue refill — it read the pair {1,2} as two
// computer-player states — and three Established traces disagree with it:
// [05 R-SHARE-01 §1] (skirmish setup writes 1 for the human seat and 2 for each
// computer seat), [05 R-ECO-01 §3] (the difficulty discount runs for control
// byte 2), and [04 R-SPEC-01 §5] ("the searching unit's owning player has
// controller type 2 (a computer player)"). The gate is control byte 2.
const controlByteComputer uint8 = 2

// ownerControlByte reads the owning player row's control byte through the
// economy ledger, which is where the session writes it [05 R-SHARE-01 §1]. A
// row this service cannot see reads as 0 — not a control-byte value, so it
// never satisfies the computer-player gate.
func (s *Service) ownerControlByte(owner uint8) uint8 {
	if s == nil || s.Economy == nil || int(owner) >= len(s.Economy.Players) {
		return 0
	}
	return s.Economy.Players[owner].ControllerState
}

// inheritStandingFields is `GetBuilt`'s post-build standing merge, the second
// of the two stages [04 §3.8] keeps distinct. Under the same alive/death-latch
// guard as the state-2 copy it moves standing-move bits 18-19 and standing-fire
// bits 20-21 from builder to product, and the experience word rides the same
// guarded block under one further gate — the OWNER's control byte reading as a
// computer player [04 §3.8][04 R-FAC-02 §4].
//
// `units.Unit.Kills` is the experience word: it is the field the capture timer's
// divide-by-five reads and the field the account record saves [05 "Unit
// capture"][08 R-SAVE-02 §6]. A product and its builder always share an owner,
// so the control byte is read once, off the builder.
func (s *Service) inheritStandingFields(builder, product *units.Unit) {
	if !standingMergeAdmits(builder, product) {
		return
	}
	product.Flags = (product.Flags &^ (StandingMoveMask | StandingFireMask)) |
		(builder.Flags & (StandingMoveMask | StandingFireMask))
	if s.ownerControlByte(builder.Owner) == controlByteComputer {
		product.Kills = builder.Kills
	}
}

// OnRefresh is the interface refresh callback, set by tests.

// ---------------------------------------------------------------------------
// C19 Rally inheritance [05 "Rally inheritance"].
// ---------------------------------------------------------------------------

// rallyInheritance is `GetBuilt`'s completion arm: it walks the builder's
// primary queue and inserts the resolved rally records — or `Park` when there
// were none — QUEUED ON THE PRODUCT [04 R-FAC-02 §4]. Every record it makes is
// therefore the product's own, and is stamped through productRecord; tick is
// the GetBuilt visit's, which is when these records come into being
// [04 §3.2].
func (s *Service) rallyInheritance(factory *units.Unit, product *units.Unit, tick uint32) {
	if factory == nil || product == nil {
		return
	}
	fq := s.queueForUnit(factory)
	if fq == nil {
		// No queue => nothing to inherit, so the standing merge still runs and
		// then Park. Retail's builder always has a queue object; an empty walk
		// and a missing one reach the same two steps [04 R-FAC-02 §4].
		s.inheritStandingFields(factory, product)
		parkID := orders.Lookup("Park")
		if parkID != 0 {
			pq := orders.BindQueueBinding(product, s.OrderBinding)
			// Inserted QUEUED, like every record this arm makes
			// [04 R-FAC-02 §4] — so the product does not speak an
			// acknowledgement for an order it was never given [04 R-ORD-01 §13].
			pq.Push(parkID, productRecord(product, tick, orders.Node{QueuedIssue: true}))
		}
		return
	}
	prim := fq.Primary()
	qMoveID := orders.Lookup("QMove")
	qPatrolID := orders.Lookup("QPatrol")
	parkID := orders.Lookup("Park")
	// [04 R-FAC-02 §4]: "a record whose name is `QMove` is RESOLVED AS COMMAND
	// 2 (move) and one named `QPatrol` as command 9 (patrol) AGAINST THE
	// PRODUCT with the record's goal triple". The descriptor a rally record
	// becomes is therefore the product's own resolution, not a constant.
	//
	// Corrected 2026-09-02 (WU-19-107, playtest report 4: "planes are not
	// moving off the factory properly and are piling up, making it impossible
	// to build more until manually moving them"). This walk used to hard-code
	// `Move_Ground` and `Patrol`, so an aircraft product of a factory carrying
	// a rally point received the GROUND move handler. Nothing recovers from
	// that: an aircraft is never admitted to the ground path scheduler
	// ([04 R-PATH-01 §9]), so no movement bit ever satisfies the record's
	// `0xE0` gate and it stalls at the head forever; and because the record is
	// not an air one it never runs the takeoff preamble, so the product's mover
	// mode stays 1 and its stamp stays on the GROUND plane — which is exactly
	// the occupancy the next product's state-2 test and the yard-close
	// admission gate wait on ([04 R-AIR-02] step 3, [04 R-FAC-02 §5],
	// [04 R-FAC-02 §6]). The plant stops after its first product until the
	// player moves the aircraft by hand, which issues the same command 2 and
	// resolves the air executor the rally should have.
	//
	// Codes 2 and 9 read no position and no target ([04 R-ORD-02 §1]), so the
	// goal triple rides on the record rather than through the resolver.
	moveID := orders.Resolve(2, product, nil, nil)
	patrolID := orders.Resolve(9, product, nil, nil)

	inherited := 0
	pq := orders.BindQueueBinding(product, s.OrderBinding)
	// Collect rally nodes in traversal order first, then tail-append to preserve order [05 C19].
	// Tail-appending (rather than pq.Push) also leaves an existing GetBuilt
	// head and its active marker untouched.
	var toAppend []*orders.Node
	for _, n := range prim {
		if n == nil {
			continue
		}
		if n.ID == qMoveID {
			if moveID != 0 {
				rec := productRecord(product, tick, orders.Node{ID: moveID, GoalX: n.GoalX, GoalY: n.GoalY, GoalZ: n.GoalZ, DynamicGate: 0, Deadline: -1, StaticGate: orders.DescriptorFor(moveID).StaticGate, Flags: 0})
				nn := &rec
				// Ensure deadline -1 for new node [04 §3.2]
				if nn.Deadline == 0 {
					nn.Deadline = -1
				}
				toAppend = append(toAppend, nn)
				inherited++
			}
		} else if n.ID == qPatrolID {
			if patrolID != 0 {
				rec := productRecord(product, tick, orders.Node{ID: patrolID, GoalX: n.GoalX, GoalY: n.GoalY, GoalZ: n.GoalZ, DynamicGate: 0, Deadline: -1, StaticGate: orders.DescriptorFor(patrolID).StaticGate})
				nn := &rec
				if nn.Deadline == 0 {
					nn.Deadline = -1
				}
				toAppend = append(toAppend, nn)
				inherited++
			}
		}
	}
	// [04 R-FAC-02 §4] fixes the order inside the completion arm: the resolved
	// rally records are walked and inserted first, THEN the standing-bit copy
	// under the §3.8 guard (with the experience word for a computer-owned
	// builder), THEN `Park` if nothing was inserted.
	s.inheritStandingFields(factory, product)
	if inherited == 0 {
		if parkID != 0 {
			rec := productRecord(product, tick, orders.Node{ID: parkID, Deadline: -1, StaticGate: orders.DescriptorFor(parkID).StaticGate})
			nn := &rec
			if nn.Deadline == 0 {
				nn.Deadline = -1
			}
			toAppend = append(toAppend, nn)
		}
	}
	if len(toAppend) > 0 {
		// Tail-append to primary, preserving traversal order [05 C19][I1], and
		// write no active marker.
		//
		// The block that used to close this arm cleared bit 12 off every record
		// and put it on index 0. Bit 12 has exactly one writer, the producer
		// insertion's after-marker branch [04 R-ORD-01 §13], and a factory
		// product's queue starts with neither of its two records carrying it
		// ([04 R-ORD-01 §15]: the pair is `[GetBuilt, BeCarried]`, both
		// head-inserted, "with neither record carrying the active marker"). So
		// the marker this invented on `GetBuilt` moved the insertion point of
		// the product's next player-issued order to index 1 — in front of its
		// own rally orders — where [04 §3.1] appends at the tail.
		pq.SetPrimary(append(pq.Primary(), toAppend...))
	}
}

// ---------------------------------------------------------------------------
// C21 Cancel-current interrupt [05 "Cancel-current and stop interrupts"].
// ---------------------------------------------------------------------------

func (s *Service) handleCancelCurrent(factory *units.Unit, node *orders.Node, tick uint32) {
	// Compute refund trunc((1 - remaining) * metalBuildCost) [05 C21].
	var remaining float32 = 1 // default if no product
	var metalCost float32
	var product *units.Unit
	if node.Target != 0 && s.World != nil {
		// Try to resolve product via world.
		product = s.World.Unit(node.Target)
		if product != nil && product.Def != nil {
			remaining = product.Remaining
			metalCost = product.Def.BuildCostMetal
		}
	}
	// If no product attached, same epilogue runs with remaining=1 => refund 0 [05 C21].
	if product == nil {
		// Try to get metalCost from product def via node Param1
		if def := s.getProductDefForNode(node); def != nil {
			metalCost = def.BuildCostMetal
		}
	}
	refund := float32(numeric.TruncateFloat32ToLow32((1 - remaining) * metalCost)) // trunc toward zero [01 §8] I3

	// Normally add to builder's metal bucket UNLESS special second state [05 C21].
	// Apply via economy mirror bucket Production.
	if s.Economy != nil {
		pIdx := int(factory.Owner)
		if pIdx >= 0 && pIdx < len(s.Economy.Players) {
			player := &s.Economy.Players[pIdx]
			isSpecial := false
			if s.IsSpecialSecondState != nil {
				isSpecial = s.IsSpecialSecondState(factory.Owner)
			}
			if isSpecial {
				// The computer player's difficulty scaling. This site is one of
				// the fourteen members of that family, and every one of them
				// pairs the constants the same way: selector 0 credits a HALF,
				// selector 1 seven tenths, any other selector the whole amount
				// [05 R-ECO-01 §3][05 R-ECO-01 §11].
				//
				// Correction (PT3-05 follow-up). This arm used to read
				// `case 0: += refund * -0.7` and `case 1: += refund * -0.5`,
				// under a comment stating the pairing was inverted relative to
				// the ledger's negative-`energyuse` site and instructing that it
				// must not be harmonized. Both halves were wrong, and the
				// executable settles both: the pairing is uniform across all
				// fourteen sites, this one included, and the scaled arm is a
				// REDUCED CREDIT rather than a debit — the site forms
				// `accumulator - refund * (-0.5)`, which ADDS half the refund.
				// The old arm subtracted seven tenths of it, so cancelling a
				// build CHARGED a computer player metal where retail pays it
				// back at a discount, and charged it the wrong fraction. The
				// "do not harmonize" instruction is retired with the reading it
				// defended.
				//
				// The fraction is applied to the float32 refund rather than
				// through internal/economy's single-narrowing helper: retail
				// forms the product and the subtraction at working precision and
				// narrows once [05 R-ECO-01 §3], where this rounds the product
				// first. The refund is an integer-valued float32 (truncated
				// above), so the two agree at every stock magnitude; the
				// residual is the same class as the one locked in
				// internal/economy's reclaim-credit tests, and closing it means
				// moving this site onto that helper.
				switch s.ModeSelector {
				case 0:
					player.Mirror[economy.Metal].Production += refund * 0.5 // credit one half [05 R-ECO-01 §11]
				case 1:
					player.Mirror[economy.Metal].Production += refund * 0.7 // credit seven tenths [05 R-ECO-01 §11]
				default:
					player.Mirror[economy.Metal].Production += refund
				}
			} else {
				player.Mirror[economy.Metal].Production += refund
			}
		}
	}

	// Step 3, after the refund and before the kill: the SAME completion
	// transition every other completion runs — not a bare `remaining = 0`
	// [05 "Cancel-current and stop interrupts"][04 R-FAC-02 §3]. The builder is
	// the factory, so a product with `activatewhenbuilt` receives its `Activate`
	// edge here and dies in step 4 of the same call, and the order-panel refresh
	// the transition owes keys on the BUILDER's identity — this arm's own
	// `OnRefresh(factory)` below, never the product's [04 R-SPEC-01 §12].
	// The queued count is intentionally untouched [R-P0-09][05 C21].
	if product != nil {
		s.applyCompletionPostureWithRetirement(product, false)
	}

	// Send the ordinary fixed-nominal kind-9 packet. Armor applies only below
	// 30000, while defender veterancy still scales this packet [05 C21][06 §9.2].
	// A cause-9 result that is accepted and lethal skips the killed-severity
	// query (severity zero, no explosion, no corpse) [04 §5.1][05 C21].
	s.lastKill = KillInfo{Damage: Kind9Damage, Severity: 0, NoCorpse: true} // severity zero [05 C21]
	if product != nil {
		// Cancel-current carries the factory as the raw attacker. The common
		// receiver owns health, provenance, reaction, and delayed death marking
		// [05 "Cancel-current and stop interrupts"][06 §9.1][06 §9.2].
		if s.World != nil {
			s.Combat.AcceptDamage(s.World, tick, combat.DamageInput{
				Victim: product.Handle, Attacker: factory.Handle, Nominal: Kind9Damage,
				Kind: Kind9Cause,
			})
		}
		// Completion posture intentionally precedes the packet. If common intake
		// latches this local victim Dying, phase 2 later owns OnDeath and the pool
		// free; defender-veteran survivors and remote-controller results do not
		// acquire that latch [01 §4.4][06 §9.2].
		s.ReleasePlacement(product.Handle)
		// Deterministically clear builder/product link after nanoframe (ON-02):
		// before nanoframe builderLinks not yet set, so no-op; after nanoframe it must be cleared
		// even on cancel, not leaked as on normal death path [P0-14]. Ensures stop/cancel cleanup deterministic.
		if s.builderLinks != nil {
			delete(s.builderLinks, product.Handle)
		}
		// Also clear any reverse mapping? product -> builder only, so delete above suffices.
		// Ensure product's own builder link cleared on cancel (before and after nanoframe unified) [05 C21].
	}

	// Lower Deactivate and StartBuilding together. Each bridge operation is
	// edge-deduplicated, preserving the single falling-edge callback contract.
	s.deactivate(factory)
	s.stopBuilding(factory)

	// Refresh interface [05 C21].
	if s != nil && s.OnRefresh != nil {
		s.OnRefresh(factory)
	}

	// Release the record's target reference and its dynamic gate before the
	// removal. The reference release is the "releases the reference at
	// completion or cancel" half of the target-removed binding
	// [05 "Build request and factory queue behavior"], and dropping gate bit 1
	// is what stops the removal below from re-delivering the cancel notice of
	// [04 R-ORDER-02 §2] into this same body: the guard is "the dynamic gate
	// still holds bit 1 AT REMOVAL", and by then this record is no longer
	// waiting on it.
	node.Target = 0
	node.DynamicGate = 0

	// Drop node WITHOUT decrementing remaining count [05 C21].
	// Remove head from primary queue without touching Param2.
	s.removeHead(factory, node)
}

func (s *Service) applyCompletionPosture(product *units.Unit) {
	s.applyCompletionPostureWithRetirement(product, true)
}

// applyCompletionPostureWithRetirement keeps cancellation's documented
// completion-then-kill ordering. Normal completion retires only construction
// bookkeeping; cancel retains it through the damage packet so that packet's
// teardown can release the frame stamp in its ordinary position [05 C21].
func (s *Service) applyCompletionPostureWithRetirement(product *units.Unit, retire bool) {
	if product == nil {
		return
	}
	product.Remaining = 0
	product.Flags |= FlagCompleted
	// Record the completion for StepUnit's caller; the transition is idempotent
	// and the second invocation names the same product [04 R-FAC-02 §3].
	s.completedInPump = product.Handle
	// Completed units become eligible for AI classification (group 4 construction) [R-P0-04][08].
	// Nanoframes are created with Flags without 0x20 (initializeNanoframe clears it); completion must restore it.
	product.Flags |= units.ClassifierEligibleStatus
	// Completion detaches through the shared cargo commit, with request mode 1:
	// "its mover mode is set to 1" [04 R-FAC-02 §3]. Detach is idempotent and
	// performs no position write or re-stamp. The mode-less form that stood here
	// left the product on whatever mode the builder link had written, which is
	// the same value today but is not the contract.
	if product.Def != nil && product.Def.BMCode != 0 && product.Attachment.Carrier != 0 {
		movement.DetachFactoryProduct(s.World, product.Handle)
	}
	if product.Def != nil && product.Def.ActivateWhenBuilt {
		s.activate(product)
	}
	// Capability bit 24 is `isfeature`, and its completion arm marks the product
	// a feature stand-in: a DIRECT store of death-cause byte 7 plus the death
	// latch, not a damage packet [04 R-SPEC-01 §12][06 §12.1]. It runs here,
	// immediately after the `activatewhenbuilt` edge, which is where §12 places
	// it in the completion order.
	//
	// Correction (RWU-19-26): this arm read `init_cloaked` and wrote
	// `FlagInitCloak` plus the cloak-requested bit, on doc 04 §3.8's earlier
	// mislabel of bit 24 as "the cloak/initial-posture handling". Bit 24 is
	// `isfeature`; §3.8 is corrected in place. The completion transition never
	// reads `init_cloaked` and never writes either cloak bit — `init_cloaked` is
	// consumed once, by the unit constructor, which seeds the cloak-requested
	// bit [05 R-ECO-01 §9][03 R-VIS-01 §6][units.Unit.InitEconomyState].
	//
	// Cause 7's credit branch is none and its attacker is not a packet field, so
	// nothing writes LastDamageSide here; the finalizer's credit path does not
	// read it for this cause [06 §12.1]. Severity is forced zero and the corpse
	// nibble forced one, so the product becomes its authored Corpse.
	if product.Def != nil && product.Def.IsFeature && s.World != nil {
		product.LastDamageCause = uint8(combat.CauseFeatureConversion)
		s.World.Destroy(product.Handle, units.DeathKilled) // direct latch, null attacker [06 §12.1]
	}
	// sharedStep already stored the capped difference-of-truncations health
	// result. Completion changes construction state only; assigning MaxHealth
	// here would erase the final-step arithmetic [05 R-WORK-01 §1].
	// Both mobile and building products retain their live stamps. Ordinary
	// movement or an aircraft's mode transition owns a mobile pad release
	// [04 R-FAC-02 §6][04 R-COLL-01 §4].
	if retire {
		s.retirePlacement(product.Handle)
	}
}

// removeHead removes the head node from factory's primary queue without
// decrement [05 C21]. Removal happens in place through the queue's own
// subtraction path so queue identity and every queue-owned service binding
// (the concrete binding and diagnostics) survive.
// The previous implementation rebuilt the segment into a fresh orders.Queue
// and rebound it, which dropped those hooks: after the first factory product
// completed, successor target orders lost target lookup and hostility, and
// secondary stockpile admission no longer saw the economy buckets.
//
// The tombstone bit is set on every freed record EXCEPT one that is the
// primary segment's front head at that moment, so "the front record of the
// primary queue is never tombstoned" [04 R-ORDER-02 §2]. Every removal this
// helper performs is a removal of that head — cancel-current's drop and
// `MobileBuild`'s phase-4 completion both act on the record the pump is
// dispatching — so the flag is false and RemovePrimaryNode's own index test
// keeps the exemption honest if a caller ever hands it a rear record.
func (s *Service) removeHead(factory *units.Unit, node *orders.Node) {
	q := s.queueForUnit(factory)
	if q == nil {
		return
	}
	q.RemovePrimaryNode(node, false)
}

// ---------------------------------------------------------------------------
// C22 Stop interrupt [05 "Cancel-current and stop interrupts"].
// ---------------------------------------------------------------------------

func (s *Service) handleStop(factory *units.Unit, node *orders.Node, tick uint32) {
	s.logMessage("Construction stopped") // verbatim [05 C22]
	// `BuildingBuild`'s satisfied bit 3 arm: "status 7 `Construction stopped`,
	// p2 -= 1, refresh, restart" [04 R-ORD-01 §5].
	s.raiseStatus(factory, statusCant, "Construction stopped")
	// Decrement node count ONCE [05 C22].
	if node.Param2 > 0 {
		node.Param2--
	} else {
		// If Param2 is 0 (queued mode zero count case for GetBuilt? but for factory build nodes, count at least 1), still decrement? For stop, treat as decrement once even if zero => stay 0.
		// Spec says decrement once, node survives.
		if node.Param2 == 0 {
			// keep 0? But spec says count is remaining build count, so decrement from 1 to 0 would be 0 but node survives and restarts.
			// We leave at 0.
		}
	}
	// Refresh interface [05 C22].
	if s != nil && s.OnRefresh != nil {
		s.OnRefresh(factory)
	}
	// Returns result 0 — node SURVIVES, machine restarts [05 C22].
	node.Phase = uint8(State0)
	node.DynamicGate = 0
	node.Deadline = -1
	// Do not remove node.
}

// ---------------------------------------------------------------------------
// State gates [05 "Factory production lifecycle"].
// ---------------------------------------------------------------------------
