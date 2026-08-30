package session

import (
	"strings"

	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/triggers"
	"github.com/nanolathe/nanolathe/internal/units"
)

// triggerLocalPlayer resolves the local human from the authoritative player
// table in ascending slot order. Campaign and direct-OTA sessions do not
// require a skirmish-lobby adapter to establish this identity [08 R-TRIG-01 §6].
func (s *Session) triggerLocalPlayer() (int, bool) {
	if s == nil || s.Econ == nil {
		return 0, false
	}
	for i := 0; i < len(s.Econ.Players); i++ {
		p := &s.Econ.Players[i]
		if p.Exists && p.ControllerState == 1 && !p.IsObserver {
			return i, true
		}
	}
	return 0, false
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
	c.IsCommander = func(u *units.Unit) bool {
		if u == nil || u.Def == nil || s.Catalog == nil || s.Econ == nil || int(u.Owner) >= len(s.Econ.Players) {
			return false
		}
		side := -1
		if int(u.Owner) < s.Skirmish.NumPlayers {
			side = s.Skirmish.Players[u.Owner].Side
		} else if s.Mission != nil && s.Mission.Type == mission.TypeCampaign && s.campaignPlayerSideKnown[u.Owner] {
			side = int(s.campaignPlayerSide[u.Owner])
		} else {
			// TODO(question): expose the campaign player table's side ordinal at
			// first construction and restore; a trace of the campaign player-table
			// writer is the decider. Do not infer it from owner parity, commander
			// type, or side name [08 R-TRIG-01 §3].
			return false
		}
		if side < 0 || side >= len(s.Catalog.Sides) || s.Catalog.Sides[side] == nil {
			return false
		}
		return strings.EqualFold(u.Def.UnitName, s.Catalog.Sides[side].Commander)
	}
	return c
}
