// internal/terminal/interceptor.go
package terminal

import (
	"encoding/base64"
	"strings"
)

// InterceptorCallbacks defines hooks for terminal side-effects.
type InterceptorCallbacks struct {
	OnTitleChange func(string)
	OnClipboard   func(string)
	OnBell        func()
	OnShellPrompt func() // OSC 133
}

// Interceptor scans raw PTY output for side-effect sequences (BEL, OSC),
// triggers callbacks, and returns the cleaned byte stream for the VT parser.
type Interceptor struct {
	callbacks InterceptorCallbacks
	buffer    []byte
}

// NewInterceptor creates a new sequence interceptor.
func NewInterceptor(cb InterceptorCallbacks) *Interceptor {
	return &Interceptor{callbacks: cb}
}

// Process parses the byte stream, extracts OSC/BEL, and returns clean data.
func (i *Interceptor) Process(data []byte) []byte {
	i.buffer = append(i.buffer, data...)

	var out []byte
	for len(i.buffer) > 0 {
		// 1. Check for OSC start: ESC ] (0x1b 0x5d)
		if len(i.buffer) >= 2 && i.buffer[0] == 0x1b && i.buffer[1] == 0x5d {
			termIdx := -1
			termLen := 0
			// OSC can be terminated by ESC \ (0x1b 0x5c) OR BEL (0x07)
			for j := 2; j < len(i.buffer)-1; j++ {
				if i.buffer[j] == 0x1b && i.buffer[j+1] == 0x5c {
					termIdx = j
					termLen = 2
					break
				}
				if i.buffer[j] == 0x07 {
					termIdx = j
					termLen = 1
					break
				}
			}
			// Check if the very last byte is BEL
			if termIdx == -1 && i.buffer[len(i.buffer)-1] == 0x07 {
				termIdx = len(i.buffer) - 1
				termLen = 1
			}

			if termIdx != -1 {
				payload := string(i.buffer[2:termIdx])
				i.handleOSC(payload)
				i.buffer = i.buffer[termIdx+termLen:]
				continue
			} else {
				// Incomplete OSC sequence, wait for more data
				break
			}
		}

		// 2. Check for standalone ASCII BEL (0x07)
		if i.buffer[0] == 0x07 {
			if i.callbacks.OnBell != nil {
				i.callbacks.OnBell()
			}
			i.buffer = i.buffer[1:]
			continue
		}

		// 3. Normal byte, pass through to VT parser
		out = append(out, i.buffer[0])
		i.buffer = i.buffer[1:]
	}

	return out
}

// handleOSC routes specific OSC sequences to their callbacks.
func (i *Interceptor) handleOSC(payload string) {
	// OSC 0, 1, 2: Terminal Title
	if strings.HasPrefix(payload, "0;") || strings.HasPrefix(payload, "1;") || strings.HasPrefix(payload, "2;") {
		title := strings.SplitN(payload, ";", 2)[1]
		if i.callbacks.OnTitleChange != nil {
			i.callbacks.OnTitleChange(title)
		}
	}

	// OSC 52: Clipboard Integration
	if strings.HasPrefix(payload, "52;") {
		parts := strings.SplitN(payload, ";", 3)
		if len(parts) == 3 {
			b64 := parts[2]
			if b64 == "?" {
				return // Query clipboard not implemented for security
			}
			decoded, err := base64.StdEncoding.DecodeString(b64)
			if err == nil && i.callbacks.OnClipboard != nil {
				i.callbacks.OnClipboard(string(decoded))
			}
		}
	}

	// OSC 133: Shell Integration (Prompt boundaries)
	if strings.HasPrefix(payload, "133;") {
		if i.callbacks.OnShellPrompt != nil {
			i.callbacks.OnShellPrompt()
		}
	}
}
