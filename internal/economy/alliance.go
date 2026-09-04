package economy

// AllianceRowBytes is the width of one alliance row: eleven bytes indexed by
// player slot number [05 R-SHARE-01 §1]. Eleven, not ten, because the player
// table is built with eleven rows and the eleventh — the neutral row 10 that
// nothing ever occupies — is addressable by index like any other
// [05 "Player slot"]. Nanolathe models the ten seated slots as
// `Player.Allies`; column 10 of a row is therefore always zero here, which is
// what a never-occupied row's column reads as in retail too.
const AllianceRowBytes = 11

// AllianceRow projects `slot`'s FIRST alliance row — this player's own
// declaration toward each other slot, non-zero meaning allied — into the
// eleven-byte form the save bank carries [05 R-SHARE-01 §1] [08 "Player
// records"]. `slot` is the row's own index, so the self column can be forced.
//
// The first row is the one every simulation consumer indexes: sharing reads
// `source.A[candidate]`, the sensor phase reads `owner.A[viewer]`, the guard's
// combat join reads `attackerOwner.A[guardOwner]`, and skirmish setup derives
// the whole row from equal ally groups [05 R-SHARE-01 §1] [08 R-SKIR-01 §2].
//
// TODO(question): which of the two runtime rows the eleven-byte `Alliances`
// box carries is not stated by either the writer or the reader census [08
// "Player records"] — one box is emitted per `Player%i` account and the
// record holds two rows. The first row is used here because it is the row
// the alliance predicate reads and the only row this build models at all;
// the second row (the multiplayer alliance screen's mirror, diagonal-only in
// skirmish [08 R-SKIR-01 §2]) has no Nanolathe representation to persist.
// Settled by a static trace of the alliance reader's destination field.
func (p Player) AllianceRow(slot int) [AllianceRowBytes]byte {
	var row [AllianceRowBytes]byte
	for i := range p.Allies {
		if p.Allies[i] {
			row[i] = 1
		}
	}
	// The self column is 1 from slot registration onward [08 R-SKIR-01 §2];
	// stating it here keeps the written row and the restored row identical
	// even for a slot whose registration this build never ran.
	if slot >= 0 && slot < AllianceRowBytes {
		row[slot] = 1
	}
	return row
}

// SetAllianceRow applies an eleven-byte saved row to `slot`'s first alliance
// row and forces the self column to 1 [08 "Player records"]. Any non-zero
// byte is "allied" [05 R-SHARE-01 §1]; column 10 names the neutral row that
// nothing occupies and is dropped [05 "Player slot"].
//
// The forcing is unconditional on this path because retail forces the self
// byte after a successful eleven-byte load. Whether it also forces it when
// the box is absent or mis-sized is not distinguishable: slot registration
// has already set the self column [08 R-SKIR-01 §2], so the byte is 1 either
// way, and callers only reach here with a loaded row.
func (p *Player) SetAllianceRow(slot int, row [AllianceRowBytes]byte) {
	if p == nil {
		return
	}
	for i := range p.Allies {
		p.Allies[i] = row[i] != 0
	}
	if slot >= 0 && slot < len(p.Allies) {
		p.Allies[slot] = true
	}
}

// AllianceRow is the Service-level read of slot's first alliance row. An
// out-of-range slot reports the zero row rather than panicking, so a save
// projection over a fixed ten-slot walk needs no bound check of its own.
func (s *Service) AllianceRow(slot int) [AllianceRowBytes]byte {
	if s == nil || slot < 0 || slot >= len(s.Players) {
		return [AllianceRowBytes]byte{}
	}
	return s.Players[slot].AllianceRow(slot)
}

// SetAllianceRow is the Service-level restore of slot's first alliance row.
func (s *Service) SetAllianceRow(slot int, row [AllianceRowBytes]byte) {
	if s == nil || slot < 0 || slot >= len(s.Players) {
		return
	}
	s.Players[slot].SetAllianceRow(slot, row)
}
