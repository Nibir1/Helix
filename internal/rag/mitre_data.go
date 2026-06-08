// internal/rag/mitre_data.go
package rag

// loadMitreTechniques returns a curated subset of MITRE ATT&CK techniques
// relevant to CLI-based red team operations.
func loadMitreTechniques() []MitreTechnique {
	return []MitreTechnique{
		{
			ID:          "T1059",
			Name:        "Command and Scripting Interpreter",
			Description: "Adversaries may abuse command and script interpreters to execute commands, scripts, or binaries.",
			Detection:   "Monitor command-line arguments, script execution logs, and process creation events. Look for unusual parent-child process relationships.",
			Alternatives: []string{
				"Use signed binaries (LOLBAS) instead of raw interpreter calls",
				"Execute in memory via reflective loading",
				"Obfuscate command line arguments",
			},
		},
		{
			ID:          "T1059.001",
			Name:        "PowerShell",
			Description: "Adversaries may abuse PowerShell commands and scripts for execution.",
			Detection:   "Enable PowerShell logging (Module, ScriptBlock, Transcription). Monitor for encoded commands and suspicious .NET assembly loads.",
			Alternatives: []string{
				"Use PowerShell without powershell.exe (e.g., unmanaged PowerShell)",
				"Employ COM scripting (WScript, CScript) for similar tasks",
				"Use .NET reflection to load assemblies directly",
			},
		},
		{
			ID:          "T1059.003",
			Name:        "Windows Command Shell",
			Description: "Adversaries may abuse the Windows command shell (cmd) to execute commands.",
			Detection:   "Monitor for cmd.exe spawning unusual child processes. Check command-line arguments for obfuscation.",
			Alternatives: []string{
				"Use WMI for execution instead of cmd",
				"Execute via scheduled tasks with minimal footprint",
				"Run as a service to blend in with legitimate processes",
			},
		},
		{
			ID:          "T1543.003",
			Name:        "Windows Service",
			Description: "Adversaries may create or modify Windows services to repeatedly execute malicious payloads.",
			Detection:   "Monitor for new service creation or modification of existing services. Check for unusual binary paths.",
			Alternatives: []string{
				"Use WMI event subscriptions for persistence (quieter)",
				"Leverage scheduled tasks instead of services",
				"Hijack a legitimate but unused service",
			},
		},
		{
			ID:          "T1082",
			Name:        "System Information Discovery",
			Description: "Adversaries may attempt to get detailed information about the operating system and hardware.",
			Detection:   "Look for enumeration commands like 'systeminfo', 'wmic', 'Get-ComputerInfo'. Volume of such commands may indicate recon.",
			Alternatives: []string{
				"Use WMI queries that blend with normal administrative activity",
				"Retrieve info via LDAP or remote registry to avoid local logs",
				"Spread recon across multiple low-and-slow queries",
			},
		},
		{
			ID:          "T1070.001",
			Name:        "Indicator Removal on Host: Clear Windows Event Logs",
			Description: "Adversaries may clear Windows Event Logs to hide the activity of an intrusion.",
			Detection:   "Monitor for EventLog clearing events (Event ID 1102). Also check for suspicious usage of 'wevtutil' or 'Clear-EventLog'.",
			Alternatives: []string{
				"Delete individual log entries instead of clearing entire logs",
				"Use log tampering techniques that avoid the clearing event",
				"Redirect logging to a null output during the operation",
			},
		},
		{
			ID:          "T1090",
			Name:        "Proxy",
			Description: "Adversaries may use a connection proxy to direct network traffic between systems or act as an intermediary for network communications.",
			Detection:   "Monitor for unexpected outbound connections, especially to non-standard ports. Look for proxy configuration changes.",
			Alternatives: []string{
				"Use protocol tunneling (DNS, HTTPS) to blend with legitimate traffic",
				"Pivot through trusted internal hosts to avoid direct external connections",
				"Utilise existing legitimate proxies already in use",
			},
		},
	}
}
