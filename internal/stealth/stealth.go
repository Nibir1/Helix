// internal/stealth/stealth.go
// Purpose: Local private-history execution policy.
//
// The executor no longer spawns processes itself: doing so hardcoded `sh`
// and bypassed the DirectorySandbox, so /sandbox strict kernel confinement
// was silently lost in stealth mode. It now only describes HOW a command
// must be run (history-suppressing environment, on-disk history policy);
// callers execute through the sandbox, which applies the real shell and
// confinement backends.
package stealth

// StealthConfig configures private execution behavior.
type StealthConfig struct {
	// PrivateHistory suppresses the child shell's own history files via
	// HISTFILE/HISTSIZE environment overrides.
	PrivateHistory bool
	// MemoryOnly keeps the input out of Helix's on-disk history file for the
	// session; in-memory history (ghost-text suggestions) is unaffected.
	MemoryOnly bool
}

// DefaultStealthConfig returns safe default settings.
func DefaultStealthConfig() StealthConfig {
	return StealthConfig{
		PrivateHistory: true,
		MemoryOnly:     true,
	}
}

// StealthExecutor describes private-history execution policy.
type StealthExecutor struct {
	config StealthConfig
}

// NewStealthExecutor creates a private-history policy provider.
func NewStealthExecutor(cfg StealthConfig) *StealthExecutor {
	return &StealthExecutor{config: cfg}
}

// Environment returns extra environment variables that suppress the child
// shell's history. Empty when PrivateHistory is disabled. Append these to
// os.Environ() when executing the command.
func (s *StealthExecutor) Environment() []string {
	if !s.config.PrivateHistory {
		return nil
	}
	return []string{
		"HISTFILE=/dev/null",
		"HISTSIZE=0",
		"HISTFILESIZE=0",
	}
}

// PersistsHistory reports whether Helix's own on-disk history file should be
// written while stealth mode is active: MemoryOnly keeps history in memory.
func (s *StealthExecutor) PersistsHistory() bool {
	return !s.config.MemoryOnly
}
