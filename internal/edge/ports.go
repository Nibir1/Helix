// internal/edge/ports.go
// Purpose: pick a loopback port that is actually free, instead of trusting a
// stock default that half the ecosystem also claims.
//
// The defaults collide badly in practice: llama.cpp and whisper.cpp both want
// 8080, and macOS AirPlay Receiver holds 5000 and 7000 on every machine by
// default. Each collision presents as the sidecar being "broken" — whichever
// process owns the port answers, so a naive probe sees a live socket and the
// requests 404.
//
// Range choice matters and is not arbitrary. Candidates sit in 28000-28999:
// below Linux's ephemeral range (32768-60999) and macOS's (49152-65535), so the
// kernel will not hand one of these to an unrelated outbound connection between
// Helix suggesting it and the user launching a server on it. It is also free of
// well-known services.
package edge

import (
	"fmt"
	"hash/fnv"
	"net"
	"strconv"
	"strings"
	"time"
)

const (
	// candidateBase and candidateSpan bound the search window. See the package
	// note for why this range and not the ephemeral one.
	candidateBase = 28000
	candidateSpan = 1000
)

// PortAvailable reports whether a TCP port on loopback is genuinely free.
//
// It CONNECTS first, then tries to bind, because neither check alone is correct:
//
//   - Bind alone gives false "free" results. Go sets SO_REUSEADDR on listeners,
//     and on BSD/macOS that permits binding 127.0.0.1:5000 while another process
//     holds the wildcard *:5000. macOS AirPlay Receiver binds exactly that way,
//     so a bind-only check called port 5000 free on a machine where AirPlay was
//     answering HTTP 403 on it — the precise case this function exists for.
//   - Connect alone gives false "occupied" results for a port that is bound but
//     not accepting, and cannot see a socket in a lingering state.
//
// A successful connect is conclusive: something is serving there. Only when
// nothing answers does the bind decide.
func PortAvailable(port int) bool {
	if port <= 0 || port > 65535 {
		return false
	}
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))

	if conn, err := net.DialTimeout("tcp", addr, 300*time.Millisecond); err == nil {
		_ = conn.Close()
		return false // something accepted a connection: not free
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

// PortOccupant returns a short description of what is on a port, or "" when it
// is free.
//
// Best effort by design: it reports whether something answers HTTP, which is
// what distinguishes "a foreign service is here" from "nothing is here" — the
// two cases that need different advice. Identifying WHICH service would require
// process inspection that is not portable.
func PortOccupant(port int) string {
	if PortAvailable(port) {
		return ""
	}
	conn, err := net.DialTimeout("tcp",
		net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 500*time.Millisecond)
	if err != nil {
		// Bound but not accepting: something holds it without serving.
		return "in use"
	}
	_ = conn.Close()
	return "in use by a running service"
}

// FreePortFor returns a usable loopback port for a service, preferring the
// upstream default and falling back to a stable uncommon one.
//
// Preferring the default matters: someone who launches `whisper-server` with no
// flags gets 8080, and quietly moving Helix off it would break the stock case to
// solve a collision that may not exist on this machine. The fallback is only
// reached when the default is genuinely unusable.
//
// The fallback is DETERMINISTIC per service — derived from the name, not
// randomly chosen — so re-running setup suggests the same port, the saved config
// keeps matching, and the user is not asked to relaunch on a different number
// every time.
//
// Returns the port and whether it is the preferred one.
func FreePortFor(service string, preferred int) (int, bool) {
	return FreePortAvoiding(service, preferred, nil)
}

// FreePortAvoiding is FreePortFor with a set of ports another service already
// claims.
//
// "Free right now" is not the same as "safe to use". whisper.cpp and llama.cpp
// both default to 8080, so on a machine where neither is running yet, both are
// free and both get assigned 8080 — creating a collision that only appears later,
// when the second one starts. Reserving what other configured services claim
// turns that into a problem Helix never creates.
func FreePortAvoiding(service string, preferred int, reserved []int) (int, bool) {
	taken := make(map[int]bool, len(reserved))
	for _, p := range reserved {
		taken[p] = true
	}

	if !taken[preferred] && PortAvailable(preferred) {
		return preferred, true
	}

	start := candidateBase + int(hashString(service)%uint32(candidateSpan))
	for i := 0; i < candidateSpan; i++ {
		port := candidateBase + (start-candidateBase+i)%candidateSpan
		if !taken[port] && PortAvailable(port) {
			return port, false
		}
	}
	// A thousand consecutive occupied ports is not a real machine; returning the
	// preferred one keeps the caller's advice coherent rather than inventing a
	// port that was never checked.
	return preferred, false
}

// hashString is a stable, non-cryptographic hash for deriving a per-service
// starting offset.
func hashString(s string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(strings.ToLower(strings.TrimSpace(s))))
	return h.Sum32()
}

// ReplacePort rewrites the port in a loopback endpoint URL, preserving scheme,
// host and path.
func ReplacePort(endpoint string, port int) string {
	trimmed := strings.TrimSpace(endpoint)
	scheme := "http://"
	rest := trimmed
	for _, p := range []string{"http://", "https://"} {
		if strings.HasPrefix(strings.ToLower(trimmed), p) {
			scheme, rest = trimmed[:len(p)], trimmed[len(p):]
			break
		}
	}

	hostPart, path := rest, ""
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		hostPart, path = rest[:i], rest[i:]
	}
	host := hostPart
	if h, _, err := net.SplitHostPort(hostPart); err == nil {
		host = h
	}
	if host == "" {
		host = "127.0.0.1"
	}
	return fmt.Sprintf("%s%s:%d%s", scheme, host, port, path)
}
