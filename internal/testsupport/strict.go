// Package testsupport provides strict-gate evidence and hygiene helpers [ON-10 §12].
package testsupport

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// StrictEvidence is the compact structured record per [ON-10 §12 Evidence contract].
// Every strict run emits this shape. Commit is git HEAD, content_manifest is
// catalog hash, map is map key, seed is global sim seed (crt seed can be derived
// from SimSeed^CrtSeed if needed), milestones are stage→tick.
type StrictEvidence struct {
	Commit          string            `json:"commit"`
	ContentManifest string            `json:"content_manifest"`
	Map             string            `json:"map"`
	Seed            uint32            `json:"seed"`
	CrtSeed         uint32            `json:"crt_seed,omitempty"`
	Players         []StrictPlayer    `json:"players"`
	MaxTick         int               `json:"max_tick"`
	Milestones      map[string]uint32 `json:"milestones"`
	Winner          int               `json:"winner"`
	Reason          string            `json:"reason"`
	FinalTick       uint32            `json:"final_tick"`
	FinalStateHash  string            `json:"final_state_hash"`
	TraceHash       string            `json:"trace_hash"`
	Fallbacks       []string          `json:"fallbacks"`
	Warnings        []string          `json:"warnings"`
}

// StrictPlayer mirrors SkirmishConfig.Players slot for evidence.
type StrictPlayer struct {
	Slot      int    `json:"slot"`
	Side      string `json:"side,omitempty"`
	Control   string `json:"control"`
	AllyGroup int    `json:"ally_group,omitempty"`
	AIProfile string `json:"ai_profile,omitempty"`
}

// StrictFailureRecord is the required failure diagnostic per [ON-10 §12].
type StrictFailureRecord struct {
	LastCompletedMilestone string          `json:"last_completed_milestone"`
	CurrentTick            uint32          `json:"current_tick"`
	Seed                   uint32          `json:"seed"`
	CrtSeed                uint32          `json:"crt_seed"`
	Handles                []uint32        `json:"handles,omitempty"`
	DefinitionKeys         []string        `json:"definition_keys,omitempty"`
	QueueHead              string          `json:"queue_head,omitempty"`
	PathStatus             string          `json:"path_status,omitempty"`
	AimState               string          `json:"aim_state,omitempty"`
	ResourceStocks         string          `json:"resource_stocks,omitempty"`
	ProjectileCount        int             `json:"projectile_count"`
	AITask                 string          `json:"ai_task,omitempty"`
	AIDeadline             uint32          `json:"ai_deadline,omitempty"`
	ResultLatch            string          `json:"result_latch,omitempty"`
	Last50Trace            []string        `json:"last_50_trace"`
	Evidence               *StrictEvidence `json:"evidence,omitempty"`
}

// HashBytes returns hex sha256 of b truncated to 16 hex chars (8 bytes) for compact evidence.
// Uses stable truncation: first 8 bytes hex.
func HashBytes(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:8])
}

// HashStrings hashes ordered strings deterministically (no map iteration) [I1].
func HashStrings(ss []string) string {
	h := sha256.New()
	for _, s := range ss {
		h.Write([]byte(s))
		h.Write([]byte{0})
	}
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:8])
}

// CommitHash returns git HEAD short or full via exec. Falls back to "unknown" if git not available.
func CommitHash() string {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

// GofmtCheck runs `gofmt -l .` and returns list of unformatted files. Empty means clean.
func GofmtCheck(root string) ([]string, error) {
	if root == "" {
		root = "."
	}
	cmd := exec.Command("gofmt", "-l", ".")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gofmt -l: %w", err)
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return nil, nil
	}
	lines := strings.Split(s, "\n")
	var filtered []string
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		// Exclude worktrees and other ignored paths that are not part of the main checkout.
		if strings.HasPrefix(l, ".worktrees/") || strings.Contains(l, "/.worktrees/") || strings.HasPrefix(l, ".git/") || strings.Contains(l, "internal/gafdumptmp") || strings.Contains(l, ".opencode/") {
			continue
		}
		if strings.HasPrefix(l, "internal/gafdumptmp/") {
			continue
		}
		filtered = append(filtered, l)
	}
	return filtered, nil
}

// VetCheck runs `go vet ./...` and returns output. Empty means clean.
func VetCheck(root string) (string, error) {
	if root == "" {
		root = "."
	}
	cmd := exec.Command("go", "vet", "./...")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("go vet: %w output: %s", err, string(out))
	}
	if len(out) != 0 {
		return string(out), fmt.Errorf("go vet output: %s", string(out))
	}
	return "", nil
}

// ProprietaryAssetCheck scans for retail assets committed to repo [ON-10 G0].
// Returns list of offending paths. Allowed to exist under ignored dirs like
// .worktrees? Root is repo root, we check only tracked files via git ls-files
// or filesystem scan excluding .git, .worktrees, /tmp.
func ProprietaryAssetCheck(root string) ([]string, error) {
	if root == "" {
		root = "."
	}
	// Use git ls-files to avoid scanning untracked .worktrees
	cmd := exec.Command("git", "ls-files")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		// fallback to filesystem walk
		return proprietaryAssetFilesystemWalk(root)
	}
	exts := []string{".hpi", ".ufo", ".ccx", ".gp3", ".tnt", ".3do", ".gaf", ".wav", ".pcx"}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var bad []string
	for _, p := range lines {
		lower := strings.ToLower(p)
		for _, ext := range exts {
			if strings.HasSuffix(lower, ext) {
				bad = append(bad, p)
			}
		}
	}
	return bad, nil
}

func proprietaryAssetFilesystemWalk(root string) ([]string, error) {
	var bad []string
	exts := []string{".hpi", ".ufo", ".ccx", ".gp3", ".tnt", ".3do", ".gaf", ".wav", ".pcx"}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			base := filepath.Base(path)
			if base == ".git" || base == ".worktrees" || base == "TotalAnnihilation" || base == ".opencode" {
				return filepath.SkipDir
			}
			return nil
		}
		lower := strings.ToLower(info.Name())
		for _, ext := range exts {
			if strings.HasSuffix(lower, ext) {
				bad = append(bad, path)
			}
		}
		return nil
	})
	return bad, err
}

// WallClockScan scans authoritative packages for time.Now usage [I6].
// Returns list of files with forbidden wall-clock.
func WallClockScan(root string) ([]string, error) {
	return grepScan(root, regexp.MustCompile(`time\.Now|time\.Since|time\.Until`), []string{"internal/clock", "internal/kernel", "internal/units", "internal/orders", "internal/cob", "internal/movement", "internal/path", "internal/economy", "internal/construction", "internal/features", "internal/combat", "internal/visibility", "internal/ai", "internal/mission", "internal/triggers", "internal/session"})
}

// MapIterationScan scans for `range .*map\[` in authoritative packages [I1].
func MapIterationScan(root string) ([]string, error) {
	// Look for range over map literal that could be authoritative map iteration.
	// We use simple regex: pattern `range` + `.*map\[` is too broad; instead search for `range\s+\w+\.Players|range\s+.*Catalog|range\s+.*Units` etc?
	// For G0 we just ensure no hidden map iteration in loop.go style files; we flag any `for\s+.*range.*map` in authoritative dirs.
	return grepScan(root, regexp.MustCompile(`for\s+.*range`), []string{"internal/session", "internal/combat", "internal/movement", "internal/economy", "internal/construction", "internal/ai"})
}

// FloatScan scans for float64/float32 in authoritative packages except allowlist [I2].
// Returns files with float usage not on allowlist (needs manual review).
func FloatScan(root string) ([]string, error) {
	return grepScan(root, regexp.MustCompile(`float(32|64)`), []string{"internal/sim/numeric", "internal/sim/rng", "internal/content", "internal/world", "internal/combat", "internal/movement", "internal/economy", "internal/visibility", "internal/ai", "internal/session"})
}

func grepScan(root string, re *regexp.Regexp, pkgPrefixes []string) ([]string, error) {
	var hits []string
	for _, prefix := range pkgPrefixes {
		dir := filepath.Join(root, prefix)
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			if re.Match(b) {
				// For map iteration we need stricter: check if file contains map iteration that is not content compile-time.
				// For now report all hits; caller filters allowlist.
				hits = append(hits, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return hits, nil
}

// EmitManifest writes a JSON manifest to path or returns bytes. Used for run manifest [ON-10 G0].
func EmitManifest(ev StrictEvidence) ([]byte, error) {
	return json.MarshalIndent(ev, "", "  ")
}

// LastNTrace returns last N formatted trace lines for failure record.
func LastNTrace(lines []string, n int) []string {
	if len(lines) <= n {
		return lines
	}
	return lines[len(lines)-n:]
}
