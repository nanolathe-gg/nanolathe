package utility

// The defense plan keeps state between thinks (the budget it has accrued,
// where buildings were lost, which way attacks came from) on the Economy.
func (e *Economy) plan() *defPlan { return &e.def }
