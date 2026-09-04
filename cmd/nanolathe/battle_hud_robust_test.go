package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nanolathe/nanolathe/internal/client"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/testsupport"
	"github.com/nanolathe/nanolathe/vfs"
)

// hiddenFS wraps a vfs.FS and pretends specific logical paths do not exist.
// It implements hudFS (vfs.FSOps + Providers) so diagnostics still work.
type hiddenFS struct {
	vfs.FSOps
	providers []vfs.ProviderInfo
	hidden    map[string]bool
}

func (h *hiddenFS) Providers() []vfs.ProviderInfo { return h.providers }
func (h *hiddenFS) ReadFileLimit(name string, max int64) ([]byte, error) {
	if h.hidden[strings.ToLower(name)] {
		return nil, fmt.Errorf("%w: %s", vfs.ErrNotFound, name)
	}
	return h.FSOps.ReadFileLimit(name, max)
}
func (h *hiddenFS) Open(name string) (vfs.File, error) {
	if h.hidden[strings.ToLower(name)] {
		return nil, fmt.Errorf("%w: %s", vfs.ErrNotFound, name)
	}
	return h.FSOps.Open(name)
}
func (h *hiddenFS) Stat(name string) (vfs.EntryInfo, error) {
	if h.hidden[strings.ToLower(name)] {
		return vfs.EntryInfo{}, fmt.Errorf("%w: %s", vfs.ErrNotFound, name)
	}
	return h.FSOps.Stat(name)
}

func newHiddenFS(base vfs.FSOps, providers []vfs.ProviderInfo, hide ...string) *hiddenFS {
	m := make(map[string]bool, len(hide))
	for _, h := range hide {
		m[strings.ToLower(h)] = true
	}
	return &hiddenFS{FSOps: base, providers: providers, hidden: m}
}

// TestBattleHUDLoadsARMAndCORE verifies ARM and CORE side HUDs load on retail
// assets (or skip when no assets) [07 §6][07 §8]. It checks side, fonts, 30
// anchors and core panel frames are mandatory and succeed for both sides.
func TestBattleHUDLoadsARMAndCORE(t *testing.T) {
	root := testsupport.RetailRoot(t)
	opts := Options{Root: root, Map: "ashap plateau", Seed: 1}
	cs, err := openContent(opts)
	if err != nil {
		t.Skipf("retail assets unavailable: %v", err)
	}
	defer cs.Close()
	for sideIdx, wantPrefix := range []string{"ARM", "COR"} {
		// The lobby refuses a roster without both a human and a computer
		// opponent [08 "Skirmish configuration"]; slot 1 is the AI so the local
		// human in slot 0 sees the side under test.
		cfg := session.SkirmishConfig{MapName: "ashap plateau", NumPlayers: 2}
		cfg.Players[0].Side = sideIdx
		cfg.Players[1].Side = 1 - sideIdx
		cfg.Players[1].Controller = session.SkirmishControllerComputer
		cfg.ApplyDefaults()
		sess, _, err := newBattleSessionWithConfig(opts, cs, cfg)
		if err != nil {
			t.Fatalf("side %d %s session: %v", sideIdx, wantPrefix, err)
		}
		pal := loadPalette(cs)
		if pal == nil {
			t.Skip("palette not available")
		}
		hud, err := loadRetailBattleHUD(cs.fs, sess, sess.Catalog, pal)
		if err != nil {
			t.Fatalf("side %d %s HUD load failed: %v", sideIdx, wantPrefix, err)
		}
		if hud.side == nil || !strings.EqualFold(hud.side.NamePrefix, wantPrefix) {
			t.Fatalf("side %d got prefix %v want %s", sideIdx, hud.side, wantPrefix)
		}
		if hud.console == nil || hud.guiFont == nil {
			t.Fatalf("side %s missing mandatory fonts", wantPrefix)
		}
		if len(hud.anchors) != 30 {
			t.Fatalf("side %s anchors %d want 30 [02 §6]", wantPrefix, len(hud.anchors))
		}
		if hud.panelTop == nil || hud.panelSide == nil || hud.panelBottom == nil {
			t.Fatalf("side %s missing mandatory panel frames [07 §6]", wantPrefix)
		}
		// ESC opens the hard-coded ARMOPT modal for either side. CORE must use
		// that same authored path; there is no COROPT branch [07 §11]. A loaded
		// window is named by the logical path it was read from.
		if hud.optionsWin == nil || !strings.EqualFold(hud.optionsWin.Name, "guis/armopt.gui") {
			t.Fatalf("%s options window = %#v; want ARMOPT.GUI", wantPrefix, hud.optionsWin)
		}
	}
}

// TestMissingOptionalStillEntersBattle verifies that missing optional
// pause/title/options modal resources do not prevent battle entry [07 §8][07 §11].
// Valid core battle reaches its first frame while those optional resources are unavailable.
func TestMissingOptionalStillEntersBattle(t *testing.T) {
	root := testsupport.RetailRoot(t)
	opts := Options{Root: root, Map: "ashap plateau", Seed: 1}
	cs, err := openContent(opts)
	if err != nil {
		t.Skipf("retail assets unavailable: %v", err)
	}
	defer cs.Close()
	// Hide optional assets via hiddenFS.
	providers := cs.fs.Providers()
	base := cs.fs
	for _, hide := range [][]string{
		{"anims/igtitles.gaf"},
		{"guis/armopt.gui", "anims/armopt.gaf"},
		{"guis/exitmenu.gui"},
		{"guis/yesorno.gui"},
		{"anims/hattfont12.gaf"},
	} {
		hfs := newHiddenFS(base, providers, hide...)
		// Need a session for each test.
		sess, _, err := newBattleSession(opts, cs)
		if err != nil {
			t.Fatalf("session: %v", err)
		}
		pal := loadPalette(cs)
		if pal == nil {
			t.Skip("palette missing")
		}
		hud, err := loadRetailBattleHUD(hfs, sess, sess.Catalog, pal)
		if err != nil {
			t.Fatalf("optional hide %v should not fail HUD load: %v", hide, err)
		}
		if hud == nil {
			t.Fatalf("hud nil after hide %v", hide)
		}
		// Verify core mandatory still present.
		if hud.panelTop == nil || hud.console == nil {
			t.Fatalf("core HUD missing after hiding optional %v", hide)
		}
		// Verify we can compose a first frame with this HUD (even without optional).
		cl, err := client.New(client.Options{Buffer: sess.Snapshot, Width: 640, Height: 480})
		if err != nil {
			t.Fatalf("client: %v", err)
		}
		cl.SetTerrain(sess.World)
		cl.SetPalette(pal)
		cl.SetFNT(hud.console)
		cl.SetModelFS(cs.fs)
		b := &battleSession{sess: sess, cat: sess.Catalog, cam: nil, hud: hud}
		cl.SetUIStage(battleHUDUIStage{hud: hud, battle: b})
		// Advance one tick and compose — should not panic.
		sess.Step(1)
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("compose with missing optional %v panicked: %v", hide, r)
				}
			}()
			_ = cl.ComposeFrame()
		}()
	}
}

// TestMissingMandatoryFailsBeforeClientWithDiagnostic checks that a missing
// mandatory side font/anchor fails before client creation with exact
// path/provider diagnostic [02 §6][AGENTS.md §Diagnostics].
func TestMissingMandatoryFailsBeforeClientWithDiagnostic(t *testing.T) {
	root := testsupport.RetailRoot(t)
	opts := Options{Root: root, Map: "ashap plateau", Seed: 1}
	cs, err := openContent(opts)
	if err != nil {
		t.Skipf("retail assets unavailable: %v", err)
	}
	defer cs.Close()
	sess, _, err := newBattleSession(opts, cs)
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	pal := loadPalette(cs)
	if pal == nil {
		t.Skip("palette missing")
	}
	// Determine mandatory font logical path for this side.
	side, err := battleSide(cs.fs, sess, sess.Catalog)
	if err != nil {
		t.Fatalf("battleSide: %v", err)
	}
	mandatoryFont := "fonts/" + strings.ToLower(side.Font) + ".fnt"
	providers := cs.fs.Providers()
	hfs := newHiddenFS(cs.fs, providers, mandatoryFont)
	_, err = loadRetailBattleHUD(hfs, sess, sess.Catalog, pal)
	if err == nil {
		t.Fatalf("expected HUD load to fail when mandatory font %s missing", mandatoryFont)
	}
	if !strings.Contains(err.Error(), "logical path "+mandatoryFont) {
		t.Fatalf("mandatory error missing logical path: %v", err)
	}
	if !strings.Contains(err.Error(), "providers searched [") {
		t.Fatalf("mandatory error missing providers diagnostic: %v", err)
	}
	// Anchor missing case: use a synthetic catalog with a side missing an anchor.
	// We can simulate by directly testing hud.AnchorsFromSide failure path via
	// loadRetailBattleHUD's anchor check — create a side with nil Anchors.
	// Instead, test that a side with empty Font also fails with diagnostic.
	// For anchor, we can create a synthetic side def missing one anchor and
	// test that loadRetailBattleHUD would fail at anchor stage.
	// Use a minimal side: clone and delete an anchor.
	origSide := side
	// Create a copy with one anchor removed.
	if len(origSide.Anchors) != 30 {
		t.Fatalf("expected 30 anchors")
	}
	// Build a catalog with broken side.
	brokenSide := *origSide
	brokenSide.Anchors = make(map[string]content.Rect, len(origSide.Anchors))
	for k, v := range origSide.Anchors {
		brokenSide.Anchors[k] = v
	}
	// Remove one mandatory anchor.
	for k := range brokenSide.Anchors {
		delete(brokenSide.Anchors, k)
		break
	}
	_ = brokenSide // used via catalog below
	// We need to test the anchor error path via loadRetailBattleHUD: it calls
	// hud.AnchorsFromSide which will fail. To trigger, we need sess.LocalOwner
	// to point at broken side index. Instead directly test AnchorsFromSide error
	// includes diagnostic wrapping by loadRetailBattleHUD.
	// For simplicity, verify that hud.AnchorsFromSide itself is mandatory:
	// If we call loadRetailBattleHUD with a session that selects broken side,
	// it should error with provider diagnostic.
	// Create a catalog copy where Sides[0] is broken.
	brokenCat := *sess.Catalog
	brokenCat.Sides = make([]*content.SideDef, len(sess.Catalog.Sides))
	copy(brokenCat.Sides, sess.Catalog.Sides)
	brokenCat.Sides[origSide.Index] = &brokenSide
	// RS-05: Session now contains sync.Mutex; copying by value triggers vet.
	// Construct a minimal Session that shares the same Skirmish/LocalOwner without copying the mutex.
	sessBroken := &session.Session{
		Skirmish:   sess.Skirmish,
		Catalog:    &brokenCat,
		LocalOwner: sess.LocalOwner,
		EnemyOwner: sess.EnemyOwner,
		Mission:    sess.Mission,
		World:      sess.World,
		Units:      sess.Units,
	}
	_, err = loadRetailBattleHUD(cs.fs, sessBroken, &brokenCat, pal)
	if err == nil {
		t.Fatal("expected anchor missing to fail")
	}
	if !strings.Contains(err.Error(), "providers searched [") {
		t.Fatalf("anchor error missing providers: %v", err)
	}
	if !strings.Contains(err.Error(), "gamedata/sidedata.tdf") {
		t.Fatalf("anchor error missing logical path sidedata: %v", err)
	}
}
