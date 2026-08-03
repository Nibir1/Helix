// internal/llamacpp/server.go
// Purpose: llama.cpp server lifecycle management.
package llamacpp

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// Server manages one llama.cpp server process.
type Server struct {
	Binary    string
	ModelPath string
	Port      int

	mu      sync.Mutex
	cmd     *exec.Cmd
	logFile *os.File
	done    chan error
	running bool
}

// NewServer creates a llama.cpp server manager.
func NewServer(binary, modelPath string, port int) *Server {
	return &Server{
		Binary:    binary,
		ModelPath: modelPath,
		Port:      port,
	}
}

// Endpoint returns the server base URL.
func (s *Server) Endpoint() string {
	return fmt.Sprintf("http://127.0.0.1:%d", s.Port)
}

// Running reports whether the server process is active.
func (s *Server) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.running
}

// Start starts the llama.cpp server and waits for health.
func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return nil
	}

	if s.ModelPath == "" {
		return fmt.Errorf("llama.cpp server requires a model path")
	}

	if _, err := os.Stat(s.ModelPath); err != nil {
		return fmt.Errorf("model file not found: %s", s.ModelPath)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}

	logDir := filepath.Join(home, ".helix")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return fmt.Errorf("create Helix log directory: %w", err)
	}

	logPath := filepath.Join(logDir, "llama-server.log")

	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open llama-server log: %w", err)
	}

	args := []string{
		"-m", s.ModelPath,
		"--host", "127.0.0.1",
		"--port", strconv.Itoa(s.Port),
		"--alias", "helix-local",
	}

	cmd := exec.Command(s.Binary, args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		logFile.Close()
		return fmt.Errorf("failed to start llama-server: %w", err)
	}

	done := make(chan error, 1)

	go func() {
		done <- cmd.Wait()
	}()

	s.cmd = cmd
	s.logFile = logFile
	s.done = done
	s.running = true

	go func() {
		<-done

		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()

	return s.waitReady(ctx)
}

// Stop stops the server process.
func (s *Server) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running || s.cmd == nil || s.cmd.Process == nil {
		return nil
	}

	_ = s.cmd.Process.Kill()

	if s.done != nil {
		<-s.done
	}

	if s.logFile != nil {
		s.logFile.Close()
		s.logFile = nil
	}

	s.cmd = nil
	s.done = nil
	s.running = false

	return nil
}

func (s *Server) waitReady(ctx context.Context) error {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 90*time.Second)
		defer cancel()
	}

	url := s.Endpoint() + "/v1/models"
	client := &http.Client{Timeout: 2 * time.Second}

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("llama.cpp server did not become healthy: %w", ctx.Err())
		case <-ticker.C:
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			if err != nil {
				continue
			}

			resp, err := client.Do(req)
			if err != nil {
				continue
			}

			resp.Body.Close()

			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
	}
}
