// internal/llamacpp/installer.go
// Purpose: llama.cpp runtime detection and source build.
package llamacpp

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// DefaultDir returns ~/.helix/llama.cpp.
func DefaultDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}

	return filepath.Join(home, ".helix", "llama.cpp")
}

// EnsureRuntime finds or builds llama-server.
func EnsureRuntime(ctx context.Context) (string, error) {
	if binary, err := LocateServerBinary(); err == nil {
		return binary, nil
	}

	ctx, cancel := context.WithTimeout(ctx, 45*time.Minute)
	defer cancel()

	return buildFromSource(ctx)
}

// LocateServerBinary finds an existing llama-server binary.
func LocateServerBinary() (string, error) {
	if path, err := exec.LookPath("llama-server"); err == nil {
		return path, nil
	}

	exe := ""
	if runtime.GOOS == "windows" {
		exe = ".exe"
	}

	candidates := []string{
		filepath.Join(DefaultDir(), "build", "bin", "llama-server"+exe),
		filepath.Join(DefaultDir(), "build", "bin", "Release", "llama-server"+exe),
	}

	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("llama-server binary not found")
}

func buildFromSource(ctx context.Context) (string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return "", fmt.Errorf("git is required to build llama.cpp")
	}

	if _, err := exec.LookPath("cmake"); err != nil {
		return "", fmt.Errorf("cmake is required to build llama.cpp")
	}

	compiler := ""
	for _, candidate := range []string{"c++", "clang++", "g++", "cl"} {
		if path, err := exec.LookPath(candidate); err == nil {
			compiler = path
			break
		}
	}

	if compiler == "" {
		return "", fmt.Errorf("no C++ compiler found; install clang, g++, or MSVC")
	}

	dir := DefaultDir()
	src := filepath.Join(dir, "src")
	build := filepath.Join(dir, "build")

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create llama.cpp directory: %w", err)
	}

	if _, err := os.Stat(src); os.IsNotExist(err) {
		if err := runCmd(ctx, dir, "git", "clone", "--depth", "1", "https://github.com/ggml-org/llama.cpp", src); err != nil {
			return "", err
		}
	}

	if err := runCmd(ctx, dir, "cmake", "-S", src, "-B", build, "-DCMAKE_BUILD_TYPE=Release"); err != nil {
		return "", err
	}

	if err := runCmd(ctx, dir, "cmake", "--build", build, "--config", "Release", "-j"); err != nil {
		return "", err
	}

	binary, err := LocateServerBinary()
	if err != nil {
		return "", fmt.Errorf("build completed but binary not found: %w", err)
	}

	return binary, nil
}

func runCmd(ctx context.Context, dir string, name string, args ...string) error {
	logPath := filepath.Join(DefaultDir(), "build.log")

	if err := os.MkdirAll(DefaultDir(), 0o755); err != nil {
		return fmt.Errorf("create llama.cpp directory: %w", err)
	}

	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open llama.cpp build log: %w", err)
	}
	defer func() { _ = logFile.Close() }()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s failed: %w (log: %s)", name, err, logPath)
	}

	return nil
}
