// internal/recon/engine.go
// Package recon provides a multi‑tool reconnaissance orchestrator.
// It parses natural‑language recon commands from the planner, runs
// tools like nmap/masscan/ffuf, and gathers structured results.

package recon

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"helix/internal/shell"
)

// ReconTarget represents a single target (IP, CIDR, URL).
type ReconTarget struct {
	Host  string
	Ports []int
}

// ReconResult holds the output of one reconnaissance command.
type ReconResult struct {
	Tool    string
	Target  string
	Raw     string
	Parsed  map[string]interface{} // structured data (e.g. open ports)
	Error   error
	Elapsed time.Duration
}

// ReconEngine manages the execution of reconnaissance tools.
type ReconEngine struct {
	env    shell.Env
	config ReconConfig
}

// ReconConfig configures the engine’s behaviour.
type ReconConfig struct {
	// Timeout per command.
	Timeout time.Duration
	// MaxParallel limits concurrent tool runs.
	MaxParallel int
	// Force allow dangerous flags (requires typed confirmation).
	AllowDangerous bool
	// Paths to tool binaries (can be overridden).
	NmapPath    string
	MasscanPath string
	FfufPath    string
	AmassPath   string
}

// DefaultReconConfig returns safe defaults.
func DefaultReconConfig() ReconConfig {
	return ReconConfig{
		Timeout:        5 * time.Minute,
		MaxParallel:    4,
		AllowDangerous: false,
		NmapPath:       "nmap",
		MasscanPath:    "masscan",
		FfufPath:       "ffuf",
		AmassPath:      "amass",
	}
}

// NewReconEngine creates a new engine.
func NewReconEngine(env shell.Env, cfg ReconConfig) *ReconEngine {
	return &ReconEngine{env: env, config: cfg}
}

// RunTool executes a single recon tool with the given arguments.
// tool is one of "nmap", "masscan", "ffuf", "amass".
// args are the command‑line arguments (e.g., "-sV", "192.168.1.1").
// On success the ReconResult.Error will be nil.
// If the tool is not found the error is still embedded in the result
// (rather than stopping the whole operation).
func (re *ReconEngine) RunTool(tool string, args ...string) (*ReconResult, error) {
	start := time.Now()
	cmdPath, err := re.getToolPath(tool)
	if err != nil {
		// Tool not found – return a result with the error inside,
		// not a hard error. This allows callers to handle it gracefully.
		return &ReconResult{
			Tool:    tool,
			Error:   err,
			Elapsed: time.Since(start),
		}, nil
	}

	// Basic safety: block obviously dangerous flags unless allowed.
	if !re.config.AllowDangerous {
		if err := re.checkArgs(tool, args); err != nil {
			return &ReconResult{Tool: tool, Error: err, Elapsed: time.Since(start)}, nil
		}
	}

	cmd := exec.Command(cmdPath, args...)
	cmd.Stdin = nil
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	cmd.Start()
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		if err != nil {
			return &ReconResult{
				Tool:    tool,
				Raw:     outBuf.String() + errBuf.String(),
				Error:   err,
				Elapsed: time.Since(start),
			}, nil
		}
	case <-time.After(re.config.Timeout):
		cmd.Process.Kill()
		return &ReconResult{
			Tool:    tool,
			Error:   fmt.Errorf("recon tool %s timed out after %v", tool, re.config.Timeout),
			Elapsed: time.Since(start),
		}, nil
	}

	rawOutput := outBuf.String() + errBuf.String()
	result := &ReconResult{
		Tool:    tool,
		Raw:     rawOutput,
		Elapsed: time.Since(start),
		Parsed:  parseOutput(tool, rawOutput),
	}
	return result, nil
}

// getToolPath returns the absolute path to the tool binary.
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
	// Verify that the binary exists.
	if _, err := exec.LookPath(path); err != nil {
		return "", fmt.Errorf("recon tool %q not found in PATH (looked for %s)", tool, path)
	}
	return path, nil
}

// checkArgs rejects obviously dangerous patterns in arguments.
func (re *ReconEngine) checkArgs(tool string, args []string) error {
	dangerous := map[string][]string{
		"nmap":    {"--script", "http-brute"}, // aggressive brute‑forcing
		"masscan": {"--rate", "1000000"},      // insane rate
		"ffuf":    {"-w", "rockyou.txt"},      // huge wordlist
	}
	if bad, exists := dangerous[tool]; exists {
		for _, arg := range args {
			for _, b := range bad {
				if strings.Contains(arg, b) {
					return fmt.Errorf("dangerous flag %q blocked for tool %s (override with AllowDangerous)", arg, tool)
				}
			}
		}
	}
	return nil
}

// parseOutput attempts to extract structured data from tool output.
// Currently supports basic nmap and masscan.
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
	// Simple regex to find open ports.
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

// SummarizeResults aggregates multiple ReconResults into a concise report.
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
