//go:build retail

package session

import (
	"encoding/binary"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/gameplay"
	"github.com/nanolathe-gg/nanolathe/internal/units"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// TestRetailConstructedWindGeneratorFollowsEveryRedraw is the end-to-end
// guard for the stock-content path investigated in issue #195. A stock
// wind generator the commander builds is paid
// `currentWindScalar × windgenerator` at every settlement pass, and the scalar
// it multiplies is the one published before that tick's economy phase, so the
// payment follows each phase-8 redraw rather than the value at completion
// [05 "Wind generation"][05 R-ECO-01 §2][05 R-PROD-01 §3]. The redraw's
// stock ARMWIN script pair (SetDirection, SetSpeed) leaves the activation bit
// set; the generator arm depends on that bit [05 R-PROD-01 §2].
//
// The ARMWIN script is the authored corpus; the test asserts the relationship
// at each pass, not a census of values. Both reserved rule sets are checked:
// no Modern policy touches this path.
func TestRetailConstructedWindGeneratorFollowsEveryRedraw(t *testing.T) {
	for _, mode := range []gameplay.Mode{gameplay.Strict31, gameplay.Modern} {
		t.Run(string(mode), func(t *testing.T) {
			f := loadRetailFixture(t)
			f.cfg.Gameplay = mode
			checkRetailWindGeneratorPaidEveryRedraw(t, f.fs, f.cat, f.cfg)
		})
	}
}

// TestRetailWindGeneratorSetSpeedActivationWriteKeepsItOn covers the content
// reported in issue #195: UberHack 4.0's wind generators execute
// `set ACTIVATION to 100` in SetSpeed once the unit is complete. The port-1
// write arm hands the whole value to the edge machine, which treats any
// nonzero value as on, so the write is a no-op on the active generator and it
// keeps being paid at every redraw [04 R-COB-03 §4][04 R-UNIT-06 §2]. A
// low-bit reading of the value would switch the generator off at the first
// redraw after completion.
//
// The third-party script is not used. The fixture is the stock ARMWIN script
// with an authored prologue on SetSpeed that performs the same write under the
// same completion guard and then jumps into the stock body. The patched bytes
// are served in place of scripts/armwin.cob, so the battle's capture and the
// catalog agree on the program (DESIGN_MULTIPLAYER §8.7).
func TestRetailWindGeneratorSetSpeedActivationWriteKeepsItOn(t *testing.T) {
	for _, mode := range []gameplay.Mode{gameplay.Strict31, gameplay.Modern} {
		t.Run(string(mode), func(t *testing.T) {
			f := loadRetailFixture(t)
			f.cfg.Gameplay = mode
			const scriptPath = "scripts/armwin.cob"
			stock, err := f.fs.ReadFileLimit(scriptPath, 4<<20)
			if err != nil {
				t.Skipf("retail %s is absent: %v", scriptPath, err)
			}
			patched := withSetSpeedActivationWrite(t, stock, 100)
			prog, err := cob.Load(patched)
			if err != nil {
				t.Fatalf("patched %s: %v", scriptPath, err)
			}
			cat := f.cat.Clone()
			def, ok := cat.Unit("ARMWIN")
			if !ok || def == nil {
				t.Skip("retail ARMWIN is absent")
			}
			def.Script = prog
			fs := meteorStageOverrideFS{FSOps: f.fs, path: scriptPath, data: patched}
			checkRetailWindGeneratorPaidEveryRedraw(t, fs, cat, f.cfg)
		})
	}
}

// withSetSpeedActivationWrite returns a copy of a COB file whose SetSpeed
// entry first runs `if (!get BUILD_PERCENT_LEFT) set ACTIVATION to value` and
// then jumps to the original SetSpeed body, keeping its argument window. The
// prologue is appended to the code section and every table offset behind it
// moves by its size [fmt cob "Header"].
func withSetSpeedActivationWrite(t *testing.T, data []byte, value int32) []byte {
	t.Helper()
	stock, err := cob.Load(data)
	if err != nil {
		t.Fatalf("stock script: %v", err)
	}
	entry, ok := stock.Scripts["SetSpeed"]
	if !ok {
		t.Fatal("stock script has no SetSpeed")
	}
	u32 := func(b []byte, off uint32) uint32 { return binary.LittleEndian.Uint32(b[off:]) }
	numScripts, numPieces, codeLen := u32(data, 0x04), u32(data, 0x08), u32(data, 0x0C)
	if u32(data, 0x14) != 0 {
		t.Skip("stock script carries trailing records; the patch does not relocate them")
	}
	start := codeLen
	prologue := []uint32{
		0x10021001, 17, // push BUILD_PERCENT_LEFT
		0x10042000,                     // get
		0x1005a000,                     // logical not
		0x10066000, uint32(start + 11), // jump-if-false to the stock body
		0x10021001, 1, // push ACTIVATION
		0x10021001, uint32(value), // push value
		0x10082000,                // set
		0x10064000, uint32(entry), // jump to the stock SetSpeed
	}
	at := u32(data, 0x24) + codeLen*4
	delta := uint32(len(prologue) * 4)
	out := make([]byte, 0, len(data)+int(delta))
	out = append(out, data[:at]...)
	for _, word := range prologue {
		out = binary.LittleEndian.AppendUint32(out, word)
	}
	out = append(out, data[at:]...)
	shift := func(off uint32) {
		if v := u32(out, off); v >= at {
			binary.LittleEndian.PutUint32(out[off:], v+delta)
		}
	}
	for _, off := range []uint32{0x18, 0x1C, 0x20, 0x28} {
		shift(off)
	}
	binary.LittleEndian.PutUint32(out[0x0C:], codeLen+uint32(len(prologue)))
	names, pieces, index := u32(out, 0x1C), u32(out, 0x20), u32(out, 0x18)
	for i := uint32(0); i < numScripts; i++ {
		shift(names + i*4)
	}
	for i := uint32(0); i < numPieces; i++ {
		shift(pieces + i*4)
	}
	for i := uint32(0); i < numScripts; i++ {
		if u32(out, index+i*4) == uint32(entry) {
			binary.LittleEndian.PutUint32(out[index+i*4:], start)
		}
	}
	return out
}

// checkRetailWindGeneratorPaidEveryRedraw builds ARMWIN with the ARM commander
// and checks, at every settlement pass across at least three redraws, that the
// generator is still activated and paid the wind scalar published before that
// tick times its windgenerator [05 R-PROD-01 §3][05 R-ECO-01 §2].
func checkRetailWindGeneratorPaidEveryRedraw(t *testing.T, fs vfs.FSOps, cat *content.Catalog, cfg SkirmishConfig) {
	t.Helper()
	s, err := NewSkirmishWithProgress(fs, cat, cfg, nil)
	if err != nil {
		t.Fatalf("construct retail %q: %v", retailMap, err)
	}
	if err := s.ValidateComposition(); err != nil {
		t.Fatalf("validate retail composition: %v", err)
	}
	const key = "ARMWIN"
	com := retailUnit(s, 0, retailARM)
	if com == nil {
		t.Fatal("ARM commander not spawned")
	}
	x, z := retailBuildSite(t, s, cat, com, key)
	if err := construction.QueueMobileBuild(com, key, x, z, 1, cat); err != nil {
		t.Fatalf("queue %s: %v", key, err)
	}
	var gen *units.Unit
	for i := 0; i < 3000 && (gen == nil || gen.Remaining != 0); i++ {
		stepRetail(s, 1)
		gen = retailUnit(s, 0, key)
	}
	if gen == nil || gen.Remaining != 0 {
		t.Fatalf("%s was not completed", key)
	}
	if !(gen.Def.WindGenerator > 0) {
		t.Fatalf("%s authors no windgenerator", key)
	}
	generator := float64(float32(gen.Def.WindGenerator))

	redraws, paidAfterThirdRedraw := 0, 0
	for i := 0; i < 3000 && paidAfterThirdRedraw < 3; i++ {
		// The settlement of tick N reads the scalar tick N-1's
		// wind phase published [05 R-PROD-01 §3].
		scalar := s.Econ.WindScalar()
		stepRetail(s, 1)
		tick := s.Clock.GlobalTick
		if s.Wind.Changed {
			redraws++
		}
		if s.Econ.ActivityReportTick(0) != tick {
			continue // no settlement pass for the owner this tick
		}
		if !gen.Alive || !gen.Activated {
			t.Fatalf("tick %d after %d redraws: generator alive=%v activated=%v", tick, redraws, gen.Alive, gen.Activated)
		}
		want := float32(scalar)
		want = float32(float64(want) * generator)
		if got := s.Econ.UnitArchived(gen.Handle)[economy.Energy].Production; got != want {
			t.Fatalf("tick %d after %d redraws: generator paid %v, want scalar %v × %v = %v", tick, redraws, got, scalar, generator, want)
		}
		if redraws >= 3 && want > 0 {
			paidAfterThirdRedraw++
		}
	}
	if paidAfterThirdRedraw < 3 {
		t.Fatalf("observed %d redraws and %d positive passes after the third; the scenario no longer exercises repeated redraws", redraws, paidAfterThirdRedraw)
	}
}
