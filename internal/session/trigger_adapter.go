package session

import (
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/triggers"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// triggerLocalPlayer resolves the local human from the authoritative player
// table. Campaign and direct-OTA sessions do not require a skirmish-lobby
// adapter to establish this identity [08 R-TRIG-01 §6]. The walk is ascending
// and the LAST human row wins, which is what the row-to-player conversion
// leaves in the local-player indices when a composition carries more than one
// human row [08 R-SKIR-01 §2] "Battle entry: what the record becomes".
func (s *Session) triggerLocalPlayer() (int, bool) {
	if s == nil || s.Econ == nil {
		return 0, false
	}
	local, found := 0, false
	for i := 0; i < len(s.Econ.Players); i++ {
		p := &s.Econ.Players[i]
		if p.Exists && p.ControllerState == 1 && !p.IsObserver {
			local, found = i, true
		}
	}
	return local, found
}

func (s *Session) missionTriggerContext(tick uint32) triggers.PollContext {
	c := triggers.PollContext{Tick: tick}
	if s == nil {
		return c
	}
	c.World = s.Units
	c.MissionArmed = s.Mission != nil && len(s.Mission.Units) != 0
	c.StampedCell = func(u *units.Unit) (int16, int16, bool) {
		if u == nil || s.Movement == nil {
			return 0, 0, false
		}
		stamp := s.Movement.Collisions[u.Handle]
		if stamp == nil {
			return 0, 0, false
		}
		return int16(stamp.CachedAnchor.X), int16(stamp.CachedAnchor.Z), true
	}
	c.Deproject = func(x, z int32) (int32, int32, int32) {
		if s.World == nil {
			return 0, 0, 0
		}
		wx, wy, wz := s.World.CursorToWorldMapPixels(x, z)
		return int32(wx.Raw()), int32(wy.Raw()), int32(wz.Raw())
	}
	c.Celebrate = func() {
		if s.publication == nil || s.publication.events == nil {
			return
		}
		// This cue is unpositioned. It crosses the authoritative publication
		// boundary and reaches the backend only after the frame commits
		// [08 R-TRIG-01 §8][I6].
		s.publication.events.Admit(frame.Event{
			Tick:         tick,
			Kind:         frame.KindAudio,
			Sound:        "Victory Condition",
			AudioAudible: true,
		})
	}
	// The trigger adapter's commander identity is the same test the kill record
	// files [08 R-TRIG-01 §3][08 R-SKIR-01 §3]: the dead definition's name
	// against the commander named on the owner's side record. It used to be a
	// second copy of that comparison with its own side lookup, which read the
	// skirmish SETUP row — empty after a load [08 R-SKIR-01 §2] "Save
	// persistence" — before falling through to the campaign player table.
	// sideForOwner is now the one side reader; see player_record.go.
	c.IsCommander = s.isCommanderForOwner
	return c
}
