// Package orders owns the order record, the two queue segments a unit carries,
// and the pump that dispatches them.
//
// A record is the 86-byte retail order record of [04 §3.2], allocated by the
// constructor every insertion path goes through and carrying a descriptor
// identity from the 68-row table of [04 §3.1]. A queue is the primary segment
// and the rear segment bit 18 selects, and the pump is [04 §3.3]'s walk: it
// reloads the head after every dispatch, hands each handler the satisfied set
// the record's dynamic gate admits, and applies the result code the handler
// returns.
//
// The package resolves commands to descriptors [04 §3.4] and hosts the handler
// bodies of [04 R-ORD-01] — the work family, the combat rows, the standing
// orders, the transports and the VTOL twins. Rows another subsystem drives —
// the build rows construction owns, the air rows movement owns — register that
// ownership on the queue instead, and the pump writes no result code over
// them.
package orders
