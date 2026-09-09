package cob

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
)

type recordingExplosionSink struct {
	vm          *VM
	sim         *rng.Simulation
	sourcePiece int
	calls       []explosionAdmission
	whole       []WholePieceExplosion
	shatter     []ShatterExplosion
	bitmaps     []BitmapExplosion
	admit       bool
}

// explosionAdmission captures the authoritative state at the instant an arena
// decision is requested. A shared sequence, rather than post-drain slices,
// makes draw/hide/order regressions observable [04 R-COB-04 §1].
type explosionAdmission struct {
	kind        string
	bitmap      BitmapExplosionKind
	draws       uint64
	sourceFlags uint8
	eventFlags  uint8
}

func (s *recordingExplosionSink) observe(kind string, bitmap BitmapExplosionKind, source ExplosionSource) {
	call := explosionAdmission{kind: kind, bitmap: bitmap, eventFlags: source.State.RenderFlags}
	if s.sim != nil {
		call.draws = s.sim.Draws()
	}
	if s.vm != nil {
		flags := s.vm.renderPieceFlags()
		if s.sourcePiece >= 0 && s.sourcePiece < len(flags) {
			call.sourceFlags = flags[s.sourcePiece]
		}
	}
	s.calls = append(s.calls, call)
}

func (s *recordingExplosionSink) AdmitWholePiece(event WholePieceExplosion) bool {
	s.observe("whole", 0, event.Source)
	s.whole = append(s.whole, event)
	return s.admit
}

func (s *recordingExplosionSink) AdmitShatter(event ShatterExplosion) bool {
	s.observe("shatter", 0, event.Source)
	s.shatter = append(s.shatter, event)
	return s.admit
}

func (s *recordingExplosionSink) AdmitBitmap(event BitmapExplosion) bool {
	s.observe("bitmap", event.Kind, event.Source)
	s.bitmaps = append(s.bitmaps, event)
	return s.admit
}

func TestExplodeOffersWholePieceAfterDrawsAndHide(t *testing.T) {
	const flags = 0x1e | 0x100 | 0x400 // physical whole piece plus bitmap 1 and 3
	vm, sim := newCensusVM(t, 0, []uint32{
		0x10021001, flags,
		0x10071000, 0,
		0x10065000,
	})
	vm.pieceFlags[0] = 0x07
	vm.Pieces[0].RotY = 99
	sink := &recordingExplosionSink{vm: vm, sim: sim, sourcePiece: 0} // allocation refusal is still a real offer.
	vm.SetExplosionSink(sink)
	vm.Drain(1)

	if got := sim.Draws(); got != 6 {
		t.Fatalf("whole-piece physical explode drew %d values, want 6 [04 R-COB-04 §1]", got)
	}
	if got := vm.pieceFlags[0]; got != 0x06 {
		t.Fatalf("physical source flags %#x, want draw bit cleared before refusal [04 R-COB-04 §1]", got)
	}
	if len(sink.whole) != 1 || len(sink.shatter) != 0 {
		t.Fatalf("physical request counts whole=%d shatter=%d, want 1/0", len(sink.whole), len(sink.shatter))
	}
	got := sink.whole[0]
	if got.Source.Identity != (ExplosionPieceIdentity{COBPiece: 0, GeometryName: "base"}) {
		t.Fatalf("source identity %#v, want base COB piece 0", got.Source.Identity)
	}
	if got.Source.State.RenderFlags != 0x06 || got.Source.State.Transform.RotY != 99 {
		t.Fatalf("source state %#v, want post-hide flags and script transform", got.Source.State)
	}
	if got.Flags != ExplosionFire|ExplosionSmoke|ExplosionFall|ExplosionWholePieceOnHit {
		t.Fatalf("physical flags %#x, want fire/smoke/fall/whole-on-hit", got.Flags)
	}

	wantRNG := rng.NewSimulation(1)
	wantRates := [3]uint16{uint16(wantRNG.Uint32n(3000)), uint16(wantRNG.Uint32n(3000)), uint16(wantRNG.Uint32n(3000))}
	wantVelocity := [3]numeric.Fixed{
		numeric.Fixed((int32(20) - int32(wantRNG.Uint32n(40))) << 14),
		numeric.Fixed(int32(wantRNG.Uint32n(10)) << 16),
		numeric.Fixed((int32(20) - int32(wantRNG.Uint32n(40))) << 14),
	}
	if got.Kinematics.AngularRates != wantRates || got.Kinematics.Velocity != wantVelocity || got.Kinematics.Lifetime != 900 {
		t.Fatalf("kinematics %#v, want rates %#v velocity %#v lifetime 900 [04 R-COB-04 §1]", got.Kinematics, wantRates, wantVelocity)
	}
	if len(sink.bitmaps) != 2 || sink.bitmaps[0].Kind != BitmapExplosionPrimary || sink.bitmaps[1].Kind != BitmapExplode3 {
		t.Fatalf("bitmap requests %#v, want bitmap1 then bitmap3 [04 R-COB-04 §1]", sink.bitmaps)
	}
	wantCalls := []explosionAdmission{
		{kind: "whole", draws: 6, sourceFlags: 0x06, eventFlags: 0x06},
		{kind: "bitmap", bitmap: BitmapExplosionPrimary, draws: 6, sourceFlags: 0x06, eventFlags: 0x06},
		{kind: "bitmap", bitmap: BitmapExplode3, draws: 6, sourceFlags: 0x06, eventFlags: 0x06},
	}
	if len(sink.calls) != len(wantCalls) {
		t.Fatalf("admission sequence %#v, want %#v", sink.calls, wantCalls)
	}
	for i, want := range wantCalls {
		if got := sink.calls[i]; got != want {
			t.Fatalf("admission %d = %#v, want %#v [04 R-COB-04 §1]", i, got, want)
		}
	}
}

func TestExplodeShatterRefusalDrawsNoFragments(t *testing.T) {
	// A full effect pool stops before its first fragment seed. The COB-side
	// boundary therefore makes only the established six physical draws [04
	// R-COB-04 §1] [04 R-COB-04 §3].
	vm, sim := newCensusVM(t, 0, []uint32{
		0x10021001, 0x03 | 0x100 | 0x400, // SHATTER|EXPLODE_ON_HIT plus bitmap 1 and 3
		0x10071000, 0,
		0x10065000,
	})
	vm.pieceFlags[0] = 0x07
	sink := &recordingExplosionSink{vm: vm, sim: sim, sourcePiece: 0}
	vm.SetExplosionSink(sink)
	vm.Drain(1)

	if got := sim.Draws(); got != 6 {
		t.Fatalf("refused shatter drew %d values, want only six physical seeds [04 R-COB-04 §3]", got)
	}
	if got := vm.pieceFlags[0]; got&0x01 != 0 {
		t.Fatalf("refused shatter left source shown: flags %#x [04 R-COB-04 §1]", got)
	}
	if len(sink.whole) != 0 || len(sink.shatter) != 1 || sink.shatter[0].Flags != ExplosionShatter|ExplosionShatterOnHit {
		t.Fatalf("shatter boundary got whole=%d shatter=%#v, want one shatter-on-hit", len(sink.whole), sink.shatter)
	}
	wantCalls := []explosionAdmission{
		{kind: "shatter", draws: 6, sourceFlags: 0x06, eventFlags: 0x06},
		{kind: "bitmap", bitmap: BitmapExplosionPrimary, draws: 6, sourceFlags: 0x06, eventFlags: 0x06},
		{kind: "bitmap", bitmap: BitmapExplode3, draws: 6, sourceFlags: 0x06, eventFlags: 0x06},
	}
	if got, want := sink.calls, wantCalls; len(got) != len(want) {
		t.Fatalf("shatter admission sequence %#v, want %#v [04 R-COB-04 §1]", got, want)
	}
	for i, want := range wantCalls {
		if got := sink.calls[i]; got != want {
			t.Fatalf("shatter admission %d = %#v, want %#v [04 R-COB-04 §1]", i, got, want)
		}
	}
}

func TestBitmapOnlyExplodeOffersAscendingBitmapsWithoutHiding(t *testing.T) {
	const flags = 0x20 | 0x100 | 0x400 | 0x2000 // bitmap-only, 1, 3, nuke
	vm, sim := newCensusVM(t, 0, []uint32{
		0x10021001, flags,
		0x10071000, 0,
		0x10065000,
	})
	vm.pieceFlags[0] = 0x07
	sink := &recordingExplosionSink{vm: vm, sim: sim, sourcePiece: 0, admit: true}
	vm.SetExplosionSink(sink)
	vm.Drain(1)

	if got := sim.Draws(); got != 0 {
		t.Fatalf("bitmap-only explode drew %d values, want 0 [04 R-COB-04 §1]", got)
	}
	if got := vm.pieceFlags[0]; got != 0x07 {
		t.Fatalf("bitmap-only source flags %#x, want unchanged [04 R-COB-04 §1]", got)
	}
	if len(sink.whole) != 0 || len(sink.shatter) != 0 {
		t.Fatalf("bitmap-only offered physical work: whole=%d shatter=%d", len(sink.whole), len(sink.shatter))
	}
	want := []BitmapExplosionKind{BitmapExplosionPrimary, BitmapExplode3, BitmapNuke1}
	if len(sink.bitmaps) != len(want) {
		t.Fatalf("bitmap requests %d, want %d", len(sink.bitmaps), len(want))
	}
	for i, kind := range want {
		if sink.bitmaps[i].Kind != kind || sink.bitmaps[i].Source.State.RenderFlags != 0x07 {
			t.Fatalf("bitmap request %d = %#v, want kind %d with shown source", i, sink.bitmaps[i], kind)
		}
	}
	wantCalls := []explosionAdmission{
		{kind: "bitmap", bitmap: BitmapExplosionPrimary, draws: 0, sourceFlags: 0x07, eventFlags: 0x07},
		{kind: "bitmap", bitmap: BitmapExplode3, draws: 0, sourceFlags: 0x07, eventFlags: 0x07},
		{kind: "bitmap", bitmap: BitmapNuke1, draws: 0, sourceFlags: 0x07, eventFlags: 0x07},
	}
	if len(sink.calls) != len(wantCalls) {
		t.Fatalf("bitmap-only admission sequence %#v, want %#v", sink.calls, wantCalls)
	}
	for i, want := range wantCalls {
		if got := sink.calls[i]; got != want {
			t.Fatalf("bitmap-only admission %d = %#v, want %#v [04 R-COB-04 §1]", i, got, want)
		}
	}
}
