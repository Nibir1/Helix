package rag

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"helix/internal/shell"

	"github.com/fatih/color"
)

// MANPage represents a processed manual page
type MANPage struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Synopsis    string   `json:"synopsis"`
	Options     []string `json:"options"`
	Examples    []string `json:"examples"`
	FullText    string   `json:"full_text"`
	Category    string   `json:"category"`
	Path        string   `json:"path"`
}

// MANIndexer handles scanning and processing MAN pages
type MANIndexer struct {
	env        shell.Env
	indexDir   string
	indexed    map[string]MANPage
	mu         sync.RWMutex
	categories []string
}

// NewMANIndexer creates a new MAN page indexer
func NewMANIndexer(env shell.Env) *MANIndexer {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = "/tmp"
	}

	indexDir := filepath.Join(homeDir, ".helix", "man_index")

	return &MANIndexer{
		env:        env,
		indexDir:   indexDir,
		indexed:    make(map[string]MANPage),
		categories: []string{"1", "2", "3", "4", "5", "6", "7", "8"},
	}
}

// IndexAvailableManPages scans and indexes all available MAN pages
func (mi *MANIndexer) IndexAvailableManPages() error {
	color.Blue("📚 Scanning for MAN pages...")

	if err := mi.ensureIndexDir(); err != nil {
		return fmt.Errorf("failed to create index directory: %w", err)
	}

	// Get MAN path
	manPath := mi.getMANPath()
	color.Cyan("🔍 MAN path: %s", manPath)

	var wg sync.WaitGroup
	pageChan := make(chan string, 100)
	resultChan := make(chan MANPage, 100)

	// Start workers to process MAN pages
	workerCount := 6
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go mi.manPageWorker(&wg, pageChan, resultChan)
	}

	// Collect results
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// Find MAN pages
	go mi.findMANPages(manPath, pageChan)

	// Process results
	processed := 0
	for page := range resultChan {
		mi.mu.Lock()
		mi.indexed[page.Name] = page
		mi.mu.Unlock()
		processed++

		if processed%50 == 0 {
			color.Green("✅ Indexed %d MAN pages...", processed)
		}
	}

	color.Green("🎉 MAN page indexing completed! Indexed %d pages", processed)
	return mi.saveIndex()
}

// Enhanced findMANPages with useful command tracking
func (mi *MANIndexer) findMANPages(manPath string, pageChan chan<- string) {
	defer close(pageChan)

	color.Cyan("🔍 Using smart MAN page discovery...")

	totalFound := 0

	// Try multiple methods
	methods := []func(chan<- string) int{
		mi.tryManKEnhanced,  // Enhanced man -k
		mi.tryDirectoryScan, // Direct directory scanning
	}

	for i, method := range methods {
		color.Cyan("Trying method %d...", i+1)
		count := method(pageChan)
		if count > 0 {
			totalFound += count
			color.Green("✅ Method %d found %d total commands", i+1, count)
		} else {
			color.Yellow("⚠️  Method %d found 0 commands", i+1)
		}
	}

	if totalFound == 0 {
		color.Red("❌ No MAN pages found using any method")
		color.Yellow("💡 MAN pages might not be installed or paths are incorrect")
	} else {
		color.Green("🎉 Found %d total commands, filtering for useful ones...", totalFound)
	}
}

// Enhanced directory scanner with filtering
func (mi *MANIndexer) scanMANCategoryEnhanced(categoryPath string, ch chan<- string, seen map[string]bool) int {
	entries, err := os.ReadDir(categoryPath)
	if err != nil {
		return 0
	}

	count := 0
	usefulCount := 0

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		// Handle various file formats: ls.1, ls.1.gz, ls.1.bz2, etc.
		if strings.Contains(name, ".") {
			command := strings.Split(name, ".")[0]
			if !seen[command] && len(command) > 1 {
				seen[command] = true
				count++

				// Only send useful commands
				if mi.isUsefulCommand(command) {
					ch <- command
					usefulCount++
				}
			}
		}
	}

	if usefulCount > 0 {
		color.Cyan("  %s: %d useful commands", filepath.Base(categoryPath), usefulCount)
	}

	return count
}

// Enhanced man -k method
func (mi *MANIndexer) tryManKEnhanced(ch chan<- string) int {
	cmd := exec.Command("man", "-k", ".")
	output, err := cmd.Output()
	if err != nil {
		return 0
	}

	count := 0
	lines := strings.Split(string(output), "\n")
	seen := make(map[string]bool)

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Extract command name from formats like:
		// "ls(1) - list directory contents"
		// "git-ls-files(1) - Show information about files"
		parts := strings.Fields(line)
		if len(parts) > 0 {
			command := parts[0]

			// Remove section numbers and parentheses
			command = strings.TrimSuffix(command, "(")
			command = strings.Split(command, "(")[0]

			// Remove git- prefix from git commands
			command = strings.TrimPrefix(command, "git-")

			if !seen[command] && len(command) > 1 {
				seen[command] = true
				ch <- command
				count++
			}
		}
	}

	return count
}

// Direct directory scanning
func (mi *MANIndexer) tryDirectoryScan(ch chan<- string) int {
	manPath := mi.getMANPath()
	paths := strings.Split(manPath, ":")
	seen := make(map[string]bool)
	count := 0

	for _, path := range paths {
		for _, category := range mi.categories {
			categoryPath := filepath.Join(path, "man"+category)
			count += mi.scanMANCategoryEnhanced(categoryPath, ch, seen)
		}
	}

	return count
}

// manPageWorker processes individual MAN pages with filtering
func (mi *MANIndexer) manPageWorker(wg *sync.WaitGroup, pageChan <-chan string, resultChan chan<- MANPage) {
	defer wg.Done()

	for command := range pageChan {
		// FILTER: Only process useful commands
		if !mi.isUsefulCommand(command) {
			continue
		}

		page, err := mi.processMANPage(command)
		if err != nil {
			continue // Skip pages that can't be processed
		}
		resultChan <- page
	}
}

// processMANPage extracts information from a single MAN page
func (mi *MANIndexer) processMANPage(command string) (MANPage, error) {
	// Get raw MAN page content
	cmd := exec.Command("man", command)
	output, err := cmd.Output()
	if err != nil {
		return MANPage{}, fmt.Errorf("failed to get MAN page for %s: %w", command, err)
	}

	content := string(output)
	return mi.parseMANContent(command, content), nil
}

// parseMANContent extracts structured information from MAN page content
func (mi *MANIndexer) parseMANContent(command, content string) MANPage {
	page := MANPage{
		Name:     command,
		FullText: content,
	}

	lines := strings.Split(content, "\n")
	var currentSection string
	var sectionContent strings.Builder

	for _, line := range lines {
		line = strings.TrimSpace(line)

		// Detect section headers
		if strings.ToUpper(line) == line && len(line) > 0 && !strings.Contains(line, " ") {
			// Save previous section
			mi.processSection(currentSection, sectionContent.String(), &page)

			// Start new section
			currentSection = line
			sectionContent.Reset()
			continue
		}

		sectionContent.WriteString(line + "\n")
	}

	// Process the last section
	mi.processSection(currentSection, sectionContent.String(), &page)

	// Clean up description
	if page.Description == "" {
		page.Description = mi.extractDescription(content)
	}

	return page
}

// processSection processes a specific MAN page section
func (mi *MANIndexer) processSection(section, content string, page *MANPage) {
	switch strings.ToUpper(section) {
	case "NAME":
		page.Description = mi.extractNameDescription(content)
	case "SYNOPSIS":
		page.Synopsis = mi.cleanSynopsis(content)
	case "DESCRIPTION":
		if page.Description == "" {
			page.Description = mi.extractFirstParagraph(content)
		}
	case "OPTIONS":
		page.Options = mi.extractOptions(content)
	case "EXAMPLES":
		page.Examples = mi.extractExamples(content)
	}
}

// extractNameDescription extracts description from NAME section
func (mi *MANIndexer) extractNameDescription(content string) string {
	// Format: "command - description"
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		if strings.Contains(line, " - ") {
			parts := strings.SplitN(line, " - ", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1])
			}
		}
	}
	return mi.extractFirstParagraph(content)
}

// extractFirstParagraph extracts the first meaningful paragraph
func (mi *MANIndexer) extractFirstParagraph(content string) string {
	lines := strings.Split(content, "\n")
	var paragraph strings.Builder

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			if paragraph.Len() > 0 {
				break
			}
			continue
		}
		if paragraph.Len() > 0 {
			paragraph.WriteString(" ")
		}
		paragraph.WriteString(line)
	}

	result := paragraph.String()
	if len(result) > 200 {
		result = result[:200] + "..."
	}
	return result
}

// cleanSynopsis cleans the synopsis section
func (mi *MANIndexer) cleanSynopsis(content string) string {
	// Remove excessive whitespace
	space := regexp.MustCompile(`\s+`)
	content = space.ReplaceAllString(content, " ")

	// Take first line or truncate
	lines := strings.Split(content, "\n")
	if len(lines) > 0 {
		synopsis := strings.TrimSpace(lines[0])
		if len(synopsis) > 150 {
			synopsis = synopsis[:150] + "..."
		}
		return synopsis
	}
	return content
}

// extractOptions extracts command options
func (mi *MANIndexer) extractOptions(content string) []string {
	var options []string
	lines := strings.Split(content, "\n")

	optionPattern := regexp.MustCompile(`^\s*[-]{1,2}[a-zA-Z0-9]`)

	for _, line := range lines {
		if optionPattern.MatchString(line) {
			option := strings.TrimSpace(line)
			if len(option) > 0 && len(option) < 100 {
				options = append(options, option)
			}
		}
	}

	if len(options) > 10 {
		return options[:10] // Limit to top 10 options
	}
	return options
}

// extractExamples extracts usage examples
func (mi *MANIndexer) extractExamples(content string) []string {
	var examples []string
	lines := strings.Split(content, "\n")

	examplePattern := regexp.MustCompile(`^\s*(?:\$|#|>)`)
	var currentExample strings.Builder

	for _, line := range lines {
		if examplePattern.MatchString(line) {
			if currentExample.Len() > 0 {
				examples = append(examples, currentExample.String())
				currentExample.Reset()
			}
			currentExample.WriteString(strings.TrimSpace(line))
		} else if currentExample.Len() > 0 && strings.TrimSpace(line) != "" {
			currentExample.WriteString(" " + strings.TrimSpace(line))
		}
	}

	if currentExample.Len() > 0 {
		examples = append(examples, currentExample.String())
	}

	if len(examples) > 5 {
		return examples[:5] // Limit to 5 examples
	}
	return examples
}

// extractDescription fallback description extraction
func (mi *MANIndexer) extractDescription(content string) string {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if len(line) > 20 && len(line) < 200 && !strings.HasPrefix(line, ".") {
			return line
		}
	}
	return "No description available"
}

// getMANPath gets the MAN path from environment or default
func (mi *MANIndexer) getMANPath() string {
	if manPath := os.Getenv("MANPATH"); manPath != "" {
		return manPath
	}

	// Better macOS MAN path detection
	if mi.env.OSName == "darwin" {
		// Common macOS MAN paths
		possiblePaths := []string{
			"/usr/share/man",
			"/usr/local/share/man",
			"/Library/Developer/CommandLineTools/SDKs/MacOSX.sdk/usr/share/man",
			"/Library/Developer/CommandLineTools/Toolchains/XcodeDefault.xctoolchain/usr/share/man",
			"/Library/Developer/CommandLineTools/usr/share/man",
			"/Applications/Xcode.app/Contents/Developer/usr/share/man",
		}

		var validPaths []string
		for _, path := range possiblePaths {
			if _, err := os.Stat(path); err == nil {
				validPaths = append(validPaths, path)
			}
		}

		if len(validPaths) > 0 {
			return strings.Join(validPaths, ":")
		}
	}

	// Fallback
	return "/usr/share/man:/usr/local/share/man"
}

// ensureIndexDir creates the index directory
func (mi *MANIndexer) ensureIndexDir() error {
	return os.MkdirAll(mi.indexDir, 0755)
}

// saveIndex saves the index to disk
func (mi *MANIndexer) saveIndex() error {
	// This will be implemented in the vector store
	// For now, we just keep in memory
	color.Green("💾 MAN page index ready (%d pages)", len(mi.indexed))
	return nil
}

// GetIndexedCount returns the number of indexed pages
func (mi *MANIndexer) GetIndexedCount() int {
	mi.mu.RLock()
	defer mi.mu.RUnlock()
	return len(mi.indexed)
}

// GetPage retrieves a MAN page by name
func (mi *MANIndexer) GetPage(name string) (MANPage, bool) {
	mi.mu.RLock()
	defer mi.mu.RUnlock()
	page, exists := mi.indexed[name]
	return page, exists
}

// SearchPages searches for MAN pages by query
func (mi *MANIndexer) SearchPages(query string) []MANPage {
	mi.mu.RLock()
	defer mi.mu.RUnlock()

	var results []MANPage
	query = strings.ToLower(query)

	for _, page := range mi.indexed {
		if strings.Contains(strings.ToLower(page.Name), query) ||
			strings.Contains(strings.ToLower(page.Description), query) ||
			strings.Contains(strings.ToLower(page.FullText), query) {
			results = append(results, page)
		}
	}

	return results
}

// Add this debug function to manindexer.go
func (mi *MANIndexer) DebugMANDiscovery() {
	color.Cyan("🔍 DEBUG: Testing MAN page discovery methods...")

	// Test MAN path detection
	manPath := mi.getMANPath()
	color.Cyan("MAN Path detected: %s", manPath)

	// Test each discovery method
	color.Cyan("Testing 'man -k' method...")
	cmd := exec.Command("man", "-k", ".")
	output, err := cmd.Output()
	if err != nil {
		color.Red("❌ 'man -k' failed: %v", err)
	} else {
		lines := strings.Split(strings.TrimSpace(string(output)), "\n")
		color.Green("✅ 'man -k' found %d entries", len(lines))
		if len(lines) > 0 {
			for i := 0; i < min(3, len(lines)); i++ {
				color.Cyan("  Sample %d: %s", i+1, lines[i])
			}
		}
	}

	// Test directory scanning
	color.Cyan("Testing directory scanning...")
	paths := strings.Split(manPath, ":")
	totalFiles := 0
	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			color.Green("✅ MAN directory exists: %s", path)
			// Count files in this directory
			count := mi.countFilesInPath(path)
			color.Cyan("  Contains ~%d files", count)
			totalFiles += count
		} else {
			color.Red("❌ MAN directory missing: %s", path)
		}
	}
	color.Cyan("Total estimated MAN files: %d", totalFiles)
}

func (mi *MANIndexer) countFilesInPath(path string) int {
	count := 0
	for _, category := range mi.categories {
		categoryPath := filepath.Join(path, "man"+category)
		entries, err := os.ReadDir(categoryPath)
		if err == nil {
			count += len(entries)
		}
	}
	return count
}

// Common commands that users actually need - EXPANDED LIST
var commonCommands = []string{
	// File operations - EXPANDED
	"ls", "cd", "pwd", "cp", "mv", "rm", "mkdir", "rmdir", "touch", "cat", "more", "less", "head", "tail",
	"find", "locate", "which", "whereis", "file", "stat", "du", "df", "mount", "umount", "chmod", "chown", "chgrp",
	"ln", "readlink", "realpath", "basename", "dirname", "pathchk", "mktemp",

	// Text processing - EXPANDED
	"grep", "egrep", "fgrep", "awk", "sed", "cut", "paste", "sort", "uniq", "wc", "tr", "tee", "column", "expand",
	"unexpand", "fmt", "pr", "nl", "fold", "join", "split", "csplit", "tac", "rev", "comm", "diff", "patch",

	// System monitoring - EXPANDED
	"ps", "top", "htop", "kill", "pkill", "killall", "jobs", "bg", "fg", "nice", "renice",
	"free", "vmstat", "iostat", "mpstat", "sar", "lsof", "netstat", "ss", "uptime", "w", "who", "last",
	"dmesg", "journalctl", "sysctl", "uname", "hostname", "domainname", "dnsdomainname", "nisdomainname", "ypdomainname",

	// Network - EXPANDED
	"ping", "traceroute", "tracepath", "curl", "wget", "ssh", "scp", "rsync", "ftp", "sftp",
	"ifconfig", "ip", "route", "arp", "hostname", "dig", "nslookup", "whois", "host", "nmap", "nc", "netcat",
	"telnet", "openssl", "ssh-keygen", "ssh-copy-id", "ssh-add", "ssh-agent",

	// Package management - EXPANDED
	"apt", "apt-get", "apt-cache", "dpkg", "yum", "dnf", "rpm", "brew", "pip", "npm", "gem", "cargo", "go", "composer",
	"apk", "zypper", "pacman", "snap", "flatpak", "conda", "port",

	// Development - EXPANDED
	"git", "svn", "make", "gcc", "g++", "clang", "gdb", "valgrind", "strace", "ltrace",
	"docker", "kubectl", "terraform", "ansible", "puppet", "chef", "node", "python", "python3", "ruby", "perl", "php",
	"java", "javac", "mvn", "gradle", "cmake", "autoconf", "automake", "libtool", "pkg-config",

	// Archives - EXPANDED
	"tar", "gzip", "gunzip", "bzip2", "bunzip2", "zip", "unzip", "7z", "rar", "unrar", "xz", "unxz", "zcat", "bzcat",
	"xzcat", "ar", "cpio", "dump", "restore",

	// User management - EXPANDED
	"who", "w", "whoami", "id", "groups", "passwd", "su", "sudo", "useradd", "userdel", "usermod",
	"groupadd", "groupdel", "groupmod", "chage", "chsh", "chfn", "newusers", "pwck", "grpck", "lastlog", "faillog",

	// Process and system - EXPANDED
	"shutdown", "reboot", "halt", "poweroff", "date", "time", "cal", "bc", "echo", "printf",
	"test", "expr", "sleep", "wait", "timeout", "watch", "crontab", "at", "batch", "nice", "renice", "nohup",
	"setsid", "screen", "tmux", "script", "logger", "wall", "write", "mesg",

	// Shell builtins and core utilities - EXPANDED
	"alias", "unalias", "export", "unset", "source", "history", "type", "help", "man", "info", "whatis", "apropos",
	"clear", "reset", "tput", "stty", "set", "shopt", "ulimit", "umask", "fc", "bind", "complete", "compgen",
	"dirs", "pushd", "popd", "wait", "times", "disown", "suspend",

	// File compression and encryption - NEW CATEGORY
	"gpg", "openssl", "md5sum", "sha1sum", "sha256sum", "sha512sum", "base64", "base32", "uuencode", "uudecode",

	// System info and hardware - NEW CATEGORY
	"lscpu", "lsblk", "lsusb", "lspci", "lsmod", "modinfo", "modprobe", "dmidecode", "hdparm", "smartctl", "fdisk",
	"parted", "mkfs", "fsck", "mount", "umount", "blkid", "swapon", "swapoff",

	// Text editors and viewers - NEW CATEGORY
	"vi", "vim", "nano", "emacs", "ed", "ex", "view", "vimdiff", "sdiff", "colordiff",

	// Terminal and session management - NEW CATEGORY
	"tty", "pts", "script", "screen", "tmux", "byobu", "expect", "dialog", "whiptail",
}

// Add this filter function
func (mi *MANIndexer) isUsefulCommand(command string) bool {
	// Skip very short commands
	if len(command) < 2 {
		return false
	}

	// Skip commands with unusual characters
	for _, char := range command {
		if !((char >= 'a' && char <= 'z') ||
			(char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') ||
			char == '-' || char == '_') {
			return false
		}
	}

	// Check if it's in our common commands list
	for _, common := range commonCommands {
		if strings.EqualFold(command, common) {
			return true
		}
	}

	// Also include commands that look useful (heuristics)
	usefulPatterns := []string{
		"git-", "docker-", "kubectl-", "aws-", "gcloud-",
		"systemctl", "journalctl", "logrotate", "crontab",
	}

	for _, pattern := range usefulPatterns {
		if strings.Contains(command, pattern) {
			return true
		}
	}

	return false
}

// GetAllIndexedPages returns all indexed MAN pages
func (mi *MANIndexer) GetAllIndexedPages() []MANPage {
	mi.mu.RLock()
	defer mi.mu.RUnlock()

	var pages []MANPage
	for _, page := range mi.indexed {
		pages = append(pages, page)
	}
	return pages
}
