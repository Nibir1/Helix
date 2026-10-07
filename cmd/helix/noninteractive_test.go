// cmd/helix/noninteractive_test.go
// Purpose: Tests for Helix non-interactive shell safety and exit-code handling.
package main

import (
	"errors"
	"testing"
)

// TestValidateNonInteractiveScriptAllowsLowRisk ensures low-risk commands pass.
func TestValidateNonInteractiveScriptAllowsLowRisk(t *testing.T) {
	if err := validateNonInteractiveScript("ls -la"); err != nil {
		t.Fatalf("expected low-risk command to pass, got: %v", err)
	}
}

// TestValidateNonInteractiveScriptBlocksHighRisk ensures destructive commands are blocked.
func TestValidateNonInteractiveScriptBlocksHighRisk(t *testing.T) {
	if err := validateNonInteractiveScript("rm -rf /"); err == nil {
		t.Fatal("expected high-risk command to be blocked")
	}
}

// TestValidateNonInteractiveScriptBlocksMediumRisk ensures medium-risk commands
// are blocked when HELIX_AUTOCONFIRM is not enabled.
func TestValidateNonInteractiveScriptBlocksMediumRisk(t *testing.T) {
	t.Setenv("HELIX_AUTOCONFIRM", "")

	if err := validateNonInteractiveScript("sed -i s/a/b/ file.txt"); err == nil {
		t.Fatal("expected medium-risk command to be blocked without HELIX_AUTOCONFIRM")
	}
}

// A command touching secret material needs a person to say yes, and a
// non-interactive run has none, so it is refused even with
// HELIX_AUTOCONFIRM=1.
func TestValidateNonInteractiveScriptRefusesSecrets(t *testing.T) {
	t.Setenv("HELIX_AUTOCONFIRM", "1")
	if err := validateNonInteractiveScript("cat ~/.ssh/id_rsa"); err == nil {
		t.Fatal("a secret was read non-interactively")
	}
	if err := validateNonInteractiveScript("sed -i s/a/b/ file.txt"); err != nil {
		t.Fatalf("HELIX_AUTOCONFIRM no longer allows an ordinary medium command: %v", err)
	}
}

// TestExitCodeGenericError ensures generic errors return exit code 1.
func TestExitCodeGenericError(t *testing.T) {
	if code := exitCode(errors.New("failure")); code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}

	if code := exitCode(nil); code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
}
