package client

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/content"
)

// strategicIconLevel describes Enhanced presentation policy, not a retail tech
// tier (DESIGN_GPU_RENDERER §18). Level zero has no badge; Unresolved distinguishes
// an unknown level from a known zero-factory route.
type strategicIconLevel struct {
	Level      int
	Evidence   string
	Unresolved string
}

// strategicResolveIconLevel applies the Enhanced policy to each retained record:
// reachable factory depth wins; otherwise its authored positive whole LEVELn is
// the fallback. Audit evidence preserves both inputs, including disagreements.
func strategicResolveIconLevel(category string, graph strategicIconLevel) strategicIconLevel {
	authored, hasAuthored := strategicAuthoredIconLevel(category)
	if !hasAuthored {
		authored.Evidence = "level: no valid positive whole Category LEVELn token"
	}
	if graph.Evidence != "" && graph.Unresolved == "" {
		graph.Evidence += "; " + authored.Evidence + "; reachable factory depth takes precedence"
		return graph
	}
	if graph.Evidence == "" {
		graph.Evidence = "Enhanced inference: no build route from Commander=true"
	}
	authored.Evidence = graph.Evidence + "; authored fallback: " + authored.Evidence
	if !hasAuthored {
		// TODO(question): what level is intended without a route or authored level?
		// Corrected authored metadata or a complete build route can settle it.
		authored.Unresolved = "no authored level and no reachable true-commander build route; no level badge"
	}
	return authored
}

// strategicAuthoredIconLevel reads the record's category independently of the
// graph's name resolution. The boolean includes conflicts: those cannot supply
// an authored fallback but remain visible when reachable graph depth resolves it.
func strategicAuthoredIconLevel(category string) (strategicIconLevel, bool) {
	level, authored, conflict := 0, false, false
	var tokens []string
	for _, token := range strings.Fields(strings.ToUpper(category)) {
		if !strings.HasPrefix(token, "LEVEL") || len(token) == len("LEVEL") {
			continue
		}
		digits := token[len("LEVEL"):]
		valid := true
		for _, c := range digits {
			if c < '0' || c > '9' {
				valid = false
				break
			}
		}
		if !valid {
			continue
		}
		n, err := strconv.Atoi(digits)
		if err != nil || n <= 0 {
			continue
		}
		tokens = append(tokens, token)
		if authored && n != level {
			conflict = true
		}
		level, authored = n, true
	}
	if conflict {
		// TODO(question): which authored level is intended by conflicting tokens?
		// A corrected category must settle the fallback when no build route exists.
		return strategicIconLevel{
			Evidence:   "level: conflicting positive whole Category LEVELn tokens " + strings.Join(tokens, ", "),
			Unresolved: "authored level tokens disagree; no level badge",
		}, true
	}
	if authored {
		return strategicIconLevel{Level: level, Evidence: fmt.Sprintf("level: authored Category LEVEL%d", level)}, true
	}
	return strategicIconLevel{}, false
}

// strategicIconBuildLevels runs a small load-time Dijkstra search with zero cost
// for ordinary products and one for entering a factory. Factory qualification
// requires Builder, BMCode=0 and a nonempty final menu, including downloads;
// CanMove is irrelevant [SC21][02 R-CAT-01 §8]. Equal-cost routes retain the first
// settled canonical predecessor, making audit evidence reproducible across maps.
func strategicIconBuildLevels(cat *content.Catalog) map[string]strategicIconLevel {
	keys := cat.SortedUnitKeys()
	out := make(map[string]strategicIconLevel, len(keys))
	distance := make(map[string]int, len(keys))
	routes := make(map[string]string, len(keys))
	settled := make(map[string]bool, len(keys))
	for _, key := range keys {
		u, _ := cat.Unit(key)
		if u != nil && u.Commander {
			distance[key] = 0
			routes[key] = key
		}
	}
	for {
		current := ""
		for _, key := range keys {
			d, reached := distance[key]
			if reached && !settled[key] && (current == "" || d < distance[current]) {
				current = key
			}
		}
		if current == "" {
			break
		}
		settled[current] = true
		builder, _ := cat.Unit(current)
		menu := cat.BuildMenus[current]
		if builder == nil || !builder.Builder || menu == nil {
			continue
		}
		for _, name := range menu.Buttons {
			key := content.CanonicalKey(name)
			u, exists := cat.Unit(key)
			if !exists || u == nil || settled[key] {
				continue
			}
			d := distance[current]
			if products := cat.BuildMenus[key]; u.Builder && u.BMCode == 0 && products != nil && len(products.Buttons) > 0 {
				d++
			}
			if previous, reached := distance[key]; !reached || d < previous {
				distance[key] = d
				routes[key] = routes[current] + " -> " + key
			}
		}
	}
	for _, key := range keys {
		if d, reached := distance[key]; reached {
			out[key] = strategicIconLevel{Level: d, Evidence: fmt.Sprintf("Enhanced inference: minimum %d factories from Commander=true via %s", d, routes[key])}
		} else {
			out[key] = strategicIconLevel{Evidence: "Enhanced inference: no build route from Commander=true", Unresolved: "no reachable true-commander build route"}
		}
	}
	return out
}
