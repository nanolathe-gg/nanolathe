package airdiag

import (
	"fmt"
	"strings"

	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/movement"
	"github.com/nanolathe/nanolathe/internal/orders"
	"github.com/nanolathe/nanolathe/internal/pool"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/vfs"
)

// Harness owns one composed session and the units it spawned into it.
type Harness struct {
	FS      *vfs.FS
	Catalog *content.Catalog
	Session *session.Session

	tick int32
}

// New mounts the install at root, compiles the catalog and composes a two
// player skirmish on mapName.
func New(root, mapName string, simSeed, crtSeed uint32) (*Harness, error) {
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		return nil, fmt.Errorf("nanolathe: mounting install failed: logical path %s, providers searched [], expected a readable Total Annihilation install: %w", root, err)
	}
	cat, err := content.Compile(fs)
	if err != nil {
		_ = fs.Close()
		return nil, err
	}
	if err := cat.Validate(); err != nil {
		_ = fs.Close()
		return nil, err
	}
	cfg := session.SkirmishConfig{MapName: mapName, NumPlayers: 2, RNGSimSeed: simSeed, RNGCrtSeed: crtSeed}
	cfg.ApplyDefaults()
	cfg.Players[0].Side, cfg.Players[0].Controller = 0, session.SkirmishControllerHuman
	cfg.Players[1].Side, cfg.Players[1].Controller = 1, session.SkirmishControllerComputer
	cfg.Players[0].AllyGroup, cfg.Players[1].AllyGroup = 5, 5
	sess, err := session.NewSkirmishWithFS(fs, cat, cfg)
	if err != nil {
		_ = fs.Close()
		return nil, err
	}
	if err := sess.ValidateComposition(); err != nil {
		_ = fs.Close()
		return nil, err
	}
	h := &Harness{FS: fs, Catalog: cat, Session: sess}
	// One dispatch completes loading and the next runs the state-6 handler; the
	// battle tick loop begins after those, exactly as the session smoke helper
	// does it [08 "Session states"].
	h.Step(2)
	return h, nil
}

// Close releases the mounted install.
func (h *Harness) Close() {
	if h != nil && h.FS != nil {
		_ = h.FS.Close()
	}
}

// Unit returns the first live unit of the named definition owned by owner.
func (h *Harness) Unit(owner uint8, key string) *units.Unit {
	for _, u := range h.Session.Units.IterSliced() {
		if u != nil && u.Alive && u.Owner == owner && u.Def != nil && strings.EqualFold(u.Def.UnitName, key) {
			return u
		}
	}
	return nil
}

// Spawn creates a unit of the named definition at a world position and wires it
// into the movement system the way skirmish placement does.
func (h *Harness) Spawn(owner uint8, key string, x, z numeric.Fixed) (*units.Unit, error) {
	def, ok := h.Catalog.Unit(key)
	if !ok || def == nil {
		return nil, fmt.Errorf("nanolathe: definition lookup failed: logical path %s, providers searched [catalog], expected a compiled unit definition", key)
	}
	y := numeric.Fixed(0)
	if h.Session.World != nil {
		y = h.Session.World.HeightAt(x, z)
		if y == numeric.Fixed(-1) {
			y = 0
		}
	}
	handle, err := h.Session.Units.Create(def, owner, x, y, z)
	if err != nil {
		return nil, err
	}
	u := h.Session.Units.Unit(handle)
	if u == nil {
		return nil, fmt.Errorf("nanolathe: unit creation failed: logical path %s, providers searched [pool], expected a live unit record", key)
	}
	u.PlacementIdx = -1
	if h.Session.Movement != nil && h.Session.Movement.Routes != nil {
		h.Session.Movement.EnsureUnit(u)
	}
	return u, nil
}

// Order submits one human order through the ordinary command boundary, exactly
// as the client does for a right-click.
func (h *Harness) Order(u *units.Unit, code int, target pool.Handle, x, y, z numeric.Fixed) error {
	cmd := session.HumanCommand{
		Kind: session.HumanOrder,
		Order: session.HumanOrderCommand{
			Handles:  []pool.Handle{u.Handle},
			Code:     code,
			Target:   target,
			Position: orders.ResolvePos{X: x, Y: y, Z: z},
		},
	}
	return h.Session.EnqueueHumanCommand(cmd)
}

// Row is one tick of observation.
type Row struct {
	Tick uint32

	X, Y, Z numeric.Fixed
	Heading uint16
	Mode    uint8
	Speed   numeric.Fixed
	Bank    uint16

	FlightBound   bool
	FlightMode    uint8
	FlightTargetH uint16
	TargetX       int32
	TargetY       int32
	TargetZ       int32
	VX, VY, VZ    int32
	TurnResidual  int16
	TurnRate      int32

	CommandBound bool
	HasPayload   bool
	CommandPos   movement.Vec3
	CommandHead  uint16

	Marker movement.AirMarkerSnapshot

	HeadName    string
	HeadPhase   uint8
	HeadGate    uint32
	HeadSat     uint32
	QueueLen    int
	ExecPhase   uint8
	ExecWaiting bool
	ExecArrived bool
	ExecDone    bool
	ExecBound   bool
}

// Observe reads one row for a unit at the current committed tick.
func (h *Harness) Observe(u *units.Unit) Row {
	r := Row{
		Tick:    h.Session.Clock.GlobalTick,
		X:       u.X,
		Y:       u.Y,
		Z:       u.Z,
		Heading: u.Move.Heading,
		Mode:    u.Move.Mode,
		Speed:   u.Move.Speed,
		Bank:    u.Move.Bank,
	}
	if m := h.Session.Movement; m != nil {
		if fl := m.Flights[u.Handle]; fl != nil {
			r.FlightBound = true
			r.FlightMode = fl.Mode
			r.FlightTargetH = fl.TargetHeading
			r.TargetX, r.TargetY, r.TargetZ = fl.TargetX, fl.TargetY, fl.TargetZ
			r.VX, r.VY, r.VZ = fl.VX, fl.VY, fl.VZ
			r.TurnResidual = fl.TurnResidual
			r.TurnRate = fl.TurnRate
			if c := fl.Command; c != nil {
				r.CommandBound = true
				r.HasPayload = c.Payload != nil
				r.CommandPos = c.Pos
				r.CommandHead = c.Heading
			}
		}
		r.Marker = m.AirMarkerState(u.Handle)
		st := m.AirExecutorState(u.Handle)
		r.ExecBound, r.ExecPhase, r.ExecWaiting = st.Bound, st.Phase, st.Waiting
		r.ExecArrived, r.ExecDone = st.Arrived, st.Done
	}
	if q := orders.QueueForUnit(u); q != nil {
		r.QueueLen = q.LenPrimary()
		var head *orders.Node
		if q.LenPrimary() > 0 {
			head = q.Primary()[0]
		} else {
			head = q.Head()
		}
		if head != nil {
			r.HeadName = orders.DescriptorFor(head.ID).Name
			r.HeadPhase = head.Phase
			r.HeadGate = head.DynamicGate
			r.HeadSat = head.Satisfied
		}
	}
	return r
}

// Step advances n authoritative ticks.
func (h *Harness) Step(n int) {
	for i := 0; i < n; i++ {
		h.Session.Step(h.tick)
		h.tick++
	}
}

// Trace advances n ticks, recording one row per tick.
func (h *Harness) Trace(u *units.Unit, n int) []Row {
	rows := make([]Row, 0, n)
	for i := 0; i < n; i++ {
		h.Step(1)
		rows = append(rows, h.Observe(u))
	}
	return rows
}

// Format renders one row as a single diagnostic line.
func (r Row) Format() string {
	return fmt.Sprintf(
		"t=%-5d pos=(%7.2f,%7.2f,%7.2f) hd=%5d mode=%d spd=%6.2f | flt m=%d tgtH=%5d tgt=(%7.2f,%7.2f,%7.2f) v=(%7.3f,%7.3f,%7.3f) res=%5d rate=%d | cmd payload=%-5v pos=(%7.2f,%7.2f,%7.2f) hd=%5d | mk=%v f=%#04x r=%d alt=%d | ord=%-16s ph=%d gate=%#x sat=%#x qlen=%d | exec b=%v ph=%d wait=%v arr=%v done=%v",
		r.Tick,
		fx(r.X), fx(r.Y), fx(r.Z), r.Heading, r.Mode, fx(r.Speed),
		r.FlightMode, r.FlightTargetH,
		fx(numeric.Fixed(r.TargetX)), fx(numeric.Fixed(r.TargetY)), fx(numeric.Fixed(r.TargetZ)),
		fx(numeric.Fixed(r.VX)), fx(numeric.Fixed(r.VY)), fx(numeric.Fixed(r.VZ)),
		r.TurnResidual, r.TurnRate,
		r.HasPayload, fx(r.CommandPos.X), fx(r.CommandPos.Y), fx(r.CommandPos.Z), r.CommandHead,
		r.Marker.IsMarker, r.Marker.Flags, r.Marker.ArrivalRadius, r.Marker.AltOffset,
		r.HeadName, r.HeadPhase, r.HeadGate, r.HeadSat, r.QueueLen,
		r.ExecBound, r.ExecPhase, r.ExecWaiting, r.ExecArrived, r.ExecDone,
	)
}

func fx(f numeric.Fixed) float64 { return float64(int64(f)) / 65536.0 }
