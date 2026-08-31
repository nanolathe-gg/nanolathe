package triggers

import "fmt"

// RetailTriggerInt is one typed integer item in a trigger account. Trigger
// save callbacks emit integer items only; the explicit item type keeps this
// package independent of the bank writer [08 R-TRIG-01 §8].
type RetailTriggerInt struct {
	Name  string
	Value int32
}

// RetailTriggerBox is reserved for a detached trigger account's binary boxes.
// Retail trigger callbacks currently emit no boxes, but retaining the typed
// shape lets a caller reject an accidental non-retail extension explicitly.
type RetailTriggerBox struct {
	Name   string
	Number int32
	Data   []byte
}

// RetailTriggerAccount is one detached account in callback order. Duplicate
// names are retained because retail calls save on every live record and the
// bank's same-name account merge/overwrite is a later writer concern [08
// R-TRIG-01 §8].
type RetailTriggerAccount struct {
	Name  string
	Ints  []RetailTriggerInt
	Boxes []RetailTriggerBox
}

// RetailTriggerImage returns trigger save accounts in the retail traversal:
// victory records first, then defeat records; within each queue the supplied
// live record order is preserved. Each record emits Satisfied, Celebrated,
// and, for the two counted death families, NumLeftToKill [08 R-TRIG-01 §8].
func RetailTriggerImage(victory, defeat []*Trigger) ([]RetailTriggerAccount, error) {
	accounts := make([]RetailTriggerAccount, 0, len(victory)+len(defeat))
	appendQueue := func(list []*Trigger, prefix string) error {
		for i, trigger := range list {
			if trigger == nil {
				continue
			}
			if trigger.Kind >= KindCount {
				return fmt.Errorf("triggers: retail save: %s trigger %d has unknown kind %d", prefix, i, trigger.Kind)
			}
			account := RetailTriggerAccount{Name: prefix + trigger.Kind.String()}
			var satisfied, celebrated int32
			if trigger.Completed {
				satisfied = 1
			}
			if trigger.Celebrated {
				celebrated = 1
			}
			account.Ints = append(account.Ints,
				RetailTriggerInt{Name: "Satisfied", Value: satisfied},
				RetailTriggerInt{Name: "Celebrated", Value: celebrated})
			if trigger.Kind == KindKillUnitType || trigger.Kind == KindUnitTypeKilled {
				account.Ints = append(account.Ints, RetailTriggerInt{Name: "NumLeftToKill", Value: trigger.Args[0]})
			}
			accounts = append(accounts, account)
		}
		return nil
	}
	if err := appendQueue(victory, "VictoryCondition_"); err != nil {
		return nil, err
	}
	if err := appendQueue(defeat, "DefeatCondition_"); err != nil {
		return nil, err
	}
	return accounts, nil
}

// RetailSaveAccounts is an alias for callers naming the detached projection
// by its eventual bank representation.
func RetailSaveAccounts(victory, defeat []*Trigger) ([]RetailTriggerAccount, error) {
	return RetailTriggerImage(victory, defeat)
}

// RestoreRetailTriggerAccounts applies the values emitted by
// RetailTriggerImage. Definition arguments and timer deadlines remain
// authored mission state [08 R-TRIG-01 §8].
func RestoreRetailTriggerAccounts(victory, defeat []*Trigger, accounts []RetailTriggerAccount) error {
	seen := make(map[string]bool, len(accounts))
	apply := func(list []*Trigger, prefix string) error {
		for _, account := range accounts {
			if len(account.Name) <= len(prefix) || account.Name[:len(prefix)] != prefix {
				continue
			}
			if seen[account.Name] {
				return fmt.Errorf("triggers: retail restore: duplicate account %q", account.Name)
			}
			seen[account.Name] = true
			name := account.Name[len(prefix):]
			var match *Trigger
			for _, trigger := range list {
				if trigger != nil && trigger.Kind.String() == name {
					if match != nil {
						return fmt.Errorf("triggers: retail restore: ambiguous condition %q", account.Name)
					}
					match = trigger
				}
			}
			if match == nil {
				return fmt.Errorf("triggers: retail restore: account %q has no matching condition", account.Name)
			}
			if len(account.Boxes) != 0 {
				return fmt.Errorf("triggers: retail restore: account %q has unsupported boxes", account.Name)
			}
			seenItems := make(map[string]bool, len(account.Ints))
			for _, item := range account.Ints {
				if item.Name != "Satisfied" && item.Name != "Celebrated" && item.Name != "NumLeftToKill" {
					continue
				}
				if item.Name == "NumLeftToKill" && match.Kind != KindKillUnitType && match.Kind != KindUnitTypeKilled {
					continue
				}
				if seenItems[item.Name] {
					return fmt.Errorf("triggers: retail restore: duplicate item %q in %q", item.Name, account.Name)
				}
				seenItems[item.Name] = true
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
