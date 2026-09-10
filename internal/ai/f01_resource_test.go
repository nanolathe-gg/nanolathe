package ai

import (
	"fmt"
	"slices"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/cob"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/sim/rng"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Established: the stock comparison includes equality, positive fractional
// net energy admits a draw, and failed enabling attempts preserve the previous
// activation state and raise no callbacks [08 R-AI-01 §2].
func TestResourceMetalMakerActivationAndDrawAdmission(t *testing.T) {
	var seeds [2]uint32
	for seed := uint32(1); seed < 10000 && (seeds[0] == 0 || seeds[1] == 0); seed++ {
		probe := rng.NewSimulation(seed)
		index := 0
		if probe.Uint32n(5) != 0 {
			index = 1
		}
		if seeds[index] == 0 {
			seeds[index] = seed
		}
	}
	if seeds[0] == 0 || seeds[1] == 0 {
		t.Fatal("could not find zero and nonzero bounded-draw fixtures")
	}
	for _, stock := range []struct {
		name    string
		energy  float32
		disable bool
	}{
		{"below", 199.5, true},
		{"equal", 200, true},
		{"above", 200.5, false},
	} {
		for _, net := range []struct {
			name       string
			production float32
			admitsDraw bool
		}{
			{"negative", 9.5, false},
			{"zero", 10, false},
			{"fractional", 10.5, true},
			{"positive", 20, true},
		} {
			for _, active := range []bool{false, true} {
				for outcome, seed := range seeds {
					t.Run(fmt.Sprintf("%s/%s/active=%t/drawNonzero=%t", stock.name, net.name, active, outcome != 0), func(t *testing.T) {
						// Authored callbacks return immediately [fmt cob]; their
						// starts expose the ordinary activation service's edges [04 §5].
						def := &content.UnitDef{
							UnitName: "maker", MaxDamage: 100, MakesMetal: 1, OnOffable: true,
							Script: &cob.Program{
								Code:        []uint32{0x10021001, 0, 0x10065000},
								Scripts:     map[string]int{"Create": 0, "Activate": 0, "Deactivate": 0},
								ScriptsByID: []int{0, 0, 0},
								Pieces:      []string{"base"},
							},
						}
						w := newAIFixtureWorld(1, nil)
						w.SetCOBBinder(func(u *units.Unit) error {
							vm := cob.NewVM(def.Script)
							bridge := cob.NewCallbackBridge(vm)
							if err := u.AttachCOBBindingPreCreate(&cob.Binding{Program: def.Script, VM: vm, Callbacks: bridge}); err != nil {
								return err
							}
							bridge.Create()
							return nil
						})
						h, err := w.Create(def, 0, 0, 0, 0)
						if err != nil {
							t.Fatal(err)
						}
						u := w.Unit(h)
						u.SetActivated(active)
						var callbacks []string
						u.COBBinding().Callbacks.SetLifecycleSink(func(event cob.LifecycleEvent) {
							callbacks = append(callbacks, event.Name+":"+event.Phase)
						})
						var cues []uint8
						u.SetStatusCueSink(func(_ *units.Unit, code uint8) { cues = append(cues, code) })
						econ := runtimeEconomy(0, 2)
						econ.Players[0].Stock[economy.Metal] = 100
						econ.Players[0].Stock[economy.Energy] = stock.energy
						econ.Players[0].AIProduction[economy.Energy] = net.production
						econ.Players[0].AIConsumption[economy.Energy] = 10
						sim := rng.NewSimulation(seed)
						m := &Manager{Player: 0, RNG: &sim, GroupResource: []pool.Handle{h}}
						m.doResource(30, w, econ)

						wantActive := active
						wantRNG := rng.NewSimulation(seed)
						if stock.disable {
							wantActive = false
						} else if net.admitsDraw {
							wantRNG.Uint32n(5)
							if outcome != 0 {
								wantActive = true
							}
						}
						if u.Activated != wantActive {
							t.Fatalf("active=%t, want %t", u.Activated, wantActive)
						}
						if sim.Draws() != wantRNG.Draws() || sim.State != wantRNG.State {
							t.Fatalf("RNG draws/state=%d/%d, want %d/%d", sim.Draws(), sim.State, wantRNG.Draws(), wantRNG.State)
						}
						var wantCallbacks []string
						var wantCues []uint8
						if active != wantActive {
							if wantActive {
								wantCallbacks = []string{"Activate:start"}
								wantCues = []uint8{units.StatusCueActivate}
							} else {
								wantCallbacks = []string{"Deactivate:start"}
								wantCues = []uint8{units.StatusCueDeactivate}
							}
						}
						if !slices.Equal(callbacks, wantCallbacks) || !slices.Equal(cues, wantCues) {
							t.Fatalf("callbacks/cues=%v/%v, want %v/%v", callbacks, cues, wantCallbacks, wantCues)
						}
					})
				}
			}
		}
	}
}
