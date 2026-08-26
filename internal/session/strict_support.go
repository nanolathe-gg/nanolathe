package session

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os/exec"
	"strings"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/clock"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/kernel"
	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/snapshot"
	"github.com/nanolathe/nanolathe/internal/world"
)

// StrictGateEvidence is the per-gate evidence record [ON-10 §12].
type StrictGateEvidence struct {
	Commit          string            `json:"commit"`
	ContentManifest string            `json:"content_manifest"`
	Map             string            `json:"map"`
	Seed            uint32            `json:"seed"`
	CrtSeed         uint32            `json:"crt_seed"`
	Players         []map[string]any  `json:"players"`
	MaxTick         int               `json:"max_tick"`
	Milestones      map[string]uint32 `json:"milestones"`
	Winner          int               `json:"winner"`
	Reason          string            `json:"reason"`
	FinalTick       uint32            `json:"final_tick"`
	FinalStateHash  string            `json:"final_state_hash"`
	TraceHash       string            `json:"trace_hash"`
	Fallbacks       []string          `json:"fallbacks"`
	Warnings        []string          `json:"warnings"`
}

// StrictFailureRecord is the detailed failure diagnostic [ON-10 §12].
type StrictFailureRecord struct {
	LastCompleted   string              `json:"last_completed_milestone"`
	CurrentTick     uint32              `json:"current_tick"`
	Seed            uint32              `json:"seed"`
	CrtSeed         uint32              `json:"crt_seed"`
	Handles         []string            `json:"handles,omitempty"`
	DefKeys         []string            `json:"definition_keys,omitempty"`
	QueueHead       string              `json:"queue_head,omitempty"`
	PathStatus      string              `json:"path_status,omitempty"`
	AimState        string              `json:"aim_state,omitempty"`
	ResourceStocks  string              `json:"resource_stocks,omitempty"`
	ProjectileCount int                 `json:"projectile_count"`
	AITask          string              `json:"ai_task,omitempty"`
	AIDeadline      uint32              `json:"ai_deadline,omitempty"`
	ResultLatch     string              `json:"result_latch,omitempty"`
	Last50Trace     []string            `json:"last_50_trace"`
	Evidence        *StrictGateEvidence `json:"evidence,omitempty"`
}

func strictCommit() string {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

// HashTrace returns deterministic sha256 of ordered trace events [INVARIANTS I1][I4].
func HashTrace(evs []SessionTraceEvent) string {
	h := sha256.New()
	for _, e := range evs {
		fmt.Fprintf(h, "%d:%s:%d:%d:%d:%d:%d:%d:%d;", e.Tick, e.Kind, e.Player, e.Handle, e.Slot, e.WeaponID, e.X.Raw(), e.Z.Raw(), e.Value)
	}
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:8])
}

// HashState returns deterministic state hash for session (units + projectiles).
func HashState(s *Session) string {
	if s == nil {
		return ""
	}
	h := sha256.New()
	if s.Units != nil {
		for _, u := range s.Units.Iter() {
			if u == nil || !u.Alive {
				continue
			}
			// Group is authoritative manager/control-group state. It is hashed
			// separately from the tactical vectors below so an unlisted unit's
			// group transition cannot alias an otherwise identical state.
			fmt.Fprintf(h, "U%d:%d:%d:%d:%.2f:%d:%d:%t|", u.Handle, int64(u.X.Raw()), int64(u.Z.Raw()), u.Health, u.Remaining, u.Flags, u.Group, u.InBuildStance)
		}
	}
	if s.Combat != nil {
		for i := 0; i < s.Combat.Count(); i++ {
			if i >= len(s.Combat.Records) {
				break
			}
			p := s.Combat.Records[i]
			if s.Combat.IsDead(pool.Handle(i + 1)) {
				continue
			}
			fmt.Fprintf(h, "P%d:%d:%d:%d|", i, int64(p.Pos.X.Raw()), int64(p.Pos.Y.Raw()), int64(p.Pos.Z.Raw()))
		}
	}
	if s.Econ != nil {
		for i := 0; i < 10; i++ {
			p := &s.Econ.Players[i]
			if !p.Exists {
				continue
			}
			// Hash the exact float32 payloads used by the authoritative economy,
			// rather than a rounded display representation. The latter aliases
			// distinct stocks and can make a resumed state appear equivalent.
			fmt.Fprintf(h, "E%d:%08x:%08x|", i,
				math.Float32bits(p.Stock[economy.Metal]),
				math.Float32bits(p.Stock[economy.Energy]))
			// The strategic score consumes the four settled aggregates, not the
			// mirror-only pass counters. Hash raw float32 payloads so every
			// authoritative bit (including signed zero/NaN payloads) is covered
			// deterministically in player then resource order [R-P0-05].
			fmt.Fprintf(h, "A%d:%08x:%08x:%08x:%08x|", i,
				math.Float32bits(p.AIProduction[economy.Metal]),
				math.Float32bits(p.AIProduction[economy.Energy]),
				math.Float32bits(p.AIConsumption[economy.Metal]),
				math.Float32bits(p.AIConsumption[economy.Energy]))
		}
	}
	// Manager tactical vectors affect future AI admissions and task choices;
	// include their exact recovered slot order in the authoritative hash
	// [R-P0-04]. Handle sequence is meaningful because vector insertion uses
	// pool order and wave merge uses replace-with-last removal.
	for player, m := range s.AI {
		if m == nil {
			continue
		}
		// The cumulative eligible-entry count selects the next classifier
		// boundary; cover it independently from tactical membership vectors so
		// identical groups with different future cadence cannot alias [08 C3].
		fmt.Fprintf(h, "C%d:%d|G%d:", player, m.EntryCount(), player)
		groups := [][]pool.Handle{
			m.GroupResource, m.GroupWaveA, m.GroupRegroupA,
			m.GroupConstruction, m.GroupNull, m.GroupWaveB,
			m.GroupRegroupB, m.GroupExplore, m.GroupRally,
		}
		for slot, members := range groups {
			fmt.Fprintf(h, "%d[", slot+1)
			for _, handle := range members {
				fmt.Fprintf(h, "%d,", handle)
			}
			fmt.Fprint(h, "];")
		}
	}
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:8])
}

// LastNTraceStrings returns formatted last N trace events for diagnostics.
func LastNTraceStrings(evs []SessionTraceEvent, n int) []string {
	if len(evs) <= n {
		n = len(evs)
	}
	start := len(evs) - n
	if start < 0 {
		start = 0
	}
	out := make([]string, 0, len(evs)-start)
	for _, e := range evs[start:] {
		out = append(out, fmt.Sprintf("T%d %s P%d H%d S%d W%d X%d Z%d V%d", e.Tick, e.Kind, e.Player, e.Handle, e.Slot, e.WeaponID, e.X.Raw(), e.Z.Raw(), e.Value))
	}
	return out
}

// FormatEvidence returns JSON indented evidence for logging.
func FormatEvidence(ev StrictGateEvidence) string {
	b, _ := json.MarshalIndent(ev, "", "  ")
	return string(b)
}

// FormatFailure returns JSON indented failure record.
func FormatFailure(fr StrictFailureRecord) string {
	b, _ := json.MarshalIndent(fr, "", "  ")
	return string(b)
}

func strictCatalogHash(cat *content.Catalog) string {
	if cat == nil {
		return "nil"
	}
	if cat.Hash != "" {
		return cat.Hash
	}
	if cat.Manifest != "" {
		return cat.Manifest
	}
	h := sha256.New()
	for _, k := range cat.SortedUnitKeys() {
		h.Write([]byte(k))
		h.Write([]byte{0})
	}
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:8])
}

func strictMinimalCatalog() *content.Catalog {
	mv := map[string]*content.MovementClass{
		"testmove": {FootprintX: 1, FootprintZ: 1, MaxSlope: 10, MaxWaterDepth: 10},
	}
	mv["testmove"].CanonicalKey = content.CanonicalKey("testmove")
	cat := &content.Catalog{
		Units: map[string]*content.UnitDef{
			"armcom": {UnitName: "armcom", MaxDamage: 3000, SightDistance: 128, MovementClass: "testmove", FootprintX: 1, FootprintZ: 1, BuildTime: 100, WorkerTime: 30, CanMove: true, MaxVelocity: 2000, TurnRate: 1000, Builder: true},
			"corcom": {UnitName: "corcom", MaxDamage: 3000, SightDistance: 128, MovementClass: "testmove", FootprintX: 1, FootprintZ: 1, BuildTime: 100, WorkerTime: 30, CanMove: true, MaxVelocity: 2000, TurnRate: 1000, Builder: true},
		},
		Movement: mv,
		Sides: []*content.SideDef{
			{Name: "ARM", Commander: "armcom"},
			{Name: "CORE", Commander: "corcom"},
		},
		Features: map[string]*content.FeatureDef{},
		Maps:     map[string]*content.MapHeader{},
	}
	for _, u := range cat.Units {
		u.CanonicalKey = content.CanonicalKey(u.UnitName)
		u.MovementClass = "testmove"
	}
	return cat
}

func strictMinimalTerrain() *world.Terrain {
	attrs := make([]formats.TNTAttribute, 32*32)
	for i := range attrs {
		attrs[i] = formats.TNTAttribute{Height: 10, Feature: world.PlotFeatureNone}
	}
	plot := world.ExpandPlot(attrs, 32, 32)
	ter := &world.Terrain{
		CellW: 32, CellH: 32,
		Plot: plot, Version: 0x2000, SeaLevel: 0, WindMin: 100, WindMax: 2000,
	}
	_ = ter.ApplySchema(nil, 0)
	return ter
}

func strictSyntheticMission() *mission.Mission {
	return &mission.Mission{
		Type: mission.TypeSkirmish, TerrainKey: "test",
		Schema:     mission.Schema{Name: "Schema 0"},
		WindBounds: mission.WindBounds{Min: 100, Max: 200},
	}
}

func strictEconomyForTest() *economy.Service {
	return &economy.Service{}
}

// strictNewSessionWithUnits builds a strict session for N units distributed across players 0,1.
func strictNewSessionWithUnits(nUnits int, simSeed, crtSeed uint32) *Session {
	cat := strictMinimalCatalog()
	terrain := strictMinimalTerrain()
	m := strictSyntheticMission()
	s := &Session{
		Catalog: cat, World: terrain, Mission: m,
		Clock:    &clock.State{Requested: 10, Active: 10},
		Kernel:   &kernel.Kernel{},
		Snapshot: &snapshot.Buffer{},
	}
	w, _ := newSlicedWorld(cat)
	s.Units = w
	s.Econ = strictEconomyForTest()
	for i := 0; i < 2; i++ {
		p := &s.Econ.Players[i]
		p.Exists = true
		p.ControllerState = uint8(i + 1)
		p.IsObserver = false
		p.StatusHalfwordAt144 = 1
		p.StatusWordAt140 = 0
		p.GameEnded = false
		p.EndGameCountdown = -1
	}
	s.Econ.SeedDeadlines(0)
	var crt rng.CRT = rng.NewCRT(crtSeed)
	s.InitWindForSession(&crt, 0)
	_ = createAndBindServices(s)
	s.RegisterAll()
	s.State = StateBattle
	def := cat.Units["armcom"]
	for i := 0; i < nUnits; i++ {
		owner := uint8(i % 2)
		x := numeric.Fixed(int64((10 + i*5) * 65536))
		z := numeric.Fixed(int64((10 + i*5) * 65536))
		y := terrain.HeightAt(x, z)
		if y == numeric.Fixed(-1) {
			y = 0
		}
		_, _ = s.Units.Create(def, owner, x, y, z)
	}
	ensureMovementForAll(s)
	publishVisibilityForAll(s)
	_ = simSeed
	return s
}

func strictQueueHeadString(handle pool.Handle, s *Session) string {
	if s == nil || s.Units == nil {
		return "nil"
	}
	u := s.Units.Unit(handle)
	if u == nil {
		return "no unit"
	}
	q := orders.QueueForUnit(u)
	if q == nil {
		return "no queue"
	}
	if q.LenPrimary() == 0 {
		if h := q.Head(); h == nil {
			return "empty"
		} else {
			return fmt.Sprintf("head %s Goal%d %d", orders.DescriptorFor(h.ID).Name, int64(h.GoalX.Raw()), int64(h.GoalZ.Raw()))
		}
	}
	head := q.Primary()[0]
	if head == nil {
		return "nil head"
	}
	return fmt.Sprintf("head %s Goal(%d,%d) State%d Deadline%d P1:%d P2:%d", orders.DescriptorFor(head.ID).Name, int64(head.GoalX.Raw()), int64(head.GoalZ.Raw()), head.MoveState, head.Deadline, head.Param1, head.Param2)
}

func strictPathStatus(handle pool.Handle, s *Session) string {
	if s == nil || s.Movement == nil {
		return "no movement"
	}
	if s.Movement.Scheduler != nil && s.Movement.Scheduler.HasRequest(handle) {
		return "request pending"
	}
	route := s.Movement.Routes[handle]
	if route == nil {
		return "no route"
	}
	if !route.Active {
		return fmt.Sprintf("inactive count=%d", route.Count)
	}
	return fmt.Sprintf("active count=%d points %v", route.Count, route.Points[:route.Count])
}

func strictAimState(handle pool.Handle, s *Session) string {
	if s == nil || s.Units == nil {
		return "no session"
	}
	u := s.Units.Unit(handle)
	if u == nil {
		return "no unit"
	}
	for idx := 0; idx < 3; idx++ {
		sl := u.SlotAt(idx)
		if sl == nil || !sl.IsPopulated() {
			continue
		}
		return fmt.Sprintf("slot%d weapon=%v reload=%d flags=0x%x aimReady=%v issue=%v target=%v", idx, sl.Weapon != nil, sl.Reload, sl.Flags, sl.Aim.Ready, sl.Aim.IssueBit, sl.Target)
	}
	return "no weapon"
}

func strictResourceStocks(player int, s *Session) string {
	if s == nil || s.Econ == nil || player < 0 || player >= 10 {
		return "no econ"
	}
	p := &s.Econ.Players[player]
	// Show stocks and carry from first builder bucket if exists
	var carryM, carryE, acceptedM, acceptedE float32
	if s.Units != nil {
		for _, u := range s.Units.IterSliced() {
			if u != nil && int(u.Owner) == player && u.Def != nil && u.Def.Builder {
				if b := s.Econ.UnitBuckets(u.Handle); b != nil {
					carryM = (*b)[economy.Metal].Carry
					carryE = (*b)[economy.Energy].Carry
					acceptedM = (*b)[economy.Metal].Accepted
					acceptedE = (*b)[economy.Energy].Accepted
				}
				break
			}
		}
	}
	return fmt.Sprintf("player%d metal=%.1f energy=%.1f capM=%.1f capE=%.1f carryM=%.3f carryE=%.3f acceptedM=%.1f acceptedE=%.1f", player, p.Stock[economy.Metal], p.Stock[economy.Energy], p.Capacity[economy.Metal], p.Capacity[economy.Energy], carryM, carryE, acceptedM, acceptedE)
}

func strictResultLatch(s *Session) string {
	if s == nil {
		return "no session"
	}
	res := s.GetResult()
	return fmt.Sprintf("ended=%v draw=%v winner=%d reason=%s tick=%d armed=%d latchCountdown=%d bits=0x%x state=%v", res.Ended, res.Draw, res.WinnerTeam, res.Reason, res.Tick, res.ArmedTick, s.Latch.Countdown, s.Latch.Bits, s.State)
}

var _ = strictCommit
var _ = HashTrace
var _ = HashState
