package construction

import (
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/economy"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// The final shared-step quantum owns the stored health. Completion only changes
// construction state; it must not turn a damaged completed product into full
// health [05 R-WORK-01 §1].
func TestAssistCompletionPreservesFinalSharedStepHealth(t *testing.T) {
	for _, bmcode := range []uint8{0, 1} {
		for _, startHealth := range []int32{50, 75} {
			t.Run(string(rune('0'+bmcode))+":"+string(rune('0'+startHealth/25)), func(t *testing.T) {
				builder := &units.Unit{Handle: pool.Handle(1), Alive: true, Def: &content.UnitDef{UnitName: "builder", WorkerTime: 750}}
				product := &units.Unit{
					Handle: pool.Handle(2), Alive: true,
					Def:       &content.UnitDef{UnitName: "product", BMCode: bmcode, BuildTime: 100, MaxDamage: 100},
					Remaining: 0.25, Health: startHealth, MaxHealth: 100,
				}
				svc := NewService(nil, nil, nil, &economy.Service{})
				if !svc.Assist(builder, product, 1) {
					t.Fatal("final assist step was not admitted")
				}
				wantHealth := startHealth + 25
				if product.Remaining != 0 || product.Flags&FlagCompleted == 0 || product.Health != wantHealth {
					t.Fatalf("completion = remaining %v flags %#x health %d, want zero/completed/%d", product.Remaining, product.Flags, product.Health, wantHealth)
				}
				if svc.Assist(builder, product, 2) {
					t.Fatal("already-completed product committed a second assist")
				}
				if product.Health != wantHealth {
					t.Fatalf("idempotent completion health = %d, want %d", product.Health, wantHealth)
				}
			})
		}
	}
}
