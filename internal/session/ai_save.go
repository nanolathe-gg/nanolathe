package session

import (
	"encoding/json"
	"fmt"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
)

// AIControllers is what a save records of the battle's Modern AI controllers
// (docs/DESIGN_SESSIONS_AI_SAVE.md "Modern AI computer player"), for the
// save's Nanolathe sidecar: the battle seed every computer player's private
// generator was seeded from, and where each generator stood for every
// controller that had begun. A load that restores it rebuilds each
// controller with the same draws: the brain's Init draws its style and
// opening variation again from the same seed, and the generator then
// continues from the recorded position. The controllers' memory — what they
// saw, their plans, their builders' tasks — is not recorded; a restored
// controller starts again from observation.
//
// It also records each computer player's configured brain parameters
// (ai.Manager.ControllerParams), so a loaded game's controllers are built as
// the saved game's were, whatever the host's settings say at the load.
type AIControllers struct {
	// Seed is the battle seed of the saved game's generators
	// (ai.Manager.BattleSeed): the fresh battle's entry seed, or the seed a
	// restored battle carried forward from its own save.
	Seed uint32 `json:"seed"`
	// Generators are the positions of the generators that existed at the
	// save, in ascending player order.
	Generators []AIGenerator `json:"generators,omitempty"`
	// Overrides are the effective parameters of every computer player that
	// had any, in ascending player order. A player without an entry played
	// the brain's defaults.
	Overrides []AIPlayerOverrides `json:"overrides,omitempty"`
	// Modern lists, in ascending order, the computer players the lobby
	// marked Modern (ai.Manager.Controller). A player not listed is Classic,
	// so a record written before the per-player choice existed restores
	// every computer player Classic (docs/DESIGN_SESSIONS_AI_SAVE.md
	// "Modern AI computer player", "Per-player selection").
	Modern []uint8 `json:"modern,omitempty"`
}

// SidecarAIControllers decodes a sidecar's `ai` record; nil when it has
// none. A record that does not parse is an error, like any malformed sidecar
// value, rather than a silent fresh draw.
func SidecarAIControllers(raw []byte) (*AIControllers, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var rec AIControllers
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, fmt.Errorf("nanolathe: save sidecar ai record: %w", err)
	}
	return &rec, nil
}

// AIGenerator is one computer player's generator position at a save.
type AIGenerator struct {
	Player   uint8  `json:"player"`
	Position uint64 `json:"position"`
}

// AIPlayerOverrides is one computer player's effective Modern AI
// parameters at a save, in canonical text (CanonicalAIParams).
type AIPlayerOverrides struct {
	Player uint8  `json:"player"`
	Params string `json:"params"`
}

// aiGeneratorRecorder is what a controller kept in ai.Manager.Ext offers a
// save: its private generator's position, after waiting for its work in
// flight, and false before it has one. The Modern AI host (internal/aikit)
// has it; the session asks without importing the controller's package.
type aiGeneratorRecorder interface{ Generator() (uint64, bool) }

// RecordAIControllers returns the battle's AI controller record for a save's
// sidecar, or nil when the battle has no computer-player manager. It records
// the seed whether or not a controller exists yet, so a game saved before its
// controllers began — or under a set that binds none — still restores the
// seed they would have drawn from. Call it between ticks, as the bank writer
// is called: it waits for any controller work in flight, which changes no
// game.
func RecordAIControllers(s *Session) *AIControllers {
	if s == nil {
		return nil
	}
	var out *AIControllers
	for player, mgr := range s.AI {
		if mgr == nil {
			continue
		}
		if out == nil {
			out = &AIControllers{Seed: mgr.BattleSeed}
		}
		position, ok := uint64(0), false
		if recorder, is := mgr.Ext.(aiGeneratorRecorder); is {
			position, ok = recorder.Generator()
		}
		if !ok && mgr.ResumeGenerator != nil {
			// Restored, saved again before its controller began: the position
			// it was to resume from is still the one to carry.
			position, ok = *mgr.ResumeGenerator, true
		}
		if ok {
			out.Generators = append(out.Generators, AIGenerator{Player: uint8(player), Position: position})
		}
		if mgr.ControllerParams != "" {
			out.Overrides = append(out.Overrides, AIPlayerOverrides{Player: uint8(player), Params: mgr.ControllerParams})
		}
		if mgr.Controller == ai.ControllerModern {
			out.Modern = append(out.Modern, uint8(player))
		}
	}
	return out
}

// restoreAIControllers puts a save's AI controller record on a restored
// battle's managers, after they are built and before any controller exists:
// every manager's battle seed becomes the recorded one, and each recorded
// generator position waits on its player's manager for the controller that
// manager builds first. A position for a slot with no manager has nothing to
// resume and is dropped. Every manager's parameters become the recorded
// ones, and a manager the record names none for plays the brain's defaults,
// as its player did in the saved game. Each player the record lists as
// Modern takes the Modern AI controller again, and every other one stays
// Classic. Nil (a retail save, or a sidecar without the record) leaves the
// managers seeded from the load's entry seed with no parameters and every
// computer player Classic: a load never reads the host's current settings,
// as it takes no mutators without a sidecar either. A record that lists a
// Modern player this build cannot play refuses the load.
func restoreAIControllers(s *Session, rec *AIControllers) error {
	if s == nil || rec == nil {
		return nil
	}
	for _, mgr := range s.AI {
		if mgr != nil {
			mgr.BattleSeed = rec.Seed
			mgr.ResumeGenerator = nil
			mgr.ControllerParams = ""
		}
	}
	for _, p := range rec.Modern {
		if int(p) >= len(s.AI) || s.AI[p] == nil || s.AI[p].Passive {
			continue
		}
		if err := s.setAIController(s.AI[p], ai.ControllerModern); err != nil {
			return err
		}
	}
	for _, o := range rec.Overrides {
		if int(o.Player) < len(s.AI) && s.AI[o.Player] != nil {
			s.AI[o.Player].ControllerParams = o.Params
		}
	}
	for _, g := range rec.Generators {
		if int(g.Player) >= len(s.AI) || s.AI[g.Player] == nil {
			continue
		}
		position := g.Position
		s.AI[g.Player].ResumeGenerator = &position
	}
	return nil
}
