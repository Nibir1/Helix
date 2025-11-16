package commands

import (
	"fmt"
	"os/exec"
	"strings"

	"helix/internal/shell"

	"github.com/fatih/color"
)

// PackageInfo holds information about a package
type PackageInfo struct {
	Name            string
	Installed       bool
	Version         string
	LatestVersion   string
	UpdateAvailable bool
}

// PackageManagerHandler interface for different package managers
type PackageManagerHandler interface {
	Name() string
	CheckPackage(pkg string) (PackageInfo, error)
	InstallCommand(pkg string) string
	UpdateCommand(pkg string) string
	RemoveCommand(pkg string) string
}

// Concrete implementations for different package managers

type AptManager struct{}
type BrewManager struct{}
type ChocoManager struct{}
type WingetManager struct{}
type PacmanManager struct{}

func (a AptManager) Name() string    { return "apt" }
func (b BrewManager) Name() string   { return "brew" }
func (c ChocoManager) Name() string  { return "choco" }
func (w WingetManager) Name() string { return "winget" }
func (p PacmanManager) Name() string { return "pacman" }

//
// ──────────────────────────────────────────────────────────────
// APT (Debian/Ubuntu)
// ──────────────────────────────────────────────────────────────
//

func (a AptManager) CheckPackage(pkg string) (PackageInfo, error) {
	info := PackageInfo{Name: pkg}

	cmd := exec.Command("dpkg", "-l", pkg)
	output, err := cmd.Output()
	if err == nil && strings.Contains(string(output), pkg) {
		info.Installed = true

		lines := strings.Split(string(output), "\n")
		for _, line := range lines {
			if strings.HasPrefix(line, "ii") && strings.Contains(line, pkg) {
				parts := strings.Fields(line)
				if len(parts) >= 3 {
					info.Version = parts[2]
				}
			}
		}
	}
	return info, nil
}

func (a AptManager) InstallCommand(pkg string) string {
	return fmt.Sprintf("sudo apt install %s", pkg)
}
func (a AptManager) UpdateCommand(pkg string) string {
	return fmt.Sprintf("sudo apt update && sudo apt upgrade %s", pkg)
}
func (a AptManager) RemoveCommand(pkg string) string {
	return fmt.Sprintf("sudo apt remove %s", pkg)
}

//
// ──────────────────────────────────────────────────────────────
// BREW (macOS)
// ──────────────────────────────────────────────────────────────
//

func (b BrewManager) CheckPackage(pkg string) (PackageInfo, error) {
	info := PackageInfo{Name: pkg}

	cmd := exec.Command("brew", "list", pkg)
	err := cmd.Run()
	info.Installed = (err == nil)

	if info.Installed {
		cmd := exec.Command("brew", "info", pkg)
		output, err := cmd.Output()
		if err == nil {
			for _, line := range strings.Split(string(output), "\n") {
				if strings.Contains(line, pkg) && strings.Contains(line, "stable") {
					parts := strings.Fields(line)
					if len(parts) > 1 {
						info.Version = parts[1]
					}
				}
			}
		}
	}
	return info, nil
}

func (b BrewManager) InstallCommand(pkg string) string {
	return fmt.Sprintf("brew install %s", pkg)
}
func (b BrewManager) UpdateCommand(pkg string) string {
	return fmt.Sprintf("brew upgrade %s", pkg)
}
func (b BrewManager) RemoveCommand(pkg string) string {
	return fmt.Sprintf("brew uninstall %s", pkg)
}

//
// ──────────────────────────────────────────────────────────────
// CHOCOLATEY (Windows)
// ──────────────────────────────────────────────────────────────
//

func (c ChocoManager) CheckPackage(pkg string) (PackageInfo, error) {
	info := PackageInfo{Name: pkg}

	cmd := exec.Command("choco", "list", "--local-only", pkg)
	output, err := cmd.Output()
	if err == nil && strings.Contains(string(output), pkg) {
		info.Installed = true

		for _, line := range strings.Split(string(output), "\n") {
			if strings.Contains(line, pkg) && strings.Count(line, " ") == 1 {
				parts := strings.Fields(line)
				if len(parts) == 2 {
					info.Version = parts[1]
				}
			}
		}
	}
	return info, nil
}

func (c ChocoManager) InstallCommand(pkg string) string {
	return fmt.Sprintf("choco install %s -y", pkg)
}
func (c ChocoManager) UpdateCommand(pkg string) string {
	return fmt.Sprintf("choco upgrade %s -y", pkg)
}
func (c ChocoManager) RemoveCommand(pkg string) string {
	return fmt.Sprintf("choco uninstall %s -y", pkg)
}

//
// ──────────────────────────────────────────────────────────────
// WINGET (Windows)
// ──────────────────────────────────────────────────────────────
//

func (w WingetManager) CheckPackage(pkg string) (PackageInfo, error) {
	info := PackageInfo{Name: pkg}

	cmd := exec.Command("winget", "list", "--name", pkg)
	output, err := cmd.Output()
	if err == nil {
		lines := strings.Split(string(output), "\n")

		for i, line := range lines {
			if i < 2 {
				continue
			}
			if strings.Contains(strings.ToLower(line), strings.ToLower(pkg)) {
				info.Installed = true

				parts := strings.Fields(line)
				if len(parts) >= 2 {
					info.Version = parts[1]
				}
				break
			}
		}
	}
	return info, nil
}

func (w WingetManager) InstallCommand(pkg string) string {
	return fmt.Sprintf("winget install %s", pkg)
}
func (w WingetManager) UpdateCommand(pkg string) string {
	return fmt.Sprintf("winget upgrade %s", pkg)
}
func (w WingetManager) RemoveCommand(pkg string) string {
	return fmt.Sprintf("winget uninstall %s", pkg)
}

//
// ──────────────────────────────────────────────────────────────
// PACMAN (Arch Linux)
// ──────────────────────────────────────────────────────────────
//

func (p PacmanManager) CheckPackage(pkg string) (PackageInfo, error) {
	info := PackageInfo{Name: pkg}

	cmd := exec.Command("pacman", "-Q", pkg)
	output, err := cmd.Output()

	if err == nil {
		info.Installed = true

		parts := strings.Fields(string(output))
		if len(parts) >= 2 {
			info.Version = parts[1]
		}
		return info, nil
	}

	cmd = exec.Command("pacman", "-Ss", fmt.Sprintf("^%s$", pkg))
	searchOutput, searchErr := cmd.Output()
	if searchErr == nil && strings.Contains(string(searchOutput), pkg) {
		info.Installed = false
	}
	return info, nil
}

func (p PacmanManager) InstallCommand(pkg string) string {
	return fmt.Sprintf("sudo pacman -S %s", pkg)
}
func (p PacmanManager) UpdateCommand(pkg string) string {
	return fmt.Sprintf("sudo pacman -Syu %s", pkg)
}
func (p PacmanManager) RemoveCommand(pkg string) string {
	return fmt.Sprintf("sudo pacman -R %s", pkg)
}

//
// ──────────────────────────────────────────────────────────────
// FACTORY
// ──────────────────────────────────────────────────────────────
//

func PackageManagerFactory(env shell.Env) PackageManagerHandler {
	pm := shell.DetectPackageManager(env)

	switch pm.Name {
	case "apt":
		return AptManager{}
	case "brew":
		return BrewManager{}
	case "choco":
		return ChocoManager{}
	case "winget":
		return WingetManager{}
	case "pacman":
		return PacmanManager{}
	default:
		return nil
	}
}

//
// ──────────────────────────────────────────────────────────────
// SAFETY LAYER — Package-Safety-v3
// ──────────────────────────────────────────────────────────────
//

// IsPackageActionSafe enforces OS-aware safety for planner & /install commands.
func IsPackageActionSafe(action, pkg string, env shell.Env) error {
	action = strings.ToLower(strings.TrimSpace(action))
	pkg = strings.TrimSpace(pkg)
	pkgLower := strings.ToLower(pkg)

	if action == "" {
		return fmt.Errorf("empty package action")
	}
	if pkg == "" {
		return fmt.Errorf("empty package name")
	}

	// No absolute paths
	if strings.HasPrefix(pkg, "/") {
		return fmt.Errorf("unsafe package name %q: absolute paths not allowed", pkg)
	}

	// No shell metacharacters
	if strings.ContainsAny(pkg, " *;&|><`$()") {
		return fmt.Errorf("unsafe package name %q: contains shell metacharacters", pkg)
	}

	// Prevent simple path traversal
	if strings.Contains(pkg, "..") {
		return fmt.Errorf("unsafe package name %q: contains '..'", pkg)
	}

	// OS-aware safety: prevent removing critical packages
	pm := PackageManagerFactory(env)
	pmName := ""
	if pm != nil {
		pmName = strings.ToLower(pm.Name())
	}

	if action == "remove" {
		critical := osAwareCriticalPackages(pmName)

		for _, c := range critical {
			if c == pkgLower {
				return fmt.Errorf("refusing to remove critical system package %q", pkg)
			}
			if strings.HasSuffix(c, "-*") {
				prefix := strings.TrimSuffix(c, "-*")
				if strings.HasPrefix(pkgLower, prefix+"-") {
					return fmt.Errorf("refusing to remove critical system package %q", pkg)
				}
			}
		}
	}

	return nil
}

func osAwareCriticalPackages(pmName string) []string {
	switch pmName {

	case "apt":
		return []string{
			"glibc", "libc6", "systemd",
			"linux", "linux-*",
			"bash", "zsh", "coreutils",
			"init", "util-linux",
		}

	case "pacman":
		return []string{
			"glibc", "linux", "linux-*",
			"systemd",
			"bash", "zsh", "coreutils",
		}

	case "brew":
		return []string{
			"bash", "zsh", "coreutils",
			"git", "curl", "openssl",
			"python", "python@3", "python3",
		}

	case "choco", "winget":
		return []string{
			"powershell",
			"git",
			"python", "python3",
			"dotnet", "dotnet-sdk",
		}
	}

	return []string{"glibc", "libc6", "systemd", "bash", "coreutils"}
}

//
// ──────────────────────────────────────────────────────────────
// EXECUTION
// ──────────────────────────────────────────────────────────────
//

func CheckPackage(pkg string, env shell.Env) (PackageInfo, error) {
	pm := PackageManagerFactory(env)
	if pm == nil {
		return PackageInfo{Name: pkg}, fmt.Errorf("no supported package manager found")
	}
	return pm.CheckPackage(pkg)
}

func HandlePackageCommand(args []string, env shell.Env, mockMode bool, execConfig ExecuteConfig) {
	if len(args) < 2 {
		color.Red("Usage: /install <package-name>")
		color.Yellow("Also available: /update <package-name>, /remove <package-name>")
		return
	}

	action := strings.ToLower(strings.TrimSpace(args[0]))
	pkg := strings.TrimSpace(args[1])

	pm := PackageManagerFactory(env)
	if pm == nil {
		color.Red("❌ No supported package manager detected")
		color.Yellow("💡 Supported: apt, brew, choco, winget, pacman")
		return
	}

	// SAFETY FIRST
	if err := IsPackageActionSafe(action, pkg, env); err != nil {
		color.Red("❌ Package action blocked: %v", err)
		return
	}

	color.Blue("📦 Package Manager: %s", pm.Name())
	color.Blue("🔍 Checking package: %s", pkg)

	info, err := pm.CheckPackage(pkg)
	if err != nil {
		color.Yellow("⚠️  Could not check package status: %v", err)
	}

	if info.Installed {
		color.Green("✅ %s is installed (v%s)", pkg, info.Version)

		if action == "install" {
			color.Yellow("💡 Package is already installed. Use '/update %s' to update.", pkg)
			return
		}
	} else {
		color.Yellow("📥 %s is not installed", pkg)

		if action == "update" {
			color.Yellow("💡 Package not installed. Use '/install %s' to install it first.", pkg)
			return
		}
		if action == "remove" {
			color.Yellow("💡 Package not installed — nothing to remove.")
			return
		}
	}

	var command string
	switch action {
	case "install":
		command = pm.InstallCommand(pkg)
	case "update":
		command = pm.UpdateCommand(pkg)
	case "remove":
		command = pm.RemoveCommand(pkg)
	default:
		color.Red("❌ Unknown package action: %s", action)
		return
	}

	color.Green("🚀 Command: %s", command)

	if !mockMode {
		if requiresSudo(pm.Name()) {
			color.Yellow("⚠️  This command may require administrator privileges")
		}

		if AskForConfirmation("Execute this command?") {
			if err := ExecuteCommand(command, execConfig, env); err != nil {
				color.Red("❌ Command failed: %v", err)
			} else {
				color.Green("✅ Command completed successfully!")
			}
		} else {
			color.Yellow("💡 Command cancelled. Run manually:")
			color.Cyan("  %s", command)
		}
	}
}

func requiresSudo(pmName string) bool {
	switch pmName {
	case "apt", "pacman":
		return true
	case "brew", "choco", "winget":
		return false
	default:
		return true
	}
}

func GetPackageManagerCommands(env shell.Env) map[string]string {
	pm := PackageManagerFactory(env)
	if pm == nil {
		return nil
	}

	return map[string]string{
		"install": pm.InstallCommand("{package}"),
		"update":  pm.UpdateCommand("{package}"),
		"remove":  pm.RemoveCommand("{package}"),
	}
}
