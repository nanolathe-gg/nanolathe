package main

import (
	"github.com/nanolathe-gg/nanolathe/internal/client"
	"github.com/nanolathe-gg/nanolathe/internal/content"
	"github.com/nanolathe-gg/nanolathe/internal/session"
)

// The two helpers below are test conveniences over production composition
// paths. Production hosts hold the richer values these discard — the fresh
// battle record and the load-time remaster's art — so neither belongs in
// production source.

// newBattleSession builds the integrated skirmish session for a direct map
// entry and keeps only the session and its catalog. It uses the canonical
// DirectSkirmishConfig normalization [08 "Skirmish configuration"] [GAP T14].
// newDirectBattleView runs the same two steps and retains the whole fresh
// battle record.
func newBattleSession(opts Options, cs *contentSet) (*session.Session, *content.Catalog, error) {
	request, err := directMapBattleRequest(opts, cs, newBattleSeedSource(opts))
	if err != nil {
		return nil, nil, err
	}
	authoritative, err := composeAuthoritativeBattle(request)
	if err != nil {
		return nil, nil, err
	}
	return authoritative.Session, authoritative.Session.Catalog, nil
}

// composeBattleEntry is composeBattleEntryWithDetail with no load-time
// remaster art, which is what a fixture that never loaded a map surface has.
func composeBattleEntry(sess *session.Session, cat *content.Catalog, cs *contentSet, cl *client.Client, shell *gameShell) (*battleSession, error) {
	return composeBattleEntryWithDetail(sess, cat, cs, cl, shell, nil)
}
