package session

import (
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/mission"
)

// Readers of the runtime PLAYER RECORD.
//
// A skirmish has two tables that describe a slot, and only one of them is
// authoritative once the battle has started.
//
// The SETUP record is the pre-battle mirror the skirmish screen edits: the
// nickname, controller, side, colour, ally group and starting resources of
// [08 R-SKIR-01 §1]. Battle entry consumes it once, in the row-to-player
// conversion of [08 R-SKIR-01 §2]: a live row "copies colour and side into the
// player's lobby record", registration "stores the controller byte", and the
// ally groups are converted into the alliance rows — `allied[i][j] = 1` for
// every row `j` sharing a non-5 group, which is the alliance predicate the rest
// of the engine reads. Nothing consults the setup rows afterwards.
//
// The PLAYER RECORD is what the battle carries, and it is what the save
// persists: the `Player%i` account writes `Controller`, `Logo`, `Side` and the
// 11-byte `Alliances` box among its nineteen scalars [08 "Player records"]. A
// load restores those, and restores into the setup record only "the five [rule
// words] ... and the map name" [08 R-SKIR-01 §2] "Save persistence" — so after
// a load every setup row reads back as controller 0, colour 0, side 0, ally
// group 0 for every slot, whatever the battle actually is.
//
// Every reader that runs after battle entry therefore reads the record.
// WU-19-228 moved the side; these helpers are the rest of the census, and the
// readers that used to prefer a setup row now go through them. The setup rows
// keep exactly two consumers, both of them before the battle: composition
// (which performs the conversion) and the pre-battle shell.

// playerRecord returns the runtime record for a slot, or nil when the slot is
// out of range or the session has no player table (an unwired composition).
func (s *Session) playerRecord(owner int) *economy.Player {
	if s == nil || s.Econ == nil || owner < 0 || owner >= 10 || owner >= len(s.Econ.Players) {
		return nil
	}
	return &s.Econ.Players[owner]
}

// sideForOwner is the slot's side, and it is the one side reader: the commander
// identity of [08 R-SKIR-01 §3], the trigger adapter's identity of
// [08 R-TRIG-01 §3] and the deathmatch respawn all ask it.
//
// A campaign battle runs no row-to-player conversion, so its side comes from
// the campaign player table: the briefing panel's `campaignside` resolves the
// two rows directly [08 R-CAMP-01 §1][08 R-CAMP-01 §3], and a retail restore
// rebuilds the same table from the account's `Side` item, "that slot's
// player-table side ordinal" [08 "Player records"]. A campaign slot neither
// writer supplied has no authored side and stays unknown [I9] — the caller
// fails closed rather than inferring one from owner parity, a commander type or
// a side name.
//
// Every other battle reads the record, which battle entry writes and the
// `Player%i` account persists.
func (s *Session) sideForOwner(owner int) (int, bool) {
	if s == nil || owner < 0 || owner >= 10 {
		return 0, false
	}
	if s.Mission != nil && s.Mission.Type == mission.TypeCampaign {
		if owner < len(s.campaignPlayerSide) && s.campaignPlayerSideKnown[owner] {
			return int(s.campaignPlayerSide[owner]), true
		}
		return 0, false
	}
	if p := s.playerRecord(owner); p != nil && p.Exists {
		return int(p.Side), true
	}
	// A battle always has a player table; a session without one is an unwired
	// composition, so the setup row is the only thing left to read.
	if owner < len(s.Skirmish.Players) {
		return s.Skirmish.Players[owner].Side, true
	}
	return 0, false
}

// controllerForOwner is the slot's control byte — 1 a locally controlled human,
// 2 a computer player, 3 a remote peer, 0 an inactive slot
// [05 R-SHARE-01 §1][08 R-SKIR-01 §2]. It is the byte registration stores and
// the `Player%i` account persists.
func (s *Session) controllerForOwner(owner int) uint8 {
	p := s.playerRecord(owner)
	if p == nil || !p.Exists {
		return 0
	}
	return p.ControllerState
}

// ownerIsObserver reports the record's observer byte, which excludes a slot
// from settlement [05 "Authoritative settlement order"] and from a result row.
//
// TODO(question): the observer byte does not survive a save. The `Player%i`
// account's item list is closed at nineteen scalars plus `Alliances`
// [08 "Player records"] and carries no observer or watcher item, so a restored
// slot reads back as an ordinary participant. Retail's own skirmish screen
// offers only Open / Player / Computer rows [08 R-SKIR-01 §1], so the state may
// be unrepresentable in a retail save rather than lost by this reader; settling
// it needs the multiplayer lobby record's watcher bit (0x40) traced to a save
// item or to nothing.
func (s *Session) ownerIsObserver(owner int) bool {
	p := s.playerRecord(owner)
	return p != nil && p.IsObserver
}

// colourForOwner is the slot's logo byte — the colour the row-to-player
// conversion copied in and the placement stamp copied again
// [08 R-SKIR-01 §2] — reported with false when the slot has no record.
func (s *Session) colourForOwner(owner int) (uint8, bool) {
	p := s.playerRecord(owner)
	if p == nil || !p.Exists {
		return 0, false
	}
	return p.Logo, true
}

// ownersAllied is the alliance predicate of [08 R-SKIR-01 §2]: the byte at
// column `j` of player `i`'s first alliance row. It is what the setup rows'
// ally groups became at battle entry, it is symmetric in skirmish because it is
// derived from equal group numbers, and group-5 rows are allied with nobody but
// themselves. The row is the last item of each `Player%i` account, so it
// survives a load while the ally-group ordinal does not.
func (s *Session) ownersAllied(i, j int) bool {
	p := s.playerRecord(i)
	if p == nil || j < 0 || j >= len(p.Allies) {
		return false
	}
	return p.Allies[j]
}
