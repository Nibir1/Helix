// internal/recon/engine.go
// Purpose: Authorized multi-tool reconnaissance orchestration.
// Phase 0 safety quarantine:
//   - recon requires explicit target authorization,
//   - unauthorized targets are rejected,
//   - dangerous flags remain blocked unless explicitly allowed.
package recon

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"helix/internal/shell"
)

// ReconResult holds the output of one reconnaissance command.
type ReconResult struct {
	Tool    string
	Target  string
	Raw     string
	Parsed  map[string]interface{}
	Error   error
	Elapsed time.Duration
}

// ReconConfig configures engine behavior.
type ReconConfig struct {
	Timeout        time.Duration
	MaxParallel    int
	AllowDangerous bool

	// RequireAuthorization enforces explicit target authorization.
	RequireAuthorization bool

	NmapPath    string
	MasscanPath string
	FfufPath    string
	AmassPath   string
}

// DefaultReconConfig returns safe defaults.
func DefaultReconConfig() ReconConfig {
	return ReconConfig{
		Timeout:              5 * time.Minute,
		MaxParallel:          4,
		AllowDangerous:       false,
		RequireAuthorization: true,
		NmapPath:             "nmap",
		MasscanPath:          "masscan",
		FfufPath:             "ffuf",
		AmassPath:            "amass",
	}
}

// ReconEngine executes reconnaissance tools.
type ReconEngine struct {
	env    shell.Env
	config ReconConfig

	authMu            sync.Mutex
	authorizedTargets map[string]string
}

// NewReconEngine creates a new engine.
func NewReconEngine(env shell.Env, cfg ReconConfig) *ReconEngine {
	return &ReconEngine{
		env:               env,
		config:            cfg,
		authorizedTargets: make(map[string]string),
	}
}

// AuthorizeTarget marks a target as explicitly authorized.
func (re *ReconEngine) AuthorizeTarget(target, reason string) {
	target = strings.TrimSpace(target)
	reason = strings.TrimSpace(reason)

	if target == "" {
		return
	}

	if reason == "" {
		reason = "manual authorization"
	}

	re.authMu.Lock()
	defer re.authMu.Unlock()

	re.authorizedTargets[target] = reason
}

// IsTargetAuthorized reports whether a target is authorized.
func (re *ReconEngine) IsTargetAuthorized(target string) bool {
	target = strings.TrimSpace(target)

	re.authMu.Lock()
	defer re.authMu.Unlock()

	_, ok := re.authorizedTargets[target]
	return ok
}

// AuthorizedTargets returns a copy of authorized targets.
func (re *ReconEngine) AuthorizedTargets() map[string]string {
	re.authMu.Lock()
	defer re.authMu.Unlock()

	out := make(map[string]string, len(re.authorizedTargets))
	for target, reason := range re.authorizedTargets {
		out[target] = reason
	}

	return out
}

// RunTool executes a recon tool.
func (re *ReconEngine) RunTool(tool string, args ...string) (*ReconResult, error) {
	start := time.Now()

	// Authorization gate.
	if re.config.RequireAuthorization {
		target := extractTarget(args)
		if target == "" {
			return &ReconResult{
				Tool:    tool,
				Error:   fmt.Errorf("recon target missing; authorize with /scan authorize <target> --reason \"...\""),
				Elapsed: time.Since(start),
			}, nil
		}

		if !re.IsTargetAuthorized(target) {
			return &ReconResult{
				Tool:    tool,
				Target:  target,
				Error:   fmt.Errorf("target %q is not authorized for reconnaissance", target),
				Elapsed: time.Since(start),
			}, nil
		}
	}

	cmdPath, err := re.getToolPath(tool)
	if err != nil {
		return &ReconResult{
			Tool:    tool,
			Error:   err,
			Elapsed: time.Since(start),
		}, nil
	}

	if !re.config.AllowDangerous {
		if err := re.checkArgs(tool, args); err != nil {
			return &ReconResult{
				Tool:    tool,
				Error:   err,
				Elapsed: time.Since(start),
			}, nil
		}
	}

	cmd := exec.Command(cmdPath, args...)
	cmd.Stdin = nil

	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	if err := cmd.Start(); err != nil {
		return &ReconResult{
			Tool:    tool,
			Error:   fmt.Errorf("failed to start %s: %w", tool, err),
			Elapsed: time.Since(start),
		}, nil
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		rawOutput := outBuf.String() + errBuf.String()

		if err != nil {
			return &ReconResult{
				Tool:    tool,
				Raw:     rawOutput,
				Error:   err,
				Elapsed: time.Since(start),
			}, nil
		}

		return &ReconResult{
			Tool:    tool,
			Raw:     rawOutput,
			Elapsed: time.Since(start),
			Parsed:  parseOutput(tool, rawOutput),
		}, nil

	case <-time.After(re.config.Timeout):
		_ = cmd.Process.Kill()

		return &ReconResult{
			Tool:    tool,
			Error:   fmt.Errorf("recon tool %s timed out after %v", tool, re.config.Timeout),
			Elapsed: time.Since(start),
		}, nil
	}
}

// extractTarget returns the last non-flag argument.
func extractTarget(args []string) string {
	for i := len(args) - 1; i >= 0; i-- {
		arg := strings.TrimSpace(args[i])
		if arg != "" && !strings.HasPrefix(arg, "-") {
			return arg
		}
	}

	return ""
}

// getToolPath resolves the tool binary.
func (re *ReconEngine) getToolPath(tool string) (string, error) {
	var path string

	switch tool {
	case "nmap":
		path = re.config.NmapPath
	case "masscan":
		path = re.config.MasscanPath
	case "ffuf":
		path = re.config.FfufPath
	case "amass":
		path = re.config.AmassPath
	default:
		return "", fmt.Errorf("unknown recon tool: %s", tool)
	}

	if _, err := exec.LookPath(path); err != nil {
		return "", fmt.Errorf("recon tool %q not found in PATH (looked for %s)", tool, path)
	}

	return path, nil
}

// checkArgs blocks obviously dangerous argument patterns.
func (re *ReconEngine) checkArgs(tool string, args []string) error {
	dangerous := map[string][]string{
		"nmap":    {"--script", "http-brute"},
		"masscan": {"--rate", "1000000"},
		"ffuf":    {"-w", "rockyou.txt"},
	}

	if bad, exists := dangerous[tool]; exists {
		for _, arg := range args {
			for _, b := range bad {
				if strings.Contains(arg, b) {
					return fmt.Errorf("dangerous flag %q blocked for tool %s", arg, tool)
				}
			}
		}
	}

	return nil
}

// parseOutput extracts structured data where possible.
func parseOutput(tool, raw string) map[string]interface{} {
	switch tool {
	case "nmap":
		return parseNmapOutput(raw)
	case "masscan":
		return parseMasscanOutput(raw)
	default:
		return map[string]interface{}{"raw": raw}
	}
}

func parseNmapOutput(out string) map[string]interface{} {
	res := map[string]interface{}{}

	re := regexp.MustCompile(`(\d+)/tcp\s+open\s+(\S+)`)
	matches := re.FindAllStringSubmatch(out, -1)

	var ports []map[string]string
	for _, m := range matches {
		ports = append(ports, map[string]string{
			"port":    m[1],
			"service": m[2],
		})
	}

	if len(ports) > 0 {
		res["open_ports"] = ports
	}

	return res
}

func parseMasscanOutput(out string) map[string]interface{} {
	res := map[string]interface{}{}

	re := regexp.MustCompile(`Discovered open port (\d+)/tcp on (\S+)`)
	matches := re.FindAllStringSubmatch(out, -1)

	var ports []map[string]string
	for _, m := range matches {
		ports = append(ports, map[string]string{
			"port": m[1],
			"ip":   m[2],
		})
	}

	if len(ports) > 0 {
		res["open_ports"] = ports
	}

	return res
}

// SummarizeResults aggregates multiple results into a report.
func SummarizeResults(results []*ReconResult) string {
	var b strings.Builder

	b.WriteString("=== Reconnaissance Summary ===\n")

	for _, r := range results {
		b.WriteString(fmt.Sprintf("[%s] %s (took %v)\n", r.Tool, r.Target, r.Elapsed))

		if r.Error != nil {
			b.WriteString(fmt.Sprintf("  Error: %v\n", r.Error))
			continue
		}

		if len(r.Parsed) > 0 {
			openPorts, ok := r.Parsed["open_ports"].([]map[string]string)
			if ok {
				b.WriteString("  Open ports:\n")
				for _, p := range openPorts {
					b.WriteString(fmt.Sprintf("    - %s/%s\n", p["port"], p["service"]))
				}
			}
		} else {
			b.WriteString("  (no structured results)\n")
		}
	}

	return b.String()
}
