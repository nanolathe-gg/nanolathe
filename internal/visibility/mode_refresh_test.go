package visibility

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/world"
)

func refreshRayService() *Service {
	s := New(flatTerrain(128, 10), ModeHistoryEnabled|ModeCurrentEnabled|ModeTerrainRay)
	s.SetRayTables(&content.LOSTables{NumTables: 1, Tables: []content.LOSTable{{TableNum: 0}}})
	return s
}

func TestRefreshModePreservesOrResetsTheCommandSelectedHistory(t *testing.T) {
	s := refreshRayService()
	s.SetShapes(oneCellShape())
	var eligible [10]bool
	eligible[0] = true
	s.Refresh(1, Observer{Owner: 0, CX: 4, CZ: 5, HeightByte: 20, Radius: 20})
	s.Refresh(2, Observer{Owner: 0, CX: 9, CZ: 9, HeightByte: 20, Radius: 20})
	kept := int(5*s.W + 4)
	if s.wordMask[kept]&1 == 0 {
		t.Fatal("initial ray stamp did not set mapping history")
	}

	// LOSType/LOS preserve the word grid while wiping current coverage. Observer
	// 2 is removed from the supplied defined-unit list and must not reappear.
	s.RefreshMode(ModeHistoryEnabled|ModeCurrentEnabled, false, eligible, []ModeRefreshObserver{{
		ID: 1, Observer: Observer{Owner: 0, CX: 15, CZ: 16, HeightByte: 20, Radius: 20},
	}})
	if s.wordMask[kept]&1 == 0 {
		t.Fatal("LOSType/LOS refresh cleared retained mapping history")
	}
	if got := s.byteGrids[0][int(9*s.W+9)]; got != 0 {
		t.Fatalf("removed observer left byte coverage %d after command wipe", got)
	}
	if got := s.byteGrids[0][int(16*s.W+15)]; got != 1 {
		t.Fatalf("circular direct republish = %d, want 1", got)
	}

	// Mapping/NowISee request the separate history refill; mapped history is
	// all player bits before the direct current stamps.
	s.RefreshMode(ModeCurrentEnabled, true, eligible, nil)
	for i, got := range s.wordMask {
		if got != 0x03ff {
			t.Fatalf("mapped history cell %d = %#x, want all player bits", i, got)
		}
	}
	// A repeated NowISee carries its reset argument even with the same mode.
	s.wordMask[0] = 0
	s.RefreshMode(ModeCurrentEnabled, true, eligible, nil)
	if got := s.wordMask[0]; got != 0x03ff {
		t.Fatalf("repeated history-reset refresh left cell %#x", got)
	}
}

func TestRefreshModeRayRetainsTilePairForLowEmitterThrottle(t *testing.T) {
	s := refreshRayService()
	var eligible [10]bool
	eligible[0] = true
	s.Refresh(7, Observer{Owner: 0, CX: 11, CZ: 12, HeightByte: 20, Radius: 20})
	idx := int(12*s.W + 11)
	if s.byteGrids[0][idx] != 1 {
		t.Fatal("fixture did not publish initial ray coverage")
	}

	// The grid reset occurs first. Clearing only the stored byte makes the
	// ordinary ray comparison accept an unchanged pair with emitter <= 5, so
	// it does not republish that tile.
	s.RefreshMode(ModeHistoryEnabled|ModeCurrentEnabled|ModeTerrainRay, false, eligible, []ModeRefreshObserver{{
		ID: 7, Observer: Observer{Owner: 0, CX: 11, CZ: 12, HeightByte: 5, Radius: 20},
	}})
	if got := s.byteGrids[0][idx]; got != 0 {
		t.Fatalf("low-emitter unchanged ray tile republished count %d, want 0", got)
	}
	// The following ordinary phase-5 visit sees the same retained pair and
	// cleared byte, so it must take the same throttle rather than republishing.
	s.Refresh(7, Observer{Owner: 0, CX: 11, CZ: 12, HeightByte: 5, Radius: 20})
	if got := s.byteGrids[0][idx]; got != 0 {
		t.Fatalf("ordinary refresh bypassed retained low-emitter throttle: %d", got)
	}
	stored := s.footprints[7]
	if stored.storedCX != 11 || stored.storedCZ != 12 || stored.storedByte != 0 {
		t.Fatalf("saved ray state = %+v, want retained pair with cleared byte", stored)
	}
	if s.RetireObserver(7) {
		t.Fatal("retiring an inactive retained record removed cleared coverage")
	}
}

func TestRefreshModeCurrentDisabledKeepsObserverFieldsAndFillsEligibleOnly(t *testing.T) {
	s := newTestService(&world.Terrain{CellW: 128, CellH: 128}, ModeHistoryEnabled|ModeCurrentEnabled)
	s.Refresh(3, Observer{Owner: 0, CX: 20, CZ: 21, HeightByte: 9, Radius: 200})
	before := s.footprints[3]
	s.byteGrids[1][0] = 7 // inactive player: retail does not refill this grid
	var eligible [10]bool
	eligible[0] = true

	s.RefreshMode(ModeHistoryEnabled|ModeTerrainRay, false, eligible, []ModeRefreshObserver{{
		ID: 3, Observer: Observer{Owner: 0, CX: 40, CZ: 41, HeightByte: 30, Radius: 300},
	}})
	after := s.footprints[3]
	if after != before {
		t.Fatalf("current-disabled refresh changed stored observer: got %+v want %+v", after, before)
	}
	for i, got := range s.byteGrids[0] {
		if got != 1 {
			t.Fatalf("eligible permanent byte cell %d = %d, want 1", i, got)
		}
	}
	if got := s.byteGrids[1][0]; got != 7 {
		t.Fatalf("ineligible byte grid was refilled to %d", got)
	}
	if s.RetireObserver(3) {
		t.Fatal("retirement through a current-disabled mode changed the filled grid")
	}
}

func TestRefreshModeSpriteDirectlyReplacesRayRecord(t *testing.T) {
	s := refreshRayService()
	var eligible [10]bool
	eligible[0] = true
	s.Refresh(4, Observer{Owner: 0, CX: 4, CZ: 4, HeightByte: 20, Radius: 20})
	s.SetShapes(fixtureShapes())
	// Radius 200 selects frame 1, whose authored anchor is (6,6). The normal
	// sprite throttle keeps its observer cell, but the live transition retains
	// the frame origin separately for a later ray comparison.
	s.RefreshMode(ModeHistoryEnabled|ModeCurrentEnabled, false, eligible, []ModeRefreshObserver{{
		ID: 4, Observer: Observer{Owner: 0, CX: 30, CZ: 31, HeightByte: 3, Radius: 200},
	}})
	stored := s.footprints[4]
	if !stored.live || stored.cx != 30 || stored.cz != 31 || stored.storedCX != 24 || stored.storedCZ != 25 {
		t.Fatalf("circular direct refresh stored wrong coordinate forms: %+v", stored)
	}
	if got := s.byteGrids[0][int(31*s.W+30)]; got != 1 {
		t.Fatalf("circular direct refresh coverage = %d, want 1", got)
	}
	// The new ray tile equals the retained sprite origin, rather than the
	// sprite observer cell, so the cleared-byte low-emitter exception applies.
	s.RefreshMode(ModeHistoryEnabled|ModeCurrentEnabled|ModeTerrainRay, false, eligible, []ModeRefreshObserver{{
		ID: 4, Observer: Observer{Owner: 0, CX: 24, CZ: 25, HeightByte: 5, Radius: 20},
	}})
	if got := s.byteGrids[0][int(25*s.W+24)]; got != 0 {
		t.Fatalf("ray throttle compared sprite cell instead of stored origin: %d", got)
	}
	s.Refresh(4, Observer{Owner: 0, CX: 24, CZ: 25, HeightByte: 5, Radius: 20})
	if got := s.byteGrids[0][int(25*s.W+24)]; got != 0 {
		t.Fatalf("ordinary ray refresh lost the retained sprite-origin throttle: %d", got)
	}
	if s.RetireObserver(4) {
		t.Fatal("retiring cross-mode inactive record changed cleared coverage")
	}
}
