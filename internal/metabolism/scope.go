package metabolism

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
)

// ScopeFor locates a turn: the project it happened in when the working
// directory is inside a repository, otherwise the machine.
//
// Keys are pseudonymous but stable. A project key is the repository's
// directory name plus a short hash of its absolute path, so two checkouts
// called "api" stay distinct and the record names no home directory. A
// machine key is a short hash of the hostname.
func ScopeFor(cwd string) Scope {
	if root, ok := repoRoot(cwd); ok {
		return Scope{Level: ScopeProject, Key: projectKey(root)}
	}
	return Scope{Level: ScopeMachine, Key: MachineKey()}
}

// MachineKey is the pseudonymous key for this machine.
func MachineKey() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown-host"
	}
	return "m-" + shortHash(strings.ToLower(host))
}

func projectKey(root string) string {
	name := filepath.Base(root)
	name = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		}
		return '-'
	}, name)
	return name + "-" + shortHash(root)
}

// repoRoot walks up from dir to the nearest directory holding .git (a
// directory in a normal clone, a file in a worktree or submodule).
func repoRoot(dir string) (string, bool) {
	if dir == "" {
		return "", false
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", false
	}
	for d := abs; ; {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return d, true
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", false
		}
		d = parent
	}
}

func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:4])
}
