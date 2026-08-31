package triggers

import (
	"fmt"
	"strings"

	"github.com/nanolathe/nanolathe/internal/save"
)

// RestoreSaveAccounts applies the typed trigger state emitted by the battle
// save callbacks.  Definition arguments and timer deadlines remain authored
// mission state; only the three established persisted items are accepted
// [08 R-TRIG-01 §8].
func RestoreSaveAccounts(victory, defeat []*Trigger, records []save.RawAccount) error {
	seen := make(map[string]bool)
	apply := func(list []*Trigger, prefix string) error {
		for _, raw := range records {
			if !strings.HasPrefix(raw.Name, prefix) {
				continue
			}
			if seen[raw.Name] {
				return fmt.Errorf("triggers: retail restore: duplicate account %q", raw.Name)
			}
			seen[raw.Name] = true
			name := strings.TrimPrefix(raw.Name, prefix)
			var match *Trigger
			for _, tr := range list {
				if tr != nil && tr.Kind.String() == name {
					if match != nil {
						return fmt.Errorf("triggers: retail restore: ambiguous condition %q", raw.Name)
					}
					match = tr
				}
			}
			if match == nil {
				return fmt.Errorf("triggers: retail restore: account %q has no matching condition", raw.Name)
			}
			items := make(map[string]bool)
			for _, item := range raw.Ints {
				if item.Name != "Satisfied" && item.Name != "Celebrated" && item.Name != "NumLeftToKill" {
					continue
				}
				if item.Name == "NumLeftToKill" && match.Kind != KindKillUnitType && match.Kind != KindUnitTypeKilled {
					continue
				}
				if items[item.Name] {
					return fmt.Errorf("triggers: retail restore: duplicate item %q in %q", item.Name, raw.Name)
				}
				items[item.Name] = true
				switch item.Name {
				case "Satisfied":
					match.Completed = item.Value != 0
				case "Celebrated":
					match.Celebrated = item.Value != 0
				case "NumLeftToKill":
					match.Args[0] = item.Value
				}
			}
		}
		return nil
	}
	if err := apply(victory, "VictoryCondition_"); err != nil {
		return err
	}
	return apply(defeat, "DefeatCondition_")
}
