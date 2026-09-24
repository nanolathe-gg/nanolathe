package survival

// Account is one living survivor's state for one resource at an income split.
type Account struct {
	Stock, Capacity float32
	// Earned is the production of a settlement pass that has not been split
	// yet; zero when the survivor has not settled since the last split.
	Earned float32
}

// SplitIncome divides each account's Earned evenly among all accounts
// (DESIGN_SURVIVAL §4.3), in slice order: the earner keeps 1/n and every
// other account is credited 1/n, moved from the earner's stock. A credit that
// does not fit in a teammate's storage goes back to the earner, so the total
// stock is conserved exactly. The earner can give only what it holds.
func SplitIncome(accts []Account) {
	n := float32(len(accts))
	if len(accts) < 2 {
		return
	}
	for i := range accts {
		earner := &accts[i]
		give := earner.Earned * (n - 1) / n
		earner.Earned = 0
		if give > earner.Stock {
			give = earner.Stock
		}
		if give <= 0 {
			continue
		}
		earner.Stock -= give
		each := give / (n - 1)
		back := give
		for j := range accts {
			if j == i {
				continue
			}
			mate := &accts[j]
			credit := min(each, mate.Capacity-mate.Stock, back)
			if credit > 0 {
				mate.Stock += credit
				back -= credit
			}
		}
		earner.Stock += back
	}
}
