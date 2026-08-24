package vfs

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
)

// ManifestRecord is one logical path's identity: who won, who was shadowed,
// and the hash of the bytes the engine will actually read. Provenance exists
// so a differing winner can be named rather than silently used — the
// deliberate intra-tier ordering divergence in docs/PLAN_01 D1 is only safe
// because of this record.
type ManifestRecord struct {
	LogicalPath string
	ProviderID  string
	Priority    int
	MountOrder  int
	Size        int64
	Hash        string   // sha256 of the winning bytes, hex; empty if unread
	Shadowed    []string // provider IDs that also held this path, losing order
}

// ManifestOptions bounds the work Manifest does. Hashing every file in a full
// install reads roughly a gigabyte, so it is opt-in.
type ManifestOptions struct {
	Hash        bool  // read and hash winning bytes
	MaxHashSize int64 // skip hashing entries larger than this (0 = no limit)
}

// Manifest returns one record per logical path, sorted by path. Unlike
// Entries, which yields one record per mount, this is the deduplicated view
// callers should use for identity (docs/SPEC_CONFLICTS.md SC4).
func (f *FS) Manifest(opts ManifestOptions) ([]ManifestRecord, error) {
	entries := f.Entries()
	seen := make(map[string]bool, len(entries))
	records := make([]ManifestRecord, 0, len(entries))

	for _, entry := range entries {
		if entry.IsDir || seen[entry.Path] {
			continue
		}
		seen[entry.Path] = true

		sources := f.Sources(entry.Path)
		if len(sources) == 0 {
			continue
		}
		winner := sources[0]
		record := ManifestRecord{
			LogicalPath: winner.Path,
			ProviderID:  providerID(winner),
			Priority:    winner.Source.Priority,
			MountOrder:  winner.Source.MountOrder,
			Size:        winner.Size,
		}
		for _, shadow := range sources[1:] {
			record.Shadowed = append(record.Shadowed, providerID(shadow))
		}
		if opts.Hash && (opts.MaxHashSize == 0 || winner.Size <= opts.MaxHashSize) {
			data, err := f.ReadFile(winner.Path)
			if err != nil {
				return nil, fmt.Errorf("vfs: manifest read %s: %w", winner.Path, err)
			}
			sum := sha256.Sum256(data)
			record.Hash = hex.EncodeToString(sum[:])
		}
		records = append(records, record)
	}

	sort.Slice(records, func(i, j int) bool { return records[i].LogicalPath < records[j].LogicalPath })
	return records, nil
}

// ManifestHash is a stable identity for the mounted content set. It covers
// logical paths, winning providers and sizes, so it is cheap; pass
// ManifestOptions{Hash: true} to Manifest when byte identity is needed.
func (f *FS) ManifestHash() (string, error) {
	records, err := f.Manifest(ManifestOptions{})
	if err != nil {
		return "", err
	}
	digest := sha256.New()
	for _, record := range records {
		fmt.Fprintf(digest, "%s\x00%s\x00%d\x00%d\n",
			record.LogicalPath, record.ProviderID, record.Priority, record.Size)
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

// providerID returns the portable identity of an entry's winning provider.
// Archives contribute their file name (extension visible, PLAN_01 C4); loose
// files contribute their path relative to the mount root. Absolute host
// paths never reach the manifest, so ManifestHash is stable across runs and
// across filesystems given the same content [PLAN_01 C13].
func providerID(info EntryInfo) string { return info.Source.ProviderID() }
