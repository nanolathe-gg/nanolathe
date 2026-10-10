package visibility

// perspectiveSensorMask contains the four sensor-owned bits [03 R-VIS-01 §4].
// Every other bit always comes from the current unit, never a retained bank.
const perspectiveSensorMask uint32 = sensorClearMask | DecloakBit

type perspectiveSensorStatus struct {
	allocationSerial uint64
	status           uint32
}

// EnableOwnerPerspectives selects the online sensor/history interpretation
// before battle entry (DESIGN_MULTIPLAYER §6.3, §16.4.1). It does not change
// grids, mode, local presentation, or an already retained sensor word.
func (s *Service) EnableOwnerPerspectives() {
	if s != nil {
		s.ownerPerspectives = true
	}
}

// StatusForPerspective reads one allocation's latched sensor bits, overlaid
// onto its current non-sensor flags. A new allocation starts with only its own
// perspective's sonar exemption [03 R-VIS-01 §4 Gate]. Serial zero is still
// inside creation, before identity commits, and must never borrow a prior
// occupant's contact (DESIGN_MULTIPLAYER §16.4.1).
//
// A hosted computer reads its host's bank, sonar exemption included, so its
// answer is always its host's (SetPerspectiveHost).
func (s *Service) StatusForPerspective(owner PlayerID, id uint16, allocationSerial uint64, unitOwner PlayerID, fallback uint32) uint32 {
	if s == nil || !s.ownerPerspectives {
		return fallback
	}
	var status uint32
	if validPlayer(owner) {
		owner = s.PerspectiveOf(owner)
		if owner == unitOwner {
			status = SonarBit
		}
		bank := s.perspectiveStatus[owner]
		if allocationSerial != 0 && int(id) < len(bank) && bank[id].allocationSerial == allocationSerial {
			status = bank[id].status
		}
	}
	return fallback&^perspectiveSensorMask | status&perspectiveSensorMask
}

// SensorTickForPerspective runs the existing sensor algorithm at one human's
// settlement deadline (DESIGN_MULTIPLAYER §6.3, §16.4.1). The input status words
// are read but never written; their sensor output belongs to this owner's
// allocation bank. The real shared reveal deadline is still written in place.
// Pass 4's source gate, "simulated on this machine", is this human's units and
// those of the computers it hosts, the rows its retail machine would have
// simulated [03 R-VIS-01 §4 pass 4]; every other owner's units are some other
// machine's.
//
// A defeated owner's pass marks every unit friendly [03 R-VIS-01 §4] pass 1,
// unless SetDefeatedHostKeepsSight is on and a computer this owner hosts
// still has a live unit in units: then the pass is an ordinary viewer's, so
// the computer that reads it keeps normal sight.
func (s *Service) SensorTickForPerspective(owner PlayerID, defeated bool, tick uint32, activePlayers int, units []SensorUnit) {
	if s == nil || !s.ownerPerspectives || !validPlayer(owner) {
		return
	}
	if defeated && s.defeatedHostKeepsSight && s.hostsLiveComputer(owner, units) {
		defeated = false
	}
	if activePlayers <= 1 {
		// Preserve both the no-write gate and the ordinary diagnostic reset.
		s.sensorTick(tick, activePlayers, units, owner, defeated)
		return
	}

	// Size the whole bank before taking any row pointers. A later sparse slot
	// must not move earlier rows away from the pointers handed to the passes.
	size := len(s.perspectiveStatus[owner])
	for _, u := range units {
		if u.Alive && u.Status != nil && u.AllocationSerial != 0 && int(u.ID)+1 > size {
			size = int(u.ID) + 1
		}
	}
	bank := s.perspectiveStatus[owner]
	if len(bank) < size {
		bank = append(bank, make([]perspectiveSensorStatus, size-len(bank))...)
		s.perspectiveStatus[owner] = bank
	}
	if cap(s.perspectiveUnits) < len(units) {
		s.perspectiveUnits = make([]SensorUnit, len(units))
	}
	s.perspectiveUnits = s.perspectiveUnits[:len(units)]
	copy(s.perspectiveUnits, units)
	if cap(s.perspectiveTransient) < len(units) {
		s.perspectiveTransient = make([]uint32, len(units))
	}
	s.perspectiveTransient = s.perspectiveTransient[:len(units)]
	for i := range s.perspectiveUnits {
		u := &s.perspectiveUnits[i]
		u.OwnerLocallySimulated = u.OwnerLocallySimulated && s.PerspectiveOf(u.Owner) == owner
		if !u.Alive || u.Status == nil {
			continue
		}
		status := s.StatusForPerspective(owner, u.ID, u.AllocationSerial, u.Owner, *u.Status)
		if u.AllocationSerial == 0 {
			// An unfinished creation has fresh temporary status, never a bank
			// identity. Production phase-5 inputs are committed allocations.
			s.perspectiveTransient[i] = status
			u.Status = &s.perspectiveTransient[i]
			continue
		}
		row := &bank[u.ID]
		*row = perspectiveSensorStatus{allocationSerial: u.AllocationSerial, status: status}
		u.Status = &row.status
	}
	s.sensorTick(tick, activePlayers, s.perspectiveUnits, owner, defeated)
}

// historyPlayer is the explored-history bit a query on behalf of viewer reads
// under Permanent LOS: retail's local player's [03 §3.2] C8 step 4, which
// online is the perspective viewer reads, its host's for a hosted computer.
func (s *Service) historyPlayer(viewer PlayerID) PlayerID {
	if s.ownerPerspectives {
		return s.PerspectiveOf(viewer)
	}
	return s.local
}

// SetPerspectiveHost makes player borrow host's perspective in an online
// battle: its sensor status reads and its Permanent LOS history reads become
// host's, and host's sensor pass treats player's units as locally simulated
// for pass 4's source gate. It is retail's hosted computer, which reads the
// sensor picture of the machine that runs it [03 R-VIS-01 §4]
// (DESIGN_MULTIPLAYER §6.2, §6.6). Current coverage stays player's own: the
// byte-grid sampler reads the querying record's grid [03 §3.2] C8 step 4.
//
// It is composition-time configuration, set before the first tick and never
// changed. A host must be a different, valid player that borrows no
// perspective itself; anything else is refused and changes nothing. Single-
// player battles never set it: there every viewer already reads the local
// slot's history bit and the unit's own status word.
func (s *Service) SetPerspectiveHost(player, host PlayerID) bool {
	if s == nil || !validPlayer(player) || !validPlayer(host) || player == host || s.perspectiveHost[host] != 0 {
		return false
	}
	for p := range s.perspectiveHost {
		if s.perspectiveHost[p] == uint8(player)+1 {
			return false // player already hosts another; one level only
		}
	}
	s.perspectiveHost[player] = uint8(host) + 1
	return true
}

// SetDefeatedHostKeepsSight selects the online seat policy's sight half
// (DESIGN_MULTIPLAYER §6.6): when on, a defeated seat whose hosted computer
// still has a live unit runs its sensor pass as an ordinary viewer's rather
// than a defeated one's, which would mark every unit friendly
// [03 R-VIS-01 §4] pass 1 and hand the computer that borrows the pass the
// whole map. Once its computers are gone the defeated marking applies as
// before. Off is retail's machine, whose computers stop with its human; it
// is the zero value, and a battle with no hosted computer never consults it.
func (s *Service) SetDefeatedHostKeepsSight(on bool) {
	if s != nil {
		s.defeatedHostKeepsSight = on
	}
}

// hostsLiveComputer reports whether some unit in units is alive and belongs
// to a player that borrows owner's perspective. The pass input holds every
// live unit, so this is "a computer owner hosts is not eliminated".
func (s *Service) hostsLiveComputer(owner PlayerID, units []SensorUnit) bool {
	for i := range units {
		u := &units[i]
		if u.Alive && u.Owner != owner && s.PerspectiveOf(u.Owner) == owner {
			return true
		}
	}
	return false
}

// PerspectiveOf is the perspective player reads: its host's when it borrows
// one (SetPerspectiveHost), otherwise its own. An invalid player reads itself.
func (s *Service) PerspectiveOf(player PlayerID) PlayerID {
	if s == nil || !validPlayer(player) || s.perspectiveHost[player] == 0 {
		return player
	}
	return PlayerID(s.perspectiveHost[player] - 1)
}
