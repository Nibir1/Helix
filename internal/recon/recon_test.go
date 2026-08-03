// internal/recon/recon_test.go
package recon

import (
	"strings"
	"testing"

	"helix/internal/shell"
)

func TestNewReconEngine(t *testing.T) {
	env := shell.Env{OSName: "linux"}
	cfg := DefaultReconConfig()

	eng := NewReconEngine(env, cfg)
	if eng == nil {
		t.Fatal("expected non-nil engine")
	}
}

func TestReconRunTool_NonexistentTool(t *testing.T) {
	env := shell.Env{OSName: "linux"}

	cfg := DefaultReconConfig()
	cfg.RequireAuthorization = false

	eng := NewReconEngine(env, cfg)

	result, err := eng.RunTool("bogus")
	if err != nil {
		t.Fatalf("unexpected hard error: %v", err)
	}

	if result == nil {
		t.Fatal("expected non-nil result")
	}

	if result.Error == nil {
		t.Fatal("expected embedded error for nonexistent tool")
	}
}

func TestReconAuthorizationRequired(t *testing.T) {
	env := shell.Env{OSName: "linux"}
	cfg := DefaultReconConfig()

	eng := NewReconEngine(env, cfg)

	result, err := eng.RunTool("nmap", "127.0.0.1")
	if err != nil {
		t.Fatalf("unexpected hard error: %v", err)
	}

	if result.Error == nil {
		t.Fatal("expected unauthorized target error")
	}

	if !strings.Contains(result.Error.Error(), "not authorized") {
		t.Fatalf("expected authorization error, got: %v", result.Error)
	}
}

func TestReconAuthorizationAllowsScan(t *testing.T) {
	env := shell.Env{OSName: "linux"}
	cfg := DefaultReconConfig()

	eng := NewReconEngine(env, cfg)
	eng.AuthorizeTarget("127.0.0.1", "local test")

	result, err := eng.RunTool("nmap", "127.0.0.1")
	if err != nil {
		t.Fatalf("unexpected hard error: %v", err)
	}

	// Tool may be missing, but it must not be an authorization error.
	if result.Error != nil && strings.Contains(result.Error.Error(), "not authorized") {
		t.Fatalf("authorization should have passed: %v", result.Error)
	}
}

func TestExtractTarget(t *testing.T) {
	target := extractTarget([]string{"-sV", "--top-ports", "100", "192.168.1.1"})
	if target != "192.168.1.1" {
		t.Fatalf("unexpected target: %s", target)
	}
}

func TestParseNmapOutput(t *testing.T) {
	nmapOut := `
Nmap scan report for 127.0.0.1
PORT   STATE SERVICE
22/tcp open  ssh
80/tcp open  http
`

	parsed := parseNmapOutput(nmapOut)

	ports, ok := parsed["open_ports"].([]map[string]string)
	if !ok {
		t.Fatalf("expected open_ports in parsed map")
	}

	if len(ports) != 2 {
		t.Fatalf("expected 2 ports, got %d", len(ports))
	}

	if ports[0]["port"] != "22" {
		t.Errorf("unexpected first port: %v", ports[0])
	}
}

func TestParseMasscanOutput(t *testing.T) {
	massOut := `
Discovered open port 22/tcp on 192.168.1.1
Discovered open port 443/tcp on 192.168.1.1
`

	parsed := parseMasscanOutput(massOut)

	ports := parsed["open_ports"].([]map[string]string)
	if len(ports) != 2 {
		t.Fatalf("expected 2 ports, got %d", len(ports))
	}
}

func TestSummarizeResults(t *testing.T) {
	results := []*ReconResult{
		{
			Tool:   "nmap",
			Target: "localhost",
			Parsed: map[string]interface{}{
				"open_ports": []map[string]string{
					{"port": "22", "service": "ssh"},
				},
			},
		},
	}

	summary := SummarizeResults(results)
	if !strings.Contains(summary, "ssh") {
		t.Errorf("expected summary to contain 'ssh', got %s", summary)
	}
}
