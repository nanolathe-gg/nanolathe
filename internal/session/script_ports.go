package session

import (
	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/combat"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// ScriptPortRules owns the adopted recorder port table. The session and
// reading unit are operands rather than retained state: rule implementations
// are shared, zero-size values, and a VM handler asks the currently bound seam
// on every read so a command-boundary switch reaches units already created
// (DESIGN_COMMUNITY_PATCH §4.5, §9).
type ScriptPortRules interface {
	ReadScriptPort(session *Session, unit *units.Unit, port cob.Port, args [4]int32) int32
}

// StrictScriptPorts preserves retail's closed twenty-port table: every
// extension identifier reads zero without inspecting session state
// [04 R-COB-03 §1].
type StrictScriptPorts struct{}

func (StrictScriptPorts) ReadScriptPort(*Session, *units.Unit, cob.Port, [4]int32) int32 {
	return 0
}

// CommunityScriptPorts implements only the eight source-established ports
// adopted by DESIGN_COMMUNITY_PATCH §4.5. The recorder's wider extension
// table remains outside this seam until its individual contracts are recorded
// [research/extensions/script-ports.md "Port table", "Unknown"].
type CommunityScriptPorts struct{}

func (CommunityScriptPorts) ReadScriptPort(s *Session, reading *units.Unit, port cob.Port, args [4]int32) int32 {
	if s == nil || reading == nil || !s.Community.ScriptPorts {
		return 0
	}
	switch port {
	case 32:
		return reading.Kills * 100
	case 69:
		return 1
	case 70:
		return sessionUnitLimit(s) * 10
	case 71:
		return int32(uint16(reading.Handle))
	case 72:
		target := rawScriptPortUnit(s, args[0])
		if target == nil {
			return 0
		}
		return int32(target.Owner)
	case 73:
		target := reading
		if uint16(args[0]) != 0 {
			target = rawScriptPortUnit(s, args[0])
		}
		if target == nil || target.Remaining == 0 {
			return 0
		}
		return 1 + int32(float32(99)*target.Remaining)
	case 74:
		if reading.Owner >= 10 || uint16(args[0]) == 0 {
			return 0
		}
		targetOwner := uint8(0)
		if target := rawScriptPortUnit(s, args[0]); target != nil {
			targetOwner = target.Owner
		}
		if s.ownersAllied(int(reading.Owner), int(targetOwner)) {
			return 1
		}
		return 0
	case 75:
		target := reading
		if uint16(args[0]) != 0 {
			target = rawScriptPortUnit(s, args[0])
		}
		if target == nil {
			return 0
		}
		player := s.playerRecord(int(target.Owner))
		if player == nil {
			return 0
		}
		controller := player.ControllerState
		if controller == combat.ControlByteHuman || controller == combat.ControlByteComputer {
			return 1
		}
		return 0
	default:
		return 0
	}
}

// ModernScriptPorts carries the Community table unchanged. Modern has no
// separate script-port policy (DESIGN_COMMUNITY_PATCH §4.5).
type ModernScriptPorts struct{ CommunityScriptPorts }

func rawScriptPortUnit(s *Session, id int32) *units.Unit {
	if s == nil || s.Units == nil {
		return nil
	}
	return s.Units.RawUnitRecord(pool.Handle(uint16(id)))
}

func (s *Session) scriptPortRules() ScriptPortRules {
	if s == nil || s.Rules.ScriptPorts == nil {
		return StrictScriptPorts{}
	}
	return s.Rules.ScriptPorts
}

var adoptedScriptPorts = [...]cob.Port{32, 69, 70, 71, 72, 73, 74, 75}

func (s *Session) bindScriptPorts(vm *cob.VM, u *units.Unit) {
	if s == nil || vm == nil || u == nil {
		return
	}
	for _, id := range adoptedScriptPorts {
		port := id
		vm.BindPortBinding(port, cob.PortBinding{Read: func(args [4]int32) int32 {
			return s.scriptPortRules().ReadScriptPort(s, u, port, args)
		}})
	}
}
