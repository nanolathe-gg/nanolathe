// Package visibility sensors implements C11, C12 [PLAN_05 WU-05-4] P0-11 [03 §3.4].
package visibility

import (
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

// Status-field roles [03 §3.4][R-VIS-01 §4]. The sonar bit doubles as the
// underwater-rejection exemption of [03 §3.2], which is why own units — whose
// friendly pair includes it — are implicitly exempt from the sea-level test.
const (
	SeenBit      uint32 = 0x100  // seen marker [03 §3.4][R-VIS-01 §4]
	SonarBit     uint32 = 0x200  // sonar contact; also the underwater exemption [03 §3.2][R-VIS-01 §4]
	FriendlyMask uint32 = 0x300  // the pair the friendly pass writes [R-VIS-01 §4]
	JammedBit    uint32 = 0x400  // jam marker; no reader anywhere [R-VIS-01 §4]
	DecloakBit   uint32 = 0x1000 // decloak timer running [03 §3.4][R-VIS-01 §4]

	// sensorClearMask is what the friendly pass clears on every unit that is
	// neither own nor seen by a defeated viewer: seen, sonar and jammed
	// together [R-VIS-01 §4] pass 1.
	sensorClearMask uint32 = 0x700
)

// DecloakDeadlineAdd is the decloak deadline offset in ticks [R-VIS-01 §4] pass 4.
const DecloakDeadlineAdd = 90

// SensorUnit is one unit as the sensor phase sees it [03 §3.4] P0-11.
//
// The phase mutates Status through the pointer — that is its entire
// authoritative output. Everything else it produces lands on the backing
// surfaces, which are presentation.
type SensorUnit struct {
	// ID correlates the sensor result with its committed unit pool slot.
	ID        uint16
	Owner     PlayerID
	Status    *uint32       // runtime status field; the phase writes 0x100/0x200/0x300/0x400/0x1000
	X, Z      numeric.Fixed // 16.16 world position
	Y         numeric.Fixed
	Alive     bool
	Hidden    bool // instance cloak bit; the seen probe's second gate [R-VIS-01 §4] pass 5
	Stealth   bool // definition stealth: the contact callback's third reject [R-VIS-01 §5]
	Active    bool // runtime activation/on-state bit required by the emitters [R-VIS-01 §4]
	OnOffable bool // definition on/off flag used by selected-unit circle presentation [03 §3.9]

	// Authored sensor distances [02 "Unit record"] P0-11. Zero means absent.
	RadarDistance    int32
	SonarDistance    int32
	RadarJam         int32
	SonarJam         int32
	MinCloakDistance int32

	// ModelTop is the candidate's bounding-box top extent in whole world
	// units. The radar admission test compares the top of that box against the
	// sea plane, so a submarine whose hull breaks the surface is radar-visible
	// while a fully submerged one is not [R-VIS-01 §5].
	ModelTop int32

	// DecloakDeadline receives tick+90 when this unit is decloaked by proximity [R-VIS-01 §4] pass 4.
	DecloakDeadline *uint32
}

// SensorInput is an immutable per-tick contact snapshot for presentation.
// SensorInputs returns copies so the renderer cannot mutate authoritative
// sensor state [03 §3.4].
type SensorInput struct {
	ID        uint16
	Owner     PlayerID
	X, Y, Z   numeric.Fixed
	Status    uint32
	Hidden    bool
	Stealth   bool // retained separately for the presentation blink gate
	Active    bool
	OnOffable bool
}

// SetViewerDefeated records whether the viewing player has been defeated or is
// an observer. It is the third disjunct of the friendly pass: a defeated
// viewer marks every live unit friendly [R-VIS-01 §4] pass 1.
func (s *Service) SetViewerDefeated(defeated bool) {
	if s != nil {
		s.viewerDefeated = defeated
	}
}

// SensorInputs returns the last completed sensor pass's contact inputs.
func (s *Service) SensorInputs() []SensorInput {
	if s == nil || len(s.sensorInputs) == 0 {
		return nil
	}
	out := make([]SensorInput, len(s.sensorInputs))
	copy(out, s.sensorInputs)
	return out
}

// worldUnit narrows a 16.16 coordinate to whole world units with an arithmetic
// shift. The radius visitor and both contact callbacks square whole world
// units, not map pixels and not fixed point [R-VIS-01 §5].
func worldUnit(v numeric.Fixed) int64 { return int64(v) >> 16 }

// planarSquared is the visitor's metric: the sum of the squared whole-world-unit
// axis deltas [R-VIS-01 §5].
func planarSquared(a, b *SensorUnit) int64 {
	dx := worldUnit(b.X) - worldUnit(a.X)
	dz := worldUnit(b.Z) - worldUnit(a.Z)
	return dx*dx + dz*dz
}

// SensorTick runs the per-tick sensor and proximity phase [03 §3.4][R-VIS-01 §4].
//
// It runs only when more than one player is active. In a one-player session
// none of the passes runs and the three status bits keep whatever value unit
// construction gave them [R-VIS-01 §4] "Gate".
//
// The five unit walks, in order [R-VIS-01 §4]:
//
//  1. clear the decloak-timer bit; set the friendly pair on own units (and on
//     everything when the viewer has been defeated), clear seen/sonar/jammed
//     otherwise;
//  2. radar and sonar emission from the viewing player's own active units;
//  3. radar-jam and sonar-jam emission from every other active unit;
//  4. the minimum-cloak proximity scan;
//  5. the seen probe, which sets the seen bit for any remaining unit standing
//     on a lit visibility tile.
//
// The phase writes neither visibility grid, and it consumes no random draws
// [R-VIS-01 §4] "Ordering and outputs", "Random draws".
//
// allied supplies the caller's alliance row. It is deliberately NOT consulted
// by pass 1: [R-VIS-01 §7] establishes that pass 1's allied disjunct cannot
// fire in retail — no writer anywhere sets the option bit it gates on — so an
// ally's radar contact never appears on the viewer's minimap and an ally's
// units are not exempted from the underwater rejection on the viewer's behalf.
// Only the proximity scan of pass 4 uses it, to separate hostiles from friends.
func (s *Service) SensorTick(tick uint32, playerCount int, allied func(a, b PlayerID) bool, units []SensorUnit) {
	if s == nil {
		return
	}
	s.sensorInputs = s.sensorInputs[:0]
	if playerCount <= 1 {
		return // more than one player required [R-VIS-01 §4] "Gate"
	}
	sea := s.seaLevelWorld()

	// Pass 1 — clear and friendly marking [R-VIS-01 §4].
	for i := range units {
		u := &units[i]
		if u.Status == nil || !u.Alive {
			continue
		}
		*u.Status &^= DecloakBit
		if u.Owner == s.local || s.viewerDefeated {
			*u.Status |= FriendlyMask
		} else {
			*u.Status &^= sensorClearMask
		}
	}

	// Pass 2 — radar and sonar emission over the viewing player's own units
	// only [R-VIS-01 §4]. The candidate rejects and the two strict admission
	// tests are [R-VIS-01 §5]'s contact callback.
	//
	// e.Active is the instance activation bit: a live unit with a nonzero
	// radar or sonar distance queries the contact callback, and draws its
	// circle below, only after that test [03 §3.4 "Sensor callback gate
	// correction"].
	//
	// TODO(question): five stock definitions author a sensor distance that
	// this gate can never admit, because no writer of the activation bit
	// reaches them — ARMANNI, ARMSS, CORSS, ARMACSUB and CORACSUB author
	// neither `activatewhenbuilt` nor `onoffable`, and are neither aircraft
	// nor factories. The remaining channel, the COB ACTIVATION port
	// ([04 §4.7] port 1, named as a writer by [05 R-PROD-01 §2]), was censused
	// over all 278 stock scripts by WU-19-158 and does not reach them either:
	// exactly nine scripts write that port and none is one of the five
	// [03 §3.4 "Sensor callback gate correction"]. So every named writer is now
	// eliminated and their authored range is dead data. What stays open is only
	// whether that is retail's intent. Do not widen this gate to "fix" it:
	// doing so asserts a writer that does not exist. Decider, now the only one
	// left: a manual retail observation of a stealth sub's sonar contact.
	for i := range units {
		e := &units[i]
		if !e.Alive || e.Owner != s.local || !e.Active {
			continue
		}
		if e.RadarDistance == 0 && e.SonarDistance == 0 {
			continue
		}
		// The visitor searches the two AUTHORED distances, unbonused; the
		// elevation bonus enters the squared radar test radius only, so any
		// extra reach beyond the search is unreachable [R-VIS-01 §4] pass 2.
		search := e.RadarDistance
		if e.SonarDistance > search {
			search = e.SonarDistance
		}
		search2 := int64(search) * int64(search)
		radarRadius := int64(e.RadarDistance) + 2*worldUnit(e.Y)
		radar2 := radarRadius * radarRadius
		sonar2 := int64(e.SonarDistance) * int64(e.SonarDistance)
		for j := range units {
			c := &units[j]
			if !c.Alive || c.Status == nil {
				continue
			}
			// Rejects in order: an own-side candidate, then definition
			// stealth, which suppresses radar and sonar outright with no
			// distance or elevation term [R-VIS-01 §5].
			if c.Owner == s.local || c.Stealth {
				continue
			}
			d2 := planarSquared(e, c)
			if d2 > search2 {
				continue // the visitor's own test is inclusive [R-VIS-01 §5]
			}
			// Both callback comparisons are strict [R-VIS-01 §5].
			if c.Y <= sea && d2 < sonar2 {
				*c.Status |= SonarBit
			}
			if sea <= c.Y+numeric.Fixed(int64(c.ModelTop)<<16) && d2 < radar2 {
				*c.Status |= SeenBit
			}
		}
	}

	// Pass 3 — jam emission from every active unit the viewing player does not
	// own. The jam callbacks apply no owner, alliance or stealth test, so they
	// reach friend and foe alike including the jammer's own side [R-VIS-01 §5].
	for i := range units {
		e := &units[i]
		if !e.Alive || e.Owner == s.local || !e.Active {
			continue
		}
		if e.RadarJam != 0 {
			r2 := int64(e.RadarJam) * int64(e.RadarJam)
			for j := range units {
				c := &units[j]
				if !c.Alive || c.Status == nil || planarSquared(e, c) > r2 {
					continue
				}
				*c.Status = (*c.Status &^ SeenBit) | JammedBit
			}
		}
		if e.SonarJam != 0 {
			r2 := int64(e.SonarJam) * int64(e.SonarJam)
			for j := range units {
				c := &units[j]
				if !c.Alive || c.Status == nil || planarSquared(e, c) > r2 {
					continue
				}
				*c.Status = (*c.Status &^ SonarBit) | JammedBit
			}
		}
	}

	// Pass 4 — minimum-cloak proximity [R-VIS-01 §4]. Retail searches the
	// cloaking unit's own side's primary candidate list of [06 §3.1], which is
	// rebuilt at most once every thirty ticks, so its breach test reads "an
	// enemy I could see up to a second ago is within mincloakdistance".
	// Nanolathe has no such per-side registry yet, so this scan walks live
	// hostiles directly and therefore breaches up to a second earlier than
	// retail. Nothing here is unknown: wiring the scan to that list once it
	// exists is the whole of the remaining work.
	for i := range units {
		src := &units[i]
		if !src.Alive || !src.Hidden || src.MinCloakDistance <= 0 || src.Status == nil {
			continue
		}
		r2 := int64(src.MinCloakDistance) * int64(src.MinCloakDistance)
		for j := range units {
			if i == j {
				continue
			}
			dst := &units[j]
			if !dst.Alive {
				continue
			}
			isEnemy := dst.Owner != src.Owner
			if allied != nil && allied(src.Owner, dst.Owner) {
				isEnemy = false
			}
			if !isEnemy {
				continue
			}
			if planarSquared(src, dst) > r2 {
				continue // the breach test is inclusive [R-VIS-01 §4] pass 4
			}
			*src.Status |= DecloakBit
			if src.DecloakDeadline != nil {
				*src.DecloakDeadline = tick + DecloakDeadlineAdd
			}
			break
		}
	}

	// Pass 5 — the seen probe: the single-point form of the §3.2 predicate
	// against the VIEWING player's mode-selected source, regardless of who owns
	// the unit. It runs after the jam pass, so line of sight restores a jammed
	// unit's seen bit within the same tick [R-VIS-01 §4] pass 5.
	for i := range units {
		u := &units[i]
		if u.Status == nil || !u.Alive {
			continue
		}
		if *u.Status&SeenBit != 0 {
			continue
		}
		if u.Hidden {
			continue // the instance cloak bit is the pass's only other gate
		}
		if s.VisiblePoint(s.local, u.X, u.Y, u.Z) {
			*u.Status |= SeenBit
		}
	}

	// The phase's whole output is the status bits the five passes above wrote,
	// plus this immutable per-unit snapshot of them. It rasterizes nothing:
	// [03 §3.10]'s 2026-08-29 correction establishes that the minimap's sensor
	// circles are presentation drawn by the CONTACTS pass, and retracts the
	// older reading that attributed them to this phase's callback tables — the
	// callbacks are one-line status-bit writers [R-VIS-01 §5]. The contacts
	// pass reads the authored distances from the unit definition and gates them
	// on its own blip and selected-unit-circle gates [03 §3.9].
	for i := range units {
		u := &units[i]
		if u.Alive && u.Status != nil {
			s.sensorInputs = append(s.sensorInputs, SensorInput{ID: u.ID, Owner: u.Owner, X: u.X, Y: u.Y, Z: u.Z, Status: *u.Status, Hidden: u.Hidden, Stealth: u.Stealth, Active: u.Active, OnOffable: u.OnOffable})
		}
	}
}

// ClearSeen drops a unit's seen marker. The phase's own first pass is the
// clear writer [R-VIS-01 §4]; this helper exists for callers that retire a
// unit outside the phase.
func ClearSeen(status *uint32) {
	if status != nil {
		*status &^= SeenBit
	}
}
