package session

import (
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/frame"
)

// PostBattleState is the small presentation-state word used by the retail
// results handler.  These values intentionally remain separate from Session's
// eight authoritative lifecycle states: the handler is entered at state 7 and
// returns to the front-end router only after its own sequence is complete
// [08 R-CAMP-01 §6].
type PostBattleState uint8

const (
	PostBattleEntry      PostBattleState = 0
	PostBattleWaitDialog PostBattleState = 1
	PostBattleFadeSetup  PostBattleState = 2
	PostBattleFade       PostBattleState = 3
	PostBattleCDCheck    PostBattleState = 4
	PostBattleOutcome    PostBattleState = 5
	PostBattleGlamour    PostBattleState = 6
	PostBattleEndMission PostBattleState = 7
	PostBattleCDIdle     PostBattleState = 8
)

// PostBattleSessionKind selects the result route. Campaign is the only kind
// allowed to mutate campaign marks or expose a successor [08 R-CAMP-01 §6–8].
type PostBattleSessionKind uint8

const (
	PostBattleCampaign PostBattleSessionKind = 1
	PostBattleSkirmish PostBattleSessionKind = 2
	PostBattleNetwork  PostBattleSessionKind = 3
)

// PostBattleControl is a typed semantic input. There is deliberately no
// string-to-action adapter here; an authored control must be admitted by the
// current handler state before it can have an effect [07 §11].
type PostBattleControl uint8

const (
	PostBattleControlNone PostBattleControl = iota
	PostBattleControlStart
	PostBattleControlMainMenu
	PostBattleControlLoadGame
	PostBattleControlSaveGame
	PostBattleControlDifficulty
	PostBattleControlKey
	PostBattleControlMouse
)

// PostBattleEffectKind is a backend-independent request. Media decoders,
// palette upload, GUI construction, and shell routing consume these requests;
// the session never emulates CD hardware or manufactures unavailable art.
type PostBattleEffectKind uint8

const (
	// NetworkStats requests the multiplayer frame/statistics collection that
	// precedes the rejection-dialog wait. Single-player routes do not emit it
	// [08 R-CAMP-01 §6].
	PostBattleEffectNetworkStats PostBattleEffectKind = iota + 1
	PostBattleEffectClearFrameCopy
	PostBattleEffectStopMusic
	PostBattleEffectFadeOutStep
	PostBattleEffectCampaignCDCheck
	PostBattleEffectOutcomeArt
	PostBattleEffectGlamourFadeStep
	PostBattleEffectGlamourSound
	PostBattleEffectPopulateEndMission
	PostBattleEffectEndingMovie
	PostBattleEffectEndingMediaSkipped
	PostBattleEffectRouteRouter
	PostBattleEffectStatPrompt
	PostBattleEffectDifficultyChanged
)

// PostBattleEffect records one semantic side effect in deterministic order.
// Level is the fade level (countdown−29 for the ten-step fade); Resource is an
// authored media/resource key; Mission is the next selected campaign index.
type PostBattleEffect struct {
	Kind     PostBattleEffectKind
	Level    int
	Resource string
	Mission  int
}

// PostBattleConfig is immutable setup supplied by the session/front end. A
// zero media flag means the optional media is unavailable and therefore takes
// the established skip boundary rather than a synthetic replacement.
type PostBattleConfig struct {
	Kind          PostBattleSessionKind
	MissionIndex  int
	CampaignPath  string
	Campaign      string
	Mission       string
	NextMission   string
	Map           string
	Players       int
	HasNext       bool
	Nomovie       bool
	Windowed      bool
	LocalSide     int
	Glamour       string
	GlamourSound  string
	GlamourLoaded bool
	EndingMedia   bool
	CampaignCDOK  bool
	Difficulty    int
	DifficultyOut *int
	Progress      *BankProgress
	// ProgressCommitted is set by the authoritative score-teardown writer
	// before this presentation handler is installed [08 R-CAMP-01 §7].
	ProgressCommitted bool
}

// PostBattleSummary is the between-mission Summary projection. The save
// writer maps this typed projection to its Summary account; keeping it here
// prevents a save operation from scanning mutable live-world state [08
// R-CAMP-01 §8; 08 "Summary"].
type PostBattleSummary struct {
	Campaign        string
	Mission         string
	Map             string
	Side            int
	Players         int
	Difficulty      int
	MissionIndex    int
	Thumbs          [25]byte
	BetweenMissions bool
}

// PostBattleController owns only frozen result data and presentation
// sequencing. Campaign progress is committed by score teardown before this
// handler is installed; this controller does not write Session or renderer
// state [03 §2.4][08 R-CAMP-01 §7][I6].
type PostBattleController struct {
	result frame.ResultView
	cfg    PostBattleConfig
	state  PostBattleState

	entered             bool
	fadeRemaining       int
	fadeDeadline        uint32
	glamourDone         bool
	glamourDue          uint32
	glamourPrompt       uint32
	promptDone          bool
	glamourSoundDone    bool
	progressDone        bool
	endMissionDone      bool
	routed              bool
	selectedMission     int
	missionSelection    int
	missionSelectionSet bool

	effects []PostBattleEffect
	order   []PostBattleState
}

// NewPostBattleController freezes the committed result at the battle→results
// boundary. Later changes to the live result or score slices cannot affect the
// sequence [08 R-CAMP-01 §6–8].
func NewPostBattleController(result frame.ResultView, cfg PostBattleConfig) *PostBattleController {
	result.Winners = append([]int(nil), result.Winners...)
	result.Losers = append([]int(nil), result.Losers...)
	result.Scores = append([]frame.ResultScore(nil), result.Scores...)
	return &PostBattleController{result: result, cfg: cfg, state: PostBattleEntry, selectedMission: -1}
}

func (c *PostBattleController) Result() frame.ResultView {
	if c == nil {
		return frame.ResultView{}
	}
	r := c.result
	r.Winners = append([]int(nil), r.Winners...)
	r.Losers = append([]int(nil), r.Losers...)
	r.Scores = append([]frame.ResultScore(nil), r.Scores...)
	return r
}

func (c *PostBattleController) State() PostBattleState {
	if c == nil {
		return PostBattleEntry
	}
	return c.state
}

// Routed reports that the owning front-end should leave the results handler
// for the session router. It is separate from PostBattleState so the retail
// handler word remains exactly 0..8 [08 R-CAMP-01 §6].
func (c *PostBattleController) Routed() bool { return c != nil && c.routed }

func (c *PostBattleController) StateOrder() []PostBattleState {
	if c == nil {
		return nil
	}
	return append([]PostBattleState(nil), c.order...)
}

func (c *PostBattleController) Effects() []PostBattleEffect {
	if c == nil {
		return nil
	}
	return append([]PostBattleEffect(nil), c.effects...)
}

func (c *PostBattleController) emit(e PostBattleEffect) { c.effects = append(c.effects, e) }

func (c *PostBattleController) enter(next PostBattleState) {
	c.state = next
	c.order = append(c.order, next)
}

func (c *PostBattleController) won() bool {
	return c.result.Ended && !c.result.Draw && strings.EqualFold(c.result.Kind, "victory")
}

func (c *PostBattleController) campaignRoute() bool {
	return c.cfg.Kind == PostBattleCampaign && (c.cfg.HasNext || !c.won())
}

// ProgressApplied reports the frozen score-teardown commit. The controller is
// intentionally not a second writer: Session.pollMissionTriggers owns the W/L
// mark before this handler is installed [08 R-CAMP-01 §7; C3 integration hazard].
func (c *PostBattleController) ProgressApplied() bool {
	return c != nil && c.progressDone
}

func (c *PostBattleController) acknowledgeProgress() {
	if c != nil && c.result.Ended && c.cfg.Kind == PostBattleCampaign {
		c.progressDone = c.cfg.ProgressCommitted
	}
}

// Summary returns the frozen between-mission projection. It is valid only for
// campaign continuation; non-campaign results intentionally return the zero
// projection [08 R-CAMP-01 §8].
func (c *PostBattleController) Summary() PostBattleSummary {
	if c == nil || c.cfg.Kind != PostBattleCampaign {
		return PostBattleSummary{}
	}
	s := PostBattleSummary{Campaign: c.cfg.Campaign, Mission: c.cfg.Mission, Map: c.cfg.Map, Side: c.cfg.LocalSide, Players: c.cfg.Players, MissionIndex: c.cfg.MissionIndex, Difficulty: c.cfg.Difficulty, BetweenMissions: true}
	// A results-screen save runs Advance before writing Summary. It therefore
	// names the successor whenever one exists, even after a loss [08
	// R-CAMP-01 §8].
	if c.cfg.HasNext {
		s.MissionIndex = c.cfg.MissionIndex + 1
		if c.cfg.NextMission != "" {
			s.Mission = c.cfg.NextMission
		}
	}
	if c.cfg.Progress != nil {
		s.Thumbs = c.cfg.Progress.Thumbs
	}
	return s
}

// NextMission is the preselected mission, not an eager mission load. Retail
// selects current+1 on a win and current on a loss while ENDMSN is populated
// [08 R-CAMP-01 §8].
func (c *PostBattleController) NextMission() (int, bool) {
	if c == nil || c.cfg.Kind != PostBattleCampaign || !c.campaignRoute() {
		return 0, false
	}
	if c.won() {
		return c.cfg.MissionIndex + 1, c.cfg.HasNext
	}
	return c.cfg.MissionIndex, true
}

// SelectMission receives a validated authored index from the ENDMSN list
// adapter before Start; showing or changing a row does not route the screen
// [08 R-CAMP-01 §8].
func (c *PostBattleController) SelectMission(index int) bool {
	if c == nil || !c.AdmitControl(PostBattleControlStart) || index < 0 {
		return false
	}
	c.missionSelection, c.missionSelectionSet = index, true
	return true
}

// SelectedMission is the successor selected by Start. It is unavailable
// until the typed Start control is accepted and is never populated merely by
// showing ENDMSN [08 R-CAMP-01 §8].
func (c *PostBattleController) SelectedMission() (int, bool) {
	if c == nil || c.selectedMission < 0 {
		return 0, false
	}
	return c.selectedMission, true
}

// AdmitControl reports whether a typed control is active in the current
// retail state. It performs no mutation; Handle performs the semantic action.
func (c *PostBattleController) AdmitControl(control PostBattleControl) bool {
	if c == nil {
		return false
	}
	switch c.state {
	case PostBattleGlamour:
		return c.glamourDone && (control == PostBattleControlKey || control == PostBattleControlMouse)
	case PostBattleEndMission:
		if control == PostBattleControlMainMenu {
			return true
		}
		if !c.campaignRoute() {
			return false
		}
		switch control {
		case PostBattleControlStart, PostBattleControlLoadGame, PostBattleControlSaveGame, PostBattleControlDifficulty:
			return true
		}
	}
	return false
}

// Handle applies one admitted typed control. Difficulty cycling is the only
// control mutation here and follows 0→1→2→0 [08 R-CAMP-01 §8]. Mission load
// and shell navigation remain semantic requests for the owning adapter.
func (c *PostBattleController) Handle(control PostBattleControl, now uint32) bool {
	if c == nil || !c.AdmitControl(control) {
		return false
	}
	switch control {
	case PostBattleControlKey, PostBattleControlMouse:
		if c.state == PostBattleGlamour && (!c.glamourDone || now <= c.glamourDue) {
			return false
		}
		c.emit(PostBattleEffect{Kind: PostBattleEffectPopulateEndMission})
		c.endMissionDone = true
		c.enter(PostBattleEndMission)
	case PostBattleControlDifficulty:
		c.cfg.Difficulty = (c.cfg.Difficulty + 1) % 3
		if c.cfg.DifficultyOut != nil {
			*c.cfg.DifficultyOut = c.cfg.Difficulty
		}
		c.emit(PostBattleEffect{Kind: PostBattleEffectDifficultyChanged, Mission: c.cfg.Difficulty})
	case PostBattleControlStart:
		if next, ok := c.NextMission(); ok {
			if c.missionSelectionSet {
				next = c.missionSelection
			}
			c.selectedMission = next
			c.emit(PostBattleEffect{Kind: PostBattleEffectPopulateEndMission, Mission: next})
			c.endMissionDone = true
			c.routed = true
		}
	case PostBattleControlMainMenu:
		c.emit(PostBattleEffect{Kind: PostBattleEffectRouteRouter})
		c.routed = true
	case PostBattleControlLoadGame, PostBattleControlSaveGame:
		// The adapter owns the dialog and sends a later typed control. Keeping
		// the state unchanged matches the modal SAVE UNDER path [08 R-CAMP-01 §8].
		_ = now
	}
	return true
}

// GlamourFadeDone is the typed presentation result for one complete palette
// fade. The controller cannot infer completion from a fixed count: each byte
// advances once per unit until it equals its target, so a six-unit difference
// takes six units even though the fade table was built with divisor five [08
// R-CAMP-01 §6].
func (c *PostBattleController) GlamourFadeDone(now uint32) bool {
	if c == nil || c.state != PostBattleGlamour || c.glamourDone {
		return false
	}
	c.glamourDone = true
	c.glamourDue = now + 30
	c.glamourPrompt = c.glamourDue + 150
	return true
}

// Step advances exactly one presentation unit. The state transition order and
// strict deadline comparisons follow [08 R-CAMP-01 §6].
func (c *PostBattleController) Step(now uint32, dialogOpen bool) {
	if c == nil || c.routed {
		return
	}
	if !c.entered {
		c.entered = true
		c.order = append(c.order, PostBattleEntry)
		if c.cfg.Kind == PostBattleNetwork {
			// Multiplayer retains the last frame behind a possible rejection
			// dialog and runs the statistics collector. The single-player path
			// clears its copy immediately [08 R-CAMP-01 §6].
			c.emit(PostBattleEffect{Kind: PostBattleEffectNetworkStats})
			c.enter(PostBattleWaitDialog)
		} else {
			c.emit(PostBattleEffect{Kind: PostBattleEffectClearFrameCopy})
			c.enter(PostBattleFadeSetup)
		}
		return
	}
	switch c.state {
	case PostBattleWaitDialog:
		if !dialogOpen {
			c.enter(PostBattleFadeSetup)
		}
	case PostBattleFadeSetup:
		c.fadeRemaining = 10
		c.fadeDeadline = now + 1
		c.enter(PostBattleFade)
	case PostBattleFade:
		if now <= c.fadeDeadline {
			return
		}
		c.emit(PostBattleEffect{Kind: PostBattleEffectFadeOutStep, Level: c.fadeRemaining - 29})
		c.fadeRemaining--
		c.fadeDeadline = now + 1
		if c.fadeRemaining == 0 {
			c.emit(PostBattleEffect{Kind: PostBattleEffectStopMusic})
			c.enter(PostBattleCDCheck)
		}
	case PostBattleCDCheck:
		if c.cfg.Kind == PostBattleCampaign && !c.cfg.CampaignCDOK {
			c.emit(PostBattleEffect{Kind: PostBattleEffectCampaignCDCheck})
			c.enter(PostBattleCDIdle)
			return
		}
		c.enter(PostBattleOutcome)
	case PostBattleOutcome:
		c.acknowledgeProgress()
		if c.cfg.Kind == PostBattleCampaign && c.won() && !c.cfg.HasNext && !c.cfg.Nomovie {
			if c.cfg.EndingMedia && !c.cfg.Windowed {
				resource := "4.zrb"
				if c.cfg.LocalSide == 0 {
					resource = "3.zrb"
				}
				c.emit(PostBattleEffect{Kind: PostBattleEffectEndingMovie, Resource: resource})
				// Retail appends the common closing reel after the side-specific
				// ending; both remain semantic media requests [08 R-CAMP-01 §6].
				c.emit(PostBattleEffect{Kind: PostBattleEffectEndingMovie, Resource: "5.zrb"})
			} else {
				c.emit(PostBattleEffect{Kind: PostBattleEffectEndingMediaSkipped})
			}
			c.emit(PostBattleEffect{Kind: PostBattleEffectRouteRouter})
			c.routed = true
			return
		}
		if c.cfg.Kind == PostBattleCampaign && c.won() && c.cfg.GlamourLoaded && c.cfg.Glamour != "" {
			c.emit(PostBattleEffect{Kind: PostBattleEffectOutcomeArt, Resource: c.cfg.Glamour})
			c.enter(PostBattleGlamour)
			return
		}
		c.emit(PostBattleEffect{Kind: PostBattleEffectOutcomeArt, Resource: outcomeResource(c.cfg.Kind, c.campaignRoute())})
		c.emit(PostBattleEffect{Kind: PostBattleEffectPopulateEndMission})
		c.endMissionDone = true
		c.enter(PostBattleEndMission)
	case PostBattleGlamour:
		if !c.glamourDone {
			// Palette ownership stays with the presentation adapter. One request
			// is emitted per unit; completion is reported only when all 1024
			// bytes equal their target [08 R-CAMP-01 §6].
			c.emit(PostBattleEffect{Kind: PostBattleEffectGlamourFadeStep})
			return
		}
		if now <= c.glamourDue {
			return
		}
		if !c.glamourSoundDone {
			c.emit(PostBattleEffect{Kind: PostBattleEffectGlamourSound, Resource: c.cfg.GlamourSound})
			c.glamourSoundDone = true
		}
		if now >= c.glamourPrompt && !c.promptDone {
			c.emit(PostBattleEffect{Kind: PostBattleEffectStatPrompt})
			c.promptDone = true
		}
	case PostBattleCDIdle:
		// CD-check adapters re-enter by constructing a controller with CDOK;
		// no unsupported-media loop or synthetic dialog is created here.
	}
}

// ResolveCampaignCD is the typed callback boundary for the optional campaign
// media check. A successful re-check resumes state 5; failure leaves the
// authored check state idle [08 R-CAMP-01 §6].
func (c *PostBattleController) ResolveCampaignCD(ok bool) bool {
	if c == nil || c.state != PostBattleCDIdle || !ok {
		return false
	}
	c.cfg.CampaignCDOK = true
	c.enter(PostBattleOutcome)
	return true
}

func outcomeResource(kind PostBattleSessionKind, route bool) string {
	if kind == PostBattleCampaign && route {
		return "Outcome1"
	}
	return "Outcome0"
}
