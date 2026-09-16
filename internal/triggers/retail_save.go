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
