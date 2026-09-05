package combat_test

import (
	"testing"

	"github.com/nanolathe/nanolathe/internal/combat"
	"github.com/nanolathe/nanolathe/internal/content"
	"github.com/nanolathe/nanolathe/internal/headless"
	"github.com/nanolathe/nanolathe/internal/session"
	"github.com/nanolathe/nanolathe/internal/sim/numeric"
	"github.com/nanolathe/nanolathe/internal/testsupport"
	"github.com/nanolathe/nanolathe/internal/units"
	"github.com/nanolathe/nanolathe/vfs"
)

// TestPeeweeFiresFromItsFlaresAtTheTargetsSweetSpot is the play-test lock for
// two reports — "Peewee bullets spawn from the centre of the model instead of
// the gun pieces" and "units shoot at the base of other units" — driven
// through the authoritative session on stock assets.
//
// The spawn point. The stock Peewee script answers `AimFromPrimary` with
// `ruparm`/`luparm` (the shoulders) and `QueryPrimary` with `rfire`/`lfire`
// (the barrel flares). Retail solves its angles from the former and spawns the
// projectile at the latter — the fire-time executor runs the forced `Query*`
// alone [06 §4.1][R-P0-07] — so every root record must start at a flare's
// composed world point, never at a shoulder and never at the unit's origin.
//
// The aim point. A live unit target resolves to the target's position plus
// the centre of its `SweetSpot` piece's vertex box [06 R-WPN-04 §1]; a solar
// collector answers piece 0 (`base`), whose box has positive height, so the
// stored aim point sits strictly above the collector's ground position.
func TestPeeweeFiresFromItsFlaresAtTheTargetsSweetSpot(t *testing.T) {
	root := testsupport.RetailRoot(t)
	fs := vfs.New()
	if err := fs.MountGameDirectory(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	cfg := session.SkirmishConfig{MapName: "ashap plateau", NumPlayers: 2}
	cfg.ApplyDefaults()
	cfg.Players[0].Controller = 0
	cfg.Players[1].Controller = 1
	composed, err := headless.ComposeFreshBattle(headless.FreshBattleRequest{
		Kind: headless.ScenarioSkirmish, Map: "ashap plateau", Skirmish: cfg, LocalOwner: 0, FS: fs,
	})
	if err != nil {
		t.Fatal(err)
	}
	sess := composed.Session
	scaled := sess.Clock.ScaledAnchor
	step := func(n int) {
		for i := 0; i < n; i++ {
			scaled++
			sess.Step(scaled)
		}
	}
	step(60)
	var com *units.Unit
	for _, u := range sess.Units.IterSliced() {
		if u != nil && u.Alive && u.Def != nil && u.Def.Commander && u.Owner == 0 {
			com = u
			break
		}
	}
	if com == nil {
		t.Skip("skirmish composed without a human commander")
	}
	shooterDef := sess.Catalog.Units[content.CanonicalKey("armpw")]
	victimDef := sess.Catalog.Units[content.CanonicalKey("armsolar")]
	if shooterDef == nil || victimDef == nil {
		t.Skip("stock armpw/armsolar absent from the catalog")
	}
	shooterH, err := sess.Units.Create(shooterDef, 0, com.X.Add(numeric.FixedFromInt(96)), com.Y, com.Z)
	if err != nil {
		t.Fatalf("create the Peewee: %v", err)
	}
	victimH, err := sess.Units.Create(victimDef, 1, com.X.Add(numeric.FixedFromInt(176)), com.Y, com.Z)
	if err != nil {
		t.Fatalf("create the target: %v", err)
	}
	shooter := sess.Units.Unit(shooterH)
	victim := sess.Units.Unit(victimH)
	binding := shooter.COBBinding()
	if binding == nil {
		t.Fatal("the Peewee has no script binding")
	}
	pieceIndex := func(name string) int32 {
		for i, n := range binding.Program.Pieces {
			if n == name {
				return int32(i)
			}
		}
		t.Fatalf("stock armpw script has no piece %q", name)
		return -1
	}
	flares := map[int32]bool{pieceIndex("rfire"): true, pieceIndex("lfire"): true}
	shoulders := map[int32]bool{pieceIndex("ruparm"): true, pieceIndex("luparm"): true}

	// The solar's SweetSpot box, computed here over the model directly so the
	// assertion does not lean on the resolver it locks.
	vb := victim.COBBinding()
	if vb == nil || vb.Model == nil {
		t.Fatal("the solar collector has no bound model")
	}
	sweet := vb.Callbacks.SweetSpot().QueryValue()
	if sweet < 0 || int(sweet) >= len(vb.PieceMap) {
		t.Fatalf("armsolar SweetSpot answered %d, outside its %d pieces", sweet, len(vb.PieceMap))
	}
	var lo, hi [3]int64
	for _, v := range vb.Model.Pieces[vb.PieceMap[sweet]].Vertices {
		for a := 0; a < 3; a++ {
			if r := v[a].Raw(); r < lo[a] {
				lo[a] = r
			} else if r > hi[a] {
				hi[a] = r
			}
		}
	}
	wantAim := combat.Vec3{
		X: victim.X.Add(numeric.Fixed((hi[0] + lo[0]) / 2)),
		Y: victim.Y.Add(numeric.Fixed((hi[1] + lo[1]) / 2)),
		Z: victim.Z.Add(numeric.Fixed((hi[2] + lo[2]) / 2)),
	}
	if wantAim.Y.Raw() <= victim.Y.Raw() {
		t.Fatalf("armsolar's SweetSpot piece box has no height (%v); the scenario cannot tell base from body", wantAim)
	}

	roots := 0
	for tick := 0; tick < 260 && roots < 4; tick++ {
		step(1)
		victim.Health = victim.MaxHealth
		now := sess.Clock.GlobalTick
		for i := 0; i < sess.Combat.Count(); i++ {
			p := sess.Combat.Records[i]
			if p.Shooter != shooterH || p.CreationTick != now || p.BurstRemaining == 0 {
				continue
			}
			roots++
			piece := int32(p.MuzzlePiece)
			switch {
			case flares[piece]:
			case shoulders[piece]:
				t.Fatalf("tick %d: root spawned from the AimFrom piece %d (a shoulder); retail fires from the Query piece [06 §4.1]", now, piece)
			default:
				t.Fatalf("tick %d: root spawned from piece %d, want rfire/lfire", now, piece)
			}
			offset, ok := binding.ComposePiece(int(piece), shooter.Move.Heading, shooter.Move.Pitch, shooter.Move.Bank)
			if !ok {
				t.Fatalf("tick %d: piece %d did not compose", now, piece)
			}
			wantPos := combat.Vec3{X: shooter.X.Add(offset[0]), Y: shooter.Y.Add(offset[1]), Z: shooter.Z.Add(offset[2])}
			if p.Pos != wantPos {
				t.Fatalf("tick %d: root at %v, want the flare's world point %v [06 §4.1][03 R-RAST-01 §8]", now, p.Pos, wantPos)
			}
			if p.Pos == (combat.Vec3{X: shooter.X, Y: shooter.Y, Z: shooter.Z}) {
				t.Fatalf("tick %d: root spawned at the unit's own position", now)
			}
			if p.TargetPos != wantAim {
				t.Fatalf("tick %d: aim point %v, want the SweetSpot box centre %v (victim at %v) [06 R-WPN-04 §1]", now, p.TargetPos, wantAim, victim.Y)
			}
		}
	}
	if roots < 4 {
		t.Fatalf("the Peewee fired only %d roots in 260 ticks", roots)
	}
}
