package core

import (
	"fmt"

	"github.com/nanolathe-gg/nanolathe/internal/aikit"
)

// Policy is one replaceable decision layer. Init runs once with the kit's
// static knowledge; Plan runs every think after the board is updated and
// emits commands through b.K.
type Policy interface {
	Init(b *Board)
	Plan(b *Board)
}

// Explaining is an optional Policy interface for visualization.
type Explaining interface {
	Explain(b *Board, x *aikit.Explain)
}

// Brain composes four policies over one board. Order matters under an
// action budget: strategy, economy, army, then production.
type Brain struct {
	Label    string
	Board    Board
	Strategy Policy
	Economy  Policy
	Army     Policy
	Prod     Policy
}

// New composes a brain. A nil policy is skipped.
func New(label string, strategy, economy, army, prod Policy) *Brain {
	return &Brain{Label: label, Strategy: strategy, Economy: economy, Army: army, Prod: prod}
}

// Name implements aikit.Brain.
func (br *Brain) Name() string { return br.Label }

func (br *Brain) policies() [4]Policy {
	return [4]Policy{br.Strategy, br.Economy, br.Army, br.Prod}
}

// Init implements aikit.Brain.
func (br *Brain) Init(k *aikit.Kit) {
	br.Board.K = k
	for _, p := range br.policies() {
		if p != nil {
			p.Init(&br.Board)
		}
	}
}

// Think implements aikit.Brain.
func (br *Brain) Think(k *aikit.Kit, o *aikit.Obs) {
	br.Board.Update(k, o)
	for _, p := range br.policies() {
		if p != nil {
			p.Plan(&br.Board)
		}
	}
}

// Explain implements aikit.Explainer.
func (br *Brain) Explain(x *aikit.Explain) {
	b := &br.Board
	if b.O == nil {
		return
	}
	x.Mode = b.Posture.Label
	x.Notes = append(x.Notes,
		fmt.Sprintf("income M %d/s E %d/s, stock M %d/%d E %d/%d", b.Metal.Income, b.Energy.Income, b.Metal.Stock, b.Metal.Cap, b.Energy.Stock, b.Energy.Cap),
		fmt.Sprintf("army value %d (%d units), enemy army seen %d", b.ArmyValue, len(b.Combat), b.EnemyArmyValue),
		fmt.Sprintf("posture eco %d%% aggression %d attack-at %d", b.Posture.EcoShare, b.Posture.Aggression, b.Posture.AttackValue))
	for _, p := range br.policies() {
		if e, ok := p.(Explaining); ok {
			e.Explain(b, x)
		}
	}
	x.Grids = append(x.Grids, b.Threat.Snapshot("threat"), b.EnemyValue.Snapshot("enemy_value"), b.OwnPower.Snapshot("own_power"))
}
