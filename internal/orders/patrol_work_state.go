package orders

// These read-only projections publish the Modern patrol receipts for parity
// traces. Canonical capture follows the same links as object references;
// neither projection serializes a host pointer. DESIGN_UNITS_ORDERS_COB
// "Modern patrol work"; retail-format saved orders have no producer receipt.
func (q *Queue) WorkReturnOrdinal(n *Node) int {
	if n == nil || n.workReturn == nil {
		return 0
	}
	if q == nil {
		return -1
	}
	if index := q.indexOfPrimary(n.workReturn); index >= 0 {
		return index + 1
	}
	return -1
}

func (n *Node) IsPatrolReturn() bool      { return n != nil && n.patrolReturn }
func (n *Node) PatrolReturnArrived() bool { return n != nil && n.patrolReturnArrived }
func (q *Queue) PatrolWorkPaused() bool   { return q != nil && q.patrolWorkPaused }
