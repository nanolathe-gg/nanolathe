package session

import (
	"fmt"

	"github.com/nanolathe-gg/nanolathe/internal/orders"
)

// HumanBuilderOptionsCommand changes the local player's six preferences at
// phase 1. They are player state, not unit stances or part of a retail save
// (DESIGN_COMMUNITY_PATCH §4.3, D5).
type HumanBuilderOptionsCommand struct {
	Owner   uint8
	Options orders.BuilderOptions
}

func validBuilderOptions(options orders.BuilderOptions) bool {
	for i := range options.Guard {
		if options.Guard[i] > orders.GuardScatter || options.Patrol[i] > orders.PatrolAssistOnly {
			return false
		}
	}
	return true
}

func (s *Session) localBuilderOptionsOwner(owner uint8) bool {
	return s != nil && owner < 10 && owner == s.LocalOwner && s.Econ != nil &&
		s.Econ.Players[owner].Exists && s.Econ.Players[owner].ControllerState == 1 && !s.Econ.Players[owner].IsObserver
}

func (s *Session) validateBuilderOptions(c HumanBuilderOptionsCommand) error {
	if !s.localBuilderOptionsOwner(c.Owner) || !validBuilderOptions(c.Options) {
		return fmt.Errorf("nanolathe: builder options rejected: logical path player %d, providers searched [session], expected a local human player and option values 0..2", c.Owner)
	}
	return nil
}

func (s *Session) builderOptionsForOwner(owner uint8) orders.BuilderOptions {
	if s == nil || !s.builderOptionsReady || owner >= 10 {
		return orders.DefaultBuilderOptions()
	}
	return s.playerBuilderOptions[owner]
}

// initializeBuilderOptions is called after service composition and before any
// unit is created. Every entry, including a load, starts from the current host
// preference; the retail bank carries no builder-option state.
func (s *Session) initializeBuilderOptions(human *orders.BuilderOptions) error {
	for i := range s.playerBuilderOptions {
		s.playerBuilderOptions[i] = orders.DefaultBuilderOptions()
	}
	s.builderOptionsReady = true
	if human != nil {
		if !validBuilderOptions(*human) {
			return fmt.Errorf("nanolathe: builder options rejected: logical path battle entry, providers searched [host], expected option values 0..2")
		}
		if s.localBuilderOptionsOwner(s.LocalOwner) {
			s.playerBuilderOptions[s.LocalOwner] = *human
		}
	}
	if s.Build != nil && s.Build.OrderBinding != nil && s.Build.OrderBinding.BuilderOptions == nil {
		s.Build.OrderBinding.BuilderOptions = s.builderOptionsForOwner
	}
	return nil
}
