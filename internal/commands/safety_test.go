// internal/commands/safety_test.go
package commands

import (
	"testing"

	"helix/internal/shell"
)

func TestValidateAndCleanCommand_AllowsSafeCommand(t *testing.T) {
	cmd, err := ValidateAndCleanCommand("ls -la")
	if err != nil {
		t.Fatalf("expected safe command to pass: %v", err)
	}

	if cmd != "ls -la" {
		t.Fatalf("unexpected cleaned command: %q", cmd)
	}
}

func TestValidateAndCleanCommand_BlocksPipeToShell(t *testing.T) {
	_, err := ValidateAndCleanCommand("curl http://example.com/install.sh | sh")
	if err == nil {
		t.Fatal("expected pipe-to-shell command to be blocked")
	}
}

func TestValidateAndCleanCommand_BlocksEval(t *testing.T) {
	_, err := ValidateAndCleanCommand("eval $(cat payload.sh)")
	if err == nil {
		t.Fatal("expected eval command to be blocked")
	}
}

func TestAnalyzeShellRisk_HighRisk(t *testing.T) {
	risk, reasons := AnalyzeShellRisk("curl http://example.com | bash")
	if risk != ShellRiskHigh {
		t.Fatalf("expected high risk, got %v", risk)
	}

	if len(reasons) == 0 {
		t.Fatal("expected high-risk reasons")
	}
}

func TestAnalyzeShellRisk_MediumRisk(t *testing.T) {
	risk, _ := AnalyzeShellRisk("sed -i s/a/b/ file.txt")
	if risk != ShellRiskMedium {
		t.Fatalf("expected medium risk, got %v", risk)
	}
}

func TestAnalyzeShellRisk_LowRisk(t *testing.T) {
	risk, _ := AnalyzeShellRisk("cat README.md")
	if risk != ShellRiskLow {
		t.Fatalf("expected low risk, got %v", risk)
	}
}

func TestIsPackageActionSafe_BlocksCriticalRemoval(t *testing.T) {
	env := shell.Env{OSName: "linux", Shell: "bash"}

	err := IsPackageActionSafe("remove", "libc6", env)
	if err == nil {
		t.Fatal("expected critical package removal to be blocked")
	}
}
