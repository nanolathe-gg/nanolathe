package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/nanolathe/nanolathe/internal/ai"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/units"
)

const defaultHeadlessTicks = 30 * 600

var errHeadlessTickLimit = errors.New("headless tick limit reached")

type headlessPlayerReport struct {
	Player          int                    `json:"player"`
	LiveUnits       int                    `json:"live_units"`
	UnitsCreated    int                    `json:"units_created"`
	AIManagerBound  bool                   `json:"ai_manager_bound"`
	OrdersSubmitted int                    `json:"orders_submitted"`
	OrderIntents    []headlessIntentReport `json:"order_intents,omitempty"`
}

type headlessIntentReport struct {
	Intent string `json:"intent"`
	Count  int    `json:"count"`
}

type headlessReport struct {
	Tick    uint32                   `json:"tick"`
	State   string                   `json:"state"`
	Result  string                   `json:"result"`
	Players [10]headlessPlayerReport `json:"players"`
}

// runHeadless composes the same session constructors as the menu paths and
// advances their ordinary single-player clock without a presentation client.
func runHeadless(opts Options, cs *contentSet, out io.Writer) error {
	if cs == nil || cs.fs == nil {
		return headlessLoadError(opts, cs, "content mount is unavailable")
	}
	if opts.Ticks < 0 {
		return headlessLoadError(opts, cs, "tick limit must not be negative")
	}

	seeds := newBattleSeedSource(opts).NextBattleSeeds()
	var sess *session.Session
	var err error
	if opts.Mission != "" {
		sess, err = session.NewMissionWithProgressSeeds(cs.fs, nil, opts.Mission, opts.Difficulty, uint32(seeds.Simulation), seeds.CRT, nil)
	} else {
		if opts.Map == "" {
			return headlessLoadError(opts, cs, "no map or mission was selected")
		}
		cfg := session.DirectSkirmishConfig(opts.Map)
		cfg.RNGSimSeed = uint32(seeds.Simulation)
		cfg.RNGCrtSeed = seeds.CRT
		sess, err = session.NewSkirmishWithProgress(cs.fs, nil, cfg, nil)
	}
	if err != nil {
		return headlessLoadError(opts, cs, err.Error())
	}

	observer := observeHeadlessSession(sess)
	limit := opts.Ticks
	if limit == 0 {
		limit = defaultHeadlessTicks
	}
	advanceHeadlessSession(sess, uint32(limit), observer)

	report := buildHeadlessReport(sess, observer)
	if err := writeHeadlessReport(opts.Report, out, report); err != nil {
		return err
	}
	if sess.State != session.StatePostBattle {
		return errHeadlessTickLimit
	}
	return nil
}

func headlessLoadError(opts Options, cs *contentSet, cause string) error {
	logical := opts.Map
	if opts.Mission != "" {
		logical = opts.Mission
	}
	if logical == "" {
		logical = "<command line>"
	}
	var providers []string
	if cs != nil && cs.fs != nil {
		providers = providerNames(cs.fs)
	}
	return &missingProductError{
		what:      "headless session load failed: " + cause,
		logical:   logical,
		providers: providers,
		expected:  "a valid skirmish map or campaign mission",
	}
}

type headlessSessionObserver struct {
	created   [10]int
	submitted [10]int
	active    [10]map[*orders.Node]struct{}
	intents   [10]map[string]int
}

func observeHeadlessSession(sess *session.Session) *headlessSessionObserver {
	observer := &headlessSessionObserver{}
	if sess == nil {
		return observer
	}
	if sess.Units != nil {
		for i := range observer.created {
			observer.created[i] = sess.Units.LiveCountForPlayer(i)
		}
		previous := sess.Units.OnCreate
		sess.Units.OnCreate = func(h pool.Handle, u *units.Unit) {
			if u != nil && int(u.Owner) < len(observer.created) {
				observer.created[u.Owner]++
			}
			if previous != nil {
				previous(h, u)
			}
		}
	}
	for i, manager := range sess.AI {
		if manager == nil || manager.QueueBuildTyped == nil {
			continue
		}
		player := i
		previous := manager.QueueBuildTyped
		manager.QueueBuildTyped = func(req ai.BuildRequest) error {
			err := previous(req)
			if err == nil {
				observer.submitted[player]++
			}
			return err
		}
	}
	observer.scanOrderQueues(sess, false)
	return observer
}

// scanOrderQueues observes the ordinary queue surface used by AI move,
// attack, explore, and rally producers. Build requests are counted at their
// typed sink above because a build node can be consumed within the same pump;
// a surviving build node is still included in the per-intent census but is not
// added to the generic submission count a second time.
func (o *headlessSessionObserver) scanOrderQueues(sess *session.Session, countNew bool) {
	if o == nil || sess == nil || sess.Units == nil {
		return
	}
	var current [10]map[*orders.Node]struct{}
	for _, u := range sess.Units.IterSliced() {
		if u == nil || int(u.Owner) >= len(sess.AI) || sess.AI[u.Owner] == nil {
			continue
		}
		q := orders.QueueOfUnit(u)
		if q == nil {
			continue
		}
		player := int(u.Owner)
		if current[player] == nil {
			current[player] = make(map[*orders.Node]struct{})
		}
		segments := [2][]*orders.Node{q.Primary(), q.Secondary()}
		for _, nodes := range segments {
			for _, node := range nodes {
				if node == nil {
					continue
				}
				current[player][node] = struct{}{}
				if !countNew {
					continue
				}
				if _, existed := o.active[player][node]; existed {
					continue
				}
				intent := orders.DescriptorFor(node.ID).Name
				if intent != "" {
					if o.intents[player] == nil {
						o.intents[player] = make(map[string]int)
					}
					o.intents[player][intent]++
				}
				if node.BuildDefKey == "" {
					o.submitted[player]++
				}
			}
		}
	}
	o.active = current
}

func advanceHeadlessSession(sess *session.Session, limit uint32, observer *headlessSessionObserver) {
	if sess == nil || sess.Clock == nil {
		return
	}
	scaledNow := sess.Clock.ScaledAnchor
	for sess.State != session.StatePostBattle && sess.Clock.GlobalTick < limit {
		remaining := limit - sess.Clock.GlobalTick
		delta := int32(5)
		if remaining < uint32(delta) {
			delta = int32(remaining)
		}
		scaledNow += delta
		sess.Step(scaledNow)
		if observer != nil {
			observer.scanOrderQueues(sess, true)
		}
	}
}

func buildHeadlessReport(sess *session.Session, observer *headlessSessionObserver) headlessReport {
	var report headlessReport
	for i := range report.Players {
		report.Players[i].Player = i
	}
	if sess == nil {
		return report
	}
	if sess.Clock != nil {
		report.Tick = sess.Clock.GlobalTick
	}
	report.State = sess.State.String()
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
	for i := range report.Players {
		if sess.Units != nil {
			report.Players[i].LiveUnits = sess.Units.LiveCountForPlayer(i)
		}
		if observer != nil {
			report.Players[i].UnitsCreated = observer.created[i]
		}
		report.Players[i].AIManagerBound = sess.AI[i] != nil
		if observer != nil {
			report.Players[i].OrdersSubmitted = observer.submitted[i]
			intentNames := make([]string, 0, len(observer.intents[i]))
			for name := range observer.intents[i] {
				intentNames = append(intentNames, name)
			}
			sort.Strings(intentNames)
			for _, name := range intentNames {
				report.Players[i].OrderIntents = append(report.Players[i].OrderIntents, headlessIntentReport{
					Intent: name,
					Count:  observer.intents[i][name],
				})
			}
		}
	}
	return report
}

func writeHeadlessReport(path string, out io.Writer, report headlessReport) error {
	w := out
	var file *os.File
	if path != "" {
		var err error
		file, err = os.Create(path)
		if err != nil {
			return fmt.Errorf("nanolathe: create headless report %q: %w", path, err)
		}
		w = file
	}
	if w == nil {
		w = io.Discard
	}
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	writeErr := encoder.Encode(report)
	if file != nil {
		closeErr := file.Close()
		if writeErr == nil && closeErr != nil {
			return fmt.Errorf("nanolathe: close headless report %q: %w", path, closeErr)
		}
	}
	if writeErr != nil {
		return fmt.Errorf("nanolathe: write headless report %q: %w", path, writeErr)
	}
	return nil
}
