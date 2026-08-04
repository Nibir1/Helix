// internal/commands/git_test.go
package commands

import "testing"

func TestIsPlannerGitActionSafe(t *testing.T) {
	// Safe args
	if err := isPlannerGitActionSafe("commit", map[string]string{"message": "hello"}); err != nil {
		t.Fatalf("expected safe, got: %v", err)
	}

	// Unsafe absolute path
	err := isPlannerGitActionSafe("add", map[string]string{"paths": "/etc/passwd"})
	if err == nil {
		t.Fatal("expected absolute path to be blocked")
	}

	// Unsafe shell meta
	err = isPlannerGitActionSafe("tag", map[string]string{"name": "v1.0; rm -rf /"})
	if err == nil {
		t.Fatal("expected shell meta to be blocked")
	}
}

func TestSanitizeGitName(t *testing.T) {
	if _, err := sanitizeGitName("valid-branch_123"); err != nil {
		t.Fatalf("expected valid name, got: %v", err)
	}
	if _, err := sanitizeGitName("invalid;branch"); err == nil {
		t.Fatal("expected invalid name to be blocked")
	}
}
