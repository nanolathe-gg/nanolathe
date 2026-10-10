//go:build retail

package session

import (
	"fmt"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/ai"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/testsupport/retailcat"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// Non-human wire rows are unused padding, not live preferences. Humans keep
// their explicit choices; other owners keep their selected rules' defaults
// (DESIGN_MULTIPLAYER §8.6; DESIGN_UNITS_ORDERS_COB "Modern patrol work").
func TestOnlineBuilderPreferencesAndRuleDefaultsRetail(t *testing.T) {
	cat, fs := retailcat.Shared(t)
	for _, survival := range []bool{false, true} {
		t.Run(fmt.Sprintf("survival%v", survival), func(t *testing.T) {
			setup := onlineTestSetup(survival, 0, 0)
			setup.MapName = admittedSkirmishMap
			setup.Computers = []OnlineComputer{{Color: 2, Kind: ai.ControllerClassic, Difficulty: 1}}
			room := matchTestRoom()
			room.MapSchema = admittedRoomSchema(t, fs, cat, setup.MapName, setup.Rows())
			request, err := NewOnlineMatchRequest(setup, SkirmishEntryOptions{}, room)
			if err != nil {
				t.Fatal(err)
			}
			for i := range request.Seats {
				if request.Seats[i].Role == MatchRoleHuman {
					request.Seats[i].BuilderOptions.Patrol = [3]orders.PatrolWorkOption{orders.PatrolWorkOption((i + 2) % 3), orders.PatrolWorkOption(i % 3), orders.PatrolWorkOption((i + 1) % 3)}
				} else if request.Seats[i].BuilderOptions != orders.DefaultBuilderOptions() {
					t.Fatal("non-human configuration lost its canonical padding")
				}
			}
			c, err := ResolveMatchConfig(request)
			if err != nil {
				t.Fatal(err)
			}
			inputs, err := FreezeMatchInputs(fs, cat, c, nil)
			if err != nil {
				t.Fatal(err)
			}
			s, err := NewPlaytestSkirmish(inputs, c, 0, nil)
			if err != nil {
				t.Fatal(err)
			}
			for i, seat := range request.Seats {
				want := s.Rules.Orders.DefaultBuilderOptions()
				if seat.Role == MatchRoleHuman {
					want = seat.BuilderOptions
				}
				if got := s.builderOptionsForOwner(uint8(i)); got != want {
					t.Errorf("owner %d role %d options = %+v, want %+v", i, seat.Role, got, want)
				}
				for _, name := range []string{"armcv", "armca"} {
					def, ok := cat.Unit(name)
					if !ok {
						t.Fatalf("retail constructor %s absent", name)
					}
					u := &units.Unit{Owner: uint8(i), Def: def}
					s.bindOrderQueue(u)
					for stance := uint32(0); stance < 3; stance++ {
						u.Flags = u.Flags&^(units.StandingFieldMask<<units.StandingMoveShift) | stance<<units.StandingMoveShift
						got := s.Rules.Orders.PatrolWork(orders.PatrolWorkRequest{Builder: u})
						if got != want.Patrol[stance] {
							t.Errorf("owner %d %s stance %d option = %d, want %d", i, name, stance, got, want.Patrol[stance])
						}
					}
				}
			}
		})
	}
}
