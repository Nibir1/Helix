// cmd/helix/daemon_cmd.go
// Purpose: BlackBox Phase 4 CLI surface:
//
//	helix daemon                 run the Living AI daemon (foreground; the
//	                             service manager supervises restarts)
//	helix remote <cmd> [args]    IPC client: status | say <text> | submit <text>
//	                             | voice | manual | logs | stop
//	helix daemon install         install the OS service (launchd/systemd/
//	                             Windows instructions) with explicit consent
//	helix daemon uninstall       remove the service
//	helix daemon status          is a daemon running?
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"helix/internal/daemon"

	"github.com/fatih/color"
)

// runDaemon implements `helix daemon` and `helix remote ...` and
// `helix daemon <install|uninstall|status>`. Returns (handled, exitCode).
func runDaemonCommand(args []string) (bool, int) {
	if len(args) == 0 {
		return false, 0
	}
	switch args[0] {
	case "daemon":
		if len(args) > 1 {
			switch args[1] {
			case "install":
				installDaemonService()
				return true, 0
			case "uninstall":
				uninstallDaemonService()
				return true, 0
			case "status":
				daemonStatusCLI()
				return true, 0
			}
		}
		runDaemonProcess()
		return true, 0
	case "remote":
		code := runRemoteClient(args[1:])
		return true, code
	}
	return false, 0
}

// runDaemonProcess boots the daemon in the foreground.
func runDaemonProcess() {
	d, err := daemon.New()
	if err != nil {
		color.Red("daemon start failed: %v", err)
		os.Exit(1)
	}
	color.Cyan("Helix daemon listening on %s (Ctrl+C to stop)", d.Addr())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := d.Run(ctx); err != nil {
		color.Red("daemon ended: %v", err)
		os.Exit(1)
	}
}

// runRemoteClient implements `helix remote ...`; returns an exit code.
func runRemoteClient(args []string) int {
	if len(args) == 0 {
		fmt.Println(`Usage: helix remote <status|say|submit|voice|manual|logs|stop> [args]
  status            daemon health, chains, recorder
  say <text>        speak text through the daemon's TTS chain
  submit <text>     run a text-channel input through the pipeline
  voice | manual    switch the daemon's interaction mode
  logs              tail the last 20 journal entries
  stop              gracefully stop the daemon`)
		return 2
	}

	var req daemon.Request
	switch args[0] {
	case "status":
		req = daemon.Request{Type: daemon.TypeStatus}
	case "say":
		if len(args) < 2 {
			color.Red("say needs text")
			return 2
		}
		req = daemon.Request{Type: daemon.TypeSay, Text: strings.Join(args[1:], " ")}
	case "submit":
		if len(args) < 2 {
			color.Red("submit needs text")
			return 2
		}
		req = daemon.Request{Type: daemon.TypeSubmit, Text: strings.Join(args[1:], " ")}
	case "voice":
		req = daemon.Request{Type: daemon.TypeMode, Text: "voice"}
	case "manual":
		req = daemon.Request{Type: daemon.TypeMode, Text: "manual"}
	case "logs":
		req = daemon.Request{Type: daemon.TypeLogTail}
	case "stop":
		req = daemon.Request{Type: daemon.TypeStop}
	default:
		color.Red("unknown remote command %q", args[0])
		return 2
	}

	conn, err := daemon.Dial()
	if err != nil {
		color.Red("daemon unreachable: %v (start it with `helix daemon`)", err)
		return 1
	}
	defer func() { _ = conn.Close() }()

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		color.Red("send failed: %v", err)
		return 1
	}

	reader := bufio.NewReader(conn)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		color.Red("no response: %v", err)
		return 1
	}
	var resp daemon.Response
	if err := json.Unmarshal(line, &resp); err != nil {
		color.Red("malformed response: %v", err)
		return 1
	}

	if !resp.OK {
		color.Red("daemon: %s", resp.Error)
		return 1
	}
	printRemoteResponse(resp)
	return 0
}

func printRemoteResponse(resp daemon.Response) {
	if len(resp.State) > 0 {
		keys := make([]string, 0, len(resp.State))
		for k := range resp.State {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Printf("%-14s %v\n", k, resp.State[k])
		}
		return
	}
	if resp.Meta != nil {
		data, _ := json.MarshalIndent(resp.Meta, "", "  ")
		fmt.Println(string(data))
		return
	}
	fmt.Println("ok")
}

// daemonRunning reports whether a daemon answers on the IPC transport
// (bounded, non-fatal probe — reused by /doctor).
func daemonRunning() bool {
	conn, err := daemon.Dial()
	if err != nil {
		return false
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	_ = json.NewEncoder(conn).Encode(daemon.Request{Type: daemon.TypeStatus})
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return false
	}
	var resp daemon.Response
	return json.Unmarshal(line, &resp) == nil && resp.OK
}

// daemonStatusCLI reports whether a daemon answers on the IPC transport.
func daemonStatusCLI() {
	conn, err := daemon.Dial()
	if err != nil {
		fmt.Println("daemon: not running")
		return
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	_ = json.NewEncoder(conn).Encode(daemon.Request{Type: daemon.TypeStatus})
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		fmt.Println("daemon: socket present but unresponsive")
		return
	}
	var resp daemon.Response
	if err := json.Unmarshal(line, &resp); err == nil && resp.OK {
		fmt.Println("daemon: running")
		printRemoteResponse(resp)
	}
}

// --- Service installers (4D) -------------------------------------------

const serviceLabel = "com.helix.daemon"

func helixBinaryPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(exe)
	if err != nil {
		return "", err
	}
	return abs, nil
}

// installDaemonService writes the platform service definition. Every
// platform requires explicit user consent here (no silent boot entries).
func installDaemonService() {
	exe, err := helixBinaryPath()
	if err != nil {
		color.Red("resolve helix path: %v", err)
		return
	}

	switch runtime.GOOS {
	case "darwin":
		plistDir := filepath.Join(homeDirOrEmpty(), "Library", "LaunchAgents")
		plist := filepath.Join(plistDir, serviceLabel+".plist")
		if err := os.MkdirAll(plistDir, 0o755); err != nil {
			color.Red("mkdir LaunchAgents: %v", err)
			return
		}
		content := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key><string>%s</string>
    <key>ProgramArguments</key>
    <array><string>%s</string><string>daemon</string></array>
    <key>RunAtLoad</key><true/>
    <key>KeepAlive</key><true/>
    <key>StandardOutPath</key><string>%s/.helix/daemon.log</string>
    <key>StandardErrorPath</key><string>%s/.helix/daemon.log</string>
</dict>
</plist>
`, serviceLabel, exe, homeDirOrEmpty(), homeDirOrEmpty())
		if err := os.WriteFile(plist, []byte(content), 0o644); err != nil {
			color.Red("write plist: %v", err)
			return
		}
		color.Green("Installed %s", plist)
		fmt.Println("Start now:   launchctl load " + plist)
		fmt.Println("Stop:        launchctl unload " + plist)
		fmt.Println("The daemon auto-starts at login (RunAtLoad) and restarts on crash (KeepAlive).")

	case "linux":
		unitDir := filepath.Join(homeDirOrEmpty(), ".config", "systemd", "user")
		unit := filepath.Join(unitDir, "helix-daemon.service")
		if err := os.MkdirAll(unitDir, 0o755); err != nil {
			color.Red("mkdir systemd user dir: %v", err)
			return
		}
		content := fmt.Sprintf(`[Unit]
Description=Helix BlackBox daemon
After=network-online.target

[Service]
ExecStart=%s daemon
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
`, exe)
		if err := os.WriteFile(unit, []byte(content), 0o644); err != nil {
			color.Red("write unit: %v", err)
			return
		}
		color.Green("Installed %s", unit)
		fmt.Println("Enable now:  systemctl --user daemon-reload && systemctl --user enable --now helix-daemon")

	default: // windows
		fmt.Println("Windows service installation (run in an elevated prompt):")
		fmt.Printf("  sc create HelixDaemon binPath= \"%s daemon\" start= auto\n", exe)
		fmt.Println("  sc start HelixDaemon")
		fmt.Println("Remove with: sc delete HelixDaemon")
	}
}

func uninstallDaemonService() {
	switch runtime.GOOS {
	case "darwin":
		plist := filepath.Join(homeDirOrEmpty(), "Library", "LaunchAgents", serviceLabel+".plist")
		_ = exec.Command("launchctl", "unload", plist).Run()
		if err := os.Remove(plist); err != nil {
			color.Yellow("remove plist: %v", err)
			return
		}
		color.Green("Removed %s", plist)
	case "linux":
		_ = exec.Command("systemctl", "--user", "disable", "--now", "helix-daemon").Run()
		unit := filepath.Join(homeDirOrEmpty(), ".config", "systemd", "user", "helix-daemon.service")
		if err := os.Remove(unit); err != nil {
			color.Yellow("remove unit: %v", err)
			return
		}
		color.Green("Removed %s (run: systemctl --user daemon-reload)", unit)
	default:
		fmt.Println("Run in an elevated prompt: sc delete HelixDaemon")
	}
}

func homeDirOrEmpty() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}
