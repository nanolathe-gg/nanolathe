package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/features"
	"github.com/nanolathe-gg/nanolathe/internal/frame"
	"github.com/nanolathe-gg/nanolathe/internal/gui"
	"github.com/nanolathe-gg/nanolathe/internal/input"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport/retailcat"
)

// These fixtures lock the requested modern input policy, not retail gestures.
func resourceFixture(t *testing.T, deposit bool) (*battleSession, *client.Client, *fakeMillisSource, pool.Handle) {
	t.Helper()
	b, s, builder := placeClickFixture(t, 64, 64)
	solar := b.cat.Units["armsolar"]
	solar.SoundCategory, solar.EnergyUse = "TEST_SOLAR", -20
	for _, item := range []struct {
		key  string
		size int32
		rate float64
	}{{"extractor", 3, 1}, {"advanced", 5, 4}} {
		u := *solar
		u.CanonicalKey, u.DefinitionHeader.CanonicalKey, u.UnitName = item.key, item.key, item.key
		u.FootprintX, u.FootprintZ, u.YardMap = item.size, item.size, strings.Repeat("o", int(item.size*item.size))
		u.ExtractsMetal, u.SoundCategory = item.rate, ""
		b.cat.Units[item.key] = &u
	}
	b.cat.BuildMenus["armcons"].Buttons = []string{"armsolar", "extractor", "advanced"}
	if deposit {
		fd := &content.FeatureDef{FootprintX: 3, FootprintZ: 3, Metal: 100, Indestructible: true}
		fd.CanonicalKey = "deposit"
		b.cat.Features["deposit"] = fd
		s.World.FeatureNames = []string{"deposit"}
		s.World.FeatureDefs = []*content.FeatureDef{fd}
		s.Features = features.NewService(s.World, nil, nil, nil)
		if s.Features.PlaceAt(20, 20, fd) == nil {
			t.Fatal("place deposit")
		}
	}
	s.Step(s.Clock.ScaledAnchor + 1)
	if err := b.enqueueSelectionCommand(session.HumanCommand{Kind: session.HumanSelectionReplace, Selection: session.HumanSelectionCommand{Handles: []pool.Handle{builder}}}); err != nil {
		t.Fatal(err)
	}
	s.Step(s.Clock.ScaledAnchor + 1)
	cl, err := client.New(client.Options{Buffer: s.Snapshot, Width: 640, Height: 480})
	if err != nil {
		t.Fatal(err)
	}
	cl.SetCamera(b.cam)
	cl.SetEnhanced(true)
	cl.SetFocused(true)
	ms := &fakeMillisSource{ms: 100}
	b.millisSource = ms
	return b, cl, ms, builder
}

func resourceInput(b *battleSession, cl *client.Client, x, y int32, down, shift bool) {
	in := input.NewState()
	in.Mouse.SetPosition(float32(x), float32(y))
	in.Mouse.SetButton(input.MouseButtonLeft, down)
	in.Kbd.SetKey(input.KeyShift, shift)
	in.Kbd.ResetEdges() // Shift was held before the click.
	b.handleInput(in, cl)
}

func resourceClickAt(b *battleSession, cl *client.Client, x, y int32, shift bool) {
	resourceInput(b, cl, x, y, true, shift)
	resourceInput(b, cl, x, y, false, shift)
}

func resourceBuildCommands(s *session.Session) []session.HumanMobileBuildCommand {
	var out []session.HumanMobileBuildCommand
	for _, c := range s.PendingHumanCommands() {
		if c.Kind == session.HumanMobileBuild {
			out = append(out, c.MobileBuild)
		}
	}
	return out
}

func TestResourceDoubleClickQueuesOnlyBuild(t *testing.T) {
	for _, kind := range []int{0, 1} {
		for _, shift := range []bool{false, true} {
			t.Run(fmt.Sprintf("type%d_shift%v", kind, shift), func(t *testing.T) {
				b, cl, ms, builder := resourceFixture(t, false)
				b.interfaceType = kind
				// Every double-click preserves existing construction, regardless of Shift.
				{
					if err := b.DispatchMobileBuild("armsolar", numeric.FixedFromInt(480), 0, numeric.FixedFromInt(480), false); err != nil {
						t.Fatal(err)
					}
					b.sess.Step(b.sess.Clock.ScaledAnchor + 1)
				}
				x, y := o5ScreenWorld(b.cam, numeric.FixedFromInt(320), 0, numeric.FixedFromInt(320))
				resourceClickAt(b, cl, x, y, shift)
				if len(b.sess.PendingHumanCommands()) != 0 || b.resourceClick == nil {
					t.Fatal("first click issued a command or failed to arm")
				}
				// Let an authoritative tick pass between clicks: Type 1 must keep selection.
				b.sess.Step(b.sess.Clock.ScaledAnchor + 1)
				ms.ms += 150
				resourceClickAt(b, cl, x, y, shift)
				cmds := resourceBuildCommands(b.sess)
				if len(cmds) != 1 || cmds[0].Product != "armsolar" || !cmds[0].Queued || !cmds[0].AppendOnly {
					t.Fatalf("build commands %+v", cmds)
				}
				if len(b.sess.PendingHumanCommands()) != 1 {
					t.Fatalf("extra command after double-click: %+v", b.sess.PendingHumanCommands())
				}
				b.sess.Step(b.sess.Clock.ScaledAnchor + 1)
				q := orders.QueueForUnit(b.sess.Units.Unit(builder))
				want := 2
				if q == nil || q.LenPrimary() != want {
					t.Fatalf("queue=%v, want %d builds", q, want)
				}
				if !orders.IsMobileBuild(q.Head().ID) {
					t.Fatal("contextual order displaced construction")
				}
				if !b.hasSelection() || b.battleState().Input.BuildDef != "" || b.resourceClick != nil {
					t.Fatal("shortcut left selection or placement in wrong state")
				}
			})
		}
	}
}

func TestResourceDepositCentersBothExtractorSizes(t *testing.T) {
	for _, advanced := range []bool{false, true} {
		t.Run(fmt.Sprint(advanced), func(t *testing.T) {
			b, cl, ms, _ := resourceFixture(t, true)
			product := "advanced"
			wantCell := int32(19)
			if !advanced {
				b.cat.BuildMenus["armcons"].Buttons = []string{"armsolar", "extractor"}
				product = "extractor"
				wantCell = 20
			}
			// Click the far corner, not the center of the 3x3 deposit.
			x, y := o5ScreenWorld(b.cam, numeric.FixedFromInt(366), 0, numeric.FixedFromInt(366))
			site, ok := b.resourceSite(x, y)
			if !ok || site.product.CanonicalKey != product || site.x != wantCell || site.z != wantCell {
				t.Fatalf("site %+v, ok=%v", site, ok)
			}
			resourceClickAt(b, cl, x, y, false)
			ms.ms += 100
			resourceClickAt(b, cl, x, y, false)
			cmds := resourceBuildCommands(b.sess)
			if len(cmds) != 1 || cmds[0].Product != product || cmds[0].WX != numeric.FixedFromInt(344) || cmds[0].WZ != numeric.FixedFromInt(344) {
				t.Fatalf("not centered on deposit: %+v", cmds)
			}
		})
	}
}

func TestResourceFoggedDepositNeverFallsBackToSolar(t *testing.T) {
	for _, product := range []string{"extractor", "advanced", ""} {
		t.Run("product="+product, func(t *testing.T) {
			b, cl, ms, _ := resourceFixture(t, true)
			b.cat.BuildMenus["armcons"].Buttons = []string{"armsolar", product}
			cur, _ := b.currentSnapshot()
			written := b.sess.Snapshot.BeginWrite()
			*written = *cur
			// The permanent deposit remains in the map snapshot even outside LOS.
			written.Visibility = frame.VisibilityView{W: 32, H: 32, Valid: true, CoverageBytes: true, Visible: make([]uint8, 32*32)}
			if err := b.sess.Snapshot.Publish(cur.Tick + 1); err != nil {
				t.Fatal(err)
			}
			cur, _ = b.currentSnapshot()
			if len(cur.Features) != 1 || snapshotFeatureVisible(cur, cur.Features[0], cur.ViewingPlayer) {
				t.Fatal("fixture must have a deposit outside current LOS")
			}
			x, y := o5ScreenWorld(b.cam, numeric.FixedFromInt(366), 0, numeric.FixedFromInt(366))
			resourceClickAt(b, cl, x, y, false)
			ms.ms += 100
			resourceClickAt(b, cl, x, y, false)
			cmds := resourceBuildCommands(b.sess)
			if product == "" {
				if len(cmds) != 0 {
					t.Fatalf("builder without an extractor queued a build on metal: %+v", cmds)
				}
				return
			}
			if len(cmds) != 1 || cmds[0].Product != product || !cmds[0].Queued || !cmds[0].AppendOnly ||
				cmds[0].WX != numeric.FixedFromInt(344) || cmds[0].WZ != numeric.FixedFromInt(344) {
				t.Fatalf("fogged deposit did not queue its centered extractor: %+v", cmds)
			}
		})
	}
}

func TestResourceSingleClickExpiryAndClassic(t *testing.T) {
	for _, modern := range []bool{false, true} {
		for _, kind := range []int{0, 1} {
			t.Run(fmt.Sprintf("modern%v_type%d", modern, kind), func(t *testing.T) {
				b, cl, ms, _ := resourceFixture(t, false)
				b.interfaceType = kind
				cl.SetEnhanced(modern)
				resourceClickAt(b, cl, 320, 320, false)
				if modern {
					if len(b.sess.PendingHumanCommands()) != 0 {
						t.Fatal("single click not deferred")
					}
					ms.ms += 401
					resourceInput(b, cl, 320, 320, false, false)
				}
				cmds := b.sess.PendingHumanCommands()
				want := session.HumanOrder
				if kind == 1 {
					want = session.HumanSelectionClear
				}
				if len(cmds) != 1 || cmds[0].Kind != want {
					t.Fatalf("single click %+v, want %v", cmds, want)
				}
			})
		}
	}
}

func TestResourceRefusedSiteAndCancel(t *testing.T) {
	for _, mode := range []string{"refuse", "escape", "renderer", "selection", "armed"} {
		t.Run(mode, func(t *testing.T) {
			b, cl, ms, _ := resourceFixture(t, false)
			if mode == "refuse" {
				b.cat.Units["armsolar"].MinWaterDepth = 10
			}
			resourceClickAt(b, cl, 320, 320, false)
			ms.ms += 100
			switch mode {
			case "refuse":
				resourceClickAt(b, cl, 320, 320, false)
			case "escape":
				in := input.NewState()
				in.Kbd.SetKey(input.KeyEscape, true)
				b.handleInput(in, cl)
			case "renderer":
				cl.SetEnhanced(false)
				resourceInput(b, cl, 320, 320, false, false)
			case "selection":
				b.enqueueSelectionCommand(session.HumanCommand{Kind: session.HumanSelectionClear})
				b.sess.Step(b.sess.Clock.ScaledAnchor + 1)
				resourceInput(b, cl, 320, 320, false, false)
			case "armed":
				b.armPlacement(b.cat.Units["armsolar"])
				resourceInput(b, cl, 320, 320, false, false)
			}
			if b.resourceClick != nil || len(resourceBuildCommands(b.sess)) != 0 {
				t.Fatal("canceled/refused click built or stayed pending")
			}
			for _, c := range b.sess.PendingHumanCommands() {
				if c.Kind == session.HumanOrder {
					t.Fatal("cancellation replayed Move")
				}
			}
		})
	}
}

func TestResourceClassificationAvoidsOtherProducers(t *testing.T) {
	b, _, _, _ := resourceFixture(t, false)
	solar := b.cat.Units["armsolar"]
	for _, sound := range []string{"ARM_FUS", "CORE_GEO", "notsolar"} {
		u := *solar
		u.SoundCategory = sound
		if resourceSolar(&u) {
			t.Fatalf("classified %s as solar", sound)
		}
	}
	for _, field := range []string{"wind", "tidal", "builder", "mobile", "no-production"} {
		u := *solar
		switch field {
		case "wind":
			u.WindGenerator = 1
		case "tidal":
			u.TidalGenerator = 1
		case "builder":
			u.Builder = true
		case "mobile":
			u.BMCode = 1
		case "no-production":
			u.EnergyUse = 0
		}
		if resourceSolar(&u) {
			t.Fatalf("classified %s as solar", field)
		}
	}
	u := *solar
	u.CanonicalKey = "fabricator"
	u.MakesMetal = 10
	u.SoundCategory = "METAL"
	b.cat.Units[u.CanonicalKey] = &u
	b.cat.BuildMenus["armcons"].Buttons = []string{"fabricator"}
	mex, sun, _ := b.resourceProducts("armcons")
	if mex != nil || sun != nil {
		t.Fatal("fabricator accepted")
	}
}

// Installed data establishes the explicit category association; custom content
// without the token remains unclassified instead of falling back to fusion.
func TestResourceRetailBuildMenuClassification(t *testing.T) {
	cat, _ := retailcat.Shared(t)
	b := &battleSession{cat: cat}
	for _, side := range []struct{ builder, solar, mex, advanced, moho string }{
		{"armcom", "armsolar", "armmex", "armack", "armmoho"},
		{"corcom", "corsolar", "cormex", "corack", "cormoho"},
	} {
		mex, solar, _ := b.resourceProducts(side.builder)
		if mex == nil || solar == nil || mex.CanonicalKey != side.mex || solar.CanonicalKey != side.solar {
			t.Fatalf("%s: extractor=%v solar=%v", side.builder, mex, solar)
		}
		mex, _, _ = b.resourceProducts(side.advanced)
		if mex == nil || mex.CanonicalKey != side.moho {
			t.Fatalf("%s did not select %s", side.advanced, side.moho)
		}
	}
	for _, name := range []string{"armfus", "corfus", "armgeo", "corgeo"} {
		u, ok := cat.Unit(name)
		if !ok {
			t.Fatalf("missing %s", name)
		}
		if resourceSolar(u) {
			t.Fatalf("%s misclassified as solar", name)
		}
	}
}

func TestResourcePublishedDoubleClickUsesEventSnapshot(t *testing.T) {
	b, cl, ms, _ := resourceFixture(t, false)
	for _, kind := range []input.PointerEventKind{input.LeftDown, input.LeftUp, input.LeftDoubleClick, input.LeftUp} {
		in := input.NewState()
		down := kind != input.LeftUp
		if !in.EnqueuePointer(input.PointerEvent{Kind: kind, X: 320, Y: 320, Modifiers: input.Modifiers{Shift: true}, Buttons: input.MouseButtons{Left: down}}) || !in.PublishPointer() {
			t.Fatal("publish pointer")
		}
		// The event's Shift and viewport coordinates survive contradictory live state.
		in.UpdatePointerMotion(input.PointerEvent{X: 10, Y: 10})
		b.handleInput(in, cl)
		ms.ms += 50
	}
	cmds := resourceBuildCommands(b.sess)
	if len(cmds) != 1 || !cmds[0].Queued || cmds[0].WX != numeric.FixedFromInt(320) {
		t.Fatalf("lost pointer snapshot: %+v", cmds)
	}
}

func TestResourceViewerOwnershipCancelsDeferredClick(t *testing.T) {
	for _, mode := range []string{"focus", "chat", "modal", "escape"} {
		t.Run(mode, func(t *testing.T) {
			b, cl, ms, _ := resourceFixture(t, false)
			resourceClickAt(b, cl, 320, 320, false)
			if b.resourceClick == nil {
				t.Fatal("first click not pending")
			}
			b.controller = newReplayController(b)
			cl.Input().Mouse.SetPosition(320, 320)
			switch mode {
			case "focus":
				cl.SetFocused(false)
			case "chat":
				b.chat.active = true
			case "modal":
				b.battleState().OpenOptions()
			case "escape":
				cl.Input().Kbd.SetKey(input.KeyEscape, true)
			}
			ms.ms += 401
			b.viewerStep(0, cl)
			if b.resourceClick != nil {
				t.Fatal("input ownership left click pending")
			}
			for _, c := range b.sess.PendingHumanCommands() {
				if c.Kind == session.HumanOrder || c.Kind == session.HumanMobileBuild {
					t.Fatal("owned input replayed world command")
				}
			}
		})
	}
}

func TestResourcePaletteStopCancelsBeforeEdgesAreConsumed(t *testing.T) {
	b, cl, _ := paletteViewer(t, []gui.Gadget{{Kind: gui.KindButton, Name: "STOP", Active: 1, QuickKey: 'S'}})
	cl.SetFocused(true)
	cl.SetEnhanced(true)
	f, _ := b.currentSnapshot()
	// Seed the first-click record at the input ownership boundary. The fixture's
	// authored command palette then consumes S before the controller sees it.
	b.resourceClick = &resourceClick{builder: f.CommandPage.Builder, selection: append([]pool.Handle(nil), f.Selection.Handles...), fallback: session.HumanCommand{Kind: session.HumanOrder, Order: session.HumanOrderCommand{Code: 1}}}
	cl.Input().EnqueueToken(input.Token{Kind: input.TokenText, Rune: 's'})
	b.viewerStep(0, cl)
	if b.resourceClick != nil {
		t.Fatal("Stop retained earlier deferred click")
	}
	for _, c := range b.sess.PendingHumanCommands() {
		if c.Kind == session.HumanOrder {
			t.Fatal("Stop followed by stale Move")
		}
	}
}

func TestResourceDoubleClickIgnoresShiftChanges(t *testing.T) {
	b, cl, ms, _ := resourceFixture(t, false)
	resourceClickAt(b, cl, 320, 320, false)
	ms.ms += 100
	in := input.NewState()
	in.Mouse.SetPosition(320, 320)
	in.Mouse.SetButton(input.MouseButtonLeft, true)
	in.Kbd.SetKey(input.KeyShift, true)
	b.handleInput(in, cl)
	cmds := resourceBuildCommands(b.sess)
	if len(cmds) != 1 || !cmds[0].Queued || !cmds[0].AppendOnly {
		t.Fatalf("Shift changed double-click meaning: %+v", cmds)
	}
}

func TestResourceFeedbackExpiresWithoutChangingShift(t *testing.T) {
	b, cl, ms, _ := resourceFixture(t, false)
	resourceClickAt(b, cl, 320, 320, false)
	ms.ms += 100
	resourceClickAt(b, cl, 320, 320, false)
	if b.resourceQueueFeedback == nil || b.battleState().Input.ShiftHeld {
		t.Fatal("missing feedback or synthesized Shift")
	}
	b.sess.Step(b.sess.Clock.ScaledAnchor + 1)
	cl.SetUIStage(battleHUDUIStage{hud: &retailBattleHUD{}, battle: b})
	trace := &overlayTrace{}
	cl.RecordFrame().Replay(trace)
	// Existing queued footprint animation must live in a transformed world
	// region even without Shift; otherwise its marker drifts at modern zoom.
	found := false
	for _, fill := range trace.fills {
		if fill.inWorld {
			found = true
		}
	}
	if !found || trace.regions != 4 {
		t.Fatalf("queue feedback not drawn inside overlay: %+v", trace)
	}
	ms.ms += resourceQueueFeedbackMillis
	resourceInput(b, cl, 320, 320, false, false)
	if b.resourceQueueFeedback != nil {
		t.Fatal("queue feedback did not expire")
	}
	expired := &overlayTrace{}
	cl.RecordFrame().Replay(expired)
	if expired.regions != 2 {
		t.Fatalf("expired feedback retained world overlay: %d boundaries", expired.regions)
	}
}

func TestResourceRefusalDoesNotStartQueueFeedback(t *testing.T) {
	b, cl, ms, _ := resourceFixture(t, false)
	b.cat.Units["armsolar"].MinWaterDepth = 10
	resourceClickAt(b, cl, 320, 320, false)
	ms.ms += 100
	resourceClickAt(b, cl, 320, 320, false)
	if b.resourceQueueFeedback != nil {
		t.Fatal("refused placement played queue feedback")
	}
}

func TestResourceSingleOrderClickReplacesQueue(t *testing.T) {
	for _, kind := range []int{0, 1} {
		t.Run(fmt.Sprint(kind), func(t *testing.T) {
			b, cl, ms, builder := resourceFixture(t, false)
			b.interfaceType = kind
			resourceClickAt(b, cl, 320, 320, false)
			ms.ms += 100
			resourceClickAt(b, cl, 320, 320, false)
			b.sess.Step(b.sess.Clock.ScaledAnchor + 1)
			if kind == 0 {
				resourceClickAt(b, cl, 400, 320, false)
				ms.ms += 401
				resourceInput(b, cl, 400, 320, false, false)
			} else {
				in := input.NewState()
				in.Mouse.SetPosition(400, 320)
				in.Mouse.SetButton(input.MouseButtonRight, true)
				b.handleInput(in, cl)
				// Modern empty-ground clicks wait for release so the same
				// capture can become a formation (interface design §3.11).
				in.Mouse.ResetEdges()
				in.Mouse.SetButton(input.MouseButtonRight, false)
				b.handleInput(in, cl)
			}
			b.sess.Step(b.sess.Clock.ScaledAnchor + 1)
			q := orders.QueueForUnit(b.sess.Units.Unit(builder))
			for _, node := range q.Primary() {
				if orders.IsMobileBuild(node.ID) {
					t.Fatal("single order click retained build queue")
				}
			}
		})
	}
}

func TestResourceFeedbackCancelsWhenSelectionExpands(t *testing.T) {
	b, cl, ms, builder := resourceFixture(t, false)
	other := placeUnit(b, "armcons", numeric.FixedFromInt(500), numeric.FixedFromInt(400))
	resourceClickAt(b, cl, 320, 320, false)
	ms.ms += 100
	resourceClickAt(b, cl, 320, 320, false)
	b.enqueueSelectionCommand(session.HumanCommand{Kind: session.HumanSelectionReplace, Selection: session.HumanSelectionCommand{Handles: []pool.Handle{builder, other.Handle}}})
	b.sess.Step(b.sess.Clock.ScaledAnchor + 1)
	b.updateResourceQueueFeedback(cl)
	if b.resourceQueueFeedback != nil {
		t.Fatal("expanded selection kept stale feedback")
	}
}
