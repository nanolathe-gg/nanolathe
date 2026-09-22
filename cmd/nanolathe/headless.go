package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/headless"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

var errHeadlessTickLimit = headless.ErrTickLimit

type headlessReport = headless.Report

// runHeadless is the graphical command's adapter to the displayless runner.
// Session construction, stepping, observation, and report semantics live in
// internal/headless so this package does not carry a second phase loop.
func runHeadless(opts Options, cs *contentSet, out io.Writer) error {
	if opts.Ticks < 0 {
		return fmt.Errorf("nanolathe: headless session load failed: tick limit must not be negative: logical path <command line>, providers searched [none], expected a non-negative authoritative tick limit")
	}
	request, reportRequest, err := headlessFreshBattleRequest(opts, cs, newBattleSeedSource(opts))
	if err != nil {
		return err
	}
	authoritative, err := composeAuthoritativeBattle(request)
	if err != nil {
		return err
	}
	if _, err := installHeadlessModelTextureRegistry(authoritative.Session, cs.unmappedMount); err != nil {
		return err
	}
	if authoritative.Session.Features != nil {
		defer authoritative.Session.Features.SetDefinitionAdmissionObserver(nil)
	}
	reportRequest.TickLimit = uint32(opts.Ticks)
	report, runErr := headless.RunSession(reportRequest, authoritative.Session)
	if report.ScenarioIdentity != "" {
		if err := writeHeadlessReport(opts.Report, out, report); err != nil {
			return err
		}
	}
	return runErr
}

// installHeadlessModelTextureRegistry gives a displayless battle the same
// battle-owned phase-7 service as a windowed battle. It deliberately creates
// no Client; the registry advances presentation metadata while RunSession owns
// the authoritative tick loop [03 R-CRD-005 §1]. An unexpected named-model
// failure rejects composition before the tick loop starts.
func installHeadlessModelTextureRegistry(sess *session.Session, fs *vfs.FS) (*client.ModelTextureRegistry, error) {
	if sess == nil {
		return nil, fmt.Errorf("nanolathe: headless session load failed: no session")
	}
	restoreStart := 0
	if sess.World != nil {
		restoreStart = len(sess.World.FeatureDefs)
	}
	if sess.Features != nil {
		restoreStart = sess.Features.DefinitionRestoreStart()
	}
	registry, err := client.NewModelTextureRegistry(fs, sess.Catalog, sess.World, restoreStart)
	if err != nil {
		return nil, fmt.Errorf("nanolathe: headless session load failed: model textures: %w", err)
	}
	sess.SetPhase7Service(registry)
	sess.SetFragmentMaterialResolver(registry.FreezeFragmentMaterial)
	if sess.Features != nil {
		sess.Features.SetDefinitionAdmissionObserver(registry.AdmitFeatureDefinition)
	}
	return registry, nil
}

func headlessFreshBattleRequest(opts Options, cs *contentSet, source BattleSeedSource) (freshBattleRequest, headless.Request, error) {
	reportRequest := headless.Request{
		Gameplay: opts.Gameplay,
		// run replaced the selector with the name the mount boundary
		// resolved, so this is the profile the run actually used.
		ContentProfile: opts.ContentProfile,
		Map:            opts.Map,
		Mission:        opts.Mission,
		Difficulty:     opts.Difficulty,
	}
	if cs == nil || cs.fs == nil {
		return freshBattleRequest{}, reportRequest, unavailableBattleContentError()
	}
	var (
		request freshBattleRequest
		err     error
	)
	if opts.Map != "" && opts.Mission != "" {
		var seeds BattleSeeds
		if source != nil {
			seeds = source.NextBattleSeeds()
		}
		request.value = headless.FreshBattleRequest{
			CommunitySources: communitySources(opts, cs),
			BuilderOptions:   sessionBuilderOptions(loadedSettings().BuilderOptions),
			Gameplay:         opts.Gameplay,
			Map:              opts.Map, Mission: opts.Mission, Difficulty: opts.Difficulty,
			LocalOwner: -1, SimulationSeed: uint32(seeds.Simulation), CRTSeed: seeds.CRT,
			FS: cs.fs, ContentLimits: cs.limits, PresentationWidth: retailScreenW, PresentationHeight: retailScreenH,
		}
	} else if opts.Mission != "" {
		request, err = missionBattleRequest(opts, cs, opts.Mission, opts.Difficulty, -1, -1, nil, source)
	} else {
		request, err = directMapBattleRequest(opts, cs, source)
	}
	if err != nil {
		return freshBattleRequest{}, reportRequest, err
	}
	reportRequest.SimulationSeed = request.value.SimulationSeed
	reportRequest.CRTSeed = request.value.CRTSeed
	return request, reportRequest, nil
}

func writeHeadlessReport(path string, out io.Writer, report headless.Report) error {
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
