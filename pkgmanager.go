// pkgmanager.go
package main

import (
	"fmt"
	"os/exec"
	"strings"

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

func (a AptManager) CheckPackage(pkg string) (PackageInfo, error) {
	info := PackageInfo{Name: pkg}

	// Check if package is installed
	cmd := exec.Command("dpkg", "-l", pkg)
	output, err := cmd.Output()
	if err == nil && strings.Contains(string(output), pkg) {
		info.Installed = true
		// Extract version (simplified)
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

func (b BrewManager) CheckPackage(pkg string) (PackageInfo, error) {
	info := PackageInfo{Name: pkg}

	// Check if package is installed
	cmd := exec.Command("brew", "list", pkg)
	err := cmd.Run()
	info.Installed = (err == nil)

	if info.Installed {
		// Get version
		cmd := exec.Command("brew", "info", pkg)
		output, err := cmd.Output()
		if err == nil {
			lines := strings.Split(string(output), "\n")
			for _, line := range lines {
				if strings.Contains(line, pkg) && strings.Contains(line, "stable") {
					parts := strings.Fields(line)
					if len(parts) > 0 {
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

func (c ChocoManager) CheckPackage(pkg string) (PackageInfo, error) {
	info := PackageInfo{Name: pkg}

	cmd := exec.Command("choco", "list", "--local-only", pkg)
	output, err := cmd.Output()
	if err == nil && strings.Contains(string(output), pkg) {
		info.Installed = true
		// Extract version (simplified)
		lines := strings.Split(string(output), "\n")
		for _, line := range lines {
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

func (w WingetManager) CheckPackage(pkg string) (PackageInfo, error) {
	info := PackageInfo{Name: pkg}

	// Check if package is installed using winget
	cmd := exec.Command("winget", "list", "--name", pkg)
	output, err := cmd.Output()

	if err == nil {
		// Parse winget output to check if package is installed
		lines := strings.Split(string(output), "\n")
		for i, line := range lines {
			// Skip header lines
			if i < 2 {
				continue
			}
			if strings.Contains(strings.ToLower(line), strings.ToLower(pkg)) {
				info.Installed = true
				// Extract version from winget output
				parts := strings.Fields(line)
				if len(parts) >= 2 {
					// Version is typically the second field in winget list output
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

func (p PacmanManager) CheckPackage(pkg string) (PackageInfo, error) {
	info := PackageInfo{Name: pkg}

	// Check if package is installed using pacman
	cmd := exec.Command("pacman", "-Q", pkg)
	output, err := cmd.Output()

	if err == nil {
		info.Installed = true
		// Extract version from pacman output (format: "package version")
		parts := strings.Fields(string(output))
		if len(parts) >= 2 {
			info.Version = parts[1]
		}
	} else {
		// Package not installed, check if it exists in repositories
		cmd := exec.Command("pacman", "-Ss", fmt.Sprintf("^%s$", pkg))
		searchOutput, searchErr := cmd.Output()
		if searchErr == nil && strings.Contains(string(searchOutput), pkg) {
			// Package exists in repositories but not installed
			info.Installed = false
		}
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

// PackageManagerFactory creates the appropriate package manager handler
func PackageManagerFactory(env Env) PackageManagerHandler {
	pkgMgr := DetectPackageManager(env)

	switch pkgMgr.Name {
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

// CheckPackage checks if a package is installed and its status
func CheckPackage(pkg string, env Env) (PackageInfo, error) {
	pm := PackageManagerFactory(env)
	if pm == nil {
		return PackageInfo{Name: pkg}, fmt.Errorf("no supported package manager found")
	}

	return pm.CheckPackage(pkg)
}

// HandlePackageCommand processes package-related commands
func HandlePackageCommand(args []string, env Env, mockMode bool, execConfig ExecuteConfig) {
	if len(args) < 2 {
		color.Red("Usage: /install <package-name>")
		color.Yellow("Also available: /update <package-name>, /remove <package-name>")
		return
	}

	action := args[0]
	pkg := args[1]

	pm := PackageManagerFactory(env)
	if pm == nil {
		color.Red("❌ No supported package manager detected")
		color.Yellow("💡 Supported: apt, brew, choco, winget, pacman")
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
			color.Yellow("💡 Package not installed, nothing to remove.")
			return
		}
	}

	var command string
	switch action {
	case "install":
		command = pm.InstallCommand(pkg)
		color.Green("🚀 Installation command: %s", command)
	case "update":
		command = pm.UpdateCommand(pkg)
		color.Green("🔄 Update command: %s", command)
	case "remove":
		command = pm.RemoveCommand(pkg)
		color.Yellow("🗑️  Removal command: %s", command)
	default:
		color.Red("❌ Unknown package action: %s", action)
		color.Yellow("💡 Available actions: install, update, remove")
		return
	}

	if !mockMode {
		// For package managers that require admin privileges, warn the user
		if requiresSudo(pm.Name()) {
			color.Yellow("⚠️  This command may require administrator privileges")
		}

		if AskForConfirmation("Execute this command?") {
			err := ExecuteCommand(command, execConfig, env)
			if err != nil {
				color.Red("❌ Command failed: %v", err)
			} else {
				color.Green("✅ Command completed successfully!")
			}
		} else {
			color.Yellow("💡 Command cancelled. You can run it manually:")
			color.Cyan("  %s", command)
		}
	}
}

// requiresSudo checks if the package manager typically requires sudo
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

// GetPackageManagerCommands returns available commands for the detected package manager
func GetPackageManagerCommands(env Env) map[string]string {
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
