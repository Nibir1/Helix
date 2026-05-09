// internal/recon/recon_test.go
package recon

import (
	"helix/internal/shell"
	"strings"
	"testing"
)

func TestNewReconEngine(t *testing.T) {
	env := shell.Env{OSName: "linux"}
	cfg := DefaultReconConfig()
	eng := NewReconEngine(env, cfg)
	if eng == nil {
		t.Fatal("expected non‑nil engine")
	}
}

func TestReconRunTool_NonexistentTool(t *testing.T) {
	env := shell.Env{OSName: "linux"}
	cfg := DefaultReconConfig()
	eng := NewReconEngine(env, cfg)
	_, err := eng.RunTool("bogus")
	if err == nil {
		t.Fatal("expected error for nonexistent tool")
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
