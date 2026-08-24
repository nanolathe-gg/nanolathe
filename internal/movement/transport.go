// Package movement — transports [04 §10.2] C31.
//
// TransportState is the explicit load/unload-executor surface that retail
// scatters across the order and unit records. The orchestrator will unify this
// with units.Unit / orders.Order once those types grow transport fields. Retail
// offsets are noted where established so the unification is mechanical.
//
// Mapping to retail [04 §10.2] (I13: offsets are identity, not layout):
//
//	Phase                  order handler-private phase byte [04 §3.2][04 §10.2] 86-byte record
//	TargetPresent          order target smart-reference non-null at +? [04 §3.2][04 §10.2] gate 1
//	ExecutorFlags          executor flags word checked against 0x10048 [04 §10.2] gate 2
//	                       TODO(question): exact offset of this flags word not established; word is on the executor/unit state.
// TODO(question): Historical analysis omitted; independently worded behavior is needed.
//	TargetModelTop         definition model-top 32-bit field [04 §10.2] gate 3 BeginTransport arg; exact def offset unknown
//	SeaLevel               map sea-level byte shifted <<16 into 16.16 [04 §10.2] gate 3
//	CargoListEmpty         carrier cargo-list head null [04 §10.2] gate 4; TODO(question): list head offset not established, modeled as bool
//	IsAirCarrier           canfly distinction for cargo-empty gate [04 §10.2] gate 4; true==air, false==ground
//	TargetFootprintX       target cached FootPrintX WORD signed [04 §10.2] phase-0 size gate
//	CarrierTransportSize   carrier definition transportsize BYTE zero-extended [02 "Unit record"][04 §10.2] phase-0 heavy gate
//	CarrierHasLiveMover    carrier mover liveness [04 §10.2] phase 0 else 7
//	CarrierCanFly          carrier definition canfly flag [02 "Unit record"][04 §10.2] phase 0
//	InterruptFlags         flags word checked with 0x42 at phase-4 interruption [04 §10.2] phase 4
//	AttachPiece            QueryTransport output 0 retained as attach piece [04 §5.3][04 §10.2] phase 2–4
//	CruiseAlt              carrier definition cruisealt integer [02 "Unit record"][04 §10.2] point-command alt/2
//	                       TODO(question): exact cruisealt/2 rounding already established as signed trunc toward zero [04 §10.2] phase 0, not simulated beyond phase advance.
//
// UnloadState mapping [04 §10.2] unload executor:
//
//	Phase                  same order phase byte
//	CargoListEmpty         immediate empty check before dispatch [04 §10.2] unload
//	CarrierHasLiveMover / CarrierCanFly  same as load phase 0 else 7 [04 §10.2] unload phase 0
//	PlacementValidFirst/Second  placement validator mode 1 results [04 §10.2] unload phases 1–2; TODO(question): validator injected, not implemented here
//	Interrupt              unload interrupt flag before second validation [04 §10.2] unload phase 2
package movement

// Result codes are the order-pump return codes [04 §3.3].
// Only those observed for the transport executors are named; the pump
// table itself owns the full set.
const (
	TransportResultContinue = 1 // advance phase by one and continue walking [04 §3.3][04 §10.2]
	TransportResultDone     = 5 // no work at phase 5 load, empty unload immediate done [04 §3.3][04 §10.2]
	TransportResultBlocked  = 7 // no live mover else 7, other phase else 7 [04 §10.2]
	TransportResultFailed   = 8 // transport mission failed / too heavy / interrupted [04 §10.2]
	TransportResultRetry    = 9 // unable to unload unit / interrupt before revalidation [04 §10.2] unload
)

// Event codes per [GAP T16][04 §10.2].
const (
	TransportEventNone   = 0
	TransportEventAttach = 12 // successful load attach at phase 4 [04 §10.2] load phase table, [GAP T16]
	TransportEventDetach = 13 // successful unload detach at phase 3 [04 §10.2] unload, [GAP T16]
)

// Entry-gate masks [04 §10.2].
const (
	transportMaskEntryGate     = 0x10048 // executor flags word must hold none of 0x10048 [04 §10.2] gate 2
	transportMaskInterruptGate = 0x42    // phase-4 interrupt flags & 0x42 [04 §10.2] phase 4 interrupted
)

// Verbatim diagnostics [04 §10.2].
const (
	TransportFailedMessage = "Transport mission failed"       // gates 1–3 share this terminal with code 8 [04 §10.2]
	HeavyTransportMessage  = "Unit is too heavy to transport" // size gate at phase 0 code 8 verbatim [04 §10.2]
	UnableUnloadMessage    = "Unable to unload unit"          // unload placement failure code 9 [04 §10.2]
)

// TransportState holds the mutable load-executor state. See package comment
// for retail offset mapping. All fixed values are raw 16.16 int32 words unless
// noted. Handles are abstracted as booleans/counters so this package does not
// depend on pool handle layout; the future unification will replace them with
// pool.Handle fields at the documented offsets (I13).
type TransportState struct {
	Phase uint8 // order handler-private phase byte [04 §3.2][04 §10.2]

	// Gate inputs — checked at entry to every load phase [04 §10.2] C31.
	TargetPresent        bool   // order target reference non-null [04 §10.2] gate 1
	ExecutorFlags        uint32 // executor flags word [04 §10.2] gate 2 mask 0x10048
	TargetY              int32  // 16.16 world Y [04 §10.2] gate 3
	TargetModelTop       int32  // definition model-top 32-bit [04 §10.2] gate 3 BeginTransport arg
	SeaLevel             int32  // sea level byte <<16 into 16.16 [04 §10.2] gate 3
	CargoListEmpty       bool   // carrier cargo-list head null [04 §10.2] gate 4
	IsAirCarrier         bool   // air vs ground carrier distinction for gate 4 [04 §10.2] C31
	CarrierTransportSize uint8  // BYTE zero-extended [02 "Unit record"][04 §10.2] phase 0
	TargetFootprintX     int16  // WORD signed [04 §10.2] phase 0 size gate

	// Phase-0 mover liveness gate [04 §10.2].
	CarrierHasLiveMover bool // live carrier mover [04 §10.2] phase 0
	CarrierCanFly       bool // canfly [02 "Unit record"][04 §10.2] phase 0

	// Phase-4 interruption [04 §10.2].
	InterruptFlags uint32 // checked with 0x42 at phase 4 [04 §10.2] interrupted

	// Attachment piece selected by QueryTransport at phase 2 [04 §5.3][04 §10.2].
	AttachPiece      int32 // output 0 retained [04 §10.2] phase 2–4
	AttachPieceValid bool  // whether AttachPiece holds a meaningful value; if false phase 2 will seed -1 root fallback [04 §5.3]

	// CruiseAlt for point-command construction [02 "Unit record"][04 §10.2] phases 0,1,3,4.
	// TODO(question): climb-away / approach point-command queuing not simulated beyond phase advance; retail queues point commands with altitude cruisealt/2 or cruisealt and radii 0x30 etc [04 §10.2].
	CarrierCruiseAlt int32 // integer cruisealt [02 "Unit record"]
}

// checkEntryGates implements the four re-checks run at entry to every load
// phase [04 §10.2] C31 and [GAP T16]. Returns failed==true with result 8 and
// the appropriate verbatim diagnostic; gate 4 returns code 8 with NO message
// [04 §10.2]. Order of checks is the retail order [04 §10.2].
func (s *TransportState) checkEntryGates() (failed bool, result int, diagnostic string) {
	if s == nil {
		return true, TransportResultFailed, TransportFailedMessage // nil state cannot have target
	}
	// Gate 1: order target reference must be non-null [04 §10.2].
	if !s.TargetPresent {
		return true, TransportResultFailed, TransportFailedMessage // [04 §10.2] gates 1–2 share Transport mission failed with code 8
	}
	// Gate 2: executor flags word must hold none of mask 0x10048 [04 §10.2] C31.
	if s.ExecutorFlags&transportMaskEntryGate != 0 {
		return true, TransportResultFailed, TransportFailedMessage // [04 §10.2] code 8
	}
	// Gate 3: target Y + modelTop SIGNED greater than seaLevel<<16 [04 §10.2] C31.
	// Both operands are 32-bit; compare signed. SeaLevel is already shifted.
	// ModelTop is the definition's 32-bit model-top field mirrored as BeginTransport arg [04 §10.2].
	sum := s.TargetY + s.TargetModelTop // 32-bit signed add, wraps as int32 in retail [04 §10.2]
	if sum <= s.SeaLevel {
		return true, TransportResultFailed, TransportFailedMessage // [04 §10.2] gate 3 emits same message directly also code 8
	}
	// Gate 4: AIR carrier cargo list must be EMPTY; ground carriers don't apply [04 §10.2] C31.
	if s.IsAirCarrier && !s.CargoListEmpty {
		return true, TransportResultFailed, "" // [04 §10.2] gate 4 returns code 8 with NO message
	}
	return false, 0, ""
}

// StepLoad advances the canonical load executor one phase tick per [04 §10.2]
// load phase table. It first re-checks the four entry gates at every phase
// [04 §10.2] C31, then dispatches on Phase. The returned result is the order-
// pump code [04 §3.3]; diagnostic is a verbatim retail string when the spec
// quotes one; event is 12 on the established attach transition and 0 otherwise
// [GAP T16][04 §10.2]. Phase is mutated on success (result 1 advances to next
// phase) and left unchanged on failure except where the table specifies the
// transition.
func (s *TransportState) StepLoad() (result int, diagnostic string, event int) {
	if s == nil {
		return TransportResultFailed, TransportFailedMessage, TransportEventNone
	}
	// Every phase re-checks the four gates before doing work [04 §10.2] C31.
	if failed, r, msg := s.checkEntryGates(); failed {
		return r, msg, TransportEventNone
	}
	switch s.Phase {
	case 0:
		// Phase 0: require live carrier mover and canfly else 7 [04 §10.2].
		if !s.CarrierHasLiveMover || !s.CarrierCanFly {
			return TransportResultBlocked, "", TransportEventNone // [04 §10.2] else 7
		}
		// Size gate: target cached FootPrintX WORD signed must be <= carrier transportsize BYTE zero-extended [04 §10.2].
		// Otherwise emit Unit is too heavy to transport verbatim and return 8 [04 §10.2].
		if int32(s.TargetFootprintX) > int32(s.CarrierTransportSize) {
			return TransportResultFailed, HeavyTransportMessage, TransportEventNone // verbatim [04 §10.2]
		}
		// Success side effects per table [04 §10.2]:
		// Set status Loading; notify carrier state 3; detach carrier from its own parent when carried;
		// raise Activate; force mover mode 2 from mode 1; queue point command at carrier X/Z with
		// altitude cruisealt/2 signed round toward zero and NO arrival radius; status bits |=0xE0.
		// TODO(question): point-command queuing, mover mode forcing, status bits and notification are established but not simulated in this isolated executor; they are visible only through integration with movement/occupancy/cob and are left to the future unification with units.Unit. Phase advance captures the contract.
		s.Phase = 1
		return TransportResultContinue, "Loading", TransportEventNone // [04 §10.2] phase 0 result 1
	case 1:
		// Phase 1: queue follow command toward target with full cruisealt altitude offset and horizontal arrival radius 0x30; status =0x100E8 [04 §10.2].
		// TODO(question): follow-command altitude and radius exact queuing not simulated beyond phase advance [04 §10.2].
		s.Phase = 2
		return TransportResultContinue, "", TransportEventNone // [04 §10.2] phase 1 result 1
	case 2:
		// Phase 2: status Preparing for transport; pre-seed first QueryTransport output to -1 and run synchronous four-output query [04 §5.3][04 §10.2].
		// Observed seed [-1,0,0,0]; missing script leaves -1 root-piece fallback [04 §10.2]; retain output 0 as attach piece; status =0x100E8 [04 §10.2].
		if !s.AttachPieceValid {
			s.AttachPiece = -1 // root-piece fallback [04 §5.3][04 §10.2]
			s.AttachPieceValid = true
		}
		s.Phase = 3
		return TransportResultContinue, "Preparing for transport", TransportEventNone // [04 §10.2] phase 2 result 1
	case 3:
		// Phase 3: start asynchronous one-argument BeginTransport with exact 32-bit target-definition model-top value mirrored through network forwarder [04 §10.2];
		// fetch selected piece world transform; construct cargo follow order with altitude offset = NEGATED integer part of that piece's world Y [04 §10.2];
		// status =0x100EA [04 §10.2].
		// TODO(question): BeginTransport async callback and cargo follow-order hang height (negated piece Y) are established but the piece world transform fetch and network mirroring are not simulated in this isolated executor [04 §10.2].
		s.Phase = 4
		return TransportResultContinue, "", TransportEventNone // [04 §10.2] phase 3 result 1
	case 4:
		// Phase 4 has two edges [04 §10.2]:
		// Interrupted: flags &0x42 present => start deferred zero-argument EndTransport and return WITHOUT attaching => code 8 [04 §10.2].
		if s.InterruptFlags&transportMaskInterruptGate != 0 {
			// TODO(question): deferred EndTransport zero-arg start exact timing/callback identity not modeled beyond result; successful load callback order is QueryTransport→BeginTransport→attachment→event 12 and no successful load runs EndTransport [04 §10.2]; interrupted edge runs EndTransport instead.
			return TransportResultFailed, "", TransportEventNone // [04 §10.2] phase-4 interrupted return 8
		}
		// Success: attach target to carrier on queried piece; emit event code 12 [GAP T16][04 §10.2]; queue climb-away point command at carrier current X/Z with altitude cruisealt, no radius; status |=0xE0 [04 §10.2].
		// TODO(question): attachment piece binding and climb-away queuing are established but occupancy/cargo-list mutation is left to units.World integration; event emission captures the contract.
		s.Phase = 5
		return TransportResultContinue, "", TransportEventAttach // [04 §10.2] phase 4 success result 1 with event 12 [GAP T16]
	case 5:
		// Phase 5: no work result 5 [04 §10.2].
		return TransportResultDone, "", TransportEventNone // [04 §10.2] phase 5 result 5
	default:
		// Other: no work result 7 [04 §10.2].
		return TransportResultBlocked, "", TransportEventNone // [04 §10.2] other result 7
	}
}

// TransportUnloadState holds the mutable unload-executor state [04 §10.2]
// unload dispatch. See package comment for mapping; retail offsets for the
// placement validator are injected.
type TransportUnloadState struct {
	Phase uint8 // order phase byte [04 §3.2][04 §10.2] unload

	// Immediate empty check — cargo list already empty returns done 5 [04 §10.2].
	CargoListEmpty bool // carrier cargo-list head null [04 §10.2] unload entry

	// Phase-0 mover gate [04 §10.2] unload.
	CarrierHasLiveMover bool // live canfly carrier mover [04 §10.2] unload phase 0
	CarrierCanFly       bool // canfly flag [02 "Unit record"][04 §10.2]

	// Placement validator results [04 §10.2] unload phases 1–2.
	// The validator is the standard placement validator in mode 1 [04 §10.2] converting drop point to footprint anchor using cargo packed footprint dimensions.
	// Injecting the bool lets callers wire the real validator without this package importing world/placement.
	PlacementValidFirst  bool // phase 1 validator before lowering command [04 §10.2]
	PlacementValidSecond bool // phase 2 revalidation before detach [04 §10.2] double validation

	// Interrupt flag checked BEFORE second validation at phase 2 [04 §10.2].
	Interrupt bool // unload interrupt flag at phase 2 returns 9 before second validation [04 §10.2]

	// TODO(question): drop point X/Z, cargo definition footprint, model-bottom offset, carrier cruisealt for point-command altitudes etc are established but not modeled; double validation anchor recompute is left as injected bools above [04 §10.2].
}

// StepUnload advances the canonical unload executor one phase tick per
// [04 §10.2] unload dispatch. Returns the order-pump result [04 §3.3],
// verbatim diagnostic when the spec quotes one, and event 13 on the
// established detach transition [GAP T16][04 §10.2]. Cargo-list empty is
// checked before phase dispatch and returns done 5 immediately [04 §10.2].
func (s *TransportUnloadState) StepUnload() (result int, diagnostic string, event int) {
	if s == nil {
		return TransportResultFailed, "", TransportEventNone
	}
	// Immediate done when cargo list already empty [04 §10.2] unload.
	if s.CargoListEmpty {
		return TransportResultDone, "", TransportEventNone // [04 §10.2] immediate done 5
	}
	switch s.Phase {
	case 0:
		// Phase 0 requires live canfly carrier mover else 7 [04 §10.2] unload.
		if !s.CarrierHasLiveMover || !s.CarrierCanFly {
			return TransportResultBlocked, "", TransportEventNone // [04 §10.2] else 7
		}
		// Success announces Unloading, records cargo reference, queues point command toward stored drop point with altitude cruisealt and radius 0x140 [04 §10.2].
		// TODO(question): drop-point storage and point-command altitude/radius exact queuing not simulated beyond phase advance [04 §10.2].
		s.Phase = 1
		return TransportResultContinue, "Unloading", TransportEventNone // [04 §10.2] unload phase 0 result inferred 1; diagnostic verbatim not quoted but status "Unloading" is established text [04 §10.2]
	case 1:
		// Phase 1 converts drop point to footprint anchor using cargo packed footprint dimensions and validates via placement validator mode 1 [04 §10.2].
		// Failure emits Unable to unload unit and returns 9 [04 §10.2]; success queues lowering command at same X/Z with signed altitude offset = cargo definition model-bottom and no radius [04 §10.2].
		if !s.PlacementValidFirst {
			return TransportResultRetry, UnableUnloadMessage, TransportEventNone // [04 §10.2] verbatim Unable to unload unit code 9
		}
		// TODO(question): lowering-command altitude model-bottom offset and no-radius queuing not simulated [04 §10.2].
		s.Phase = 2
		return TransportResultContinue, "", TransportEventNone // [04 §10.2] success result inferred 1
	case 2:
		// Phase 2 REVALIDATES — second anchor recompute plus validator before release [04 §10.2] double validation.
		// An unload interrupt flag returns 9 BEFORE second validation [04 §10.2].
		if s.Interrupt {
			return TransportResultRetry, "", TransportEventNone // [04 §10.2] returns 9 before second validation
		}
		// Failed revalidation emits same message and returns 9 [04 §10.2].
		if !s.PlacementValidSecond {
			return TransportResultRetry, UnableUnloadMessage, TransportEventNone // [04 §10.2]
		}
		// Success starts deferred zero-argument EndTransport FIRST, then detaches cargo (reserved no-piece index), then constructs climb-away point command at carrier current X/Z with altitude cruisealt — release order exactly callback→detach→climb-away [04 §10.2].
		// TODO(question): EndTransport deferred start vs detach ordering is established but callback identity (EndTransport vs BeginTransport) and cargo-list mutation are left to integration with cob/units; phase advance captures the contract.
		s.Phase = 3
		return TransportResultContinue, "", TransportEventNone // [04 §10.2] success result inferred 1
	case 3:
		// Phase 3 emits event code 13 with no text payload and finishes [04 §10.2][GAP T16].
		s.Phase = 4
		return TransportResultDone, "", TransportEventDetach // [04 §10.2] phase 3 event 13 [GAP T16] result 5 done
	default:
		return TransportResultBlocked, "", TransportEventNone // [04 §10.2] other result 7 inferred
	}
}
