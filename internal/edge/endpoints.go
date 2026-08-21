// internal/edge/endpoints.go
// Purpose: detect local sidecars configured onto the same address.
//
// Why this needs to exist: several of the runtimes Helix talks to pick the SAME
// stock port. llama-server defaults to 8080. whisper.cpp's server also defaults
// to 8080. They cannot both be there, and the symptom of the clash is not an
// error that names it — it is llama.cpp answering a transcription request with a
// 404, or whisper.cpp answering a chat request with one. The user sees "local
// STT is broken", or worse, a health check that says "reachable".
//
// Naming the collision directly is the difference between a five-minute fix and
// an afternoon.
package edge

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// Endpoint is one configured local service.
type Endpoint struct {
	// Service is the user-facing name ("llama.cpp", "whisper-local").
	Service string

	// Role says what it is for ("LLM", "STT", "TTS"), so a conflict message can
	// explain why two things wanting one port is a problem.
	Role string

	// URL is the configured endpoint.
	URL string

	// Active reports whether this service is actually selected. An inactive
	// service sharing a port is worth a note, not a warning.
	Active bool
}

// Conflict is two or more services pointed at one address.
type Conflict struct {
	Address   string
	Endpoints []Endpoint
}

// Involves reports whether the conflict includes an active service — the case
// that actually breaks something right now.
func (c Conflict) Involves() bool {
	for _, e := range c.Endpoints {
		if e.Active {
			return true
		}
	}
	return false
}

// Describe renders the conflict and what to do about it.
func (c Conflict) Describe() string {
	names := make([]string, 0, len(c.Endpoints))
	for _, e := range c.Endpoints {
		label := fmt.Sprintf("%s (%s)", e.Service, e.Role)
		if !e.Active {
			label += " [not selected]"
		}
		names = append(names, label)
	}
	return fmt.Sprintf("%s is configured for %s", c.Address, strings.Join(names, " and "))
}

// FindConflicts groups endpoints by host:port and returns the addresses claimed
// more than once, ordered for stable output.
//
// Only host:port is compared, deliberately: two services on one port are in
// conflict no matter how their paths differ, because a single process owns the
// listener.
func FindConflicts(endpoints []Endpoint) []Conflict {
	byAddr := map[string][]Endpoint{}
	for _, e := range endpoints {
		addr := hostPort(e.URL)
		if addr == "" {
			continue
		}
		byAddr[addr] = append(byAddr[addr], e)
	}

	var out []Conflict
	for addr, group := range byAddr {
		if len(group) < 2 {
			continue
		}
		// Distinct SERVICES, not distinct entries: the same service listed twice
		// is not a conflict.
		seen := map[string]bool{}
		distinct := make([]Endpoint, 0, len(group))
		for _, e := range group {
			if seen[e.Service] {
				continue
			}
			seen[e.Service] = true
			distinct = append(distinct, e)
		}
		if len(distinct) < 2 {
			continue
		}
		sort.Slice(distinct, func(i, j int) bool { return distinct[i].Service < distinct[j].Service })
		out = append(out, Conflict{Address: addr, Endpoints: distinct})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Address < out[j].Address })
	return out
}

// hostPort reduces a URL to host:port, filling in the scheme's default port so
// "http://host" and "http://host:80" compare equal.
func hostPort(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		switch strings.ToLower(u.Scheme) {
		case "https":
			port = "443"
		default:
			port = "80"
		}
	}
	// Loopback spellings are the same listener; without this, a sidecar
	// configured as "localhost" and another as "127.0.0.1" would not be seen to
	// clash — which is exactly how the defaults are written.
	if isLoopback(host) {
		host = "127.0.0.1"
	}
	return host + ":" + port
}

func isLoopback(host string) bool {
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1", "0.0.0.0", "[::1]":
		return true
	}
	return false
}
