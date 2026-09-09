package main

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/formats"
	"github.com/nanolathe/nanolathe/internal/audio"
	"github.com/nanolathe/nanolathe/internal/camera"
	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/economy"
	"github.com/nanolathe/nanolathe/internal/gui"
	"github.com/nanolathe/nanolathe/internal/input"
	"github.com/nanolathe/nanolathe/internal/mission"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/settings"
	"github.com/nanolathe/nanolathe/internal/sim/rng"
	"github.com/nanolathe/nanolathe/internal/testsupport"
	"github.com/nanolathe/nanolathe/internal/ui"
	"github.com/nanolathe/nanolathe/internal/visibility"
)

type chatMillis struct{ now uint32 }

func (m *chatMillis) Millis32() uint32 {
	m.now += 34
	return m.now
}

type chatOutputConfigSpy struct{ config audio.OutputConfig }

func (*chatOutputConfigSpy) PlaySample(*audio.Sample, float64, float64) error { return nil }
func (s *chatOutputConfigSpy) ConfigureOutput(config audio.OutputConfig)      { s.config = config }

func testTalkWindow() *gui.Window {
	return &gui.Window{
		Rect: gui.Rect{X: 128, Y: 447, W: 512, H: 33}, OriginX: 128, OriginY: 447,
		Gadgets: []gui.Gadget{
			{Kind: gui.KindPanel, Active: 1, Rect: gui.Rect{X: 128, Y: 447, W: 512, H: 33}},
			{Kind: gui.KindTextBox, Name: "TALK", Active: 1, Attribs: 1, Rect: gui.Rect{X: 16, Y: 7, W: 350, H: 19}, MaxChars: 63, ColorF: 15},
			{Kind: gui.KindButton, Name: "SENDTO", Active: 1, Rect: gui.Rect{X: 375, Y: 6, W: 120, H: 20}},
		},
	}
}

func enqueueText(in *input.State, text string) {
	for _, r := range text {
		in.EnqueueToken(input.Token{Kind: input.TokenText, Rune: r})
	}
}

func TestTalkProductionFlowComposeCancelAndOwnInput(t *testing.T) {
	b := newTestBattle(testCatalogON05(), testWorldON05(100, 100))
	b.sess.Econ = &economy.Service{}
	b.sess.Econ.Players[0].Exists = true
	b.sess.Econ.Players[0].Name = "Player"
	b.hud = &retailBattleHUD{talkWin: testTalkWindow()}
	b.millisSource = &chatMillis{}
	b.controller = NewBattleController(b, b.millisSource)
	cl := b.cl
	cl.SetFocused(true)
	in := cl.Input()

	in.EnqueueToken(input.Token{Kind: input.TokenEdit, Key: input.KeyEnter})
	b.viewerStep(0, cl)
	if !b.chat.active || b.hud.talkPanel == nil || !b.hud.talkPanel.EditorCaptured() {
		t.Fatal("unclaimed Enter did not open and focus TALK")
	}
	if b.hud.talkPanel.ActiveAt(b.hud.talkPanel.Index("SENDTO")) {
		t.Fatal("single-player TALK left SENDTO visible")
	}
	enqueueText(in, "hello")
	in.EnqueueToken(input.Token{Kind: input.TokenEdit, Key: input.KeyEnter})
	b.viewerStep(0, cl)
	lines := cl.MessageRing().Visible()
	if len(lines) != 1 || lines[0].Text != "<Player> hello" || lines[0].Class != 4 || lines[0].SourceUnit != 0 || lines[0].SpeakerSlot != 10 {
		t.Fatalf("local TALK line = %+v, want class-4 <Player> hello", lines)
	}
	if b.chat.active || b.hud.talkPanel.TextOf("TALK") != "" {
		t.Fatal("commit did not close and clear TALK")
	}

	in.EnqueueToken(input.Token{Kind: input.TokenEdit, Key: input.KeyEnter})
	b.viewerStep(0, cl)
	enqueueText(in, "discard")
	in.EnqueueToken(input.Token{Kind: input.TokenEdit, Key: input.KeyEscape})
	b.viewerStep(0, cl)
	if got := len(cl.MessageRing().Visible()); got != 1 {
		t.Fatalf("Escape posted a line: visible=%d, want 1", got)
	}

	// TALK owns world presses and held arrows, but the pointer-edge branch of
	// the camera remains live [07 §3][07 §10].
	in.EnqueueToken(input.Token{Kind: input.TokenEdit, Key: input.KeyEnter})
	b.viewerStep(0, cl)
	in.Kbd.SetKey(input.KeyRight, true)
	in.Mouse.SetPosition(639, 200)
	in.Mouse.SetButton(input.MouseButtonLeft, true)
	before := b.cam.X
	b.viewerStep(0, cl)
	if b.cam.X <= before {
		t.Fatal("TALK suppressed pointer-edge camera movement")
	}
	if got := b.sess.PendingHumanCommands(); len(got) != 0 {
		t.Fatalf("TALK leaked world input: %+v", got)
	}
	in.Mouse.ResetEdges()
	in.Mouse.SetPosition(320, 200)
	before = b.cam.X
	b.viewerStep(0, cl)
	if b.cam.X != before {
		t.Fatal("TALK admitted held-arrow camera movement away from an edge")
	}
	in.Mouse.SetButton(input.MouseButtonMiddle, true)
	in.Mouse.SetPosition(340, 200)
	before = b.cam.X
	b.viewerStep(0, cl)
	if b.cam.X != before {
		t.Fatal("TALK admitted middle-drag camera movement")
	}
}

func TestTalkPaintsAboveRetainedUnitInfo(t *testing.T) {
	resetUnitInfoState(t)
	talk := &gui.Window{
		Rect: gui.Rect{X: 2, Y: 2, W: 10, H: 10}, OriginX: 2, OriginY: 2,
		Header: gui.Header{Panel: "talk"}, Gadgets: []gui.Gadget{{Kind: gui.KindPanel, Active: 1}},
	}
	info := &gui.Window{
		Rect: gui.Rect{X: 2, Y: 2, W: 10, H: 10}, OriginX: 2, OriginY: 2,
		Header: gui.Header{Panel: "info"}, Gadgets: []gui.Gadget{{Kind: gui.KindPanel, Active: 1}},
	}
	h := &retailBattleHUD{
		talkWin: talk, talkPanel: ui.NewPanel(talk), screenW: 32, screenH: 24,
		common: &formats.GAF{Entries: []formats.GAFEntry{bindingEntry("talk", 41), bindingEntry("info", 52)}},
	}
	b := &battleSession{hud: h}
	b.chat.active = true
	unitInfoUI = &unitInfoScreen{window: info, panel: ui.NewPanel(info), def: &content.UnitDef{}}
	c := bindingClient(t)
	c.SetUIStage(battleHUDUIStage{hud: h, battle: b})
	if got := c.ComposeFrameSnapshot().Indexed[2*32+2]; got != 41 {
		t.Fatalf("overlap pixel=%d, want newest TALK window 41 above UNITINFO", got)
	}
}

func TestLocalCommandParserAndSessionMasks(t *testing.T) {
	words := tokenizeLocalCommand("  one;two\tthree#ignored four")
	if strings.Join(words, "|") != "one;two|three" {
		t.Fatalf("tokenizer = %q, want semicolon content and # termination", words)
	}
	if got := localCommandInt([]string{"command", "-12junk"}, 1); got != -12 {
		t.Fatalf("integer argument = %d, want signed decimal prefix -12", got)
	}
	many := tokenizeLocalCommand(strings.Repeat("x ", 30))
	if len(many) != localCommandWords {
		t.Fatalf("tokenizer words = %d, want %d", len(many), localCommandWords)
	}

	campaign := &battleSession{sess: &session.Session{Mission: &mission.Mission{Type: mission.TypeCampaign}, Econ: &economy.Service{}}}
	campaign.sess.Econ.Players[0].Exists = true
	campaign.sess.Audio = audio.NewService(nil)
	campaign.sess.Audio.Music.Open(3)
	campaign.dispatchLocalCommand("+NoShake")
	if pending := campaign.sess.PendingHumanCommands(); len(pending) != 1 || pending[0].Kind != session.HumanNoShake {
		t.Fatalf("campaign NoShake = %+v, want one mask-1 command", pending)
	}
	campaign.dispatchLocalCommand("+ATM")
	if got := len(campaign.sess.PendingHumanCommands()); got != 1 {
		t.Fatalf("campaign ATM crossed mask-2 gate: pending=%d", got)
	}
	campaign.dispatchLocalCommand("+DoubleShot")
	campaign.dispatchLocalCommand("+HalfShot ignored")
	if got := len(campaign.sess.PendingHumanCommands()); got != 1 {
		t.Fatalf("campaign damage modes crossed mask-2 gate: pending=%d", got)
	}
	campaign.dispatchLocalCommand("+Radar")
	if campaign.radarOptions != 0 {
		t.Fatalf("campaign Radar options=%#x, want clear", campaign.radarOptions)
	}
	campaign.dispatchLocalCommand("+CDPlay 2junk")
	if got := campaign.sess.Audio.Music.CurTrack(); got != 2 || !campaign.sess.Audio.Music.IsPlaying() {
		t.Fatalf("campaign CDPlay track/status = %d/%v, want 2/playing", got, campaign.sess.Audio.Music.Status())
	}
	campaign.dispatchLocalCommand("+CDStop ignored")
	if got := campaign.sess.Audio.Music.CurTrack(); got != 0 || campaign.sess.Audio.Music.Status() != audio.StatusIdle {
		t.Fatalf("campaign CDStop track/status = %d/%v, want 0/idle", got, campaign.sess.Audio.Music.Status())
	}
	campaign.dispatchLocalCommand("+CDPlay 0")
	if got := campaign.sess.Audio.Music.CurTrack(); got != 1 || !campaign.sess.Audio.Music.IsPlaying() {
		t.Fatalf("CDPlay argument zero track/status = %d/%v, want tick-selected 1/playing", got, campaign.sess.Audio.Music.Status())
	}
	campaign.sess.Audio = audio.NewService(nil)
	campaign.dispatchLocalCommand("+CDPlay 2")
	if got := campaign.sess.Audio.Music.CurTrack(); got != 0 || campaign.sess.Audio.Music.Status() != audio.StatusIdle {
		t.Fatalf("zero-track CDPlay track/status = %d/%v, want 0/idle", got, campaign.sess.Audio.Music.Status())
	}
	cl, err := client.New(client.Options{})
	if err != nil {
		t.Fatal(err)
	}
	campaign.cl = cl
	campaign.sess.Econ.Players[0].Name = "Player"
	campaign.commitLocalChat("+ATM")
	if lines := cl.MessageRing().Visible(); len(lines) != 1 || lines[0].Text != "<Player> +ATM" {
		t.Fatalf("campaign refusal did not retain the typed line: %+v", lines)
	}
	skirmish := &battleSession{sess: &session.Session{Econ: &economy.Service{}}}
	skirmish.sess.Econ.Players[0].Exists = true
	skirmish.dispatchLocalCommand("+ATM")
	if pending := skirmish.sess.PendingHumanCommands(); len(pending) != 1 || pending[0].Kind != session.HumanATM {
		t.Fatalf("skirmish ATM = %+v, want one mask-2 command", pending)
	}
	skirmish.dispatchLocalCommand("+DoubleShot")
	skirmish.dispatchLocalCommand("+HalfShot ignored")
	shotPending := skirmish.sess.PendingHumanCommands()
	if len(shotPending) != 3 || shotPending[1].Kind != session.HumanDoubleShot || shotPending[2].Kind != session.HumanHalfShot {
		t.Fatalf("skirmish damage-mode commands = %+v", shotPending)
	}
	skirmish.dispatchLocalCommand("+Radar ignored")
	if skirmish.radarOptions != radarAllContactsOption {
		t.Fatalf("skirmish Radar options=%#x, want full-radar bit", skirmish.radarOptions)
	}
	skirmish.dispatchLocalCommand("+Radar")
	if skirmish.radarOptions != 0 {
		t.Fatalf("second skirmish Radar options=%#x, want clear", skirmish.radarOptions)
	}
	long := "+" + strings.Repeat("a", 100)
	skirmish.dispatchLocalCommand(long)
	if len(skirmish.chat.lastCommand) != lastCommandBytes {
		t.Fatalf("last command bytes = %d, want %d", len(skirmish.chat.lastCommand), lastCommandBytes)
	}
	resources := &battleSession{sess: &session.Session{LocalOwner: 3}}
	resources.dispatchLocalCommand("+NoMetal")
	resources.dispatchLocalCommand("+NoEnergy 2 -12junk")
	resources.dispatchLocalCommand("+NoMetal 263")
	resources.dispatchLocalCommand("+Selectable")
	pending := resources.sess.PendingHumanCommands()
	if len(pending) != 4 || pending[0].Kind != session.HumanSetResource || pending[0].SetResource.Player != 3 || pending[0].SetResource.Resource != economy.Metal || pending[0].SetResource.Amount != 0 || pending[1].SetResource.Player != 2 || pending[1].SetResource.Resource != economy.Energy || pending[1].SetResource.Amount != -12 || pending[2].SetResource.Player != 7 || pending[2].SetResource.Amount != 0 || pending[3].Kind != session.HumanMakeSelectable {
		t.Fatalf("resource/selectable command payloads = %+v", pending)
	}
	campaignVisibility := &battleSession{sess: &session.Session{Mission: &mission.Mission{Type: mission.TypeCampaign}}}
	for _, command := range []string{"+LOS", "+Mapping", "+NowISee", "+LOSType"} {
		campaignVisibility.dispatchLocalCommand(command)
	}
	pending = campaignVisibility.sess.PendingHumanCommands()
	if len(pending) != 1 || pending[0].Kind != session.HumanVisibility || pending[0].Visibility.ToggleMask != visibility.ModeTerrainRay {
		t.Fatalf("campaign visibility commands = %+v, want LOSType only", pending)
	}
	skirmishVisibility := &battleSession{sess: &session.Session{}}
	for _, command := range []string{"+LOS", "+Mapping", "+LOSType", "+NowISee"} {
		skirmishVisibility.dispatchLocalCommand(command)
	}
	pending = skirmishVisibility.sess.PendingHumanCommands()
	if len(pending) != 4 || pending[0].Visibility.ToggleMask != visibility.ModeCurrentEnabled || pending[1].Visibility.ToggleMask != visibility.ModeHistoryEnabled || pending[2].Visibility.ToggleMask != visibility.ModeTerrainRay || pending[3].Visibility.ClearMask != visibility.ModeHistoryEnabled|visibility.ModeCurrentEnabled {
		t.Fatalf("skirmish visibility commands = %+v", pending)
	}
}

func TestCorePresentationCommandsPersistExactValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	t.Setenv(settings.EnvPath, path)
	s := settings.Defaults()
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	cl, err := client.New(client.Options{})
	if err != nil {
		t.Fatal(err)
	}
	b := &battleSession{cl: cl}
	b.dispatchLocalCommand("+SwitchAlt")
	stored, err := settings.Load()
	if err != nil || !b.switchAlt || stored.SwitchAlt != 1 {
		t.Fatalf("SwitchAlt toggle runtime=%t stored=%d err=%v", b.switchAlt, stored.SwitchAlt, err)
	}
	b.dispatchLocalCommand("+SwitchAlt 2")
	stored, _ = settings.Load()
	if b.switchAlt || stored.SwitchAlt != 1 {
		t.Fatalf("explicit SwitchAlt runtime=%t stored=%d, want clear without persistence", b.switchAlt, stored.SwitchAlt)
	}
	b.dispatchLocalCommand("+ScreenChat")
	b.dispatchLocalCommand("+ScrollSpeed 0")
	b.dispatchLocalCommand("+IFace 7")
	stored, err = settings.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cl.ScreenChat() != 0 || stored.Messages.ScreenChat != 0 || b.scrollSetting() != 0 || stored.ScrollSpeed != 0 || b.interfaceType != 7 || stored.InterfaceType != settings.InterfaceTypeRightClick {
		t.Fatalf("stored core values: screen=%d/%d scroll=%d/%d iface=%d/%d", cl.ScreenChat(), stored.Messages.ScreenChat, b.scrollSetting(), stored.ScrollSpeed, b.interfaceType, stored.InterfaceType)
	}
	previousOutput := audio.GlobalOutput()
	t.Cleanup(func() {
		audio.ConfigureOutput(audio.OutputConfig{MasterEnabled: true, EffectsVolume: 1, SoundMode: audio.SoundModeMono, MixingBuffers: settings.DefaultMixingBuffers})
		audio.SetGlobalOutput(previousOutput)
	})
	audio.SetGlobalOutput(nil)
	wantOutput := audio.OutputConfig{MasterEnabled: false, EffectsVolume: 0.25, SoundMode: audio.SoundModeMono, MixingBuffers: 3}
	audio.ConfigureOutput(wantOutput)
	output := &chatOutputConfigSpy{}
	audio.SetGlobalOutput(output)
	storedAudio := stored.Audio
	b.dispatchLocalCommand("+Sound3D")
	wantOutput.SoundMode = audio.SoundMode3D
	stored, err = settings.Load()
	if err != nil || output.config != wantOutput || stored.Audio != storedAudio {
		t.Fatalf("Sound3D live/stored = %#v/%+v, want %#v/%+v err=%v", output.config, stored.Audio, wantOutput, storedAudio, err)
	}
	b.dispatchLocalCommand("+Sound3D")
	wantOutput.SoundMode = audio.SoundModeMono
	if output.config != wantOutput {
		t.Fatalf("second Sound3D toggle = %#v, want %#v", output.config, wantOutput)
	}
	directSetup := session.SkirmishConfig{LineOfSight: 0, Mapping: 1, LOSType: 0}
	b.sess = &session.Session{Skirmish: directSetup}
	b.dispatchLocalCommand("+LOSType")
	b.dispatchLocalCommand("+NowISee")
	stored, err = settings.Load()
	if err != nil || b.sess.Skirmish != directSetup || stored.Skirmish.LineOfSight != 1 || stored.Skirmish.Mapping != 1 || stored.Skirmish.LOSType != 1 {
		t.Fatalf("non-writing direct visibility changed setup/settings: setup=%+v stored=%+v err=%v", b.sess.Skirmish, stored.Skirmish, err)
	}
	b.dispatchLocalCommand("+LOS")
	b.dispatchLocalCommand("+Mapping")
	b.dispatchLocalCommand("+LOS")
	b.dispatchLocalCommand("+Mapping")
	stored, err = settings.Load()
	if err != nil || b.sess.Skirmish != directSetup || stored.Skirmish.LineOfSight != directSetup.LineOfSight || stored.Skirmish.Mapping != directSetup.Mapping || stored.Skirmish.LOSType != directSetup.LOSType {
		t.Fatalf("direct visibility write changed setup or serialized live flags: setup=%+v stored=%+v err=%v", b.sess.Skirmish, stored.Skirmish, err)
	}
	beforeRadar, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	b.dispatchLocalCommand("+Radar")
	afterRadar, err := os.ReadFile(path)
	if err != nil || b.radarOptions != radarAllContactsOption || string(afterRadar) != string(beforeRadar) {
		t.Fatalf("Radar live/no-save = %#x/%t err=%v", b.radarOptions, string(afterRadar) == string(beforeRadar), err)
	}

	// Direct map entry keeps the unsaved TShadow/FShadow bits live until the
	// next write-all command, which then captures them beside its own bit.
	if err := settings.Defaults().Save(); err != nil {
		t.Fatal(err)
	}
	b.dispatchLocalCommand("+TShadow")
	b.dispatchLocalCommand("+FShadow")
	master, vehicle, shading := cl.ShadowOptions()
	stored, err = settings.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !master || vehicle || !shading || cl.FeatureShadows() || stored.Display.VehicleShadows != 1 || stored.Display.FeatureShadows != 1 {
		t.Fatalf("direct unsaved shadows: live=%t/%t/%t/%t stored=%d/%d", master, vehicle, shading, cl.FeatureShadows(), stored.Display.VehicleShadows, stored.Display.FeatureShadows)
	}
	b.dispatchLocalCommand("+AntiAlias")
	stored, err = settings.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cl.AntiAlias() || stored.Display.AntiAlias != 0 || stored.Display.Shadows != 1 || stored.Display.VehicleShadows != 0 || stored.Display.FeatureShadows != 0 || stored.Display.Shading != 1 {
		t.Fatalf("direct visual write = live AA %t, stored %+v", cl.AntiAlias(), stored.Display)
	}

	// The shell path retains the same independent bits in its live display
	// block. TShadow/FShadow do not write immediately; Shading does and carries
	// the two earlier changes through the normal whole-settings save.
	if err := settings.Defaults().Save(); err != nil {
		t.Fatal(err)
	}
	shellClient, err := client.New(client.Options{})
	if err != nil {
		t.Fatal(err)
	}
	shell := &gameShell{display: settings.DefaultDisplay(), settingsWritable: true}
	shellBattle := &battleSession{cl: shellClient, shell: shell}
	shellBattle.dispatchLocalCommand("+TShadow")
	shellBattle.dispatchLocalCommand("+FShadow")
	stored, err = settings.Load()
	if err != nil {
		t.Fatal(err)
	}
	if shell.display.VehicleShadows != 0 || shell.display.FeatureShadows != 0 || stored.Display.VehicleShadows != 1 || stored.Display.FeatureShadows != 1 {
		t.Fatalf("shell unsaved shadows: live=%+v stored=%+v", shell.display, stored.Display)
	}
	shellBattle.dispatchLocalCommand("+Shading")
	stored, err = settings.Load()
	if err != nil {
		t.Fatal(err)
	}
	master, vehicle, shading = shellClient.ShadowOptions()
	if !master || vehicle || shading || shellClient.FeatureShadows() || stored.Display.Shadows != 1 || stored.Display.VehicleShadows != 0 || stored.Display.FeatureShadows != 0 || stored.Display.Shading != 0 {
		t.Fatalf("shell visual write: live=%t/%t/%t/%t stored=%+v", master, vehicle, shading, shellClient.FeatureShadows(), stored.Display)
	}
	shellBattle.dispatchLocalCommand("+Shadow")
	if master, vehicle, shading = shellClient.ShadowOptions(); master || vehicle || shading {
		t.Fatalf("Shadow changed sibling bits: live=%t/%t/%t", master, vehicle, shading)
	}
	if err := settings.Defaults().Save(); err != nil {
		t.Fatal(err)
	}
	shell.setup.LineOfSight, shell.setup.Mapping, shell.setup.LOSType = 0, 1, 0
	shellSetup := shell.setup
	shellSessionSetup := session.SkirmishConfig{LineOfSight: 1, Mapping: 0, LOSType: 1}
	shellBattle.sess = &session.Session{Skirmish: shellSessionSetup}
	shellBattle.dispatchLocalCommand("+LOSType")
	shellBattle.dispatchLocalCommand("+NowISee")
	stored, err = settings.Load()
	if err != nil || shell.setup != shellSetup || shellBattle.sess.Skirmish != shellSessionSetup || stored.Skirmish.LineOfSight != 1 || stored.Skirmish.Mapping != 1 || stored.Skirmish.LOSType != 1 {
		t.Fatalf("non-writing shell visibility changed setup/settings: shell=%+v session=%+v stored=%+v err=%v", shell.setup, shellBattle.sess.Skirmish, stored.Skirmish, err)
	}
	shellBattle.dispatchLocalCommand("+LOS")
	shellBattle.dispatchLocalCommand("+Mapping")
	shellBattle.dispatchLocalCommand("+LOS")
	shellBattle.dispatchLocalCommand("+Mapping")
	stored, err = settings.Load()
	if err != nil || shell.setup != shellSetup || shellBattle.sess.Skirmish != shellSessionSetup || stored.Skirmish.LineOfSight != shellSetup.LineOfSight || stored.Skirmish.Mapping != shellSetup.Mapping || stored.Skirmish.LOSType != shellSetup.LOSType {
		t.Fatalf("shell visibility write changed setup or serialized live flags: shell=%+v session=%+v stored=%+v err=%v", shell.setup, shellBattle.sess.Skirmish, stored.Skirmish, err)
	}
}

// TestRetailTalkCapture writes the installed TALK.GUI over a real battle when
// NANOLATHE_TALK_SHOT names an output PNG. It is opt-in visual evidence; the
// ordinary behavioral assertions above stay asset-free and fast.
func TestRetailTalkCapture(t *testing.T) {
	path := os.Getenv("NANOLATHE_TALK_SHOT")
	if path == "" {
		t.Skip("set NANOLATHE_TALK_SHOT to capture TALK.GUI")
	}
	root := testsupport.RetailRoot(t)
	opts := Options{Root: root, Map: "ashap plateau", Seed: 1}
	cs, err := openContent(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	shell, err := newGameShell(opts, cs)
	if err != nil {
		t.Fatal(err)
	}
	rng.SeedGlobal(1, 1)
	sess, cat, err := newBattleSession(opts, cs)
	if err != nil {
		t.Fatal(err)
	}
	for step := int32(1); step <= 30 && sess.Snapshot.Current() == nil; step++ {
		sess.Step(step)
	}
	if sess.Snapshot.Current() == nil {
		t.Fatal("battle did not publish a snapshot")
	}
	const width, height = 640, 480
	cam := &camera.Camera{ViewW: width, ViewH: height, MapW: int32(sess.World.CellW * 16), MapH: int32(sess.World.CellH * 16)}
	centerBattleStartCamera(sess, cam)
	b := &battleSession{sess: sess, cat: cat, cam: cam, shell: shell}
	pal := retailPaletteForTest(t, cs)
	b.hud, err = loadRetailBattleHUD(cs.fs, sess, cat, pal, shell, newBattleWindowContext(cs, nil))
	if err != nil {
		t.Fatal(err)
	}
	cl, err := client.New(client.Options{Buffer: sess.Snapshot, Width: width, Height: height})
	if err != nil {
		t.Fatal(err)
	}
	cl.SetTerrain(sess.World)
	cl.SetCamera(cam)
	cl.SetPalette(pal)
	cl.SetFNT(b.hud.console)
	cl.SetModelFS(cs.fs)
	cl.SetUIStage(battleHUDUIStage{hud: b.hud, battle: b})
	b.cl = cl
	if !b.openTalk(cl.Input(), cl) {
		t.Fatal("installed TALK.GUI did not open")
	}
	b.hud.talkPanel.SetText("TALK", "Local chat and commands")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, cl.ComposeFrame()); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
