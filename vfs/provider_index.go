package vfs

import "strings"

// providerIndex is the logical-path table every provider keeps, together with
// the two accessors the overlay resolves through. The archive and loose
// providers build their tables from different sources but answer a lookup and
// a one-level enumeration identically, so the table lives here once.
//
// children returns the matches in map order; the overlay is what imposes an
// order on what it publishes, and that is unchanged by this sharing.
type providerIndex struct {
	entries map[string]*providerEntry
}

func newProviderIndex() providerIndex {
	return providerIndex{entries: make(map[string]*providerEntry)}
}

func (x *providerIndex) lookup(name string) (*providerEntry, bool) {
	entry, ok := x.entries[name]
	return entry, ok
}

func (x *providerIndex) children(parent string) []*providerEntry {
	result := make([]*providerEntry, 0)
	prefix := parent
	if prefix != "" {
		prefix += "/"
	}
	for name, entry := range x.entries {
		if name == "" || !strings.HasPrefix(name, prefix) {
			continue
		}
		rest := strings.TrimPrefix(name, prefix)
		if rest != "" && !strings.Contains(rest, "/") {
			result = append(result, entry)
		}
	}
	return result
}
