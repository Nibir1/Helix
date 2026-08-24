// internal/speech/local_diagnosis.go
// Purpose: turn a failed local-sidecar request into something the user can act
// on.
//
// A raw transport error is not a diagnosis. "piper-local: HTTP 403:" — an empty
// body and a bare status — is technically accurate and practically useless: it
// does not say that something IS listening, that it is not Piper, what it
// probably is, or what to do. And the most common cause on macOS is not even a
// Helix problem: AirPlay Receiver owns port 5000 by default, answers HTTP, and
// returns 403 to everything.
//
// So each local adapter runs its failure through here before returning it.
package speech

import (
	"errors"
	"fmt"
	"net/url"
	"runtime"
	"strings"
	"syscall"

	"helix/internal/providers"
)

// knownPortSquatter names the service that habitually owns a port on this
// platform, when there is one. Empty means "no common culprit".
//
// Only well-known, platform-default occupants belong here. A guess that is
// usually wrong is worse than no guess at all.
func knownPortSquatter(port string) string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	switch port {
	case "5000", "7000":
		// AirPlay Receiver, on by default since macOS Monterey. It answers HTTP
		// and 403s everything, which is exactly what a misconfigured sidecar
		// looks like.
		return "macOS AirPlay Receiver"
	}
	return ""
}

// LocalDiagnosis explains why a request to a local sidecar failed.
//
// Args:
//   - provider: the Helix provider name, for the leading label.
//   - origin: the endpoint that was tried (scheme://host:port).
//   - startCmd: the command that launches this sidecar, or "".
//   - configKey: the /config key that repoints it, or "".
//   - err: the failure.
//
// Returns: a multi-line explanation ending in the concrete next step.
// Complexity: O(len(err)).
func LocalDiagnosis(provider, origin, startCmd, configKey string, err error) error {
	if err == nil {
		return nil
	}
	port := endpointPort(origin)

	var b strings.Builder
	fmt.Fprintf(&b, "%s at %s: ", provider, origin)

	switch {
	case isConnectionRefused(err):
		b.WriteString("nothing is listening.")
		appendLines(&b, startCmd != "", "Start it:", "  "+startCmd)

	case isStatus(err, 401, 403):
		// A server that answers and refuses is a DIFFERENT server. A real local
		// sidecar has no credentials to reject.
		code, _ := providers.StatusCode(err)
		fmt.Fprintf(&b, "something IS listening and refused the request (HTTP %d).", code)
		b.WriteString("\n  That is not this sidecar — a local one has no credentials to reject.")
		if squatter := knownPortSquatter(port); squatter != "" {
			fmt.Fprintf(&b, "\n  On this platform port %s is normally %s.", port, squatter)
		} else {
			fmt.Fprintf(&b, "\n  Find the owner:  lsof -nP -iTCP:%s -sTCP:LISTEN", port)
		}
		// No "move it to a free port" here: Helix assigns ports itself now, and
		// offering the manual alternative alongside an assignment it has already
		// made produced two contradictory port numbers in one message.
		appendLines(&b, configKey != "",
			"Run /blackbox setup to have Helix pick a free port for it.")

	case providers.IsNotFound(err):
		b.WriteString("a server answered, but none of the routes Helix knows exist on it.")
		b.WriteString("\n  Either it is a different service, or its API moved.")
		appendLines(&b, configKey != "", "If the sidecar is elsewhere:", "  /config "+configKey+" <url>")

	default:
		b.WriteString(err.Error())
		appendLines(&b, startCmd != "", "Expected launch:", "  "+startCmd)
	}

	return errors.New(b.String())
}

// appendLines adds a labelled block when the condition holds.
func appendLines(b *strings.Builder, when bool, lines ...string) {
	if !when {
		return
	}
	for _, l := range lines {
		b.WriteString("\n  " + l)
	}
}

// endpointPort extracts the port from an endpoint, defaulting by scheme.
func endpointPort(origin string) string {
	u, err := url.Parse(strings.TrimSpace(origin))
	if err != nil || u.Host == "" {
		return "?"
	}
	if p := u.Port(); p != "" {
		return p
	}
	if strings.EqualFold(u.Scheme, "https") {
		return "443"
	}
	return "80"
}

// isClientError reports whether err carries a 4xx status — evidence that a
// server answered but this route is not the one, so another may be.
func isClientError(err error) bool {
	code, ok := providers.StatusCode(err)
	return ok && code >= 400 && code < 500
}

// isStatus reports whether err carries any of the given HTTP status codes.
func isStatus(err error, codes ...int) bool {
	got, ok := providers.StatusCode(err)
	if !ok {
		return false
	}
	for _, c := range codes {
		if got == c {
			return true
		}
	}
	return false
}

// isConnectionRefused reports whether the failure was "nothing listening".
//
// Checked through errors.Is on the syscall rather than by string match, so it
// holds across platforms and locales.
func isConnectionRefused(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED)
}

// Config keys for the local sidecars, kept next to the diagnosis that prints
// them.
const (
	whisperCfgKey = "stt-url"
	piperCfgKey   = "tts-url"
	kokoroCfgKey  = "tts-url"
	csmCfgKey     = "tts-url"
)

// Launch commands are rendered against the endpoint ACTUALLY configured, not a
// constant.
//
// They used to be fixed strings carrying a hardcoded port, which produced
// self-contradicting advice the moment an endpoint moved: "piper-local at
// http://127.0.0.1:28183: nothing is listening. Start it: ... --port 5001".
// Following that command starts a server Helix will not talk to.

func whisperStartCmd(origin string) string {
	return fmt.Sprintf("whisper-server -m models/ggml-base.en.bin --port %s",
		endpointPort(origin))
}

func piperStartCmd(origin string) string {
	return fmt.Sprintf("python3 -m piper.http_server -m en_US-lessac-medium.onnx --port %s",
		endpointPort(origin))
}

// csmStartCmd names the csm.rs server invocation against the configured port.
//
// The --port is explicit because csm.rs defaults to 8080, which whisper.cpp and
// llama.cpp also default to: a user with a local STT chain or a local brain is
// exactly the user who wants CSM, and they would collide on first launch.
func csmStartCmd(origin string) string {
	return fmt.Sprintf("csm-server --model-id sesame/csm-1b --port %s",
		endpointPort(origin))
}

func kokoroStartCmd(origin string) string {
	// Kokoro serves 8880 inside the container; only the published port moves.
	return fmt.Sprintf("docker run -p %s:8880 ghcr.io/remsky/kokoro-fastapi-cpu",
		endpointPort(origin))
}
