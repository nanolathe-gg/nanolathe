package economy

import (
	"math"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Carry gates admit unordered values; requested stores precede the verdict
// and denied two-resource work credits neither accepted bucket [05 R-ECO-01 §1, §7].
func TestCarryAdmissionExceptionalBranches(t *testing.T) {
	carries := []struct {
		name   string
		value  float32
		admits bool
	}{
		{"positive", 1, false}, {"negative", -1, true}, {"zero", 0, true},
		{"negative-zero", float32(math.Copysign(0, -1)), true}, {"nan", float32(math.NaN()), true},
	}
	for _, e := range carries {
		t.Run(e.name, func(t *testing.T) {
			b := [2]Bucket{Energy: {Carry: e.value}, Metal: {Requested: 9, Accepted: 8, Carry: 7}}
			if got := AdmitOneResource(&b, 3); got != e.admits {
				t.Fatalf("verdict=%t", got)
			}
			want := float32(0)
			if e.admits {
				want = 3
			}
			if b[Energy].Requested != 3 || b[Energy].Accepted != want || b[Metal] != (Bucket{Requested: 9, Accepted: 8, Carry: 7}) {
				t.Fatalf("buckets=%+v", b)
			}
			for _, m := range carries {
				t.Run(m.name, func(t *testing.T) {
					b := [2]Bucket{Energy: {Carry: e.value}, Metal: {Carry: m.value}}
					admitted := e.admits && m.admits
					if got := AdmitTwoResource(&b, 3, 5); got != admitted {
						t.Fatalf("verdict=%t", got)
					}
					we, wm := float32(0), float32(0)
					if admitted {
						we, wm = 3, 5
					}
					if b[Energy].Requested != 3 || b[Metal].Requested != 5 || b[Energy].Accepted != we || b[Metal].Accepted != wm {
						t.Fatalf("buckets=%+v", b)
					}
				})
			}
		})
	}
	if AdmitOneResource(nil, 3) || AdmitTwoResource(nil, 3, 5) {
		t.Fatal("nil admitted")
	}
}

func TestImmediateDebitRejectsUnorderedOperands(t *testing.T) {
	nan := float32(math.NaN())
	for _, tc := range []struct {
		name         string
		es, ms, e, m float32
	}{
		{"energy-stock", nan, 10, 1, 1}, {"metal-stock", 10, nan, 1, 1},
		{"energy-payment", 10, 10, nan, 1}, {"metal-payment", 10, 10, 1, nan},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := Player{Stock: [2]float32{Energy: tc.es, Metal: tc.ms}}
			b := [2]Bucket{{Requested: 3, Accepted: 4}, {Requested: 5, Accepted: 6}}
			before := b
			if ImmediateDebit(&p, &b, tc.e, tc.m) {
				t.Fatal("unordered payment admitted")
			}
			if b != before || math.Float32bits(p.Stock[Energy]) != math.Float32bits(tc.es) || math.Float32bits(p.Stock[Metal]) != math.Float32bits(tc.ms) {
				t.Fatal("refused payment changed state")
			}
		})
	}
	p := Player{Stock: [2]float32{Energy: 3, Metal: 5}}
	if !ImmediateDebit(&p, nil, 3, 5) || p.Stock != ([2]float32{}) || p.Mirror[Energy].Requested != 3 || p.Mirror[Metal].Requested != 5 {
		t.Fatal("exact stock payment failed")
	}
}

func TestNegativePoolSettlementAdmitsNextRequest(t *testing.T) {
	var s Service
	s.Players[0].Mirror[Energy].Production = -1
	s.Settle(0, 0, nil)
	b := &s.Players[0].Mirror
	// Zero old debt times the negative-infinite debt ratio produces unordered
	// carry in the live apply-back [05 R-ECO-01 §5]. Do not sanitize it.
	if !math.IsNaN(float64(b[Energy].Carry)) {
		t.Fatalf("carry=%v", b[Energy].Carry)
	}
	if !AdmitOneResource(b, 2) || b[Energy].Requested != 2 || b[Energy].Accepted != 2 {
		t.Fatalf("next admission=%+v", b[Energy])
	}
}

func TestUpkeepUsesAdmissionVerdictForUnorderedCarry(t *testing.T) {
	w := units.NewSliced(10, &content.Catalog{})
	var s Service
	s.Players[0].Exists = true
	def := economyFixtureDef(&content.UnitDef{UnitName: "nan-carry", ExtractsMetal: 1, EnergyUse: 2, BuildTime: 1, MaxDamage: 1})
	h, err := w.Create(def, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	u := w.Unit(h)
	u.Activated = true
	u.SpotMetal = 9
	b := s.UnitBuckets(h)
	b[Energy].Carry = float32(math.NaN())
	s.PerUnitProductionFills(0, w)
	if b[Energy].Requested != 2 || b[Energy].Accepted != 2 || b[Metal].Production != 9 {
		t.Fatalf("buckets=%+v", b)
	}
}

func TestUnorderedEnergyUseTakesRefundArm(t *testing.T) {
	for _, controller := range []uint8{1, 2} {
		if got := refundProduction(t, math.NaN(), controller, 0, 3); !math.IsNaN(float64(got)) {
			t.Fatalf("controller=%d production=%v", controller, got)
		}
	}
}
