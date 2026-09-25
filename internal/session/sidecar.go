package session

import (
	"github.com/nanolathe-gg/nanolathe/internal/community"
	"github.com/nanolathe-gg/nanolathe/internal/save"
	"github.com/nanolathe-gg/nanolathe/internal/version"
)

// SaveSidecar is the part of a save's sidecar the session owns: the bound
// rule set and its base, the Community sources and battle-entry table, the
// mutators and the catalog identity after them
// (docs/DESIGN_MODS_MUTATORS.md §7.2). The host fills the mod, the content
// profile and the configured unit limit, which are its selections, not the
// session's.
func SaveSidecar(s *Session) save.Sidecar {
	out := save.Sidecar{Profile: version.ProfileID(), Mutators: map[string]string{}}
	if s == nil {
		return out
	}
	out.Rules = s.Rules.Name
	if out.Rules == "" {
		out.Rules = string(s.Gameplay.Normalize())
	}
	out.Gameplay = string(s.Gameplay.Normalize())
	out.Community = save.SidecarCommunity{
		Sources: save.SidecarCommunitySources{
			Content:     append([]community.Overrides(nil), s.CommunitySources.Content...),
			Player:      s.CommunitySources.Player,
			CommandLine: append([]community.Overrides(nil), s.CommunitySources.CommandLine...),
		},
		Entry: s.EntryCommunity,
	}
	out.Mutators = s.Mutators.Map()
	if s.Catalog != nil {
		out.Catalog = s.Catalog.Hash
		out.ContentManifest = s.Catalog.Manifest
	}
	return out
}

// SidecarCommunitySources converts a sidecar's recorded sources back into
// the session's composition inputs.
func SidecarCommunitySources(c save.SidecarCommunity) CommunitySources {
	return CommunitySources{
		Content:     append([]community.Overrides(nil), c.Sources.Content...),
		Player:      c.Sources.Player,
		CommandLine: append([]community.Overrides(nil), c.Sources.CommandLine...),
	}
}
