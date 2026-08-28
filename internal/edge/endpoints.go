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
	"net"
	"net/url"
	"sort"
	"strconv"
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

	// Occupied reports whether something is listening on the address right now.
	// A collision between two configurations is theoretical until then.
	Occupied bool
}

// ActiveCount is how many of the colliding services are actually selected.
func (c Conflict) ActiveCount() int {
	n := 0
	for _, e := range c.Endpoints {
		if e.Active {
			n++
		}
	}
	return n
}

// Involves reports whether this collision can break something as configured.
//
// TWO active services, not one. A single selected service sharing an address
// with an unselected one is not a conflict in any meaningful sense — nothing
// else is going to bind that port — and reporting it as one produced a red
// warning with a six-line remedy on a machine where the port was simply free.
// The unselected case is still surfaced, quietly, because it will matter if the
// other service is ever selected.
func (c Conflict) Involves() bool {
	return c.ActiveCount() >= 2
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
		out = append(out, Conflict{
			Address:   addr,
			Endpoints: distinct,
			Occupied:  !portFree(addr),
		})
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

// portFree reports whether an address currently has nothing on it.
func portFree(hostPort string) bool {
	_, portStr, err := net.SplitHostPort(hostPort)
	if err != nil {
		return true
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return true
	}
	return PortAvailable(port)
}
