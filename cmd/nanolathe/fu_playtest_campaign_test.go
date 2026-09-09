//go:build retail

package main

// Scenario 2 of the fu-playtest acceptance round: a campaign sequence driven
// through the retail front end in-process — MAINMENU -> SINGLE -> NEWGAME ->
// MSNBRIEF -> loading -> battle -> ENDMSN -> MSNBRIEF -> loading -> battle —
// exercising the mission's own authored triggers, plus the visibility mode
// word derived for missions that author a non-default mapping/lineofsight
// pair [08 R-CAMP-01 §2] [08 R-CAMP-01 §8] [08 "Victory and defeat triggers"]
// [08 R-SKIR-01 §4] [03 R-VIS-01 §1].

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/mission"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/triggers"
	"github.com/nanolathe-gg/nanolathe/internal/ui"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/internal/visibility"
)

const fuArmCampaign = "Arm Campaign"

type fuTriggerNote struct {
	Kind string   `json:"kind"`
	Type string   `json:"type"`
	Args [3]int32 `json:"args"`
	Done bool     `json:"completed"`
}

type fuBattleNote struct {
	Label         string          `json:"label"`
	CampaignPath  string          `json:"campaign_path"`
	CampaignIndex int             `json:"campaign_index"`
	MissionName   string          `json:"mission_name"`
	TerrainKey    string          `json:"terrain_key"`
	Difficulty    int             `json:"difficulty"`
	VisMode       uint8           `json:"vis_mode"`
	Mapping       int32           `json:"mapping"`
	LineOfSight   int32           `json:"lineofsight"`
	Victory       []fuTriggerNote `json:"victory"`
	Defeat        []fuTriggerNote `json:"defeat"`
	Opening       fuCensus        `json:"opening"`
	EndPath       string          `json:"end_path"`
	EndTick       uint32          `json:"end_tick"`
	Result        session.Result  `json:"result"`
	VictoryDone   bool            `json:"victory_done"`
	DefeatDone    bool            `json:"defeat_done"`
	TriggersAtEnd []fuTriggerNote `json:"triggers_at_end"`
	NextMission   int             `json:"next_mission"`
	NextIsRetry   bool            `json:"next_is_retry"`
	Notes         []string        `json:"notes"`
}

func fuTriggerNotesOf(list []*triggers.Trigger) []fuTriggerNote {
	var out []fuTriggerNote
	for _, tr := range list {
		if tr != nil {
			out = append(out, fuTriggerNote{Kind: tr.Kind.String(), Type: tr.Type, Args: tr.Args, Done: tr.Completed})
		}
	}
	return out
}

func fuDescribeMission(b *battleSession, label string) fuBattleNote {
	sess := b.sess
	m := sess.Mission
	n := fuBattleNote{Label: label}
	if m != nil {
		n.CampaignPath, n.CampaignIndex, n.MissionName, n.TerrainKey, n.Difficulty = m.CampaignPath, m.CampaignIndex, m.CampaignMissionName, m.TerrainKey, m.Difficulty
		n.Victory = fuTriggerNotesOf(m.Victory)
		n.Defeat = fuTriggerNotesOf(m.Defeat)
		if m.OTA != nil {
			g := mission.DecodeMissionGlobals(m.OTA.Global)
			n.Mapping, n.LineOfSight = g.Mapping, g.LineOfSight
		}
	}
	if sess.Vis != nil {
		n.VisMode = uint8(sess.Vis.Mode())
	}
	return n
}

func fuRecordEnd(note *fuBattleNote, sess *session.Session) {
	note.EndTick = sess.Clock.GlobalTick
	note.Result = sess.GetResult()
	note.VictoryDone, note.DefeatDone = sess.VictoryDone, sess.DefeatDone
	if sess.Mission != nil {
		note.TriggersAtEnd = append(fuTriggerNotesOf(sess.Mission.Victory), fuTriggerNotesOf(sess.Mission.Defeat)...)
	}
}

func fuEnded(b *battleSession) bool { return b.sess.State == session.StatePostBattle }

// fuWinByLocationTrigger sends every local mobile unit the authored
// move-unit-to-radius victory condition can accept to that point and waits
// for the result. The evaluator completes on any owner-0 unit of the
// matching type — an empty type is the ANYTYPE wildcard — that is complete,
// classifier-eligible and inside the radius [08 "Victory trigger types"]
// [08 R-TRIG-01 §2] [08 R-TRIG-01 §3]. Ordering the whole group rather than
// one runner is what makes the step robust: which single unit reaches the
// point first is an outcome of the enemy picket's replans, not a contract,
// and a lone runner boxed in and killed on the way is a legitimate timeline
// (feature-lifecycle changes on main moved exactly that). It returns false
// when the mission authors no such condition, no unit can satisfy it, or
// nobody reached it within the bound.
func fuWinByLocationTrigger(f *fuShell, b *battleSession, note *fuBattleNote) bool {
	sess := b.sess
	if sess.Mission == nil {
		return false
	}
	var target *triggers.Trigger
	for _, tr := range sess.Mission.Victory {
		if tr != nil && tr.Kind == triggers.KindMoveUnitToRadius {
			target = tr
			break
		}
	}
	if target == nil {
		note.Notes = append(note.Notes, "no move-unit-to-radius victory condition is authored")
		return false
	}
	gx, gz := target.Args[0], target.Args[1]
	local := sess.LocalOwner
	var escort []pool.Handle
	var names []string
	for _, u := range fuUnitsOwnedBy(sess, local) {
		if !fuMobile(sess, u) || u.Def == nil || u.Remaining != 0 || (target.Type != "" && !strings.EqualFold(target.Type, u.Def.UnitName)) {
			continue
		}
		escort = append(escort, u.Handle)
		names = append(names, fmt.Sprintf("%s(%d)@%d,%d", u.Def.UnitName, u.Handle, int32(u.X>>16), int32(u.Z>>16)))
	}
	if len(escort) == 0 {
		note.Notes = append(note.Notes, "no local mobile unit can satisfy the location condition")
		return false
	}
	wx, wz := numeric.Fixed(int64(gx)<<16), numeric.Fixed(int64(gz)<<16)
	wy := sess.World.HeightAt(wx, wz)
	if err := sess.EnqueueHumanCommand(session.HumanCommand{Kind: session.HumanOrder, Order: session.HumanOrderCommand{
		Handles: escort, Code: 1, Position: orders.ResolvePos{X: wx, Y: wy, Z: wz},
	}}); err != nil {
		f.t.Fatal(err)
	}
	note.Notes = append(note.Notes, fmt.Sprintf("%d local mobile units ordered to the authored point %d,%d radius %d: %s", len(escort), gx, gz, target.Args[2], strings.Join(names, " ")))
	ok := f.stepUntil(12000, fuEnded)
	// Fates are read from the session by handle: a freed slot reads as dead,
	// never through a pointer taken before the run.
	var fates []string
	for _, h := range escort {
		u := sess.Units.Unit(h)
		switch {
		case u == nil || !u.Alive:
			fates = append(fates, fmt.Sprintf("%d dead", h))
		default:
			fates = append(fates, fmt.Sprintf("%d %s alive at %d,%d", h, u.Def.UnitName, int32(u.X>>16), int32(u.Z>>16)))
		}
	}
	note.Notes = append(note.Notes, fmt.Sprintf("escort fates at tick %d (trigger completed=%v): %s", sess.Clock.GlobalTick, target.Completed, strings.Join(fates, "; ")))
	return ok
}

// fuLoseByAuthoredTrigger destroys the units an authored all-units-killed-of-
// type defeat condition names, or every local unit when none is authored, and
// waits for the result [08 "Defeat trigger types"].
func fuLoseByAuthoredTrigger(f *fuShell, b *battleSession, note *fuBattleNote) bool {
	sess := b.sess
	local := sess.LocalOwner
	typ := ""
	if sess.Mission != nil {
		for _, tr := range sess.Mission.Defeat {
			if tr != nil && tr.Kind == triggers.KindAllUnitsKilledOfType && tr.Type != "" && fuFindUnit(sess, local, fuIsDef(tr.Type)) != nil {
				typ = tr.Type
				break
			}
		}
	}
	destroyed := 0
	for _, u := range fuUnitsOwnedBy(sess, local) {
		if typ == "" || strings.EqualFold(u.Def.UnitName, typ) {
			sess.Units.DestroyBy(u.Handle, units.DeathKilled, 0)
			destroyed++
		}
	}
	if typ == "" {
		note.EndPath = fmt.Sprintf("all %d local units destroyed (default defeat condition)", destroyed)
	} else {
		note.EndPath = fmt.Sprintf("%d local %s destroyed (authored AllUnitsKilledOfType)", destroyed, typ)
	}
	return f.stepUntil(3000, fuEnded)
}

// fuRouteResult pumps ENDMSN and presses its Start control, which is the
// successor mission after a win and the same mission after a loss
// [08 R-CAMP-01 §8].
func fuRouteResult(f *fuShell, b *battleSession, note *fuBattleNote) {
	t := f.t
	if !f.finishToEndMission(b) {
		state := -1
		if b.postBattle != nil {
			state = int(b.postBattle.State())
		}
		t.Fatalf("%s: the post-battle sequence never reached ENDMSN (state %d)", note.Label, state)
	}
	next, ok := b.postBattle.NextMission()
	if !ok {
		t.Fatalf("%s: ENDMSN offers no Start route: %+v", note.Label, b.postBattle.Summary())
	}
	note.NextMission = next
	note.NextIsRetry = next == note.CampaignIndex
	b.doResultAction(ui.ResultActionContinue, f.cl)
	if f.shell.battle != nil {
		t.Fatalf("%s: Start left the battle installed", note.Label)
	}
	if f.shell.briefing == nil {
		t.Fatalf("%s: Start did not open the successor briefing", note.Label)
	}
}

func TestFUPlaytestCampaignSequence(t *testing.T) {
	f := fuNewShell(t, 7)
	shell := f.shell
	dir := fuOutDir(t)
	var notes []fuBattleNote
	defer func() { fuWriteJSON(t, dir, "campaign-sequence.json", notes) }()

	shell.activateGadget("SINGLE")
	shell.activateGadget("NewCamp")
	if shell.frontend.Mode != modeMenuMission {
		t.Fatalf("NewCamp -> mode %v, want NEWGAME", shell.frontend.Mode)
	}
	for i, c := range shell.campaignOptions {
		if strings.EqualFold(c.Name, fuArmCampaign) {
			shell.campaignIdx = i
			break
		}
	}
	if shell.campaignIdx < 0 || shell.campaignIdx >= len(shell.campaignOptions) {
		t.Skip("no campaign is listed on NEWGAME")
	}
	campaign := shell.campaignOptions[shell.campaignIdx]
	if len(campaign.Missions) < 2 {
		t.Skipf("campaign %q has no successor mission", campaign.Name)
	}
	shell.missionIdx = 0
	shell.activateGadget("Start")
	if shell.briefing == nil {
		t.Fatal("NEWGAME Start did not open MSNBRIEF")
	}
	shell.dispatchBriefing(BriefingActionStart)
	b1 := f.waitBattle("first mission")

	// Battle 1: the initial orders, then the authored location victory.
	n1 := fuDescribeMission(b1, "battle1")
	f.step(30)
	n1.Opening = fuTakeCensus(b1.sess)
	f.step(270)
	if fuWinByLocationTrigger(f, b1, &n1) {
		n1.EndPath = "authored MoveUnitToRadius victory condition"
	} else {
		t.Errorf("battle1: the authored location victory condition did not end the mission: %v", n1.Notes)
		if !fuLoseByAuthoredTrigger(f, b1, &n1) {
			fuRecordEnd(&n1, b1.sess)
			notes = append(notes, n1)
			t.Fatalf("battle1 reached no result: %+v", n1)
		}
	}
	fuRecordEnd(&n1, b1.sess)
	fuRouteResult(f, b1, &n1)
	notes = append(notes, n1)
	t.Logf("battle1 %q -> %s (%s) at tick %d via %s; next mission %d (retry=%v)", n1.MissionName, n1.Result.Kind, n1.Result.Reason, n1.EndTick, n1.EndPath, n1.NextMission, n1.NextIsRetry)
	if won := strings.EqualFold(n1.Result.Kind, "victory"); won == n1.NextIsRetry {
		t.Errorf("battle1 result %q routed to mission %d from %d [08 R-CAMP-01 §8]", n1.Result.Kind, n1.NextMission, n1.CampaignIndex)
	}

	// Battle 2 in the same process: the route ENDMSN chose, ended by a defeat.
	shell.dispatchBriefing(BriefingActionStart)
	b2 := f.waitBattle("second battle")
	if b2 == b1 {
		t.Fatal("the second battle is the first battle's object")
	}
	n2 := fuDescribeMission(b2, "battle2")
	if n2.CampaignIndex != n1.NextMission {
		t.Fatalf("second battle is mission %d, ENDMSN selected %d", n2.CampaignIndex, n1.NextMission)
	}
	f.step(30)
	n2.Opening = fuTakeCensus(b2.sess)
	f.step(270)
	if b2.sess.Clock.GlobalTick < 300 {
		t.Fatalf("second battle did not advance: tick %d", b2.sess.Clock.GlobalTick)
	}
	if !fuLoseByAuthoredTrigger(f, b2, &n2) {
		fuRecordEnd(&n2, b2.sess)
		notes = append(notes, n2)
		t.Fatalf("battle2: %s armed no defeat within 3000 ticks (victory=%v defeat=%v)", n2.EndPath, b2.sess.VictoryDone, b2.sess.DefeatDone)
	}
	fuRecordEnd(&n2, b2.sess)
	fuRouteResult(f, b2, &n2)
	notes = append(notes, n2)
	t.Logf("battle2 %q -> %s (%s) at tick %d via %s; next mission %d (retry=%v)", n2.MissionName, n2.Result.Kind, n2.Result.Reason, n2.EndTick, n2.EndPath, n2.NextMission, n2.NextIsRetry)
	if !n2.NextIsRetry {
		t.Errorf("battle2 result %q did not route to a retry: next %d from %d", n2.Result.Kind, n2.NextMission, n2.CampaignIndex)
	}

	// Battle 3: the retry, started and stepped.
	shell.dispatchBriefing(BriefingActionStart)
	b3 := f.waitBattle("third battle")
	n3 := fuDescribeMission(b3, "battle3")
	if n3.CampaignIndex != n2.NextMission {
		t.Fatalf("third battle is mission %d, ENDMSN selected %d", n3.CampaignIndex, n2.NextMission)
	}
	f.step(60)
	n3.Opening = fuTakeCensus(b3.sess)
	n3.EndPath = "left running"
	fuRecordEnd(&n3, b3.sess)
	notes = append(notes, n3)
	t.Logf("battle3 %q running at tick %d", n3.MissionName, b3.sess.Clock.GlobalTick)
}

// TestFUPlaytestCampaignGateDefeatTrigger exercises AC01's authored
// AllUnitsKilledOfType defeat condition directly on the session: losing the
// named structure ends the mission in defeat even though the commander lives
// [08 "Defeat trigger types"] [08 "Evaluation"].
func TestFUPlaytestCampaignGateDefeatTrigger(t *testing.T) {
	root := probeRetail(t)
	cs, err := openContent(Options{Root: root})
	if err != nil {
		t.Skipf("retail assets unavailable: %v", err)
	}
	defer cs.Close()
	selector := fmt.Sprintf("camps/%s.tdf:MISSION0", fuArmCampaign)
	sess, err := session.NewMissionWithProgressSeeds(cs.fs, nil, selector, 0, 7, 7, nil)
	if err != nil {
		t.Skipf("%s unavailable: %v", selector, err)
	}
	var typ string
	for _, tr := range sess.Mission.Defeat {
		if tr != nil && tr.Kind == triggers.KindAllUnitsKilledOfType && tr.Type != "" {
			typ = tr.Type
		}
	}
	if typ == "" {
		t.Skipf("%s authors no AllUnitsKilledOfType defeat condition: %+v", selector, fuTriggerNotesOf(sess.Mission.Defeat))
	}
	fuAdvance(sess, 60, nil)
	local := sess.LocalOwner
	gate := fuFindUnit(sess, local, fuIsDef(typ))
	if gate == nil {
		t.Fatalf("the local player owns no %s to lose", typ)
	}
	commander := fuFindUnit(sess, local, func(u *units.Unit) bool { return u.Def.Commander })
	sess.Units.DestroyBy(gate.Handle, units.DeathKilled, 0)
	fuAdvance(sess, sess.Clock.GlobalTick+900, nil)
	result := sess.GetResult()
	t.Logf("%s lost at tick 60: state %s, defeat=%v victory=%v, result %s (%s) at tick %d, commander alive=%v",
		typ, sess.State.String(), sess.DefeatDone, sess.VictoryDone, result.Kind, result.Reason, result.Tick, commander != nil && commander.Alive)
	if sess.State != session.StatePostBattle || !sess.DefeatDone || !strings.EqualFold(result.Kind, "defeat") {
		t.Fatalf("losing %s did not end the mission in defeat: state %s defeat=%v result %+v", typ, sess.State.String(), sess.DefeatDone, result)
	}
}

// TestFUPlaytestCampaignNondefaultVisibility locks the campaign visibility
// mode word for three missions: the authored fixture (mapping=0,
// lineofsight=0), the one stock mission that authors mapping=0 with
// lineofsight=1, and the stock Arm MISSION0 (1/1). The fixture is a loose
// overlay mounted above every retail tier, the way --remaster mounts one,
// and shadows only SHERWOOD.ota inside this filesystem [02 §2].
func TestFUPlaytestCampaignNondefaultVisibility(t *testing.T) {
	root := probeRetail(t)
	cs, err := openContent(Options{Root: root})
	if err != nil {
		t.Skipf("retail assets unavailable: %v", err)
	}
	defer cs.Close()
	overlay, err := filepath.Abs(filepath.Join("..", "..", "probes", "fu-playtest", "testdata", "vis0"))
	if err != nil {
		t.Fatal(err)
	}
	if err := cs.fs.MountDirectory(overlay, remasterPriority); err != nil {
		t.Fatalf("mount overlay %s: %v", overlay, err)
	}
	cases := []struct {
		name     string
		selector string
		mapping  int32
		los      int32
		want     visibility.Mode
	}{
		{"authored Mapped+Permanent", "camps/fu_playtest_vis.tdf:MISSION0", 0, 0, visibility.ModeTerrainRay},
		{"stock Jungle Journey Mapped+True", "camps/battle tactics - core short.tdf:MISSION5", 0, 1, visibility.ModeCurrentEnabled | visibility.ModeTerrainRay},
		{"stock Arm MISSION0 Unmapped+True", "camps/Arm Campaign.tdf:MISSION0", 1, 1, visibility.ModeHistoryEnabled | visibility.ModeCurrentEnabled | visibility.ModeTerrainRay},
	}
	type row struct {
		Name, Selector, Terrain string
		Mapping, LineOfSight    int32
		ModeRaw, Mode, Want     uint8
		Units                   int
		Err                     string
	}
	var rows []row
	for _, c := range cases {
		r := row{Name: c.name, Selector: c.selector, Want: uint8(c.want)}
		sess, err := session.NewMissionWithProgressSeeds(cs.fs, nil, c.selector, 0, 7, 7, nil)
		if err != nil {
			r.Err = err.Error()
			rows = append(rows, r)
			t.Errorf("%s: compose %q: %v", c.name, c.selector, err)
			continue
		}
		g := mission.DecodeMissionGlobals(sess.Mission.OTA.Global)
		r.Terrain, r.Mapping, r.LineOfSight = sess.Mission.TerrainKey, g.Mapping, g.LineOfSight
		r.ModeRaw = uint8(sess.Vis.Mode())
		r.Mode = uint8(sess.Vis.Mode() &^ visibility.ModeFogCacheValid)
		r.Units = len(sess.Units.Iter())
		fuAdvance(sess, 60, nil)
		rows = append(rows, r)
		if r.Mapping != c.mapping || r.LineOfSight != c.los {
			t.Errorf("%s: OTA authors mapping=%d lineofsight=%d, want %d/%d", c.name, r.Mapping, r.LineOfSight, c.mapping, c.los)
		}
		if visibility.Mode(r.Mode) != c.want {
			t.Errorf("%s: session.Vis mode word = %#x, want %#x [03 R-VIS-01 §1]", c.name, r.Mode, uint8(c.want))
		}
		t.Logf("%s: terrain %q mapping=%d lineofsight=%d mode=%#x (raw %#x) units=%d tick=%d", c.name, r.Terrain, r.Mapping, r.LineOfSight, r.Mode, r.ModeRaw, r.Units, sess.Clock.GlobalTick)
	}
	fuWriteJSON(t, fuOutDir(t), "campaign-visibility-modes.json", rows)
}
