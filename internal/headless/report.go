package headless

import (
	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"sort"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/session"
)

// groupSampleInterval is the cadence, in authoritative ticks, at which the
// displayless observer copies each manager's ten task-group member counts. It
// matches the attack wave's own 300-tick reschedule so one sample exists for
// every wave invocation [08 R-AI-01 §4]. Sampling is a read of committed
// state: it draws no random numbers and writes no authoritative field.
const groupSampleInterval uint32 = 300

// GroupSample is one player's manager task-group census at one tick. The
// index is the retail group-record number: 0 is the ungrouped sentinel and
// 1..9 are the nine task records in slot order [08 R-P0-04 §2].
type GroupSample struct {
	Tick   uint32     `json:"tick"`
	Counts [10]uint16 `json:"counts"`
}

// AttackEvent records the first attack-family order a player's units received.
// For a computer player that order comes from the attack wave's broadcast of
// intent 3 in unit-pool order [08 R-AI-01 §4][08 R-AI-01 §9].
// AttackEvent is one recorded attack intent: which tick it was issued on, the
// intent name, and the actor and target pool slots.
type AttackEvent struct {
	Tick   uint32 `json:"tick"`
	Intent string `json:"intent"`
	Actor  uint32 `json:"actor"`
	Target uint32 `json:"target"`
}

// IntentReport counts how many orders of one intent a player submitted.
type IntentReport struct {
	Intent string `json:"intent"`
	Count  int    `json:"count"`
}

// PlayerReport is one player slot's row of the run report.
type PlayerReport struct {
	Player          int            `json:"player"`
	LiveUnits       int            `json:"live_units"`
	UnitsCreated    int            `json:"units_created"`
	AIManagerBound  bool           `json:"ai_manager_bound"`
	OrdersSubmitted int            `json:"orders_submitted"`
	Kills           int            `json:"kills"`
	Losses          int            `json:"losses"`
	OrderIntents    []IntentReport `json:"order_intents,omitempty"`
	FirstAttack     *AttackEvent   `json:"first_attack,omitempty"`
	GroupSamples    []GroupSample  `json:"group_samples,omitempty"`
}

// Report is a stable diagnostic surface, not additional authoritative state.
// StateHash retains its legacy JSON field name. Its version-prefixed value is
// Session.PartialStateFingerprint, whose intentionally partial coverage is listed
// in docs/DESIGN_RUNTIME_DETERMINISM.md §4; it is not a whole-session parity gate.
type Report struct {
	Community       community.Features `json:"gameplay_features"`
	CommunityDigest string             `json:"gameplay_features_digest"`
	EntryCommunity  community.Features `json:"entry_gameplay_features"`
	Gameplay        gameplay.Mode      `json:"gameplay"`
	// Rules is the bound rule set's name. Gameplay reports only the reserved
	// base word, so a registered third-party set is visible only here.
	Rules string `json:"rules"`
	// ContentProfile is the resolved content profile: `retail` for an
	// unmodified install, otherwise the profile whose markers the mounted
	// overlay presented or the one the host selected explicitly
	// (docs/DESIGN_CONTENT_VFS.md §5 "Content profiles").
	ContentProfile            string           `json:"content_profile,omitempty"`
	ScenarioKind              ScenarioKind     `json:"scenario_kind"`
	ScenarioIdentity          string           `json:"scenario_identity"`
	SimulationSeed            uint32           `json:"simulation_seed"`
	CRTSeed                   uint32           `json:"crt_seed"`
	SimulationState           uint32           `json:"simulation_state"`
	CRTState                  uint32           `json:"crt_state"`
	SimulationDraws           uint64           `json:"simulation_draws"`
	CRTDraws                  uint64           `json:"crt_draws"`
	CatalogHash               string           `json:"catalog_hash,omitempty"`
	ManifestHash              string           `json:"manifest_hash,omitempty"`
	Tick                      uint32           `json:"tick"`
	Status                    string           `json:"status"`
	State                     string           `json:"state"`
	Result                    string           `json:"result"`
	StateHash                 string           `json:"state_hash"`
	PresentationEventsDropped uint64           `json:"presentation_events_dropped,omitempty"`
	Players                   [10]PlayerReport `json:"players"`
}

// attackFamily is the set of canonical command names the order resolver
// produces for intent 3, the computer player's attack intent, plus the
// position-only arms of that same code [04 R-ORD-02 §1]. The observer uses it
// only to name the first attack broadcast in the report.
var attackFamily = map[string]struct{}{
	"Attack_Chase":     {},
	"Attack_NoMove":    {},
	"Attack_Kamikaze":  {},
	"AttackSpecial":    {},
	"AttackUType":      {},
	"Suppress":         {},
	"AirStrike":        {},
	"AirToAir":         {},
	"AirToGround":      {},
	"AirToGroundHover": {},
}

func isAttackIntent(name string) bool {
	_, ok := attackFamily[name]
	return ok
}

// noteAttack records the earliest attack-family order seen for a player. The
// scan that calls it walks the unit pool in ascending order, so the recorded
// actor is the first such unit in pool order at the earliest observed tick —
// the same order the group broadcast itself uses [08 R-AI-01 §9].
func (o *observer) noteAttack(player int, tick uint32, intent string, actor pool.Handle, target pool.Handle) {
	if o == nil || player < 0 || player >= len(o.firstAttack) {
		return
	}
	if o.firstAttack[player] != nil {
		return
	}
	o.firstAttack[player] = &AttackEvent{Tick: tick, Intent: intent, Actor: uint32(actor), Target: uint32(target)}
}

// sampleGroups copies every bound manager's task-group member counts once per
// groupSampleInterval ticks. It reads the exported vectors in fixed record
// order, never ranges a map, and never mutates the manager [I1].
func (o *observer) sampleGroups(sess *session.Session) {
	if o == nil || sess == nil || sess.Clock == nil {
		return
	}
	tick := sess.Clock.GlobalTick
	if tick%groupSampleInterval != 0 {
		return
	}
	if o.sampled && o.lastSample == tick {
		return
	}
	o.sampled, o.lastSample = true, tick
	for i, manager := range sess.AI {
		if manager == nil || i >= len(o.groups) {
			continue
		}
		o.groups[i] = append(o.groups[i], GroupSample{Tick: tick, Counts: groupCounts(manager)})
	}
}

// groupCounts reads the nine task records plus the ungrouped sentinel. Record
// zero has no vector on the manager, so its slot stays zero: retail's
// ungrouped record is not one of the nine task vectors [08 R-P0-04 §2].
func groupCounts(m *ai.Manager) [10]uint16 {
	var counts [10]uint16
	if m == nil {
		return counts
	}
	vectors := [9][]pool.Handle{
		m.GroupResource,
		m.GroupWaveA,
		m.GroupRegroupA,
		m.GroupConstruction,
		m.GroupNull,
		m.GroupWaveB,
		m.GroupRegroupB,
		m.GroupExplore,
		m.GroupRally,
	}
	for i, vector := range vectors {
		counts[i+1] = uint16(len(vector))
	}
	return counts
}

func buildReport(request Request, kind ScenarioKind, identity string, sess *session.Session, observer *observer) (Report, error) {
	report := Report{
		ScenarioKind:     kind,
		Gameplay:         sess.Gameplay.Normalize(),
		Rules:            sess.Rules.Name,
		Community:        sess.Community,
		EntryCommunity:   sess.EntryCommunity,
		CommunityDigest:  sess.Community.Digest(),
		ContentProfile:   request.ContentProfile,
		ScenarioIdentity: identity,
		SimulationSeed:   request.SimulationSeed,
		CRTSeed:          request.CRTSeed,
		Tick:             sess.Clock.GlobalTick,
		State:            sess.State.String(),
	}
	if simulation := sess.SimRNG(); simulation != nil {
		report.SimulationState = simulation.State
		report.SimulationDraws = simulation.Draws()
	}
	if crt := sess.CrtRNG(); crt != nil {
		report.CRTState = crt.State
		report.CRTDraws = crt.Draws()
	}
	if sess.Catalog != nil {
		report.CatalogHash = sess.Catalog.Hash
		report.ManifestHash = sess.Catalog.Manifest
	}
	if sess.Snapshot != nil {
		report.PresentationEventsDropped, _ = sess.Snapshot.RetainedEventsDropped()
	}
	for i := range report.Players {
		report.Players[i].Player = i
		if sess.Units != nil {
			report.Players[i].LiveUnits = sess.Units.LiveCountForPlayer(i)
		}
		report.Players[i].UnitsCreated = observer.created[i]
		report.Players[i].AIManagerBound = sess.AI[i] != nil
		report.Players[i].OrdersSubmitted = observer.submitted[i]
		// Kills and losses are the settlement counters the result collector
		// already maintains; the report only copies them [08 R-CAMP-01 §7].
		if sess.Econ != nil && i < len(sess.Econ.Players) {
			report.Players[i].Kills = int(sess.Econ.Players[i].Kills)
			report.Players[i].Losses = int(sess.Econ.Players[i].Losses)
		}
		report.Players[i].FirstAttack = observer.firstAttack[i]
		report.Players[i].GroupSamples = observer.groups[i]
		names := make([]string, 0, len(observer.intents[i]))
		for name := range observer.intents[i] {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			report.Players[i].OrderIntents = append(report.Players[i].OrderIntents, IntentReport{Intent: name, Count: observer.intents[i][name]})
		}
	}
	result := sess.GetResult()
	if result.Ended {
		switch result.Kind {
		case "victory":
			report.Result = "won"
		case "defeat":
			report.Result = "lost"
		case "draw":
			report.Result = "draw"
		}
	}
	hash, err := sess.PartialStateFingerprint()
	if err != nil {
		return report, err
	}
	report.StateHash = hash
	return report, nil
}

// descriptorName is the small indirection the observer uses so the attack
// classification and the intent census read the same descriptor table.
func descriptorName(id orders.ID) string { return orders.DescriptorFor(id).Name }
