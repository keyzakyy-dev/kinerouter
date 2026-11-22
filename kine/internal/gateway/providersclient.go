package gateway

// GET /api/providers/client — the paginated, credential-free connection list
// the quota tracker and topology views read. Ported from
// src/app/api/providers/client/route.js.
//
// Eligibility mirrors the JS route: the provider must have a usage endpoint and
// either an OAuth connection or a provider that allows api-key usage queries.
// MiniMax auth-lease connections (authType "mcode-lease") are eligible the
// same way OAuth ones are — they borrow the desktop app's sign-in per request.

import (
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/keyzakyy-dev/kinerouter/internal/lease"
	"github.com/keyzakyy-dev/kinerouter/internal/store"
)

const (
	defaultClientPageSize = 20
	maxClientPageSize     = 500
)

func (g *Gateway) registerProvidersClientRoutes(mux *http.ServeMux) {
	// Registered after /api/providers/{id}; the literal path wins in the Go
	// 1.22 mux, so "client" is not read as a provider id.
	mux.HandleFunc("GET /api/providers/client", g.requireAuth(g.handleProvidersClient))
}

func (g *Gateway) usageEligible(c *store.Connection) bool {
	e, ok := g.reg.ByID[c.Provider]
	if !ok || !e.UsageSupported() {
		return false
	}
	return c.AuthType == "oauth" || lease.IsLeaseAuth(c.AuthType) || e.UsageApikey()
}

func (g *Gateway) handleProvidersClient(w http.ResponseWriter, r *http.Request) {
	conns, err := g.store.ListConnections("", false)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "Failed to fetch providers")
		return
	}

	eligible := make([]*store.Connection, 0, len(conns))
	seen := map[string]bool{}
	for _, c := range conns {
		if g.usageEligible(c) {
			eligible = append(eligible, c)
			seen[c.Provider] = true
		}
	}
	providerOptions := make([]string, 0, len(seen))
	for id := range seen {
		providerOptions = append(providerOptions, id)
	}
	sort.Strings(providerOptions)

	q := r.URL.Query()
	provider := q.Get("provider")
	if provider == "" {
		provider = "all"
	}
	accountStatus := q.Get("accountStatus")
	if accountStatus == "" {
		accountStatus = "all"
	}

	providerFiltered := eligible
	if provider != "all" {
		providerFiltered = make([]*store.Connection, 0, len(eligible))
		for _, c := range eligible {
			if c.Provider == provider {
				providerFiltered = append(providerFiltered, c)
			}
		}
	}

	filtered := make([]*store.Connection, 0, len(providerFiltered))
	for _, c := range providerFiltered {
		switch accountStatus {
		case "active":
			if c.IsActive {
				filtered = append(filtered, c)
			}
		case "inactive":
			if !c.IsActive {
				filtered = append(filtered, c)
			}
		default:
			filtered = append(filtered, c)
		}
	}

	if q.Get("sort") == "provider" {
		sort.SliceStable(filtered, func(i, j int) bool {
			return filtered[i].Provider < filtered[j].Provider
		})
	} else {
		sort.SliceStable(filtered, func(i, j int) bool {
			if filtered[i].Priority != filtered[j].Priority {
				return filtered[i].Priority < filtered[j].Priority
			}
			return filtered[i].Provider < filtered[j].Provider
		})
	}

	page := parsePositiveInt(q.Get("page"), 1)
	pageSize := min(parsePositiveInt(q.Get("pageSize"), defaultClientPageSize), maxClientPageSize)
	total := len(filtered)
	totalPages := max(1, (total+pageSize-1)/pageSize)
	if page > totalPages {
		page = totalPages
	}
	start := (page - 1) * pageSize
	end := min(start+pageSize, total)

	out := make([]map[string]any, 0, end-start)
	for _, c := range filtered[start:end] {
		out = append(out, sanitizeConn(c))
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"connections":     out,
		"providerOptions": providerOptions,
		"pagination": map[string]any{
			"page": page, "pageSize": pageSize, "total": total, "totalPages": totalPages,
		},
		"totals": map[string]any{
			"eligibleConnections":         len(eligible),
			"providerFilteredConnections": len(providerFiltered),
		},
	})
}

func parsePositiveInt(raw string, fallback int) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}
