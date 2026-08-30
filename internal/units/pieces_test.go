package units

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/cob"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/model"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
)

func TestBuildRenderPieceFlags_GeometryDefaults(t *testing.T) {
	// Model hierarchy walk sets bit1|bit2 always and bit0 only when >=3 vertices [04 §"Piece flag polarity"].
	mdl := &model.Model{
		Pieces: []model.Piece{
			{
				Name:     "base",
				Parent:   -1,
				Vertices: nil, // 0 vertices -> bare attachment point -> 0x06
			},
			{
				Name:   "turret",
				Parent: 0,
				Vertices: [][3]numeric.Fixed{
					{0, 0, 0},
					{1, 0, 0},
				}, // 2 vertices -> still 0x06
			},
			{
				Name:   "barrel",
				Parent: 1,
				Vertices: [][3]numeric.Fixed{
					{0, 0, 0},
					{1, 0, 0},
					{2, 0, 0},
				}, // exactly 3 -> drawn -> 0x07
			},
			{
				Name:   "flare",
				Parent: 1,
				Vertices: [][3]numeric.Fixed{
					{0, 0, 0}, {1, 0, 0}, {2, 0, 0}, {3, 0, 0}, {4, 0, 0},
				}, // 5 -> 0x07
			},
		},
		Root: 0,
	}
	flags := BuildRenderPieceFlags(mdl)
	if len(flags) != 4 {
		t.Fatalf("flags len %d want 4", len(flags))
	}
	expect := []uint8{0x06, 0x06, 0x07, 0x07}
	for i, want := range expect {
		if flags[i] != want {
			t.Fatalf("piece %d flags %#x want %#x [04 §\"Piece flag polarity\"]", i, flags[i], want)
		}
		// Bits 1 and 2 must always be set.
		if flags[i]&0x02 == 0 || flags[i]&0x04 == 0 {
			t.Fatalf("piece %d missing cache/shade bits: %#x", i, flags[i])
		}
	}
	// Verify building via unit method matches.
	u := &Unit{}
	u.InitRenderPieceFlags(mdl)
	if len(u.RenderPieceFlags) != 4 {
		t.Fatalf("unit flags len %d want 4", len(u.RenderPieceFlags))
	}
	for i, want := range expect {
		if u.RenderPieceFlags[i] != want {
			t.Fatalf("unit piece %d flags %#x want %#x", i, u.RenderPieceFlags[i], want)
		}
	}
}

func TestRenderPieceTableBeforeScriptAttachment(t *testing.T) {
	// The render-piece table is model-owned and may be built before the VM is
	// attached; it must not live inside the VM [R-COB-01 §1] [04 §"Piece flag polarity"].
	mdl := &model.Model{
		Pieces: []model.Piece{
			{Name: "base", Parent: -1, Vertices: [][3]numeric.Fixed{{0, 0, 0}, {1, 0, 0}, {2, 0, 0}}},
			{Name: "pad", Parent: 0, Vertices: nil},
		},
		Root: 0,
	}
	u := &Unit{Alive: true}
	u.InitRenderPieceFlags(mdl)
	if u.RenderPieceFlags == nil {
		t.Fatalf("pre-attachment unit has nil RenderPieceFlags [04 §\"Piece flag polarity\"]")
	}
	if len(u.RenderPieceFlags) != 2 {
		t.Fatalf("pre-attachment flags len %d want 2", len(u.RenderPieceFlags))
	}
	if u.RenderPieceFlags[0] != 0x07 {
		t.Fatalf("geometry piece flags %#x want 0x07", u.RenderPieceFlags[0])
	}
	if u.RenderPieceFlags[1] != 0x06 {
		t.Fatalf("bare piece flags %#x want 0x06", u.RenderPieceFlags[1])
	}
	if u.GetScript() != nil {
		t.Fatalf("pre-attachment unit unexpectedly has a VM [R-COB-01 §1]")
	}
}

func TestUnitRenderFlagOpcodesToggleExactlyOneBit(t *testing.T) {
	// Each of the six opcodes must toggle exactly its bit via the unit record [04 §"Piece flag polarity"].
	mdl := &model.Model{
		Pieces: []model.Piece{
			{Name: "base", Parent: -1, Vertices: [][3]numeric.Fixed{{0, 0, 0}, {1, 0, 0}, {2, 0, 0}}}, // 0x07
			{Name: "arm", Parent: 0, Vertices: nil},                                                   // 0x06
		},
		Root: 0,
	}
	prog := &cob.Program{
		Code:        []uint32{0x10065000}, // dummy, will be replaced per subtest
		Scripts:     map[string]int{"Test": 0},
		Pieces:      []string{"base", "arm"},
		ScriptsByID: []int{0},
		Statics:     0,
	}
	// Build unit flags prog-mapped [04 §"Piece flag polarity"].
	// For identity names, prog-mapped equals model walk.
	flags := BuildRenderPieceFlagsForProgram(mdl, prog, nil)
	if len(flags) != 2 {
		t.Fatalf("flags len %d", len(flags))
	}
	// Pair: lower sets, higher clears.
	cases := []struct {
		name    string
		opcode  uint32
		mask    uint8
		set     bool
		piece   int
		prepare uint8 // initial flags for that piece
	}{
		{"show sets bit0", 0x10005000, 0x01, true, 1, 0x06},
		{"hide clears bit0", 0x10006000, 0x01, false, 0, 0x07},
		{"cache sets bit1", 0x10007000, 0x02, true, 0, 0x06 ^ 0x02}, // clear bit1 then set
		{"dont-cache clears bit1", 0x10008000, 0x02, false, 0, 0x07},
		{"shade sets bit2", 0x1000d000, 0x04, true, 0, 0x06 ^ 0x04}, // clear shade then set
		{"dont-shade clears bit2", 0x1000e000, 0x04, false, 0, 0x07},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := &Unit{Alive: true}
			u.RenderPieceFlags = make([]uint8, len(flags))
			copy(u.RenderPieceFlags, flags)
			// Set the piece under test to known prepare value
			if tc.piece < len(u.RenderPieceFlags) {
				u.RenderPieceFlags[tc.piece] = tc.prepare
			}
			otherPiece := 1 - tc.piece
			beforeOther := u.RenderPieceFlags[otherPiece]
			// Build program with single flag opcode then return.
			code := []uint32{tc.opcode, uint32(tc.piece), 0x10065000}
			p := &cob.Program{
				Code:        code,
				Scripts:     map[string]int{"Test": 0},
				Pieces:      []string{"base", "arm"},
				ScriptsByID: []int{0},
			}
			vm := cob.NewVM(p)
			// Bind via unit record [04 §"Piece flag polarity"].
			vm.BindRenderFlagHandlers(func() []uint8 { return u.RenderPieceFlags }, func(piece int, mask uint8, set bool) bool {
				return u.SetRenderPieceFlag(piece, mask, set)
			})
			vm.Threads[0].Status = cob.ThreadRunning
			vm.Threads[0].PC = 0
			vm.Drain(1)
			if vm.Threads[0].Status != cob.ThreadIdle {
				t.Fatalf("thread should have terminated, status %d", vm.Threads[0].Status)
			}
			after := u.RenderPieceFlags[tc.piece]
			var want uint8
			if tc.set {
				want = tc.prepare | tc.mask
			} else {
				want = tc.prepare &^ tc.mask
			}
			if after != want {
				t.Fatalf("piece %d after %#x want %#x (prepare %#x mask %#x set %v)", tc.piece, after, want, tc.prepare, tc.mask, tc.set)
			}
			// Ensure exactly one bit changed and no other bits affected.
			diff := tc.prepare ^ after
			if diff != tc.mask {
				t.Fatalf("changed bits %#x want exactly %#x", diff, tc.mask)
			}
			if u.RenderPieceFlags[otherPiece] != beforeOther {
				t.Fatalf("other piece changed: before %#x after %#x", beforeOther, u.RenderPieceFlags[otherPiece])
			}
			// Also verify SnapshotFlags reflects unit record when bound.
			snap := vm.SnapshotFlags()
			if len(snap) != len(u.RenderPieceFlags) {
				t.Fatalf("snapshot len %d want %d", len(snap), len(u.RenderPieceFlags))
			}
			for i := range snap {
				if snap[i] != u.RenderPieceFlags[i] {
					t.Fatalf("snapshot[%d] %#x != unit %#x", i, snap[i], u.RenderPieceFlags[i])
				}
			}
		})
	}
}

func TestWorldCreateFixtureSharesFlags(t *testing.T) {
	// Verify the fallback fixture path shares VM flags with the unit [04 §"Piece flag polarity"].
	world := newFixtureWorld(4, nil)
	def := &content.UnitDef{UnitName: "flagshare", MaxDamage: 100, Limit: -1}
	// Provide a synthetic program via Def.Script
	def.Script = &cob.Program{
		Code: []uint32{
			0x10005000, 0, // show piece 0
			0x10065000, // return
		},
		Scripts:     map[string]int{"Create": 0},
		Pieces:      []string{"base"},
		ScriptsByID: []int{0},
	}
	h, err := world.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	u := world.Unit(h)
	if u == nil {
		t.Fatal("unit nil")
	}
	if len(u.RenderPieceFlags) == 0 {
		t.Fatalf("unit RenderPieceFlags empty after fixture create")
	}
	// The VM should be bound to the same storage; draining a show should affect unit.
	// Create already ran show, so piece should be drawn (bit0 set). Check flag is 0x07 or 0x06|0x01
	if u.RenderPieceFlags[0]&0x01 == 0 {
		t.Fatalf("fixture Create show did not set draw bit via unit record: %#x", u.RenderPieceFlags[0])
	}
}
