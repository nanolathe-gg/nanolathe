//go:build pathbench && retail

package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// pbTracer is a diagnostic-only trajectory dump for visual inspection of
// jams. It runs only on the diagnostic pass when NANOLATHE_PBTRACE
// names a directory, and never on a timed pass.
type pbTracer struct {
	f     *os.File
	every int
}

type pbTraceHeader struct {
	Case   string
	Rules  string
	Size   int
	W, H   int32
	Cells  string // one byte per cell: '.' open, '#' void, 'f' feature, digit height tier
	Goals  [][3]float64
	Starts [][3]float64
}

type pbTraceFrame struct {
	Tick  int
	Units [][6]float64 // cellX, cellZ, owner, blocked, hasOrder, handle
}

func pbTraceOpen(sc *pbScene, id, rules string, n int) *pbTracer {
	dir := os.Getenv("NANOLATHE_PBTRACE")
	if dir == "" {
		return nil
	}
	name := strings.NewReplacer("/", "_").Replace(fmt.Sprintf("%s__%s__n%d.jsonl", id, rules, n))
	f, e := os.Create(filepath.Join(dir, name))
	if e != nil {
		return nil
	}
	ter := sc.S.World
	var b strings.Builder
	for z := int32(0); z < ter.CellH; z++ {
		for x := int32(0); x < ter.CellW; x++ {
			p := ter.PlotAt(x, z)
			switch {
			case p.IsVoid():
				b.WriteByte('#')
			case p.IsRealFeature():
				b.WriteByte('f')
			default:
				h := p.Height()
				b.WriteByte(byte('0' + min(9, int(h)/16)))
			}
		}
	}
	hd := pbTraceHeader{Case: id, Rules: rules, Size: n, W: ter.CellW, H: ter.CellH, Cells: b.String()}
	enc, _ := json.Marshal(hd)
	f.Write(enc)
	f.Write([]byte("\n"))
	every := 15
	if v, e := strconv.Atoi(os.Getenv("NANOLATHE_PBTRACE_EVERY")); e == nil && v > 0 {
		every = v
	}
	return &pbTracer{f: f, every: every}
}

func (tr *pbTracer) frame(sc *pbScene, tick int) {
	if tr == nil || tick%tr.every != 0 {
		return
	}
	fr := pbTraceFrame{Tick: tick}
	sys := sc.S.Movement
	for player := 0; player < 10; player++ {
		sc.S.Units.ForEachPlayerSliceLive(player, func(u *units.Unit) {
			if u.Def == nil || u.Def.CanFly {
				return
			}
			blocked := 0.0
			if sys != nil && int(u.Handle) < len(sys.Collisions) {
				if c := sys.Collisions[u.Handle]; c != nil && c.Blocked {
					blocked = 1
				}
			}
			has := 0.0
			if q := orders.QueueOfUnit(u); q != nil && q.Head() != nil {
				has = 1
			}
			fr.Units = append(fr.Units, [6]float64{float64(u.X.Raw()) / 65536 / 16, float64(u.Z.Raw()) / 65536 / 16, float64(u.Owner), blocked, has, float64(u.Handle)})
		})
	}
	enc, _ := json.Marshal(fr)
	tr.f.Write(enc)
	tr.f.Write([]byte("\n"))
}

func (tr *pbTracer) close(sc *pbScene) {
	if tr == nil {
		return
	}
	var goals [][3]float64
	for _, a := range sc.Actors {
		if !a.GoalActive {
			continue
		}
		goals = append(goals, [3]float64{float64(a.Goal[0].Raw()) / 65536 / 16, float64(a.Goal[1].Raw()) / 65536 / 16, float64(a.Radius) / 16})
	}
	enc, _ := json.Marshal(map[string]any{"Goals": goals})
	tr.f.Write(enc)
	tr.f.Write([]byte("\n"))
	tr.f.Close()
}
