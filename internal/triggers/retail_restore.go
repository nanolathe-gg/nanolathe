package triggers

import (
	"fmt"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/save"
)

// RestoreSaveAccounts applies the typed trigger state emitted by the battle
// save callbacks.  Definition arguments and timer deadlines remain authored
// mission state; only the four established persisted items are accepted
// [08 R-TRIG-01 §8].
//
// NumUnits — KillAllMobileUnits' scratch count of surviving mobile enemy
// units — is accepted and discarded. Retail restores it with a default of 0
// into a field its notification zeroes and recounts before ever reading, so
// the value carries nothing; this port keeps no scratch field for it at all
// [08 R-TRIG-01 §8]. It is read here so that a retail-written account is not
// rejected and so that the item is still duplicate-checked.
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
				switch item.Name {
				case "Satisfied", "Celebrated", "NumLeftToKill", "NumUnits":
				default:
					continue
				}
				if item.Name == "NumLeftToKill" && match.Kind != KindKillUnitType && match.Kind != KindUnitTypeKilled {
					continue
				}
				if item.Name == "NumUnits" && match.Kind != KindKillAllMobileUnits {
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
				case "NumUnits":
					// Accepted and dropped: see the doc comment above.
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
