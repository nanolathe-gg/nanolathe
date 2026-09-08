package session

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"

	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/frame"
	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/save"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/vfs"
)

// RetailLoadDeps supplies the already selected content and the two fresh
// battle-entry random seeds. Retail saves omit both streams [08 "Scheduler
// and random state in saves"] [01 R-CORE-02].
type RetailLoadDeps struct {
	FS      vfs.FSOps
	Catalog *content.Catalog
	SimSeed uint32
	CRTSeed uint32
	// UnitLimit is the configured `[Preferences] UnitLimit` as it stands when
	// the load starts. A restored battle's pool is sized from the limit word
	// as it was *before* the restore — the save's own Summary `maxunits` is
	// written into the configured limit and only reaches the next battle
	// [08 R-ENTRY-01 §6] — so this, not the save, is what sizes the pool of a
	// non-campaign restore. Zero takes the missing-value default through the
	// same clamp the setup record uses [08 R-SKIR-01 §6].
	UnitLimit int
}

// RetailBattleStage is an unreachable, fully detached staging result. The
// caller may atomically install Session only after this function succeeds.
// StableUnit maps each saved nonzero unit ID to its forced pool handle.
type RetailBattleStage struct {
	Image      *save.BattleImage
	Session    *Session
	StableUnit map[uint16]pool.Handle
}

// StageRetailBattle validates and stages an in-battle retail save. It does
// not run a tick and does not mutate the caller's Bank or any existing
// session. Only the D1 identity/allocation pass is performed; body fixups are
// owned by later persistence stages [08 R-SAVE-02 §11].
func StageRetailBattle(bank *save.Bank, deps RetailLoadDeps) (*RetailBattleStage, error) {
	preflight, err := PreflightRetailLoad(bank)
	if err != nil {
		return nil, err
	}
	if preflight.Route != RetailLoadRouteBattleRestoration {
		return nil, fmt.Errorf("session: retail save is a between-missions continuation, not a battle")
	}
	image, err := save.DecodeBattleImage(bank)
	if err != nil {
		return nil, err
	}
	if deps.FS == nil {
		return nil, fmt.Errorf("session: retail battle staging requires filesystem")
	}
	cat, err := strictCatalogWithProgress(deps.FS, deps.Catalog, nil)
	if err != nil {
		return nil, fmt.Errorf("session: retail catalog resolution: %w", err)
	}
	m, err := loadRetailStageMission(deps.FS, image.Summary)
	if err != nil {
		return nil, fmt.Errorf("session: retail mission resolution: %w", err)
	}
	// The pool's player-slice order only consults the peer-identity sort key
	// in session kind 3 (multiplayer); kinds 1 (campaign) and 2 (skirmish)
	// both order by slot regardless of that word [08 R-SESS-01 §7]. A loaded
	// battle restores its session kind from the save's own Summary.Gametype
	// [08 "Load process"], never from mission.Type — a different discriminant
	// for how the mission *file* is resolved, not for the session's kind.
	// Retail's Gametype save value only distinguishes campaign from "not
	// campaign"; this engine never implements true kind-3 network
	// multiplayer, so every non-campaign restore runs as a skirmish (kind 2)
	// restore and needs no saved sort-key field at all [08 R-SESS-01 §7
	// "Consequence for single-player"].
	sessionKind := sessionKindCampaign
	if image.Summary.Gametype == GametypeMultiplayer {
		sessionKind = sessionKindSkirmish
	}
	// The pool is `limit × 10 + 1` records, `limit` per slot
	// [05 R-SHARE-01 §7]. The limit is the session word as it stands at the
	// world rebuild, which is before the restoration dispatcher runs: a
	// campaign restore therefore keeps the OTA `maxunits` the mission loader
	// just decoded, and a non-campaign restore keeps the configured
	// `[Preferences] UnitLimit` [08 R-SKIR-01 §6][08 R-ENTRY-01 §6]. The
	// save's Summary `maxunits` is deliberately not read here: it is written
	// into the configured limit and only reaches the *next* battle
	// [08 R-ENTRY-01 §6].
	poolRecords := int(campaignUnitLimit(m))
	if sessionKind == sessionKindSkirmish {
		// Verbatim: the configured word was clamped when the profile was
		// read, and a value a previous restore carried into it is used as it
		// stands [08 R-SESS-01 §9].
		poolRecords = unitLimitOrDefault(deps.UnitLimit)
	}
	// A restored campaign runs under the same unit restriction a fresh one
	// does. Battle entry's unit-restriction loader is kind 1 only, and it runs
	// in §2 step 4 — before the world rebuild's catalog compile compacts the
	// cleared records out, so the restriction manifests as catalog removal,
	// re-sorting and renumbering rather than as an allocator refusal
	// [08 R-ENTRY-01 §2 step 4] [05 R-SHARE-01 §8]. The load path runs "§3
	// world rebuild in full" [08 R-ENTRY-01 §8 "Load path summary"], so the
	// same step precedes it here: the restriction is resolved after the
	// mission and applied before the terrain, the pool and every unit, which
	// is what makes the restored definition index space the one the save was
	// written against.
	//
	// The restriction lasts exactly one battle, so it is applied to a detached
	// clone; deps.Catalog is a caller-shared compiled catalog and must not be
	// mutated. The pool count is read above, from the session unit limit and
	// never from the definition count [05 R-SHARE-01 §7].
	if sessionKind == sessionKindCampaign {
		cat, err = applyUseOnlyRestriction(deps.FS, cat, m.UseOnlyPath)
		if err != nil {
			return nil, err
		}
	}
	terrain, err := loadTerrainStrict(deps.FS, cat, m)
	if err != nil {
		return nil, fmt.Errorf("session: retail map resolution: %w", err)
	}
	unitsWorld, err := newBattleSlicedWorldWithCOBSized(cat, deps.FS, sessionKind, [pool.PlayerCount]uint32{}, poolRecords)
	if err != nil {
		return nil, err
	}

	// Seed first, then restore the saved scheduler image. No composition or
	// forced allocation is allowed to step the clock before this boundary
	// [08 "Scheduler and random state in saves"] [01 R-CORE-02].
	clk := &clock.State{}
	s := &Session{
		State:        StateBattle,
		Catalog:      cat,
		World:        terrain,
		Mission:      m,
		Clock:        clk,
		Snapshot:     frame.NewBuffer(),
		Units:        unitsWorld,
		Econ:         &economy.Service{},
		Latch:        NewEndLatch(),
		CampaignSlot: m.CampaignIndex,
		Skirmish: SkirmishConfig{
			MapName: image.Summary.MapName, NumPlayers: int(image.Summary.Players),
			Difficulty: int(image.Summary.Difficulty), Mapping: int(image.Summary.Mapping),
			LineOfSight: int(image.Summary.LineOfSight), LOSType: int(image.Summary.LineOfSightType),
			CommanderDeath: int(image.Summary.CommanderDeath),
			// The same word the pool was just sized from, so every later
			// reader of the session limit — the AI's half-capacity term
			// [08 R-AI-01 §13], the path scheduler — agrees with the slice it
			// is describing [05 R-SHARE-01 §7].
			UnitLimit: poolRecords,
		},
	}
	s.SeedSessionRNG(deps.SimSeed, deps.CRTSeed)
	if err := s.Clock.LoadBoxChecked(image.Scheduler); err != nil {
		return nil, fmt.Errorf("session: restore scheduler: %w", err)
	}
	for i := range image.Players {
		if image.Players[i].Index >= 0 && image.Players[i].Index < len(s.Econ.Players) {
			image.Players[i].ApplyToEconomy(&s.Econ.Players[image.Players[i].Index])
			s.Econ.Players[image.Players[i].Index].Exists = true
		}
	}
	// The shared end countdown and latch are not among the persisted
	// `Player%i` keys [05 R-ECO-01 §12], and the world rebuild's per-player
	// reset runs before the restoration dispatcher [08 R-ENTRY-01 §8]
	// [08 R-SAVE-02 §11], so a load resumes with the countdown unarmed exactly
	// as a fresh entry does. Mirror the latch built above onto every record the
	// way the live tick does: the settlement gate reads the per-player mirror
	// and settles only while it is negative [08 R-TRIG-01 §6]
	// [05 "Authoritative settlement order"]. Elimination needs no such write —
	// it is derived from the two unit counters the forced-slot allocator
	// rebuilds, never from a flag [05 R-ECO-01 §12].
	s.publishEndCountdown()
	s.InitBattleWindForSession()
	// The world rebuild installs the authored storm after its scheduler reset
	// and before the per-player reset constructs AI records or any unit is
	// reserved [08 R-ENTRY-01 §3 step 21-24]. A bad selected default therefore
	// fails the detached stage before those later effects.
	if err := s.initMeteor(); err != nil {
		return nil, err
	}
	if err := createAndBindServices(s); err != nil {
		return nil, fmt.Errorf("session: retail shell composition: %w", err)
	}
	// The per-player reset that builds every AI record belongs to the world
	// rebuild, which runs "for every kind and for loads alike" — before the
	// restoration dispatcher, and before any unit exists [08 R-ENTRY-01 §3
	// step 24][08 R-SAVE-02 §11-A]. Keeping it here, ahead of
	// the forced-slot reservation, is what makes a restored battle's planner a
	// battle-entry planner that then meets a restored world.
	if err := initializeRestoredBattleAI(s, deps.FS, m, sessionKind); err != nil {
		return nil, fmt.Errorf("session: retail restore: computer player construction: %w", err)
	}
	stable, err := reserveRetailUnits(s.Units, cat, image.Units.Records)
	if err != nil {
		return nil, err
	}
	return &RetailBattleStage{Image: image, Session: s, StableUnit: stable}, nil
}

func loadRetailStageMission(fs vfs.FSOps, summary save.Summary) (*mission.Mission, error) {
	if summary.Gametype == GametypeMultiplayer {
		if strings.TrimSpace(summary.MapName) == "" {
			return nil, fmt.Errorf("missing saved map identity")
		}
		return mission.LoadWithType(fs, mission.TypeSkirmish, summary.MapName, 0, int(summary.Players), nil)
	}
	if strings.TrimSpace(summary.Campaign) == "" {
		return nil, fmt.Errorf("missing saved campaign identity")
	}
	campaign, err := mission.DiscoverCampaign(fs, summary.Campaign)
	if err != nil {
		return nil, fmt.Errorf("discover campaign: %w", err)
	}
	if campaign == nil {
		return nil, fmt.Errorf("discover campaign: no campaign returned")
	}
	var idx int
	found := false
	for _, stub := range campaign.Missions {
		if strings.EqualFold(strings.TrimSpace(stub.Name), strings.TrimSpace(summary.Mission)) {
			idx = stub.Index
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("saved mission %q not found in campaign %q", summary.Mission, campaign.OriginalPath)
	}
	return mission.LoadCampaignWithSink(fs, campaign.Path, idx, int(summary.Difficulty), int(summary.Players), nil)
}

func reserveRetailUnits(w *units.World, cat *content.Catalog, records []save.UnitRecord) (map[uint16]pool.Handle, error) {
	if w == nil || cat == nil {
		return nil, fmt.Errorf("session: retail unit reservation has nil world/catalog")
	}
	// Validate every mandatory identity before the first allocation, so a bad
	// image cannot expose a partially reserved result even inside the stage.
	for _, rec := range records {
		// Compatibility records are null results. They retain their writer
		// enumeration position for ScriptN indexing but have no identity or
		// allocation pass [08 R-SAVE-UNIT-01] [08 R-SAVE-02 §6].
		if rec.Compat {
			continue
		}
		if len(rec.Data) != save.UnitBoxSize {
			return nil, fmt.Errorf("session: retail unit %d is not a standard 0xB8 record", rec.Number)
		}
		if rec.StableID == 0 {
			return nil, fmt.Errorf("session: retail unit %d has null stable slot", rec.Number)
		}
		owner := rec.Data[0x20]
		if owner >= 10 {
			return nil, fmt.Errorf("session: retail unit %d owner %d outside player slices", rec.Number, owner)
		}
		nameBytes := rec.Data[:0x20]
		if n := bytes.IndexByte(nameBytes, 0); n >= 0 {
			nameBytes = nameBytes[:n]
		}
		name := strings.TrimSpace(string(nameBytes))
		if name == "" {
			return nil, fmt.Errorf("session: retail unit %d has empty definition identity", rec.Number)
		}
		if _, ok := cat.Units[content.CanonicalKey(name)]; !ok {
			return nil, fmt.Errorf("session: retail unit %d definition %q is unresolved", rec.Number, name)
		}
	}
	stable := make(map[uint16]pool.Handle, len(records))
	for _, rec := range records {
		if rec.Compat {
			continue
		}
		nameBytes := rec.Data[:0x20]
		if n := bytes.IndexByte(nameBytes, 0); n >= 0 {
			nameBytes = nameBytes[:n]
		}
		def := cat.Units[content.CanonicalKey(strings.TrimSpace(string(nameBytes)))]
		x := numeric.Fixed(int64(int32(binary.LittleEndian.Uint32(rec.Data[0x2b:]))))
		y := numeric.Fixed(int64(int32(binary.LittleEndian.Uint32(rec.Data[0x2f:]))))
		z := numeric.Fixed(int64(int32(binary.LittleEndian.Uint32(rec.Data[0x33:]))))
		h, err := w.CreateWithForcedSlot(def, rec.Data[0x20], x, y, z, pool.Handle(rec.StableID))
		if err != nil {
			return nil, fmt.Errorf("session: retail unit %d forced slot %d: %w", rec.Number, rec.StableID, err)
		}
		stable[rec.StableID] = h
	}
	return stable, nil
}
