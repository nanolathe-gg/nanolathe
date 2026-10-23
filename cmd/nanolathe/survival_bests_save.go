package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"

	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/settings"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// Best-score eligibility belongs to the host, alongside the director's save
// record, never in the retail bank or authoritative session (DESIGN_SURVIVAL
// §8.1, §11). Keep the original comparison identity and sticky exclusions;
// treating a resumed battle as a fresh attempt would erase accepted cheats.
type survivalBestRecord struct {
	Version  int    `json:"version"`
	Key      string `json:"key,omitempty"`
	Content  string `json:"content,omitempty"`
	Excluded string `json:"excluded,omitempty"`
}

const survivalBestRecordVersion = 1

func (b *battleSession) recordSurvivalBest() json.RawMessage {
	if b == nil || b.sess == nil || !b.sess.IsSurvival() {
		return nil
	}
	rec := survivalBestRecord{Version: survivalBestRecordVersion, Excluded: "personal-best eligibility unavailable"}
	if run := b.survivalBest; run != nil {
		rec.Key, rec.Excluded = run.key, run.excluded
		rec.Content = hex.EncodeToString(run.content[:])
		if rec.Key == "" && rec.Excluded == "" {
			rec.Excluded = "personal-best eligibility unavailable"
		}
	}
	// This record contains only strings and an integer; marshaling cannot fail.
	data, _ := json.Marshal(rec)
	return data
}

// restoreSurvivalBest reads a loaded Survival save's personal-best
// eligibility for the restored battle s (docs/DESIGN_SURVIVAL.md §8.1).
// smartMakersChanged reports that the load runs under another Smart makers
// selection than the save recorded (docs/DESIGN_MODS_MUTATORS.md D26): the
// resumed run then plays under another mutator set than its key names, so it
// leaves that key, with that reason rather than a content change.
func restoreSurvivalBest(raw json.RawMessage, s *session.Session, fs vfs.FSOps, smartMakersChanged bool) (*survivalBestRun, error) {
	if s == nil || !s.IsSurvival() {
		if len(raw) != 0 {
			return nil, survivalBestSaveError("a personal-best record outside Survival")
		}
		return nil, nil
	}
	// A save without eligibility information can still resume the battle, but
	// cannot establish that it was an eligible attempt before the save.
	run := &survivalBestRun{excluded: "save has no personal-best eligibility record"}
	if len(raw) == 0 {
		return run, nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, survivalBestSaveError("a null personal-best record")
	}
	var rec survivalBestRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, survivalBestSaveError(err.Error())
	}
	if rec.Version != survivalBestRecordVersion {
		return nil, survivalBestSaveError(fmt.Sprintf("version %d", rec.Version))
	}
	run.key, run.excluded = rec.Key, rec.Excluded
	if rec.Key != "" {
		key, err := hex.DecodeString(rec.Key)
		if err != nil || len(key) != 32 {
			return nil, survivalBestSaveError("an invalid comparison key")
		}
	}
	if rec.Content != "" {
		digest, err := hex.DecodeString(rec.Content)
		if err != nil || len(digest) != len(run.content) {
			return nil, survivalBestSaveError("an invalid content identity")
		}
		copy(run.content[:], digest)
	}
	if run.excluded == "" {
		if run.key == "" || rec.Content == "" {
			return nil, survivalBestSaveError("eligibility without comparison and content identities")
		}
		if smartMakersChanged {
			run.excluded = "Smart makers changed at load"
		} else if digest, err := s.SurvivalSaveContentDigest(fs); err != nil || digest != run.content {
			run.excluded = "content changed since save"
		} else {
			// Bests may have improved on this host since the save. Reload the
			// current record instead of carrying its cached score in the save.
			run.best, run.exists, run.err = settings.LoadSurvivalBest(run.key)
			if run.err != nil {
				fmt.Fprintln(os.Stderr, run.err)
			}
		}
	}
	return run, nil
}

func survivalBestSaveError(what string) error {
	return fmt.Errorf("nanolathe: save personal-best record unreadable: %s: logical path save/sidecar/survivalBest, providers searched [sidecar], expected version %d eligibility and comparison identities", what, survivalBestRecordVersion)
}
