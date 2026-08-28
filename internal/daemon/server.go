// internal/daemon/server.go
// Purpose: IPC transport (ADR-004): newline-delimited JSON over a Unix
// domain socket at ~/.helix/daemon.sock (0600) on macOS/Linux; on Windows —
// where stdlib cannot serve named pipes — a loopback-only TCP listener with
// a random per-start token in ~/.helix/daemon.conn.json (0600). Auth model:
// filesystem permissions (unix) / possession of the token file (Windows),
// both same-UID by construction (threat V7).
package daemon

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
)

// Handler dispatches one decoded request to a response.
type Handler interface {
	Handle(ctx context.Context, req Request) Response
}

// Server is the IPC endpoint.
type Server struct {
	ln    net.Listener
	token string
	addr  string
}

// SocketPath returns the Unix socket path.
func SocketPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".helix", "daemon.sock"), nil
}

func connInfoPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".helix", "daemon.conn.json"), nil
}

// Listen binds the platform transport and returns the server.
func Listen() (*Server, error) {
	if runtime.GOOS == "windows" {
		return listenTCP()
	}

	path, err := SocketPath()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}

	// A stale socket from a crashed daemon blocks bind: remove, then verify
	// no live daemon answers on it first.
	if _, err := os.Stat(path); err == nil {
		if err := probeLive(path); err == nil {
			return nil, fmt.Errorf("a Helix daemon is already running (socket %s)", path)
		}
		_ = os.Remove(path)
	}

	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return nil, err
	}
	return &Server{ln: ln, addr: path}, nil
}

// listenTCP binds loopback:0 and records addr+token for the client.
func listenTCP() (*Server, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		_ = ln.Close()
		return nil, err
	}
	token := hex.EncodeToString(raw)

	infoPath, err := connInfoPath()
	if err != nil {
		_ = ln.Close()
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(infoPath), 0o700); err != nil {
		_ = ln.Close()
		return nil, err
	}
	data, _ := json.Marshal(map[string]string{"addr": ln.Addr().String(), "token": token})
	if err := os.WriteFile(infoPath, data, 0o600); err != nil {
		_ = ln.Close()
		return nil, err
	}
	return &Server{ln: ln, token: token, addr: ln.Addr().String()}, nil
}

// Addr returns the bound transport address.
func (s *Server) Addr() string { return s.addr }

// Serve accepts connections until the listener closes. Each connection is
// one NDJSON request/response exchange, handled concurrently; connection
// errors never abort the accept loop.
func (s *Server) Serve(ctx context.Context, h Handler) error {
	go func() {
		<-ctx.Done()
		_ = s.ln.Close()
	}()

	for {
		conn, err := s.ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			continue
		}
		go s.handleConn(ctx, conn, h)
	}
}

func (s *Server) handleConn(ctx context.Context, conn net.Conn, h Handler) {
	defer func() { _ = conn.Close() }()

	if s.token != "" {
		// Windows TCP: first line must be the token.
		reader := bufio.NewReader(conn)
		line, err := reader.ReadString('\n')
		if err != nil || trimSpace(line) != s.token {
			_, _ = conn.Write([]byte(`{"type":"response","ok":false,"error":"unauthorized"}` + "\n"))
			return
		}
		s.readLoop(ctx, reader, conn, h)
		return
	}
	s.readLoop(ctx, bufio.NewReader(conn), conn, h)
}

func (s *Server) readLoop(ctx context.Context, reader *bufio.Reader, conn net.Conn, h Handler) {
	// One connection may carry several requests; the client closes to hang up.
	for {
		if ctx.Err() != nil {
			return
		}
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return // EOF / reset: connection done
		}
		if len(trimSpace(string(line))) == 0 {
			continue
		}

		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			writeResp(conn, Response{Type: TypeResponse, OK: false, Error: "malformed request"})
			continue
		}

		writeResp(conn, h.Handle(ctx, req))
	}
}

func writeResp(conn net.Conn, r Response) {
	data, err := json.Marshal(r)
	if err != nil {
		return
	}
	_, _ = conn.Write(append(data, '\n'))
}

func trimSpace(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r != '\n' && r != '\r' && r != ' ' && r != '\t' {
			out = append(out, r)
		}
	}
	return string(out)
}

// probeLive reports whether an existing socket answers.
func probeLive(path string) error {
	conn, err := net.Dial("unix", path)
	if err != nil {
		return err
	}
	_ = conn.Close()
	return nil
}

// Dial connects to the running daemon's transport (client side).
func Dial() (net.Conn, error) {
	if runtime.GOOS == "windows" {
		infoPath, err := connInfoPath()
		if err != nil {
			return nil, err
		}
		data, err := os.ReadFile(infoPath)
		if err != nil {
			return nil, fmt.Errorf("no daemon connection info (is the daemon running?): %w", err)
		}
		var info struct {
			Addr  string `json:"addr"`
			Token string `json:"token"`
		}
		if err := json.Unmarshal(data, &info); err != nil {
			return nil, fmt.Errorf("corrupt daemon.conn.json: %w", err)
		}
		conn, err := net.Dial("tcp", info.Addr)
		if err != nil {
			return nil, err
		}
		_, _ = conn.Write([]byte(info.Token + "\n"))
		return conn, nil
	}

	path, err := SocketPath()
	if err != nil {
		return nil, err
	}
	return net.Dial("unix", path)
}
