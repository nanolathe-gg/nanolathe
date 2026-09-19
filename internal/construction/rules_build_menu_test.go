package construction

import (
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"slices"
	"testing"
)

// Modern admission retains authored overflow and duplicates; Strict sees the
// unchanged retail list. This is Nanolathe policy, not a claimed patch limit.
func TestBuildProductsSelectsAuthoredMembershipWithoutMutation(t *testing.T) {
	menu := &content.BuildMenuPage{Buttons: []string{"base", "ai"}, AuthoredButtons: []string{"base", "ai", "ai", "factory"}}
	for _, tt := range []struct {
		rules Rules
		want  []string
	}{
		{nil, menu.Buttons}, {StrictRules{}, menu.Buttons}, {&ModernRules{}, menu.AuthoredButtons},
	} {
		if got := BuildProducts(tt.rules, menu); !slices.Equal(got, tt.want) {
			t.Fatalf("products %v want %v", got, tt.want)
		}
		if n := testing.AllocsPerRun(50, func() { _ = BuildProducts(tt.rules, menu) }); n != 0 {
			t.Fatalf("dispatch allocates %v", n)
		}
	}
	if len(menu.Buttons) != 2 || len(menu.AuthoredButtons) != 4 {
		t.Fatal("selection mutated catalog")
	}
}
